import { afterEach, describe, expect, it, vi } from "vitest";
import { chooseSymbolMatch, resolveWorkspaceUri, runBlastRadius } from "./blastRadius";
import { Uri } from "./test/vscode-stub";
import type * as vscode from "vscode";
import * as vscodeRuntime from "vscode";
import type { Connection } from "./connection";
import type { GraphiClient } from "./graphiClient";
import { resolveAnalyzerRoute } from "./graphiClient";
import { SCHEMA_VERSION, type Contract, type SearchMatch } from "./contract";

// The stub Uri is structurally compatible with what resolveWorkspaceUri reads
// (fsPath + joinPath); cast for the static check (real Uri only at runtime host).
const folders = [{ uri: Uri.file("/work/repo") }] as unknown as {
  uri: vscode.Uri;
}[];

const match = (nodeID: string, qualifiedName: string): SearchMatch => ({
  node_id: nodeID,
  kind: "function",
  qualified_name: qualifiedName,
  source_path: `${nodeID}.go`,
  line: 3,
  column: 1,
  rank: -1,
});

afterEach(() => {
  vi.restoreAllMocks();
  const window = vscodeRuntime.window as unknown as { activeTextEditor?: unknown };
  window.activeTextEditor = undefined;
});

describe("resolveWorkspaceUri — path-traversal guard (S5)", () => {
  it("resolves a relative path inside the workspace", () => {
    const uri = resolveWorkspaceUri("pkg/a.go", folders);
    expect(uri?.fsPath).toBe("/work/repo/pkg/a.go");
  });

  it("accepts an absolute path inside the workspace", () => {
    const uri = resolveWorkspaceUri("/work/repo/pkg/a.go", folders);
    expect(uri?.fsPath).toBe("/work/repo/pkg/a.go");
  });

  it("rejects ../ traversal that escapes the workspace", () => {
    expect(resolveWorkspaceUri("../../etc/passwd", folders)).toBeNull();
  });

  it("rejects an absolute path outside the workspace", () => {
    expect(resolveWorkspaceUri("/etc/passwd", folders)).toBeNull();
  });

  it("rejects when there is no workspace folder", () => {
    expect(resolveWorkspaceUri("a.go", undefined)).toBeNull();
    expect(resolveWorkspaceUri("a.go", [])).toBeNull();
  });

  it("rejects empty paths", () => {
    expect(resolveWorkspaceUri("", folders)).toBeNull();
  });
});

describe("chooseSymbolMatch", () => {
  it("returns the sole exact candidate without prompting", async () => {
    const only = match("0123456789abcdef", "pkg.Foo");
    const client = {
      resolveSymbol: vi.fn(async () => ({ outcome: "found", matches: [only] })),
    } as unknown as GraphiClient;
    const prompt = vi.spyOn(vscodeRuntime.window, "showQuickPick");
    await expect(chooseSymbolMatch(client, "Foo")).resolves.toEqual(only);
    expect(prompt).not.toHaveBeenCalled();
  });

  it("requires an explicit selection for ambiguous exact candidates", async () => {
    const matches = [
      match("aaaaaaaaaaaaaaaa", "one.Foo"),
      match("bbbbbbbbbbbbbbbb", "two.Foo"),
    ];
    const client = {
      resolveSymbol: vi.fn(async () => ({ outcome: "ambiguous", matches })),
    } as unknown as GraphiClient;
    vi.spyOn(vscodeRuntime.window, "showQuickPick").mockResolvedValue({
      label: "bbbbbbbbbbbbbbbb",
    });
    await expect(chooseSymbolMatch(client, "Foo")).resolves.toEqual(matches[1]);
  });

  it("declines honestly when no exact candidate exists", async () => {
    const client = {
      resolveSymbol: vi.fn(async () => ({ outcome: "not_found", matches: [] })),
    } as unknown as GraphiClient;
    await expect(chooseSymbolMatch(client, "Foo")).resolves.toBeNull();
  });
});

describe("runBlastRadius — exact NodeId boundary", () => {
  it("does not request an unrelated analyzer for an agent_brief-only contract", async () => {
    const contract: Contract = {
      schema_version: SCHEMA_VERSION,
      resources: ["search", "analyze/agent_brief"],
      streams: [],
    };
    const getImpact = vi.fn();
    const conn = {
      client: () => ({ getImpact }),
      contract: () => contract,
      analyzerRoute: () => resolveAnalyzerRoute(contract),
    } as unknown as Connection;

    expect(resolveAnalyzerRoute(contract)).toBeNull();
    await runBlastRadius(conn);
    expect(getImpact).not.toHaveBeenCalled();
  });

  it("never sends the plain cursor identifier to impact", async () => {
    const candidates = [
      match("aaaaaaaaaaaaaaaa", "one.Foo"),
      match("bbbbbbbbbbbbbbbb", "two.Foo"),
    ];
    const getImpact = vi.fn(async (_route: string, nodeID: string) => ({
      analyzer: "impact",
      outcome: "empty",
      symbol: nodeID,
      nodes: [],
    }));
    const client = {
      resolveSymbol: vi.fn(async () => ({ outcome: "ambiguous", matches: candidates })),
      getImpact,
    };
    const conn = {
      client: () => client,
      contract: () => ({
        schema_version: SCHEMA_VERSION,
        resources: ["search", "analyze/impact"],
        streams: [],
      }),
      analyzerRoute: () => "analyze/impact",
    } as unknown as Connection;
    const window = vscodeRuntime.window as unknown as {
      activeTextEditor?: {
        document: {
          getWordRangeAtPosition: () => vscodeRuntime.Range;
          getText: () => string;
        };
        selection: { active: vscodeRuntime.Position };
      };
    };
    const position = new vscodeRuntime.Position(0, 1);
    window.activeTextEditor = {
      document: {
        getWordRangeAtPosition: () => new vscodeRuntime.Range(position, position),
        getText: () => "Foo",
      },
      selection: { active: position },
    };
    vi.spyOn(vscodeRuntime.window, "showQuickPick").mockResolvedValue({
      label: "bbbbbbbbbbbbbbbb",
    });

    await runBlastRadius(conn);

    expect(getImpact).toHaveBeenCalledWith("analyze/impact", "bbbbbbbbbbbbbbbb");
    expect(getImpact).not.toHaveBeenCalledWith("analyze/impact", "Foo");
  });
});
