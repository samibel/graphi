import type { SearchMatch } from "./types";

interface Props {
  query: string;
  results: SearchMatch[];
  onSelect: (nodeId: string) => void;
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
          {results.map((m) => (
            <li key={m.node_id}>
              <button type="button" onClick={() => onSelect(m.node_id)}>
                <code>{m.qualified_name}</code>
                <span className="hint-inline">
                  {" "}
                  {m.kind} · {m.source_path}:{m.line}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
