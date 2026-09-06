package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/eval/retrieval"
)

// TestRetrievalTargetsGate_IsWiredToTheCommand is AC-7(b)'s wiring check.
// docs/eval/retrieval-targets.json used to be evaluated by nothing a release
// runs, so a missed target was invisible outside one Go test.
func TestRetrievalTargetsGate_IsWiredToTheCommand(t *testing.T) {
	runner, ok := DefaultGates()["retrieval-targets"]
	if !ok {
		t.Fatal("DefaultGates omits retrieval-targets")
	}
	shell, ok := runner.(*shellRunner)
	if !ok {
		t.Fatalf("retrieval-targets runner = %T, want *shellRunner", runner)
	}
	want := []string{"run", "./cmd/retrieval-eval", "-check-targets", retrieval.GateReportPath}
	if shell.cmd != "go" || !reflect.DeepEqual(shell.args, want) {
		t.Fatalf("retrieval-targets invokes %s %v, want go %v", shell.cmd, shell.args, want)
	}

	required := false
	for _, name := range requiredGates {
		if name == "retrieval-targets" {
			required = true
		}
	}
	if !required {
		t.Fatalf("retrieval-targets is not in requiredGates %v; a gate that is only supplied and never required cannot be detected as ABSENT", requiredGates)
	}
}

// TestRetrievalTargetsGate_AgreesWithTheEvaluator runs the real gate against
// the committed evidence and requires its answer to equal the evaluator's.
//
// This is the failing-branch test AC-7(b) asks for, and it is written so it
// stays honest in both directions: today the committed evidence records misses
// and the gate must therefore be RED, and on the day the recovery story closes
// them the gate must go green for the same reason and not by accident.
func TestRetrievalTargetsGate_AgreesWithTheEvaluator(t *testing.T) {
	chdirRepoRoot(t)
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	expected, err := retrieval.CheckTargets(retrieval.TargetCheckInputs{
		RepoRoot:            root,
		ReportPath:          retrieval.GateReportPath,
		RequireCandidateSHA: retrieval.GateCandidateSHA,
	})
	if err != nil {
		t.Fatalf("the evaluator could not read the committed evidence: %v", err)
	}

	runner := DefaultGates()["retrieval-targets"]
	score, runErr := runner.Run()

	if expected.MissCount > 0 {
		if runErr == nil {
			t.Fatalf("the retrieval-targets gate passed while the evaluator records %d missed target(s), first %q. A gate that does not bite on a recorded miss is not a gate.",
				expected.MissCount, expected.FirstMissName)
		}
		if !strings.Contains(runErr.Error(), expected.FirstMissName) {
			t.Errorf("the gate failed but did not name the first missed target %q: %v", expected.FirstMissName, runErr)
		}
		if classifyGate(runErr) != StateFail {
			t.Errorf("a missed target classified as %q, want %q — it is a gate that ran and found a problem, not an unobservable measurement", classifyGate(runErr), StateFail)
		}
		return
	}
	if runErr != nil {
		t.Fatalf("the retrieval-targets gate failed while the evaluator records every target met: %v", runErr)
	}
	if score == 0 {
		t.Error("a passing retrieval-targets gate scored 0")
	}
}

func chdirRepoRoot(t *testing.T) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod walking up from %s", wd)
		}
		dir = parent
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
}
