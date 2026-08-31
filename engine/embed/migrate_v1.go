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
// `Generations` field lets the operator (and the tests) confirm the
// migration landed correctly without re-running a SELECT.
type LegacyMigrationResult struct {
	// Migrated is true when rows from the legacy `vectors` table were
	// copied into one or more new v1 generations. False means either
	// the legacy table did not exist OR a v1 generation already exists
	// (the idempotent second-run path).
	Migrated bool
	// Generations is the list of v1 generations created (or already
	// present on a re-run). One per distinct legacy embedder_id
	// (SW-261 review round 2 MAJOR 4). The legacy schema permits the
	// same node_id under several embedder_ids; mapping every embedder
	// into ONE generation hit a uniqueness violation on
	// (generation_id, node_id). One-generation-per-embedder is the
	// honest reading: each embedder_id is its own embedding space, and
	// the migration should not collapse them.
	Generations []MigratedGeneration
	// RowsMigrated is the total number of legacy rows copied across
	// every generated v1 generation. Zero when the legacy table was
	// empty or absent.
	RowsMigrated int
	// EmbedderIDs is the sorted list of distinct embedder_id values
	// found in the legacy table. Mirrors the v1 fingerprint's ModelID
	// for every generation the migration produced.
	EmbedderIDs []string
}

// MigratedGeneration is one legacy embedder's migration result.
type MigratedGeneration struct {
	// GenerationID is the id of the v1 generation created (or already
	// present on a re-run) for this embedder.
	GenerationID GenerationID
	// EmbedderID is the legacy embedder_id whose rows this generation
	// holds.
	EmbedderID string
	// RowsMigrated is the number of legacy rows copied into this
	// generation. Zero when the embedder had no rows.
	RowsMigrated int
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

// MigrateFromLegacyVectors copies every row from the legacy `vectors`
// table into one v1 generation per distinct embedder_id, marks every
// generation stale (its fingerprint is NOT the current one), and
// records the migration result. The migration is IDEMPOTENT: a second
// call sees the v1 generations already present and returns Migrated=
// false without re-copying or touching the active pointer.
//
// SW-261 review round 2 (MAJOR 4): the legacy schema permits the same
// node_id under several embedder_ids, and the destination key is
// (generation_id, node_id). The pre-fix shape copied every embedder
// into ONE generation, so the second copy of a shared node hit a
// uniqueness error and the production opener failed. The honest
// reading is one generation per legacy embedder — each embedder is
// its own embedding space, and the migration must not collapse them.
// One-generation-per-embedder also matches the v1 schema's
// fingerprint semantics: ModelID is the embedder identifier, so a
// separate generation per embedder gives each its own fingerprint
// and its own "stale vs ready" answer.
//
// The lexical graphstore is byte-untouched: this function only operates
// on the meta sidecar (ingest-meta.db). The caller is responsible for
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
	// Enumerate legacy rows and their embedder ids; the migration
	// produces one v1 generation per distinct embedder id (MAJOR 4).
	embedderIDs, err := distinctLegacyEmbedderIDs(ctx, db)
	if err != nil {
		return LegacyMigrationResult{}, err
	}
	if len(embedderIDs) == 0 {
		// Empty table — shouldn't have happened (the COUNT guard above)
		// but be defensive.
		return LegacyMigrationResult{Migrated: false}, nil
	}
	// Idempotency check: if a v1 generation already exists for every
	// embedder id, the migration is a no-op (the second call returns
	// Migrated=false and reports the existing generations).
	existingIDs, err := findExistingV1Generations(ctx, db)
	if err != nil {
		return LegacyMigrationResult{}, err
	}
	if len(existingIDs) >= len(embedderIDs) {
		var (
			migs  []MigratedGeneration
			total int
		)
		for _, embID := range embedderIDs {
			fp := legacyFingerprint(ctx, db, embID)
			id := fp.ID()
			rows, _ := countRowsFor(ctx, db, string(id))
			migs = append(migs, MigratedGeneration{GenerationID: id, EmbedderID: embID, RowsMigrated: rows})
			total += rows
		}
		return LegacyMigrationResult{
			Migrated:     false,
			Generations:  migs,
			RowsMigrated: total,
			EmbedderIDs:  embedderIDs,
		}, nil
	}
	// Fresh migration: one generation per embedder.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyMigrationResult{}, fmt.Errorf("embed: migration begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var (
		migs  []MigratedGeneration
		total int
	)
	for _, embID := range embedderIDs {
		fp := legacyFingerprint(ctx, db, embID)
		id := fp.ID()
		// Insert the generation row, marked active=0 (only the
		// lexicographically-first embedder's generation is marked
		// active=1, the others are inactive pending operator decision).
		// The schema allows at most ONE active row across the whole
		// sidecar, so exactly one migrated generation is marked active
		// — the first embedder in canonical order — and the rest are
		// written inactive. The
		// runtime's loadSemanticState will read the active generation
		// and answer StateStale (fingerprint mismatch), regardless of
		// which embedder built it. Inactive generations stay on disk
		// for inspection; a fresh --semantic build under a chosen
		// embedder will demote them.
		active := 0
		if embID == embedderIDs[0] {
			active = 1
		}
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO generations (id, fingerprint, fingerprint_dim, document_schema, row_count, is_active, is_staging)
            VALUES (?, ?, ?, ?, 0, ?, 0)`,
			string(id), fp.Canonical(), fp.Dim, fp.DocumentSchema, active); err != nil {
			return LegacyMigrationResult{}, fmt.Errorf("embed: insert v1 generation for %s: %w", embID, err)
		}
		// Copy every legacy row for this embedder. The legacy table
		// does not carry provenance or text_hash — those fields are
		// "" / 0 for v1 rows. The (generation_id, node_id) PK lets
		// one embedder's rows land without colliding on a node id
		// shared with another embedder — that was the pre-fix bug.
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO generation_rows
                (generation_id, document_id, node_id, text_hash, path, start_line, end_line, span_method, vector)
            SELECT ?, '', node_id, '', '', 0, 0, '', vec
            FROM vectors WHERE embedder_id = ?`,
			string(id), embID); err != nil {
			return LegacyMigrationResult{}, fmt.Errorf("embed: copy legacy rows for %s: %w", embID, err)
		}
		// Stamp the row_count from what we just inserted.
		var n int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM generation_rows WHERE generation_id = ?`,
			string(id)).Scan(&n); err != nil {
			return LegacyMigrationResult{}, fmt.Errorf("embed: stamp v1 row count: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE generations SET row_count = ? WHERE id = ?`,
			n, string(id)); err != nil {
			return LegacyMigrationResult{}, fmt.Errorf("embed: stamp v1 row count: %w", err)
		}
		migs = append(migs, MigratedGeneration{GenerationID: id, EmbedderID: embID, RowsMigrated: n})
		total += n
	}
	if err := tx.Commit(); err != nil {
		return LegacyMigrationResult{}, fmt.Errorf("embed: commit v1 migration: %w", err)
	}
	return LegacyMigrationResult{
		Migrated:     true,
		Generations:  migs,
		RowsMigrated: total,
		EmbedderIDs:  embedderIDs,
	}, nil
}

// legacyFingerprint computes the v1 Fingerprint for one legacy embedder.
// It probes the dim from the first row of the embedder's partition —
// the legacy schema permits heterogeneous dims (a legacy bug); the
// migration uses the first row's dim and proceeds. GraphGeneration
// stays at the documented placeholder.
func legacyFingerprint(ctx context.Context, db *sql.DB, embID string) Fingerprint {
	var dim int
	if err := db.QueryRowContext(ctx,
		`SELECT dim FROM vectors WHERE embedder_id = ? LIMIT 1`,
		embID).Scan(&dim); err != nil {
		// Best-effort: an unknown embedder (no rows) gets dim 0. The
		// fingerprint then has an empty canonical for that embedder's
		// generation; the migrate path only calls this for embedders
		// that DO have rows, so this branch is defensive.
		dim = 0
	}
	return Fingerprint{
		ModelID:         embID,
		DocumentSchema:  "v1",
		Dim:             dim,
		GraphGeneration: GraphGenerationPlaceholder,
	}
}

// findExistingV1Generations returns the v1 generation ids (one per
// legacy embedder, per MAJOR 4). The list is sorted by id for a
// deterministic comparison. The migration is considered complete when
// the count of existing generations matches (or exceeds) the count of
// distinct legacy embedders.
func findExistingV1Generations(ctx context.Context, db *sql.DB) ([]GenerationID, error) {
	rows, err := db.QueryContext(ctx, `
        SELECT id FROM generations WHERE document_schema = 'v1' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("embed: probe v1 generations: %w", err)
	}
	defer rows.Close()
	var out []GenerationID
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("embed: scan v1 generation id: %w", err)
		}
		out = append(out, GenerationID(id))
	}
	return out, rows.Err()
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
