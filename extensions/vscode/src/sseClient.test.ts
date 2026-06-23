import { afterEach, describe, expect, it, vi } from "vitest";
import {
  backoffDelay,
  parseSseChunk,
  SseClient,
  type SseReconnectConfig,
} from "./sseClient";
import { SCHEMA_VERSION } from "./contract";
import { SchemaMismatchError } from "./graphiClient";

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("parseSseChunk — manual line parser", () => {
  it("parses event + data frames and keeps the incomplete tail", () => {
    const { frames, rest } = parseSseChunk(
      "event: ready\ndata: {\"schema_version\":1}\n\nevent: ingest-completed\ndata: {\"type\":\"x\"}\n\nevent: par",
    );
    expect(frames).toHaveLength(2);
    expect(frames[0].event).toBe("ready");
    expect(JSON.parse(frames[0].data).schema_version).toBe(1);
    expect(frames[1].event).toBe("ingest-completed");
    expect(rest).toContain("event: par");
  });

  it("handles CRLF and multi-line data and id lines", () => {
    const { frames } = parseSseChunk(
      "id: 5\r\nevent: error\r\ndata: {\"a\":1}\r\n\r\n",
    );
    expect(frames[0].event).toBe("error");
    expect(JSON.parse(frames[0].data).a).toBe(1);
  });

  it("ignores comment/keepalive lines", () => {
    const { frames } = parseSseChunk(": keepalive\nevent: bye\ndata: {}\n\n");
    expect(frames[0].event).toBe("bye");
  });
});

describe("backoffDelay — bounded exponential + jitter", () => {
  const cfg: SseReconnectConfig = { maxAttempts: 5, maxIntervalMs: 8000, baseIntervalMs: 500 };
  it("never exceeds the configured cap", () => {
    for (let attempt = 1; attempt <= 20; attempt++) {
      expect(backoffDelay(attempt, cfg)).toBeLessThanOrEqual(cfg.maxIntervalMs);
      expect(backoffDelay(attempt, cfg)).toBeGreaterThanOrEqual(0);
    }
  });
});

// Build a streaming Response whose body emits the given chunks then ends.
function streamRes(chunks: string[], status = 200): Response {
  const enc = new TextEncoder();
  let i = 0;
  const body = new ReadableStream<Uint8Array>({
    pull(controller) {
      if (i < chunks.length) {
        controller.enqueue(enc.encode(chunks[i++]));
      } else {
        controller.close();
      }
    },
  });
  return new Response(body, { status });
}

describe("SseClient — ready-frame schema guard + reconnect bound", () => {
  it("fires onConnected on a matching ready frame", async () => {
    vi.stubGlobal("fetch", () =>
      Promise.resolve(
        streamRes([`event: ready\ndata: {"schema_version":${SCHEMA_VERSION}}\n\n`]),
      ),
    );
    const connected = vi.fn();
    const sse = new SseClient(
      "http://127.0.0.1:8080",
      ["ingest-completed"],
      { maxAttempts: 0, maxIntervalMs: 10, baseIntervalMs: 1 },
      { onConnected: connected, onDisconnected: () => {} },
    );
    sse.start();
    await vi.waitFor(() => expect(connected).toHaveBeenCalledWith(SCHEMA_VERSION));
    sse.stop();
  });

  it("fails closed (onSchemaMismatch) on a mismatched ready frame", async () => {
    vi.stubGlobal("fetch", () =>
      Promise.resolve(streamRes([`event: ready\ndata: {"schema_version":99}\n\n`])),
    );
    const mismatch = vi.fn();
    const connected = vi.fn();
    const sse = new SseClient(
      "http://127.0.0.1:8080",
      [],
      { maxAttempts: 0, maxIntervalMs: 10, baseIntervalMs: 1 },
      { onConnected: connected, onSchemaMismatch: mismatch },
    );
    sse.start();
    await vi.waitFor(() =>
      expect(mismatch).toHaveBeenCalledWith(expect.any(SchemaMismatchError)),
    );
    expect(connected).not.toHaveBeenCalled();
    sse.stop();
  });

  it("dispatches negotiated data events", async () => {
    vi.stubGlobal("fetch", () =>
      Promise.resolve(
        streamRes([
          `event: ready\ndata: {"schema_version":${SCHEMA_VERSION}}\n\n`,
          `event: ingest-completed\ndata: {"type":"ingest-completed"}\n\n`,
        ]),
      ),
    );
    const onData = vi.fn();
    const sse = new SseClient(
      "http://127.0.0.1:8080",
      ["ingest-completed"],
      { maxAttempts: 0, maxIntervalMs: 10, baseIntervalMs: 1 },
      { onData },
    );
    sse.start();
    await vi.waitFor(() =>
      expect(onData).toHaveBeenCalledWith("ingest-completed", expect.anything()),
    );
    sse.stop();
  });

  it("bounds reconnect attempts and stays disconnected after the cap", async () => {
    let calls = 0;
    vi.stubGlobal("fetch", () => {
      calls += 1;
      return Promise.resolve(streamRes([], 503)); // always unavailable
    });
    const disconnected = vi.fn();
    const sse = new SseClient(
      "http://127.0.0.1:8080",
      [],
      { maxAttempts: 2, maxIntervalMs: 5, baseIntervalMs: 1 },
      { onDisconnected: disconnected },
    );
    sse.start();
    // initial connect + 2 bounded retries = 3 fetches, then it gives up.
    await vi.waitFor(() => expect(calls).toBeGreaterThanOrEqual(3));
    const after = calls;
    await new Promise((r) => setTimeout(r, 30));
    expect(calls).toBe(after); // no more attempts after the cap
    sse.stop();
  });

  it("412 on the stream is a fatal schema mismatch (no reconnect)", async () => {
    vi.stubGlobal("fetch", () => Promise.resolve(streamRes([], 412)));
    const mismatch = vi.fn();
    const disconnected = vi.fn();
    const sse = new SseClient(
      "http://127.0.0.1:8080",
      [],
      { maxAttempts: 5, maxIntervalMs: 5, baseIntervalMs: 1 },
      { onSchemaMismatch: mismatch, onDisconnected: disconnected },
    );
    sse.start();
    await vi.waitFor(() => expect(mismatch).toHaveBeenCalled());
    sse.stop();
  });
});
