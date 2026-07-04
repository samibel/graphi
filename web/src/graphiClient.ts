// graphiClient is the SINGLE backend boundary for the web client. Every network
// call goes to the SW-044 HTTP/SSE surface at the configured loopback base URL.
// No other network calls are made (zero-outbound client contract, S1/S5). The
// fail-closed schema_version guard (R5/AC-4) lives here so it is enforced in
// exactly one place on every response path: 200 bodies, error envelopes, and
// the SSE `ready` frame.
import type {
  AgentToolResult,
  Contract,
  Envelope,
  ErrorEnvelope,
  ImpactResult,
  QueryResult,
  SearchMatch,
  SearchResult,
  StreamEvent,
} from "./types";
import { SCHEMA_VERSION } from "./types";
import { assertLoopbackBase } from "./loopback";

const BASE = (import.meta.env.VITE_GRAPHI_URL as string | undefined) ?? "";
// Fail-closed at module load if someone points us off-loopback (S1).
assertLoopbackBase(BASE);

/** Thrown on ANY schema-version drift; the UI renders this fail-closed (AC-4). */
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
 * fail-closed schema guard on EVERY path (D2/Q1/S2):
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

  // Success path: the version stamp is MANDATORY and must match (AC-4 gap that
  // the old getJSON missed — 200 bodies were not validated).
  if (version !== SCHEMA_VERSION) {
    throw new SchemaMismatchError(SCHEMA_VERSION, version);
  }
  return body as Envelope<T>;
}

async function getEnvelope<T>(path: string): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    // Header is OPTIONAL per R5; we send it so the server can 412 early, but the
    // body guard above is what actually enforces the contract.
    headers: { "X-Graphi-Schema-Version": String(SCHEMA_VERSION) },
  });
  const env = await decodeEnvelope<T>(res);
  return env.payload;
}

/**
 * Boot-time capability negotiation (A3/A5). The contract document is itself a
 * versioned envelope-like shape carrying its own schema_version, so it is
 * guarded the same way (the ready/error guard cannot cover it — it is a normal
 * GET). Returns negotiated resources[] + streams[].
 */
export async function getContract(): Promise<Contract> {
  // The server wraps /contract in the standard success envelope
  // ({schema_version, payload:{schema_version, resources, streams}}), so it
  // rides the same decode path as every other GET. getEnvelope validates the
  // OUTER stamp; the contract document's OWN stamp is validated here.
  const contract = await getEnvelope<Contract>("/contract");
  if (contract.schema_version !== SCHEMA_VERSION) {
    throw new SchemaMismatchError(SCHEMA_VERSION, contract.schema_version);
  }
  return contract;
}

/** Resolve the impact/blast-radius analyzer route from negotiated resources. */
export function resolveAnalyzerRoute(contract: Contract): string | null {
  // resources are like "query/callers", "search", "analyze/<analyzer>".
  const analyzers = contract.resources.filter((r) => r.startsWith("analyze/"));
  if (analyzers.length === 0) return null;
  // Prefer an analyzer whose name suggests impact/blast-radius; else first.
  const impact = analyzers.find((r) => /impact|blast/i.test(r));
  return impact ?? analyzers[0];
}

/** True when /contract advertises the given resource (per-feature degradation). */
export function hasResource(contract: Contract, resource: string): boolean {
  return contract.resources.includes(resource);
}

/**
 * Typed availability wrapper for the EP-020 agent tools. A 503 / absent
 * analyzer is a NORMAL degraded state (the daemon simply was not built/wired
 * with that analyzer), so it must render as "unavailable" — never crash and
 * never blank the UI. Schema mismatches still propagate (fail-closed, AC-4).
 */
export type AgentToolResponse =
  | { available: true; result: AgentToolResult }
  | { available: false; reason: string };

async function getAgentTool(path: string): Promise<AgentToolResponse> {
  try {
    const result = await getEnvelope<AgentToolResult>(path);
    return { available: true, result };
  } catch (e) {
    // 503 "unavailable" (no analysis service) and 404 "not_found" (analyzer
    // absent from this build) are typed degradations, same as fetchImpact's
    // gating; anything else — SchemaMismatchError above all — stays thrown.
    if (e instanceof ApiError && (e.code === "unavailable" || e.code === "not_found")) {
      return { available: false, reason: e.message };
    }
    throw e;
  }
}

/**
 * Related-files agent tool (EP-020). Same target/direction query shape as the
 * VS Code extension client so the shared HTTP surface sees identical requests.
 */
export async function relatedFiles(
  target: string,
  direction?: string,
): Promise<AgentToolResponse> {
  const dir = direction ? `&direction=${encodeURIComponent(direction)}` : "";
  return getAgentTool(
    `/analyze/related_files?target=${encodeURIComponent(target)}${dir}`,
  );
}

/** Change-risk agent tool (EP-020). */
export async function changeRisk(target: string): Promise<AgentToolResponse> {
  return getAgentTool(`/analyze/change_risk?target=${encodeURIComponent(target)}`);
}

/**
 * Agent-brief tool (EP-020). Callers should gate this on the /contract
 * advertisement (`hasResource(contract, "analyze/agent_brief")`); the typed
 * unavailable response is the backstop when the gate is skipped or stale.
 */
export async function agentBrief(topic?: string): Promise<AgentToolResponse> {
  const q = topic ? `?symbol=${encodeURIComponent(topic)}` : "";
  return getAgentTool(`/analyze/agent_brief${q}`);
}

/**
 * Lexical symbol search (FTS, case-insensitive per-token prefix match over
 * qualified names). Resolves a free-text seed into candidate node ids before
 * the neighborhood load — /query/neighborhood itself is an exact-id lookup.
 */
export async function searchSymbols(
  q: string,
  limit = 20,
): Promise<SearchMatch[]> {
  const res = await getEnvelope<SearchResult>(
    `/search?q=${encodeURIComponent(q)}&limit=${limit}`,
  );
  return res.matches;
}

/** Fetch the neighborhood of a seed symbol — the bounded graph endpoint (AC-1). */
export async function fetchNeighborhood(
  seed: string,
  depth = 2,
): Promise<QueryResult> {
  return getEnvelope<QueryResult>(
    `/query/neighborhood?symbol=${encodeURIComponent(seed)}&depth=${depth}`,
  );
}

/**
 * Fetch the blast-radius (impact) of a symbol via the NEGOTIATED analyzer route
 * (no hard-coded /analyze/impact — A3). `analyzerRoute` comes from getContract +
 * resolveAnalyzerRoute.
 */
export async function fetchImpact(
  analyzerRoute: string,
  symbol: string,
): Promise<ImpactResult> {
  return getEnvelope<ImpactResult>(
    `/${analyzerRoute}?symbol=${encodeURIComponent(symbol)}&direction=reverse`,
  );
}

export interface SSEHandlers {
  /** Called once with the schema_version read from the first `ready` frame. */
  onReady?: (serverVersion: number) => void;
  /** Called for each data event (e.g. ingest-completed). */
  onData?: (event: StreamEvent) => void;
  /** Called on a SchemaMismatchError or ApiError surfaced by the stream. */
  onError?: (err: Error) => void;
  /** Called when the server sends the terminal `bye` frame. */
  onBye?: () => void;
  /** Transport-level error (network); non-blocking, the UI may auto-retry. */
  onTransportError?: (err: Event) => void;
}

/**
 * Subscribe to the SSE stream using NAMED events (D3/D6). The server emits
 * `ready`/`bye`/`error` framing events plus data events (e.g. ingest-completed);
 * `es.onmessage` would never fire for these. The schema guard reads the version
 * from the `ready` frame (EventSource cannot send the request header). Returns
 * an unsubscribe function that closes the stream cleanly (no leak).
 *
 * `dataStreams` is the set of negotiated data event names from /contract.streams
 * (framing events ready/bye/error are always registered).
 */
export function subscribeSSE(
  dataStreams: string[],
  handlers: SSEHandlers,
): () => void {
  const es = new EventSource(`${BASE}/events`);
  let closed = false;
  const close = () => {
    if (!closed) {
      closed = true;
      es.close();
    }
  };

  es.addEventListener("ready", (ev) => {
    try {
      const data = JSON.parse((ev as MessageEvent).data) as {
        schema_version?: number;
      };
      if (data.schema_version !== SCHEMA_VERSION) {
        close();
        handlers.onError?.(
          new SchemaMismatchError(SCHEMA_VERSION, data.schema_version),
        );
        return;
      }
      handlers.onReady?.(data.schema_version);
    } catch {
      close();
      handlers.onError?.(new SchemaMismatchError(SCHEMA_VERSION, undefined));
    }
  });

  es.addEventListener("error", (ev) => {
    // A NAMED `error` frame carries an error envelope; a bare transport error
    // has no parseable data.
    const raw = (ev as MessageEvent).data as string | undefined;
    if (typeof raw === "string" && raw.length > 0) {
      try {
        const body = JSON.parse(raw) as ErrorEnvelope;
        const code = body.error?.code ?? "internal";
        close();
        handlers.onError?.(
          code === "schema_mismatch"
            ? new SchemaMismatchError(SCHEMA_VERSION, body.schema_version)
            : new ApiError(code, body.error?.message ?? "stream error"),
        );
        return;
      } catch {
        /* fall through to transport error */
      }
    }
    handlers.onTransportError?.(ev);
  });

  es.addEventListener("bye", () => {
    close();
    handlers.onBye?.();
  });

  // Register the negotiated data event listeners (e.g. ingest-completed).
  for (const name of dataStreams) {
    es.addEventListener(name, (ev) => {
      try {
        handlers.onData?.(JSON.parse((ev as MessageEvent).data) as StreamEvent);
      } catch {
        /* ignore malformed data frame */
      }
    });
  }

  return close;
}
