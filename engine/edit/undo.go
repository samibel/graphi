package edit

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/samibel/graphi/engine/ingest"
)

// pathSep is the OS path separator, used to build saga temp snapshot paths in a
// shape consistent with applyBatch.
const pathSep = os.PathSeparator

// mkUndoTempDir creates a temp dir for the undo compensation snapshot.
func mkUndoTempDir() (string, error) {
	d, err := os.MkdirTemp("", "graphi-undo-comp-*")
	if err != nil {
		return "", fmt.Errorf("%w: create undo compensation dir: %v", ErrWrite, err)
	}
	return d, nil
}

// cleanupDir best-effort removes a temp dir.
func cleanupDir(dir string) {
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

// readFileIfExists reads a file, returning empty bytes (not an error) when the
// file does not exist — a touched file may have been created by the forward edit
// and thus have no pre-undo content under a different name, but for the rename/
// move shapes SW-038 targets the touched paths always exist post-edit.
func readFileIfExists(abs string) ([]byte, error) {
	b, err := os.ReadFile(abs) //nolint:gosec // abs sanitized via resolvePath
	if os.IsNotExist(err) {
		return nil, nil
	}
	return b, err
}

// ApplyRefactorRecorded is the SW-038 recording entry point: it runs the same
// ApplyRefactor saga and, ON SUCCESS, persists a durable audit/undo record via
// the supplied ChangeRecorder — the pre-edit graph snapshot + captured pre-edit
// source bytes keyed by a fresh crypto-random undo token, plus one change_record
// row (operation, target, before/after, actor, timestamp, undo token).
//
// It is the single shared implementation both surfaces reach through
// client.Direct (parity by construction). A DryRun op is rejected here (preview
// has nothing to record; the surface calls RefactorPreview for that). A
// rolled-back saga writes NO change_record row and leaves NO orphan undo snapshot
// (the saga removes the snapshot on rollback; the recorder is never invoked).
func (a *Applier) ApplyRefactorRecorded(ctx context.Context, op RefactorOp, actor string, rec *ChangeRecorder) (ChangeRecord, RefactorResult, error) {
	if rec == nil {
		return ChangeRecord{}, RefactorResult{}, fmt.Errorf("%w: nil change recorder", ErrInvalidOp)
	}
	if op.DryRun {
		return ChangeRecord{}, RefactorResult{}, fmt.Errorf("%w: ApplyRefactorRecorded does not accept DryRun (use RefactorPreview)", ErrInvalidOp)
	}

	out := RefactorResult{Result: Result{TargetNodeID: op.TargetSymbol, Outcome: OutcomeRolledBack}}
	if err := validateRefactorOp(op); err != nil {
		return ChangeRecord{}, out, err
	}
	files, truncated, err := a.blastRadius(ctx, op.TargetSymbol)
	if err != nil {
		return ChangeRecord{}, out, err
	}
	out.ImpactFiles = files
	out.Truncated = truncated
	ops, err := a.planRefactor(op, files)
	if err != nil {
		return ChangeRecord{}, out, err
	}
	out.PlannedOps = ops

	opType, err := opTypeForRefactorKind(op.Kind)
	if err != nil {
		return ChangeRecord{}, out, err
	}
	res, arts, err := a.applyBatch(ctx, ops, opType)
	out.Result = res
	out.Result.TargetNodeID = op.TargetSymbol
	if err != nil {
		// Saga rolled back: no record, no orphan snapshot (applyBatch cleaned up).
		return ChangeRecord{}, out, err
	}

	// Success: persist the undo artifacts + the change_record. If persisting the
	// audit fails we discard the artifacts (no orphan) and surface the error; the
	// graph mutation itself already committed and is consistent.
	record, recErr := a.commitChangeRecord(ctx, rec, recordInputs{
		opType:     string(opType),
		target:     op.TargetSymbol,
		oldName:    op.OldName,
		newName:    op.NewName,
		touched:    res.TouchedFiles,
		actor:      actor,
		editID:     res.EditID,
		recordedAt: a.now().UnixNano(),
		arts:       arts,
	})
	if recErr != nil {
		arts.discard()
		return ChangeRecord{}, out, recErr
	}
	out.Result.UndoToken = record.UndoToken
	return record, out, nil
}

// recordInputs bundles the fields needed to persist one applied-edit change record.
type recordInputs struct {
	opType     string
	target     string
	oldName    string
	newName    string
	touched    []string
	actor      string
	editID     string
	recordedAt int64
	arts       *sagaArtifacts
}

// commitChangeRecord mints an undo token, persists the pre-edit snapshot +
// captured source bytes under it (the undo store), and inserts the change_record
// row. The snapshot dir is taken over by the recorder on success; the saga's
// temp snapshot dir is removed once its bytes are copied into the durable store.
func (a *Applier) commitChangeRecord(ctx context.Context, rec *ChangeRecorder, in recordInputs) (ChangeRecord, error) {
	token, err := mintUndoToken()
	if err != nil {
		return ChangeRecord{}, err
	}
	snapRef, err := rec.persistUndoArtifacts(token, in.arts.snapPath, in.arts.originals)
	if err != nil {
		return ChangeRecord{}, err
	}
	// The durable copy now holds the snapshot; remove the saga's temp dir.
	in.arts.discard()

	touched := append([]string(nil), in.touched...)
	sort.Strings(touched)
	record := ChangeRecord{
		EditID:       in.editID,
		OpType:       in.opType,
		TargetNodeID: in.target,
		OldName:      in.oldName,
		NewName:      in.newName,
		TouchedFiles: touched,
		Actor:        in.actor,
		RecordedAt:   in.recordedAt,
		UndoToken:    token,
		SnapshotRef:  snapRef,
	}
	if err := rec.record(ctx, record); err != nil {
		return ChangeRecord{}, err
	}
	return record, nil
}

// Undo reverses a previously applied edit identified by its undo token. It is a
// SECOND compensating saga (not a raw poke): it looks up the change_record +
// persisted pre-edit snapshot + captured source bytes, restores the source bytes
// atomically (writeFileAtomic), Loads the pre-edit graph snapshot (fail-closed,
// atomic), re-indexes the touched files, runs the consistency check, and finally
// records a NEW change_record row with op_type=undo and reverses_edit_id set to
// the original edit id (AC-3: the reversal is itself auditable). Its own fresh
// undo token makes the undo reversible in turn (auditable-by-construction;
// double-undo is not a required feature).
//
// On ANY fault in the undo restore it compensates: nothing is half-applied. The
// graph snapshot Load is itself atomic (fail-closed), so a failed Load leaves the
// store unmodified. To make a fault AFTER a partial source restore reversible, the
// undo captures the CURRENT (post-edit) source bytes + graph snapshot FIRST and
// restores them on any undo failure, mirroring the forward saga's rollback anchor.
func (a *Applier) Undo(ctx context.Context, undoToken, actor string, rec *ChangeRecorder) (ChangeRecord, error) {
	if rec == nil {
		return ChangeRecord{}, fmt.Errorf("%w: nil change recorder", ErrInvalidOp)
	}
	original, err := rec.ChangeRecordByUndoToken(ctx, undoToken)
	if err != nil {
		return ChangeRecord{}, err
	}
	if original.OpType == string(opTypeUndo) {
		// Undoing an undo is auditable-by-construction but not a supported feature.
		return ChangeRecord{}, fmt.Errorf("%w: cannot undo an undo record", ErrInvalidOp)
	}
	sources, err := rec.loadUndoSources(undoToken)
	if err != nil {
		return ChangeRecord{}, err
	}

	// Compensating-saga anchors: capture the CURRENT (post-edit) source bytes and
	// the CURRENT graph snapshot so a fault mid-undo is fully reversible (no
	// half-applied undo).
	curSnapDir, curSnapPath, curOriginals, err := a.captureUndoCompensationAnchors(ctx, sources)
	if err != nil {
		return ChangeRecord{}, err
	}
	defer cleanupDir(curSnapDir)

	compensate := func(cause error) (ChangeRecord, error) {
		var rbErrs []string
		for rel, content := range curOriginals {
			abs := joinRoot(a.root, rel)
			if rerr := writeFileAtomic(abs, content); rerr != nil {
				rbErrs = append(rbErrs, fmt.Sprintf("restore %s: %v", rel, rerr))
			}
		}
		if rerr := a.store.Load(ctx, curSnapPath); rerr != nil {
			rbErrs = append(rbErrs, fmt.Sprintf("restore graph: %v", rerr))
		}
		if len(rbErrs) > 0 {
			return ChangeRecord{}, fmt.Errorf("%w: undo compensation failed: %v (original fault: %v)", ErrRollback, rbErrs, cause)
		}
		return ChangeRecord{}, cause
	}

	// Step 1: restore the captured pre-edit source bytes atomically.
	touched := make([]string, 0, len(sources))
	for rel := range sources {
		touched = append(touched, rel)
	}
	sort.Strings(touched)
	for _, rel := range touched {
		abs, err := a.resolveUndoPath(rel)
		if err != nil {
			return compensate(err)
		}
		if err := writeFileAtomic(abs, sources[rel]); err != nil {
			return compensate(fmt.Errorf("%w: restore source %s: %v", ErrWrite, rel, err))
		}
	}

	// Step 2: Load the pre-edit graph snapshot (fail-closed, atomic).
	if err := a.store.Load(ctx, original.SnapshotRef); err != nil {
		return compensate(fmt.Errorf("%w: load pre-edit snapshot: %v", ErrReindex, err))
	}

	// Step 3: re-index the touched files so the ingest sidecar (cache/reverse-deps)
	// matches the restored source, carrying undo provenance.
	prov, err := a.newEditProvenance(ingest.EditOpUndo)
	if err != nil {
		return compensate(err)
	}
	if err := a.ingester.IngestChangedWithProvenance(ctx, a.root, touched, prov); err != nil {
		return compensate(fmt.Errorf("%w: re-index after undo: %v", ErrReindex, err))
	}

	// Step 4: consistency gate — the restored graph must equal a full re-index of
	// the restored source. The inherited fault seam is honored here so a test can
	// force the undo saga's own failure path (compensation).
	if a.fault == faultConsistencyCheck {
		a.setFault(faultNone)
		return compensate(fmt.Errorf("%w: injected inconsistency", ErrInconsistent))
	}
	checker := a.checker
	if checker == nil {
		checker = DefaultConsistencyChecker
	}
	if err := checker.Check(ctx, a.store, a.ingester, a.root); err != nil {
		return compensate(fmt.Errorf("%w: post-undo consistency: %v", ErrInconsistent, err))
	}

	// Step 5: record the reversal as its OWN auditable change_record. The undo
	// itself persists a fresh undo snapshot (the now-restored pre-edit graph), so
	// it is reversible in turn. Its op_type=undo and reverses_edit_id link it to
	// the original (AC-3).
	undoToken2, err := mintUndoToken()
	if err != nil {
		return compensate(err)
	}
	undoSnapRef, err := rec.persistUndoArtifacts(undoToken2, curSnapPath, curOriginals)
	if err != nil {
		return compensate(err)
	}
	reversal := ChangeRecord{
		EditID:         prov.EditID,
		OpType:         string(opTypeUndo),
		TargetNodeID:   original.TargetNodeID,
		OldName:        original.NewName, // before-undo name was the new name…
		NewName:        original.OldName, // …after-undo it is back to the old name.
		TouchedFiles:   touched,
		Actor:          actor,
		RecordedAt:     a.now().UnixNano(),
		UndoToken:      undoToken2,
		SnapshotRef:    undoSnapRef,
		ReversesEditID: original.EditID,
	}
	if err := rec.record(ctx, reversal); err != nil {
		return compensate(err)
	}
	return reversal, nil
}

// opTypeUndo is the change_record op_type for a reversal. It equals the ingest
// closed-enum EditOpUndo so the undo re-index records edit_provenance under a
// label that distinguishes a reversal from a forward edit, and the change_record
// op_type column reads the same value.
const opTypeUndo = ingest.EditOpUndo

// captureUndoCompensationAnchors snapshots the CURRENT graph and reads the CURRENT
// (post-edit) bytes of every file the undo will touch, so a fault mid-undo can be
// fully compensated back to the pre-undo state.
func (a *Applier) captureUndoCompensationAnchors(ctx context.Context, sources map[string][]byte) (snapDir, snapPath string, originals map[string][]byte, err error) {
	snapDir, err = mkUndoTempDir()
	if err != nil {
		return "", "", nil, err
	}
	snapPath = snapDir + string(pathSep) + "pre-undo.snapshot"
	if serr := a.store.Snapshot(ctx, snapPath); serr != nil {
		cleanupDir(snapDir)
		return "", "", nil, fmt.Errorf("%w: snapshot pre-undo graph: %v", ErrWrite, serr)
	}
	originals = make(map[string][]byte, len(sources))
	for rel := range sources {
		abs, perr := a.resolveUndoPath(rel)
		if perr != nil {
			cleanupDir(snapDir)
			return "", "", nil, perr
		}
		b, rerr := readFileIfExists(abs)
		if rerr != nil {
			cleanupDir(snapDir)
			return "", "", nil, fmt.Errorf("%w: read current %s: %v", ErrInvalidOp, rel, rerr)
		}
		originals[rel] = b
	}
	return snapDir, snapPath, originals, nil
}

// resolveUndoPath sanitizes a repo-relative path from a stored undo record back to
// an absolute on-disk path within the repo root (the same invariant resolvePath
// enforces for forward edits).
func (a *Applier) resolveUndoPath(rel string) (string, error) {
	_, abs, err := a.resolvePath(rel)
	if err != nil {
		return "", err
	}
	return abs, nil
}
