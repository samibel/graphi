package main

import (
	"context"
	"fmt"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/diagnostic"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/scenario"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/internal/evalreport"
)

// SyntheticSignalFixture is the fixture_ref for the hand-built signal-quality
// graph. Unlike the ingested corpus fixtures, its shape is exact by
// construction, so the measurement has ground truth: precisely ONE finding
// (dead_symbol sig.deadHelper) is a true positive in default output; every
// other candidate must be suppressed, gated, or aggregated.
const SyntheticSignalFixture = "synthetic-signal"

// buildSignalStore constructs the ground-truth graph:
//
//	sig.deadHelper   sig/helper.go       — dead, unexported → the ONE default finding
//	sig.LiveFn       sig/live.go         — called by sig.mainFn → not dead
//	sig.deadTestOnly sig/helper_test.go  — dead → suppressed (test_code)
//	gen.deadGen      sig/api.gen.go      — dead → suppressed (generated)
//	sig.ServeHTTP    sig/http.go         — dead → suppressed (framework_entrypoint)
//	sig.PublicUnused sig/pub.go          — dead → suppressed (public_api_no_evidence)
//	sig.userA/userB  → ext.Dep           — heuristic refs → aggregated external imports
func buildSignalStore() (*graphstore.MemStore, error) {
	ctx := context.Background()
	store := graphstore.NewMemStore()

	mk := func(kind, qn, path string, line int) (model.Node, error) {
		n, err := model.NewNode(kind, qn, path, line, 1)
		if err != nil {
			return model.Node{}, err
		}
		return n, store.PutNode(ctx, n)
	}

	type spec struct {
		kind, qn, path string
		line           int
	}
	nodes := map[string]model.Node{}
	for _, s := range []spec{
		{"function", "sig.deadHelper", "sig/helper.go", 5},
		{"function", "sig.LiveFn", "sig/live.go", 3},
		{"function", "main.main", "sig/main.go", 8}, // framework entrypoint (dead but suppressed)
		{"function", "sig.deadTestOnly", "sig/helper_test.go", 7},
		{"function", "gen.deadGen", "sig/api.gen.go", 3},
		{"function", "sig.ServeHTTP", "sig/http.go", 12},
		{"function", "sig.PublicUnused", "sig/pub.go", 4},
		{"function", "sig.userA", "sig/a.go", 2},
		{"function", "sig.userB", "sig/b.go", 2},
		{"function", "ext.Dep", "ext/dep.go", 1},
	} {
		n, err := mk(s.kind, s.qn, s.path, s.line)
		if err != nil {
			return nil, err
		}
		nodes[s.qn] = n
	}

	edge := func(from, to, kind string, tier model.ConfidenceTier, conf float64, ev string) error {
		e, err := model.NewEdge(nodes[from].ID(), nodes[to].ID(), kind, tier, conf, "signal fixture", []string{ev})
		if err != nil {
			return err
		}
		return store.PutEdge(ctx, e)
	}
	// Live symbols: everything reachable from the entrypoint. userA/userB are
	// live so their heuristic external references are the ONLY unresolved-ref
	// candidates, and deadHelper stays the single true dead-symbol positive.
	for _, callee := range []string{"sig.LiveFn", "sig.userA", "sig.userB"} {
		if err := edge("main.main", callee, "calls", model.TierConfirmed, 0.95, "sig/main.go:9"); err != nil {
			return nil, err
		}
	}
	// Aggregation fixture: two heuristic references to the SAME external target.
	if err := edge("sig.userA", "ext.Dep", "references", model.TierHeuristic, 0.3, "sig/a.go:3"); err != nil {
		return nil, err
	}
	if err := edge("sig.userB", "ext.Dep", "references", model.TierHeuristic, 0.3, "sig/b.go:3"); err != nil {
		return nil, err
	}
	return store, nil
}

// signalEngine wraps the synthetic store in the shared scenario engine.
func signalEngine() (*scenario.FixtureEngine, error) {
	store, err := buildSignalStore()
	if err != nil {
		return nil, err
	}
	return scenario.NewFixtureEngine(resolve.Deps{Query: query.New(store), Search: search.New(store)}), nil
}

// expectedDefaultFindings is the ground truth for default diagnose output on
// the synthetic fixture: code → file of the ONLY true positives.
var expectedDefaultFindings = map[string]string{
	"dead_symbol": "sig/helper.go",
}

// measureSignal runs the diagnostics twice (default, --all) over the
// ground-truth fixture and derives the signal-quality score from four
// components, each 0–100, averaged:
//
//	c1 precision:        1 - false positives / shown (ground truth known)
//	c2 recall:           expected findings actually shown
//	c3 noise reduction:  (1 - defaultShown/allAnalyzed) scaled so filtering
//	                     at least half of the raw candidates scores 100
//	c4 action safety:    100 iff no mutating, non-preview action is attached
//	                     to any suppressed or heuristic finding
//
// The per-metric values are recorded in the report for auditability.
func measureSignal() (evalreport.SignalMetrics, error) {
	ctx := context.Background()
	store, err := buildSignalStore()
	if err != nil {
		return evalreport.SignalMetrics{}, err
	}

	def, err := diagnostic.DiagnoseWithOptions(ctx, store, nil, diagnostic.DiagnoseOptions{})
	if err != nil {
		return evalreport.SignalMetrics{}, fmt.Errorf("default diagnose: %w", err)
	}
	all, err := diagnostic.DiagnoseWithOptions(ctx, store, nil, diagnostic.DiagnoseOptions{All: true})
	if err != nil {
		return evalreport.SignalMetrics{}, fmt.Errorf("all diagnose: %w", err)
	}

	m := evalreport.SignalMetrics{
		DefaultCount: len(def.Diagnostics),
		AllCount:     len(all.Diagnostics),
		Analyzed:     def.Summary.TotalAnalyzed,
	}
	for cat, n := range def.Summary.SuppressedByCategory {
		m.SuppressedByCategory = append(m.SuppressedByCategory, fmt.Sprintf("%s=%d", cat, n))
		m.SuppressedTotal += n
	}
	sort.Strings(m.SuppressedByCategory)

	// c1 precision + c2 recall against ground truth.
	seen := map[string]bool{}
	for _, d := range def.Diagnostics {
		key := d.Code
		if expectedDefaultFindings[key] == d.File {
			seen[key] = true
		} else {
			m.FalsePositives++
		}
	}
	precision := 1.0
	if len(def.Diagnostics) > 0 {
		precision = 1 - float64(m.FalsePositives)/float64(len(def.Diagnostics))
	}
	recall := float64(len(seen)) / float64(len(expectedDefaultFindings))

	// c3 noise reduction: default output vs raw analyzed candidates.
	noise := 0.0
	if m.Analyzed > 0 {
		noise = 1 - float64(m.DefaultCount)/float64(m.Analyzed)
	}
	noiseScore := noise / 0.5 * 100
	if noiseScore > 100 {
		noiseScore = 100
	}

	// c4 action safety over the FULL output.
	for _, d := range all.Diagnostics {
		unsafe := d.Suppression != "" || d.Confidence == diagnostic.ConfidenceHeuristic
		if !unsafe {
			continue
		}
		for _, a := range d.Actions {
			if a.Kind == diagnostic.ActionSafeDeleteSymbol && !a.Preview {
				m.UnsafeActions++
			}
		}
	}
	safety := 100.0
	if m.UnsafeActions > 0 {
		safety = 0
	}

	m.FalsePositiveRate = 1 - precision
	m.Score = (precision*100 + recall*100 + noiseScore + safety) / 4
	return m, nil
}
