// Read-only HoverProvider (AC-2): when the cursor rests on a symbol, query the
// Engine for the symbol's neighborhood and render a compact code-intelligence
// summary (kind, qualified name, neighbor/edge counts, top outgoing relations)
// as Markdown. Async and non-blocking; degrades to no hover when the Engine is
// offline or the `query/neighborhood` resource is absent.
import * as vscode from "vscode";
import type { Connection } from "../connection";
import { hasResource } from "../graphiClient";

export class GraphiHoverProvider implements vscode.HoverProvider {
  constructor(private readonly conn: Connection) {}

  async provideHover(
    document: vscode.TextDocument,
    position: vscode.Position,
  ): Promise<vscode.Hover | undefined> {
    const client = this.conn.client();
    const contract = this.conn.contract();
    if (!client || !contract) return undefined;
    if (!hasResource(contract, "query/neighborhood")) return undefined; // degrade

    const range = document.getWordRangeAtPosition(position);
    if (!range) return undefined;
    const symbol = document.getText(range);
    if (!symbol) return undefined;

    try {
      const result = await client.getNeighborhood(symbol, 1);
      if (result.outcome === "not_found" || result.nodes.length === 0) {
        return undefined;
      }
      const md = new vscode.MarkdownString(undefined, true);
      md.isTrusted = false; // no command links; pure display
      const seed = result.nodes.find((n) => n.id === symbol) ?? result.nodes[0];
      md.appendMarkdown(`**graphi** · \`${seed.kind}\` ${seed.qualified_name}\n\n`);
      md.appendMarkdown(
        `${result.nodes.length} node(s), ${result.edges.length} edge(s) in 1-hop neighborhood.\n\n`,
      );
      const outgoing = result.edges
        .filter((e) => e.from === seed.id)
        .slice(0, 8)
        .map((e) => `- \`${e.kind}\` → ${shortId(e.to)} _(${e.confidence_tier})_`);
      if (outgoing.length > 0) {
        md.appendMarkdown(outgoing.join("\n"));
      }
      return new vscode.Hover(md, range);
    } catch {
      return undefined; // never throw out of a provider; never block
    }
  }
}

function shortId(id: string): string {
  const parts = id.split(/[.#/]/);
  return parts[parts.length - 1] || id;
}
