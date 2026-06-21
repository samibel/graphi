package ledger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newLedger(t *testing.T) (*Ledger, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return l, path
}

// AC: cross-restart restore — cumulative restored exactly, no loss/reset.
func TestCrossRestart_CumulativeRestored(t *testing.T) {
	l, path := newLedger(t)
	if _, err := l.Record(Credit{CallID: "c1", Model: "m", MicroUSD: 1_000_000, Priced: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Record(Credit{CallID: "c2", Model: "m", MicroUSD: 2_500_000, Priced: true}); err != nil {
		t.Fatal(err)
	}
	want := MicroUSD(3_500_000)
	if got := l.Cumulative(); got != want {
		t.Errorf("before close: cumulative=%d want %d", got, want)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen and verify restoration.
	l2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	if got := l2.Cumulative(); got != want {
		t.Errorf("after reopen: cumulative=%d want %d (loss/reset detected)", got, want)
	}
}

// AC: no double-count / no replay-drift — multiple reloads are stable.
func TestNoDoubleCount_ReloadStable(t *testing.T) {
	l, path := newLedger(t)
	for i := 0; i < 5; i++ {
		if _, err := l.Record(Credit{CallID: "c", Model: "m", MicroUSD: 100, Priced: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	want := MicroUSD(500)
	for reload := 0; reload < 3; reload++ {
		l2, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := l2.Cumulative(); got != want {
			t.Errorf("reload %d: cumulative=%d want %d (double-count/replay drift)", reload, got, want)
		}
		if err := l2.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// AC: torn-write recovery — corrupt/truncate the final line; Open recovers to
// the last fully-committed consistent state.
func TestTornWrite_TruncatesCorruptTail(t *testing.T) {
	l, path := newLedger(t)
	if _, err := l.Record(Credit{CallID: "c1", Model: "m", MicroUSD: 1_000_000, Priced: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Record(Credit{CallID: "c2", Model: "m", MicroUSD: 2_000_000, Priced: true}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	// Append a torn (partial/garbage) line to simulate a mid-commit crash.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("{\"seq\":3,\"session_id\":\"sess-x\",\"call_id\":\"c3\"")...) // no closing
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	l2, err := Open(path)
	if err != nil {
		t.Fatalf("Open should recover torn tail, got %v", err)
	}
	defer l2.Close()
	// Only the two valid entries counted; the torn third line dropped.
	if got, want := l2.Cumulative(), MicroUSD(3_000_000); got != want {
		t.Errorf("after torn recovery: cumulative=%d want %d", got, want)
	}
	if n := len(l2.Entries()); n != 2 {
		t.Errorf("entries after torn recovery: %d want 2", n)
	}

	// The file on disk should now be truncated back to the valid boundary.
	l3, _ := Open(path)
	defer l3.Close()
	// Recording again must continue from Seq=3 (the torn line never committed).
	e, err := l3.Record(Credit{CallID: "c3", Model: "m", MicroUSD: 500_000, Priced: true})
	if err != nil {
		t.Fatal(err)
	}
	if e.Seq != 3 {
		t.Errorf("Seq after torn recovery should resume at 3, got %d", e.Seq)
	}
}

// AC: rollup identity — cumulative == sum of per-session totals == sum of entries.
func TestRollupIdentity(t *testing.T) {
	l, path := newLedger(t)
	// Session 1.
	l.Record(Credit{CallID: "c1", Model: "m", MicroUSD: 1_000_000, Priced: true})
	l.Record(Credit{CallID: "c2", Model: "m", MicroUSD: 500_000, Priced: true})
	l.Close()
	// Session 2 (after restart).
	l2, _ := Open(path)
	l2.Record(Credit{CallID: "c3", Model: "m", MicroUSD: 250_000, Priced: true})
	l2.Close()
	// Session 3.
	l3, _ := Open(path)
	l3.Record(Credit{CallID: "c4", Model: "m", MicroUSD: 125_000, Priced: true})
	defer l3.Close()

	cum, bySession := l3.RecomputeFromEntries()
	if cum != l3.Cumulative() {
		t.Errorf("cached cumulative %d != recomputed %d (drift)", l3.Cumulative(), cum)
	}
	var sessionSum MicroUSD
	for _, v := range bySession {
		sessionSum += v
	}
	if sessionSum != cum {
		t.Errorf("sum(sessions)=%d != cumulative=%d", sessionSum, cum)
	}
	if len(bySession) != 3 {
		t.Errorf("want 3 sessions, got %d (%v)", len(bySession), bySession)
	}
}

// AC: clean session boundary — fresh session total starts at 0 after restart;
// cumulative continues from restored value.
func TestSessionBoundary_FreshSessionZeroCumulativeContinues(t *testing.T) {
	l, path := newLedger(t)
	l.Record(Credit{CallID: "c1", Model: "m", MicroUSD: 1_000_000, Priced: true})
	if got := l.SessionTotal(); got != 1_000_000 {
		t.Errorf("session total before close: %d", got)
	}
	l.Close()

	l2, _ := Open(path)
	defer l2.Close()
	// Fresh session total is 0, cumulative continues from restored 1_000_000.
	if got := l2.SessionTotal(); got != 0 {
		t.Errorf("fresh session total should start at 0, got %d", got)
	}
	if got := l2.Cumulative(); got != 1_000_000 {
		t.Errorf("cumulative should continue from restored value, got %d", got)
	}
	// New entries grow the fresh session without resetting cumulative.
	l2.Record(Credit{CallID: "c2", Model: "m", MicroUSD: 250_000, Priced: true})
	if got := l2.SessionTotal(); got != 250_000 {
		t.Errorf("new session total: %d", got)
	}
	if got := l2.Cumulative(); got != 1_250_000 {
		t.Errorf("cumulative after new entry: %d", got)
	}
}

// AC: unpriced entries recorded for audit but contribute 0 to totals.
func TestUnpricedEntries_ContributeZero(t *testing.T) {
	l, _ := newLedger(t)
	defer l.Close()
	l.Record(Credit{CallID: "c1", Model: "unknown", MicroUSD: 0, Priced: false})
	l.Record(Credit{CallID: "c2", Model: "m", MicroUSD: 1_000_000, Priced: true})
	if got, want := l.Cumulative(), MicroUSD(1_000_000); got != want {
		t.Errorf("unpriced should contribute 0: cumulative=%d want %d", got, want)
	}
	if n := len(l.Entries()); n != 2 {
		t.Errorf("both entries recorded for audit: %d want 2", n)
	}
}

// AC: local-only — remote journal paths rejected; no network.
func TestOpen_RejectsRemote(t *testing.T) {
	for _, remote := range []string{"http://e.com/l.jsonl", "https://e.com/l.jsonl"} {
		if _, err := Open(remote); err == nil {
			t.Errorf("remote %q should be rejected", remote)
		}
	}
}

// AC: closed ledger returns ErrClosed.
func TestClosed_Errors(t *testing.T) {
	l, _ := newLedger(t)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Record(Credit{CallID: "c", Model: "m", MicroUSD: 1, Priced: true}); err != ErrClosed {
		t.Errorf("want ErrClosed, got %v", err)
	}
}

// AC: deterministic local storage — reload yields identical in-memory state for
// identical journal bytes (Seq assignment stable).
func TestReload_DeterministicSeq(t *testing.T) {
	l, path := newLedger(t)
	for i := 0; i < 4; i++ {
		l.Record(Credit{CallID: "c", Model: "m", MicroUSD: 1, Priced: true})
	}
	l.Close()
	l2, _ := Open(path)
	defer l2.Close()
	got := l2.Entries()
	for i, e := range got {
		if e.Seq != int64(i+1) {
			t.Errorf("Seq not deterministic: entry %d has Seq %d", i, e.Seq)
		}
	}
}

// AC: static local-first guard — no net import.
func TestPackageHasNoNetImport(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			trim := strings.TrimSpace(line)
			if strings.Contains(trim, "\"net\"") && !strings.HasPrefix(trim, "//") {
				t.Errorf("%s: forbidden net import: %s", f, trim)
			}
		}
	}
}
