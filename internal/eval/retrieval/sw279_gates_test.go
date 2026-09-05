package retrieval

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestSW279_ScriptGates runs the Python gate suite for the SW-279 Phase 2 harvest
// scripts, so the checks that keep the dataset honest are on the same gate as the
// dataset itself rather than in a command someone has to remember to run.
//
// The suite lives at scripts/eval/tests/test_sw279_gates.py: 51 cases, of which 43 break
// exactly one thing and assert the script refuses, and 8 are positive controls that
// reproduce a committed artefact byte for byte or accept a legitimate variation — a gate
// that always fails would otherwise look identical to one that works.
//
// # Why this wrapper does more than look for the word "ok"
//
// Round 1 asserted only that the output contained "... ok" somewhere. With the pinned
// spf13/cobra clone unavailable, all of the finalizer cases skipped, the remaining cases
// printed "ok", and `go test` returned 0 — a wrapper reporting PASS over a skipped
// population, which is precisely the SW-273 defect class these gates exist to prevent.
//
// So the wrapper asserts a contract instead:
//
//   - the suite's own declared case count must equal wantCases below. Two independent
//     declarations, one in Python and one here, so deleting or renaming a case fails the
//     build rather than quietly shrinking the evidence;
//   - every declared case must have run;
//   - a skip is sanctioned ONLY when the case is one of cobraDependentCases AND the pinned
//     clone is genuinely absent from this machine. Any other skip is a failure;
//   - a sanctioned skip does not pass. The test SKIPS, naming what did not run, because a
//     partial gate run is a non-result and must not be reported as a green gate.
const wantCases = 51

// The exact cases that read the pinned spf13/cobra checkout. They may skip, and only they,
// and only when that checkout is missing. Written out rather than pattern-matched, so a new
// case cannot join the skippable set by being named like one.
var cobraDependentCases = map[string]bool{
	"__main__.UnresolvedRowsBlockAndAreNeverConverted.test_an_unresolved_row_blocks_completion_and_keeps_its_verdict":                      true,
	"__main__.UnresolvedRowsBlockAndAreNeverConverted.test_the_completing_run_reproduces_the_committed_ledger_byte_for_byte":               true,
	"__main__.TheIndependenceGuardFiresOnRejectionRows.test_one_actor_annotating_and_reviewing_a_rejection_is_refused":                     true,
	"__main__.TheIndependenceGuardFiresOnRejectionRows.test_a_reviewer_the_plan_did_not_pair_with_this_annotator_is_refused":               true,
	"__main__.TheIndependenceGuardFiresOnRejectionRows.test_a_judgement_claiming_another_annotator_is_refused":                             true,
	"__main__.TheIndependenceGuardFiresOnRejectionRows.test_a_rejection_whose_note_cites_a_file_absent_at_the_pin_is_refused":              true,
	"__main__.TheIndependenceGuardFiresOnRejectionRows.test_a_rejection_citing_a_path_outside_the_old_extension_allowlist_is_refused":      true,
	"__main__.TheReannotationPassMustBeFreshAndAttested.test_pointing_the_superseding_slot_at_an_actor_the_plan_did_not_assign_is_refused": true,
	"__main__.TheReannotationPassMustBeFreshAndAttested.test_a_superseding_annotator_that_already_annotated_the_row_is_refused":            true,
	"__main__.TheReannotationPassMustBeFreshAndAttested.test_a_superseding_reviewer_that_already_reviewed_the_row_is_refused":              true,
	"__main__.TheReannotationPassMustBeFreshAndAttested.test_an_attested_output_digest_that_is_not_the_committed_bytes_is_refused":         true,
	"__main__.TheReannotationPassMustBeFreshAndAttested.test_an_attested_input_digest_that_is_not_the_committed_bytes_is_refused":          true,
	"__main__.TheFinalizerBoundsTheReRollChannel.test_superseding_a_row_that_is_not_unresolved_is_refused":                                 true,
	"__main__.TheFinalizerBoundsTheReRollChannel.test_a_second_supersession_of_the_same_row_is_refused":                                    true,
	"__main__.TheFinalizerBoundsTheReRollChannel.test_a_third_annotation_of_the_same_row_is_refused":                                       true,
}

// sw279Pin is the spf13/cobra commit the answerability pass was annotated against.
const sw279Pin = "a0a6ae020bb3899ff0276067863e50523f897370"

// gateSummary is the SW279-GATES contract line the Python suite prints last.
type gateSummary struct {
	declared, refusals, positiveControls int
	ran, ok, skipped, failed             int
	skippedCases                         []string
}

func parseGateSummary(out string) (gateSummary, error) {
	var summary gateSummary
	fields := map[string]*int{
		"declared": &summary.declared, "refusals": &summary.refusals,
		"positive_controls": &summary.positiveControls, "ran": &summary.ran,
		"ok": &summary.ok, "skipped": &summary.skipped, "failed": &summary.failed,
	}
	found := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "SW279-GATES-SKIPPED "):
			summary.skippedCases = append(summary.skippedCases,
				strings.TrimSpace(strings.TrimPrefix(line, "SW279-GATES-SKIPPED ")))
		case strings.HasPrefix(line, "SW279-GATES "):
			found = true
			for _, token := range strings.Fields(strings.TrimPrefix(line, "SW279-GATES ")) {
				key, value, split := strings.Cut(token, "=")
				target, known := fields[key]
				if !split || !known {
					continue
				}
				parsed, err := strconv.Atoi(value)
				if err != nil {
					return summary, fmt.Errorf("SW279-GATES %s=%q is not a number", key, value)
				}
				*target = parsed
			}
		}
	}
	if !found {
		return summary, fmt.Errorf("the suite printed no SW279-GATES summary line")
	}
	sort.Strings(summary.skippedCases)
	return summary, nil
}

// pinnedCobraPresent reports whether this machine has the read-only clone the finalizer
// cases need, resolved exactly as the Python suite resolves it.
func pinnedCobraPresent() bool {
	root := os.Getenv("GRAPHI_CORPUS_COBRA")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		root = filepath.Join(home, ".cache", "graphi", "corpus", "cobra")
	}
	if _, err := os.Stat(filepath.Join(root, "command.go")); err != nil {
		return false
	}
	head, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(string(head)), sw279Pin)
}

func TestSW279_ScriptGates(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("SKIP: no python3 on PATH; the SW-279 harvest-script gates did not run")
	}

	root, err := repoRoot()
	if err != nil {
		t.Skipf("SKIP: cannot locate the repository root (%v); the SW-279 harvest-script gates did not run", err)
	}
	suite := filepath.Join(root, "scripts", "eval", "tests", "test_sw279_gates.py")
	if _, err := os.Stat(suite); err != nil {
		t.Fatalf("the SW-279 gate suite is missing at %s: %v", suite, err)
	}

	cmd := exec.Command(python, suite)
	cmd.Dir = root
	out, runErr := cmd.CombinedOutput()
	t.Logf("python3 %s\n%s", suite, out)
	if runErr != nil {
		t.Fatalf("the SW-279 harvest-script gates failed: %v", runErr)
	}

	summary, err := parseGateSummary(string(out))
	if err != nil {
		t.Fatalf("cannot read the SW-279 gate result: %v\n%s", err, out)
	}
	if summary.declared != wantCases {
		t.Fatalf("the SW-279 gate suite declares %d cases, this wrapper expects %d. "+
			"If a case was added or removed on purpose, update wantCases in the same commit; "+
			"the two declarations exist so that a shrinking suite cannot pass quietly.",
			summary.declared, wantCases)
	}
	if summary.ran != summary.declared {
		t.Fatalf("the SW-279 gate suite declares %d cases but ran %d", summary.declared, summary.ran)
	}
	if summary.failed != 0 {
		t.Fatalf("%d SW-279 gate case(s) failed:\n%s", summary.failed, out)
	}
	if len(summary.skippedCases) != summary.skipped {
		t.Fatalf("the SW-279 gate suite reported %d skips but named %d",
			summary.skipped, len(summary.skippedCases))
	}

	if summary.skipped == 0 {
		if summary.ok != summary.declared {
			t.Fatalf("no SW-279 gate case skipped, but only %d of %d passed:\n%s",
				summary.ok, summary.declared, out)
		}
		t.Logf("all %d SW-279 gate cases ran: %d refusals, %d positive controls",
			summary.declared, summary.refusals, summary.positiveControls)
		return
	}

	// Something skipped. The only sanctioned reason is the pinned spf13/cobra clone being
	// absent, and only for the cases that read it. Anything else is a failure, not a skip.
	unsanctioned := make([]string, 0, len(summary.skippedCases))
	for _, id := range summary.skippedCases {
		if !cobraDependentCases[id] {
			unsanctioned = append(unsanctioned, id)
		}
	}
	if len(unsanctioned) > 0 {
		t.Fatalf("SW-279 gate case(s) skipped for a reason this wrapper does not sanction: %s\n%s",
			strings.Join(unsanctioned, ", "), out)
	}
	if pinnedCobraPresent() {
		t.Fatalf("the pinned spf13/cobra clone is present, so no SW-279 gate case may skip, "+
			"yet %d did: %s\n%s", summary.skipped, strings.Join(summary.skippedCases, ", "), out)
	}
	if summary.skipped != len(cobraDependentCases) {
		t.Fatalf("the pinned spf13/cobra clone is absent, so exactly the %d cases that read it "+
			"must skip, but %d did: %s", len(cobraDependentCases), summary.skipped,
			strings.Join(summary.skippedCases, ", "))
	}

	// A partial run is a non-result. It is reported as a SKIP rather than a PASS, because a
	// green gate over a population that never executed is the defect these gates exist to
	// prevent, and a wrapper that swallows it is that defect wearing a test's clothes.
	t.Skipf("SKIP: no spf13/cobra clone at the pin (%s), so the %d finalizer gate cases did not "+
		"run. %d of %d SW-279 gate cases passed; this is NOT a pass for the suite. Clone "+
		"spf13/cobra at the pin, or set GRAPHI_CORPUS_COBRA, to run it whole.",
		sw279Pin, summary.skipped, summary.ok, summary.declared)
}

// repoRoot resolves the module root from go.mod's location.
func repoRoot() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", err
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		return "", os.ErrNotExist
	}
	return filepath.Dir(gomod), nil
}
