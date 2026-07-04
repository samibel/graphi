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
import { hasResource } from "./graphiClient";

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

    // SW-138 / SW-137: IDE affordances as thin clients over the shared HTTP surface.
    vscode.commands.registerCommand("graphi.relatedFiles", async () => {
      const client = conn.client();
      if (!client) {
        void vscode.window.showInformationMessage("graphi: daemon not connected.");
        return;
      }
      const symbol = await pickSymbol("Related files");
      if (!symbol) return;
      const result = await client.relatedFiles(symbol);
      void vscode.window.showInformationMessage(`graphi related_files: ${JSON.stringify(result)}`);
    }),
    vscode.commands.registerCommand("graphi.explainSymbol", async () => {
      const client = conn.client();
      if (!client) {
        void vscode.window.showInformationMessage("graphi: daemon not connected.");
        return;
      }
      const symbol = await pickSymbol("Explain symbol");
      if (!symbol) return;
      const result = await client.explainSymbol(symbol);
      void vscode.window.showInformationMessage(`graphi explain_symbol: ${JSON.stringify(result)}`);
    }),
    vscode.commands.registerCommand("graphi.changeRisk", async () => {
      const client = conn.client();
      if (!client) {
        void vscode.window.showInformationMessage("graphi: daemon not connected.");
        return;
      }
      const symbol = await pickSymbol("Change risk");
      if (!symbol) return;
      const result = await client.changeRisk(symbol);
      void vscode.window.showInformationMessage(`graphi change_risk: ${JSON.stringify(result)}`);
    }),
    vscode.commands.registerCommand("graphi.openBrief", async () => {
      const client = conn.client();
      if (!client) {
        void vscode.window.showInformationMessage("graphi: daemon not connected.");
        return;
      }
      const contract = await client.getContract().catch(() => null);
      if (!contract || !hasResource(contract, "analyze/agent_brief")) {
        void vscode.window.showInformationMessage("graphi: agent_brief not advertised by daemon.");
        return;
      }
      const topic = await vscode.window.showInputBox({ prompt: "Optional agent_brief topic" });
      const result = await client.agentBrief(topic ?? undefined);
      void vscode.window.showInformationMessage(`graphi agent_brief: ${JSON.stringify(result)}`);
    }),
    vscode.commands.registerCommand("graphi.exportAgentContext", async () => {
      const client = conn.client();
      if (!client) {
        void vscode.window.showInformationMessage("graphi: daemon not connected.");
        return;
      }
      const topic = await vscode.window.showInputBox({ prompt: "Optional export topic" });
      const contract = await client.getContract().catch(() => null);
      if (contract && hasResource(contract, "analyze/agent_brief")) {
        const result = await client.agentBrief(topic ?? undefined);
        void vscode.window.showInformationMessage(`graphi export: ${JSON.stringify(result)}`);
      } else {
        void vscode.window.showInformationMessage(`graphi export: agent_brief not available; topic=${topic ?? ""}`);
      }
    }),
    vscode.commands.registerCommand("graphi.watchIndexStatus", async () => {
      const client = conn.client();
      if (!client) {
        void vscode.window.showInformationMessage("graphi: daemon not connected.");
        return;
      }
      const result = await client.health();
      void vscode.window.showInformationMessage(`graphi: daemon status ${result.status}, schema v${result.schema_version}`);
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

async function pickSymbol(title: string): Promise<string | undefined> {
  const editor = vscode.window.activeTextEditor;
  const selected = editor?.document.getText(editor.selection);
  const input = await vscode.window.showInputBox({
    prompt: `${title}: symbol reference`,
    value: selected,
  });
  return input?.trim() || undefined;
}

export function deactivate(): void {
  /* stateless: connection + stream dispose via subscriptions */
}
