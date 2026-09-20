# CodeRank Product Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Promote the already-qualified CodeRank adapter into an explicitly selected, fail-closed product profile whose queries, builds, publications, surfaces, and evaluation all run through one public selection path.

**Architecture:** Add an immutable `Selection` value and a context-aware constructor at the `engine/embed` seam, register CodeRank as an explicitly-selected-only scheme, give the adapter a poisonable session, replace live-index mutation during a build with a private staging index plus a fail-closed snapshot publication barrier, move the attested query boundary into `engine/search`, make `engine/retrieval` propagate semantic hard errors instead of degrading, and re-point evaluation at the public selector so product parity and product-path requalification measure the shipped code.

**Tech Stack:** Go 1.26.6 (`CGO_ENABLED=0` default build), Go standard library (`net/http`, `encoding/json`, `crypto/sha256`, `sync/atomic`), SQLite generation store, existing `engine/embed` / `engine/search` / `engine/retrieval` / `surfaces` / `internal/eval/retrieval` packages.

**Spec:** `docs/superpowers/specs/2026-09-17-coderank-product-integration-design.md`

## Global Constraints

Every task's requirements implicitly include this section. Values are copied verbatim from the spec.

- Implementation is gated on the frozen four-arm development qualification reporting `DEVELOPMENT PROMOTION: YES`. Do not begin Task 1 before that decision exists.
- The standard GrapHi binary remains buildable with `CGO_ENABLED=0`.
- An empty embedder selector constructs, activates, downloads, and contacts no embedder.
- GrapHi never performs automatic external model access.
- Potion remains the fast daemonless standard profile offered by `setup-embedder`.
- CodeRank is optional, local, CPU-qualified, and accessed only through an operator-managed loopback sidecar.
- GrapHi ships neither CodeRank weights nor Python, PyTorch, Transformers, or SentenceTransformers runtimes.
- GrapHi does not install, launch, update, restart, or supervise the CodeRank sidecar.
- Model, revision, tokenizer, runtime, precision, normalization, dimension, admission, and query instruction remain reproducibly pinned.
- The selector is exactly `GRAPHI_EMBEDDER=coderank:/absolute/path/to/coderank.json`. The manifest path must be absolute and canonical; a relative path is rejected. Endpoint, model name, and credentials are never separate selector parameters.
- The primary serialized bundle ceiling remains exactly 1,200 `cl100k_base` tokens.
- The minimum CodeRank development result remains 56/64, not 60/64.
- The spent holdout remains `47/64`, `RELEASE: NO`; it is never reused for selection, tuning, or authorization.
- No release threshold, stratum gate, run-validity gate, or operating budget may be waived after evidence is opened.
- The endpoint and the process epoch are excluded from the durable fingerprint. The process epoch never enters the carry-forward comparison.
- A persisted vector may be carried forward only when `active generation state == StateReady AND stored fingerprint == requested fingerprint byte-for-byte AND admitted document text_hash is unchanged`. No subset comparison is sufficient.
- Under `coderank_required`, no manifest, construction, availability, attestation, generation, or query failure may be translated into an empty registry, Potion selection, or successful lexical-only retrieval.
- Attestation errors expose the failing phase and a repair action, never expected or observed digests or raw epochs. Raw process epochs are never emitted on any surface.
- The canonical semantic-status document remains byte-identical across `graphi semantic status --json`, MCP `semantic_status`, and `GET /semantic/status`.
- Evaluation constructs CodeRank through the public selection path only; no evaluation-only adapter is retained.

### Build and test commands

Run one command per invocation (compound `;`/`&&` shell commands are blocked in this environment):

- `go build ./...`
- `go vet ./...`
- `CGO_ENABLED=0 go build ./cmd/graphi`
- `go test ./engine/embed/...`
- `go test ./engine/search/...`
- `go test ./engine/retrieval/...`
- `go test ./cmd/... ./surfaces/...`
- `go test ./internal/eval/retrieval/...`

---

## Scope Check

The spec covers two separable subsystems. This plan implements the first and stops at its boundary, exactly as `2026-09-16-embedded-model-dev-qualification.md` stopped at the promotion boundary.

**In this plan (Tasks 1–14):** product boundary and explicit selection, durable identity and vector reuse, runtime attestation and process epoch, atomic generation publication, runtime state machine and retrieval semantics, component boundaries, product surfaces, the evaluation path, product parity, product-path development requalification, boundary conformance, and operator documentation.

**Deferred to a follow-up plan — "CodeRank candidate freeze and independent holdout":** the spec's *Candidate freeze and release procedure*. It requires a newly curated sealed 64-query holdout dataset with a preregistered stratum distribution, a one-shot evaluation, and the compound `RELEASE: YES` rule. That work is gated on this plan's Task 13 reporting `PRODUCT REQUALIFICATION: YES`, produces no product code, and curating a new holdout is an independent deliverable. Splitting it keeps this plan's output working, testable software on its own.

---

## File Structure

### Engine embed seam

- Create `engine/embed/cause.go`: the eight engine-owned stable error causes, the typed `CauseError` carrying phase and repair, and digest/epoch-free rendering.
- Create `engine/embed/cause_test.go`: cause round-trip, `errors.As` unwrap, and a redaction test proving no 64-hex digest or epoch reaches `Error()`.
- Create `engine/embed/selection.go`: `SelectionMode`, the immutable `Selection` value, the context-aware `SchemeConstructorContext` table, and `ResolveSelection`.
- Create `engine/embed/selection_test.go`: unconfigured / potion / coderank_required resolution, required-scheme hard failure, and unknown-scheme graceful skip.
- Create `engine/embed/session.go`: the optional `SessionReporter` capability and the three session-state constants.
- Create `engine/embed/snapshot.go`: the immutable `Snapshot`, the `SnapshotPublisher` / `SnapshotSource` seams, and `SnapshotHolder`'s atomic swap and invalidation.
- Create `engine/embed/snapshot_test.go`: immutability, capture-once semantics, invalidation cause, and publish validation.
- Modify `engine/embed/defaults.go`: add `RegisterSchemeContext` / `ConstructorContext` alongside the existing table without changing the empty-selector graceful skip.
- Modify `engine/embed/generate.go`: build into a private staging index, add `GenerateAndPublish`, harden the carry-forward comparison, and run the publication barrier.
- Modify `engine/embed/generate_test.go`: pin the private-staging and barrier behaviour.
- Create `engine/embed/carryforward_test.go`: exact-equality carry-forward tests that reject model-only, dimension-only, and partial-profile matches.
- Modify `engine/embed/status.go`: carry selection, session, durable fingerprint, and the exact repair action.
- Modify `engine/embed/status_test.go`: cover the new fields across states.

### CodeRank adapter

- Create `engine/embed/coderank/scheme.go`: the `coderank` scheme registration and the absolute-canonical manifest-path policy.
- Create `engine/embed/coderank/scheme_test.go`: relative, non-canonical, symlinked, and empty-path rejection; registration-does-not-activate.
- Create `engine/embed/coderank/session.go`: the `unbound` / `bound` / `poisoned` state machine and terminal poisoning.
- Create `engine/embed/coderank/session_test.go`: concurrency-safe transitions and terminality.
- Modify `engine/embed/coderank/embedder.go`: gate every entry point on the session, poison on any binding mismatch, and map failures to engine causes.
- Modify `engine/embed/coderank/embedder_test.go`: fake-sidecar injection at each documented switch point.
- Create `engine/embed/coderank/identity_test.go`: the durable-field mutation matrix plus endpoint/epoch exclusion.

### Search and retrieval

- Create `engine/search/attested.go`: the safe semantic operation — snapshot capture, state and fingerprint validation, session check, attested query embedding, response validation, vector search.
- Create `engine/search/attested_test.go`: each hard-failure condition under a required profile.
- Modify `engine/search/service.go`: accept the resolved `Selection` and the snapshot source.
- Modify `engine/search/semantic.go`: route through the attested boundary and fail closed when required.
- Modify `engine/retrieval/service.go`: expose whether the profile is required and surface the semantic cause.
- Modify `engine/retrieval/retrieval.go`: propagate a semantic hard error instead of degrading to lexical.
- Modify `engine/retrieval/semantic_first_test.go`: prove no Potion or lexical-only result is published under a required profile.

### Surfaces and composition

- Modify `cmd/internal/runtime/runtime.go`: resolve the selection once and build the search service, generation build, and snapshot publisher from it.
- Modify `cmd/graphi/semantic.go`: replace the env-reading registry helper with the selection helper.
- Modify `cmd/graphi/serve.go`, `cmd/graphi/zeroconfig.go`, `cmd/graphi/query.go`: pass the resolved selection to MCP, HTTP, and the index path.
- Modify `cmd/graphi/main.go`: import the coderank package so the scheme is available but never default.
- Modify `surfaces/client/semantic_status.go`: carry the new status fields into the canonical document.
- Modify `surfaces/mcp/mcp.go`, `surfaces/http/server.go`: accept the selection alongside the registry.
- Modify `cmd/graphi/semantic_surfaces_test.go`: extend the three-surface goldens.

### Evaluation

- Modify `internal/eval/retrieval/model_qualification.go`: construct the CodeRank arm through the public selector.
- Modify `internal/eval/retrieval/model_qualification_measure.go`: obtain the operating attestation from the selected embedder.
- Modify `internal/eval/retrieval/taskcontext.go`: validate capture state, fingerprint, session, and degradation.
- Create `internal/eval/retrieval/product_parity.go`: the byte-parity comparison between the qualified adapter path and the public product path.
- Create `internal/eval/retrieval/product_parity_test.go`: parity over the frozen development inputs.
- Modify `internal/eval/retrieval/model_qualification_stats.go` and `model_qualification_report.go`: add the product-path requalification decision and report block.

### Conformance and documentation

- Modify `internal/canary/gate.go`: restate the CodeRank exemption as product-path loopback IPC.
- Create `engine/embed/coderank/product_boundary_test.go`: CGo-free, no-artifact, no-launch, registered-but-inactive proofs.
- Modify `docs/semantic-search.md`: the CodeRank profile, operator lifecycle, change matrix, and failure vocabulary.

---

## Task 1: Engine-owned error-cause vocabulary

**Files:**
- Create: `engine/embed/cause.go`
- Create: `engine/embed/cause_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks (this is the leaf).
- Produces: `embed.Cause` (string type) with the constants `CauseProfileInvalid`, `CauseRuntimeUnavailable`, `CauseAttestationMismatch`, `CauseGenerationMissing`, `CauseGenerationStale`, `CauseGenerationCorrupt`, `CauseSnapshotInconsistent`, `CausePublishFailed`; `func NewCauseError(cause Cause, phase, repair, detail string, err error) *CauseError`; `func (*CauseError) Error() string`; `func (*CauseError) Unwrap() error`; `func (*CauseError) Repair() string`; `func CauseOf(err error) (Cause, bool)`.

- [ ] **Step 1: Write the failing test**

Create `engine/embed/cause_test.go`:

```go
package embed

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

func TestCauseError_CarriesCausePhaseAndRepair(t *testing.T) {
	inner := errors.New("dial tcp 127.0.0.1:9999: connection refused")
	err := NewCauseError(CauseRuntimeUnavailable, "adapter construction", "start the CodeRank sidecar, then rerun", "sidecar is not reachable", inner)

	got, ok := CauseOf(err)
	if !ok || got != CauseRuntimeUnavailable {
		t.Fatalf("CauseOf = (%q, %v), want (%q, true)", got, ok, CauseRuntimeUnavailable)
	}
	if err.Repair() != "start the CodeRank sidecar, then rerun" {
		t.Fatalf("Repair = %q", err.Repair())
	}
	if !errors.Is(err, inner) {
		t.Fatalf("errors.Is(err, inner) = false, want true")
	}
	if !strings.Contains(err.Error(), "coderank_runtime_unavailable") {
		t.Fatalf("Error = %q, want the cause name", err.Error())
	}
	if !strings.Contains(err.Error(), "adapter construction") {
		t.Fatalf("Error = %q, want the failing phase", err.Error())
	}
}

func TestCauseError_NeverRendersDigestsOrEpochs(t *testing.T) {
	digest := strings.Repeat("a", 64)
	epoch := "pid-4242-boot-1789000000"
	inner := &RuntimeAttestationError{
		Phase:    "before query embedding",
		Expected: RuntimeAttestation{IdentityDigest: digest, Epoch: epoch},
		Observed: RuntimeAttestation{IdentityDigest: strings.Repeat("b", 64), Epoch: "pid-9999-boot-1789000001"},
		Reason:   "identity digest or process epoch changed",
	}
	err := NewCauseError(CauseAttestationMismatch, "before query embedding", "restart graphi after the sidecar restart", "runtime binding changed", inner)

	rendered := err.Error()
	if regexp.MustCompile(`[0-9a-f]{64}`).MatchString(rendered) {
		t.Fatalf("Error leaked a digest: %q", rendered)
	}
	if strings.Contains(rendered, epoch) || strings.Contains(rendered, "pid-") {
		t.Fatalf("Error leaked an epoch: %q", rendered)
	}
}

func TestCauseOf_UnknownErrorReportsNoCause(t *testing.T) {
	if got, ok := CauseOf(errors.New("plain")); ok {
		t.Fatalf("CauseOf = (%q, true), want (\"\", false)", got)
	}
	if got, ok := CauseOf(nil); ok {
		t.Fatalf("CauseOf(nil) = (%q, true), want (\"\", false)", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./engine/embed/ -run TestCause -v`
Expected: FAIL — `undefined: NewCauseError`, `undefined: CauseRuntimeUnavailable`, `undefined: CauseOf`.

- [ ] **Step 3: Write minimal implementation**

Create `engine/embed/cause.go`:

```go
package embed

import (
	"errors"
	"fmt"
)

// Cause is the engine-owned, stable vocabulary of semantic failure causes.
// CLI, MCP and HTTP may map a cause to a surface-appropriate transport code
// and rendering, but they may not change its meaning or invent a new one.
type Cause string

const (
	// CauseProfileInvalid: the CodeRank selector or manifest is unusable —
	// a relative or non-canonical path, an unreadable file, a schema or
	// protocol mismatch, or a pin that fails validation.
	CauseProfileInvalid Cause = "coderank_profile_invalid"
	// CauseRuntimeUnavailable: the manifest is valid but the loopback
	// sidecar could not be reached or answered outside the protocol.
	CauseRuntimeUnavailable Cause = "coderank_runtime_unavailable"
	// CauseAttestationMismatch: protocol, identity digest or process epoch
	// did not match the expected runtime binding, or the adapter session
	// is poisoned.
	CauseAttestationMismatch Cause = "coderank_attestation_mismatch"
	// CauseGenerationMissing: no active semantic generation exists.
	CauseGenerationMissing Cause = "semantic_generation_missing"
	// CauseGenerationStale: an active generation exists under a different
	// durable fingerprint.
	CauseGenerationStale Cause = "semantic_generation_stale"
	// CauseGenerationCorrupt: an active generation exists under the right
	// fingerprint but failed validation.
	CauseGenerationCorrupt Cause = "semantic_generation_corrupt"
	// CauseSnapshotInconsistent: the durable generation committed but the
	// live snapshot does not match it, so the process serves neither the
	// old index under the new generation nor a lexical fallback.
	CauseSnapshotInconsistent Cause = "semantic_snapshot_inconsistent"
	// CausePublishFailed: the live snapshot swap itself failed.
	CausePublishFailed Cause = "semantic_generation_publish_failed"
)

// CauseError is the typed carrier for a Cause. It renders the cause, the
// failing phase and a sanitized detail only: expected and observed identity
// digests, raw process epochs and endpoints never reach Error(). The wrapped
// error remains reachable through Unwrap for errors.Is/As, but callers that
// render to an operator render THIS value, not the wrapped one.
type CauseError struct {
	// Cause is the stable vocabulary entry.
	Cause Cause
	// Phase names the operation that failed ("adapter construction",
	// "before query embedding", "before generation commit", ...).
	Phase string
	// Detail is a short, already-sanitized explanation. It must not contain
	// a digest, an epoch, or an endpoint.
	Detail string
	// repair is the exact operator action that leaves this state.
	repair string
	// err is the wrapped error, reachable through Unwrap but never rendered.
	err error
}

// NewCauseError builds a CauseError. detail must already be sanitized.
func NewCauseError(cause Cause, phase, repair, detail string, err error) *CauseError {
	return &CauseError{Cause: cause, Phase: phase, Detail: detail, repair: repair, err: err}
}

// Error renders cause, phase and detail — nothing else.
func (e *CauseError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("graphi: %s at %s", string(e.Cause), e.Phase)
	}
	return fmt.Sprintf("graphi: %s at %s: %s", string(e.Cause), e.Phase, e.Detail)
}

// Unwrap exposes the wrapped error for errors.Is/As without rendering it.
func (e *CauseError) Unwrap() error { return e.err }

// Repair returns the exact operator action, satisfying the Repairable
// interface engine/search and engine/embed/status already consume.
func (e *CauseError) Repair() string { return e.repair }

// CauseOf reports the Cause carried by err, if any.
func CauseOf(err error) (Cause, bool) {
	if err == nil {
		return "", false
	}
	var ce *CauseError
	if errors.As(err, &ce) {
		return ce.Cause, true
	}
	return "", false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./engine/embed/ -run TestCause -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add engine/embed/cause.go engine/embed/cause_test.go
git commit -m "feat(embed): add the engine-owned semantic error-cause vocabulary"
```

---

## Task 2: Context-aware constructor and the immutable Selection value

**Files:**
- Create: `engine/embed/selection.go`
- Create: `engine/embed/selection_test.go`
- Modify: `engine/embed/defaults.go`

**Interfaces:**
- Consumes: `embed.Cause`, `embed.NewCauseError`, `embed.CauseOf` (Task 1).
- Produces: `type SchemeConstructorContext func(ctx context.Context, arg string) (Embedder, error)`; `func RegisterSchemeContext(scheme string, make SchemeConstructorContext)`; `func DefaultContextConstructors() map[string]SchemeConstructorContext`; `func ConstructorContext(ctx context.Context, selector string, ctor map[string]SchemeConstructorContext) (Embedder, error)`; `type SelectionMode string` with `SelectionUnconfigured`, `SelectionPotion`, `SelectionCodeRankRequired`; `type Selection struct{...}` with methods `Mode() SelectionMode`, `Profile() string`, `Explicit() bool`, `Required() bool`, `Embedder() (Embedder, bool)`, `Registry() *Registry`; `func ResolveSelection(ctx context.Context, selector string, ctor map[string]SchemeConstructorContext) (Selection, error)`; `const SchemeCodeRank = "coderank"`.

- [ ] **Step 1: Write the failing test**

Create `engine/embed/selection_test.go`:

```go
package embed

import (
	"context"
	"errors"
	"testing"
)

type selTestEmbedder struct{ id string }

func (e selTestEmbedder) ID() string  { return e.id }
func (e selTestEmbedder) Dim() int    { return 3 }
func (e selTestEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	return [][]float32{}, nil
}

func selTable() map[string]SchemeConstructorContext {
	return map[string]SchemeConstructorContext{
		"static": func(context.Context, string) (Embedder, error) {
			return selTestEmbedder{id: "static:potion-code-16M-v2@rev"}, nil
		},
		SchemeCodeRank: func(_ context.Context, arg string) (Embedder, error) {
			if arg != "/abs/coderank.json" {
				return nil, NewCauseError(CauseProfileInvalid, "adapter construction", "export GRAPHI_EMBEDDER=coderank:/absolute/path/to/coderank.json", "manifest path must be absolute and canonical", nil)
			}
			return selTestEmbedder{id: "coderank:model@rev:digest"}, nil
		},
	}
}

func TestResolveSelection_EmptySelectorIsUnconfigured(t *testing.T) {
	sel, err := ResolveSelection(context.Background(), "", selTable())
	if err != nil {
		t.Fatalf("ResolveSelection: %v", err)
	}
	if sel.Mode() != SelectionUnconfigured || sel.Explicit() || sel.Required() {
		t.Fatalf("mode=%q explicit=%v required=%v", sel.Mode(), sel.Explicit(), sel.Required())
	}
	if _, ok := sel.Embedder(); ok {
		t.Fatalf("empty selector constructed an embedder")
	}
	if sel.Registry().Configured() {
		t.Fatalf("empty selector produced a configured registry")
	}
}

func TestResolveSelection_UnknownSchemeStaysGracefulSkip(t *testing.T) {
	sel, err := ResolveSelection(context.Background(), "nosuchscheme:arg", selTable())
	if err != nil {
		t.Fatalf("ResolveSelection: %v", err)
	}
	if sel.Mode() != SelectionUnconfigured {
		t.Fatalf("mode = %q, want unconfigured", sel.Mode())
	}
}

func TestResolveSelection_StaticIsPotionAndNotRequired(t *testing.T) {
	sel, err := ResolveSelection(context.Background(), "static:potion-code-16M-v2@rev", selTable())
	if err != nil {
		t.Fatalf("ResolveSelection: %v", err)
	}
	if sel.Mode() != SelectionPotion || sel.Profile() != "potion" {
		t.Fatalf("mode=%q profile=%q", sel.Mode(), sel.Profile())
	}
	if !sel.Explicit() || sel.Required() {
		t.Fatalf("explicit=%v required=%v, want true/false", sel.Explicit(), sel.Required())
	}
	if !sel.Registry().Configured() {
		t.Fatalf("potion selection produced an unconfigured registry")
	}
}

func TestResolveSelection_CodeRankIsRequired(t *testing.T) {
	sel, err := ResolveSelection(context.Background(), "coderank:/abs/coderank.json", selTable())
	if err != nil {
		t.Fatalf("ResolveSelection: %v", err)
	}
	if sel.Mode() != SelectionCodeRankRequired || sel.Profile() != "coderank" {
		t.Fatalf("mode=%q profile=%q", sel.Mode(), sel.Profile())
	}
	if !sel.Explicit() || !sel.Required() {
		t.Fatalf("explicit=%v required=%v, want true/true", sel.Explicit(), sel.Required())
	}
}

func TestResolveSelection_CodeRankFailureIsHardNotGracefulSkip(t *testing.T) {
	sel, err := ResolveSelection(context.Background(), "coderank:relative/coderank.json", selTable())
	if err == nil {
		t.Fatalf("ResolveSelection returned no error; selection=%q", sel.Mode())
	}
	cause, ok := CauseOf(err)
	if !ok || cause != CauseProfileInvalid {
		t.Fatalf("CauseOf = (%q, %v), want (%q, true)", cause, ok, CauseProfileInvalid)
	}
	if sel.Mode() != SelectionUnconfigured {
		t.Fatalf("a failed required selection must not be reported as configured; got %q", sel.Mode())
	}
	if _, ok := sel.Embedder(); ok {
		t.Fatalf("a failed required selection exposed an embedder")
	}
}

func TestResolveSelection_NonRequiredConstructorFailureIsAlsoAnError(t *testing.T) {
	boom := errors.New("non-loopback ollama host")
	table := map[string]SchemeConstructorContext{
		"ollama": func(context.Context, string) (Embedder, error) { return nil, boom },
	}
	if _, err := ResolveSelection(context.Background(), "ollama:example.com", table); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the constructor error", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./engine/embed/ -run TestResolveSelection -v`
Expected: FAIL — `undefined: ResolveSelection`, `undefined: SchemeConstructorContext`, `undefined: SchemeCodeRank`.

- [ ] **Step 3: Write minimal implementation**

Create `engine/embed/selection.go`:

```go
package embed

import (
	"context"
	"strings"
	"sync"
)

// SchemeCodeRank is the selector scheme for the loopback-sidecar-backed
// CodeRank profile. It is the only scheme whose selection is REQUIRED: once
// recognized, no failure may be translated into a graceful skip.
const SchemeCodeRank = "coderank"

// requiredSchemes names the schemes whose selection is fail-closed.
var requiredSchemes = map[string]bool{SchemeCodeRank: true}

// SchemeConstructorContext is the context-aware form of SchemeConstructor.
// A constructor that contacts a local sidecar during construction (CodeRank
// reads its initial attestation) needs a cancellable, bounded context; the
// legacy SchemeConstructor cannot express that.
type SchemeConstructorContext func(ctx context.Context, arg string) (Embedder, error)

var (
	ctxMu           sync.Mutex
	ctxConstructors = map[string]SchemeConstructorContext{}
)

// RegisterSchemeContext adds a context-aware constructor for a selector
// scheme. Registering an empty scheme or a nil constructor is a no-op.
// Registration never activates an embedder: the constructor table is the
// opt-in seam, and the default selector is empty.
func RegisterSchemeContext(scheme string, make SchemeConstructorContext) {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	if scheme == "" || make == nil {
		return
	}
	ctxMu.Lock()
	ctxConstructors[scheme] = make
	ctxMu.Unlock()
}

// DefaultContextConstructors returns the merged constructor table: every
// context-aware scheme, plus every legacy SchemeConstructor adapted to
// ignore the context. A context-aware registration wins on collision.
func DefaultContextConstructors() map[string]SchemeConstructorContext {
	m := map[string]SchemeConstructorContext{}
	for scheme, make := range DefaultConstructors() {
		legacy := make
		m[scheme] = func(_ context.Context, arg string) (Embedder, error) { return legacy(arg) }
	}
	ctxMu.Lock()
	for scheme, make := range ctxConstructors {
		m[scheme] = make
	}
	ctxMu.Unlock()
	return m
}

// ConstructorContext is Constructor with a context. An empty selector and an
// unknown scheme both remain graceful skips — (nil, nil) — so the CGo-free,
// embedderless default is untouched. A recognized scheme's constructor error
// is returned verbatim; the caller decides whether it is fail-closed.
func ConstructorContext(ctx context.Context, selector string, ctor map[string]SchemeConstructorContext) (Embedder, error) {
	scheme, arg := splitSelector(selector)
	if scheme == "" {
		return nil, nil
	}
	make, ok := ctor[scheme]
	if !ok || make == nil {
		return nil, nil
	}
	return make(ctx, arg)
}

// SelectionMode is the closed vocabulary of resolved profile selections.
type SelectionMode string

const (
	// SelectionUnconfigured: no embedder was selected. Current lexical
	// behaviour remains available; nothing is constructed or contacted.
	SelectionUnconfigured SelectionMode = "unconfigured"
	// SelectionPotion: the existing daemonless opt-in profile was selected.
	SelectionPotion SelectionMode = "potion"
	// SelectionCodeRankRequired: CodeRank was explicitly selected and every
	// semantic operation is fail-closed.
	SelectionCodeRankRequired SelectionMode = "coderank_required"
)

// Selection is the immutable resolution of GRAPHI_EMBEDDER. The composition
// root resolves it ONCE and passes this value to CLI, MCP, HTTP, daemon and
// status wiring; deeper modules never reread the environment and never
// choose an embedder of their own.
type Selection struct {
	mode     SelectionMode
	profile  string
	explicit bool
	embedder Embedder
}

// Mode reports the resolved selection mode.
func (s Selection) Mode() SelectionMode {
	if s.mode == "" {
		return SelectionUnconfigured
	}
	return s.mode
}

// Profile is the human-facing profile name ("", "potion", "coderank").
func (s Selection) Profile() string { return s.profile }

// Explicit reports whether the operator named a recognized scheme.
func (s Selection) Explicit() bool { return s.explicit }

// Required reports whether semantic operations are fail-closed.
func (s Selection) Required() bool { return s.mode == SelectionCodeRankRequired }

// Embedder returns the constructed embedder, or (nil, false) when none.
func (s Selection) Embedder() (Embedder, bool) {
	if s.embedder == nil {
		return nil, false
	}
	return s.embedder, true
}

// Registry returns a FROZEN registry holding the selected embedder, or the
// frozen empty graceful-skip registry when none was selected. It is the one
// place a Selection becomes the registry the existing seams consume, so a
// caller cannot wire the selection correctly and the registry wrongly.
func (s Selection) Registry() *Registry {
	r := NewRegistry()
	if s.embedder != nil {
		_ = r.Register(s.embedder)
	}
	r.Freeze()
	return r
}

// ResolveSelection resolves a selector string into an immutable Selection.
//
//   - empty or unknown scheme  -> unconfigured, no error, nothing constructed;
//   - a recognized scheme      -> the constructed embedder and its mode;
//   - a constructor error      -> the zero Selection AND the error. For a
//     required scheme this is the fail-closed contract: the caller must not
//     fall back to an empty registry, to Potion, or to lexical-only.
func ResolveSelection(ctx context.Context, selector string, ctor map[string]SchemeConstructorContext) (Selection, error) {
	scheme, _ := splitSelector(selector)
	emb, err := ConstructorContext(ctx, selector, ctor)
	if err != nil {
		return Selection{}, err
	}
	if emb == nil {
		return Selection{mode: SelectionUnconfigured}, nil
	}
	if requiredSchemes[scheme] {
		return Selection{mode: SelectionCodeRankRequired, profile: scheme, explicit: true, embedder: emb}, nil
	}
	return Selection{mode: SelectionPotion, profile: profileForScheme(scheme), explicit: true, embedder: emb}, nil
}

// profileForScheme names the human-facing profile a non-required scheme
// serves. `static` is the pinned Potion profile `setup-embedder` offers.
func profileForScheme(scheme string) string {
	if scheme == "static" {
		return "potion"
	}
	return scheme
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./engine/embed/ -run TestResolveSelection -v`
Expected: PASS (6 tests).

- [ ] **Step 5: Verify the existing embed suite is unchanged**

Run: `go test ./engine/embed/`
Expected: PASS — the legacy `Constructor` / `RegisterScheme` path is untouched.

- [ ] **Step 6: Commit**

```bash
git add engine/embed/selection.go engine/embed/selection_test.go
git commit -m "feat(embed): add the context-aware constructor and the immutable Selection value"
```

---

## Task 3: CodeRank scheme registration and the absolute-canonical manifest policy

**Files:**
- Create: `engine/embed/coderank/scheme.go`
- Create: `engine/embed/coderank/scheme_test.go`
- Modify: `cmd/graphi/main.go`

**Interfaces:**
- Consumes: `embed.SchemeCodeRank`, `embed.RegisterSchemeContext`, `embed.NewCauseError`, `embed.CauseProfileInvalid`, `embed.CauseRuntimeUnavailable` (Tasks 1–2); `coderank.NewFromManifest(ctx, path)` (existing).
- Produces: `const coderank.Scheme = "coderank"`; `func coderank.ValidateManifestPath(path string) error`; `func coderank.RepairAction() string`; the `init` that registers the scheme.

- [ ] **Step 1: Write the failing test**

Create `engine/embed/coderank/scheme_test.go`:

```go
package coderank

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/embed"
)

func TestValidateManifestPath_RejectsNonAbsoluteAndNonCanonical(t *testing.T) {
	cases := []struct{ name, path string }{
		{"empty", ""},
		{"relative", "coderank.json"},
		{"dot relative", "./coderank.json"},
		{"parent traversal", "/etc/../tmp/coderank.json"},
		{"trailing slash segment", "/tmp//coderank.json"},
		{"dot segment", "/tmp/./coderank.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateManifestPath(tc.path)
			if err == nil {
				t.Fatalf("ValidateManifestPath(%q) = nil, want a profile-invalid error", tc.path)
			}
			cause, ok := embed.CauseOf(err)
			if !ok || cause != embed.CauseProfileInvalid {
				t.Fatalf("CauseOf = (%q, %v), want (%q, true)", cause, ok, embed.CauseProfileInvalid)
			}
		})
	}
}

func TestValidateManifestPath_AcceptsAbsoluteCanonical(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coderank.json")
	if err := ValidateManifestPath(path); err != nil {
		t.Fatalf("ValidateManifestPath(%q) = %v, want nil", path, err)
	}
}

func TestScheme_IsRegisteredButNeverActive(t *testing.T) {
	table := embed.DefaultContextConstructors()
	if _, ok := table[Scheme]; !ok {
		t.Fatalf("scheme %q is not registered in the context constructor table", Scheme)
	}
	sel, err := embed.ResolveSelection(context.Background(), "", table)
	if err != nil {
		t.Fatalf("ResolveSelection(empty): %v", err)
	}
	if sel.Mode() != embed.SelectionUnconfigured {
		t.Fatalf("an empty selector activated %q", sel.Mode())
	}
	if _, ok := sel.Embedder(); ok {
		t.Fatalf("an empty selector constructed an embedder")
	}
}

func TestScheme_RelativeSelectorFailsClosedWithoutDialing(t *testing.T) {
	table := embed.DefaultContextConstructors()
	sel, err := embed.ResolveSelection(context.Background(), "coderank:relative/coderank.json", table)
	if err == nil {
		t.Fatalf("relative selector resolved to %q with no error", sel.Mode())
	}
	cause, ok := embed.CauseOf(err)
	if !ok || cause != embed.CauseProfileInvalid {
		t.Fatalf("CauseOf = (%q, %v), want (%q, true)", cause, ok, embed.CauseProfileInvalid)
	}
	if sel.Mode() != embed.SelectionUnconfigured {
		t.Fatalf("failed selection reported mode %q", sel.Mode())
	}
	var repairable interface{ Repair() string }
	if !strings.Contains(err.Error(), "coderank_profile_invalid") {
		t.Fatalf("error %q does not name the cause", err.Error())
	}
	if ce, ok := err.(interface{ Repair() string }); ok {
		repairable = ce
	}
	if repairable == nil || repairable.Repair() == "" {
		t.Fatalf("error carries no repair action")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./engine/embed/coderank/ -run TestScheme -v`
Expected: FAIL — `undefined: ValidateManifestPath`, `undefined: Scheme`.

- [ ] **Step 3: Write minimal implementation**

Create `engine/embed/coderank/scheme.go`:

```go
package coderank

import (
	"context"
	"path/filepath"

	"github.com/samibel/graphi/engine/embed"
)

// Scheme is the selector scheme for the CodeRank profile. The ONLY accepted
// selector form is:
//
//	GRAPHI_EMBEDDER=coderank:/absolute/path/to/coderank.json
//
// Endpoint, model name and credentials are never separate selector
// parameters: the strict manifest is the single CodeRank configuration
// source.
const Scheme = embed.SchemeCodeRank

// RepairAction is the exact operator action for an invalid CodeRank profile.
// It never names a digest, an epoch or an endpoint.
func RepairAction() string {
	return "export GRAPHI_EMBEDDER=coderank:/absolute/path/to/coderank.json and start the pinned sidecar on loopback"
}

// ValidateManifestPath enforces the selector's path policy BEFORE anything is
// read or dialed: the path must be absolute and already canonical, so
// selection cannot change with the working directory and cannot be smuggled
// through `..` segments. It performs no filesystem access.
func ValidateManifestPath(path string) error {
	fail := func(detail string) error {
		return embed.NewCauseError(embed.CauseProfileInvalid, "selector resolution", RepairAction(), detail, nil)
	}
	if path == "" {
		return fail("manifest path is empty")
	}
	if !filepath.IsAbs(path) {
		return fail("manifest path must be absolute")
	}
	if filepath.Clean(path) != path {
		return fail("manifest path must be canonical")
	}
	return nil
}

// init registers the CodeRank scheme on the CONTEXT-AWARE constructor table
// so the initial sidecar attestation is cancellable and bounded. Registration
// makes the scheme AVAILABLE; it never makes it active. The default selector
// is empty, so nothing is constructed, downloaded or contacted by default,
// and CodeRank is never registered as a default embedder.
func init() {
	embed.RegisterSchemeContext(Scheme, func(ctx context.Context, arg string) (embed.Embedder, error) {
		if err := ValidateManifestPath(arg); err != nil {
			return nil, err
		}
		e, err := NewFromManifest(ctx, arg)
		if err != nil {
			return nil, classifyConstructionError(err)
		}
		return e, nil
	})
}

// classifyConstructionError maps an adapter construction failure onto the
// engine's stable cause vocabulary. An attestation failure is a runtime
// binding problem; everything else at construction time is a profile problem.
func classifyConstructionError(err error) error {
	if _, ok := err.(*embed.RuntimeAttestationError); ok {
		return embed.NewCauseError(embed.CauseAttestationMismatch, "adapter construction", RepairAction(),
			"the sidecar runtime does not match the pinned manifest identity", err)
	}
	if isTransportError(err) {
		return embed.NewCauseError(embed.CauseRuntimeUnavailable, "adapter construction", RepairAction(),
			"the pinned loopback sidecar did not answer the attestation request", err)
	}
	return embed.NewCauseError(embed.CauseProfileInvalid, "adapter construction", RepairAction(),
		"the CodeRank manifest could not be loaded or validated", err)
}
```

- [ ] **Step 4: Add the transport-error discriminator**

`classifyConstructionError` in Step 3 already calls `isTransportError`, so the file does not compile until this step lands. Apply Steps 3 and 4 as one edit before running Step 5.

Append to `engine/embed/coderank/scheme.go`:

```go
// isTransportError reports whether err came from contacting the sidecar
// rather than from reading or validating the manifest. The adapter's request
// helper wraps every transport failure with the "coderank: request " prefix
// and every non-200 with "coderank: <path> returned HTTP ", so the
// discriminator is a property of this package's own error strings, not of a
// third-party message.
func isTransportError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "coderank: request ") ||
		strings.Contains(msg, " returned HTTP ") ||
		strings.Contains(msg, "coderank: decode ")
}
```

Add `"strings"` to the file's import block.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./engine/embed/coderank/ -run TestScheme -v`
Expected: PASS (4 tests, including the table subtests).

- [ ] **Step 6: Close the Go/Python manifest-validation asymmetry**

`Manifest.Validate()` (`manifest.go:93-103`) checks only NON-EMPTINESS for
`runtime.name`, `admission.algorithm` and `admission.algorithm_version`, while
the reference sidecar (`scripts/eval/coderank_sidecar.py:101-104`) pins their
exact values. In evaluation the Python side catches a drifted manifest. In
PRODUCT there is no Python validator in the loop at all — GrapHi ships only the
Go client, and the sidecar is operator-managed — so the lax Go check is the only
gate GrapHi applies. An operator could point a product GrapHi at a manifest
declaring `admission.algorithm: "sliding-window"` and the adapter would
construct happily, serving an UNQUALIFIED profile. The durable fingerprint would
differ, so no vectors are silently mixed, but the spec requires that admission
and runtime stay "reproducibly pinned" and that any profile change take new
development evidence first. Fail closed instead.

Add to `Validate()` in `engine/embed/coderank/manifest.go`, directly after the
existing dimension/precision/normalization/compute check:

```go
	// The pinned profile is the one the qualification measured. These three
	// fields are checked for EXACT equality, not merely non-emptiness: the
	// reference sidecar pins them too, and in product there is no sidecar-side
	// validator in GrapHi's own trust path, so this is the only gate.
	if m.Runtime.Name != PinnedRuntimeName {
		return errors.New("coderank: profile requires the pinned sentence-transformers runtime")
	}
	if m.Admission.Algorithm != PinnedAdmissionAlgorithm || m.Admission.AlgorithmVersion != PinnedAdmissionAlgorithmVersion {
		return errors.New("coderank: profile requires the pinned first-n-tokens admission algorithm at version 1")
	}
```

with the constants declared next to `QueryInstruction`:

```go
// The pinned profile values. They are mirrored by the reference sidecar's
// validate_manifest; the two MUST agree, and a test pins that they do.
const (
	PinnedRuntimeName               = "sentence-transformers"
	PinnedAdmissionAlgorithm        = "first-n-tokens"
	PinnedAdmissionAlgorithmVersion = "1"
)
```

Add to `engine/embed/coderank/manifest_test.go` a table test asserting each of
the three mutations is now REJECTED, and a test that reads the three pinned
literals out of `scripts/eval/coderank_sidecar.py` and asserts they equal the Go
constants, so the two validators cannot drift apart again:

```go
func TestManifestPins_GoAndReferenceSidecarAgree(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "scripts", "eval", "coderank_sidecar.py"))
	if err != nil {
		t.Fatalf("read reference sidecar: %v", err)
	}
	for _, pin := range []string{PinnedRuntimeName, PinnedAdmissionAlgorithm, PinnedAdmissionAlgorithmVersion} {
		if !strings.Contains(string(src), `"`+pin+`"`) {
			t.Fatalf("reference sidecar does not pin %q; the two validators have drifted", pin)
		}
	}
}
```

Run: `go test ./engine/embed/coderank/ -run TestManifest -v`
Expected: PASS, with the three mutation subtests now rejecting.

- [ ] **Step 7: Wire the scheme into the product composition root**

In `cmd/graphi/main.go`, add the blank import directly under the existing ollama import:

```go
	_ "github.com/samibel/graphi/engine/embed/coderank" // opt-in loopback CodeRank profile: registers the "coderank" scheme; never a default, never constructed without an explicit absolute-manifest selector
	_ "github.com/samibel/graphi/engine/embed/ollama"   // opt-in loopback embedder: registers the "ollama" scheme; never constructed on the default path
```

- [ ] **Step 8: Prove the default binary is still CGo-free**

Run: `CGO_ENABLED=0 go build ./cmd/graphi`
Expected: builds with no error.

- [ ] **Step 9: Run the full affected suites**

Run: `go test ./engine/embed/... ./cmd/graphi/ ./internal/canary/`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add engine/embed/coderank/scheme.go engine/embed/coderank/scheme_test.go engine/embed/coderank/manifest.go engine/embed/coderank/manifest_test.go cmd/graphi/main.go
git commit -m "feat(coderank): register the explicitly selected absolute-manifest coderank scheme"
```

---

## Task 4: Adapter session lifecycle and terminal poisoning

**Files:**
- Create: `engine/embed/session.go`
- Create: `engine/embed/coderank/session.go`
- Create: `engine/embed/coderank/session_test.go`
- Modify: `engine/embed/coderank/embedder.go`

**Interfaces:**
- Consumes: `embed.RuntimeAttestationError` (existing), `embed.NewCauseError`, `embed.CauseAttestationMismatch` (Task 1), `coderank.RepairAction` (Task 3).
- Produces: `const embed.SessionUnbound = "unbound"`, `embed.SessionBound = "bound"`, `embed.SessionPoisoned = "poisoned"`; `type embed.SessionReporter interface { SessionState() string }`; `func (*coderank.Embedder) SessionState() string`; unexported `func (*coderank.Embedder) guard(phase string) error` and `func (*coderank.Embedder) poison(phase string, err error) error`.

- [ ] **Step 1: Write the failing test**

Create `engine/embed/coderank/session_test.go`:

```go
package coderank

import (
	"sync"
	"testing"

	"github.com/samibel/graphi/engine/embed"
)

func TestSession_StartsUnboundAndBindsOnce(t *testing.T) {
	var s session
	if got := s.state(); got != embed.SessionUnbound {
		t.Fatalf("state = %q, want %q", got, embed.SessionUnbound)
	}
	s.bind()
	if got := s.state(); got != embed.SessionBound {
		t.Fatalf("state = %q, want %q", got, embed.SessionBound)
	}
}

func TestSession_PoisoningIsTerminal(t *testing.T) {
	var s session
	s.bind()
	s.poison()
	if got := s.state(); got != embed.SessionPoisoned {
		t.Fatalf("state = %q, want %q", got, embed.SessionPoisoned)
	}
	s.bind() // a rebind attempt must NOT resurrect a poisoned session
	if got := s.state(); got != embed.SessionPoisoned {
		t.Fatalf("poisoned session rebound to %q", got)
	}
}

func TestSession_ConcurrentPoisonIsSafe(t *testing.T) {
	var s session
	s.bind()
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.poison()
			_ = s.state()
		}()
	}
	wg.Wait()
	if got := s.state(); got != embed.SessionPoisoned {
		t.Fatalf("state = %q, want %q", got, embed.SessionPoisoned)
	}
}

func TestEmbedder_GuardRefusesAPoisonedSession(t *testing.T) {
	e := &Embedder{}
	e.session.bind()
	e.session.poison()
	err := e.guard("before query embedding")
	if err == nil {
		t.Fatalf("guard on a poisoned session returned nil")
	}
	cause, ok := embed.CauseOf(err)
	if !ok || cause != embed.CauseAttestationMismatch {
		t.Fatalf("CauseOf = (%q, %v), want (%q, true)", cause, ok, embed.CauseAttestationMismatch)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./engine/embed/coderank/ -run TestSession -v`
Expected: FAIL — `undefined: session`, `e.session undefined`, `e.guard undefined`.

- [ ] **Step 3: Add the engine-side optional capability**

Create `engine/embed/session.go`:

```go
package embed

// Session states an attested adapter may report. They are ORTHOGONAL to the
// generation state and to the profile selection: an adapter can be bound
// while the generation is stale, and poisoned while the generation is ready.
const (
	// SessionUnbound: no expected runtime binding has been established.
	SessionUnbound = "unbound"
	// SessionBound: a validated (protocol, identity_digest, epoch) binding
	// is frozen for this adapter instance.
	SessionBound = "bound"
	// SessionPoisoned: a protocol, identity or epoch mismatch was observed.
	// Poisoning is TERMINAL for the adapter instance: no new admission,
	// embedding or publication operation begins, and the adapter never
	// silently binds itself to a replacement sidecar.
	SessionPoisoned = "poisoned"
)

// SessionReporter is the OPTIONAL capability an attested adapter exposes so
// the search boundary and the status surface can read its session state
// without knowing the adapter's type. An embedder that does not implement it
// is treated as having no session to check.
type SessionReporter interface {
	SessionState() string
}
```

- [ ] **Step 4: Implement the adapter session**

Create `engine/embed/coderank/session.go`:

```go
package coderank

import (
	"sync/atomic"

	"github.com/samibel/graphi/engine/embed"
)

// session is the adapter's threadsafe unbound/bound/poisoned state machine.
// Poisoning is terminal and one-way; bind never resurrects a poisoned
// session. The zero value is unbound.
type session struct {
	// v holds 0 (unbound), 1 (bound) or 2 (poisoned).
	v atomic.Int32
}

const (
	sessionUnbound int32 = 0
	sessionBound   int32 = 1
	sessionPoison  int32 = 2
)

// bind moves an unbound session to bound. It is a no-op once poisoned.
func (s *session) bind() { s.v.CompareAndSwap(sessionUnbound, sessionBound) }

// poison moves the session to the terminal poisoned state. Idempotent and
// safe under concurrency.
func (s *session) poison() { s.v.Store(sessionPoison) }

// state renders the engine-visible session state.
func (s *session) state() string {
	switch s.v.Load() {
	case sessionBound:
		return embed.SessionBound
	case sessionPoison:
		return embed.SessionPoisoned
	default:
		return embed.SessionUnbound
	}
}
```

- [ ] **Step 5: Gate every adapter entry point on the session**

In `engine/embed/coderank/embedder.go`, add the field to the struct:

```go
type Embedder struct {
	manifest Manifest
	client   *http.Client
	expected embed.RuntimeAttestation
	session  session
}
```

Add the capability assertion to the existing `var (...)` compile-time block:

```go
	_ embed.SessionReporter = (*Embedder)(nil)
```

Append these methods to the file:

```go
// SessionState implements embed.SessionReporter.
func (e *Embedder) SessionState() string { return e.session.state() }

// guard refuses to start a new operation on a poisoned session. It is the
// first statement of every operation that admits, embeds, attests or probes.
func (e *Embedder) guard(phase string) error {
	if e.session.state() == embed.SessionPoisoned {
		return embed.NewCauseError(embed.CauseAttestationMismatch, phase, RepairAction(),
			"the adapter session is poisoned and will not bind to a replacement sidecar", nil)
	}
	return nil
}

// poison atomically moves the session to the terminal poisoned state and
// returns the cause-carrying error for the failing operation. Every
// protocol, identity or epoch mismatch flows through here.
func (e *Embedder) poison(phase string, err error) error {
	e.session.poison()
	return embed.NewCauseError(embed.CauseAttestationMismatch, phase, RepairAction(),
		"the sidecar runtime binding changed during the operation", err)
}
```

- [ ] **Step 6: Bind on construction and poison on every mismatch**

In `newFromManifest`, after `e.expected = got`, add:

```go
	e.session.bind()
```

In `verifyBinding`, replace the body with:

```go
func (e *Embedder) verifyBinding(phase string, b responseBinding) error {
	got := embed.RuntimeAttestation{IdentityDigest: b.IdentityDigest, Epoch: b.Epoch}
	if b.Protocol != ProtocolVersion || got != e.expected {
		return e.poison(phase, &embed.RuntimeAttestationError{
			Phase: phase, Expected: e.expected, Observed: got, Reason: "response binding mismatch",
		})
	}
	return nil
}
```

In `fetchOperatingAttestation`, replace each of the two `return OperatingAttestation{}, &embed.RuntimeAttestationError{...}` statements with a poisoning return — but ONLY once a binding has been established, so construction's first fetch still fails without poisoning a session that was never bound:

```go
	if err := embed.ValidateRuntimeAttestation(got); err != nil {
		return OperatingAttestation{}, e.attestationFailure("attestation", want, got, err.Error())
	}
	if out.Protocol != ProtocolVersion || got.IdentityDigest != want.IdentityDigest || (want.Epoch != "" && got.Epoch != want.Epoch) || out.Dimension != e.manifest.Dimension {
		return OperatingAttestation{}, e.attestationFailure("attestation", want, got, "manifest or runtime binding mismatch")
	}
```

and add the helper:

```go
// attestationFailure renders an attestation mismatch. A session that is
// already bound is poisoned: the inconsistency is terminal for this adapter
// instance. A session that was never bound (the construction-time fetch)
// simply fails — no instance exists to poison yet, and a NEW adapter may
// legitimately bind a new epoch after revalidating the complete identity.
func (e *Embedder) attestationFailure(phase string, want, got embed.RuntimeAttestation, reason string) error {
	inner := &embed.RuntimeAttestationError{Phase: phase, Expected: want, Observed: got, Reason: reason}
	if e.session.state() == embed.SessionBound {
		return e.poison(phase, inner)
	}
	return inner
}
```

Add the guard as the first statement of `Admit`, `Embed`, `EmbedQueryWithDiagnostics`, `RuntimeAttestation`, `OperatingAttestation`, `ProbeDim` and `CheckAvailable`, for example:

```go
func (e *Embedder) Admit(ctx context.Context, text string) (embed.Admitted, error) {
	if err := e.guard("admission"); err != nil {
		return embed.Admitted{}, err
	}
	...
}
```

- [ ] **Step 7: Run test to verify it passes**

Run: `go test ./engine/embed/coderank/ -run TestSession -v`
Expected: PASS (4 tests).

- [ ] **Step 8: Write the fake-sidecar injection matrix**

Append to `engine/embed/coderank/embedder_test.go`. `switchAt` is a controllable fake sidecar that serves the pinned attestation until the named switch point, then serves a changed epoch. Reuse the existing `httptest` fixture helpers in that file (manifest builder, handler, `newTestEmbedder`) rather than writing new ones.

```go
// switchPoint names one place the sidecar may be swapped underneath an
// in-flight operation. Every one of them must fail the affected operation
// and poison the adapter.
type switchPoint string

const (
	switchBeforeQuery         switchPoint = "before_query"
	switchBetweenPreflight    switchPoint = "between_preflight_and_response"
	switchBeforeBuild         switchPoint = "before_build"
	switchDuringAdmission     switchPoint = "during_admission"
	switchBetweenDocuments    switchPoint = "between_document_embeddings"
	switchImmediatelyPreCommit switchPoint = "immediately_before_commit"
	switchAfterCommit         switchPoint = "after_commit_before_next_query"
	switchDuringConcurrency   switchPoint = "during_concurrent_queries"
)

func TestAttestation_EverySwitchPointFailsTheOperationAndPoisons(t *testing.T) {
	for _, point := range []switchPoint{
		switchBeforeQuery, switchBetweenPreflight, switchBeforeBuild,
		switchDuringAdmission, switchBetweenDocuments, switchImmediatelyPreCommit,
		switchAfterCommit, switchDuringConcurrency,
	} {
		t.Run(string(point), func(t *testing.T) {
			e, sidecar := newSwitchingEmbedder(t, point)
			if err := runOperationFor(t, e, sidecar, point); err == nil {
				t.Fatalf("switching the sidecar at %s did not fail the operation", point)
			}
			if got := e.SessionState(); got != embed.SessionPoisoned {
				t.Fatalf("session = %q after a switch at %s, want %q", got, point, embed.SessionPoisoned)
			}
			// Poisoning is terminal: a later call through the SAME instance
			// fails without silently rebinding, even after the sidecar has
			// settled on its new identity.
			sidecar.settle()
			if _, err := e.EmbedQuery(context.Background(), "anything"); err == nil {
				t.Fatalf("a poisoned adapter rebound to the replacement sidecar")
			}
		})
	}
}

func TestAttestation_ConcurrentQueriesSucceedOnlyWhileTheirOwnResponseIsBound(t *testing.T) {
	e, sidecar := newSwitchingEmbedder(t, switchDuringConcurrency)
	var wg sync.WaitGroup
	results := make([]error, 16)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = e.EmbedQuery(context.Background(), "q")
		}(i)
	}
	sidecar.switchNow()
	wg.Wait()
	sawFailure := false
	for _, err := range results {
		if err != nil {
			sawFailure = true
		}
	}
	if !sawFailure {
		t.Fatalf("a mid-flight sidecar switch produced no failing concurrent query")
	}
	if got := e.SessionState(); got != embed.SessionPoisoned {
		t.Fatalf("session = %q, want %q", got, embed.SessionPoisoned)
	}
}
```

Implement two helpers in the same file:

- `func newSwitchingEmbedder(t *testing.T, at switchPoint) (*Embedder, *switchingSidecar)` — starts an `httptest.Server` whose handler serves the pinned identity and epoch until `switchNow()` is called, then serves a different epoch. `settle()` stops switching further. The embedder is constructed through `newFromManifest` against that server.
- `func runOperationFor(t *testing.T, e *Embedder, s *switchingSidecar, at switchPoint) error` — drives the operation the switch point names: `EmbedQuery` for the query points, `Admit` for `switchDuringAdmission`, and `embed.GenerateAndPersist` over a two-node fixture for the build points, arranging `switchNow()` to fire at the right moment via the handler's per-request counter. Use `GenerateAndPersist` (which exists today), not `GenerateAndPublish`, so this task has no forward dependency on Task 6.

- [ ] **Step 9: Run the injection matrix**

Run: `go test ./engine/embed/coderank/ -run TestAttestation -v`
Expected: PASS — 8 subtests plus the concurrency test.

- [ ] **Step 10: Run the adapter contract suite**

Run: `go test ./engine/embed/coderank/`
Expected: PASS — the existing `httptest` contract tests still hold.

- [ ] **Step 11: Commit**

```bash
git add engine/embed/session.go engine/embed/coderank/session.go engine/embed/coderank/session_test.go engine/embed/coderank/embedder.go engine/embed/coderank/embedder_test.go
git commit -m "feat(coderank): make a runtime inconsistency terminally poison the adapter session"
```

---

## Task 5: Durable-identity mutation matrix and exact carry-forward

**Files:**
- Create: `engine/embed/coderank/identity_test.go`
- Create: `engine/embed/carryforward_test.go`
- Modify: `engine/embed/generate.go`

**Interfaces:**
- Consumes: `coderank.Manifest` and `Manifest.IdentityDigest()` (existing), `embed.Fingerprint.Canonical()` (existing), `embed.MemGenerationStore` (existing test adapter).
- Produces: no new exported API. `GenerateAndPersistWithProgress` gains an explicit `priorGen.Fingerprint.Canonical() == fp.Canonical()` guard before carry-forward.

- [ ] **Step 1: Write the failing durable-identity mutation test**

Create `engine/embed/coderank/identity_test.go`:

```go
package coderank

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// validManifest is the fully populated pinned profile the mutation matrix
// perturbs one field at a time. Digests are deliberately non-live fixtures.
func validManifest() Manifest {
	sum := sha256.Sum256([]byte(QueryInstruction))
	return Manifest{
		SchemaVersion: 1,
		Protocol:      ProtocolVersion,
		Endpoint:      "http://127.0.0.1:8731",
		Model:         ArtifactPin{ID: "nomic-ai/CodeRankEmbed", Revision: "immutable-model-revision", SHA256: strings.Repeat("a", 64)},
		Tokenizer:     ArtifactPin{ID: "nomic-ai/CodeRankEmbed", Revision: "immutable-tokenizer-revision", SHA256: strings.Repeat("b", 64)},
		Runtime:       RuntimePin{Name: "sentence-transformers", Version: "pinned-runtime", SHA256: strings.Repeat("d", 64)},
		Dimension:     768,
		Precision:     "float32",
		Normalization: "l2",
		Compute:       "cpu",
		Admission:     AdmissionPin{MaxTokens: 8192, Reserve: 0, Algorithm: "first-n-tokens", AlgorithmVersion: "1"},
		Query: QueryProfilePin{
			ID: "coderank-code-search-query", Version: "1",
			Instruction: QueryInstruction, InstructionSHA256: hex.EncodeToString(sum[:]),
		},
	}
}

func TestIdentityDigest_EveryDurableFieldChangesIt(t *testing.T) {
	base := validManifest()
	baseDigest := base.IdentityDigest()

	cases := []struct {
		name  string
		apply func(*Manifest)
	}{
		{"schema version", func(m *Manifest) { m.SchemaVersion = 2 }},
		{"protocol", func(m *Manifest) { m.Protocol = "graphi-coderank/4" }},
		{"model id", func(m *Manifest) { m.Model.ID = "other/Model" }},
		{"model revision", func(m *Manifest) { m.Model.Revision = "other-revision" }},
		{"model sha256", func(m *Manifest) { m.Model.SHA256 = strings.Repeat("c", 64) }},
		{"tokenizer id", func(m *Manifest) { m.Tokenizer.ID = "other/Tokenizer" }},
		{"tokenizer revision", func(m *Manifest) { m.Tokenizer.Revision = "other-revision" }},
		{"tokenizer sha256", func(m *Manifest) { m.Tokenizer.SHA256 = strings.Repeat("e", 64) }},
		{"runtime name", func(m *Manifest) { m.Runtime.Name = "other-runtime" }},
		{"runtime version", func(m *Manifest) { m.Runtime.Version = "other-version" }},
		{"runtime sha256", func(m *Manifest) { m.Runtime.SHA256 = strings.Repeat("f", 64) }},
		{"dimension", func(m *Manifest) { m.Dimension = 1024 }},
		{"precision", func(m *Manifest) { m.Precision = "float16" }},
		{"normalization", func(m *Manifest) { m.Normalization = "none" }},
		{"compute", func(m *Manifest) { m.Compute = "gpu" }},
		{"admission max tokens", func(m *Manifest) { m.Admission.MaxTokens = 512 }},
		{"admission reserve", func(m *Manifest) { m.Admission.Reserve = 2 }},
		{"admission algorithm", func(m *Manifest) { m.Admission.Algorithm = "middle-out" }},
		{"admission algorithm version", func(m *Manifest) { m.Admission.AlgorithmVersion = "2" }},
		{"query profile id", func(m *Manifest) { m.Query.ID = "other-profile" }},
		{"query profile version", func(m *Manifest) { m.Query.Version = "2" }},
		{"query instruction", func(m *Manifest) { m.Query.Instruction = "Other instruction: " }},
		{"query instruction sha256", func(m *Manifest) { m.Query.InstructionSHA256 = strings.Repeat("1", 64) }},
	}

	seen := map[string]string{baseDigest: "base"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validManifest()
			tc.apply(&m)
			got := m.IdentityDigest()
			if got == baseDigest {
				t.Fatalf("mutating %s did not change the identity digest", tc.name)
			}
			if prior, dup := seen[got]; dup {
				t.Fatalf("mutating %s collides with %s", tc.name, prior)
			}
			seen[got] = tc.name
		})
	}
}

func TestIdentityDigest_EndpointIsExcluded(t *testing.T) {
	base := validManifest()
	moved := validManifest()
	moved.Endpoint = "http://127.0.0.1:9999"
	if base.IdentityDigest() != moved.IdentityDigest() {
		t.Fatalf("changing only the loopback endpoint changed the durable identity")
	}
}

func TestChunkerConfig_IsEndpointFreeAndStable(t *testing.T) {
	a := &Embedder{manifest: validManifest()}
	b := &Embedder{manifest: validManifest()}
	b.manifest.Endpoint = "http://localhost:1234"
	if a.ChunkerConfig() != b.ChunkerConfig() {
		t.Fatalf("the durable profile is not endpoint-free")
	}
	if strings.Contains(a.ChunkerConfig(), "8731") {
		t.Fatalf("the durable profile leaked the endpoint: %s", a.ChunkerConfig())
	}
}

func TestExpectedAttestation_EpochIsNotInTheDurableProfile(t *testing.T) {
	e := &Embedder{manifest: validManifest(), expected: RuntimeAttestationFixture("pid-1-boot-1")}
	f := &Embedder{manifest: validManifest(), expected: RuntimeAttestationFixture("pid-2-boot-2")}
	if e.ChunkerConfig() != f.ChunkerConfig() {
		t.Fatalf("the process epoch entered the durable profile")
	}
}
```

Add the fixture helper to the same file:

```go
// RuntimeAttestationFixture builds a well-formed attestation whose epoch the
// caller varies, so a test can prove the epoch never reaches the durable
// profile.
func RuntimeAttestationFixture(epoch string) embed.RuntimeAttestation {
	return embed.RuntimeAttestation{IdentityDigest: strings.Repeat("9", 64), Epoch: epoch}
}
```

and add `"github.com/samibel/graphi/engine/embed"` to the import block.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./engine/embed/coderank/ -run TestIdentityDigest -v`
Expected: FAIL — `undefined: RuntimeAttestationFixture` on the first compile, then PASS for the digest subtests once the helper exists. Add the helper, rerun, and confirm every subtest passes. If a subtest fails, `IdentityDigest` is missing a durable field: add it to the `parts` slice in `manifest.go` in the documented order before continuing.

- [ ] **Step 3: Write the failing carry-forward test**

Create `engine/embed/carryforward_test.go`:

```go
package embed

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// countingEmbedder records how many texts it was asked to embed so a test
// can prove a carry-forward skipped the model entirely.
type countingEmbedder struct {
	id      string
	dim     int
	chunker string
	calls   int
}

func (e *countingEmbedder) ID() string            { return e.id }
func (e *countingEmbedder) Dim() int              { return e.dim }
func (e *countingEmbedder) ChunkerConfig() string { return e.chunker }
func (e *countingEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	e.calls += len(texts)
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = make([]float32, e.dim)
		out[i][0] = 1
	}
	return out, nil
}

func carryForwardRegistry(t *testing.T, e Embedder) *Registry {
	t.Helper()
	r := NewRegistry()
	if err := r.Register(e); err != nil {
		t.Fatalf("register: %v", err)
	}
	r.Freeze()
	return r
}

func TestCarryForward_RequiresExactFingerprintEquality(t *testing.T) {
	ctx := context.Background()
	nodes := []model.Node{model.NewNode("n1", "func", "pkg.Fn", "a.go", 1, 1)}
	docs := V2DocumentSource{}
	store := NewMemGenerationStore()

	first := &countingEmbedder{id: "coderank:m@r:digest", dim: 4, chunker: `{"model":"m","dim":4}`}
	if _, err := GenerateAndPersist(ctx, carryForwardRegistry(t, first), nodes, docs, NewIndex(), store, "gen-1"); err != nil {
		t.Fatalf("first build: %v", err)
	}
	if first.calls != 1 {
		t.Fatalf("first build embedded %d texts, want 1", first.calls)
	}

	// Identical durable identity: the row carries forward, no embed call.
	same := &countingEmbedder{id: "coderank:m@r:digest", dim: 4, chunker: `{"model":"m","dim":4}`}
	if _, err := GenerateAndPersist(ctx, carryForwardRegistry(t, same), nodes, docs, NewIndex(), store, "gen-1"); err != nil {
		t.Fatalf("carry-forward build: %v", err)
	}
	if same.calls != 0 {
		t.Fatalf("carry-forward build embedded %d texts, want 0", same.calls)
	}

	// Partial matches must NOT authorize reuse.
	partials := []struct {
		name string
		emb  *countingEmbedder
	}{
		{"model id only", &countingEmbedder{id: "coderank:m@r:digest", dim: 8, chunker: `{"model":"m","dim":4}`}},
		{"dimension only", &countingEmbedder{id: "coderank:other@r:digest", dim: 4, chunker: `{"model":"m","dim":4}`}},
		{"profile only", &countingEmbedder{id: "coderank:other@r:other", dim: 4, chunker: `{"model":"m","dim":4}`}},
	}
	for _, tc := range partials {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := GenerateAndPersist(ctx, carryForwardRegistry(t, tc.emb), nodes, docs, NewIndex(), store, "gen-1"); err != nil {
				t.Fatalf("build: %v", err)
			}
			if tc.emb.calls != 1 {
				t.Fatalf("%s reused a vector across a different fingerprint (embedded %d texts, want 1)", tc.name, tc.emb.calls)
			}
		})
	}
}

func TestCarryForward_RequiresUnchangedTextHash(t *testing.T) {
	ctx := context.Background()
	docs := V2DocumentSource{}
	store := NewMemGenerationStore()
	emb := &countingEmbedder{id: "coderank:m@r:digest", dim: 4, chunker: `{"model":"m"}`}

	before := []model.Node{model.NewNode("n1", "func", "pkg.Fn", "a.go", 1, 1)}
	if _, err := GenerateAndPersist(ctx, carryForwardRegistry(t, emb), before, docs, NewIndex(), store, "gen-1"); err != nil {
		t.Fatalf("first build: %v", err)
	}
	emb.calls = 0

	// Same node id, changed document text (the qualified name moved), so the
	// admitted document's text_hash changes and the row must be re-embedded.
	after := []model.Node{model.NewNode("n1", "func", "pkg.Renamed", "a.go", 1, 1)}
	if _, err := GenerateAndPersist(ctx, carryForwardRegistry(t, emb), after, docs, NewIndex(), store, "gen-1"); err != nil {
		t.Fatalf("second build: %v", err)
	}
	if emb.calls != 1 {
		t.Fatalf("changed text_hash carried a stale vector forward (embedded %d texts, want 1)", emb.calls)
	}
}
```

If `model.NewNode` has a different constructor signature in this tree, mirror the node construction already used in `engine/embed/generate_test.go` verbatim rather than inventing one.

- [ ] **Step 4: Run test to verify it fails or is under-specified**

Run: `go test ./engine/embed/ -run TestCarryForward -v`
Expected: the partial-match subtests are the ones at risk. Record which subtests fail.

- [ ] **Step 5: Harden the carry-forward guard**

In `engine/embed/generate.go`, in `GenerateAndPersistWithProgress`, replace the prior-generation probe:

```go
	if store != nil {
		if priorGen, priorState, err := store.Active(ctx, fp, nil); err == nil && priorState == StateReady && priorGen.ID != "" {
			priorID = priorGen.ID
			hasPrior = true
			priorTotal = priorGen.RowCount
		}
	}
```

with the explicit three-condition form:

```go
	if store != nil {
		// Carry-forward is authorized ONLY by the full conjunction:
		//
		//	active generation state == StateReady
		//	AND stored fingerprint == requested fingerprint byte-for-byte
		//	AND admitted document text_hash is unchanged (checked per row below)
		//
		// The canonical comparison is written out here rather than left
		// implicit in Active's own state computation: a store adapter that
		// ever loosened its fingerprint comparison would otherwise silently
		// authorize reuse across embedding spaces. The process epoch never
		// enters this comparison — it binds a running operation, not
		// persisted deterministic vectors.
		if priorGen, priorState, err := store.Active(ctx, fp, nil); err == nil &&
			priorState == StateReady &&
			priorGen.ID != "" &&
			priorGen.Fingerprint.Canonical() == fp.Canonical() {
			priorID = priorGen.ID
			hasPrior = true
			priorTotal = priorGen.RowCount
		}
	}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./engine/embed/ -run TestCarryForward -v`
Expected: PASS (2 tests plus 3 subtests).

- [ ] **Step 7: Run the whole embed and adapter suites**

Run: `go test ./engine/embed/...`
Expected: PASS — the generation-store conformance suite's existing carry-forward call-count assertions still hold.

- [ ] **Step 8: Commit**

```bash
git add engine/embed/coderank/identity_test.go engine/embed/carryforward_test.go engine/embed/generate.go
git commit -m "test(embed): pin the durable-identity matrix and exact carry-forward equality"
```

---

## Task 6: Private staging index and the fail-closed publication barrier

**Files:**
- Create: `engine/embed/snapshot.go`
- Create: `engine/embed/snapshot_test.go`
- Modify: `engine/embed/generate.go`
- Modify: `engine/embed/generate_test.go`

**Interfaces:**
- Consumes: `embed.Cause`, `embed.NewCauseError` (Task 1); `embed.VectorIndex`, `embed.Fingerprint`, `embed.GenerationID`, `embed.State` (existing).
- Produces: `type Snapshot struct { GenerationID GenerationID; Fingerprint Fingerprint; State State; Index VectorIndex }`; `type SnapshotPublisher interface { Invalidate(cause Cause); Publish(s *Snapshot) error }`; `type SnapshotSource interface { Capture() (*Snapshot, error) }`; `type SnapshotHolder struct{...}` with `func NewSnapshotHolder() *SnapshotHolder`, `Publish`, `Invalidate`, `Capture`; `type GenerateOptions struct {...}`; `func GenerateAndPublish(ctx context.Context, opts GenerateOptions) (GenerateResult, error)`; `GenerateResult.Snapshot *Snapshot`.

- [ ] **Step 1: Write the failing test**

Create `engine/embed/snapshot_test.go`:

```go
package embed

import (
	"context"
	"errors"
	"testing"

	"github.com/samibel/graphi/core/model"
)

func TestSnapshotHolder_CaptureBeforeAnyPublishIsMissing(t *testing.T) {
	h := NewSnapshotHolder()
	snap, err := h.Capture()
	if err == nil {
		t.Fatalf("Capture on an unpublished holder returned snapshot %+v", snap)
	}
	if cause, _ := CauseOf(err); cause != CauseGenerationMissing {
		t.Fatalf("cause = %q, want %q", cause, CauseGenerationMissing)
	}
}

func TestSnapshotHolder_PublishRejectsANonReadySnapshot(t *testing.T) {
	h := NewSnapshotHolder()
	err := h.Publish(&Snapshot{GenerationID: "v2-abc", State: StateStale, Index: NewIndex()})
	if err == nil {
		t.Fatalf("Publish accepted a non-ready snapshot")
	}
	if _, ok := h.snapshot(); ok {
		t.Fatalf("a rejected publish still moved the live pointer")
	}
}

func TestSnapshotHolder_CaptureReturnsOneConsistentTuple(t *testing.T) {
	h := NewSnapshotHolder()
	fp := Fingerprint{ModelID: "coderank:m@r:d", Dim: 4, DocumentSchema: DocumentSchema, GraphGeneration: "g1"}
	first := &Snapshot{GenerationID: fp.ID(), Fingerprint: fp, State: StateReady, Index: NewIndex()}
	if err := h.Publish(first); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	captured, err := h.Capture()
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// A concurrent publication of a DIFFERENT generation must not alter the
	// tuple the in-flight query already captured.
	other := Fingerprint{ModelID: "coderank:m@r:d", Dim: 4, DocumentSchema: DocumentSchema, GraphGeneration: "g2"}
	if err := h.Publish(&Snapshot{GenerationID: other.ID(), Fingerprint: other, State: StateReady, Index: NewIndex()}); err != nil {
		t.Fatalf("second Publish: %v", err)
	}
	if captured.GenerationID != fp.ID() || captured.Fingerprint.Canonical() != fp.Canonical() {
		t.Fatalf("a concurrent publication mutated an already-captured snapshot")
	}
}

func TestSnapshotHolder_InvalidateServesNeitherOldIndexNorFallback(t *testing.T) {
	h := NewSnapshotHolder()
	fp := Fingerprint{ModelID: "coderank:m@r:d", Dim: 4, DocumentSchema: DocumentSchema, GraphGeneration: "g1"}
	if err := h.Publish(&Snapshot{GenerationID: fp.ID(), Fingerprint: fp, State: StateReady, Index: NewIndex()}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	h.Invalidate(CauseSnapshotInconsistent)
	snap, err := h.Capture()
	if err == nil {
		t.Fatalf("Capture after Invalidate returned %+v", snap)
	}
	if cause, _ := CauseOf(err); cause != CauseSnapshotInconsistent {
		t.Fatalf("cause = %q, want %q", cause, CauseSnapshotInconsistent)
	}
}

// failingPublisher proves the barrier's post-commit failure path.
type failingPublisher struct {
	invalidated Cause
	err         error
}

func (p *failingPublisher) Invalidate(cause Cause) { p.invalidated = cause }
func (p *failingPublisher) Publish(*Snapshot) error {
	return p.err
}

func TestGenerateAndPublish_PostCommitSwapFailureIsFailClosed(t *testing.T) {
	ctx := context.Background()
	store := NewMemGenerationStore()
	emb := &countingEmbedder{id: "coderank:m@r:d", dim: 4, chunker: `{"m":1}`}
	pub := &failingPublisher{err: errors.New("swap refused")}

	_, err := GenerateAndPublish(ctx, GenerateOptions{
		Registry:        carryForwardRegistry(t, emb),
		Nodes:           []model.Node{model.NewNode("n1", "func", "pkg.Fn", "a.go", 1, 1)},
		Docs:            V2DocumentSource{},
		Store:           store,
		GraphGeneration: "g1",
		Publisher:       pub,
	})
	if err == nil {
		t.Fatalf("a failed live-snapshot swap was reported as success")
	}
	if cause, _ := CauseOf(err); cause != CausePublishFailed {
		t.Fatalf("cause = %q, want %q", cause, CausePublishFailed)
	}
	if pub.invalidated != CauseSnapshotInconsistent {
		t.Fatalf("the barrier did not mark the live snapshot inconsistent before swapping")
	}
}

func TestGenerateAndPublish_DoesNotMutateTheLiveIndexDuringConstruction(t *testing.T) {
	ctx := context.Background()
	store := NewMemGenerationStore()
	emb := &countingEmbedder{id: "coderank:m@r:d", dim: 4, chunker: `{"m":1}`}
	live := NewIndex()
	h := NewSnapshotHolder()

	res, err := GenerateAndPublish(ctx, GenerateOptions{
		Registry:        carryForwardRegistry(t, emb),
		Nodes:           []model.Node{model.NewNode("n1", "func", "pkg.Fn", "a.go", 1, 1)},
		Docs:            V2DocumentSource{},
		Store:           store,
		GraphGeneration: "g1",
		Publisher:       h,
	})
	if err != nil {
		t.Fatalf("GenerateAndPublish: %v", err)
	}
	if live.Len() != 0 {
		t.Fatalf("the build mutated an index it was never given: %d rows", live.Len())
	}
	if res.Snapshot == nil || res.Snapshot.Index.Len() != 1 {
		t.Fatalf("the published snapshot does not carry the built rows")
	}
	captured, err := h.Capture()
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if captured.GenerationID != res.GenerationID {
		t.Fatalf("captured generation %q, built %q", captured.GenerationID, res.GenerationID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./engine/embed/ -run 'TestSnapshotHolder|TestGenerateAndPublish' -v`
Expected: FAIL — `undefined: NewSnapshotHolder`, `undefined: GenerateOptions`, `undefined: GenerateAndPublish`.

- [ ] **Step 3: Write the snapshot implementation**

Create `engine/embed/snapshot.go`:

```go
package embed

import "sync/atomic"

// Snapshot is ONE complete, immutable view of a published semantic
// generation. A query captures exactly one of these and uses it for the
// entire operation, so a concurrent publication can never mix a query's
// generation, fingerprint and rows.
//
// A published Snapshot is IMMUTABLE: neither the struct nor the VectorIndex
// it names is mutated after Publish returns. A new generation publishes a
// NEW Snapshot over a NEW private index; it never writes into a live one.
type Snapshot struct {
	// GenerationID is the durable generation this snapshot serves.
	GenerationID GenerationID
	// Fingerprint is the durable identity the generation was built under.
	Fingerprint Fingerprint
	// State is the validated state at publication time. Only StateReady is
	// publishable.
	State State
	// Index is the private, fully built vector index for this generation.
	Index VectorIndex
}

// SnapshotPublisher is the write side of the publication barrier. The build
// path holds only this narrow seam, so a test can inject a swap failure.
type SnapshotPublisher interface {
	// Invalidate makes the live snapshot unavailable with the given cause.
	// It is called AFTER durable commit and BEFORE the swap, so the interval
	// between the two serves neither the old index under the new generation
	// nor a lexical fallback.
	Invalidate(cause Cause)
	// Publish swaps the complete immutable snapshot in one operation.
	Publish(s *Snapshot) error
}

// SnapshotSource is the read side. engine/search holds only this.
type SnapshotSource interface {
	// Capture returns the one live snapshot, or a cause-carrying error when
	// none is published or the live snapshot is inconsistent.
	Capture() (*Snapshot, error)
}

// SnapshotHolder is the process-memory pointer the barrier swaps. Its zero
// value is "nothing published yet".
type SnapshotHolder struct {
	live      atomic.Pointer[Snapshot]
	invalidBy atomic.Pointer[Cause]
}

var (
	_ SnapshotPublisher = (*SnapshotHolder)(nil)
	_ SnapshotSource    = (*SnapshotHolder)(nil)
)

// NewSnapshotHolder returns an empty holder.
func NewSnapshotHolder() *SnapshotHolder { return &SnapshotHolder{} }

// Publish validates and installs the snapshot in one pointer swap, clearing
// any prior invalidation. An incomplete or non-ready snapshot is refused and
// leaves the live pointer untouched.
func (h *SnapshotHolder) Publish(s *Snapshot) error {
	if s == nil || s.Index == nil || s.GenerationID == "" {
		return NewCauseError(CausePublishFailed, "live snapshot publication",
			"graphi index --semantic", "the snapshot is incomplete", nil)
	}
	if s.State != StateReady {
		return NewCauseError(CausePublishFailed, "live snapshot publication",
			"graphi index --semantic", "only a validated ready generation may be published", nil)
	}
	h.live.Store(s)
	h.invalidBy.Store(nil)
	return nil
}

// Invalidate marks the live snapshot unavailable. It is one-way until the
// next successful Publish.
func (h *SnapshotHolder) Invalidate(cause Cause) {
	c := cause
	h.invalidBy.Store(&c)
}

// Capture returns the live snapshot. It never returns a partially swapped
// tuple: the pointer is swapped atomically, and the pointed-to Snapshot is
// immutable.
func (h *SnapshotHolder) Capture() (*Snapshot, error) {
	if c := h.invalidBy.Load(); c != nil {
		return nil, NewCauseError(*c, "live snapshot capture", "restart graphi to reload the committed generation",
			"the committed generation and the live snapshot are inconsistent", nil)
	}
	s := h.live.Load()
	if s == nil {
		return nil, NewCauseError(CauseGenerationMissing, "live snapshot capture", "graphi index --semantic",
			"no semantic generation has been published in this process", nil)
	}
	return s, nil
}

// snapshot is the unexported test accessor for the raw live pointer.
func (h *SnapshotHolder) snapshot() (*Snapshot, bool) {
	s := h.live.Load()
	return s, s != nil
}
```

- [ ] **Step 4: Move the build onto a private index behind the barrier**

In `engine/embed/generate.go`:

1. Add the `Snapshot` field and the unexported vector accumulator to `GenerateResult`:

```go
	// Snapshot is the immutable live snapshot this build published, or nil
	// when the caller supplied no publisher.
	Snapshot *Snapshot
	// fingerprint is the durable identity this build was fingerprinted
	// under. Unexported: callers read it through Snapshot.Fingerprint.
	fingerprint Fingerprint
	// vectors is the staging row set, used to rebuild a caller-supplied
	// index AFTER a successful commit. It is unexported: the live index is
	// never mutated during generation construction.
	vectors []Vector
```

2. Add the options type and the new entry point:

```go
// GenerateOptions is the input to GenerateAndPublish. It is the shape the
// product composition root uses; GenerateAndPersist and
// GenerateAndPersistWithProgress remain as thin wrappers for callers that
// only want the durable pass.
type GenerateOptions struct {
	Registry        *Registry
	Nodes           []model.Node
	Docs            DocumentSource
	Store           GenerationStore
	GraphGeneration string
	Progress        GenerationProgressFunc
	// Publisher receives the complete immutable snapshot after a successful
	// durable commit. Nil means "durable pass only".
	Publisher SnapshotPublisher
	// NewIndex builds the PRIVATE staging index. Nil selects the
	// brute-force backend. The build never writes into a live index.
	NewIndex func() VectorIndex
}

// GenerateAndPublish runs the embedding-generation pass into a PRIVATE
// staging index and then applies the fail-closed publication barrier:
//
//  1. before durable commit the prior generation remains active;
//  2. the validated staging generation is committed atomically in SQLite;
//  3. during the interval before the live swap, semantic retrieval is not
//     ready (the publisher is invalidated);
//  4. the complete immutable snapshot is swapped in one operation;
//  5. only then may the process report StateReady for the new generation.
//
// A durable commit that succeeds while the live swap fails leaves the
// publisher invalidated with semantic_snapshot_inconsistent and returns
// semantic_generation_publish_failed. The committed generation remains the
// durable source of truth, so a restart reloads and validates it.
func GenerateAndPublish(ctx context.Context, opts GenerateOptions) (GenerateResult, error) {
	newIndex := opts.NewIndex
	if newIndex == nil {
		newIndex = func() VectorIndex { return NewIndex() }
	}
	staging := newIndex()
	res, err := generateInto(ctx, opts.Registry, opts.Nodes, opts.Docs, staging, opts.Store, opts.Progress, opts.GraphGeneration)
	if err != nil {
		return GenerateResult{}, err
	}
	if opts.Publisher == nil || !res.Configured {
		return res, nil
	}
	// Step 3: the interval between durable commit and the live swap serves
	// nothing. Step 4: one atomic swap of the complete snapshot.
	opts.Publisher.Invalidate(CauseSnapshotInconsistent)
	snap := &Snapshot{
		GenerationID: res.GenerationID,
		Fingerprint:  res.fingerprint,
		State:        StateReady,
		Index:        staging,
	}
	if perr := opts.Publisher.Publish(snap); perr != nil {
		return GenerateResult{}, NewCauseError(CausePublishFailed, "live snapshot publication",
			"restart graphi to reload the committed generation",
			"the durable generation committed but the live snapshot could not be swapped", perr)
	}
	res.Snapshot = snap
	return res, nil
}
```

3. Rename the existing body of `GenerateAndPersistWithProgress` to `generateInto` with the same parameter list, add an unexported `fingerprint Fingerprint` field to `GenerateResult`, set `res.fingerprint = fp` right after the fingerprint is built, and append each written row's vector to `res.vectors` next to every existing `index.Put` call.

4. Replace `GenerateAndPersistWithProgress` with the compatibility wrapper:

```go
// GenerateAndPersistWithProgress is the durable-only pass. The caller's
// index is rebuilt from the staging rows AFTER a successful commit, so a
// live index is never mutated during generation construction.
func GenerateAndPersistWithProgress(ctx context.Context, reg *Registry, nodes []model.Node, docs DocumentSource, index VectorIndex, store GenerationStore, onProgress GenerationProgressFunc, graphGeneration string) (GenerateResult, error) {
	staging := NewIndex()
	res, err := generateInto(ctx, reg, nodes, docs, staging, store, onProgress, graphGeneration)
	if err != nil {
		return GenerateResult{}, err
	}
	if index != nil && res.Configured {
		if rerr := index.Rebuild(ctx, res.vectors); rerr != nil {
			return GenerateResult{}, rerr
		}
	}
	return res, nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./engine/embed/ -run 'TestSnapshotHolder|TestGenerateAndPublish' -v`
Expected: PASS (6 tests).

- [ ] **Step 6: Prove the pre-commit failure paths preserve the active generation**

Add to `engine/embed/generate_test.go`:

```go
func TestGenerateAndPublish_PreCommitFailureLeavesTheActiveGenerationIntact(t *testing.T) {
	ctx := context.Background()
	store := NewMemGenerationStore()
	nodes := []model.Node{model.NewNode("n1", "func", "pkg.Fn", "a.go", 1, 1)}
	good := &countingEmbedder{id: "coderank:m@r:d", dim: 4, chunker: `{"m":1}`}
	h := NewSnapshotHolder()

	if _, err := GenerateAndPublish(ctx, GenerateOptions{
		Registry: carryForwardRegistry(t, good), Nodes: nodes, Docs: V2DocumentSource{},
		Store: store, GraphGeneration: "g1", Publisher: h,
	}); err != nil {
		t.Fatalf("first build: %v", err)
	}
	before, err := h.Capture()
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// An attestation failure immediately before commit must abort staging.
	failing := &attestingEmbedder{countingEmbedder: countingEmbedder{id: "coderank:m@r:d", dim: 4, chunker: `{"m":1}`}, failAt: "before generation commit"}
	if _, err := GenerateAndPublish(ctx, GenerateOptions{
		Registry: carryForwardRegistry(t, failing), Nodes: nodes, Docs: V2DocumentSource{},
		Store: store, GraphGeneration: "g2", Publisher: h,
	}); err == nil {
		t.Fatalf("a pre-commit attestation failure was reported as success")
	}
	after, err := h.Capture()
	if err != nil {
		t.Fatalf("Capture after the failed build: %v", err)
	}
	if after.GenerationID != before.GenerationID {
		t.Fatalf("a pre-commit failure moved the live generation from %q to %q", before.GenerationID, after.GenerationID)
	}
	gen, state, aerr := store.Active(ctx, before.Fingerprint, nil)
	if aerr != nil || state != StateReady || gen.ID != before.GenerationID {
		t.Fatalf("a pre-commit failure moved the durable active pointer: id=%q state=%v err=%v", gen.ID, state, aerr)
	}
}
```

Add the `attestingEmbedder` helper to the same file: it embeds `countingEmbedder`, implements `ExpectedRuntimeAttestation` and `RuntimeAttestation`, and returns a changed epoch once the named phase is reached. Model it on the existing fake in `engine/embed/attestation_test.go` rather than writing a new shape.

- [ ] **Step 7: Run the affected suites**

Run: `go test ./engine/embed/...`
Expected: PASS.

Run: `go test ./cmd/... ./internal/eval/retrieval/ ./engine/search/...`
Expected: PASS — the wrapper keeps every existing `GenerateAndPersist` caller working.

- [ ] **Step 8: Commit**

```bash
git add engine/embed/snapshot.go engine/embed/snapshot_test.go engine/embed/generate.go engine/embed/generate_test.go
git commit -m "feat(embed): build into a private index behind a fail-closed publication barrier"
```

---

## Task 7: The attested query boundary in engine/search

**Files:**
- Create: `engine/search/attested.go`
- Create: `engine/search/attested_test.go`
- Modify: `engine/search/service.go`
- Modify: `engine/search/semantic.go`

**Interfaces:**
- Consumes: `embed.Selection` (Task 2), `embed.SessionReporter` / `embed.SessionBound` / `embed.SessionPoisoned` (Task 4), `embed.SnapshotSource` / `embed.Snapshot` (Task 6), `embed.CauseOf` / `embed.NewCauseError` / the cause constants (Task 1), `embed.EmbedQueryWithDiagnostics` (existing).
- Produces: `func (s *Service) WithSelection(sel embed.Selection) *Service`; `func (s *Service) Selection() embed.Selection`; `func (s *Service) WithSnapshots(src embed.SnapshotSource) *Service`; `type attestedResult struct { Snapshot *embed.Snapshot; Hits []embed.Hit }`; `func (s *Service) attestedSemanticSearch(ctx context.Context, query string, limit int) (attestedResult, error)`; `SemanticResponse.Cause embed.Cause` (new wire field, `omitempty`).

- [ ] **Step 1: Write the failing test**

Create `engine/search/attested_test.go`:

```go
package search

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/embed"
)

// attestedFake is a CodeRank-shaped embedder: it reports a session state and
// a runtime attestation, and can be made to drift on demand.
type attestedFake struct {
	dim      int
	session  string
	expected embed.RuntimeAttestation
	live     embed.RuntimeAttestation
	embedErr error
}

func (f *attestedFake) ID() string          { return "coderank:m@r:digest" }
func (f *attestedFake) Dim() int            { return f.dim }
func (f *attestedFake) SessionState() string { return f.session }
func (f *attestedFake) ExpectedRuntimeAttestation() embed.RuntimeAttestation { return f.expected }
func (f *attestedFake) RuntimeAttestation(context.Context) (embed.RuntimeAttestation, error) {
	return f.live, nil
}
func (f *attestedFake) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if f.embedErr != nil {
		return nil, f.embedErr
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = make([]float32, f.dim)
		out[i][0] = 1
	}
	return out, nil
}

func boundFake() *attestedFake {
	a := embed.RuntimeAttestation{IdentityDigest: strings.Repeat("a", 64), Epoch: "pid-1-boot-1"}
	return &attestedFake{dim: 4, session: embed.SessionBound, expected: a, live: a}
}

func readySnapshot(fp embed.Fingerprint) *embed.SnapshotHolder {
	h := embed.NewSnapshotHolder()
	_ = h.Publish(&embed.Snapshot{GenerationID: fp.ID(), Fingerprint: fp, State: embed.StateReady, Index: embed.NewIndex()})
	return h
}

func requiredService(t *testing.T, emb embed.Embedder, src embed.SnapshotSource, fp embed.Fingerprint) *Service {
	t.Helper()
	reg := embed.NewRegistry()
	if err := reg.Register(emb); err != nil {
		t.Fatalf("register: %v", err)
	}
	reg.Freeze()
	sel := embed.SelectionForTest(embed.SelectionCodeRankRequired, "coderank", emb)
	return New(nil).
		WithSemantic(reg, embed.NewIndex(), nil).
		WithSemanticState(SemanticState{State: embed.StateReady, Requested: fp}).
		WithSelection(sel).
		WithSnapshots(src)
}

func codeRankFingerprint() embed.Fingerprint {
	return embed.Fingerprint{ModelID: "coderank:m@r:digest", Dim: 4, DocumentSchema: embed.DocumentSchema, GraphGeneration: "g1"}
}

func TestAttested_RequiredProfileFailsClosedOnMissingSnapshot(t *testing.T) {
	fp := codeRankFingerprint()
	svc := requiredService(t, boundFake(), embed.NewSnapshotHolder(), fp)
	if _, err := svc.SemanticSearch(context.Background(), "find the retry policy", 10); err == nil {
		t.Fatalf("a required profile served a result with no published snapshot")
	} else if cause, _ := embed.CauseOf(err); cause != embed.CauseGenerationMissing {
		t.Fatalf("cause = %q, want %q", cause, embed.CauseGenerationMissing)
	}
}

func TestAttested_RequiredProfileFailsClosedOnFingerprintMismatch(t *testing.T) {
	built := codeRankFingerprint()
	requested := codeRankFingerprint()
	requested.GraphGeneration = "g2"
	svc := requiredService(t, boundFake(), readySnapshot(built), requested)
	if _, err := svc.SemanticSearch(context.Background(), "find the retry policy", 10); err == nil {
		t.Fatalf("a required profile served vectors across fingerprints")
	} else if cause, _ := embed.CauseOf(err); cause != embed.CauseGenerationStale {
		t.Fatalf("cause = %q, want %q", cause, embed.CauseGenerationStale)
	}
}

func TestAttested_RequiredProfileFailsClosedOnPoisonedSession(t *testing.T) {
	fp := codeRankFingerprint()
	f := boundFake()
	f.session = embed.SessionPoisoned
	svc := requiredService(t, f, readySnapshot(fp), fp)
	if _, err := svc.SemanticSearch(context.Background(), "find the retry policy", 10); err == nil {
		t.Fatalf("a required profile served a result from a poisoned session")
	} else if cause, _ := embed.CauseOf(err); cause != embed.CauseAttestationMismatch {
		t.Fatalf("cause = %q, want %q", cause, embed.CauseAttestationMismatch)
	}
}

func TestAttested_RequiredProfileFailsClosedWhenTheRuntimeDriftsBeforeTheQuery(t *testing.T) {
	fp := codeRankFingerprint()
	f := boundFake()
	f.live = embed.RuntimeAttestation{IdentityDigest: strings.Repeat("b", 64), Epoch: "pid-2-boot-2"}
	svc := requiredService(t, f, readySnapshot(fp), fp)
	if _, err := svc.SemanticSearch(context.Background(), "find the retry policy", 10); err == nil {
		t.Fatalf("a required profile queried a switched sidecar")
	} else if cause, _ := embed.CauseOf(err); cause != embed.CauseAttestationMismatch {
		t.Fatalf("cause = %q, want %q", cause, embed.CauseAttestationMismatch)
	}
}

func TestAttested_RequiredProfileNeverDegradesToAnUnavailableResponse(t *testing.T) {
	fp := codeRankFingerprint()
	f := boundFake()
	f.embedErr = errors.New("sidecar closed the connection")
	svc := requiredService(t, f, readySnapshot(fp), fp)
	resp, err := svc.SemanticSearch(context.Background(), "find the retry policy", 10)
	if err == nil {
		t.Fatalf("a provider error became the response %+v", resp)
	}
	if resp.Available {
		t.Fatalf("a failed required semantic operation reported Available=true")
	}
}

func TestAttested_ReadyRequiredProfileWithZeroHitsIsASuccess(t *testing.T) {
	fp := codeRankFingerprint()
	svc := requiredService(t, boundFake(), readySnapshot(fp), fp)
	resp, err := svc.SemanticSearch(context.Background(), "find the retry policy", 10)
	if err != nil {
		t.Fatalf("SemanticSearch: %v", err)
	}
	if !resp.Available || resp.State != embed.StateReady {
		t.Fatalf("available=%v state=%v, want true/ready", resp.Available, resp.State)
	}
	if len(resp.Hits) != 0 {
		t.Fatalf("hits = %d, want 0 over an empty index", len(resp.Hits))
	}
}
```

- [ ] **Step 2: Add the Selection test constructor the test needs**

Append to `engine/embed/selection.go`:

```go
// SelectionForTest builds a Selection without resolving a selector. It exists
// so tests in OTHER packages can construct a required profile without a live
// sidecar; production code resolves selections through ResolveSelection.
func SelectionForTest(mode SelectionMode, profile string, emb Embedder) Selection {
	return Selection{mode: mode, profile: profile, explicit: emb != nil, embedder: emb}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./engine/search/ -run TestAttested -v`
Expected: FAIL — `svc.WithSelection undefined`, `svc.WithSnapshots undefined`.

- [ ] **Step 4: Write the attested boundary**

Create `engine/search/attested.go`:

```go
package search

import (
	"context"
	"math"

	"github.com/samibel/graphi/engine/embed"
)

// attestedResult is one safe semantic operation's output: the captured
// snapshot and the ranked hits produced against exactly that snapshot.
type attestedResult struct {
	Snapshot *embed.Snapshot
	Hits     []embed.Hit
}

// attestedSemanticSearch owns the safe semantic operation. The order is
// fixed and is the spec's query-check sequence:
//
//  1. capture one immutable live generation snapshot;
//  2. require StateReady and exact durable fingerprint equality;
//  3. verify the runtime before the query embedding (done inside
//     embed.EmbedQueryWithDiagnostics, which also re-verifies after);
//  4. prepend the pinned query instruction exactly once (adapter-owned);
//  5. request the query embedding;
//  6. validate protocol, identity digest, epoch, dimension, cardinality,
//     unknown-token count and finite vector values (adapter-owned) plus the
//     cardinality and dimension checks below;
//  7. only then compare the vector with the captured index.
//
// engine/retrieval receives either a successful result or a hard error. This
// function never converts a provider error into an empty hit list.
func (s *Service) attestedSemanticSearch(ctx context.Context, query string, limit int) (attestedResult, error) {
	emb, ok := s.embedReg.Active()
	if !ok {
		return attestedResult{}, embed.NewCauseError(embed.CauseRuntimeUnavailable, "semantic query",
			"graphi setup-embedder", "no embedder is active for a required profile", nil)
	}

	// 1. One snapshot for the whole operation.
	snap, err := s.captureSnapshot()
	if err != nil {
		return attestedResult{}, err
	}

	// 2. Ready, and the exact durable fingerprint.
	if snap.State != embed.StateReady {
		return attestedResult{}, embed.NewCauseError(causeForState(snap.State), "semantic query",
			"graphi index --semantic", "the active generation is not ready", nil)
	}
	if snap.Fingerprint.Canonical() != s.semanticState.Requested.Canonical() {
		return attestedResult{}, embed.NewCauseError(embed.CauseGenerationStale, "semantic query",
			"graphi index --semantic", "the active generation was built under a different durable identity", nil)
	}

	// 2b. The adapter session must be bound. A poisoned session never
	// silently rebinds to a replacement sidecar.
	if sr, ok := emb.(embed.SessionReporter); ok && sr.SessionState() != embed.SessionBound {
		return attestedResult{}, embed.NewCauseError(embed.CauseAttestationMismatch, "semantic query",
			"restart graphi after restarting the sidecar", "the adapter session is not bound", nil)
	}

	// 3-6. Attested embedding. The preflight detects a switch before the
	// request; the adapter's response binding detects a switch between
	// preflight and response.
	result, err := embed.EmbedQueryWithDiagnostics(ctx, emb, query)
	if err != nil {
		return attestedResult{}, classifyQueryError(err)
	}
	if len(result.Vectors) != 1 {
		return attestedResult{}, embed.NewCauseError(embed.CauseAttestationMismatch, "semantic query",
			"restart graphi after restarting the sidecar", "the provider returned an unexpected vector cardinality", nil)
	}
	vec := result.Vectors[0]
	if len(vec) != emb.Dim() {
		return attestedResult{}, embed.NewCauseError(embed.CauseAttestationMismatch, "semantic query",
			"restart graphi after restarting the sidecar", "the query vector dimension does not match the pinned profile", nil)
	}
	for _, v := range vec {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return attestedResult{}, embed.NewCauseError(embed.CauseAttestationMismatch, "semantic query",
				"restart graphi after restarting the sidecar", "the query vector contains a non-finite value", nil)
		}
	}

	// 7. Rank against the captured snapshot's index only.
	return attestedResult{Snapshot: snap, Hits: snap.Index.Search(vec, limit)}, nil
}

// captureSnapshot returns the one live snapshot for this operation. A service
// wired without a snapshot source falls back to the plumbed semantic state
// and the service-level index, which is the pre-existing Potion behaviour.
func (s *Service) captureSnapshot() (*embed.Snapshot, error) {
	if s.snapshots != nil {
		return s.snapshots.Capture()
	}
	return &embed.Snapshot{
		GenerationID: s.semanticState.Requested.ID(),
		Fingerprint:  s.semanticState.Requested,
		State:        s.semanticState.State,
		Index:        s.index,
	}, nil
}

// causeForState maps a generation state onto the engine's stable causes.
func causeForState(state embed.State) embed.Cause {
	switch state {
	case embed.StateStale:
		return embed.CauseGenerationStale
	case embed.StateCorrupt:
		return embed.CauseGenerationCorrupt
	default:
		return embed.CauseGenerationMissing
	}
}

// classifyQueryError preserves an already-typed cause and otherwise names the
// failure as a runtime-unavailable condition. An attestation error carries
// its own cause from the adapter; a transport error does not.
func classifyQueryError(err error) error {
	if _, ok := embed.CauseOf(err); ok {
		return err
	}
	if _, ok := err.(*embed.RuntimeAttestationError); ok {
		return embed.NewCauseError(embed.CauseAttestationMismatch, "semantic query",
			"restart graphi after restarting the sidecar", "the sidecar runtime binding changed during the query", err)
	}
	return embed.NewCauseError(embed.CauseRuntimeUnavailable, "semantic query",
		"start the pinned sidecar on loopback, then rerun", "the query embedding request did not complete", err)
}
```

- [ ] **Step 5: Wire the selection and the snapshot source into the service**

In `engine/search/service.go`, add the fields to `Service`:

```go
	// selection is the resolved, immutable profile selection. Its Required()
	// flag decides whether a semantic failure is a hard error or the typed
	// unavailable response.
	selection embed.Selection
	// snapshots is the live-generation source. Nil keeps the pre-existing
	// service-level index behaviour.
	snapshots embed.SnapshotSource
```

and the builders:

```go
// WithSelection plumbs the resolved profile selection. Under a required
// profile every semantic failure is fail-closed: no Potion substitution and
// no lexical-only result labelled as a CodeRank result.
func (s *Service) WithSelection(sel embed.Selection) *Service {
	s.selection = sel
	return s
}

// Selection returns the resolved selection the service was wired with.
func (s *Service) Selection() embed.Selection {
	if s == nil {
		return embed.Selection{}
	}
	return s.selection
}

// WithSnapshots plumbs the live-generation snapshot source so every query
// captures exactly one generation/fingerprint/index tuple.
func (s *Service) WithSnapshots(src embed.SnapshotSource) *Service {
	s.snapshots = src
	return s
}
```

- [ ] **Step 6: Route SemanticSearch through the boundary**

In `engine/search/semantic.go`, add the wire field to `SemanticResponse` after `Reason`:

```go
	// Cause is the engine-owned stable failure cause, present only on a
	// fail-closed required-profile response. Surfaces map it to their own
	// transport codes without changing its meaning.
	Cause embed.Cause `json:"cause,omitempty"`
```

Replace the body of `SemanticSearch` from the `if limit <= 0` line onward with:

```go
	if limit <= 0 {
		limit = DefaultResultLimit
	}
	if query == "" {
		return SemanticResponse{Query: query, Available: true, State: embed.StateReady, Hits: []SemanticHit{}}, nil
	}
	res, err := s.attestedSemanticSearch(ctx, query, limit)
	if err != nil {
		cause, _ := embed.CauseOf(err)
		if s.selection.Required() {
			// Fail-closed: a required profile never degrades. The caller
			// receives the hard error; no result is published.
			return SemanticResponse{Query: query, Available: false, State: embed.StateMissing, Cause: cause, Hits: []SemanticHit{}}, err
		}
		if u := repairable(err); u != "" {
			return SemanticResponse{Query: query, Available: false, State: embed.StateMissing, Reason: u, Hits: []SemanticHit{}}, nil
		}
		return SemanticResponse{}, err
	}
	hits := make([]SemanticHit, 0, len(res.Hits))
	for _, h := range res.Hits {
		hit := SemanticHit{NodeID: string(h.NodeID), DocumentID: h.DocumentID, Score: h.Score}
		if s.nodeReader != nil {
			if n, gerr := s.nodeReader.GetNode(ctx, h.NodeID); gerr == nil {
				hit.Kind = n.Kind()
				hit.QualifiedName = n.QualifiedName()
				hit.SourcePath = n.SourcePath()
				hit.Line = n.Line()
				hit.Column = n.Column()
			}
		}
		hits = append(hits, hit)
	}
	return SemanticResponse{Query: query, Available: true, State: embed.StateReady, Hits: hits}, nil
```

Leave the three early returns above it (nil registry, no active embedder, availability check, non-ready plumbed state) unchanged for the non-required path, but add a required-profile guard immediately after the `emb, ok := s.embedReg.Active()` block:

```go
	if s.selection.Required() && !ok {
		return SemanticResponse{Query: query, Available: false, State: embed.StateMissing, Cause: embed.CauseRuntimeUnavailable, Hits: []SemanticHit{}},
			embed.NewCauseError(embed.CauseRuntimeUnavailable, "semantic query", "graphi setup-embedder",
				"the required CodeRank profile has no active embedder", nil)
	}
```

and, in the non-ready-state short circuit, fail closed when required:

```go
	if !s.semanticState.State.IsZero() && s.semanticState.State != embed.StateReady {
		if s.selection.Required() {
			cause := causeForState(s.semanticState.State)
			return SemanticResponse{Query: query, Available: false, State: s.semanticState.State, Cause: cause, Hits: []SemanticHit{}},
				embed.NewCauseError(cause, "semantic query", "graphi index --semantic", "the active generation is not ready", nil)
		}
		return SemanticResponse{Query: query, Available: false, State: s.semanticState.State, Reason: s.semanticState.Reason, Hits: []SemanticHit{}}, nil
	}
```

- [ ] **Step 7: Run test to verify it passes**

Run: `go test ./engine/search/ -run TestAttested -v`
Expected: PASS (6 tests).

- [ ] **Step 8: Run the search suite and the serialized-byte parity suite**

Run: `go test ./engine/search/...`
Expected: PASS — the default-build graceful-skip goldens are unchanged because `Cause` is `omitempty` and the unconfigured path never sets it.

Run: `go test ./surfaces/`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add engine/search/attested.go engine/search/attested_test.go engine/search/service.go engine/search/semantic.go engine/embed/selection.go
git commit -m "feat(search): own the attested query boundary and fail closed under a required profile"
```

---

## Task 8: Fail-closed retrieval under a required profile

**Files:**
- Modify: `engine/retrieval/service.go`
- Modify: `engine/retrieval/retrieval.go`
- Modify: `engine/retrieval/semantic_first_test.go`

**Interfaces:**
- Consumes: `search.Service.Selection()` and `SemanticResponse.Cause` (Task 7); `embed.CauseOf` (Task 1).
- Produces: unexported `func (b *searchServiceBridge) required() bool`; `semanticOutcome` gains a trailing `error` return; `Result.Cause embed.Cause`.

- [ ] **Step 1: Write the failing test**

Append to `engine/retrieval/semantic_first_test.go`:

```go
func TestRetrieve_RequiredProfileAbortsInsteadOfServingLexicalOnly(t *testing.T) {
	hardErr := embed.NewCauseError(embed.CauseAttestationMismatch, "semantic query",
		"restart graphi after restarting the sidecar", "the sidecar runtime binding changed", nil)
	eng := &engine{
		lexical:  &stubLexical{hits: []lexicalHit{{NodeID: "n1", Score: 10, Path: "a.go", QualifiedName: "pkg.Fn"}}},
		semantic: &stubSemantic{err: hardErr, isRequired: true},
	}
	res, err := eng.Retrieve(context.Background(), Request{Query: "how does retry backoff work", Mode: ModeAuto})
	if err == nil {
		t.Fatalf("a required profile published %d lexical rows after a semantic hard error", len(res.Rows))
	}
	if cause, _ := embed.CauseOf(err); cause != embed.CauseAttestationMismatch {
		t.Fatalf("cause = %q, want %q", cause, embed.CauseAttestationMismatch)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("rows = %d, want 0", len(res.Rows))
	}
}

func TestRetrieve_RequiredProfileStillAnswersAnExplicitLexicalOnlyRequest(t *testing.T) {
	hardErr := embed.NewCauseError(embed.CauseRuntimeUnavailable, "semantic query", "start the sidecar", "unreachable", nil)
	eng := &engine{
		lexical:  &stubLexical{hits: []lexicalHit{{NodeID: "n1", Score: 10, Path: "a.go", QualifiedName: "pkg.Fn"}}},
		semantic: &stubSemantic{err: hardErr, isRequired: true},
	}
	res, err := eng.Retrieve(context.Background(), Request{Query: "pkg.Fn", Mode: ModeLexicalOnly})
	if err != nil {
		t.Fatalf("an explicit lexical-only request failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows))
	}
	if res.Degradation != StateLexicalOnly {
		t.Fatalf("degradation = %q, want %q", res.Degradation, StateLexicalOnly)
	}
}

func TestRetrieve_LexicalBackfillUnderAReadyRequiredProfileIsNotDegradation(t *testing.T) {
	eng := &engine{
		lexical: &stubLexical{hits: []lexicalHit{{NodeID: "n2", Score: 9, Path: "b.go", QualifiedName: "pkg.Other"}}},
		semantic: &stubSemantic{
			out:        semanticOutcomeFixture("n1"),
			isRequired: true,
		},
	}
	res, err := eng.Retrieve(context.Background(), Request{Query: "how does retry backoff work", Mode: ModeAuto})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if res.Degradation != StateReady {
		t.Fatalf("degradation = %q, want %q for lexical backfill under a ready semantic result", res.Degradation, StateReady)
	}
	if len(res.Rows) == 0 {
		t.Fatalf("a ready required profile produced no rows")
	}
}
```

Add `isRequired bool` to the existing `stubSemantic` in that file plus `func (s *stubSemantic) required() bool { return s.isRequired }`, and add `semanticOutcomeFixture` returning a `semanticOutcome{Available: true, State: StateReady, Hits: []semanticHit{{NodeID: id, CosineScore: 0.9, Path: "a.go", QualifiedName: "pkg.Fn"}}}`. Reuse whatever stub names the file already defines rather than introducing parallel ones.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./engine/retrieval/ -run TestRetrieve_Required -v`
Expected: FAIL — the first test fails because `semanticOutcome` swallows the error and the engine serves the lexical list.

- [ ] **Step 3: Expose "required" through the semantic bridge**

In `engine/retrieval/service.go`, add:

```go
// required reports whether the wired selection makes semantic operations
// fail-closed. A required profile never degrades into a Potion or
// lexical-only result, so the retrieval module must propagate the semantic
// error rather than answer over lexical candidates.
func (b *searchServiceBridge) required() bool {
	if b == nil || b.service == nil {
		return false
	}
	return b.service.Selection().Required()
}
```

and change `searchServiceBridge.search` to preserve the cause on a fail-closed response:

```go
	resp, err := b.service.SemanticSearch(ctx, query, limit)
	if err != nil {
		return semanticOutcome{}, err
	}
```

(unchanged — `SemanticSearch` already returns the hard error under a required profile).

- [ ] **Step 4: Propagate the hard error through the engine**

In `engine/retrieval/retrieval.go`, add the interface and change `semanticOutcome`'s signature:

```go
// requiredSemanticProvider is the OPTIONAL capability the semantic provider
// exposes when its profile is fail-closed. A provider that does not
// implement it keeps the historical fail-soft behaviour.
type requiredSemanticProvider interface {
	required() bool
}

func (e *engine) semanticOutcome(ctx context.Context, req Request) (state State, reason string, hits []semanticHit, modelFP, indexFP string, err error) {
```

Replace the two swallow sites:

```go
	out, serr := e.semantic.search(ctx, req.Query, candidateK)
	if serr != nil {
		if rp, ok := e.semantic.(requiredSemanticProvider); ok && rp.required() {
			// Fail-closed: an explicitly selected CodeRank profile never
			// degrades into a successful lexical-only retrieval.
			return state, reason, nil, "", "", serr
		}
		mustStderr("retrieval: semantic search failed: %v\n", serr)
		return state, reason, nil, "", "", nil
	}
	if !out.Available {
		if rp, ok := e.semantic.(requiredSemanticProvider); ok && rp.required() {
			return out.State, out.Reason, nil, "", "", fmt.Errorf("retrieval: required semantic profile is unavailable: %s", out.Reason)
		}
		state = out.State
		reason = out.Reason
		return state, reason, nil, "", "", nil
	}
```

In `Retrieve`, propagate it:

```go
	state, reason, semHits, semFP, idxFP, semErr := e.semanticOutcome(ctx, req)
	if semErr != nil {
		// No retrieval result is published: not a lexical list, not an
		// empty semantic hit list, not a Potion substitution.
		return Result{}, semErr
	}
```

Add `"github.com/samibel/graphi/engine/embed"` to the file's imports if it is not already there. Do NOT add a cause field to `Result`: a required-profile failure publishes no `Result` at all, so a field that is empty on every served result would be dead weight. The cause travels on the returned error, where `embed.CauseOf` reads it.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./engine/retrieval/ -run TestRetrieve_Required -v`
Expected: PASS (2 tests).

Run: `go test ./engine/retrieval/ -run TestRetrieve_LexicalBackfill -v`
Expected: PASS.

- [ ] **Step 6: Run the retrieval suite, including byte parity**

Run: `go test ./engine/retrieval/`
Expected: PASS — `byte_parity_test.go` is unaffected because the unconfigured path has no required provider.

- [ ] **Step 7: Commit**

```bash
git add engine/retrieval/service.go engine/retrieval/retrieval.go engine/retrieval/semantic_first_test.go
git commit -m "feat(retrieval): abort instead of degrading when the selected profile is required"
```

---

## Task 9: One resolved selection at the composition root

**Files:**
- Modify: `cmd/internal/runtime/runtime.go`
- Modify: `cmd/graphi/semantic.go`
- Modify: `cmd/graphi/serve.go`
- Modify: `cmd/graphi/zeroconfig.go`
- Modify: `cmd/graphi/query.go`

**Interfaces:**
- Consumes: `embed.ResolveSelection`, `embed.DefaultContextConstructors`, `embed.Selection` (Task 2); `embed.NewSnapshotHolder`, `embed.GenerateAndPublish`, `embed.GenerateOptions` (Task 6); `search.Service.WithSelection` / `WithSnapshots` (Task 7).
- Produces: `func runtime.ResolveSelection(ctx context.Context) (embed.Selection, error)`; `func runtime.NewSearchServiceForSelection(ctx context.Context, store graphstore.Graphstore, metaDir string, sel embed.Selection) (*search.Service, error)`; `func runtime.BuildSemanticGenerationForSelection(ctx context.Context, ing *ingest.Ingester, graphStore graphstore.Graphstore, sel embed.Selection, generationStore embed.GenerationStore, docs embed.DocumentSource, publisher embed.SnapshotPublisher, progress embed.GenerationProgressFunc) (embed.GenerateResult, error)`; `func selectionFromEnv(ctx context.Context) (embed.Selection, error)` in `cmd/graphi`.

- [ ] **Step 1: Write the failing test**

Create `cmd/internal/runtime/selection_test.go`:

```go
package runtime

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/embed"
)

func TestResolveSelection_UnsetSelectorIsUnconfigured(t *testing.T) {
	t.Setenv(embed.EnvSelector, "")
	sel, err := ResolveSelection(context.Background())
	if err != nil {
		t.Fatalf("ResolveSelection: %v", err)
	}
	if sel.Mode() != embed.SelectionUnconfigured {
		t.Fatalf("mode = %q, want unconfigured", sel.Mode())
	}
}

func TestResolveSelection_InvalidCodeRankSelectorIsAHardError(t *testing.T) {
	t.Setenv(embed.EnvSelector, "coderank:not/absolute.json")
	sel, err := ResolveSelection(context.Background())
	if err == nil {
		t.Fatalf("a relative CodeRank manifest resolved to %q", sel.Mode())
	}
	if cause, ok := embed.CauseOf(err); !ok || cause != embed.CauseProfileInvalid {
		t.Fatalf("cause = %q (%v), want %q", cause, ok, embed.CauseProfileInvalid)
	}
}

func TestNewSearchServiceForSelection_RequiredProfileNeverDegradesToLexical(t *testing.T) {
	t.Setenv(embed.EnvSelector, "coderank:not/absolute.json")
	sel, err := ResolveSelection(context.Background())
	if err == nil {
		t.Fatalf("expected a hard selection error")
	}
	// The composition root must NOT build a search service from a failed
	// required selection; it must refuse.
	if _, svcErr := NewSearchServiceForSelection(context.Background(), graphstore.NewMemStore(), "", sel); svcErr == nil {
		t.Fatalf("a failed required selection produced a working search service")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/internal/runtime/ -run 'TestResolveSelection|TestNewSearchServiceForSelection' -v`
Expected: FAIL — `undefined: ResolveSelection`, `undefined: NewSearchServiceForSelection`.

- [ ] **Step 3: Resolve the selection once in the runtime**

Add to `cmd/internal/runtime/runtime.go`:

```go
// ResolveSelection reads GRAPHI_EMBEDDER ONCE and returns the immutable
// selection every surface shares. Deeper modules never reread the
// environment and never choose an embedder of their own.
//
// The context bounds the initial sidecar attestation an attested adapter
// performs during construction, so a hung sidecar cannot wedge startup.
func ResolveSelection(ctx context.Context) (embed.Selection, error) {
	return embed.ResolveSelection(ctx, os.Getenv(embed.EnvSelector), embed.DefaultContextConstructors())
}

// NewSearchServiceForSelection builds the shared search service from an
// already-resolved selection. Lexical search is always available. A REQUIRED
// selection that failed to resolve is refused outright: translating it into
// an empty registry would be the silent Potion/lexical fallback the design
// forbids.
func NewSearchServiceForSelection(ctx context.Context, store graphstore.Graphstore, metaDir string, sel embed.Selection) (*search.Service, error) {
	emb, ok := sel.Embedder()
	if !ok {
		if sel.Required() {
			return nil, embed.NewCauseError(embed.CauseProfileInvalid, "search service composition",
				"export GRAPHI_EMBEDDER=coderank:/absolute/path/to/coderank.json and start the pinned sidecar on loopback",
				"the required CodeRank profile did not resolve to an embedder", nil)
		}
		return search.New(store), nil // graceful skip: nothing configured
	}
	svc := newSearchServiceWithEmbedderAndSelection(store, metaDir, emb, sel)
	return svc, nil
}
```

Rename the existing `NewSearchServiceWithEmbedder` body into `newSearchServiceWithEmbedderAndSelection(store, metaDir, emb, sel)` and, at each of its two return sites, chain the selection and the snapshot source:

```go
		holder := embed.NewSnapshotHolder()
		if semanticState.State == embed.StateReady {
			// ... existing reload of `index` from the active generation ...
			_ = holder.Publish(&embed.Snapshot{
				GenerationID: gen.ID,
				Fingerprint:  semanticState.Requested,
				State:        embed.StateReady,
				Index:        index,
			})
		}
		return svc.WithSemantic(reg, index, store).
			WithSemanticState(semanticState).
			WithSelection(sel).
			WithSnapshots(holder), nil
```

Keep the exported `NewSearchServiceWithEmbedder(store, metaDir, emb)` as a wrapper that passes `embed.Selection{}` so existing tests compile unchanged. Keep `NewSearchService(store, metaDir)` as a wrapper that resolves the selection and, on a required-selection error, returns a lexical-only service after reporting to stderr — EXCEPT that a required selection must not silently degrade, so make it:

```go
// NewSearchService is the legacy env-reading entry point. It is retained for
// callers that have no resolved selection. A required selection that fails
// is FATAL to semantic search AND is reported; the caller receives a
// lexical-only service, which is safe only because a required profile's
// semantic operations then have no active embedder and fail closed in
// SemanticSearch. Prefer NewSearchServiceForSelection at a composition root.
func NewSearchService(store graphstore.Graphstore, metaDir string) *search.Service {
	sel, err := ResolveSelection(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: embedder disabled: %v\n", err)
		return search.New(store).WithSelection(sel)
	}
	svc, serr := NewSearchServiceForSelection(context.Background(), store, metaDir, sel)
	if serr != nil {
		fmt.Fprintf(os.Stderr, "graphi: embedder disabled: %v\n", serr)
		return search.New(store).WithSelection(sel)
	}
	return svc
}
```

- [ ] **Step 4: Publish through the barrier on the index path**

Add to `cmd/internal/runtime/runtime.go`, next to `BuildSemanticGeneration`:

```go
// BuildSemanticGenerationForSelection is BuildSemanticGeneration driven by a
// resolved selection and the publication barrier. The build writes into a
// PRIVATE staging index; the live snapshot is swapped only after the durable
// commit succeeds.
func BuildSemanticGenerationForSelection(
	ctx context.Context,
	ing *ingest.Ingester,
	graphStore graphstore.Graphstore,
	sel embed.Selection,
	generationStore embed.GenerationStore,
	docs embed.DocumentSource,
	publisher embed.SnapshotPublisher,
	progress embed.GenerationProgressFunc,
) (embed.GenerateResult, error) {
	if ing == nil {
		return embed.GenerateResult{}, fmt.Errorf("runtime: BuildSemanticGenerationForSelection: nil ingester")
	}
	if _, ok := sel.Embedder(); !ok {
		if sel.Required() {
			return embed.GenerateResult{}, embed.NewCauseError(embed.CauseProfileInvalid, "semantic build",
				"export GRAPHI_EMBEDDER=coderank:/absolute/path/to/coderank.json and start the pinned sidecar on loopback",
				"the required CodeRank profile did not resolve to an embedder", nil)
		}
		return embed.GenerateResult{Configured: false}, nil
	}
	if generationStore == nil {
		return embed.GenerateResult{}, fmt.Errorf("runtime: BuildSemanticGenerationForSelection: nil generation store")
	}
	release, err := acquireIngestLock(ctx, ing.MetaDir(), nil)
	if err != nil {
		return embed.GenerateResult{}, fmt.Errorf("acquire ingest lock for semantic: %w", err)
	}
	defer release()
	nodes, err := graphStore.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return embed.GenerateResult{}, fmt.Errorf("runtime: BuildSemanticGenerationForSelection: snapshot under lock: %w", err)
	}
	sortNodesForBuild(nodes)
	graphGen, gerr := graphGenerationFromStore(ctx, graphStore)
	if gerr != nil {
		return embed.GenerateResult{}, fmt.Errorf("runtime: BuildSemanticGenerationForSelection: read graph identity: %w", gerr)
	}
	return embed.GenerateAndPublish(ctx, embed.GenerateOptions{
		Registry:        sel.Registry(),
		Nodes:           nodes,
		Docs:            docs,
		Store:           generationStore,
		GraphGeneration: graphGen,
		Progress:        progress,
		Publisher:       publisher,
	})
}
```

Extract the existing in-place node sort in `BuildSemanticGeneration` into `func sortNodesForBuild(nodes []model.Node)` and call it from both functions so the two paths cannot drift.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./cmd/internal/runtime/ -run 'TestResolveSelection|TestNewSearchServiceForSelection' -v`
Expected: PASS (3 tests).

- [ ] **Step 6: Pass the resolved selection to every surface**

In `cmd/graphi/semantic.go`, replace `runtimeEmbedderRegistryFromEnv` with:

```go
// selectionFromEnv resolves the process-wide profile selection once. Every
// surface (CLI, MCP, HTTP, daemon, zero-config) takes its registry from the
// SAME resolved value, so the three surfaces can never disagree about which
// profile is selected.
func selectionFromEnv(ctx context.Context) (embed.Selection, error) {
	return rtime.ResolveSelection(ctx)
}

// runtimeEmbedderRegistryFromEnv is retained as the registry accessor the
// surface constructors take. It reports the resolution error to stderr and
// returns the graceful-skip registry; a REQUIRED profile that failed still
// reads as unconfigured here, and the status document names the cause.
func runtimeEmbedderRegistryFromEnv(ctx context.Context) (*embed.Registry, embed.Selection) {
	sel, err := selectionFromEnv(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: embedder disabled: %v\n", err)
		return embed.NewRegistry(), sel
	}
	return sel.Registry(), sel
}
```

Update the three call sites to pass a context and the selection:

- `cmd/graphi/serve.go:62`:
  ```go
  	reg, sel := runtimeEmbedderRegistryFromEnv(ctx)
  	options = append(options, mcp.WithEmbedderRegistry(reg), mcp.WithSelection(sel))
  ```
- `cmd/graphi/serve.go:418`:
  ```go
  	reg, sel := runtimeEmbedderRegistryFromEnv(runCtx)
  	srv := httpsrv.New(c, broker).WithWiki(store).WithDescriptors(asvc.Names()).
  		WithEmbedderRegistry(reg).WithSelection(sel)
  ```
- `cmd/graphi/zeroconfig.go:123`: the same two-line form as the HTTP site above.

Add `WithSelection` to both surfaces:

- `surfaces/mcp/mcp.go`: a `selection embed.Selection` field, `func WithSelection(sel embed.Selection) ServerOption`, and `func (s *Server) selectionForCall() embed.Selection`.
- `surfaces/http/server.go`: a `selection embed.Selection` field and `func (s *Server) WithSelection(sel embed.Selection) *Server`.

- [ ] **Step 7: Drive the index path through the barrier**

In `cmd/graphi/query.go`, replace the `rtime.BuildSemanticGeneration(...)` call at line 357 with:

```go
	sel, selErr := selectionFromEnv(ctx)
	if selErr != nil {
		fmt.Fprintf(os.Stderr, "graphi: index --semantic: %v\n", selErr)
		return 2
	}
	res, err := rtime.BuildSemanticGenerationForSelection(ctx, ing, store, sel, table, docs, embed.NewSnapshotHolder(), eprog.Handle)
```

The CLI's holder is process-local and discarded when the command exits; the durable generation is the source of truth the next process reloads. Keep it explicit rather than passing nil so the barrier runs on every build path.

- [ ] **Step 8: Run the affected suites**

Run: `go build ./...`
Expected: builds.

Run: `go test ./cmd/... ./surfaces/...`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add cmd/internal/runtime/runtime.go cmd/internal/runtime/selection_test.go cmd/graphi/semantic.go cmd/graphi/serve.go cmd/graphi/zeroconfig.go cmd/graphi/query.go surfaces/mcp/mcp.go surfaces/http/server.go
git commit -m "feat(cmd): resolve one immutable embedder selection for every surface"
```

---

## Task 10: The semantic-status document gains selection, session, fingerprint and repair

**Files:**
- Modify: `engine/embed/status.go`
- Modify: `engine/embed/status_test.go`
- Modify: `surfaces/client/semantic_status.go`
- Modify: `cmd/graphi/semantic_surfaces_test.go`

**Interfaces:**
- Consumes: `embed.Selection` (Task 2), `embed.SessionReporter` (Task 4), `embed.CauseOf` (Task 1).
- Produces: `Status.Selection StatusSelection` with `{Profile string; Explicit bool; Required bool}`; `Status.Session string`; `Status.Fingerprint string`; `Status.Cause Cause`; `func LoadStatusForSelection(ctx context.Context, metaDir string, sel Selection, graphGeneration string, nodes NodeReferencer) Status`; `SemanticStatusOptions.Selection embed.Selection`; the wire document gains `selection`, `session`, `fingerprint` and `cause`.

- [ ] **Step 1: Write the failing test**

Append to `engine/embed/status_test.go`:

```go
func TestLoadStatusForSelection_CodeRankReportsSelectionSessionAndFingerprint(t *testing.T) {
	ctx := context.Background()
	f := &statusAttestedFake{dim: 4, session: SessionBound}
	sel := SelectionForTest(SelectionCodeRankRequired, "coderank", f)

	st := LoadStatusForSelection(ctx, t.TempDir(), sel, "g1", nil)

	if st.Selection.Profile != "coderank" || !st.Selection.Explicit || !st.Selection.Required {
		t.Fatalf("selection = %+v, want coderank/explicit/required", st.Selection)
	}
	if st.Session != SessionBound {
		t.Fatalf("session = %q, want %q", st.Session, SessionBound)
	}
	if st.Fingerprint == "" {
		t.Fatalf("status carries no durable fingerprint")
	}
	if st.Repair == "" {
		t.Fatalf("a non-ready status carries no repair action")
	}
}

func TestLoadStatusForSelection_NeverEmitsARawEpoch(t *testing.T) {
	ctx := context.Background()
	f := &statusAttestedFake{dim: 4, session: SessionBound, epoch: "pid-4242-boot-1789000000"}
	sel := SelectionForTest(SelectionCodeRankRequired, "coderank", f)

	st := LoadStatusForSelection(ctx, t.TempDir(), sel, "g1", nil)
	blob := st.Model.ID + st.Model.Revision + st.Model.SHA256 + st.Fingerprint + st.Repair + st.Session + string(st.Cause)
	if strings.Contains(blob, "pid-4242") {
		t.Fatalf("status leaked a raw process epoch: %q", blob)
	}
}

func TestLoadStatusForSelection_UnconfiguredKeepsTheFirstRunShape(t *testing.T) {
	st := LoadStatusForSelection(context.Background(), "", Selection{}, "", nil)
	if st.Configured || st.Selection.Profile != "" || st.Selection.Required {
		t.Fatalf("unconfigured status = %+v", st)
	}
	if st.Session != SessionUnbound {
		t.Fatalf("session = %q, want %q", st.Session, SessionUnbound)
	}
	if st.Repair != setupRepair() {
		t.Fatalf("repair = %q, want %q", st.Repair, setupRepair())
	}
}
```

Add a `statusAttestedFake` to the same file implementing `ID`, `Dim`, `Embed`, `SessionState` and, when `epoch != ""`, `ExpectedRuntimeAttestation` / `RuntimeAttestation`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./engine/embed/ -run TestLoadStatusForSelection -v`
Expected: FAIL — `undefined: LoadStatusForSelection`, `st.Selection undefined`.

- [ ] **Step 3: Extend the engine-owned Status**

In `engine/embed/status.go`, add the fields to `Status` after `State`:

```go
	// Selection is the resolved profile selection: which profile was
	// chosen, whether the operator chose it explicitly, and whether
	// semantic operations are fail-closed.
	Selection StatusSelection `json:"selection"`
	// Session is the attested adapter's session state
	// (unbound|bound|poisoned). Always present; "unbound" when the active
	// embedder exposes no session.
	Session string `json:"session"`
	// Fingerprint is the REQUESTED durable fingerprint's canonical form.
	// It is the identity a persisted vector must match byte-for-byte.
	Fingerprint string `json:"fingerprint"`
	// Cause is the stable failure cause when one applies. Empty on a ready
	// status. It never carries a digest, an epoch or an endpoint.
	Cause Cause `json:"cause,omitempty"`
```

and the type:

```go
// StatusSelection is the selection block of the status document.
type StatusSelection struct {
	Profile  string `json:"profile"`
	Explicit bool   `json:"explicit"`
	Required bool   `json:"required"`
}
```

Add the selection-aware loader, keeping `LoadStatus` as a wrapper so existing callers compile:

```go
// LoadStatusForSelection composes the canonical Status from the RESOLVED
// selection rather than from a bare registry, so the document can report the
// profile, the explicit/required flags and the adapter session. Like
// LoadStatus it never returns an error: every failure path fails closed to a
// typed shape.
func LoadStatusForSelection(ctx context.Context, metaDir string, sel Selection, graphGeneration string, nodes NodeReferencer) Status {
	s := loadStatus(ctx, metaDir, sel.Registry(), graphGeneration, nodes)
	s.Selection = StatusSelection{Profile: sel.Profile(), Explicit: sel.Explicit(), Required: sel.Required()}
	s.Session = SessionUnbound
	if emb, ok := sel.Embedder(); ok {
		if sr, ok := emb.(SessionReporter); ok {
			s.Session = sr.SessionState()
		}
		s.Fingerprint = fingerprintForEmbedder(emb, graphGeneration).Canonical()
	}
	if s.State != StateReady {
		s.Cause = causeForStatus(s.State, s.Session)
	}
	return s
}

// LoadStatus retains the registry-only form for callers that have no
// resolved selection. It reports the unconfigured selection block.
func LoadStatus(ctx context.Context, metaDir string, reg *Registry, graphGeneration string, nodes NodeReferencer) Status {
	s := loadStatus(ctx, metaDir, reg, graphGeneration, nodes)
	s.Session = SessionUnbound
	return s
}

// causeForStatus names the stable cause for a non-ready status. A poisoned
// session outranks the generation state: the operator's first action is to
// restart after the sidecar restart, not to re-index.
func causeForStatus(state State, session string) Cause {
	if session == SessionPoisoned {
		return CauseAttestationMismatch
	}
	switch state {
	case StateStale:
		return CauseGenerationStale
	case StateCorrupt:
		return CauseGenerationCorrupt
	default:
		return CauseGenerationMissing
	}
}
```

Also set `s.Fingerprint = fp.Canonical()` inside `loadStatus` right after `fp := fingerprintForEmbedder(...)` so the registry-only path carries it too.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./engine/embed/ -run TestLoadStatusForSelection -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Carry the new fields onto the canonical wire document**

In `surfaces/client/semantic_status.go`:

1. Bump the schema version, because four required fields are added:
   ```go
   const SemanticStatusJSONSchemaVersion = 2
   ```
2. Add `Selection embed.Selection` to `SemanticStatusOptions`.
3. Add the fields to `semanticStatusDoc`, in wire order, directly after `State`:
   ```go
   	Selection        embed.StatusSelection   `json:"selection"`
   	Session          string                  `json:"session"`
   	Fingerprint      string                  `json:"fingerprint"`
   	Cause            embed.Cause             `json:"cause,omitempty"`
   ```
4. In `composeSemanticStatus`, call `embed.LoadStatusForSelection(ctx, metaDir, opts.Selection, graphGeneration, nodes)` when `opts.Selection.Explicit()` or `opts.Embedder == nil`, and populate the four new doc fields from it.

- [ ] **Step 6: Pass the selection from all three surfaces**

- `cmd/graphi/semantic.go`: add `Selection: sel` to the `client.SemanticStatusOptions{...}` literal, taking `sel` from `runtimeEmbedderRegistryFromEnv`.
- `surfaces/mcp/toolcalls.go:1098`: add `Selection: s.selectionForCall()`.
- `surfaces/http/handlers.go:255`: add `Selection: s.selection`.

- [ ] **Step 7: Extend the three-surface goldens**

In `cmd/graphi/semantic_surfaces_test.go`, extend `TestSemanticStatus_FiveStateGoldensAcrossCLIMCPHTTP` with two CodeRank cases — `coderank_required` + `bound` + `ready`, and `coderank_required` + `poisoned` — and assert, for every case, that the CLI, MCP and HTTP byte streams are identical and that no golden contains a 64-hex run outside the `fingerprint` field or the substring `pid-`.

- [ ] **Step 8: Run the surface suites**

Run: `go test ./engine/embed/ ./surfaces/... ./cmd/graphi/`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add engine/embed/status.go engine/embed/status_test.go surfaces/client/semantic_status.go surfaces/mcp/toolcalls.go surfaces/http/handlers.go cmd/graphi/semantic.go cmd/graphi/semantic_surfaces_test.go
git commit -m "feat(surfaces): report selection, session, durable fingerprint and cause in semantic status"
```

---

## Task 11: Evaluation constructs CodeRank through the public selector

**Files:**
- Modify: `internal/eval/retrieval/model_qualification.go`
- Modify: `internal/eval/retrieval/model_qualification_measure.go`
- Modify: `internal/eval/retrieval/taskcontext.go`

**Interfaces:**
- Consumes: `embed.ResolveSelection`, `embed.DefaultContextConstructors`, `embed.Selection` (Task 2); `embed.SessionReporter` (Task 4); `coderank.Scheme` (Task 3).
- Produces: `func qualificationSelector(manifestPath string) string`; `qualificationArmEmbedder` returns `(embed.Embedder, embed.Selection, *embed.Fingerprint, []byte, error)`; `func validateCaptureRunValidity(state embed.State, requested, active embed.Fingerprint, session string, degradation string) error`.

- [ ] **Step 1: Write the failing test**

Append to `internal/eval/retrieval/model_qualification_test.go`:

```go
func TestQualificationSelector_IsTheAbsoluteManifestProductSelector(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coderank.json")
	got := qualificationSelector(path)
	want := "coderank:" + path
	if got != want {
		t.Fatalf("qualificationSelector = %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, coderank.Scheme+":") {
		t.Fatalf("the evaluation selector does not use the product scheme")
	}
}

func TestValidateCaptureRunValidity_RejectsEveryNonQualifyingCapture(t *testing.T) {
	fp := embed.Fingerprint{ModelID: "coderank:m@r:d", Dim: 768, DocumentSchema: embed.DocumentSchema, GraphGeneration: "g1"}
	other := fp
	other.GraphGeneration = "g2"

	cases := []struct {
		name        string
		state       embed.State
		active      embed.Fingerprint
		session     string
		degradation string
	}{
		{"not ready", embed.StateStale, fp, embed.SessionBound, "none"},
		{"fingerprint drift", embed.StateReady, other, embed.SessionBound, "none"},
		{"unbound session", embed.StateReady, fp, embed.SessionUnbound, "none"},
		{"poisoned session", embed.StateReady, fp, embed.SessionPoisoned, "none"},
		{"degraded capture", embed.StateReady, fp, embed.SessionBound, "lexical_only"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateCaptureRunValidity(tc.state, fp, tc.active, tc.session, tc.degradation); err == nil {
				t.Fatalf("%s was accepted as a valid capture", tc.name)
			}
		})
	}
	if err := validateCaptureRunValidity(embed.StateReady, fp, fp, embed.SessionBound, "none"); err != nil {
		t.Fatalf("a fully valid capture was rejected: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/eval/retrieval/ -run 'TestQualificationSelector|TestValidateCaptureRunValidity' -v`
Expected: FAIL — `undefined: qualificationSelector`, `undefined: validateCaptureRunValidity`.

- [ ] **Step 3: Route the CodeRank arm through the public selector**

In `internal/eval/retrieval/model_qualification.go`:

```go
// qualificationSelector renders the PRODUCT selector for a manifest. After
// product integration, evaluation constructs CodeRank through the public
// selection path and retains no evaluation-only adapter: the arm the
// qualification measures is the arm the operator gets.
func qualificationSelector(manifestPath string) string {
	return coderank.Scheme + ":" + manifestPath
}
```

Change the `ArmCodeRank` branch of `qualificationArmEmbedder` to:

```go
	case ArmCodeRank:
		abs, aerr := filepath.Abs(manifestPath)
		if aerr != nil {
			return nil, embed.Selection{}, nil, nil, fmt.Errorf("embedded-model qualification capture: resolve manifest path: %w", aerr)
		}
		abs = filepath.Clean(abs)
		loadedManifest, err = readStableQualificationManifest(abs, pin.ManifestSHA256, func() error {
			sel, serr := embed.ResolveSelection(ctx, qualificationSelector(abs), embed.DefaultContextConstructors())
			if serr != nil {
				return serr
			}
			if !sel.Required() {
				return fmt.Errorf("the coderank selector did not resolve to a required profile")
			}
			selection = sel
			emb, _ = sel.Embedder()
			return nil
		})
```

Widen the signature to `func qualificationArmEmbedder(ctx context.Context, arm QualificationArm, pre QualificationPreregistration, manifestPath string) (embed.Embedder, embed.Selection, *embed.Fingerprint, []byte, error)`, declare `var selection embed.Selection` at the top, return it from every branch (the zero value for the Potion and lexical arms), and update every call site. Delete the direct `coderank.NewFromManifest` call: the public selector is now the only construction path in evaluation.

- [ ] **Step 4: Do the same in the operating-measurement path**

In `internal/eval/retrieval/model_qualification_measure.go`, replace the `coderank.NewFromManifest(ctx, options.ManifestPath)` call at line 95 with the same `embed.ResolveSelection(ctx, qualificationSelector(abs), embed.DefaultContextConstructors())` form, then type-assert the selected embedder to the operating-attestation capability:

```go
			sel, serr := embed.ResolveSelection(ctx, qualificationSelector(abs), embed.DefaultContextConstructors())
			if serr != nil {
				constructErr = serr
				return constructErr
			}
			emb, _ := sel.Embedder()
			oa, ok := emb.(interface {
				OperatingAttestation(context.Context) (coderank.OperatingAttestation, error)
			})
			if !ok {
				constructErr = fmt.Errorf("the selected coderank embedder exposes no operating attestation")
				return constructErr
			}
			sidecar = oa
```

Change `sidecar`'s declared type from `*coderank.Embedder` to that same anonymous interface (or a named `operatingAttestor` interface declared in the file), so the measurement path holds only the capability it needs and never a concrete evaluation-only adapter.

- [ ] **Step 5: Add the capture run-validity check**

Add to `internal/eval/retrieval/model_qualification.go`:

```go
// validateCaptureRunValidity invalidates a COMPLETE run after any query that
// lacked StateReady, the expected fingerprint, a bound session, or
// degradation == none. There is no partial credit: a single failing capture
// invalidates the run rather than being dropped from the population.
func validateCaptureRunValidity(state embed.State, requested, active embed.Fingerprint, session, degradation string) error {
	if state != embed.StateReady {
		return fmt.Errorf("capture is invalid: generation state is %s, want ready", state)
	}
	if active.Canonical() != requested.Canonical() {
		return fmt.Errorf("capture is invalid: the active generation fingerprint differs from the requested fingerprint")
	}
	if session != embed.SessionBound {
		return fmt.Errorf("capture is invalid: adapter session is %s, want bound", session)
	}
	if degradation != "none" {
		return fmt.Errorf("capture is invalid: degradation is %q, want none", degradation)
	}
	return nil
}
```

- [ ] **Step 6: Call it from the capture path**

In `internal/eval/retrieval/taskcontext.go`, after the existing `state != embed.StateReady` check around line 1047, replace that block with a call to the shared validator, sourcing the session from the embedder and the degradation from the retrieval result:

```go
	session := embed.SessionUnbound
	if sr, ok := emb.(embed.SessionReporter); ok {
		session = sr.SessionState()
	}
	if verr := validateCaptureRunValidity(state, fp, gen.Fingerprint, session, "none"); verr != nil {
		return nil, fmt.Errorf("task-context eval: %w", verr)
	}
```

Keep the existing "refusing fallback" semantics: the function still returns an error rather than degrading.

- [ ] **Step 7: Run test to verify it passes**

Run: `go test ./internal/eval/retrieval/ -run 'TestQualificationSelector|TestValidateCaptureRunValidity' -v`
Expected: PASS (2 tests plus 5 subtests).

- [ ] **Step 8: Prove no evaluation-only CodeRank constructor remains**

Run: `grep -rn "coderank.NewFromManifest" --include="*.go" internal/ cmd/`
Expected: no matches outside `engine/embed/coderank`.

- [ ] **Step 9: Run the evaluation suite**

Run: `go test ./internal/eval/retrieval/`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/eval/retrieval/model_qualification.go internal/eval/retrieval/model_qualification_measure.go internal/eval/retrieval/model_qualification_test.go internal/eval/retrieval/taskcontext.go
git commit -m "refactor(eval): construct CodeRank through the public product selector only"
```

---

## Task 12: Product parity between the qualified adapter and the public product path

**Files:**
- Create: `internal/eval/retrieval/product_parity.go`
- Create: `internal/eval/retrieval/product_parity_test.go`

**Interfaces:**
- Consumes: `embed.ResolveSelection` (Task 2), `embed.Fingerprint`, `embed.Row`, `embed.GenerateAndPublish` (Task 6), `qualificationSelector` (Task 11), `SHA256Hex` (existing in this package).
- Produces: `type ProductParityObservation struct {...}`; `type ProductParityReport struct {...}`; `func CompareProductParity(qualified, product ProductParityObservation) ProductParityReport`; `func (ProductParityReport) Identical() bool`.

- [ ] **Step 1: Write the failing test**

Create `internal/eval/retrieval/product_parity_test.go`:

```go
package retrieval

import (
	"strings"
	"testing"
)

func parityObservation() ProductParityObservation {
	return ProductParityObservation{
		AdmittedBytesSHA256:   strings.Repeat("1", 64),
		AdmittedTokenCounts:   []int{120, 340},
		DocumentVectorsSHA256: strings.Repeat("2", 64),
		QueryVectorsSHA256:    strings.Repeat("3", 64),
		DurableFingerprint:    "8:model-id\n3:rev",
		PersistedRowsSHA256:   strings.Repeat("4", 64),
		SemanticTop50SHA256:   strings.Repeat("5", 64),
		FusedCandidatesSHA256: strings.Repeat("6", 64),
		BundleSHA256:          strings.Repeat("7", 64),
		BundleTokens:          1200,
		DiagnosticsSHA256:     strings.Repeat("8", 64),
		BlindDecisionsSHA256:  strings.Repeat("9", 64),
	}
}

func TestCompareProductParity_IdenticalObservationsPass(t *testing.T) {
	rep := CompareProductParity(parityObservation(), parityObservation())
	if !rep.Identical() {
		t.Fatalf("identical observations reported differences: %+v", rep.Differences)
	}
	if len(rep.Differences) != 0 {
		t.Fatalf("differences = %v, want none", rep.Differences)
	}
}

func TestCompareProductParity_EveryComparedFieldIsChecked(t *testing.T) {
	mutations := map[string]func(*ProductParityObservation){
		"admitted_bytes":    func(o *ProductParityObservation) { o.AdmittedBytesSHA256 = strings.Repeat("a", 64) },
		"admitted_tokens":   func(o *ProductParityObservation) { o.AdmittedTokenCounts = []int{120, 341} },
		"document_vectors":  func(o *ProductParityObservation) { o.DocumentVectorsSHA256 = strings.Repeat("a", 64) },
		"query_vectors":     func(o *ProductParityObservation) { o.QueryVectorsSHA256 = strings.Repeat("a", 64) },
		"durable_fingerprint": func(o *ProductParityObservation) { o.DurableFingerprint = "different" },
		"persisted_rows":    func(o *ProductParityObservation) { o.PersistedRowsSHA256 = strings.Repeat("a", 64) },
		"semantic_top50":    func(o *ProductParityObservation) { o.SemanticTop50SHA256 = strings.Repeat("a", 64) },
		"fused_candidates":  func(o *ProductParityObservation) { o.FusedCandidatesSHA256 = strings.Repeat("a", 64) },
		"bundle_bytes":      func(o *ProductParityObservation) { o.BundleSHA256 = strings.Repeat("a", 64) },
		"bundle_tokens":     func(o *ProductParityObservation) { o.BundleTokens = 1199 },
		"diagnostics":       func(o *ProductParityObservation) { o.DiagnosticsSHA256 = strings.Repeat("a", 64) },
		"blind_decisions":   func(o *ProductParityObservation) { o.BlindDecisionsSHA256 = strings.Repeat("a", 64) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			product := parityObservation()
			mutate(&product)
			rep := CompareProductParity(parityObservation(), product)
			if rep.Identical() {
				t.Fatalf("mutating %s was not detected", name)
			}
			found := false
			for _, d := range rep.Differences {
				if d == name {
					found = true
				}
			}
			if !found {
				t.Fatalf("differences = %v, want %q", rep.Differences, name)
			}
		})
	}
}

func TestProductParity_BundleCeilingIsExactlyTwelveHundredTokens(t *testing.T) {
	o := parityObservation()
	o.BundleTokens = 1201
	if err := ValidateProductParityObservation(o); err == nil {
		t.Fatalf("a bundle above the 1,200-token ceiling was accepted")
	}
	o.BundleTokens = 1200
	if err := ValidateProductParityObservation(o); err != nil {
		t.Fatalf("an exact 1,200-token bundle was rejected: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/eval/retrieval/ -run 'TestCompareProductParity|TestProductParity' -v`
Expected: FAIL — `undefined: ProductParityObservation`, `undefined: CompareProductParity`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/eval/retrieval/product_parity.go`:

```go
package retrieval

import (
	"fmt"
	"sort"
)

// ProductParityBundleCeiling is the primary serialized bundle ceiling. It is
// exactly 1,200 cl100k_base tokens and is not changed by product integration.
const ProductParityBundleCeiling = 1200

// ProductParityObservation is one path's complete observable output over the
// frozen development inputs. Every field is either a digest of canonical
// bytes or an exact integer: the comparison is byte-identity, not similarity.
type ProductParityObservation struct {
	// AdmittedBytesSHA256 digests the concatenated admitted document bytes
	// in canonical document order.
	AdmittedBytesSHA256 string `json:"admitted_bytes_sha256"`
	// AdmittedTokenCounts is the exact per-document admitted token count,
	// in the same canonical order.
	AdmittedTokenCounts []int `json:"admitted_token_counts"`
	// DocumentVectorsSHA256 digests every document vector.
	DocumentVectorsSHA256 string `json:"document_vectors_sha256"`
	// QueryVectorsSHA256 digests every query vector.
	QueryVectorsSHA256 string `json:"query_vectors_sha256"`
	// DurableFingerprint is the canonical fingerprint string, compared
	// verbatim rather than as a digest so a mismatch is readable.
	DurableFingerprint string `json:"durable_fingerprint"`
	// PersistedRowsSHA256 digests the persisted generation rows in
	// canonical (node_id, document_id) order.
	PersistedRowsSHA256 string `json:"persisted_rows_sha256"`
	// SemanticTop50SHA256 digests the semantic top-50 result list per query.
	SemanticTop50SHA256 string `json:"semantic_top50_sha256"`
	// FusedCandidatesSHA256 digests the fused candidate list per query.
	FusedCandidatesSHA256 string `json:"fused_candidates_sha256"`
	// BundleSHA256 digests the serialized bundle bytes per query.
	BundleSHA256 string `json:"bundle_sha256"`
	// BundleTokens is the exact bundle token count.
	BundleTokens int `json:"bundle_tokens"`
	// DiagnosticsSHA256 digests the per-query diagnostics document.
	DiagnosticsSHA256 string `json:"diagnostics_sha256"`
	// BlindDecisionsSHA256 digests the final blind decisions.
	BlindDecisionsSHA256 string `json:"blind_decisions_sha256"`
}

// ProductParityReport names every compared field that differed. An empty
// Differences slice is the only passing outcome.
type ProductParityReport struct {
	Differences []string `json:"differences"`
}

// Identical reports byte-identity across every compared field.
func (r ProductParityReport) Identical() bool { return len(r.Differences) == 0 }

// ValidateProductParityObservation enforces the invariants an observation
// must satisfy before it can be compared at all. The bundle ceiling is
// exactly 1,200 tokens; product integration does not change that contract.
func ValidateProductParityObservation(o ProductParityObservation) error {
	if o.BundleTokens <= 0 || o.BundleTokens > ProductParityBundleCeiling {
		return fmt.Errorf("product parity: bundle token count %d must be in 1..%d", o.BundleTokens, ProductParityBundleCeiling)
	}
	return nil
}

// CompareProductParity compares the qualified adapter path against the public
// product path on identical frozen development inputs. Every listed field
// must be byte-identical; a single difference blocks the product-path
// requalification gate.
func CompareProductParity(qualified, product ProductParityObservation) ProductParityReport {
	var diffs []string
	add := func(name string, equal bool) {
		if !equal {
			diffs = append(diffs, name)
		}
	}
	add("admitted_bytes", qualified.AdmittedBytesSHA256 == product.AdmittedBytesSHA256)
	add("admitted_tokens", equalInts(qualified.AdmittedTokenCounts, product.AdmittedTokenCounts))
	add("document_vectors", qualified.DocumentVectorsSHA256 == product.DocumentVectorsSHA256)
	add("query_vectors", qualified.QueryVectorsSHA256 == product.QueryVectorsSHA256)
	add("durable_fingerprint", qualified.DurableFingerprint == product.DurableFingerprint)
	add("persisted_rows", qualified.PersistedRowsSHA256 == product.PersistedRowsSHA256)
	add("semantic_top50", qualified.SemanticTop50SHA256 == product.SemanticTop50SHA256)
	add("fused_candidates", qualified.FusedCandidatesSHA256 == product.FusedCandidatesSHA256)
	add("bundle_bytes", qualified.BundleSHA256 == product.BundleSHA256)
	add("bundle_tokens", qualified.BundleTokens == product.BundleTokens)
	add("diagnostics", qualified.DiagnosticsSHA256 == product.DiagnosticsSHA256)
	add("blind_decisions", qualified.BlindDecisionsSHA256 == product.BlindDecisionsSHA256)
	sort.Strings(diffs)
	return ProductParityReport{Differences: diffs}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/eval/retrieval/ -run 'TestCompareProductParity|TestProductParity' -v`
Expected: PASS (3 tests plus 12 subtests).

- [ ] **Step 5: Add the environment-gated live parity capture**

Append to `internal/eval/retrieval/product_parity_test.go` an environment-gated capture that runs both paths over the frozen development inputs and compares them, mirroring the gating convention already used by `model_qualification_capture_test.go`:

```go
func TestProductParity_QualifiedAndProductPathsAreByteIdentical(t *testing.T) {
	manifest := os.Getenv("GRAPHI_CODERANK_MANIFEST")
	if manifest == "" {
		t.Skip("set GRAPHI_CODERANK_MANIFEST to an absolute pinned manifest to run the live product-parity capture")
	}
	// Both observations come from the SAME public selector; the "qualified"
	// side replays the frozen development capture and the "product" side
	// runs the shipped path. Capture helpers live alongside the existing
	// qualification capture harness — reuse them rather than duplicating.
	qualified := captureFrozenDevelopmentObservation(t, manifest)
	product := captureProductPathObservation(t, manifest)
	if err := ValidateProductParityObservation(product); err != nil {
		t.Fatalf("product observation: %v", err)
	}
	rep := CompareProductParity(qualified, product)
	if !rep.Identical() {
		t.Fatalf("product path diverges from the qualified path: %v", rep.Differences)
	}
}
```

Implement both capture helpers in the same file with this exact signature and contract:

```go
// captureFrozenDevelopmentObservation replays the frozen development capture
// for the M3_coderank arm: it constructs the arm through
// qualificationArmEmbedder (which now resolves the public selector), runs the
// preregistered 64-query dataset, and digests the twelve compared fields in
// canonical order.
func captureFrozenDevelopmentObservation(t *testing.T, manifestPath string) ProductParityObservation

// captureProductPathObservation runs the SAME 64 queries through the shipped
// product path: runtime.ResolveSelection over the same absolute manifest,
// runtime.BuildSemanticGenerationForSelection for the generation, the search
// service's attested query boundary for retrieval, and the production bundle
// serializer for the 1,200-token bundles. It digests the same twelve fields
// in the same canonical order.
func captureProductPathObservation(t *testing.T, manifestPath string) ProductParityObservation
```

Both must build every digest with `SHA256Hex` over canonically ordered bytes (documents in `(node_id, document_id)` order, queries in dataset order), and neither may construct CodeRank by any route other than the public selector. Where the two helpers would share digesting logic, factor it into one `func digestParityFields(...) ProductParityObservation` so the two sides cannot drift in how they hash.

- [ ] **Step 6: Run the gated test both ways**

Run: `go test ./internal/eval/retrieval/ -run TestProductParity_QualifiedAndProductPathsAreByteIdentical -v`
Expected: SKIP without the environment variable set.

With a live pinned sidecar and `GRAPHI_CODERANK_MANIFEST` exported to an absolute canonical manifest path: Expected PASS with zero differences.

- [ ] **Step 7: Commit**

```bash
git add internal/eval/retrieval/product_parity.go internal/eval/retrieval/product_parity_test.go
git commit -m "test(eval): compare the qualified adapter and public product paths byte-for-byte"
```

---

## Task 13: Product-path development requalification decision and report

**Files:**
- Modify: `internal/eval/retrieval/model_qualification_stats.go`
- Modify: `internal/eval/retrieval/model_qualification_stats_test.go`
- Modify: `internal/eval/retrieval/model_qualification_report.go`
- Modify: `internal/eval/retrieval/model_qualification_report_test.go`

**Interfaces:**
- Consumes: `EvaluateQualification`, `QualificationInput`, `QualificationDecision`, `GateResult`, `gate` (existing); `ProductParityReport` (Task 12).
- Produces: `type QualificationPath string` with `PathDevelopment = "development"` and `PathProduct = "product"`; `QualificationInput.Path QualificationPath`; `QualificationInput.ProductParity ProductParityReport`; the `product_path_parity` gate; `QualificationReport.Path`; the `PRODUCT REQUALIFICATION: YES|NO` headline.

- [ ] **Step 1: Write the failing test**

Append to `internal/eval/retrieval/model_qualification_stats_test.go`:

```go
func TestEvaluateQualification_ProductPathAddsTheParityGate(t *testing.T) {
	in := passingQualificationInput(t) // the existing all-gates-pass fixture
	in.Path = PathProduct
	in.ProductParity = ProductParityReport{}

	decision, err := EvaluateQualification(in)
	if err != nil {
		t.Fatalf("EvaluateQualification: %v", err)
	}
	if !decision.Promote {
		t.Fatalf("a passing product-path run did not promote; gates=%+v", decision.Gates)
	}
	if !hasGate(decision.Gates, "product_path_parity") {
		t.Fatalf("the product path did not add the parity gate")
	}
}

func TestEvaluateQualification_ProductPathParityDifferenceBlocksPromotion(t *testing.T) {
	in := passingQualificationInput(t)
	in.Path = PathProduct
	in.ProductParity = ProductParityReport{Differences: []string{"semantic_top50"}}

	decision, err := EvaluateQualification(in)
	if err != nil {
		t.Fatalf("EvaluateQualification: %v", err)
	}
	if decision.Promote {
		t.Fatalf("a product path that diverges from the qualified path was promoted")
	}
}

func TestEvaluateQualification_DevelopmentPathGateListIsUnchanged(t *testing.T) {
	in := passingQualificationInput(t)
	in.Path = PathDevelopment

	decision, err := EvaluateQualification(in)
	if err != nil {
		t.Fatalf("EvaluateQualification: %v", err)
	}
	if hasGate(decision.Gates, "product_path_parity") {
		t.Fatalf("the development path gained a product-only gate")
	}
}
```

Add the `hasGate` helper to the same file. If `passingQualificationInput` does not already exist, extract the all-gates-pass fixture the file's existing combined-rule test builds into that helper rather than writing a second fixture.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/eval/retrieval/ -run TestEvaluateQualification_ProductPath -v`
Expected: FAIL — `in.Path undefined`, `undefined: PathProduct`.

- [ ] **Step 3: Write minimal implementation**

In `internal/eval/retrieval/model_qualification_stats.go`:

```go
// QualificationPath names which construction path produced the evidence.
type QualificationPath string

const (
	// PathDevelopment is the frozen four-arm development qualification that
	// produced DEVELOPMENT PROMOTION. Its gate list is byte-frozen so a
	// prior decision stays reproducible.
	PathDevelopment QualificationPath = "development"
	// PathProduct is the requalification run through the PUBLIC product
	// selector. It adds exactly one gate: byte parity with the qualified
	// path on identical frozen development inputs.
	PathProduct QualificationPath = "product"
)
```

Add to `QualificationInput`:

```go
	// Path selects the gate list. The zero value is PathDevelopment so
	// existing callers and stored reports are unchanged.
	Path QualificationPath `json:"path,omitempty"`
	// ProductParity is the direct parity comparison result. It is consulted
	// only on PathProduct.
	ProductParity ProductParityReport `json:"product_parity,omitempty"`
```

In `EvaluateQualification`, after the existing `gates := []GateResult{...}` literal:

```go
	if in.Path == PathProduct {
		// The product path must reproduce the promoted result exactly. A
		// single differing field means the shipped path is not the path the
		// development qualification measured.
		observed := "byte-identical"
		if !in.ProductParity.Identical() {
			observed = fmt.Sprintf("%d differing field(s): %v", len(in.ProductParity.Differences), in.ProductParity.Differences)
		}
		gates = append(gates, gate("product_path_parity", in.ProductParity.Identical(), observed,
			"byte-identical admitted bytes, vectors, fingerprints, rows, top-50, fused candidates, 1,200-token bundles, diagnostics and blind decisions"))
	}
	promote := true
	for _, result := range gates {
		promote = promote && result.Passed
	}
```

(Delete the pre-existing `promote` loop above so it is computed once, after the conditional append.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/eval/retrieval/ -run TestEvaluateQualification -v`
Expected: PASS, including the pre-existing development-path tests.

- [ ] **Step 5: Render the product-path headline**

In `internal/eval/retrieval/model_qualification_report.go`:

1. Add `Path QualificationPath `json:"path,omitempty"`` to `QualificationReport` and carry it through `canonicalQualificationReport`, `qualificationInputFromReport` and `deriveQualificationReport`.
2. Replace the headline block in `renderQualificationMarkdown`:

```go
	headline := "DEVELOPMENT PROMOTION"
	if report.Path == PathProduct {
		headline = "PRODUCT REQUALIFICATION"
	}
	if report.Decision.Promote {
		fmt.Fprintf(&out, "%s: YES\n", headline)
	} else {
		fmt.Fprintf(&out, "%s: NO\n", headline)
	}
```

3. Bump `QualificationReportSchemaVersion` from `3` to `4`, because the report gains two fields.

- [ ] **Step 6: Extend the golden report test**

In `internal/eval/retrieval/model_qualification_report_test.go`, add `"product_path_parity"`, `"path"` and `"product_parity"` to the required-substrings list, and add a case asserting that a `PathProduct` report renders `PRODUCT REQUALIFICATION: YES` while a `PathDevelopment` report still renders `DEVELOPMENT PROMOTION: YES`.

- [ ] **Step 7: Run the evaluation suite**

Run: `go test ./internal/eval/retrieval/`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/eval/retrieval/model_qualification_stats.go internal/eval/retrieval/model_qualification_stats_test.go internal/eval/retrieval/model_qualification_report.go internal/eval/retrieval/model_qualification_report_test.go
git commit -m "feat(eval): add the product-path requalification gate and report headline"
```

---

## Task 14: Product boundary conformance and operator documentation

**Files:**
- Create: `engine/embed/coderank/product_boundary_test.go`
- Modify: `internal/canary/gate.go`
- Modify: `docs/semantic-search.md`

**Interfaces:**
- Consumes: `embed.DefaultContextConstructors`, `embed.ResolveSelection` (Task 2); `coderank.Scheme` (Task 3).
- Produces: no new exported API.

- [ ] **Step 1: Write the failing test**

Create `engine/embed/coderank/product_boundary_test.go`:

```go
package coderank

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/embed"
)

func TestBoundary_PackageShipsNoModelOrRuntimeArtifacts(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			t.Fatalf("the adapter package must stay flat; found directory %q", name)
		}
		if !strings.HasSuffix(name, ".go") {
			t.Fatalf("the adapter package ships only Go source; found %q", name)
		}
	}
}

func TestBoundary_PackageLaunchesNoProcessAndReadsNoEnvironment(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	forbidden := map[string]string{
		"os/exec": "GrapHi does not install, launch, update, restart or supervise the sidecar",
		"os/user": "the adapter needs no user identity",
	}
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			for _, imp := range file.Imports {
				name := strings.Trim(imp.Path.Value, `"`)
				if why, bad := forbidden[name]; bad {
					t.Fatalf("%s imports %q: %s", filepath.Base(path), name, why)
				}
			}
			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				if ident.Name == "os" && (sel.Sel.Name == "Getenv" || sel.Sel.Name == "LookupEnv") {
					t.Fatalf("%s reads the environment; the strict manifest is the single configuration source", filepath.Base(path))
				}
				return true
			})
		}
	}
}

func TestBoundary_EmptySelectorConstructsNothingAndOpensNoSocket(t *testing.T) {
	table := embed.DefaultContextConstructors()
	sel, err := embed.ResolveSelection(context.Background(), "", table)
	if err != nil {
		t.Fatalf("ResolveSelection: %v", err)
	}
	if _, ok := sel.Embedder(); ok {
		t.Fatalf("the empty selector constructed an embedder")
	}
	if sel.Registry().Configured() {
		t.Fatalf("the empty selector produced a configured registry")
	}
}

func TestBoundary_CodeRankIsNeverADefaultRegistration(t *testing.T) {
	r := embed.NewDefaultRegistry()
	if r.Configured() {
		t.Fatalf("the default registry is configured")
	}
	for _, id := range r.IDs() {
		if strings.HasPrefix(id, Scheme+":") {
			t.Fatalf("coderank is registered as a default embedder: %q", id)
		}
	}
}

func TestBoundary_NoCgoEmbedderReachesTheDefaultRegistry(t *testing.T) {
	if offenders := embed.AssertNoCgoEmbedder(embed.NewDefaultRegistry()); len(offenders) != 0 {
		t.Fatalf("%s", embed.FormatCgoEmbedderFailure(offenders))
	}
}
```

- [ ] **Step 2: Run test to verify it fails or passes**

Run: `go test ./engine/embed/coderank/ -run TestBoundary -v`
Expected: PASS if the adapter already respects the boundary; a FAIL names the exact violation to fix before continuing. Do not weaken a test to make it pass.

- [ ] **Step 3: Restate the canary exemption for the product path**

In `internal/canary/gate.go`, replace the comment above `outboundDialExactCodeRank` (lines 137–143) with:

```go
// outboundDialExactAllowlist contains reviewed leaf packages whose exception
// must not be inherited by future descendants. CodeRank is the OPT-IN,
// EXPLICITLY SELECTED product profile: it is reached only through an
// absolute-manifest `GRAPHI_EMBEDDER=coderank:/abs/path` selector, accepts
// only a literal loopback origin (mapping localhost directly to 127.0.0.1),
// installs a no-proxy/no-DNS dialer, rejects redirects and credentials, and
// registers its scheme without ever becoming a default or an active
// embedder. Its adapter and boundary tests pin those properties. Keeping the
// entry EXACT means a future engine/embed/coderank/* transport is scanned
// normally instead of silently inheriting this local-IPC exception.
```

- [ ] **Step 4: Run the canary suite**

Run: `go test ./internal/canary/`
Expected: PASS — the exemption's shape is unchanged; only its justification text is.

- [ ] **Step 5: Document the operator lifecycle**

Append a `## CodeRank (optional, loopback sidecar)` section to `docs/semantic-search.md` containing, verbatim from the spec:

1. The selector form and the absolute-canonical path rule.
2. The first-activation sequence: provision the pinned model tree and pinned Python runtime separately; verify the manifest and artifacts with the sidecar tool; start the sidecar manually on a literal loopback address; export the explicit CodeRank selector; check `graphi semantic status` for `coderank`, `bound`, and the expected fingerprint; run `graphi index --semantic`; check `StateReady` and the active generation before querying. State plainly that GrapHi performs none of these repair or lifecycle actions automatically.
3. The change matrix as a Markdown table with all nine rows (GrapHi restart only; sidecar restart with identical identity; loopback endpoint change only; model/revision/model digest; tokenizer or admission; runtime pin/precision/normalization; query instruction or query profile; graph generation only; persisted row corruption) and their three columns (durable fingerprint, persisted vectors, required action).
4. The eight stable error causes with the operator action for each.
5. A statement that an explicitly selected CodeRank profile never falls back to Potion or lexical-only retrieval, that Potion remains the daemonless standard `setup-embedder` profile, and that GrapHi ships neither CodeRank weights nor Python/PyTorch/Transformers/SentenceTransformers runtimes.
6. A statement that the design claims no protection against a deliberately malicious local sidecar.

- [ ] **Step 6: Run the whole suite**

Run: `go build ./...`
Expected: builds.

Run: `CGO_ENABLED=0 go build ./cmd/graphi`
Expected: builds.

Run: `go vet ./...`
Expected: clean.

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add engine/embed/coderank/product_boundary_test.go internal/canary/gate.go docs/semantic-search.md
git commit -m "docs(semantic): document the CodeRank product boundary, lifecycle and change matrix"
```

---

## Acceptance mapping

Each spec acceptance criterion maps to the task that satisfies it:

| # | Criterion | Task |
|---|---|---|
| 1 | Development qualification is `YES` before integration begins | Global Constraints (gate on the existing decision) |
| 2 | The standard binary remains CGo-free and embedderless by default | 3, 14 |
| 3 | CodeRank requires the explicit absolute-manifest selector | 2, 3 |
| 4 | Vectors reused only under exact fingerprint and document-hash equality | 5 |
| 5 | Every query and build is bound to a validated identity and epoch | 4, 6, 7 |
| 6 | Any sidecar inconsistency poisons the adapter and fails the operation | 4, 7 |
| 7 | Durable and in-memory publication never exposes a mixed snapshot | 6 |
| 8 | Explicit selection never falls back to Potion or lexical-only | 7, 8, 9 |
| 9 | Product-path requalification reproduces the promoted result | 11, 12, 13 |
| 10 | Only a newly frozen candidate on a new sealed holdout can produce `RELEASE: YES` | Deferred follow-up plan (see Scope Check) |

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-09-17-coderank-product-integration.md`. Two execution options:

**1. Subagent-Driven (recommended)** — a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
