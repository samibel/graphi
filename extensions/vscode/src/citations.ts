// Pure citation-mapping logic, framework-agnostic (no vscode import) so it is
// unit-testable in isolation. blastRadius.ts wires it into the vscode UI.
import type { ImpactResult, ResultNode, SearchMatch } from "./contract";

/** A clickable citation: symbol id + optional file:line source location. */
export interface CitationItem {
  label: string;
  description?: string;
  detail?: string; // "path:line"
  filePath?: string;
  line?: number;
}

/** toCitationItems maps an impact result + node lookup to clickable citations. */
export function toCitationItems(
  impact: ImpactResult,
  nodes: Map<string, ResultNode>,
): CitationItem[] {
  return (impact.nodes ?? []).map((reached) => {
    const id = reached.node.id;
    const n = nodes.get(id) ?? reached.node;
    return {
      label: id,
      description: n?.qualified_name ?? id,
      detail: n ? `${n.source_path}:${n.line}` : undefined,
      filePath: n?.source_path,
      line: n?.line,
    };
  });
}

/** toSearchCitations maps a search-result match list to citations. */
export function toSearchCitations(
  matches: SearchMatch[],
): CitationItem[] {
  return matches.map((m) => ({
    label: m.node_id,
    description: m.qualified_name,
    detail: `${m.source_path}:${m.line}`,
    filePath: m.source_path,
    line: m.line,
  }));
}
