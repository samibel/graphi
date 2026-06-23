// Type definitions mirroring graphi's SW-039 HTTP envelope + engine/query.Result.
// These are the ONLY shapes the web client depends on — it is a pure consumer
// of the published HTTP/SSE contract (no shared Go code, no codegen).

/** Envelope returned by every data route. Payload carries the engine bytes. */
export interface Envelope<T> {
  schema_version: number;
  payload: T;
}

/** A node in a query Result (engine/query.ResultNode). */
export interface ResultNode {
  id: string;
  kind: string;
  qualified_name: string;
  source_path: string;
  line: number;
  column: number;
}

/** An edge in a query Result, carrying provenance verbatim (engine/query.ResultEdge). */
export interface ResultEdge {
  id: string;
  from: string;
  to: string;
  kind: string;
  confidence_tier: string; // confirmed | inferred | heuristic | ...
  confidence: number; // [0,1]
  reason: string;
  evidence: string[];
}

/** Canonical structural-query result (engine/query.Result). */
export interface QueryResult {
  operation: string;
  symbol: string;
  outcome: string; // found | empty | not_found
  depth?: number;
  nodes: ResultNode[];
  edges: ResultEdge[];
}

/** Impact (blast-radius) analysis payload. */
export interface ImpactResult {
  analyzer: string;
  impacted: string[]; // impacted node ids
  provenance?: { tier?: string };
}

/** SSE event shape from /events (engine/observe.Event). */
export interface StreamEvent {
  type: string; // e.g. ingest-completed
  ts: string;
  payload?: { files?: number };
}

export const SCHEMA_VERSION = 1;
