package ingest

import (
	"context"
	"database/sql"
	"fmt"
)

func (i *Ingester) initSchema(ctx context.Context) error {
	// edit_provenance is the SW-037 side-channel: the per-edit audit record
	// (source edit id, operation type, timestamp) keyed by the affected
	// NodeId/EdgeId. It deliberately lives here in the ingest meta sidecar — NOT
	// in core/model or model.Graph.Marshal — because the edit id and timestamp are
	// volatile (properties of HOW the graph was last mutated, not of the source
	// content). Embedding them in the marshalled graph would make the AC-3
	// incremental-vs-full digest differ for every edit; keeping them out of the
	// graph is what lets AC-3's structural graphDigest stay byte-identical while
	// AC-1's edit provenance still distinguishes which edit touched what. The
	// dirty_units row carries the same edit context (edit_id/op_type/recorded_at)
	// so RecoverWithRoot reproduces identical side-channel state after a crash
	// (provenance-idempotent recovery).
	// Base DDL is CREATE TABLE IF NOT EXISTS only — it must NEVER be relied upon to
	// add a column to a table that already exists (CREATE TABLE IF NOT EXISTS
	// silently no-ops on an existing table, leaving new columns unapplied). The
	// dirty_units table here is declared with ONLY its original SW-036/EP-001
	// shape (path); the SW-037 edit-context columns are added by the versioned
	// migration ladder below so that a pre-SW-037 on-disk sidecar is migrated in
	// place rather than left with a stale schema. See migrate().
	const ddl = `
CREATE TABLE IF NOT EXISTS file_content_cache (
	path TEXT PRIMARY KEY,
	content_hash TEXT NOT NULL,
	node_ids TEXT NOT NULL,
	last_ingested_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS reverse_deps (
	path TEXT PRIMARY KEY,
	dependents TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS dirty_units (
	path TEXT PRIMARY KEY
);
CREATE TABLE IF NOT EXISTS edit_provenance (
	element_id TEXT NOT NULL,
	element_kind TEXT NOT NULL,
	edit_id TEXT NOT NULL,
	op_type TEXT NOT NULL,
	recorded_at INTEGER NOT NULL,
	PRIMARY KEY(element_id, edit_id)
);
CREATE TABLE IF NOT EXISTS ingest_semantics (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`
	if _, err := i.meta.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("ingest: init schema: %w", err)
	}
	return i.migrate(ctx)
}

// schemaVersion is the current sidecar schema version. Bump it (and add a step
// to migrate) whenever an additive schema change is introduced.
//
//	0 -> 1 : SW-037 — add edit-context columns to dirty_units.
//	1 -> 2 : SW-050 — add has_links flag to file_content_cache (linker cascade).
const schemaVersion = 2

// migrate applies additive schema changes exactly once, gated on PRAGMA
// user_version, so an existing on-disk ingest-meta.db (e.g. one created by a
// pre-SW-037 story with dirty_units(path) only) is upgraded deterministically
// instead of relying on CREATE TABLE IF NOT EXISTS (which cannot add columns to
// an already-existing table). Each step is itself idempotent and column-presence
// guarded, so the ladder is safe even on a fresh DB and on a DB whose
// user_version was never tracked before this story.
func (i *Ingester) migrate(ctx context.Context) error {
	var current int
	if err := i.meta.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("ingest: read user_version: %w", err)
	}
	if current >= schemaVersion {
		return nil
	}
	if current < 1 {
		if err := i.migrateDirtyUnitsEditContext(ctx); err != nil {
			return fmt.Errorf("ingest: migrate dirty_units edit context: %w", err)
		}
	}
	if current < 2 {
		if err := i.migrateCacheHasLinks(ctx); err != nil {
			return fmt.Errorf("ingest: migrate file_content_cache has_links: %w", err)
		}
	}
	// PRAGMA does not accept bound parameters; schemaVersion is a trusted constant.
	if _, err := i.meta.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("ingest: set user_version: %w", err)
	}
	return nil
}

// migrateDirtyUnitsEditContext adds the SW-037 edit-context columns to an
// existing dirty_units table when they are absent. ADD COLUMN with a NOT NULL
// DEFAULT is safe on a populated table. Detection via PRAGMA table_info makes the
// step idempotent regardless of prior user_version tracking.
func (i *Ingester) migrateDirtyUnitsEditContext(ctx context.Context) error {
	have, err := i.columnSet(ctx, "dirty_units")
	if err != nil {
		return err
	}
	adds := []struct {
		col string
		ddl string
	}{
		{"edit_id", "ALTER TABLE dirty_units ADD COLUMN edit_id TEXT NOT NULL DEFAULT ''"},
		{"op_type", "ALTER TABLE dirty_units ADD COLUMN op_type TEXT NOT NULL DEFAULT ''"},
		{"recorded_at", "ALTER TABLE dirty_units ADD COLUMN recorded_at INTEGER NOT NULL DEFAULT 0"},
	}
	for _, a := range adds {
		if _, ok := have[a.col]; ok {
			continue
		}
		if _, err := i.meta.ExecContext(ctx, a.ddl); err != nil {
			return fmt.Errorf("ingest: add column %s: %w", a.col, err)
		}
	}
	return nil
}

// migrateCacheHasLinks adds the SW-050 has_links flag to file_content_cache when
// absent. The flag records whether a file produced deferred linker inputs
// (PendingRefs/Imports) so the same-package-directory sibling cascade only fires
// among genuinely linkable files (real Go), never among unrelated stub files
// that merely share a directory. The step is idempotent (PRAGMA-detected).
func (i *Ingester) migrateCacheHasLinks(ctx context.Context) error {
	have, err := i.columnSet(ctx, "file_content_cache")
	if err != nil {
		return err
	}
	if _, ok := have["has_links"]; ok {
		return nil
	}
	if _, err := i.meta.ExecContext(ctx, "ALTER TABLE file_content_cache ADD COLUMN has_links INTEGER NOT NULL DEFAULT 0"); err != nil {
		return fmt.Errorf("ingest: add column has_links: %w", err)
	}
	return nil
}

// columnSet returns the set of column names on a table via PRAGMA table_info.
// The table name is a trusted in-package literal, never caller-supplied.
func (i *Ingester) columnSet(ctx context.Context, table string) (map[string]struct{}, error) {
	rows, err := i.meta.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil, fmt.Errorf("ingest: table_info(%s): %w", table, err)
	}
	defer rows.Close()
	cols := make(map[string]struct{})
	for rows.Next() {
		var (
			cid        int
			name, ctyp string
			notNull    int
			dfltValue  sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &ctyp, &notNull, &dfltValue, &pk); err != nil {
			return nil, fmt.Errorf("ingest: scan table_info(%s): %w", table, err)
		}
		cols[name] = struct{}{}
	}
	return cols, rows.Err()
}
