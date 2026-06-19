// This file documents the SW-004 change: the before/after state of the system
// and the reasoning behind it (story AC5 / the [DOC] item). It carries no code.
//
// # Before SW-004
//
// The graph existed (core/model node/edge value types, SW-002) and was durably
// stored and listed in canonical id order (core/graphstore, SW-003), but there
// was NO structural navigation over it. A caller could list all nodes or all
// edges, but could not ask "who calls this symbol?", "what does it call?",
// "what references it?", "where is it defined?", or "what is in its
// neighborhood?". There was also no surface (CLI or MCP) exposing such queries,
// so AI-agent and IDE consumers had no read path into the graph's relationships.
//
// # After SW-004
//
// A single, shared, read-only query service (engine/query) answers all five
// navigation questions. It depends only on core/model and a read-only Reader
// view of the graphstore (no reachable mutation path — a compile-time
// guarantee), and it MUST NOT import surfaces/. Both the CLI (surfaces/cli) and
// the MCP stdio server (surfaces/mcp) route every query through the SAME service
// via query.Dispatch and emit bytes from the SAME canonical serializer
// (query.Marshal), so the two surfaces are byte-identical by construction. cmd
// wires both surfaces (`graphi query …`, `graphi mcp`) while preserving the
// original SW-001 parser-registry behavior.
//
// Key guarantees delivered:
//   - Every result node/edge carries the edge's confidence_tier, confidence,
//     reason, and evidence VERBATIM from the model — never re-derived.
//   - Neighborhood is bounded by the single documented constant
//     MaxNeighborhoodDepth; over-max requests are clamped (not rejected) and the
//     effective depth is reported. Traversal is cycle-guarded and terminates.
//   - One canonical comparator (tier, then from/to/kind, then content-addressed
//     id) plus materialize-then-sort eliminate Go map-iteration nondeterminism,
//     so output is byte-stable across repeated runs and across surfaces.
//   - Unresolved symbols return a typed not_found Result (Found()==false), and
//     resolved-but-zero-match returns a distinct empty Result — neither is an
//     error and neither is a partial guess.
//
// # Reasoning
//
// The architecture mandates "one Engine runtime serves all surfaces — no
// mode-forked logic". Concentrating all traversal/ordering/serialization in
// engine/query (and forbidding surface-local logic) makes MCP↔CLI parity a
// structural property rather than a maintenance burden, and keeps the read-only
// and layering invariants enforceable by the type system and CI. Byte-stable,
// provenance-carrying results are essential for token-efficient, trustworthy
// consumption by AI agents.
//
// # Data flow
//
//	┌──────────────┐     ┌──────────────┐
//	│ surfaces/cli │     │ surfaces/mcp │      (no query logic; format-only)
//	└──────┬───────┘     └──────┬───────┘
//	       │  Dispatch(op,…)    │  Dispatch(op,…)
//	       └─────────┬──────────┘
//	                 ▼
//	        ┌──────────────────┐
//	        │  engine/query    │  comparator + serializer (canonical, shared)
//	        │  Service         │  Callers/Callees/References/Definition/Neighborhood
//	        └────────┬─────────┘
//	                 │  Reader (read-only subset)
//	                 ▼
//	        ┌──────────────────┐
//	        │ core/graphstore  │ ← core/model (Node/Edge + provenance)
//	        └──────────────────┘
//
// Equivalent Mermaid:
//
//	flowchart TD
//	  CLI[surfaces/cli] -->|Dispatch| SVC[engine/query.Service]
//	  MCP[surfaces/mcp] -->|Dispatch| SVC
//	  SVC -->|Reader read-only| GS[core/graphstore]
//	  GS --> M[core/model]
//	  SVC -->|Marshal canonical bytes| OUT[(byte-stable result)]
package query
