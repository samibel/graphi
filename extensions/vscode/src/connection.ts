// Connection lifecycle: owns the hardened GraphiClient + the host-side SseClient,
// drives the status-bar connected/disconnected indicator, and bounds reconnects
// on SSE drop. Never throws out of a command (no IDE crash) and never blocks the
// UI thread (all network is async). The auth token lives in SecretStorage and is
// supplied to the client/SSE as a header-only provider — never logged, never in
// a URL (S3).
import * as vscode from "vscode";
import { GraphiClient, SchemaMismatchError } from "./graphiClient";
import { SseClient, type SseReconnectConfig } from "./sseClient";
import { assertLoopback } from "./loopback";
import type { Contract } from "./contract";
import { resolveAnalyzerRoute } from "./graphiClient";

const SECRET_TOKEN_KEY = "graphi.authToken";
const DEFAULT_URL = "http://127.0.0.1:8080";

export type ConnState = "connected" | "disconnected" | "mismatch";

/** Listener notified on connection-state or data-stream changes. */
export interface ConnectionListener {
  onState?: (state: ConnState, detail?: string) => void;
  /** A negotiated data event (e.g. ingest-completed) — drives live refresh. */
  onData?: (eventName: string) => void;
}

export class Connection {
  private readonly item: vscode.StatusBarItem;
  private _client: GraphiClient | null = null;
  private _contract: Contract | null = null;
  private _analyzerRoute: string | null = null;
  private sse: SseClient | null = null;
  private state: ConnState = "disconnected";
  private readonly listeners = new Set<ConnectionListener>();

  constructor(
    private readonly secrets: vscode.SecretStorage,
    private readonly subscriptions: { dispose(): unknown }[],
  ) {
    this.item = vscode.window.createStatusBarItem(
      vscode.StatusBarAlignment.Left,
      50,
    );
    this.item.command = "graphi.retry";
    this.subscriptions.push(this.item);
    this.render();
    this.item.show();
  }

  // --- config ---------------------------------------------------------------

  url(): string {
    return (
      vscode.workspace.getConfiguration("graphi").get<string>("daemonUrl") ??
      DEFAULT_URL
    );
  }

  private reconnectConfig(): SseReconnectConfig {
    const cfg = vscode.workspace.getConfiguration("graphi");
    return {
      maxAttempts: cfg.get<number>("reconnect.maxAttempts") ?? 8,
      maxIntervalMs: cfg.get<number>("reconnect.maxIntervalMs") ?? 30000,
    };
  }

  maxDepth(): number {
    const d = vscode.workspace.getConfiguration("graphi").get<number>("maxDepth") ?? 2;
    return Math.max(1, Math.min(3, d)); // hard ceiling 3
  }

  maxNodes(): number {
    return vscode.workspace.getConfiguration("graphi").get<number>("maxNodes") ?? 1500;
  }

  maxEdges(): number {
    return vscode.workspace.getConfiguration("graphi").get<number>("maxEdges") ?? 4000;
  }

  // --- token (SecretStorage) ------------------------------------------------

  /** Token provider passed to the client/SSE; reads from SecretStorage lazily. */
  private tokenProvider = (): Promise<string | undefined> =>
    Promise.resolve(this.secrets.get(SECRET_TOKEN_KEY)).then(
      (v) => v ?? undefined,
    );

  async setAuthToken(token: string | undefined): Promise<void> {
    if (token && token.length > 0) {
      await this.secrets.store(SECRET_TOKEN_KEY, token);
    } else {
      await this.secrets.delete(SECRET_TOKEN_KEY);
    }
    await this.refresh();
  }

  // --- accessors ------------------------------------------------------------

  client(): GraphiClient | null {
    return this._client;
  }

  contract(): Contract | null {
    return this._contract;
  }

  analyzerRoute(): string | null {
    return this._analyzerRoute;
  }

  currentState(): ConnState {
    return this.state;
  }

  addListener(l: ConnectionListener): vscode.Disposable {
    this.listeners.add(l);
    return new vscode.Disposable(() => this.listeners.delete(l));
  }

  // --- lifecycle ------------------------------------------------------------

  /**
   * refresh() validates loopback, pings health, negotiates /contract, and opens
   * the SSE stream. Best-effort: returns true when connected. A non-loopback URL
   * is rejected with a sanitized error and NO request is issued (S1).
   */
  async refresh(): Promise<boolean> {
    const url = this.url();
    try {
      assertLoopback(url); // reject non-loopback BEFORE any request (S1)
    } catch (e) {
      this.teardownStream();
      this._client = null;
      this.setState("disconnected", sanitize(e));
      void vscode.window.showErrorMessage(
        "graphi: configured daemon URL is not loopback (local-first). Update graphi.daemonUrl.",
      );
      return false;
    }

    try {
      const client = new GraphiClient(url, this.tokenProvider);
      await client.health();
      const contract = await client.getContract();
      this._client = client;
      this._contract = contract;
      this._analyzerRoute = resolveAnalyzerRoute(contract);
      this.startStream(client, contract);
      this.setState("connected");
      return true;
    } catch (e) {
      this.teardownStream();
      this._client = null;
      if (e instanceof SchemaMismatchError) {
        this.setState("mismatch", e.message);
        void vscode.window.showErrorMessage(`graphi: ${e.message}`);
      } else {
        this.setState("disconnected", sanitize(e));
      }
      return false;
    }
  }

  /** retry() resets SSE backoff and re-pings (graphi.retry command). */
  async retry(): Promise<void> {
    await this.refresh();
    this.sse?.retryNow();
  }

  private startStream(client: GraphiClient, contract: Contract): void {
    this.teardownStream();
    const dataStreams = contract.streams.filter(
      (s) => s !== "ready" && s !== "bye" && s !== "error",
    );
    this.sse = new SseClient(
      client.base(),
      dataStreams,
      this.reconnectConfig(),
      {
        onConnected: () => this.setState("connected"),
        onDisconnected: (reason) => this.setState("disconnected", reason),
        onData: (name) => {
          for (const l of this.listeners) l.onData?.(name);
        },
        onSchemaMismatch: (err) => {
          this.setState("mismatch", err.message);
          void vscode.window.showErrorMessage(`graphi: ${err.message}`);
        },
        onApiError: () => {
          /* non-fatal stream error; status unchanged */
        },
      },
      this.tokenProvider,
    );
    this.sse.start();
  }

  private teardownStream(): void {
    this.sse?.stop();
    this.sse = null;
  }

  private setState(state: ConnState, detail?: string): void {
    this.state = state;
    this.render(detail);
    for (const l of this.listeners) l.onState?.(state, detail);
  }

  private render(detail?: string): void {
    switch (this.state) {
      case "connected":
        this.item.text = "$(graph) graphi: connected";
        this.item.tooltip = `Connected to ${this.url()}`;
        break;
      case "mismatch":
        this.item.text = "$(error) graphi: version mismatch";
        this.item.tooltip = detail ?? "Schema version mismatch (blocking).";
        break;
      default:
        this.item.text = "$(warning) graphi: disconnected";
        this.item.tooltip = "graphi daemon unreachable. Click to retry.";
    }
  }

  dispose(): void {
    this.teardownStream();
    this.item.dispose();
  }
}

/** Sanitize an error to a short, non-leaking string (no URL/token/internals). */
function sanitize(e: unknown): string {
  if (e instanceof SchemaMismatchError) return e.message;
  if (e instanceof Error) return e.name;
  return "error";
}
