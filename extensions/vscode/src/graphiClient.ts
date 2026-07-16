// graphiClient is the SINGLE backend boundary for the VS Code extension host.
// Every network call goes to the SW-044 HTTP/SSE surface at the configured
// loopback base URL. The client is READ-ONLY BY CONSTRUCTION: only GET
// query/analyze/search/health/contract methods exist — no write/refactor/undo
// verbs are defined, so the extension cannot mutate the graph or workspace.
//
// The fail-closed schema_version guard (R5/AC-7) lives here so it is enforced in
// exactly one place on EVERY response path: 200 bodies, error envelopes, HTTP
// 412, and /contract. (The SSE `ready` frame is guarded in sseClient.ts, which
// reuses SchemaMismatchError from here.) Ported from web/src/graphiClient.ts;
// the only adaptations are a config-driven base URL (no import.meta.env) and
// host-side `fetch` (Node 18+).
import {
  SCHEMA_VERSION,
  type Contract,
  type Envelope,
  type ErrorEnvelope,
  type ImpactResult,
  type QueryResult,
  type SearchMatch,
  type SearchResult,
} from "./contract";
import { assertLoopback } from "./loopback";

/** Thrown on ANY schema-version drift; the UI renders this fail-closed (AC-7). */
export class SchemaMismatchError extends Error {
  readonly clientVersion: number;
  readonly serverVersion?: number;
  constructor(clientVersion: number, serverVersion?: number) {
    const server = serverVersion === undefined ? "unknown" : `v${serverVersion}`;
    super(
      `schema version mismatch: client built for v${clientVersion}, server is ${server}`,
    );
    this.name = "SchemaMismatchError";
    this.clientVersion = clientVersion;
    this.serverVersion = serverVersion;
  }
}

/** Typed, sanitized error for non-mismatch error envelopes (400/404/503/500). */
export class ApiError extends Error {
  readonly code: string;
  constructor(code: string, message: string) {
    super(message); // server-sanitized message only (S4)
    this.name = "ApiError";
    this.code = code;
  }
}

/**
 * Honest result of resolving editor text to canonical graph identities.
 *
 * Structural query and analyzer routes accept an exact NodeId, not a lexical
 * symbol.  Keeping ambiguity explicit prevents a ranked search result from
 * silently turning "Foo" into whichever Foo happened to sort first.
 */
export type SymbolResolution =
  | { outcome: "found"; matches: [SearchMatch] }
  | { outcome: "ambiguous"; matches: SearchMatch[] }
  | { outcome: "not_found"; matches: [] };

const SYMBOL_RESOLUTION_LIMIT = 100;
const NODE_ID_PATTERN = /^[0-9a-f]{16}$/;

function requireNodeID(value: string): string {
  if (!NODE_ID_PATTERN.test(value)) {
    throw new ApiError("bad_request", "invalid graph node id");
  }
  return value;
}

/** Return the final language-neutral segment of a qualified symbol name. */
function terminalSymbol(qualifiedName: string): string {
  const parts = qualifiedName.split(/::|[.#/\\$]/).filter(Boolean);
  return parts[parts.length - 1] ?? qualifiedName;
}

/**
 * Reduce lexical search results to exact identity candidates.  Fuzzy results
 * are deliberately rejected: only NodeId, full qualified-name, or final-name
 * equality can authorize a subsequent exact-NodeId request.
 */
export function resolveSearchMatches(
  symbolText: string,
  matches: readonly SearchMatch[],
): SymbolResolution {
  const symbol = symbolText.trim();
  if (!symbol) return { outcome: "not_found", matches: [] };

  const byID = new Map<string, SearchMatch>();
  for (const match of matches) {
    // NodeId is the canonical fixed-width lowercase-hex identity. A malformed
    // search payload must not authorize an exact structural/analyzer request.
    if (
      typeof match.node_id !== "string" ||
      typeof match.qualified_name !== "string" ||
      !NODE_ID_PATTERN.test(match.node_id)
    ) {
      continue;
    }
    if (
      match.node_id === symbol ||
      match.qualified_name === symbol ||
      terminalSymbol(match.qualified_name) === symbol
    ) {
      byID.set(match.node_id, match);
    }
  }
  const exact = [...byID.values()];
  if (exact.length === 0) return { outcome: "not_found", matches: [] };
  if (exact.length === 1) {
    return { outcome: "found", matches: [exact[0]] };
  }
  return { outcome: "ambiguous", matches: exact };
}

function isErrorEnvelope(body: unknown): body is ErrorEnvelope {
  return (
    typeof body === "object" &&
    body !== null &&
    "error" in body &&
    typeof (body as { error: unknown }).error === "object"
  );
}

function envelopeVersion(body: unknown): number | undefined {
  if (typeof body === "object" && body !== null && "schema_version" in body) {
    const v = (body as { schema_version: unknown }).schema_version;
    return typeof v === "number" ? v : undefined;
  }
  return undefined;
}

function isContract(body: unknown): body is Contract {
  if (typeof body !== "object" || body === null) return false;
  const value = body as {
    resources?: unknown;
    streams?: unknown;
  };
  return (
    Array.isArray(value.resources) &&
    value.resources.every((entry) => typeof entry === "string") &&
    Array.isArray(value.streams) &&
    value.streams.every((entry) => typeof entry === "string")
  );
}

/**
 * Decode a fetch Response into a typed success envelope, enforcing the
 * fail-closed schema guard on EVERY path:
 *   - HTTP 412                                  → SchemaMismatchError
 *   - error envelope code === "schema_mismatch" → SchemaMismatchError
 *   - any envelope schema_version !== SCHEMA_VERSION → SchemaMismatchError
 *   - other error envelopes (400/404/503/500)  → ApiError(code, message)
 * Only after all guards pass does it return the typed payload.
 */
export async function decodeEnvelope<T>(res: Response): Promise<Envelope<T>> {
  // 412 is the explicit drift status; treat it as a mismatch even if the body
  // is unparseable.
  if (res.status === 412) {
    let serverVersion: number | undefined;
    try {
      serverVersion = envelopeVersion(await res.clone().json());
    } catch {
      /* body may be absent */
    }
    throw new SchemaMismatchError(SCHEMA_VERSION, serverVersion);
  }

  let body: unknown;
  try {
    body = await res.json();
  } catch {
    if (!res.ok) throw new ApiError("internal", `HTTP ${res.status}`);
    throw new SchemaMismatchError(SCHEMA_VERSION, undefined);
  }

  const version = envelopeVersion(body);

  if (isErrorEnvelope(body)) {
    const { code, message } = body.error;
    if (code === "schema_mismatch") {
      throw new SchemaMismatchError(SCHEMA_VERSION, version);
    }
    // Version on an error envelope must still match (fail-closed everywhere).
    if (version !== SCHEMA_VERSION) {
      throw new SchemaMismatchError(SCHEMA_VERSION, version);
    }
    throw new ApiError(code, message);
  }

  // Success path: the version stamp is MANDATORY and must match.
  if (version !== SCHEMA_VERSION) {
    throw new SchemaMismatchError(SCHEMA_VERSION, version);
  }
  return body as Envelope<T>;
}

export class GraphiClient {
  constructor(private readonly baseUrl: string) {
    // Loopback hard precondition on EVERY construction (S1) — fail fast, no
    // request is ever issued against a non-loopback target.
    assertLoopback(baseUrl);
  }

  /** Expose the base URL for the SSE reader (which streams, not envelope-decodes). */
  base(): string {
    return this.baseUrl;
  }

  /** Build request headers with the schema-version stamp. */
  headers(): Record<string, string> {
    return {
      // Header is OPTIONAL per R5; we send it so the server can 412 early, but
      // the body guard is what actually enforces the contract.
      "X-Graphi-Schema-Version": String(SCHEMA_VERSION),
    };
  }

  /** The single GET primitive. There is no non-GET method on this class (R-only). */
  private async getEnvelope<T>(path: string): Promise<T> {
    const res = await fetch(`${this.baseUrl}${path}`, {
      method: "GET",
      headers: this.headers(),
    });
    const env = await decodeEnvelope<T>(res);
    return env.payload;
  }

  /**
   * Boot-time capability negotiation. The contract document is itself a
   * versioned envelope-like shape carrying its own schema_version, so it is
   * guarded the same way.
   */
  async getContract(): Promise<Contract> {
    // /contract uses the same outer success envelope as every other data
    // route.  getEnvelope validates that outer stamp and unwraps payload; the
    // contract document's own stamp is independently mandatory.
    const payload: unknown = await this.getEnvelope<unknown>("/contract");
    const version = envelopeVersion(payload);
    if (version !== SCHEMA_VERSION) {
      throw new SchemaMismatchError(SCHEMA_VERSION, version);
    }
    if (!isContract(payload)) {
      throw new ApiError("internal", "invalid contract response");
    }
    return payload;
  }

  /** Read-only: neighborhood of an exact seed NodeId. */
  async getNeighborhood(nodeID: string, depth = 2): Promise<QueryResult> {
    const exact = requireNodeID(nodeID);
    return this.getEnvelope<QueryResult>(
      `/query/neighborhood?symbol=${encodeURIComponent(exact)}&depth=${depth}`,
    );
  }

  /** Read-only: definition relation for an exact NodeId. */
  async getDefinition(nodeID: string): Promise<QueryResult> {
    const exact = requireNodeID(nodeID);
    return this.getEnvelope<QueryResult>(
      `/query/definition?symbol=${encodeURIComponent(exact)}`,
    );
  }

  /** Read-only: inbound reference relations for an exact NodeId. */
  async getReferences(nodeID: string): Promise<QueryResult> {
    const exact = requireNodeID(nodeID);
    return this.getEnvelope<QueryResult>(
      `/query/references?symbol=${encodeURIComponent(exact)}`,
    );
  }

  /**
   * Read-only: blast-radius (impact) via the NEGOTIATED analyzer route (no
   * hard-coded /analyze/impact). `analyzerRoute` comes from getContract +
   * resolveAnalyzerRoute.
   */
  async getImpact(analyzerRoute: string, nodeID: string): Promise<ImpactResult> {
    const exact = requireNodeID(nodeID);
    return this.getEnvelope<ImpactResult>(
      `/${analyzerRoute}?symbol=${encodeURIComponent(exact)}&direction=reverse`,
    );
  }

  /** Read-only: lexical/symbol search. */
  async search(q: string, limit = 50): Promise<SearchResult> {
    return this.getEnvelope<SearchResult>(
      `/search?q=${encodeURIComponent(q)}&limit=${limit}`,
    );
  }

  /** Resolve editor text to one or more exact graph NodeIds via lexical search. */
  async resolveSymbol(symbolText: string): Promise<SymbolResolution> {
    const result = await this.search(symbolText, SYMBOL_RESOLUTION_LIMIT);
    return resolveSearchMatches(symbolText, result.matches);
  }

  /** Read-only: explain symbol analyzer. */
  async explainSymbol(symbol: string): Promise<unknown> {
    return this.getEnvelope<unknown>(`/analyze/explain_symbol?symbol=${encodeURIComponent(symbol)}`);
  }

  /** Read-only: related files analyzer. */
  async relatedFiles(symbol: string): Promise<unknown> {
    return this.getEnvelope<unknown>(`/analyze/related_files?target=${encodeURIComponent(symbol)}`);
  }

  /** Read-only: change risk analyzer. */
  async changeRisk(target: string): Promise<unknown> {
    return this.getEnvelope<unknown>(`/analyze/change_risk?target=${encodeURIComponent(target)}`);
  }

  /** Read-only: agent brief (degrades if not advertised). */
  async agentBrief(topic?: string): Promise<unknown> {
    const q = topic ? `?symbol=${encodeURIComponent(topic)}` : "";
    return this.getEnvelope<unknown>(`/analyze/agent_brief${q}`);
  }

  /** Read-only: health check (used by the connection status). Guarded. */
  async health(): Promise<{ status: string; schema_version: number }> {
    const res = await fetch(`${this.baseUrl}/healthz`, {
      method: "GET",
      headers: this.headers(),
    });
    if (res.status === 412) throw new SchemaMismatchError(SCHEMA_VERSION);
    if (!res.ok) throw new ApiError("unavailable", `HTTP ${res.status}`);
    const body = (await res.json()) as { status: string; schema_version: number };
    if (body.schema_version !== SCHEMA_VERSION) {
      throw new SchemaMismatchError(SCHEMA_VERSION, body.schema_version);
    }
    return body;
  }
}

/** Resolve the impact/blast-radius analyzer route from negotiated resources. */
export function resolveAnalyzerRoute(contract: Contract): string | null {
  const analyzers = contract.resources.filter((r) => r.startsWith("analyze/"));
  return (
    analyzers.find((resource) => {
      const name = resource.slice("analyze/".length).toLowerCase();
      return (
        name === "impact" ||
        name === "blast" ||
        name === "blast-radius" ||
        name === "blast_radius"
      );
    }) ?? null
  );
}

/** True when /contract advertises the given resource (per-feature degradation). */
export function hasResource(contract: Contract, resource: string): boolean {
  return contract.resources.includes(resource);
}
