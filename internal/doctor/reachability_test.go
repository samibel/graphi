package doctor

import (
	"context"
	"strings"
	"testing"
)

// SW-248 — `graphi doctor` must not report configuration as if it were
// capability.
//
// The shipped v0.11.0 readout was two true lines that together misled:
//
//	[executor-seam] 10 migrated operation(s): 0 legacy, 10 shadow, 0 active
//	[executor-divergence] UNKNOWN: no dual-run observation has been recorded
//
// The first counts what is configured, the second reports what was recorded,
// and neither says that a client bound to the shipped default cannot reach one
// of the ten — so the pair reads as "armed and waiting" when the truth is
// "armed and unreachable here".

// shippedShape is the real v0.11.0 configuration in miniature: two operations
// in shadow on the compiled-in default, neither of them advertised by the
// profile a stock install binds.
func shippedShape() []ExecutorSeamPosition {
	return []ExecutorSeamPosition{
		{
			Operation: "dead_code", Mode: "shadow", EnvVar: "GRAPHI_CANARY_DEAD_CODE",
			ReachableVia: []string{"graphi mcp -labs"}, InDefaultProfile: false,
			ReachEvaluated: true, DefaultProfile: "graphi mcp",
		},
		{
			Operation: "repo_overview", Mode: "shadow", EnvVar: "GRAPHI_CANARY_REPO_OVERVIEW",
			ReachableVia: []string{"graphi mcp -labs"}, InDefaultProfile: false,
			ReachEvaluated: true, DefaultProfile: "graphi mcp",
		},
	}
}

// TestSW248_SeamCheckDoesNotStopAtTheModeCounts is AC-2. The counts stay
// exactly where they were — the executor-rollback workflow reads them — and the
// sentence they used to be the whole of now continues.
func TestSW248_SeamCheckDoesNotStopAtTheModeCounts(t *testing.T) {
	res := ExecutorSeamCheck(shippedShape(), nil).Run(context.Background(), fakeEnv{})
	if !strings.Contains(res.Message, "0 legacy, 2 shadow, 0 active") {
		t.Fatalf("the mode counts moved; SW-248 appends to them and replaces nothing: %q", res.Message)
	}
	if !strings.Contains(res.Message, "NONE of the 2 dual-running operation(s) is reachable through `graphi mcp`") {
		t.Fatalf("the message states the configuration and not its reachability: %q", res.Message)
	}
	if !strings.Contains(res.Detail, "dead_code: shadow (compiled-in default), NOT in the default profile; reachable via graphi mcp -labs") {
		t.Errorf("the per-operation detail does not carry reachability:\n%s", res.Detail)
	}
	if !strings.Contains(res.Detail, "NOT ONE of the 2 dual-running operation(s) is advertised by `graphi mcp`") {
		t.Errorf("the detail does not spell out the all-unreachable case:\n%s", res.Detail)
	}
	// The shipped configuration must stay INFO. WARNing on a stock install's
	// own default is what trains an operator to ignore the check, which SW-244
	// spent a paragraph avoiding and this story does not get to undo.
	if res.Status != StatusInfo {
		t.Errorf("the shipped configuration reports %q, want %q", res.Status, StatusInfo)
	}
}

// TestSW248_SeamCheckWarnsWhenNothingReachesADualRunningOperation is the one
// reachability finding that IS a health failure of the build: an operation that
// pays the dual run and can never be observed by anyone.
func TestSW248_SeamCheckWarnsWhenNothingReachesADualRunningOperation(t *testing.T) {
	positions := append(shippedShape(), ExecutorSeamPosition{
		Operation: "orphan_op", Mode: "shadow", EnvVar: "GRAPHI_CANARY_ORPHAN_OP",
		ReachEvaluated: true, DefaultProfile: "graphi mcp",
	})
	res := ExecutorSeamCheck(positions, nil).Run(context.Background(), fakeEnv{})
	if res.Status != StatusWarn {
		t.Fatalf("an operation no profile reaches reports %q, want %q", res.Status, StatusWarn)
	}
	if !strings.Contains(res.Message, "orphan_op") || !strings.Contains(res.Message, "NO shipped profile") {
		t.Errorf("the warning does not name the unreachable operation: %q", res.Message)
	}
	if !strings.Contains(res.Detail, "orphan_op: shadow (compiled-in default), reachable through NO shipped profile") {
		t.Errorf("the detail does not say the operation is unreachable:\n%s", res.Detail)
	}
}

// TestSW248_SeamCheckSaysSoWhenReachabilityWasNotEvaluated closes the direction
// the fix could have failed in. Rendering "reachable: none" for an answer nobody
// computed would be the same defect with the sign flipped.
func TestSW248_SeamCheckSaysSoWhenReachabilityWasNotEvaluated(t *testing.T) {
	res := ExecutorSeamCheck([]ExecutorSeamPosition{
		{Operation: "dead_code", Mode: "shadow", EnvVar: "GRAPHI_CANARY_DEAD_CODE"},
	}, nil).Run(context.Background(), fakeEnv{})
	if strings.Contains(res.Message, "reachable") {
		t.Errorf("the message claims a reachability it was never given: %q", res.Message)
	}
	if !strings.Contains(res.Detail, "reachability not evaluated") {
		t.Errorf("the detail does not disclose that reachability is unknown:\n%s", res.Detail)
	}
	if strings.Contains(res.Detail, "reachable through NO shipped profile") {
		t.Errorf("an unevaluated readout renders as unreachable:\n%s", res.Detail)
	}
}

// TestSW248_SeamCheckReportsAReachableDualRun is the positive control. Without
// it the check could pass every case above by saying "unreachable" always.
func TestSW248_SeamCheckReportsAReachableDualRun(t *testing.T) {
	res := ExecutorSeamCheck([]ExecutorSeamPosition{
		{
			Operation: "search", Mode: "shadow", EnvVar: "GRAPHI_CANARY_SEARCH",
			ReachableVia: []string{"graphi mcp", "graphi mcp -labs"}, InDefaultProfile: true,
			ReachEvaluated: true, DefaultProfile: "graphi mcp",
		},
	}, nil).Run(context.Background(), fakeEnv{})
	if !strings.Contains(res.Message, "1 of 1 dual-running operation(s) reachable through `graphi mcp`") {
		t.Errorf("a reachable dual run is not reported as reachable: %q", res.Message)
	}
	if !strings.Contains(res.Detail, "search: shadow (compiled-in default), reachable via graphi mcp | graphi mcp -labs") {
		t.Errorf("the detail does not name the profiles that reach it:\n%s", res.Detail)
	}
	if strings.Contains(res.Detail, "NOT ONE of the") {
		t.Errorf("a reachable seam gets the unreachable paragraph:\n%s", res.Detail)
	}
}

// TestSW248_DivergenceCheckDistinguishesTheThreeShapes is AC-3 and AC-4 at the
// doctor boundary: the same three conditions the divergence renderer separates
// must be separate here too, because `graphi doctor` is what an operator runs
// first and often the only thing they read.
func TestSW248_DivergenceCheckDistinguishesTheThreeShapes(t *testing.T) {
	base := ExecutorDivergence{
		Directory:      "/state/executor-divergence",
		ReachEvaluated: true,
		DefaultProfile: "graphi mcp",
		SeamOperations: 2,
	}

	// SHAPE 1 — reachable, simply not called yet.
	one := base
	one.State = "PARTIAL-UNKNOWN"
	one.Observations = 4
	one.ReachableInDefault = 2
	one.Unobserved = []string{"waiting_op"}
	one.UnobservedReachable = []string{"waiting_op"}
	resOne := ExecutorDivergenceCheck(one, nil).Run(context.Background(), fakeEnv{})

	// SHAPE 2 — one observed, one the bound profile cannot reach.
	two := base
	two.State = "PARTIAL-UNKNOWN"
	two.Observations = 4
	two.ReachableInDefault = 1
	two.Unobserved = []string{"labs_only_op"}
	two.UnobservedOptIn = []string{"labs_only_op"}
	resTwo := ExecutorDivergenceCheck(two, nil).Run(context.Background(), fakeEnv{})

	// SHAPE 3 — the stock v0.11.0 condition: empty, and nothing reachable.
	three := base
	three.State = "UNKNOWN-AND-UNOBSERVABLE"
	three.Unobserved = []string{"dead_code", "repo_overview"}
	three.UnobservedOptIn = []string{"dead_code", "repo_overview"}
	three.Unfillable = true
	resThree := ExecutorDivergenceCheck(three, nil).Run(context.Background(), fakeEnv{})

	t.Logf("SHAPE 1 message: %s\nSHAPE 1 detail:\n%s", resOne.Message, resOne.Detail)
	t.Logf("SHAPE 2 message: %s\nSHAPE 2 detail:\n%s", resTwo.Message, resTwo.Detail)
	t.Logf("SHAPE 3 message: %s\nSHAPE 3 action: %s\nSHAPE 3 detail:\n%s",
		resThree.Message, resThree.Action, resThree.Detail)

	if !strings.Contains(resOne.Detail, "never observed YET, but reachable through `graphi mcp`") {
		t.Errorf("shape 1 does not say a call would record one:\n%s", resOne.Detail)
	}
	if strings.Contains(resOne.Detail, "NOT OBSERVABLE") {
		t.Errorf("shape 1 borrows shape 2's wording:\n%s", resOne.Detail)
	}
	if !strings.Contains(resTwo.Detail, `NOT OBSERVABLE through `+"`graphi mcp`"+` (UNKNOWN here means "not possible", not "not yet"): labs_only_op`) {
		t.Errorf("shape 2 does not distinguish 'not possible' from 'not yet':\n%s", resTwo.Detail)
	}
	if !strings.Contains(resTwo.Message, "none of which `graphi mcp` can reach") {
		t.Errorf("shape 2's headline hides the reachability finding in the detail: %q", resTwo.Message)
	}
	if !strings.Contains(resThree.Message, "UNKNOWN and UNFILLABLE") {
		t.Errorf("shape 3 reports an unfillable record as merely empty: %q", resThree.Message)
	}
	if !strings.Contains(resThree.Message, `NOT "not yet"`) {
		t.Errorf("shape 3 leaves 'wait for it' as a reading: %q", resThree.Message)
	}
	if !strings.Contains(resThree.Message, "NOT a statement that the two paths agree") {
		t.Errorf("shape 3 drops the honesty rule while adding the new one: %q", resThree.Message)
	}
	if resThree.Action == "" {
		t.Error("shape 3 tells an operator the record cannot fill and not what to do about it")
	}
	if resOne.Message == resTwo.Message || resTwo.Message == resThree.Message || resOne.Message == resThree.Message {
		t.Errorf("two of the three shapes produce the same headline:\n1: %s\n2: %s\n3: %s",
			resOne.Message, resTwo.Message, resThree.Message)
	}
	for _, res := range []CheckResult{resOne, resTwo, resThree} {
		if res.Status == StatusPass {
			t.Errorf("an unobserved operation reads PASS: %q", res.Message)
		}
	}
}

// TestSW248_AnEmptyRecordThatCanFillKeepsTheOldWording pins the narrowness of
// AC-4's new state. If every empty record started claiming to be unfillable,
// the new sentence would be worth as little as the one it replaced.
func TestSW248_AnEmptyRecordThatCanFillKeepsTheOldWording(t *testing.T) {
	res := ExecutorDivergenceCheck(ExecutorDivergence{
		State:              "UNKNOWN",
		Directory:          "/state/executor-divergence",
		ReachEvaluated:     true,
		DefaultProfile:     "graphi mcp",
		SeamOperations:     1,
		ReachableInDefault: 1,
		Unobserved:         []string{"waiting_op"},
		// Unfillable stays false: the profile reaches it, so a call fills it.
	}, nil).Run(context.Background(), fakeEnv{})
	if strings.Contains(res.Message, "UNFILLABLE") {
		t.Fatalf("an empty but fillable record is reported as unfillable: %q", res.Message)
	}
	if !strings.Contains(res.Message, "NOT a statement that the two paths agree") {
		t.Errorf("the original honesty rule was lost: %q", res.Message)
	}
}

// TestSW248_UnreachableEverywhereIsReportedAsABuildDefect keeps the doctor
// readout level with the CI gate: what cmd/seamreach refuses to merge, an
// already-built binary must be able to say out loud.
func TestSW248_UnreachableEverywhereIsReportedAsABuildDefect(t *testing.T) {
	res := ExecutorDivergenceCheck(ExecutorDivergence{
		State:             "UNKNOWN-AND-UNOBSERVABLE",
		Directory:         "/state/executor-divergence",
		ReachEvaluated:    true,
		DefaultProfile:    "graphi mcp",
		SeamOperations:    1,
		Unobserved:        []string{"orphan_op"},
		UnobservedNowhere: []string{"orphan_op"},
		Unfillable:        true,
	}, nil).Run(context.Background(), fakeEnv{})
	if !strings.Contains(res.Detail, "NOT REACHABLE through ANY shipped profile — nothing can ever observe these: orphan_op") {
		t.Fatalf("the detail does not report the strongest true statement:\n%s", res.Detail)
	}
	if strings.Contains(res.Detail, "NOT OBSERVABLE through `graphi mcp` (UNKNOWN here means") {
		t.Errorf("the weaker sentence is printed beside the stronger one:\n%s", res.Detail)
	}
}
