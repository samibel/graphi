// Two-node "why connected" compare panel (Web/IDE Polish). The pair is picked
// via shift-click (or the compare toggle) on the graph; this panel lists every
// edge from the CURRENT graph payload that connects the two selected nodes —
// in either direction — reusing the WhyConnectedPanel explanation rendering
// (kind, confidence tier, reason, evidence). When nothing connects them it
// says so explicitly instead of rendering nothing.
import type { ResultEdge, ResultNode } from "./types";
import { EdgeExplanationList } from "./WhyConnectedPanel";

interface Props {
  /** The compare picks, in click order (0–2 node ids). */
  ids: string[];
  /** Raw payload edges of the loaded graph. */
  edges: ResultEdge[];
  /** Raw payload nodes (id → qualified name lookups). */
  nodes: ResultNode[];
  onClear: () => void;
}

/** All edges connecting a and b, in either direction. Pure — exported for tests. */
export function connectingEdges(
  edges: ResultEdge[],
  a: string,
  b: string,
): ResultEdge[] {
  return edges.filter(
    (e) => (e.from === a && e.to === b) || (e.from === b && e.to === a),
  );
}

export function CompareConnectionPanel({ ids, edges, nodes, onClear }: Props) {
  if (ids.length === 0) return null;

  const name = (id: string) =>
    nodes.find((n) => n.id === id)?.qualified_name ?? id;

  const body =
    ids.length < 2 ? (
      <p className="hint-inline">
        <code>{name(ids[0])}</code> picked — shift-click a second node to
        compare.
      </p>
    ) : (
      (() => {
        const connecting = connectingEdges(edges, ids[0], ids[1]);
        return connecting.length === 0 ? (
          <p className="hint-inline no-direct-edge">
            No direct edge connects <code>{name(ids[0])}</code> and{" "}
            <code>{name(ids[1])}</code> in the loaded graph.
          </p>
        ) : (
          <>
            <p className="hint-inline">
              {connecting.length} edge{connecting.length === 1 ? "" : "s"}{" "}
              between <code>{name(ids[0])}</code> and <code>{name(ids[1])}</code>
              :
            </p>
            <EdgeExplanationList edges={connecting} />
          </>
        );
      })()
    );

  return (
    <div className="compare-panel" data-testid="compare-panel">
      <h3>
        Why connected — compare{" "}
        <button type="button" className="compare-clear" onClick={onClear}>
          clear
        </button>
      </h3>
      {body}
    </div>
  );
}
