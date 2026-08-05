package ingest_test

// Adversarial pins for the evidence write path (trust_evidence.go): the
// publish-window discipline must hold for the DETAIL rows exactly as it does
// for the snapshot triple. The attack — a full pass whose evidence write fails
// after the graph committed — was held by the production code (the pass fails
// loudly before finishFullPass, the marker stays open, readers derive
// INCOMPLETE) and is pinned here as a regression gate.

import (
	"context"
	"errors"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/trust"
)

// TestTrustEvidence_WriteFailureFailsThePass — the silent-skip attack on the
// evidence write: after a certified pass, a second full pass whose evidence
// publish fails (injected via a sidecar trigger that aborts the wipe) must
// fail IngestAll loudly, must NOT certify the sidecar (the full-pass marker
// stays open, so finishFullPass never ran), and readers must derive
// INCOMPLETE — never a certified graph with silently missing evidence rows.
// After the fault clears, the next pass recovers to CURRENT and serves rows.
func TestTrustEvidence_WriteFailureFailsThePass(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, typeresolveFixture())
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll (seed): %v", err)
	}
	certifiedGen := liveGeneration(ctx, t, store)

	// Injected fault: the evidence publish begins by wiping the tables; a
	// BEFORE DELETE trigger aborts that wipe, failing the metaTx exactly
	// inside the publish window (after the graph's own commits).
	if _, err := ing.MetaDB().ExecContext(ctx, `CREATE TRIGGER fail_evidence_wipe
		BEFORE DELETE ON trust_file_evidence
		BEGIN SELECT RAISE(ABORT, 'injected evidence write failure'); END`); err != nil {
		t.Fatalf("install trigger: %v", err)
	}

	err := ing.IngestAll(ctx, root)
	if err == nil {
		t.Fatal("IngestAll succeeded although the evidence write failed — silent skip inside the publish window")
	}
	marker, merr := ing.FullPassInProgress(ctx)
	if merr != nil {
		t.Fatalf("FullPassInProgress: %v", merr)
	}
	if !marker {
		t.Fatal("full-pass marker cleared although the evidence write failed — finishFullPass ran past a failed publish")
	}
	_, state, serr := trust.Evaluate(ctx, store, trustFreshness(ctx, t, ing, root), liveGeneration(ctx, t, store))
	if serr != nil {
		t.Fatalf("Evaluate: %v", serr)
	}
	if state == trust.StateCurrent {
		t.Fatalf("FALSE GREEN: a pass that failed its evidence write reads CURRENT")
	}
	if state != trust.StateIncomplete {
		t.Errorf("state = %s, want INCOMPLETE while the marker is open", state)
	}
	// The failed pass minted a new graph generation; the sidecar must still
	// serve the LAST CERTIFIED pass's rows or nothing — never rows minted by
	// the failed pass under its uncertified generation.
	failedGen := liveGeneration(ctx, t, store)
	if failedGen != certifiedGen {
		if _, ferr := ing.FileEvidence(ctx, failedGen, "main.go"); !errors.Is(ferr, ingest.ErrTrustEvidenceNotFound) {
			t.Errorf("FileEvidence under the failed pass's generation: err %v, want ErrTrustEvidenceNotFound", ferr)
		}
	}

	// Fault cleared: the next pass recovers to a certified CURRENT and the
	// evidence rows are served under the new generation.
	if _, err := ing.MetaDB().ExecContext(ctx, "DROP TRIGGER fail_evidence_wipe"); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll (recovery): %v", err)
	}
	_, state, serr = trust.Evaluate(ctx, store, trustFreshness(ctx, t, ing, root), liveGeneration(ctx, t, store))
	if serr != nil {
		t.Fatalf("Evaluate (recovery): %v", serr)
	}
	if state != trust.StateCurrent {
		t.Errorf("state = %s, want CURRENT after the recovery pass", state)
	}
	if _, err := ing.FileEvidence(ctx, liveGeneration(ctx, t, store), "main.go"); err != nil {
		t.Errorf("FileEvidence after recovery: %v", err)
	}
}
