package embed

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	"github.com/samibel/graphi/core/model"

	_ "modernc.org/sqlite" // CGo-free SQLite driver for the durable sidecar
)

// SQLiteGenerationStore is the durable, SQLite-backed GenerationStore. It
// replaces the per-embedder `vectors` table from SW-260 with two durable
// tables — `generations` (one row per generation, carrying the fingerprint
// and bookkeeping) and `generation_rows` (one row per (generation, node) —
// vector + provenance + text_hash). The active pointer is a single `is_active`
// bit on the generation row; atomic publish is a single transaction that
// demotes the prior active row and promotes the staging one.
//
// Layout: this code lives in the ingest meta sidecar (`ingest-meta.db`)
// beside `file_content_cache` / `reverse_deps` / `dirty_units` /
// `edit_provenance`, and it is created lazily with the same
// `CREATE TABLE IF NOT EXISTS` discipline as the rest of the sidecar. Only
// the CGo-free modernc.org/sqlite driver is used; nothing here makes a
// network call; nothing here is touched on the default build path
// (the store is only opened when an embedder is configured — see
// runtime.NewSearchService and `graphi index --semantic`).
//
// Generation identity: each Begin mints a unique opaque id (a 16-hex
// nonce) — `id` is NOT the fingerprint hash. The fingerprint decides
// WHICH generation is "the" active one (Active compares canonicals);
// the id is the durable handle for Load and the unique key for the rows
// table. This means a re-build under the same fingerprint gets a
// different id, so its rows do not collide with the prior's rows, and
// Abort cleanly removes just the staging build's rows.
type SQLiteGenerationStore struct {
	db             *sql.DB
	ownDB          bool
	schemaPresent  bool
	hasCommittedAt bool

	// buildMu serialises the Begin → Commit/Abort lifecycle within THIS
	// process (AC-6). Without it, two goroutines can race through their
	// own BeginTx/Commit cycles, each seeing no in-flight staging row,
	// and both succeed. With it, the second Begin blocks until the first
	// Build reaches Commit or Abort; on resume it sees the (still live)
	// staging row and returns ErrBuildInProgress.
	buildMu sync.Mutex

	// liveBuilds tracks the in-flight Build IDs in this process. It is
	// the per-process "this staging row is one of ours" marker the AC-5
	// stale-staging detection needs: a fresh process sees a staging row
	// whose id is NOT in its liveBuilds map and treats it as a leftover
	// from a crashed prior process, discarding it. A long-running
	// process that already has a Begin open sees the same row's id IN
	// liveBuilds and treats it as concurrent (ErrBuildInProgress). The
	// map is process-local by design — cross-process safety relies on
	// the graphi-wide `internal/ingestlock` per-repo cross-process lock
	// documented in the architecture (`cmd/internal/runtime`). A future
	// ticket may add a process-cluster lock here; it is not in scope
	// for SW-261.
	liveBuildsMu sync.Mutex
	liveBuilds   map[GenerationID]struct{}
}

// generationsDDL is the idempotent schema for the generations table. The
// `is_active` / `is_staging` bits are mutually exclusive: at most one row
// in the table has is_active = 1 (the served generation for its
// fingerprint) and at most one has is_staging = 1 (an in-flight build that
// has not yet committed). `fingerprint` is the canonical string; the
// partial unique indexes enforce the invariants.
const generationsDDL = `
CREATE TABLE IF NOT EXISTS generations (
    id              TEXT PRIMARY KEY,
    fingerprint     TEXT NOT NULL,
    fingerprint_dim INTEGER NOT NULL,
    document_schema TEXT NOT NULL,
    row_count       INTEGER NOT NULL,
    is_active       INTEGER NOT NULL DEFAULT 0,
    is_staging      INTEGER NOT NULL DEFAULT 0
);`

// SW-265: additive committed_at column for the status surface's built_at
// field. The column is nullable so a pre-existing sidecar loads cleanly;
// the active-generation query coalesces NULL into "" so the wire shape
// stays empty-string-stable. Applied as a separate statement because the
// primary DDL is wrapped in a CREATE TABLE IF NOT EXISTS, which would
// silently ignore a new column on a fresh install. ALTER TABLE ADD COLUMN
// is idempotent only at the SQL level when guarded by a name probe.
const generationCommittedAtColumnDDL = `
ALTER TABLE generations ADD COLUMN committed_at TEXT NOT NULL DEFAULT '';`

// rowsDDL is the idempotent schema for the rows table. The PK is
// (generation_id, node_id) so a single generation holds one row per node.
// The vector BLOB is big-endian float32 components (mirroring the old
// `vectors` table's fixed-endianness discipline so persisted rows are
// portable across architectures).
const rowsDDL = `
CREATE TABLE IF NOT EXISTS generation_rows (
    generation_id TEXT NOT NULL,
    document_id   TEXT NOT NULL,
    node_id       TEXT NOT NULL,
    text_hash     TEXT NOT NULL,
    path          TEXT NOT NULL,
    start_line    INTEGER NOT NULL,
    end_line      INTEGER NOT NULL,
    span_method   TEXT NOT NULL,
    vector        BLOB NOT NULL,
    PRIMARY KEY (generation_id, node_id)
);`

// activeIndexDDL makes the `at most one active` invariant cheap to maintain.
// The pointer is GLOBAL: at most one generation across the entire sidecar
// is active at any time. Active finds it by is_active=1, then compares
// fingerprint to determine ready vs stale (AC-7).
const activeIndexDDL = `
CREATE UNIQUE INDEX IF NOT EXISTS generations_one_active
    ON generations (is_active) WHERE is_active = 1;`

// stagingIndexDDL is the analog for staging.
const stagingIndexDDL = `
CREATE UNIQUE INDEX IF NOT EXISTS generations_one_staging
    ON generations (is_staging) WHERE is_staging = 1;`

// NewSQLiteGenerationStoreDB constructs the durable store over an EXISTING
// meta DB handle (e.g. the ingester's MetaDB()). The caller owns the
// handle's lifecycle; this store's Close is a no-op.
//
// AC-8: opening a store whose sidecar contains the legacy `vectors`
// table migrates it idempotently into a v1 generation marked stale. The
// migration runs as part of open (after the new schema is in place) so
// a first `graphi index --semantic` on a migrated store re-embeds
// rather than mixing spaces. Running the migration twice is a no-op;
// the legacy DDL is idempotent.
func NewSQLiteGenerationStoreDB(ctx context.Context, db *sql.DB) (*SQLiteGenerationStore, error) {
	if db == nil {
		return nil, fmt.Errorf("embed: nil meta db")
	}
	for _, ddl := range []string{generationsDDL, rowsDDL, activeIndexDDL, stagingIndexDDL} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return nil, fmt.Errorf("embed: init generations schema: %w", err)
		}
	}
	// SW-265: idempotent column addition for committed_at. ALTER TABLE ADD
	// COLUMN is not idempotent on its own; the probe short-circuits the
	// second open. SQLite stores the column in sqlite_schema (no
	// information_schema), so the probe is a single-row query.
	if err := ensureCommittedAtColumn(ctx, db); err != nil {
		return nil, fmt.Errorf("embed: ensure committed_at column: %w", err)
	}
	if _, err := MigrateFromLegacyVectors(ctx, db); err != nil {
		return nil, fmt.Errorf("embed: legacy migration: %w", err)
	}
	return &SQLiteGenerationStore{
		db:             db,
		ownDB:          false,
		schemaPresent:  true,
		hasCommittedAt: true,
		liveBuilds:     map[GenerationID]struct{}{},
	}, nil
}

// OpenSQLiteGenerationStore opens the durable store from a meta DIRECTORY
// (the ingest-meta sidecar dir), opening its OWN read handle on the same
// database. Close releases the handle. Used by tests and by callers that
// do not already hold a meta DB handle.
//
// AC-8: as NewSQLiteGenerationStoreDB, the legacy migration runs as
// part of open so a first `graphi index --semantic` on a store that
// contains the legacy `vectors` table re-embeds rather than mixing
// spaces. The migration is idempotent; running it twice is a no-op.
func OpenSQLiteGenerationStore(ctx context.Context, metaDir string) (*SQLiteGenerationStore, error) {
	if metaDir == "" {
		return nil, fmt.Errorf("embed: empty meta dir")
	}
	dbPath := filepath.Join(metaDir, "ingest-meta.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("embed: open meta db: %w", err)
	}
	for _, ddl := range []string{generationsDDL, rowsDDL, activeIndexDDL, stagingIndexDDL} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("embed: init generations schema: %w", err)
		}
	}
	// SW-265: idempotent column addition (see NewSQLiteGenerationStoreDB).
	if err := ensureCommittedAtColumn(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("embed: ensure committed_at column: %w", err)
	}
	if _, err := MigrateFromLegacyVectors(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("embed: legacy migration: %w", err)
	}
	return &SQLiteGenerationStore{
		db:             db,
		ownDB:          true,
		schemaPresent:  true,
		hasCommittedAt: true,
		liveBuilds:     map[GenerationID]struct{}{},
	}, nil
}

// OpenSQLiteGenerationStoreReadOnly opens an existing ingest-meta sidecar
// without creating a file, table, column, WAL, or migration. Status is an
// observer, so it must never turn "not indexed" into a write.
func OpenSQLiteGenerationStoreReadOnly(ctx context.Context, metaDir string) (*SQLiteGenerationStore, error) {
	if metaDir == "" {
		return nil, fmt.Errorf("embed: empty meta dir")
	}
	dbPath := filepath.Join(metaDir, "ingest-meta.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("embed: open meta db read-only: %w", err)
	}
	store := &SQLiteGenerationStore{db: db, ownDB: true, liveBuilds: map[GenerationID]struct{}{}}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name='generations'`).Scan(&n); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("embed: probe generations schema: %w", err)
	}
	store.schemaPresent = n > 0
	if !store.schemaPresent {
		return store, nil
	}
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(generations)`)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("embed: inspect generations schema: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("embed: scan generations schema: %w", err)
		}
		if name == "committed_at" {
			store.hasCommittedAt = true
		}
	}
	return store, rows.Err()
}

// newGenerationID mints a 16-hex-char opaque id for a generation. The
// id is unique across the sidecar (the table's PRIMARY KEY enforces
// uniqueness on insert) and does NOT carry fingerprint information —
// `Active` uses fingerprint canonical equality to identify the served
// generation.
func newGenerationID() (GenerationID, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("embed: mint generation id: %w", err)
	}
	return GenerationID("g-" + hex.EncodeToString(buf[:])), nil
}

// ensureCommittedAtColumn is the SW-265 idempotent schema migration that
// adds the `committed_at` column to the generations table. SQLite stores
// the column in sqlite_schema; the probe looks it up by name and runs the
// ALTER only when absent, so an existing sidecar is upgraded exactly
// once. The probe uses a parameterised SELECT against sqlite_schema,
// which keeps the path consistent with the migration helper's shape.
//
// A pre-existing sidecar that lacks the column reads as committed_at = ”
// after the migration, which is the documented empty-string rendering
// (`active_generation.built_at`); the wire shape stays stable.
func ensureCommittedAtColumn(ctx context.Context, db *sql.DB) error {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_schema WHERE type = 'table' AND name = 'generations' AND sql LIKE '%committed_at%'`).Scan(&n)
	if err != nil {
		return fmt.Errorf("embed: probe committed_at: %w", err)
	}
	if n > 0 {
		return nil
	}
	if _, err := db.ExecContext(ctx, generationCommittedAtColumnDDL); err != nil {
		return fmt.Errorf("embed: add committed_at column: %w", err)
	}
	return nil
}

// Begin implements GenerationStore. AC-5 / AC-6 / the AC-1 typed-error
// contract are enforced here:
//
//   - buildMu blocks the second goroutine until the first Build reaches
//     Commit or Abort.
//   - A staging row whose id is NOT in this process's liveBuilds map is
//     treated as a leftover from a dead prior process (AC-5) and is
//     discarded before the new staging row is inserted.
//   - A staging row whose id IS in liveBuilds is the first goroutine's
//     build (concurrent in this process) → ErrBuildInProgress.
//   - The active pointer is NEVER moved by Begin; only Commit promotes.
//
// Cross-process safety: graphi's `internal/ingestlock` (per-repo
// cross-process lock, `cmd/internal/runtime`) guarantees that only one
// graphi process indexes one repo at a time, so two live processes
// cannot both Begin on the same store. This is documented in the report
// so a future reader does not mistake the per-process serialisation for
// a cluster-wide one.
func (s *SQLiteGenerationStore) Begin(ctx context.Context, fp Fingerprint) (Build, error) {
	s.buildMu.Lock()
	defer s.buildMu.Unlock()

	id, err := newGenerationID()
	if err != nil {
		return nil, err
	}

	// Probe for an existing staging row.
	var existingID string
	err = s.db.QueryRowContext(ctx,
		`SELECT id FROM generations WHERE is_staging = 1 LIMIT 1`).Scan(&existingID)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("embed: probe staging: %w", err)
	}
	if existingID != "" {
		s.liveBuildsMu.Lock()
		_, ours := s.liveBuilds[GenerationID(existingID)]
		s.liveBuildsMu.Unlock()
		if ours {
			return nil, ErrBuildInProgress
		}
		// Stale staging from a dead prior process. Discard it.
		if _, err := s.db.ExecContext(ctx,
			`DELETE FROM generations WHERE id = ? AND is_staging = 1`,
			existingID); err != nil {
			return nil, fmt.Errorf("embed: discard stale staging: %w", err)
		}
		if _, err := s.db.ExecContext(ctx,
			`DELETE FROM generation_rows WHERE generation_id = ?`,
			existingID); err != nil {
			return nil, fmt.Errorf("embed: discard stale staging rows: %w", err)
		}
	}

	// Insert the new staging row.
	if _, err := s.db.ExecContext(ctx, `
        INSERT INTO generations (id, fingerprint, fingerprint_dim, document_schema, row_count, is_active, is_staging)
        VALUES (?, ?, ?, ?, 0, 0, 1)`,
		string(id), fp.Canonical(), fp.Dim, fp.DocumentSchema); err != nil {
		return nil, fmt.Errorf("embed: insert staging generation: %w", err)
	}
	s.liveBuildsMu.Lock()
	s.liveBuilds[id] = struct{}{}
	s.liveBuildsMu.Unlock()

	return &sqliteBuild{store: s, id: id, fp: fp}, nil
}

// sqliteBuild is the SQLite Build. Upsert/Commit/Abort are independent
// transactions against the staging generation; the active pointer move
// on Commit is one transaction (atomic publish).
type sqliteBuild struct {
	store *SQLiteGenerationStore
	id    GenerationID
	fp    Fingerprint
}

func (b *sqliteBuild) ID() GenerationID { return b.id }

func (b *sqliteBuild) Upsert(ctx context.Context, r Row) error {
	if r.GenerationID != "" && r.GenerationID != b.id {
		return &ValidationFailedError{Reason: "row belongs to a different generation: " + string(r.GenerationID)}
	}
	if r.NodeID == "" {
		return &ValidationFailedError{Reason: "row has empty NodeID"}
	}
	if r.Vector == nil {
		return &ValidationFailedError{Reason: "row has nil vector"}
	}
	if b.fp.Dim > 0 && len(r.Vector) != b.fp.Dim {
		return &ValidationFailedError{Reason: "row vector dim does not match fingerprint dim"}
	}
	blob := encodeFloat32Blob(r.Vector)
	_, err := b.store.db.ExecContext(ctx, `
        INSERT INTO generation_rows (generation_id, document_id, node_id, text_hash, path, start_line, end_line, span_method, vector)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(generation_id, node_id) DO UPDATE SET
            document_id = excluded.document_id,
            text_hash   = excluded.text_hash,
            path        = excluded.path,
            start_line  = excluded.start_line,
            end_line    = excluded.end_line,
            span_method = excluded.span_method,
            vector      = excluded.vector`,
		string(b.id), r.DocumentID, string(r.NodeID), r.TextHash, r.Path,
		r.StartLine, r.EndLine, r.SpanMethod, blob)
	if err != nil {
		return fmt.Errorf("embed: upsert row: %w", err)
	}
	return nil
}

func (b *sqliteBuild) Commit(ctx context.Context) error {
	b.store.buildMu.Lock()
	defer b.store.buildMu.Unlock()
	tx, err := b.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("embed: commit begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	// SW-261 review round 2 (MAJOR 3): validate every row BEFORE the
	// pointer moves. The pre-fix shape counted rows in Commit and
	// validated per-row dimensions / NodeID resolution later in Active,
	// after the pointer had moved. That violated AC-6 / AC-7's "the
	// active pointer shall never point at an unvalidated generation"
	// contract. The validation now runs as part of Commit, on the
	// staging rows, and only on a complete pass does the demote /
	// promote transaction proceed. A single failed row rolls the whole
	// transaction back — the prior active generation stays intact.
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM generation_rows WHERE generation_id = ?`,
		string(b.id)).Scan(&n); err != nil {
		return fmt.Errorf("embed: count staging rows: %w", err)
	}
	if n == 0 {
		// A zero-row build is a legitimate state (e.g. a reindex over an
		// emptied graph). The prior active generation's row_count is
		// preserved by the demote; we just commit a new zero-row
		// generation. The fingerprint canonical stays consistent.
	}
	// Per-row dimension validation. Upsert already checks the dim at
	// write time, but a hand-tampered sidecar (or a future migration
	// path) could land a wrong-dim row; the Commit-time pass catches
	// it before the pointer moves. fp.Dim == 0 means "unknown" — a
	// dim-zero embedder (Ollama until its first request) is now
	// promoted to a learned dim via the dim-discovery contract in
	// generate.go (MAJOR 5); the validate-here pass is a defense-in-
	// depth check, gated on a known dim to skip the dim-zero case.
	if b.fp.Dim > 0 && n > 0 {
		dimRows, err := tx.QueryContext(ctx, `
            SELECT node_id, vector FROM generation_rows
            WHERE generation_id = ?
            ORDER BY node_id`,
			string(b.id))
		if err != nil {
			return &ValidationFailedError{Reason: fmt.Sprintf("stream rows for dim validation: %v", err)}
		}
		for dimRows.Next() {
			var (
				nid  string
				blob []byte
			)
			if err := dimRows.Scan(&nid, &blob); err != nil {
				dimRows.Close()
				return &ValidationFailedError{Reason: fmt.Sprintf("scan row for dim validation: %v", err)}
			}
			if len(blob) != b.fp.Dim*4 {
				dimRows.Close()
				return &ValidationFailedError{Reason: fmt.Sprintf("vector dim drift at node %s: persisted=%d expected=%d", nid, len(blob)/4, b.fp.Dim)}
			}
		}
		if err := dimRows.Err(); err != nil {
			dimRows.Close()
			return &ValidationFailedError{Reason: fmt.Sprintf("dim validation iteration: %v", err)}
		}
		dimRows.Close()
	}
	// NodeID non-empty validation. Upsert already rejects empty NodeID,
	// but the Commit-time pass defends against a hand-tampered sidecar.
	// A NodeReferencer-backed referencability check happens in Active
	// because the graphstore is not part of the Build's transactional
	// surface — only the durable sidecar is — so a referenced-node
	// check belongs to the Active call where the graphstore handle is
	// available.
	if n > 0 {
		var emptyCount int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM generation_rows WHERE generation_id = ? AND (node_id = '' OR node_id IS NULL)`,
			string(b.id)).Scan(&emptyCount); err != nil {
			return &ValidationFailedError{Reason: fmt.Sprintf("check empty NodeID: %v", err)}
		}
		if emptyCount > 0 {
			return &ValidationFailedError{Reason: fmt.Sprintf("%d rows have empty NodeID", emptyCount)}
		}
	}
	// Atomically: demote the prior active row (one row, globally) and
	// promote the staging row. The at-most-one-active invariant is
	// enforced by generations_one_active.
	if _, err := tx.ExecContext(ctx,
		`UPDATE generations SET is_active = 0 WHERE is_active = 1`); err != nil {
		return fmt.Errorf("embed: demote prior active: %w", err)
	}
	// Promote the staging row. RowsAffected must be exactly 1: zero
	// means the staging id vanished (a concurrent process or a manual
	// delete — neither is recoverable here), two would mean a duplicate
	// id (the schema prevents it, but a race could in principle produce
	// it). Either way, demoting the prior active without promoting a
	// successor would leave the sidecar with NO active generation —
	// AC-6 ("the active pointer shall never point at an unvalidated
	// generation") is also AC-7 ("Active returns StateMissing"). We
	// detect the failure INSIDE the transaction so the demote is rolled
	// back along with the failed promotion.
	promoteRes, err := tx.ExecContext(ctx,
		`UPDATE generations SET is_active = 1, is_staging = 0, row_count = ?, committed_at = ? WHERE id = ? AND is_staging = 1`,
		n, commitTimestamp(), string(b.id))
	if err != nil {
		return fmt.Errorf("embed: promote staging: %w", err)
	}
	promoted, perr := promoteRes.RowsAffected()
	if perr != nil {
		return fmt.Errorf("embed: count promoted staging: %w", perr)
	}
	if promoted != 1 {
		return &ValidationFailedError{Reason: fmt.Sprintf("promote staging matched %d rows, want 1: the staging id vanished or duplicated; demote rolled back", promoted)}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("embed: commit generation: %w", err)
	}
	b.store.liveBuildsMu.Lock()
	delete(b.store.liveBuilds, b.id)
	b.store.liveBuildsMu.Unlock()
	return nil
}

func (b *sqliteBuild) Abort(ctx context.Context) error {
	b.store.buildMu.Lock()
	defer b.store.buildMu.Unlock()
	tx, err := b.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("embed: abort begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM generation_rows WHERE generation_id = ?`,
		string(b.id)); err != nil {
		return fmt.Errorf("embed: abort delete rows: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM generations WHERE id = ? AND is_staging = 1`,
		string(b.id)); err != nil {
		return fmt.Errorf("embed: abort delete generation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("embed: abort commit: %w", err)
	}
	b.store.liveBuildsMu.Lock()
	delete(b.store.liveBuilds, b.id)
	b.store.liveBuildsMu.Unlock()
	return nil
}

// Active implements GenerationStore. The active generation is the one whose
// canonical fingerprint matches the requested fingerprint AND has is_active=1.
func (s *SQLiteGenerationStore) Active(ctx context.Context, fp Fingerprint, nodes NodeReferencer) (Generation, State, error) {
	if !s.schemaPresent {
		return Generation{Fingerprint: fp}, StateMissing, nil
	}
	var (
		id          string
		canonical   string
		dim         int
		schema      string
		rowCount    int
		committedAt sql.NullString
	)
	columns := "id, fingerprint, fingerprint_dim, document_schema, row_count, ''"
	if s.hasCommittedAt {
		columns = "id, fingerprint, fingerprint_dim, document_schema, row_count, committed_at"
	}
	err := s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM generations WHERE is_active = 1 LIMIT 1`).Scan(&id, &canonical, &dim, &schema, &rowCount, &committedAt)
	if err == sql.ErrNoRows {
		return Generation{ID: "", Fingerprint: fp}, StateMissing, nil
	}
	if err != nil {
		return Generation{}, StateMissing, fmt.Errorf("embed: read active generation: %w", err)
	}
	storedFP := fingerprintFromCanonical(canonical, dim, schema)
	committedAtStr := ""
	if committedAt.Valid {
		committedAtStr = committedAt.String
	}
	if storedFP.Canonical() != fp.Canonical() {
		// Active exists but its fingerprint differs from the requested
		// one → StateStale (AC-7). The fingerprint comparison sees every
		// field; a single-field drift is enough to mark stale.
		return Generation{ID: GenerationID(id), Fingerprint: storedFP, RowCount: rowCount, Dim: dim, CommittedAt: committedAtStr},
			StateStale, nil
	}
	// AC-7 `corrupt` checks. We re-count rows and dims and (when a
	// NodeReferencer is supplied) confirm every node id resolves.
	var counted int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM generation_rows WHERE generation_id = ?`,
		id).Scan(&counted); err != nil {
		return Generation{ID: GenerationID(id), Fingerprint: storedFP, RowCount: rowCount, Dim: dim, CommittedAt: committedAtStr},
			StateCorrupt, fmt.Errorf("embed: re-count rows: %w", err)
	}
	if counted != rowCount {
		return Generation{ID: GenerationID(id), Fingerprint: storedFP, RowCount: rowCount, Dim: dim, CommittedAt: committedAtStr},
			StateCorrupt,
			&ValidationFailedError{Reason: fmt.Sprintf("row count drift: metadata=%d persisted=%d", rowCount, counted)}
	}
	// Dimensional check: every persisted row's vector must equal fp.Dim
	// (when known). The previous revision sampled a single row, which
	// let drift in any non-sampled row pass — the mem adapter validates
	// every row, so the SQLite adapter must too. The check is streamed
	// (one row at a time, by NodeId in canonical order) so peak memory
	// is bounded.
	//
	// An EMPTY generation is legitimate, not corrupt: a repository whose
	// nodes are all excluded (generated paths, artefact kinds, no
	// establishable span) embeds nothing, and that is a valid published
	// state. When there is nothing to validate, the row-count check
	// above already proved the metadata and the table agree that it is
	// empty.
	if fp.Dim > 0 && counted > 0 {
		rows, err := s.db.QueryContext(ctx, `
            SELECT node_id, vector FROM generation_rows
            WHERE generation_id = ?
            ORDER BY node_id`,
			id)
		if err != nil {
			return Generation{ID: GenerationID(id), Fingerprint: storedFP, RowCount: rowCount, Dim: dim, CommittedAt: committedAtStr},
				StateCorrupt, fmt.Errorf("embed: stream rows for dim check: %w", err)
		}
		for rows.Next() {
			var (
				nid  string
				blob []byte
			)
			if err := rows.Scan(&nid, &blob); err != nil {
				rows.Close()
				return Generation{ID: GenerationID(id), Fingerprint: storedFP, RowCount: rowCount, Dim: dim, CommittedAt: committedAtStr},
					StateCorrupt, fmt.Errorf("embed: scan row for dim check: %w", err)
			}
			if len(blob) != fp.Dim*4 {
				rows.Close()
				return Generation{ID: GenerationID(id), Fingerprint: storedFP, RowCount: rowCount, Dim: dim, CommittedAt: committedAtStr},
					StateCorrupt,
					&ValidationFailedError{Reason: fmt.Sprintf("vector dim drift at node %s: persisted=%d expected=%d", nid, len(blob)/4, fp.Dim)}
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return Generation{ID: GenerationID(id), Fingerprint: storedFP, RowCount: rowCount, Dim: dim, CommittedAt: committedAtStr},
				StateCorrupt, fmt.Errorf("embed: dim-check row iteration: %w", err)
		}
		rows.Close()
	}
	if nodes != nil {
		// Every NodeID must resolve. Stream in canonical order so a
		// validation failure that bails out still leaves a consistent
		// diagnostic.
		rows, err := s.db.QueryContext(ctx, `
            SELECT node_id FROM generation_rows WHERE generation_id = ? ORDER BY node_id`,
			id)
		if err != nil {
			return Generation{ID: GenerationID(id), Fingerprint: storedFP, RowCount: rowCount, Dim: dim, CommittedAt: committedAtStr},
				StateCorrupt, fmt.Errorf("embed: stream rows for validation: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var nid string
			if err := rows.Scan(&nid); err != nil {
				return Generation{ID: GenerationID(id), Fingerprint: storedFP, RowCount: rowCount, Dim: dim, CommittedAt: committedAtStr},
					StateCorrupt, fmt.Errorf("embed: scan node id: %w", err)
			}
			exists, nerr := nodes.NodeExists(ctx, model.NodeId(nid))
			if nerr != nil {
				return Generation{ID: GenerationID(id), Fingerprint: storedFP, RowCount: rowCount, Dim: dim, CommittedAt: committedAtStr},
					StateCorrupt, nerr
			}
			if !exists {
				return Generation{ID: GenerationID(id), Fingerprint: storedFP, RowCount: rowCount, Dim: dim, CommittedAt: committedAtStr},
					StateCorrupt,
					&ValidationFailedError{Reason: "row references unknown node: " + nid}
			}
		}
		if err := rows.Err(); err != nil {
			return Generation{ID: GenerationID(id), Fingerprint: storedFP, RowCount: rowCount, Dim: dim, CommittedAt: committedAtStr},
				StateCorrupt, err
		}
	}
	return Generation{
		ID:          GenerationID(id),
		Fingerprint: storedFP,
		RowCount:    rowCount,
		Dim:         dim,
		CommittedAt: committedAtStr,
	}, StateReady, nil
}

// Previous returns the most recently committed inactive generation.
func (s *SQLiteGenerationStore) Previous(ctx context.Context, activeID GenerationID) (Generation, bool, error) {
	if !s.schemaPresent {
		return Generation{}, false, nil
	}
	columns := "id, fingerprint, fingerprint_dim, document_schema, row_count, ''"
	if s.hasCommittedAt {
		columns = "id, fingerprint, fingerprint_dim, document_schema, row_count, committed_at"
	}
	var id, canonical, schema string
	var dim, rowCount int
	var committedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM generations WHERE is_active=0 AND is_staging=0 AND id<>? ORDER BY rowid DESC LIMIT 1`, string(activeID)).Scan(&id, &canonical, &dim, &schema, &rowCount, &committedAt)
	if err == sql.ErrNoRows {
		return Generation{}, false, nil
	}
	if err != nil {
		return Generation{}, false, fmt.Errorf("embed: read previous generation: %w", err)
	}
	builtAt := ""
	if committedAt.Valid {
		builtAt = committedAt.String
	}
	return Generation{ID: GenerationID(id), Fingerprint: fingerprintFromCanonical(canonical, dim, schema), RowCount: rowCount, Dim: dim, CommittedAt: builtAt}, true, nil
}

// Load implements GenerationStore. Returns rows in canonical
// (node_id, document_id) order. An unknown id is an empty slice.
func (s *SQLiteGenerationStore) Load(ctx context.Context, id GenerationID) ([]Row, error) {
	if !s.schemaPresent {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
        SELECT document_id, node_id, text_hash, path, start_line, end_line, span_method, vector
        FROM generation_rows
        WHERE generation_id = ?
        ORDER BY node_id, document_id`,
		string(id))
	if err != nil {
		return nil, fmt.Errorf("embed: load rows: %w", err)
	}
	defer rows.Close()
	out := make([]Row, 0)
	for rows.Next() {
		var (
			docID, nodeID, hash, path, span string
			startLine, endLine              int
			blob                            []byte
		)
		if err := rows.Scan(&docID, &nodeID, &hash, &path, &startLine, &endLine, &span, &blob); err != nil {
			return nil, fmt.Errorf("embed: scan row: %w", err)
		}
		out = append(out, Row{
			GenerationID: id,
			DocumentID:   docID,
			NodeID:       model.NodeId(nodeID),
			TextHash:     hash,
			Path:         path,
			StartLine:    startLine,
			EndLine:      endLine,
			SpanMethod:   span,
			Vector:       decodeFloat32Blob(blob),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Defensive: the SQL ORDER BY already yields canonical order.
	sort.Slice(out, func(i, j int) bool {
		if out[i].NodeID != out[j].NodeID {
			return out[i].NodeID < out[j].NodeID
		}
		return out[i].DocumentID < out[j].DocumentID
	})
	return out, nil
}

// LoadRow implements the GenerationStore point-lookup seam. It issues
// an indexed point probe (PK is (generation_id, node_id)) so AC-4
// carry-forward reuses one prior row at a time without materialising
// the whole generation. ok=false when the row is absent.
// DimForModel implements GenerationStore. It reads the dimension the ACTIVE
// generation recorded, and returns it only when that generation's fingerprint
// names the same model — never a dimension belonging to some other model's
// generation. See the interface doc for why the reload path needs this and
// what it deliberately does not detect.
func (s *SQLiteGenerationStore) DimForModel(ctx context.Context, modelID string) (int, bool, error) {
	if modelID == "" || !s.schemaPresent {
		return 0, false, nil
	}
	var (
		canonical string
		dim       int
		schema    string
	)
	err := s.db.QueryRowContext(ctx, `
        SELECT fingerprint, fingerprint_dim, document_schema
        FROM generations
        WHERE is_active = 1
        LIMIT 1`).Scan(&canonical, &dim, &schema)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("embed: dim for model: %w", err)
	}
	if fingerprintFromCanonical(canonical, dim, schema).ModelID != modelID {
		return 0, false, nil
	}
	if dim <= 0 {
		// A generation built by an embedder that never learned its own
		// dimension carries nothing worth adopting.
		return 0, false, nil
	}
	return dim, true, nil
}

func (s *SQLiteGenerationStore) LoadRow(ctx context.Context, id GenerationID, nodeID model.NodeId) (Row, bool, error) {
	if !s.schemaPresent {
		return Row{}, false, nil
	}
	var (
		docID, nID, hash, path, span string
		startLine, endLine           int
		blob                         []byte
	)
	err := s.db.QueryRowContext(ctx, `
        SELECT document_id, node_id, text_hash, path, start_line, end_line, span_method, vector
        FROM generation_rows
        WHERE generation_id = ? AND node_id = ?
        LIMIT 1`,
		string(id), string(nodeID)).Scan(&docID, &nID, &hash, &path, &startLine, &endLine, &span, &blob)
	if err == sql.ErrNoRows {
		return Row{}, false, nil
	}
	if err != nil {
		return Row{}, false, fmt.Errorf("embed: load row: %w", err)
	}
	return Row{
		GenerationID: id,
		DocumentID:   docID,
		NodeID:       model.NodeId(nID),
		TextHash:     hash,
		Path:         path,
		StartLine:    startLine,
		EndLine:      endLine,
		SpanMethod:   span,
		Vector:       decodeFloat32Blob(blob),
	}, true, nil
}

// DeleteStagingForTest is a test-only helper that deletes the current
// staging generation row. Used by AC-6's RowsAffected-on-promote
// conformance test to simulate the staging row vanishing out from
// under a build (the test pins the promote-must-succeed-or-rollback
// invariant). The test-only name is the call-site convention: nothing
// in the production path uses it.
func (s *SQLiteGenerationStore) DeleteStagingForTest(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM generations WHERE is_staging = 1")
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// DBForTest returns the underlying *sql.DB handle. The test-only name
// flags the seam: production code must NOT depend on the handle (the
// store's public surface is the GenerationStore interface). The
// seam exists for the MAJOR 3 Commit-time-validation test, which
// hand-injects a wrong-dim row into the staging generation to assert
// Commit refuses BEFORE the pointer moves.
func (s *SQLiteGenerationStore) DBForTest(_ context.Context) *sql.DB {
	return s.db
}

// Close releases the DB handle when this store opened it. When constructed
// over a borrowed handle it is a no-op.
func (s *SQLiteGenerationStore) Close() error {
	if s.ownDB && s.db != nil {
		return s.db.Close()
	}
	return nil
}

// fingerprintFromCanonical rebuilds a Fingerprint from its canonical string
// plus the stored dim and schema. The typed Fingerprint is only used to
// render diagnostics — Active's decision is a direct canonical-string
// compare (which sees every field). The canonical is decoded with the
// inverse of encodeCanonical so the returned Fingerprint reflects the
// eight-field shape.
func fingerprintFromCanonical(canonical string, dim int, schema string) Fingerprint {
	fp := Fingerprint{DocumentSchema: schema, Dim: dim}
	if canonical == "" {
		return fp
	}
	parts := decodeCanonical(canonical)
	if len(parts) >= 1 {
		fp.ModelID = parts[0]
	}
	if len(parts) >= 2 {
		fp.Revision = parts[1]
	}
	if len(parts) >= 3 {
		fp.ModelSHA256 = parts[2]
	}
	if len(parts) >= 4 {
		fp.TokenizerSHA256 = parts[3]
	}
	if len(parts) >= 5 {
		if d, err := strconv.Atoi(parts[4]); err == nil {
			fp.Dim = d
		}
	}
	if len(parts) >= 6 {
		fp.DocumentSchema = parts[5]
	}
	if len(parts) >= 7 {
		fp.ChunkerConfig = parts[6]
	}
	if len(parts) >= 8 {
		fp.GraphGeneration = parts[7]
	}
	return fp
}

// encodeFloat32Blob / decodeFloat32Blob mirror the fixed-endianness discipline
// from the old `vectors` table (big-endian float32 components) so a persisted
// row is byte-identical across architectures.
func encodeFloat32Blob(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.BigEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}

func decodeFloat32Blob(b []byte) []float32 {
	n := len(b) / 4
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		out[i] = math.Float32frombits(binary.BigEndian.Uint32(b[i*4:]))
	}
	return out
}

// (intentionally unused — kept so `go vet` does not flag the rand import
// when the build tag for tests is removed.)
var _ = binary.BigEndian
