import { afterEach, describe, expect, it, vi } from "vitest";
import {
  decodeEnvelope,
  GraphiClient,
  hasResource,
  resolveAnalyzerRoute,
  resolveSearchMatches,
  SchemaMismatchError,
} from "./graphiClient";
import { assertLoopback } from "./loopback";
import { SCHEMA_VERSION, type Contract, type SearchMatch } from "./contract";

function mkRes(body: unknown, status = 200): Response {
  return new Response(typeof body === "string" ? body : JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

const okBody = (payload: unknown) => ({
  schema_version: SCHEMA_VERSION as number,
  payload,
});
const errBody = (code: string, message: string, v: number = SCHEMA_VERSION) => ({
  schema_version: v,
  error: { code, message },
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

// --- loopback hard precondition (S1) ---------------------------------------
describe("assertLoopback", () => {
  it("accepts loopback URLs", () => {
    expect(() => assertLoopback("http://127.0.0.1:8080")).not.toThrow();
    expect(() => assertLoopback("http://localhost:8080")).not.toThrow();
    expect(() => assertLoopback("http://[::1]:8080")).not.toThrow();
  });
  it("refuses non-loopback URLs (zero-outbound contract)", () => {
    expect(() => assertLoopback("http://0.0.0.0:8080")).toThrow(/non-loopback/);
    expect(() => assertLoopback("http://8.8.8.8:8080")).toThrow(/non-loopback/);
    expect(() => assertLoopback("http://example.com:8080")).toThrow(/non-loopback/);
  });
  it("refuses malformed URLs", () => {
    expect(() => assertLoopback("not a url")).toThrow(/invalid/);
  });
});

describe("GraphiClient constructor", () => {
  it("constructs on loopback", () => {
    expect(() => new GraphiClient("http://127.0.0.1:8080")).not.toThrow();
  });
  it("throws on non-loopback at construction (fails fast, no request)", () => {
    expect(() => new GraphiClient("http://10.0.0.1:8080")).toThrow(/non-loopback/);
  });
});

// --- AC-7 four-path fail-closed schema guard -------------------------------
describe("decodeEnvelope — fail-closed schema guard (AC-7)", () => {
  it("(a) matching version → resolves with payload", async () => {
    const env = await decodeEnvelope<{ symbol: string }>(
      mkRes(okBody({ symbol: "pkg.Func" })),
    );
    expect(env.payload.symbol).toBe("pkg.Func");
  });

  it("(b) 200 body version mismatch → SchemaMismatchError", async () => {
    await expect(
      decodeEnvelope(mkRes({ schema_version: 99, payload: {} })),
    ).rejects.toBeInstanceOf(SchemaMismatchError);
  });

  it("(c) HTTP 412 → SchemaMismatchError", async () => {
    await expect(
      decodeEnvelope(mkRes(errBody("schema_mismatch", "bad"), 412)),
    ).rejects.toBeInstanceOf(SchemaMismatchError);
  });

  it("(d) error-envelope code schema_mismatch → SchemaMismatchError", async () => {
    await expect(
      decodeEnvelope(mkRes(errBody("schema_mismatch", "bad"), 400)),
    ).rejects.toBeInstanceOf(SchemaMismatchError);
  });

  it("(e) error-envelope version mismatch → SchemaMismatchError", async () => {
    await expect(
      decodeEnvelope(mkRes(errBody("not_found", "x", 99), 404)),
    ).rejects.toBeInstanceOf(SchemaMismatchError);
  });

  it("(f) non-mismatch error envelope → ApiError(code,message)", async () => {
    await expect(
      decodeEnvelope(mkRes(errBody("not_found", "missing"), 404)),
    ).rejects.toMatchObject({ name: "ApiError", code: "not_found" });
  });
});

// --- read-only by construction (only GET issued) ---------------------------
describe("GraphiClient read-only surface", () => {
  it("issues only GET requests for query/search/impact/health", async () => {
    const nodeID = "0123456789abcdef";
    const methods: string[] = [];
    vi.stubGlobal("fetch", (url: string, init?: RequestInit) => {
      methods.push((init?.method ?? "GET").toUpperCase());
      if (String(url).includes("/healthz")) {
        return Promise.resolve(
          mkRes({ status: "ok", schema_version: SCHEMA_VERSION }),
        );
      }
      return Promise.resolve(mkRes(okBody({ symbol: "x", nodes: [], edges: [] })));
    });
    const c = new GraphiClient("http://127.0.0.1:8080");
    await c.health();
    await c.getNeighborhood(nodeID);
    await c.getDefinition(nodeID);
    await c.getReferences(nodeID);
    await c.getImpact("analyze/impact", nodeID);
    await c.search("x");
    expect(methods.every((m) => m === "GET")).toBe(true);
    // No mutating verb exists on the class surface.
    expect((c as unknown as Record<string, unknown>).post).toBeUndefined();
    expect((c as unknown as Record<string, unknown>).delete).toBeUndefined();
  });

  it("health() fails closed on schema mismatch", async () => {
    vi.stubGlobal("fetch", () =>
      Promise.resolve(mkRes({ status: "ok", schema_version: 99 })),
    );
    const c = new GraphiClient("http://127.0.0.1:8080");
    await expect(c.health()).rejects.toBeInstanceOf(SchemaMismatchError);
  });

  it("getContract fails closed on 412", async () => {
    vi.stubGlobal("fetch", () =>
      Promise.resolve(mkRes(errBody("schema_mismatch", "x"), 412)),
    );
    const c = new GraphiClient("http://127.0.0.1:8080");
    await expect(c.getContract()).rejects.toBeInstanceOf(SchemaMismatchError);
  });

  it("getContract unwraps the server's success envelope", async () => {
    const contract: Contract = {
      schema_version: SCHEMA_VERSION,
      resources: ["search", "query/definition", "analyze/impact"],
      streams: ["ready", "bye", "error"],
    };
    vi.stubGlobal("fetch", () => Promise.resolve(mkRes(okBody(contract))));
    const c = new GraphiClient("http://127.0.0.1:8080");
    const result = await c.getContract();
    expect(result).toEqual(contract);
    expect(result).not.toHaveProperty("payload");
  });

  it("getContract fails closed on a mismatched inner contract version", async () => {
    vi.stubGlobal("fetch", () =>
      Promise.resolve(
        mkRes(
          okBody({
            schema_version: 99,
            resources: ["search"],
            streams: [],
          }),
        ),
      ),
    );
    const c = new GraphiClient("http://127.0.0.1:8080");
    await expect(c.getContract()).rejects.toBeInstanceOf(SchemaMismatchError);
  });

  it("getContract rejects a same-version payload that violates the contract shape", async () => {
    vi.stubGlobal("fetch", () =>
      Promise.resolve(
        mkRes(
          okBody({
            schema_version: SCHEMA_VERSION,
            resources: "search",
            streams: [],
          }),
        ),
      ),
    );
    const c = new GraphiClient("http://127.0.0.1:8080");
    await expect(c.getContract()).rejects.toMatchObject({
      name: "ApiError",
      code: "internal",
    });
  });

  it("does not advertise unsupported bearer authentication", async () => {
    let seenAuth: string | undefined;
    vi.stubGlobal("fetch", (_url: string, init?: RequestInit) => {
      seenAuth = (init?.headers as Record<string, string>)?.Authorization;
      return Promise.resolve(mkRes(okBody({ symbol: "x", nodes: [], edges: [] })));
    });
    const c = new GraphiClient("http://127.0.0.1:8080");
    await c.getNeighborhood("0123456789abcdef");
    expect(seenAuth).toBeUndefined();
  });

  it("blocks a plain symbol before any exact-NodeId request is issued", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const c = new GraphiClient("http://127.0.0.1:8080");
    await expect(c.getNeighborhood("Foo")).rejects.toMatchObject({
      name: "ApiError",
      code: "bad_request",
    });
    await expect(c.getImpact("analyze/impact", "Foo")).rejects.toMatchObject({
      name: "ApiError",
      code: "bad_request",
    });
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

// --- lexical text -> exact NodeId resolution -------------------------------
describe("resolveSearchMatches / exact NodeId boundary", () => {
  const match = (
    nodeID: string,
    qualifiedName: string,
    rank: number,
  ): SearchMatch => ({
    node_id: nodeID,
    kind: "function",
    qualified_name: qualifiedName,
    source_path: `${nodeID}.go`,
    line: 7,
    column: 3,
    rank,
  });

  it("rejects fuzzy hits and resolves one exact final-name match", () => {
    const result = resolveSearchMatches("Foo", [
      match("1111111111111111", "pkg.FooFactory", -10),
      match("2222222222222222", "pkg.Foo", -1),
    ]);
    expect(result).toEqual({
      outcome: "found",
      matches: [expect.objectContaining({ node_id: "2222222222222222" })],
    });
  });

  it("preserves ambiguity instead of silently choosing ranked result zero", () => {
    const result = resolveSearchMatches("Foo", [
      match("1111111111111111", "one.Foo", -100),
      match("2222222222222222", "two.Foo", -1),
    ]);
    expect(result.outcome).toBe("ambiguous");
    expect(result.matches.map((m) => m.node_id)).toEqual([
      "1111111111111111",
      "2222222222222222",
    ]);
  });

  it("reports not_found when search has no exact identity candidate", () => {
    expect(
      resolveSearchMatches("Foo", [match("fuzzy", "pkg.FooFactory", -10)]),
    ).toEqual({ outcome: "not_found", matches: [] });
  });

  it("rejects a malformed node_id even when the qualified name is exact", () => {
    expect(resolveSearchMatches("Foo", [match("Foo", "pkg.Foo", -1)])).toEqual({
      outcome: "not_found",
      matches: [],
    });
  });

  it("never sends the plain editor identifier to exact-NodeId endpoints", async () => {
    const urls: string[] = [];
    vi.stubGlobal("fetch", (input: string | URL | Request) => {
      const url = String(input);
      urls.push(url);
      if (url.includes("/search?")) {
        return Promise.resolve(
          mkRes(
            okBody({
              query: "Foo",
              matches: [match("0123456789abcdef", "pkg.Foo", -1)],
            }),
          ),
        );
      }
      return Promise.resolve(
        mkRes(
          okBody({
            operation: "definition",
            symbol: "0123456789abcdef",
            outcome: "empty",
            nodes: [],
            edges: [],
          }),
        ),
      );
    });

    const c = new GraphiClient("http://127.0.0.1:8080");
    const resolved = await c.resolveSymbol("Foo");
    expect(resolved.outcome).toBe("found");
    if (resolved.outcome !== "found") throw new Error("expected one exact match");
    const nodeID = resolved.matches[0].node_id;
    await c.getDefinition(nodeID);
    await c.getReferences(nodeID);
    await c.getImpact("analyze/impact", nodeID);

    const exactCalls = urls.filter((url) => !url.includes("/search?"));
    expect(exactCalls).toHaveLength(3);
    expect(exactCalls.every((url) => url.includes("symbol=0123456789abcdef"))).toBe(true);
    expect(exactCalls.every((url) => !url.includes("symbol=Foo"))).toBe(true);
  });
});

// --- contract negotiation ---------------------------------------------------
describe("resolveAnalyzerRoute / hasResource", () => {
  const contract: Contract = {
    schema_version: SCHEMA_VERSION,
    resources: ["query/neighborhood", "search", "analyze/impact"],
    streams: ["ingest-completed", "ready", "bye", "error"],
  };
  it("prefers an impact analyzer", () => {
    expect(resolveAnalyzerRoute(contract)).toBe("analyze/impact");
  });
  it("returns null when no analyzer is advertised", () => {
    expect(
      resolveAnalyzerRoute({ ...contract, resources: ["search"] }),
    ).toBeNull();
  });
  it("does not substitute an unrelated analyzer when impact is absent", () => {
    expect(
      resolveAnalyzerRoute({
        ...contract,
        resources: ["search", "analyze/agent_brief", "analyze/change_risk"],
      }),
    ).toBeNull();
  });
  it("accepts an explicitly advertised blast-radius alias", () => {
    expect(
      resolveAnalyzerRoute({
        ...contract,
        resources: ["search", "analyze/blast_radius"],
      }),
    ).toBe("analyze/blast_radius");
  });
  it("detects advertised resources for per-feature degradation", () => {
    expect(hasResource(contract, "query/neighborhood")).toBe(true);
    expect(hasResource(contract, "analyze/missing")).toBe(false);
  });
});
