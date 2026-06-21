// Package analysis is graphi's pluggable analysis layer: the registry and
// dispatch surface for analyzers that compute higher-order answers over the
// canonical graph (impact/blast-radius, call-chains, concept resolution, graph
// metrics, …). Each analyzer is a read-only plug-in that consumes the EP-001
// graph and returns a provenance-carrying, deterministic result.
//
// # Layering
//
// analysis is an engine package. It consumes core/model, a read-only view of
// core/graphstore, and engine/query (for the shared Reader contract and the
// ResultNode/ResultEdge/Outcome result primitives). It MUST NOT import surfaces/.
// engine→engine is an allowed same-layer edge under the SW-013 layer guard
// (Check flags only edges where importedRank > importerRank; 2→2 is not a
// violation).
//
// # Read-only by construction
//
// Analyzers depend on query.Reader — the read-only subset of
// graphstore.Graphstore — and receive it per Analyze call. There is no reachable
// mutation path from analysis, so an analyzer can never write to the graph. This
// is a compile-time guarantee inherited from query.Reader, not a convention.
//
// # Determinism
//
// Every result is materialized into slices and sorted by a single canonical
// comparator (see serialize.go) before it is returned, never emitted directly
// from a Go map. Combined with the canonical serializer, identical inputs over
// the same graph state yield byte-identical output across runs, daemon restarts,
// and separate processes (mirrors the query.Service determinism contract).
//
// # Zero outbound network
//
// The package imports only the Go standard library plus internal core/engine
// packages. It has no net/http dependency; the local-first invariant holds by
// construction and is asserted statically in tests (go list -deps excludes net).
package analysis
