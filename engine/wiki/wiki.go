// Package wiki auto-generates a browsable, hyperlinked wiki from the code graph
// (SW-041). It runs community detection (engine/community) over the hot graph
// and renders one Markdown page per detected community plus a top-level index.
//
// Determinism is the core contract: page set, ordering, and per-page body are
// byte-for-byte identical for the same graph (no random seed, no wall-clock
// content). All iteration is sorted (by NodeId / community ID). Content is
// derived from graph facts only — members, intra/inter-community edges,
// degree-ranked representatives. There is NO natural-language synthesis and NO
// LLM backend: generation is fully local and offline.
//
// Layering: engine. Depends only on core/model + core/graphstore (read-only) +
// engine/community. Served over HTTP by surfaces/http.
package wiki

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/community"
)

// Page is one rendered wiki page (index or community).
type Page struct {
	ID    string // "index" or "<community-id>"
	Title string
	Body  string // Markdown
}

// Wiki is the generated wiki: an index page plus one page per community.
type Wiki struct {
	Index  Page
	Pages  []Page // ordered by community ID asc
	pageID map[string]struct{}
}

// PageByID returns the page with the given id and whether it exists.
func (w Wiki) PageByID(id string) (Page, bool) {
	if id == "index" || id == "" {
		return w.Index, true
	}
	if _, ok := w.pageID[id]; !ok {
		return Page{}, false
	}
	for _, p := range w.Pages {
		if p.ID == id {
			return p, true
		}
	}
	return Page{}, false
}

// Generate builds the wiki from the graph reachable via reader. It is
// deterministic, read-only, and offline.
func Generate(ctx context.Context, reader graphstore.Graphstore) (Wiki, error) {
	comms, err := community.Detect(ctx, reader)
	if err != nil {
		return Wiki{}, fmt.Errorf("wiki: community detect: %w", err)
	}

	// Resolve node details (QualifiedName, SourcePath) for member rendering.
	nodes, err := reader.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return Wiki{}, fmt.Errorf("wiki: nodes: %w", err)
	}
	nodeByID := make(map[model.NodeId]model.Node, len(nodes))
	for _, n := range nodes {
		nodeByID[n.ID()] = n
	}
	// memberCommunity[nodeId] = communityID (1-based)
	memberComm := make(map[model.NodeId]int, len(nodes))
	for _, c := range comms {
		for _, m := range c.Members {
			memberComm[m] = c.ID
		}
	}

	edges, err := reader.Edges(ctx, graphstore.Query{})
	if err != nil {
		return Wiki{}, fmt.Errorf("wiki: edges: %w", err)
	}

	// degree[nodeId] for representative ranking (undirected degree).
	degree := make(map[model.NodeId]int)
	for _, e := range edges {
		degree[e.From()]++
		degree[e.To()]++
	}

	w := Wiki{pageID: map[string]struct{}{}}
	w.Index = renderIndex(comms, nodeByID)
	for _, c := range comms {
		p := renderCommunity(c, nodeByID, memberComm, edges, degree)
		w.Pages = append(w.Pages, p)
		w.pageID[p.ID] = struct{}{}
	}
	return w, nil
}

// renderIndex builds the top-level index: all communities with member counts,
// ordered by community ID (stable).
func renderIndex(comms []community.Community, nodeByID map[model.NodeId]model.Node) Page {
	var b strings.Builder
	fmt.Fprintln(&b, "# graphi wiki — community index")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "_Auto-generated from the code graph. %d communit%s._\n",
		len(comms), plural(len(comms), "y", "ies"))
	fmt.Fprintln(&b)
	for _, c := range comms {
		fmt.Fprintf(&b, "- [Community %d](/wiki/c/%d) — %d member%s\n",
			c.ID, c.ID, len(c.Members), plural(len(c.Members), "", "s"))
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "_Generated locally from graph facts. No LLM, no network._")
	return Page{ID: "index", Title: "Community Index", Body: b.String()}
}

// renderCommunity builds one community page: members (deduped, sorted),
// intra-community edges, cross-links to neighboring communities, and
// degree-ranked representatives. All iteration is sorted → deterministic.
func renderCommunity(c community.Community, nodeByID map[model.NodeId]model.Node,
	memberComm map[model.NodeId]int, edges []model.Edge, degree map[model.NodeId]int) Page {
	var b strings.Builder
	fmt.Fprintf(&b, "# Community %d\n\n", c.ID)
	fmt.Fprintf(&b, "_%d member%s._\n\n", len(c.Members), plural(len(c.Members), "", "s"))

	memberSet := map[model.NodeId]struct{}{}
	for _, m := range c.Members {
		memberSet[m] = struct{}{}
	}

	// Members (sorted by NodeId — c.Members is already sorted by the detector).
	fmt.Fprintln(&b, "## Members")
	for _, m := range c.Members {
		n := nodeByID[m]
		qn := n.QualifiedName()
		if qn == "" {
			qn = string(m) // fall back to NodeId if qualified name missing
		}
		fmt.Fprintf(&b, "- `%s` [%s] — %s:%d\n", qn, nonEmpty(n.Kind(), "node"), nonEmpty(n.SourcePath(), "?"), n.Line())
	}
	fmt.Fprintln(&b)

	// Representatives: degree-ranked top-3 (stable tie-break by NodeId).
	reps := make([]model.NodeId, len(c.Members))
	copy(reps, c.Members)
	sort.Slice(reps, func(i, j int) bool {
		if degree[reps[i]] != degree[reps[j]] {
			return degree[reps[i]] > degree[reps[j]] // higher degree first
		}
		return reps[i] < reps[j] // NodeId tie-break
	})
	top := reps
	if len(top) > 3 {
		top = top[:3]
	}
	fmt.Fprintln(&b, "## Representatives (by degree)")
	for _, r := range top {
		fmt.Fprintf(&b, "- `%s` (degree %d)\n", r, degree[r])
	}
	fmt.Fprintln(&b)

	// Intra-community edges (sorted by edge id).
	fmt.Fprintln(&b, "## Internal edges")
	intra := 0
	for _, e := range edges { // edges slice is canonically sorted by the store
		_, fIn := memberSet[e.From()]
		_, tIn := memberSet[e.To()]
		if fIn && tIn {
			intra++
			fmt.Fprintf(&b, "- `%s` → `%s` (%s)\n", e.From(), e.To(), e.Kind())
		}
	}
	if intra == 0 {
		fmt.Fprintln(&b, "_none_")
	}
	fmt.Fprintln(&b)

	// Cross-links to neighboring communities (unique, sorted by neighbor id).
	neighbors := map[int]struct{}{}
	for _, e := range edges {
		fc, fok := memberComm[e.From()]
		tc, tok := memberComm[e.To()]
		if fok && tok && fc != tc {
			if fc == c.ID {
				neighbors[tc] = struct{}{}
			} else if tc == c.ID {
				neighbors[fc] = struct{}{}
			}
		}
	}
	ns := make([]int, 0, len(neighbors))
	for n := range neighbors {
		ns = append(ns, n)
	}
	sort.Ints(ns)
	fmt.Fprintln(&b, "## Neighboring communities")
	if len(ns) == 0 {
		fmt.Fprintln(&b, "_none_")
	} else {
		for _, n := range ns {
			fmt.Fprintf(&b, "- [→ Community %d](/wiki/c/%d)\n", n, n)
		}
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "[← back to index](/wiki)")

	return Page{ID: fmt.Sprintf("%d", c.ID), Title: fmt.Sprintf("Community %d", c.ID), Body: b.String()}
}

func plural(n int, singular, pluralSuffix string) string {
	if n == 1 {
		return singular
	}
	return pluralSuffix
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
