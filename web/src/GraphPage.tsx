// GraphPage is the original single-view graph surface extracted from App (SW-046
// routing refactor). Its behaviour is UNCHANGED from SW-045: seed symbol drives
// the neighborhood load; clicking a node triggers blast-radius + citation
// highlights; "clear" / Esc resets; named SSE events refresh incrementally.
//
// The seed survives navigation away-and-back via the URL `?symbol=` query
// param: it is read on mount and pushed back to the URL on load (U3 — preserve
// graph-view state when leaving for the wiki and returning).
import { useState } from "react";
import { GraphView } from "./GraphView";
import { Legend } from "./Legend";
import { useGraph } from "./useGraph";

function readSeedFromUrl(): string {
  return new URLSearchParams(window.location.search).get("symbol") ?? "";
}

export function GraphPage() {
  const [seedInput, setSeedInput] = useState(readSeedFromUrl);
  const [activeSeed, setActiveSeed] = useState(readSeedFromUrl);
  const { state, select, clear, pick } = useGraph(activeSeed, 2);

  // BLOCKING, fail-closed schema-mismatch state: render no graph data (AC-4).
  if (state.schemaMismatch) {
    return (
      <div className="blocking-banner" role="alert">
        <h2>Version mismatch</h2>
        <p>{state.schemaMismatch}</p>
        <p className="hint-inline">
          The client and server disagree on the contract version. Update the
          client or the daemon so they match, then reload.
        </p>
      </div>
    );
  }

  const empty =
    !state.loading &&
    state.nodes.length === 0 &&
    activeSeed !== "" &&
    !state.candidates;

  const submitSeed = (seed: string) => {
    setActiveSeed(seed);
    // Preserve the seed in the URL so it survives nav to /wiki and back (U3).
    const params = new URLSearchParams(window.location.search);
    if (seed) params.set("symbol", seed);
    else params.delete("symbol");
    const qs = params.toString();
    window.history.replaceState(
      null,
      "",
      qs ? `${window.location.pathname}?${qs}` : window.location.pathname,
    );
  };

  return (
    <>
      <div className="bar">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            submitSeed(seedInput.trim());
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
        {!state.analyzerRoute && state.contract && (
          <span className="hint-inline" title="no impact analyzer injected via /contract">
            blast-radius unavailable
          </span>
        )}
        <Legend />
      </div>

      {state.sseDisconnected && (
        <div className="notice">live updates disconnected — retrying…</div>
      )}
      {state.error && <div className="error">⚠ {state.error}</div>}

      {state.candidates && (
        <div className="candidates">
          <p className="hint-inline">
            {state.candidates.length} matches for “{activeSeed}” — pick one:
          </p>
          <ul>
            {state.candidates.map((m) => (
              <li key={m.node_id}>
                <button type="button" onClick={() => pick(m.node_id)}>
                  <code>{m.qualified_name}</code>
                  <span className="hint-inline">
                    {" "}
                    {m.kind} · {m.source_path}:{m.line}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}

      <div className="viewport">
        {state.loading && <p className="hint">Loading graph…</p>}
        {empty && <p className="hint">No symbols found for “{activeSeed}”.</p>}
        {!state.loading && state.nodes.length === 0 && activeSeed === "" && (
          <p className="hint">Enter a seed symbol and click “load”.</p>
        )}
        <GraphView state={state} onSelect={select} onClear={clear} />
      </div>

      <footer className="status">
        {state.nodes.length} nodes · {state.edges.length} edges ·{" "}
        {state.selecting
          ? `selecting: ${state.selected}…`
          : state.selected
            ? `selected: ${state.selected}`
            : "no selection"}
      </footer>
    </>
  );
}
