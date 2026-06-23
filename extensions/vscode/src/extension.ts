// graphi VS Code extension entry point (SW-048). Wires a loopback-enforced
// connection (status bar + host-side SSE reader), read-only code-intelligence
// providers (hover, definition, references, results tree), and the Sigma graph
// webview. The extension holds no Engine internals and never mutates the graph —
// it is a pure HTTP/SSE client of the local daemon.
import * as vscode from "vscode";
import { Connection } from "./connection";
import { runBlastRadius } from "./blastRadius";
import { runSearch } from "./search";
import { runShowGraph } from "./webview/graphWebview";
import { GraphiHoverProvider } from "./providers/hoverProvider";
import {
  GraphiDefinitionProvider,
  GraphiReferenceProvider,
} from "./providers/definitionProvider";
import {
  ResultsTreeProvider,
  registerRevealNodeCommand,
} from "./providers/resultsTree";

export function activate(context: vscode.ExtensionContext): void {
  const conn = new Connection(context.secrets, context.subscriptions);
  context.subscriptions.push(conn);

  // --- code-intelligence providers (read-only, degrade independently) -------
  const tree = new ResultsTreeProvider(conn);
  context.subscriptions.push(
    tree,
    vscode.window.registerTreeDataProvider("graphiResults", tree),
    vscode.languages.registerHoverProvider("*", new GraphiHoverProvider(conn)),
    vscode.languages.registerDefinitionProvider(
      "*",
      new GraphiDefinitionProvider(conn),
    ),
    vscode.languages.registerReferenceProvider(
      "*",
      new GraphiReferenceProvider(conn),
    ),
    registerRevealNodeCommand(),
  );

  // Tree refreshes on active-editor change / cursor move (debounced) and SSE.
  context.subscriptions.push(
    vscode.window.onDidChangeActiveTextEditor(() => tree.scheduleRefresh()),
    vscode.window.onDidChangeTextEditorSelection(() => tree.scheduleRefresh()),
    conn.addListener({ onData: () => tree.scheduleRefresh() }),
  );

  // --- commands -------------------------------------------------------------
  context.subscriptions.push(
    vscode.commands.registerCommand("graphi.blastRadius", () => runBlastRadius(conn)),
    vscode.commands.registerCommand("graphi.search", () => runSearch(conn)),
    vscode.commands.registerCommand("graphi.showGraph", () =>
      runShowGraph(conn, context.extensionUri),
    ),
    vscode.commands.registerCommand("graphi.retry", () => void conn.retry()),
    vscode.commands.registerCommand("graphi.setAuthToken", async () => {
      const token = await vscode.window.showInputBox({
        prompt: "graphi: auth token for the local daemon (stored in SecretStorage)",
        password: true,
        placeHolder: "leave empty to clear",
      });
      // undefined = cancelled (no change); "" = clear.
      if (token === undefined) return;
      await conn.setAuthToken(token);
      void vscode.window.showInformationMessage(
        token ? "graphi: auth token stored." : "graphi: auth token cleared.",
      );
    }),

    // Re-check connection when the configured daemon URL changes.
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (
        e.affectsConfiguration("graphi.daemonUrl") ||
        e.affectsConfiguration("graphi.reconnect")
      ) {
        void conn.refresh();
      }
    }),
  );

  // Connect on activation (off the UI thread; never blocks).
  void conn.refresh().then(() => tree.scheduleRefresh());
}

export function deactivate(): void {
  /* stateless: connection + stream dispose via subscriptions */
}
