package ingest_test

// RED GATE (WP-02 / gate-first). Scorecard row #5: "Link progress interval —
// minutes of silence → < 2s between events". The field finding was that on an
// 11.7k-file monorepo the link phase emitted progress exactly ONCE (a single
// PhaseLink event after all cross-file linking), so for minutes only clock
// heartbeats — not real progress — kept the phase from tripping the silence
// guard. WP-02 emits incremental PhaseLink events from inside linkFiles as each
// batch of files is resolved and committed.
//
// Wall-clock timing between events is flaky under CI load, so this gate uses the
// deterministic proxy the plan specifies: the link phase must emit at least TWO
// distinct PhaseLink events with STRICTLY INCREASING Done and a populated,
// non-zero Total — i.e. link is a stream of climbing progress, no longer a
// single silent block. On a real repo those events are seconds apart; here the
// event count + increasing Done stands in for "< 2s between events".

import (
	"context"
	"fmt"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
)

func TestLinkProgress_Incremental(t *testing.T) {
	ctx := context.Background()

	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })

	// A multi-module Java repo with many cross-file references: enough files to
	// span several linkProgressBatchSize batches, so the link phase MUST emit
	// more than one event once progress is real. 8 modules x 3 packages x 8
	// files + 8 app files = 200 linkable files.
	repo := writeRepo(t, genJavaFanoutRepo(8, 8))
	ing := newIngester(t, store, parse.NewDefaultRegistry())

	var link []ingest.ProgressEvent
	ing.WithProgress(func(ev ingest.ProgressEvent) {
		if ev.Phase == ingest.PhaseLink {
			link = append(link, ev)
		}
	})

	if err := ing.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	t.Logf("[GATE WP-02] link phase emitted %d PhaseLink events; Done sequence: %s",
		len(link), doneSeq(link))

	if len(link) < 2 {
		t.Fatalf("link phase emitted %d PhaseLink events, want >= 2 (link is still a single silent block): %+v", len(link), link)
	}

	// Done must strictly increase across the link phase, and Total must be a real
	// (non-zero) denominator on every event — proving Done is climbing toward a
	// known Total, not a lone 0/0 or a single 100%% flash.
	prev := -1
	for _, ev := range link {
		if ev.Total <= 0 {
			t.Fatalf("PhaseLink event has non-positive Total (%d): %+v (whole stream: %+v)", ev.Total, ev, link)
		}
		if ev.Done <= prev {
			t.Fatalf("PhaseLink Done not strictly increasing: got %d after %d (whole stream: %+v)", ev.Done, prev, link)
		}
		if ev.Done > ev.Total {
			t.Fatalf("PhaseLink Done %d exceeds Total %d: %+v", ev.Done, ev.Total, link)
		}
		prev = ev.Done
	}

	// The phase must actually finish at 100%%: the final event reaches Total.
	last := link[len(link)-1]
	if last.Done != last.Total {
		t.Fatalf("final PhaseLink event = %+v, want Done == Total (link completed): %+v", last, link)
	}
}

func doneSeq(evs []ingest.ProgressEvent) string {
	out := ""
	for i, ev := range evs {
		if i > 0 {
			out += "->"
		}
		out += fmt.Sprintf("%d/%d", ev.Done, ev.Total)
	}
	return out
}
