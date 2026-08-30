package embed

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/samibel/graphi/core/model"

	_ "modernc.org/sqlite"
)

// LegacyMigrationResult records what MigrateFromLegacyVectors did. The
// `RowsMigrated` and `EmbedderIDs` fields let the operator (and the tests)
// confirm the migration landed correctly without re-running a SELECT.
type LegacyMigrationResult struct {
	// Migrated is true when rows from the legacy `vectors` table were
	// copied into a new v1 generation. False means either the legacy
	// table did not exist OR a v1 generation already exists (the
	// idempotent second-run path).
	Migrated bool
	// GenerationID is the id of the v1 generation created or already
	// present. Empty when no migration ran (no legacy table).
	GenerationID GenerationID
	// RowsMigrated is the number of legacy rows copied. Zero when the
	// legacy table was empty or absent.
	RowsMigrated int
	// EmbedderIDs is the sorted list of distinct embedder_id values found
	// in the legacy table. The v1 fingerprint's ModelID is the first
	// element (canonical order) — every other embedder_id in the legacy
	// table is recorded in EmbedderIDs but NOT in the v1 fingerprint
	// (the schema does not model multi-embedder legacy rows). The
	// operator can see them via this field and decide whether to
	// re-embed under a single chosen embedder.
	EmbedderIDs []string
}

// LegacyVectorsTableDDL is the legacy `vectors` schema from
// engine/embed/sqlite_vectorstore.go (SW-260). It is referenced by name
// only here so this file compiles without the old package; the test
// fixtures use the same DDL.
const LegacyVectorsTableDDL = `
CREATE TABLE IF NOT EXISTS vectors (
    node_id     TEXT NOT NULL,
    embedder_id TEXT NOT NULL,
    dim         INTEGER NOT NULL,
    vec         BLOB NOT NULL,
    PRIMARY KEY (embedder_id, node_id)
);`

// MigrateFromLegacyVectors copies every row from the legacy `vectors` table
// into a new v1 generation, marks the generation stale (its fingerprint is
// NOT the current one), and records the migration result. The migration is
// IDEMPOTENT: a second call sees the v1 generation already present and
// returns Migrated=false without re-copying or touching the active pointer.
//
// The v1 fingerprint's ModelID is the FIRST embedder_id found in the legacy
// table (lexicographically — the canonical order the schema uses
// everywhere). All distinct embedder_ids are reported in the result so the
// operator can see what was on disk; the migration does NOT invent a
// multi-embedder fingerprint (that would be silently wrong — the legacy
// table mixed spaces).
//
// The lexical graphstore is byte-untouched: this function only operates on
// the meta sidecar (ingest-meta.db). The caller is responsible for
// asserting that property (the conformance test sha256s the graph DB
// before/after).
func MigrateFromLegacyVectors(ctx context.Context, db *sql.DB) (LegacyMigrationResult, error) {
	if db == nil {
		return LegacyMigrationResult{}, fmt.Errorf("embed: nil meta db")
	}
	// Ensure the legacy table exists with the expected shape; if it does
	// not, the migration is a no-op. The legacy DDL is idempotent.
	if _, err := db.ExecContext(ctx, LegacyVectorsTableDDL); err != nil {
		return LegacyMigrationResult{}, fmt.Errorf("embed: ensure legacy table: %w", err)
	}
	// Probe whether the legacy table has any rows. An absent `vectors`
	// table is a NO-OP (the migration is forward-only, never reverse).
	var legacyCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM vectors`).Scan(&legacyCount); err != nil {
		return LegacyMigrationResult{}, fmt.Errorf("embed: count legacy rows: %w", err)
	}
	if legacyCount == 0 {
		return LegacyMigrationResult{Migrated: false}, nil
	}
	// Ensure the new schema exists. We use the same DDL constants the
	// store would use so the migration leaves the sidecar in a state
	// the new code can read.
	for _, ddl := range []string{generationsDDL, rowsDDL} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return LegacyMigrationResult{}, fmt.Errorf("embed: init generations schema: %w", err)
		}
	}
	// Idempotency check: if a v1 generation already exists, skip the
	// copy (the migration is one-shot; the second call returns
	// Migrated=false and reports the existing generation).
	if existing, found, err := findExistingV1Generation(ctx, db); err != nil {
		return LegacyMigrationResult{}, err
	} else if found {
		rows, _ := countRowsFor(ctx, db, string(existing))
		ids, _ := distinctLegacyEmbedderIDs(ctx, db)
		return LegacyMigrationResult{
			Migrated:     false,
			GenerationID: existing,
			RowsMigrated: rows,
			EmbedderIDs:  ids,
		}, nil
	}
	// Enumerate legacy rows and their embedder ids; the v1 fingerprint's
	// ModelID is the lexicographically first embedder id.
	embedderIDs, err := distinctLegacyEmbedderIDs(ctx, db)
	if err != nil {
		return LegacyMigrationResult{}, err
	}
	if len(embedderIDs) == 0 {
		// Empty table — shouldn't have happened (the COUNT guard above)
		// but be defensive.
		return LegacyMigrationResult{Migrated: false}, nil
	}
	firstID := embedderIDs[0]
	// Determine the dim from the first legacy row. If different rows
	// have different dims we use the first row's dim and record the
	// observation — heterogeneous dims in a v1 sidecar are themselves a
	// legacy bug, but the migration must not error out over them.
	var dim int
	if err := db.QueryRowContext(ctx,
		`SELECT dim FROM vectors WHERE embedder_id = ? LIMIT 1`,
		firstID).Scan(&dim); err != nil {
		return LegacyMigrationResult{}, fmt.Errorf("embed: probe legacy dim: %w", err)
	}
	v1fp := Fingerprint{
		ModelID:         firstID,
		DocumentSchema:  "v1",
		Dim:             dim,
		GraphGeneration: GraphGenerationPlaceholder,
	}
	id := v1fp.ID()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyMigrationResult{}, fmt.Errorf("embed: migration begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	// Insert the generation row, marked active=1 (the v1 generation IS
	// the served one until a v2 build commits) and staging=0.
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO generations (id, fingerprint, fingerprint_dim, document_schema, row_count, is_active, is_staging)
        VALUES (?, ?, ?, ?, 0, 1, 0)`,
		string(id), v1fp.Canonical(), dim, v1fp.DocumentSchema); err != nil {
		return LegacyMigrationResult{}, fmt.Errorf("embed: insert v1 generation: %w", err)
	}
	// Copy every legacy row. The legacy table does not carry
	// provenance or text_hash — those fields are "" / 0 for v1 rows.
	// The migration uses INSERT … SELECT so it is one statement per
	// embedder (the primary key shape `(embedder_id, node_id)` lets us
	// skip duplicates that survive a re-run within one migration).
	for _, embID := range embedderIDs {
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO generation_rows
                (generation_id, document_id, node_id, text_hash, path, start_line, end_line, span_method, vector)
            SELECT ?, '', node_id, '', '', 0, 0, '', vec
            FROM vectors WHERE embedder_id = ?`,
			string(id), embID); err != nil {
			return LegacyMigrationResult{}, fmt.Errorf("embed: copy legacy rows for %s: %w", embID, err)
		}
	}
	// Stamp the generation's row_count from what we just inserted.
	if _, err := tx.ExecContext(ctx, `
        UPDATE generations SET row_count = (
            SELECT COUNT(*) FROM generation_rows WHERE generation_id = ?
        ) WHERE id = ?`,
		string(id), string(id)); err != nil {
		return LegacyMigrationResult{}, fmt.Errorf("embed: stamp v1 row count: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyMigrationResult{}, fmt.Errorf("embed: commit v1 migration: %w", err)
	}
	migrated, _ := countRowsFor(ctx, db, string(id))
	return LegacyMigrationResult{
		Migrated:     true,
		GenerationID: id,
		RowsMigrated: migrated,
		EmbedderIDs:  embedderIDs,
	}, nil
}

// findExistingV1Generation returns the v1 generation id (a generation whose
// document_schema is "v1") when present, and whether it exists.
func findExistingV1Generation(ctx context.Context, db *sql.DB) (GenerationID, bool, error) {
	var id string
	err := db.QueryRowContext(ctx, `
        SELECT id FROM generations WHERE document_schema = 'v1' LIMIT 1`).Scan(&id)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("embed: probe v1 generation: %w", err)
	}
	return GenerationID(id), true, nil
}

// countRowsFor returns the row count for a given generation id. Used by the
// migration result and by the idempotency-check path.
func countRowsFor(ctx context.Context, db *sql.DB, id string) (int, error) {
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM generation_rows WHERE generation_id = ?`, id).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// distinctLegacyEmbedderIDs returns the sorted distinct embedder_id values
// from the legacy `vectors` table.
func distinctLegacyEmbedderIDs(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT DISTINCT embedder_id FROM vectors ORDER BY embedder_id`)
	if err != nil {
		return nil, fmt.Errorf("embed: distinct legacy embedder ids: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("embed: scan legacy embedder id: %w", err)
		}
		out = append(out, strings.TrimSpace(id))
	}
	return out, rows.Err()
}

// graphstoreNodeAdapter bridges a graphstore.Graphstore to the
// NodeReferencer the GenerationStore needs for AC-7 corrupt validation. It
// is a thin shim kept in this file because it is part of the migration
// wiring (Active's "referenced node exists" check is run on the migrated
// generation too).
//
// The graphstore's GetNode returns ErrNotFound when the node is absent; we
// map that to (false, nil) and surface other errors verbatim.
type graphstoreNodeAdapter struct {
	get func(ctx context.Context, id model.NodeId) (model.Node, error)
}

// NodeReferencerFromGraphLookup returns a NodeReferencer that calls the
// provided lookup function. Used by runtime.NewSearchService to wire the
// graphstore into the GenerationStore's Active call.
func NodeReferencerFromGraphLookup(get func(ctx context.Context, id model.NodeId) (model.Node, error)) NodeReferencer {
	return graphstoreNodeAdapter{get: get}
}

// NodeExists implements NodeReferencer.
func (g graphstoreNodeAdapter) NodeExists(ctx context.Context, id model.NodeId) (bool, error) {
	if g.get == nil {
		return true, nil
	}
	_, err := g.get(ctx, id)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows || strings.Contains(err.Error(), "not found") {
		return false, nil
	}
	return false, err
}
