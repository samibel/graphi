// Graph visualization webview (AC-5): fetches the neighborhood payload and
// renders a minimal interactive HTML/SVG graph; clicking a node reveals its
// citation in the editor. Local resources only (no remote scripts).
import * as vscode from "vscode";
import type { Connection } from "../connection";
import type { QueryResult } from "../contract";
import { symbolUnderCursor, reveal } from "../blastRadius";

export async function runShowGraph(conn: Connection): Promise<void> {
  const client = conn.client();
  if (!client) {
    void vscode.window.showErrorMessage("graphi daemon is offline. Start it with `graphi http`.");
    return;
  }
  const sym = symbolUnderCursor(vscode.window.activeTextEditor);
  if (!sym) {
    void vscode.window.showWarningMessage("graphi: place the cursor on a symbol first.");
    return;
  }
  let result: QueryResult;
  try {
    result = await client.getNeighborhood(sym, 2);
  } catch (e) {
    void vscode.window.showErrorMessage(`graphi: ${String(e)}`);
    return;
  }

  const panel = vscode.window.createWebviewPanel(
    "graphiGraph",
    `graphi — ${sym}`,
    vscode.ViewColumn.Beside,
    { enableScripts: true, retainContextWhenHidden: false },
  );

  const nodeById = new Map(result.nodes.map((n) => [n.id, n]));
  panel.webview.html = renderHtml(sym, result);

  panel.webview.onDidReceiveMessage(async (msg) => {
    if (msg?.kind === "click" && typeof msg.id === "string") {
      const n = nodeById.get(msg.id);
      if (n?.source_path && n.line) {
        await reveal(n.source_path, n.line);
      }
    }
  });
}

function renderHtml(seed: string, result: QueryResult): string {
  // Minimal deterministic SVG layout: arrange nodes on a circle around the seed.
  const cx = 300, cy = 250, r = 180;
  const others = result.nodes.filter((n) => n.id !== seed);
  const pos = new Map<string, { x: number; y: number }>();
  pos.set(seed, { x: cx, y: cy });
  others.forEach((n, i) => {
    const a = (2 * Math.PI * i) / Math.max(1, others.length);
    pos.set(n.id, { x: cx + r * Math.cos(a), y: cy + r * Math.sin(a) });
  });
  const edgesSvg = result.edges
    .map((e) => {
      const f = pos.get(e.from), t = pos.get(e.to);
      if (!f || !t) return "";
      const color = e.confidence_tier === "confirmed" ? "#10b981" : "#6b7280";
      return `<line x1="${f.x}" y1="${f.y}" x2="${t.x}" y2="${t.y}" stroke="${color}" stroke-width="1.5" opacity="0.6"/>`;
    })
    .join("\n");
  const nodesSvg = result.nodes
    .map((n) => {
      const p = pos.get(n.id)!;
      const isSeed = n.id === seed;
      const fill = isSeed ? "#dc2626" : "#3b82f6";
      return `<g class="node" data-id="${n.id}" onclick="clickNode('${n.id}')">
        <circle cx="${p.x}" cy="${p.y}" r="${isSeed ? 10 : 7}" fill="${fill}"/>
        <text x="${p.x}" y="${p.y - 12}" fill="#9ca3af" font-size="10" text-anchor="middle">${n.qualified_name || n.id}</text>
      </g>`;
    })
    .join("\n");
  return `<!doctype html><html><head><meta charset="utf-8"/>
  <style>body{background:#0b0f17;font-family:system-ui;margin:0}svg{width:100%;height:100vh}.node{cursor:pointer}.node:hover circle{stroke:#fff}</style>
  </head><body>
  <svg viewBox="0 0 600 500">
  ${edgesSvg}
  ${nodesSvg}
  </svg>
  <script>
    function clickNode(id){
      const api = (typeof acquireVsCodeApi === 'function') ? acquireVsCodeApi() : null;
      if (api) api.postMessage({kind:'click', id});
    }
  </script>
  </body></html>`;
}
