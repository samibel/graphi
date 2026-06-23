// Pure payload-bounding logic for the graph webview, framework-agnostic (no
// `vscode` import) so it is unit-testable in isolation. The host truncates the
// neighborhood to the configured node/edge caps BEFORE postMessage (perf bound:
// node cap 1500 / edge cap 4000 by default) and drops edges whose endpoints fall
// outside the kept node set. The seed is always retained (kept first).
import type { QueryResult } from "../contract";
import type { GraphMessage, WireEdge, WireNode } from "./protocol";

export function boundedGraphMessage(
  seed: string,
  result: QueryResult,
  maxNodes: number,
  maxEdges: number,
): GraphMessage {
  const ordered = [...result.nodes].sort((a, b) =>
    a.id === seed ? -1 : b.id === seed ? 1 : 0,
  );
  const keptNodes = ordered.slice(0, maxNodes);
  const keptIds = new Set(keptNodes.map((n) => n.id));
  const candidateEdges = result.edges.filter(
    (e) => keptIds.has(e.from) && keptIds.has(e.to),
  );
  const keptEdges = candidateEdges.slice(0, maxEdges);
  const truncated =
    result.nodes.length > keptNodes.length ||
    candidateEdges.length > keptEdges.length;

  const nodes: WireNode[] = keptNodes.map((n) => ({
    id: n.id,
    label: n.qualified_name || n.id,
    kind: n.kind,
    source_path: n.source_path,
    line: n.line,
    blast: false,
    citation: false,
  }));
  const edges: WireEdge[] = keptEdges.map((e) => ({
    id: e.id,
    from: e.from,
    to: e.to,
    kind: e.kind,
    confidence_tier: e.confidence_tier,
    hasEvidence: Array.isArray(e.evidence) && e.evidence.length > 0,
    blast: false,
    citation: false,
  }));
  return { kind: "graph", seed, nodes, edges, truncated };
}
