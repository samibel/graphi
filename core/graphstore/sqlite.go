package graphstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/samibel/graphi/core/model"

	_ "modernc.org/sqlite" // pure-Go, CGo-free SQLite driver
)

// sqliteDriverName is modernc.org/sqlite's registered database/sql driver name.
const sqliteDriverName = "sqlite"

// SQLiteStore is the durable Graphstore backend. SQLite (WAL journal mode, FTS5)
// is the authoritative source of truth; an evictable in-memory hot cache sits in
// front of it. Writes commit to SQLite FIRST (base rows + FTS5 in one
// transaction), then update the cache — so evicting the cache never loses data.
//
// Concurrency: reads may run concurrently; all writes are serialized through a
// single writer mutex (the canonical pattern for SQLite under WAL). The cache is
// guarded independently so reads can proceed while a rebuild is in flight.
type SQLiteStore struct {
	db   *sql.DB
	path string

	writeMu sync.Mutex // serializes all write transactions (single writer)

	cacheMu sync.RWMutex
	cache   *memGraph // nil => evicted; rebuilt lazily on next read

	closed atomic.Bool

	// failAfterCommit, when set via SetFailAfterCommitHook, makes the NEXT write
	// return this error AFTER the SQLite transaction has committed but BEFORE the
	// cache is updated. This proves the write-ordering / no-data-loss invariant:
	// durable state is complete, the cache is merely invalidated. Test-only.
	failHookMu      sync.Mutex
	failAfterCommit error

	// rebuilds counts how many times the cache was (re)built from SQLite. Tests
	// read it to confirm a query was served from a fresh rebuild after eviction
	// (the observable cache-miss/rebuild signal).
	rebuilds atomic.Int64
}

var _ Graphstore = (*SQLiteStore)(nil)

// memGraph is the in-memory hot cache: a transparent mirror of the durable state.
// It is never authoritative.
type memGraph struct {
	nodes map[model.NodeId]model.Node
	edges map[model.EdgeId]model.Edge
}

// OpenSQLite opens (creating if needed) a SQLite-backed store at dbPath. It
// applies WAL journal mode to every pooled connection (via the DSN and a
// connection-init hook), creates the schema including an FTS5 index over the
// searchable text fields, and runs a startup self-check asserting WAL is active
// and FTS5 is usable — failing fast with an actionable error otherwise.
func OpenSQLite(dbPath string) (*SQLiteStore, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, errors.New("graphstore: empty sqlite path")
	}
	// _pragma applies per-connection PRAGMAs to EVERY pooled connection, so WAL
	// (a database-level mode that still requires per-connection busy_timeout and
	// foreign_keys) holds across the pool. WAL itself is database-global once set.
	// cache_size(-64000) = 64 MB page cache and temp_store(MEMORY) keep bulk
	// passes off disk; both are per-connection tuning only — durability
	// semantics (WAL + synchronous=NORMAL at commit) are unchanged.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-64000)&_pragma=temp_store(MEMORY)",
		filepath.ToSlash(dbPath))

	db, err := sql.Open(sqliteDriverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("graphstore: open sqlite: %w", err)
	}

	s := &SQLiteStore{db: db, path: dbPath}

	if err := s.initSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.selfCheck(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	// PRIV-01 (SW-119): the graph derives from potentially private source, so
	// the database files are owner-only. The SQLite driver creates them with
	// the umask default (typically 0644); tighten the main file plus the WAL
	// sidecars, migrating a pre-existing too-wide store on open.
	if err := TightenDBFileModes(dbPath); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// OpenSQLiteReadOnly opens an EXISTING store without modifying its content:
// mode=ro (never creates a missing database) plus query_only on every pooled
// connection, no schema init, no migrations, no file-mode tightening. It backs
// strictly-observational surfaces (`graphi status`, snapshot listings). Reads
// work as usual; every write method fails with SQLite's readonly error. Note
// that reading a WAL-mode database still creates the transient -wal/-shm
// coordination sidecars when absent (empty WAL, shared-memory index) — the
// database content itself is never touched. immutable=1 would avoid even
// that, but it is unsafe while a concurrent writer (daemon) may commit.
func OpenSQLiteReadOnly(dbPath string) (*SQLiteStore, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, errors.New("graphstore: empty sqlite path")
	}
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("graphstore: open read-only: %w", err)
	}
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)",
		filepath.ToSlash(dbPath))
	db, err := sql.Open(sqliteDriverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("graphstore: open sqlite read-only: %w", err)
	}
	s := &SQLiteStore{db: db, path: dbPath}
	// Probe with a harmless query so a corrupt/non-SQLite file fails here, not
	// on the first caller read.
	var one int
	if err := db.QueryRowContext(context.Background(), "SELECT 1").Scan(&one); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("graphstore: read-only probe: %w", err)
	}
	return s, nil
}

// CountNodes reports the durable node count directly from SQLite, bypassing the
// hot cache so read-only observers don't pay a full cache build.
func (s *SQLiteStore) CountNodes(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM nodes").Scan(&n); err != nil {
		return 0, fmt.Errorf("graphstore: count nodes: %w", err)
	}
	return n, nil
}

// TightenDBFileModes chmods a SQLite database file and its -wal/-shm sidecars
// to 0600 when they exist with wider permissions (PRIV-01). The main file's
// failure is an error; absent sidecars are fine (SQLite creates them lazily,
// inheriting the main file's mode).
func TightenDBFileModes(dbPath string) error {
	for i, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		fi, err := os.Stat(p)
		if err != nil || fi.Mode().Perm() == 0o600 {
			continue
		}
		if err := os.Chmod(p, 0o600); err != nil {
			if i == 0 {
				return fmt.Errorf("graphstore: tighten db file mode: %w", err)
			}
		}
	}
	return nil
}

// SQLiteFactory is a Factory for the durable backend. It places the database file
// inside dir.
func SQLiteFactory(dir string) (Graphstore, error) {
	return OpenSQLite(filepath.Join(dir, "graphi.db"))
}

// graphstoreSchemaVersion is the on-disk edge layout version, stamped into
// PRAGMA user_version. Bump it whenever the physical edges/reasons schema
// changes so a pre-existing store built by an older binary is detected and
// re-created rather than mis-read.
//
//	0 : implicit — the original inline layout (edges.reason TEXT, edges.evidence
//	    TEXT) written before this stamp existed.
//	2 : WP-06 storage diet — the highly-repetitive edge `reason` is interned into
//	    the `reasons` dictionary (edges.reason_id); `evidence` stays inline (it is
//	    per-edge-unique, so interning it does not dedup); and edges are no longer
//	    full-text indexed in `search` (nodes only).
//	3 : WP-10 node meta — nodes carry a NON-identity `meta` column (JSON-encoded
//	    NodeMeta: source annotations + flags). Added non-destructively via ALTER
//	    TABLE ADD COLUMN (default ''), so a pre-existing nodes table is migrated
//	    in place rather than re-created.
//	4 : deterministic FTS rowids — every `search` row is keyed by
//	    ftsNodeRowid(owner_id) instead of an auto-assigned rowid, so FTS
//	    delete/replace needs no per-batch rowid map (whose seed re-scanned the
//	    whole search table at every BeginBatch) and no owner-keyed full-table
//	    scan. migrateFTSRowids rebuilds a pre-v4 search table in place from
//	    `nodes`; graph bytes/snapshots are unaffected (FTS is derived state).
const graphstoreSchemaVersion = 4

// initSchema creates the base tables and the (node-only) FTS5 index. Edge
// provenance is stored compactly: the highly-repetitive `reason` string is
// interned into the `reasons` dictionary and the edge row references it by
// integer id (millions of near-identical edges then share one dictionary row
// instead of repeating the text inline). `evidence` stays inline as a JSON
// array: it is per-edge-unique (it carries the specific file:line references),
// so interning it would add a dictionary row + UNIQUE-index entry per edge for
// no dedup — strictly worse than inline. The JSON evidence encoding is preserved
// verbatim, so the null/empty/populated distinction round-trips exactly. The
// FTS5 virtual table covers node name (qualified_name) only — edge reason is NOT
// full-text indexed (it is filtered by substring over the hot cache, matching
// MemStore); FTS-indexing millions of repetitive edge reasons was a dominant
// on-disk cost.
func (s *SQLiteStore) initSchema(ctx context.Context) error {
	// Detect and re-create a pre-existing old-layout edges table before creating
	// the current schema (a no-op on a fresh DB). CREATE TABLE IF NOT EXISTS
	// alone would leave the old inline columns in place and the interned reads
	// would fail with "no such column: reason_id".
	if err := s.migrateEdgeLayout(ctx); err != nil {
		return err
	}
	// Add the WP-10 `meta` column to a pre-existing nodes table (no-op on a fresh
	// DB, where the DDL below creates it directly).
	if err := s.migrateNodeMeta(ctx); err != nil {
		return err
	}
	const ddl = `
CREATE TABLE IF NOT EXISTS nodes (
	id             TEXT PRIMARY KEY,
	kind           TEXT NOT NULL,
	qualified_name TEXT NOT NULL,
	source_path    TEXT NOT NULL,
	line           INTEGER NOT NULL,
	col            INTEGER NOT NULL,
	-- WP-10: JSON-encoded NON-identity NodeMeta (annotations/flags). Empty string
	-- means "no metadata". Migrated onto a pre-existing table by migrateNodeMeta.
	meta           TEXT NOT NULL DEFAULT ''
);
-- reasons interns the repetitive edge reason string so millions of
-- near-identical edges share one dictionary row instead of repeating the text
-- inline. Evidence is NOT interned (it is per-edge-unique).
CREATE TABLE IF NOT EXISTS reasons (
	id   INTEGER PRIMARY KEY,
	text TEXT NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS edges (
	id              TEXT PRIMARY KEY,
	from_id         TEXT NOT NULL,
	to_id           TEXT NOT NULL,
	kind            TEXT NOT NULL,
	confidence_tier TEXT NOT NULL,
	confidence      REAL NOT NULL,
	reason_id       INTEGER NOT NULL,
	evidence        TEXT NOT NULL,
	FOREIGN KEY(from_id)   REFERENCES nodes(id),
	FOREIGN KEY(to_id)     REFERENCES nodes(id),
	FOREIGN KEY(reason_id) REFERENCES reasons(id)
);
CREATE VIRTUAL TABLE IF NOT EXISTS search USING fts5(
	owner_kind UNINDEXED,  -- 'node' (edges are never FTS-indexed)
	owner_id   UNINDEXED,
	text
);
-- Retire superseded endpoint-only and endpoint+id indexes. Exactly two
-- endpoint+kind+id composites remain: their endpoint prefix still serves
-- FK/delete lookups, their endpoint+kind prefix serves sparse filtered reads,
-- and their complete order serves deterministic unfiltered bounded reads.
-- Keeping redundant variants fails the per-edge storage budget and amplifies
-- every ingest write.
DROP INDEX IF EXISTS edges_from_id;
DROP INDEX IF EXISTS edges_to_id;
DROP INDEX IF EXISTS edges_from_id_edge_id;
DROP INDEX IF EXISTS edges_to_id_edge_id;
-- BoundedGraphLookup stops after its explicit window. Unfiltered reads use
-- deterministic (kind,id) order; explicitly filtered reads retain EdgeId order.
-- The indexes are content-neutral: graph bytes/snapshot ordering do not change.
CREATE INDEX IF NOT EXISTS edges_from_kind_id_edge_id ON edges(from_id, kind, id);
CREATE INDEX IF NOT EXISTS edges_to_kind_id_edge_id   ON edges(to_id, kind, id);
-- Symbol-lookup indexes (CORE-01, ADR 0003 D3): SymbolLookupPort's
-- QualifiedName/SourcePath equality lookups were full nodes scans without
-- them (pinned by the SP-11 spike). Content-neutral like the endpoint
-- indexes — no row content changes, listings stay ordered by id, so graph
-- bytes/snapshots are unaffected and no user_version bump is needed.
CREATE INDEX IF NOT EXISTS nodes_qualified_name ON nodes(qualified_name);
CREATE INDEX IF NOT EXISTS nodes_source_path    ON nodes(source_path);
CREATE TABLE IF NOT EXISTS kv_meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`
	if _, err := s.db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("graphstore: init schema (FTS5 may be unavailable): %w", err)
	}
	// Re-key a pre-v4 search table to deterministic rowids. Runs after the DDL
	// (so the tables exist on a fresh DB) and before the stamp below (so it can
	// still observe the store's prior version).
	if err := s.migrateFTSRowids(ctx); err != nil {
		return err
	}
	// Stamp the layout version (informational; read by the doctor and by future
	// migrations). user_version takes no bound parameter, so the compile-time
	// constant is formatted directly.
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", graphstoreSchemaVersion)); err != nil {
		return fmt.Errorf("graphstore: stamp schema version: %w", err)
	}
	return nil
}

// migrateFTSRowids rebuilds the `search` table with deterministic rowids
// (ftsNodeRowid) for a store written before schema version 4, whose rows
// carry auto-assigned rowids. The rebuild is content-neutral: `search` is
// derived state (Snapshot never serializes it, SearchNodes joins on
// owner_id), so graph bytes and warm-start validity are untouched — no
// re-index is required. A fresh DB (empty tables) does the same work on zero
// rows. Runs in one transaction so a failure leaves the old table intact.
func (s *SQLiteStore) migrateFTSRowids(ctx context.Context) error {
	var ver int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&ver); err != nil {
		return fmt.Errorf("graphstore: read schema version: %w", err)
	}
	if ver >= 4 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("graphstore: begin fts rowid migration: %w", err)
	}
	rollback := func() { _ = tx.Rollback() }
	if _, err := tx.ExecContext(ctx, "DELETE FROM search"); err != nil {
		rollback()
		return fmt.Errorf("graphstore: clear pre-v4 search: %w", err)
	}
	rows, err := tx.QueryContext(ctx, "SELECT id, qualified_name FROM nodes")
	if err != nil {
		rollback()
		return fmt.Errorf("graphstore: read nodes for fts rebuild: %w", err)
	}
	type searchRow struct {
		rowid int64
		id    string
		text  string
	}
	// Materialize before re-inserting: SQLite tolerates writes with an open
	// cursor poorly across drivers, and a node-id + name pair set is small.
	var pending []searchRow
	for rows.Next() {
		var id, qn string
		if err := rows.Scan(&id, &qn); err != nil {
			rows.Close()
			rollback()
			return fmt.Errorf("graphstore: scan node for fts rebuild: %w", err)
		}
		rowid, err := ftsNodeRowid(model.NodeId(id))
		if err != nil {
			rows.Close()
			rollback()
			return err
		}
		pending = append(pending, searchRow{rowid: rowid, id: id, text: qn})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		rollback()
		return fmt.Errorf("graphstore: iterate nodes for fts rebuild: %w", err)
	}
	rows.Close()
	for _, r := range pending {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO search (rowid, owner_kind, owner_id, text) VALUES (?, 'node', ?, ?)",
			r.rowid, r.id, r.text); err != nil {
			rollback()
			return fmt.Errorf("graphstore: rebuild fts row for %s: %w", r.id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("graphstore: commit fts rowid migration: %w", err)
	}
	return nil
}

// migrateEdgeLayout detects a pre-existing edges table written under the old
// inline layout (a `reason` column, no `reason_id`) and drops the edge data —
// the edges table, the interning dictionary, and every edge row in the FTS
// index — so the current schema can be created fresh below. This is safe
// because the ingest warm-start stamp is bumped in lockstep (see
// engine/ingest/warmstart.go), so an upgraded binary re-indexes rather than
// warm-starting: a full pass repopulates the edges. Nodes and kv_meta are left
// untouched. A fresh DB (no edges table) and an already-current DB (reason_id
// present) both return without changes.
func (s *SQLiteStore) migrateEdgeLayout(ctx context.Context) error {
	var edgesTables int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='edges'").Scan(&edgesTables); err != nil {
		return fmt.Errorf("graphstore: probe edges table: %w", err)
	}
	if edgesTables == 0 {
		return nil // fresh DB: the DDL below builds the current layout.
	}
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info(edges)")
	if err != nil {
		return fmt.Errorf("graphstore: inspect edges columns: %w", err)
	}
	hasReasonID := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("graphstore: scan edges column: %w", err)
		}
		if name == "reason_id" {
			hasReasonID = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("graphstore: iterate edges columns: %w", err)
	}
	rows.Close()
	if hasReasonID {
		return nil // already the current interned layout.
	}
	// Old inline layout: drop the edge data and any stale edge FTS rows, then let
	// the DDL recreate the current schema. A full re-index repopulates.
	for _, stmt := range []string{
		"DELETE FROM search WHERE owner_kind='edge'",
		"DROP TABLE IF EXISTS edges",
		"DROP TABLE IF EXISTS reasons",
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("graphstore: migrate edge layout (%s): %w", stmt, err)
		}
	}
	return nil
}

// migrateNodeMeta adds the WP-10 `meta` column to a pre-existing nodes table
// that predates it (built by an older binary). ADD COLUMN with DEFAULT ” is
// non-destructive: existing node rows keep their identity/content and simply
// gain an empty-metadata column, which decodes to the zero NodeMeta. A fresh DB
// (no nodes table) is a no-op — the DDL creates the column directly — and an
// already-migrated DB (meta present) returns without changes. The ingest
// warm-start stamp is bumped in lockstep so an upgraded binary re-indexes and
// repopulates real metadata rather than trusting the empty backfill.
func (s *SQLiteStore) migrateNodeMeta(ctx context.Context) error {
	var nodesTables int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='nodes'").Scan(&nodesTables); err != nil {
		return fmt.Errorf("graphstore: probe nodes table: %w", err)
	}
	if nodesTables == 0 {
		return nil // fresh DB: the DDL builds the current layout with meta.
	}
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info(nodes)")
	if err != nil {
		return fmt.Errorf("graphstore: inspect nodes columns: %w", err)
	}
	hasMeta := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("graphstore: scan nodes column: %w", err)
		}
		if name == "meta" {
			hasMeta = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("graphstore: iterate nodes columns: %w", err)
	}
	rows.Close()
	if hasMeta {
		return nil // already migrated.
	}
	if _, err := s.db.ExecContext(ctx, "ALTER TABLE nodes ADD COLUMN meta TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("graphstore: add nodes.meta column: %w", err)
	}
	return nil
}

// encodeNodeMeta renders a NodeMeta to its stored JSON form, or "" when the meta
// is zero (so the common no-metadata node stores an empty string, not "{}").
func encodeNodeMeta(m model.NodeMeta) (string, error) {
	if m.IsZero() {
		return "", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("graphstore: encode node meta: %w", err)
	}
	return string(b), nil
}

// decodeNodeMeta parses stored node meta JSON back into a normalized NodeMeta.
// An empty string or "{}" (the no-metadata sentinels) decode to the zero value.
func decodeNodeMeta(s string) (model.NodeMeta, error) {
	if s == "" || s == "{}" {
		return model.NodeMeta{}, nil
	}
	var m model.NodeMeta
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return model.NodeMeta{}, fmt.Errorf("graphstore: decode node meta: %w", err)
	}
	// Re-normalize (sort/dedup) so downstream ordering is deterministic even if
	// the stored bytes were hand-edited.
	return model.NewNodeMeta(m.Annotations, m.Flags), nil
}

// internStringTx get-or-inserts text into the `reasons` dictionary and returns
// its stable id. It is idempotent: INSERT OR IGNORE leaves an existing row
// untouched, and the follow-up SELECT resolves the id whether the row was just
// created or pre-existed.
func internStringTx(ctx context.Context, tx *sql.Tx, text string) (int64, error) {
	if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO reasons (text) VALUES (?)", text); err != nil {
		return 0, fmt.Errorf("graphstore: intern string: %w", err)
	}
	var id int64
	if err := tx.QueryRowContext(ctx, "SELECT id FROM reasons WHERE text = ?", text).Scan(&id); err != nil {
		return 0, fmt.Errorf("graphstore: intern lookup: %w", err)
	}
	return id, nil
}

// selfCheck asserts journal_mode=wal on a pooled connection and that FTS5 is
// usable by creating and dropping a throwaway fts5 table. It fails fast with an
// actionable error so a misconfigured/CGo build is caught at startup.
func (s *SQLiteStore) selfCheck(ctx context.Context) error {
	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("graphstore: self-check journal_mode query: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("graphstore: self-check failed: journal_mode=%q, expected wal", mode)
	}
	if _, err := s.db.ExecContext(ctx, "CREATE VIRTUAL TABLE IF NOT EXISTS _fts5_selfcheck USING fts5(x)"); err != nil {
		return fmt.Errorf("graphstore: self-check failed: FTS5 unavailable: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "DROP TABLE IF EXISTS _fts5_selfcheck"); err != nil {
		return fmt.Errorf("graphstore: self-check drop fts5 probe: %w", err)
	}
	return nil
}

// JournalMode returns the active journal mode (for tests/diagnostics).
func (s *SQLiteStore) JournalMode(ctx context.Context) (string, error) {
	var mode string
	err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode)
	return mode, err
}

// CacheRebuilds returns how many times the hot cache was (re)built from SQLite.
// It is the observable cache-miss/rebuild signal used by tests.
func (s *SQLiteStore) CacheRebuilds() int64 { return s.rebuilds.Load() }

// SetFailAfterCommitHook arms a one-shot fault injection: the next write commits
// to SQLite, then returns err before touching the cache. Test-only.
func (s *SQLiteStore) SetFailAfterCommitHook(err error) {
	s.failHookMu.Lock()
	s.failAfterCommit = err
	s.failHookMu.Unlock()
}

func (s *SQLiteStore) takeFailHook() error {
	s.failHookMu.Lock()
	defer s.failHookMu.Unlock()
	err := s.failAfterCommit
	s.failAfterCommit = nil
	return err
}

// ---- writes (SQLite first, then cache) ----

func (s *SQLiteStore) PutNode(ctx context.Context, n model.Node) error {
	if s.closed.Load() {
		return ErrClosed
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("graphstore: begin tx: %w", err)
	}
	if err := upsertNodeTx(ctx, tx, n); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("graphstore: commit node: %w", err)
	}

	// Durable state is now complete. Fault-injection point: a failure here leaves
	// SQLite authoritative and the cache merely invalidated.
	if hookErr := s.takeFailHook(); hookErr != nil {
		s.evict()
		return hookErr
	}

	s.cacheMu.Lock()
	if s.cache != nil {
		s.cache.nodes[n.ID()] = n
	}
	s.cacheMu.Unlock()
	return nil
}

func (s *SQLiteStore) PutEdge(ctx context.Context, e model.Edge) error {
	if s.closed.Load() {
		return ErrClosed
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	// Endpoint existence: surface a typed error rather than a raw FK violation.
	if err := s.assertNodeExists(ctx, e.From()); err != nil {
		return err
	}
	if err := s.assertNodeExists(ctx, e.To()); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("graphstore: begin tx: %w", err)
	}
	if err := upsertEdgeTx(ctx, tx, e); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("graphstore: commit edge: %w", err)
	}

	if hookErr := s.takeFailHook(); hookErr != nil {
		s.evict()
		return hookErr
	}

	s.cacheMu.Lock()
	if s.cache != nil {
		s.cache.edges[e.ID()] = e
	}
	s.cacheMu.Unlock()
	return nil
}

// DeleteNode removes the node and every incident edge in a single SQLite
// transaction (durable FIRST), then updates the cache. The node's FTS5 row is
// removed in the same transaction so the search index never references a
// deleted owner (edges carry no FTS rows). Idempotent: a missing node deletes
// zero rows and is not an error. Crash-safe: a fault between commit and cache
// update leaves SQLite authoritative and only invalidates the cache (mirrors
// PutNode), so the next read rebuilds correctly.
func (s *SQLiteStore) DeleteNode(ctx context.Context, id model.NodeId) error {
	if s.closed.Load() {
		return ErrClosed
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	// Collect incident edge IDs first so the cache cascade matches the durable
	// cascade exactly (FTS rows are keyed by edge id too).
	incident, err := s.incidentEdgeIDs(ctx, id)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("graphstore: begin tx: %w", err)
	}
	// Edges first (they FK-reference the node), then the node. Edges carry no FTS
	// rows (nodes only), so no edge-FTS cleanup is needed here.
	if _, err := tx.ExecContext(ctx, "DELETE FROM edges WHERE from_id=? OR to_id=?", string(id), string(id)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("graphstore: delete incident edges: %w", err)
	}
	rowid, err := ftsNodeRowid(id)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM search WHERE rowid=?", rowid); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("graphstore: delete node fts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM nodes WHERE id=?", string(id)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("graphstore: delete node: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("graphstore: commit delete node: %w", err)
	}

	// Durable state is now complete. Fault-injection point mirrors PutNode.
	if hookErr := s.takeFailHook(); hookErr != nil {
		s.evict()
		return hookErr
	}

	s.cacheMu.Lock()
	if s.cache != nil {
		delete(s.cache.nodes, id)
		for _, eid := range incident {
			delete(s.cache.edges, eid)
		}
	}
	s.cacheMu.Unlock()
	return nil
}

// DeleteEdge removes a single edge (durable first, then cache). Idempotent.
func (s *SQLiteStore) DeleteEdge(ctx context.Context, id model.EdgeId) error {
	if s.closed.Load() {
		return ErrClosed
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("graphstore: begin tx: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM edges WHERE id=?", string(id)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("graphstore: delete edge: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("graphstore: commit delete edge: %w", err)
	}

	if hookErr := s.takeFailHook(); hookErr != nil {
		s.evict()
		return hookErr
	}

	s.cacheMu.Lock()
	if s.cache != nil {
		delete(s.cache.edges, id)
	}
	s.cacheMu.Unlock()
	return nil
}

// incidentEdgeIDs returns the IDs of every edge with the given node as From or
// To endpoint, read directly from the durable layer (so the cache cascade in
// DeleteNode matches the rows the transaction will remove).
func (s *SQLiteStore) incidentEdgeIDs(ctx context.Context, id model.NodeId) ([]model.EdgeId, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM edges WHERE from_id=? OR to_id=?", string(id), string(id))
	if err != nil {
		return nil, fmt.Errorf("graphstore: query incident edges: %w", err)
	}
	defer rows.Close()
	var out []model.EdgeId
	for rows.Next() {
		var eid string
		if err := rows.Scan(&eid); err != nil {
			return nil, fmt.Errorf("graphstore: scan incident edge: %w", err)
		}
		out = append(out, model.EdgeId(eid))
	}
	return out, rows.Err()
}

func (s *SQLiteStore) assertNodeExists(ctx context.Context, id model.NodeId) error {
	var one int
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM nodes WHERE id = ?", string(id)).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnknownEdgeEndpoint
	}
	if err != nil {
		return fmt.Errorf("graphstore: check endpoint: %w", err)
	}
	return nil
}

func upsertNodeTx(ctx context.Context, tx *sql.Tx, n model.Node) error {
	metaJSON, err := encodeNodeMeta(n.Meta())
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO nodes (id, kind, qualified_name, source_path, line, col, meta)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	kind=excluded.kind, qualified_name=excluded.qualified_name,
	source_path=excluded.source_path, line=excluded.line, col=excluded.col,
	meta=excluded.meta`,
		string(n.ID()), n.Kind(), n.QualifiedName(), n.SourcePath(), n.Line(), n.Column(), metaJSON); err != nil {
		return fmt.Errorf("graphstore: upsert node: %w", err)
	}
	// Refresh FTS row for this node. Deterministic-rowid delete-then-insert
	// keeps it idempotent AND rowid-keyed — owner_id is UNINDEXED, so the old
	// owner-keyed DELETE was a full search-table scan per upsert.
	rowid, err := ftsNodeRowid(n.ID())
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM search WHERE rowid=?", rowid); err != nil {
		return fmt.Errorf("graphstore: fts5 clear node: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO search (rowid, owner_kind, owner_id, text) VALUES (?, 'node', ?, ?)",
		rowid, string(n.ID()), n.QualifiedName()); err != nil {
		return fmt.Errorf("graphstore: fts5 index node: %w", err)
	}
	return nil
}

func upsertEdgeTx(ctx context.Context, tx *sql.Tx, e model.Edge) error {
	evidence := CompactEvidence(e.Evidence())
	evJSON, err := json.Marshal(evidence)
	if err != nil {
		return fmt.Errorf("graphstore: encode evidence: %w", err)
	}
	// Intern the repetitive reason string; the edge row stores its dictionary id.
	// Evidence stays inline (per-edge-unique). Edges are never full-text indexed.
	reasonID, err := internStringTx(ctx, tx, e.Reason())
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO edges (id, from_id, to_id, kind, confidence_tier, confidence, reason_id, evidence)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	from_id=excluded.from_id, to_id=excluded.to_id, kind=excluded.kind,
	confidence_tier=excluded.confidence_tier, confidence=excluded.confidence,
	reason_id=excluded.reason_id, evidence=excluded.evidence`,
		string(e.ID()), string(e.From()), string(e.To()), e.Kind(),
		string(e.Tier()), e.Confidence(), reasonID, string(evJSON)); err != nil {
		return fmt.Errorf("graphstore: upsert edge: %w", err)
	}
	return nil
}

// CompactEvidence deduplicates, deterministically sorts, and bounds the
// evidence slice. It returns a nil slice for empty input so the JSON encoding
// stays a consistent empty array.
func CompactEvidence(ev []string) []string {
	if len(ev) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ev))
	for _, s := range ev {
		seen[s] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	const evidenceCap = 64
	if len(out) > evidenceCap {
		out = out[:evidenceCap]
	}
	return out
}

// ---- reads (cache-first, transparent rebuild from SQLite on miss) ----

// ensureCache returns the hot cache, rebuilding it from SQLite if evicted. The
// rebuild is authoritative-equivalent: it reads every durable row, so cache hits
// and rebuilds are indistinguishable to callers.
func (s *SQLiteStore) ensureCache(ctx context.Context) (*memGraph, error) {
	s.cacheMu.RLock()
	c := s.cache
	s.cacheMu.RUnlock()
	if c != nil {
		return c, nil
	}

	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.cache != nil { // another goroutine rebuilt it
		return s.cache, nil
	}
	rebuilt, err := s.loadAllFromDB(ctx)
	if err != nil {
		return nil, err
	}
	s.cache = rebuilt
	s.rebuilds.Add(1)
	return rebuilt, nil
}

func (s *SQLiteStore) loadAllFromDB(ctx context.Context) (*memGraph, error) {
	g := &memGraph{
		nodes: make(map[model.NodeId]model.Node),
		edges: make(map[model.EdgeId]model.Edge),
	}
	nrows, err := s.db.QueryContext(ctx, "SELECT kind, qualified_name, source_path, line, col, meta FROM nodes")
	if err != nil {
		return nil, fmt.Errorf("graphstore: load nodes: %w", err)
	}
	defer nrows.Close()
	for nrows.Next() {
		var kind, qn, sp, metaJSON string
		var line, col int
		if err := nrows.Scan(&kind, &qn, &sp, &line, &col, &metaJSON); err != nil {
			return nil, fmt.Errorf("graphstore: scan node: %w", err)
		}
		n, err := model.NewNode(kind, qn, sp, line, col)
		if err != nil {
			return nil, fmt.Errorf("graphstore: reconstruct node: %w", err)
		}
		meta, err := decodeNodeMeta(metaJSON)
		if err != nil {
			return nil, err
		}
		if !meta.IsZero() {
			n = n.WithMeta(meta)
		}
		g.nodes[n.ID()] = n
	}
	if err := nrows.Err(); err != nil {
		return nil, fmt.Errorf("graphstore: iterate nodes: %w", err)
	}

	erows, err := s.db.QueryContext(ctx, `
SELECT e.from_id, e.to_id, e.kind, e.confidence_tier, e.confidence, r.text, e.evidence
FROM edges e
JOIN reasons r ON e.reason_id = r.id`)
	if err != nil {
		return nil, fmt.Errorf("graphstore: load edges: %w", err)
	}
	defer erows.Close()
	for erows.Next() {
		var from, to, kind, tier, reason, evJSON string
		var conf float64
		if err := erows.Scan(&from, &to, &kind, &tier, &conf, &reason, &evJSON); err != nil {
			return nil, fmt.Errorf("graphstore: scan edge: %w", err)
		}
		var evidence []string
		if err := json.Unmarshal([]byte(evJSON), &evidence); err != nil {
			return nil, fmt.Errorf("graphstore: decode evidence: %w", err)
		}
		e, err := model.NewEdge(model.NodeId(from), model.NodeId(to), kind,
			model.ConfidenceTier(tier), conf, reason, evidence)
		if err != nil {
			return nil, fmt.Errorf("graphstore: reconstruct edge: %w", err)
		}
		g.edges[e.ID()] = e
	}
	if err := erows.Err(); err != nil {
		return nil, fmt.Errorf("graphstore: iterate edges: %w", err)
	}
	return g, nil
}

func (s *SQLiteStore) GetNode(ctx context.Context, id model.NodeId) (model.Node, error) {
	if s.closed.Load() {
		return model.Node{}, ErrClosed
	}
	c, err := s.ensureCache(ctx)
	if err != nil {
		return model.Node{}, err
	}
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	n, ok := c.nodes[id]
	if !ok {
		return model.Node{}, ErrNotFound
	}
	return n, nil
}

func (s *SQLiteStore) GetEdge(ctx context.Context, id model.EdgeId) (model.Edge, error) {
	if s.closed.Load() {
		return model.Edge{}, ErrClosed
	}
	c, err := s.ensureCache(ctx)
	if err != nil {
		return model.Edge{}, err
	}
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	e, ok := c.edges[id]
	if !ok {
		return model.Edge{}, ErrNotFound
	}
	return e, nil
}

func (s *SQLiteStore) Nodes(ctx context.Context, q Query) ([]model.Node, error) {
	if s.closed.Load() {
		return nil, ErrClosed
	}
	var textHits map[model.NodeId]struct{}
	if q.Text != "" {
		var err error
		textHits, err = s.ftsNodeIDs(ctx, q.Text)
		if err != nil {
			return nil, err
		}
	}
	c, err := s.ensureCache(ctx)
	if err != nil {
		return nil, err
	}
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	out := make([]model.Node, 0, len(c.nodes))
	for id, n := range c.nodes {
		if q.NodeKind != "" && n.Kind() != q.NodeKind {
			continue
		}
		if textHits != nil {
			if _, ok := textHits[id]; !ok {
				continue
			}
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}

func (s *SQLiteStore) Edges(ctx context.Context, q Query) ([]model.Edge, error) {
	if s.closed.Load() {
		return nil, ErrClosed
	}
	c, err := s.ensureCache(ctx)
	if err != nil {
		return nil, err
	}
	// Edge reason is no longer full-text indexed; a Text query filters by
	// case-insensitive substring over the hot cache's reason, matching MemStore
	// exactly so both backends stay contract-identical.
	needle := strings.ToLower(q.Text)
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	out := make([]model.Edge, 0, len(c.edges))
	for _, e := range c.edges {
		if q.EdgeKind != "" && e.Kind() != q.EdgeKind {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(e.Reason()), needle) {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}

// ftsNodeIDs runs an FTS5 MATCH (bound parameter) and returns matching node IDs.
func (s *SQLiteStore) ftsNodeIDs(ctx context.Context, text string) (map[model.NodeId]struct{}, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT owner_id FROM search WHERE owner_kind='node' AND text MATCH ?", ftsQuery(text))
	if err != nil {
		return nil, fmt.Errorf("graphstore: fts5 node search: %w", err)
	}
	defer rows.Close()
	out := make(map[model.NodeId]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("graphstore: scan fts5 node hit: %w", err)
		}
		out[model.NodeId(id)] = struct{}{}
	}
	return out, rows.Err()
}

// ftsQuery turns free user text into a safe FTS5 prefix query. The whole value is
// still passed as a bound parameter; quoting each token as a string protects
// against FTS5 query-syntax injection while keeping substring/prefix semantics.
// SearchNodes runs an FTS5 MATCH query over the searchable node text,
// returning ranked hits joined back to the nodes table. Results are ordered by
// bm25 rank ascending (better matches first), then by qualified_name and node
// id for deterministic tie-breaking. The query is parameterized; user input is
// never concatenated into SQL.
func (s *SQLiteStore) SearchNodes(ctx context.Context, text string, limit int) ([]RankedNode, error) {
	if s.closed.Load() {
		return nil, ErrClosed
	}
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}

	// rank MATCHes by bm25, then deterministic tie-break.
	q := `
SELECT n.kind, n.qualified_name, n.source_path, n.line, n.col, n.meta, rank
FROM search s JOIN nodes n ON s.owner_id = n.id
WHERE s.owner_kind = 'node' AND s.text MATCH ?
ORDER BY rank ASC, n.qualified_name ASC, n.id ASC`
	args := []any{ftsQuery(text)}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("graphstore: fts5 search nodes: %w", err)
	}
	defer rows.Close()

	out := make([]RankedNode, 0, 64)
	for rows.Next() {
		var kind, qn, sp, metaJSON string
		var line, col int
		var rank float64
		if err := rows.Scan(&kind, &qn, &sp, &line, &col, &metaJSON, &rank); err != nil {
			return nil, fmt.Errorf("graphstore: scan ranked node: %w", err)
		}
		n, err := model.NewNode(kind, qn, sp, line, col)
		if err != nil {
			return nil, fmt.Errorf("graphstore: reconstruct ranked node: %w", err)
		}
		meta, err := decodeNodeMeta(metaJSON)
		if err != nil {
			return nil, err
		}
		if !meta.IsZero() {
			n = n.WithMeta(meta)
		}
		out = append(out, RankedNode{Node: n, Rank: rank})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graphstore: iterate ranked nodes: %w", err)
	}
	return out, nil
}

func ftsQuery(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return `""`
	}
	quoted := make([]string, 0, len(fields))
	for _, f := range fields {
		// Double quotes inside an FTS5 string token are escaped by doubling.
		f = strings.ReplaceAll(f, `"`, `""`)
		quoted = append(quoted, `"`+f+`"*`)
	}
	return strings.Join(quoted, " ")
}

// ---- cache eviction ----

func (s *SQLiteStore) EvictCache(ctx context.Context) error {
	if s.closed.Load() {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.evict()
	return nil
}

func (s *SQLiteStore) evict() {
	s.cacheMu.Lock()
	s.cache = nil
	s.cacheMu.Unlock()
}

// ---- snapshot / load ----

func (s *SQLiteStore) Snapshot(ctx context.Context, path string) error {
	if s.closed.Load() {
		return ErrClosed
	}
	safe, err := safeSnapshotPath(path)
	if err != nil {
		return err
	}
	// Snapshot reads durable state directly (source of truth), independent of
	// cache occupancy, so snapshots are deterministic regardless of cache state.
	g, err := s.loadAllFromDB(ctx)
	if err != nil {
		return err
	}
	nodes := make([]model.Node, 0, len(g.nodes))
	for _, n := range g.nodes {
		nodes = append(nodes, n)
	}
	edges := make([]model.Edge, 0, len(g.edges))
	for _, e := range g.edges {
		edges = append(edges, e)
	}
	data, err := encodeSnapshot(nodes, edges)
	if err != nil {
		return err
	}
	return writeFileAtomic(safe, data)
}

// Load is fail-closed and atomic: it validates the snapshot fully, then replaces
// the durable contents in a single transaction and re-derives the FTS5 index. On
// any error the store is left unmodified. The cache is evicted so the next read
// reflects the loaded state.
func (s *SQLiteStore) Load(ctx context.Context, path string) error {
	if s.closed.Load() {
		return ErrClosed
	}
	safe, err := safeSnapshotPath(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(safe) //nolint:gosec // path sanitized by safeSnapshotPath
	if err != nil {
		return err
	}
	g, err := decodeSnapshot(data) // full validation BEFORE any mutation
	if err != nil {
		return err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("graphstore: load begin tx: %w", err)
	}
	rollback := func() { _ = tx.Rollback() }

	// Replace-into-fresh semantics: clear everything, then re-insert. The whole
	// thing is one transaction, so a failure rolls back to the prior state.
	for _, stmt := range []string{"DELETE FROM search", "DELETE FROM edges", "DELETE FROM reasons", "DELETE FROM nodes"} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			rollback()
			return fmt.Errorf("graphstore: load clear (%s): %w", stmt, err)
		}
	}
	// Nodes first (FK targets), then edges. FTS5 re-derived inside the upserts.
	for _, n := range g.Nodes() {
		if err := upsertNodeTx(ctx, tx, n); err != nil {
			rollback()
			return err
		}
	}
	for _, e := range g.Edges() {
		if err := upsertEdgeTx(ctx, tx, e); err != nil {
			rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("graphstore: load commit: %w", err)
	}
	s.evict() // next read rebuilds from the freshly loaded durable state
	return nil
}

// SetMetadata stores a durable key/value pair in the kv_meta table.
func (s *SQLiteStore) SetMetadata(ctx context.Context, key, value string) error {
	if s.closed.Load() {
		return ErrClosed
	}
	if strings.TrimSpace(key) == "" {
		return errors.New("graphstore: empty metadata key")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if _, err := s.db.ExecContext(ctx,
		"INSERT INTO kv_meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
		key, value); err != nil {
		return fmt.Errorf("graphstore: set metadata: %w", err)
	}
	return nil
}

// Metadata returns the value for key, or ErrNotFound if it does not exist.
func (s *SQLiteStore) Metadata(ctx context.Context, key string) (string, error) {
	if s.closed.Load() {
		return "", ErrClosed
	}
	var value string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM kv_meta WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("graphstore: get metadata: %w", err)
	}
	return value, nil
}

// WALCheckpoint runs a SQLite WAL checkpoint. mode is passed to PRAGMA
// wal_checkpoint(mode) and must be one of the SQLite checkpoint modes
// (PASSIVE, FULL, RESTART, TRUNCATE); anything else is rejected so no
// unvalidated string ever reaches the pragma. Use "TRUNCATE" to fold the WAL
// back into the main DB.
func (s *SQLiteStore) WALCheckpoint(ctx context.Context, mode string) error {
	if s.closed.Load() {
		return ErrClosed
	}
	if mode == "" {
		mode = "TRUNCATE"
	}
	mode = strings.ToUpper(mode)
	switch mode {
	case "PASSIVE", "FULL", "RESTART", "TRUNCATE":
	default:
		return fmt.Errorf("graphstore: invalid wal_checkpoint mode %q", mode)
	}
	_, err := s.db.ExecContext(ctx, fmt.Sprintf("PRAGMA wal_checkpoint(%s)", mode))
	if err != nil {
		return fmt.Errorf("graphstore: wal_checkpoint(%s): %w", mode, err)
	}
	return nil
}

func (s *SQLiteStore) Close() error {
	if s.closed.Swap(true) {
		return nil
	}
	s.evict()
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("graphstore: close db: %w", err)
	}
	return nil
}
