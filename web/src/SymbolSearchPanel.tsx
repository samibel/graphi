import type { SearchMatch } from "./types";

interface Props {
  query: string;
  results: SearchMatch[];
  onSelect: (nodeId: string) => void;
}

/** FTS rank scores are floats (e.g. bm25 negatives); integers print bare. */
function formatRank(rank: number): string {
  return Number.isInteger(rank) ? String(rank) : rank.toFixed(2);
}

export function SymbolSearchPanel({ query, results, onSelect }: Props) {
  if (!query) return null;
  return (
    <div className="symbol-search-panel" data-testid="symbol-search-panel">
      <h3>Search results for “{query}”</h3>
      {results.length === 0 ? (
        <p className="hint">No matches.</p>
      ) : (
        <ul>
          {results.map((m, i) => (
            <li key={m.node_id}>
              <button type="button" onClick={() => onSelect(m.node_id)}>
                <span className="rank">#{i + 1}</span> <code>{m.qualified_name}</code>
                <span className="hint-inline">
                  {" "}
                  {m.kind} · {m.source_path}:{m.line} · rank {formatRank(m.rank)}
                  {m.confidence !== undefined &&
                    ` · confidence ${m.confidence.toFixed(2)}`}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
