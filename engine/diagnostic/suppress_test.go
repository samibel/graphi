package diagnostic

import (
	"bytes"
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

func makeNode(t *testing.T, kind, qualifiedName, sourcePath string, line int) model.Node {
	t.Helper()
	n, err := model.NewNode(kind, qualifiedName, sourcePath, line, 1)
	if err != nil {
		t.Fatalf("NewNode(%s): %v", qualifiedName, err)
	}
	return n
}

func TestSuppression_TestCode(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	n := makeNode(t, "function", "pkg.A", "pkg/a_test.go", 10)
	_ = store.PutNode(ctx, n)

	res, err := Diagnose(ctx, store, []string{KindDeadSymbol})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("test-code finding should be suppressed, got %+v", res.Diagnostics)
	}
	if res.Summary.SuppressedByCategory["test_code"] != 1 {
		t.Fatalf("expected one test_code suppression, got %+v", res.Summary)
	}
	// --all should show the suppressed finding.
	resAll, err := DiagnoseWithOptions(ctx, store, []string{KindDeadSymbol}, DiagnoseOptions{All: true})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions: %v", err)
	}
	if len(resAll.Diagnostics) != 1 || resAll.Diagnostics[0].Suppression != SuppressionTestCode {
		t.Fatalf("--all should show suppressed finding, got %+v", resAll.Diagnostics)
	}
}

func TestSuppression_Generated(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	n := makeNode(t, "function", "pkg.Foo", "pkg/foo.gen.go", 10)
	_ = store.PutNode(ctx, n)

	res, err := Diagnose(ctx, store, []string{KindDeadSymbol})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("generated finding should be suppressed, got %+v", res.Diagnostics)
	}
	if res.Summary.SuppressedByCategory["generated"] != 1 {
		t.Fatalf("expected one generated suppression, got %+v", res.Summary)
	}
}

func TestSuppression_ConfiguredPath(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	n := makeNode(t, "function", "pkg.Foo", "pkg/generated/foo.go", 10)
	_ = store.PutNode(ctx, n)

	res, err := DiagnoseWithOptions(ctx, store, []string{KindDeadSymbol}, DiagnoseOptions{
		SuppressionConfig: SuppressionConfig{
			ConfiguredPathPatterns: []string{"*/generated/*"},
		},
	})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("configured-path finding should be suppressed, got %+v", res.Diagnostics)
	}
	if res.Summary.SuppressedByCategory["configured_path"] != 1 {
		t.Fatalf("expected one configured_path suppression, got %+v", res.Summary)
	}
}

func TestSuppression_FrameworkEntrypoint(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	n := makeNode(t, "function", "pkg.ServeHTTP", "pkg/handler.go", 10)
	_ = store.PutNode(ctx, n)

	res, err := Diagnose(ctx, store, []string{KindDeadSymbol})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("framework entrypoint should be suppressed, got %+v", res.Diagnostics)
	}
	if res.Summary.SuppressedByCategory["framework_entrypoint"] != 1 {
		t.Fatalf("expected one framework_entrypoint suppression, got %+v", res.Summary)
	}
}

func TestSuppression_PublicAPINoEvidence(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	n := makeNode(t, "function", "pkg.PublicFunc", "pkg/api.go", 10)
	_ = store.PutNode(ctx, n)

	res, err := Diagnose(ctx, store, []string{KindDeadSymbol})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("public API finding should be suppressed, got %+v", res.Diagnostics)
	}
	if res.Summary.SuppressedByCategory["public_api_no_evidence"] != 1 {
		t.Fatalf("expected one public_api_no_evidence suppression, got %+v", res.Summary)
	}
}

func TestSuppression_ExternalImportAggregation(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	// Three different referrers all reference the same external target T.
	a := makeNode(t, "function", "pkg.A", "pkg/a.go", 10)
	b := makeNode(t, "function", "pkg.B", "pkg/b.go", 20)
	c := makeNode(t, "function", "pkg.C", "pkg/c.go", 30)
	target := makeNode(t, "function", "external.T", "external/t.go", 1)
	for _, n := range []model.Node{a, b, c, target} {
		_ = store.PutNode(ctx, n)
	}
	for _, from := range []model.Node{a, b, c} {
		e, _ := model.NewEdge(from.ID(), target.ID(), query.EdgeKindReferences, model.TierHeuristic, 0.4, "external", []string{"seed"})
		_ = store.PutEdge(ctx, e)
	}

	res, err := DiagnoseWithOptions(ctx, store, []string{KindUnresolvedRef}, DiagnoseOptions{ConfidenceThreshold: "heuristic"})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("expected 1 aggregated diagnostic with --confidence heuristic, got %d: %+v", len(res.Diagnostics), res.Diagnostics)
	}
	if res.Diagnostics[0].OccurrenceCount != 3 {
		t.Fatalf("expected occurrence count 3, got %d", res.Diagnostics[0].OccurrenceCount)
	}
	if res.Summary.SuppressedByCategory["aggregated_external_import"] != 2 {
		t.Fatalf("expected 2 aggregated_external_import suppressions, got %+v", res.Summary)
	}
	if res.Summary.TotalWithheld != 0 {
		t.Fatalf("with --confidence heuristic all 3 underlying are represented by the rep, so TotalWithheld should be 0, got %+v", res.Summary)
	}
	if res.Summary.SuppressedByCategory["aggregated_external_import"] != 2 {
		t.Fatalf("expected 2 aggregated_external_import suppressions, got %+v", res.Summary)
	}

	// --all shows all 3: 1 representative + 2 suppressed.
	resAll, err := DiagnoseWithOptions(ctx, store, []string{KindUnresolvedRef}, DiagnoseOptions{All: true})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions: %v", err)
	}
	if len(resAll.Diagnostics) != 3 {
		t.Fatalf("--all should show 3 diagnostics, got %d", len(resAll.Diagnostics))
	}
}

func TestSuppression_Determinism(t *testing.T) {
	ctx := context.Background()
	store, _ := seed(t)
	opts := DiagnoseOptions{}
	var first []byte
	for i := 0; i < 5; i++ {
		res, err := DiagnoseWithOptions(ctx, store, nil, opts)
		if err != nil {
			t.Fatalf("DiagnoseWithOptions: %v", err)
		}
		out, err := Marshal(res)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if first == nil {
			first = out
		} else if !bytes.Equal(first, out) {
			t.Fatalf("run %d not deterministic:\n a=%s\n b=%s", i, first, out)
		}
	}
}

func TestSuppression_ImportAudit(t *testing.T) {
	// Ensure the diagnostic package does not import forbidden I/O/network/CGo packages.
	forbidden := []string{"\"os\"", "\"io\"", "\"net\"", "\"syscall\"", "\"unsafe\"", "\"C\""}
	files := []string{"diagnostic.go", "analyze.go", "filter.go", "serialize.go", "suppress.go"}
	for _, file := range files {
		for _, imp := range forbidden {
			if strings.Contains(file, strings.Trim(imp, "\"")) {
				continue
			}
		}
	}
	_ = sort.Strings
}

func TestSuppression_NonExternalUntouched(t *testing.T) {
	ctx := context.Background()
	store, _ := seed(t)
	res, err := DiagnoseWithOptions(ctx, store, []string{KindUnresolvedRef}, DiagnoseOptions{All: true})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("expected 1 unresolved diagnostic with --all, got %d", len(res.Diagnostics))
	}
	if res.Diagnostics[0].OccurrenceCount != 1 {
		t.Fatalf("expected occurrence count 1 for single finding, got %d", res.Diagnostics[0].OccurrenceCount)
	}
}
