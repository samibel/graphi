// Package interproc implements graphi's Sharir-Pnueli interprocedural analysis
// framework (SW-030, EP-005). It computes per-procedure functional summaries
// (input-to-output transfer relations) over the call graph, caches them in a
// content-addressed store keyed by SHA-256 of the procedure's input state, and
// composes summaries at call sites rather than re-analyzing callees inline.
//
// # Core algorithm
//
// The solver is a worklist-based iterative fixpoint engine:
//
//  1. Build the call graph from graph edges (kind "calls").
//  2. Run Tarjan SCC detection to identify recursive/mutually-recursive cycles.
//  3. Process SCCs in reverse topological order (callees before callers).
//  4. Within each SCC, iterate until the summary fixpoint is reached or the
//     widening threshold fires (default: 3 iterations) to force convergence.
//  5. Cache each computed summary under its content-addressed key.
//
// # Cap enforcement
//
// Five independent cap dimensions bound worst-case cost:
//   - MaxProcedures — maximum number of procedures analyzed
//   - MaxIterations — maximum fixpoint iterations per SCC
//   - MaxSCCSize — maximum SCC size before conservative over-approximation
//   - MaxSummaryEntries — maximum entries in the summary cache
//   - MaxTotalWork — maximum total work units (node visits)
//
// When any cap is exceeded, the analyzer emits a diagnostic, marks affected
// summaries as approximate, and records the cap-hit in provenance. No silent
// truncation; no unbounded blowup.
//
// # Layering
//
// interproc is a sub-package of engine/analysis. It imports core/model and
// engine/query (read-only) but MUST NOT import engine/analysis (avoiding an
// import cycle). The parent analysis package wraps the interproc.Analyzer with
// a thin adapter for registry dispatch, mirroring the taint sub-package pattern.
//
// # Determinism
//
// All iteration orders are canonical: SCCs are processed in reverse topological
// order; within each SCC, procedures are sorted by ID. Summary keys are
// content-addressed (SHA-256), so identical inputs always produce the same cache
// key. The result is byte-for-byte deterministic across runs.
package interproc
