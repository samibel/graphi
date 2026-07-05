// Highlight reducers are PURE functions over a node/edge attribute map. They
// are deliberately framework-agnostic (no Sigma/Graphology import) so they are
// unit-testable without a DOM/WebGL. GraphView wires them into Sigma node/edge
// reducers.
//
// Three visually DISTINCT, color-independent-redundant treatments (AC-3, U1/U5):
//   - blast     → nodes/edges in the impact set      (red, enlarged / solid weight)
//   - citation  → edges carrying evidence/provenance  (amber, thicker weight)
//   - dimmed    → everything out of scope             (faded default)
// Citation is derived from edges that carry EVIDENCE (D4) — NOT a second fetch
// and NOT merely edges incident to the selection.
// Clearing the selection resets every attribute to its default (AC clear).

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
 * are within the in-scope (blast) node set get the citation treatment. These are
 * the evidence/provenance edges backing the highlighted relationships, distinct
 * from the blast edges (AC-3). Nodes incident to a citation edge are flagged so
 * they can be styled. Pure; does not mutate inputs.
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
      .filter(
        (e) =>
          e.hasEvidence && (inScope.has(e.from) || inScope.has(e.to)),
      )
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

// --- Visual styles (applied by Sigma reducers in GraphView) -----------------
// Redundant encodings so meaning never relies on color alone (U5):
//   blast    = red   + enlarged  + solid (z above)
//   citation = amber + thicker edge weight
//   dimmed   = gray  + faded (low opacity) when a selection is active

export const COLOR_DEFAULT = "#6b7280"; // gray
export const COLOR_BLAST = "#dc2626"; // red
export const COLOR_CITATION = "#d97706"; // amber
export const COLOR_DIMMED = "#374151"; // faded gray (out of scope)
export const SIZE_DEFAULT = 8;
export const SIZE_BLAST = 14;
export const SIZE_SEED = 12;

// Neutral-state node colors keyed by node kind, so the unselected graph is
// readable at a glance (which dots are files vs. functions vs. types) instead
// of a uniform gray. Unknown kinds fall back to COLOR_DEFAULT.
export const KIND_COLORS: Record<string, string> = {
  function: "#3b82f6", // blue
  func: "#3b82f6",
  method: "#8b5cf6", // violet
  type: "#10b981", // green
  struct: "#10b981",
  interface: "#10b981",
  class: "#10b981",
  file: "#06b6d4", // cyan
  package: "#ec4899", // pink
  module: "#ec4899",
  var: "#eab308", // yellow
  const: "#eab308",
  field: "#eab308",
};

export function colorForKind(kind: unknown): string {
  if (typeof kind !== "string") return COLOR_DEFAULT;
  return KIND_COLORS[kind.toLowerCase()] ?? COLOR_DEFAULT;
}
