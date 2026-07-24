package embed

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"path/filepath"
	"sort"

	"github.com/samibel/graphi/core/model"

	_ "modernc.org/sqlite" // CGo-free SQLite driver for the durable vectors sidecar
)

// SQLiteVectorTable is the durable, SQLite-backed VectorTable (the SW-061 "sidecar
// `vectors` table"). It satisfies the VectorTable seam from a dedicated sidecar
// table — graphstore is NOT extended with a vectors column (architect A1).
//
// Each row records a NodeId, its vector bytes, the embedder identity that produced
// it, and the vector dimension. The (embedderID, dim) scope is the invalidation
// key: Load returns ONLY the rows produced by the currently active embedder of the
// expected dimension, so a CHANGED or ABSENT embedder transparently invalidates
// stale vectors rather than silently mixing embedding spaces (story AC: "durable
// persistence + reload"). Upsert is keyed by (node_id, embedder_id) so re-indexing
// with the same embedder replaces in place and a second embedder's vectors coexist
// durably without clobbering the first.
//
// The table lives in the ingest meta sidecar (the -meta DB), the same DB that
// already holds file_content_cache / reverse_deps / dirty_units / edit_provenance.
// It is created idempotently (CREATE TABLE IF NOT EXISTS) and uses only the
// CGo-free modernc.org/sqlite driver, so it never perturbs the CGo-free gate. It
// performs NO network I/O — it is a pure local read/write.
type SQLiteVectorTable struct {
	db         *sql.DB
	ownDB      bool // true when this table opened db itself (Close releases it)
	embedderID string
	dim        int
}

// vectorsDDL is the idempotent sidecar schema. The vector is stored as a BLOB of
// big-endian float32 components (fixed-endianness so a persisted index is portable
// across architectures, mirroring the model identity pre-image discipline).
const vectorsDDL = `
CREATE TABLE IF NOT EXISTS vectors (
	node_id     TEXT NOT NULL,
	embedder_id TEXT NOT NULL,
	dim         INTEGER NOT NULL,
	vec         BLOB NOT NULL,
	PRIMARY KEY (embedder_id, node_id)
);`

// NewSQLiteVectorTableDB constructs a durable vector table over an EXISTING meta
// DB handle (e.g. the ingest Ingester's MetaDB()). It owns only the `vectors`
// table; the caller owns the DB handle's lifecycle (Close on this table is a
// no-op). embedderID and dim scope every Upsert/Load to a single embedding space.
func NewSQLiteVectorTableDB(ctx context.Context, db *sql.DB, embedderID string, dim int) (*SQLiteVectorTable, error) {
	if db == nil {
		return nil, fmt.Errorf("embed: nil meta db")
	}
	if _, err := db.ExecContext(ctx, vectorsDDL); err != nil {
		return nil, fmt.Errorf("embed: init vectors table: %w", err)
	}
	return &SQLiteVectorTable{db: db, ownDB: false, embedderID: embedderID, dim: dim}, nil
}

// OpenSQLiteVectorTable opens the durable vector table from a meta DIRECTORY (the
// -meta sidecar dir), opening its OWN read handle on the same ingest-meta.db. It
// is the reload-at-startup entry point: a fresh process can rebuild the in-memory
// index from durable storage WITHOUT an Ingester and WITHOUT re-embedding (the
// reload is a pure local read). Close releases the opened handle.
//
// embedderID/dim scope the read to the active embedding space; a different active
// embedder (or none) reads zero rows, so stale vectors never leak across spaces.
func OpenSQLiteVectorTable(ctx context.Context, metaDir, embedderID string, dim int) (*SQLiteVectorTable, error) {
	if metaDir == "" {
		return nil, fmt.Errorf("embed: empty meta dir")
	}
	dbPath := filepath.Join(metaDir, "ingest-meta.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("embed: open meta db: %w", err)
	}
	if _, err := db.ExecContext(ctx, vectorsDDL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("embed: init vectors table: %w", err)
	}
	return &SQLiteVectorTable{db: db, ownDB: true, embedderID: embedderID, dim: dim}, nil
}

// Upsert implements VectorTable. It durably stores (or replaces) the vector for
// v.NodeID under this table's (embedder_id) scope.
func (t *SQLiteVectorTable) Upsert(ctx context.Context, v Vector) error {
	blob := encodeVector(v.Values)
	_, err := t.db.ExecContext(ctx, `
INSERT INTO vectors (node_id, embedder_id, dim, vec) VALUES (?, ?, ?, ?)
ON CONFLICT(embedder_id, node_id) DO UPDATE SET
	dim=excluded.dim,
	vec=excluded.vec`,
		string(v.NodeID), t.embedderID, len(v.Values), blob)
	if err != nil {
		return fmt.Errorf("embed: upsert vector: %w", err)
	}
	return nil
}

// Load implements VectorTable. It returns every stored vector FOR THIS EMBEDDING
// SPACE (embedder_id + dim), in canonical NodeId order. Rows produced by a
// different embedder — or of a different dimension — are excluded, so a changed or
// absent embedder yields zero stale vectors (the invalidation contract).
func (t *SQLiteVectorTable) Load(ctx context.Context) ([]Vector, error) {
	// The invalidation key is the embedder identity; the dimension is a secondary
	// guard. Some embedders (e.g. Ollama) discover Dim() only after the first Embed,
	// so Dim() can legitimately be 0 at reload-construction time. When the expected
	// dim is unknown (<= 0) we scope on embedder_id alone (every row this embedder
	// wrote shares one dimension by construction); when it is known we additionally
	// require it to match, so a model whose dimension changed loads zero stale rows.
	query := "SELECT node_id, vec FROM vectors WHERE embedder_id = ? ORDER BY node_id"
	queryArgs := []any{t.embedderID}
	if t.dim > 0 {
		query = "SELECT node_id, vec FROM vectors WHERE embedder_id = ? AND dim = ? ORDER BY node_id"
		queryArgs = []any{t.embedderID, t.dim}
	}
	rows, err := t.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("embed: load vectors: %w", err)
	}
	defer rows.Close()
	var out []Vector
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, fmt.Errorf("embed: scan vector: %w", err)
		}
		out = append(out, Vector{NodeID: model.NodeId(id), Values: decodeVector(blob)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// The SQL ORDER BY already yields canonical order; re-establish defensively.
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out, nil
}

// Close releases the DB handle when this table opened it (OpenSQLiteVectorTable).
// When constructed over a borrowed handle (NewSQLiteVectorTableDB) it is a no-op.
func (t *SQLiteVectorTable) Close() error {
	if t.ownDB && t.db != nil {
		return t.db.Close()
	}
	return nil
}

// encodeVector serializes a float32 slice to a fixed-endianness (big-endian) BLOB
// so a persisted vector is byte-identical and portable across architectures.
func encodeVector(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.BigEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}

// decodeVector reverses encodeVector. A trailing partial component (corrupt row)
// is ignored rather than panicking.
func decodeVector(b []byte) []float32 {
	n := len(b) / 4
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		out[i] = math.Float32frombits(binary.BigEndian.Uint32(b[i*4:]))
	}
	return out
}
