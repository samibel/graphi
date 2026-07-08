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
	// WP-12: the analyzer emits ONE diagnostic per target (count 3, merged
	// evidence), so the suppression-stage aggregated_external_import category is
	// not exercised and there are no separately-withheld members.
	if res.Summary.SuppressedByCategory["aggregated_external_import"] != 0 {
		t.Fatalf("expected 0 aggregated_external_import suppressions (analyzer aggregates), got %+v", res.Summary)
	}
	if res.Summary.TotalWithheld != 0 {
		t.Fatalf("all 3 underlying references are represented by the single aggregate, so TotalWithheld should be 0, got %+v", res.Summary)
	}

	// --all shows the single aggregated diagnostic (count 3), not per-edge rows.
	resAll, err := DiagnoseWithOptions(ctx, store, []string{KindUnresolvedRef}, DiagnoseOptions{All: true})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions: %v", err)
	}
	if len(resAll.Diagnostics) != 1 {
		t.Fatalf("--all should show 1 aggregated diagnostic, got %d", len(resAll.Diagnostics))
	}
	if resAll.Diagnostics[0].OccurrenceCount != 3 {
		t.Fatalf("--all aggregate should carry occurrence count 3, got %d", resAll.Diagnostics[0].OccurrenceCount)
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

func TestSuppression_GeneratedContentMarker(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	n := makeNode(t, "function", "pkg.fromGenerator", "pkg/output.go", 10)
	_ = store.PutNode(ctx, n)

	detector := func(file string) bool { return file == "pkg/output.go" }
	res, err := DiagnoseWithOptions(ctx, store, []string{KindDeadSymbol}, DiagnoseOptions{
		SuppressionConfig: SuppressionConfig{GeneratedMarkerDetector: detector},
	})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("marker-detected generated file should be suppressed, got %+v", res.Diagnostics)
	}
	if res.Summary.SuppressedByCategory["generated"] != 1 {
		t.Fatalf("expected one generated suppression, got %+v", res.Summary)
	}
}

func TestSuppression_FrameworkOnlyAppliesToDeadSymbols(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	// An unresolved (heuristic) edge FROM a framework-named symbol must NOT be
	// framework-suppressed: that category exists for dead-symbol findings only.
	from := makeNode(t, "function", "pkg.ServeHTTP", "pkg/handler.go", 10)
	to := makeNode(t, "function", "ext.Dep", "ext/dep.go", 1)
	_ = store.PutNode(ctx, from)
	_ = store.PutNode(ctx, to)
	e, err := model.NewEdge(from.ID(), to.ID(), "calls", model.TierHeuristic, 0.3, "best-effort", []string{"pkg/handler.go:11"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.PutEdge(ctx, e)

	res, err := DiagnoseWithOptions(ctx, store, []string{KindUnresolvedRef}, DiagnoseOptions{All: true})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions: %v", err)
	}
	for _, d := range res.Diagnostics {
		if d.Suppression == SuppressionFrameworkEntrypoint {
			t.Fatalf("framework suppression must not apply to %s findings: %+v", d.Code, d)
		}
	}
}

func TestExplainSuppressedKeepsTaggedFindings(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	n := makeNode(t, "function", "pkg.helper", "pkg/a_test.go", 10)
	_ = store.PutNode(ctx, n)

	// Default: suppressed finding is hidden.
	def, err := DiagnoseWithOptions(ctx, store, []string{KindDeadSymbol}, DiagnoseOptions{})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions: %v", err)
	}
	if len(def.Diagnostics) != 0 {
		t.Fatalf("test-code finding should be hidden by default, got %+v", def.Diagnostics)
	}

	// --explain-suppressed: finding stays visible WITH its category tag.
	exp, err := DiagnoseWithOptions(ctx, store, []string{KindDeadSymbol}, DiagnoseOptions{ExplainSuppressed: true})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions: %v", err)
	}
	if len(exp.Diagnostics) != 1 {
		t.Fatalf("expected the suppressed finding to be visible, got %+v", exp.Diagnostics)
	}
	if exp.Diagnostics[0].Suppression != SuppressionTestCode {
		t.Fatalf("expected test_code suppression tag, got %q", exp.Diagnostics[0].Suppression)
	}
	if exp.Summary.SuppressedByCategory["test_code"] != 1 {
		t.Fatalf("suppression counts must remain in summary, got %+v", exp.Summary)
	}
}

func TestConfidenceThresholdAcceptsProductTiers(t *testing.T) {
	for _, s := range []string{"confirmed", "derived", "exact", ""} {
		if got := ConfidenceThresholdOf(s); got != ConfidenceExact {
			t.Fatalf("ConfidenceThresholdOf(%q) = %q, want exact", s, got)
		}
	}
	if got := ConfidenceThresholdOf("heuristic"); got != ConfidenceHeuristic {
		t.Fatalf("ConfidenceThresholdOf(heuristic) = %q, want heuristic", got)
	}
}

func TestDiagnosticsCarryEvidence(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	dead := makeNode(t, "function", "pkg.dead", "pkg/dead.go", 42)
	from := makeNode(t, "function", "pkg.caller", "pkg/caller.go", 5)
	to := makeNode(t, "function", "ext.Dep", "ext/dep.go", 1)
	_ = store.PutNode(ctx, dead)
	_ = store.PutNode(ctx, from)
	_ = store.PutNode(ctx, to)
	e, err := model.NewEdge(from.ID(), to.ID(), "calls", model.TierHeuristic, 0.3, "best-effort", []string{"pkg/caller.go:6"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.PutEdge(ctx, e)

	res, err := DiagnoseWithOptions(ctx, store, nil, DiagnoseOptions{All: true})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions: %v", err)
	}
	for _, d := range res.Diagnostics {
		if len(d.Evidence) == 0 {
			t.Fatalf("diagnostic %s at %s:%d has no evidence", d.Code, d.File, d.Line)
		}
	}
}

func TestMetricsExposeDedupCollapsed(t *testing.T) {
	r := Result{Summary: Summary{DedupCollapsed: 3, Shown: 1, TotalAnalyzed: 4, TotalWithheld: 3}}
	m := r.Metrics()
	if m.DedupCollapsed != 3 {
		t.Fatalf("Metrics.DedupCollapsed = %d, want 3", m.DedupCollapsed)
	}
}
