package ingest

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
)

// writeRepoIngest creates a tiny repo for internal package tests.
func writeRepoIngest(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// fakeClock is a deterministic clock for heartbeat tests.
type fakeClock struct {
	t time.Time
}

func (c *fakeClock) Now() time.Time      { return c.t }
func (c *fakeClock) Add(d time.Duration) { c.t = c.t.Add(d) }

// TestHeartbeat_PhasesInOrder asserts that a full ingest emits the complete
// phase sequence including the newly-exposed write/FTS/checkpoint phases.
func TestHeartbeat_PhasesInOrder(t *testing.T) {
	repo := writeRepoIngest(t, map[string]string{
		"shop/cart.go": `package shop
func checkout() int { return price() }
`,
		"shop/price.go": `package shop
func price() int { return 10 }
`,
	})
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	i, err := New(store, NewNotebookParser(parse.NewDefaultRegistry()), t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var phases []Phase
	i.WithProgress(func(ev ProgressEvent) {
		phases = append(phases, ev.Phase)
	})
	if err := i.IngestAll(context.Background(), repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	seen := make(map[Phase]bool)
	for _, p := range phases {
		seen[p] = true
	}
	required := []Phase{PhaseWalk, PhaseParse, PhaseWrite, PhaseLink, PhaseFTS, PhaseResolve, PhaseCheckpoint, PhaseDone}
	for _, p := range required {
		if !seen[p] {
			t.Fatalf("phase %s not emitted; got %v", p, phases)
		}
	}
	// The first occurrence of each required phase must be in order.
	firstIdx := make(map[Phase]int)
	for idx, p := range phases {
		if _, ok := firstIdx[p]; !ok {
			firstIdx[p] = idx
		}
	}
	for i := 1; i < len(required); i++ {
		if firstIdx[required[i-1]] > firstIdx[required[i]] {
			t.Fatalf("phases out of order: first %s at %d, first %s at %d; full %v",
				required[i-1], firstIdx[required[i-1]], required[i], firstIdx[required[i]], phases)
		}
	}
}

// TestHeartbeat_EmitsOnInterval asserts that the heartbeat emits a progress
// event for the current phase when the interval elapses without a natural event.
func TestHeartbeat_EmitsOnInterval(t *testing.T) {
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	i, err := New(store, NewNotebookParser(parse.NewDefaultRegistry()), t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	clk := &fakeClock{t: time.Now()}
	i.WithClock(clk)
	i.WithHeartbeatMode(HeartbeatNonTTY)

	var phases []Phase
	i.WithProgress(func(ev ProgressEvent) {
		phases = append(phases, ev.Phase)
	})

	// Emit a natural event, then advance time past the heartbeat interval.
	i.notifyProgress(context.Background(), ProgressEvent{Phase: PhaseLink})
	phases = nil
	clk.Add(11 * time.Second)
	i.heartbeat(context.Background(), PhaseLink)

	if len(phases) == 0 {
		t.Fatal("heartbeat did not emit a progress event after interval elapsed")
	}
	if phases[0] != PhaseLink {
		t.Fatalf("heartbeat emitted phase %v, want link", phases)
	}
}

// TestHeartbeat_ModeAwareInterval asserts that TTY and non-TTY modes use
// different intervals.
func TestHeartbeat_ModeAwareInterval(t *testing.T) {
	if heartbeatModeInterval(HeartbeatTTY) != heartbeatIntervalTTY {
		t.Fatalf("TTY interval = %v, want %v", heartbeatModeInterval(HeartbeatTTY), heartbeatIntervalTTY)
	}
	if heartbeatModeInterval(HeartbeatNonTTY) != heartbeatIntervalNonTTY {
		t.Fatalf("non-TTY interval = %v, want %v", heartbeatModeInterval(HeartbeatNonTTY), heartbeatIntervalNonTTY)
	}
	if heartbeatIntervalTTY >= heartbeatIntervalNonTTY {
		t.Fatal("TTY interval should be shorter than non-TTY interval")
	}
	if heartbeatIntervalNonTTY >= heartbeatMaxInterval {
		t.Fatalf("non-TTY interval %v must be < %v", heartbeatIntervalNonTTY, heartbeatMaxInterval)
	}
}
