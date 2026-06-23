// Highlight reducers are PURE functions over a node/edge attribute map. They
// are deliberately framework-agnostic (no Sigma/Graphology import) so they are
// unit-testable without a DOM/WebGL. The webview wires them into Sigma node/edge
// reducers. Ported verbatim from web/src/highlights.ts (SW-045) — the single
// shared viz logic across the web client and the VS Code extension.
//
// Three visually DISTINCT, color-independent-redundant treatments:
//   - blast     → nodes/edges in the impact set      (red, enlarged / solid weight)
//   - citation  → edges carrying evidence/provenance  (amber, dashed/secondary)
//   - dimmed    → everything out of scope             (faded default)
// Citation is derived from edges that carry EVIDENCE — NOT a second fetch and
// NOT merely edges incident to the selection.

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
  /** True when the source edge carried evidence (provenance) — citation source. */
  hasEvidence: boolean;
  blast: boolean;
  citation: boolean;
}

/** Mark the blast-radius node set. Idempotent over repeated calls; pure. */
export function applyBlast(
  nodes: HighlightableNode[],
  impactedIds: Set<string>,
): HighlightableNode[] {
  return nodes.map((n) => ({ ...n, blast: impactedIds.has(n.id) }));
}

/**
 * Mark citation edges: edges carrying evidence (`hasEvidence`) whose endpoints
 * are within the in-scope (blast) node set get the citation treatment. Pure.
 */
export function applyCitation(
  edges: HighlightableEdge[],
  nodes: HighlightableNode[],
  selected: string,
): { edges: HighlightableEdge[]; nodes: HighlightableNode[] } {
  const inScope = new Set(nodes.filter((n) => n.blast).map((n) => n.id));
  inScope.add(selected);
  const citedEdges = new Set(
    edges
      .filter((e) => e.hasEvidence && (inScope.has(e.from) || inScope.has(e.to)))
      .map((e) => e.id),
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

/** Clear ALL highlights (reset to neutral full-graph view). Pure. */
export function clearHighlights(
  nodes: HighlightableNode[],
  edges: HighlightableEdge[],
): { nodes: HighlightableNode[]; edges: HighlightableEdge[] } {
  return {
    nodes: nodes.map((n) => ({ ...n, blast: false, citation: false })),
    edges: edges.map((e) => ({ ...e, blast: false, citation: false })),
  };
}

// --- Visual styles (applied by Sigma reducers in the webview) ---------------
export const COLOR_DEFAULT = "#6b7280"; // gray
export const COLOR_BLAST = "#dc2626"; // red
export const COLOR_CITATION = "#d97706"; // amber
export const COLOR_DIMMED = "#374151"; // faded gray (out of scope)
export const SIZE_DEFAULT = 8;
export const SIZE_BLAST = 14;
