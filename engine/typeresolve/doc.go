// Package typeresolve will provide the go/types-backed CONFIRMED-tier
// resolution pass for Go (roadmap Phase 4, v0.2.0): type-checked
// calls/references/implements edges with correct method dispatch, shadowing,
// and import resolution — the depth the name-based engine/link heuristics
// cannot reach.
//
// This package is currently DARK: nothing in engine/ingest calls it yet. It
// ships in four reviewable slices; this first slice contains only the
// object→node identity mapping (qn.go) plus the golden cross-test that pins it
// byte-exactly against the real core/parse extractor. That mapping is the
// load-bearing artifact of the whole phase: a confirmed edge can only ever be
// attached to a node the extractor actually created, and any drift between the
// two naming schemes silently drops edges. The cross-test makes that drift a
// test failure instead.
//
// Hard constraints (mirrored from the roadmap and enforced by tests/CI):
//   - stdlib go/types ONLY — no golang.org/x/tools, no go/packages (which
//     shells out and can touch the network/module cache). Precedent:
//     internal/canary/gate.go already type-checks with stdlib go/types.
//   - No new module dependencies, no network, CGo-free.
//   - Deterministic: identical input yields identical output.
//
// Layering: typeresolve is an engine package. It imports core/model and
// (in tests) core/parse; it must never import surfaces/ or cmd/.
package typeresolve
