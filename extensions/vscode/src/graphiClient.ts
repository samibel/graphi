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
// the only adaptations are a config-driven base URL (no import.meta.env), an
// optional header-only auth token, and host-side `fetch` (Node 18+).
import {
  SCHEMA_VERSION,
  type Contract,
  type Envelope,
  type ErrorEnvelope,
  type ImpactResult,
  type QueryResult,
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

/** Provides the optional auth token (header-only) lazily; never logged. */
export type TokenProvider = () => Promise<string | undefined>;

export class GraphiClient {
  private readonly tokenProvider?: TokenProvider;

  constructor(
    private readonly baseUrl: string,
    tokenProvider?: TokenProvider,
  ) {
    // Loopback hard precondition on EVERY construction (S1) — fail fast, no
    // request is ever issued against a non-loopback target.
    assertLoopback(baseUrl);
    this.tokenProvider = tokenProvider;
  }

  /** Expose the base URL for the SSE reader (which streams, not envelope-decodes). */
  base(): string {
    return this.baseUrl;
  }

  /** Build request headers: schema-version stamp + optional bearer token. */
  async headers(): Promise<Record<string, string>> {
    const h: Record<string, string> = {
      // Header is OPTIONAL per R5; we send it so the server can 412 early, but
      // the body guard is what actually enforces the contract.
      "X-Graphi-Schema-Version": String(SCHEMA_VERSION),
    };
    const token = await this.tokenProvider?.();
    if (token) h.Authorization = `Bearer ${token}`; // header-only, never in URL (S3)
    return h;
  }

  /** The single GET primitive. There is no non-GET method on this class (R-only). */
  private async getEnvelope<T>(path: string): Promise<T> {
    const res = await fetch(`${this.baseUrl}${path}`, {
      method: "GET",
      headers: await this.headers(),
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
    const res = await fetch(`${this.baseUrl}/contract`, {
      method: "GET",
      headers: await this.headers(),
    });
    if (res.status === 412) throw new SchemaMismatchError(SCHEMA_VERSION);
    let body: unknown;
    try {
      body = await res.json();
    } catch {
      throw new ApiError("internal", `HTTP ${res.status}`);
    }
    if (isErrorEnvelope(body)) {
      const { code, message } = body.error;
      if (code === "schema_mismatch") throw new SchemaMismatchError(SCHEMA_VERSION);
      throw new ApiError(code, message);
    }
    if (envelopeVersion(body) !== SCHEMA_VERSION) {
      throw new SchemaMismatchError(SCHEMA_VERSION, envelopeVersion(body));
    }
    return body as Contract;
  }

  /** Read-only: neighborhood of a seed symbol — the bounded graph endpoint. */
  async getNeighborhood(seed: string, depth = 2): Promise<QueryResult> {
    return this.getEnvelope<QueryResult>(
      `/query/neighborhood?symbol=${encodeURIComponent(seed)}&depth=${depth}`,
    );
  }

  /**
   * Read-only: blast-radius (impact) via the NEGOTIATED analyzer route (no
   * hard-coded /analyze/impact). `analyzerRoute` comes from getContract +
   * resolveAnalyzerRoute.
   */
  async getImpact(analyzerRoute: string, symbol: string): Promise<ImpactResult> {
    return this.getEnvelope<ImpactResult>(
      `/${analyzerRoute}?symbol=${encodeURIComponent(symbol)}&direction=reverse`,
    );
  }

  /** Read-only: lexical/symbol search. */
  async search(q: string, limit = 50): Promise<SearchResult> {
    return this.getEnvelope<SearchResult>(
      `/search?q=${encodeURIComponent(q)}&limit=${limit}`,
    );
  }

  /** Read-only: health check (used by the connection status). Guarded. */
  async health(): Promise<{ status: string; schema_version: number }> {
    const res = await fetch(`${this.baseUrl}/healthz`, {
      method: "GET",
      headers: await this.headers(),
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
  if (analyzers.length === 0) return null;
  const impact = analyzers.find((r) => /impact|blast/i.test(r));
  return impact ?? analyzers[0];
}

/** True when /contract advertises the given resource (per-feature degradation). */
export function hasResource(contract: Contract, resource: string): boolean {
  return contract.resources.includes(resource);
}
