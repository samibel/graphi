package coverage

import (
	"testing"
)

// TestCheckStableTier_RealMatrix is the production assertion for SCOPE-01: the
// checked-in matrix tags exactly the 12 frozen operations `tier: stable`. This is
// the same invariant `go run ./cmd/coverage -check` and the CI gate enforce.
func TestCheckStableTier_RealMatrix(t *testing.T) {
	rep := CheckStableTier(loadRealMatrix(t))
	if !rep.Pass() {
		t.Fatalf("checked-in matrix stable tier != the frozen 12:\n%s", rep.Format())
	}
	if rep.Count != rep.Want || rep.Want != 12 {
		t.Fatalf("stable set size = %d (want %d, canonical want %d)", rep.Count, rep.Want, 12)
	}
}

func TestCheckMCPDefaultProfile_RealMatrix(t *testing.T) {
	rep := CheckMCPDefaultProfile(loadRealMatrix(t))
	if !rep.Pass() {
		t.Fatalf("checked-in MCP profile != StableMCPToolNames:\n%s", rep.Format())
	}
	if rep.Count != 11 || rep.Want != 11 {
		t.Fatalf("default MCP profile size = %d, want %d (canonical %d)", rep.Count, 11, rep.Want)
	}
}

// The global 12-id check cannot catch a stable operation represented on the
// wrong surface. Reproduce the former impact bug: demote MCP impact and promote
// analyzer impact. The global set still passes; the profile guard must fail.
func TestCheckMCPDefaultProfile_ImpactCannotMoveToLabs(t *testing.T) {
	matrix := loadRealMatrix(t)
	for i := range matrix {
		switch {
		case matrix[i].Category == CategoryMCPTool && matrix[i].ID == "impact":
			matrix[i].Tier = TierLabs
		case matrix[i].Category == CategoryAnalyzer && matrix[i].ID == "impact":
			matrix[i].Tier = TierStable
		}
	}
	if rep := CheckStableTier(matrix); !rep.Pass() {
		t.Fatalf("fixture must preserve the global 12-id invariant:\n%s", rep.Format())
	}
	rep := CheckMCPDefaultProfile(matrix)
	if rep.Pass() || !contains(rep.Missing, "impact") {
		t.Fatalf("MCP profile guard missed stable impact demotion:\n%s", rep.Format())
	}
}

// TestCheckStableTier_ThirteenthFails proves the guard bites: adding a 13th
// stable row (a non-canonical id tagged stable) fails the invariant.
func TestCheckStableTier_ThirteenthFails(t *testing.T) {
	matrix := append(loadRealMatrix(t), Capability{
		ID: "compound", Category: CategoryMCPTool, Status: StatusShipped, Tier: TierStable,
	})
	rep := CheckStableTier(matrix)
	if rep.Pass() {
		t.Fatal("expected FAIL for a 13th stable entry, got PASS")
	}
	if !capsContain(rep.Extra, CategoryMCPTool, "compound") {
		t.Fatalf("expected 'compound' in Extra, got:\n%s", rep.Format())
	}
}

// TestCheckStableTier_MissingFails proves a dropped stable op fails: retag one of
// the frozen 12 to labs and the invariant reports it missing.
func TestCheckStableTier_MissingFails(t *testing.T) {
	matrix := loadRealMatrix(t)
	demoted := false
	for i := range matrix {
		if matrix[i].Category == CategoryMCPTool && matrix[i].ID == "search" && matrix[i].Tier == TierStable {
			matrix[i].Tier = TierLabs
			demoted = true
			break
		}
	}
	if !demoted {
		t.Fatal("fixture precondition failed: no stable mcp-tool row 'search' to demote")
	}
	rep := CheckStableTier(matrix)
	if rep.Pass() {
		t.Fatal("expected FAIL for a missing stable op, got PASS")
	}
	if !contains(rep.Missing, "search") {
		t.Fatalf("expected 'search' in Missing, got:\n%s", rep.Format())
	}
}

// TestCheckStableTier_DuplicateStableIDFails proves that tagging the SAME op
// stable twice (e.g. both the mcp-tool and cli-subcommand 'search' rows) is a
// 13th entry by count, even though the id-set is unchanged.
func TestCheckStableTier_DuplicateStableIDFails(t *testing.T) {
	matrix := append(loadRealMatrix(t), Capability{
		ID: "search", Category: CategoryCLI, Status: StatusShipped, Tier: TierStable,
	})
	rep := CheckStableTier(matrix)
	if rep.Pass() {
		t.Fatalf("expected FAIL for a duplicate stable id (count %d != %d), got PASS", rep.Count, rep.Want)
	}
}

// TestCanonicalStableOps_Twelve pins the canonical set exposed for cross-surface
// agreement.
func TestCanonicalStableOps_Twelve(t *testing.T) {
	if got := CanonicalStableOps(); len(got) != 12 {
		t.Fatalf("CanonicalStableOps() = %d ops, want 12: %v", len(got), got)
	}
}
