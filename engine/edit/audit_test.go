package edit_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/edit"
	"github.com/samibel/graphi/engine/ingest"
)

// arharness extends the refactor harness with a persistent meta dir and a
// ChangeRecorder so audit/undo durability can be asserted across a store reopen.
type arharness struct {
	*rharness
	metaDir  string
	recorder *edit.ChangeRecorder
}

// newAuditHarness builds a refactor harness whose ingest-meta sidecar lives in a
// durable (non-temp-cleaned-per-call) dir so the change_record table survives a
// recorder/store reopen, plus a ChangeRecorder over that sidecar.
func newAuditHarness(t *testing.T, files map[string]string) *arharness {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	metaDir := t.TempDir()
	ing, err := ingest.New(store, &refactorParser{}, metaDir)
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	t.Cleanup(func() { _ = ing.Close() })
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	checker := edit.NewParserConsistencyChecker(func() (graphstore.Graphstore, *ingest.Ingester, func(), error) {
		fs := graphstore.NewMemStore()
		fi, ierr := ingest.New(fs, &refactorParser{}, t.TempDir())
		if ierr != nil {
			return nil, nil, nil, ierr
		}
		return fs, fi, func() { _ = fi.Close(); _ = fs.Close() }, nil
	})
	applier, err := edit.NewApplier(store, ing, root, checker)
	if err != nil {
		t.Fatalf("NewApplier: %v", err)
	}
	recorder, err := edit.NewChangeRecorder(ctx, ing, metaDir)
	if err != nil {
		t.Fatalf("NewChangeRecorder: %v", err)
	}
	h := &rharness{root: root, store: store, ing: ing, parser: &refactorParser{}, applier: applier}
	return &arharness{rharness: h, metaDir: metaDir, recorder: recorder}
}

// AC-2: an applied refactor yields exactly one durable change record with all
// required fields populated, and that record survives a store/recorder reopen.
func TestChangeRecorder_AuditPersistenceAndDurability(t *testing.T) {
	ctx := context.Background()
	h := newAuditHarness(t, map[string]string{
		"a_def.go": "def:Widget\n",
		"b_use.go": "def:UseB\nref:a_def.go:Widget\nWidget()\n",
	})
	fixed := time.Unix(1700009999, 0).UTC()
	h.applier.SetClock(func() time.Time { return fixed })

	rec, _, err := h.applier.ApplyRefactorRecorded(ctx, edit.RefactorOp{
		Kind:         edit.RefactorRename,
		TargetSymbol: h.symbolID("Widget", "a_def.go"),
		OldName:      "Widget",
		NewName:      "Gadget",
	}, "tester", h.recorder)
	if err != nil {
		t.Fatalf("ApplyRefactorRecorded: %v", err)
	}
	if rec.OpType != "rename" {
		t.Fatalf("op_type = %q, want rename", rec.OpType)
	}
	if rec.Actor != "tester" {
		t.Fatalf("actor = %q, want tester", rec.Actor)
	}
	if rec.RecordedAt != fixed.UnixNano() {
		t.Fatalf("recorded_at = %d, want %d (pinned clock)", rec.RecordedAt, fixed.UnixNano())
	}
	if rec.UndoToken == "" {
		t.Fatal("undo token empty")
	}
	if rec.SnapshotRef == "" {
		t.Fatal("snapshot_ref empty")
	}
	// The snapshot ref must resolve to a real persisted file.
	if _, err := os.Stat(rec.SnapshotRef); err != nil {
		t.Fatalf("snapshot_ref not resolvable: %v", err)
	}
	// Exactly one record so far.
	all, err := h.recorder.ChangeRecords(ctx)
	if err != nil {
		t.Fatalf("ChangeRecords: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(all))
	}

	// Durability: reopen a fresh recorder over the SAME meta dir (the sidecar is
	// on disk, not in memory) and the record is still there.
	reopened, err := edit.NewChangeRecorder(ctx, h.ing, h.metaDir)
	if err != nil {
		t.Fatalf("reopen recorder: %v", err)
	}
	got, err := reopened.ChangeRecordByUndoToken(ctx, rec.UndoToken)
	if err != nil {
		t.Fatalf("lookup after reopen: %v", err)
	}
	if got.EditID != rec.EditID || got.Actor != "tester" {
		t.Fatalf("durable record mismatch: %+v", got)
	}
}

// AC-2 (negative): a ROLLED-BACK edit writes NO change record and leaves NO
// orphan undo snapshot.
func TestChangeRecorder_RolledBackEditLeavesNoArtifacts(t *testing.T) {
	ctx := context.Background()
	h := newAuditHarness(t, map[string]string{
		"a_def.go": "def:Widget\n",
		"b_use.go": "def:UseB\nref:a_def.go:Widget\nWidget()\n",
		"c_use.go": "def:UseC\nref:a_def.go:Widget\nWidget()\n",
	})
	graphBefore := h.liveDigest(t)

	h.applier.SetBatchFaultHook(2) // fail writing the 2nd touched file
	_, _, err := h.applier.ApplyRefactorRecorded(ctx, edit.RefactorOp{
		Kind:         edit.RefactorRename,
		TargetSymbol: h.symbolID("Widget", "a_def.go"),
		OldName:      "Widget",
		NewName:      "Gadget",
	}, "tester", h.recorder)
	if err == nil {
		t.Fatal("expected rollback error")
	}
	// No change record.
	all, err := h.recorder.ChangeRecords(ctx)
	if err != nil {
		t.Fatalf("ChangeRecords: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("rolled-back edit left %d change records, want 0", len(all))
	}
	// No orphan undo snapshot: the undo store dir is empty.
	undoDir := filepath.Join(h.metaDir, "undo")
	entries, _ := os.ReadDir(undoDir)
	if len(entries) != 0 {
		t.Fatalf("rolled-back edit left %d undo-store entries, want 0", len(entries))
	}
	// Graph unchanged.
	if h.liveDigest(t) != graphBefore {
		t.Fatal("graph not rolled back")
	}
}

// AC-3: undo round-trip across the implemented refactor kinds and single/multi-
// file blast radii — the graph digest and every touched source file are
// byte-identical to pre-edit, and a reversal record with reverses_edit_id is
// written. (extract/move fail closed since SW-112 / SAFE-01 and can never mint
// an undo record.)
func TestUndo_RoundTripByteIdentical(t *testing.T) {
	kinds := []struct {
		name string
		kind edit.RefactorKind
	}{
		{"rename", edit.RefactorRename},
		{"signature_change", edit.RefactorSignatureChange},
	}
	radii := []struct {
		name  string
		files map[string]string
	}{
		{"single-file", map[string]string{"a_def.go": "def:Widget\n"}},
		{"multi-file", map[string]string{
			"a_def.go": "def:Widget\n",
			"b_use.go": "def:UseB\nref:a_def.go:Widget\nWidget()\n",
			"c_use.go": "def:UseC\nref:a_def.go:Widget\nWidget()\n",
		}},
	}
	for _, k := range kinds {
		for _, r := range radii {
			t.Run(k.name+"/"+r.name, func(t *testing.T) {
				ctx := context.Background()
				h := newAuditHarness(t, r.files)

				graphBefore := h.liveDigest(t)
				srcBefore := map[string]string{}
				for f := range r.files {
					srcBefore[f] = h.read(t, f)
				}

				rec, _, err := h.applier.ApplyRefactorRecorded(ctx, edit.RefactorOp{
					Kind:         k.kind,
					TargetSymbol: h.symbolID("Widget", "a_def.go"),
					OldName:      "Widget",
					NewName:      "Gadget",
				}, "tester", h.recorder)
				if err != nil {
					t.Fatalf("apply: %v", err)
				}
				if h.liveDigest(t) == graphBefore {
					t.Fatal("expected the graph to change after the refactor")
				}

				reversal, err := h.applier.Undo(ctx, rec.UndoToken, "tester", h.recorder)
				if err != nil {
					t.Fatalf("undo: %v", err)
				}
				// AC-3 (a): graph digest byte-identical to pre-edit.
				if h.liveDigest(t) != graphBefore {
					t.Fatalf("post-undo graph digest != pre-edit digest")
				}
				// AC-3 (b): every touched source file byte-identical to pre-edit.
				for f, want := range srcBefore {
					if got := h.read(t, f); got != want {
						t.Fatalf("file %s not restored: %q != %q", f, got, want)
					}
				}
				// AC-3 (c): reversal record with op_type=undo + reverses_edit_id.
				if reversal.OpType != "undo" {
					t.Fatalf("reversal op_type = %q, want undo", reversal.OpType)
				}
				if reversal.ReversesEditID != rec.EditID {
					t.Fatalf("reverses_edit_id = %q, want %q", reversal.ReversesEditID, rec.EditID)
				}
			})
		}
	}
}

// AC-3 (compensation): a fault in the undo saga itself compensates — the
// post-edit state is left intact (no half-applied undo). We force the
// consistency-check fault during the undo re-index window.
func TestUndo_FaultCompensates(t *testing.T) {
	ctx := context.Background()
	h := newAuditHarness(t, map[string]string{
		"a_def.go": "def:Widget\n",
		"b_use.go": "def:UseB\nref:a_def.go:Widget\nWidget()\n",
	})
	rec, _, err := h.applier.ApplyRefactorRecorded(ctx, edit.RefactorOp{
		Kind:         edit.RefactorRename,
		TargetSymbol: h.symbolID("Widget", "a_def.go"),
		OldName:      "Widget",
		NewName:      "Gadget",
	}, "tester", h.recorder)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	postEditDigest := h.liveDigest(t)
	postEditSrc := h.read(t, "a_def.go")

	// Force the undo's consistency gate to fail; the undo must compensate back to
	// the post-edit state (no half-applied undo).
	h.applier.SetFaultHook(edit.FaultConsistencyCheck)
	if _, err := h.applier.Undo(ctx, rec.UndoToken, "tester", h.recorder); err == nil {
		t.Fatal("expected undo to fail under the injected consistency fault")
	}
	if h.liveDigest(t) != postEditDigest {
		t.Fatal("faulted undo left the graph half-applied")
	}
	if h.read(t, "a_def.go") != postEditSrc {
		t.Fatal("faulted undo left source half-applied")
	}
}

// Undo-store path safety: the persisted snapshot lives WITHIN the meta dir.
func TestChangeRecorder_UndoSnapshotWithinMetaDir(t *testing.T) {
	ctx := context.Background()
	h := newAuditHarness(t, map[string]string{"a_def.go": "def:Widget\n"})
	rec, _, err := h.applier.ApplyRefactorRecorded(ctx, edit.RefactorOp{
		Kind:         edit.RefactorRename,
		TargetSymbol: h.symbolID("Widget", "a_def.go"),
		OldName:      "Widget",
		NewName:      "Gadget",
	}, "tester", h.recorder)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	rel, err := filepath.Rel(h.metaDir, rec.SnapshotRef)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || len(rel) >= 2 && rel[:2] == ".." {
		t.Fatalf("snapshot_ref %q escapes meta dir %q (rel=%q)", rec.SnapshotRef, h.metaDir, rel)
	}
}
