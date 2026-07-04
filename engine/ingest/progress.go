package ingest

import (
	"context"
	"encoding/json"
	"time"

	"github.com/samibel/graphi/engine/observe"
)

// HeartbeatMode selects the cadence for clock-driven progress heartbeats.
type HeartbeatMode string

const (
	// HeartbeatTTY is for human-facing terminals; heartbeats are frequent.
	HeartbeatTTY HeartbeatMode = "tty"
	// HeartbeatNonTTY is for JSON/HTTP/SSE consumers; heartbeats are less frequent
	// but still keep any indeterminate phase alive.
	HeartbeatNonTTY HeartbeatMode = "non-tty"
)

// Default heartbeat intervals. The contract is that no phase may be silent
// longer than the configured threshold (the story AC requires < 15s).
const (
	heartbeatIntervalTTY    = 2 * time.Second
	heartbeatIntervalNonTTY = 10 * time.Second
	heartbeatMaxInterval    = 15 * time.Second
)

// Clock abstracts time for deterministic heartbeat testing.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// heartbeatModeInterval returns the heartbeat interval for mode.
func heartbeatModeInterval(mode HeartbeatMode) time.Duration {
	switch mode {
	case HeartbeatTTY:
		return heartbeatIntervalTTY
	default:
		return heartbeatIntervalNonTTY
	}
}

// WithHeartbeatMode selects the heartbeat cadence and returns the receiver.
func (i *Ingester) WithHeartbeatMode(m HeartbeatMode) *Ingester {
	i.heartbeatMode = m
	i.heartbeatInterval = heartbeatModeInterval(m)
	return i
}

// WithClock injects a clock for deterministic heartbeat tests.
func (i *Ingester) WithClock(c Clock) *Ingester {
	i.clock = c
	return i
}

// Phase identifies a stage of a full ingest pass.
type Phase string

const (
	// PhaseWalk is the tree scan; Total is unknown (0) and Done counts files
	// discovered so far.
	PhaseWalk Phase = "walk"
	// PhaseDrift is the warm-start change scan (DriftSetWithProgress): the
	// walk that compares on-disk hashes against the cached index. Done counts
	// files checked; Total is unknown. The engine itself never emits it — the
	// CLI warm-start path synthesizes it from the drift walk callback.
	PhaseDrift Phase = "drift"
	// PhaseParse is the per-file parse+commit loop; Total is known.
	PhaseParse Phase = "parse"
	// PhaseWrite covers the durable node/edge commit inside the meta transaction.
	PhaseWrite Phase = "write"
	// PhaseLink covers the stale purge, reverse-dep index, and cross-file
	// linker pass; indeterminate.
	PhaseLink Phase = "link"
	// PhaseResolve is the whole-repo go/types confirmed-tier pass; indeterminate.
	PhaseResolve Phase = "resolve"
	// PhaseFTS covers the full-text index updates that happen alongside commits.
	PhaseFTS Phase = "fts"
	// PhaseCheckpoint covers the final meta-transaction commit and durability.
	PhaseCheckpoint Phase = "checkpoint"
	// PhaseDone means the pass committed successfully.
	PhaseDone Phase = "done"
)

// ProgressEvent is one observation of full-ingest progress. Events are
// delivered synchronously from the ingesting goroutine; handlers must be fast
// and must not block.
type ProgressEvent struct {
	Phase Phase
	Done  int // files processed so far (walk: files discovered so far)
	Total int // total files; 0 while unknown (walk/link/resolve)
	// Path is the repo-relative file the parse loop just finished; empty for
	// the phases that are not per-file. Renderers use it to show WHICH file
	// the pass is on — the diagnostic that identifies a pathological file.
	Path string
}

// progressPublishMinGap throttles same-phase "ingest-progress" broker
// publishes so a slow SSE reader's 16-deep buffer never drowns in per-file
// events. Phase transitions and PhaseDone always publish.
const progressPublishMinGap = 150 * time.Millisecond

// WithProgress attaches a progress callback and returns the receiver for
// chaining (same pattern as WithBroker). When attached, IngestAll delivers
// phase and per-file progress events; incremental/watcher paths emit nothing.
// Nil = no-op (default).
func (i *Ingester) WithProgress(fn func(ProgressEvent)) *Ingester {
	i.progress = fn
	return i
}

// notifyProgress delivers ev to the attached callback and mirrors it to the
// broker as a throttled "ingest-progress" event. It is nil-safe like
// notifyIngest and never returns an error — progress must not fail ingestion.
func (i *Ingester) notifyProgress(ctx context.Context, ev ProgressEvent) {
	if i.progress != nil {
		i.progress(ev)
	}
	if i.broker == nil {
		return
	}
	now := i.clock.Now()
	// The throttle never swallows a terminal observation: phase transitions,
	// PhaseDone, and the 100% event of a bounded phase (Done == Total) always
	// publish — otherwise an SSE client whose last update landed <150ms before
	// completion would never see the phase finish.
	atCompletion := ev.Total > 0 && ev.Done == ev.Total
	if ev.Phase == i.lastProgressPh && ev.Phase != PhaseDone && !atCompletion && now.Sub(i.lastProgressPub) < progressPublishMinGap {
		return
	}
	i.lastProgressPh = ev.Phase
	i.lastProgressPub = now
	i.lastProgressTime = now
	body := map[string]any{
		"phase": string(ev.Phase),
		"done":  ev.Done,
		"total": ev.Total,
	}
	if ev.Path != "" {
		body["path"] = ev.Path
	}
	payload, _ := json.Marshal(body)
	i.broker.Publish(ctx, observe.Event{Type: "ingest-progress", Ts: now, Payload: payload})
}

func (i *Ingester) heartbeat(ctx context.Context, ph Phase) {
	if i.progress == nil && i.broker == nil {
		return
	}
	now := i.clock.Now()
	if now.Sub(i.lastProgressTime) < i.heartbeatInterval {
		return
	}
	i.notifyProgress(ctx, ProgressEvent{Phase: ph})
}
