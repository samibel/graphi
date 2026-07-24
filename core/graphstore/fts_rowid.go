package graphstore

import (
	"fmt"
	"strconv"

	"github.com/samibel/graphi/core/model"
)

// ftsNodeRowid maps a NodeId to its deterministic FTS rowid: the id IS a
// fixed-width 16-char lowercase-hex xxhash64 (core/model, pinned by its
// tests), so its integer value is a bijective 64-bit rowid. Keying the
// `search` table this way lets every FTS delete/replace be rowid-keyed with
// no lookup state at all: owner_id is UNINDEXED, so the owner-keyed DELETE
// would be a full FTS-table scan per row, and the previous per-batch
// rowid map re-scanned the whole search table at every BeginBatch.
//
// The value deliberately round-trips through the unsigned parse: ids above
// 0x7fff... map to negative rowids, which SQLite (and FTS5) accept as
// ordinary 64-bit rowids; uniqueness is what matters, not sign. A malformed
// id fails closed rather than hashing to some other row.
func ftsNodeRowid(id model.NodeId) (int64, error) {
	if len(id) != 16 {
		return 0, fmt.Errorf("graphstore: malformed node id %q for fts rowid", id)
	}
	h, err := strconv.ParseUint(string(id), 16, 64)
	if err != nil {
		return 0, fmt.Errorf("graphstore: malformed node id %q for fts rowid: %w", id, err)
	}
	return int64(h), nil
}
