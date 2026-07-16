import { afterEach, describe, expect, it, vi } from "vitest";
import * as vscode from "vscode";
import type { Connection } from "../connection";
import { SCHEMA_VERSION, type QueryResult, type SearchMatch } from "../contract";
import { runShowGraph } from "./graphWebview";

const nodeID = "0123456789abcdef";
const match: SearchMatch = {
  node_id: nodeID,
  kind: "function",
  qualified_name: "pkg.Foo",
  source_path: "pkg/foo.go",
  line: 5,
  column: 1,
  rank: -1,
};
const neighborhood: QueryResult = {
  operation: "neighborhood",
  symbol: nodeID,
  outcome: "found",
  depth: 2,
  nodes: [
    {
      id: nodeID,
      kind: "function",
      qualified_name: "pkg.Foo",
      source_path: "pkg/foo.go",
      line: 5,
      column: 1,
    },
  ],
  edges: [],
};

let activePanel: vscode.WebviewPanel | undefined;

function setActiveSymbol(symbol: string): void {
  const position = new vscode.Position(0, 1);
  const window = vscode.window as unknown as { activeTextEditor?: unknown };
  window.activeTextEditor = {
    document: {
      getWordRangeAtPosition: () => new vscode.Range(position, position),
      getText: () => symbol,
    },
    selection: { active: position },
  };
}

function fakePanel(): vscode.WebviewPanel {
  let disposed = false;
  let onDispose: (() => unknown) | undefined;
  const webview = {
    html: "",
    cspSource: "vscode-webview:",
    asWebviewUri: (uri: vscode.Uri) => uri,
    onDidReceiveMessage: () => new vscode.Disposable(() => undefined),
    postMessage: async () => true,
  };
  const panel = {
    title: "",
    webview,
    reveal: () => undefined,
    onDidDispose: (listener: () => unknown) => {
      onDispose = listener;
      return new vscode.Disposable(() => undefined);
    },
    dispose: () => {
      if (disposed) return;
      disposed = true;
      onDispose?.();
    },
  } as unknown as vscode.WebviewPanel;
  activePanel = panel;
  return panel;
}

function connectionFor(client: object): Connection {
  return {
    client: () => client,
    contract: () => ({
      schema_version: SCHEMA_VERSION,
      resources: ["search", "query/neighborhood"],
      streams: [],
    }),
    maxDepth: () => 2,
    maxNodes: () => 100,
    maxEdges: () => 200,
    addListener: () => new vscode.Disposable(() => undefined),
  } as unknown as Connection;
}

afterEach(() => {
  activePanel?.dispose();
  activePanel = undefined;
  const window = vscode.window as unknown as { activeTextEditor?: unknown };
  window.activeTextEditor = undefined;
  vi.restoreAllMocks();
});

describe("runShowGraph — exact NodeId boundary", () => {
  it("resolves cursor text before loading the neighborhood", async () => {
    setActiveSymbol("Foo");
    const getNeighborhood = vi.fn(async () => neighborhood);
    const client = {
      resolveSymbol: vi.fn(async () => ({ outcome: "found", matches: [match] })),
      getNeighborhood,
    };
    vi.spyOn(vscode.window, "createWebviewPanel").mockReturnValue(fakePanel());

    await runShowGraph(connectionFor(client), vscode.Uri.file("/extension"));

    expect(getNeighborhood).toHaveBeenCalledWith(nodeID, 2);
    expect(getNeighborhood).not.toHaveBeenCalledWith("Foo", 2);
  });

  it("does not open or query when an ambiguous name selection is cancelled", async () => {
    setActiveSymbol("Foo");
    const getNeighborhood = vi.fn();
    const client = {
      resolveSymbol: vi.fn(async () => ({
        outcome: "ambiguous",
        matches: [match, { ...match, node_id: "fedcba9876543210" }],
      })),
      getNeighborhood,
    };
    vi.spyOn(vscode.window, "showQuickPick").mockResolvedValue(undefined);
    const createPanel = vi.spyOn(vscode.window, "createWebviewPanel");

    await runShowGraph(connectionFor(client), vscode.Uri.file("/extension"));

    expect(createPanel).not.toHaveBeenCalled();
    expect(getNeighborhood).not.toHaveBeenCalled();
  });
});
