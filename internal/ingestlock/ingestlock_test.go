package ingestlock

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// holdLock takes the real lock the way the runtime does (BEGIN IMMEDIATE on a
// pinned connection) and returns a release func.
func holdLock(t *testing.T, metaDir string) func() {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=busy_timeout(0)", filepath.ToSlash(Path(metaDir))))
	if err != nil {
		t.Fatalf("open lock db: %v", err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("open lock conn: %v", err)
	}
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		_ = conn.Close()
		_ = db.Close()
	}
}

// TestProbe_AbsentFreeHeld pins the three-state matrix: no lock DB on disk,
// an existing but unheld lock DB, and a lock held by another connection.
func TestProbe_AbsentFreeHeld(t *testing.T) {
	metaDir := t.TempDir()

	state, err := Probe(context.Background(), metaDir)
	if err != nil {
		t.Fatalf("probe absent: %v", err)
	}
	if state != StateAbsent {
		t.Fatalf("probe of missing lock DB = %q, want %q", state, StateAbsent)
	}

	release := holdLock(t, metaDir)
	state, err = Probe(context.Background(), metaDir)
	if err != nil {
		t.Fatalf("probe held: %v", err)
	}
	if state != StateHeld {
		t.Fatalf("probe while another connection holds the lock = %q, want %q", state, StateHeld)
	}

	release()
	state, err = Probe(context.Background(), metaDir)
	if err != nil {
		t.Fatalf("probe free: %v", err)
	}
	if state != StateFree {
		t.Fatalf("probe of released lock = %q, want %q", state, StateFree)
	}
}

// TestProbe_NeverCreatesLockFile pins the non-destructive contract: probing a
// state dir that has never seen contention must not mint the lock DB (doctor
// and status are pure observers).
func TestProbe_NeverCreatesLockFile(t *testing.T) {
	metaDir := t.TempDir()
	if _, err := Probe(context.Background(), metaDir); err != nil {
		t.Fatalf("probe: %v", err)
	}
	entries, err := os.ReadDir(metaDir)
	if err != nil {
		t.Fatalf("read meta dir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("probe created files in an empty meta dir: %v", names)
	}
}

// TestProbe_DoesNotDisturbHolder pins that probing a held lock leaves the
// holder's transaction fully usable: its ROLLBACK still succeeds and it can
// re-acquire afterwards.
func TestProbe_DoesNotDisturbHolder(t *testing.T) {
	metaDir := t.TempDir()
	release := holdLock(t, metaDir)
	for range 3 {
		state, err := Probe(context.Background(), metaDir)
		if err != nil {
			t.Fatalf("probe: %v", err)
		}
		if state != StateHeld {
			t.Fatalf("probe = %q, want %q", state, StateHeld)
		}
	}
	release() // ROLLBACK must succeed after being probed

	second := holdLock(t, metaDir) // and the lock must be re-acquirable
	defer second()
	if state, err := Probe(context.Background(), metaDir); err != nil || state != StateHeld {
		t.Fatalf("probe after re-acquire = %q, %v; want %q, nil", state, err, StateHeld)
	}
}

// TestIsBusy pins the busy/locked classification the waiter loop and the probe
// share.
func TestIsBusy(t *testing.T) {
	for _, tc := range []struct {
		err  string
		want bool
	}{
		{"database is locked (5) (SQLITE_BUSY)", true},
		{"table is locked", true},
		{"SQLITE_BUSY: database busy", true},
		{"no such table: ingest_semantics", false},
		{"disk I/O error", false},
	} {
		if got := IsBusy(fmt.Errorf("%s", tc.err)); got != tc.want {
			t.Errorf("IsBusy(%q) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
