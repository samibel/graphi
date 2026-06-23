import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { WikiCommunityPage } from "./WikiCommunityPage";
import { _clearWikiCache } from "./wikiCache";
import { renderRoute, flush } from "./testRender";
import { community1Md, communitySingletonMd } from "./__fixtures__/wiki";

vi.mock("./wikiClient", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./wikiClient")>();
  return { ...actual, fetchWikiPage: vi.fn() };
});
import { fetchWikiPage, WikiPageNotFoundError } from "./wikiClient";
const mockFetchPage = vi.mocked(fetchWikiPage);

let mounted: { unmount: () => void } | null = null;
beforeEach(() => {
  _clearWikiCache();
  mockFetchPage.mockReset();
});
afterEach(() => {
  mounted?.unmount();
  mounted = null;
});

describe("WikiCommunityPage — distinct states (Slice 4)", () => {
  it("reads :id verbatim and fetches that page", async () => {
    mockFetchPage.mockResolvedValue(community1Md);
    const r = await renderRoute(
      "/wiki/c/:id",
      <WikiCommunityPage />,
      "/wiki/c/1",
    );
    mounted = r;
    await flush();
    expect(mockFetchPage).toHaveBeenCalledWith("1");
    expect(r.container.querySelector("h1")?.textContent).toBe("Community 1");
  });

  it("renders members + neighbor cross-links + back-link (AC-3)", async () => {
    mockFetchPage.mockResolvedValue(community1Md);
    const r = await renderRoute(
      "/wiki/c/:id",
      <WikiCommunityPage />,
      "/wiki/c/1",
    );
    mounted = r;
    await flush();
    const hrefs = Array.from(r.container.querySelectorAll("a")).map((a) =>
      a.getAttribute("href"),
    );
    expect(hrefs).toEqual(["/wiki/c/2", "/wiki/c/3", "/wiki"]);
  });

  it("404 → DISTINCT 'community not found' state (not unavailable/empty)", async () => {
    mockFetchPage.mockRejectedValue(new WikiPageNotFoundError("999"));
    const r = await renderRoute(
      "/wiki/c/:id",
      <WikiCommunityPage />,
      "/wiki/c/999",
    );
    mounted = r;
    await flush();
    expect(r.container.querySelector(".wiki-notfound")).not.toBeNull();
    // Still offers a back-link to the index.
    expect(
      r.container.querySelector('a[href="/wiki"]'),
    ).not.toBeNull();
  });

  it("singleton (size-1) community renders as an ordinary page (AC-5)", async () => {
    mockFetchPage.mockResolvedValue(communitySingletonMd);
    const r = await renderRoute(
      "/wiki/c/:id",
      <WikiCommunityPage />,
      "/wiki/c/3",
    );
    mounted = r;
    await flush();
    expect(r.container.querySelector("h1")?.textContent).toBe("Community 3");
    const codes = Array.from(r.container.querySelectorAll("code")).map(
      (c) => c.textContent,
    );
    expect(codes).toContain("pkg/c.Solo");
  });
});
