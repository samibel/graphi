// Package taint implements graphi's taint analyzer (SW-028, EP-005). It
// propagates labels from configured sources to sinks over the SYMBOL graph
// (calls/references/defines edges) using a worklist algorithm with canonical
// ordering, emitting one finding per realized source→sink flow with per-step
// provenance. Honest scope note: graphi's graph has no statement or CFG nodes,
// so this is symbol-level reachability with gen/kill label sets — NOT
// statement-level flow-sensitive dataflow. Sources/sinks match by (substring)
// name patterns; treat findings as triage leads, not proofs.
//
// Scope (v1): intraprocedural-only with a pluggable SummaryProvider interface
// for interprocedural support when SW-030 ships. Go goroutines/channels are
// handled conservatively (channels as taint boundaries). Implicit flows
// (control dependence, timing, exceptions) are out of scope.
//
// Layering: taint is a sub-package of engine/analysis. It imports core/model,
// engine/query (read-only), and engine/analysis (for the Analyzer interface and
// Analysis result type). It MUST NOT import surfaces/ or cmd/.
//
// Determinism: the worklist uses a priority queue with canonical ordering (node
// id, then label). All output is sorted deterministically before return. Labels
// use sparse representation (only tainted nodes carry labels).
package taint
