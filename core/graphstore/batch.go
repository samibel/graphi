package graphstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/samibel/graphi/core/model"
)

// BeginBatch opens a native batched write session: one SQLite transaction,
// statements prepared once, the single-writer mutex held for the session's
// lifetime. Per-row cost stays flat without seeding any whole-graph state at
// open (three batches per ingest pass each paid O(all nodes + all FTS rows)
// at BeginBatch — even a one-file watcher commit):
//
//   - exists memoizes POSITIVE endpoint knowledge only: batch-local puts plus
//     committed nodes verified by an indexed point probe inside the tx (which
//     therefore also observes this batch's deletes), so PutEdge's endpoint
//     check (ErrUnknownEdgeEndpoint) costs at most one probe per distinct
//     endpoint and sees nodes put earlier in the SAME batch.
//   - FTS rowids are deterministic (ftsNodeRowid: the NodeId's hex value), so
//     delete/insert are rowid-keyed with no per-batch rowid map. The `search`
//     table keys its rows by an UNINDEXED owner_id — `DELETE ... WHERE
//     owner_id=?` would be a full-table scan per row, O(N²) on a cold index.
//
// On Commit the hot cache is evicted rather than replayed: the next read
// rebuilds it from SQLite in one scan, which is provably consistent and costs
// ~one rebuild per batch.
func (s *SQLiteStore) BeginBatch(ctx context.Context) (Batch, error) {
	if s.closed.Load() {
		return nil, ErrClosed
	}
	s.writeMu.Lock()
	b := &sqliteBatch{s: s}
	if err := b.init(ctx); err != nil {
		b.close()
		s.writeMu.Unlock()
		return nil, err
	}
	return b, nil
}

// sqliteBatch is the SQLiteStore Batch. It owns writeMu until done.
type sqliteBatch struct {
	s      *SQLiteStore
	tx     *sql.Tx
	done   bool
	exists map[model.NodeId]struct{} // positive endpoint memo: puts + verified probes

	// internCache memoizes reason/evidence text → dictionary id within the batch
	// tx, so the repetitive provenance strings that dominate a cold index intern
	// with a single round-trip each instead of one per edge.
	internCache map[string]int64

	stmtNodeUpsert   *sql.Stmt
	stmtEdgeUpsert   *sql.Stmt
	stmtReasonIns    *sql.Stmt // INSERT OR IGNORE INTO reasons(text) VALUES(?)
	stmtReasonSel    *sql.Stmt // SELECT id FROM reasons WHERE text=?
	stmtNodeExists   *sql.Stmt // SELECT 1 FROM nodes WHERE id=?
	stmtFTSDelete    *sql.Stmt // by rowid
	stmtFTSInsert    *sql.Stmt // explicit deterministic rowid
	stmtNodeDelete   *sql.Stmt
	stmtEdgeDelete   *sql.Stmt
	stmtEdgesDelEndp *sql.Stmt // DELETE FROM edges WHERE from_id=? OR to_id=?
}

func (b *sqliteBatch) init(ctx context.Context) error {
	tx, err := b.s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("graphstore: begin batch tx: %w", err)
	}
	b.tx = tx

	prep := func(q string) (*sql.Stmt, error) { return tx.PrepareContext(ctx, q) }
	if b.stmtNodeUpsert, err = prep(`
INSERT INTO nodes (id, kind, qualified_name, source_path, line, col, meta)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	kind=excluded.kind, qualified_name=excluded.qualified_name,
	source_path=excluded.source_path, line=excluded.line, col=excluded.col,
	meta=excluded.meta`); err != nil {
		return fmt.Errorf("graphstore: prepare node upsert: %w", err)
	}
	if b.stmtEdgeUpsert, err = prep(`
INSERT INTO edges (id, from_id, to_id, kind, confidence_tier, confidence, reason_id, evidence)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	from_id=excluded.from_id, to_id=excluded.to_id, kind=excluded.kind,
	confidence_tier=excluded.confidence_tier, confidence=excluded.confidence,
	reason_id=excluded.reason_id, evidence=excluded.evidence`); err != nil {
		return fmt.Errorf("graphstore: prepare edge upsert: %w", err)
	}
	if b.stmtReasonIns, err = prep("INSERT OR IGNORE INTO reasons (text) VALUES (?)"); err != nil {
		return fmt.Errorf("graphstore: prepare reason insert: %w", err)
	}
	if b.stmtReasonSel, err = prep("SELECT id FROM reasons WHERE text = ?"); err != nil {
		return fmt.Errorf("graphstore: prepare reason select: %w", err)
	}
	b.internCache = make(map[string]int64)
	if b.stmtNodeExists, err = prep("SELECT 1 FROM nodes WHERE id=?"); err != nil {
		return fmt.Errorf("graphstore: prepare node exists: %w", err)
	}
	if b.stmtFTSDelete, err = prep("DELETE FROM search WHERE rowid=?"); err != nil {
		return fmt.Errorf("graphstore: prepare fts delete: %w", err)
	}
	if b.stmtFTSInsert, err = prep("INSERT INTO search (rowid, owner_kind, owner_id, text) VALUES (?, ?, ?, ?)"); err != nil {
		return fmt.Errorf("graphstore: prepare fts insert: %w", err)
	}
	if b.stmtNodeDelete, err = prep("DELETE FROM nodes WHERE id=?"); err != nil {
		return fmt.Errorf("graphstore: prepare node delete: %w", err)
	}
	if b.stmtEdgeDelete, err = prep("DELETE FROM edges WHERE id=?"); err != nil {
		return fmt.Errorf("graphstore: prepare edge delete: %w", err)
	}
	if b.stmtEdgesDelEndp, err = prep("DELETE FROM edges WHERE from_id=? OR to_id=?"); err != nil {
		return fmt.Errorf("graphstore: prepare incident delete: %w", err)
	}

	// The endpoint memo starts empty: committed nodes are verified lazily by
	// endpointExists' indexed probe, batch-local puts extend it directly.
	b.exists = make(map[model.NodeId]struct{})
	return nil
}

// endpointExists reports whether id is a live node endpoint: batch-local puts
// and previously verified ids answer from the memo; otherwise one indexed
// point probe inside the tx decides (and a hit is memoized). Probing the tx —
// not the committed snapshot — means an id deleted earlier in this batch
// correctly reads as gone (DeleteNode also evicts it from the memo).
func (b *sqliteBatch) endpointExists(ctx context.Context, id model.NodeId) (bool, error) {
	if _, ok := b.exists[id]; ok {
		return true, nil
	}
	var one int
	err := b.stmtNodeExists.QueryRowContext(ctx, string(id)).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("graphstore: check endpoint: %w", err)
	}
	b.exists[id] = struct{}{}
	return true, nil
}

// refreshFTS replaces the owner's FTS row: deterministic-rowid delete of the
// previous row (a no-op when absent) + insert under the same rowid.
func (b *sqliteBatch) refreshFTS(ctx context.Context, kind, id, text string) error {
	rowid, err := ftsNodeRowid(model.NodeId(id))
	if err != nil {
		return err
	}
	if _, err := b.stmtFTSDelete.ExecContext(ctx, rowid); err != nil {
		return fmt.Errorf("graphstore: fts5 clear %s: %w", kind, err)
	}
	if _, err := b.stmtFTSInsert.ExecContext(ctx, rowid, kind, id, text); err != nil {
		return fmt.Errorf("graphstore: fts5 index %s: %w", kind, err)
	}
	return nil
}

// deleteFTS removes the owner's FTS row if present (deterministic rowid; the
// delete is an idempotent no-op when no row exists).
func (b *sqliteBatch) deleteFTS(ctx context.Context, kind, id string) error {
	rowid, err := ftsNodeRowid(model.NodeId(id))
	if err != nil {
		return err
	}
	if _, err := b.stmtFTSDelete.ExecContext(ctx, rowid); err != nil {
		return fmt.Errorf("graphstore: fts5 delete %s: %w", kind, err)
	}
	return nil
}

func (b *sqliteBatch) PutNode(ctx context.Context, n model.Node) error {
	if b.done {
		return ErrClosed
	}
	metaJSON, err := encodeNodeMeta(n.Meta())
	if err != nil {
		return err
	}
	if _, err := b.stmtNodeUpsert.ExecContext(ctx,
		string(n.ID()), n.Kind(), n.QualifiedName(), n.SourcePath(), n.Line(), n.Column(), metaJSON); err != nil {
		return fmt.Errorf("graphstore: upsert node: %w", err)
	}
	if err := b.refreshFTS(ctx, "node", string(n.ID()), n.QualifiedName()); err != nil {
		return err
	}
	b.exists[n.ID()] = struct{}{}
	return nil
}

// intern get-or-inserts text into the `reasons` dictionary and returns its id,
// memoizing within the batch tx. Mirrors internStringTx on the single-write
// path but keyed through prepared statements + the batch-local cache.
func (b *sqliteBatch) intern(ctx context.Context, text string) (int64, error) {
	if id, ok := b.internCache[text]; ok {
		return id, nil
	}
	if _, err := b.stmtReasonIns.ExecContext(ctx, text); err != nil {
		return 0, fmt.Errorf("graphstore: intern string: %w", err)
	}
	var id int64
	if err := b.stmtReasonSel.QueryRowContext(ctx, text).Scan(&id); err != nil {
		return 0, fmt.Errorf("graphstore: intern lookup: %w", err)
	}
	b.internCache[text] = id
	return id, nil
}

func (b *sqliteBatch) PutEdge(ctx context.Context, e model.Edge) error {
	if b.done {
		return ErrClosed
	}
	for _, endpoint := range [2]model.NodeId{e.From(), e.To()} {
		ok, err := b.endpointExists(ctx, endpoint)
		if err != nil {
			return err
		}
		if !ok {
			return ErrUnknownEdgeEndpoint
		}
	}
	evJSON, err := json.Marshal(e.Evidence())
	if err != nil {
		return fmt.Errorf("graphstore: encode evidence: %w", err)
	}
	reasonID, err := b.intern(ctx, e.Reason())
	if err != nil {
		return err
	}
	if _, err := b.stmtEdgeUpsert.ExecContext(ctx,
		string(e.ID()), string(e.From()), string(e.To()), e.Kind(),
		string(e.Tier()), e.Confidence(), reasonID, string(evJSON)); err != nil {
		return fmt.Errorf("graphstore: upsert edge: %w", err)
	}
	// Edges are not full-text indexed (nodes only); no FTS refresh here.
	return nil
}

func (b *sqliteBatch) DeleteNode(ctx context.Context, id model.NodeId) error {
	if b.done {
		return ErrClosed
	}
	// Cascade exactly like the store's DeleteNode: incident edges (including
	// batch-local ones), then the node with its FTS row. Edges carry no FTS rows,
	// so only the node's FTS row is cleaned up. Idempotent for missing nodes.
	if _, err := b.stmtEdgesDelEndp.ExecContext(ctx, string(id), string(id)); err != nil {
		return fmt.Errorf("graphstore: delete incident edges: %w", err)
	}
	if err := b.deleteFTS(ctx, "node", string(id)); err != nil {
		return err
	}
	if _, err := b.stmtNodeDelete.ExecContext(ctx, string(id)); err != nil {
		return fmt.Errorf("graphstore: delete node: %w", err)
	}
	delete(b.exists, id)
	return nil
}

func (b *sqliteBatch) DeleteEdge(ctx context.Context, id model.EdgeId) error {
	if b.done {
		return ErrClosed
	}
	// Edges carry no FTS rows (nodes only), so there is no edge-FTS cleanup.
	if _, err := b.stmtEdgeDelete.ExecContext(ctx, string(id)); err != nil {
		return fmt.Errorf("graphstore: delete edge: %w", err)
	}
	return nil
}

// Commit makes the session durable, releases the single-writer lock, and
// evicts the hot cache (next read rebuilds consistently from SQLite). The
// fail-after-commit fault-injection hook fires exactly as on single writes.
func (b *sqliteBatch) Commit(ctx context.Context) error {
	if b.done {
		return ErrClosed
	}
	b.done = true
	defer b.s.writeMu.Unlock()
	defer b.close()
	if err := b.tx.Commit(); err != nil {
		return fmt.Errorf("graphstore: commit batch: %w", err)
	}
	// Durable state is complete; the cache is merely invalidated from here on.
	b.s.evict()
	if hookErr := b.s.takeFailHook(); hookErr != nil {
		return hookErr
	}
	return nil
}

// Rollback discards the session. No-op after Commit (defer-safe).
func (b *sqliteBatch) Rollback() error {
	if b.done {
		return nil
	}
	b.done = true
	defer b.s.writeMu.Unlock()
	defer b.close()
	if err := b.tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return fmt.Errorf("graphstore: rollback batch: %w", err)
	}
	return nil
}

// close releases the prepared statements (nil-safe; tx end releases them too,
// this just frees driver-side resources promptly).
func (b *sqliteBatch) close() {
	for _, st := range []*sql.Stmt{
		b.stmtNodeUpsert, b.stmtEdgeUpsert, b.stmtReasonIns, b.stmtReasonSel,
		b.stmtNodeExists, b.stmtFTSDelete, b.stmtFTSInsert, b.stmtNodeDelete,
		b.stmtEdgeDelete, b.stmtEdgesDelEndp,
	} {
		if st != nil {
			_ = st.Close()
		}
	}
}
