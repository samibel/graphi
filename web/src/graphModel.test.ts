import { describe, expect, it } from "vitest";
import { buildRenderGraph } from "./graphModel";

const node = (id: string) => ({ id, blast: false, citation: false });
const edge = (id: string, kind: string) => ({
  id,
  from: "a",
  to: "b",
  kind,
  hasEvidence: false,
  blast: false,
  citation: false,
});

describe("buildRenderGraph", () => {
  it("preserves parallel relationships between the same endpoints", () => {
    const graph = buildRenderGraph(
      [node("a"), node("b")],
      [edge("calls:a:b", "calls"), edge("references:a:b", "references")],
      "a",
    );

    expect(graph.multi).toBe(true);
    expect(graph.size).toBe(2);
    expect(graph.hasEdge("calls:a:b")).toBe(true);
    expect(graph.hasEdge("references:a:b")).toBe(true);
  });

  it("does not create dangling edges or duplicate edge keys", () => {
    const duplicate = edge("calls:a:b", "calls");
    const graph = buildRenderGraph(
      [node("a"), node("b")],
      [duplicate, duplicate, { ...edge("missing", "calls"), to: "missing" }],
      "a",
    );

    expect(graph.size).toBe(1);
  });
});
