// Pure citation-mapping logic, framework-agnostic (no vscode import) so it is
// unit-testable in isolation. blastRadius.ts wires it into the vscode UI.
import type { ImpactResult, ResultNode } from "./contract";

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
  return impact.impacted.map((id) => {
    const n = nodes.get(id);
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
  matches: Array<{ id: string; path: string; line: number }>,
): CitationItem[] {
  return matches.map((m) => ({
    label: m.id,
    detail: `${m.path}:${m.line}`,
    filePath: m.path,
    line: m.line,
  }));
}
