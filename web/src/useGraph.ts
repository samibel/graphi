// useGraph ties neighborhood load, blast-radius/citation highlights, and SSE
// incremental refetch together. It is the single state owner for the graph view.
import { useCallback, useEffect, useRef, useState } from "react";
import { fetchImpact, fetchNeighborhood, subscribeSSE } from "./graphiClient";
import type { QueryResult, ResultEdge, ResultNode, StreamEvent } from "./types";
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
  selected: string | null;
  loading: boolean;
  error: string | null;
}

const EMPTY: GraphState = { nodes: [], edges: [], selected: null, loading: false, error: null };

function toHL(result: QueryResult): { nodes: HighlightableNode[]; edges: HighlightableEdge[] } {
  const nodes: HighlightableNode[] = result.nodes.map((n: ResultNode) => ({
    id: n.id, blast: false, citation: false,
  }));
  const edges: HighlightableEdge[] = result.edges.map((e: ResultEdge) => ({
    id: e.id, from: e.from, to: e.to, blast: false, citation: false,
  }));
  return { nodes, edges };
}

export function useGraph(seed: string, depth: number) {
  const [state, setState] = useState<GraphState>(EMPTY);
  const seedRef = useRef({ seed, depth });
  seedRef.current = { seed, depth };

  // Load the neighborhood of the seed (AC-1).
  const load = useCallback(async (s: string, d: number) => {
    setState((st) => ({ ...st, loading: true, error: null }));
    try {
      const res = await fetchNeighborhood(s, d);
      const { nodes, edges } = toHL(res);
      setState({ nodes, edges, selected: null, loading: false, error: null });
    } catch (e) {
      setState((st) => ({ ...st, loading: false, error: String(e) }));
    }
  }, []);

  useEffect(() => {
    void load(seed, depth);
  }, [seed, depth, load]);

  // Select a node → blast + citation highlights (AC-2/3).
  const select = useCallback(async (id: string) => {
    setState((st) => {
      // optimistic: mark selected immediately
      return { ...st, selected: id };
    });
    try {
      const impact = await fetchImpact(id);
      setState((st) => {
        const blasted = applyBlast(st.nodes, new Set(impact.impacted));
        const cited = applyCitation(st.edges, blasted, id);
        return { ...st, nodes: cited.nodes, edges: cited.edges, selected: id };
      });
    } catch (e) {
      setState((st) => ({ ...st, error: String(e) }));
    }
  }, []);

  // Clear selection → reset all highlights (AC-5).
  const clear = useCallback(() => {
    setState((st) => {
      const cleared = clearHighlights(st.nodes, st.edges);
      return { ...cleared, selected: null, loading: st.loading, error: st.error };
    });
  }, []);

  // SSE incremental refetch on ingest events (AC-4): refetch neighborhood and
  // MERGE — no full reload, no interaction block.
  useEffect(() => {
    const unsub = subscribeSSE((_e: StreamEvent) => {
      const { seed: s, depth: d } = seedRef.current;
      void (async () => {
        try {
          const res = await fetchNeighborhood(s, d);
          setState((st) => {
            const merged = toHL(res);
            // preserve current selection/highlights by re-applying if a node is selected
            if (st.selected) {
              const blast = applyBlast(merged.nodes, new Set(st.nodes.filter((n) => n.blast).map((n) => n.id)));
              const cite = applyCitation(merged.edges, blast, st.selected);
              return { nodes: cite.nodes, edges: cite.edges, selected: st.selected, loading: false, error: null };
            }
            return { ...merged, selected: null, loading: false, error: null };
          });
        } catch {
          /* SSE-driven refetch is best-effort; never block interaction */
        }
      })();
    });
    return unsub;
  }, []);

  return { state, load, select, clear };
}
