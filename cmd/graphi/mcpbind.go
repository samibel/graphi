package main

import (
	"fmt"
	"io"
	"sync"
	"time"

	rtime "github.com/samibel/graphi/cmd/internal/runtime"
	"github.com/samibel/graphi/engine/ingest"
)

// lockWaitingAnnounceGap throttles the repeated "still waiting for the ingest
// lock" stderr lines: the waiter's onBusy fires roughly once per busy_timeout
// window, which would otherwise log every few seconds for minutes.
const lockWaitingAnnounceGap = 60 * time.Second

// mcpBindStatus is the live snapshot behind mcp.WithBindStatus for the MCP
// binder. It is written by the bind goroutine (the runtime's Progress/Status
// callbacks) and read by the protocol loop (Render, called with the MCP
// server's mu held), so it takes only its OWN mutex and never calls back into
// the server — the lock order Server.mu → mcpBindStatus.mu is acyclic.
type mcpBindStatus struct {
	mu  sync.Mutex
	now func() time.Time // injectable clock for tests

	mode        string // "" (idle) | "binding" | "waiting" | "indexing"
	root        string
	phase       ingest.Phase
	done, total int
	start       time.Time // bind attempt start (BeginAttempt)
	waitStart   time.Time // first lock-busy observation of this attempt

	lastWaitAnnounce time.Time // announceBindEvent throttle state
}

func newMCPBindStatus() *mcpBindStatus {
	return &mcpBindStatus{now: time.Now}
}

// BeginAttempt marks the start of one binder invocation; everything a prior
// attempt left behind is reset.
func (b *mcpBindStatus) BeginAttempt() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.mode = "binding"
	b.root = ""
	b.phase = ""
	b.done, b.total = 0, 0
	b.start = b.now()
	b.waitStart = time.Time{}
	b.lastWaitAnnounce = time.Time{}
}

// HandleEvent consumes one runtime lifecycle observation.
func (b *mcpBindStatus) HandleEvent(ev rtime.BindEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch ev.Kind {
	case rtime.BindRootResolved:
		b.root = ev.Root
	case rtime.BindLockWaiting:
		if b.mode != "waiting" {
			b.mode = "waiting"
			b.waitStart = b.now()
		}
		b.root = ev.Root
	case rtime.BindLockAcquired:
		b.mode = "binding"
	}
}

// HandleProgress consumes one ingest progress event.
func (b *mcpBindStatus) HandleProgress(ev ingest.ProgressEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.mode = "indexing"
	b.phase = ev.Phase
	b.done, b.total = ev.Done, ev.Total
}

// Clear marks the attempt finished (success or failure); Render falls back to
// the static message until the next BeginAttempt.
func (b *mcpBindStatus) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.mode = ""
}

// Render is the mcp.WithBindStatus func: one line describing the in-flight
// bind, or "" for the static fallback. It must stay fast and non-blocking —
// it runs with the MCP server's mutex held.
func (b *mcpBindStatus) Render() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	elapsed := func(since time.Time) string {
		return b.now().Sub(since).Truncate(time.Second).String()
	}
	switch b.mode {
	case "waiting":
		return fmt.Sprintf("waiting for another graphi process indexing %s (waited %s)", b.root, elapsed(b.waitStart))
	case "binding":
		if b.root == "" {
			return fmt.Sprintf("resolving repository root (%s elapsed)", elapsed(b.start))
		}
		return fmt.Sprintf("preparing %s (%s elapsed)", b.root, elapsed(b.start))
	case "indexing":
		switch b.phase {
		case ingest.PhaseWalk:
			if b.done > 0 {
				return fmt.Sprintf("indexing %s: scanning repository, %d files found (%s elapsed)", b.root, b.done, elapsed(b.start))
			}
			return fmt.Sprintf("indexing %s: scanning repository (%s elapsed)", b.root, elapsed(b.start))
		case ingest.PhaseDrift:
			if b.done > 0 {
				return fmt.Sprintf("checking %s for changes, %d files checked (%s elapsed)", b.root, b.done, elapsed(b.start))
			}
			return fmt.Sprintf("checking %s for changes (%s elapsed)", b.root, elapsed(b.start))
		case ingest.PhaseParse:
			return fmt.Sprintf("indexing %s: parse %d/%d files (%s elapsed)", b.root, b.done, b.total, elapsed(b.start))
		case ingest.PhaseLink:
			return fmt.Sprintf("indexing %s: linking cross-file references (%s elapsed)", b.root, elapsed(b.start))
		case ingest.PhaseResolve:
			return fmt.Sprintf("indexing %s: resolving types (%s elapsed)", b.root, elapsed(b.start))
		default:
			return fmt.Sprintf("indexing %s: finishing up (%s elapsed)", b.root, elapsed(b.start))
		}
	default:
		return ""
	}
}

// announceBindEvent prints the MCP binder's lifecycle to stderr, where MCP
// clients surface server logs. The resolved-root line is the MCP-path parity
// of the sync-path announcement; the waiting lines make a session blocked on
// ANOTHER process's index visibly alive instead of silent.
func (b *mcpBindStatus) announceBindEvent(w io.Writer, ev rtime.BindEvent) {
	switch ev.Kind {
	case rtime.BindRootResolved:
		fmt.Fprintf(w, "graphi: mcp: binding repository root %s\n", ev.Root)
	case rtime.BindLockWaiting:
		b.mu.Lock()
		first := b.lastWaitAnnounce.IsZero()
		throttled := !first && b.now().Sub(b.lastWaitAnnounce) < lockWaitingAnnounceGap
		if !throttled {
			b.lastWaitAnnounce = b.now()
		}
		waited := b.now().Sub(b.waitStart).Truncate(time.Second)
		b.mu.Unlock()
		if throttled {
			return
		}
		if first {
			fmt.Fprintf(w, "graphi: mcp: waiting for another graphi process indexing %s\n", ev.Root)
			return
		}
		fmt.Fprintf(w, "graphi: mcp: still waiting for the ingest lock on %s (%s elapsed)\n", ev.Root, waited)
	case rtime.BindLockAcquired:
		fmt.Fprintln(w, "graphi: mcp: ingest lock acquired — continuing")
	}
}
