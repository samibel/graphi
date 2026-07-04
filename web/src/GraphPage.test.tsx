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

// The GraphView stub exposes shift-click probes so the two-node compare
// wiring (onSelect(id, shiftKey)) can be driven without Sigma/WebGL.
vi.mock("./GraphView", () => ({
  GraphView: ({ onSelect }: { onSelect: (id: string, shift?: boolean) => void }) => (
    <div data-testid="graphview">
      {["n1", "n2", "n3"].map((id) => (
        <button
          key={id}
          type="button"
          data-testid={`shift-click-${id}`}
          onClick={() => onSelect(id, true)}
        />
      ))}
    </div>
  ),
}));

const fetchNeighborhood = vi.fn<(s: string, d?: number) => Promise<QueryResult>>();
const searchSymbols = vi.fn<(q: string, limit?: number) => Promise<SearchMatch[]>>();

vi.mock("./graphiClient", () => ({
  getContract: vi.fn(async () => ({ schema_version: 1, resources: [], streams: [] })),
  resolveAnalyzerRoute: () => null,
  hasResource: (c: { resources: string[] }, r: string) => c.resources.includes(r),
  fetchNeighborhood: (s: string, d?: number) => fetchNeighborhood(s, d),
  searchSymbols: (q: string, limit?: number) => searchSymbols(q, limit),
  fetchImpact: vi.fn(),
  relatedFiles: vi.fn(),
  changeRisk: vi.fn(),
  agentBrief: vi.fn(),
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

describe("GraphPage — two-node compare (shift-click)", () => {
  async function shiftClick(container: HTMLElement, id: string) {
    await act(async () => {
      container
        .querySelector(`[data-testid="shift-click-${id}"]`)!
        .dispatchEvent(new MouseEvent("click", { bubbles: true }));
      await new Promise((r) => setTimeout(r, 0));
    });
  }

  it("shift-clicking a connected pair lists their edges via the compare panel", async () => {
    fetchNeighborhood.mockResolvedValue(queryPayload);
    const r = await mountWithSeed("pkg.Func");
    mounted = r;
    await shiftClick(r.container, "n2");
    // One pick prompts for the second node.
    expect(r.container.textContent).toContain("shift-click a second node");
    await shiftClick(r.container, "n1");
    const panel = r.container.querySelector('[data-testid="compare-panel"]')!;
    expect(panel.textContent).toContain("pkg.Caller");
    expect(panel.textContent).toContain("pkg.Func");
    // Reuses the why-connected rendering: kind, tier, confidence, reason, evidence.
    expect(panel.textContent).toContain("calls");
    expect(panel.textContent).toContain("(confirmed)");
    expect(panel.textContent).toContain("direct call");
    expect(panel.textContent).toContain("pkg/b.go:20");
    // Shift-clicks feed COMPARE, not the blast-radius selection.
    expect(r.container.textContent).toContain("no selection");
    expect(r.container.textContent).toContain("comparing: n2 ↔ n1");
  });

  it("shows an explicit no-direct-edge state for an unconnected pair, and clears", async () => {
    fetchNeighborhood.mockResolvedValue(queryPayload);
    const r = await mountWithSeed("pkg.Func");
    mounted = r;
    await shiftClick(r.container, "n2");
    await shiftClick(r.container, "n3");
    const panel = r.container.querySelector('[data-testid="compare-panel"]')!;
    expect(panel.textContent).toContain("No direct edge connects");
    expect(panel.textContent).toContain("pkg.Caller");
    expect(panel.textContent).toContain("pkg.Other");

    await act(async () => {
      panel
        .querySelector(".compare-clear")!
        .dispatchEvent(new MouseEvent("click", { bubbles: true }));
      await new Promise((res) => setTimeout(res, 0));
    });
    expect(r.container.querySelector('[data-testid="compare-panel"]')).toBeNull();
  });
});
