// Package pdg implements graphi's Program Dependence Graph analyzer (SW-029,
// EP-005). It computes data-dependence edges via reaching-definitions analysis
// and control-dependence edges via post-dominance tree construction (Cooper-
// Harvey-Kennedy algorithm), merging both into a single PDG result.
//
// Scope (v1): intraprocedural-only. Honest scope note: the analyzer consumes
// the canonical EP-001 SYMBOL graph (function/type/variable nodes with
// calls/references/defines edges) — graphi's parsers produce no statement,
// expression, or CFG nodes — so "data dependence" here means symbol-level
// def-use reachability and "control dependence" is post-dominance over the
// call/reference structure, a coarse approximation of a classical PDG. It
// produces typed edge sets with stable edge kinds ("data_dep", "control_dep")
// and provenance on every edge.
//
// Layering: pdg is a sub-package of engine/analysis. It imports core/model,
// core/graphstore, and engine/query (read-only). It MUST NOT import the parent
// engine/analysis package (avoiding import cycles); the parent wraps it with a
// thin adapter for registry dispatch, mirroring the taint sub-package pattern.
//
// Determinism: the reaching-definitions worklist uses a container/heap priority
// queue with canonical ordering (node ID). All output edges are sorted
// deterministically before return: by (From, To, Kind). Identical inputs over
// the same graph state yield identical output across runs.
//
// Zero outbound network: the package imports only the Go standard library plus
// internal core/engine packages. No net/http dependency.
package pdg
