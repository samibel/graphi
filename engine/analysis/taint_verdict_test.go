package analysis_test

// WP-04: a taint analysis over a graph that CANNOT match a sink must report the
// honest `no_sink_candidates` verdict, not `empty` — an empty/clean result on an
// un-analyzable graph is a false all-clear, the exact failure the vuln-go field
// test flagged ("solved: true, flows: []" read as "checked, clean").

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
)

func putNode(t *testing.T, store graphstore.Graphstore, kind, qn string) model.NodeId {
	t.Helper()
	n, err := model.NewNode(kind, qn, "app/main.go", 1, 1)
	if err != nil {
		t.Fatalf("new node %q: %v", qn, err)
	}
	if err := store.PutNode(context.Background(), n); err != nil {
		t.Fatalf("put node %q: %v", qn, err)
	}
	return n.ID()
}

func TestTaintVerdict_NoSinkCandidates(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })

	// A source-shaped node but NO sink anywhere: a flow cannot exist by
	// construction. This must be no_sink_candidates, never empty.
	putNode(t, store, "function", "os.Getenv")
	putNode(t, store, "function", "app.handler")

	svc := analysis.NewDefaultService(store)
	res, err := svc.Dispatch(ctx, "taint", analysis.Params{})
	if err != nil {
		t.Fatalf("dispatch taint: %v", err)
	}
	if res.Outcome != query.OutcomeNoSinkCandidates {
		t.Errorf("outcome = %q, want %q (a graph with no sink symbols must not read as a clean/empty result)",
			res.Outcome, query.OutcomeNoSinkCandidates)
	}
	if res.Outcome == query.OutcomeEmpty {
		t.Errorf("outcome is empty — a false all-clear on an un-analyzable graph (the WP-04 regression)")
	}
}

func TestTaintVerdict_EmptyWhenSinkExistsButNoFlow(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })

	// Both a source and a sink candidate exist, but there is NO edge connecting
	// them — a genuine "checked, no flow found". This must stay `empty`, distinct
	// from no_sink_candidates.
	putNode(t, store, "function", "os.Getenv")   // source (env_input)
	putNode(t, store, "function", "os.ReadFile") // sink (file_open)

	svc := analysis.NewDefaultService(store)
	res, err := svc.Dispatch(ctx, "taint", analysis.Params{})
	if err != nil {
		t.Fatalf("dispatch taint: %v", err)
	}
	if res.Outcome != query.OutcomeEmpty {
		t.Errorf("outcome = %q, want %q (sink+source present but no path is a genuine empty, not no_sink_candidates)",
			res.Outcome, query.OutcomeEmpty)
	}
}
