// Contract-derived test fixtures (Q5). Shapes mirror surfaces/http/contract.schema.json
// exactly: success envelope {schema_version, payload}, error envelope
// {schema_version, error:{code,message}}, the /contract document, and SSE frames.
// SCHEMA_VERSION is imported from the same barrel the client uses so the fixtures
// cannot drift from the built-against version.
import { SCHEMA_VERSION } from "../types";
import type {
  Contract,
  ErrorEnvelope,
  ImpactResult,
  QueryResult,
  SearchResult,
} from "../types";

export const queryPayload: QueryResult = {
  operation: "neighborhood",
  symbol: "pkg.Func",
  outcome: "found",
  depth: 2,
  nodes: [
    { id: "n1", kind: "func", qualified_name: "pkg.Func", source_path: "pkg/a.go", line: 10, column: 1 },
    { id: "n2", kind: "func", qualified_name: "pkg.Caller", source_path: "pkg/b.go", line: 20, column: 1 },
    { id: "n3", kind: "func", qualified_name: "pkg.Other", source_path: "pkg/c.go", line: 30, column: 1 },
  ],
  edges: [
    // e1 carries evidence → citation candidate; e2 has none.
    { id: "e1", from: "n2", to: "n1", kind: "calls", confidence_tier: "confirmed", confidence: 1, reason: "direct call", evidence: ["pkg/b.go:20"] },
    { id: "e2", from: "n3", to: "n1", kind: "calls", confidence_tier: "inferred", confidence: 0.5, reason: "inferred", evidence: [] },
  ],
};

export const impactPayload: ImpactResult = {
  analyzer: "impact",
  impacted: ["n1", "n2"],
  provenance: { tier: "confirmed" },
};

export const searchPayload: SearchResult = {
  query: "release",
  matches: [
    { node_id: "s1", kind: "function", qualified_name: "main.release", source_path: "cmd/release/main.go", line: 12, column: 1, rank: -1.5 },
    { node_id: "s2", kind: "file", qualified_name: "cmd/release/main.go", source_path: "cmd/release/main.go", line: 1, column: 1, rank: -1.0 },
  ],
};

export const contractDoc: Contract = {
  schema_version: SCHEMA_VERSION,
  resources: ["query/neighborhood", "query/callers", "search", "analyze/impact"],
  streams: ["ready", "bye", "error", "ingest-completed"],
};

export const contractNoAnalyzer: Contract = {
  schema_version: SCHEMA_VERSION,
  resources: ["query/neighborhood", "search"],
  streams: ["ready", "bye", "error", "ingest-completed"],
};

export function successEnvelope<T>(payload: T): { schema_version: number; payload: T } {
  return { schema_version: SCHEMA_VERSION, payload };
}

export function mismatchedSuccessEnvelope<T>(payload: T): { schema_version: number; payload: T } {
  return { schema_version: SCHEMA_VERSION + 99, payload };
}

export function errorEnvelope(
  code: ErrorEnvelope["error"]["code"],
  message: string,
): ErrorEnvelope {
  return { schema_version: SCHEMA_VERSION, error: { code, message } } as ErrorEnvelope;
}

export const sseReadyFrame = JSON.stringify({ schema_version: SCHEMA_VERSION });
export const sseReadyFrameMismatch = JSON.stringify({ schema_version: SCHEMA_VERSION + 99 });
export const sseByeFrame = JSON.stringify({});
export const sseDataFrame = JSON.stringify({ type: "ingest-completed", ts: "2026-06-23T00:00:00Z", payload: { files: 3 } });
export const sseErrorFrame = JSON.stringify(errorEnvelope("internal", "stream blew up"));
