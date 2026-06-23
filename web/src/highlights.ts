// Highlight reducers are PURE functions over a node/edge attribute map. They
// are deliberately framework-agnostic (no Sigma/Graphology import) so they are
// unit-testable without a DOM/WebGL. The GraphView wires them into Graphology
// node/edge attribute reducers.
//
// Two highlight classes, visually distinct (AC-3):
//   - blast     → nodes/edges in the impact set (red, enlarged)
//   - citation  → edges carrying provenance into the selected node (dashed amber)
// Clearing the selection resets every attribute to its default (AC-5).

export type Attrs = Record<string, unknown>;

export interface HighlightableNode extends Attrs {
  id: string;
  blast: boolean;
  citation: boolean;
}

export interface HighlightableEdge extends Attrs {
  id: string;
  from: string;
  to: string;
  blast: boolean;
  citation: boolean;
}

/** Mark the blast-radius node set. Idempotent over repeated calls. */
export function applyBlast(
  nodes: HighlightableNode[],
  impactedIds: Set<string>,
): HighlightableNode[] {
  return nodes.map((n) => ({ ...n, blast: impactedIds.has(n.id) }));
}

/**
 * Mark citation edges: edges whose `to` is the selected symbol carry provenance
 * "into" it — these are the citation edges, distinct from blast. Returns edges
 * with `citation` set, and nodes incident to a citation edge flagged so they
 * can be styled too.
 */
export function applyCitation(
  edges: HighlightableEdge[],
  nodes: HighlightableNode[],
  selected: string,
): { edges: HighlightableEdge[]; nodes: HighlightableNode[] } {
  const citedEdges = new Set(
    edges.filter((e) => e.to === selected).map((e) => e.id),
  );
  const incidentNodes = new Set<string>();
  for (const e of edges) {
    if (citedEdges.has(e.id)) {
      incidentNodes.add(e.from);
      incidentNodes.add(e.to);
    }
  }
  return {
    edges: edges.map((e) => ({ ...e, citation: citedEdges.has(e.id) })),
    nodes: nodes.map((n) => ({ ...n, citation: incidentNodes.has(n.id) })),
  };
}

/** Clear ALL highlights (AC-5). */
export function clearHighlights(
  nodes: HighlightableNode[],
  edges: HighlightableEdge[],
): { nodes: HighlightableNode[]; edges: HighlightableEdge[] } {
  return {
    nodes: nodes.map((n) => ({ ...n, blast: false, citation: false })),
    edges: edges.map((e) => ({ ...e, blast: false, citation: false })),
  };
}

// --- Visual styles (applied by Sigma reducers in GraphView) -----------------

export const COLOR_DEFAULT = "#6b7280"; // gray
export const COLOR_BLAST = "#dc2626"; // red
export const COLOR_CITATION = "#d97706"; // amber
export const SIZE_DEFAULT = 8;
export const SIZE_BLAST = 14;
