package archintel

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/shape"
)

const toolViolations = "architecture_violations"

// ViolationsMethodVersion stamps the detection logic version into the summary.
const ViolationsMethodVersion = "architecture_violations/1"

// Violation rank bands (rank = band<<20 + score, score < 1<<20).
const (
	bandVIdentity  = 10
	bandCycles     = 9
	bandBackEdges  = 7
	bandCoupling   = 5
	bandGodModules = 3
	bandVNext      = 1
)

// Detection thresholds — pinned constants, quoted in the reasons when they
// fire so every finding is auditable.
const (
	// highCouplingMin: an unordered pair with at least this many edges in BOTH
	// directions is coupling (no clean layering), not a back-edge.
	highCouplingMin = 3
	// godMinCommunities: god-module detection needs a partition this large to
	// be meaningful at all.
	godMinCommunities = 4
	// godEdgeSharePct / godTouchPct: a community is a god module when it holds
	// at least half of ALL inter-community edge mass and touches at least this
	// share of the other communities.
	godEdgeSharePct = 50
	godTouchPct     = 60
	// cycleCap bounds how many distinct cycles are reported.
	cycleCap = 8
	// backEdgeRows / couplingRows bound those sections.
	backEdgeRows = 12
	couplingRows = 8
)

// ViolationsParams carries the architecture_violations inputs.
type ViolationsParams struct {
	// Deps are the shared engine services.
	Deps resolve.Deps
	// MaxItems caps the item list (0 selects DefaultMaxItems).
	MaxItems int
}

func (p ViolationsParams) maxItems() int {
	if p.MaxItems <= 0 {
		return DefaultMaxItems
	}
	return p.MaxItems
}

// Violations detects architecture violations on the community dependency
// graph: dependency cycles, edges against the dominant direction, high-coupling
// pairs, and god modules.
func Violations(ctx context.Context, p ViolationsParams) (*contract.Result, error) {
	if !p.Deps.Available() {
		return shape.Unavailable(toolViolations), nil
	}
	m, err := buildModel(ctx, p.Deps)
	if err != nil {
		return nil, err
	}
	if len(m.commIDs) == 0 {
		return emptyGraph(toolViolations), nil
	}

	ev := shape.NewEvidenceSet()
	var items []contract.Item
	sampleRefs := func(ids ...int) []string {
		var out []string
		for _, c := range ids {
			if m.sample[c] != "" {
				out = append(out, ev.Add(m.sample[c], 0, "violation"))
				return out // one representative citation per finding
			}
		}
		return out
	}

	// Band 9: dependency cycles on the dominant-direction graph.
	// Cycle rows name communities via display() (label + id): dominant-prefix
	// labels are not unique, and a loop of three same-labeled communities is
	// unreadable without the ids.
	cycles := findCycles(m)
	for i, cyc := range cycles {
		labels := make([]string, 0, len(cyc)+1)
		counts := make([]string, 0, len(cyc))
		total := 0
		for j, c := range cyc {
			labels = append(labels, m.display(c))
			n := m.pairCount[pair{c, cyc[(j+1)%len(cyc)]}]
			counts = append(counts, fmt.Sprintf("%d", n))
			total += n
		}
		labels = append(labels, m.display(cyc[0])) // close the loop visually
		items = append(items, contract.Item{
			RefID:          fmt.Sprintf("cycle-%d", i+1),
			Rank:           bandCycles<<20 + clampScore(total),
			Reason:         fmt.Sprintf("cycle: %s — dependency direction loops [%s edge(s) along the cycle]", strings.Join(labels, " → "), strings.Join(counts, ", ")),
			EvidenceRefIDs: sampleRefs(cyc...),
		})
	}

	// Bands 7 and 5: per unordered pair, either a back-edge against the
	// dominant direction (light reverse traffic) or a high-coupling pair
	// (heavy traffic both ways — no clean layering possible). Mutually
	// exclusive by the highCouplingMin threshold.
	type pairRow struct {
		a, b, fwd, rev int // fwd = a→b count, rev = b→a count
	}
	var backEdges, coupled []pairRow
	seenPairs := map[pair]struct{}{}
	for p2 := range m.pairCount {
		a, b := p2.from, p2.to
		if a > b {
			a, b = b, a
		}
		up := pair{a, b}
		if _, ok := seenPairs[up]; ok {
			continue // one visit per unordered pair
		}
		seenPairs[up] = struct{}{}
		n := m.pairCount[up]
		rev := m.pairCount[pair{b, a}]
		weaker := min(n, rev)
		switch {
		case weaker >= highCouplingMin:
			coupled = append(coupled, pairRow{a, b, n, rev})
		case weaker > 0:
			// Orient the row so (a→b) is the dominant direction and rev the
			// violation. A tie below the coupling threshold carries no
			// direction signal and is not reported.
			if n == rev {
				continue
			}
			if n > rev {
				backEdges = append(backEdges, pairRow{a, b, n, rev})
			} else {
				backEdges = append(backEdges, pairRow{b, a, rev, n})
			}
		}
	}
	sort.Slice(backEdges, func(i, j int) bool {
		if backEdges[i].rev != backEdges[j].rev {
			return backEdges[i].rev > backEdges[j].rev
		}
		if backEdges[i].a != backEdges[j].a {
			return backEdges[i].a < backEdges[j].a
		}
		return backEdges[i].b < backEdges[j].b
	})
	for i, r := range backEdges {
		if i >= backEdgeRows {
			break
		}
		items = append(items, contract.Item{
			RefID:          fmt.Sprintf("backedge:%d-%d", r.b, r.a),
			Rank:           bandBackEdges<<20 + clampScore(r.rev),
			Reason:         fmt.Sprintf("unexpected dependency: %s → %s — %d edge(s) against the dominant direction (%s → %s, %d edge(s))", m.display(r.b), m.display(r.a), r.rev, m.label[r.a], m.label[r.b], r.fwd),
			EvidenceRefIDs: sampleRefs(r.b, r.a),
		})
	}
	sort.Slice(coupled, func(i, j int) bool {
		li, lj := min(coupled[i].fwd, coupled[i].rev), min(coupled[j].fwd, coupled[j].rev)
		if li != lj {
			return li > lj
		}
		if coupled[i].a != coupled[j].a {
			return coupled[i].a < coupled[j].a
		}
		return coupled[i].b < coupled[j].b
	})
	for i, r := range coupled {
		if i >= couplingRows {
			break
		}
		items = append(items, contract.Item{
			RefID:          fmt.Sprintf("coupling:%d-%d", r.a, r.b),
			Rank:           bandCoupling<<20 + clampScore(min(r.fwd, r.rev)),
			Reason:         fmt.Sprintf("high coupling: %s ↔ %s — %d ↔ %d edge(s) (both ≥ %d: no clean layering between them)", m.display(r.a), m.display(r.b), r.fwd, r.rev, highCouplingMin),
			EvidenceRefIDs: sampleRefs(r.a, r.b),
		})
	}

	// Band 3: god modules — a community that concentrates the inter-community
	// edge mass AND touches most of the partition.
	gods := 0
	if len(m.commIDs) >= godMinCommunities && m.interSum > 0 {
		for _, c := range m.commIDs {
			incident, touched := 0, map[int]struct{}{}
			for p2, n := range m.pairCount {
				if p2.from != c && p2.to != c {
					continue
				}
				incident += n
				other := p2.from
				if other == c {
					other = p2.to
				}
				touched[other] = struct{}{}
			}
			sharePct := incident * 100 / m.interSum
			touchPct := len(touched) * 100 / (len(m.commIDs) - 1)
			if sharePct < godEdgeSharePct || touchPct < godTouchPct {
				continue
			}
			gods++
			items = append(items, contract.Item{
				RefID:          fmt.Sprintf("god:%d", c),
				Rank:           bandGodModules<<20 + clampScore(sharePct),
				Reason:         fmt.Sprintf("god module: %s — touches %d/%d other communities (≥%d%%) and holds %d%% of all inter-community edges (≥%d%%)", m.display(c), len(touched), len(m.commIDs)-1, godTouchPct, sharePct, godEdgeSharePct),
				EvidenceRefIDs: sampleRefs(c),
			})
		}
	}

	// findings counts the DETECTED total per category — the item list may be
	// shorter (per-category row caps + the shape.Finish item cap), which
	// Limits already reports.
	findings := len(cycles) + len(backEdges) + len(coupled) + gods
	if findings == 0 {
		// Clean is a first-class, cited answer — not an empty shrug.
		items = append(items, contract.Item{
			RefID: "clean",
			Rank:  bandVIdentity << 20,
			Reason: fmt.Sprintf("clean: no cycles, no edges against a dominant direction, no pair with ≥%d edge(s) both ways, no god module — %d communities, %d inter-community edge(s) checked",
				highCouplingMin, len(m.commIDs), m.interSum),
		})
	}

	summary := fmt.Sprintf("architecture_violations: %d finding(s) — %d cycle(s), %d unexpected dependencies, %d high-coupling pair(s), %d god module(s) across %d communities (%s)",
		findings, len(cycles), len(backEdges), len(coupled), gods, len(m.commIDs), ViolationsMethodVersion)
	if findings == 0 {
		summary = fmt.Sprintf("architecture_violations: clean — %d communities, %d inter-community edge(s) checked (%s)",
			len(m.commIDs), m.interSum, ViolationsMethodVersion)
	}

	// Band 1: suggested next calls.
	items = append(items, contract.Item{
		RefID:  "next-1",
		Rank:   bandVNext<<20 + 1,
		Reason: "next: graphi architecture — the full community/layer view",
	})

	r := &contract.Result{
		Outcome:    contract.OutcomeFound,
		Summary:    summary,
		Items:      items,
		Evidence:   ev.List(),
		Confidence: m.tally.Confidence("unknown", "community_graph"),
	}
	return shape.Finish(r, p.maxItems())
}

// findCycles enumerates distinct dependency cycles on the dominant-direction
// graph, restricted to the cyclic remnant left by layer assignment. For each
// cyclic community (ascending) it takes the BFS-shortest loop back to itself
// (neighbors visited in ascending order), canonicalizes the rotation to start
// at the smallest member, and dedupes. Bounded by cycleCap.
func findCycles(m *archModel) [][]int {
	if len(m.cyclic) == 0 {
		return nil
	}
	inCycle := map[int]struct{}{}
	for _, c := range m.cyclic {
		inCycle[c] = struct{}{}
	}
	depsOf := map[int][]int{}
	for p, dom := range m.dominant {
		if !dom {
			continue
		}
		if _, ok := inCycle[p.from]; !ok {
			continue
		}
		if _, ok := inCycle[p.to]; !ok {
			continue
		}
		depsOf[p.from] = append(depsOf[p.from], p.to)
	}
	for _, deps := range depsOf {
		sort.Ints(deps)
	}

	seen := map[string]struct{}{}
	var out [][]int
	for _, start := range m.cyclic {
		if len(out) >= cycleCap {
			break
		}
		// BFS from start back to start over dominant edges.
		parent := map[int]int{}
		queue := append([]int{}, depsOf[start]...)
		for _, q := range queue {
			if _, ok := parent[q]; !ok {
				parent[q] = start
			}
		}
		var loop []int
		for len(queue) > 0 && loop == nil {
			cur := queue[0]
			queue = queue[1:]
			if cur == start {
				// Reconstruct start → … → start.
				path := []int{start}
				for at := parent[start]; at != start; at = parent[at] {
					path = append(path, at)
				}
				// path is reversed (start, pred(start), …); flip to walk order.
				for i, j := 1, len(path)-1; i < j; i, j = i+1, j-1 {
					path[i], path[j] = path[j], path[i]
				}
				loop = path
				break
			}
			for _, next := range depsOf[cur] {
				if _, ok := parent[next]; ok && next != start {
					continue
				}
				if _, ok := parent[next]; !ok {
					parent[next] = cur
				}
				queue = append(queue, next)
			}
		}
		if loop == nil {
			continue
		}
		canon := canonicalizeCycle(loop)
		key := fmt.Sprint(canon)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, canon)
	}
	return out
}

// canonicalizeCycle rotates the cycle so it starts at its smallest community
// id, making equal cycles compare equal regardless of discovery order.
func canonicalizeCycle(cyc []int) []int {
	best := 0
	for i, c := range cyc {
		if c < cyc[best] {
			best = i
		}
	}
	out := make([]int, 0, len(cyc))
	out = append(out, cyc[best:]...)
	out = append(out, cyc[:best]...)
	return out
}
