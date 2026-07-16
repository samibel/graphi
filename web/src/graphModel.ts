// Pure Graphology model construction for the Sigma view. Keeping this outside
// React/WebGL makes structural rendering invariants directly testable.
import Graph from "graphology";
import type { HighlightableEdge, HighlightableNode } from "./highlights";
import { radialLayout } from "./layout";

export function buildRenderGraph(
  nodes: HighlightableNode[],
  edges: HighlightableEdge[],
  seed: string | null,
): Graph {
  const positions = radialLayout(
    nodes.map((node) => node.id),
    edges,
    seed,
  );
  // Code graphs legitimately contain several relationship kinds between the
  // same endpoint pair. A simple Graph silently forced the view to discard all
  // but the first edge; multi mode preserves the engine result exactly.
  const graph = new Graph({ multi: true });
  for (const node of nodes) {
    const position = positions.get(node.id) ?? { x: 0, y: 0 };
    graph.addNode(node.id, { ...node, x: position.x, y: position.y });
  }
  for (const edge of edges) {
    if (
      graph.hasNode(edge.from) &&
      graph.hasNode(edge.to) &&
      !graph.hasEdge(edge.id)
    ) {
      graph.addEdgeWithKey(edge.id, edge.from, edge.to, { ...edge });
    }
  }
  return graph;
}
