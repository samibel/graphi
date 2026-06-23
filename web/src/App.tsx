// App is the routed application shell (SW-046). It hosts a PERSISTENT header nav
// (U3) linking the graph view and the wiki in both directions, plus the route
// outlet:
//   /              → GraphPage (the SW-045 graph view, unchanged behaviour)
//   /wiki          → WikiIndexPage (community index)
//   /wiki/c/:id    → WikiCommunityPage (one community)
//
// All API-derived strings still render as escaped React text (S3) — wiki bodies
// go through WikiMarkdown with raw HTML hard-disabled; no dangerouslySetInnerHTML.
import { Link, Route, Routes } from "react-router-dom";
import { GraphPage } from "./GraphPage";
import { WikiIndexPage } from "./WikiIndexPage";
import { WikiCommunityPage } from "./WikiCommunityPage";
import "./styles.css";

export function App() {
  return (
    <div className="app">
      <nav className="appnav">
        <h1>graphi</h1>
        <Link to="/" className="navlink">
          graph
        </Link>
        <Link to="/wiki" className="navlink">
          wiki
        </Link>
      </nav>

      <Routes>
        <Route path="/" element={<GraphPage />} />
        <Route path="/wiki" element={<WikiIndexPage />} />
        <Route path="/wiki/c/:id" element={<WikiCommunityPage />} />
      </Routes>
    </div>
  );
}
