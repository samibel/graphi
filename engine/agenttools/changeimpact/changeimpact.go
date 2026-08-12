// Package changeimpact implements the change_impact agent tool (labs) — the
// "Change Risk 2.0" assessment. Given a unified diff or a symbol/file target,
// one call returns: the changed symbols, the public-API subset, direct and
// transitive dependents (bounded walks), the tests covering the change (via
// engine/testintel), configuration files among the changes, explicit reasons,
// and a risk level.
//
// The frozen-stable change_risk operation is untouched: this is a SEPARATE
// labs operation, so the GA envelope keeps its bytes while the richer
// assessment iterates in labs.
//
// The confidence distribution is DERIVED from evidence (the tier mix of every
// consumed edge — the C1 envelope's standard method), never invented: an
// assessment built on confirmed edges says so, one built on heuristics says
// that instead.
package changeimpact

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/risk"
	"github.com/samibel/graphi/engine/agenttools/shape"
	"github.com/samibel/graphi/engine/analysis/githistory"
	pathclass "github.com/samibel/graphi/engine/classify"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/testintel"
)

const tool = "change_impact"

// MethodVersion stamps the assembly logic version into the summary.
const MethodVersion = "change_impact/1"

// Rank bands (rank = band<<20 + score, score < 1<<20).
const (
	bandChanged    = 10
	bandPublicAPI  = 9
	bandDependents = 8
	bandTransitive = 7
	bandTests      = 6
	bandCoChange   = 5
	bandConfigs    = 4
	bandReasons    = 3
	bandRisk       = 2
)

// Bounds and section caps.
const (
	DefaultDepth   = 2
	MaxDepth       = 3
	edgesPerNode   = 64
	walkNodeBudget = 512
	changedRows    = 12
	dependentRows  = 10
	testRows       = 8
	reasonRows     = 8
)

// Params carries the change_impact inputs. Exactly one of Target/Diff must be
// non-empty (change_risk precedent).
type Params struct {
	Target string
	// Diff is a unified diff; a git range is served by piping
	// `git diff <range>` into the CLI.
	Diff string
	// Depth bounds the transitive walk; 0 selects DefaultDepth, clamp [1, MaxDepth].
	Depth int
	// MaxItems caps the item list (0 selects shape.DefaultMaxItems).
	MaxItems int
	Deps     resolve.Deps
	// Provider is the optional surface-boundary git-history source for the
	// co-change section ("you changed A, but B usually changes with it").
	// Nil skips the section.
	Provider githistory.GitProvider
	// Now overrides the history window's reference time (zero = wall clock;
	// tests pass a fixed time for byte determinism).
	Now time.Time
}

func (p Params) depth() int {
	d := p.Depth
	if d == 0 {
		d = DefaultDepth
	}
	if d < 1 {
		d = 1
	}
	if d > MaxDepth {
		d = MaxDepth
	}
	return d
}

// Assemble computes the Change Risk 2.0 assessment.
func Assemble(ctx context.Context, p Params) (*contract.Result, error) {
	if p.Target == "" && p.Diff == "" {
		return nil, errors.New("missing target or diff")
	}
	if p.Target != "" && p.Diff != "" {
		return nil, errors.New("pass either target or diff, not both")
	}
	if !p.Deps.Available() {
		return shape.Unavailable(tool), nil
	}

	subjects, unresolved, truncated, diffPaths, err := resolveSubjects(ctx, p)
	if err != nil {
		return nil, err
	}
	if len(subjects) == 0 && len(unresolved) == 0 && len(diffPaths) == 0 {
		return shape.Empty(tool, firstNonEmpty(p.Target, "diff")), nil
	}

	bounded, bok := p.Deps.Query.Reader().(graphstore.BoundedGraphLookup)
	lookup, lok := p.Deps.Query.Reader().(graphstore.GraphLookup)
	if !bok || !lok {
		return shape.Unavailable(tool), nil
	}

	subs := append([]model.Node{}, subjects...)
	sort.Slice(subs, func(i, j int) bool { return subs[i].ID() < subs[j].ID() })
	subjectIDs := map[model.NodeId]struct{}{}
	subjectFiles := map[string]struct{}{}
	for _, s := range subs {
		subjectIDs[s.ID()] = struct{}{}
		if s.SourcePath() != "" {
			subjectFiles[s.SourcePath()] = struct{}{}
		}
	}

	ev := shape.NewEvidenceSet()
	tally := shape.TierTally{}
	var items []contract.Item

	// Band 9: the changed symbols; band 8: their public-API subset.
	publicAPI := 0
	for i, s := range subs {
		evID := ev.Add(s.SourcePath(), s.Line(), "changed")
		if i < changedRows {
			items = append(items, contract.Item{
				RefID:          string(s.ID()),
				Rank:           bandChanged<<20 + (changedRows - i),
				Reason:         fmt.Sprintf("changed: %s %s (%s:%d)", s.Kind(), s.QualifiedName(), s.SourcePath(), s.Line()),
				EvidenceRefIDs: []string{evID},
			})
		}
		if isExportedName(s.QualifiedName()) {
			publicAPI++
			if publicAPI <= changedRows {
				items = append(items, contract.Item{
					RefID:          "api:" + string(s.ID()),
					Rank:           bandPublicAPI<<20 + (changedRows - publicAPI),
					Reason:         fmt.Sprintf("public_api: %s %s (%s:%d) — exported surface changed", s.Kind(), s.QualifiedName(), s.SourcePath(), s.Line()),
					EvidenceRefIDs: []string{evID},
				})
			}
		}
	}

	// Bands 7 and 6: direct dependents (hop 1, all kinds) and the transitive
	// closure (bounded BFS over calls/references).
	fanIn := 0
	directDependents := map[model.NodeId]model.Edge{}
	dependentFiles := map[string]struct{}{}
	visited := map[model.NodeId]struct{}{}
	for id := range subjectIDs {
		visited[id] = struct{}{}
	}
	frontier := append([]model.NodeId{}, idsOf(subs)...)
	transitive := 0
	for d := 1; d <= p.depth() && len(frontier) > 0; d++ {
		var hopEdges []model.Edge
		for _, id := range frontier {
			var (
				edges []model.Edge
				trunc bool
				err   error
			)
			if d == 1 {
				edges, trunc, err = bounded.IncomingBounded(ctx, id, edgesPerNode)
			} else {
				edges, trunc, err = bounded.IncomingBounded(ctx, id, edgesPerNode, query.EdgeKindCalls, query.EdgeKindReferences)
			}
			if err != nil {
				return nil, err
			}
			truncated = truncated || trunc
			hopEdges = append(hopEdges, edges...)
		}
		sort.Slice(hopEdges, func(i, j int) bool { return hopEdges[i].ID() < hopEdges[j].ID() })

		var neighborIDs []model.NodeId
		seen := map[model.NodeId]struct{}{}
		for _, e := range hopEdges {
			if _, dup := seen[e.From()]; !dup {
				seen[e.From()] = struct{}{}
				neighborIDs = append(neighborIDs, e.From())
			}
		}
		neighbors, err := lookup.NodesByID(ctx, neighborIDs)
		if err != nil {
			return nil, err
		}
		nodeByID := make(map[model.NodeId]model.Node, len(neighbors))
		for _, n := range neighbors {
			nodeByID[n.ID()] = n
		}

		var next []model.NodeId
		for _, e := range hopEdges {
			from := e.From()
			if _, isSubject := subjectIDs[from]; isSubject {
				continue
			}
			tally.Count(e.Tier())
			n, hydrated := nodeByID[from]
			if d == 1 {
				fanIn++
				if _, dup := directDependents[from]; !dup {
					directDependents[from] = e
				}
				if hydrated && n.SourcePath() != "" {
					if _, own := subjectFiles[n.SourcePath()]; !own {
						dependentFiles[n.SourcePath()] = struct{}{}
					}
				}
			}
			if !hydrated || n.SourcePath() == "" {
				continue
			}
			if _, done := visited[from]; done {
				continue
			}
			if len(visited) >= walkNodeBudget {
				truncated = true
				continue
			}
			visited[from] = struct{}{}
			if d > 1 {
				transitive++
			}
			next = append(next, from)
		}
		frontier = next
	}

	// Emit the top direct dependents (edge id order is canonical).
	depIDs := make([]model.NodeId, 0, len(directDependents))
	for id := range directDependents {
		depIDs = append(depIDs, id)
	}
	sort.Slice(depIDs, func(i, j int) bool { return depIDs[i] < depIDs[j] })
	depNodes, err := lookup.NodesByID(ctx, depIDs)
	if err != nil {
		return nil, err
	}
	depByID := make(map[model.NodeId]model.Node, len(depNodes))
	for _, n := range depNodes {
		depByID[n.ID()] = n
	}
	emitted := 0
	for _, id := range depIDs {
		if emitted >= dependentRows {
			break
		}
		n, ok := depByID[id]
		if !ok {
			continue
		}
		e := directDependents[id]
		emitted++
		var evIDs []string
		for _, ref := range e.Evidence() {
			evIDs = append(evIDs, ev.AddRef(ref, "dependent"))
		}
		items = append(items, contract.Item{
			RefID:          string(id),
			Rank:           bandDependents<<20 + int(e.Confidence()*1000),
			Reason:         fmt.Sprintf("dependent: %s %s (%s:%d) [%s %s]", n.Kind(), n.QualifiedName(), n.SourcePath(), n.Line(), e.Kind(), e.Tier()),
			EvidenceRefIDs: evIDs,
		})
	}
	if transitive > 0 {
		items = append(items, contract.Item{
			RefID:  "transitive",
			Rank:   bandTransitive << 20,
			Reason: fmt.Sprintf("transitive: %d further dependent symbol(s) within depth %d", transitive, p.depth()),
		})
	}

	// Band 5: the tests covering the change (shared testintel mapping).
	mapping := testintel.Result{}
	if len(subs) > 0 {
		mapping, err = testintel.TestsFor(ctx, p.Deps.Query, subs, p.depth())
		if err != nil {
			return nil, err
		}
		truncated = truncated || mapping.Truncated
	}
	directlyTested := map[model.NodeId]struct{}{}
	testEmitted := 0
	testTotal := 0
	seenTest := map[model.NodeId]struct{}{}
	for _, l := range mapping.Links {
		tally.Count(l.Tier)
		if l.Signal == testintel.SignalDirectCall && l.Tier != model.TierHeuristic {
			directlyTested[l.Subject] = struct{}{}
		}
		if _, dup := seenTest[l.Test.ID]; dup {
			continue
		}
		seenTest[l.Test.ID] = struct{}{}
		testTotal++
		if testEmitted >= testRows {
			continue
		}
		testEmitted++
		var evIDs []string
		for _, ref := range l.Evidence {
			evIDs = append(evIDs, ev.AddRef(ref, string(l.Signal)))
		}
		items = append(items, contract.Item{
			RefID:          string(l.Test.ID),
			Rank:           bandTests<<20 + (testRows - testEmitted),
			Reason:         fmt.Sprintf("test: %s %s (%s:%d) [%s]", l.Test.Kind, l.Test.QualifiedName, l.Test.SourcePath, l.Test.Line, l.Signal),
			EvidenceRefIDs: evIDs,
		})
	}

	// Band 5: co-change partners from bounded git history — files that
	// usually change together with the changed files but are NOT in this
	// change. The strongest "you probably forgot B" signal git can give.
	coChangePartners := 0
	var coChangeLead string
	if p.Provider != nil {
		changedFiles := map[string]struct{}{}
		for _, path := range diffPaths {
			changedFiles[model.NormalizePath(path)] = struct{}{}
		}
		for f := range subjectFiles {
			changedFiles[f] = struct{}{}
		}
		if len(changedFiles) > 0 {
			hist, err := githistory.New(p.Provider, githistory.Config{Now: p.Now}).Run(ctx)
			if err != nil {
				return nil, err
			}
			type partner struct {
				path string
				with string
				co   int
			}
			best := map[string]partner{}
			for _, g := range hist.CoChangeGroups {
				if len(g.Files) != 2 {
					continue
				}
				a, b := g.Files[0], g.Files[1]
				_, aIn := changedFiles[a]
				_, bIn := changedFiles[b]
				if aIn == bIn {
					continue // both changed (nothing forgotten) or both untouched
				}
				changed, other := a, b
				if bIn {
					changed, other = b, a
				}
				if cur, ok := best[other]; !ok || g.CoCommits > cur.co || (g.CoCommits == cur.co && changed < cur.with) {
					best[other] = partner{path: other, with: changed, co: g.CoCommits}
				}
			}
			partners := make([]partner, 0, len(best))
			for _, pt := range best {
				partners = append(partners, pt)
			}
			sort.Slice(partners, func(i, j int) bool {
				if partners[i].co != partners[j].co {
					return partners[i].co > partners[j].co
				}
				return partners[i].path < partners[j].path
			})
			for i, pt := range partners {
				if i >= testRows {
					break
				}
				coChangePartners++
				if coChangeLead == "" {
					coChangeLead = pt.path
				}
				evID := ev.Add(pt.path, 0, "co_change")
				score := pt.co
				if score >= 1<<20 {
					score = 1<<20 - 1
				}
				items = append(items, contract.Item{
					RefID:          "co:" + pt.path,
					Rank:           bandCoChange<<20 + score,
					Reason:         fmt.Sprintf("co_change: %s usually changes with %s (%d co-commit(s) in window) — not in this change", pt.path, pt.with, pt.co),
					EvidenceRefIDs: []string{evID},
				})
			}
		}
	}

	// Band 4: configuration files among the diff paths.
	configCount := 0
	for _, path := range diffPaths {
		if !pathclass.IsConfigPath(path) {
			continue
		}
		configCount++
		if configCount > testRows {
			continue
		}
		evID := ev.Add(path, 0, "config")
		items = append(items, contract.Item{
			RefID:          "config:" + path,
			Rank:           bandConfigs<<20 + 1,
			Reason:         fmt.Sprintf("config: %s — configuration change rides this diff", path),
			EvidenceRefIDs: []string{evID},
		})
	}

	// Band 3: explicit reasons (the roadmap's "reasons" list, deterministic).
	var reasons []string
	if publicAPI > 0 {
		reasons = append(reasons, fmt.Sprintf("public interface changed (%d exported symbol(s))", publicAPI))
	}
	reasons = append(reasons, fmt.Sprintf("%d direct and %d transitive dependent(s) across %d file(s)", len(directDependents), transitive, len(dependentFiles)))
	untested := 0
	for _, s := range subs {
		if _, ok := directlyTested[s.ID()]; ok {
			continue
		}
		untested++
		if untested <= 3 {
			reasons = append(reasons, fmt.Sprintf("no test directly covers %s", s.QualifiedName()))
		}
	}
	if untested > 3 {
		reasons = append(reasons, fmt.Sprintf("%d further changed symbol(s) without a direct test", untested-3))
	}
	if coChangePartners > 0 {
		reasons = append(reasons, fmt.Sprintf("%d file(s) usually change with this change but are untouched (e.g. %s)", coChangePartners, coChangeLead))
	}
	if configCount > 0 {
		reasons = append(reasons, fmt.Sprintf("%d configuration file(s) in the change", configCount))
	}
	for i, reason := range reasons {
		if i >= reasonRows {
			break
		}
		items = append(items, contract.Item{
			RefID:  fmt.Sprintf("reason-%d", i+1),
			Rank:   bandReasons<<20 + (len(reasons) - i),
			Reason: "reason: " + reason,
		})
	}

	// Band 2: the risk classification. Base thresholds are single-source with
	// change_risk (risk.LevelFor); a public-API change escalates one step —
	// the documented, deterministic 2.0 rule.
	level := risk.LevelFor(fanIn, len(dependentFiles))
	if publicAPI > 0 {
		level = escalate(level)
	}
	if len(subs) == 0 {
		level = risk.LevelUnknown
	}
	items = append(items, contract.Item{
		RefID:  "risk",
		Rank:   bandRisk << 20,
		Reason: fmt.Sprintf("risk: %s — fan-in %d, %d dependent file(s), %d public-API change(s)", level, fanIn, len(dependentFiles), publicAPI),
	})

	// unknown diff paths are cited so nothing silently disappears.
	for i, path := range unresolved {
		if i >= reasonRows {
			break
		}
		evID := ev.Add(path, 0, "unknown")
		items = append(items, contract.Item{
			RefID:          "unknown:" + path,
			Rank:           bandRisk<<20 - 1 - i,
			Reason:         fmt.Sprintf("unknown: %s — no symbols in the graph for this diff path", path),
			EvidenceRefIDs: []string{evID},
		})
	}

	r := &contract.Result{
		Outcome: contract.OutcomeFound,
		Summary: fmt.Sprintf("change_impact: risk %s — %d changed symbol(s), %d affected symbol(s), %d covering test(s), %d public-API change(s), %d co-change partner(s), %d config file(s), %d unknown path(s) (%s)",
			level, len(subs), len(directDependents)+transitive, testTotal, publicAPI, coChangePartners, configCount, len(unresolved), MethodVersion),
		Items:      items,
		Evidence:   ev.List(),
		Confidence: tally.Confidence("unknown", "no_edges"),
	}
	out, err := shape.Finish(r, p.MaxItems)
	if err != nil {
		return nil, err
	}
	if truncated {
		out.Limits.Truncated = true
		if out.Outcome == contract.OutcomeFound {
			out.Outcome = contract.OutcomePartial
		}
		if out.Limits.Next == "" {
			out.Limits.Next = "bounded reads clipped; dependent/test counts are lower bounds"
		}
	}
	return out, nil
}

// escalate raises a risk level one step (the public-API rule).
func escalate(l risk.Level) risk.Level {
	switch l {
	case risk.LevelLow:
		return risk.LevelMedium
	case risk.LevelMedium:
		return risk.LevelHigh
	default:
		return l
	}
}

// isExportedName reports whether the final segment of a qualified name starts
// with an uppercase letter and the name carries a package prefix (mirrors the
// diagnostic suppression heuristic).
func isExportedName(qn string) bool {
	parts := strings.Split(qn, ".")
	if len(parts) < 2 {
		return false
	}
	last := parts[len(parts)-1]
	if last == "" {
		return false
	}
	return unicode.IsUpper(rune(last[0]))
}

func idsOf(nodes []model.Node) []model.NodeId {
	out := make([]model.NodeId, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.ID())
	}
	return out
}

// resolveSubjects turns the target/diff into subjects plus unresolved and the
// raw diff paths (for the config section).
func resolveSubjects(ctx context.Context, p Params) ([]model.Node, []string, bool, []string, error) {
	if p.Diff != "" {
		paths := risk.DiffPaths(p.Diff)
		if len(paths) == 0 {
			return nil, nil, false, nil, errors.New("diff contains no file paths")
		}
		subjects, unresolved, truncated, err := testintel.SubjectsFromDiff(ctx, p.Deps.Query, paths, 0, 0)
		return subjects, unresolved, truncated, paths, err
	}
	res, err := resolve.Seeds(ctx, p.Deps, p.Target, 5)
	if err != nil {
		return nil, nil, false, nil, err
	}
	if res.Ambiguous() {
		nodes := make([]model.Node, 0, len(res.Candidates))
		for _, c := range res.Candidates {
			nodes = append(nodes, c.Node)
		}
		return nodes, nil, false, nil, nil
	}
	return res.Nodes, nil, false, nil, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
