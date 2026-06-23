// Host-side SSE reader. The browser `EventSource` is NOT available in the VS
// Code extension host (Node), so we stream /events with global `fetch` +
// `Response.body` (Node 18+) and parse the `event:`/`id:`/`data:` line protocol
// by hand. This is the SINGLE owner of stream state in the host; the webview
// never opens a stream (it is fed via postMessage). Reconnect uses bounded
// exponential backoff + jitter (caps from config) and a manual reset
// (`retryNow`). All work is async and off the UI thread.
//
// The schema guard is fail-closed on the `ready` frame (mirrors the web client's
// subscribeSSE): a mismatched version is a blocking SchemaMismatchError, not a
// transport retry. Named-event dispatch routes framing events (ready/bye/error)
// and negotiated data streams separately.
import { SCHEMA_VERSION, type ErrorEnvelope } from "./contract";
import { ApiError, SchemaMismatchError, type TokenProvider } from "./graphiClient";

export interface SseReconnectConfig {
  /** Max reconnect attempts before giving up (0 = unlimited). */
  maxAttempts: number;
  /** Cap for the exponential backoff interval, milliseconds. */
  maxIntervalMs: number;
  /** Base interval used for the first backoff step, milliseconds. */
  baseIntervalMs?: number;
}

export interface SseHandlers {
  /** Connected: first `ready` frame received and schema-validated. */
  onConnected?: (serverVersion: number) => void;
  /** Disconnected: transport dropped or `bye`; reconnect (if any) is scheduled. */
  onDisconnected?: (reason: string) => void;
  /** A negotiated data event (e.g. ingest-completed). */
  onData?: (eventName: string, data: unknown) => void;
  /** A blocking schema mismatch surfaced from the stream — fail-closed. */
  onSchemaMismatch?: (err: SchemaMismatchError) => void;
  /** A non-mismatch API error carried by a named `error` frame. */
  onApiError?: (err: ApiError) => void;
}

/** Compute the bounded backoff delay (exponential + full jitter) for an attempt. */
export function backoffDelay(
  attempt: number,
  cfg: SseReconnectConfig,
): number {
  const base = cfg.baseIntervalMs ?? 500;
  const exp = Math.min(cfg.maxIntervalMs, base * 2 ** Math.max(0, attempt - 1));
  // Full jitter: random in [0, exp]. Keeps reconnect storms uncorrelated.
  return Math.floor(Math.random() * exp);
}

interface ParsedFrame {
  event: string;
  data: string;
}

/**
 * Pure SSE line-protocol parser. Feeds a text chunk; returns completed frames
 * and the unconsumed tail. Frames are separated by a blank line; lines starting
 * with `event:`/`id:`/`data:` set fields; `data:` lines accumulate (joined by
 * newline). Exported for unit testing the manual parser.
 */
export function parseSseChunk(
  buffer: string,
): { frames: ParsedFrame[]; rest: string } {
  const frames: ParsedFrame[] = [];
  // Normalize CRLF then split into complete records on the blank-line boundary.
  const normalized = buffer.replace(/\r\n/g, "\n");
  const records = normalized.split("\n\n");
  // The last element is a (possibly incomplete) trailing record — keep it.
  const rest = records.pop() ?? "";
  for (const rec of records) {
    if (rec.trim() === "") continue;
    let event = "message";
    const dataLines: string[] = [];
    for (const rawLine of rec.split("\n")) {
      if (rawLine.startsWith(":")) continue; // comment/keepalive
      const idx = rawLine.indexOf(":");
      const field = idx === -1 ? rawLine : rawLine.slice(0, idx);
      // Per spec, a single leading space after the colon is stripped.
      let value = idx === -1 ? "" : rawLine.slice(idx + 1);
      if (value.startsWith(" ")) value = value.slice(1);
      if (field === "event") event = value;
      else if (field === "data") dataLines.push(value);
      // `id:` is parsed but not retained (no Last-Event-ID resume in scope).
    }
    frames.push({ event, data: dataLines.join("\n") });
  }
  return { frames, rest };
}

export class SseClient {
  private abort: AbortController | null = null;
  private attempt = 0;
  private retryTimer: ReturnType<typeof setTimeout> | null = null;
  private stopped = true;
  private fatal = false; // schema mismatch is terminal until config change

  constructor(
    private readonly baseUrl: string,
    private readonly dataStreams: string[],
    private readonly cfg: SseReconnectConfig,
    private readonly handlers: SseHandlers,
    private readonly tokenProvider?: TokenProvider,
  ) {}

  /** Begin streaming (idempotent). Off-thread; never blocks the caller. */
  start(): void {
    if (!this.stopped) return;
    this.stopped = false;
    this.fatal = false;
    this.attempt = 0;
    void this.connect();
  }

  /** Stop streaming and cancel any pending reconnect. Clean, leak-free. */
  stop(): void {
    this.stopped = true;
    if (this.retryTimer) {
      clearTimeout(this.retryTimer);
      this.retryTimer = null;
    }
    this.abort?.abort();
    this.abort = null;
  }

  /** Manual reset (graphi.retry): clears backoff/fatal state and reconnects now. */
  retryNow(): void {
    this.stop();
    this.start();
  }

  private scheduleReconnect(reason: string): void {
    this.handlers.onDisconnected?.(reason);
    if (this.stopped || this.fatal) return;
    this.attempt += 1;
    if (this.cfg.maxAttempts > 0 && this.attempt > this.cfg.maxAttempts) {
      this.stopped = true; // bounded: give up after the cap
      return;
    }
    const delay = backoffDelay(this.attempt, this.cfg);
    this.retryTimer = setTimeout(() => {
      this.retryTimer = null;
      if (!this.stopped && !this.fatal) void this.connect();
    }, delay);
  }

  private dispatch(frame: ParsedFrame): void {
    switch (frame.event) {
      case "ready": {
        let version: number | undefined;
        try {
          version = (JSON.parse(frame.data) as { schema_version?: number })
            .schema_version;
        } catch {
          version = undefined;
        }
        if (version !== SCHEMA_VERSION) {
          this.fatal = true;
          this.abort?.abort();
          this.handlers.onSchemaMismatch?.(
            new SchemaMismatchError(SCHEMA_VERSION, version),
          );
          return;
        }
        this.attempt = 0; // healthy connection resets backoff
        this.handlers.onConnected?.(version);
        return;
      }
      case "bye":
        this.scheduleReconnect("stream closed by server");
        return;
      case "error": {
        try {
          const body = JSON.parse(frame.data) as ErrorEnvelope;
          const code = body.error?.code ?? "internal";
          if (code === "schema_mismatch") {
            this.fatal = true;
            this.abort?.abort();
            this.handlers.onSchemaMismatch?.(
              new SchemaMismatchError(SCHEMA_VERSION, body.schema_version),
            );
          } else {
            this.handlers.onApiError?.(
              new ApiError(code, body.error?.message ?? "stream error"),
            );
          }
        } catch {
          /* malformed error frame — ignore */
        }
        return;
      }
      default:
        if (this.dataStreams.includes(frame.event)) {
          let data: unknown;
          try {
            data = JSON.parse(frame.data);
          } catch {
            data = undefined;
          }
          this.handlers.onData?.(frame.event, data);
        }
    }
  }

  private async connect(): Promise<void> {
    this.abort = new AbortController();
    const headers: Record<string, string> = {
      Accept: "text/event-stream",
      "X-Graphi-Schema-Version": String(SCHEMA_VERSION),
    };
    const token = await this.tokenProvider?.();
    if (token) headers.Authorization = `Bearer ${token}`;
    try {
      const res = await fetch(`${this.baseUrl}/events`, {
        method: "GET",
        headers,
        signal: this.abort.signal,
      });
      if (res.status === 412) {
        this.fatal = true;
        this.handlers.onSchemaMismatch?.(new SchemaMismatchError(SCHEMA_VERSION));
        return;
      }
      if (!res.ok || !res.body) {
        this.scheduleReconnect(`HTTP ${res.status}`);
        return;
      }
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      for (;;) {
        const { value, done } = await reader.read();
        if (done) {
          this.scheduleReconnect("stream ended");
          return;
        }
        buffer += decoder.decode(value, { stream: true });
        const { frames, rest } = parseSseChunk(buffer);
        buffer = rest;
        for (const frame of frames) {
          this.dispatch(frame);
          if (this.fatal) return; // stop consuming after a blocking mismatch
        }
      }
    } catch (e) {
      if (this.stopped || this.fatal) return; // aborted intentionally
      this.scheduleReconnect(e instanceof Error ? e.message : "transport error");
    }
  }
}
