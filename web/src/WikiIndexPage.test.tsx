import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { WikiIndexPage } from "./WikiIndexPage";
import { _clearWikiCache } from "./wikiCache";
import { renderAt, flush } from "./testRender";
import {
  indexMd,
  indexEmptyMd,
  indexLargeMd,
  indexSingleMd,
} from "./__fixtures__/wiki";

// Mock the dedicated wiki fetch path so each state is driven from fixtures.
vi.mock("./wikiClient", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./wikiClient")>();
  return { ...actual, fetchWikiIndex: vi.fn() };
});
import {
  fetchWikiIndex,
  WikiUnavailableError,
} from "./wikiClient";
const mockFetchIndex = vi.mocked(fetchWikiIndex);

let mounted: { unmount: () => void } | null = null;
beforeEach(() => {
  _clearWikiCache();
  mockFetchIndex.mockReset();
});
afterEach(() => {
  mounted?.unmount();
  mounted = null;
});

describe("WikiIndexPage — distinct states (Slice 4, U1)", () => {
  it("populated: renders the community list verbatim", async () => {
    mockFetchIndex.mockResolvedValue(indexMd);
    const r = await renderAt(<WikiIndexPage />);
    mounted = r;
    await flush();
    const hrefs = Array.from(r.container.querySelectorAll("a")).map((a) =>
      a.getAttribute("href"),
    );
    expect(hrefs).toEqual(["/wiki/c/1", "/wiki/c/2", "/wiki/c/3"]);
  });

  it("empty (0 communities): renders empty-state, NOT unavailable, no pages", async () => {
    mockFetchIndex.mockResolvedValue(indexEmptyMd);
    const r = await renderAt(<WikiIndexPage />);
    mounted = r;
    await flush();
    expect(r.container.querySelector(".wiki-empty")).not.toBeNull();
    expect(r.container.querySelector(".wiki-unavailable")).toBeNull();
    // No community links produced (AC-4).
    expect(r.container.querySelector('a[href^="/wiki/c/"]')).toBeNull();
  });

  it("unavailable (404): renders DISTINCT unavailable state, not empty (U1)", async () => {
    mockFetchIndex.mockRejectedValue(new WikiUnavailableError());
    const r = await renderAt(<WikiIndexPage />);
    mounted = r;
    await flush();
    expect(r.container.querySelector(".wiki-unavailable")).not.toBeNull();
    expect(r.container.querySelector(".wiki-empty")).toBeNull();
  });

  it("single community: exactly one community link", async () => {
    mockFetchIndex.mockResolvedValue(indexSingleMd);
    const r = await renderAt(<WikiIndexPage />);
    mounted = r;
    await flush();
    expect(r.container.querySelectorAll('a[href^="/wiki/c/"]').length).toBe(1);
  });

  it("large index (hundreds): renders every link without error", async () => {
    mockFetchIndex.mockResolvedValue(indexLargeMd);
    const r = await renderAt(<WikiIndexPage />);
    mounted = r;
    await flush();
    expect(r.container.querySelectorAll('a[href^="/wiki/c/"]').length).toBe(300);
  });
});
