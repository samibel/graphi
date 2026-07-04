import { afterEach, describe, expect, it, vi } from "vitest";
import {
  agentBrief,
  ApiError,
  changeRisk,
  decodeEnvelope,
  getContract,
  hasResource,
  relatedFiles,
  resolveAnalyzerRoute,
  SchemaMismatchError,
  searchSymbols,
  subscribeSSE,
} from "./graphiClient";
import { SCHEMA_VERSION } from "./types";
import {
  agentToolPayload,
  contractDoc,
  contractNoAnalyzer,
  errorEnvelope,
  mismatchedSuccessEnvelope,
  queryPayload,
  searchPayload,
  sseByeFrame,
  sseDataFrame,
  sseErrorFrame,
  sseReadyFrame,
  sseReadyFrameMismatch,
  successEnvelope,
} from "./__fixtures__/contract";

function mkRes(body: unknown, status = 200): Response {
  return new Response(typeof body === "string" ? body : JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

// --- AC-4 decode guard matrix (Q1) -----------------------------------------
describe("decodeEnvelope — fail-closed schema guard (AC-4)", () => {
  it("(a) matching version → resolves with payload", async () => {
    const env = await decodeEnvelope<typeof queryPayload>(
      mkRes(successEnvelope(queryPayload)),
    );
    expect(env.payload.symbol).toBe("pkg.Func");
  });

  it("(b) 200 body version mismatch → SchemaMismatchError, no data", async () => {
    await expect(
      decodeEnvelope(mkRes(mismatchedSuccessEnvelope(queryPayload))),
    ).rejects.toBeInstanceOf(SchemaMismatchError);
  });

  it("(c) HTTP 412 → SchemaMismatchError", async () => {
    await expect(
      decodeEnvelope(mkRes(errorEnvelope("schema_mismatch", "bad version"), 412)),
    ).rejects.toBeInstanceOf(SchemaMismatchError);
  });

  it("(d) error envelope code schema_mismatch → SchemaMismatchError", async () => {
    await expect(
      decodeEnvelope(mkRes(errorEnvelope("schema_mismatch", "bad version"), 400)),
    ).rejects.toBeInstanceOf(SchemaMismatchError);
  });

  it("(e) 400/404/503/500 envelopes → ApiError (distinct from mismatch)", async () => {
    for (const [code, status] of [
      ["bad_request", 400],
      ["not_found", 404],
      ["unavailable", 503],
      ["internal", 500],
    ] as const) {
      const err = await decodeEnvelope(
        mkRes(errorEnvelope(code, `sanitized ${code}`), status),
      ).catch((e) => e);
      expect(err).toBeInstanceOf(ApiError);
      expect(err).not.toBeInstanceOf(SchemaMismatchError);
      expect((err as ApiError).code).toBe(code);
      expect((err as ApiError).message).toBe(`sanitized ${code}`);
    }
  });

  it("surfaces the client + server versions on the mismatch error", async () => {
    const err = (await decodeEnvelope(
      mkRes(mismatchedSuccessEnvelope(queryPayload)),
    ).catch((e) => e)) as SchemaMismatchError;
    expect(err.clientVersion).toBe(SCHEMA_VERSION);
    expect(err.serverVersion).toBe(SCHEMA_VERSION + 99);
  });
});

// --- /contract negotiation -------------------------------------------------
describe("getContract + resolveAnalyzerRoute", () => {
  it("unwraps the envelope the server actually sends (regression: the raw envelope leaked as the contract, so resources was undefined)", async () => {
    // The server wraps /contract in the standard success envelope — the
    // fixture MUST be envelope-shaped or this test cannot catch the bug.
    vi.stubGlobal("fetch", vi.fn(async () => mkRes(successEnvelope(contractDoc))));
    const c = await getContract();
    expect(c).not.toHaveProperty("payload");
    expect(Array.isArray(c.resources)).toBe(true);
    expect(Array.isArray(c.streams)).toBe(true);
    expect(c.resources).toContain("analyze/impact");
    expect(resolveAnalyzerRoute(c)).toBe("analyze/impact");
  });

  it("gates blast-radius off when no analyzer is injected", () => {
    expect(resolveAnalyzerRoute(contractNoAnalyzer)).toBeNull();
  });

  it("throws SchemaMismatchError on a mismatched OUTER envelope version", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => mkRes(mismatchedSuccessEnvelope(contractDoc))),
    );
    await expect(getContract()).rejects.toBeInstanceOf(SchemaMismatchError);
  });

  it("throws SchemaMismatchError on a mismatched INNER contract version", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        mkRes(successEnvelope({ ...contractDoc, schema_version: 999 })),
      ),
    );
    await expect(getContract()).rejects.toBeInstanceOf(SchemaMismatchError);
  });
});

// --- /search seed resolution ------------------------------------------------
describe("searchSymbols", () => {
  it("unwraps matches from the search envelope", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => mkRes(successEnvelope(searchPayload))),
    );
    const matches = await searchSymbols("release");
    expect(matches).toHaveLength(2);
    expect(matches[0].node_id).toBe("s1");
    expect(matches[0].qualified_name).toBe("main.release");
  });

  it("propagates the fail-closed guard on a mismatched envelope", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => mkRes(mismatchedSuccessEnvelope(searchPayload))),
    );
    await expect(searchSymbols("x")).rejects.toBeInstanceOf(SchemaMismatchError);
  });

  it("surfaces sanitized error envelopes as ApiError", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => mkRes(errorEnvelope("bad_request", "q required"), 400)),
    );
    await expect(searchSymbols("x")).rejects.toBeInstanceOf(ApiError);
  });
});

// --- EP-020 agent tools (related_files / change_risk / agent_brief) ---------
describe("agent tools — relatedFiles / changeRisk / agentBrief", () => {
  it("relatedFiles: unwraps the agent-tool envelope on the found path", async () => {
    const fetchMock = vi.fn<(url: string) => Promise<Response>>(async () =>
      mkRes(successEnvelope(agentToolPayload)),
    );
    vi.stubGlobal("fetch", fetchMock);
    const res = await relatedFiles("pkg.Func");
    expect(fetchMock.mock.calls[0][0]).toBe(
      "/analyze/related_files?target=pkg.Func",
    );
    if (!res.available) throw new Error("expected available");
    expect(res.result.outcome).toBe("ok");
    expect(res.result.items).toHaveLength(2);
    expect(res.result.items[0]).toMatchObject({
      ref_id: "pkg/a.go",
      rank: 1,
      evidence_ref_ids: ["ev1"],
    });
    expect(res.result.evidence[0]).toMatchObject({ path: "pkg/a.go", line: 10 });
    expect(res.result.confidence.top).toBe("high");
  });

  it("relatedFiles: forwards an explicit direction", async () => {
    const fetchMock = vi.fn<(url: string) => Promise<Response>>(async () =>
      mkRes(successEnvelope(agentToolPayload)),
    );
    vi.stubGlobal("fetch", fetchMock);
    await relatedFiles("pkg.Func", "reverse");
    expect(fetchMock.mock.calls[0][0]).toBe(
      "/analyze/related_files?target=pkg.Func&direction=reverse",
    );
  });

  it("changeRisk: hits /analyze/change_risk with the target", async () => {
    const fetchMock = vi.fn<(url: string) => Promise<Response>>(async () =>
      mkRes(successEnvelope(agentToolPayload)),
    );
    vi.stubGlobal("fetch", fetchMock);
    const res = await changeRisk("pkg.Func");
    expect(fetchMock.mock.calls[0][0]).toBe("/analyze/change_risk?target=pkg.Func");
    expect(res.available).toBe(true);
  });

  it("agentBrief: no topic → bare route; topic → symbol query", async () => {
    const fetchMock = vi.fn<(url: string) => Promise<Response>>(async () =>
      mkRes(successEnvelope(agentToolPayload)),
    );
    vi.stubGlobal("fetch", fetchMock);
    await agentBrief();
    await agentBrief("release flow");
    expect(fetchMock.mock.calls[0][0]).toBe("/analyze/agent_brief");
    expect(fetchMock.mock.calls[1][0]).toBe("/analyze/agent_brief?symbol=release%20flow");
  });

  it("503 unavailable → typed unavailable, no throw (degraded capability)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => mkRes(errorEnvelope("unavailable", "capability unavailable"), 503)),
    );
    for (const call of [
      () => relatedFiles("x"),
      () => changeRisk("x"),
      () => agentBrief("x"),
    ]) {
      const res = await call();
      expect(res).toEqual({ available: false, reason: "capability unavailable" });
    }
  });

  it("absent analyzer (404 not_found) → typed unavailable, no throw", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => mkRes(errorEnvelope("not_found", "no such analyzer"), 404)),
    );
    const res = await relatedFiles("x");
    expect(res).toEqual({ available: false, reason: "no such analyzer" });
  });

  it("schema mismatch still propagates fail-closed (never a typed result)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => mkRes(mismatchedSuccessEnvelope(agentToolPayload))),
    );
    await expect(relatedFiles("x")).rejects.toBeInstanceOf(SchemaMismatchError);
    await expect(changeRisk("x")).rejects.toBeInstanceOf(SchemaMismatchError);
    await expect(agentBrief()).rejects.toBeInstanceOf(SchemaMismatchError);
  });

  it("other error envelopes (bad_request/internal) still throw ApiError", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => mkRes(errorEnvelope("bad_request", "target required"), 400)),
    );
    await expect(relatedFiles("")).rejects.toBeInstanceOf(ApiError);
  });

  it("hasResource gates on the /contract advertisement", () => {
    expect(hasResource(contractDoc, "analyze/impact")).toBe(true);
    expect(hasResource(contractDoc, "analyze/agent_brief")).toBe(false);
  });
});

// --- Named SSE events (AC-6, D3/D6) ----------------------------------------
class MockEventSource {
  static last: MockEventSource | null = null;
  listeners = new Map<string, (ev: MessageEvent) => void>();
  closed = false;
  constructor(public url: string) {
    MockEventSource.last = this;
  }
  addEventListener(type: string, cb: (ev: MessageEvent) => void) {
    this.listeners.set(type, cb);
  }
  close() {
    this.closed = true;
  }
  emit(type: string, data: string) {
    this.listeners.get(type)?.({ data } as MessageEvent);
  }
}

describe("subscribeSSE — named events", () => {
  function install() {
    vi.stubGlobal("EventSource", MockEventSource as unknown as typeof EventSource);
  }

  it("captures schema_version from the ready frame", () => {
    install();
    const onReady = vi.fn();
    subscribeSSE(["ingest-completed"], { onReady });
    MockEventSource.last!.emit("ready", sseReadyFrame);
    expect(onReady).toHaveBeenCalledWith(SCHEMA_VERSION);
  });

  it("blocks (SchemaMismatchError) + closes on a mismatched ready frame", () => {
    install();
    const onError = vi.fn();
    subscribeSSE(["ingest-completed"], { onError });
    MockEventSource.last!.emit("ready", sseReadyFrameMismatch);
    expect(onError).toHaveBeenCalledTimes(1);
    expect(onError.mock.calls[0][0]).toBeInstanceOf(SchemaMismatchError);
    expect(MockEventSource.last!.closed).toBe(true);
  });

  it("delivers named data events (no reliance on onmessage)", () => {
    install();
    const onData = vi.fn();
    subscribeSSE(["ingest-completed"], { onData });
    MockEventSource.last!.emit("ready", sseReadyFrame);
    MockEventSource.last!.emit("ingest-completed", sseDataFrame);
    expect(onData).toHaveBeenCalledTimes(1);
    expect(onData.mock.calls[0][0].type).toBe("ingest-completed");
  });

  it("maps an error frame to the error envelope and closes", () => {
    install();
    const onError = vi.fn();
    subscribeSSE(["ingest-completed"], { onError });
    MockEventSource.last!.emit("error", sseErrorFrame);
    expect(onError.mock.calls[0][0]).toBeInstanceOf(ApiError);
    expect(MockEventSource.last!.closed).toBe(true);
  });

  it("closes cleanly on bye and on unsubscribe (no leak)", () => {
    install();
    const onBye = vi.fn();
    const unsub = subscribeSSE(["ingest-completed"], { onBye });
    MockEventSource.last!.emit("bye", sseByeFrame);
    expect(onBye).toHaveBeenCalled();
    expect(MockEventSource.last!.closed).toBe(true);
    unsub(); // idempotent
    expect(MockEventSource.last!.closed).toBe(true);
  });
});
