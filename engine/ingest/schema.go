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
//	2 -> 3 : P1 WP1.2 — add the trust_file_evidence / trust_package_evidence
//	    detail-evidence tables (PRD §14.3): generation-bound per-file and
//	    per-package trust rows for target-scope assessments (trust_evidence.go).
//	3 -> 4 : ADR 0007/0008 (language-GA program) — trust_package_evidence gains
//	    a language column and it joins the PRIMARY KEY: with per-language
//	    semantic registrants, one directory can carry one row PER LANGUAGE,
//	    and the old (generation, package_key) key would silently clobber the
//	    sibling's row. SQLite cannot alter a primary key, so the step rebuilds
//	    the table, backfilling 'go' — exactly right, because every row a v3
//	    sidecar can hold came from the sole go registrant.
//	4 -> 5 : W0.g (legible abstention) — add trust_language_skips, the
//	    generation-bound record of each semantic registrant's NAMED skip
//	    counters (engine/jvmresolve's java_receiver_untyped &c). It is a table
//	    of its OWN rather than a column on trust_package_evidence because the
//	    counters are repository-global per language and carry no package
//	    attribution: keying them by package would manufacture an attribution
//	    the binder never made. See trust_evidence.go, LanguageSkips.
//	5 -> 6 : W0.g review round 1 — add trust_skip_provenance, the record of
//	    WHICH GENERATION the skip counters above actually describe and which
//	    registrants wrote them. Creating trust_language_skips is not the same
//	    event as recording one: the 4 -> 5 step migrates a store written by an
//	    older binary, and without this table every pre-existing generation
//	    would start reading "available, nothing skipped" the moment the table
//	    appeared — a migration turning a correct fail-closed answer into a
//	    false all-clear. Availability is a property of the GENERATION's
//	    provenance, never of the sidecar's schema.
const schemaVersion = 6

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
	if current < 3 {
		if err := i.migrateTrustEvidence(ctx); err != nil {
			return fmt.Errorf("ingest: migrate trust evidence tables: %w", err)
		}
	}
	if current < 4 {
		if err := i.migratePackageEvidenceLanguage(ctx); err != nil {
			return fmt.Errorf("ingest: migrate package evidence language: %w", err)
		}
	}
	if current < 5 {
		if err := i.migrateLanguageSkips(ctx); err != nil {
			return fmt.Errorf("ingest: migrate language skip table: %w", err)
		}
	}
	if current < 6 {
		if err := i.migrateSkipProvenance(ctx); err != nil {
			return fmt.Errorf("ingest: migrate skip provenance table: %w", err)
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
	have, err := columnSet(ctx, i.meta, "dirty_units")
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
	have, err := columnSet(ctx, i.meta, "file_content_cache")
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

// migrateTrustEvidence creates the P1 WP1.2 detail-evidence tables (PRD §14.3)
// when absent. These are whole NEW tables (never a column add), so CREATE TABLE
// IF NOT EXISTS is the correct idempotent form here — the base-DDL caveat above
// (silent no-op on an existing table) is exactly the desired behavior for a
// re-run. An old on-disk sidecar migrates additively: existing rows and tables
// are untouched. A sidecar that has NOT run this step (user_version < 3, e.g.
// opened read-only by an upgraded observer) reads as evidence-unavailable
// through the trust_evidence.go read ports — never as empty-healthy.
func (i *Ingester) migrateTrustEvidence(ctx context.Context) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS trust_file_evidence (
	generation_id TEXT NOT NULL,
	path TEXT NOT NULL,
	language TEXT NOT NULL,
	parse_status TEXT NOT NULL,
	parse_reason TEXT NOT NULL,
	resolved_derived INTEGER NOT NULL,
	resolved_heuristic INTEGER NOT NULL,
	resolved_external INTEGER NOT NULL,
	skipped INTEGER NOT NULL,
	ambiguous INTEGER NOT NULL,
	PRIMARY KEY (generation_id, path)
);
CREATE TABLE IF NOT EXISTS trust_package_evidence (
	generation_id TEXT NOT NULL,
	package_key TEXT NOT NULL,
	state TEXT NOT NULL,
	degraded_reason TEXT NOT NULL,
	type_errors INTEGER NOT NULL,
	dropped_intents INTEGER NOT NULL,
	confirmed_edges INTEGER NOT NULL,
	skipped_files INTEGER NOT NULL,
	PRIMARY KEY (generation_id, package_key)
);`
	if _, err := i.meta.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("ingest: create trust evidence tables: %w", err)
	}
	return nil
}

// querier is the read seam columnSet needs, satisfied by both *sql.DB and
// *sql.Tx — the latter is what lets a migration step evaluate its idempotency
// guard INSIDE the same transaction that performs the rewrite.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// columnSet returns the set of column names on a table via PRAGMA table_info.
// The table name is a trusted in-package literal, never caller-supplied.
func columnSet(ctx context.Context, q querier, table string) (map[string]struct{}, error) {
	rows, err := q.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
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

// migratePackageEvidenceLanguage rebuilds trust_package_evidence with the
// language column in its PRIMARY KEY (schema 3 -> 4). A column-presence guard
// keeps the step idempotent; the copy backfills 'go' because every row a v3
// sidecar can hold came from the sole go registrant — no information is
// invented and none is lost. SQLite cannot ALTER a primary key, hence the
// create-copy-drop-rename shape, executed as one script so a crash between
// statements is repaired by the next open re-running the guarded step.
func (i *Ingester) migratePackageEvidenceLanguage(ctx context.Context) error {
	// The migration MUST be atomic and idempotent. It was neither: a bare
	// multi-statement ExecContext is not run in one transaction, so a crash —
	// or a second process racing the same meta home (the ingest lock serializes
	// this in production, but tests share a state home) — left
	// trust_package_evidence_v4 half-built, and the next run failed at
	// `CREATE TABLE trust_package_evidence_v4` with "table already exists". Two
	// changes fix it: wrap the whole script in a transaction (a crash rolls back
	// to the pre-migration state, and the guard re-runs cleanly), and drop
	// any leftover _v4 first so a re-run after a rolled-back attempt starts from
	// a clean slate rather than colliding with its own debris.
	//
	// THE GUARD IS EVALUATED INSIDE THE SAME TRANSACTION AS THE REWRITE (ADR
	// 0009 review round 1, finding 2). It used to be read from i.meta before
	// BeginTx, which left a window: a second process could pass the stale guard
	// after the winner committed, re-run the copy against the already-migrated
	// table, and silently reset EVERY row's language to 'go' — destroying rows
	// the jvm registrant had written. With the guard on the tx there is no
	// window: the losing writer either sees the migrated table and no-ops, or
	// its write collides with the winner's lock and errs, and the next open
	// re-runs the guarded step cleanly. An error here is always retryable;
	// silent data loss was not.
	tx, err := i.meta.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ingest: begin trust_package_evidence migration: %w", err)
	}
	have, err := columnSet(ctx, tx, "trust_package_evidence")
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, ok := have["language"]; ok {
		_ = tx.Rollback() // already migrated; nothing was written
		return nil
	}
	const ddl = `
DROP TABLE IF EXISTS trust_package_evidence_v4;
CREATE TABLE trust_package_evidence_v4 (
	generation_id TEXT NOT NULL,
	language TEXT NOT NULL,
	package_key TEXT NOT NULL,
	state TEXT NOT NULL,
	degraded_reason TEXT NOT NULL,
	type_errors INTEGER NOT NULL,
	dropped_intents INTEGER NOT NULL,
	confirmed_edges INTEGER NOT NULL,
	skipped_files INTEGER NOT NULL,
	PRIMARY KEY (generation_id, language, package_key)
);
INSERT INTO trust_package_evidence_v4
	SELECT generation_id, 'go', package_key, state, degraded_reason,
	       type_errors, dropped_intents, confirmed_edges, skipped_files
	FROM trust_package_evidence;
DROP TABLE trust_package_evidence;
ALTER TABLE trust_package_evidence_v4 RENAME TO trust_package_evidence;`
	if _, err := tx.ExecContext(ctx, ddl); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("ingest: rebuild trust_package_evidence with language: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ingest: commit trust_package_evidence migration: %w", err)
	}
	return nil
}

// migrateLanguageSkips creates trust_language_skips (schema 4 -> 5): one row
// per (generation, language, skip name) carrying the count of sites that
// registrant refused to bind under that NAMED reason.
//
// THE GUARD IS EVALUATED INSIDE THE SAME TRANSACTION AS THE CREATE, and the
// whole step runs in that transaction. This is the migration-race shape fixed
// in W0.b and re-fixed for the 3 -> 4 step above (ADR 0009 review round 1,
// finding 2): a guard read outside the transaction leaves a window in which a
// second process passes a stale guard after the winner committed and re-runs
// the body against already-migrated state. CREATE TABLE IF NOT EXISTS would
// paper over that here, but writing the step in the safe shape is the point —
// the next step to be added by copying this one inherits the discipline
// instead of the hazard, and a crash mid-step rolls back rather than leaving
// debris. A losing writer either sees the table and no-ops, or collides with
// the winner's lock and errs retryably; the next open re-runs the guarded
// step cleanly.
func (i *Ingester) migrateLanguageSkips(ctx context.Context) error {
	tx, err := i.meta.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ingest: begin trust_language_skips migration: %w", err)
	}
	var present int
	if err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'trust_language_skips'").
		Scan(&present); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("ingest: probe trust_language_skips: %w", err)
	}
	if present > 0 {
		_ = tx.Rollback() // already migrated; nothing was written
		return nil
	}
	const ddl = `
CREATE TABLE trust_language_skips (
	generation_id TEXT NOT NULL,
	language TEXT NOT NULL,
	skip_name TEXT NOT NULL,
	count INTEGER NOT NULL,
	PRIMARY KEY (generation_id, language, skip_name)
);`
	if _, err := tx.ExecContext(ctx, ddl); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("ingest: create trust_language_skips: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ingest: commit trust_language_skips migration: %w", err)
	}
	return nil
}

// migrateSkipProvenance creates trust_skip_provenance (schema 5 -> 6): the
// record of WHICH GENERATIONS carry an abstention record at all, and which
// semantic registrants wrote each one.
//
// WHY A MIGRATION CANNOT SUPPLY THIS FACT, WHICH IS THE WHOLE POINT. Creating
// trust_language_skips (the 4 -> 5 step) makes the table READABLE; it does not
// make the generations already in the store RECORDED. Gating availability on
// the schema version therefore flipped every pre-existing generation from a
// correct "this sidecar cannot tell me" to "available, nothing was skipped" the
// instant a `graphi sync` migrated the store — and sync does not re-record
// (it no-ops when the index is current), so the store parked in a false
// all-clear until an unrelated rebuild. This table is deliberately EMPTY after
// the migration: no row exists for a generation written before the record
// existed, so every such generation keeps failing closed, and only a pass that
// actually writes evidence mints its own row.
//
// The step carries the same shape as its two predecessors — guard evaluated
// INSIDE the transaction that performs the create, rollback on every error —
// so a step added by copying this one inherits the discipline rather than the
// migration race W0.b fixed.
func (i *Ingester) migrateSkipProvenance(ctx context.Context) error {
	tx, err := i.meta.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ingest: begin trust_skip_provenance migration: %w", err)
	}
	var present int
	if err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'trust_skip_provenance'").
		Scan(&present); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("ingest: probe trust_skip_provenance: %w", err)
	}
	if present > 0 {
		_ = tx.Rollback() // already migrated; nothing was written
		return nil
	}
	// language '' is the GENERATION SENTINEL: "a provenance-aware pass recorded
	// this generation". Registrant rows carry the language. The two are one
	// table because they answer one question — what does the abstention record
	// for this generation actually cover — and separating them would allow a
	// state where a generation is recorded by nobody yet claims registrants.
	const ddl = `
CREATE TABLE trust_skip_provenance (
	generation_id TEXT NOT NULL,
	language TEXT NOT NULL,
	PRIMARY KEY (generation_id, language)
);`
	if _, err := tx.ExecContext(ctx, ddl); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("ingest: create trust_skip_provenance: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ingest: commit trust_skip_provenance migration: %w", err)
	}
	return nil
}
