// graphiClient is the single backend boundary for the web client. Every call
// goes to the SW-039 HTTP/SSE surface at the configured base URL (loopback
// daemon). No other network calls are made (zero-outbound client contract).
import type {
  Envelope,
  ImpactResult,
  QueryResult,
  StreamEvent,
} from "./types";
import { SCHEMA_VERSION } from "./types";

const BASE = (import.meta.env.VITE_GRAPHI_URL as string | undefined) ?? "";

async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { "X-Graphi-Schema-Version": String(SCHEMA_VERSION) },
  });
  if (res.status === 412) {
    throw new Error(`schema version mismatch (client expects v${SCHEMA_VERSION})`);
  }
  if (!res.ok) {
    throw new Error(`graphi ${path}: HTTP ${res.status}`);
  }
  return (await res.json()) as T;
}

/** Fetch the neighborhood of a seed symbol — the bounded graph endpoint. */
export async function fetchNeighborhood(
  seed: string,
  depth = 2,
): Promise<QueryResult> {
  const env = await getJSON<Envelope<QueryResult>>(
    `/query/neighborhood?symbol=${encodeURIComponent(seed)}&depth=${depth}`,
  );
  return env.payload;
}

/** Fetch the blast-radius (impact, reverse) of a symbol. */
export async function fetchImpact(symbol: string): Promise<ImpactResult> {
  const env = await getJSON<Envelope<ImpactResult>>(
    `/analyze/impact?symbol=${encodeURIComponent(symbol)}&direction=reverse`,
  );
  return env.payload;
}

/**
 * Subscribe to the SSE event stream. Returns an unsubscribe function. The
 * callback is invoked for each event; errors are surfaced via onError so the UI
 * can retry without blocking interaction.
 */
export function subscribeSSE(
  onEvent: (e: StreamEvent) => void,
  onError?: (err: Event) => void,
): () => void {
  const es = new EventSource(`${BASE}/events`);
  es.onmessage = (msg) => {
    try {
      onEvent(JSON.parse(msg.data) as StreamEvent);
    } catch {
      /* ignore malformed lines */
    }
  };
  es.onerror = (err) => {
    if (onError) onError(err);
  };
  return () => es.close();
}
