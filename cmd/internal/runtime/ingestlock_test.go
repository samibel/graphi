package runtime

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/samibel/graphi/engine/ingest"
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
	release, err := acquireIngestLock(context.Background(), "")
	if err != nil {
		t.Fatalf("empty metaDir must be a no-op, got %v", err)
	}
	release()
}
