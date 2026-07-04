// useGraph is the single state owner for the graph view. It ties together:
//   - boot-time /contract negotiation (no hard-coded analyzer route; A3),
//   - neighborhood load + render (AC-1),
//   - single-symbol blast-radius + citation highlights (AC-2/AC-3),
//   - fail-closed schema-mismatch handling (AC-4) surfaced as a blocking state,
//   - named-event SSE refresh WITHOUT a full reload, preserving the active
//     selection/highlight (AC-6, U4).
import { useCallback, useEffect, useRef, useState } from "react";
import {
  fetchImpact,
  fetchNeighborhood,
  getContract,
  resolveAnalyzerRoute,
  SchemaMismatchError,
  searchSymbols,
  subscribeSSE,
} from "./graphiClient";
import type { Contract, QueryResult, ResultEdge, ResultNode, SearchMatch } from "./types";
import {
  applyBlast,
  applyCitation,
  clearHighlights,
  type HighlightableEdge,
  type HighlightableNode,
} from "./highlights";

export interface GraphState {
  nodes: HighlightableNode[];
  edges: HighlightableEdge[];
  /** The raw payload nodes backing `nodes` (id ↔ name/kind/path lookups). */
  resultNodes: ResultNode[];
  /** The raw payload edges backing `edges` (kind/confidence/reason/evidence). */
  resultEdges: ResultEdge[];
  selected: string | null;
  /** Selection acknowledged immediately while the blast-radius fetch is in flight (U3). */
  selecting: boolean;
  loading: boolean;
  /** Sanitized, non-blocking error message (ApiError / transient). */
  error: string | null;
  /** FAIL-CLOSED schema-mismatch banner message; when set, render NO graph data (AC-4). */
  schemaMismatch: string | null;
  /** True while the SSE stream is disconnected/retrying (non-blocking, U2). */
  sseDisconnected: boolean;
  /** Negotiated capabilities from /contract; null until boot completes. */
  contract: Contract | null;
  /** Resolved blast-radius analyzer route, or null when no analyzer is injected. */
  analyzerRoute: string | null;
  /** Ambiguous seed → ranked candidates awaiting a user pick; null otherwise. */
  candidates: SearchMatch[] | null;
  /** The node id the current graph was loaded from (SSE refreshes re-use it). */
  resolvedSeed: string | null;
}

const EMPTY: GraphState = {
  nodes: [],
  edges: [],
  resultNodes: [],
  resultEdges: [],
  selected: null,
  selecting: false,
  loading: false,
  error: null,
  schemaMismatch: null,
  sseDisconnected: false,
  contract: null,
  analyzerRoute: null,
  candidates: null,
  resolvedSeed: null,
};

function toHL(result: QueryResult): {
  nodes: HighlightableNode[];
  edges: HighlightableEdge[];
} {
  const nodes: HighlightableNode[] = result.nodes.map((n: ResultNode) => ({
    id: n.id,
    blast: false,
    citation: false,
  }));
  const edges: HighlightableEdge[] = result.edges.map((e: ResultEdge) => ({
    id: e.id,
    from: e.from,
    to: e.to,
    // Citation derives from evidence-bearing edges (D4), captured at hydrate time.
    hasEvidence: Array.isArray(e.evidence) && e.evidence.length > 0,
    blast: false,
    citation: false,
  }));
  return { nodes, edges };
}

/**
 * Pick the unambiguous best hit for a typed seed, or null when the user must
 * choose. Preference order: unique case-insensitive exact qualified-name match,
 * then unique last-segment name match (`release` ⇒ `pkg.Release`), then a
 * single-result list. Pure — exported for tests.
 */
export function chooseMatch(q: string, matches: SearchMatch[]): SearchMatch | null {
  if (matches.length === 0) return null;
  if (matches.length === 1) return matches[0];
  const needle = q.toLowerCase();
  const exact = matches.filter((m) => m.qualified_name.toLowerCase() === needle);
  if (exact.length === 1) return exact[0];
  const byName = matches.filter((m) => {
    const qn = m.qualified_name.toLowerCase();
    const base = qn.split("/").pop() ?? qn;
    // File nodes are qualified by their path — the name is the basename WITH
    // its extension ("main.go"), so only symbol QNs split on ".".
    const seg = m.kind === "file" ? base : (base.split(".").pop() ?? base);
    return seg === needle;
  });
  if (byName.length === 1) return byName[0];
  return null;
}

export function useGraph(seed: string, depth: number) {
  const [state, setState] = useState<GraphState>(EMPTY);
  const seedRef = useRef({ seed, depth });
  seedRef.current = { seed, depth };
  const analyzerRef = useRef<string | null>(null);

  // Boot: negotiate capabilities via /contract before any data calls (A3).
  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const contract = await getContract();
        const route = resolveAnalyzerRoute(contract);
        analyzerRef.current = route;
        if (!cancelled) {
          setState((st) => ({ ...st, contract, analyzerRoute: route }));
        }
      } catch (e) {
        if (cancelled) return;
        if (e instanceof SchemaMismatchError) {
          setState((st) => ({ ...st, schemaMismatch: e.message }));
        } else {
          setState((st) => ({ ...st, error: String((e as Error).message ?? e) }));
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  // The node id the graph is currently loaded from; SSE refreshes MUST re-use
  // this (not the raw typed seed) or every ingest event would re-run the
  // exact-id lookup with free text and wipe the graph.
  const resolvedRef = useRef<string | null>(null);

  // Load the neighborhood of a RESOLVED node id (AC-1).
  const loadNode = useCallback(async (id: string, d: number) => {
    setState((st) => ({ ...st, loading: true, error: null }));
    try {
      const res = await fetchNeighborhood(id, d);
      const { nodes, edges } = toHL(res);
      resolvedRef.current = id;
      setState((st) => ({
        ...st,
        nodes,
        edges,
        resultNodes: res.nodes,
        resultEdges: res.edges,
        selected: null,
        selecting: false,
        loading: false,
        error: null,
        candidates: null,
        resolvedSeed: id,
      }));
    } catch (e) {
      if (e instanceof SchemaMismatchError) {
        // Fail-closed: drop all graph data, raise the blocking banner (AC-4).
        setState((st) => ({ ...EMPTY, contract: st.contract, analyzerRoute: st.analyzerRoute, schemaMismatch: e.message }));
      } else {
        setState((st) => ({ ...st, loading: false, error: String((e as Error).message ?? e) }));
      }
    }
  }, []);

  // Resolve the typed seed, then load. The neighborhood endpoint is an exact
  // node-id lookup, so free text (e.g. "release") goes through /search first:
  // exact-id hit → load directly; unambiguous match → auto-load; several
  // plausible hits → surface them as candidates; nothing → empty ("No symbols
  // found"). A pasted node id / deep-linked ?symbol= keeps working via the
  // exact-first attempt.
  const load = useCallback(async (s: string, d: number) => {
    if (!s) {
      resolvedRef.current = null;
      setState((st) => ({ ...EMPTY, contract: st.contract, analyzerRoute: st.analyzerRoute }));
      return;
    }
    setState((st) => ({ ...st, loading: true, error: null, candidates: null }));
    try {
      const direct = await fetchNeighborhood(s, d);
      if (direct.nodes.length > 0) {
        const { nodes, edges } = toHL(direct);
        resolvedRef.current = s;
        setState((st) => ({
          ...st,
          nodes,
          edges,
          resultNodes: direct.nodes,
          resultEdges: direct.edges,
          selected: null,
          selecting: false,
          loading: false,
          error: null,
          candidates: null,
          resolvedSeed: s,
        }));
        return;
      }
      const matches = await searchSymbols(s);
      const best = chooseMatch(s, matches);
      if (best) {
        await loadNode(best.node_id, d);
        return;
      }
      resolvedRef.current = null;
      setState((st) => ({
        ...st,
        nodes: [],
        edges: [],
        resultNodes: [],
        resultEdges: [],
        selected: null,
        selecting: false,
        loading: false,
        error: null,
        candidates: matches.length > 0 ? matches : null,
        resolvedSeed: null,
      }));
    } catch (e) {
      if (e instanceof SchemaMismatchError) {
        setState((st) => ({ ...EMPTY, contract: st.contract, analyzerRoute: st.analyzerRoute, schemaMismatch: e.message }));
      } else {
        setState((st) => ({ ...st, loading: false, error: String((e as Error).message ?? e) }));
      }
    }
  }, [loadNode]);

  // Candidate click → load that node without re-resolving.
  const pick = useCallback(
    (nodeId: string) => {
      void loadNode(nodeId, seedRef.current.depth);
    },
    [loadNode],
  );

  useEffect(() => {
    void load(seed, depth);
  }, [seed, depth, load]);

  // Select a node → blast + citation highlights (AC-2/AC-3). Blast-radius is
  // gated on the negotiated analyzer; if none is injected, selection still gives
  // immediate feedback but no impact fetch is attempted.
  const select = useCallback(async (id: string) => {
    setState((st) => ({ ...st, selected: id, selecting: true })); // immediate feedback (U3)
    const route = analyzerRef.current;
    if (!route) {
      setState((st) => ({ ...st, selecting: false }));
      return;
    }
    try {
      const impact = await fetchImpact(route, id);
      setState((st) => {
        const blasted = applyBlast(st.nodes, new Set(impact.impacted));
        const cited = applyCitation(st.edges, blasted, id);
        return { ...st, nodes: cited.nodes, edges: cited.edges, selected: id, selecting: false };
      });
    } catch (e) {
      if (e instanceof SchemaMismatchError) {
        setState((st) => ({ ...EMPTY, contract: st.contract, analyzerRoute: st.analyzerRoute, schemaMismatch: e.message }));
      } else {
        setState((st) => ({ ...st, selecting: false, error: String((e as Error).message ?? e) }));
      }
    }
  }, []);

  // Clear selection → reset all highlights.
  const clear = useCallback(() => {
    setState((st) => {
      const cleared = clearHighlights(st.nodes, st.edges);
      return { ...st, ...cleared, selected: null, selecting: false };
    });
  }, []);

  // SSE: named events only (D3). On a data event, bounded re-hydrate WITHOUT a
  // full reload, preserving the active selection/highlight (AC-6, U4). The
  // viewport (pan/zoom) is owned by Sigma and is not touched here.
  useEffect(() => {
    if (!state.contract) return;
    const dataStreams = state.contract.streams.filter(
      (s) => s !== "ready" && s !== "bye" && s !== "error",
    );
    const unsub = subscribeSSE(dataStreams, {
      onReady: () => setState((st) => ({ ...st, sseDisconnected: false })),
      onData: () => {
        // Refresh from the RESOLVED node id — the raw seed may be free text
        // that the exact-id endpoint cannot serve.
        const s = resolvedRef.current;
        const { depth: d } = seedRef.current;
        if (!s) return;
        void (async () => {
          try {
            const res = await fetchNeighborhood(s, d);
            setState((st) => {
              const merged = toHL(res);
              if (st.selected) {
                // Re-apply the active highlight over the refreshed graph so the
                // selection survives the refresh (no reload, no selection drop).
                const prevBlast = new Set(
                  st.nodes.filter((n) => n.blast).map((n) => n.id),
                );
                const blast = applyBlast(merged.nodes, prevBlast);
                const cite = applyCitation(merged.edges, blast, st.selected);
                return {
                  ...st,
                  nodes: cite.nodes,
                  edges: cite.edges,
                  resultNodes: res.nodes,
                  resultEdges: res.edges,
                };
              }
              return {
                ...st,
                nodes: merged.nodes,
                edges: merged.edges,
                resultNodes: res.nodes,
                resultEdges: res.edges,
              };
            });
          } catch (e) {
            if (e instanceof SchemaMismatchError) {
              setState((st) => ({ ...EMPTY, contract: st.contract, analyzerRoute: st.analyzerRoute, schemaMismatch: e.message }));
            }
            // Other refetch failures are best-effort; never block interaction.
          }
        })();
      },
      onError: (err) => {
        if (err instanceof SchemaMismatchError) {
          setState((st) => ({ ...EMPTY, contract: st.contract, analyzerRoute: st.analyzerRoute, schemaMismatch: err.message }));
        } else {
          setState((st) => ({ ...st, error: err.message }));
        }
      },
      onTransportError: () => setState((st) => ({ ...st, sseDisconnected: true })),
      onBye: () => setState((st) => ({ ...st, sseDisconnected: true })),
    });
    return unsub;
  }, [state.contract]);

  return { state, load, select, clear, pick };
}
