package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ingestSemanticsVersion identifies the SEMANTICS of the graph a full pass
// produces — extractor naming, linker resolution, the typeresolve confirmed
// tier, edge provenance shapes. It is stored in the meta sidecar by a
// successful full pass and checked by CanWarmStart: content hashes alone
// cannot detect that the BINARY changed, so without this stamp an upgraded
// graphi would greet an old store with "up to date" and serve a graph the
// current code would never produce.
//
// Bump whenever identical source bytes would ingest into a different graph:
//
//	1 : v0.2.x — go/types confirmed tier (engine/typeresolve) live.
const ingestSemanticsVersion = "1"

// CanWarmStart reports whether the meta sidecar holds a reusable prior index:
// a non-empty file cache written under the CURRENT ingest semantics. files is
// the cached file count (0 ⇒ cold). Callers use this to replace a full
// re-index with a drift pass (DriftSet + IngestChangedWithProgress); any
// error or mismatch means "start cold", never "trust the store".
func (i *Ingester) CanWarmStart(ctx context.Context) (files int, ok bool, err error) {
	if err := i.meta.QueryRowContext(ctx, "SELECT COUNT(*) FROM file_content_cache").Scan(&files); err != nil {
		return 0, false, fmt.Errorf("ingest: warm-start probe: %w", err)
	}
	if files == 0 {
		return 0, false, nil
	}
	var v string
	err = i.meta.QueryRowContext(ctx, "SELECT value FROM ingest_semantics WHERE key = 'semantics_version'").Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return files, false, nil // pre-stamp store (older binary): re-index once
	}
	if err != nil {
		return files, false, fmt.Errorf("ingest: warm-start stamp: %w", err)
	}
	return files, v == ingestSemanticsVersion, nil
}

// stampSemanticsTx records the current ingest semantics on the supplied
// transaction. Called at the end of a successful FULL pass only — an
// incremental pass never changes semantics, and a store without the stamp
// must stay cold until a full pass under the current binary has run.
func (i *Ingester) stampSemanticsTx(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx,
		"INSERT INTO ingest_semantics(key, value) VALUES('semantics_version', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		ingestSemanticsVersion)
	if err != nil {
		return fmt.Errorf("ingest: stamp semantics: %w", err)
	}
	return nil
}
