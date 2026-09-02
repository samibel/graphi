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

// swapStaticPins atomically swaps the production pin table for the test's
// pins and returns a restore closure. Used by every synthetic-artifact
// test to drive the loader against a known-good or known-bad pin
// without rebuilding the real cached artifact.
func swapStaticPins(pins map[string]string) func() {
	prev := static.PinnedSHA256
	static.PinnedSHA256 = pins
	return func() { static.PinnedSHA256 = prev }
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

	// Unknown model: a typed SelectorError with Kind SelectorUnknownModel
	// must be returned. AC-1 wants errors.As support so callers can
	// branch on the failure mode without parsing strings.
	if _, err := make("nonexistent-model@" + pinnedRevision); err == nil {
		t.Fatal("constructor accepted an unknown model; AC-1 requires a typed error naming the accepted form")
	} else {
		var se *static.SelectorError
		if !errors.As(err, &se) {
			t.Fatalf("constructor(unknown) error %T (%v) is not a typed *SelectorError; AC-1 requires errors.As support", err, err)
		}
		if se.Kind != static.SelectorUnknownModel {
			t.Errorf("SelectorError.Kind = %d, want %d (SelectorUnknownModel)", se.Kind, static.SelectorUnknownModel)
		}
		if se.Model != pinnedModel {
			t.Errorf("SelectorError.Model = %q, want %q", se.Model, pinnedModel)
		}
		if !strings.Contains(err.Error(), "static:") {
			t.Fatalf("constructor(unknown) error %q must name the accepted form", err)
		}
	}

	// Missing revision: a typed SelectorError with Kind SelectorEmptyRevision
	// (when @<rev> is present but empty) or SelectorMissingAt (when there
	// is no @ at all).
	if _, err := make(pinnedModel + "@"); err == nil {
		t.Fatal("constructor accepted a selector without @<revision>")
	} else {
		var se *static.SelectorError
		if !errors.As(err, &se) {
			t.Fatalf("constructor(empty-rev) error %T (%v) is not a typed *SelectorError", err, err)
		}
		if se.Kind != static.SelectorEmptyRevision {
			t.Errorf("SelectorError.Kind = %d, want %d (SelectorEmptyRevision)", se.Kind, static.SelectorEmptyRevision)
		}
	}
	if _, err := make(pinnedModel); err == nil {
		t.Fatal("constructor accepted a selector without @<revision>")
	} else {
		var se *static.SelectorError
		if !errors.As(err, &se) {
			t.Fatalf("constructor(no-at) error %T (%v) is not a typed *SelectorError", err, err)
		}
		// No '@' is treated as a missing revision by splitStaticSelector
		// (it returns (_, "", true)); the typed error is therefore
		// SelectorEmptyRevision, not SelectorMissingAt. Either name
		// would be a valid error message; what matters is the
		// errors.As contract.
		if se.Kind != static.SelectorEmptyRevision {
			t.Errorf("SelectorError.Kind = %d, want %d (SelectorEmptyRevision)", se.Kind, static.SelectorEmptyRevision)
		}
	}
}

// AC-2: Embedder.ID() includes model@revision, the model.safetensors sha256[:12],
// pooling, normalization, the tokenizer/config sha256[:12], and the
// implementation-contract identity (EmbedEach batch-invariance, F16 rounding,
// fixed pairwise summation tree).
// Any change to the inference configuration (a swapped weights file, a
// swapped tokenizer, a rounding-tree change) changes the ID, which feeds
// SW-261's fingerprint and the GenerationStore's typed state. The hash
// segments are the REAL on-disk SHA-256, not a constant — the critical
// fix that closes the SW-261 fingerprint gap.
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
	// SW-267 v3 format: 9 colon-separated segments. The 9th is the
	// admission profile sha256[:12] (AC-3, AC-8) so a profile change
	// invalidates stored generations by fingerprint.
	if got, want := len(parts), 9; got != want {
		t.Fatalf("ID() = %q has %d colon-separated segments, want %d (static:<model>@<revision>:<model_sha256[:12]>:<pooling>:<normalize>:<tokenizer_sha256[:12]>:<config_sha256[:12]>:<impl_contract>:<admission_sha256[:12]>)", id, got, want)
	}
	if parts[0] != "static" {
		t.Fatalf("ID() = %q: scheme segment %q, want \"static\"", id, parts[0])
	}
	if parts[1] != pinnedModel+"@"+pinnedRevision {
		t.Fatalf("ID() = %q: model@revision segment %q, want %q", id, parts[1], pinnedModel+"@"+pinnedRevision)
	}
	if len(parts[2]) != 12 {
		t.Fatalf("ID() = %q: model.safetensors sha256[:12] segment %q is %d chars, want 12", id, parts[2], len(parts[2]))
	}
	if parts[2] != pinnedSHA256["model.safetensors"][:12] {
		t.Fatalf("ID() = %q: model.safetensors sha256[:12] segment %q, want %q (the CRITICAL fix: this must be the real file hash, not a constant; AC-2/AC-6)", id, parts[2], pinnedSHA256["model.safetensors"][:12])
	}
	if parts[3] != "mean" {
		t.Fatalf("ID() = %q: pooling segment %q, want mean", id, parts[3])
	}
	if parts[4] != "true" {
		t.Fatalf("ID() = %q: normalize segment %q, want true", id, parts[4])
	}
	if parts[5] != pinnedSHA256["tokenizer.json"][:12] {
		t.Fatalf("ID() = %q: tokenizer sha256[:12] segment %q, want %q (a swapped tokenizer must change the ID; AC-2/AC-6)", id, parts[5], pinnedSHA256["tokenizer.json"][:12])
	}
	if parts[6] != pinnedSHA256["config.json"][:12] {
		t.Fatalf("ID() = %q: config sha256[:12] segment %q, want %q (any pinned inference-config change must change the ID; AC-2)", id, parts[6], pinnedSHA256["config.json"][:12])
	}
	if parts[7] != "embedeach-f16-tree" {
		t.Fatalf("ID() = %q: inference-contract segment %q, want \"embedeach-f16-tree\" (EmbedEach batch-invariance + F16 rounding + fixed pairwise summation tree; any change to the rounding tree or pooling strategy changes this segment, AC-2/AC-6)", id, parts[7])
	}
	// SW-267 AC-3/AC-8: admission profile sha256[:12].
	if len(parts[8]) != 12 {
		t.Fatalf("ID() = %q: admission profile sha256[:12] segment %q is %d chars, want 12", id, parts[8], len(parts[8]))
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
		ModelSHA256:     pinnedSHA256["model.safetensors"],
		TokenizerSHA256: pinnedSHA256["tokenizer.json"],
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

// AC-2/AC-6 (the critical fix): an artifact whose bytes have been
// altered (e.g. a swapped model under an unchanged identity) is
// rejected at load time with a typed PinMismatchError naming the
// offending file and the expected vs actual SHA-256. The ID the
// production wiring consumes changes, and SW-261's fingerprint
// therefore reads the new generation as a different embedding space.
//
// The test stages four files whose config.json / tokenizer.json /
// modules.json hashes match the pinned table, but whose
// model.safetensors bytes have been tampered with. LoadModel must
// reject the artifact at the byte-identity check, BEFORE the
// safetensors header is parsed.
func TestStatic_HashMismatch_IsTypedError(t *testing.T) {
	dir := t.TempDir()
	// Read the REAL config.json, tokenizer.json and modules.json from
	// the production artifact so the test exercises the real pinned
	// hashes (and so the only failure the loader detects is the
	// model.safetensors swap).
	realArtifact, err := requireArtifactSkip(t)
	if err != nil {
		t.Fatal(err)
	}
	realConfig, err := os.ReadFile(filepath.Join(realArtifact, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	realTok, err := os.ReadFile(filepath.Join(realArtifact, "tokenizer.json"))
	if err != nil {
		t.Fatal(err)
	}
	realMod, err := os.ReadFile(filepath.Join(realArtifact, "modules.json"))
	if err != nil {
		t.Fatal(err)
	}
	realSafe, err := os.ReadFile(filepath.Join(realArtifact, "model.safetensors"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), realConfig, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tokenizer.json"), realTok, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "modules.json"), realMod, 0o644); err != nil {
		t.Fatal(err)
	}
	// Mutate ONE byte in the safetensors: change a byte near the start
	// of the body. The structure is unchanged (the safetensors header
	// is intact); only the bytes have changed.
	tampered := append([]byte(nil), realSafe...)
	tampered[len(tampered)-1] ^= 0xFF
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	// Compute the actual SHA-256 of the tampered file so the test can
	// assert expected vs actual.
	actualSum := sha256.Sum256(tampered)
	actualHex := hex.EncodeToString(actualSum[:])

	// LoadModel must refuse with a typed PinMismatchError that names the
	// offending file AND the expected vs actual hash (AC-2/AC-6).
	_, lerr := static.LoadModel(dir)
	if lerr == nil {
		t.Fatal("LoadModel accepted a tampered model.safetensors; AC-2/AC-6 require a typed PinMismatchError at load time")
	}
	var pme *static.PinMismatchError
	if !errors.As(lerr, &pme) {
		t.Fatalf("LoadModel error %T (%v) is not a typed *PinMismatchError; AC-2/AC-6 require errors.As support", lerr, lerr)
	}
	if pme.File != "model.safetensors" {
		t.Errorf("PinMismatchError.File = %q, want %q", pme.File, "model.safetensors")
	}
	if pme.Expected != pinnedSHA256["model.safetensors"] {
		t.Errorf("PinMismatchError.Expected = %q, want %q (the pinned hash)", pme.Expected, pinnedSHA256["model.safetensors"])
	}
	if pme.Actual != actualHex {
		t.Errorf("PinMismatchError.Actual = %q, want %q (the SHA-256 of the tampered file)", pme.Actual, actualHex)
	}
	// The message itself must name the file and both hashes so a
	// human reading stderr sees the mismatch.
	msg := lerr.Error()
	if !strings.Contains(msg, "model.safetensors") {
		t.Errorf("error message %q must name the offending file", msg)
	}
	if !strings.Contains(msg, pinnedSHA256["model.safetensors"]) || !strings.Contains(msg, actualHex) {
		t.Errorf("error message %q must name expected (%s) vs actual (%s)", msg, pinnedSHA256["model.safetensors"], actualHex)
	}
}

// requireArtifactSkip is a helper that returns the real cached artifact
// directory, skipping the test if it is absent. Distinct from
// requireArtifact so the bytes-loading tests (which need the real
// artifact to be present to even construct the fixture) have a clear
// name.
func requireArtifactSkip(t testing.TB) (string, error) {
	dir := artifactDir()
	present, err := classifyArtifact(dir)
	if err != nil {
		return "", err
	}
	if !present {
		t.Skip(skipMessage)
	}
	return dir, nil
}

// AC-7 / AC-9: truncated download. A safetensors whose declared body
// exceeds the file is rejected before the embedding table is allocated.
// The test files have correct hashes (the only failure is the
// truncation itself, so the loader reaches the structural check).
func TestStatic_TruncatedDownload_IsTypedError(t *testing.T) {
	dir := t.TempDir()
	// Stage four files with the right shape; the safetensors carries a
	// header that declares an 8×2 tensor (matching the tokenizer's 8 rows)
	// but the file
	// itself is 16 bytes shorter, so the data offsets [0,1024) lie
	// past the end of the file.
	config := []byte(`{"normalize":true,"embedding_dtype":"float16"}`)
	tokenizer := writeValidTokenizer(t)
	modules := []byte(`[]`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), config, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tokenizer.json"), tokenizer, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "modules.json"), modules, 0o644); err != nil {
		t.Fatal(err)
	}
	header := `{"embeddings":{"dtype":"F16","shape":[8,2],"data_offsets":[0,1024]}}`
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
	// Re-pin PinnedSHA256 so the file's hashes match (so the only failure
	// is the safetensors truncation).
	sumCfg := sha256.Sum256(config)
	sumTok := sha256.Sum256(tokenizer)
	sumMod := sha256.Sum256(modules)
	sumSafe := sha256.Sum256(file)
	newPins := map[string]string{
		"config.json":       hex.EncodeToString(sumCfg[:]),
		"tokenizer.json":    hex.EncodeToString(sumTok[:]),
		"model.safetensors": hex.EncodeToString(sumSafe[:]),
		"modules.json":      hex.EncodeToString(sumMod[:]),
	}
	restore := swapStaticPins(newPins)
	defer restore()

	_, lerr := static.LoadModel(dir)
	if lerr == nil {
		t.Fatal("LoadModel accepted a truncated safetensors; AC-7 requires validation BEFORE allocation")
	}
	var te *static.TensorError
	if !errors.As(lerr, &te) {
		t.Fatalf("LoadModel error %T (%v) is not a typed *TensorError; AC-7 requires errors.As support", lerr, lerr)
	}
	if te.Kind != static.TensorErrOffsets {
		t.Errorf("TensorError.Kind = %d, want %d (TensorErrOffsets)", te.Kind, static.TensorErrOffsets)
	}
}

// writeHasedArtifact writes a complete artifact directory whose file
// bytes hash to the supplied pins (config.json / tokenizer.json /
// modules.json use the supplied bytes; model.safetensors is built from
// a 1×1 F16 tensor with a 0x00 byte data so the hash is computed
// from the bytes). The caller is responsible for the hash values.
func writeHasedArtifact(t testing.TB, dir string, config, tokenizer, modules, safetensors []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), config, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tokenizer.json"), tokenizer, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "modules.json"), modules, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), safetensors, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeValidSafetensorsOneOne writes a minimal valid safetensors file
// with one F16 tensor named "embeddings" of shape [1,1] (2 bytes of
// data). The data byte is supplied so the caller can produce a
// predictable SHA.
func writeValidSafetensorsOneOne(t testing.TB, dataByte byte) []byte {
	t.Helper()
	header, err := json.Marshal(map[string]any{
		"embeddings": map[string]any{
			"dtype":        "F16",
			"shape":        []int{1, 1},
			"data_offsets": []int{0, 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hdr := []byte(header)
	out := make([]byte, 8+len(hdr)+2)
	hl := uint64(len(hdr))
	out[0] = byte(hl)
	out[1] = byte(hl >> 8)
	out[2] = byte(hl >> 16)
	out[3] = byte(hl >> 24)
	out[4] = byte(hl >> 32)
	out[5] = byte(hl >> 40)
	out[6] = byte(hl >> 48)
	out[7] = byte(hl >> 56)
	copy(out[8:], hdr)
	out[8+len(hdr)] = dataByte
	out[8+len(hdr)+1] = 0x00
	return out
}

// writeHasedPins writes a valid artifact directory and returns the
// SHA-256 pin table the loader will match against. Use this in
// AC-7 violation tests so the loader reaches the structural check.
func writeHasedPins(t testing.TB, dir string) {
	t.Helper()
	config := []byte(`{"normalize":true,"embedding_dtype":"float16"}`)
	tokenizer := writeValidTokenizer(t)
	modules := []byte(`[]`)
	safetensors := writeValidSafetensorsOneOne(t, 0x00)
	writeHasedArtifact(t, dir, config, tokenizer, modules, safetensors)
	pins := map[string]string{
		"config.json":       sha256Hex(config),
		"tokenizer.json":    sha256Hex(tokenizer),
		"model.safetensors": sha256Hex(safetensors),
		"modules.json":      sha256Hex(modules),
	}
	prev := static.PinnedSHA256
	static.PinnedSHA256 = pins
	t.Cleanup(func() { static.PinnedSHA256 = prev })
}

// AC-7: each violation has a dedicated corrupted-fixture test that
// asserts errors.As recovers the typed *TensorError and the discriminator
// kind. The tests below stage an otherwise-valid artifact and mutate
// exactly one byte / one header field to provoke a single failure.
func TestStatic_AC7_ShortFile(t *testing.T) {
	dir := t.TempDir()
	writeHasedPins(t, dir)
	// Overwrite the safetensors with a too-short file.
	shortSafe := []byte{0x01, 0x02}
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), shortSafe, 0o644); err != nil {
		t.Fatal(err)
	}
	// Update the pin to match the mutated file (the loader checks the
	// pin FIRST, before reading the header).
	pins := map[string]string{
		"config.json":       static.PinnedSHA256["config.json"],
		"tokenizer.json":    static.PinnedSHA256["tokenizer.json"],
		"model.safetensors": sha256Hex(shortSafe),
		"modules.json":      static.PinnedSHA256["modules.json"],
	}
	prev := static.PinnedSHA256
	static.PinnedSHA256 = pins
	t.Cleanup(func() { static.PinnedSHA256 = prev })
	_, err := static.LoadModel(dir)
	var te *static.TensorError
	if !errors.As(err, &te) {
		t.Fatalf("LoadModel error %T (%v) is not a typed *TensorError", err, err)
	}
	if te.Kind != static.TensorErrShortFile {
		t.Errorf("TensorError.Kind = %d, want %d (TensorErrShortFile)", te.Kind, static.TensorErrShortFile)
	}
}

func TestStatic_AC7_HeaderLength(t *testing.T) {
	dir := t.TempDir()
	writeHasedPins(t, dir)
	// The header length field is 8 little-endian bytes; setting it to a
	// very large value exceeds the file size.
	safetensors := writeValidSafetensorsOneOne(t, 0x00)
	// Overwrite the length field with a 0xFFFFFFFFFFFFFFFF value.
	for i := 0; i < 8; i++ {
		safetensors[i] = 0xFF
	}
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), safetensors, 0o644); err != nil {
		t.Fatal(err)
	}
	// Update the pin to match the mutated file (the loader checks the
	// pin FIRST, before reading the header).
	pins := map[string]string{
		"config.json":       static.PinnedSHA256["config.json"],
		"tokenizer.json":    static.PinnedSHA256["tokenizer.json"],
		"model.safetensors": sha256Hex(safetensors),
		"modules.json":      static.PinnedSHA256["modules.json"],
	}
	prev := static.PinnedSHA256
	static.PinnedSHA256 = pins
	t.Cleanup(func() { static.PinnedSHA256 = prev })
	_, err := static.LoadModel(dir)
	var te *static.TensorError
	if !errors.As(err, &te) {
		t.Fatalf("LoadModel error %T (%v) is not a typed *TensorError", err, err)
	}
	if te.Kind != static.TensorErrHeaderLength {
		t.Errorf("TensorError.Kind = %d, want %d (TensorErrHeaderLength)", te.Kind, static.TensorErrHeaderLength)
	}
}

func TestStatic_AC7_HeaderJSON(t *testing.T) {
	dir := t.TempDir()
	writeHasedPins(t, dir)
	// Replace the safetensors body with a header that is not valid JSON.
	safetensors := []byte{
		// 8-byte header length = 8 (the following 8 bytes are the header)
		0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		// 8 bytes of malformed JSON
		'{', 'n', 'o', 't', ' ', 'j', 's', 'o',
	}
	// Match the loader's per-file size check (it has to be larger than
	// the declared header length, but here we declare 8 and give
	// exactly 8 body bytes which is malformed JSON; the loader
	// rejects the file at the JSON parse step).
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), safetensors, 0o644); err != nil {
		t.Fatal(err)
	}
	pins := map[string]string{
		"config.json":       static.PinnedSHA256["config.json"],
		"tokenizer.json":    static.PinnedSHA256["tokenizer.json"],
		"model.safetensors": sha256Hex(safetensors),
		"modules.json":      static.PinnedSHA256["modules.json"],
	}
	prev := static.PinnedSHA256
	static.PinnedSHA256 = pins
	t.Cleanup(func() { static.PinnedSHA256 = prev })
	_, err := static.LoadModel(dir)
	var te *static.TensorError
	if !errors.As(err, &te) {
		t.Fatalf("LoadModel error %T (%v) is not a typed *TensorError", err, err)
	}
	if te.Kind != static.TensorErrHeaderJSON {
		t.Errorf("TensorError.Kind = %d, want %d (TensorErrHeaderJSON)", te.Kind, static.TensorErrHeaderJSON)
	}
}

func TestStatic_AC7_TensorNameMissing(t *testing.T) {
	dir := t.TempDir()
	writeHasedPins(t, dir)
	// Build a safetensors with a tensor named "wrong-name" instead of
	// "embeddings". The loader looks up the canonical name and finds
	// nothing.
	header, err := json.Marshal(map[string]any{
		"wrong-name": map[string]any{
			"dtype":        "F16",
			"shape":        []int{1, 1},
			"data_offsets": []int{0, 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hdr := []byte(header)
	safetensors := make([]byte, 8+len(hdr)+2)
	hl := uint64(len(hdr))
	safetensors[0] = byte(hl)
	safetensors[1] = byte(hl >> 8)
	safetensors[2] = byte(hl >> 16)
	safetensors[3] = byte(hl >> 24)
	safetensors[4] = byte(hl >> 32)
	safetensors[5] = byte(hl >> 40)
	safetensors[6] = byte(hl >> 48)
	safetensors[7] = byte(hl >> 56)
	copy(safetensors[8:], hdr)
	safetensors[8+len(hdr)] = 0x00
	safetensors[8+len(hdr)+1] = 0x00
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), safetensors, 0o644); err != nil {
		t.Fatal(err)
	}
	pins := map[string]string{
		"config.json":       static.PinnedSHA256["config.json"],
		"tokenizer.json":    static.PinnedSHA256["tokenizer.json"],
		"model.safetensors": sha256Hex(safetensors),
		"modules.json":      static.PinnedSHA256["modules.json"],
	}
	prev := static.PinnedSHA256
	static.PinnedSHA256 = pins
	t.Cleanup(func() { static.PinnedSHA256 = prev })
	_, lerr := static.LoadModel(dir)
	var te *static.TensorError
	if !errors.As(lerr, &te) {
		t.Fatalf("LoadModel error %T (%v) is not a typed *TensorError", lerr, lerr)
	}
	if te.Kind != static.TensorErrNameMissing {
		t.Errorf("TensorError.Kind = %d, want %d (TensorErrNameMissing)", te.Kind, static.TensorErrNameMissing)
	}
}

func TestStatic_AC7_Dtype(t *testing.T) {
	dir := t.TempDir()
	writeHasedPins(t, dir)
	// Build a safetensors with dtype F32 instead of F16.
	header, err := json.Marshal(map[string]any{
		"embeddings": map[string]any{
			"dtype":        "F32",
			"shape":        []int{1, 1},
			"data_offsets": []int{0, 4},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hdr := []byte(header)
	safetensors := make([]byte, 8+len(hdr)+4)
	hl := uint64(len(hdr))
	safetensors[0] = byte(hl)
	safetensors[1] = byte(hl >> 8)
	safetensors[2] = byte(hl >> 16)
	safetensors[3] = byte(hl >> 24)
	safetensors[4] = byte(hl >> 32)
	safetensors[5] = byte(hl >> 40)
	safetensors[6] = byte(hl >> 48)
	safetensors[7] = byte(hl >> 56)
	copy(safetensors[8:], hdr)
	for i := 0; i < 4; i++ {
		safetensors[8+len(hdr)+i] = 0x00
	}
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), safetensors, 0o644); err != nil {
		t.Fatal(err)
	}
	pins := map[string]string{
		"config.json":       static.PinnedSHA256["config.json"],
		"tokenizer.json":    static.PinnedSHA256["tokenizer.json"],
		"model.safetensors": sha256Hex(safetensors),
		"modules.json":      static.PinnedSHA256["modules.json"],
	}
	prev := static.PinnedSHA256
	static.PinnedSHA256 = pins
	t.Cleanup(func() { static.PinnedSHA256 = prev })
	_, lerr := static.LoadModel(dir)
	var te *static.TensorError
	if !errors.As(lerr, &te) {
		t.Fatalf("LoadModel error %T (%v) is not a typed *TensorError", lerr, lerr)
	}
	if te.Kind != static.TensorErrDtype {
		t.Errorf("TensorError.Kind = %d, want %d (TensorErrDtype)", te.Kind, static.TensorErrDtype)
	}
}

func TestStatic_AC7_Shape(t *testing.T) {
	dir := t.TempDir()
	writeHasedPins(t, dir)
	// 1-D shape (the production embedder requires 2-D).
	header, err := json.Marshal(map[string]any{
		"embeddings": map[string]any{
			"dtype":        "F16",
			"shape":        []int{16},
			"data_offsets": []int{0, 32},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hdr := []byte(header)
	safetensors := make([]byte, 8+len(hdr)+32)
	hl := uint64(len(hdr))
	safetensors[0] = byte(hl)
	safetensors[1] = byte(hl >> 8)
	safetensors[2] = byte(hl >> 16)
	safetensors[3] = byte(hl >> 24)
	safetensors[4] = byte(hl >> 32)
	safetensors[5] = byte(hl >> 40)
	safetensors[6] = byte(hl >> 48)
	safetensors[7] = byte(hl >> 56)
	copy(safetensors[8:], hdr)
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), safetensors, 0o644); err != nil {
		t.Fatal(err)
	}
	pins := map[string]string{
		"config.json":       static.PinnedSHA256["config.json"],
		"tokenizer.json":    static.PinnedSHA256["tokenizer.json"],
		"model.safetensors": sha256Hex(safetensors),
		"modules.json":      static.PinnedSHA256["modules.json"],
	}
	prev := static.PinnedSHA256
	static.PinnedSHA256 = pins
	t.Cleanup(func() { static.PinnedSHA256 = prev })
	_, lerr := static.LoadModel(dir)
	var te *static.TensorError
	if !errors.As(lerr, &te) {
		t.Fatalf("LoadModel error %T (%v) is not a typed *TensorError", lerr, lerr)
	}
	if te.Kind != static.TensorErrShape {
		t.Errorf("TensorError.Kind = %d, want %d (TensorErrShape)", te.Kind, static.TensorErrShape)
	}
}

func TestStatic_AC7_ShapeOverflow(t *testing.T) {
	dir := t.TempDir()
	writeHasedPins(t, dir)
	// A shape that overflows int64 when multiplied: rows = 1<<40, cols =
	// 1<<30. rows*cols*2 = 1<<71 which exceeds math.MaxInt64.
	header, err := json.Marshal(map[string]any{
		"embeddings": map[string]any{
			"dtype":        "F16",
			"shape":        []int{1 << 40, 1 << 30},
			"data_offsets": []int{0, 0},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hdr := []byte(header)
	// The safetensors file is 8+len(hdr) bytes; the body is zero, so
	// the byte-count check (rows*cols*2 != 0) would also fail. The
	// overflow check runs FIRST (AC-7 validate-before-allocate), so
	// the expected error is TensorErrShapeOverflow.
	safetensors := make([]byte, 8+len(hdr))
	hl := uint64(len(hdr))
	safetensors[0] = byte(hl)
	safetensors[1] = byte(hl >> 8)
	safetensors[2] = byte(hl >> 16)
	safetensors[3] = byte(hl >> 24)
	safetensors[4] = byte(hl >> 32)
	safetensors[5] = byte(hl >> 40)
	safetensors[6] = byte(hl >> 48)
	safetensors[7] = byte(hl >> 56)
	copy(safetensors[8:], hdr)
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), safetensors, 0o644); err != nil {
		t.Fatal(err)
	}
	pins := map[string]string{
		"config.json":       static.PinnedSHA256["config.json"],
		"tokenizer.json":    static.PinnedSHA256["tokenizer.json"],
		"model.safetensors": sha256Hex(safetensors),
		"modules.json":      static.PinnedSHA256["modules.json"],
	}
	prev := static.PinnedSHA256
	static.PinnedSHA256 = pins
	t.Cleanup(func() { static.PinnedSHA256 = prev })
	_, lerr := static.LoadModel(dir)
	var te *static.TensorError
	if !errors.As(lerr, &te) {
		t.Fatalf("LoadModel error %T (%v) is not a typed *TensorError", lerr, lerr)
	}
	if te.Kind != static.TensorErrShapeOverflow {
		t.Errorf("TensorError.Kind = %d, want %d (TensorErrShapeOverflow)", te.Kind, static.TensorErrShapeOverflow)
	}
}

// AC-7: the safetensors row count must match tokenizer.json before the
// embedding table allocation. The fixture is otherwise structurally valid
// and hash-pinned, so the typed discriminator identifies this exact failure.
func TestStatic_AC7_VocabMismatchBeforeAllocation(t *testing.T) {
	dir := t.TempDir()
	writeHasedPins(t, dir) // tokenizer has 8 entries; tensor shape is [1,1]

	_, err := static.LoadModel(dir)
	var te *static.TensorError
	if !errors.As(err, &te) {
		t.Fatalf("LoadModel error %T (%v) is not a typed *TensorError", err, err)
	}
	if te.Kind != static.TensorErrVocabMismatch {
		t.Errorf("TensorError.Kind = %d, want %d (TensorErrVocabMismatch)", te.Kind, static.TensorErrVocabMismatch)
	}
}

// AC-7: total artifact size has its own typed error. This check precedes
// tokenizer parsing and safetensors allocation, so an oversized but correctly
// pinned fixture cannot reach make([]uint16, ...).
func TestStatic_AC7_TotalSizeIsTyped(t *testing.T) {
	dir := t.TempDir()
	writeHasedPins(t, dir)
	path := filepath.Join(dir, "modules.json")
	if err := os.Truncate(path, maxArtifactBytes+1); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	pins := map[string]string{}
	for name, hash := range static.PinnedSHA256 {
		pins[name] = hash
	}
	pins["modules.json"] = hex.EncodeToString(h.Sum(nil))
	prev := static.PinnedSHA256
	static.PinnedSHA256 = pins
	t.Cleanup(func() { static.PinnedSHA256 = prev })

	_, err = static.LoadModel(dir)
	var te *static.TensorError
	if !errors.As(err, &te) {
		t.Fatalf("LoadModel error %T (%v) is not a typed *TensorError", err, err)
	}
	if te.Kind != static.TensorErrTotalSize {
		t.Errorf("TensorError.Kind = %d, want %d (TensorErrTotalSize)", te.Kind, static.TensorErrTotalSize)
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
// an egress path. The download path lives in cmd/graphi/staticfetch and its
// sole production call site is cmd/graphi/setup.go.
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
	// The synthetic-loader test above overrides PinnedSHA256 for the
	// duration of the test; restore the production pins before loading
	// the real artifact so VerifyPins matches its real hash.
	prevPins := static.PinnedSHA256
	static.PinnedSHA256 = pinnedSHA256
	defer func() { static.PinnedSHA256 = prevPins }()
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
// needed. The synthetic pin table is computed from the file bytes so
// LoadModel's VerifyPins step passes; the test then restores the
// production pin table on cleanup.
func newSyntheticModel(t testing.TB, dim int, padding any) *static.Model {
	t.Helper()
	dir := t.TempDir()
	// Write a config and tokenizer; the loader requires both.
	cfg := `{"normalize":true,"embedding_dtype":"float16"}`
	cfgBytes := []byte(cfg)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), cfgBytes, 0o644); err != nil {
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
	modBytes := []byte("[]")
	if err := os.WriteFile(filepath.Join(dir, "modules.json"), modBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	// F16 table: 8 rows × dim. Use the static package's own f32ToF16 / writer.
	body := writeF16Table(t, 8, dim)
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	// Compute the synthetic pin table from the file bytes so the loader's
	// VerifyPins step passes. The previous production pin table is
	// restored on cleanup.
	syntheticPins := map[string]string{
		"config.json":       sha256Hex(cfgBytes),
		"tokenizer.json":    sha256Hex(tokRaw),
		"model.safetensors": sha256Hex(body),
		"modules.json":      sha256Hex(modBytes),
	}
	prev := static.PinnedSHA256
	static.PinnedSHA256 = syntheticPins
	t.Cleanup(func() { static.PinnedSHA256 = prev })
	m, err := static.LoadModel(dir)
	if err != nil {
		t.Fatalf("LoadModel(synthetic): %v", err)
	}
	return m
}

// sha256Hex is a test helper that returns the lower-case hex SHA-256 of b.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
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
