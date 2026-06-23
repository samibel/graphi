// graphiClient is the ONLY backend boundary for the VS Code extension. It talks
// to the local graphi HTTP daemon and is READ-ONLY BY CONSTRUCTION: only GET
// query/analyze/search/health methods exist — no write/refactor/undo verbs are
// defined, so the extension cannot mutate the graph or workspace.
//
// Local-first / zero-outbound: assertLoopback rejects any non-loopback daemon
// URL, mirroring SW-039's http.AssertLoopback. The extension never dials
// anything but 127.0.0.1/localhost/::1.
import type {
  Envelope,
  ImpactResult,
  QueryResult,
  SearchResult,
} from "./contract";
import { SCHEMA_VERSION } from "./contract";

/** assertLoopback throws if url is not a loopback daemon address. */
export function assertLoopback(url: string): void {
  let u: URL;
  try {
    u = new URL(url);
  } catch {
    throw new Error(`graphi: invalid daemon URL "${url}"`);
  }
  const host = u.hostname.toLowerCase();
  const loopback = new Set(["127.0.0.1", "localhost", "::1", "[::1]"]);
  if (!loopback.has(host)) {
    throw new Error(
      `graphi: refusing non-loopback daemon URL "${url}" (local-first, loopback-only)`,
    );
  }
}

export class GraphiClient {
  constructor(private readonly baseUrl: string) {
    assertLoopback(baseUrl);
  }

  private async get<T>(path: string): Promise<T> {
    const res = await fetch(`${this.baseUrl}${path}`, {
      headers: { "X-Graphi-Schema-Version": String(SCHEMA_VERSION) },
    });
    if (res.status === 412) {
      throw new Error(`graphi: schema version mismatch (want v${SCHEMA_VERSION})`);
    }
    if (!res.ok) {
      throw new Error(`graphi ${path}: HTTP ${res.status}`);
    }
    return (await res.json()) as Envelope<T> extends Envelope<infer P> ? P : never;
  }

  /** Read-only: neighborhood of a symbol (the graph payload for the webview). */
  async getNeighborhood(symbol: string, depth = 2): Promise<QueryResult> {
    return this.get<QueryResult>(
      `/query/neighborhood?symbol=${encodeURIComponent(symbol)}&depth=${depth}`,
    );
  }

  /** Read-only: blast-radius (impact, reverse). */
  async getImpact(symbol: string): Promise<ImpactResult> {
    return this.get<ImpactResult>(
      `/analyze/impact?symbol=${encodeURIComponent(symbol)}&direction=reverse`,
    );
  }

  /** Read-only: lexical/symbol search. */
  async search(q: string, limit = 50): Promise<SearchResult> {
    return this.get<SearchResult>(
      `/search?q=${encodeURIComponent(q)}&limit=${limit}`,
    );
  }

  /** Read-only: health check (used by connection status). */
  async health(): Promise<{ status: string; schema_version: number }> {
    const res = await fetch(`${this.baseUrl}/healthz`);
    if (!res.ok) throw new Error(`graphi: health HTTP ${res.status}`);
    return (await res.json()) as { status: string; schema_version: number };
  }
}
