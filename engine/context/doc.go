// Package context is graphi's token-efficient context-assembly engine (story
// SW-016, epic EP-003). It takes a query plus the candidate matches produced by
// the EP-001 query/search layer and emits a compact, deterministic, citation-
// backed context bundle: a ranked, size-budgeted set of trimmed evidence
// snippets, each carrying a precise source citation (file path and line range).
//
// Scope is strictly the context-shaping transform — candidates in, winnowed
// evidence out. It runs fully local (the only I/O path is a disk-only
// SourceReader that rejects remote sources), performs no token metering, and no
// pricing (those are SW-017 / SW-018).
//
// Layering: context is an engine package. It imports the sibling engine packages
// engine/query and engine/search for the intake adapters only (engine→engine;
// the SW-013 layer guard fires on strictly-upward edges, so same-layer imports
// are allowed). It never imports surfaces/ or cmd/. It holds no query/search
// logic of its own.
//
// Determinism contract: Assemble is a pure function of its inputs. There is no
// wall-clock, no randomness, and no reliance on map-iteration order. Identical
// (query, candidates, opts, source-bytes) yield byte-identical bundles across
// processes. Ranking is a stable total order.
package context

// MethodVersion is the version stamp of the assembly method. It is a fixed
// constant so the determinism contract holds; if the winnowing/ranking/budget
// algorithm changes in a way that alters output for identical inputs, bump this
// stamp so callers can attribute bundles to the method that produced them.
const MethodVersion = "context-v1"
