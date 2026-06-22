// Package contracts implements graphi's cross-service contract drift detection
// analyzer (SW-031, EP-005). It identifies producer/consumer contracts (HTTP
// endpoints, gRPC service definitions, shared data structures) via a versioned
// pattern registry, links matching producer↔consumer pairs across service
// boundaries, and detects structural drift between linked shapes — classifying
// each as breaking (removed field, changed type) or non-breaking (added
// optional field).
//
// Layering: contracts is a sub-package of engine/analysis. It imports
// core/model and engine/query (read-only). It MUST NOT import engine/analysis
// (avoiding an import cycle); the parent package wraps it with a thin adapter
// for registry dispatch.
//
// Determinism: all output (contracts, drifts, diagnostics) is sorted by
// canonical comparators before return. Repeated runs over the same graph state
// yield identical results.
package contracts
