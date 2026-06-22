package edit_test

import (
	"context"
	"testing"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/edit"
	"github.com/samibel/graphi/engine/ingest"
)

// --- AC-1: the saga mints provenance and surfaces edit_id --------------------

func TestApply_RecordsProvenanceAndSurfacesEditID(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, map[string]string{
		"a.go": "package a\nconst X = 1\n",
	})
	// Pin the clock so the recorded timestamp is deterministic.
	fixed := time.Unix(1700000000, 0).UTC()
	h.applier.SetClock(func() time.Time { return fixed })

	res, err := h.applier.Apply(ctx, edit.EditOp{
		FilePath: "a.go",
		ByteSpan: edit.Span{Start: 0, End: 0},
		// Insert a comment line; identity-preserving.
		Replacement: []byte("// edited\n"),
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Outcome != edit.OutcomeApplied {
		t.Fatalf("outcome = %q, want applied", res.Outcome)
	}
	if res.EditID == "" {
		t.Fatal("expected a non-empty surfaced edit id on Result")
	}

	recs, err := h.ing.EditProvenance(ctx)
	if err != nil {
		t.Fatalf("EditProvenance: %v", err)
	}
	if len(recs) == 0 {
		t.Fatal("expected provenance rows after an applied edit")
	}
	for _, r := range recs {
		if r.EditID != res.EditID {
			t.Fatalf("provenance edit id %q != surfaced %q", r.EditID, res.EditID)
		}
		if r.OpType != ingest.EditOpApply {
			t.Fatalf("op_type = %q, want apply", r.OpType)
		}
		if r.RecordedAt != fixed.UnixNano() {
			t.Fatalf("recorded_at = %d, want %d (pinned clock, single timestamp)", r.RecordedAt, fixed.UnixNano())
		}
	}
}

// --- AC-3: incremental == full re-index byte-identical DESPITE differing edit
// metadata. The shipped graphDigest excludes the side-channel, so AC-3 PASSES
// even though the incremental store carries edit provenance the full re-index
// never saw. This is the regression guard for the side-channel separation. -----

func TestApply_ByteIdenticalExcludesEditProvenance(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, map[string]string{
		"a.go": "package a\nconst X = 1\n",
		"b.go": "package b\nuse:a.go\n",
	})

	// The saga's own consistency gate runs graphDigest(live) vs a full re-index;
	// a successful Apply means AC-3 held DESPITE the incremental store now holding
	// edit_provenance rows with an edit id + timestamp the full re-index lacks.
	res, err := h.applier.Apply(ctx, edit.EditOp{
		FilePath:    "a.go",
		ByteSpan:    edit.Span{Start: 0, End: 0},
		Replacement: []byte("// c\n"),
	})
	if err != nil {
		t.Fatalf("Apply (AC-3 gate inside): %v", err)
	}
	if res.Outcome != edit.OutcomeApplied {
		t.Fatalf("outcome = %q, want applied", res.Outcome)
	}

	// The incremental store DOES carry edit provenance...
	recs, err := h.ing.EditProvenance(ctx)
	if err != nil {
		t.Fatalf("EditProvenance: %v", err)
	}
	if len(recs) == 0 {
		t.Fatal("expected the incremental store to carry edit provenance")
	}

	// ...yet a fresh FULL re-index of the post-edit source yields the SAME
	// structural digest as the live incremental store (side-channel excluded).
	fresh := graphstore.NewMemStore()
	defer fresh.Close()
	fi, err := ingest.New(fresh, &stubParser{}, t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	defer fi.Close()
	if err := fi.IngestAll(ctx, h.root); err != nil {
		t.Fatalf("full IngestAll: %v", err)
	}

	liveDigest := marshalDigest(t, h.store)
	fullDigest := marshalDigest(t, fresh)
	if liveDigest != fullDigest {
		t.Fatalf("AC-3 violated: incremental graph diverges from full re-index despite side-channel exclusion")
	}
}

func marshalDigest(t *testing.T, store graphstore.Graphstore) string {
	t.Helper()
	ctx := context.Background()
	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("nodes: %v", err)
	}
	edges, err := store.Edges(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("edges: %v", err)
	}
	b, err := model.NewGraph(nodes, edges).Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
