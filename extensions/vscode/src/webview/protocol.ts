// Typed host<->webview message protocol. Shared by BOTH bundles (host cjs +
// webview iife) so the contract is checked at compile time on both sides. The
// host owns all network/SSE/reconnect; the webview is a pure view. Inbound
// webview->host messages are validated/whitelisted in the host (S5) before any
// side effect (e.g. reveal is constrained to workspace-resolvable documents).

/** A node as sent to the webview (host has already truncated/bounded the set). */
export interface WireNode {
  id: string;
  label: string;
  kind: string;
  source_path: string;
  line: number;
  blast: boolean;
  citation: boolean;
}

/** An edge as sent to the webview. */
export interface WireEdge {
  id: string;
  from: string;
  to: string;
  kind: string;
  confidence_tier: string;
  hasEvidence: boolean;
  blast: boolean;
  citation: boolean;
}

// --- host -> webview --------------------------------------------------------

export interface GraphMessage {
  kind: "graph";
  seed: string;
  nodes: WireNode[];
  edges: WireEdge[];
  /** True when the host truncated the payload to the node/edge caps. */
  truncated: boolean;
}

export interface StatusMessage {
  kind: "status";
  connected: boolean;
  /** Non-empty when a blocking schema mismatch is active (fail-closed render). */
  mismatch?: string;
}

export type HostToWebview = GraphMessage | StatusMessage;

// --- webview -> host --------------------------------------------------------

export interface SelectMessage {
  kind: "select";
  id: string;
}

export interface RevealMessage {
  kind: "reveal";
  path: string;
  line: number;
}

export interface ReadyMessage {
  kind: "ready";
}

export type WebviewToHost = SelectMessage | RevealMessage | ReadyMessage;

/** Validate/whitelist an inbound webview->host message (S5 trust boundary). */
export function parseWebviewMessage(raw: unknown): WebviewToHost | null {
  if (typeof raw !== "object" || raw === null) return null;
  const kind = (raw as { kind?: unknown }).kind;
  if (kind === "ready") return { kind: "ready" };
  if (kind === "select") {
    const id = (raw as { id?: unknown }).id;
    return typeof id === "string" ? { kind: "select", id } : null;
  }
  if (kind === "reveal") {
    const path = (raw as { path?: unknown }).path;
    const line = (raw as { line?: unknown }).line;
    if (typeof path === "string" && typeof line === "number") {
      return { kind: "reveal", path, line };
    }
    return null;
  }
  return null;
}
