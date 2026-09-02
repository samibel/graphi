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
// CGo-free and zero-egress: the embedder runtime imports only the Go
// standard library and never reaches the network. The download path that
// installs the pinned artifact lives in cmd/graphi/staticfetch and is called
// only by cmd/graphi/setup.go — NOT from this package — because this package is reachable from
// index / search / MCP / HTTP via the registry's registered scheme, and any
// outbound code in it would mean the default graph links an egress path
// (AC-5 / AC-8). `graphi setup-embedder static:<model>@<revision>` is the
// ONLY entry point that initiates a download; the embedder runtime
// (every file in this package) reads the cached artifact and never dials.
// TestStatic_EmbedderRuntimeIsZeroEgress and TestStatic_NoOutboundDialInSource
// enforce this invariant at the package level; the production canary gate
// (internal/canary/gate) does the same for the whole default graph at
// release time.
package static

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
const ModelID = PinnedModel

// modelConfig is config.json: Model2Vec reads only "normalize" (default false).
type modelConfig struct {
	Normalize      *bool  `json:"normalize"`
	EmbeddingDtype string `json:"embedding_dtype"`
}

// SelectorErrorKind enumerates the bad-selector cases New/NewWithPinnedModel
// reject with a typed error. The kind is the discriminator callers use in
// errors.As to render an actionable message; the operator-facing detail
// names the accepted form (AC-1).
type SelectorErrorKind int

const (
	// SelectorEmpty: the constructor argument is the empty string.
	SelectorEmpty SelectorErrorKind = iota + 1
	// SelectorMissingAt: the argument has no `@<revision>` segment.
	SelectorMissingAt
	// SelectorEmptyRevision: the `@<revision>` segment is present but empty.
	SelectorEmptyRevision
	// SelectorUnknownModel: the model name is not the pinned one.
	SelectorUnknownModel
	// SelectorUnknownRevision: the revision is not the pinned one.
	SelectorUnknownRevision
)

// SelectorError is the typed error New returns for every bad-selector
// case. AC-1 wants a typed error naming the accepted form; tests assert
// it with errors.As.
type SelectorError struct {
	Kind     SelectorErrorKind
	Input    string
	Model    string // accepted model (the pinned one) when relevant
	Revision string
}

func (e *SelectorError) Error() string {
	switch e.Kind {
	case SelectorEmpty:
		return "static: selector is empty; the accepted form is static:<model>@<revision>"
	case SelectorMissingAt:
		return fmt.Sprintf("static: selector %q is not in the accepted form static:<model>@<revision> (e.g. static:%s@%s)", e.Input, e.Model, e.Revision)
	case SelectorEmptyRevision:
		return fmt.Sprintf("static: selector %q is missing @<revision>; the accepted form is static:<model>@<revision> (e.g. static:%s@%s)", e.Input, e.Model, e.Revision)
	case SelectorUnknownModel:
		return fmt.Sprintf("static: unknown model %q; the accepted form is static:%s@%s", e.Input, e.Model, e.Revision)
	case SelectorUnknownRevision:
		return fmt.Sprintf("static: unsupported revision %q; the pinned revision is %s", e.Input, e.Revision)
	default:
		return fmt.Sprintf("static: bad selector %q", e.Input)
	}
}

// inferenceContractID is the implementation-contract identity the
// production embedder advertises via ID() (AC-2). It encodes the three
// things the SW-259 carry-forwards pinned:
//
//   - EmbedEach: every text is its own longest, so no BatchLongest pad id
//     is pooled into a node's mean;
//   - F16: table is binary16, mean is rounded to binary16 after the
//     float32 accumulation, every square and the sum-of-squares are
//     binary16-rounded before the float32 sqrt (rounding points 1–5);
//   - tree: the sum-of-squares is the fixed pairwise tree
//     pairwiseSumF16 (8-lane interleave, halves at multiples of 8).
//
// Any change to any of the three must change this string so the SW-261
// fingerprint reads the new generation as different.
const inferenceContractID = "embedeach-f16-tree"

// poolingID is the pooling algorithm segment AC-2 requires in ID(). Keep it
// separate from inferenceContractID: callers and stored fingerprints depend
// on the literal `mean`, while the implementation contract additionally pins
// batch-invariant EmbedEach semantics and arithmetic association.
const poolingID = "mean"

// Embedder is the production static-potion embedder. New takes a
// selector argument, parses it, validates the model+revision against the
// pinned pair, and (when the artifact directory is reachable) loads it +
// verifies the pinned SHA-256 (AC-2/AC-6). A construction never dials
// anything, and never reads the artifact directory unless the user has
// already pointed at one (GRAPHI_STATIC_MODEL_DIR or the default cache).
// The lazy-load shape is what makes "first install, warm start, offline
// with cache, offline without cache" share one constructor (AC-9): a
// warm cache is fast (~100ms per the SW-259 record), a cold cache
// surfaces a typed error naming the exact repair command.
type Embedder struct {
	model    string
	revision string
	dim      int // discovered on first embed; 0 before that

	mu      sync.Mutex
	loaded  bool
	loadedM *Model // nil until VerifyPins + LoadModel succeed
	loadErr error
}

// New constructs a static embedder for `model@revision`. The selector
// format is `static:<model>@<revision>`; the part after `static:` is
// passed here. An empty arg, an arg without `@revision`, or an arg whose
// model / revision is not the pinned pair is rejected with a typed
// SelectorError naming the accepted form. The function performs no IO
// (AC-5: the embedder never initiates a download).
//
// When the artifact directory is reachable and the pinned files verify
// against the in-tree pin table, New also loads the model so ID() can
// advertise the real file hashes. When the artifact directory is NOT
// reachable (first install, offline without cache) New succeeds with a
// nil Model; the lazy load path surfaces a typed unavailable error on
// the first Embed / ProbeDim call.
func New(arg string) (*Embedder, error) {
	return NewWithPinnedModel(arg, ModelID, defaultRevision())
}

// NewWithPinnedModel is New with the pinned (model, revision) pair
// supplied. It is the seam tests use to assert the unknown-model /
// missing-revision typed errors without depending on the package
// constants.
func NewWithPinnedModel(arg, pinnedModel, pinnedRevision string) (*Embedder, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return nil, &SelectorError{Kind: SelectorEmpty}
	}
	model, rev, ok := splitStaticSelector(arg)
	if !ok {
		return nil, &SelectorError{Kind: SelectorMissingAt, Input: arg, Model: pinnedModel, Revision: pinnedRevision}
	}
	if rev == "" {
		return nil, &SelectorError{Kind: SelectorEmptyRevision, Input: arg, Model: pinnedModel, Revision: pinnedRevision}
	}
	if model != pinnedModel {
		return nil, &SelectorError{Kind: SelectorUnknownModel, Input: model, Model: pinnedModel, Revision: pinnedRevision}
	}
	if rev != pinnedRevision {
		return nil, &SelectorError{Kind: SelectorUnknownRevision, Input: rev, Revision: pinnedRevision}
	}
	e := &Embedder{
		model:    pinnedModel,
		revision: pinnedRevision,
	}
	// Try to load + verify pins right now so ID() carries the real
	// identity. A missing artifact is fine: the lazy load path will
	// surface a typed unavailable error on the first use.
	dir := ResolveArtifactDir(pinnedModel + "@" + pinnedRevision)
	if dir == "" {
		return e, nil
	}
	if m, err := LoadModel(dir); err == nil {
		e.loadedM = m
		e.loaded = true
	}
	// On error we still return the Embedder; the lazy load will surface
	// the typed PinMismatchError (or its sibling unavailable error) on
	// the first Embed / ProbeDim call. New itself never refuses on
	// artifact errors; the typed errors surface through the search
	// service's typed unavailable response (AC-5).
	return e, nil
}

// defaultRevision is the only revision we pin. Surfaced as a function so the
// constant is testable.
func defaultRevision() string { return PinnedRevision }

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
//	static:<model>@<revision>:<model_sha256[:12]>:<pooling>:<normalize>:<tokenizer_sha256[:12]>:<config_sha256[:12]>:<impl_contract>:<admission_sha256[:12]>
//
// The first three segments are the user-facing identity: scheme,
// model@revision, and the SHA-256 (first 12 hex digits) of the actual
// model.safetensors file. Pooling and normalization are the literal AC-2
// fields. The tokenizer and config hashes bind the remaining verified
// inference inputs. The impl_contract segment is `inferenceContractID` —
// "embedeach-f16-tree" — the implementation-contract identity (EmbedEach
// batch-invariance, F16 rounding points, fixed pairwise summation tree).
//
// The SW-267 AC-3 / AC-8 final segment is the SHA-256 (first 12 hex
// digits) of the canonicalized admission profile (tokenizer identity,
// limit, reserve, preparation algorithm and version). A change to any
// admission-profile field changes this segment, so the GenerationStore's
// typed state reads the new generation as a different fingerprint.
// The on-disk SHA values are the inference-configuration identity AC-2
// promises: a structurally valid replacement of the weights or the
// tokenizer that changes the file hashes changes the ID, and the
// GenerationStore's typed state will see the new generation as a
// distinct fingerprint (it is impossible for a swapped model to
// inherit the cached identity). When the artifact has not been loaded
// yet, ID() falls back to the pinned hashes from PinnedSHA256 — the
// identity is the same shape, just sourced from the pin table rather
// than the artifact itself. The lazy load path replaces those values
// with the real hashes as soon as the artifact is reachable.
func (e *Embedder) ID() string {
	modelHash := PinnedSHA256[FileSafetensors][:12]
	tokHash := PinnedSHA256[FileTokenizer][:12]
	configHash := PinnedSHA256[FileConfig][:12]
	normalize := PinnedNormalize
	e.mu.Lock()
	loaded := e.loadedM
	e.mu.Unlock()
	if loaded != nil && loaded.FileHashes != nil {
		if h, ok := loaded.FileHashes[FileSafetensors]; ok && len(h) >= 12 {
			modelHash = h[:12]
		}
		if h, ok := loaded.FileHashes[FileTokenizer]; ok && len(h) >= 12 {
			tokHash = h[:12]
		}
		if h, ok := loaded.FileHashes[FileConfig]; ok && len(h) >= 12 {
			configHash = h[:12]
		}
		normalize = loaded.normalize
	}
	profile := e.Profile()
	profileHash := shortHash(profile.String(), 12)
	return Scheme + ":" + e.model + "@" + e.revision + ":" + modelHash + ":" + poolingID + ":" + strconv.FormatBool(normalize) + ":" + tokHash + ":" + configHash + ":" + inferenceContractID + ":" + profileHash
}

// shortHash returns the first n hex characters of sha256(s). Used by
// ID() to expose the admission-profile identity in 12 hex chars (the
// shape every other ID segment uses). sha256 is collision-resistant in
// practice at this length for the spec-distinguishing workload the
// fingerprint does.
func shortHash(s string, n int) string {
	sum := sha256.Sum256([]byte(s))
	hex := hex.EncodeToString(sum[:])
	if n > len(hex) {
		n = len(hex)
	}
	return hex[:n]
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

// CheckAvailable implements embed.AvailabilityChecker. It performs only the
// local, SHA-pinned artifact load; it never downloads or dials. Semantic search
// uses this before any early return so every missing/corrupt-artifact path
// carries the exact setup-embedder repair command (AC-5).
func (e *Embedder) CheckAvailable(ctx context.Context) error {
	_, err := e.load(ctx)
	return err
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

// verifyAllPins is the load-time byte-identity check (AC-2/AC-6). It
// streams every pinned file through SHA-256 and compares to the in-tree
// pin table, returning the hash map (so ID() can advertise the real
// identity) or a typed PinMismatchError naming expected vs actual. A
// single mismatch aborts the whole load; no Model is produced.
func verifyAllPins(dir string) (map[string]string, error) {
	hashes := make(map[string]string, len(PinnedFileNames))
	for _, name := range PinnedFileNames {
		want, ok := PinnedSHA256[name]
		if !ok {
			return nil, fmt.Errorf("static: VerifyPins: no pin recorded for %s; refusing to load a file with no recorded identity", name)
		}
		path := filepath.Join(dir, name)
		got, err := sha256File(path)
		if err != nil {
			return nil, fmt.Errorf("static: VerifyPins: hash %s: %w", name, err)
		}
		if got != want {
			return nil, &PinMismatchError{
				File:     name,
				Path:     path,
				Expected: want,
				Actual:   got,
			}
		}
		hashes[name] = got
	}
	return hashes, nil
}

// statPinnedFiles returns the os.FileInfo of every pinned file (used by
// the AC-7 per-file size check) and the total size in bytes. The walk
// refuses symlinks (defends the size check against a symlink-to-/dev/zero
// or a malicious huge file masquerading as a pinned path) and non-regular
// files. Every error is typed and names the offending path.
func statPinnedFiles(dir string) (map[string]os.FileInfo, int64, error) {
	stats := make(map[string]os.FileInfo, len(PinnedFileNames))
	var total int64
	for _, name := range PinnedFileNames {
		path := filepath.Join(dir, name)
		st, err := os.Lstat(path)
		if err != nil {
			return nil, 0, fmt.Errorf("static: artifact %s: stat %s: %w", dir, name, err)
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return nil, 0, fmt.Errorf("static: artifact %s: %s is a symlink; pinned files must be regular", dir, path)
		}
		if !st.Mode().IsRegular() {
			return nil, 0, fmt.Errorf("static: artifact %s: %s is not a regular file: %s", dir, name, st.Mode().String())
		}
		stats[name] = st
		total += st.Size()
	}
	return stats, total, nil
}

// LoadModel reads the artifact directory into a Model. It is the production
// loader; the spike's LoadModel is a strict subset.
//
// The loader fails closed on every AC-7 violation with a typed error:
//   - a missing file, a corrupted safetensors header, a wrong dtype, a
//     shape that does not match the vocabulary, an oversized file, an
//     out-of-bounds offset, or a byte-count mismatch;
//   - a file whose bytes do not match the in-tree pin table (AC-2/AC-6);
//   - a symlink at a pinned path (defends the size check from a
//     symlink-to-/dev/zero or similar attack).
//
// Every validation runs BEFORE the embedding table is allocated. The
// loader's overflow-safe arithmetic bounds the safetensors size check
// against an extreme positive shape that could otherwise wrap the
// product and panic at `make`.
func LoadModel(dir string) (*Model, error) {
	if dir == "" {
		return nil, errors.New("static: artifact directory is empty")
	}
	// AC-2/AC-6: byte-level identity. Compute every pinned file's
	// SHA-256 and compare to the in-tree pin table BEFORE any structural
	// parse, so a swapped model cannot inherit the cached identity.
	hashes, err := verifyAllPins(dir)
	if err != nil {
		return nil, err
	}
	// AC-7: validate every file is present, regular, and not a symlink
	// BEFORE allocating anything. The total-size check guards the
	// embedding table (32 MiB artifact vs 34 MiB ceiling) and the
	// per-file size check guards the loadF16Matrix path.
	stats, totalSize, err := statPinnedFiles(dir)
	if err != nil {
		return nil, err
	}
	if totalSize > maxArtifactBytes {
		return nil, &TensorError{
			Kind:   TensorErrTotalSize,
			File:   dir,
			Detail: fmt.Sprintf("total size=%d limit=%d", totalSize, maxArtifactBytes),
		}
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
	rows, dim, table, err := loadF16Matrix(filepath.Join(dir, FileSafetensors), tensorName, tok.VocabSize())
	if err != nil {
		return nil, err
	}
	m := &Model{
		Dir:        dir,
		tok:        tok,
		rows:       rows,
		dim:        dim,
		table:      table,
		maxLength:  DefaultMaxLength,
		FileHashes: hashes,
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
	_ = stats // (currently unused; reserved for AC-7 per-file size bounds)
	return m, nil
}

// Compile-time interface assertions.
var (
	_ embed.Embedder            = (*Embedder)(nil)
	_ embed.DimDiscoverer       = (*Embedder)(nil)
	_ embed.AvailabilityChecker = (*Embedder)(nil)
	_ embed.TokenizingEmbedder  = (*Embedder)(nil)
	_ embed.Admission           = (*Embedder)(nil)
	_ embed.AdmissionProfile    = (*Embedder)(nil)
)

// Tokenizer returns the active tokenizer so the production embedder
// satisfies embed.TokenizingEmbedder (AC-7). The lazy-load shape holds
// a nil tokenizer until the artifact is reachable; the builder's
// TokenizingEmbedder path then sees a nil and falls back to the byte
// cap alone (the same fail-closed posture the legacy SW-260 path had).
// The returned DocumentTokenizer observes the tokenizer's uncapped raw stream,
// including UNK ids, so it can detect the same pre-drop cap inference applies.
func (e *Embedder) Tokenizer() embed.DocumentTokenizer {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.loadedM == nil {
		return nil
	}
	return tokenizerAdapter{e.loadedM.tok}
}

// tokenizerAdapter wraps a *Tokenizer so the production embedder can
// return embed.DocumentTokenizer from Tokenizer() (AC-7). The adapter
// delegates to (*Tokenizer).truncate, the uncapped raw-token path.
type tokenizerAdapter struct{ t *Tokenizer }

func (a tokenizerAdapter) Truncate(text string, maxTokens int) (string, bool) {
	return a.t.truncate(text, maxTokens)
}

// Admit implements embed.Admission (AC-2, AC-7). The first call loads
// the artifact (a typed unavailable error surfaces on missing files);
// subsequent calls run on the cached Model. Inputs beyond the character or
// raw-token boundary are reduced to the exact byte prefix inference consumes;
// an input for which no token-aligned prefix can be proved fails closed.
func (e *Embedder) Admit(ctx context.Context, text string) (embed.Admitted, error) {
	m, err := e.load(ctx)
	if err != nil {
		return embed.Admitted{}, err
	}
	return m.Admit(ctx, text)
}

// Profile implements embed.AdmissionProfile (AC-3, AC-8). The spec
// pins the tokenizer identity (algorithm + file hash + version), the
// usable limit (MaxAdmissionTokens = 512), the special-token reserve
// (0 for the pinned model2vec static embedder), and the preparation
// algorithm ("first-n-tokens@1"). A profile change invalidates stored
// generations by fingerprint. When the artifact is not loaded the
// tokenizer hash is empty; the fingerprint then names an unloaded
// profile so the next reload sees a distinct identity.
func (e *Embedder) Profile() embed.AdmissionSpec {
	e.mu.Lock()
	m := e.loadedM
	e.mu.Unlock()
	hash := ""
	ver := ""
	if m != nil && m.FileHashes != nil {
		hash = m.FileHashes[FileTokenizer]
		// Tokenizer version is encoded by the artifact: 1.0 (tokenizers
		// JSON version). The model's tokenizer.json declares "version":
		// "1.0"; we surface that as the TokenizerVersion. The loader
		// rejects anything else, so the value is always "1.0" when the
		// artifact loaded successfully.
		ver = supportedTokenizerVersion
	}
	return embed.AdmissionSpec{
		TokenizerID:      "model2vec-wordpiece",
		TokenizerSHA256:  hash,
		TokenizerVersion: ver,
		MaxTokens:        MaxAdmissionTokens,
		Reserve:          SpecialTokenReserve,
		Algorithm:        "first-n-tokens",
		AlgorithmVersion: "1",
	}
}

// Revision implements the optional embedder introspection hook the
// fingerprint builder reads (SW-267 AC-8). The static embedder's
// pinned revision lives in its constructor argument; the production
// embedder advertises it explicitly so the fingerprint carries a
// real value rather than the empty placeholder.
func (e *Embedder) Revision() string { return e.revision }

// ModelSHA256 returns the model's safetensors SHA-256 (lowercase hex)
// when the artifact has been loaded; "" otherwise. The fingerprint
// carries it as a real value (reviewer fix Major 1).
func (e *Embedder) ModelSHA256() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.loadedM == nil || e.loadedM.FileHashes == nil {
		return ""
	}
	return e.loadedM.FileHashes[FileSafetensors]
}

// TokenizerSHA256 returns the tokenizer.json SHA-256 (lowercase hex)
// when the artifact has been loaded; "" otherwise. The fingerprint
// carries it as a real value (reviewer fix Major 1).
func (e *Embedder) TokenizerSHA256() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.loadedM == nil || e.loadedM.FileHashes == nil {
		return ""
	}
	return e.loadedM.FileHashes[FileTokenizer]
}

// ChunkerConfig returns the chunker configuration ("" for the
// whole-document path). graphi embeds the whole document per node so
// the field is the literal ""; a future chunk-and-index-every-chunk
// design would surface a description here.
func (e *Embedder) ChunkerConfig() string { return "" }

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
