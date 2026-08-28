package divergence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeSegmentFile plants a foreign process's segment at path with the given
// mtime, so a test can build the retention pressure prune reacts to without
// running 64 processes.
func writeSegmentFile(t *testing.T, path string, seg segment, mod time.Time) {
	t.Helper()
	raw, err := json.MarshalIndent(seg, "", "  ")
	if err != nil {
		t.Fatalf("marshal segment: %v", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// prune deletes by modification time and has no writer-liveness concept beyond
// protecting the pruning process's OWN segment: a still-running writer that has
// simply been quiet since its last flush looks exactly as old as one whose
// process exited months ago, so its already-written counts can be deleted out
// from under it. That bound is documented on maxSegments and in
// docs/executor-seam-rollback.md, and this test pins the part that keeps it
// honest rather than silent — the loss is counted, the count survives the
// pruning of a segment that had pruned some itself, and the read path reports
// the totals as a lower bound in both the human and the JSON form.
func TestPrunedSegmentsAreCountedAndDisclosedAsALowerBound(t *testing.T) {
	stateDir := t.TempDir()
	dir := Dir(stateDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	base := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)

	// Exactly maxSegments foreign segments: one more (this process's own) is
	// what tips the directory over the cap. The oldest one carries a pruned
	// tally of its own, so the carry-forward is exercised and not assumed.
	for i := 0; i < maxSegments; i++ {
		seen := base.Add(time.Duration(i) * time.Minute)
		seg := segment{
			Schema:     Schema,
			PID:        90000 + i,
			Operations: []OperationRecord{{Operation: "victim", Observations: 1, FirstSeen: &seen, LastSeen: &seen}},
		}
		if i == 0 {
			seg.Pruned = 7
		}
		writeSegmentFile(t, filepath.Join(dir, fmt.Sprintf("9%04d-deadbeefdeadbeef.json", i)), seg, seen)
	}

	store, err := NewStore(stateDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	// The first observation always flushes, and the flush is what prunes.
	store.RecordDivergence("live", false, "", "", "")
	if err := store.LastError(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if _, err := os.Stat(store.Path()); err != nil {
		t.Fatalf("the pruning process deleted its own segment: %v", err)
	}

	rep, err := Read(stateDir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	// One victim was dropped, and it was carrying seven of its own.
	if rep.Pruned != 8 {
		t.Fatalf("pruned = %d, want 8 (the deleted segment plus the 7 it had itself pruned)", rep.Pruned)
	}
	if rep.Segments != maxSegments {
		t.Fatalf("segments = %d, want %d — the cap holds and this writer's own segment survives",
			rep.Segments, maxSegments)
	}
	if rep.Unreadable != 0 {
		t.Fatalf("unreadable = %d, want 0", rep.Unreadable)
	}

	doc := Assess(rep, []string{"live", "victim"}, nil)
	if doc.Pruned != 8 {
		t.Fatalf("document pruned = %d, want 8", doc.Pruned)
	}
	human := renderString(t, doc)
	if !contains(human, "8 pruned") {
		t.Fatalf("the human form hides the pruning:\n%s", human)
	}
	if !contains(human, "have been pruned") || !contains(human, "lower bound") {
		t.Fatalf("the human form does not disclose the totals as a lower bound:\n%s", human)
	}
	if !contains(human, "still-running but quiet writer") {
		t.Fatalf("the human form does not say WHAT can be pruned:\n%s", human)
	}
	if raw := string(jsonBytes(t, doc)); !contains(raw, `"pruned_segments": 8`) {
		t.Fatalf("the JSON form hides the pruning:\n%s", raw)
	}
}

// The disclosure must not fire when nothing was pruned: a lower-bound warning
// on an exact total would train an operator to ignore it.
func TestUnprunedRecordMakesNoLowerBoundClaim(t *testing.T) {
	stateDir := t.TempDir()
	store, err := NewStore(stateDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	store.RecordDivergence("live", false, "", "", "")

	rep, err := Read(stateDir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if rep.Pruned != 0 {
		t.Fatalf("pruned = %d, want 0", rep.Pruned)
	}
	human := renderString(t, Assess(rep, []string{"live"}, nil))
	if contains(human, "have been pruned") || contains(human, "lower bound") {
		t.Fatalf("an exact record claims to be a lower bound:\n%s", human)
	}
}
