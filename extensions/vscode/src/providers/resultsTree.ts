// Read-only results tree (AC-1/AC-2): a TreeDataProvider for the `graphiResults`
// view, keyed off the active editor file + cursor symbol. It shows the 1-hop
// neighborhood of the symbol under the cursor (kind + qualified name; click →
// reveal source location). Refreshes (debounced) on active-editor change and
// cursor movement, and on SSE data events via the Connection listener. Async,
// non-blocking; degrades when the Engine is offline or the resource is absent.
import * as vscode from "vscode";
import type { Connection } from "../connection";
import { hasResource } from "../graphiClient";
import { symbolUnderCursor, reveal } from "../blastRadius";
import type { ResultNode } from "../contract";

class NodeItem extends vscode.TreeItem {
  constructor(readonly node: ResultNode) {
    super(node.qualified_name || node.id, vscode.TreeItemCollapsibleState.None);
    this.description = node.kind;
    this.tooltip = `${node.source_path}:${node.line}`;
    this.iconPath = new vscode.ThemeIcon("symbol-method");
    if (node.source_path) {
      this.command = {
        command: "graphi.revealNode",
        title: "Reveal",
        arguments: [node.source_path, node.line],
      };
    }
  }
}

export class ResultsTreeProvider implements vscode.TreeDataProvider<NodeItem> {
  private readonly _onDidChange = new vscode.EventEmitter<void>();
  readonly onDidChangeTreeData = this._onDidChange.event;

  private items: NodeItem[] = [];
  private currentSymbol = "";
  private debounce: ReturnType<typeof setTimeout> | null = null;

  constructor(private readonly conn: Connection) {}

  getTreeItem(element: NodeItem): vscode.TreeItem {
    return element;
  }

  getChildren(): NodeItem[] {
    return this.items;
  }

  /** Schedule a debounced refresh keyed off the active editor + cursor. */
  scheduleRefresh(): void {
    if (this.debounce) clearTimeout(this.debounce);
    this.debounce = setTimeout(() => {
      this.debounce = null;
      void this.refresh();
    }, 250);
  }

  async refresh(): Promise<void> {
    const client = this.conn.client();
    const contract = this.conn.contract();
    if (!client || !contract || !hasResource(contract, "query/neighborhood")) {
      this.items = [];
      this._onDidChange.fire();
      return;
    }
    const symbol = symbolUnderCursor(vscode.window.activeTextEditor);
    if (!symbol) {
      this.items = [];
      this.currentSymbol = "";
      this._onDidChange.fire();
      return;
    }
    this.currentSymbol = symbol;
    try {
      const result = await client.getNeighborhood(symbol, this.conn.maxDepth());
      // Guard against a stale in-flight response after the cursor moved on.
      if (this.currentSymbol !== symbol) return;
      this.items = result.nodes
        .slice(0, this.conn.maxNodes())
        .map((n) => new NodeItem(n));
      this._onDidChange.fire();
    } catch {
      this.items = [];
      this._onDidChange.fire();
    }
  }

  dispose(): void {
    if (this.debounce) clearTimeout(this.debounce);
    this._onDidChange.dispose();
  }
}

/** registerRevealNodeCommand wires the tree-item click to reveal(). */
export function registerRevealNodeCommand(): vscode.Disposable {
  return vscode.commands.registerCommand(
    "graphi.revealNode",
    (path: string, line: number) => reveal(path, line),
  );
}
