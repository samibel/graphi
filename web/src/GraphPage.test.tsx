// Seed-resolution flow (the "release" bug): the neighborhood endpoint is an
// exact node-id lookup, so free text must be resolved via /search — zero hits
// show the empty hint, an unambiguous hit auto-loads, several plausible hits
// render a candidate picker whose click loads that node.
import { afterEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { GraphPage } from "./GraphPage";
import { queryPayload } from "./__fixtures__/contract";
import type { QueryResult, SearchMatch } from "./types";

vi.mock("./GraphView", () => ({
  GraphView: () => <div data-testid="graphview" />,
}));

const fetchNeighborhood = vi.fn<(s: string, d?: number) => Promise<QueryResult>>();
const searchSymbols = vi.fn<(q: string, limit?: number) => Promise<SearchMatch[]>>();

vi.mock("./graphiClient", () => ({
  getContract: vi.fn(async () => ({ schema_version: 1, resources: [], streams: [] })),
  resolveAnalyzerRoute: () => null,
  fetchNeighborhood: (s: string, d?: number) => fetchNeighborhood(s, d),
  searchSymbols: (q: string, limit?: number) => searchSymbols(q, limit),
  fetchImpact: vi.fn(),
  subscribeSSE: vi.fn(() => () => {}),
  SchemaMismatchError: class extends Error {},
}));

const notFound: QueryResult = {
  operation: "neighborhood",
  symbol: "release",
  outcome: "not_found",
  nodes: [],
  edges: [],
};

function match(id: string, qn: string): SearchMatch {
  return {
    node_id: id,
    kind: "function",
    qualified_name: qn,
    source_path: "pkg/a.go",
    line: 1,
    column: 1,
    rank: -1,
  };
}

async function mountWithSeed(seed: string) {
  window.history.replaceState(null, "", seed ? `/?symbol=${seed}` : "/");
  const container = document.createElement("div");
  document.body.appendChild(container);
  let root!: Root;
  await act(async () => {
    root = createRoot(container);
    root.render(<GraphPage />);
  });
  // Drain the chained async resolution (direct lookup → search → load).
  for (let i = 0; i < 3; i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  }
  return {
    container,
    unmount: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

let mounted: { unmount: () => void } | null = null;
afterEach(() => {
  mounted?.unmount();
  mounted = null;
  vi.clearAllMocks();
  window.history.replaceState(null, "", "/");
});

describe("GraphPage — seed resolution via /search", () => {
  it("shows the empty hint only when search finds nothing either", async () => {
    fetchNeighborhood.mockResolvedValue(notFound);
    searchSymbols.mockResolvedValue([]);
    const r = await mountWithSeed("release");
    mounted = r;
    expect(searchSymbols.mock.calls[0]?.[0]).toBe("release");
    expect(r.container.textContent).toContain("No symbols found");
  });

  it("auto-loads an unambiguous match", async () => {
    fetchNeighborhood.mockImplementation(async (s) =>
      s === "s1" ? queryPayload : notFound,
    );
    searchSymbols.mockResolvedValue([
      match("s1", "main.release"),
      match("s2", "pkg.Other"),
    ]);
    const r = await mountWithSeed("release");
    mounted = r;
    // chooseMatch resolves the unique last-segment hit; its id is loaded.
    expect(fetchNeighborhood).toHaveBeenCalledWith("s1", 2);
    expect(r.container.textContent).toContain("3 nodes");
    expect(r.container.textContent).not.toContain("No symbols found");
  });

  it("renders a candidate picker for ambiguous matches and loads the pick", async () => {
    fetchNeighborhood.mockImplementation(async (s) =>
      s === "s1" ? queryPayload : notFound,
    );
    searchSymbols.mockResolvedValue([
      match("s1", "main.release"),
      match("s2", "pkg.release"),
    ]);
    const r = await mountWithSeed("release");
    mounted = r;
    const buttons = r.container.querySelectorAll(".candidates button");
    expect(buttons).toHaveLength(2);
    expect(r.container.textContent).not.toContain("No symbols found");

    await act(async () => {
      buttons[0].dispatchEvent(new MouseEvent("click", { bubbles: true }));
      await new Promise((res) => setTimeout(res, 0));
    });
    expect(fetchNeighborhood).toHaveBeenCalledWith("s1", 2);
    expect(r.container.querySelector(".candidates")).toBeNull();
    expect(r.container.textContent).toContain("3 nodes");
  });
});
