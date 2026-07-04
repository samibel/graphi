import { describe, expect, it, vi } from "vitest";
import { renderAt } from "./testRender";
import {
  CompareConnectionPanel,
  connectingEdges,
} from "./CompareConnectionPanel";
import { queryPayload } from "./__fixtures__/contract";

const { nodes, edges } = queryPayload;

describe("connectingEdges", () => {
  it("matches edges in either direction", () => {
    // e1 is n2 → n1; asking for (n1, n2) must still find it.
    expect(connectingEdges(edges, "n1", "n2").map((e) => e.id)).toEqual(["e1"]);
    expect(connectingEdges(edges, "n2", "n1").map((e) => e.id)).toEqual(["e1"]);
  });

  it("returns nothing for unconnected nodes", () => {
    expect(connectingEdges(edges, "n2", "n3")).toEqual([]);
  });
});

describe("CompareConnectionPanel", () => {
  it("renders nothing before any pick", async () => {
    const { container } = await renderAt(
      <CompareConnectionPanel ids={[]} edges={edges} nodes={nodes} onClear={() => {}} />,
    );
    expect(container.querySelector('[data-testid="compare-panel"]')).toBeNull();
  });

  it("prompts for the second node after one pick", async () => {
    const { container } = await renderAt(
      <CompareConnectionPanel ids={["n2"]} edges={edges} nodes={nodes} onClear={() => {}} />,
    );
    expect(container.textContent).toContain("pkg.Caller");
    expect(container.textContent).toContain("shift-click a second node");
  });

  it("lists connecting edges with kind, tier, confidence, reason and evidence", async () => {
    const { container } = await renderAt(
      <CompareConnectionPanel
        ids={["n2", "n1"]}
        edges={edges}
        nodes={nodes}
        onClear={() => {}}
      />,
    );
    const text = container.textContent!;
    expect(text).toContain("1 edge between");
    expect(text).toContain("pkg.Caller");
    expect(text).toContain("pkg.Func");
    // Reuses the WhyConnectedPanel rendering: kind (tier) confidence, reason, evidence.
    expect(text).toContain("calls");
    expect(text).toContain("(confirmed)");
    expect(text).toContain("1.00");
    expect(text).toContain("direct call");
    expect(text).toContain("pkg/b.go:20");
    // Tier is carried on the data-tier attribute (heuristic-marking convention).
    expect(
      container.querySelector('.edge-explanations li[data-tier="confirmed"]'),
    ).not.toBeNull();
  });

  it("shows an explicit 'no direct edge' state when nothing connects the pair", async () => {
    const { container } = await renderAt(
      <CompareConnectionPanel
        ids={["n2", "n3"]}
        edges={edges}
        nodes={nodes}
        onClear={() => {}}
      />,
    );
    expect(container.textContent).toContain("No direct edge connects");
    expect(container.textContent).toContain("pkg.Caller");
    expect(container.textContent).toContain("pkg.Other");
    expect(container.querySelector(".edge-explanations")).toBeNull();
  });

  it("clear button invokes onClear", async () => {
    const onClear = vi.fn();
    const { container } = await renderAt(
      <CompareConnectionPanel
        ids={["n2", "n1"]}
        edges={edges}
        nodes={nodes}
        onClear={onClear}
      />,
    );
    container
      .querySelector(".compare-clear")!
      .dispatchEvent(new MouseEvent("click", { bubbles: true }));
    expect(onClear).toHaveBeenCalledTimes(1);
  });
});
