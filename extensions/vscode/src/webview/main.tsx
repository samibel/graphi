// Webview entry (pure view, no `vscode` API, no network). Renders the
// host-supplied graph with Sigma/Graphology (WebGL), supports native pan/zoom
// and node selection, and navigates the editor on select via postMessage. On a
// new host `graph` message (e.g. SSE live update, AC-4) it rebuilds the
// Graphology model while PRESERVING the Sigma camera (pan/zoom) and the active
// selection — pattern ported from web/src/{GraphView.tsx,useGraph.ts}. A
// blocking schema-mismatch status renders a fail-closed banner and NO graph.
import { useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import Graph from "graphology";
import Sigma from "sigma";
import {
  COLOR_BLAST,
  COLOR_CITATION,
  COLOR_DEFAULT,
  COLOR_DIMMED,
  SIZE_BLAST,
  SIZE_DEFAULT,
} from "../highlights";
import type { GraphMessage, HostToWebview, WebviewToHost } from "./protocol";

interface VsApi {
  postMessage(msg: WebviewToHost): void;
}
declare function acquireVsCodeApi(): VsApi;

const vscodeApi: VsApi = acquireVsCodeApi();

function App() {
  const containerRef = useRef<HTMLDivElement>(null);
  const sigmaRef = useRef<Sigma | null>(null);
  const selectedRef = useRef<string | null>(null);
  const [graph, setGraph] = useState<GraphMessage | null>(null);
  const [mismatch, setMismatch] = useState<string | null>(null);
  const [connected, setConnected] = useState(true);

  // Inbound host messages. The webview never fetches; it only reflects state.
  useEffect(() => {
    const onMessage = (ev: MessageEvent) => {
      const msg = ev.data as HostToWebview;
      if (!msg || typeof msg !== "object") return;
      if (msg.kind === "graph") {
        setMismatch(null);
        setGraph(msg);
      } else if (msg.kind === "status") {
        setConnected(msg.connected);
        setMismatch(msg.mismatch ?? null);
      }
    };
    window.addEventListener("message", onMessage);
    vscodeApi.postMessage({ kind: "ready" }); // request the first payload
    return () => window.removeEventListener("message", onMessage);
  }, []);

  // (Re)build the Graphology graph whenever the payload changes. Sigma's camera
  // is preserved across setGraph (viewport continuity); the selection survives.
  useEffect(() => {
    if (mismatch) return; // fail-closed: render no graph during a mismatch
    if (!containerRef.current || !graph) return;
    const g = new Graph();
    for (const n of graph.nodes) {
      g.addNode(n.id, {
        label: n.label,
        blast: n.blast,
        citation: n.citation,
        x: Math.random(),
        y: Math.random(),
        size: SIZE_DEFAULT,
        color: COLOR_DEFAULT,
      });
    }
    for (const e of graph.edges) {
      if (g.hasNode(e.from) && g.hasNode(e.to) && !g.hasEdge(e.from, e.to)) {
        g.addEdge(e.from, e.to, { blast: e.blast, citation: e.citation });
      }
    }

    if (!sigmaRef.current) {
      sigmaRef.current = new Sigma(g, containerRef.current, {
        defaultNodeColor: COLOR_DEFAULT,
        defaultEdgeColor: COLOR_DEFAULT,
        labelDensity: 0.3,
      });
      sigmaRef.current.on("clickNode", ({ node }) => {
        selectedRef.current = String(node);
        vscodeApi.postMessage({ kind: "select", id: String(node) });
        sigmaRef.current?.refresh();
      });
    } else {
      sigmaRef.current.setGraph(g); // camera/viewport retained across updates
    }

    const hasSelection = () => selectedRef.current !== null;
    sigmaRef.current.setSetting("nodeReducer", (node, data) => {
      const out = { ...data };
      if (data.blast || node === selectedRef.current) {
        out.color = COLOR_BLAST;
        out.size = SIZE_BLAST;
      } else if (data.citation) {
        out.color = COLOR_CITATION;
        out.size = SIZE_DEFAULT + 2;
      } else if (hasSelection()) {
        out.color = COLOR_DIMMED;
      } else {
        out.color = COLOR_DEFAULT;
      }
      return out;
    });
    sigmaRef.current.setSetting("edgeReducer", (_edge, data) => {
      const out = { ...data };
      if (data.blast) out.color = COLOR_BLAST;
      else if (data.citation) out.color = COLOR_CITATION;
      else if (hasSelection()) out.color = COLOR_DIMMED;
      else out.color = COLOR_DEFAULT;
      return out;
    });
    sigmaRef.current.refresh();
  }, [graph, mismatch]);

  useEffect(() => {
    return () => {
      sigmaRef.current?.kill();
      sigmaRef.current = null;
    };
  }, []);

  return (
    <>
      {mismatch && (
        <div className="banner">
          graphi: {mismatch} — display blocked (fail-closed). Update the extension
          or Engine to matching schema versions.
        </div>
      )}
      {!mismatch && !connected && (
        <div className="banner">graphi: disconnected — showing last graph.</div>
      )}
      <div ref={containerRef} className="graph-canvas" />
    </>
  );
}

const rootEl = document.getElementById("root");
if (rootEl) createRoot(rootEl).render(<App />);
