// Package testimpact implements the test_impact agent tool (labs): given a
// unified diff or a symbol/file target, which tests must run, which are
// recommended, and which are probably unaffected — so an agent runs seven
// tests instead of the whole suite.
//
// Buckets, derived from engine/testintel's on-demand mapping (never from
// materialized edges, never from an LLM):
//
//	must_run            — direct-call links at confirmed/derived tier: the test
//	                      provably exercises a changed symbol.
//	recommended         — transitive links, heuristic-tier direct links, naming
//	                      matches, and same-directory test files.
//	probably_unaffected — every other known test file (universe minus the two
//	                      buckets above; row-capped, fully counted in the summary).
//	unknown             — diff paths that resolved to no graph symbols, and the
//	                      explicit truncation marker when a bounded read clipped.
package testimpact

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/risk"
	"github.com/samibel/graphi/engine/agenttools/shape"
	"github.com/samibel/graphi/engine/testintel"
)

const tool = "test_impact"

// MethodVersion stamps the assembly logic version into the summary.
const MethodVersion = "test_impact/1"

// Rank bands (rank = band<<20 + score, score < 1<<20).
const (
	bandMustRun     = 9
	bandRecommended = 8
	bandUnaffected  = 3
	bandUnknown     = 2
)

// Section row caps (the global item cap still applies on top).
const (
	mustRunRows     = 16
	recommendedRows = 16
	unaffectedRows  = 8
	unknownRows     = 8
)

// Params carries the test_impact inputs. Exactly one of Target/Diff must be
// non-empty (change_risk precedent).
type Params struct {
	// Target is a symbol reference or repo-relative path.
	Target string
	// Diff is a unified diff (the CLI reads it from a file or stdin; a git
	// range is served by piping `git diff <range>` in).
	Diff string
	// Depth bounds the reverse walk; 0 selects testintel.DefaultDepth.
	Depth int
	// MaxItems caps the item list (0 selects shape.DefaultMaxItems).
	MaxItems int
	// Deps are the shared engine services.
	Deps resolve.Deps
}

// Assemble resolves the change surface and buckets the repository's tests.
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

	subjects, unresolved, truncated, err := resolveSubjects(ctx, p)
	if err != nil {
		return nil, err
	}
	if len(subjects) == 0 && len(unresolved) == 0 {
		return shape.Empty(tool, firstNonEmpty(p.Target, "diff")), nil
	}

	mapping := testintel.Result{}
	if len(subjects) > 0 {
		mapping, err = testintel.TestsFor(ctx, p.Deps.Query, subjects, p.Depth)
		if err != nil {
			return nil, err
		}
	}
	truncated = truncated || mapping.Truncated

	ev := shape.NewEvidenceSet()
	tally := shape.TierTally{}
	var items []contract.Item

	// Partition the links: must_run vs recommended, deduplicated per test
	// symbol with the strongest signal winning (links arrive sorted by
	// (subject, test, signal), so iteration is canonical).
	type testRow struct {
		link  testintel.Link
		score int
	}
	mustRun := map[model.NodeId]testRow{}
	recommended := map[model.NodeId]testRow{}
	coveredFiles := map[string]struct{}{}
	for _, l := range mapping.Links {
		tally.Count(l.Tier)
		coveredFiles[l.Test.SourcePath] = struct{}{}
		strong := l.Signal == testintel.SignalDirectCall && l.Tier != model.TierHeuristic
		score := linkScore(l)
		if strong {
			if cur, ok := mustRun[l.Test.ID]; !ok || score > cur.score {
				mustRun[l.Test.ID] = testRow{link: l, score: score}
			}
			continue
		}
		if _, promoted := mustRun[l.Test.ID]; promoted {
			continue
		}
		if cur, ok := recommended[l.Test.ID]; !ok || score > cur.score {
			recommended[l.Test.ID] = testRow{link: l, score: score}
		}
	}
	// A test that earned must_run through one link never also appears in
	// recommended.
	for id := range mustRun {
		delete(recommended, id)
	}

	emit := func(rows map[model.NodeId]testRow, band, cap int, label string) int {
		ordered := make([]testRow, 0, len(rows))
		for _, r := range rows {
			ordered = append(ordered, r)
		}
		sort.Slice(ordered, func(i, j int) bool {
			if ordered[i].score != ordered[j].score {
				return ordered[i].score > ordered[j].score
			}
			return ordered[i].link.Test.ID < ordered[j].link.Test.ID
		})
		emitted := 0
		for _, r := range ordered {
			if emitted >= cap {
				break
			}
			emitted++
			var evIDs []string
			for _, ref := range r.link.Evidence {
				evIDs = append(evIDs, ev.AddRef(ref, string(r.link.Signal)))
			}
			items = append(items, contract.Item{
				RefID:          string(r.link.Test.ID),
				Rank:           band<<20 + r.score,
				Reason:         fmt.Sprintf("%s: %s %s (%s:%d) [%s, depth %d, %s]", label, r.link.Test.Kind, r.link.Test.QualifiedName, r.link.Test.SourcePath, r.link.Test.Line, r.link.Signal, r.link.Depth, r.link.Tier),
				EvidenceRefIDs: evIDs,
			})
		}
		return len(ordered)
	}
	mustRunTotal := emit(mustRun, bandMustRun, mustRunRows, "must_run")
	recommendedTotal := emit(recommended, bandRecommended, recommendedRows, "recommended")

	// Same-directory test files without a symbol-level link stay recommended
	// at file granularity.
	nearbyExtra := 0
	for _, f := range mapping.NearbyTestFiles {
		if _, covered := coveredFiles[f]; covered {
			continue
		}
		if nearbyExtra >= recommendedRows {
			break
		}
		nearbyExtra++
		evID := ev.Add(f, 0, "package")
		items = append(items, contract.Item{
			RefID:          "near:" + f,
			Rank:           bandRecommended << 20,
			Reason:         fmt.Sprintf("recommended: %s [package proximity]", f),
			EvidenceRefIDs: []string{evID},
		})
		coveredFiles[f] = struct{}{}
	}
	recommendedTotal += nearbyExtra

	// probably_unaffected: the remaining test-file universe.
	unaffectedTotal := 0
	unaffectedEmitted := 0
	for _, f := range mapping.AllTestFiles {
		if _, covered := coveredFiles[f]; covered {
			continue
		}
		unaffectedTotal++
		if unaffectedEmitted >= unaffectedRows {
			continue
		}
		unaffectedEmitted++
		evID := ev.Add(f, 0, "unaffected")
		items = append(items, contract.Item{
			RefID:          "far:" + f,
			Rank:           bandUnaffected<<20 + 1,
			Reason:         fmt.Sprintf("probably_unaffected: %s [no link found within bounds]", f),
			EvidenceRefIDs: []string{evID},
		})
	}

	// A change with NO test signal at all is itself a finding: cite the
	// subject and say so explicitly instead of returning an empty, uncitable
	// result (the fixture-without-tests case).
	if len(subjects) > 0 && mustRunTotal == 0 && recommendedTotal == 0 && unaffectedTotal == 0 {
		lead := subjects[0]
		for _, s := range subjects[1:] {
			if s.ID() < lead.ID() {
				lead = s
			}
		}
		evID := ev.Add(lead.SourcePath(), lead.Line(), "coverage")
		items = append(items, contract.Item{
			RefID:          "coverage",
			Rank:           bandUnknown<<20 + 2,
			Reason:         "coverage: no test files known to the graph for this change — treat it as untested",
			EvidenceRefIDs: []string{evID},
		})
	}

	// unknown: unresolved diff paths (never guessed into a bucket).
	for i, path := range unresolved {
		if i >= unknownRows {
			break
		}
		evID := ev.Add(path, 0, "unknown")
		items = append(items, contract.Item{
			RefID:          "unknown:" + path,
			Rank:           bandUnknown<<20 + 1,
			Reason:         fmt.Sprintf("unknown: %s — no symbols in the graph for this diff path", path),
			EvidenceRefIDs: []string{evID},
		})
	}

	level := risk.LevelUnknown
	if len(subjects) > 0 {
		// Coverage-shaped risk hint: changes with zero must-run tests are the
		// risky ones.
		switch {
		case mustRunTotal == 0 && recommendedTotal == 0:
			level = risk.LevelHigh
		case mustRunTotal == 0:
			level = risk.LevelMedium
		default:
			level = risk.LevelLow
		}
	}

	r := &contract.Result{
		Outcome: contract.OutcomeFound,
		Summary: fmt.Sprintf("test_impact: %d changed symbol(s) — %d must-run, %d recommended, %d probably-unaffected test target(s), %d unknown path(s); coverage risk %s (%s)",
			len(subjects), mustRunTotal, recommendedTotal, unaffectedTotal, len(unresolved), level, MethodVersion),
		Items:      items,
		Evidence:   ev.List(),
		Confidence: tally.Confidence("unknown", "no_links"),
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
			out.Limits.Next = "bounded reads clipped; results are a superset-safe lower bound — treat missing links as unknown, not as proof of no coverage"
		}
	}
	return out, nil
}

// linkScore ranks links inside a bucket: signal class first, then tier, then
// shallower depth.
func linkScore(l testintel.Link) int {
	signal := 0
	switch l.Signal {
	case testintel.SignalDirectCall:
		signal = 300
	case testintel.SignalTransitive:
		signal = 200
	case testintel.SignalNaming:
		signal = 100
	}
	tier := 1
	switch l.Tier {
	case model.TierConfirmed:
		tier = 3
	case model.TierDerived:
		tier = 2
	}
	depth := 0
	if l.Depth > 0 {
		depth = testintel.MaxDepth - l.Depth + 1
	}
	return signal + tier*10 + depth
}

// resolveSubjects turns the target/diff into subject symbols plus unresolved
// diff paths.
func resolveSubjects(ctx context.Context, p Params) ([]model.Node, []string, bool, error) {
	if p.Diff != "" {
		paths := risk.DiffPaths(p.Diff)
		if len(paths) == 0 {
			return nil, nil, false, errors.New("diff contains no file paths")
		}
		return testintel.SubjectsFromDiff(ctx, p.Deps.Query, paths, 0, 0)
	}
	res, err := resolve.Seeds(ctx, p.Deps, p.Target, 5)
	if err != nil {
		return nil, nil, false, err
	}
	if res.Ambiguous() {
		nodes := make([]model.Node, 0, len(res.Candidates))
		for _, c := range res.Candidates {
			nodes = append(nodes, c.Node)
		}
		return nodes, nil, false, nil
	}
	return res.Nodes, nil, false, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
