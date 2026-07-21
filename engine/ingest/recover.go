package ingest

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// Recover reprocesses any units that were marked dirty but not cleared (e.g.
// after a crash). It returns nil when the dirty set is empty.
func (i *Ingester) Recover(ctx context.Context) error {
	if err := i.guardReadOnly(); err != nil {
		return err
	}
	rows, err := i.meta.QueryContext(ctx, "SELECT path FROM dirty_units")
	if err != nil {
		return fmt.Errorf("ingest: recover query dirty: %w", err)
	}
	defer rows.Close()
	var dirty []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return fmt.Errorf("ingest: recover scan dirty: %w", err)
		}
		dirty = append(dirty, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(dirty) == 0 {
		return nil
	}

	// Reprocess dirty units in a full IngestChanged pass. We do not know the
	// original root here, so Recover requires the caller to supply it.
	return errors.New("ingest: Recover requires root; use RecoverWithRoot")
}

// RecoverWithRoot reprocesses dirty units relative to root. It reads the edit
// context persisted on each dirty row and replays the SAME provenance, so the
// edit_provenance side-channel ends in the identical state an uninterrupted edit
// would have produced (provenance-idempotent recovery). Dirty rows are grouped
// by their edit context: rows that carry a (edit_id, op_type, recorded_at) are
// re-ingested through the provenance-aware path; rows with no edit context
// (full-ingest leftovers) are re-ingested without provenance.
func (i *Ingester) RecoverWithRoot(ctx context.Context, root string) error {
	if err := i.guardReadOnly(); err != nil {
		return err
	}
	rows, err := i.meta.QueryContext(ctx, "SELECT path, edit_id, op_type, recorded_at FROM dirty_units")
	if err != nil {
		return fmt.Errorf("ingest: recover query dirty: %w", err)
	}
	defer rows.Close()

	type editKey struct {
		editID     string
		opType     string
		recordedAt int64
	}
	groups := make(map[editKey][]string)
	var order []editKey
	for rows.Next() {
		var p, editID, opType string
		var recordedAt int64
		if err := rows.Scan(&p, &editID, &opType, &recordedAt); err != nil {
			return fmt.Errorf("ingest: recover scan dirty: %w", err)
		}
		k := editKey{editID: editID, opType: opType, recordedAt: recordedAt}
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], p)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	if len(order) == 0 {
		return nil
	}

	// Deterministic replay order.
	sort.Slice(order, func(a, b int) bool {
		if order[a].editID != order[b].editID {
			return order[a].editID < order[b].editID
		}
		return order[a].recordedAt < order[b].recordedAt
	})

	for _, k := range order {
		paths := groups[k]
		if k.editID == "" {
			if err := i.IngestChanged(ctx, root, paths); err != nil {
				return err
			}
			continue
		}
		prov := EditProvenance{EditID: k.editID, OpType: EditOpType(k.opType), Timestamp: k.recordedAt}
		if err := i.IngestChangedWithProvenance(ctx, root, paths, prov); err != nil {
			return err
		}
	}
	return nil
}
