package analysis_test

import (
	"bytes"
	"context"
	"sort"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
)

// seedConceptGraph builds a concept-resolution fixture. Concept term "Error":
//
//	nodes: pkg.Error (function, matched), pkg.ErrorType (type, matched),
//	       pkg.Caller (function, references Error), pkg.RecoverHandler (handler,
//	       references Error), pkg.Unrelated (no edges)
//	edges: Caller --references(derived)--> Error
//	       RecoverHandler --references(confirmed)--> Error
//
// Expected: definitions {Error, ErrorType}; handler {RecoverHandler};
// reference {Caller}; ranked definition > handler > reference.
func seedConceptGraph(t *testing.T) (*graphstore.MemStore, map[string]model.NodeId) {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	spec := []struct {
		kind, qn, path string
	}{
		{"function", "pkg.Error", "pkg/error.go"},
		{"type", "pkg.ErrorType", "pkg/error_type.go"},
		{"function", "pkg.Caller", "pkg/caller.go"},
		{"function", "pkg.RecoverHandler", "pkg/recover.go"},
		{"function", "pkg.Unrelated", "pkg/unrelated.go"},
	}
	ids := make(map[string]model.NodeId, len(spec))
	nodes := make(map[string]model.Node, len(spec))
	for _, s := range spec {
		n, err := model.NewNode(s.kind, s.qn, s.path, 7, 3)
		if err != nil {
			t.Fatalf("NewNode(%s): %v", s.qn, err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode(%s): %v", s.qn, err)
		}
		ids[s.qn] = n.ID()
		nodes[s.qn] = n
	}
	mk := func(from, to, kind string, tier model.ConfidenceTier, reason string) {
		e, err := model.NewEdge(nodes[from].ID(), nodes[to].ID(), kind, tier, 0.9, reason, []string{"pkg/x.go:1"})
		if err != nil {
			t.Fatalf("NewEdge(%s->%s): %v", from, to, err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatalf("PutEdge(%s->%s): %v", from, to, err)
		}
	}
	mk("pkg.Caller", "pkg.Error", string(query.EdgeKindReferences), model.TierDerived, "Caller references Error")
	mk("pkg.RecoverHandler", "pkg.Error", string(query.EdgeKindReferences), model.TierConfirmed, "RecoverHandler references Error")
	return store, ids
}

func conceptKinds(res analysis.Analysis) map[string]string {
	out := map[string]string{}
	for _, l := range res.Locations {
		out[l.Node.QualifiedName] = l.Kind
	}
	return out
}

func TestConceptClassification(t *testing.T) {
	store, _ := seedConceptGraph(t)
	svc := analysis.NewDefaultService(store)

	res, err := svc.Dispatch(context.Background(), "concept", analysis.Params{Concept: "Error"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if res.Outcome != query.OutcomeFound {
		t.Fatalf("outcome = %s, want found", res.Outcome)
	}
	kinds := conceptKinds(res)
	if kinds["pkg.Error"] != analysis.KindDefinition {
		t.Errorf("pkg.Error kind = %q, want definition", kinds["pkg.Error"])
	}
	if kinds["pkg.ErrorType"] != analysis.KindDefinition {
		t.Errorf("pkg.ErrorType kind = %q, want definition", kinds["pkg.ErrorType"])
	}
	if kinds["pkg.RecoverHandler"] != analysis.KindHandler {
		t.Errorf("pkg.RecoverHandler kind = %q, want handler (surfaced distinctly from references)", kinds["pkg.RecoverHandler"])
	}
	if kinds["pkg.Caller"] != analysis.KindReference {
		t.Errorf("pkg.Caller kind = %q, want reference", kinds["pkg.Caller"])
	}
	if _, present := kinds["pkg.Unrelated"]; present {
		t.Error("pkg.Unrelated should not appear in concept results")
	}
}

func TestConceptRanking(t *testing.T) {
	store, _ := seedConceptGraph(t)
	svc := analysis.NewDefaultService(store)

	res, err := svc.Dispatch(context.Background(), "concept", analysis.Params{Concept: "Error"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	// Order must be: definitions first, then handler, then reference.
	want := []string{analysis.KindDefinition, analysis.KindDefinition, analysis.KindHandler, analysis.KindReference}
	if len(res.Locations) != len(want) {
		t.Fatalf("locations = %d, want %d", len(res.Locations), len(want))
	}
	for i, w := range want {
		if res.Locations[i].Kind != w {
			t.Fatalf("location %d kind = %q, want %q (ranking definition>handler>reference violated)", i, res.Locations[i].Kind, w)
		}
	}
	// Both definitions lead; handler before reference.
	if res.Locations[2].Node.QualifiedName != "pkg.RecoverHandler" {
		t.Errorf("handler slot = %s, want pkg.RecoverHandler", res.Locations[2].Node.QualifiedName)
	}
	if res.Locations[3].Node.QualifiedName != "pkg.Caller" {
		t.Errorf("reference slot = %s, want pkg.Caller", res.Locations[3].Node.QualifiedName)
	}
}

func TestConceptNoMatches(t *testing.T) {
	store, _ := seedConceptGraph(t)
	svc := analysis.NewDefaultService(store)

	res, err := svc.Dispatch(context.Background(), "concept", analysis.Params{Concept: "NoSuchConcept"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if res.Outcome != query.OutcomeEmpty {
		t.Fatalf("outcome = %s, want empty ('no matches')", res.Outcome)
	}
	if len(res.Locations) != 0 {
		t.Fatalf("expected 0 locations, got %d", len(res.Locations))
	}
}

func TestConceptProvenance(t *testing.T) {
	store, _ := seedConceptGraph(t)
	svc := analysis.NewDefaultService(store)

	res, err := svc.Dispatch(context.Background(), "concept", analysis.Params{Concept: "Error"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	for _, l := range res.Locations {
		// Every location carries file/line provenance via the node.
		if l.Node.SourcePath == "" {
			t.Errorf("%s: empty source path (no file/line provenance)", l.Node.QualifiedName)
		}
		if l.Node.Line == 0 {
			t.Errorf("%s: zero line (no file/line provenance)", l.Node.QualifiedName)
		}
		switch l.Kind {
		case analysis.KindDefinition:
			if l.ReachedVia != nil {
				t.Errorf("%s: definition should have nil edge (FTS provenance), got %s", l.Node.QualifiedName, l.ReachedVia.ID)
			}
		default: // handler / reference
			if l.ReachedVia == nil {
				t.Errorf("%s: %s must carry the reaching edge", l.Node.QualifiedName, l.Kind)
				continue
			}
			e := l.ReachedVia
			if e.ID == "" || !e.Tier.Valid() || e.Reason == "" {
				t.Errorf("%s: reaching edge missing provenance (id/tier/reason)", l.Node.QualifiedName)
			}
			if e.To != l.Node.ID && e.From != l.Node.ID {
				t.Errorf("%s: reaching edge %s does not touch this location", l.Node.QualifiedName, e.ID)
			}
		}
	}
}

func TestConceptDeterminism(t *testing.T) {
	ctx := context.Background()
	s1, _ := seedConceptGraph(t)
	svc1 := analysis.NewDefaultService(s1)
	first, err := svc1.Dispatch(ctx, "concept", analysis.Params{Concept: "Error"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	firstBytes, _ := analysis.Marshal(first)
	for i := 0; i < 30; i++ {
		res, err := svc1.Dispatch(ctx, "concept", analysis.Params{Concept: "Error"})
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		b, _ := analysis.Marshal(res)
		if !bytes.Equal(firstBytes, b) {
			t.Fatalf("iteration %d non-byte-identical (determinism violated)", i)
		}
	}
	s2, _ := seedConceptGraph(t)
	svc2 := analysis.NewDefaultService(s2)
	r2, err := svc2.Dispatch(ctx, "concept", analysis.Params{Concept: "Error"})
	if err != nil {
		t.Fatalf("svc2: %v", err)
	}
	b2, _ := analysis.Marshal(r2)
	if !bytes.Equal(firstBytes, b2) {
		t.Fatal("two independent services produced non-identical concept output")
	}
}

// searchlessReader implements query.Reader but NOT SearchNodes, to verify the
// concept analyzer registers only when the backend can search.
type searchlessReader struct {
	store *graphstore.MemStore
}

func (s searchlessReader) GetNode(ctx context.Context, id model.NodeId) (model.Node, error) {
	return s.store.GetNode(ctx, id)
}
func (s searchlessReader) GetEdge(ctx context.Context, id model.EdgeId) (model.Edge, error) {
	return s.store.GetEdge(ctx, id)
}
func (s searchlessReader) Nodes(ctx context.Context, q graphstore.Query) ([]model.Node, error) {
	return s.store.Nodes(ctx, q)
}
func (s searchlessReader) Edges(ctx context.Context, q graphstore.Query) ([]model.Edge, error) {
	return s.store.Edges(ctx, q)
}

func TestConceptGracefulWithoutSearch(t *testing.T) {
	store, _ := seedConceptGraph(t)
	// A reader that satisfies query.Reader but NOT Searcher.
	svc := analysis.NewDefaultService(searchlessReader{store: store})
	names := svc.Names()
	sort.Strings(names)
	if containsAnalyzer(names, "concept") {
		t.Fatalf("concept should NOT register when reader lacks SearchNodes; names = %v", names)
	}
	if !containsAnalyzer(names, "impact") || !containsAnalyzer(names, "call-chain") {
		t.Fatalf("impact+call-chain must still register; names = %v", names)
	}
}

func TestConceptRegisteredWithSearch(t *testing.T) {
	store, _ := seedConceptGraph(t)
	svc := analysis.NewDefaultService(store)
	names := svc.Names()
	if !containsAnalyzer(names, "concept") {
		t.Fatalf("concept not registered with a searchable backend; names = %v", names)
	}
}
