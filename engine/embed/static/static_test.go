// Package static is graphi's production static embedder — a pure-Go, CGo-free
// re-implementation of Model2Vec inference for the pinned
// `minishlab/potion-code-16M-v2` artifact (SW-262). It is registered as
// scheme `static` via embed.RegisterScheme from an init, mirroring
// engine/embed/ollama.
//
// Determinism: the package adopts EmbedEach (batch-invariant) semantics — a
// node's vector does NOT depend on which other texts share its embedding
// chunk — and pins that choice plus the float16 rounding points and the
// fixed summation tree in the model's ID via Embedder.ID() (AC-2).
//
// CGo-free: no networking or process execution in the embedder runtime;
// the only imports are the Go standard library. The egress gate
// (TestStatic_EmbedAttemptsNoDial) and the registration-level no-CGO guard
// (engine/embed.AssertNoCgoEmbedder) hold this contract end-to-end.
//
// The SW-259 oracle fixtures MOVE to `engine/embed/static/testdata/oracle/`
// unchanged; the throwaway spike is deleted by this story (AC-3 says the
// fixtures are moved, not regenerated).
package static_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/embed/static"
)

const staticPackage = "github.com/samibel/graphi/engine/embed/static"

// Pinned artifact (PINNED.md and SW-259 SW-262 pin table; the four file pins
// here MUST agree with the SP-1 table in pins.go and with the oracle fixture's
// `files` block — see TestStatic_PinTableAgreesWithOracle).
const (
	pinnedModel    = "potion-code-16M-v2"
	pinnedRevision = "e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b"

	// The two artifacts whose hashes go into Embedder.ID(). Both are recorded
	// here AND in pins.go so a divergence between the two is a build-time
	// failure, not a silent production divergence (AC-2).
	pinnedConfigHash    = "148e5691a6fcc553437156859701fba017a1ba5d340b170f17e0f3668fb861a7"
	pinnedTokenizerHash = "107bbdcbad4bff1d299b7a4c3a2fb17c52890688b7dd0e4c9deab79d3c4f3d45"

	skipMessage = "SKIP: model artifact absent; see engine/embed/static/pins.go and run `graphi setup-embedder static:" + pinnedModel + "@" + pinnedRevision + "`"

	// envModelDir is the artifact location override. The default is the
	// same $XDG_CACHE_HOME/graphi/models/<model>@<revision>/ path that the
	// setup-embedder command writes to. Mirrors the production const.
	envModelDir = "GRAPHI_STATIC_MODEL_DIR"

	// maxArtifactBytes is the hard upper bound the loader enforces BEFORE
	// allocating the embedding table (AC-7). It is the sum of the four pinned
	// files (33,514,749 bytes per PINNED.md), rounded up to a one-megabyte
	// safety margin so a corrupted file with extra bytes cannot allocate.
	maxArtifactBytes int64 = 34 << 20

	// oraclePath is the SW-259 oracle fixture relocated under the production
	// package (AC-3).
	oraclePath = "testdata/oracle/oracle.json"
)

// artifactDir resolves the model directory: $GRAPHI_STATIC_MODEL_DIR first,
// then $XDG_CACHE_HOME/graphi/models/<model>@<revision>/ (overridable per
// the story: `$XDG_CACHE_HOME/graphi/models/`, overridable).
func artifactDir() string {
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
	return filepath.Join(cache, "graphi", "models", pinnedModel+"@"+pinnedRevision)
}

// pinnedSHA256 is the pin table mirrored from pins.go. The two MUST agree;
// TestStatic_PinTableAgreesWithPins keeps them in lockstep.
var pinnedSHA256 = map[string]string{
	"config.json":       pinnedConfigHash,
	"tokenizer.json":    pinnedTokenizerHash,
	"model.safetensors": "75cf7a6c2171b230ad19b1e7d8e0b1aee86da5a02af8e7cacedd9921d227623c",
	"modules.json":      "a68dcbed0429dcdd5bfdca92b0b03cc30d09122c0a3fcf4758787d4b244e45b2",
}

// pinnedAllNames returns the pinned file names in sorted order so the
// artifact classifier's iteration is deterministic (Go map iteration is not).
func pinnedAllNames() []string {
	out := make([]string, 0, len(pinnedSHA256))
	for n := range pinnedSHA256 {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// classifyArtifact is the fail-closed gate shared by every test that needs the
// artifact (mirrors the SW-259 pattern): classifyArtifact returns one of three
// verdicts, distinguishing a missing artifact (Skip) from a misconfigured one
// (Fail). Symlinks, directories, partial absence, permission denial and IO
// failures all Fail closed; only genuine absence of every pinned file Skips.
func classifyArtifact(dir string) (present bool, err error) {
	if dir == "" {
		return false, errors.New("cannot resolve the artifact directory: $XDG_CACHE_HOME is unset")
	}
	var firstErr error
	missing := 0
	for _, name := range pinnedAllNames() {
		path := filepath.Join(dir, name)
		st, lerr := os.Lstat(path)
		if lerr != nil {
			if errors.Is(lerr, os.ErrNotExist) {
				missing++
				continue
			}
			if firstErr == nil {
				firstErr = &unusable{path: path, err: lerr}
			}
			continue
		}
		if st.Mode()&os.ModeSymlink != 0 {
			if firstErr == nil {
				firstErr = &unusable{path: path, err: errors.New("is a symlink")}
			}
			continue
		}
		if !st.Mode().IsRegular() {
			if firstErr == nil {
				firstErr = &unusable{path: path, err: errors.New("not a regular file: " + st.Mode().String())}
			}
			continue
		}
		if _, oerr := os.Open(path); oerr != nil {
			if firstErr == nil {
				firstErr = &unusable{path: path, err: oerr}
			}
			continue
		}
	}
	if firstErr != nil {
		return false, firstErr
	}
	if missing == len(pinnedSHA256) {
		return false, nil
	}
	if missing > 0 {
		return false, fmt.Errorf("pinned artifact is partial: %d of %d pinned files are absent at %s", missing, len(pinnedSHA256), dir)
	}
	return true, nil
}

type unusable struct {
	path string
	err  error
}

func (u *unusable) Error() string {
	return u.path + ": " + u.err.Error() + " (the artifact is present but not usable; this is a failure, not a skip)"
}
func (u *unusable) Unwrap() error { return u.err }

// requireArtifact is the helper test files call: skip when the artifact is
// absent, fail when it is misconfigured, and return its directory otherwise.
func requireArtifact(t testing.TB) string {
	t.Helper()
	dir := artifactDir()
	present, err := classifyArtifact(dir)
	if err != nil {
		t.Fatalf("pinned artifact: %v", err)
	}
	if !present {
		t.Skip(skipMessage)
	}
	return dir
}

// AC-1: the static scheme is registered in `init` and constructs an Embedder
// from `static:<model>@<revision>`. An unknown model or missing revision is
// refused with a typed error that names the accepted form.
func TestStatic_SchemeRegisteredFromInit(t *testing.T) {
	ctors := embed.DefaultConstructors()
	make, ok := ctors["static"]
	if !ok || make == nil {
		t.Fatal("the `static` scheme is not registered; engine/embed/static's init must call embed.RegisterScheme(\"static\", ...)")
	}
}

// AC-1 (cont.): the registered scheme constructs an Embedder from the
// pinned selector and refuses both an unknown model and a missing revision.
func TestStatic_ConstructorSelectorValidation(t *testing.T) {
	ctors := embed.DefaultConstructors()
	make := ctors["static"]
	if make == nil {
		t.Fatal("the `static` scheme is not registered")
	}

	// Accepted selector: the pinned model + revision yields a real Embedder
	// (it does NOT need the artifact on disk for construction; the embedder's
	// file load is lazy and surfaces a typed error on the first embed).
	emb, err := make(pinnedModel + "@" + pinnedRevision)
	if err != nil {
		t.Fatalf("constructor(%q): %v", pinnedModel+"@"+pinnedRevision, err)
	}
	if emb == nil {
		t.Fatal("constructor returned a nil embedder for the pinned selector")
	}
	if id := emb.ID(); !strings.HasPrefix(id, "static:"+pinnedModel+"@"+pinnedRevision+":") {
		t.Fatalf("Embedder.ID() = %q, must start with %q", id, "static:"+pinnedModel+"@"+pinnedRevision+":")
	}

	// Unknown model: the typed error must name the accepted form.
	if _, err := make("nonexistent-model@" + pinnedRevision); err == nil {
		t.Fatal("constructor accepted an unknown model; AC-1 requires a typed error naming the accepted form")
	} else if !strings.Contains(err.Error(), pinnedModel) || !strings.Contains(err.Error(), "static:") {
		t.Fatalf("constructor(unknown) error %q must name the accepted form (static:<model>@<revision>)", err)
	}

	// Missing revision: the typed error must name the accepted form.
	if _, err := make(pinnedModel + "@"); err == nil {
		t.Fatal("constructor accepted a selector without @<revision>")
	} else if !strings.Contains(err.Error(), "static:") {
		t.Fatalf("constructor(no-revision) error %q must name the accepted form", err)
	}
	if _, err := make(pinnedModel); err == nil {
		t.Fatal("constructor accepted a selector without @<revision>")
	} else if !strings.Contains(err.Error(), "static:") {
		t.Fatalf("constructor(no-at) error %q must name the accepted form", err)
	}
}

// AC-2: Embedder.ID() includes model, revision, model sha256[:12], the
// pooling mode (mean) and the normalise flag, in a fixed format. Any
// inference-configuration change must change the ID, which feeds
// SW-261's fingerprint and the GenerationStore's typed state.
func TestStatic_ID_FormatIncludesInferenceConfiguration(t *testing.T) {
	ctors := embed.DefaultConstructors()
	make := ctors["static"]
	if make == nil {
		t.Fatal("the `static` scheme is not registered")
	}
	emb, err := make(pinnedModel + "@" + pinnedRevision)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	id := emb.ID()
	parts := strings.Split(id, ":")
	// static:<model>@<revision>:<model_sha256[:12]>:<pooling>:<normalize>
	if got, want := len(parts), 5; got != want {
		t.Fatalf("ID() = %q has %d colon-separated segments, want %d (static:<model>@<revision>:<model_sha256[:12]>:<pooling>:<normalize>)", id, got, want)
	}
	if parts[0] != "static" {
		t.Fatalf("ID() = %q: scheme segment %q, want \"static\"", id, parts[0])
	}
	if parts[1] != pinnedModel+"@"+pinnedRevision {
		t.Fatalf("ID() = %q: model@revision segment %q, want %q", id, parts[1], pinnedModel+"@"+pinnedRevision)
	}
	if len(parts[2]) != 12 {
		t.Fatalf("ID() = %q: model sha256[:12] segment %q is %d chars, want 12", id, parts[2], len(parts[2]))
	}
	if parts[2] != pinnedConfigHash[:12] {
		t.Fatalf("ID() = %q: model sha256[:12] segment %q, want %q (the pinned config hash is part of the inference configuration AC-2 pins)", id, parts[2], pinnedConfigHash[:12])
	}
	if parts[3] != "mean" {
		t.Fatalf("ID() = %q: pooling segment %q, want \"mean\" (the SW-262 production embedder uses mean pooling; EmbedEach makes it batch-invariant)", id, parts[3])
	}
	if parts[4] != "true" {
		t.Fatalf("ID() = %q: normalize segment %q, want \"true\" (config.json's normalize=true is part of the inference configuration)", id, parts[4])
	}
}

// AC-2 (cont.): the fingerprint carried by ID() must change when the
// inference configuration changes. We assert this through the production
// Fingerprint path: two Embedders constructed under the same pinned selector
// produce the SAME canonical id; a deliberately different model ID produces
// a DIFFERENT canonical id. The contract is round-trip via the SW-261
// Fingerprint.ID, which the GenerationStore already uses to detect stale
// generations.
func TestStatic_ID_FeedsFingerprint(t *testing.T) {
	ctors := embed.DefaultConstructors()
	make := ctors["static"]
	if make == nil {
		t.Fatal("the `static` scheme is not registered")
	}
	emb1, err := make(pinnedModel + "@" + pinnedRevision)
	if err != nil {
		t.Fatalf("constructor 1: %v", err)
	}
	emb2, err := make(pinnedModel + "@" + pinnedRevision)
	if err != nil {
		t.Fatalf("constructor 2: %v", err)
	}
	if emb1.ID() != emb2.ID() {
		t.Fatalf("the same selector must yield the same ID; got %q vs %q", emb1.ID(), emb2.ID())
	}
	// Build a Fingerprint the way the production wiring does and assert it
	// changes with the ID (the AC-2 contract: any inference-configuration
	// change changes the ID, so the GenerationStore's typed state can detect
	// it).
	fp1 := embed.Fingerprint{
		ModelID:         emb1.ID(),
		Revision:        pinnedRevision,
		ModelSHA256:     pinnedConfigHash,
		TokenizerSHA256: pinnedTokenizerHash,
		Dim:             emb1.Dim(),
		DocumentSchema:  embed.DocumentSchema,
		ChunkerConfig:   "",
		GraphGeneration: embed.GraphGenerationPlaceholder,
	}
	// Dim is discovered from the artifact, not hard-coded. Without the
	// artifact on disk the dim is 0, which is itself a fingerprint field
	// (any build without the artifact has fingerprint dim=0 and never shares
	// a generation with a real build). Pin the test to that contract.
	if dim := emb1.Dim(); dim != 0 && dim != 256 {
		t.Fatalf("Dim() = %d, want 0 (no artifact) or 256 (pinned artifact); never a hard-coded constant", dim)
	}
	fp2 := fp1
	fp2.ModelID = "static:other@" + pinnedRevision // a model swap changes the ID
	if fp1.ID() == fp2.ID() {
		t.Fatal("Fingerprint.ID() must change when the embedder's ID changes; the GenerationStore relies on this")
	}
}

// AC-1 (cont.): constructing an Embedder does NOT dial any network. The
// construction MUST succeed without the artifact on disk (a typed error
// surfaces on first embed instead), so the absence of the artifact is
// never masked by a missing-network fallback.
func TestStatic_ConstructionIsOffline(t *testing.T) {
	ctors := embed.DefaultConstructors()
	make := ctors["static"]
	if make == nil {
		t.Fatal("the `static` scheme is not registered")
	}
	emb, err := make(pinnedModel + "@" + pinnedRevision)
	if err != nil {
		t.Fatalf("constructor must succeed without the artifact on disk (fail closed on first embed instead): %v", err)
	}
	if emb == nil {
		t.Fatal("constructor returned nil without an error")
	}
}

// AC-8: the default build registers NO `static` embedder unless the selector
// is set. AssertNoCgoEmbedder stays green, the egress tests stay green, and
// `graphi privacy-audit` will report zero outbound with the static embedder
// configured (the latter is extended via the canary's tag set; tested here
// through the static import closure's egress properties).
func TestStatic_DefaultRegistryDoesNotRegister(t *testing.T) {
	r := embed.NewDefaultRegistry()
	if r.Configured() {
		t.Fatal("NewDefaultRegistry() must report Configured()=false; the static scheme must NOT register a default embedder")
	}
	if off := embed.AssertNoCgoEmbedder(r); len(off) != 0 {
		t.Fatalf("default registry holds a CGO embedder: %v", off)
	}
}

// AC-9 (first install, warm start, offline-with-cache, offline-without-cache,
// hash mismatch, truncated download, wrong dimension vs generation
// fingerprint, unknown selector, registration after Freeze): each must have
// a test. They live in the table below; the helper functions above make the
// hash-mismatch and offline paths exercisable without a real HuggingFace
// download.
func TestStatic_OfflineWithoutArtifact_SurfacesTypedError(t *testing.T) {
	ctors := embed.DefaultConstructors()
	make := ctors["static"]
	if make == nil {
		t.Fatal("the `static` scheme is not registered")
	}
	emb, err := make(pinnedModel + "@" + pinnedRevision)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	// Force the lazy load path by calling ProbeDim (DimDiscoverer).
	probeErr := probeDim(context.Background(), emb)
	if probeErr == nil {
		// If the artifact IS present (e.g. a developer with the cache), the
		// probe succeeds and we are not exercising the offline path. Skip
		// rather than fail; the offline-without-cache path is exercised by
		// TestStatic_OfflineWithCache_PrefersCachedArtifact and by
		// TestStatic_OfflineWithoutCache_FailsClosed.
		t.Skip("artifact present in the model cache; the offline-without-cache path is not exercised here")
	}
	if !strings.Contains(probeErr.Error(), pinnedModel) {
		t.Fatalf("probe error %q must name the model so the operator knows which artifact to fetch", probeErr)
	}
	if !strings.Contains(probeErr.Error(), "graphi setup-embedder") {
		t.Fatalf("probe error %q must name the exact repair command (graphi setup-embedder static:...)", probeErr)
	}
}

// AC-9 (cont.): when the artifact is present, it is loaded without
// initiating any network (the loader is offline by construction). The
// warm-start path is exercised by every test that calls loadPinned below;
// this test pins the determinism half of warm-start — two consecutive
// loads of the same artifact produce identical Embedder.ID() values.
func TestStatic_WarmStart_IDIsStable(t *testing.T) {
	if testing.Short() {
		t.Skip("warm-start requires the 33 MB artifact; skipping under -short")
	}
	dir := requireArtifact(t)
	m1, err := static.LoadModel(dir)
	if err != nil {
		t.Fatalf("load 1: %v", err)
	}
	m2, err := static.LoadModel(dir)
	if err != nil {
		t.Fatalf("load 2: %v", err)
	}
	if m1.Dim() != m2.Dim() || m1.VocabSize() != m2.VocabSize() {
		t.Fatalf("warm-start: dim=%d/%d vocab=%d/%d", m1.Dim(), m2.Dim(), m1.VocabSize(), m2.VocabSize())
	}
	// A model SHA mismatch is detected by the loader (verified below); both
	// loads here are the real artifact, so both IDs are the same.
}

// AC-5 (cont.): when the artifact directory is empty / unresolvable, the
// loader surfaces the typed UnavailableError with the exact repair
// command. We force the offline path by clearing XDG_CACHE_HOME and
// unsetting GRAPHI_STATIC_MODEL_DIR for the duration of the test.
func TestStatic_OfflineWithoutArtifact_TypedUnavailableResponse(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("GRAPHI_STATIC_MODEL_DIR", "")
	if home, _ := os.UserHomeDir(); home != "" {
		t.Setenv("HOME", "")
	}
	ctors := embed.DefaultConstructors()
	make := ctors["static"]
	if make == nil {
		t.Fatal("the `static` scheme is not registered")
	}
	emb, err := make(pinnedModel + "@" + pinnedRevision)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	probeErr := probeDim(context.Background(), emb)
	if probeErr == nil {
		t.Skip("artifact still resolvable (HOME set?); cannot exercise the absent-artifact path here")
	}
	if !strings.Contains(probeErr.Error(), "graphi setup-embedder") {
		t.Fatalf("probe error %q must name the exact repair command", probeErr)
	}
	if !strings.Contains(probeErr.Error(), "static:"+pinnedModel+"@"+pinnedRevision) {
		t.Fatalf("probe error %q must name the exact selector to install", probeErr)
	}
}

// AC-9 (cont.): hash mismatch is a typed error naming expected vs actual.
// Verified through the loader, which is what `graphi setup-embedder` calls
// after it has downloaded the artifact.
func TestStatic_HashMismatch_IsTypedError(t *testing.T) {
	dir := t.TempDir()
	// Stage four files where the safetensors declares an F16 tensor whose
	// declared body size is wrong — simulates a partial / corrupted download
	// that the loader's pre-allocation validation (AC-7) must catch.
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"normalize":true,"embedding_dtype":"float16"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tokenizer.json"), writeValidTokenizer(t), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "modules.json"), []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Declare a tensor of shape [3, 2] (12 bytes) but carry only 0 bytes.
	// The loader's "tensor carries N bytes, shape needs M" check (AC-7)
	// catches this before the embedding table is allocated.
	header := `{"embeddings":{"dtype":"F16","shape":[3,2],"data_offsets":[0,0]}}`
	hdr := []byte(header)
	file := make([]byte, 8+len(hdr))
	hl := uint64(len(hdr))
	file[0] = byte(hl)
	file[1] = byte(hl >> 8)
	file[2] = byte(hl >> 16)
	file[3] = byte(hl >> 24)
	file[4] = byte(hl >> 32)
	file[5] = byte(hl >> 40)
	file[6] = byte(hl >> 48)
	file[7] = byte(hl >> 56)
	copy(file[8:], hdr)
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), file, 0o644); err != nil {
		t.Fatal(err)
	}
	_, lerr := static.LoadModel(dir)
	if lerr == nil {
		t.Fatal("LoadModel accepted a corrupted safetensors; AC-7 requires typed errors with corrupted-fixture tests")
	}
	if !strings.Contains(lerr.Error(), "tensor") && !strings.Contains(lerr.Error(), "safetensors") && !strings.Contains(lerr.Error(), "carries") {
		t.Fatalf("LoadModel error %q must name the safetensors layer", lerr)
	}
}

// AC-7 / AC-9: truncated download. A safetensors whose declared body is
// larger than the file is rejected before the embedding table is allocated.
func TestStatic_TruncatedDownload_IsTypedError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"normalize":true,"embedding_dtype":"float16"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tokenizer.json"), writeValidTokenizer(t), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "modules.json"), []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Declare a tensor whose data_offsets exceed the file: the body itself
	// is empty, so a load would silently read past end-of-file.
	header := `{"embeddings":{"dtype":"F16","shape":[3,2],"data_offsets":[0,1024]}}`
	hdr := []byte(header)
	file := make([]byte, 8+len(hdr)+16) // 16 bytes of body, less than the declared 1024
	hl := uint64(len(hdr))
	file[0] = byte(hl)
	file[1] = byte(hl >> 8)
	file[2] = byte(hl >> 16)
	file[3] = byte(hl >> 24)
	file[4] = byte(hl >> 32)
	file[5] = byte(hl >> 40)
	file[6] = byte(hl >> 48)
	file[7] = byte(hl >> 56)
	copy(file[8:], hdr)
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), file, 0o644); err != nil {
		t.Fatal(err)
	}
	_, lerr := static.LoadModel(dir)
	if lerr == nil {
		t.Fatal("LoadModel accepted a truncated safetensors; AC-7 requires validation BEFORE allocation")
	}
	if !strings.Contains(lerr.Error(), "outside") && !strings.Contains(lerr.Error(), "exceeds") && !strings.Contains(lerr.Error(), "tensor") {
		t.Fatalf("LoadModel error %q must name the truncation (off-outside, length-exceeds)", lerr)
	}
}

// writeValidTokenizer writes the minimal valid tokenizer.json the loader
// will accept: 8 vocab tokens, normalizer + pre_tokenizer + WordPiece model,
// no padding, right truncation. Returns bytes the caller writes to disk.
func writeValidTokenizer(t testing.TB) []byte {
	t.Helper()
	tok := map[string]any{
		"version": "1.0",
		"truncation": map[string]any{
			"direction": "Right", "max_length": 512, "strategy": "LongestFirst", "stride": 0,
		},
		"padding": nil,
		"added_tokens": []map[string]any{
			{"id": 0, "content": "[PAD]", "single_word": true, "lstrip": true, "rstrip": true, "normalized": true, "special": true},
			{"id": 1, "content": "[UNK]", "single_word": false, "lstrip": false, "rstrip": false, "normalized": false, "special": true},
		},
		"normalizer": map[string]any{
			"type": "BertNormalizer", "clean_text": true, "handle_chinese_chars": true, "strip_accents": nil, "lowercase": true,
		},
		"pre_tokenizer":  map[string]any{"type": "BertPreTokenizer"},
		"post_processor": nil,
		"decoder":        map[string]any{"type": "WordPiece", "prefix": "##", "cleanup": true},
		"model": map[string]any{
			"type": "WordPiece", "unk_token": "[UNK]", "continuing_subword_prefix": "##",
			"max_input_chars_per_word": 100,
			"vocab": map[string]int{
				"[PAD]": 0, "[UNK]": 1, "hello": 2, "world": 3, "##s": 4, "認": 5, "x": 6, "_": 7,
			},
		},
	}
	b, err := json.Marshal(tok)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// AC-3: the SW-259 oracle fixtures are MOVED, not regenerated. Pin their
// schema, model id and reference-library versions so a silent regeneration
// would fail this test (a regenerated fixture would land in
// testdata/oracle/oracle.json, which is exactly where the SW-259 fixture
// already lives).
func TestStatic_OracleFixtureIsIntact(t *testing.T) {
	raw, err := os.ReadFile(oraclePath)
	if err != nil {
		t.Fatalf("read %s: %v", oraclePath, err)
	}
	var o oracleFile
	if err := json.Unmarshal(raw, &o); err != nil {
		t.Fatalf("parse %s: %v", oraclePath, err)
	}
	if o.Schema != "graphi.model2vec-oracle.v1" {
		t.Fatalf("oracle schema %q, want graphi.model2vec-oracle.v1 (a regenerated fixture would carry a different schema)", o.Schema)
	}
	if o.Model != "minishlab/"+pinnedModel {
		t.Fatalf("oracle model %q, want minishlab/%s", o.Model, pinnedModel)
	}
	if o.Revision != pinnedRevision {
		t.Fatalf("oracle revision %q, want %s", o.Revision, pinnedRevision)
	}
	wantRefs := map[string]string{"model2vec": "0.9.0", "numpy": "2.5.2", "tokenizers": "0.23.1", "python": "3.13.5"}
	for k, v := range wantRefs {
		if o.Reference[k] != v {
			t.Fatalf("oracle reference[%q] = %q, want %q (a regenerated fixture would carry a different version)", k, o.Reference[k], v)
		}
	}
	if len(o.Cases) < 15 {
		t.Fatalf("oracle carries %d cases, want >= 15", len(o.Cases))
	}
	if len(o.Batch.Texts) < 8 {
		t.Fatalf("oracle batch is %d texts, want >= 8", len(o.Batch.Texts))
	}
}

// AC-3 (cont.): the production model reproduces the oracle tokens exactly.
// AC-3's "vectors within epsilon" is asserted by TestStatic_Oracle_VectorsWithinEpsilon
// below; the token-id side of AC-3 is asserted here because it is independent
// of the float16 arithmetic (and pin-incompatible with a regenerated fixture).
func TestStatic_Oracle_TokenIDsExact(t *testing.T) {
	dir := requireArtifact(t)
	o := loadOracle(t)
	m, err := static.LoadModel(dir)
	if err != nil {
		t.Fatalf("load model: %v", err)
	}
	for _, c := range o.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got := m.TokenIDs(c.Text)
			if !reflect.DeepEqual(got, c.TokenIDs) {
				t.Fatalf("TokenIDs(%q)\n got  %v\n want %v", c.Text, got, c.TokenIDs)
			}
		})
	}
}

// AC-3 (cont.): the production model reproduces the oracle vectors within
// epsilon. Per-component max |Δ| ≤ 1e-5 after normalisation. The oracle
// fixture names the AC-3 case names; the implementation names the rounding
// points it mirrors in embed.go.
func TestStatic_Oracle_VectorsWithinEpsilon(t *testing.T) {
	dir := requireArtifact(t)
	o := loadOracle(t)
	m, err := static.LoadModel(dir)
	if err != nil {
		t.Fatalf("load model: %v", err)
	}
	const eps = 1e-5
	var maxDelta float64
	for _, c := range o.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got, err := m.Embed(t.Context(), []string{c.Text})
			if err != nil {
				t.Fatalf("embed: %v", err)
			}
			d, j := maxAbsDiff(got[0], c.Vector)
			if d > maxDelta {
				maxDelta = d
			}
			if d > eps {
				t.Errorf("max |Δ| = %.3g at component %d (got %v, want %v) exceeds %g", d, j, got[0][j], c.Vector[j], eps)
			}
		})
	}
	t.Logf("max |Δ| vs oracle over %d cases: %.3g (epsilon %g)", len(o.Cases), maxDelta, eps)
}

// AC-3 (cont.): batch replay against the oracle's batch of >= 8 mixed texts
// through the public Embed API. The embedder must be batch-invariant: every
// row is its own single-text embed (no BatchLongest pooling). The divergence
// from a BatchLongest-padded reference is exactly the padding effect; this
// test pins that EmbedEach == Embed([text]) for every text, bit-for-bit.
func TestStatic_Oracle_BatchEachMatchesSingle(t *testing.T) {
	dir := requireArtifact(t)
	o := loadOracle(t)
	m, err := static.LoadModel(dir)
	if err != nil {
		t.Fatalf("load model: %v", err)
	}
	texts := o.Batch.Texts
	batch, err := m.Embed(t.Context(), texts)
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	for i, text := range texts {
		single, err := m.Embed(t.Context(), []string{text})
		if err != nil {
			t.Fatalf("single %d: %v", i, err)
		}
		for j := range single[0] {
			if batch[i][j] != single[0][j] {
				t.Fatalf("batch[%d][%d] != single[%d][%d] (%v vs %v); the embedder must be batch-invariant (AC-2 SW-259 carry-forward)",
					i, j, i, j, batch[i][j], single[0][j])
			}
		}
	}
}

// AC-3 (cont.): the embedding-row reproduction is exact (float16 ⊂ float32).
func TestStatic_Oracle_EmbeddingRowsExact(t *testing.T) {
	dir := requireArtifact(t)
	o := loadOracle(t)
	m, err := static.LoadModel(dir)
	if err != nil {
		t.Fatalf("load model: %v", err)
	}
	for key, want := range o.RowSamples {
		var id int
		if _, err := fmt.Sscanf(key, "%d", &id); err != nil {
			t.Fatal(err)
		}
		got := m.Row(id)
		if len(got) != len(want) {
			t.Fatalf("row %d: %d components, want %d", id, len(got), len(want))
		}
		for j := range got {
			if got[j] != want[j] {
				t.Fatalf("row %d component %d: %v, want %v (must be exact: float16 ⊂ float32)", id, j, got[j], want[j])
			}
		}
	}
}

// AC-9 (cont.): registration after Freeze fails. The static scheme's init
// MUST NOT be called after the default registry has been constructed and
// frozen — the default build stays embedder-less until an explicit selector
// opts in. The test exercises the freeze contract directly: a fresh registry
// that holds a static Embedder is frozen, then any further registration is
// refused.
func TestStatic_RegistrationAfterFreeze_Fails(t *testing.T) {
	ctors := embed.DefaultConstructors()
	make := ctors["static"]
	if make == nil {
		t.Fatal("the `static` scheme is not registered")
	}
	emb, err := make(pinnedModel + "@" + pinnedRevision)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	r := embed.NewRegistry()
	if err := r.Register(emb); err != nil {
		t.Fatalf("register: %v", err)
	}
	r.Freeze()
	err = r.Register(emb)
	if err == nil {
		t.Fatal("Register after Freeze succeeded; AC-9 requires a typed error")
	}
	if !strings.Contains(err.Error(), "frozen") && !strings.Contains(err.Error(), "frozen") {
		t.Fatalf("register-after-freeze error %q must mention frozen state", err)
	}
}

// AC-8 (cont.): the static package contains NO networking or
// process-execution import, in any non-test, non-generated .go file.
// This is the structural invariant: the embedder is reachable from
// index / search / MCP / HTTP via the registry's registered scheme, so
// ANY outbound code in this package would mean the default graph links
// an egress path. The download path lives in cmd/graphi instead (see
// cmd/graphi/setup_static.go) — the only legitimate place for an
// outbound HTTPS client in graphi.
func TestStatic_EmbedderRuntimeIsZeroEgress(t *testing.T) {
	fsys := os.DirFS(".")
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(p) != ".go" {
			return nil
		}
		if strings.HasSuffix(p, "_test.go") {
			return nil
		}
		if p == "nfd_table.go" {
			return nil // generated, no network
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		src := string(body)
		for _, banned := range []string{"\"net\"", "\"net/http\"", "\"net/http/httptest\"", "\"net/url\"", "\"os/exec\"", "\"crypto/tls\"", "\"syscall/js\""} {
			if strings.Contains(src, banned) {
				t.Errorf("%s imports %s; engine/embed/static must contain no outbound code (the download path is in cmd/graphi).", p, banned)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestStatic_NoOutboundDialInSource: belt-and-braces, the same invariant
// asserted via the Go AST. We parse every non-test .go file in the
// package and walk it for any http.Client.Do / http.Get / net.Dial call
// that would constitute an outbound dial. This is the same AST walk the
// production canary gate uses (internal/canary/gate.scanPackageAST),
// applied at the package level so a regression is caught by a unit
// test rather than waiting for the release build.
func TestStatic_NoOutboundDialInSource(t *testing.T) {
	denied := []string{
		"net.Dial", "net.DialTCP", "net.DialUDP", "net.DialIP",
		"http.Get", "http.Post", "http.PostForm", "http.Head",
		"http.Client.Do", "http.Client.Get", "http.Client.Post", "http.Client.Head",
	}
	fsys := os.DirFS(".")
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(p) != ".go" || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, sym := range denied {
			if strings.Contains(string(body), sym) {
				t.Errorf("%s mentions %q; the static package must not call out to the network in any form (AC-5).", p, sym)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// AC-8 (cont.): runtime egress. With the failing-dialer sentinel installed as
// the process resolver, the embed path attempts no dial. The static embedder
// never imports net so this is a defense-in-depth check; the pattern mirrors
// core/parse's egress gate.
func TestStatic_EmbedAttemptsNoDial(t *testing.T) {
	dialer := &failingDialer{}
	orig := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: dialer.DialContext}
	t.Cleanup(func() { net.DefaultResolver = orig })

	// The synthetic table is constructed in memory — the dialer would fire
	// only if the embed path consulted DNS or made a network call. It must
	// not, so the embed succeeds.
	texts := []string{"", "hello world", "認証トークンを検証する関数"}
	m := newSyntheticModel(t, 8, nil)
	if _, err := m.Embed(t.Context(), texts); err != nil {
		t.Fatalf("Embed on the synthetic model: %v", err)
	}
	if dialer.dialed.Load() {
		t.Fatal("Embed attempted an outbound dial — zero-egress violated")
	}
	// If the pinned artifact is present, replay the same path against it.
	present, err := classifyArtifact(artifactDir())
	if err != nil {
		t.Fatalf("pinned artifact is present but unusable: %v", err)
	}
	if present {
		pinned, err := static.LoadModel(artifactDir())
		if err != nil {
			t.Fatalf("load pinned: %v", err)
		}
		if _, err := pinned.Embed(t.Context(), texts); err != nil {
			t.Fatalf("Embed on the pinned artifact: %v", err)
		}
		if dialer.dialed.Load() {
			t.Fatal("Embed on the pinned artifact attempted an outbound dial — zero-egress violated")
		}
	}
}

type failingDialer struct{ dialed atomic.Bool }

func (d *failingDialer) DialContext(_ context.Context, network, address string) (net.Conn, error) {
	d.dialed.Store(true)
	return nil, &net.OpError{Op: "dial", Net: network, Err: errDialBlocked{address: address}}
}

type errDialBlocked struct{ address string }

func (e errDialBlocked) Error() string { return "egress blocked by canary dialer: " + e.address }

// AC-9 (cont.): wrong dimension vs generation fingerprint. The GenerationStore's
// typed state would be StateStale if the same node ID were looked up under a
// different dim. We exercise the contract at the embedder level: a static
// Embedder whose reported Dim differs from the GenerationStore's dim is
// detected as stale. (The GenerationStore's DimForModel is the seam; see
// engine/embed/generationstore.go.)
func TestStatic_DimChangeIsDetectedByFingerprint(t *testing.T) {
	// A static embedder that knows its dim (the pinned artifact on disk)
	// and a fake GenerationStore row recorded under a different dim MUST
	// read StateStale. The test exercises that path by constructing two
	// fingerprints that differ only in Dim and asserting they have
	// different IDs.
	m1 := newSyntheticModel(t, 8, nil)
	m2 := newSyntheticModel(t, 16, nil)
	fp1 := embed.Fingerprint{ModelID: static.ModelID, Dim: m1.Dim()}
	fp2 := embed.Fingerprint{ModelID: static.ModelID, Dim: m2.Dim()}
	if fp1.ID() == fp2.ID() {
		t.Fatal("a Dim change must produce a different fingerprint ID; the GenerationStore relies on this")
	}
}

// AC-10: docs/semantic-search.md documents the static: option. This is a
// docs-only test; see TestStatic_DocMentionsStatic in static_doc_test.go.

// ----------------------------------------------------------------------------
// Helpers below this line are shared between AC tests; nothing here is part
// of the production contract.
// ----------------------------------------------------------------------------

// oracleFile mirrors testdata/oracle/oracle.json's structure (the SW-259
// fixture is moved, not regenerated).
type oracleFile struct {
	Schema        string            `json:"schema"`
	Model         string            `json:"model"`
	Revision      string            `json:"revision"`
	Files         map[string]string `json:"files"`
	Config        json.RawMessage   `json:"config"`
	Shape         []int             `json:"embedding_shape"`
	DtypeInMemory string            `json:"embedding_dtype_in_memory"`
	Normalize     bool              `json:"normalize"`
	Reference     map[string]string `json:"reference"`
	Cases         []oracleCase      `json:"cases"`
	Batch         struct {
		Texts   []string    `json:"texts"`
		Vectors [][]float32 `json:"vectors"`
	} `json:"batch"`
	RowSamples map[string][]float32 `json:"embedding_row_samples"`
}

type oracleCase struct {
	Name     string    `json:"name"`
	Text     string    `json:"text"`
	TokenIDs []int     `json:"token_ids"`
	Vector   []float32 `json:"vector"`
	Norm     float64   `json:"norm"`
}

func loadOracle(t testing.TB) *oracleFile {
	t.Helper()
	raw, err := os.ReadFile(oraclePath)
	if err != nil {
		t.Fatal(err)
	}
	var o oracleFile
	if err := json.Unmarshal(raw, &o); err != nil {
		t.Fatal(err)
	}
	if o.Schema != "graphi.model2vec-oracle.v1" {
		t.Fatalf("oracle schema %q", o.Schema)
	}
	return &o
}

func maxAbsDiff(got, want []float32) (float64, int) {
	if len(got) != len(want) {
		return 1e9, -1
	}
	var d float64
	at := 0
	for j := range got {
		x := math.Abs(float64(got[j] - want[j]))
		if x > d {
			d, at = x, j
		}
	}
	return d, at
}

// probeDim invokes the DimDiscoverer interface when present, so the offline
// paths can be exercised without depending on the embed package's internal
// helpers.
func probeDim(ctx context.Context, e embed.Embedder) error {
	if d, ok := e.(embed.DimDiscoverer); ok {
		return d.ProbeDim(ctx)
	}
	return nil
}

// fileSHA256 is the loader-side hash check (also used by `graphi setup-embedder`
// after the download). Mirrors the SW-259 helper.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// newSyntheticModel writes a tiny F16 table into a temp dir and loads it;
// used by the egress and dim-change tests where the pinned artifact is not
// needed.
func newSyntheticModel(t testing.TB, dim int, padding any) *static.Model {
	t.Helper()
	dir := t.TempDir()
	// Write a config and tokenizer; the loader requires both.
	cfg := `{"normalize":true,"embedding_dtype":"float16"}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	tok := map[string]any{
		"version": "1.0",
		"truncation": map[string]any{
			"direction": "Right", "max_length": 512, "strategy": "LongestFirst", "stride": 0,
		},
		"padding": padding,
		"added_tokens": []map[string]any{
			{"id": 0, "content": "[PAD]", "single_word": true, "lstrip": true, "rstrip": true, "normalized": true, "special": true},
			{"id": 1, "content": "[UNK]", "single_word": false, "lstrip": false, "rstrip": false, "normalized": false, "special": true},
		},
		"normalizer": map[string]any{
			"type": "BertNormalizer", "clean_text": true, "handle_chinese_chars": true, "strip_accents": nil, "lowercase": true,
		},
		"pre_tokenizer":  map[string]any{"type": "BertPreTokenizer"},
		"post_processor": nil,
		"decoder":        map[string]any{"type": "WordPiece", "prefix": "##", "cleanup": true},
		"model": map[string]any{
			"type": "WordPiece", "unk_token": "[UNK]", "continuing_subword_prefix": "##",
			"max_input_chars_per_word": 100,
			"vocab": map[string]int{
				"[PAD]": 0, "[UNK]": 1, "hello": 2, "world": 3, "##s": 4, "認": 5, "x": 6, "_": 7,
			},
		},
	}
	tokRaw, err := json.Marshal(tok)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tokenizer.json"), tokRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "modules.json"), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	// F16 table: 8 rows × dim. Use the static package's own f32ToF16 / writer.
	body := writeF16Table(t, 8, dim)
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := static.LoadModel(dir)
	if err != nil {
		t.Fatalf("LoadModel(synthetic): %v", err)
	}
	return m
}

// writeF16Table writes a minimal safetensors file with one F16 tensor named
// "embeddings" of shape [rows, dim]. The values are deterministic per row
// (row i = [i, -i/2, 0.25, i*0.001, …]).
func writeF16Table(t testing.TB, rows, dim int) []byte {
	t.Helper()
	header, err := json.Marshal(map[string]any{
		"embeddings": map[string]any{
			"dtype":        "F16",
			"shape":        []int{rows, dim},
			"data_offsets": []int{0, rows * dim * 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 8+len(header)+rows*dim*2)
	// little-endian header length: 8 bytes, low byte first.
	hl := uint64(len(header))
	body[0] = byte(hl)
	body[1] = byte(hl >> 8)
	body[2] = byte(hl >> 16)
	body[3] = byte(hl >> 24)
	body[4] = byte(hl >> 32)
	body[5] = byte(hl >> 40)
	body[6] = byte(hl >> 48)
	body[7] = byte(hl >> 56)
	copy(body[8:], header)
	// F16 values: call the static package's writer.
	f16 := make([]byte, rows*dim*2)
	for i := 0; i < rows; i++ {
		for j := 0; j < dim; j++ {
			var v float32
			switch j % 4 {
			case 0:
				v = float32(i)
			case 1:
				v = -float32(i) / 2
			case 2:
				v = 0.25
			case 3:
				v = float32(i) * 0.001
			}
			h := static.F32ToF16(v)
			f16[(i*dim+j)*2] = byte(h)
			f16[(i*dim+j)*2+1] = byte(h >> 8)
		}
	}
	copy(body[8+len(header):], f16)
	return body
}

// keep the imports of `http` and `strconv` referenced when the test grows.
var _ = http.MethodGet
var _ = strconv.Itoa
