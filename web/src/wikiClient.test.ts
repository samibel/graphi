import { afterEach, describe, expect, it, vi } from "vitest";
import {
  fetchWikiIndex,
  fetchWikiPage,
  WikiError,
  WikiPageNotFoundError,
  WikiUnavailableError,
} from "./wikiClient";
import { community1Md, indexMd } from "./__fixtures__/wiki";

function mkText(body: string, status = 200): Response {
  return new Response(body, {
    status,
    headers: { "content-type": "text/markdown; charset=utf-8" },
  });
}

function mkErrEnvelope(code: string, message: string, status: number): Response {
  return new Response(
    JSON.stringify({ schema_version: 1, error: { code, message } }),
    { status, headers: { "content-type": "application/json" } },
  );
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("wikiClient — dedicated non-envelope wiki fetch (Slice 1)", () => {
  it("200 → returns the raw markdown VERBATIM (no mutation, AC-2)", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => mkText(indexMd)));
    const body = await fetchWikiIndex();
    expect(body).toBe(indexMd); // byte-for-byte passthrough
  });

  it("fetchWikiPage 200 → raw markdown verbatim", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => mkText(community1Md)));
    expect(await fetchWikiPage("1")).toBe(community1Md);
  });

  it("/wiki 404 → WikiUnavailableError (wiki disabled, NOT empty)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => mkErrEnvelope("not_found", "wiki unavailable", 404)),
    );
    const err = await fetchWikiIndex().catch((e) => e);
    expect(err).toBeInstanceOf(WikiUnavailableError);
    expect((err as WikiUnavailableError).message).toBe("wiki unavailable");
  });

  it("/wiki/c/x 404 → WikiPageNotFoundError carrying the id", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => mkErrEnvelope("not_found", "unknown community", 404)),
    );
    const err = await fetchWikiPage("999").catch((e) => e);
    expect(err).toBeInstanceOf(WikiPageNotFoundError);
    expect((err as WikiPageNotFoundError).id).toBe("999");
  });

  it("WikiUnavailableError is distinct from WikiPageNotFoundError (state distinction)", () => {
    expect(new WikiUnavailableError()).not.toBeInstanceOf(WikiPageNotFoundError);
    expect(new WikiPageNotFoundError("1")).not.toBeInstanceOf(
      WikiUnavailableError,
    );
  });

  it("non-404 non-2xx → WikiError with status", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => mkText("boom", 503)));
    const err = await fetchWikiIndex().catch((e) => e);
    expect(err).toBeInstanceOf(WikiError);
    expect((err as WikiError).status).toBe(503);
  });

  it("tolerates a 404 with no/garbled body (no schema guard on this surface)", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("", { status: 404 })));
    const err = await fetchWikiIndex().catch((e) => e);
    expect(err).toBeInstanceOf(WikiUnavailableError);
    expect((err as WikiUnavailableError).message).toBe("wiki unavailable");
  });
});

describe("wikiClient — read-only / loopback guards (Slice 6, AC-6)", () => {
  type FetchCalls = [url: string, init?: RequestInit][];

  it("issues ONLY GET for both routes (no PUT/POST/DELETE)", async () => {
    const fetchMock = vi.fn(async () => mkText(indexMd));
    vi.stubGlobal("fetch", fetchMock);
    await fetchWikiIndex();
    await fetchWikiPage("1");
    for (const call of fetchMock.mock.calls as unknown as FetchCalls) {
      expect(call[1]?.method).toBe("GET");
    }
  });

  it("targets only the /wiki* paths on the loopback base", async () => {
    const fetchMock = vi.fn(async () => mkText(indexMd));
    vi.stubGlobal("fetch", fetchMock);
    await fetchWikiIndex();
    await fetchWikiPage("42");
    const urls = (fetchMock.mock.calls as unknown as FetchCalls).map((c) =>
      String(c[0]),
    );
    // BASE is "" in tests (same-origin / loopback proxy) → relative /wiki paths.
    expect(urls[0]).toBe("/wiki");
    expect(urls[1]).toBe("/wiki/c/42");
    for (const u of urls) expect(/^https?:\/\//.test(u)).toBe(false);
  });

  it("URL-encodes the id without re-deriving it (preservation of identity)", async () => {
    const fetchMock = vi.fn(async () => mkText(community1Md));
    vi.stubGlobal("fetch", fetchMock);
    await fetchWikiPage("a/b");
    const calls = fetchMock.mock.calls as unknown as FetchCalls;
    expect(String(calls[0][0])).toBe("/wiki/c/a%2Fb");
  });
});
