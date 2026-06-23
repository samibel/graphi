// Typed contract shapes consumed from the graphi HTTP daemon (SW-039). The
// extension re-declares these (no shared codegen) — it depends only on the
// published HTTP contract.

export interface Envelope<T> {
  schema_version: number;
  payload: T;
}

export interface ResultNode {
  id: string;
  kind: string;
  qualified_name: string;
  source_path: string;
  line: number;
  column: number;
}

export interface ResultEdge {
  id: string;
  from: string;
  to: string;
  kind: string;
  confidence_tier: string;
  confidence: number;
  reason: string;
  evidence: string[];
}

export interface QueryResult {
  operation: string;
  symbol: string;
  outcome: string;
  depth?: number;
  nodes: ResultNode[];
  edges: ResultEdge[];
}

export interface ImpactResult {
  analyzer: string;
  impacted: string[];
  provenance?: { tier?: string };
}

export interface SearchResult {
  query: string;
  matches: Array<{ id: string; path: string; line: number }>;
}

export const SCHEMA_VERSION = 1;
