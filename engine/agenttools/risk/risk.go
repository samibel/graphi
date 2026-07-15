// Package risk implements the change_risk agent tool: an evidence-based local
// blast-radius estimate for changing a symbol, file, or diff. It reports an
// explicit risk level (low/medium/high/unknown), states whether the target
// resolved exactly or heuristically, and prefers "unknown" over guessing.
package risk

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

const tool = "change_risk"

// Level is the closed risk vocabulary.
type Level string

const (
	LevelLow     Level = "low"
	LevelMedium  Level = "medium"
	LevelHigh    Level = "high"
	LevelUnknown Level = "unknown"
)

// Thresholds for the evidence-based level. Deliberately simple and documented:
// fan-in (inbound calls+references) and the number of distinct dependent files
// drive the level; anything unresolved is unknown.
const (
	lowMaxFanIn          = 2
	lowMaxDependentFiles = 2
	highMinFanIn         = 20
	highMinDependents    = 8
)

// Assess estimates the local risk of changing target (node id, path, or symbol
// query) or the files named by a unified diff. Exactly one of target/diff may
// be empty.
func Assess(ctx context.Context, deps resolve.Deps, target, diff string, maxItems int) (*contract.Result, error) {
	if target == "" && diff == "" {
		return nil, errors.New("missing target or diff")
	}
	if !deps.Available() {
		return shape.Unavailable(tool), nil
	}

	var (
		res resolve.Resolution
		err error
	)
	if diff != "" {
		res, err = resolveDiff(ctx, deps, diff)
	} else {
		res, err = resolve.Strict(ctx, deps, target)
	}
	if err != nil {
		return nil, err
	}
	if res.Ambiguous() {
		return shape.Finish(shape.Ambiguous(tool, target, res.Candidates), maxItems)
	}
	if !res.Resolved() {
		r := shape.Empty(tool, firstNonEmpty(target, "diff paths"))
		r.Summary = "risk: unknown — " + r.Summary
		return r, nil
	}

	seedIDs := make(map[model.NodeId]struct{}, len(res.Nodes))
	seedFiles := make(map[string]struct{}, len(res.Nodes))
	for _, n := range res.Nodes {
		seedIDs[n.ID()] = struct{}{}
		seedFiles[n.SourcePath()] = struct{}{}
	}

	// CORE-02 (ADR 0003 D7): only the seeds' incident edges are read (one
	// Incoming+Outgoing pair per seed), never the whole edge set. All the
	// aggregates below are order-independent (integer counts, sets, a tier
	// tally); inboundEdges is explicitly re-sorted before shaping.
	reader := deps.Query.Reader()
	glk, ok := reader.(graphstore.GraphLookup)
	if !ok {
		return nil, query.ErrSelectiveLookupUnavailable
	}

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

	var (
		fanIn, fanOut  int
		callsIn        int
		dependentFiles = map[string]struct{}{}
		tally          = shape.TierTally{}
		inboundEdges   []model.Edge
	)
	for _, seed := range res.Nodes {
		in, err := glk.Incoming(ctx, seed.ID())
		if err != nil {
			return nil, err
		}
		for _, e := range in {
			if _, fromSeed := seedIDs[e.From()]; fromSeed {
				continue // seed-to-seed edges count neither as fan-in nor fan-out
			}
			fanIn++
			if e.Kind() == "calls" {
				callsIn++
			}
			tally.Count(e.Tier())
			inboundEdges = append(inboundEdges, e)
			if n := lookup(e.From()); n != nil {
				if _, own := seedFiles[n.SourcePath()]; !own && n.SourcePath() != "" {
					dependentFiles[n.SourcePath()] = struct{}{}
				}
			}
		}
		out, err := glk.Outgoing(ctx, seed.ID())
		if err != nil {
			return nil, err
		}
		for _, e := range out {
			if _, toSeed := seedIDs[e.To()]; toSeed {
				continue
			}
			fanOut++
		}
	}

	level := LevelMedium
	switch {
	case fanIn > highMinFanIn || len(dependentFiles) > highMinDependents:
		level = LevelHigh
	case fanIn <= lowMaxFanIn && len(dependentFiles) <= lowMaxDependentFiles:
		level = LevelLow
	}

	testFiles := 0
	for f := range dependentFiles {
		if isTestPath(f) {
			testFiles++
		}
	}
	testHint := "no test files among dependents"
	if testFiles > 0 {
		testHint = fmt.Sprintf("%d dependent test file(s)", testFiles)
	}

	resolution := "heuristically"
	if res.Method.Exact() {
		resolution = "exactly"
	}

	ev := shape.NewEvidenceSet()
	items := make([]contract.Item, 0, len(inboundEdges)+1)
	for _, n := range res.Nodes {
		evID := ev.Add(n.SourcePath(), n.Line(), "target")
		items = append(items, contract.Item{
			RefID:          string(n.ID()),
			Rank:           1 << 20,
			Reason:         fmt.Sprintf("target: %s %s (%s:%d)", n.Kind(), n.QualifiedName(), n.SourcePath(), n.Line()),
			EvidenceRefIDs: []string{evID},
		})
	}
	sort.Slice(inboundEdges, func(i, j int) bool {
		if inboundEdges[i].Confidence() != inboundEdges[j].Confidence() {
			return inboundEdges[i].Confidence() > inboundEdges[j].Confidence()
		}
		return inboundEdges[i].ID() < inboundEdges[j].ID()
	})
	for i, e := range inboundEdges {
		n := lookup(e.From())
		if n == nil {
			continue
		}
		var evIDs []string
		for _, ref := range e.Evidence() {
			evIDs = append(evIDs, ev.AddRef(ref, "inbound"))
		}
		items = append(items, contract.Item{
			RefID:          string(n.ID()),
			Rank:           len(inboundEdges) - i,
			Reason:         fmt.Sprintf("affected %s: %s %s (%s:%d) [%s]", e.Kind(), n.Kind(), n.QualifiedName(), n.SourcePath(), n.Line(), e.Tier()),
			EvidenceRefIDs: evIDs,
		})
	}

	r := &contract.Result{
		Outcome: contract.OutcomeFound,
		Summary: fmt.Sprintf("risk: %s — fan-in %d (%d calls) from %d file(s), fan-out %d; target resolved %s (%s); %s",
			level, fanIn, callsIn, len(dependentFiles), fanOut, resolution, res.Method, testHint),
		Items:      items,
		Evidence:   ev.List(),
		Confidence: tally.Confidence("unknown", "no_inbound_edges"),
	}
	return shape.Finish(r, maxItems)
}

// resolveDiff extracts changed file paths from a unified diff and resolves
// every graph node in those files as the change target. CORE-02 (ADR 0003 D7):
// one selective SourcePath lookup per changed file, never a full node scan;
// the merged result is re-sorted to the canonical NodeId order the old
// full-scan filter produced.
func resolveDiff(ctx context.Context, deps resolve.Deps, diff string) (resolve.Resolution, error) {
	paths := DiffPaths(diff)
	if len(paths) == 0 {
		return resolve.Resolution{}, nil
	}
	symbols, ok := deps.Query.Reader().(graphstore.SymbolLookupPort)
	if !ok {
		return resolve.Resolution{}, query.ErrSelectiveLookupUnavailable
	}
	pathSet := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		pathSet[model.NormalizePath(p)] = struct{}{}
	}
	var nodes []model.Node
	for p := range pathSet {
		ns, err := symbols.SourcePath(ctx, p)
		if err != nil {
			return resolve.Resolution{}, err
		}
		nodes = append(nodes, ns...)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID() < nodes[j].ID() })
	return resolve.Resolution{Method: resolve.MethodDiff, Nodes: nodes}, nil
}

// DiffPaths extracts the changed file paths from unified-diff text ("+++ b/…"
// and "--- a/…" headers), deduplicated and sorted. /dev/null is skipped.
func DiffPaths(diff string) []string {
	set := map[string]struct{}{}
	for _, line := range strings.Split(diff, "\n") {
		var p string
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			p = strings.TrimPrefix(line, "+++ b/")
		case strings.HasPrefix(line, "--- a/"):
			p = strings.TrimPrefix(line, "--- a/")
		case strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "--- "):
			p = strings.TrimSpace(line[4:])
		default:
			continue
		}
		p = strings.TrimSpace(p)
		if p == "" || p == "/dev/null" {
			continue
		}
		set[p] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// isTestPath reports whether a path looks like test code (mirrors the
// diagnostic suppression defaults).
func isTestPath(p string) bool {
	base := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		base = p[i+1:]
	}
	return strings.Contains(base, "_test.") || strings.Contains(base, ".test.") ||
		strings.Contains(p, "/test/") || strings.Contains(p, "/tests/") || strings.Contains(p, "/testdata/")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
