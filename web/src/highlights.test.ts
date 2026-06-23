import { describe, expect, it } from "vitest";
import {
  applyBlast,
  applyCitation,
  clearHighlights,
  type HighlightableEdge,
  type HighlightableNode,
} from "./highlights";

function mkNodes(ids: string[]): HighlightableNode[] {
  return ids.map((id) => ({ id, blast: false, citation: false }));
}
function mkEdge(id: string, from: string, to: string): HighlightableEdge {
  return { id, from, to, blast: false, citation: false };
}

describe("applyBlast", () => {
  it("marks only impacted nodes", () => {
    const nodes = mkNodes(["A", "B", "C"]);
    const out = applyBlast(nodes, new Set(["A", "C"]));
    expect(out[0].blast).toBe(true);
    expect(out[1].blast).toBe(false);
    expect(out[2].blast).toBe(true);
  });

  it("is pure — does not mutate input", () => {
    const nodes = mkNodes(["A"]);
    applyBlast(nodes, new Set(["A"]));
    expect(nodes[0].blast).toBe(false);
  });
});

describe("applyCitation", () => {
  it("flags citation edges into the selected node and their incident nodes", () => {
    const edges = [mkEdge("e1", "X", "S"), mkEdge("e2", "S", "Y"), mkEdge("e3", "Z", "S")];
    const nodes = mkNodes(["S", "X", "Y", "Z"]);
    const { edges: oe, nodes: on } = applyCitation(edges, nodes, "S");
    // e1 (X->S) and e3 (Z->S) point INTO S → citation; e2 (S->Y) does not
    expect(oe[0].citation).toBe(true);
    expect(oe[1].citation).toBe(false);
    expect(oe[2].citation).toBe(true);
    // incident nodes X, Z, S flagged; Y is not
    const flag = (id: string) => on.find((n) => n.id === id)!.citation;
    expect(flag("S")).toBe(true);
    expect(flag("X")).toBe(true);
    expect(flag("Z")).toBe(true);
    expect(flag("Y")).toBe(false);
  });

  it("citation is distinct from blast attributes", () => {
    const edges = [mkEdge("e1", "X", "S")];
    const nodes = mkNodes(["S", "X"]);
    const blasted = applyBlast(nodes, new Set(["X"]));
    const cited = applyCitation(edges, blasted, "S");
    // X is BOTH blast (impacted) and citation (cites S) — two independent attrs
    expect(cited.nodes.find((n) => n.id === "X")!.blast).toBe(true);
    expect(cited.nodes.find((n) => n.id === "X")!.citation).toBe(true);
    expect(cited.edges[0].citation).toBe(true);
    expect(cited.edges[0].blast).toBe(false);
  });
});

describe("clearHighlights", () => {
  it("resets every highlight attribute", () => {
    let nodes = applyBlast(mkNodes(["A", "B"]), new Set(["A", "B"]));
    let edges = [mkEdge("e1", "A", "B")];
    const cited = applyCitation(edges, nodes, "B");
    nodes = cited.nodes;
    edges = cited.edges;
    const cleared = clearHighlights(nodes, edges);
    expect(cleared.nodes.every((n) => !n.blast && !n.citation)).toBe(true);
    expect(cleared.edges.every((e) => !e.blast && !e.citation)).toBe(true);
  });
});
