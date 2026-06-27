package daemon

import (
	"sync"
	"testing"
)

// fakeWatchManager records StartWatch/StopWatch invocations so the control-plane
// wiring (SW-101 AC-1) can be asserted without a real filesystem watcher.
type fakeWatchManager struct {
	mu      sync.Mutex
	started map[string]string // id -> root
	stopped []string
}

func newFakeWatchManager() *fakeWatchManager {
	return &fakeWatchManager{started: map[string]string{}}
}

func (f *fakeWatchManager) StartWatch(id, root string) error {
	f.mu.Lock()
	f.started[id] = root
	f.mu.Unlock()
	return nil
}

func (f *fakeWatchManager) StopWatch(id string) {
	f.mu.Lock()
	f.stopped = append(f.stopped, id)
	f.mu.Unlock()
}

// TestControl_TrackStartsWatcher asserts that tracking a workspace through the
// control plane starts a filesystem watcher exactly once (idempotent on
// re-track) and that untracking stops it — the surfaces/daemon wiring that lets
// the running daemon refresh the graph without an explicit re-index.
func TestControl_TrackStartsWatcher(t *testing.T) {
	wm := newFakeWatchManager()
	c := newControlWithWatch(nil, wm)

	root := t.TempDir()
	id, err := c.track(root)
	if err != nil {
		t.Fatalf("track: %v", err)
	}

	wm.mu.Lock()
	if got := wm.started[id]; got != root {
		wm.mu.Unlock()
		t.Fatalf("StartWatch root = %q, want %q", got, root)
	}
	wm.mu.Unlock()

	// Re-tracking the same root is idempotent: no second StartWatch.
	if _, err := c.track(root); err != nil {
		t.Fatalf("re-track: %v", err)
	}
	wm.mu.Lock()
	if len(wm.started) != 1 {
		wm.mu.Unlock()
		t.Fatalf("expected 1 StartWatch, got %d", len(wm.started))
	}
	wm.mu.Unlock()

	// Untrack stops the watcher.
	c.untrack(id)
	wm.mu.Lock()
	defer wm.mu.Unlock()
	if len(wm.stopped) != 1 || wm.stopped[0] != id {
		t.Fatalf("StopWatch = %v, want [%s]", wm.stopped, id)
	}
}

// TestControl_NoWatchManagerIsInert confirms the default control plane (no
// WatchManager) tracks/untracks without panicking — no regression.
func TestControl_NoWatchManagerIsInert(t *testing.T) {
	c := newControl(nil)
	id, err := c.track(t.TempDir())
	if err != nil {
		t.Fatalf("track: %v", err)
	}
	c.untrack(id)
}
