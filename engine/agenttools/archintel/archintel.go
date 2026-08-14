// Package archintel implements the architecture agent tools (labs, P2
// architecture intelligence): `architecture` — the automatic community/layer
// view of the code graph — and `architecture_violations` — cycles, dependency
// back-edges, high-coupling pairs, and god modules, each with concrete edge
// counts.
//
// The partition comes from the deterministic Louvain detector (core/community,
// the SW-103 grouping mechanism) over the symbol-only projection of the graph;
// communities are labeled with their dominant package prefix so the output
// reads as architecture, not as opaque cluster ids. Dependency DIRECTION
// between communities is decided by edge majority, and layers are derived by
// deterministically peeling the dominant-direction graph from its foundation.
// No LLM classification anywhere — every line is a graph fact.
//
// Cost model: this is a documented whole-graph pass (the same precedent as
// repo_overview's Communities opt-in — detection needs every edge by
// definition): exactly one node catalog read and one edge catalog read, then
// pure in-memory aggregation.
package archintel

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corecommunity "github.com/samibel/graphi/core/community"
	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/shape"
	"github.com/samibel/graphi/engine/community"
)

const tool = "architecture"

// MethodVersion stamps the assembly logic version into the summary.
const MethodVersion = "architecture/1"

// Rank bands (rank = band<<20 + score, score < 1<<20).
const (
	bandIdentity     = 10
	bandCommunities  = 8
	bandDependencies = 6
	bandNext         = 1
)

// Section caps and defaults.
const (
	DefaultMaxItems = 60
	communityRows   = 20
	dependencyRows  = 12
	neighborNames   = 3 // depends-on / used-by labels listed per community row
)

// Params carries the architecture inputs.
type Params struct {
	// Deps are the shared engine services.
	Deps resolve.Deps
	// MaxItems caps the item list (0 selects DefaultMaxItems).
	MaxItems int
}

func (p Params) maxItems() int {
	if p.MaxItems <= 0 {
		return DefaultMaxItems
	}
	return p.MaxItems
}

// clampScore keeps a section score inside its band.
func clampScore(s int) int {
	if s >= 1<<20 {
		return 1<<20 - 1
	}
	if s < 0 {
		return 0
	}
	return s
}

// pair is an ordered (from,to) community-id pair.
type pair struct{ from, to int }

// archModel is the shared community/dependency model both tools consume.
type archModel struct {
	commIDs   []int          // community ids, ascending
	size      map[int]int    // community id → member count
	label     map[int]string // community id → dominant package prefix
	labelHits map[int]int    // members sharing the dominant prefix
	sample    map[int]string // deterministic sample source path (smallest member with one)
	symbols   int            // total symbol nodes in the partition

	pairCount map[pair]int  // ordered inter-community edge counts
	dominant  map[pair]bool // a→b present when a depends on b by edge majority
	interSum  int           // total inter-community edges

	level  map[int]int // community id → layer (foundation = 1; 0 = cyclic)
	cyclic []int       // community ids stuck in a dependency cycle, ascending

	tally shape.TierTally // confidence tiers of inter-community edges
}

// buildModel runs the one-pass projection + detection + aggregation.
func buildModel(ctx context.Context, deps resolve.Deps) (*archModel, error) {
	reader := deps.Query.Reader()
	nodes, err := reader.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return nil, err
	}
	edges, err := reader.Edges(ctx, graphstore.Query{})
	if err != nil {
		return nil, err
	}

	// Symbol-only projection: the same WP-14 hygiene rule the SW-103 detector
	// applies (communities group symbols, never external/package/file
	// artifacts), with edges incident to an excluded node dropped.
	byID := make(map[model.NodeId]model.Node, len(nodes))
	ids := make([]model.NodeId, 0, len(nodes))
	for _, n := range nodes {
		if community.IsArtifactKind(n.Kind()) {
			continue
		}
		byID[n.ID()] = n
		ids = append(ids, n.ID())
	}
	projEdges := make([]model.Edge, 0, len(edges))
	for _, e := range edges {
		if _, ok := byID[e.From()]; !ok {
			continue
		}
		if _, ok := byID[e.To()]; !ok {
			continue
		}
		projEdges = append(projEdges, e)
	}

	res := corecommunity.Detect(ids, projEdges)

	m := &archModel{
		size:      map[int]int{},
		label:     map[int]string{},
		labelHits: map[int]int{},
		sample:    map[int]string{},
		symbols:   len(ids),
		pairCount: map[pair]int{},
		dominant:  map[pair]bool{},
		level:     map[int]int{},
		tally:     shape.TierTally{},
	}

	memberOf := make(map[model.NodeId]int, len(ids))
	for _, c := range res.Communities {
		m.commIDs = append(m.commIDs, c.ID)
		m.size[c.ID] = len(c.Members)
		prefixes := map[string]int{}
		for _, member := range c.Members { // members arrive sorted by NodeId asc
			memberOf[member] = c.ID
			n := byID[member]
			prefixes[packagePrefix(n.QualifiedName())]++
			if m.sample[c.ID] == "" && n.SourcePath() != "" {
				m.sample[c.ID] = n.SourcePath()
			}
		}
		m.label[c.ID], m.labelHits[c.ID] = dominantPrefix(prefixes)
	}
	sort.Ints(m.commIDs)

	// Inter-community edge counts and confidence tiers.
	for _, e := range projEdges {
		from, to := memberOf[e.From()], memberOf[e.To()]
		if from == to {
			continue
		}
		m.pairCount[pair{from, to}]++
		m.interSum++
		m.tally.Count(e.Tier())
	}

	// Dependency direction by edge majority: a depends on b when strictly more
	// edges run a→b than b→a. A tie stays undirected (pure coupling, no layer
	// signal). Each unordered pair is visited once via its canonical (lo,hi)
	// key — the map may hold either direction (or both).
	seen := map[pair]struct{}{}
	for p := range m.pairCount {
		lo, hi := p.from, p.to
		if lo > hi {
			lo, hi = hi, lo
		}
		up := pair{lo, hi}
		if _, ok := seen[up]; ok {
			continue
		}
		seen[up] = struct{}{}
		n := m.pairCount[up]
		rev := m.pairCount[pair{hi, lo}]
		switch {
		case n > rev:
			m.dominant[up] = true
		case rev > n:
			m.dominant[pair{hi, lo}] = true
		}
	}

	m.assignLayers()
	return m, nil
}

// assignLayers peels the dominant-direction graph from its foundation:
// level(c) = 1 + max(level of the communities c depends on), 1 when it depends
// on nothing. Communities whose dependencies never fully resolve are in a
// cycle; they keep level 0 and are listed in cyclic.
func (m *archModel) assignLayers() {
	depsOf := map[int][]int{}
	for p, dom := range m.dominant {
		if dom {
			depsOf[p.from] = append(depsOf[p.from], p.to)
		}
	}
	for {
		progressed := false
		for _, c := range m.commIDs {
			if m.level[c] != 0 {
				continue
			}
			max := 0
			ready := true
			for _, d := range depsOf[c] {
				if m.level[d] == 0 {
					ready = false
					break
				}
				if m.level[d] > max {
					max = m.level[d]
				}
			}
			if ready {
				m.level[c] = max + 1
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}
	for _, c := range m.commIDs {
		if m.level[c] == 0 {
			m.cyclic = append(m.cyclic, c)
		}
	}
}

// maxLevel returns the highest assigned layer.
func (m *archModel) maxLevel() int {
	max := 0
	for _, l := range m.level {
		if l > max {
			max = l
		}
	}
	return max
}

// display renders a community as "<label> (community <id>)".
func (m *archModel) display(id int) string {
	return fmt.Sprintf("%s (community %d)", m.label[id], id)
}

// HealthView is the narrow community-graph read the code_health detectors
// consume (P5 / TODO-21): the partition size plus the two architecture-level
// violation families, rendered exactly like the architecture_violations rows.
// It exists so codehealth composes this package's model without this package
// exporting its internals.
type HealthView struct {
	Communities int
	// Cycles renders each community dependency cycle as its display-label walk.
	Cycles []string
	// BackEdges renders each against-dominant-direction dependency.
	BackEdges []string
}

// Health computes the HealthView over the same deterministic model the
// architecture tools use (one node + one edge catalog read).
func Health(ctx context.Context, deps resolve.Deps) (HealthView, error) {
	m, err := buildModel(ctx, deps)
	if err != nil {
		return HealthView{}, err
	}
	out := HealthView{Communities: len(m.commIDs)}
	for _, cyc := range findCycles(m) {
		labels := make([]string, 0, len(cyc)+1)
		for _, c := range cyc {
			labels = append(labels, m.display(c))
		}
		labels = append(labels, m.display(cyc[0]))
		out.Cycles = append(out.Cycles, strings.Join(labels, " → "))
	}
	for p, dom := range m.dominant {
		if !dom {
			continue
		}
		rev := m.pairCount[pair{p.to, p.from}]
		if rev == 0 || min(m.pairCount[p], rev) >= highCouplingMin {
			continue
		}
		out.BackEdges = append(out.BackEdges, fmt.Sprintf("%s → %s — %d edge(s) against the dominant direction (%d)", m.display(p.to), m.display(p.from), rev, m.pairCount[p]))
	}
	sort.Strings(out.BackEdges)
	return out, nil
}

// packagePrefix extracts the structural package of a qualified name: the
// prefix before the final "." (a name with no "." is its own package).
func packagePrefix(qn string) string {
	if i := strings.LastIndexByte(qn, '.'); i >= 0 {
		return qn[:i]
	}
	return qn
}

// dominantPrefix picks the most common package prefix (ties break to the
// lexicographically smallest) and how many members share it.
func dominantPrefix(prefixes map[string]int) (string, int) {
	best, hits := "", 0
	for prefix, n := range prefixes {
		if n > hits || (n == hits && prefix < best) {
			best, hits = prefix, n
		}
	}
	if best == "" {
		best = "(unnamed)"
	}
	return best, hits
}

// emptyGraph is the shared empty-graph degradation (mirrors repo_overview).
func emptyGraph(toolName string) *contract.Result {
	return &contract.Result{
		Outcome: contract.OutcomeEmpty,
		Summary: toolName + ": the graph is empty — run `graphi index` (or `graphi sync`) first",
		Confidence: contract.Confidence{
			Distribution: map[string]float64{"unknown": 1},
			Top:          "unknown",
			Method:       "empty_graph",
		},
	}
}

// Assemble builds the automatic architecture view: communities labeled by
// dominant package prefix, layered by dependency direction, plus the strongest
// inter-community dependencies.
func Assemble(ctx context.Context, p Params) (*contract.Result, error) {
	if !p.Deps.Available() {
		return shape.Unavailable(tool), nil
	}
	m, err := buildModel(ctx, p.Deps)
	if err != nil {
		return nil, err
	}
	if len(m.commIDs) == 0 {
		return emptyGraph(tool), nil
	}

	ev := shape.NewEvidenceSet()
	var items []contract.Item

	// Band 10: identity/totals.
	cyclicNote := ""
	if len(m.cyclic) > 0 {
		cyclicNote = fmt.Sprintf("; %d in a dependency cycle — run architecture-violations", len(m.cyclic))
	}
	items = append(items, contract.Item{
		RefID: "identity",
		Rank:  bandIdentity << 20,
		Reason: fmt.Sprintf("architecture: %d communities over %d symbols — %d inter-community edge(s), %d layer(s) [detector louvain]%s",
			len(m.commIDs), m.symbols, m.interSum, m.maxLevel(), cyclicNote),
	})

	// Band 8: one row per community, top layer first (foundation last), sized
	// communities first within a layer. Order is made explicit by sorting and
	// assigning a strictly decreasing score.
	rows := append([]int{}, m.commIDs...)
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if m.level[a] != m.level[b] {
			return m.level[a] > m.level[b]
		}
		if m.size[a] != m.size[b] {
			return m.size[a] > m.size[b]
		}
		return a < b
	})
	for i, c := range rows {
		if i >= communityRows {
			break
		}
		layer := fmt.Sprintf("layer %d", m.level[c])
		if m.level[c] == 0 {
			layer = "layer ? [cyclic]"
		}
		reason := fmt.Sprintf("%s: %s — %d member(s), %d on prefix %s%s%s",
			layer, m.display(c), m.size[c], m.labelHits[c], m.label[c],
			m.neighborList(c, true), m.neighborList(c, false))
		var evIDs []string
		if m.sample[c] != "" {
			evIDs = []string{ev.Add(m.sample[c], 0, "community")}
		}
		items = append(items, contract.Item{
			RefID:          fmt.Sprintf("community:%d", c),
			Rank:           bandCommunities<<20 + clampScore(len(rows)-i),
			Reason:         reason,
			EvidenceRefIDs: evIDs,
		})
	}

	// Band 6: strongest dominant dependencies (back-edges and ties are the
	// violations tool's subject).
	type depRow struct {
		from, to, n, rev int
	}
	var deps []depRow
	for p2, dom := range m.dominant {
		if !dom {
			continue
		}
		deps = append(deps, depRow{p2.from, p2.to, m.pairCount[p2], m.pairCount[pair{p2.to, p2.from}]})
	}
	sort.Slice(deps, func(i, j int) bool {
		if deps[i].n != deps[j].n {
			return deps[i].n > deps[j].n
		}
		if deps[i].from != deps[j].from {
			return deps[i].from < deps[j].from
		}
		return deps[i].to < deps[j].to
	})
	for i, d := range deps {
		if i >= dependencyRows {
			break
		}
		rev := ""
		if d.rev > 0 {
			rev = fmt.Sprintf(" (reverse %d — see architecture-violations)", d.rev)
		}
		var evIDs []string
		if m.sample[d.from] != "" {
			evIDs = []string{ev.Add(m.sample[d.from], 0, "dependency")}
		}
		items = append(items, contract.Item{
			RefID:          fmt.Sprintf("dep:%d-%d", d.from, d.to),
			Rank:           bandDependencies<<20 + clampScore(d.n),
			Reason:         fmt.Sprintf("dependency: %s → %s — %d edge(s)%s", m.display(d.from), m.display(d.to), d.n, rev),
			EvidenceRefIDs: evIDs,
		})
	}

	// Band 1: suggested next calls.
	next := []string{
		"architecture-violations — cycles, back-edges, high coupling, god modules",
		"repo-overview -communities — community sizes in whole-repository context",
	}
	for i, n := range next {
		items = append(items, contract.Item{
			RefID:  fmt.Sprintf("next-%d", i+1),
			Rank:   bandNext<<20 + (len(next) - i),
			Reason: "next: graphi " + n,
		})
	}

	r := &contract.Result{
		Outcome: contract.OutcomeFound,
		Summary: fmt.Sprintf("architecture: %d communities, %d layer(s), %d inter-community edge(s), %d in cycles — top layer %s (%s)",
			len(m.commIDs), m.maxLevel(), m.interSum, len(m.cyclic), topLayerLabel(m, rows), MethodVersion),
		Items:      items,
		Evidence:   ev.List(),
		Confidence: m.tally.Confidence("unknown", "community_graph"),
	}
	return shape.Finish(r, p.maxItems())
}

// topLayerLabel names the first community row (the top layer's largest
// community), or "-" for a degenerate partition.
func topLayerLabel(m *archModel, rows []int) string {
	if len(rows) == 0 {
		return "-"
	}
	return m.label[rows[0]]
}

// neighborList renders up to neighborNames dominant neighbors of c — its
// dependencies (out=true) or its dependents (out=false) — with edge counts,
// ordered by count desc then label asc.
func (m *archModel) neighborList(c int, out bool) string {
	type nb struct {
		label string
		n     int
	}
	var nbs []nb
	for p, dom := range m.dominant {
		if !dom {
			continue
		}
		if out && p.from == c {
			nbs = append(nbs, nb{m.label[p.to], m.pairCount[p]})
		}
		if !out && p.to == c {
			nbs = append(nbs, nb{m.label[p.from], m.pairCount[p]})
		}
	}
	if len(nbs) == 0 {
		return ""
	}
	sort.Slice(nbs, func(i, j int) bool {
		if nbs[i].n != nbs[j].n {
			return nbs[i].n > nbs[j].n
		}
		return nbs[i].label < nbs[j].label
	})
	parts := make([]string, 0, neighborNames)
	for i, x := range nbs {
		if i >= neighborNames {
			break
		}
		parts = append(parts, fmt.Sprintf("%s (%d)", x.label, x.n))
	}
	prefix := "; depends on: "
	if !out {
		prefix = "; used by: "
	}
	return prefix + strings.Join(parts, ", ")
}
