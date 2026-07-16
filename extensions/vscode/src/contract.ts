// Wire-contract types consumed from the SW-044 HTTP/SSE surface. The ENVELOPE /
// error / contract / SSE-frame shapes mirror surfaces/http/contract.schema.json
// (the single Go<->TS source of truth); the EP-002 payload shapes ride inside
// `envelope.payload` and are versioned independently. These are ported verbatim
// from web/src/{contract.gen.ts,payload.ts,types.ts} so the extension and the
// web client agree on the wire by construction (R5 single-contract invariant).

// --- Generated ENVELOPE shapes (mirror of contract.gen.ts) ------------------

/** Success data response. payload carries engine canonical bytes verbatim. */
export interface RawEnvelope {
  schema_version: 1;
  payload: { [k: string]: unknown };
}

/** The single error shape shared by every REST error AND the SSE error frame. */
export interface ErrorEnvelope {
  schema_version: 1;
  error: {
    code:
      | "bad_request"
      | "not_found"
      | "schema_mismatch"
      | "unavailable"
      | "labs_disabled"
      | "invalid_host"
      | "origin_forbidden"
      | "request_too_large"
      | "internal";
    /** Sanitized, client-safe message. Never a raw engine string/path/trace. */
    message: string;
  };
}

/** The /contract capability-negotiation document. */
export interface Contract {
  schema_version: 1;
  /** Negotiable read resources: query/<op>, search, analyze/<analyzer>. */
  resources: string[];
  /** SSE event types observable on /events (data + framing events). */
  streams: string[];
}

/** Logical shape of one SSE frame on /events. */
export interface SseFrame {
  event: string;
  id: number;
  data: { [k: string]: unknown };
}

/**
 * Typed success envelope; narrows the opaque generated `payload` at decode
 * sites while keeping the schema-derived `schema_version` literal.
 */
export interface Envelope<T> extends Omit<RawEnvelope, "payload"> {
  payload: T;
}

/**
 * The client's built-against ENVELOPE schema version. Sourced from the literal
 * type so a contract bump that regenerates the mirror forces this constant to
 * be reconciled — the runtime guard and the static type stay in lockstep.
 */
export const SCHEMA_VERSION: RawEnvelope["schema_version"] = 1;

// --- Curated EP-002 PAYLOAD shapes (mirror of payload.ts) -------------------

/** A node in a query Result (engine/query.ResultNode). */
export interface ResultNode {
  id: string;
  kind: string;
  qualified_name: string;
  source_path: string;
  line: number;
  column: number;
}

/** An edge in a query Result, carrying provenance verbatim. */
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
}

/** Search result payload (engine/search.Response). */
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
