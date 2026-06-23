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
function mkEdge(
  id: string,
  from: string,
  to: string,
  hasEvidence = false,
): HighlightableEdge {
  return { id, from, to, hasEvidence, blast: false, citation: false };
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
  it("flags only evidence-bearing edges in scope as citation", () => {
    // e1 carries evidence into S (in scope via selection); e2 has no evidence;
    // e3 carries evidence but both endpoints are out of scope.
    const edges = [
      mkEdge("e1", "X", "S", true),
      mkEdge("e2", "Y", "S", false),
      mkEdge("e3", "P", "Q", true),
    ];
    const nodes = mkNodes(["S", "X", "Y", "P", "Q"]);
    const { edges: oe } = applyCitation(edges, nodes, "S");
    expect(oe.find((e) => e.id === "e1")!.citation).toBe(true); // evidence + in scope
    expect(oe.find((e) => e.id === "e2")!.citation).toBe(false); // no evidence
    expect(oe.find((e) => e.id === "e3")!.citation).toBe(false); // out of scope
  });

  it("flags nodes incident to citation edges", () => {
    const edges = [mkEdge("e1", "X", "S", true)];
    const nodes = mkNodes(["S", "X", "Y"]);
    const { nodes: on } = applyCitation(edges, nodes, "S");
    const flag = (id: string) => on.find((n) => n.id === id)!.citation;
    expect(flag("S")).toBe(true);
    expect(flag("X")).toBe(true);
    expect(flag("Y")).toBe(false);
  });

  it("citation is distinct from blast (AC-3)", () => {
    const edges = [mkEdge("e1", "X", "S", true)];
    const nodes = mkNodes(["S", "X"]);
    const blasted = applyBlast(nodes, new Set(["X"])); // X is impacted (blast)
    const cited = applyCitation(edges, blasted, "S");
    const x = cited.nodes.find((n) => n.id === "X")!;
    expect(x.blast).toBe(true); // independent attributes
    expect(x.citation).toBe(true);
    const e1 = cited.edges[0];
    expect(e1.citation).toBe(true); // edge is a citation edge
    expect(e1.blast).toBe(false); // but not a blast edge — distinct treatment
  });

  it("is pure — does not mutate input edges", () => {
    const edges = [mkEdge("e1", "X", "S", true)];
    const nodes = mkNodes(["S", "X"]);
    applyCitation(edges, applyBlast(nodes, new Set(["X"])), "S");
    expect(edges[0].citation).toBe(false);
  });
});

describe("clearHighlights", () => {
  it("resets every highlight attribute", () => {
    const nodes = applyBlast(mkNodes(["A", "B"]), new Set(["A", "B"]));
    const edges = [mkEdge("e1", "A", "B", true)];
    const cited = applyCitation(edges, nodes, "B");
    const cleared = clearHighlights(cited.nodes, cited.edges);
    expect(cleared.nodes.every((n) => !n.blast && !n.citation)).toBe(true);
    expect(cleared.edges.every((e) => !e.blast && !e.citation)).toBe(true);
  });
});
