package retrieval

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSW279_ScriptGates runs the Python gate suite for the SW-279 Phase 2 harvest
// scripts, so the checks that keep the dataset honest are on the same gate as the
// dataset itself rather than in a command someone has to remember to run.
//
// The suite lives at scripts/eval/tests/test_sw279_gates.py. Each of its cases breaks
// exactly one thing and asserts the script refuses, and the refusals are paired with
// positive controls that reproduce the committed artefacts byte for byte — a gate that
// always fails would otherwise look identical to one that works.
//
// It SKIPS visibly, and only when the tools it drives are genuinely absent: no python3
// on PATH, or no read-only spf13/cobra clone at the pin (two of its cases run the
// answerability finalizer, which verifies the checkout HEAD before reading a span; the
// Python suite skips those two itself and runs the rest).
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
	out, err := cmd.CombinedOutput()
	t.Logf("python3 %s\n%s", suite, out)
	if err != nil {
		t.Fatalf("the SW-279 harvest-script gates failed: %v", err)
	}
	// A run in which every case skipped would pass silently and prove nothing.
	if !strings.Contains(string(out), "... ok") {
		t.Fatalf("no SW-279 gate case actually ran:\n%s", out)
	}
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
