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
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/link"
	"github.com/samibel/graphi/engine/observe"

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

// EditOpType is the closed enum of edit operations that may originate a
// provenance record. It mirrors engine/edit's RefactorKind closed-set discipline
// (rename/extract/move/signature_change) plus the base single-op "apply", and a
// synthetic "recovery"-class value is intentionally NOT introduced: recovery
// replays the ORIGINAL edit context (provenance-idempotent recovery), so the
// recorded op_type after a crash+recover is identical to an uninterrupted edit.
type EditOpType string

const (
	// EditOpApply is the base identity-preserving single-op edit (engine/edit.Apply).
	EditOpApply EditOpType = "apply"
	// EditOpRename mirrors edit.RefactorRename.
	EditOpRename EditOpType = "rename"
	// EditOpExtract mirrors edit.RefactorExtract.
	EditOpExtract EditOpType = "extract"
	// EditOpMove mirrors edit.RefactorMove.
	EditOpMove EditOpType = "move"
	// EditOpSignatureChange mirrors edit.RefactorSignatureChange.
	EditOpSignatureChange EditOpType = "signature_change"
	// EditOpInline mirrors edit.RefactorInline (SW-092): substitute a symbol's
	// value at every reference site and remove the declaration.
	EditOpInline EditOpType = "inline"
	// EditOpSafeDelete mirrors edit.RefactorSafeDelete (SW-093): remove a symbol
	// only after a reference-safety gate clears.
	EditOpSafeDelete EditOpType = "safe_delete"
	// EditOpUndo is the SW-038 reversal: an undo restores the pre-edit source +
	// graph snapshot and re-indexes the touched files under this op-type so the
	// edit_provenance row distinguishes a reversal from a forward edit.
	EditOpUndo EditOpType = "undo"
)

// validEditOpTypes is the closed set; unknown values are rejected so the audit
// field cannot be poisoned by an arbitrary caller string.
var validEditOpTypes = map[EditOpType]struct{}{
	EditOpApply:           {},
	EditOpRename:          {},
	EditOpExtract:         {},
	EditOpMove:            {},
	EditOpSignatureChange: {},
	EditOpInline:          {},
	EditOpSafeDelete:      {},
	EditOpUndo:            {},
}

// Valid reports whether t is a member of the closed op-type enum.
func (t EditOpType) Valid() bool {
	_, ok := validEditOpTypes[t]
	return ok
}

// EditProvenance is the per-edit audit context threaded from the engine/edit
// saga into a provenance-aware ingest pass. The EditID is minted ONCE and the
// Timestamp captured ONCE in the saga (a single value shared across the whole
// touched set), never per-element inside ingest. It is recorded into the
// edit_provenance side-channel in Phase 2 of the dirty-flag metaTx, keyed by
// every affected NodeId/EdgeId.
type EditProvenance struct {
	// EditID is the saga-minted identifier of the originating edit.
	EditID string
	// OpType is the closed-enum operation kind.
	OpType EditOpType
	// Timestamp is the Unix-nanosecond wall-clock instant captured once by the saga.
	Timestamp int64
}

// Validate enforces the closed op-type enum and a non-empty edit id. A
// zero-value EditProvenance (no originating edit) is reported as invalid so the
// zero-provenance ingest paths (full IngestAll, plain recovery) opt out by
// passing nil rather than an empty struct.
func (p EditProvenance) Validate() error {
	if strings.TrimSpace(p.EditID) == "" {
		return fmt.Errorf("ingest: empty edit id")
	}
	if !p.OpType.Valid() {
		return fmt.Errorf("ingest: unknown op_type %q", p.OpType)
	}
	return nil
}

// Ingester runs incremental and full ingestion.
type Ingester struct {
	store  graphstore.Graphstore
	parser Parser
	meta   *sql.DB
	linker *link.Linker

	// bounds are the fail-closed parse-time resource bounds (SW-055 AC#6) applied
	// to untrusted inputs: max file size (checked before ReadFile via FileInfo),
	// parse timeout (context.WithTimeout on the Parse ctx), and recursion depth
	// (enforced inside core/parse). On any breach the offending file is SKIPPED
	// with a structured diagnostic and ingestion continues — never parse-anyway,
	// never silently truncate. Defaulted in New to parse.DefaultResourceBounds().
	bounds parse.ResourceBounds

	// skipMu guards skipped. skipped accumulates the fail-closed skip diagnostics
	// of the most recent ingest pass (oversize / timeout / depth breaches). It
	// carries ONLY structured provenance, never raw source bytes.
	skipMu  sync.Mutex
	skipped []SkipDiagnostic

	// broker, when set, is notified of ingest lifecycle events (e.g.
	// ingest-completed) so surfaces (HTTP SSE) can stream freshness updates to
	// clients. Nil = no-op (default); existing callers are unaffected.
	broker *observe.Broker

	// test hooks
	failAfterDirtyMark error
	hookMu             sync.Mutex
}

// WithBroker attaches an event broker and returns the receiver for chaining.
// When attached, IngestAll/IngestChanged publish a lifecycle event on success.
// Without a broker ingest behaves exactly as before.
func (i *Ingester) WithBroker(b *observe.Broker) *Ingester {
	i.broker = b
	return i
}

// notifyIngest publishes a loss-tolerant lifecycle event. It is nil-safe and
// never returns an error — a publish failure must not fail ingestion.
func (i *Ingester) notifyIngest(ctx context.Context, kind string, files int) {
	if i.broker == nil {
		return
	}
	payload, _ := json.Marshal(map[string]int{"files": files})
	i.broker.Publish(ctx, observe.Event{Type: kind, Ts: time.Now(), Payload: payload})
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
	i := &Ingester{store: store, parser: parser, meta: db, linker: link.New(), bounds: parse.DefaultResourceBounds()}
	// Apply the fail-closed recursion-depth bound to the shared parse path
	// (process-wide; core/parse reads it per Extract). Size + timeout are enforced
	// at this ingest boundary directly.
	parse.SetMaxParseDepth(i.bounds.MaxDepth)
	if err := i.initSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return i, nil
}

// SkipReason categorizes why a file was skipped fail-closed.
type SkipReason string

const (
	// SkipOversize: file exceeded ResourceBounds.MaxFileSize (checked before read).
	SkipOversize SkipReason = "oversize"
	// SkipTimeout: parse exceeded ResourceBounds.ParseTimeout.
	SkipTimeout SkipReason = "timeout"
	// SkipMaxDepth: input exceeded ResourceBounds.MaxDepth (nesting/recursion).
	SkipMaxDepth SkipReason = "max-depth"
)

// SkipDiagnostic is the structured, source-free record of a fail-closed skip. It
// carries ONLY provenance (path, reason, the observed size for oversize) — never
// raw source bytes (SW-055 AC#6 default-deny source sanitization).
type SkipDiagnostic struct {
	Path   string     // repo-relative path of the skipped file
	Reason SkipReason // why it was skipped
	Size   int64      // observed size in bytes (oversize only; 0 otherwise)
}

// WithResourceBounds overrides the fail-closed parse-time resource bounds and
// returns the receiver for chaining. It also applies the depth bound to the
// process-wide parse path. Passing the zero ResourceBounds disables all bounds.
func (i *Ingester) WithResourceBounds(b parse.ResourceBounds) *Ingester {
	i.bounds = b
	parse.SetMaxParseDepth(b.MaxDepth)
	return i
}

// SkippedDiagnostics returns a copy of the fail-closed skip diagnostics recorded
// during the most recent ingest pass.
func (i *Ingester) SkippedDiagnostics() []SkipDiagnostic {
	i.skipMu.Lock()
	defer i.skipMu.Unlock()
	out := make([]SkipDiagnostic, len(i.skipped))
	copy(out, i.skipped)
	return out
}

// recordSkip appends a fail-closed skip diagnostic (concurrency-safe).
func (i *Ingester) recordSkip(d SkipDiagnostic) {
	i.skipMu.Lock()
	i.skipped = append(i.skipped, d)
	i.skipMu.Unlock()
}

// resetSkips clears skip diagnostics at the start of an ingest pass.
func (i *Ingester) resetSkips() {
	i.skipMu.Lock()
	i.skipped = nil
	i.skipMu.Unlock()
}

func (i *Ingester) initSchema(ctx context.Context) error {
	// edit_provenance is the SW-037 side-channel: the per-edit audit record
	// (source edit id, operation type, timestamp) keyed by the affected
	// NodeId/EdgeId. It deliberately lives here in the ingest meta sidecar — NOT
	// in core/model or model.Graph.Marshal — because the edit id and timestamp are
	// volatile (properties of HOW the graph was last mutated, not of the source
	// content). Embedding them in the marshalled graph would make the AC-3
	// incremental-vs-full digest differ for every edit; keeping them out of the
	// graph is what lets AC-3's structural graphDigest stay byte-identical while
	// AC-1's edit provenance still distinguishes which edit touched what. The
	// dirty_units row carries the same edit context (edit_id/op_type/recorded_at)
	// so RecoverWithRoot reproduces identical side-channel state after a crash
	// (provenance-idempotent recovery).
	// Base DDL is CREATE TABLE IF NOT EXISTS only — it must NEVER be relied upon to
	// add a column to a table that already exists (CREATE TABLE IF NOT EXISTS
	// silently no-ops on an existing table, leaving new columns unapplied). The
	// dirty_units table here is declared with ONLY its original SW-036/EP-001
	// shape (path); the SW-037 edit-context columns are added by the versioned
	// migration ladder below so that a pre-SW-037 on-disk sidecar is migrated in
	// place rather than left with a stale schema. See migrate().
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
CREATE TABLE IF NOT EXISTS edit_provenance (
	element_id TEXT NOT NULL,
	element_kind TEXT NOT NULL,
	edit_id TEXT NOT NULL,
	op_type TEXT NOT NULL,
	recorded_at INTEGER NOT NULL,
	PRIMARY KEY(element_id, edit_id)
);
`
	if _, err := i.meta.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("ingest: init schema: %w", err)
	}
	return i.migrate(ctx)
}

// schemaVersion is the current sidecar schema version. Bump it (and add a step
// to migrate) whenever an additive schema change is introduced.
//
//	0 -> 1 : SW-037 — add edit-context columns to dirty_units.
//	1 -> 2 : SW-050 — add has_links flag to file_content_cache (linker cascade).
const schemaVersion = 2

// migrate applies additive schema changes exactly once, gated on PRAGMA
// user_version, so an existing on-disk ingest-meta.db (e.g. one created by a
// pre-SW-037 story with dirty_units(path) only) is upgraded deterministically
// instead of relying on CREATE TABLE IF NOT EXISTS (which cannot add columns to
// an already-existing table). Each step is itself idempotent and column-presence
// guarded, so the ladder is safe even on a fresh DB and on a DB whose
// user_version was never tracked before this story.
func (i *Ingester) migrate(ctx context.Context) error {
	var current int
	if err := i.meta.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("ingest: read user_version: %w", err)
	}
	if current >= schemaVersion {
		return nil
	}
	if current < 1 {
		if err := i.migrateDirtyUnitsEditContext(ctx); err != nil {
			return fmt.Errorf("ingest: migrate dirty_units edit context: %w", err)
		}
	}
	if current < 2 {
		if err := i.migrateCacheHasLinks(ctx); err != nil {
			return fmt.Errorf("ingest: migrate file_content_cache has_links: %w", err)
		}
	}
	// PRAGMA does not accept bound parameters; schemaVersion is a trusted constant.
	if _, err := i.meta.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("ingest: set user_version: %w", err)
	}
	return nil
}

// migrateDirtyUnitsEditContext adds the SW-037 edit-context columns to an
// existing dirty_units table when they are absent. ADD COLUMN with a NOT NULL
// DEFAULT is safe on a populated table. Detection via PRAGMA table_info makes the
// step idempotent regardless of prior user_version tracking.
func (i *Ingester) migrateDirtyUnitsEditContext(ctx context.Context) error {
	have, err := i.columnSet(ctx, "dirty_units")
	if err != nil {
		return err
	}
	adds := []struct {
		col string
		ddl string
	}{
		{"edit_id", "ALTER TABLE dirty_units ADD COLUMN edit_id TEXT NOT NULL DEFAULT ''"},
		{"op_type", "ALTER TABLE dirty_units ADD COLUMN op_type TEXT NOT NULL DEFAULT ''"},
		{"recorded_at", "ALTER TABLE dirty_units ADD COLUMN recorded_at INTEGER NOT NULL DEFAULT 0"},
	}
	for _, a := range adds {
		if _, ok := have[a.col]; ok {
			continue
		}
		if _, err := i.meta.ExecContext(ctx, a.ddl); err != nil {
			return fmt.Errorf("ingest: add column %s: %w", a.col, err)
		}
	}
	return nil
}

// migrateCacheHasLinks adds the SW-050 has_links flag to file_content_cache when
// absent. The flag records whether a file produced deferred linker inputs
// (PendingRefs/Imports) so the same-package-directory sibling cascade only fires
// among genuinely linkable files (real Go), never among unrelated stub files
// that merely share a directory. The step is idempotent (PRAGMA-detected).
func (i *Ingester) migrateCacheHasLinks(ctx context.Context) error {
	have, err := i.columnSet(ctx, "file_content_cache")
	if err != nil {
		return err
	}
	if _, ok := have["has_links"]; ok {
		return nil
	}
	if _, err := i.meta.ExecContext(ctx, "ALTER TABLE file_content_cache ADD COLUMN has_links INTEGER NOT NULL DEFAULT 0"); err != nil {
		return fmt.Errorf("ingest: add column has_links: %w", err)
	}
	return nil
}

// columnSet returns the set of column names on a table via PRAGMA table_info.
// The table name is a trusted in-package literal, never caller-supplied.
func (i *Ingester) columnSet(ctx context.Context, table string) (map[string]struct{}, error) {
	rows, err := i.meta.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil, fmt.Errorf("ingest: table_info(%s): %w", table, err)
	}
	defer rows.Close()
	cols := make(map[string]struct{})
	for rows.Next() {
		var (
			cid        int
			name, ctyp string
			notNull    int
			dfltValue  sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &ctyp, &notNull, &dfltValue, &pk); err != nil {
			return nil, fmt.Errorf("ingest: scan table_info(%s): %w", table, err)
		}
		cols[name] = struct{}{}
	}
	return cols, rows.Err()
}

// EditProvenanceRecord is one row of the edit_provenance side-channel: the edit
// that last touched an element, keyed by NodeId/EdgeId.
type EditProvenanceRecord struct {
	ElementID   string
	ElementKind string
	EditID      string
	OpType      EditOpType
	RecordedAt  int64
}

// EditProvenance returns every edit_provenance row, sorted by (element_id,
// edit_id), so callers (and SW-038's audit/undo, and the AC-1 tests) can read
// the per-edit audit record. It reads the side-channel only — it never touches
// the graph or the AC-3 structural digest.
func (i *Ingester) EditProvenance(ctx context.Context) ([]EditProvenanceRecord, error) {
	rows, err := i.meta.QueryContext(ctx, `
SELECT element_id, element_kind, edit_id, op_type, recorded_at
FROM edit_provenance
ORDER BY element_id, edit_id`)
	if err != nil {
		return nil, fmt.Errorf("ingest: query edit provenance: %w", err)
	}
	defer rows.Close()
	var out []EditProvenanceRecord
	for rows.Next() {
		var r EditProvenanceRecord
		var op string
		if err := rows.Scan(&r.ElementID, &r.ElementKind, &r.EditID, &op, &r.RecordedAt); err != nil {
			return nil, fmt.Errorf("ingest: scan edit provenance: %w", err)
		}
		r.OpType = EditOpType(op)
		out = append(out, r)
	}
	return out, rows.Err()
}

// MetaDB exposes the ingest-meta SQLite sidecar handle so a sibling engine
// side-channel (SW-038's change_record audit/undo store in engine/edit) can own
// its OWN table in the SAME sidecar that already holds edit_provenance,
// file_content_cache, reverse_deps, and dirty_units — never in core/graphstore
// (which would poison the AC-1 marshalled-graph digest). The returned handle is
// read/write but the caller MUST confine itself to its own table(s); the ingest
// pipeline owns every table declared in initSchema. It is exposed at the engine
// layer only (engine/edit consumes it); no surface ever touches it.
func (i *Ingester) MetaDB() *sql.DB { return i.meta }

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
	i.resetSkips()
	units, err := i.walk(root)
	if err != nil {
		return err
	}

	if err := i.metaTx(ctx, func(tx *sql.Tx) error {
		// Capture the node IDs of the PREVIOUS full pass (if this store is being
		// re-indexed) BEFORE clearing the cache, so nodes of files that have since
		// disappeared can be purged — otherwise a full re-index of a reused store
		// retains stale nodes and is no longer "full", breaking byte-identity
		// against a fresh full index AND against the incremental path.
		priorNodeIDs, err := i.allCachedNodeIDsTx(ctx, tx)
		if err != nil {
			return err
		}

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
		var fileRefs []link.FileRefs
		owned := make(map[string]struct{})
		parserEdges := make(map[string]struct{})
		for _, u := range units {
			nodeIDs, edgeIDs, fwd, fr, err := i.parseAndCommit(ctx, u)
			if err != nil {
				return err
			}
			if err := i.upsertCacheTx(ctx, tx, u.relPath, u.hash, nodeIDs, fr != nil); err != nil {
				return err
			}
			refs[u.relPath] = fwd
			for _, id := range nodeIDs {
				owned[id] = struct{}{}
			}
			for _, eid := range edgeIDs {
				parserEdges[eid] = struct{}{}
			}
			if fr != nil {
				fileRefs = append(fileRefs, *fr)
			}
		}

		// Purge any prior node no longer produced by this pass (deleted files,
		// or renamed symbols on a reused store). DeleteNode cascades incident
		// edges, so no stale cross-file edge can survive.
		for _, id := range priorNodeIDs {
			if _, kept := owned[id]; kept {
				continue
			}
			if err := i.store.DeleteNode(ctx, model.NodeId(id)); err != nil {
				return fmt.Errorf("ingest: purge stale node %s: %w", id, err)
			}
		}

		// Translate import-path forward refs into the directory key space so the
		// incremental cascade can look them up by directory (BLOCK-1). The index
		// is built from the now-fully-committed node set.
		nodes, err := i.store.Nodes(ctx, graphstore.Query{})
		if err != nil {
			return fmt.Errorf("ingest: read nodes for reverse deps: %w", err)
		}
		idx := link.BuildIndex(nodes)
		dirRefs := make(map[string][]string, len(refs))
		for file, targets := range refs {
			dirRefs[file] = reverseDepKeys(idx, targets)
		}
		if err := i.writeReverseDepsTx(ctx, tx, dirRefs); err != nil {
			return err
		}
		// Post-node-commit linker pass (site 1): all nodes are now committed, so
		// cross-file/cross-package edges can be resolved against the full set.
		if _, err := i.linkFiles(ctx, fileRefs, owned, parserEdges); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	i.notifyIngest(ctx, "ingest-completed", len(units))
	return nil
}

// IngestChanged performs incremental ingestion: it walks root, skips unchanged
// files via the content cache, re-parses changed files, and re-parses direct
// dependents affected by import/symbol changes. It carries no edit provenance;
// callers that originate an edit use IngestChangedWithProvenance.
func (i *Ingester) IngestChanged(ctx context.Context, root string, changed []string) error {
	if err := i.ingestChanged(ctx, root, changed, nil); err != nil {
		return err
	}
	i.notifyIngest(ctx, "ingest-changed", len(changed))
	return nil
}

// IngestChangedWithProvenance is the provenance-aware incremental ingest entry
// point. It behaves identically to IngestChanged but additionally records the
// supplied edit provenance against every affected NodeId/EdgeId in the
// edit_provenance side-channel, atomically with the Phase-2 cache/clear-dirty
// commit. prov.EditID is minted once and prov.Timestamp captured once by the
// caller (the engine/edit saga); the same value is shared across the whole
// touched set. The provenance is also persisted on the dirty_units row in Phase
// 1 so RecoverWithRoot reproduces identical side-channel state after a crash.
func (i *Ingester) IngestChangedWithProvenance(ctx context.Context, root string, changed []string, prov EditProvenance) error {
	if err := prov.Validate(); err != nil {
		return err
	}
	return i.ingestChanged(ctx, root, changed, &prov)
}

// ingestChanged is the shared core for the zero-provenance and provenance-aware
// entry points. When prov is non-nil the edit context rides Phase 1 (the dirty
// row) and Phase 2 (the edit_provenance side-channel) of the existing two-phase
// dirty-flag protocol, so a crash before Phase-2 commit leaves the file dirty
// AND no provenance recorded, and a crash after leaves both committed — there is
// no window where the graph is updated but provenance is missing/stale.
func (i *Ingester) ingestChanged(ctx context.Context, root string, changed []string, prov *EditProvenance) error {
	i.resetSkips()
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

	// Phase 1: persist dirty flags (with the edit context, if any) in their own
	// transaction so a crash after this point leaves recoverable state that also
	// reproduces the side-channel.
	if err := i.metaTx(ctx, func(tx *sql.Tx) error {
		for _, u := range units {
			if _, ok := toProcess[u.relPath]; !ok {
				continue
			}
			if err := i.markDirtyTx(ctx, tx, u.relPath, prov); err != nil {
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

	// Phase 2: parse, commit to graphstore, update cache/reverse-deps, record edit
	// provenance, clear dirty — all in one meta transaction.
	return i.metaTx(ctx, func(tx *sql.Tx) error {
		var fileRefs []link.FileRefs
		owned := make(map[string]struct{})
		parserEdges := make(map[string]struct{})
		fwdByFile := make(map[string][]string)
		for _, u := range units {
			if _, ok := toProcess[u.relPath]; !ok {
				continue
			}
			nodeIDs, edgeIDs, fwd, fr, err := i.parseAndCommit(ctx, u)
			if err != nil {
				return err
			}
			if err := i.upsertCacheTx(ctx, tx, u.relPath, u.hash, nodeIDs, fr != nil); err != nil {
				return err
			}
			fwdByFile[u.relPath] = fwd
			for _, id := range nodeIDs {
				owned[id] = struct{}{}
			}
			for _, eid := range edgeIDs {
				parserEdges[eid] = struct{}{}
			}
			if fr != nil {
				fileRefs = append(fileRefs, *fr)
			}
			// Record provenance for every node AND intra-file edge the
			// incremental pass touched (including reverse-dep cascade units),
			// atomically with the cache/clear-dirty commit.
			if prov != nil {
				if err := i.recordEditProvenanceTx(ctx, tx, "node", nodeIDs, *prov); err != nil {
					return err
				}
				if err := i.recordEditProvenanceTx(ctx, tx, "edge", edgeIDs, *prov); err != nil {
					return err
				}
			}
			if err := i.clearDirtyTx(ctx, tx, u.relPath); err != nil {
				return err
			}
		}

		// Update reverse deps AFTER all reprocessed nodes are committed, so the
		// import-path → directory translation (BLOCK-1) resolves target packages
		// against the full committed node set rather than a partial mid-loop view.
		if len(fwdByFile) > 0 {
			nodes, err := i.store.Nodes(ctx, graphstore.Query{})
			if err != nil {
				return fmt.Errorf("ingest: read nodes for reverse deps: %w", err)
			}
			idx := link.BuildIndex(nodes)
			files := make([]string, 0, len(fwdByFile))
			for f := range fwdByFile {
				files = append(files, f)
			}
			sort.Strings(files) // deterministic update order
			for _, f := range files {
				if err := i.updateReverseDepsTx(ctx, tx, f, reverseDepKeys(idx, fwdByFile[f])); err != nil {
					return err
				}
			}
		}

		// Post-node-commit linker pass (site 2): re-resolve cross-file edges for
		// the reprocessed files against the full committed node set, removing
		// stale from-owned cross-file edges first. The cascade closure
		// (dependentsOf) guarantees every file whose edges could change is in the
		// reprocessed set, so the incremental result converges with a full pass.
		linkEdgeIDs, err := i.linkFiles(ctx, fileRefs, owned, parserEdges)
		if err != nil {
			return err
		}
		// Funnel the linker's cross-file edge IDs into the edit-provenance
		// side-channel so an incremental edit records provenance for them too.
		if prov != nil {
			if err := i.recordEditProvenanceTx(ctx, tx, "edge", linkEdgeIDs, *prov); err != nil {
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

// recordEditProvenanceTx writes one edit_provenance row per element id, keyed by
// (element_id, edit_id), on the supplied transaction. It is O(touched elements)
// and rides the existing Phase-2 metaTx so the provenance commit is atomic with
// the cache/clear-dirty commit. Re-running the same edit (e.g. crash recovery
// replaying the original edit context) upserts identical rows, so the
// side-channel is idempotent under recovery.
func (i *Ingester) recordEditProvenanceTx(ctx context.Context, tx *sql.Tx, kind string, ids []string, prov EditProvenance) error {
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO edit_provenance (element_id, element_kind, edit_id, op_type, recorded_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(element_id, edit_id) DO UPDATE SET
	element_kind=excluded.element_kind,
	op_type=excluded.op_type,
	recorded_at=excluded.recorded_at`,
			id, kind, prov.EditID, string(prov.OpType), prov.Timestamp); err != nil {
			return fmt.Errorf("ingest: record edit provenance: %w", err)
		}
	}
	return nil
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

// RecoverWithRoot reprocesses dirty units relative to root. It reads the edit
// context persisted on each dirty row and replays the SAME provenance, so the
// edit_provenance side-channel ends in the identical state an uninterrupted edit
// would have produced (provenance-idempotent recovery). Dirty rows are grouped
// by their edit context: rows that carry a (edit_id, op_type, recorded_at) are
// re-ingested through the provenance-aware path; rows with no edit context
// (full-ingest leftovers) are re-ingested without provenance.
func (i *Ingester) RecoverWithRoot(ctx context.Context, root string) error {
	rows, err := i.meta.QueryContext(ctx, "SELECT path, edit_id, op_type, recorded_at FROM dirty_units")
	if err != nil {
		return fmt.Errorf("ingest: recover query dirty: %w", err)
	}
	defer rows.Close()

	type editKey struct {
		editID     string
		opType     string
		recordedAt int64
	}
	groups := make(map[editKey][]string)
	var order []editKey
	for rows.Next() {
		var p, editID, opType string
		var recordedAt int64
		if err := rows.Scan(&p, &editID, &opType, &recordedAt); err != nil {
			return fmt.Errorf("ingest: recover scan dirty: %w", err)
		}
		k := editKey{editID: editID, opType: opType, recordedAt: recordedAt}
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], p)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	if len(order) == 0 {
		return nil
	}

	// Deterministic replay order.
	sort.Slice(order, func(a, b int) bool {
		if order[a].editID != order[b].editID {
			return order[a].editID < order[b].editID
		}
		return order[a].recordedAt < order[b].recordedAt
	})

	for _, k := range order {
		paths := groups[k]
		if k.editID == "" {
			if err := i.IngestChanged(ctx, root, paths); err != nil {
				return err
			}
			continue
		}
		prov := EditProvenance{EditID: k.editID, OpType: EditOpType(k.opType), Timestamp: k.recordedAt}
		if err := i.IngestChangedWithProvenance(ctx, root, paths, prov); err != nil {
			return err
		}
	}
	return nil
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
		// SW-055 AC#6: fail-closed max-file-size bound. Check the size from the
		// directory entry's FileInfo BEFORE reading any bytes into memory, so a
		// multi-GB adversarial file is never even read. On breach the file is
		// SKIPPED with a structured, source-free diagnostic and the walk continues.
		if i.bounds.MaxFileSize > 0 {
			if info, ierr := d.Info(); ierr == nil && info.Size() > i.bounds.MaxFileSize {
				i.recordSkip(SkipDiagnostic{Path: rel, Reason: SkipOversize, Size: info.Size()})
				return nil
			}
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

// parseAndCommit parses one file, writes its nodes/intra-file edges to
// graphstore, and returns the node IDs, the edge IDs it committed (the
// side-channel edge key set), the list of files it references (forward refs),
// and the deferred link inputs (pending refs + imports) for the post-node-commit
// linker pass. Cross-file edges are NOT emitted here — they are emitted by
// linkFiles after every file's nodes are committed (the ordering constraint that
// motivated SW-050).
func (i *Ingester) parseAndCommit(ctx context.Context, u fileUnit) ([]string, []string, []string, *link.FileRefs, error) {
	// SW-055 AC#6: fail-closed parse timeout. Bound the wall-clock time a single
	// Parse may consume on untrusted input; on expiry the parse is abandoned.
	parseCtx := ctx
	if i.bounds.ParseTimeout > 0 {
		var cancel context.CancelFunc
		parseCtx, cancel = context.WithTimeout(ctx, time.Duration(i.bounds.ParseTimeout))
		defer cancel()
	}
	res, err := i.parser.Parse(parseCtx, u.relPath, u.src)
	if err != nil {
		// Fail closed on a resource-bound breach: SKIP the file with a structured,
		// source-free diagnostic and continue ingestion (never parse-anyway / never
		// truncate). A genuine parse error (invalid syntax, etc.) still aborts as
		// before — only the three bound sentinels route to the skip path.
		switch {
		case errors.Is(err, parse.ErrMaxDepthExceeded):
			i.recordSkip(SkipDiagnostic{Path: u.relPath, Reason: SkipMaxDepth})
			return nil, nil, nil, nil, nil
		case errors.Is(err, parse.ErrParseTimeout) ||
			(i.bounds.ParseTimeout > 0 && parseCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil):
			i.recordSkip(SkipDiagnostic{Path: u.relPath, Reason: SkipTimeout})
			return nil, nil, nil, nil, nil
		case errors.Is(err, parse.ErrFileTooLarge):
			i.recordSkip(SkipDiagnostic{Path: u.relPath, Reason: SkipOversize})
			return nil, nil, nil, nil, nil
		}
		return nil, nil, nil, nil, fmt.Errorf("ingest: parse %s: %w", u.relPath, err)
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
		return nil, nil, nil, nil, err
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
			return nil, nil, nil, nil, fmt.Errorf("ingest: delete stale node %s: %w", id, err)
		}
	}

	for _, n := range res.Nodes {
		if err := i.store.PutNode(ctx, n); err != nil {
			return nil, nil, nil, nil, fmt.Errorf("ingest: put node: %w", err)
		}
		nodeIDs = append(nodeIDs, string(n.ID()))
	}
	edgeIDs := make([]string, 0, len(res.Edges))
	for _, e := range res.Edges {
		if err := i.store.PutEdge(ctx, e); err != nil {
			return nil, nil, nil, nil, fmt.Errorf("ingest: put edge: %w", err)
		}
		edgeIDs = append(edgeIDs, string(e.ID()))
	}

	// Capture the linker inputs for the post-node-commit pass. They are non-nil
	// only when the parser recorded deferred refs/imports (the real Go parser);
	// the stub parsers leave them empty, so the linker is a no-op for them.
	var fr *link.FileRefs
	if len(res.PendingRefs) > 0 || len(res.Imports) > 0 {
		fr = &link.FileRefs{
			SourcePath: model.NormalizePath(u.relPath),
			Dir:        posixDir(model.NormalizePath(u.relPath)),
			Language:   res.Meta.Language,
			Pending:    res.PendingRefs,
			Imports:    res.Imports,
		}
	}

	// Forward refs = paths this file imports/uses. For the stub parser this is
	// supplied in the parse result; a real parser derives it from imports.
	return nodeIDs, edgeIDs, res.References, fr, nil
}

// posixDir returns the directory portion of a normalized POSIX path; the repo
// root maps to "" (mirrors engine/link's directory key).
func posixDir(p string) string {
	d := filepath.ToSlash(filepath.Dir(p))
	if d == "." {
		return ""
	}
	return d
}

// linkFiles is the post-node-commit linker pass (SW-050). It is called once per
// ingest run AFTER every (re)processed file's nodes are committed, so the
// ordering constraint that previously dropped cross-file edges no longer applies:
// the symbol index is built from the FULL committed node set (store.Nodes) and
// the linker resolves every deferred ref against it.
//
// fileRefs are the link inputs of the (re)processed files. ownedNodeIDs is the
// set of node IDs belonging to those files. parserEdges is the set of edge IDs
// parseAndCommit just (re)committed for those files THIS pass (its res.Edges:
// defines + any edge a parser resolves itself, including cross-file edges some
// parsers emit directly); they are current-by-construction and must be kept.
//
// The sweep removes STALE from-owned linker-kind edges before re-linking: a
// calls/references/imports edge whose From is owned but which was NOT
// (re)committed by parseAndCommit this pass is deleted, then the linker re-emits
// the still-valid ones. Deleting even when To is also owned (BLOCK-2) is required
// because an identity-preserving caller edit keeps the From NodeId, so
// DeleteNode's incident-edge cascade never fires and the stale edge would
// otherwise survive incrementally while being absent from a full pass. Keying the
// keep-set on the freshly-committed parser edges (rather than on To-ownership)
// preserves intra-file AND parser-emitted cross-file edges — only the linker's
// own deferred edges, which the linker re-emits, are swept. This makes the
// incremental result converge byte-identically with a full re-index.
//
// It returns the committed cross-file edge IDs so the incremental path can record
// edit provenance for them.
func (i *Ingester) linkFiles(ctx context.Context, fileRefs []link.FileRefs, ownedNodeIDs map[string]struct{}, parserEdges map[string]struct{}) ([]string, error) {
	// Nothing reprocessed: no nodes to sweep stale edges from and nothing to
	// re-link. (BLOCK-2: gating on ownedNodeIDs, NOT fileRefs — an edit that
	// removes the LAST cross-file ref leaves fileRefs empty yet still owns
	// reprocessed nodes whose stale from-owned cross-file edges must be swept.
	// Returning early on empty fileRefs skipped that sweep and let the stale edge
	// survive.)
	if len(ownedNodeIDs) == 0 {
		return nil, nil
	}

	nodes, err := i.store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return nil, fmt.Errorf("ingest: link read nodes: %w", err)
	}

	// Remove stale from-owned linker edges before re-linking.
	allEdges, err := i.store.Edges(ctx, graphstore.Query{})
	if err != nil {
		return nil, fmt.Errorf("ingest: link read edges: %w", err)
	}
	for _, e := range allEdges {
		if _, fromOwned := ownedNodeIDs[string(e.From())]; !fromOwned {
			continue
		}
		// Only the linker's own edge kinds are swept here.
		if e.Kind() != "calls" && e.Kind() != "references" && e.Kind() != "imports" &&
			e.Kind() != "implements" && e.Kind() != "inherits" && e.Kind() != "overrides" {
			continue
		}
		// Keep any edge parseAndCommit just (re)committed for these files this pass
		// — it is current, not stale. This covers intra-file edges AND any
		// cross-file edge a parser resolves itself (res.Edges). Everything else
		// from-owned of a linker kind is a stale linker edge from a prior pass.
		if _, fresh := parserEdges[string(e.ID())]; fresh {
			continue
		}
		if err := i.store.DeleteEdge(ctx, e.ID()); err != nil {
			return nil, fmt.Errorf("ingest: delete stale cross-file edge %s: %w", e.ID(), err)
		}
	}

	idx := link.BuildIndex(nodes)

	// FU-5: dispatch the linker per language. Group the (re)processed files by
	// their Language and call Link once per language against the SHARED index
	// (cross-file resolution is intra-language, but the index spans the whole
	// committed node set). Languages are visited in sorted order and edges are
	// keyed by content-derived EdgeId in the store, so the result is independent
	// of grouping/iteration order. A file whose Language has no registered resolver
	// is a no-op (Link returns no edges), exactly as before for non-Go files.
	byLang := map[string][]link.FileRefs{}
	for _, fr := range fileRefs {
		lang := fr.Language
		if lang == "" {
			lang = "go" // FU-1 default: untagged refs are Go (back-compat).
		}
		byLang[lang] = append(byLang[lang], fr)
	}
	langs := make([]string, 0, len(byLang))
	for lang := range byLang {
		langs = append(langs, lang)
	}
	sort.Strings(langs)

	edgeIDs := make([]string, 0)
	for _, lang := range langs {
		edges, _, err := i.linker.Link(lang, byLang[lang], idx)
		if err != nil {
			return nil, fmt.Errorf("ingest: link %s: %w", lang, err)
		}
		for _, e := range edges {
			if err := i.store.PutEdge(ctx, e); err != nil {
				return nil, fmt.Errorf("ingest: link put edge %s: %w", e.ID(), err)
			}
			edgeIDs = append(edgeIDs, string(e.ID()))
		}
	}
	return edgeIDs, nil
}

// allCachedNodeIDsTx returns every node ID recorded in the cache (across all
// files), on the supplied transaction. Used by IngestAll to purge nodes that a
// re-index no longer produces.
func (i *Ingester) allCachedNodeIDsTx(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT node_ids FROM file_content_cache")
	if err != nil {
		return nil, fmt.Errorf("ingest: list cached node ids: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var ids []string
		if err := json.Unmarshal([]byte(raw), &ids); err != nil {
			return nil, fmt.Errorf("ingest: decode cached node ids: %w", err)
		}
		out = append(out, ids...)
	}
	return out, rows.Err()
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
func (i *Ingester) removeFileTx(ctx context.Context, tx *sql.Tx, path string) error {
	// Drop the file's graph nodes first (DeleteNode cascades incident edges).
	ids, err := i.cachedNodeIDs(ctx, path)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := i.store.DeleteNode(ctx, model.NodeId(id)); err != nil {
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
