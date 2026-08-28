package main

import (
	"os"
	"strings"
	"testing"

	"github.com/samibel/graphi/cmd/internal/runtime"
	"github.com/samibel/graphi/surfaces/client"
)

const rollbackDocPath = "../../docs/executor-seam-rollback.md"
const rollbackWorkflowPath = "../../.github/workflows/executor-rollback.yml"

// SW-232 AC-4: the operator rollback page covers EVERY operation currently on
// the executor path, by id and by the variable that rolls it back.
//
// It is a test and not a review note because the failure mode is silent: a
// future story that migrates an eleventh operation would ship a page that reads
// complete and is not. The migrated set is the source of truth, so the page is
// checked against it rather than against a copy of it.
func TestSW232_RollbackDocCoversEveryMigratedOperation(t *testing.T) {
	page := readRollbackDoc(t)
	migrated := client.MigratedOperations()
	if len(migrated) == 0 {
		t.Fatal("no migrated operations; this check is vacuous")
	}
	for _, op := range migrated {
		if !strings.Contains(page, op) {
			t.Errorf("%s does not mention the migrated operation %q", rollbackDocPath, op)
		}
		if envVar := runtime.EnvCanaryModeFor(op); !strings.Contains(page, envVar) {
			t.Errorf("%s does not name %q, the variable that rolls %q back", rollbackDocPath, envVar, op)
		}
	}
	if !strings.Contains(page, runtime.EnvCanaryModeAll) {
		t.Errorf("%s does not document the whole-seam switch %q", rollbackDocPath, runtime.EnvCanaryModeAll)
	}
}

// AC-4: the page answers the four questions the acceptance criterion names —
// how to force legacy, what the switch's scope is, how to verify it took
// effect, and how to get back. Each is checked by the phrase a reader would
// look for, so a page that lost a section fails rather than merely reading
// thinner.
func TestSW232_RollbackDocAnswersTheOperatorQuestions(t *testing.T) {
	page := readRollbackDoc(t)
	for _, want := range []struct {
		what   string
		phrase string
	}{
		{"how to force legacy", "GRAPHI_CANARY_ALL=legacy"},
		{"the scope of the switch", "Per process, not global"},
		{"that a running server must be restarted", "Restart it"},
		{"how to verify it took effect", "graphi doctor"},
		{"how to return to the prior setting", "unset GRAPHI_CANARY_ALL"},
		{"how to read the divergence record", "graphi doctor -divergence"},
		{"that UNKNOWN is not parity", "not a statement that the two paths agree"},
		{"which CI leg exercises this", "executor-rollback.yml"},
	} {
		if !strings.Contains(page, want.phrase) {
			t.Errorf("%s does not say %s (looked for %q)", rollbackDocPath, want.what, want.phrase)
		}
	}
	// The page must not tell an operator to flip the shipped default to the
	// dual-run position: that is a release decision, deliberately not this
	// page's advice (SW-232 out of scope).
	if strings.Contains(page, "GRAPHI_CANARY_ALL=shadow graphi mcp\n") {
		t.Error("the rollback page recommends running the shipped install in shadow")
	}
}

// TestSW244_RollbackDocStatesTheShippedDefault is AC-7 of SW-244.
//
// An operator opens this page mid-incident, and the first thing they need from
// it is where they are STARTING. A page that names the wrong shipped position
// is worse than no page: it tells them a rollback is a no-op when it is not, or
// that unsetting a variable returns them somewhere it does not.
//
// The check is written against client's compiled-in default rather than against
// the literal `shadow`, so the NEXT story to move that constant fails here and
// has to correct the page in the same commit — which is the failure this test
// exists to force, and the one SW-244 itself found the page in.
func TestSW244_RollbackDocStatesTheShippedDefault(t *testing.T) {
	page := readRollbackDoc(t)
	client.ResetCanaryModes()
	t.Cleanup(client.ResetCanaryModes)
	shipped := string(client.CanaryModeDefault())

	// The positions table has to mark the right row, and only that row.
	marked := "| `" + shipped + "` **(shipped default)** |"
	if !strings.Contains(page, marked) {
		t.Errorf("%s does not mark %q as the shipped default in its positions table "+
			"(looked for %q)", rollbackDocPath, shipped, marked)
	}
	for _, other := range client.CanaryModes() {
		if string(other) == shipped {
			continue
		}
		if stale := "| `" + string(other) + "` **(shipped default)** |"; strings.Contains(page, stale) {
			t.Errorf("%s still marks %q as the shipped default", rollbackDocPath, other)
		}
	}

	// And §3's scope note, which is the sentence an operator acts on: what they
	// get when no variable is set.
	if want := "Unset is `" + shipped + "`"; !strings.Contains(page, want) {
		t.Errorf("%s does not tell an operator what an unset switch gives them "+
			"(looked for %q)", rollbackDocPath, want)
	}

	// The verification section's example readout must show the shipped
	// configuration, not a rolled-back one — it is what a reader compares their
	// own `graphi doctor` output against.
	if want := ": " + shipped + " (compiled-in default)"; !strings.Contains(page, want) {
		t.Errorf("%s's `graphi doctor` example does not show the shipped position "+
			"(looked for %q)", rollbackDocPath, want)
	}
}

// AC-5: the CI leg the page points at exists, moves the switch, runs the
// suites in that position, and asserts the round trip. A page promising an
// exercised rollback beside a workflow that does not exercise it would be worse
// than no promise.
func TestSW232_RollbackWorkflowExercisesTheSwitch(t *testing.T) {
	raw, err := os.ReadFile(rollbackWorkflowPath)
	if err != nil {
		t.Fatalf("read %s: %v", rollbackWorkflowPath, err)
	}
	yaml := string(raw)
	for _, want := range []struct {
		what   string
		phrase string
	}{
		{"it runs on pull requests", "pull_request:"},
		{"it forces the kill switch to legacy", runtime.EnvCanaryModeAll + ": \"legacy\""},
		{"it runs the surface parity suites", "go test ./surfaces/"},
		{"it runs the characterization suites", "Characterization"},
		{"it reads the divergence record without a server", "doctor -divergence --json"},
		{"it asserts the round trip back", "round-trip"},
	} {
		if !strings.Contains(yaml, want.phrase) {
			t.Errorf("%s does not prove %s (looked for %q)", rollbackWorkflowPath, want.what, want.phrase)
		}
	}
	// The leg must not leave the switch flipped for anything downstream, and it
	// must not be the place someone quietly changes the shipped default.
	if strings.Contains(yaml, "GRAPHI_CANARY_ALL: \"shadow\"") && !strings.Contains(yaml, "shadow-observability") {
		t.Error("the rollback leg installs shadow outside its labelled step")
	}
}

// TestSW245_RollbackDocStatesTheNewCostProfile is AC-7 of SW-245.
//
// The page's §2 used to tell an operator that `shadow` costs "about 2.0× legacy
// in latency, CPU and allocations". Half of that is now wrong and the wrong half
// is the one they act on: the caller no longer waits for the second path, while
// the CPU and the allocations are exactly where they were. A page that kept the
// old sentence would have an operator reclaiming latency that is not there by
// rolling back a comparison that costs them nothing at the median — and a page
// that replaced it with "shadow is free" would have them ignore a real 2× in
// allocation on a host that has no headroom.
//
// So the check is two-sided on purpose: the page must state BOTH that the caller
// stopped waiting and that the machine did not stop paying.
func TestSW245_RollbackDocStatesTheNewCostProfile(t *testing.T) {
	page := readRollbackDoc(t)
	for _, want := range []struct {
		what   string
		phrase string
	}{
		{"that the second path is off the request thread", "does **not**\nrun on the thread that serves your request"},
		{"what the caller now waits for", "0.973× legacy"},
		{"what it used to be", "**2.05×** at p50 before SW-245"},
		{"that the CPU and allocation cost did not go away", "unchanged at about 2.0× legacy"},
		{"the saturated-host figure", "1.89×"},
		{"that the deferral queue is bounded", "at most 64 comparisons"},
		{"that a lost comparison is not agreement", "never\nevidence of agreement"},
	} {
		if !strings.Contains(page, want.phrase) {
			t.Errorf("%s does not state %s (looked for %q)", rollbackDocPath, want.what, want.phrase)
		}
	}
	// The superseded claim must be gone, not merely contradicted further down.
	if strings.Contains(page, "**about 2.0×\nlegacy** in latency, CPU and allocations") {
		t.Errorf("%s still claims shadow costs 2.0× in LATENCY; SW-245 removed that half of "+
			"the cost and an operator reading it would roll back to reclaim nothing", rollbackDocPath)
	}
}

// AC-4 of SW-245: the page teaches an operator to read the coverage line, names
// both causes of a skipped comparison, and states that graphi does not sample —
// because "SKIPPED: 0" only means something if the reader knows nothing was
// dropped on purpose.
func TestSW245_RollbackDocExplainsCoverage(t *testing.T) {
	page := readRollbackDoc(t)
	for _, want := range []struct {
		what   string
		phrase string
	}{
		{"the coverage line", "coverage:"},
		{"the three per-operation numbers", "**SKIPPED**"},
		{"the load cause", "`queue-full`"},
		{"the shutdown cause", "`drain-abandoned`"},
		{"that graphi does not sample", "**There is no sampling.**"},
		{"that partial coverage is not a doctor PASS", "refuses to report **PASS** while"},
	} {
		if !strings.Contains(page, want.phrase) {
			t.Errorf("%s does not explain %s (looked for %q)", rollbackDocPath, want.what, want.phrase)
		}
	}
}

func readRollbackDoc(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(rollbackDocPath)
	if err != nil {
		t.Fatalf("read %s: %v", rollbackDocPath, err)
	}
	return string(raw)
}
