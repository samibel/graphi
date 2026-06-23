// graphi web client root. Seed symbol drives the neighborhood load; clicking a
// node triggers blast-radius + citation highlights; "clear" resets; SSE updates
// the graph incrementally. See docs/surfaces-web.md.
import { useState } from "react";
import { GraphView } from "./GraphView";
import { useGraph } from "./useGraph";
import "./styles.css";

const DEFAULT_SEED = new URLSearchParams(window.location.search).get("symbol") ?? "";

export function App() {
  const [seedInput, setSeedInput] = useState(DEFAULT_SEED);
  const [activeSeed, setActiveSeed] = useState(DEFAULT_SEED);
  const { state, select, clear } = useGraph(activeSeed, 2);

  return (
    <div className="app">
      <header className="bar">
        <h1>graphi</h1>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            setActiveSeed(seedInput.trim());
          }}
        >
          <input
            placeholder="seed symbol (e.g. pkg.Func)"
            value={seedInput}
            onChange={(e) => setSeedInput(e.target.value)}
          />
          <button type="submit">load</button>
        </form>
        <button onClick={clear} disabled={!state.selected}>
          clear selection
        </button>
        <span className="legend">
          <i style={{ background: "#dc2626" }} /> blast-radius &nbsp;
          <i style={{ background: "#d97706" }} /> citation/provenance
        </span>
      </header>
      {state.error && <div className="error">⚠ {state.error}</div>}
      <div className="viewport">
        {state.nodes.length === 0 && !state.loading && (
          <p className="hint">Enter a seed symbol and click "load".</p>
        )}
        <GraphView state={state} onSelect={select} />
      </div>
      <footer className="status">
        {state.nodes.length} nodes · {state.edges.length} edges ·{" "}
        {state.selected ? `selected: ${state.selected}` : "no selection"}
      </footer>
    </div>
  );
}
