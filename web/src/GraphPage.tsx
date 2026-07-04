// GraphPage is the original single-view graph surface extracted from App (SW-046
// routing refactor). Its behaviour is UNCHANGED from SW-045: seed symbol drives
// the neighborhood load; clicking a node triggers blast-radius + citation
// highlights; "clear" / Esc resets; named SSE events refresh incrementally.
//
// Web/IDE Polish additions: shift-click (or the "compare" toggle) picks a
// two-node pair whose direct edges are explained in CompareConnectionPanel;
// AgentToolsPanel runs the EP-020 agent tools against the selection/seed; the
// selected node feeds ExportAgentContext's Focus section.
//
// The seed survives navigation away-and-back via the URL `?symbol=` query
// param: it is read on mount and pushed back to the URL on load (U3 — preserve
// graph-view state when leaving for the wiki and returning).
import { useState } from "react";
import { GraphView } from "./GraphView";
import { Legend } from "./Legend";
import { SymbolSearchPanel } from "./SymbolSearchPanel";
import { WhyConnectedPanel } from "./WhyConnectedPanel";
import { CompareConnectionPanel } from "./CompareConnectionPanel";
import { AgentToolsPanel } from "./AgentToolsPanel";
import { ExportAgentContext } from "./ExportAgentContext";
import { useGraph } from "./useGraph";
import { hasResource, searchSymbols } from "./graphiClient";
import type { SearchMatch, ResultEdge } from "./types";

function readSeedFromUrl(): string {
  return new URLSearchParams(window.location.search).get("symbol") ?? "";
}

export function GraphPage() {
  const [seedInput, setSeedInput] = useState(readSeedFromUrl);
  const [activeSeed, setActiveSeed] = useState(readSeedFromUrl);
  const { state, select, clear, pick } = useGraph(activeSeed, 2);

  const [searchQuery, setSearchQuery] = useState("");
  const [searchResults, setSearchResults] = useState<SearchMatch[] | null>(null);
  const [selectedEdges, setSelectedEdges] = useState<ResultEdge[]>([]);
  // Two-node compare picks (shift-click or compare toggle), in click order.
  const [compareMode, setCompareMode] = useState(false);
  const [comparePair, setComparePair] = useState<string[]>([]);

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

  const handleSearch = async (q: string) => {
    setSearchQuery(q);
    if (!q.trim()) {
      setSearchResults(null);
      return;
    }
    try {
      const matches = await searchSymbols(q.trim());
      setSearchResults(matches);
    } catch {
      setSearchResults([]);
    }
  };

  const handleEdgeSelect = (edge: ResultEdge) => {
    setSelectedEdges([edge]);
  };

  const addComparePick = (id: string) => {
    setComparePair((prev) => {
      if (prev.includes(id)) return prev;
      if (prev.length >= 2) return [prev[1], id]; // keep the latest two picks
      return [...prev, id];
    });
  };

  // Node click: plain click selects (blast radius); shift-click — or any click
  // while the compare toggle is on — adds the node to the compare pair.
  const handleNodeSelect = (id: string, shiftKey?: boolean) => {
    if (shiftKey || compareMode) {
      addComparePick(id);
      return;
    }
    void select(id);
  };

  const toggleCompare = () => {
    setCompareMode((on) => {
      const next = !on;
      // Entering compare mode with an active selection seeds the pair with it.
      if (next && state.selected) {
        setComparePair((prev) =>
          prev.length === 0 ? [state.selected as string] : prev,
        );
      }
      return next;
    });
  };

  const clearCompare = () => {
    setComparePair([]);
    setCompareMode(false);
  };

  const submitSeed = (seed: string) => {
    setActiveSeed(seed);
    setComparePair([]);
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

  const selectedNode =
    state.resultNodes.find((n) => n.id === state.selected) ?? null;
  // Agent tools run against the selected node, else the resolved seed.
  const toolTarget = state.selected ?? state.resolvedSeed;
  const toolTargetLabel =
    state.resultNodes.find((n) => n.id === toolTarget)?.qualified_name ??
    toolTarget ??
    undefined;
  const briefAdvertised = state.contract
    ? hasResource(state.contract, "analyze/agent_brief")
    : false;

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
        <button
          type="button"
          onClick={() => handleSearch(seedInput)}
          disabled={!seedInput.trim()}
        >
          search
        </button>
        <button onClick={clear} disabled={!state.selected}>
          clear selection
        </button>
        <button
          type="button"
          onClick={toggleCompare}
          aria-pressed={compareMode}
          title="compare two nodes (or shift-click a pair)"
        >
          {compareMode ? "compare: on" : "compare"}
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
        <GraphView state={state} onSelect={handleNodeSelect} onClear={clear} onEdgeSelect={handleEdgeSelect} />
      </div>

      <SymbolSearchPanel
        query={searchQuery}
        results={searchResults ?? []}
        onSelect={(id) => {
          submitSeed(id);
          setSearchResults(null);
        }}
      />
      <WhyConnectedPanel edges={selectedEdges} />
      <CompareConnectionPanel
        ids={comparePair}
        edges={state.resultEdges}
        nodes={state.resultNodes}
        onClear={clearCompare}
      />
      {(toolTarget !== null || briefAdvertised) && (
        <AgentToolsPanel
          target={toolTarget}
          targetLabel={toolTargetLabel}
          briefAdvertised={briefAdvertised}
        />
      )}
      <ExportAgentContext node={selectedNode} edges={selectedEdges} />

      <footer className="status">
        {state.nodes.length} nodes · {state.edges.length} edges ·{" "}
        {state.selecting
          ? `selecting: ${state.selected}…`
          : state.selected
            ? `selected: ${state.selected}`
            : "no selection"}
        {comparePair.length > 0 && ` · comparing: ${comparePair.join(" ↔ ")}`}
      </footer>
    </>
  );
}
