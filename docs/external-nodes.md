# External nodes (WP-03)

Canonical contract for interned external-symbol nodes — the graph's
representation of calls and references whose target lives **outside the
indexed repository** (standard library or third-party code, e.g.
`os/exec.Command`, `db.Query`).

## What an external node is

- **Minted by the linker, never by a parser.** When `engine/link` cannot
  resolve a cross-package call/reference and the receiver is a known import
  alias, it mints one node of kind `external` instead of dropping the
  reference.
- **Interned by construction.** The node is keyed by its qualified symbol
  name with an empty source path and zero line/column, so every callsite in
  every file that names the same external symbol produces the byte-identical
  NodeId — one node per unique qualified name, never one per callsite.
- **Evidence lives on the edge.** Provenance for the relationship sits on
  the incident `calls`/`references` edge, never on the node itself.

## Why it exists

Name-keyed analyses need a real graph node to match against. The taint
analyzer's sinks and sources (`os/exec.Command`, `db.Query`, …) would
otherwise refer to dropped, invisible references. Taint reads nodes
directly, so the query-surface filtering below does not hide external nodes
from it.

## Hard limits — by design

External nodes are deliberately second-class:

- **Heuristic tier by construction.** The incident edges are always
  `heuristic`, never `confirmed` — the target's source is not in the repo,
  so nothing can be proven against it.
- **Terminal.** External nodes carry no outgoing edges.
- **Excluded from every structural query surface**: `search`, `callers`,
  `callees`, `references`, `neighborhood`, and `impact` all filter them out
  (`engine/query/service.go`, `engine/search`, `engine/analysis/impact.go`).

The practical consequence for users: structural queries and impact
reachability cover the symbols **your repository defines**. A call into the
standard library or a third-party package is recorded (and visible to
taint), but it is not a navigable result — graphi does not claim call-graph
or impact coverage across code it has not indexed.

Implementation anchors: `core/parse.KindExternal` (vocabulary),
`engine/link` (minting and flood guards), `engine/query/service.go` and
`engine/analysis/impact.go` (filtering).
