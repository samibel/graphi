// WikiIndexPage renders the wiki index (SW-046). It owns FOUR distinct states
// (U1) so a config problem is never confused with a data state:
//   loading      — fetch in flight
//   unavailable  — WikiUnavailableError (daemon started without WithWiki → 404)
//   empty        — 200 body reporting "0 communit…" (graph has no nodes)
//   populated    — 200 body with one or more community list items
//
// The index body is rendered VERBATIM via WikiMarkdown; even the large-index
// case (hundreds of communities) is a single long Markdown list rendered
// linearly — no virtualization (see wikiPerf.test for the measured budget).
import { useEffect, useState } from "react";
import { WikiMarkdown } from "./WikiMarkdown";
import { fetchWikiIndex, WikiUnavailableError } from "./wikiClient";
import { cachedIndex } from "./wikiCache";

type State =
  | { kind: "loading" }
  | { kind: "unavailable"; message: string }
  | { kind: "error"; message: string }
  | { kind: "ready"; body: string };

// A 200 index body is "empty" iff the Engine reported zero communities. The
// renderIndex template emits "_Auto-generated from the code graph. 0 communities._".
// We detect the DATA-empty case purely from the bytes (no re-derivation) so it
// stays visually distinct from the unavailable (404) CONFIG case.
function isEmptyIndex(body: string): boolean {
  return /\b0 communit(?:y|ies)\b/.test(body);
}

export function WikiIndexPage() {
  const [state, setState] = useState<State>({ kind: "loading" });

  useEffect(() => {
    let alive = true;
    setState({ kind: "loading" });
    cachedIndex(fetchWikiIndex)
      .then((body) => {
        if (alive) setState({ kind: "ready", body });
      })
      .catch((err: unknown) => {
        if (!alive) return;
        if (err instanceof WikiUnavailableError) {
          setState({ kind: "unavailable", message: err.message });
        } else {
          setState({
            kind: "error",
            message: err instanceof Error ? err.message : "failed to load wiki",
          });
        }
      });
    return () => {
      alive = false;
    };
  }, []);

  if (state.kind === "loading") {
    return <p className="hint">Loading wiki index…</p>;
  }

  if (state.kind === "unavailable") {
    return (
      <div className="wiki-state wiki-unavailable" role="status">
        <h2>Wiki unavailable</h2>
        <p>
          The daemon is running without the wiki surface enabled. Restart it with
          the wiki store attached to browse the auto-generated community wiki.
        </p>
        <p className="hint-inline">({state.message})</p>
      </div>
    );
  }

  if (state.kind === "error") {
    return (
      <div className="wiki-state wiki-error" role="alert">
        <h2>Couldn’t load the wiki</h2>
        <p>{state.message}</p>
      </div>
    );
  }

  // ready — distinguish DATA-empty (0 communities) from a populated index.
  if (isEmptyIndex(state.body)) {
    return (
      <div className="wiki-page">
        <div className="wiki-state wiki-empty" role="status">
          <h2>No communities yet</h2>
          <p>
            The graph has no detected communities. Index an analyzed repository,
            then reload to see community pages here.
          </p>
        </div>
        <WikiMarkdown body={state.body} />
      </div>
    );
  }

  return (
    <div className="wiki-page">
      <WikiMarkdown body={state.body} />
    </div>
  );
}
