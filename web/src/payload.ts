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
  confidence_tier: string; // confirmed | derived | heuristic | ...
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

/** One provenance-bearing node reached by the impact traversal. */
export interface ImpactReachedNode {
  node: ResultNode;
  reached_via: ResultEdge;
  depth: number;
}

/** Impact (blast-radius) analysis payload (engine/analysis.Analysis). */
export interface ImpactResult {
  analyzer: string;
  outcome: string;
  symbol: string;
  truncated?: boolean;
  nodes?: ImpactReachedNode[];
}

/** One ranked lexical search hit (engine/search.Match). */
export interface SearchMatch {
  node_id: string;
  kind: string;
  qualified_name: string;
  source_path: string;
  line: number;
  column: number;
  rank: number;
  /** Optional per-match confidence [0,1]; not emitted by every backend build. */
  confidence?: number;
}

// --- EP-020 agent-tool response envelope -------------------------------------
// Canonical agent-first tool contract (engine/agenttools/contract/contract.go).
// Served by GET /analyze/{related_files|change_risk|agent_brief}; the JSON
// field names below mirror the Go struct tags exactly.

/** Result classification (contract.Outcome). */
export type AgentOutcome =
  | "ok"
  | "found"
  | "partial"
  | "ambiguous"
  | "empty"
  | "unavailable"
  | "error";

/** A file:line citation backing an item (contract.Evidence). */
export interface AgentEvidence {
  ref_id: string;
  path: string;
  line: number;
  span?: string;
  role: string;
}

/** A single ranked result row (contract.Item). */
export interface AgentItem {
  ref_id: string;
  rank: number;
  reason: string;
  evidence_ref_ids: string[];
}

/** Normalized confidence distribution over outcome labels (contract.Confidence). */
export interface AgentConfidence {
  distribution: Record<string, number>;
  top: string;
  method: string; // e.g. "normalized" | "heuristic" | "empty"
}

/** Size-budget enforcement metadata (contract.Limits). */
export interface AgentLimits {
  cap_applied: number;
  total_available: number;
  dropped: number;
  truncated: boolean;
  next: string;
}

/** The canonical agent-response envelope (contract.Result). */
export interface AgentToolResult {
  outcome: AgentOutcome;
  summary: string;
  items: AgentItem[];
  evidence: AgentEvidence[];
  confidence: AgentConfidence;
  limits: AgentLimits;
}

/** Canonical search response (engine/search.Response). */
export interface SearchResult {
  query: string;
  matches: SearchMatch[];
}

/** SSE data-event payload (engine/observe.Event). */
export interface StreamEvent {
  type: string; // e.g. ingest-completed
  ts: string;
  payload?: { files?: number };
}
