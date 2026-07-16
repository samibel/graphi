// Read-only Definition + Reference providers (AC-2). Editor text is lexical;
// the structural HTTP routes are exact-NodeId APIs. Both providers therefore
// resolve through /search first and never pass a bare identifier to /query.
// Ambiguous exact-name matches are kept explicit: definitions/references for
// every valid candidate are returned rather than silently choosing rank 1.
import * as vscode from "vscode";
import type { Connection } from "../connection";
import { hasResource } from "../graphiClient";
import { resolveWorkspaceUri } from "../blastRadius";
import type { QueryResult, ResultNode, SearchMatch } from "../contract";

interface SourceCitation {
  filePath: string;
  line: number;
  column: number;
}

/** Parse canonical Engine evidence (`path:line` or `path:line:column`). */
export function parseSourceCitation(evidence: string): SourceCitation | null {
  const match = /^(.*?):(\d+)(?::(\d+))?$/.exec(evidence.trim());
  if (!match || !match[1]) return null;
  const line = Number(match[2]);
  const column = match[3] === undefined ? 1 : Number(match[3]);
  if (!Number.isSafeInteger(line) || line < 1) return null;
  if (!Number.isSafeInteger(column) || column < 1) return null;
  return { filePath: match[1], line, column };
}

function locationForSource(
  sourcePath: string,
  line: number,
  column: number,
): vscode.Location | undefined {
  if (!sourcePath) return undefined;
  const uri = resolveWorkspaceUri(sourcePath);
  if (!uri) return undefined;
  // Engine source coordinates are one-based; VS Code positions are zero-based.
  const pos = new vscode.Position(Math.max(0, line - 1), Math.max(0, column - 1));
  return new vscode.Location(uri, pos);
}

function locationForNode(node: ResultNode | undefined): vscode.Location | undefined {
  if (!node) return undefined;
  return locationForSource(node.source_path, node.line, node.column);
}

function locationForMatch(match: SearchMatch): vscode.Location | undefined {
  return locationForSource(match.source_path, match.line, match.column);
}

function locationForEvidence(evidence: string): vscode.Location | undefined {
  const citation = parseSourceCitation(evidence);
  if (!citation) return undefined;
  return locationForSource(citation.filePath, citation.line, citation.column);
}

function uniqueLocations(locations: readonly vscode.Location[]): vscode.Location[] {
  const seen = new Set<string>();
  const result: vscode.Location[] = [];
  for (const location of locations) {
    const key = `${location.uri.fsPath}:${location.range.start.line}:${location.range.start.character}`;
    if (seen.has(key)) continue;
    seen.add(key);
    result.push(location);
  }
  return result;
}

/** Resolve definition-edge evidence, falling back to the exact search citation. */
export function definitionLocations(
  match: SearchMatch,
  result: QueryResult,
): vscode.Location[] {
  if (result.outcome === "not_found") return [];
  const cited: vscode.Location[] = [];
  for (const edge of result.edges) {
    if (edge.kind !== "defines" || edge.to !== match.node_id) continue;
    for (const evidence of edge.evidence) {
      const location = locationForEvidence(evidence);
      if (location) cited.push(location);
    }
  }
  if (cited.length > 0) return uniqueLocations(cited);
  const fallback = locationForMatch(match);
  return fallback ? [fallback] : [];
}

/** Map reference-edge evidence to actual use sites without dropping provenance. */
export function referenceLocations(result: QueryResult): vscode.Location[] {
  if (result.outcome === "not_found") return [];
  const nodes = new Map(result.nodes.map((node) => [node.id, node]));
  const locations: vscode.Location[] = [];
  for (const edge of result.edges) {
    if (edge.kind !== "references") continue;
    let cited = false;
    for (const evidence of edge.evidence) {
      const location = locationForEvidence(evidence);
      if (!location) continue;
      locations.push(location);
      cited = true;
    }
    // Old indexes may not carry evidence. Keep a conservative, explicit
    // fallback to the referrer's own source location rather than inventing one.
    if (!cited) {
      const fallback = locationForNode(nodes.get(edge.from));
      if (fallback) locations.push(fallback);
    }
  }
  return uniqueLocations(locations);
}

export class GraphiDefinitionProvider implements vscode.DefinitionProvider {
  constructor(private readonly conn: Connection) {}

  async provideDefinition(
    document: vscode.TextDocument,
    position: vscode.Position,
  ): Promise<vscode.Location | vscode.Location[] | undefined> {
    const client = this.conn.client();
    const contract = this.conn.contract();
    if (!client || !contract) return undefined;
    if (
      !hasResource(contract, "search") ||
      !hasResource(contract, "query/definition")
    ) {
      return undefined;
    }

    const range = document.getWordRangeAtPosition(position);
    if (!range) return undefined;
    const symbolText = document.getText(range).trim();
    if (!symbolText) return undefined;

    try {
      const resolution = await client.resolveSymbol(symbolText);
      if (resolution.outcome === "not_found") return undefined;

      const resolved = await Promise.all(
        resolution.matches.map(async (match) => ({
          match,
          result: await client.getDefinition(match.node_id),
        })),
      );
      const locations = resolved.flatMap(({ match, result }) =>
        definitionLocations(match, result),
      );
      const unique = uniqueLocations(locations);
      if (unique.length === 0) return undefined;
      return unique.length === 1 ? unique[0] : unique;
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
    context: vscode.ReferenceContext,
  ): Promise<vscode.Location[]> {
    const client = this.conn.client();
    const contract = this.conn.contract();
    if (!client || !contract) return [];
    if (
      !hasResource(contract, "search") ||
      !hasResource(contract, "query/references")
    ) {
      return [];
    }

    const range = document.getWordRangeAtPosition(position);
    if (!range) return [];
    const symbolText = document.getText(range).trim();
    if (!symbolText) return [];

    try {
      const resolution = await client.resolveSymbol(symbolText);
      if (resolution.outcome === "not_found") return [];

      const resolved = await Promise.all(
        resolution.matches.map(async (match) => ({
          match,
          result: await client.getReferences(match.node_id),
        })),
      );
      const locations: vscode.Location[] = [];
      for (const { match, result } of resolved) {
        locations.push(...referenceLocations(result));
        if (context.includeDeclaration) {
          const declaration = locationForMatch(match);
          if (declaration) locations.push(declaration);
        }
      }
      return uniqueLocations(locations);
    } catch {
      return [];
    }
  }
}
