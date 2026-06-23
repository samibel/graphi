// Graph visualization webview (AC-3/AC-4). Replaces the hand-rolled SVG with a
// Sigma/Graphology renderer hosted in a bundled, CSP-restricted webview. The
// HOST owns all network/SSE: it fetches a depth/node/edge-BOUNDED neighborhood,
// truncates before postMessage, validates inbound messages, wires select→reveal,
// re-seeds on cursor change, and pushes live updates on SSE data events. The
// webview never opens a stream and never imports `vscode` — it is a pure view.
//
// Security: strict nonce CSP, localResourceRoots scoped to out/, no remote/inline
// scripts, no unsafe-inline. Inbound messages are whitelisted (S5); reveal is
// constrained to workspace-resolvable documents.
import * as vscode from "vscode";
import * as crypto from "crypto";
import type { Connection } from "../connection";
import { symbolUnderCursor, reveal } from "../blastRadius";
import type { QueryResult, ResultNode } from "../contract";
import { parseWebviewMessage, type HostToWebview } from "./protocol";
import { boundedGraphMessage } from "./bounding";

let current: GraphPanel | undefined;

export async function runShowGraph(
  conn: Connection,
  extensionUri: vscode.Uri,
): Promise<void> {
  let client = conn.client();
  if (!client) {
    const ok = await conn.refresh();
    if (!ok) {
      void vscode.window.showErrorMessage(
        "graphi daemon is offline. Start it with `graphi http`.",
      );
      return;
    }
    client = conn.client();
  }
  if (!client) return;
  const seed = symbolUnderCursor(vscode.window.activeTextEditor);
  if (!seed) {
    void vscode.window.showWarningMessage("graphi: place the cursor on a symbol first.");
    return;
  }
  if (current) {
    current.reveal();
    await current.load(seed);
    return;
  }
  current = new GraphPanel(conn, extensionUri);
  current.onDispose(() => {
    current = undefined;
  });
  await current.load(seed);
}

class GraphPanel {
  private readonly panel: vscode.WebviewPanel;
  private readonly disposables: vscode.Disposable[] = [];
  private seed = "";
  private nodeById = new Map<string, ResultNode>();
  private webviewReady = false;
  private pending: HostToWebview[] = [];

  constructor(
    private readonly conn: Connection,
    private readonly extensionUri: vscode.Uri,
  ) {
    this.panel = vscode.window.createWebviewPanel(
      "graphiGraph",
      "graphi — graph",
      vscode.ViewColumn.Beside,
      {
        enableScripts: true,
        retainContextWhenHidden: true,
        localResourceRoots: [vscode.Uri.joinPath(this.extensionUri, "out")],
      },
    );
    this.panel.webview.html = this.html();

    this.disposables.push(
      this.panel.webview.onDidReceiveMessage((raw) => this.onMessage(raw)),
      // Live update on SSE data events (AC-4): re-fetch bounded neighborhood.
      this.conn.addListener({
        onData: () => void this.refreshFromStream(),
        onState: (state, detail) =>
          this.post({
            kind: "status",
            connected: state === "connected",
            mismatch: state === "mismatch" ? detail : undefined,
          }),
      }),
      // Re-seed the subgraph on editor cursor context (AC-3 cursor-context focus).
      vscode.window.onDidChangeTextEditorSelection((e) => {
        if (e.textEditor === vscode.window.activeTextEditor) {
          const sym = symbolUnderCursor(e.textEditor);
          if (sym && sym !== this.seed) void this.load(sym);
        }
      }),
    );
    this.panel.onDidDispose(() => this.dispose(), null, this.disposables);
  }

  reveal(): void {
    this.panel.reveal(vscode.ViewColumn.Beside);
  }

  onDispose(cb: () => void): void {
    this.disposables.push(new vscode.Disposable(cb));
  }

  /** Load the bounded neighborhood of `seed` and push it to the webview. */
  async load(seed: string): Promise<void> {
    this.seed = seed;
    this.panel.title = `graphi — ${seed}`;
    const client = this.conn.client();
    if (!client) {
      this.post({ kind: "status", connected: false });
      return;
    }
    try {
      const result = await client.getNeighborhood(seed, this.conn.maxDepth());
      this.applyResult(seed, result);
    } catch (e) {
      this.post({
        kind: "status",
        connected: false,
        mismatch: e instanceof Error && e.name === "SchemaMismatchError" ? e.message : undefined,
      });
    }
  }

  /** Re-fetch on SSE data without resetting the seed (live update, AC-4). */
  private async refreshFromStream(): Promise<void> {
    if (!this.seed) return;
    await this.load(this.seed);
  }

  private applyResult(seed: string, result: QueryResult): void {
    this.nodeById = new Map(result.nodes.map((n) => [n.id, n]));
    const msg = boundedGraphMessage(
      seed,
      result,
      this.conn.maxNodes(),
      this.conn.maxEdges(),
    );
    if (msg.truncated) {
      void vscode.window.showInformationMessage(
        `graphi: graph truncated to ${this.conn.maxNodes()} nodes / ${this.conn.maxEdges()} edges for display.`,
      );
    }
    this.post(msg);
  }

  private onMessage(raw: unknown): void {
    const msg = parseWebviewMessage(raw); // whitelist/validate (S5)
    if (!msg) return;
    if (msg.kind === "ready") {
      this.webviewReady = true;
      for (const m of this.pending) this.panel.webview.postMessage(m);
      this.pending = [];
      return;
    }
    if (msg.kind === "select" || msg.kind === "reveal") {
      const node =
        msg.kind === "reveal"
          ? { source_path: msg.path, line: msg.line }
          : this.nodeById.get(msg.id);
      if (node?.source_path && node.line) {
        void reveal(node.source_path, node.line); // constrained to workspace (S5)
      }
    }
  }

  private post(msg: HostToWebview): void {
    if (this.webviewReady) {
      void this.panel.webview.postMessage(msg);
    } else {
      this.pending.push(msg);
    }
  }

  private html(): string {
    const nonce = crypto.randomBytes(16).toString("base64");
    const scriptUri = this.panel.webview.asWebviewUri(
      vscode.Uri.joinPath(this.extensionUri, "out", "webview", "main.js"),
    );
    const csp = [
      `default-src 'none'`,
      `img-src ${this.panel.webview.cspSource} data:`,
      `style-src ${this.panel.webview.cspSource} 'nonce-${nonce}'`,
      `script-src 'nonce-${nonce}'`,
    ].join("; ");
    return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta http-equiv="Content-Security-Policy" content="${csp}" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <style nonce="${nonce}">
    html, body, #root { height: 100%; margin: 0; padding: 0; }
    body { background: #0b0f17; color: #9ca3af; font-family: system-ui, sans-serif; }
    #root { position: relative; }
    .graph-canvas { width: 100%; height: 100vh; }
    .banner { position: absolute; top: 0; left: 0; right: 0; padding: 8px 12px;
      background: #7f1d1d; color: #fff; font-size: 13px; z-index: 10; }
  </style>
</head>
<body>
  <div id="root"></div>
  <script nonce="${nonce}" src="${scriptUri}"></script>
</body>
</html>`;
  }

  dispose(): void {
    while (this.disposables.length) this.disposables.pop()?.dispose();
    this.panel.dispose();
  }
}
