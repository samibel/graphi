// Read-only Definition + Reference providers (AC-2), composed from the Engine's
// negotiated read endpoints. SW-044 exposes no dedicated def/refs payload, so:
//   - Definition  → resolve the symbol's own node from query/neighborhood and
//                   point at its source_path:line.
//   - References  → the impacted set from the negotiated analyzer (callers /
//                   blast-radius), each mapped to a node source location.
// Both are async, non-blocking, and degrade independently when their backing
// resource is absent from /contract. All locations are constrained to
// workspace-resolvable documents (S5) via resolveWorkspaceUri.
import * as vscode from "vscode";
import type { Connection } from "../connection";
import { hasResource } from "../graphiClient";
import { resolveWorkspaceUri } from "../blastRadius";
import type { ResultNode } from "../contract";

function locationFor(node: ResultNode | undefined): vscode.Location | undefined {
  if (!node || !node.source_path) return undefined;
  const uri = resolveWorkspaceUri(node.source_path);
  if (!uri) return undefined;
  const pos = new vscode.Position(Math.max(0, node.line - 1), Math.max(0, node.column));
  return new vscode.Location(uri, pos);
}

export class GraphiDefinitionProvider implements vscode.DefinitionProvider {
  constructor(private readonly conn: Connection) {}

  async provideDefinition(
    document: vscode.TextDocument,
    position: vscode.Position,
  ): Promise<vscode.Location | undefined> {
    const client = this.conn.client();
    const contract = this.conn.contract();
    if (!client || !contract) return undefined;
    if (!hasResource(contract, "query/neighborhood")) return undefined;

    const range = document.getWordRangeAtPosition(position);
    if (!range) return undefined;
    const symbol = document.getText(range);
    if (!symbol) return undefined;

    try {
      const result = await client.getNeighborhood(symbol, 1);
      const seed = result.nodes.find((n) => n.id === symbol) ?? result.nodes[0];
      return locationFor(seed);
    } catch {
      return undefined;
    }
  }
}

export class GraphiReferenceProvider implements vscode.ReferenceProvider {
  constructor(private readonly conn: Connection) {}

  async provideReferences(
    document: vscode.TextDocument,
    position: vscode.Position,
  ): Promise<vscode.Location[]> {
    const client = this.conn.client();
    const contract = this.conn.contract();
    const route = this.conn.analyzerRoute();
    if (!client || !contract || !route) return []; // degrade: no analyzer
    if (!hasResource(contract, "query/neighborhood")) return [];

    const range = document.getWordRangeAtPosition(position);
    if (!range) return [];
    const symbol = document.getText(range);
    if (!symbol) return [];

    try {
      const [impact, neigh] = await Promise.all([
        client.getImpact(route, symbol),
        client.getNeighborhood(symbol, 1),
      ]);
      const byId = new Map(neigh.nodes.map((n) => [n.id, n]));
      const locs: vscode.Location[] = [];
      for (const id of impact.impacted) {
        const loc = locationFor(byId.get(id));
        if (loc) locs.push(loc);
      }
      return locs;
    } catch {
      return [];
    }
  }
}
