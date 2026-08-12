// Package testintel computes the symbol↔test mapping ON DEMAND: given subject
// symbols, which test symbols exercise them, and by which signal. It is the
// shared engine leaf behind test_impact and change_impact.
//
// The mapping is deliberately DERIVED, never materialized: no new edge kinds,
// no ingest changes, no graph-schema growth — the frozen `index` output and
// the incremental-vs-full parity gates stay byte-identical. Every read is
// selective and bounded (CORE-02): bounded reverse walks over calls/references
// plus exact SourcePath lookups for the naming/package heuristics — never a
// catalog scan, and never the lexical search service (whose ranking semantics
// differ between the Mem and SQLite backends and would break cross-backend
// byte identity).
//
// Signals, strongest first:
//
//	direct_call — the test symbol has a calls/references edge to the subject.
//	transitive  — the test reaches the subject through the bounded walk (depth ≥ 2).
//	naming      — a symbol in a same-directory test file matches the Test<X> /
//	              <X>Test / test_<x> conventions for the subject's name.
//	package     — a same-directory test file exists (file-level, weakest).
package testintel

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	pathclass "github.com/samibel/graphi/engine/classify"
	"github.com/samibel/graphi/engine/query"
)

// Signal names the evidence class of one link.
type Signal string

const (
	SignalDirectCall Signal = "direct_call"
	SignalTransitive Signal = "transitive"
	SignalNaming     Signal = "naming"
)

// Walk bounds (mirror symbol_context's reverse walk).
const (
	DefaultDepth   = 2
	MaxDepth       = 3
	edgesPerNode   = 64
	walkNodeBudget = 512
	// nearbyFileCap bounds the same-directory test files consulted per subject
	// directory for the naming/package heuristics.
	nearbyFileCap = 8
)

// Link is one subject↔test relationship with its evidence.
type Link struct {
	Subject model.NodeId
	Test    query.ResultNode
	Signal  Signal
	// Depth is the walk depth for call signals (1 = direct); 0 for naming.
	Depth int
	// Tier is the confidence tier of the edge into the test symbol for call
	// signals; naming links are always heuristic.
	Tier model.ConfidenceTier
	// Evidence carries "path:line" refs (edge evidence for call signals, the
	// test symbol's definition site for naming links).
	Evidence []string
}

// Result is the derived mapping for one subject set.
type Result struct {
	// Links are the call + naming links, sorted by (Subject, Test.ID, Signal).
	Links []Link
	// NearbyTestFiles are same-directory test files per subject directory
	// (the package-proximity signal), sorted, capped at nearbyFileCap per dir.
	NearbyTestFiles []string
	// AllTestFiles is every test file the compact aggregate knows about,
	// sorted — the universe test_impact subtracts from.
	AllTestFiles []string
	// Truncated reports whether any bounded read clipped.
	Truncated bool
}

// TestsFor computes the mapping for subjects over the query service's
// selective ports. depth 0 selects DefaultDepth; values clamp to [1, MaxDepth].
func TestsFor(ctx context.Context, svc *query.Service, subjects []model.Node, depth int) (Result, error) {
	if depth == 0 {
		depth = DefaultDepth
	}
	if depth < 1 {
		depth = 1
	}
	if depth > MaxDepth {
		depth = MaxDepth
	}

	out := Result{}
	if len(subjects) == 0 || svc == nil {
		return out, nil
	}
	reader := svc.Reader()
	bounded, bok := reader.(graphstore.BoundedGraphLookup)
	lookup, lok := reader.(graphstore.GraphLookup)
	symbols, sok := reader.(graphstore.SymbolLookupPort)
	agg, aok := reader.(graphstore.BriefAggregatePort)
	if !bok || !lok || !sok || !aok {
		// Both shipped backends implement every port; a reader that does not
		// yields no links rather than a full scan.
		return out, nil
	}

	// Canonical subject order so identical inputs walk identically.
	subs := append([]model.Node{}, subjects...)
	sort.Slice(subs, func(i, j int) bool { return subs[i].ID() < subs[j].ID() })

	// The test-file universe and the per-directory neighborhoods come from one
	// compact aggregate.
	stats, err := agg.BriefStats(ctx, 1)
	if err != nil {
		return Result{}, err
	}
	subjectDirs := map[string]struct{}{}
	for _, s := range subs {
		if s.SourcePath() != "" {
			subjectDirs[dirOf(s.SourcePath())] = struct{}{}
		}
	}
	nearbyPerDir := map[string][]string{}
	for _, f := range stats.Files {
		if !pathclass.IsTestPath(f.Path) {
			continue
		}
		out.AllTestFiles = append(out.AllTestFiles, f.Path)
		d := dirOf(f.Path)
		if _, near := subjectDirs[d]; near && len(nearbyPerDir[d]) < nearbyFileCap {
			nearbyPerDir[d] = append(nearbyPerDir[d], f.Path)
		}
	}
	sort.Strings(out.AllTestFiles)
	for _, files := range nearbyPerDir {
		out.NearbyTestFiles = append(out.NearbyTestFiles, files...)
	}
	sort.Strings(out.NearbyTestFiles)

	// Call-graph links: one bounded reverse walk per subject.
	linkSeen := map[string]struct{}{}
	addLink(&out, linkSeen, walkLinks(ctx, bounded, lookup, subs, depth, &out.Truncated)...)

	// Naming links: exact symbol listings of the nearby test files.
	naming, err := namingLinks(ctx, symbols, subs, out.NearbyTestFiles)
	if err != nil {
		return Result{}, err
	}
	addLink(&out, linkSeen, naming...)

	sort.Slice(out.Links, func(i, j int) bool {
		a, b := out.Links[i], out.Links[j]
		if a.Subject != b.Subject {
			return a.Subject < b.Subject
		}
		if a.Test.ID != b.Test.ID {
			return a.Test.ID < b.Test.ID
		}
		return a.Signal < b.Signal
	})
	return out, nil
}

// addLink appends links, deduplicating (subject, test, signal) triples.
func addLink(out *Result, seen map[string]struct{}, links ...Link) {
	for _, l := range links {
		key := string(l.Subject) + "|" + string(l.Test.ID) + "|" + string(l.Signal)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out.Links = append(out.Links, l)
	}
}

// walkLinks performs the bounded reverse BFS from every subject, terminating
// branches at test nodes (mirrors symbol_context's walk shape).
func walkLinks(ctx context.Context, bounded graphstore.BoundedGraphLookup, lookup graphstore.GraphLookup, subs []model.Node, depth int, truncated *bool) []Link {
	var links []Link
	for _, sub := range subs {
		visited := map[model.NodeId]struct{}{sub.ID(): {}}
		frontier := []model.NodeId{sub.ID()}
		for d := 1; d <= depth && len(frontier) > 0; d++ {
			var hopEdges []model.Edge
			for _, id := range frontier {
				edges, trunc, err := bounded.IncomingBounded(ctx, id, edgesPerNode, query.EdgeKindCalls, query.EdgeKindReferences)
				if err != nil {
					continue // fail-soft per node; the walk stays bounded either way
				}
				*truncated = *truncated || trunc
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
				return links
			}
			nodeByID := make(map[model.NodeId]model.Node, len(neighbors))
			for _, n := range neighbors {
				nodeByID[n.ID()] = n
			}

			var next []model.NodeId
			for _, e := range hopEdges {
				from := e.From()
				if from == sub.ID() {
					continue
				}
				n, ok := nodeByID[from]
				if !ok || n.SourcePath() == "" {
					continue
				}
				if _, done := visited[from]; done {
					continue
				}
				if len(visited) >= walkNodeBudget {
					*truncated = true
					continue
				}
				visited[from] = struct{}{}
				if pathclass.IsTestPath(n.SourcePath()) {
					signal := SignalDirectCall
					if d > 1 {
						signal = SignalTransitive
					}
					links = append(links, Link{
						Subject:  sub.ID(),
						Test:     resultNode(n),
						Signal:   signal,
						Depth:    d,
						Tier:     e.Tier(),
						Evidence: e.Evidence(),
					})
					continue // a test node terminates its branch
				}
				next = append(next, from)
			}
			frontier = next
		}
	}
	return links
}

// namingLinks lists the symbols of the nearby test files via exact SourcePath
// lookups and matches them against the naming conventions for each subject.
func namingLinks(ctx context.Context, symbols graphstore.SymbolLookupPort, subs []model.Node, testFiles []string) ([]Link, error) {
	var links []Link
	for _, file := range testFiles {
		nodes, err := symbols.SourcePath(ctx, model.NormalizePath(file))
		if err != nil {
			return nil, err
		}
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID() < nodes[j].ID() })
		for _, sub := range subs {
			if dirOf(sub.SourcePath()) != dirOf(file) {
				continue
			}
			want := lastName(sub.QualifiedName())
			if want == "" {
				continue
			}
			for _, tn := range nodes {
				if !matchesNaming(lastName(tn.QualifiedName()), want) {
					continue
				}
				links = append(links, Link{
					Subject:  sub.ID(),
					Test:     resultNode(tn),
					Signal:   SignalNaming,
					Tier:     model.TierHeuristic,
					Evidence: []string{tn.SourcePath() + ":" + strconv.Itoa(tn.Line())},
				})
			}
		}
	}
	return links, nil
}

// matchesNaming reports whether test symbol name t exercises subject name x by
// convention: TestX / TestX_* / BenchmarkX (Go), XTest (JUnit style),
// test_x / test_x_* (Python/Ruby).
func matchesNaming(t, x string) bool {
	if t == "" || x == "" {
		return false
	}
	if strings.HasPrefix(t, "Test"+x) || strings.HasPrefix(t, "Benchmark"+x) {
		return true
	}
	if t == x+"Test" || t == x+"Tests" {
		return true
	}
	lower := strings.ToLower(x)
	return t == "test_"+lower || strings.HasPrefix(t, "test_"+lower+"_")
}

// lastName returns the final dot segment of a qualified name.
func lastName(qn string) string {
	if i := strings.LastIndex(qn, "."); i >= 0 {
		return qn[i+1:]
	}
	return qn
}

// dirOf returns the directory portion of a repo-relative path ("" for root).
func dirOf(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}

func resultNode(n model.Node) query.ResultNode {
	return query.ResultNode{
		ID:            n.ID(),
		Kind:          n.Kind(),
		QualifiedName: n.QualifiedName(),
		SourcePath:    n.SourcePath(),
		Line:          n.Line(),
	}
}

// SubjectsFromDiff resolves a unified diff's file paths into the graph symbols
// they contain, via exact SourcePath lookups. It returns the resolved subjects
// plus the diff paths that matched nothing in the graph (unresolved — the
// caller reports them instead of guessing). Bounded: at most maxFiles diff
// files and maxPerFile symbols per file are consulted (0 selects the
// defaults); dropped counts are reported via the truncated flag.
func SubjectsFromDiff(ctx context.Context, svc *query.Service, diffPaths []string, maxFiles, maxPerFile int) (subjects []model.Node, unresolved []string, truncated bool, err error) {
	if maxFiles <= 0 {
		maxFiles = 16
	}
	if maxPerFile <= 0 {
		maxPerFile = 64
	}
	symbols, ok := svc.Reader().(graphstore.SymbolLookupPort)
	if !ok {
		return nil, append([]string{}, diffPaths...), false, nil
	}
	paths := append([]string{}, diffPaths...)
	sort.Strings(paths)
	if len(paths) > maxFiles {
		paths = paths[:maxFiles]
		truncated = true
	}
	for _, p := range paths {
		nodes, err := symbols.SourcePath(ctx, model.NormalizePath(p))
		if err != nil {
			return nil, nil, false, err
		}
		if len(nodes) == 0 {
			unresolved = append(unresolved, p)
			continue
		}
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID() < nodes[j].ID() })
		if len(nodes) > maxPerFile {
			nodes = nodes[:maxPerFile]
			truncated = true
		}
		subjects = append(subjects, nodes...)
	}
	return subjects, unresolved, truncated, nil
}
