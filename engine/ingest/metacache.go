package ingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/link"
)

// cachedNodeIDs returns the node IDs previously produced for path.
func (i *Ingester) cachedNodeIDs(ctx context.Context, path string) ([]string, error) {
	var raw string
	err := i.meta.QueryRowContext(ctx, "SELECT node_ids FROM file_content_cache WHERE path = ?", path).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ingest: read cache: %w", err)
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, fmt.Errorf("ingest: decode node ids: %w", err)
	}
	return ids, nil
}

// rowQuerier is the QueryRowContext surface shared by *sql.DB and *sql.Tx, so the
// interned-node refcount check can run against either the live meta handle
// (commitParsed) or the open Phase-2 transaction (removeFileTx).
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// nodeReferencedByOtherFile reports whether any cached file OTHER than excludePath
// still lists node id in its node_ids. It underpins the WP-01 interned-package-node
// lifecycle: a package node shared by many files must persist as long as ≥1 file
// declares it, so full and incremental ingests stay byte-identical. The check is a
// strict no-op for per-file nodes — their NodeId embeds the unique source path, so
// no other cache row can reference the same id. node_ids is a JSON array of quoted
// id strings; the LIKE pattern matches the id delimited by its surrounding quotes
// (NodeIds are fixed-width hashes, so no id is a substring of another).
func (i *Ingester) nodeReferencedByOtherFile(ctx context.Context, q rowQuerier, excludePath, id string) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx,
		`SELECT 1 FROM file_content_cache WHERE path != ? AND node_ids LIKE ? LIMIT 1`,
		excludePath, "%\""+id+"\"%").Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("ingest: interned node refcount: %w", err)
	}
	return true, nil
}

// upsertCacheTx writes/updates the cache entry for a file. hasLinks records
// whether the file produced deferred linker inputs, gating the same-package
// sibling cascade in dependentsOf.
func (i *Ingester) upsertCacheTx(ctx context.Context, tx *sql.Tx, path, hash string, nodeIDs []string, hasLinks bool) error {
	raw, err := json.Marshal(nodeIDs)
	if err != nil {
		return fmt.Errorf("ingest: encode node ids: %w", err)
	}
	hl := 0
	if hasLinks {
		hl = 1
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO file_content_cache (path, content_hash, node_ids, last_ingested_at, has_links)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
	content_hash=excluded.content_hash,
	node_ids=excluded.node_ids,
	last_ingested_at=excluded.last_ingested_at,
	has_links=excluded.has_links`,
		path, hash, string(raw), 1, hl) // timestamp stub
	return err
}

// reverseDepKeys translates a file's forward-ref targets into the key space the
// incremental cascade (dependentsOf) looks up: directories. A real-Go forward
// ref is an import-path string (e.g. "example.com/repo/tax"); dependentsOf is
// called with a repo-relative FILE path and resolves siblings/importers by
// DIRECTORY, so an import-path key is never hit (BLOCK-1). We translate every
// import path that resolves into the repo to the importing package's
// directory(ies) via the committed symbol index. A target that resolves to no
// directory (a stub-parser file-path "import", or a stdlib/3rd-party package not
// present in the repo) is kept verbatim, preserving the stub key space and
// causing no phantom dependents. idx is built once per pass from store.Nodes.
func reverseDepKeys(idx *link.SymbolIndex, targets []string) []string {
	out := make([]string, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	add := func(k string) {
		if _, ok := seen[k]; ok {
			return
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	for _, t := range targets {
		dirs := idx.DirsForImport(t)
		if len(dirs) == 0 {
			add(t) // stub file path / stdlib / 3rd-party: keep as-is
			continue
		}
		for _, d := range dirs {
			add(d)
		}
	}
	return out
}

// writeReverseDepsTx rebuilds the reverse dependency index from forward refs.
// refs is already translated into the directory key space by reverseDepKeys.
func (i *Ingester) writeReverseDepsTx(ctx context.Context, tx *sql.Tx, refs map[string][]string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM reverse_deps"); err != nil {
		return err
	}
	// deps[target] = set of files that depend on target.
	deps := make(map[string]map[string]struct{})
	for file, targets := range refs {
		for _, t := range targets {
			if deps[t] == nil {
				deps[t] = make(map[string]struct{})
			}
			deps[t][file] = struct{}{}
		}
	}
	for target, set := range deps {
		list := make([]string, 0, len(set))
		for d := range set {
			list = append(list, d)
		}
		sort.Strings(list)
		raw, err := json.Marshal(list)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO reverse_deps (path, dependents) VALUES (?, ?)
ON CONFLICT(path) DO UPDATE SET dependents=excluded.dependents`,
			target, string(raw)); err != nil {
			return err
		}
	}
	return nil
}

// updateReverseDepsTx incrementally updates reverse deps for a single file.
func (i *Ingester) updateReverseDepsTx(ctx context.Context, tx *sql.Tx, file string, refs []string) error {
	// Remove file from all existing reverse dep entries.
	rows, err := tx.QueryContext(ctx, "SELECT path, dependents FROM reverse_deps")
	if err != nil {
		return err
	}
	defer rows.Close()
	updates := make(map[string][]string)
	for rows.Next() {
		var target, raw string
		if err := rows.Scan(&target, &raw); err != nil {
			return err
		}
		var list []string
		if err := json.Unmarshal([]byte(raw), &list); err != nil {
			return err
		}
		filtered := make([]string, 0, len(list))
		for _, d := range list {
			if d != file {
				filtered = append(filtered, d)
			}
		}
		if len(filtered) != len(list) {
			updates[target] = filtered
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for target, list := range updates {
		raw, err := json.Marshal(list)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE reverse_deps SET dependents = ? WHERE path = ?", string(raw), target); err != nil {
			return err
		}
	}

	// Add file to new targets.
	for _, t := range refs {
		var raw string
		err := tx.QueryRowContext(ctx, "SELECT dependents FROM reverse_deps WHERE path = ?", t).Scan(&raw)
		var list []string
		if errors.Is(err, sql.ErrNoRows) {
			list = []string{}
		} else if err != nil {
			return err
		} else {
			if err := json.Unmarshal([]byte(raw), &list); err != nil {
				return err
			}
		}
		list = append(list, file)
		sort.Strings(list)
		list = dedupStrings(list)
		raw2, err := json.Marshal(list)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO reverse_deps (path, dependents) VALUES (?, ?)
ON CONFLICT(path) DO UPDATE SET dependents=excluded.dependents`,
			t, string(raw2)); err != nil {
			return err
		}
	}
	return nil
}

// dependentsOf returns the FULL reverse-dependency closure for path that a
// cross-file linker pass requires (SW-050):
//
//   - import dependents: files that import the package owning path. reverse_deps
//     is keyed in the DIRECTORY key space: an importing file's import-path
//     forward refs are translated to the imported package's directory(ies) at
//     write time (reverseDepKeys), so a lookup by the changed file's directory
//     finds every cross-package importer. Stub parsers store file-path keys,
//     which we also look up directly for back-compat. (BLOCK-1: previously
//     reverse_deps was keyed by raw import-path string but looked up by file
//     path, so the import-dependent branch resolved nothing on real Go.)
//   - same-package siblings: every other file in path's own DIRECTORY (Open Q1).
//     Same-package edges are resolved by bare name within the directory, so a
//     rename in one file can change a NON-importing sibling's cross-file edges;
//     making directory siblings mutual dependents guarantees that sibling is
//     re-linked, closing the cascade so no edge a full pass would emit is missed
//     and no stale edge survives.
//
// The result is sorted and deduped; path itself is excluded.
func (i *Ingester) dependentsOf(ctx context.Context, path string) ([]string, error) {
	set := map[string]struct{}{}

	// Import dependents. Look up reverse_deps under BOTH the changed file's
	// directory (real-Go import-path translation) and its raw file path (stub
	// parsers key by file path). Either may be empty; both are safe to query.
	addImportDeps := func(key string) error {
		var raw string
		err := i.meta.QueryRowContext(ctx, "SELECT dependents FROM reverse_deps WHERE path = ?", key).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("ingest: read reverse deps: %w", err)
		}
		var deps []string
		if err := json.Unmarshal([]byte(raw), &deps); err != nil {
			return err
		}
		for _, d := range deps {
			set[d] = struct{}{}
		}
		return nil
	}
	dirKey := posixDir(model.NormalizePath(path))
	if err := addImportDeps(dirKey); err != nil {
		return nil, err
	}
	if dirKey != path {
		if err := addImportDeps(path); err != nil {
			return nil, err
		}
	}

	// Same-package siblings: every other cached LINKABLE file sharing path's
	// directory. The cascade fires only when path itself is a linkable file
	// (produced deferred linker inputs); unrelated files that merely share a
	// directory but produce no cross-file refs (e.g. data files, or distinct
	// non-Go units) are never dragged in. This keeps the closure exact: it
	// covers exactly the files whose cross-file edges a full pass could change.
	dir := dirKey
	changedHasLinks, err := i.fileHasLinks(ctx, path)
	if err != nil {
		return nil, err
	}
	if changedHasLinks {
		rows, err := i.meta.QueryContext(ctx, "SELECT path FROM file_content_cache WHERE has_links = 1")
		if err != nil {
			return nil, fmt.Errorf("ingest: list cached files: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				return nil, err
			}
			if p == path {
				continue
			}
			if posixDir(model.NormalizePath(p)) == dir {
				set[p] = struct{}{}
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Strings(out)
	return out, nil
}

// fileHasLinks reports whether a cached file produced deferred linker inputs.
// A miss (uncached/new file) reports false: a brand-new file has no committed
// siblings to cascade to yet, and its own refs are resolved on its first pass.
func (i *Ingester) fileHasLinks(ctx context.Context, path string) (bool, error) {
	var hl int
	err := i.meta.QueryRowContext(ctx, "SELECT has_links FROM file_content_cache WHERE path = ?", path).Scan(&hl)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("ingest: read has_links: %w", err)
	}
	return hl == 1, nil
}

// linkableCachedPaths returns every cached file that produced deferred linker
// inputs (has_links = 1) — the set a go.mod change must re-link so confirmed
// edges that lose their type-check proof degrade to heuristic instead of
// disappearing (see the expansion in ingestChanged).
func (i *Ingester) linkableCachedPaths(ctx context.Context) ([]string, error) {
	rows, err := i.meta.QueryContext(ctx, "SELECT path FROM file_content_cache WHERE has_links = 1")
	if err != nil {
		return nil, fmt.Errorf("ingest: list linkable files: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// markDirtyTx / clearDirtyTx manage the crash-recovery dirty set. When prov is
// non-nil the originating edit context is persisted on the dirty row, so
// RecoverWithRoot can replay the SAME provenance after a crash, making recovery
// provenance-idempotent (the recovered side-channel matches an uninterrupted
// edit). A nil prov stores empty edit context (full-ingest / plain recovery).
func (i *Ingester) markDirtyTx(ctx context.Context, tx *sql.Tx, path string, prov *EditProvenance) error {
	var editID, opType string
	var recordedAt int64
	if prov != nil {
		editID = prov.EditID
		opType = string(prov.OpType)
		recordedAt = prov.Timestamp
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO dirty_units (path, edit_id, op_type, recorded_at) VALUES (?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
	edit_id=excluded.edit_id,
	op_type=excluded.op_type,
	recorded_at=excluded.recorded_at`,
		path, editID, opType, recordedAt)
	return err
}

func (i *Ingester) clearDirtyTx(ctx context.Context, tx *sql.Tx, path string) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM dirty_units WHERE path = ?", path)
	return err
}

// cachedPathsTx returns all paths currently in the cache.
func (i *Ingester) cachedPathsTx(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT path FROM file_content_cache")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// removeFileTx removes a deleted file's graph nodes (cascading their incident
// edges, including cross-file edges that pointed INTO this file's symbols) and
// its sidecar bookkeeping. Deleting the nodes is what makes an incremental pass
// over a deleted file converge byte-identically with a full re-index: a full
// pass simply never re-creates them, so the incremental pass must drop them.
func (i *Ingester) removeFileTx(ctx context.Context, tx *sql.Tx, w graphstore.Writer, path string) error {
	// Drop the file's graph nodes first (DeleteNode cascades incident edges).
	ids, err := i.cachedNodeIDs(ctx, path)
	if err != nil {
		return err
	}
	for _, id := range ids {
		// WP-01 interned-node lifecycle: keep a shared `package` node alive while a
		// sibling file still declares it (checked against the tx so an earlier
		// removeFileTx in this pass is already visible). A no-op for per-file nodes.
		shared, err := i.nodeReferencedByOtherFile(ctx, tx, path, id)
		if err != nil {
			return err
		}
		if shared {
			continue
		}
		if err := w.DeleteNode(ctx, model.NodeId(id)); err != nil {
			return fmt.Errorf("ingest: delete node of removed file %s: %w", path, err)
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM file_content_cache WHERE path = ?", path); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM reverse_deps WHERE path = ?", path); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM dirty_units WHERE path = ?", path)
	return err
}

// metaTx runs fn inside a single SQLite transaction for the meta DB.
func (i *Ingester) metaTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := i.meta.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ingest: begin meta tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func dedupStrings(a []string) []string {
	if len(a) == 0 {
		return a
	}
	out := make([]string, 0, len(a))
	var last string
	for _, s := range a {
		if s == last {
			continue
		}
		out = append(out, s)
		last = s
	}
	return out
}
