import { afterEach, describe, expect, it, vi } from "vitest";
import {
  decodeEnvelope,
  GraphiClient,
  hasResource,
  resolveAnalyzerRoute,
  SchemaMismatchError,
} from "./graphiClient";
import { assertLoopback } from "./loopback";
import { SCHEMA_VERSION, type Contract } from "./contract";

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
    await c.getNeighborhood("x");
    await c.getImpact("analyze/impact", "x");
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

  it("sends Authorization header from token provider (never in URL)", async () => {
    let seenAuth: string | undefined;
    let seenUrl = "";
    vi.stubGlobal("fetch", (url: string, init?: RequestInit) => {
      seenUrl = String(url);
      seenAuth = (init?.headers as Record<string, string>)?.Authorization;
      return Promise.resolve(mkRes(okBody({ symbol: "x", nodes: [], edges: [] })));
    });
    const c = new GraphiClient("http://127.0.0.1:8080", () =>
      Promise.resolve("secret"),
    );
    await c.getNeighborhood("x");
    expect(seenAuth).toBe("Bearer secret");
    expect(seenUrl).not.toContain("secret");
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
  it("detects advertised resources for per-feature degradation", () => {
    expect(hasResource(contract, "query/neighborhood")).toBe(true);
    expect(hasResource(contract, "analyze/missing")).toBe(false);
  });
});
