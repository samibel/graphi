package daemon

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// WatchManager is the optional SW-101 seam that starts/stops a filesystem
// watcher for a tracked workspace so the running daemon refreshes its graph
// without an explicit re-index command. It is defined as an interface here (not
// imported from engine/watch) so the layering stays cmd → surfaces → engine:
// engine/watch's manager satisfies it structurally and is injected by cmd. When
// nil, the control plane is watch-inert — default daemon behavior, no regression.
type WatchManager interface {
	// StartWatch begins watching root under the given workspace id. Re-tracking
	// the same id is idempotent.
	StartWatch(id, root string) error
	// StopWatch stops watching the given workspace id (no-op if absent).
	StopWatch(id string)
}

// control holds the daemon's control-plane state (SW-096): the registry of
// tracked workspaces and a monotonic generation/epoch bumped on reload. It is
// safe for concurrent use.
type control struct {
	mu         sync.Mutex
	tracked    map[string]string // workspace id -> root
	generation uint64
	startTime  time.Time
	nowFn      func() time.Time // injectable clock; defaults to time.Now
	watch      WatchManager     // SW-101: optional filesystem-watch wiring (nil = inert)
}

func newControl(now func() time.Time) *control {
	if now == nil {
		now = time.Now
	}
	return &control{tracked: map[string]string{}, startTime: now(), nowFn: now}
}

// newControlWithWatch is like newControl but wires an optional WatchManager so
// tracking a workspace also starts a filesystem watcher (SW-101 AC-1).
func newControlWithWatch(now func() time.Time, wm WatchManager) *control {
	c := newControl(now)
	c.watch = wm
	return c
}

// TrackedWorkspace is one entry in the daemon's tracked-workspace registry.
type TrackedWorkspace struct {
	ID   string `json:"id"`
	Root string `json:"root"`
}

// DaemonStatus is the deterministic status payload. The comparable state subset
// (Generation + Tracked, sorted by id) is free of wall-clock noise; PID and
// UptimeMs are runtime fields excluded from state-equality comparisons.
type DaemonStatus struct {
	PID        int                `json:"pid"`
	SocketPath string             `json:"socket_path"`
	Generation uint64             `json:"generation"`
	UptimeMs   int64              `json:"uptime_ms"`
	Tracked    []TrackedWorkspace `json:"tracked"`
}

// track registers a workspace root and returns its stable id. Re-tracking the
// same root is idempotent (same id).
func (c *control) track(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("daemon: track requires a non-empty root")
	}
	id := workspaceID(root)
	c.mu.Lock()
	_, already := c.tracked[id]
	c.tracked[id] = root
	wm := c.watch
	c.mu.Unlock()
	// SW-101: start a filesystem watcher so the daemon refreshes the graph for
	// this workspace without an explicit re-index. Idempotent on re-track.
	if wm != nil && !already {
		if err := wm.StartWatch(id, root); err != nil {
			return id, fmt.Errorf("daemon: start watch: %w", err)
		}
	}
	return id, nil
}

// untrack deregisters a workspace id. Removing a missing id is a no-op.
func (c *control) untrack(id string) {
	c.mu.Lock()
	_, existed := c.tracked[id]
	delete(c.tracked, id)
	wm := c.watch
	c.mu.Unlock()
	if wm != nil && existed {
		wm.StopWatch(id)
	}
}

// reload bumps the generation/epoch (a hot reconfigure re-scan in the real
// engine) and returns the new generation.
func (c *control) reload() uint64 {
	c.mu.Lock()
	c.generation++
	g := c.generation
	c.mu.Unlock()
	return g
}

// status builds the deterministic status payload: tracked workspaces sorted by
// id, the current generation, and runtime fields.
func (c *control) status(socketPath string) DaemonStatus {
	c.mu.Lock()
	tracked := make([]TrackedWorkspace, 0, len(c.tracked))
	for id, root := range c.tracked {
		tracked = append(tracked, TrackedWorkspace{ID: id, Root: root})
	}
	gen := c.generation
	start := c.startTime
	c.mu.Unlock()
	sort.Slice(tracked, func(i, j int) bool { return tracked[i].ID < tracked[j].ID })
	return DaemonStatus{
		PID:        os.Getpid(),
		SocketPath: socketPath,
		Generation: gen,
		UptimeMs:   c.nowFn().Sub(start).Milliseconds(),
		Tracked:    tracked,
	}
}

// workspaceID derives a stable id from a workspace root via FNV-1a.
func workspaceID(root string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(root))
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], h.Sum64())
	return fmt.Sprintf("ws_%x", b)
}
