// GraphView renders the Graphology model in a Sigma.js (WebGL) canvas and wires
// node clicks to the blast/citation highlight reducers via Sigma node/edge
// reducers. Pan/zoom is Sigma-native (AC-1). The reducers derive all visuals
// from the pure HighlightState attributes computed in highlights.ts.
//
// Sigma v3 only registers the "line" and "arrow" edge programs — using any
// other `type` value (e.g. "dashed") throws at render time and white-screens
// the app, so citation edges are encoded as amber + thicker instead.
import { useEffect, useRef } from "react";
import Graph from "graphology";
import Sigma from "sigma";
import { drawDiscNodeLabel, type NodeHoverDrawingFunction } from "sigma/rendering";
import type { ResultEdge } from "./types";
import {
  colorForKind,
  COLOR_BLAST,
  COLOR_CITATION,
  COLOR_DEFAULT,
  COLOR_DIMMED,
  SIZE_BLAST,
  SIZE_DEFAULT,
  SIZE_SEED,
} from "./highlights";
import { radialLayout } from "./layout";
import type { GraphState } from "./useGraph";

// Dark-theme hover renderer. Sigma's default (drawDiscNodeHover) paints a
// WHITE label box — unreadable with our light label text and jarring on the
// near-black canvas. Same geometry as the default, but the backdrop uses the
// site surface color with a faint phosphor glow. Purely visual.
const drawDarkNodeHover: NodeHoverDrawingFunction = (context, data, settings) => {
  const size = settings.labelSize;
  context.font = `${settings.labelWeight} ${size}px ${settings.labelFont}`;

  context.fillStyle = "#141b19"; // --color-surface-2
  context.shadowOffsetX = 0;
  context.shadowOffsetY = 0;
  context.shadowBlur = 8;
  context.shadowColor = "rgba(62, 230, 176, .25)"; // phosphor glow
  const PADDING = 2;
  if (typeof data.label === "string") {
    const textWidth = context.measureText(data.label).width;
    const boxWidth = Math.round(textWidth + 5);
    const boxHeight = Math.round(size + 2 * PADDING);
    const radius = Math.max(data.size, size / 2) + PADDING;
    const angleRadian = Math.asin(boxHeight / 2 / radius);
    const xDeltaCoord = Math.sqrt(
      Math.abs(Math.pow(radius, 2) - Math.pow(boxHeight / 2, 2)),
    );
    context.beginPath();
    context.moveTo(data.x + xDeltaCoord, data.y + boxHeight / 2);
    context.lineTo(data.x + radius + boxWidth, data.y + boxHeight / 2);
    context.lineTo(data.x + radius + boxWidth, data.y - boxHeight / 2);
    context.lineTo(data.x + xDeltaCoord, data.y - boxHeight / 2);
    context.arc(data.x, data.y, radius, angleRadian, -angleRadian);
    context.closePath();
    context.fill();
  } else {
    context.beginPath();
    context.arc(data.x, data.y, data.size + PADDING, 0, Math.PI * 2);
    context.closePath();
    context.fill();
  }
  context.shadowOffsetX = 0;
  context.shadowOffsetY = 0;
  context.shadowBlur = 0;

  drawDiscNodeLabel(context, data, settings);
};

interface Props {
  state: GraphState;
  /** Node click. `shiftKey` is true for shift-clicks (two-node compare mode). */
  onSelect: (id: string, shiftKey?: boolean) => void;
  onClear: () => void;
  onEdgeSelect?: (edge: ResultEdge) => void;
}

export function GraphView({ state, onSelect, onClear, onEdgeSelect }: Props) {
  const containerRef = useRef<HTMLDivElement>(null);
  const sigmaRef = useRef<Sigma | null>(null);
  const graphRef = useRef<Graph | null>(null);
  const selectedRef = useRef<string | null>(state.selected);
  selectedRef.current = state.selected;
  const seedRef = useRef<string | null>(state.resolvedSeed);
  seedRef.current = state.resolvedSeed;
  // The Sigma click listeners are registered ONCE (instance creation), so they
  // must read the latest callbacks/payload through refs, not stale closures.
  const onSelectRef = useRef(onSelect);
  onSelectRef.current = onSelect;
  const onEdgeSelectRef = useRef(onEdgeSelect);
  onEdgeSelectRef.current = onEdgeSelect;
  const resultEdgesRef = useRef(state.resultEdges);
  resultEdgesRef.current = state.resultEdges;

  // (Re)build the Graphology graph whenever nodes/edges change. SSE refresh
  // rebuilds the graph but Sigma's camera (pan/zoom) is preserved across
  // setGraph, satisfying AC-6 viewport continuity (U4). Positions come from the
  // deterministic radial layout (seed at the center, one ring per hop), so the
  // same graph always lands in the same place — no per-render re-scramble.
  useEffect(() => {
    if (!containerRef.current) return;
    const positions = radialLayout(
      state.nodes.map((n) => n.id),
      state.edges,
      state.resolvedSeed,
    );
    const g = new Graph();
    for (const n of state.nodes) {
      const p = positions.get(n.id) ?? { x: 0, y: 0 };
      g.addNode(n.id, { ...n, x: p.x, y: p.y });
    }
    for (const e of state.edges) {
      if (g.hasNode(e.from) && g.hasNode(e.to) && !g.hasEdge(e.from, e.to)) {
        // Keyed by the payload edge id so clickEdge can look the raw edge back
        // up in resultEdges (auto-generated Graphology keys never match).
        g.addEdgeWithKey(e.id, e.from, e.to, { ...e });
      }
    }
    graphRef.current = g;

    if (!sigmaRef.current) {
      sigmaRef.current = new Sigma(g, containerRef.current, {
        defaultNodeColor: COLOR_DEFAULT,
        defaultEdgeColor: COLOR_DEFAULT,
        renderEdgeLabels: true,
        // The graph canvas is near-black (#080b0a) — Sigma's default black
        // labels are invisible on it. Colors match the site text palette.
        labelColor: { color: "#d5e0dc" },
        edgeLabelColor: { color: "#8ba09a" },
        defaultDrawNodeHover: drawDarkNodeHover,
        labelSize: 12,
        edgeLabelSize: 10,
      });
      sigmaRef.current.on("clickNode", ({ node, event }) => {
        // Shift-click routes into two-node compare mode; Sigma exposes the
        // originating DOM event on `event.original`.
        const original = (event as { original?: unknown }).original;
        const shift =
          typeof original === "object" &&
          original !== null &&
          "shiftKey" in original &&
          Boolean((original as { shiftKey?: boolean }).shiftKey);
        onSelectRef.current(String(node), shift);
      });
      sigmaRef.current.on("clickEdge", ({ edge }) => {
        // Look up the RAW payload edge (kind/confidence/reason/evidence) — the
        // highlightable edge only carries render attributes.
        const e = resultEdgesRef.current.find((x) => x.id === edge);
        if (e) onEdgeSelectRef.current?.(e);
      });
    } else {
      sigmaRef.current.setGraph(g); // camera/viewport retained
    }

    const hasSelection = () => selectedRef.current !== null;

    sigmaRef.current.setSetting("nodeReducer", (node, data) => {
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
        out.color = colorForKind(data.kind);
        out.size = SIZE_DEFAULT;
      }
      if (node === seedRef.current) {
        // The seed anchors the layout — keep it recognizable and labeled.
        out.size = Math.max(Number(out.size) || SIZE_DEFAULT, SIZE_SEED);
        out.forceLabel = true;
      }
      return out;
    });

    sigmaRef.current.setSetting("edgeReducer", (_edge, data) => {
      const out = { ...data };
      if (data.blast) {
        out.color = COLOR_BLAST;
        out.type = "arrow";
        out.size = 2;
      } else if (data.citation) {
        // Distinct, redundant encoding (U1/U5): amber + thicker line. Sigma v3
        // has no dashed edge program, so weight carries the redundancy.
        out.color = COLOR_CITATION;
        out.size = 3;
      } else if (hasSelection()) {
        out.color = COLOR_DIMMED;
      } else if (data.confidenceTier === "heuristic") {
        // Heuristic edges stay visually secondary in the neutral view.
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
