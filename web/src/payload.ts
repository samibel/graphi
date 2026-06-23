// Curated EP-002 PAYLOAD shapes (engine/query.Result family). These ride inside
// `envelope.payload` and are versioned independently from the HTTP ENVELOPE
// contract. Per SW-045 plan.md, only the ENVELOPE shapes are schema-generated
// (see contract.gen.ts); validating these payload shapes is explicitly OUT of
// scope for this story, so they remain hand-curated here.

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

/** SSE data-event payload (engine/observe.Event). */
export interface StreamEvent {
  type: string; // e.g. ingest-completed
  ts: string;
  payload?: { files?: number };
}
