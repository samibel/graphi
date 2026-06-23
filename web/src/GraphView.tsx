// GraphView renders the Graphology model in a Sigma.js (WebGL) canvas and wires
// node clicks to the blast/citation highlight reducers via Sigma node/edge
// reducers. Pan/zoom is Sigma-native (AC-1). The reducers derive all visuals
// from the pure HighlightState attributes computed in highlights.ts.
import { useEffect, useRef } from "react";
import Graph from "graphology";
import Sigma from "sigma";
import {
  COLOR_BLAST,
  COLOR_CITATION,
  COLOR_DEFAULT,
  COLOR_DIMMED,
  SIZE_BLAST,
  SIZE_DEFAULT,
} from "./highlights";
import type { GraphState } from "./useGraph";

interface Props {
  state: GraphState;
  onSelect: (id: string) => void;
  onClear: () => void;
}

export function GraphView({ state, onSelect, onClear }: Props) {
  const containerRef = useRef<HTMLDivElement>(null);
  const sigmaRef = useRef<Sigma | null>(null);
  const graphRef = useRef<Graph | null>(null);
  const selectedRef = useRef<string | null>(state.selected);
  selectedRef.current = state.selected;

  // (Re)build the Graphology graph whenever nodes/edges change. SSE refresh
  // rebuilds the graph but Sigma's camera (pan/zoom) is preserved across
  // setGraph, satisfying AC-6 viewport continuity (U4).
  useEffect(() => {
    if (!containerRef.current) return;
    const g = new Graph();
    for (const n of state.nodes) g.addNode(n.id, { ...n, x: Math.random(), y: Math.random() });
    for (const e of state.edges) {
      if (g.hasNode(e.from) && g.hasNode(e.to) && !g.hasEdge(e.from, e.to)) {
        g.addEdge(e.from, e.to, { ...e });
      }
    }
    graphRef.current = g;

    if (!sigmaRef.current) {
      sigmaRef.current = new Sigma(g, containerRef.current, {
        defaultNodeColor: COLOR_DEFAULT,
        defaultEdgeColor: COLOR_DEFAULT,
        labelDensity: 0.3,
        renderEdgeLabels: true,
      });
      sigmaRef.current.on("clickNode", ({ node }) => onSelect(String(node)));
    } else {
      sigmaRef.current.setGraph(g); // camera/viewport retained
    }

    const hasSelection = () => selectedRef.current !== null;

    sigmaRef.current.setSetting("nodeReducer", (_node, data) => {
      const out = { ...data };
      if (data.blast) {
        out.color = COLOR_BLAST;
        out.size = SIZE_BLAST;
      } else if (data.citation) {
        out.color = COLOR_CITATION;
        out.size = SIZE_DEFAULT + 2;
      } else if (hasSelection()) {
        // Out of scope while a selection is active → dim it (AC-2).
        out.color = COLOR_DIMMED;
        out.size = SIZE_DEFAULT;
      } else {
        out.color = COLOR_DEFAULT;
        out.size = SIZE_DEFAULT;
      }
      return out;
    });

    sigmaRef.current.setSetting("edgeReducer", (_edge, data) => {
      const out = { ...data };
      if (data.blast) {
        out.color = COLOR_BLAST;
        out.type = "arrow";
      } else if (data.citation) {
        // Distinct, redundant encoding (U1/U5): amber + dashed line type.
        out.color = COLOR_CITATION;
        out.type = "dashed";
      } else if (hasSelection()) {
        out.color = COLOR_DIMMED;
      } else {
        out.color = COLOR_DEFAULT;
      }
      return out;
    });

    sigmaRef.current.refresh();
  }, [state, onSelect]);

  // Keyboard Esc clears the active selection (U5).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && selectedRef.current) onClear();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClear]);

  useEffect(() => {
    return () => {
      sigmaRef.current?.kill();
      sigmaRef.current = null;
    };
  }, []);

  return <div ref={containerRef} className="graph-canvas" />;
}
