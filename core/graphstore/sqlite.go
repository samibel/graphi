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
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-64000)&_pragma=temp_store(MEMORY)",
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
	return s, nil
}

// SQLiteFactory is a Factory for the durable backend. It places the database file
// inside dir.
func SQLiteFactory(dir string) (Graphstore, error) {
	return OpenSQLite(filepath.Join(dir, "graphi.db"))
}

// initSchema creates the base tables and the FTS5 index. Evidence is stored as a
// JSON array so the null/empty/populated distinction round-trips exactly. The
// FTS5 virtual table covers node name (qualified_name) and edge reason — the
// searchable text fields named in the ACs.
func (s *SQLiteStore) initSchema(ctx context.Context) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS nodes (
	id             TEXT PRIMARY KEY,
	kind           TEXT NOT NULL,
	qualified_name TEXT NOT NULL,
	source_path    TEXT NOT NULL,
	line           INTEGER NOT NULL,
	col            INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS edges (
	id              TEXT PRIMARY KEY,
	from_id         TEXT NOT NULL,
	to_id           TEXT NOT NULL,
	kind            TEXT NOT NULL,
	confidence_tier TEXT NOT NULL,
	confidence      REAL NOT NULL,
	reason          TEXT NOT NULL,
	evidence        TEXT NOT NULL,
	FOREIGN KEY(from_id) REFERENCES nodes(id),
	FOREIGN KEY(to_id)   REFERENCES nodes(id)
);
CREATE VIRTUAL TABLE IF NOT EXISTS search USING fts5(
	owner_kind UNINDEXED,  -- 'node' or 'edge'
	owner_id   UNINDEXED,
	text
);
-- Endpoint indexes: DeleteNode's incident-edge cascade and incidentEdgeIDs
-- filter on from_id/to_id; without these every node delete full-scans the
-- edge table (and the FK enforcement lacks its child index). Content-neutral:
-- listings stay ordered by id, so graph bytes are unaffected.
CREATE INDEX IF NOT EXISTS edges_from_id ON edges(from_id);
CREATE INDEX IF NOT EXISTS edges_to_id   ON edges(to_id);
`
	if _, err := s.db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("graphstore: init schema (FTS5 may be unavailable): %w", err)
	}
	return nil
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
// transaction (durable FIRST), then updates the cache. The FTS5 rows for the
// node and the cascaded edges are removed in the same transaction so the search
// index never references a deleted owner. Idempotent: a missing node deletes
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
	// Edges first (they FK-reference the node), then the node; FTS rows alongside.
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM search WHERE owner_kind='edge' AND owner_id IN (SELECT id FROM edges WHERE from_id=? OR to_id=?)",
		string(id), string(id)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("graphstore: delete node fts edges: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM edges WHERE from_id=? OR to_id=?", string(id), string(id)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("graphstore: delete incident edges: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM search WHERE owner_kind='node' AND owner_id=?", string(id)); err != nil {
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
	if _, err := tx.ExecContext(ctx, "DELETE FROM search WHERE owner_kind='edge' AND owner_id=?", string(id)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("graphstore: delete edge fts: %w", err)
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
	if _, err := tx.ExecContext(ctx, `
INSERT INTO nodes (id, kind, qualified_name, source_path, line, col)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	kind=excluded.kind, qualified_name=excluded.qualified_name,
	source_path=excluded.source_path, line=excluded.line, col=excluded.col`,
		string(n.ID()), n.Kind(), n.QualifiedName(), n.SourcePath(), n.Line(), n.Column()); err != nil {
		return fmt.Errorf("graphstore: upsert node: %w", err)
	}
	// Refresh FTS row for this node (delete-then-insert keeps it idempotent).
	if _, err := tx.ExecContext(ctx, "DELETE FROM search WHERE owner_kind='node' AND owner_id=?", string(n.ID())); err != nil {
		return fmt.Errorf("graphstore: fts5 clear node: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO search (owner_kind, owner_id, text) VALUES ('node', ?, ?)",
		string(n.ID()), n.QualifiedName()); err != nil {
		return fmt.Errorf("graphstore: fts5 index node: %w", err)
	}
	return nil
}

func upsertEdgeTx(ctx context.Context, tx *sql.Tx, e model.Edge) error {
	evJSON, err := json.Marshal(e.Evidence())
	if err != nil {
		return fmt.Errorf("graphstore: encode evidence: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO edges (id, from_id, to_id, kind, confidence_tier, confidence, reason, evidence)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	from_id=excluded.from_id, to_id=excluded.to_id, kind=excluded.kind,
	confidence_tier=excluded.confidence_tier, confidence=excluded.confidence,
	reason=excluded.reason, evidence=excluded.evidence`,
		string(e.ID()), string(e.From()), string(e.To()), e.Kind(),
		string(e.Tier()), e.Confidence(), e.Reason(), string(evJSON)); err != nil {
		return fmt.Errorf("graphstore: upsert edge: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM search WHERE owner_kind='edge' AND owner_id=?", string(e.ID())); err != nil {
		return fmt.Errorf("graphstore: fts5 clear edge: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO search (owner_kind, owner_id, text) VALUES ('edge', ?, ?)",
		string(e.ID()), e.Reason()); err != nil {
		return fmt.Errorf("graphstore: fts5 index edge: %w", err)
	}
	return nil
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
	nrows, err := s.db.QueryContext(ctx, "SELECT kind, qualified_name, source_path, line, col FROM nodes")
	if err != nil {
		return nil, fmt.Errorf("graphstore: load nodes: %w", err)
	}
	defer nrows.Close()
	for nrows.Next() {
		var kind, qn, sp string
		var line, col int
		if err := nrows.Scan(&kind, &qn, &sp, &line, &col); err != nil {
			return nil, fmt.Errorf("graphstore: scan node: %w", err)
		}
		n, err := model.NewNode(kind, qn, sp, line, col)
		if err != nil {
			return nil, fmt.Errorf("graphstore: reconstruct node: %w", err)
		}
		g.nodes[n.ID()] = n
	}
	if err := nrows.Err(); err != nil {
		return nil, fmt.Errorf("graphstore: iterate nodes: %w", err)
	}

	erows, err := s.db.QueryContext(ctx, "SELECT from_id, to_id, kind, confidence_tier, confidence, reason, evidence FROM edges")
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
	var textHits map[model.EdgeId]struct{}
	if q.Text != "" {
		var err error
		textHits, err = s.ftsEdgeIDs(ctx, q.Text)
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
	out := make([]model.Edge, 0, len(c.edges))
	for id, e := range c.edges {
		if q.EdgeKind != "" && e.Kind() != q.EdgeKind {
			continue
		}
		if textHits != nil {
			if _, ok := textHits[id]; !ok {
				continue
			}
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

func (s *SQLiteStore) ftsEdgeIDs(ctx context.Context, text string) (map[model.EdgeId]struct{}, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT owner_id FROM search WHERE owner_kind='edge' AND text MATCH ?", ftsQuery(text))
	if err != nil {
		return nil, fmt.Errorf("graphstore: fts5 edge search: %w", err)
	}
	defer rows.Close()
	out := make(map[model.EdgeId]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("graphstore: scan fts5 edge hit: %w", err)
		}
		out[model.EdgeId(id)] = struct{}{}
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
SELECT n.kind, n.qualified_name, n.source_path, n.line, n.col, rank
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
		var kind, qn, sp string
		var line, col int
		var rank float64
		if err := rows.Scan(&kind, &qn, &sp, &line, &col, &rank); err != nil {
			return nil, fmt.Errorf("graphstore: scan ranked node: %w", err)
		}
		n, err := model.NewNode(kind, qn, sp, line, col)
		if err != nil {
			return nil, fmt.Errorf("graphstore: reconstruct ranked node: %w", err)
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
	for _, stmt := range []string{"DELETE FROM search", "DELETE FROM edges", "DELETE FROM nodes"} {
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
