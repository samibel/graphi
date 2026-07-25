package runtime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/internal/ingestlock"
	"github.com/samibel/graphi/internal/state"
)

// TestOpenSession_ConcurrentSessionsShareOneFullPass pins the cross-process
// ingest lock: two sessions racing on the same cold auto-managed store (the
// shape of several MCP clients auto-starting `graphi mcp` for one workspace)
// must not each run their own full index. The winner takes the full pass; the
// waiter blocks on the lock and then warm-starts over the certified store —
// zero drift, no parse at all.
func TestOpenSession_ConcurrentSessionsShareOneFullPass(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var fullPasses atomic.Int32
	run := func(done chan<- error) {
		sawParse := false
		rt, err := OpenSession(context.Background(), Options{
			Roots: []string{repo},
			Progress: func(ev ingest.ProgressEvent) {
				if ev.Phase == ingest.PhaseParse {
					sawParse = true
				}
			},
		})
		if rt != nil {
			rt.Close()
		}
		if sawParse {
			fullPasses.Add(1)
		}
		done <- err
	}

	done := make(chan error, 2)
	go run(done)
	go run(done)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent OpenSession failed: %v", err)
		}
	}
	switch fullPasses.Load() {
	case 1: // exactly one session indexed; the other warm-started
	case 0:
		t.Fatal("no session ran the initial full pass")
	default:
		t.Fatal("both sessions ran a full parse; the ingest lock must serialize them onto one pass")
	}
}

// TestAcquireIngestLock_EmptyMetaDirIsNoOp: an in-memory sidecar has no
// on-disk identity to contend on, so no lock file may be created anywhere.
func TestAcquireIngestLock_EmptyMetaDirIsNoOp(t *testing.T) {
	release, err := acquireIngestLock(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("empty metaDir must be a no-op, got %v", err)
	}
	release()
}

// TestAcquireIngestLock_OnBusyNotifies pins the waiter's observability seam:
// while another connection holds the lock, every busy acquisition attempt
// invokes onBusy (so the MCP path can render a live waiting state), and a
// cancelled waiter returns the context error instead of looping forever.
func TestAcquireIngestLock_OnBusyNotifies(t *testing.T) {
	oldTimeout := ingestLockBusyTimeoutMS
	ingestLockBusyTimeoutMS = 50 // shrink the per-attempt block so the test is fast
	defer func() { ingestLockBusyTimeoutMS = oldTimeout }()

	metaDir := t.TempDir()
	holder, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=busy_timeout(0)", filepath.ToSlash(ingestlock.Path(metaDir))))
	if err != nil {
		t.Fatalf("open holder: %v", err)
	}
	defer holder.Close()
	conn, err := holder.Conn(context.Background())
	if err != nil {
		t.Fatalf("holder conn: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), "ROLLBACK") }()

	ctx, cancel := context.WithCancel(context.Background())
	var notifications atomic.Int32
	onBusy := func() {
		if notifications.Add(1) == 1 {
			cancel() // first notification: the waiter observed contention — stop waiting
		}
	}
	if _, err := acquireIngestLock(ctx, metaDir, onBusy); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter returned %v, want context.Canceled", err)
	}
	if notifications.Load() < 1 {
		t.Fatal("onBusy was never invoked while the lock was held")
	}
}

// TestOpenSession_EmitsRootResolvedStatus pins the Status seam: the first
// lifecycle event of a successful OpenSession is BindRootResolved carrying the
// detected repository root.
func TestOpenSession_EmitsRootResolvedStatus(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var events []BindEvent
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rt, err := OpenSession(ctx, Options{
		Roots:  []string{repo},
		Status: func(ev BindEvent) { events = append(events, ev) },
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	rt.Close()
	if len(events) == 0 {
		t.Fatal("no BindEvents emitted")
	}
	first := events[0]
	if first.Kind != BindRootResolved {
		t.Fatalf("first event kind = %q, want %q", first.Kind, BindRootResolved)
	}
	resolved, ok := state.DetectRepo(repo)
	if !ok {
		t.Fatalf("fixture repo %q not detectable", repo)
	}
	if first.Root != resolved {
		t.Fatalf("first event root = %q, want %q", first.Root, resolved)
	}
	for _, ev := range events {
		if ev.Kind == BindLockWaiting || ev.Kind == BindLockAcquired {
			t.Fatalf("uncontended OpenSession emitted %q", ev.Kind)
		}
	}
}
