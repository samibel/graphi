// graphi VS Code extension entry point (SW-043). Registers read-only commands,
// a loopback-enforced connection status item, and wires the blast-radius /
// search / graph-webview commands. The extension holds no Engine internals.
import * as vscode from "vscode";
import { Connection } from "./connection";
import { runBlastRadius } from "./blastRadius";
import { runSearch } from "./search";
import { runShowGraph } from "./webview/graphWebview";

export function activate(context: vscode.ExtensionContext): void {
  const conn = new Connection(context.subscriptions);

  context.subscriptions.push(
    vscode.commands.registerCommand("graphi.blastRadius", () => runBlastRadius(conn)),
    vscode.commands.registerCommand("graphi.search", () => runSearch(conn)),
    vscode.commands.registerCommand("graphi.showGraph", () => runShowGraph(conn)),
    vscode.commands.registerCommand("graphi.retry", () => void conn.refresh()),

    // Re-check connection when the configured daemon URL changes.
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (e.affectsConfiguration("graphi.daemonUrl")) {
        void conn.refresh();
      }
    }),
  );
}

export function deactivate(): void {
  /* stateless: connection status item disposes via subscriptions */
}
