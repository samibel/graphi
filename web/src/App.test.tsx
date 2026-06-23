import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { App } from "./App";
import { _clearWikiCache } from "./wikiCache";
import { indexMd, community1Md } from "./__fixtures__/wiki";

// Stub the graph page's heavy deps (sigma canvas + live SSE) so the routing
// shell can mount in jsdom. GraphView is replaced with a marker; the graph
// data hooks are inert.
vi.mock("./GraphView", () => ({
  GraphView: () => <div data-testid="graphview" />,
}));
vi.mock("./graphiClient", () => ({
  getContract: vi.fn(async () => ({ schema_version: 1, resources: [], streams: [] })),
  resolveAnalyzerRoute: () => null,
  fetchNeighborhood: vi.fn(),
  fetchImpact: vi.fn(),
  subscribeSSE: vi.fn(() => () => {}),
  SchemaMismatchError: class extends Error {},
}));
vi.mock("./wikiClient", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./wikiClient")>();
  return {
    ...actual,
    fetchWikiIndex: vi.fn(async () => indexMd),
    fetchWikiPage: vi.fn(async () => community1Md),
  };
});

async function mountAt(path: string) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  let root!: Root;
  await act(async () => {
    root = createRoot(container);
    root.render(
      <MemoryRouter initialEntries={[path]}>
        <App />
      </MemoryRouter>,
    );
  });
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });
  return {
    container,
    unmount: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

let mounted: { unmount: () => void } | null = null;
beforeEach(() => _clearWikiCache());
afterEach(() => {
  mounted?.unmount();
  mounted = null;
  vi.clearAllMocks();
});

describe("App — routing + persistent nav (Slice 3, AC-6/U3)", () => {
  it("persistent nav renders on every route", async () => {
    for (const path of ["/", "/wiki", "/wiki/c/1"]) {
      const r = await mountAt(path);
      const navlinks = Array.from(
        r.container.querySelectorAll(".appnav .navlink"),
      ).map((a) => a.getAttribute("href"));
      expect(navlinks).toEqual(["/", "/wiki"]);
      r.unmount();
    }
  });

  it("'/' mounts the graph view", async () => {
    const r = await mountAt("/");
    mounted = r;
    expect(r.container.querySelector('[data-testid="graphview"]')).not.toBeNull();
  });

  it("'/wiki' mounts the wiki index (community links present)", async () => {
    const r = await mountAt("/wiki");
    mounted = r;
    expect(
      r.container.querySelectorAll('a[href^="/wiki/c/"]').length,
    ).toBeGreaterThan(0);
  });

  it("deep-link '/wiki/c/:id' mounts the community page directly (AC-6)", async () => {
    const r = await mountAt("/wiki/c/1");
    mounted = r;
    expect(r.container.querySelector(".wiki-body h1")?.textContent).toBe(
      "Community 1",
    );
  });
});
