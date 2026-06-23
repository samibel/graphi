// WikiCommunityPage renders one community page at /wiki/c/:id (SW-046). It reads
// the route param VERBATIM and fetches /wiki/c/{id}; the body (members,
// representatives, internal edges, neighbor cross-links, back-link) is rendered
// VERBATIM via WikiMarkdown. Distinct states (U1):
//   loading      — fetch in flight
//   not-found    — WikiPageNotFoundError (unknown community id → 404)
//   ready        — 200 markdown body
//
// A single-community graph yields exactly one such page (AC-5); singleton
// (size-1) communities are ordinary community pages under the Decision-2
// interpretation of AC-5's "uncategorized".
import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { WikiMarkdown } from "./WikiMarkdown";
import { fetchWikiPage, WikiPageNotFoundError } from "./wikiClient";
import { cachedPage } from "./wikiCache";

type State =
  | { kind: "loading" }
  | { kind: "not-found"; message: string }
  | { kind: "error"; message: string }
  | { kind: "ready"; body: string };

export function WikiCommunityPage() {
  // id is taken verbatim from the route — never re-derived or re-slugged.
  const { id = "" } = useParams<{ id: string }>();
  const [state, setState] = useState<State>({ kind: "loading" });

  useEffect(() => {
    let alive = true;
    setState({ kind: "loading" });
    cachedPage(id, () => fetchWikiPage(id))
      .then((body) => {
        if (alive) setState({ kind: "ready", body });
      })
      .catch((err: unknown) => {
        if (!alive) return;
        if (err instanceof WikiPageNotFoundError) {
          setState({ kind: "not-found", message: err.message });
        } else {
          setState({
            kind: "error",
            message: err instanceof Error ? err.message : "failed to load page",
          });
        }
      });
    return () => {
      alive = false;
    };
  }, [id]);

  if (state.kind === "loading") {
    return <p className="hint">Loading community {id}…</p>;
  }

  if (state.kind === "not-found") {
    return (
      <div className="wiki-state wiki-notfound" role="status">
        <h2>Community not found</h2>
        <p>
          No community with id <code>{id}</code> exists in the current wiki
          snapshot.
        </p>
        <p>
          <Link className="wiki-xref" to="/wiki">
            ← back to index
          </Link>
        </p>
      </div>
    );
  }

  if (state.kind === "error") {
    return (
      <div className="wiki-state wiki-error" role="alert">
        <h2>Couldn’t load community {id}</h2>
        <p>{state.message}</p>
        <p>
          <Link className="wiki-xref" to="/wiki">
            ← back to index
          </Link>
        </p>
      </div>
    );
  }

  return (
    <div className="wiki-page">
      <WikiMarkdown body={state.body} />
    </div>
  );
}
