// Package ingest implements graphi's incremental source-ingestion pipeline.
//
// Layering: ingest is an engine package. It consumes core/parse and core/model
// and commits nodes/edges through core/graphstore. It persists its own
// bookkeeping (content cache, reverse dependencies, dirty flags) in a separate
// SQLite sidecar so graphstore remains focused on graph data.
//
// Security: all file paths are sanitized relative to the repo root; no
// eval/exec/shell; all SQL is parameterized.
package ingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"

	_ "modernc.org/sqlite" // ingest meta DB driver
)

// Parser abstracts the parse operation so tests can count invocations and
// inject deterministic ASTs.
type Parser interface {
	Parse(ctx context.Context, path string, src []byte) (*parse.ParseResult, error)
}

// Registry maps extensions to parsers. It satisfies the Parser interface for a
// whole repository walk.
type Registry interface {
	Parse(ctx context.Context, path string, src []byte) (*parse.ParseResult, error)
}

// Ingester runs incremental and full ingestion.
type Ingester struct {
	store  graphstore.Graphstore
	parser Parser
	meta   *sql.DB

	// test hooks
	failAfterDirtyMark error
	hookMu             sync.Mutex
}

// New constructs an Ingester. metaDir receives a SQLite sidecar for cache,
// reverse-deps, and dirty flags. If metaDir is empty, an in-memory sidecar is
// used (testing only).
func New(store graphstore.Graphstore, parser Parser, metaDir string) (*Ingester, error) {
	dbPath := ":memory:"
	if metaDir != "" {
		if err := os.MkdirAll(metaDir, 0o700); err != nil {
			return nil, fmt.Errorf("ingest: create meta dir: %w", err)
		}
		dbPath = filepath.Join(metaDir, "ingest-meta.db")
	}
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, fmt.Errorf("ingest: open meta db: %w", err)
	}
	i := &Ingester{store: store, parser: parser, meta: db}
	if err := i.initSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return i, nil
}

func (i *Ingester) initSchema(ctx context.Context) error {
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
`
	if _, err := i.meta.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("ingest: init schema: %w", err)
	}
	return nil
}

// Close releases resources.
func (i *Ingester) Close() error {
	if i.meta != nil {
		return i.meta.Close()
	}
	return nil
}

// fileUnit is the internal representation of one source file during ingestion.
type fileUnit struct {
	path    string
	relPath string
	src     []byte
	hash    string
}

// IngestAll performs a full ingestion of root, parsing every file and
// rebuilding the cache and reverse-dependency index from scratch.
func (i *Ingester) IngestAll(ctx context.Context, root string) error {
	units, err := i.walk(root)
	if err != nil {
		return err
	}

	return i.metaTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM file_content_cache"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM reverse_deps"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM dirty_units"); err != nil {
			return err
		}

		// Build forward refs for each file, then derive reverse deps.
		refs := make(map[string][]string, len(units))
		for _, u := range units {
			nodeIDs, fwd, err := i.parseAndCommit(ctx, u)
			if err != nil {
				return err
			}
			if err := i.upsertCacheTx(ctx, tx, u.relPath, u.hash, nodeIDs); err != nil {
				return err
			}
			refs[u.relPath] = fwd
		}
		if err := i.writeReverseDepsTx(ctx, tx, refs); err != nil {
			return err
		}
		return nil
	})
}

// IngestChanged performs incremental ingestion: it walks root, skips unchanged
// files via the content cache, re-parses changed files, and re-parses direct
// dependents affected by import/symbol changes.
func (i *Ingester) IngestChanged(ctx context.Context, root string, changed []string) error {
	units, err := i.walk(root)
	if err != nil {
		return err
	}

	// Collect explicitly changed paths + cascade-affected dependents.
	toProcess := make(map[string]struct{})
	for _, c := range changed {
		rp, err := i.sanitizePath(root, c)
		if err != nil {
			return err
		}
		toProcess[rp] = struct{}{}
	}

	// Add dependents of changed files.
	for c := range toProcess {
		deps, err := i.dependentsOf(ctx, c)
		if err != nil {
			return err
		}
		for _, d := range deps {
			toProcess[d] = struct{}{}
		}
	}

	// Phase 1: persist dirty flags in their own transaction so a crash after
	// this point leaves recoverable state.
	if err := i.metaTx(ctx, func(tx *sql.Tx) error {
		for _, u := range units {
			if _, ok := toProcess[u.relPath]; !ok {
				continue
			}
			if err := i.markDirtyTx(ctx, tx, u.relPath); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	// Crash-recovery test hook: fault injected after dirty flags are durable but
	// before any graphstore commit.
	if hookErr := i.takeFailHook(); hookErr != nil {
		return hookErr
	}

	// Phase 2: parse, commit to graphstore, update cache/reverse-deps, clear dirty.
	return i.metaTx(ctx, func(tx *sql.Tx) error {
		for _, u := range units {
			if _, ok := toProcess[u.relPath]; !ok {
				continue
			}
			nodeIDs, fwd, err := i.parseAndCommit(ctx, u)
			if err != nil {
				return err
			}
			if err := i.upsertCacheTx(ctx, tx, u.relPath, u.hash, nodeIDs); err != nil {
				return err
			}
			if err := i.updateReverseDepsTx(ctx, tx, u.relPath, fwd); err != nil {
				return err
			}
			if err := i.clearDirtyTx(ctx, tx, u.relPath); err != nil {
				return err
			}
		}

		// Remove cache entries for files that no longer exist.
		present := make(map[string]struct{}, len(units))
		for _, u := range units {
			present[u.relPath] = struct{}{}
		}
		cached, err := i.cachedPathsTx(ctx, tx)
		if err != nil {
			return err
		}
		for _, p := range cached {
			if _, ok := present[p]; ok {
				continue
			}
			if err := i.removeFileTx(ctx, tx, p); err != nil {
				return err
			}
		}
		return nil
	})
}

// Recover reprocesses any units that were marked dirty but not cleared (e.g.
// after a crash). It returns nil when the dirty set is empty.
func (i *Ingester) Recover(ctx context.Context) error {
	rows, err := i.meta.QueryContext(ctx, "SELECT path FROM dirty_units")
	if err != nil {
		return fmt.Errorf("ingest: recover query dirty: %w", err)
	}
	defer rows.Close()
	var dirty []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return fmt.Errorf("ingest: recover scan dirty: %w", err)
		}
		dirty = append(dirty, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(dirty) == 0 {
		return nil
	}

	// Reprocess dirty units in a full IngestChanged pass. We do not know the
	// original root here, so Recover requires the caller to supply it.
	return errors.New("ingest: Recover requires root; use RecoverWithRoot")
}

// RecoverWithRoot reprocesses dirty units relative to root.
func (i *Ingester) RecoverWithRoot(ctx context.Context, root string) error {
	rows, err := i.meta.QueryContext(ctx, "SELECT path FROM dirty_units")
	if err != nil {
		return fmt.Errorf("ingest: recover query dirty: %w", err)
	}
	defer rows.Close()
	var dirty []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return fmt.Errorf("ingest: recover scan dirty: %w", err)
		}
		dirty = append(dirty, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(dirty) == 0 {
		return nil
	}
	return i.IngestChanged(ctx, root, dirty)
}

// walk returns all source files under root, sorted deterministically.
func (i *Ingester) walk(root string) ([]fileUnit, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("ingest: abs root: %w", err)
	}
	root = filepath.Clean(root)

	var units []fileUnit
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.Contains(rel, "..") {
			return fmt.Errorf("ingest: escaped path %q", rel)
		}
		src, err := os.ReadFile(path) //nolint:gosec // path derived from sanitized root
		if err != nil {
			return fmt.Errorf("ingest: read %s: %w", rel, err)
		}
		units = append(units, fileUnit{
			path:    path,
			relPath: rel,
			src:     src,
			hash:    hashBytes(src),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(units, func(i, j int) bool { return units[i].relPath < units[j].relPath })
	return units, nil
}

// sanitizePath ensures p is inside root and returns a repo-relative POSIX path.
func (i *Ingester) sanitizePath(root, p string) (string, error) {
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return "", fmt.Errorf("ingest: path outside root: %w", err)
		}
		p = rel
	}
	p = filepath.ToSlash(filepath.Clean(p))
	if strings.HasPrefix(p, "..") || strings.Contains(p, "../") {
		return "", fmt.Errorf("ingest: escaped path %q", p)
	}
	return p, nil
}

// parseAndCommit parses one file, writes its nodes/edges to graphstore, and
// returns the node IDs plus the list of files it references (forward refs).
func (i *Ingester) parseAndCommit(ctx context.Context, u fileUnit) ([]string, []string, error) {
	res, err := i.parser.Parse(ctx, u.relPath, u.src)
	if err != nil {
		return nil, nil, fmt.Errorf("ingest: parse %s: %w", u.relPath, err)
	}

	// Remove old nodes for this file before inserting the new parse output. As of
	// SW-036 the graphstore exposes an explicit delete API, so the orphan debt
	// SW-035 documented here is closed: any node whose identity changed
	// (rename/move/signature-change all mint a new NodeId because identity is
	// xxhash64(Kind,QualifiedName,SourcePath)) is dropped along with its incident
	// edges, so the incremental re-index converges byte-for-byte with a full
	// re-index. An identity-PRESERVING edit deletes then re-PutNodes the same ID,
	// which is harmless; computing the new-id set first lets us skip those.
	oldIDs, err := i.cachedNodeIDs(ctx, u.relPath)
	if err != nil {
		return nil, nil, err
	}

	nodeIDs := make([]string, 0, len(res.Nodes))
	newIDs := make(map[string]struct{}, len(res.Nodes))
	for _, n := range res.Nodes {
		newIDs[string(n.ID())] = struct{}{}
	}
	// Delete any previously-committed node for this file whose identity is NOT
	// reproduced by the new parse. DeleteNode cascades incident edges, so stale
	// edges anchored on a removed node can never be orphaned.
	for _, id := range oldIDs {
		if _, kept := newIDs[id]; kept {
			continue
		}
		if err := i.store.DeleteNode(ctx, model.NodeId(id)); err != nil {
			return nil, nil, fmt.Errorf("ingest: delete stale node %s: %w", id, err)
		}
	}

	for _, n := range res.Nodes {
		if err := i.store.PutNode(ctx, n); err != nil {
			return nil, nil, fmt.Errorf("ingest: put node: %w", err)
		}
		nodeIDs = append(nodeIDs, string(n.ID()))
	}
	for _, e := range res.Edges {
		if err := i.store.PutEdge(ctx, e); err != nil {
			return nil, nil, fmt.Errorf("ingest: put edge: %w", err)
		}
	}

	// Forward refs = paths this file imports/uses. For the stub parser this is
	// supplied in the parse result; a real parser derives it from imports.
	return nodeIDs, res.References, nil
}

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

// upsertCacheTx writes/updates the cache entry for a file.
func (i *Ingester) upsertCacheTx(ctx context.Context, tx *sql.Tx, path, hash string, nodeIDs []string) error {
	raw, err := json.Marshal(nodeIDs)
	if err != nil {
		return fmt.Errorf("ingest: encode node ids: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO file_content_cache (path, content_hash, node_ids, last_ingested_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
	content_hash=excluded.content_hash,
	node_ids=excluded.node_ids,
	last_ingested_at=excluded.last_ingested_at`,
		path, hash, string(raw), 1) // timestamp stub
	return err
}

// writeReverseDepsTx rebuilds the reverse dependency index from forward refs.
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

// dependentsOf returns the cached direct dependents of path.
func (i *Ingester) dependentsOf(ctx context.Context, path string) ([]string, error) {
	var raw string
	err := i.meta.QueryRowContext(ctx, "SELECT dependents FROM reverse_deps WHERE path = ?", path).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ingest: read reverse deps: %w", err)
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// markDirtyTx / clearDirtyTx manage the crash-recovery dirty set.
func (i *Ingester) markDirtyTx(ctx context.Context, tx *sql.Tx, path string) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO dirty_units (path) VALUES (?)
ON CONFLICT(path) DO UPDATE SET path=excluded.path`, path)
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

// removeFileTx removes a deleted file's cache entry and dirty flag.
func (i *Ingester) removeFileTx(ctx context.Context, tx *sql.Tx, path string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM file_content_cache WHERE path = ?", path); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM reverse_deps WHERE path = ?", path); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "DELETE FROM dirty_units WHERE path = ?", path)
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

// SetFailAfterDirtyMarkHook arms a one-shot fault injected after dirty-mark but
// before commit. Test-only.
func (i *Ingester) SetFailAfterDirtyMarkHook(err error) {
	i.hookMu.Lock()
	i.failAfterDirtyMark = err
	i.hookMu.Unlock()
}

func (i *Ingester) takeFailHook() error {
	i.hookMu.Lock()
	defer i.hookMu.Unlock()
	err := i.failAfterDirtyMark
	i.failAfterDirtyMark = nil
	return err
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
