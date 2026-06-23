// GraphView renders the Graphology model in a Sigma.js (WebGL) canvas and wires
// node clicks to the blast/citation highlight reducers via Sigma reducers.
import { useEffect, useRef } from "react";
import Graph from "graphology";
import Sigma from "sigma";
import {
  COLOR_BLAST,
  COLOR_CITATION,
  COLOR_DEFAULT,
  SIZE_BLAST,
  SIZE_DEFAULT,
} from "./highlights";
import type { GraphState } from "./useGraph";

interface Props {
  state: GraphState;
  onSelect: (id: string) => void;
}

export function GraphView({ state, onSelect }: Props) {
  const containerRef = useRef<HTMLDivElement>(null);
  const sigmaRef = useRef<Sigma | null>(null);
  const graphRef = useRef<Graph | null>(null);

  // (Re)build the Graphology graph whenever nodes/edges change.
  useEffect(() => {
    if (!containerRef.current) return;
    const g = new Graph();
    for (const n of state.nodes) g.addNode(n.id, { ...n });
    for (const e of state.edges) {
      if (!g.hasEdge(e.from, e.to)) g.addEdge(e.from, e.to, { ...e });
    }
    graphRef.current = g;

    // Recreate Sigma only once per container; refresh settings otherwise.
    if (!sigmaRef.current) {
      sigmaRef.current = new Sigma(g, containerRef.current, {
        defaultNodeColor: COLOR_DEFAULT,
        defaultEdgeColor: COLOR_DEFAULT,
        labelDensity: 0.3,
        renderEdgeLabels: true,
      });
      sigmaRef.current.on("clickNode", ({ node }) => onSelect(String(node)));
    } else {
      sigmaRef.current.setGraph(g);
    }

    // reducers apply the distinct visual styles per highlight attribute
    sigmaRef.current.setSetting("nodeReducer", (_node, data) => {
      const out = { ...data };
      if (data.blast) {
        out.color = COLOR_BLAST;
        out.size = SIZE_BLAST;
      } else if (data.citation) {
        out.color = COLOR_CITATION;
        out.size = SIZE_DEFAULT + 2;
      } else {
        out.color = COLOR_DEFAULT;
        out.size = SIZE_DEFAULT;
      }
      return out;
    });
    sigmaRef.current.setSetting("edgeReducer", (_edge, data) => {
      const out = { ...data };
      if (data.blast) out.color = COLOR_BLAST;
      else if (data.citation) {
        out.color = COLOR_CITATION;
        out.type = "arrow";
      } else {
        out.color = COLOR_DEFAULT;
      }
      return out;
    });
    sigmaRef.current.refresh();
  }, [state, onSelect]);

  useEffect(() => {
    return () => {
      sigmaRef.current?.kill();
      sigmaRef.current = null;
    };
  }, []);

  return <div ref={containerRef} className="graph-canvas" />;
}
