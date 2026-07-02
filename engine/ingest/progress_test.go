package ingest_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/observe"
)

// TestIngestAll_ProgressEvents asserts the full-ingest event sequence over a
// small repo: walk first, then one parse event per file with a monotonically
// increasing Done and the known Total, then link, resolve, and a final done.
func TestIngestAll_ProgressEvents(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	defer store.Close()
	i := newIngester(t, store, &stubParser{})

	repo := writeRepo(t, map[string]string{
		"a.go": "package a\n",
		"b.go": "package b\n",
		"c.go": "package c\n",
		"d.go": "package d\n",
		"e.go": "package e\n",
	})

	var events []ingest.ProgressEvent
	i.WithProgress(func(ev ingest.ProgressEvent) { events = append(events, ev) })

	if err := i.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no progress events delivered")
	}
	if events[0].Phase != ingest.PhaseWalk {
		t.Fatalf("first event phase = %q, want %q", events[0].Phase, ingest.PhaseWalk)
	}
	last := events[len(events)-1]
	if last.Phase != ingest.PhaseDone || last.Done != 5 || last.Total != 5 {
		t.Fatalf("last event = %+v, want done{5,5}", last)
	}

	var parseDones []int
	phaseOrder := map[ingest.Phase]int{ingest.PhaseWalk: 0, ingest.PhaseParse: 1, ingest.PhaseLink: 2, ingest.PhaseResolve: 3, ingest.PhaseDone: 4}
	prevOrder := -1
	for _, ev := range events {
		ord, ok := phaseOrder[ev.Phase]
		if !ok {
			t.Fatalf("unknown phase %q", ev.Phase)
		}
		if ord < prevOrder {
			t.Fatalf("phase %q after a later phase (events: %+v)", ev.Phase, events)
		}
		prevOrder = ord
		if ev.Phase == ingest.PhaseParse {
			if ev.Total != 5 {
				t.Fatalf("parse event Total = %d, want 5", ev.Total)
			}
			if ev.Done > 0 {
				parseDones = append(parseDones, ev.Done)
			}
		}
	}
	if len(parseDones) != 5 {
		t.Fatalf("per-file parse events = %v, want [1 2 3 4 5]", parseDones)
	}
	for k, d := range parseDones {
		if d != k+1 {
			t.Fatalf("parse Done sequence = %v, want [1 2 3 4 5]", parseDones)
		}
	}
}

// TestIngestAll_ProgressNilSafe guards the default path: no WithProgress, no
// broker — IngestAll must run exactly as before.
func TestIngestAll_ProgressNilSafe(t *testing.T) {
	store := graphstore.NewMemStore()
	defer store.Close()
	i := newIngester(t, store, &stubParser{})
	repo := writeRepo(t, map[string]string{"a.go": "package a\n"})
	if err := i.IngestAll(context.Background(), repo); err != nil {
		t.Fatalf("IngestAll without progress callback: %v", err)
	}
}

// TestIngestAll_ProgressBrokerEvent asserts the throttled "ingest-progress"
// mirror: every phase transition bypasses the throttle, so a subscriber sees
// at least one event per phase, and "ingest-completed" still arrives last.
func TestIngestAll_ProgressBrokerEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := graphstore.NewMemStore()
	defer store.Close()
	i := newIngester(t, store, &stubParser{})
	b := observe.New()
	i.WithBroker(b)
	ch := b.Subscribe(ctx)

	repo := writeRepo(t, map[string]string{"a.go": "package a\n", "b.go": "package b\n"})
	if err := i.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	phases := map[string]bool{}
	completed := false
	deadline := time.After(5 * time.Second)
	for !completed {
		select {
		case ev := <-ch:
			switch ev.Type {
			case "ingest-progress":
				var p struct {
					Phase string `json:"phase"`
				}
				if err := json.Unmarshal(ev.Payload, &p); err != nil {
					t.Fatalf("bad ingest-progress payload %q: %v", ev.Payload, err)
				}
				phases[p.Phase] = true
			case "ingest-completed":
				completed = true
			}
		case <-deadline:
			t.Fatalf("timed out; phases seen: %v", phases)
		}
	}
	for _, want := range []ingest.Phase{ingest.PhaseWalk, ingest.PhaseParse, ingest.PhaseLink, ingest.PhaseResolve, ingest.PhaseDone} {
		if !phases[string(want)] {
			t.Fatalf("no ingest-progress event for phase %q (saw %v)", want, phases)
		}
	}
}

// TestIngestAll_NoDoneOnError asserts the error contract: when the pass fails
// (here: nonexistent root fails the walk), no PhaseDone event is emitted —
// line cleanup is the caller's job, not the event stream's.
func TestIngestAll_NoDoneOnError(t *testing.T) {
	store := graphstore.NewMemStore()
	defer store.Close()
	i := newIngester(t, store, &stubParser{})

	var events []ingest.ProgressEvent
	i.WithProgress(func(ev ingest.ProgressEvent) { events = append(events, ev) })

	if err := i.IngestAll(context.Background(), filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("IngestAll on a nonexistent root must fail")
	}
	for _, ev := range events {
		if ev.Phase == ingest.PhaseDone {
			t.Fatalf("PhaseDone emitted on a failed pass (events: %+v)", events)
		}
	}
}

// TestDriftSet_NoProgress pins that only the full-ingest path reports walk
// progress: the watcher's reconcile primitive must stay silent even with a
// callback attached.
func TestDriftSet_NoProgress(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	defer store.Close()
	i := newIngester(t, store, &stubParser{})
	repo := writeRepo(t, map[string]string{"a.go": "package a\n"})
	if err := i.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	var events []ingest.ProgressEvent
	i.WithProgress(func(ev ingest.ProgressEvent) { events = append(events, ev) })
	if _, _, err := i.DriftSet(ctx, repo); err != nil {
		t.Fatalf("DriftSet: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("DriftSet emitted %d progress events, want 0: %+v", len(events), events)
	}
}
