package seamreach

import (
	"strings"
	"testing"
)

// TestSW248_LiveSeamSatisfiesTheInvariant is the gate itself, run as a unit
// test so a violation fails an ordinary `go test ./...` as well as the
// dedicated CI leg. The two are not redundant: the leg proves the BINARY works
// (and, with -inject-unreachable, that it goes red), this proves the RULE on
// every run somebody makes locally.
func TestSW248_LiveSeamSatisfiesTheInvariant(t *testing.T) {
	rep := Check(Live())
	if !rep.Pass() {
		t.Fatalf("operation(s) dual-run on the executor seam with no shipped profile that reaches them: %v\n%s",
			rep.Unreachable, rep.Matrix())
	}
}

// TestSW248_GateFailsOnAnUnreachableShadowOperation is AC-5's discriminating
// case. A gate that has never been watched failing is a claim, and this one
// exists because an absent check let a defect ship — so the failure is
// exercised, not asserted.
func TestSW248_GateFailsOnAnUnreachableShadowOperation(t *testing.T) {
	rep := Check(Live().WithUnreachable("demo_unreachable_op"))
	if rep.Pass() {
		t.Fatal("the gate passed a shadow-mode operation that no shipped profile reaches; " +
			"it would not have caught the migration it exists to catch")
	}
	if len(rep.Unreachable) != 1 || rep.Unreachable[0] != "demo_unreachable_op" {
		t.Fatalf("the gate names %v as unreachable, want exactly [demo_unreachable_op]", rep.Unreachable)
	}
	out := rep.Format("")
	if !strings.Contains(out, "seam-reachability check FAILED") {
		t.Errorf("the failing render does not say it failed:\n%s", out)
	}
	if !strings.Contains(out, "demo_unreachable_op") {
		t.Errorf("the failing render does not name the offending operation:\n%s", out)
	}
}

// TestSW248_LegacyOperationsAreNotHeldToTheInvariant pins the rule's scope. An
// operation rolled back to `legacy` runs one path, records nothing and costs
// nothing, so demanding a profile reach it would fail a build for a
// configuration that is the documented remedy during an incident.
func TestSW248_LegacyOperationsAreNotHeldToTheInvariant(t *testing.T) {
	seam := Seam{
		Operations: []Operation{{ID: "rolled_back", Mode: "legacy"}},
		Profiles: []Profile{
			{ID: "mcp-default", Invocation: "graphi mcp", Default: true, Reaches: map[string]bool{}},
		},
	}
	rep := Check(seam)
	if !rep.Pass() {
		t.Fatalf("a legacy operation was held to the reachability invariant: %v", rep.Unreachable)
	}
	if rep.DualRun != 0 {
		t.Fatalf("DualRun = %d for a seam with nothing dual-running", rep.DualRun)
	}
}

// TestSW248_ActiveOperationsAreHeldToTheInvariant closes the other half. The
// rule is written on "not legacy" rather than on "shadow" because `active`
// serves the caller from the executor: an unreachable one is dead surface, and
// a rule that only looked at `shadow` would let it through.
func TestSW248_ActiveOperationsAreHeldToTheInvariant(t *testing.T) {
	seam := Seam{
		Operations: []Operation{{ID: "promoted", Mode: "active"}},
		Profiles: []Profile{
			{ID: "mcp-default", Invocation: "graphi mcp", Default: true, Reaches: map[string]bool{}},
		},
	}
	if Check(seam).Pass() {
		t.Fatal("an ACTIVE operation no profile reaches passed the invariant")
	}
}

// TestSW248_DeclarationMatchesTheLiveMatrix is the second rule: the checked-in
// matrix is the thing a reviewer reads, so it has to be true.
//
// This is the rule that would have caught the defect. The invariant holds today
// and held on the day the defect shipped — `graphi mcp -labs` reaches all ten.
// What was missing was a file whose summary line changes, in the diff, when the
// answer to "can a stock install reach any of this?" changes.
func TestSW248_DeclarationMatchesTheLiveMatrix(t *testing.T) {
	live := Check(Live()).Matrix()
	if live != Declaration() {
		t.Fatalf("internal/seamreach/reachability.txt is stale; regenerate it with "+
			"`go run ./cmd/seamreach -generate` and commit it with the change that caused it.\n"+
			"--- live ---\n%s\n--- declared ---\n%s", live, Declaration())
	}
}

// TestSW248_TheDeclarationStatesTheDefaultProfileReachCount asserts the
// declaration carries the SENTENCE, not merely the data. A matrix a reader has
// to add up themselves would have failed the same way the doctor line did: true
// in every cell, silent about what the cells mean together.
func TestSW248_TheDeclarationStatesTheDefaultProfileReachCount(t *testing.T) {
	rep := Check(Live())
	matrix := rep.Matrix()
	if !strings.Contains(matrix, "dual-running operation(s) are reachable through") {
		t.Fatalf("the declaration does not state how many dual-running operations the default profile reaches:\n%s", matrix)
	}
	if rep.ReachableInDefault == 0 && !strings.Contains(matrix, "records NO dual-run evidence at all") {
		t.Fatalf("the default profile reaches nothing on the seam and the declaration does not say so:\n%s", matrix)
	}
}
