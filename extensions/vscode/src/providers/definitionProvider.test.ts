import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as vscode from "vscode";
import type { Connection } from "../connection";
import type {
  Contract,
  QueryResult,
  SearchMatch,
} from "../contract";
import { SCHEMA_VERSION } from "../contract";
import {
  GraphiDefinitionProvider,
  GraphiReferenceProvider,
  parseSourceCitation,
} from "./definitionProvider";

const contract: Contract = {
  schema_version: SCHEMA_VERSION,
  resources: ["search", "query/definition", "query/references"],
  streams: [],
};

const workspace = vscode.workspace as unknown as {
  workspaceFolders: readonly { uri: vscode.Uri }[] | undefined;
};

function searchMatch(nodeID: string, qualifiedName: string, path: string): SearchMatch {
  return {
    node_id: nodeID,
    kind: "function",
    qualified_name: qualifiedName,
    source_path: path,
    line: 5,
    column: 2,
    rank: -1,
  };
}

function queryResult(
  operation: string,
  symbol: string,
  edges: QueryResult["edges"],
  nodes: QueryResult["nodes"] = [],
): QueryResult {
  return { operation, symbol, outcome: edges.length > 0 ? "found" : "empty", nodes, edges };
}

function documentFor(symbol: string): vscode.TextDocument {
  return {
    getWordRangeAtPosition: () =>
      new vscode.Range(new vscode.Position(0, 0), new vscode.Position(0, symbol.length)),
    getText: () => symbol,
  } as unknown as vscode.TextDocument;
}

function fakeConnection(client: object): Connection {
  return {
    client: () => client,
    contract: () => contract,
  } as unknown as Connection;
}

beforeEach(() => {
  workspace.workspaceFolders = [{ uri: vscode.Uri.file("/work/repo") }];
});

afterEach(() => {
  workspace.workspaceFolders = undefined;
  vi.restoreAllMocks();
});

describe("GraphiDefinitionProvider — lexical text to exact NodeId", () => {
  it("queries definition with the resolved NodeId and preserves edge evidence", async () => {
    const match = searchMatch("0123456789abcdef", "pkg.Foo", "pkg/foo.go");
    const getDefinition = vi.fn(async (nodeID: string) =>
      queryResult("definition", nodeID, [
        {
          id: "edge-1",
          from: "file-node",
          to: nodeID,
          kind: "defines",
          confidence_tier: "confirmed",
          confidence: 1,
          reason: "parser definition",
          evidence: ["pkg/foo.go:12:4"],
        },
      ]),
    );
    const client = {
      resolveSymbol: vi.fn(async () => ({ outcome: "found", matches: [match] })),
      getDefinition,
    };
    const provider = new GraphiDefinitionProvider(fakeConnection(client));

    const location = await provider.provideDefinition(
      documentFor("Foo"),
      new vscode.Position(0, 1),
    );

    expect(getDefinition).toHaveBeenCalledWith("0123456789abcdef");
    expect(getDefinition).not.toHaveBeenCalledWith("Foo");
    expect(Array.isArray(location)).toBe(false);
    const single = location as vscode.Location;
    expect(single.uri.fsPath).toBe("/work/repo/pkg/foo.go");
    expect(single.range.start.line).toBe(11);
    expect(single.range.start.character).toBe(3);
  });

  it("returns all valid definitions for an ambiguous exact name", async () => {
    const matches = [
      searchMatch("aaaaaaaaaaaaaaaa", "one.Foo", "one/foo.go"),
      searchMatch("bbbbbbbbbbbbbbbb", "two.Foo", "two/foo.go"),
    ];
    const getDefinition = vi.fn(async (nodeID: string) => {
      const match = matches.find((candidate) => candidate.node_id === nodeID);
      if (!match) throw new Error("plain symbol reached exact endpoint");
      return queryResult("definition", nodeID, []);
    });
    const client = {
      resolveSymbol: vi.fn(async () => ({ outcome: "ambiguous", matches })),
      getDefinition,
    };
    const provider = new GraphiDefinitionProvider(fakeConnection(client));

    const locations = await provider.provideDefinition(
      documentFor("Foo"),
      new vscode.Position(0, 1),
    );

    expect(Array.isArray(locations)).toBe(true);
    expect(locations).toHaveLength(2);
    expect(getDefinition.mock.calls.map(([nodeID]) => nodeID)).toEqual([
      "aaaaaaaaaaaaaaaa",
      "bbbbbbbbbbbbbbbb",
    ]);
  });

  it("does not call an exact endpoint for not_found", async () => {
    const getDefinition = vi.fn();
    const client = {
      resolveSymbol: vi.fn(async () => ({ outcome: "not_found", matches: [] })),
      getDefinition,
    };
    const provider = new GraphiDefinitionProvider(fakeConnection(client));
    await expect(
      provider.provideDefinition(documentFor("Missing"), new vscode.Position(0, 1)),
    ).resolves.toBeUndefined();
    expect(getDefinition).not.toHaveBeenCalled();
  });
});

describe("GraphiReferenceProvider — reference-site provenance", () => {
  it("queries references with NodeId and returns the edge-evidence use site", async () => {
    const match = searchMatch("0123456789abcdef", "pkg.Foo", "pkg/foo.go");
    const getReferences = vi.fn(async (nodeID: string) =>
      queryResult(
        "references",
        nodeID,
        [
          {
            id: "ref-edge",
            from: "referrer-node",
            to: nodeID,
            kind: "references",
            confidence_tier: "confirmed",
            confidence: 1,
            reason: "resolved reference",
            evidence: ["use/caller.go:41:7"],
          },
        ],
        [
          {
            id: "referrer-node",
            kind: "function",
            qualified_name: "use.Caller",
            source_path: "use/caller.go",
            line: 10,
            column: 1,
          },
        ],
      ),
    );
    const client = {
      resolveSymbol: vi.fn(async () => ({ outcome: "found", matches: [match] })),
      getReferences,
    };
    const provider = new GraphiReferenceProvider(fakeConnection(client));

    const locations = await provider.provideReferences(
      documentFor("Foo"),
      new vscode.Position(0, 1),
      { includeDeclaration: false },
    );

    expect(getReferences).toHaveBeenCalledWith("0123456789abcdef");
    expect(getReferences).not.toHaveBeenCalledWith("Foo");
    expect(locations).toHaveLength(1);
    expect(locations[0].uri.fsPath).toBe("/work/repo/use/caller.go");
    expect(locations[0].range.start.line).toBe(40);
    expect(locations[0].range.start.character).toBe(6);
  });
});

describe("parseSourceCitation", () => {
  it("parses path:line and path:line:column without losing path colons", () => {
    expect(parseSourceCitation("pkg/a.go:12")).toEqual({
      filePath: "pkg/a.go",
      line: 12,
      column: 1,
    });
    expect(parseSourceCitation("C:\\repo\\a.go:12:3")).toEqual({
      filePath: "C:\\repo\\a.go",
      line: 12,
      column: 3,
    });
  });

  it("rejects malformed or zero-based evidence", () => {
    expect(parseSourceCitation("not-a-citation")).toBeNull();
    expect(parseSourceCitation("a.go:0")).toBeNull();
  });
});
