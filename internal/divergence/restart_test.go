package divergence

import (
	"os"
	"os/exec"
	"testing"
)

// envSubprocessDir is the marker that turns a re-executed copy of this test
// binary into the WRITER half of the restart journey.
const envSubprocessDir = "GRAPHI_DIVERGENCE_SUBPROCESS_DIR"

// SW-232 AC-1, proven the only way it can honestly be proven: a SEPARATE
// PROCESS writes the record, exits, and this process — which shares no memory
// with it and was never told what it observed — reads the counts back.
//
// The in-process test beside this one would pass against a global variable.
// This one cannot: the writing process is gone by the time the assertion runs,
// so only bytes on disk can carry the answer. That is exactly the gap the
// SW-238 precondition assessment recorded as unclosable before this story.
func TestRecordSurvivesProcessRestart(t *testing.T) {
	if dir := os.Getenv(envSubprocessDir); dir != "" {
		// Writer half, running inside the re-executed binary.
		store, err := NewStore(dir)
		if err != nil {
			t.Fatalf("subprocess NewStore: %v", err)
		}
		store.RecordDivergence("dead_code", false, "", "", "")
		store.RecordDivergence("dead_code", true, "bytes", "3 bytes: abc", "4 bytes: abcd")
		store.RecordDivergence("search_ast", false, "", "", "")
		if err := store.Flush(); err != nil {
			t.Fatalf("subprocess Flush: %v", err)
		}
		return
	}

	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cmd := exec.Command(exe, "-test.run=TestRecordSurvivesProcessRestart", "-test.v")
	cmd.Env = append(os.Environ(), envSubprocessDir+"="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("writer subprocess: %v\n%s", err, out)
	}

	rep, err := Read(dir)
	if err != nil {
		t.Fatalf("Read after restart: %v", err)
	}
	doc := Assess(rep, []string{"dead_code", "search_ast", "compound"})
	if doc.State != StateDiverged {
		t.Fatalf("state after restart = %q, want %q", doc.State, StateDiverged)
	}
	if doc.Observations != 3 || doc.Mismatches != 1 {
		t.Fatalf("totals after restart = %d/%d, want 3 observations / 1 mismatch", doc.Observations, doc.Mismatches)
	}
	states := map[string]OperationState{}
	for _, op := range doc.Operations {
		states[op.Operation] = op.State
	}
	if states["dead_code"] != StateDiverged {
		t.Errorf("dead_code = %q, want DIVERGED", states["dead_code"])
	}
	if states["search_ast"] != StateAgreed {
		t.Errorf("search_ast = %q, want NO-DIVERGENCE-OBSERVED", states["search_ast"])
	}
	if states["compound"] != StateUnknown {
		t.Errorf("compound = %q, want UNKNOWN — it was never observed", states["compound"])
	}
}
