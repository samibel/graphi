import { describe, expect, it } from "vitest";
import { boundedGraphMessage } from "./bounding";
import type { QueryResult, ResultEdge, ResultNode } from "../contract";

function node(id: string): ResultNode {
  return { id, kind: "func", qualified_name: id, source_path: `${id}.go`, line: 1, column: 0 };
}
function edge(id: string, from: string, to: string, evidence: string[] = []): ResultEdge {
  return {
    id,
    from,
    to,
    kind: "calls",
    confidence_tier: "confirmed",
    confidence: 1,
    reason: "",
    evidence,
  };
}

describe("boundedGraphMessage — host-side perf bounding", () => {
  it("keeps the seed and bounds nodes/edges to the caps", () => {
    const nodes = Array.from({ length: 10 }, (_, i) => node(`n${i}`));
    const edges = Array.from({ length: 10 }, (_, i) =>
      edge(`e${i}`, `n${i}`, `n${(i + 1) % 10}`),
    );
    const result: QueryResult = { operation: "neighborhood", symbol: "n5", outcome: "found", nodes, edges };
    const msg = boundedGraphMessage("n5", result, 3, 5);
    expect(msg.nodes).toHaveLength(3);
    expect(msg.nodes[0].id).toBe("n5"); // seed retained first
    expect(msg.edges.length).toBeLessThanOrEqual(5);
    expect(msg.truncated).toBe(true);
  });

  it("drops edges whose endpoints fall outside the kept node set", () => {
    const result: QueryResult = {
      operation: "neighborhood",
      symbol: "a",
      outcome: "found",
      nodes: [node("a"), node("b")],
      edges: [edge("e1", "a", "b"), edge("e2", "a", "ghost")],
    };
    const msg = boundedGraphMessage("a", result, 100, 100);
    expect(msg.edges.map((e) => e.id)).toEqual(["e1"]);
  });

  it("marks hasEvidence from edge evidence (citation source)", () => {
    const result: QueryResult = {
      operation: "neighborhood",
      symbol: "a",
      outcome: "found",
      nodes: [node("a"), node("b")],
      edges: [edge("e1", "a", "b", ["proof.go:1"])],
    };
    const msg = boundedGraphMessage("a", result, 100, 100);
    expect(msg.edges[0].hasEvidence).toBe(true);
  });

  it("reports not truncated when under caps", () => {
    const result: QueryResult = {
      operation: "neighborhood",
      symbol: "a",
      outcome: "found",
      nodes: [node("a")],
      edges: [],
    };
    expect(boundedGraphMessage("a", result, 100, 100).truncated).toBe(false);
  });
});
