package ingest

import (
	"context"
	"encoding/json"
	"time"

	"github.com/samibel/graphi/engine/observe"
)

// Phase identifies a stage of a full ingest pass.
type Phase string

const (
	// PhaseWalk is the tree scan; Total is unknown (0) and Done counts files
	// discovered so far.
	PhaseWalk Phase = "walk"
	// PhaseParse is the per-file parse+commit loop; Total is known.
	PhaseParse Phase = "parse"
	// PhaseLink covers the stale purge, reverse-dep index, and cross-file
	// linker pass; indeterminate.
	PhaseLink Phase = "link"
	// PhaseResolve is the whole-repo go/types confirmed-tier pass; indeterminate.
	PhaseResolve Phase = "resolve"
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
	now := time.Now()
	if ev.Phase == i.lastProgressPh && ev.Phase != PhaseDone && now.Sub(i.lastProgressPub) < progressPublishMinGap {
		return
	}
	i.lastProgressPh = ev.Phase
	i.lastProgressPub = now
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
