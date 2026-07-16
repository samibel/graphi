package analysis

import (
	"container/heap"
	"context"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// DefaultMaxNodes is the documented output budget for impact reachability. A
// request with MaxNodes <= 0 uses this cap. MaxNodes also derives a finite work
// budget, so a small answer request cannot walk an arbitrarily large connected
// component before truncating its output.
const DefaultMaxNodes = 1024

// impactWorkMultiplier leaves enough headroom to rank more candidates than the
// output retains while still bounding node traversal work.
// impactEdgeWorkMultiplier independently caps incident edges read/evaluated, so
// one high-degree frontier cannot allocate its complete adjacency list before
// the node budget is applied. impactHydrationSlack tolerates missing/external
// endpoints without turning a frontier into an unbounded NodesByID request.
// Exhausting any budget is reported via Analysis.Truncated; partial work is
// never presented as complete.
const (
	impactWorkMultiplier     = 8
	impactEdgeWorkMultiplier = 16
	impactKindBudgetFactor   = 2
	impactMaxKinds           = 16
	impactHydrationSlack     = 32
)

// externalNodeKind is the interned external-symbol node kind (WP-03), excluded
// from impact reachability results. Mirrors core/parse.KindExternal.
const externalNodeKind = "external"

// dependencyKinds are the edge kinds traversed for impact when Params.Kinds is
// empty: the canonical calls/references vocabulary fixed by engine/query.
// `defines` is deliberately NOT a default kind — a file "defining" a symbol is
// containment, not dependency, and including it put a file node in every
// symbol's blast radius as depth-1 noise. Pass Kinds explicitly (e.g.
// -kinds calls,references,defines) to opt back in.
var dependencyKinds = []string{
	string(query.EdgeKindCalls),
	string(query.EdgeKindReferences),
}

// impactAnalyzer computes reverse (downstream dependents / blast radius) and
// forward (upstream dependencies) transitive reachability over the read-only
// graph. It is the reference analyzer that established the engine/analysis
// package and registry (SW-022); sibling EP-004 analyzers (call-chain SW-023,
// concept SW-024, metrics SW-025) are registered against the same contract.
type impactAnalyzer struct{}

func (impactAnalyzer) Name() string { return "impact" }

// Analyze resolves the seed and runs a cycle-guarded bounded traversal in the
// requested direction, attaching the best-tier reaching edge to every reached
// node, then materializes-sorts-truncates for a deterministic, ranked result.
// CORE-02 (ADR 0003 D7): the traversal expands each frontier node through the
// endpoint-selective port, so cost scales with the reached component's degree
// sum, never with the whole edge set.
func (a impactAnalyzer) Analyze(ctx context.Context, r query.Reader, p Params) (Analysis, error) {
	const op = "impact"

	lookup, ok := r.(graphstore.GraphLookup)
	if !ok {
		return Analysis{}, query.ErrSelectiveLookupUnavailable
	}
	boundedLookup, ok := r.(graphstore.BoundedGraphLookup)
	if !ok {
		return Analysis{}, query.ErrSelectiveLookupUnavailable
	}

	// Resolve the seed. A missing symbol is an explicit not-found result, NEVER
	// an error (parity with query.Service.resolve). The point read goes through
	// the port (bounded) rather than Reader.GetNode (whole-graph cache on SQLite).
	if seed, err := lookup.NodesByID(ctx, []model.NodeId{p.Symbol}); err != nil {
		return Analysis{}, err
	} else if len(seed) == 0 {
		return notFound(op, p.Symbol), nil
	}

	// Default direction is Reverse (dependents / blast radius): "impact of X"
	// colloquially means "who is affected if X changes", and it is what the TUI
	// blast panel and the edit planner need. The engine owns this default;
	// surfaces pass the direction through verbatim (empty = this default).
	direction := p.Direction
	if direction == "" {
		direction = Reverse
	}

	max := p.MaxNodes
	if max <= 0 {
		max = DefaultMaxNodes
	}
	maxInt := int(^uint(0) >> 1)
	workLimit := maxInt
	if max <= maxInt/impactWorkMultiplier {
		workLimit = max * impactWorkMultiplier
	}
	edgeWorkLimit := maxInt
	if max <= maxInt/impactEdgeWorkMultiplier {
		edgeWorkLimit = max * impactEdgeWorkMultiplier
	}

	// Kinds is untrusted surface input. SQLite's sparse-safe bounded contract
	// performs one limit-capped index probe per distinct kind, so accepting an
	// arbitrary list would turn one MaxNodes=1 request into thousands of DB
	// probes. Bound distinct kinds by both MaxNodes and a hard ceiling; omitting
	// requested kinds is always surfaced as a partial result.
	kindLimit := impactMaxKinds
	if max <= maxInt/impactKindBudgetFactor && max*impactKindBudgetFactor < kindLimit {
		kindLimit = max * impactKindBudgetFactor
	}
	var (
		kinds          []string
		kindsTruncated bool
	)
	if len(p.Kinds) == 0 {
		kinds = append([]string(nil), dependencyKinds...)
	} else {
		// Select only the canonical smallest kindLimit+1 distinct values while
		// scanning the untrusted input. Memory stays O(kindLimit), not O(input),
		// even when a maximum-size transport request contains hundreds of
		// thousands of kind strings.
		kinds, kindsTruncated = canonicalImpactKinds(p.Kinds, kindLimit)
	}

	// BFS with per-node selective expansion. For each frontier node:
	//   Reverse (dependents / blast radius)   : neighbors are the FROM endpoints
	//     of its INCOMING edges (everything pointing AT it) — the
	//     reverse-dependency (rdeps) convention.
	//   Forward (dependencies)                : neighbors are the TO endpoints of
	//     its OUTGOING edges (everything it points at) — traversal ALONG edge
	//     direction.
	// Cycle guard: expand each node at most once. The seed is excluded from its
	// own result (a node is neither its own dependent nor its own dependency).
	// Work is bounded independently of graph size. Up to 8× MaxNodes are visited
	// and at most 16× MaxNodes returned incident edges are evaluated. Distinct
	// kind probes are capped at min(2× MaxNodes, 16); each backend probe reads
	// at most limit+1 rows per retained kind. Thus neither endpoint degree nor an
	// attacker-sized kind list can force an unbounded adjacency allocation or DB
	// query fan-out. Ranking can
	// choose among more candidates than it emits without a high-degree node
	// forcing an adjacency-sized allocation. Once either budget (or the bounded
	// hydration window) is exhausted the result is explicitly partial via
	// Analysis.Truncated.
	type nbr struct {
		id    model.NodeId
		via   query.ResultEdge
		depth int
	}
	reached := make(map[model.NodeId]ReachedNode)
	depth := map[model.NodeId]int{p.Symbol: 0}
	expanded := map[model.NodeId]struct{}{}
	frontier := []model.NodeId{p.Symbol}
	workTruncated := kindsTruncated
	edgesExamined := 0

	for len(frontier) > 0 && len(reached) < workLimit {
		// Gather this hop's neighbor candidates via endpoint-selective reads.
		candidateByID := map[model.NodeId]nbr{}
		var next []model.NodeId
		for _, cur := range frontier {
			if _, done := expanded[cur]; done {
				continue
			}
			expanded[cur] = struct{}{}
			remainingEdges := edgeWorkLimit - edgesExamined
			if remainingEdges <= 0 {
				workTruncated = true
				break
			}
			var (
				edges             []model.Edge
				incidentTruncated bool
				err               error
			)
			if direction == Forward {
				edges, incidentTruncated, err = boundedLookup.OutgoingBounded(ctx, cur, remainingEdges, kinds...)
			} else {
				edges, incidentTruncated, err = boundedLookup.IncomingBounded(ctx, cur, remainingEdges, kinds...)
			}
			if err != nil {
				return Analysis{}, err
			}
			edgesExamined += len(edges)
			if incidentTruncated {
				workTruncated = true
			}
			for _, e := range edges {
				id := e.From()
				if direction == Forward {
					id = e.To()
				}
				if id == p.Symbol {
					continue
				}
				via := edgeToResult(e)
				if existing, seen := reached[id]; seen {
					// Keep improving provenance even after first discovery.
					if edgeBetter(via, existing.ReachedVia) {
						existing.ReachedVia = via
						reached[id] = existing
					}
					continue
				}
				candidate := nbr{id: id, via: via, depth: depth[cur] + 1}
				if previous, exists := candidateByID[id]; !exists || edgeBetter(via, previous.via) {
					candidateByID[id] = candidate
				}
			}
			if incidentTruncated {
				break
			}
		}

		// Rank this layer before applying its work window. That keeps the most
		// trustworthy candidate edges instead of accepting map/traversal order.
		cands := make([]nbr, 0, len(candidateByID))
		for _, candidate := range candidateByID {
			cands = append(cands, candidate)
		}
		sort.Slice(cands, func(i, j int) bool {
			if edgeBetter(cands[i].via, cands[j].via) {
				return true
			}
			if edgeBetter(cands[j].via, cands[i].via) {
				return false
			}
			return cands[i].id < cands[j].id
		})
		remaining := workLimit - len(reached)
		hydrationLimit := remaining
		if hydrationLimit <= maxInt-impactHydrationSlack {
			hydrationLimit += impactHydrationSlack
		}
		if len(cands) > hydrationLimit {
			cands = cands[:hydrationLimit]
			workTruncated = true
		}

		// Hydrate only the bounded candidate window. NodesByID skips no-longer-
		// existing endpoints (referential drift), matching the previous behavior.
		fetched := map[model.NodeId]model.Node{}
		if len(cands) > 0 {
			ids := make([]model.NodeId, 0, len(cands))
			for _, candidate := range cands {
				ids = append(ids, candidate.id)
			}
			ns, err := lookup.NodesByID(ctx, ids)
			if err != nil {
				return Analysis{}, err
			}
			for _, n := range ns {
				fetched[n.ID()] = n
			}
		}

		for _, nb := range cands {
			n, ok := fetched[nb.id]
			if !ok {
				continue // referential drift: endpoint no longer exists
			}
			// WP-03 query hygiene: interned external nodes are terminal heuristic
			// linker artifacts (stdlib / 3rd-party targets), not part of the
			// dependency graph a user reasons about — exclude them from the blast
			// radius. They have no outgoing edges, so not enqueuing them also stops
			// traversal cleanly.
			if n.Kind() == externalNodeKind {
				continue
			}
			if len(reached) >= workLimit {
				workTruncated = true
				break
			}
			depth[nb.id] = nb.depth
			reached[nb.id] = ReachedNode{
				Node:       nodeToResult(n),
				ReachedVia: nb.via,
				Depth:      nb.depth,
			}
			next = append(next, nb.id)
		}
		frontier = next
		if workTruncated {
			break
		}
	}
	if len(frontier) > 0 && len(reached) >= workLimit {
		workTruncated = true
	}

	outcome := query.OutcomeFound
	if len(reached) == 0 {
		outcome = query.OutcomeEmpty
	}

	nodes := make([]ReachedNode, 0, len(reached))
	for _, rn := range reached {
		nodes = append(nodes, rn)
	}
	sortReached(nodes)

	truncated := workTruncated
	if len(nodes) > max {
		nodes = nodes[:max]
		truncated = true
	}

	return Analysis{
		Analyzer:  op,
		Outcome:   outcome,
		Symbol:    p.Symbol,
		Truncated: truncated,
		Nodes:     nodes,
	}, nil
}

func canonicalImpactKinds(kinds []string, limit int) ([]string, bool) {
	selector := newImpactKindSelector(limit)
	for _, kind := range kinds {
		selector.add(kind)
	}
	return selector.result(limit)
}

// impactKindSelector retains only the lexicographically smallest limit+1
// distinct values in a max-heap. The extra value is the exact truncation proof;
// larger values can then be discarded immediately. kept mirrors only heap
// membership, so both structures remain independently bounded by limit+1.
type impactKindSelector struct {
	capacity int
	values   maxStringHeap
	kept     map[string]struct{}
}

func newImpactKindSelector(limit int) *impactKindSelector {
	capacity := 0
	if limit >= 0 && limit < int(^uint(0)>>1) {
		capacity = limit + 1
	}
	return &impactKindSelector{
		capacity: capacity,
		kept:     make(map[string]struct{}, capacity),
	}
}

func (s *impactKindSelector) add(value string) {
	if s.capacity == 0 {
		return
	}
	if _, exists := s.kept[value]; exists {
		return
	}
	if len(s.values) < s.capacity {
		heap.Push(&s.values, value)
		s.kept[value] = struct{}{}
		return
	}
	if value >= s.values[0] {
		return
	}
	largest := heap.Pop(&s.values).(string)
	delete(s.kept, largest)
	heap.Push(&s.values, value)
	s.kept[value] = struct{}{}
}

func (s *impactKindSelector) result(limit int) ([]string, bool) {
	out := append([]string(nil), s.values...)
	sort.Strings(out)
	truncated := len(out) > limit
	if truncated {
		out = out[:limit]
	}
	return out, truncated
}

type maxStringHeap []string

func (h maxStringHeap) Len() int           { return len(h) }
func (h maxStringHeap) Less(i, j int) bool { return h[i] > h[j] }
func (h maxStringHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *maxStringHeap) Push(value any)    { *h = append(*h, value.(string)) }
func (h *maxStringHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}
