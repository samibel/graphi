// Package related implements the related_files agent tool: a deterministic,
// evidence-cited "what should I read first?" ranking backed by graph
// proximity. Files are ranked by summed edge confidence toward/from the
// resolved anchor, with an explicit per-file reason.
package related

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/shape"
	"github.com/samibel/graphi/engine/query"
)

const tool = "related_files"

// Direction values accepted by Files.
const (
	DirectionBoth         = "both"
	DirectionDependencies = "dependencies"
	DirectionDependents   = "dependents"
)

// fileScore accumulates one candidate file's relationship to the anchor.
type fileScore struct {
	path      string
	score     float64 // summed edge confidence
	inbound   int     // edges from this file INTO the anchor (dependent)
	outbound  int     // edges from the anchor INTO this file (dependency)
	kinds     map[string]int
	tally     shape.TierTally
	evidence  map[string]struct{} // raw "path:line" refs, bounded at shaping time
	symbolSet map[string]struct{} // related symbol names touched
}

// Files resolves anchor (free-text task, symbol, path, or node id) and returns
// a ranked, cited read-first file list in the C1 contract shape. direction
// filters to "dependencies" (files the anchor points at), "dependents" (files
// pointing at the anchor), or "both" (default).
func Files(ctx context.Context, deps resolve.Deps, anchor, direction string, maxFiles int) (*contract.Result, error) {
	if anchor == "" {
		return nil, errors.New("missing anchor")
	}
	switch direction {
	case "", DirectionBoth, DirectionDependencies, DirectionDependents:
	default:
		return nil, fmt.Errorf("invalid direction %q: want dependencies, dependents, or both", direction)
	}
	if direction == "" {
		direction = DirectionBoth
	}
	if !deps.Available() {
		return shape.Unavailable(tool), nil
	}

	res, err := resolve.Seeds(ctx, deps, anchor, 5)
	if err != nil {
		return nil, err
	}
	if res.Ambiguous() {
		return shape.Finish(shape.Ambiguous(tool, anchor, res.Candidates), maxFiles)
	}
	if !res.Resolved() {
		return shape.Empty(tool, anchor), nil
	}

	seedIDs := make(map[model.NodeId]struct{}, len(res.Nodes))
	seedFiles := make(map[string]struct{}, len(res.Nodes))
	for _, n := range res.Nodes {
		seedIDs[n.ID()] = struct{}{}
		seedFiles[n.SourcePath()] = struct{}{}
	}

	// CORE-02 (ADR 0003 D7): only the seeds' incident edges are read (one
	// Incoming+Outgoing pair per seed), never the whole edge set.
	reader := deps.Query.Reader()
	glk, ok := reader.(graphstore.GraphLookup)
	if !ok {
		return nil, query.ErrSelectiveLookupUnavailable
	}

	scores := map[string]*fileScore{}
	nodeCache := map[model.NodeId]*model.Node{}
	lookup := func(id model.NodeId) *model.Node {
		if n, ok := nodeCache[id]; ok {
			return n
		}
		ns, err := glk.NodesByID(ctx, []model.NodeId{id})
		if err != nil || len(ns) == 0 {
			nodeCache[id] = nil
			return nil
		}
		nodeCache[id] = &ns[0]
		return &ns[0]
	}
	record := func(other model.NodeId, e model.Edge, inbound bool) {
		n := lookup(other)
		if n == nil {
			return
		}
		path := n.SourcePath()
		if _, own := seedFiles[path]; own || path == "" {
			return
		}
		fs, ok := scores[path]
		if !ok {
			fs = &fileScore{
				path:      path,
				kinds:     map[string]int{},
				tally:     shape.TierTally{},
				evidence:  map[string]struct{}{},
				symbolSet: map[string]struct{}{},
			}
			scores[path] = fs
		}
		fs.score += e.Confidence()
		if inbound {
			fs.inbound++
		} else {
			fs.outbound++
		}
		fs.kinds[e.Kind()]++
		fs.tally.Count(e.Tier())
		for _, ref := range e.Evidence() {
			fs.evidence[ref] = struct{}{}
		}
		fs.symbolSet[n.QualifiedName()] = struct{}{}
	}

	// Gather the seeds' incident edges selectively, then replay record() in
	// canonical EdgeId order — the exact accumulation order the old full-scan
	// loop had — so the float score sums stay byte-identical.
	type match struct {
		other   model.NodeId
		edge    model.Edge
		inbound bool
	}
	var matches []match
	for _, n := range res.Nodes {
		if direction != DirectionDependents {
			out, err := glk.Outgoing(ctx, n.ID())
			if err != nil {
				return nil, err
			}
			for _, e := range out {
				if _, toSeed := seedIDs[e.To()]; toSeed {
					continue // seed-to-seed: neither dependency nor dependent
				}
				matches = append(matches, match{other: e.To(), edge: e, inbound: false}) // anchor → other: dependency
			}
		}
		if direction != DirectionDependencies {
			in, err := glk.Incoming(ctx, n.ID())
			if err != nil {
				return nil, err
			}
			for _, e := range in {
				if _, fromSeed := seedIDs[e.From()]; fromSeed {
					continue
				}
				matches = append(matches, match{other: e.From(), edge: e, inbound: true}) // other → anchor: dependent
			}
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].edge.ID() < matches[j].edge.ID() })
	for _, m := range matches {
		record(m.other, m.edge, m.inbound)
	}

	ranked := make([]*fileScore, 0, len(scores))
	for _, fs := range scores {
		ranked = append(ranked, fs)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		if ci, cj := ranked[i].inbound+ranked[i].outbound, ranked[j].inbound+ranked[j].outbound; ci != cj {
			return ci > cj
		}
		return ranked[i].path < ranked[j].path
	})

	ev := shape.NewEvidenceSet()
	tally := shape.TierTally{}
	items := make([]contract.Item, 0, len(ranked))
	for i, fs := range ranked {
		var evIDs []string
		for _, ref := range sortedBounded(fs.evidence, 3) {
			evIDs = append(evIDs, ev.AddRef(ref, "edge"))
		}
		for label, n := range fs.tally {
			tally[label] += n
		}
		items = append(items, contract.Item{
			RefID:          fs.path,
			Rank:           len(ranked) - i,
			Reason:         reasonFor(fs),
			EvidenceRefIDs: evIDs,
		})
	}

	seedNames := make([]string, 0, len(res.Nodes))
	for _, n := range res.Nodes {
		seedNames = append(seedNames, n.QualifiedName())
	}
	sort.Strings(seedNames)
	if len(seedNames) > 3 {
		seedNames = seedNames[:3]
	}

	r := &contract.Result{
		Outcome: contract.OutcomeFound,
		Summary: fmt.Sprintf("%d related files for %q (anchor: %s via %s, direction: %s)",
			len(ranked), anchor, strings.Join(seedNames, ", "), res.Method, direction),
		Items:      items,
		Evidence:   ev.List(),
		Confidence: tally.Confidence("unknown", "no_edges"),
	}
	if len(items) == 0 {
		r.Outcome = contract.OutcomeEmpty
		r.Summary = fmt.Sprintf("anchor %q resolved (%s) but has no %s edges to other files", anchor, res.Method, direction)
	}
	return shape.Finish(r, maxFiles)
}

// reasonFor renders the per-file relevance explanation the PRD requires.
func reasonFor(fs *fileScore) string {
	kinds := make([]string, 0, len(fs.kinds))
	for k := range fs.kinds {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	parts := make([]string, 0, len(kinds))
	for _, k := range kinds {
		parts = append(parts, fmt.Sprintf("%s×%d", k, fs.kinds[k]))
	}
	rel := make([]string, 0, 2)
	if fs.inbound > 0 {
		rel = append(rel, fmt.Sprintf("%d edges into anchor", fs.inbound))
	}
	if fs.outbound > 0 {
		rel = append(rel, fmt.Sprintf("%d edges from anchor", fs.outbound))
	}
	syms := sortedBounded(fs.symbolSet, 2)
	return fmt.Sprintf("%s (%s; symbols: %s)", strings.Join(rel, ", "), strings.Join(parts, ", "), strings.Join(syms, ", "))
}

// sortedBounded returns up to n sorted members of the set.
func sortedBounded(set map[string]struct{}, n int) []string {
	out := shape.SortStrings(set)
	if len(out) > n {
		out = out[:n]
	}
	return out
}
