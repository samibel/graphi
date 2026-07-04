// Contract-derived test fixtures (Q5). Shapes mirror surfaces/http/contract.schema.json
// exactly: success envelope {schema_version, payload}, error envelope
// {schema_version, error:{code,message}}, the /contract document, and SSE frames.
// SCHEMA_VERSION is imported from the same barrel the client uses so the fixtures
// cannot drift from the built-against version.
import { SCHEMA_VERSION } from "../types";
import type {
  AgentToolResult,
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

// EP-020 agent-tool envelopes (engine/agenttools/contract.Result JSON shape).
export const agentToolPayload: AgentToolResult = {
  outcome: "ok",
  summary: "2 related files for pkg.Func",
  items: [
    { ref_id: "pkg/a.go", rank: 1, reason: "co-changed 12 times", evidence_ref_ids: ["ev1"] },
    { ref_id: "pkg/b.go", rank: 2, reason: "imports the target package", evidence_ref_ids: [] },
  ],
  evidence: [{ ref_id: "ev1", path: "pkg/a.go", line: 10, role: "definition" }],
  confidence: { distribution: { high: 0.7, medium: 0.3 }, top: "high", method: "normalized" },
  limits: { cap_applied: 20, total_available: 2, dropped: 0, truncated: false, next: "" },
};

export const agentToolPayloadHeuristic: AgentToolResult = {
  outcome: "ok",
  summary: "risk estimate from co-change heuristics",
  items: [
    { ref_id: "pkg/risky.go", rank: 1, reason: "high fan-in", evidence_ref_ids: ["ev1"] },
  ],
  evidence: [{ ref_id: "ev1", path: "pkg/risky.go", line: 42, role: "caller" }],
  confidence: { distribution: { medium: 1 }, top: "medium", method: "heuristic" },
  limits: { cap_applied: 1, total_available: 9, dropped: 8, truncated: true, next: "offset=1" },
};

export const agentToolPayloadEmpty: AgentToolResult = {
  outcome: "empty",
  summary: "no related files found",
  items: [],
  evidence: [],
  confidence: { distribution: {}, top: "", method: "empty" },
  limits: { cap_applied: 20, total_available: 0, dropped: 0, truncated: false, next: "" },
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
