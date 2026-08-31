// Package static is graphi's PRODUCTION static embedder (SW-262): a pure-Go,
// CGo-free re-implementation of Model2Vec static-embedding inference for the
// pinned `minishlab/potion-code-16M-v2` artifact (model potion-code-16M-v2,
// revision e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b, SHA-256-pinned in
// pins.go). It is registered as scheme `static` from `init`, mirroring
// engine/embed/ollama.
//
// What is implemented is exactly what the pinned tokenizer.json declares and
// nothing more: BertNormalizer (clean_text, handle_chinese_chars,
// strip_accents, lowercase), BertPreTokenizer, WordPiece, the two added
// tokens [PAD]/[UNK], right truncation at 512 tokens, and no post-processor.
// The safetensors reader handles one F16 tensor; the float16 codec is
// written here (f16.go). The loader validates the safetensors header BEFORE
// allocating the embedding table (AC-7), with each violation surfacing a
// typed error covered by a corrupted-fixture test.
//
// Determinism: this package adopts EmbedEach (batch-invariant) semantics —
// a node's vector does NOT depend on which other texts share its embedding
// chunk — and pins that choice (plus the float16 rounding points and the
// fixed pairwise summation tree) in Embedder.ID(). See embed.go's
// `pipeline` block for the rounding points and the pairwiseSum* helpers for
// the summation tree.
//
// CGo-free: only stdlib imports in the embedder runtime. The download path
// (download.go) imports net/http and net/url — that is its purpose, and
// `graphi setup-embedder` is the ONLY entry point that initiates a download
// (AC-5). AC-8 (AssertNoCgoEmbedder and the egress canary) and AC-5 (the
// embedder runtime is offline by construction; no setup-embedder call is
// reachable from index/search/MCP/HTTP) hold this contract end-to-end.
package static

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/samibel/graphi/engine/embed"
)

// Scheme is the selector scheme this package handles, e.g.
// "static:potion-code-16M-v2@<revision>".
const Scheme = "static"

// Artifact file names of a Model2Vec directory layout. Loader requires every
// one of them to be present (the spike classified partial absence as a
// failure, not a skip; that contract carries forward to the production
// loader — see AC-7 / TestStatic_HashMismatch_IsTypedError).
const (
	FileConfig      = "config.json"
	FileTokenizer   = "tokenizer.json"
	FileSafetensors = "model.safetensors"
	FileModules     = "modules.json"
)

// tensorName is the embedding table's key in model.safetensors for the
// Model2Vec layout. The sentence-transformers layout uses another name and
// a subfolder; not implemented.
const tensorName = "embeddings"

// DefaultMaxLength is model2vec's StaticModel.encode(max_length=512): the
// token cap applied after unk removal, and (× median token length) the
// character cap applied before tokenisation.
const DefaultMaxLength = 512

// ModelID is the user-facing model name (without the "minishlab/" prefix
// the HuggingFace id carries).
const ModelID = "potion-code-16M-v2"

// poolingMode is the single pooling mode the production embedder implements.
// The ID format encodes it (AC-2); changing it changes every cached
// generation, so an embedder with a different mode would read as stale on
// reload.
const poolingMode = "mean"

// configHash / tokenizerHash mirror the pin table in pins.go. They enter the
// Embedder.ID() format, so a divergence between this file and pins.go is a
// build-time test failure (TestStatic_PinTableAgreesWithPins).
const (
	configHash    = "148e5691a6fcc553437156859701fba017a1ba5d340b170f17e0f3668fb861a7"
	tokenizerHash = "107bbdcbad4bff1d299b7a4c3a2fb17c52890688b7dd0e4c9deab79d3c4f3d45"
)

// modelConfig is config.json: Model2Vec reads only "normalize" (default false).
type modelConfig struct {
	Normalize      *bool  `json:"normalize"`
	EmbeddingDtype string `json:"embedding_dtype"`
}

// Embedder is the production static-potion embedder. It is constructed lazily:
// New takes a selector argument, parses it, and validates the model+revision;
// the artifact itself is NOT read until Embed is called for the first time. A
// construction never dials anything, and never reads the artifact directory.
// The lazy-load shape is what makes "first install, warm start, offline with
// cache, offline without cache" share one constructor (AC-9): a warm cache
// is fast (LoadModel in <100ms per the SW-259 record), a cold cache surfaces
// a typed error naming the exact repair command.
type Embedder struct {
	model    string
	revision string
	dim      int    // discovered on first embed; 0 before that
	modelSHA string // pinned config hash, the [:12] of which enters ID()

	mu      sync.Mutex
	loaded  bool
	loadedM *Model
	loadErr error
}

// New constructs a static embedder for `model@revision`. The selector format
// is `static:<model>@<revision>`; the part after `static:` is passed here. An
// empty arg, an arg without `@revision`, or an arg whose model is not the
// pinned one is rejected with a typed error naming the accepted form. The
// function performs no IO (AC-5: the embedder never initiates a download).
func New(arg string) (*Embedder, error) {
	return NewWithPinnedModel(arg, ModelID, defaultRevision())
}

// NewWithPinnedModel is New with the pinned (model, revision) pair supplied.
// It is the seam tests use to assert the unknown-model / missing-revision
// typed errors without depending on the package constants.
func NewWithPinnedModel(arg, pinnedModel, pinnedRevision string) (*Embedder, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return nil, fmt.Errorf("static: selector is empty; the accepted form is static:<model>@<revision>")
	}
	model, rev, ok := splitStaticSelector(arg)
	if !ok {
		return nil, fmt.Errorf("static: selector %q is not in the accepted form static:<model>@<revision> (e.g. static:%s@%s)", arg, pinnedModel, pinnedRevision)
	}
	if rev == "" {
		return nil, fmt.Errorf("static: selector %q is missing @<revision>; the accepted form is static:<model>@<revision> (e.g. static:%s@%s)", arg, pinnedModel, pinnedRevision)
	}
	if model != pinnedModel {
		return nil, fmt.Errorf("static: unknown model %q; the accepted form is static:%s@%s", model, pinnedModel, pinnedRevision)
	}
	if rev != pinnedRevision {
		return nil, fmt.Errorf("static: unsupported revision %q; the pinned revision is %s", rev, pinnedRevision)
	}
	return &Embedder{
		model:    pinnedModel,
		revision: pinnedRevision,
		modelSHA: configHash,
	}, nil
}

// defaultRevision is the only revision we pin. Surfaced as a function so the
// constant is testable.
func defaultRevision() string { return pinnedRevision }

var pinnedRevision = "e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b"

// splitStaticSelector splits "model@revision" into its components. An arg
// without "@" yields (_, "", false) — the caller treats it as a missing
// revision.
func splitStaticSelector(arg string) (model, rev string, ok bool) {
	i := strings.IndexByte(arg, '@')
	if i < 0 {
		return arg, "", true // no '@' is a missing revision
	}
	return strings.TrimSpace(arg[:i]), strings.TrimSpace(arg[i+1:]), true
}

// ID implements embed.Embedder. The format is
//
//	static:<model>@<revision>:<config_sha256[:12]>:<pooling>:<normalize>
//
// Any change to the model, revision, configuration hash, pooling mode or
// normalisation flag changes the ID, which feeds the SW-261 fingerprint and
// the GenerationStore's typed state. The literal ":" between segments is
// the canonical separator; the literal "@" between model and revision
// matches the user-facing selector shape.
func (e *Embedder) ID() string {
	normalize := "true"
	// When the artifact is loaded, the on-disk normalize flag enters the ID
	// too — the contract says any inference-configuration change must change
	// the ID. Until the first load the on-disk value is unknown, so the ID
	// reads normalize=true (the pinned config.json's value) for the
	// fingerprint's first build; a subsequent embed that reveals a different
	// value would produce a different ID and the GenerationStore would
	// fingerprint the new one (the AC-2 contract).
	if e.loadedM != nil && !e.loadedM.normalize {
		normalize = "false"
	}
	return Scheme + ":" + e.model + "@" + e.revision + ":" + e.modelSHA[:12] + ":" + poolingMode + ":" + normalize
}

// Dim implements embed.Embedder. The dimension is read from the artifact and
// never hard-coded (AC-2). 0 means "no embed has run yet"; the runtime's
// pre-fingerprint DimDiscoverer probe forces one.
func (e *Embedder) Dim() int {
	if e.loadedM != nil {
		return e.loadedM.dim
	}
	return e.dim
}

// ProbeDim implements embed.DimDiscoverer. It loads the artifact (if absent:
// typed error naming the repair command) and caches the discovered dim so
// the fingerprint can be built before any real embedding pass.
func (e *Embedder) ProbeDim(ctx context.Context) error {
	m, err := e.load(ctx)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dim == 0 {
		e.dim = m.dim
	}
	return nil
}

// load fetches and validates the artifact, caching the result on success and
// any error on failure. The artifact directory is resolved by
// ResolveArtifactDir, which honours GRAPHI_STATIC_MODEL_DIR and the
// XDG_CACHE_HOME convention. A missing artifact surfaces the exact repair
// command; a misconfigured artifact (corrupted, wrong size, wrong dtype,
// truncated, partial) surfaces a typed error naming the field.
func (e *Embedder) load(ctx context.Context) (*Model, error) {
	e.mu.Lock()
	if e.loaded {
		e.mu.Unlock()
		return e.loadedM, e.loadErr
	}
	dir := ResolveArtifactDir(e.model + "@" + e.revision)
	if dir == "" {
		// Cannot resolve the cache directory at all: the typed error names
		// the repair, never a silent default (AC-5).
		err := loadAbsentError(e.model + "@" + e.revision)
		e.loadErr = err
		e.loaded = true
		e.mu.Unlock()
		return nil, err
	}
	m, err := LoadModel(dir)
	if err != nil {
		// Wrap any load failure in the typed unavailable error so the
		// search service's typed-unavailable rendering carries the exact
		// repair command (AC-5). The underlying cause is preserved
		// through %w so a developer can still see why the artifact is
		// unavailable (corrupt hash, missing file, symlink, etc.).
		err = wrapUnavailable(e.model+"@"+e.revision, err)
	}
	e.loadErr = err
	if err == nil {
		e.loadedM = m
		e.dim = m.dim
	}
	e.loaded = true
	e.mu.Unlock()
	return m, err
}

// Embed implements embed.Embedder. The first call loads the artifact; every
// call uses EmbedEach semantics (batch-invariant — no BatchLongest pooling).
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	m, err := e.load(ctx)
	if err != nil {
		return nil, err
	}
	return m.Embed(ctx, texts)
}

// loadAbsentError is the typed error load surfaces when the artifact is
// absent. It is the single source of truth for "no artifact → run
// setup-embedder" — engine/search.SemanticResponse renders it as the typed
// unavailable response, and `graphi index --semantic` renders it as the
// non-zero exit (AC-5).
func loadAbsentError(selector string) error {
	return &UnavailableError{
		Reason:       "no embedder artifact cached",
		Selector:     selector,
		RepairCmd:    "graphi setup-embedder static:" + ModelID + "@" + defaultRevision(),
		ModelPathEnv: envModelDir,
	}
}

// wrapUnavailable wraps an underlying loader error with the typed
// unavailable envelope, so the search service's typed-unavailable
// rendering carries the exact repair command while the underlying cause
// remains visible via Unwrap (AC-5).
func wrapUnavailable(selector string, cause error) error {
	return &wrappedUnavailable{
		Unavailable: loadAbsentError(selector).(*UnavailableError),
		Cause:       cause,
	}
}

// wrappedUnavailable is UnavailableError plus a wrapped cause. The
// search service's typed-unavailable renderer can unwrap to read the
// underlying cause; the operator-facing message names the repair command.
type wrappedUnavailable struct {
	Unavailable *UnavailableError
	Cause       error
}

func (w *wrappedUnavailable) Error() string {
	if w.Cause != nil {
		return w.Unavailable.Error() + ": " + w.Cause.Error()
	}
	return w.Unavailable.Error()
}

func (w *wrappedUnavailable) Unwrap() error { return w.Cause }

// Repair returns the repair command (exposed as a method so callers can
// extract it without parsing the error string).
func (w *wrappedUnavailable) Repair() string { return w.Unavailable.RepairCmd }

// envModelDir is the artifact location override. The default is the same
// $XDG_CACHE_HOME/graphi/models/<model>@<revision>/ path that PINNED.md and
// the setup-embedder command both write to. Surfaced as a const so the
// static_test.go reference and the production code cannot drift.
const envModelDir = "GRAPHI_STATIC_MODEL_DIR"

// UnavailableError is the typed error every "no artifact" path returns. Its
// `RepairCmd` is the exact `graphi setup-embedder static:...` command the
// user runs (AC-5); its `Reason` is what the search service renders into the
// typed unavailable response.
type UnavailableError struct {
	Reason       string
	Selector     string
	RepairCmd    string
	ModelPathEnv string
}

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("%s (model=%s; run `%s` to download, or set %s to a local artifact directory)",
		e.Reason, e.Selector, e.RepairCmd, e.ModelPathEnv)
}

// Repair returns the repair command a user can copy-paste.
func (e *UnavailableError) Repair() string { return e.RepairCmd }

// ResolveArtifactDir is the canonical lookup for the artifact directory. It
// honours $GRAPHI_STATIC_MODEL_DIR first, then $XDG_CACHE_HOME/graphi/models/
// (default), overridable. The default matches `graphi setup-embedder`'s
// install target so the two paths agree.
func ResolveArtifactDir(modelAtRev string) string {
	if d := os.Getenv(envModelDir); d != "" {
		return d
	}
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		if home, _ := os.UserHomeDir(); home != "" {
			cache = filepath.Join(home, ".cache")
		}
	}
	if cache == "" {
		return ""
	}
	return filepath.Join(cache, "graphi", "models", modelAtRev)
}

// LoadModel reads config.json, tokenizer.json and model.safetensors from dir
// and returns the loaded model. Every step of the loader fails closed: a
// missing file, a corrupted safetensors header, a wrong dtype, a shape that
// does not match the vocabulary, or an oversized artifact all surface a
// typed error naming the field. Validation runs BEFORE the embedding table
// is allocated (AC-7).
//
// maxArtifactBytes is enforced as a sum of file sizes BEFORE the safetensors
// is opened — a single oversized file cannot allocate.
const maxArtifactBytes int64 = 34 << 20 // 34 MiB; the four pinned files sum to ~32 MiB

// LoadModel reads the artifact directory into a Model. It is the production
// loader; the spike's LoadModel is a strict subset.
func LoadModel(dir string) (*Model, error) {
	if dir == "" {
		return nil, errors.New("static: artifact directory is empty")
	}
	// Validate every required file is present, regular, and not a symlink.
	// This is the AC-7 pre-allocation gate: we never open a file whose
	// declared size would exceed maxArtifactBytes, and we never follow a
	// symlink.
	var totalSize int64
	for _, name := range []string{FileConfig, FileTokenizer, FileSafetensors, FileModules} {
		path := filepath.Join(dir, name)
		st, lerr := os.Lstat(path)
		if lerr != nil {
			return nil, fmt.Errorf("static: artifact %s: %w", dir, lerr)
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("static: artifact %s: %s is a symlink; pinned files must be regular", dir, path)
		}
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("static: artifact %s: %s is not a regular file", dir, path)
		}
		totalSize += st.Size()
	}
	if totalSize > maxArtifactBytes {
		return nil, fmt.Errorf("static: artifact %s: total size %d exceeds pinned limit %d (AC-7)", dir, totalSize, maxArtifactBytes)
	}

	cfgRaw, err := os.ReadFile(filepath.Join(dir, FileConfig))
	if err != nil {
		return nil, err
	}
	var cfg modelConfig
	if err := json.Unmarshal(cfgRaw, &cfg); err != nil {
		return nil, fmt.Errorf("static: artifact %s: config.json: %w", dir, err)
	}
	tok, err := LoadTokenizer(filepath.Join(dir, FileTokenizer))
	if err != nil {
		return nil, err
	}
	rows, dim, table, err := loadF16Matrix(filepath.Join(dir, FileSafetensors), tensorName)
	if err != nil {
		return nil, err
	}
	if rows != tok.VocabSize() {
		return nil, fmt.Errorf("static: artifact %s: %d embedding rows for %d vocabulary tokens", dir, rows, tok.VocabSize())
	}
	m := &Model{
		Dir:       dir,
		tok:       tok,
		rows:      rows,
		dim:       dim,
		table:     table,
		maxLength: DefaultMaxLength,
	}
	if cfg.Normalize != nil {
		m.normalize = *cfg.Normalize
	}
	lengths := make([]int, rows)
	for id := range lengths {
		lengths[id] = utf8.RuneCountInString(tok.Token(id))
	}
	sort.Ints(lengths)
	if rows%2 == 1 {
		m.medianTokenLength = lengths[rows/2]
	} else {
		m.medianTokenLength = int(float64(lengths[rows/2-1]+lengths[rows/2]) / 2)
	}
	m.ArtifactBytes = totalSize
	return m, nil
}

// Compile-time interface assertions.
var (
	_ embed.Embedder      = (*Embedder)(nil)
	_ embed.DimDiscoverer = (*Embedder)(nil)
)

// init registers the `static` scheme with engine/embed's constructor table
// so an explicit GRAPHI_EMBEDDER=static:<model>@<revision> selector names it.
// Mirroring engine/embed/ollama: registration from init, nothing constructed
// or dialed until the selector names it (AC-1). The default build keeps
// semantic search OFF (RegisterDefaults registers nothing); the constructor
// table is the opt-in seam.
func init() {
	embed.RegisterScheme(Scheme, func(arg string) (embed.Embedder, error) {
		return New(arg)
	})
}
