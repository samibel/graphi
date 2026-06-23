// Connection status: a status-bar item reflecting daemon reachability with an
// actionable Retry command (AC-1). Never throws out of a command (no IDE crash).
import * as vscode from "vscode";
import { GraphiClient } from "./graphiClient";

export class Connection {
  private readonly item: vscode.StatusBarItem;
  private _client: GraphiClient | null = null;

  constructor(private readonly subscriptions: { dispose(): unknown }[]) {
    this.item = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 50);
    this.item.command = "graphi.retry";
    this.subscriptions.push(this.item);
    void this.refresh();
  }

  url(): string {
    return vscode.workspace.getConfiguration("graphi").get<string>("daemonUrl")
      ?? "http://127.0.0.1:8080";
  }

  /** client() returns the GraphiClient or null if the URL is invalid/offline. */
  client(): GraphiClient | null {
    return this._client;
  }

  /** refresh() pings the daemon and updates the status bar. Best-effort. */
  async refresh(): Promise<boolean> {
    try {
      const url = this.url();
      const c = new GraphiClient(url);
      await c.health();
      this._client = c;
      this.item.text = "$(graph) graphi: online";
      this.item.tooltip = `Connected to ${url}`;
      this.item.show();
      return true;
    } catch (e) {
      this._client = null;
      this.item.text = "$(warning) graphi: offline";
      this.item.tooltip = `Daemon unreachable. Click to retry. (${String(e)})`;
      this.item.show();
      return false;
    }
  }
}
