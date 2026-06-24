package coverage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
)

func loadRealMatrix(t *testing.T) []Capability {
	t.Helper()
	root, err := ModuleRoot()
	if err != nil {
		t.Fatalf("ModuleRoot: %v", err)
	}
	caps, err := LoadMatrix(filepath.Join(root, filepath.FromSlash(MatrixYAMLPath)))
	if err != nil {
		t.Fatalf("LoadMatrix: %v", err)
	}
	return caps
}

// TestCheck_RealMatrixMatchesLive is the production assertion: the checked-in
// matrix matches the live registries (AC-3, AC-4 happy path). This is the same
// check `go run ./cmd/coverage -check` and the CI gate run.
func TestCheck_RealMatrixMatchesLive(t *testing.T) {
	rep := Check(mustEnumerate(t), loadRealMatrix(t))
	if !rep.Pass() {
		t.Fatalf("checked-in matrix has drifted from live code:\n%s", rep.Format())
	}
}

// TestCheck_FakeCapabilityFailsThenPasses demonstrates the guard biting back:
// an undocumented live capability is reported as drift, and adding its matrix
// row clears it (AC-5). It also proves, via a throwaway analysis.Registry, that
// a newly-registered analyzer WOULD surface through Names() — i.e. the live
// enumerator picks up real additions — without committing a fake row.
func TestCheck_FakeCapabilityFailsThenPasses(t *testing.T) {
	// Proof that a registered analyzer is observable via the same Names() seam
	// the live enumerator uses: register a fake into a throwaway registry.
	reg := analysis.NewRegistry()
	if err := reg.Register(fakeAnalyzer{}); err != nil {
		t.Fatalf("register fake analyzer: %v", err)
	}
	if !contains(reg.Names(), "fake-analyzer") {
		t.Fatalf("registry.Names() did not surface the registered fake analyzer: %v", reg.Names())
	}

	// Simulate that fake analyzer being live by injecting it into a copy of the
	// live set, then check against the REAL (un-updated) matrix.
	live := mustEnumerate(t)
	live.Analyzers = append(sortedCopy(live.Analyzers), "fake-analyzer")
	live.Analyzers = sortedCopy(live.Analyzers)

	matrix := loadRealMatrix(t)
	rep := Check(live, matrix)
	if rep.Pass() {
		t.Fatal("expected drift for undocumented live analyzer 'fake-analyzer', got PASS")
	}
	if !capsContain(rep.MissingFromMatrix, CategoryAnalyzer, "fake-analyzer") {
		t.Fatalf("expected fake-analyzer in MissingFromMatrix, got: %s", rep.Format())
	}

	// Now document it: add the matrix row and re-check → PASS.
	matrix = append(matrix, Capability{
		ID: "fake-analyzer", Category: CategoryAnalyzer, Status: StatusShipped, Epic: "EP-TEST",
	})
	if rep := Check(live, matrix); !rep.Pass() {
		t.Fatalf("expected PASS after documenting fake-analyzer, got:\n%s", rep.Format())
	}
}

// TestCheck_PhantomShipped: a matrix row marked shipped for a non-live id.
func TestCheck_PhantomShipped(t *testing.T) {
	live := LiveSet{Parsers: []string{"go"}}
	matrix := []Capability{
		{ID: "go", Category: CategoryParser, Status: StatusShipped},
		{ID: "cobol", Category: CategoryParser, Status: StatusShipped}, // phantom
	}
	rep := Check(live, matrix)
	if rep.Pass() || !capsContain(rep.PhantomShipped, CategoryParser, "cobol") {
		t.Fatalf("expected phantom 'cobol', got:\n%s", rep.Format())
	}
}

// TestCheck_MislabeledPlanned: a live id marked planned in the matrix.
func TestCheck_MislabeledPlanned(t *testing.T) {
	live := LiveSet{Analyzers: []string{"impact"}}
	matrix := []Capability{
		{ID: "impact", Category: CategoryAnalyzer, Status: StatusPlanned}, // live but planned
	}
	rep := Check(live, matrix)
	if rep.Pass() || !capsContain(rep.MislabeledPlanned, CategoryAnalyzer, "impact") {
		t.Fatalf("expected mislabeled 'impact', got:\n%s", rep.Format())
	}
}

// TestCheck_PlannedNonLiveIsClean: a planned row for a genuinely absent capability
// (e.g. the deferred html parser) is NOT drift.
func TestCheck_PlannedNonLiveIsClean(t *testing.T) {
	live := LiveSet{Parsers: []string{"go"}}
	matrix := []Capability{
		{ID: "go", Category: CategoryParser, Status: StatusShipped},
		{ID: "html", Category: CategoryParser, Status: StatusPlanned},
	}
	if rep := Check(live, matrix); !rep.Pass() {
		t.Fatalf("planned non-live parser should be clean, got:\n%s", rep.Format())
	}
}

// TestCheck_InformationalRowsIgnored: feature-unit rows are not live-checked.
func TestCheck_InformationalRowsIgnored(t *testing.T) {
	live := LiveSet{Parsers: []string{"go"}}
	matrix := []Capability{
		{ID: "go", Category: CategoryParser, Status: StatusShipped},
		{ID: "FU-1", Category: "feature-unit", Status: StatusShipped}, // not code-derived → ignored
	}
	if rep := Check(live, matrix); !rep.Pass() {
		t.Fatalf("informational row should be ignored, got:\n%s", rep.Format())
	}
}

type fakeAnalyzer struct{}

func (fakeAnalyzer) Name() string { return "fake-analyzer" }
func (fakeAnalyzer) Analyze(context.Context, query.Reader, analysis.Params) (analysis.Analysis, error) {
	return analysis.Analysis{}, nil
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

func capsContain(caps []Capability, cat, id string) bool {
	for _, c := range caps {
		if c.Category == cat && c.ID == id {
			return true
		}
	}
	return false
}
