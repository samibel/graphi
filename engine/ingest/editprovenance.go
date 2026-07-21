package ingest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// EditOpType is the closed enum of edit operations that may originate a
// provenance record. It mirrors engine/edit's RefactorKind closed-set discipline
// (rename/extract/move/signature_change) plus the base single-op "apply", and a
// synthetic "recovery"-class value is intentionally NOT introduced: recovery
// replays the ORIGINAL edit context (provenance-idempotent recovery), so the
// recorded op_type after a crash+recover is identical to an uninterrupted edit.
type EditOpType string

const (
	// EditOpApply is the base identity-preserving single-op edit (engine/edit.Apply).
	EditOpApply EditOpType = "apply"
	// EditOpRename mirrors edit.RefactorRename.
	EditOpRename EditOpType = "rename"
	// EditOpExtract mirrors edit.RefactorExtract.
	EditOpExtract EditOpType = "extract"
	// EditOpMove mirrors edit.RefactorMove.
	EditOpMove EditOpType = "move"
	// EditOpSignatureChange mirrors edit.RefactorSignatureChange.
	EditOpSignatureChange EditOpType = "signature_change"
	// EditOpInline mirrors edit.RefactorInline (SW-092): substitute a symbol's
	// value at every reference site and remove the declaration.
	EditOpInline EditOpType = "inline"
	// EditOpSafeDelete mirrors edit.RefactorSafeDelete (SW-093): remove a symbol
	// only after a reference-safety gate clears.
	EditOpSafeDelete EditOpType = "safe_delete"
	// EditOpUndo is the SW-038 reversal: an undo restores the pre-edit source +
	// graph snapshot and re-indexes the touched files under this op-type so the
	// edit_provenance row distinguishes a reversal from a forward edit.
	EditOpUndo EditOpType = "undo"
)

// validEditOpTypes is the closed set; unknown values are rejected so the audit
// field cannot be poisoned by an arbitrary caller string.
var validEditOpTypes = map[EditOpType]struct{}{
	EditOpApply:           {},
	EditOpRename:          {},
	EditOpExtract:         {},
	EditOpMove:            {},
	EditOpSignatureChange: {},
	EditOpInline:          {},
	EditOpSafeDelete:      {},
	EditOpUndo:            {},
}

// Valid reports whether t is a member of the closed op-type enum.
func (t EditOpType) Valid() bool {
	_, ok := validEditOpTypes[t]
	return ok
}

// EditProvenance is the per-edit audit context threaded from the engine/edit
// saga into a provenance-aware ingest pass. The EditID is minted ONCE and the
// Timestamp captured ONCE in the saga (a single value shared across the whole
// touched set), never per-element inside ingest. It is recorded into the
// edit_provenance side-channel in Phase 2 of the dirty-flag metaTx, keyed by
// every affected NodeId/EdgeId.
type EditProvenance struct {
	// EditID is the saga-minted identifier of the originating edit.
	EditID string
	// OpType is the closed-enum operation kind.
	OpType EditOpType
	// Timestamp is the Unix-nanosecond wall-clock instant captured once by the saga.
	Timestamp int64
}

// Validate enforces the closed op-type enum and a non-empty edit id. A
// zero-value EditProvenance (no originating edit) is reported as invalid so the
// zero-provenance ingest paths (full IngestAll, plain recovery) opt out by
// passing nil rather than an empty struct.
func (p EditProvenance) Validate() error {
	if strings.TrimSpace(p.EditID) == "" {
		return fmt.Errorf("ingest: empty edit id")
	}
	if !p.OpType.Valid() {
		return fmt.Errorf("ingest: unknown op_type %q", p.OpType)
	}
	return nil
}

// EditProvenanceRecord is one row of the edit_provenance side-channel: the edit
// that last touched an element, keyed by NodeId/EdgeId.
type EditProvenanceRecord struct {
	ElementID   string
	ElementKind string
	EditID      string
	OpType      EditOpType
	RecordedAt  int64
}

// EditProvenance returns every edit_provenance row, sorted by (element_id,
// edit_id), so callers (and SW-038's audit/undo, and the AC-1 tests) can read
// the per-edit audit record. It reads the side-channel only — it never touches
// the graph or the AC-3 structural digest.
func (i *Ingester) EditProvenance(ctx context.Context) ([]EditProvenanceRecord, error) {
	rows, err := i.meta.QueryContext(ctx, `
SELECT element_id, element_kind, edit_id, op_type, recorded_at
FROM edit_provenance
ORDER BY element_id, edit_id`)
	if err != nil {
		return nil, fmt.Errorf("ingest: query edit provenance: %w", err)
	}
	defer rows.Close()
	var out []EditProvenanceRecord
	for rows.Next() {
		var r EditProvenanceRecord
		var op string
		if err := rows.Scan(&r.ElementID, &r.ElementKind, &r.EditID, &op, &r.RecordedAt); err != nil {
			return nil, fmt.Errorf("ingest: scan edit provenance: %w", err)
		}
		r.OpType = EditOpType(op)
		out = append(out, r)
	}
	return out, rows.Err()
}

// recordEditProvenanceTx writes one edit_provenance row per element id, keyed by
// (element_id, edit_id), on the supplied transaction. It is O(touched elements)
// and rides the existing Phase-2 metaTx so the provenance commit is atomic with
// the cache/clear-dirty commit. Re-running the same edit (e.g. crash recovery
// replaying the original edit context) upserts identical rows, so the
// side-channel is idempotent under recovery.
func (i *Ingester) recordEditProvenanceTx(ctx context.Context, tx *sql.Tx, kind string, ids []string, prov EditProvenance) error {
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO edit_provenance (element_id, element_kind, edit_id, op_type, recorded_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(element_id, edit_id) DO UPDATE SET
	element_kind=excluded.element_kind,
	op_type=excluded.op_type,
	recorded_at=excluded.recorded_at`,
			id, kind, prov.EditID, string(prov.OpType), prov.Timestamp); err != nil {
			return fmt.Errorf("ingest: record edit provenance: %w", err)
		}
	}
	return nil
}
