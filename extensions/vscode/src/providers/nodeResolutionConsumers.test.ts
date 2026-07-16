import { afterEach, describe, expect, it, vi } from "vitest";
import * as vscode from "vscode";
import type { Connection } from "../connection";
import { SCHEMA_VERSION, type Contract, type QueryResult, type SearchMatch } from "../contract";
import { GraphiHoverProvider } from "./hoverProvider";
import { ResultsTreeProvider } from "./resultsTree";

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
const contract: Contract = {
  schema_version: SCHEMA_VERSION,
  resources: ["search", "query/neighborhood"],
  streams: [],
};
const neighborhood: QueryResult = {
  operation: "neighborhood",
  symbol: nodeID,
  outcome: "found",
  depth: 1,
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

function documentFor(symbol: string): vscode.TextDocument {
  return {
    getWordRangeAtPosition: () =>
      new vscode.Range(new vscode.Position(0, 0), new vscode.Position(0, symbol.length)),
    getText: () => symbol,
  } as unknown as vscode.TextDocument;
}

function connectionFor(client: object): Connection {
  return {
    client: () => client,
    contract: () => contract,
    maxDepth: () => 2,
    maxNodes: () => 100,
  } as unknown as Connection;
}

function setActiveSymbol(symbol: string): void {
  const position = new vscode.Position(0, 1);
  const window = vscode.window as unknown as { activeTextEditor?: unknown };
  window.activeTextEditor = {
    document: documentFor(symbol),
    selection: { active: position },
  };
}

afterEach(() => {
  const window = vscode.window as unknown as { activeTextEditor?: unknown };
  window.activeTextEditor = undefined;
  vi.restoreAllMocks();
});

describe("GraphiHoverProvider — exact NodeId boundary", () => {
  it("resolves cursor text and queries neighborhood only with NodeId", async () => {
    const getNeighborhood = vi.fn(async () => neighborhood);
    const client = {
      resolveSymbol: vi.fn(async () => ({ outcome: "found", matches: [match] })),
      getNeighborhood,
    };
    const provider = new GraphiHoverProvider(connectionFor(client));

    const hover = await provider.provideHover(
      documentFor("Foo"),
      new vscode.Position(0, 1),
    );

    expect(hover).toBeDefined();
    expect(getNeighborhood).toHaveBeenCalledWith(nodeID, 1);
    expect(getNeighborhood).not.toHaveBeenCalledWith("Foo", 1);
  });

  it("degrades on ambiguity without querying an exact endpoint", async () => {
    const getNeighborhood = vi.fn();
    const client = {
      resolveSymbol: vi.fn(async () => ({
        outcome: "ambiguous",
        matches: [match, { ...match, node_id: "fedcba9876543210" }],
      })),
      getNeighborhood,
    };
    const provider = new GraphiHoverProvider(connectionFor(client));
    await expect(
      provider.provideHover(documentFor("Foo"), new vscode.Position(0, 1)),
    ).resolves.toBeUndefined();
    expect(getNeighborhood).not.toHaveBeenCalled();
  });
});

describe("ResultsTreeProvider — exact NodeId boundary", () => {
  it("uses NodeId when unique and clears the tree when the name becomes ambiguous", async () => {
    setActiveSymbol("Foo");
    const getNeighborhood = vi.fn(async () => neighborhood);
    const resolveSymbol = vi
      .fn()
      .mockResolvedValueOnce({ outcome: "found", matches: [match] })
      .mockResolvedValueOnce({
        outcome: "ambiguous",
        matches: [match, { ...match, node_id: "fedcba9876543210" }],
      });
    const tree = new ResultsTreeProvider(
      connectionFor({ resolveSymbol, getNeighborhood }),
    );

    await tree.refresh();
    expect(tree.getChildren()).toHaveLength(1);
    expect(getNeighborhood).toHaveBeenCalledWith(nodeID, 2);
    expect(getNeighborhood).not.toHaveBeenCalledWith("Foo", 2);

    await tree.refresh();
    expect(tree.getChildren()).toHaveLength(0);
    expect(getNeighborhood).toHaveBeenCalledTimes(1);
    tree.dispose();
  });
});
