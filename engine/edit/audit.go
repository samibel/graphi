package edit

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/samibel/graphi/engine/ingest"
)

// MarshalChangeRecord is the single canonical serializer for a ChangeRecord,
// shared by every surface so the MCP and CLI emit byte-identical bytes for the
// same record (parity by construction). Field order is fixed by struct-tag
// declaration order; HTML escaping is disabled; the trailing newline is trimmed;
// TouchedFiles is sorted defensively. It mirrors engine/query.Marshal.
func MarshalChangeRecord(rec ChangeRecord) ([]byte, error) {
	tf := append([]string(nil), rec.TouchedFiles...)
	sort.Strings(tf)
	rec.TouchedFiles = tf
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rec); err != nil {
		return nil, fmt.Errorf("edit: marshal change record: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// MarshalRefactorResult is the canonical serializer for a RefactorResult preview
// (the AC-1 impact set returned BEFORE mutation). ImpactFiles/TouchedFiles are
// already sorted by the saga; HTML escaping is disabled and the trailing newline
// trimmed for byte-stability across surfaces.
func MarshalRefactorResult(res RefactorResult) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(res); err != nil {
		return nil, fmt.Errorf("edit: marshal refactor result: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// ChangeRecord is the durable, auditable record of one applied edit/refactor (or
// one undo reversal) emitted by the SW-038 command surface. It is the AC-2
// artifact: operation, target, before/after, actor, timestamp, and an undo token.
//
// "before/after" is captured as: before = SnapshotRef (the persisted pre-edit
// graph snapshot) + OldName; after = the committed graph + NewName. Element-level
// deltas remain available via the edit_provenance side-channel joined on EditID.
//
// The record is persisted in the change_record table in the ingest-meta SQLite
// sidecar (a sibling of edit_provenance) — NEVER in core/graphstore, because an
// audit table in the graph store would poison the marshalled-graph digest the
// consistency check and AC-1 byte-identity invariant compare.
type ChangeRecord struct {
	// EditID is the saga-minted identifier of the originating edit (= Result.EditID
	// for an applied edit; a freshly minted id for an undo reversal). PRIMARY KEY.
	EditID string `json:"edit_id"`
	// OpType is the operation kind (rename/extract/move/signature_change/undo).
	OpType string `json:"op_type"`
	// TargetNodeID is the resolved NodeId the operation targeted.
	TargetNodeID string `json:"target_node_id"`
	// OldName / NewName are the human-readable before/after spellings.
	OldName string `json:"old_name,omitempty"`
	NewName string `json:"new_name,omitempty"`
	// TouchedFiles is the set of repo-relative source files the edit re-indexed.
	TouchedFiles []string `json:"touched_files"`
	// Actor is the surface-supplied request identity (e.g. "mcp"/"cli" or a
	// caller-supplied principal). RECORDED but EXCLUDED from the AC-4 parity
	// comparable subset.
	Actor string `json:"actor"`
	// RecordedAt is the Unix-nanosecond instant the edit was committed, taken from
	// the saga clock seam (SetClock) so tests can pin it.
	RecordedAt int64 `json:"recorded_at"`
	// UndoToken is the unguessable, crypto-random token a later Undo presents to
	// reverse this edit. UNIQUE. Empty is never persisted.
	UndoToken string `json:"undo_token"`
	// SnapshotRef is the path to the persisted pre-edit graph snapshot (the
	// graph-rollback anchor undo loads). For an undo record it points at the
	// reversed edit's restore snapshot.
	SnapshotRef string `json:"snapshot_ref"`
	// ReversesEditID, when non-empty, is the EditID this record reversed (set only
	// on op_type=undo records). AC-3's "reversal recorded as its own entry".
	ReversesEditID string `json:"reverses_edit_id,omitempty"`
}

// ChangeRecorder owns the change_record audit table plus the durable undo store
// (persisted pre-edit graph snapshots + captured source bytes keyed by undo
// token). It writes into the SAME ingest-meta sidecar that holds edit_provenance,
// reusing the Ingester's meta handle (engine layer only; no surface reaches it).
//
// Durability: change_record is a SQLite table, so a record survives a process
// restart. The undo store (snapshots + source bytes) lives on disk under
// <metaDir>/undo/<undo_token>/, written atomically within the meta dir.
type ChangeRecorder struct {
	db      *sql.DB
	undoDir string
}

// undoSeq disambiguates two undo tokens minted within the same nanosecond.
var undoSeq uint64

// NewChangeRecorder constructs a recorder over the Ingester's meta sidecar. It
// creates the change_record table if absent (additive; sibling to
// edit_provenance) and the on-disk undo store under metaDir/undo. metaDir is the
// SAME directory passed to ingest.New; an empty metaDir uses an in-memory sidecar
// (the undo store is then placed under a temp dir, testing only).
func NewChangeRecorder(ctx context.Context, ing *ingest.Ingester, metaDir string) (*ChangeRecorder, error) {
	if ing == nil {
		return nil, fmt.Errorf("%w: nil ingester", ErrInvalidOp)
	}
	db := ing.MetaDB()
	if db == nil {
		return nil, fmt.Errorf("%w: ingester has no meta db", ErrInvalidOp)
	}
	undoDir := ""
	if strings.TrimSpace(metaDir) != "" {
		undoDir = filepath.Join(metaDir, "undo")
	} else {
		tmp, err := os.MkdirTemp("", "graphi-undo-store-*")
		if err != nil {
			return nil, fmt.Errorf("%w: create undo store: %v", ErrWrite, err)
		}
		undoDir = tmp
	}
	if err := os.MkdirAll(undoDir, 0o700); err != nil {
		return nil, fmt.Errorf("%w: create undo dir: %v", ErrWrite, err)
	}
	r := &ChangeRecorder{db: db, undoDir: undoDir}
	if err := r.initSchema(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

// initSchema creates the change_record table if it does not exist. It is additive
// and idempotent (CREATE TABLE IF NOT EXISTS), declared with its full SW-038
// shape; no later migration is required for this story.
func (r *ChangeRecorder) initSchema(ctx context.Context) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS change_record (
	edit_id TEXT PRIMARY KEY,
	op_type TEXT NOT NULL,
	target_node_id TEXT,
	old_name TEXT,
	new_name TEXT,
	touched_files TEXT NOT NULL,
	actor TEXT NOT NULL,
	recorded_at INTEGER NOT NULL,
	undo_token TEXT NOT NULL UNIQUE,
	snapshot_ref TEXT NOT NULL,
	reverses_edit_id TEXT
);`
	if _, err := r.db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("ingest: init change_record schema: %w", err)
	}
	return nil
}

// mintUndoToken returns an unguessable undo token using the SW-037 minting scheme
// (a process-monotonic sequence + crypto/rand bytes). It is NOT derived from the
// edit id, so an attacker cannot aim an undo at an arbitrary prior edit by
// guessing.
func mintUndoToken() (string, error) {
	var rnd [16]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", fmt.Errorf("%w: mint undo token: %v", ErrWrite, err)
	}
	seq := atomic.AddUint64(&undoSeq, 1)
	return fmt.Sprintf("undo_%06x_%s", seq&0xffffff, hex.EncodeToString(rnd[:])), nil
}

// snapshotPathFor returns the durable snapshot path for an undo token, confined
// to the undo store dir under a sanitized leaf (the token is minted internally
// and contains only [a-z0-9_], but we validate defensively against path escape).
func (r *ChangeRecorder) snapshotPathFor(token string) (string, error) {
	leaf := filepath.Base(filepath.Clean(token))
	if leaf == "." || leaf == ".." || strings.ContainsAny(token, "/\\") {
		return "", fmt.Errorf("%w: unsafe undo token %q", ErrInvalidOp, token)
	}
	dir := filepath.Join(r.undoDir, leaf)
	// Re-validate the joined dir stays within undoDir (defence in depth).
	rel, err := filepath.Rel(r.undoDir, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: undo token escapes store %q", ErrInvalidOp, token)
	}
	return dir, nil
}

// persistUndoArtifacts copies the saga's pre-edit graph snapshot to the durable
// undo store and writes the captured pre-edit source bytes there, keyed by undo
// token. It is called ONLY on a successful saga (the snapshot otherwise survives
// nowhere — the saga removes it on rollback, leaving no orphan). The snapshot and
// source bytes are written atomically within the meta dir under a sanitized path.
//
// Returns the durable snapshot path stored as ChangeRecord.SnapshotRef.
func (r *ChangeRecorder) persistUndoArtifacts(token, srcSnapshotPath string, originals map[string][]byte) (string, error) {
	dir, err := r.snapshotPathFor(token)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("%w: create undo entry dir: %v", ErrWrite, err)
	}
	// Copy the graph snapshot bytes into the durable store atomically.
	snapBytes, err := os.ReadFile(srcSnapshotPath) //nolint:gosec // saga-internal temp path
	if err != nil {
		return "", fmt.Errorf("%w: read pre-edit snapshot: %v", ErrWrite, err)
	}
	durableSnap := filepath.Join(dir, "pre-edit.snapshot")
	if err := writeFileAtomic(durableSnap, snapBytes); err != nil {
		return "", fmt.Errorf("%w: persist undo snapshot: %v", ErrWrite, err)
	}
	// Persist the captured pre-edit source bytes as a single JSON blob
	// (relPath -> base64-less raw bytes via json's []byte encoding) atomically.
	blob, err := json.Marshal(originals)
	if err != nil {
		return "", fmt.Errorf("%w: encode undo sources: %v", ErrWrite, err)
	}
	if err := writeFileAtomic(filepath.Join(dir, "sources.json"), blob); err != nil {
		return "", fmt.Errorf("%w: persist undo sources: %v", ErrWrite, err)
	}
	return durableSnap, nil
}

// loadUndoSources reads back the captured pre-edit source bytes for an undo token.
func (r *ChangeRecorder) loadUndoSources(token string) (map[string][]byte, error) {
	dir, err := r.snapshotPathFor(token)
	if err != nil {
		return nil, err
	}
	blob, err := os.ReadFile(filepath.Join(dir, "sources.json")) //nolint:gosec // sanitized path within undo store
	if err != nil {
		return nil, fmt.Errorf("%w: read undo sources: %v", ErrInvalidOp, err)
	}
	var originals map[string][]byte
	if err := json.Unmarshal(blob, &originals); err != nil {
		return nil, fmt.Errorf("%w: decode undo sources: %v", ErrInvalidOp, err)
	}
	return originals, nil
}

// recordTx inserts one change_record row on the supplied transaction. touched_files
// is stored as a JSON array. It is called within the saga's audit commit so a
// rolled-back edit (which never reaches this call) writes NO row.
func (r *ChangeRecorder) recordTx(ctx context.Context, tx *sql.Tx, rec ChangeRecord) error {
	tf, err := json.Marshal(rec.TouchedFiles)
	if err != nil {
		return fmt.Errorf("ingest: encode touched_files: %w", err)
	}
	var reverses any
	if rec.ReversesEditID != "" {
		reverses = rec.ReversesEditID
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO change_record
	(edit_id, op_type, target_node_id, old_name, new_name, touched_files, actor, recorded_at, undo_token, snapshot_ref, reverses_edit_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.EditID, rec.OpType, rec.TargetNodeID, rec.OldName, rec.NewName,
		string(tf), rec.Actor, rec.RecordedAt, rec.UndoToken, rec.SnapshotRef, reverses); err != nil {
		return fmt.Errorf("ingest: insert change_record: %w", err)
	}
	return nil
}

// record persists one change_record row in its own transaction.
func (r *ChangeRecorder) record(ctx context.Context, rec ChangeRecord) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ingest: begin change_record tx: %w", err)
	}
	if err := r.recordTx(ctx, tx, rec); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ChangeRecords returns every change_record row, sorted by (recorded_at, edit_id)
// so reads are deterministic. It reads the side-channel only — never the graph or
// the AC-1 structural digest. Mirrors the EditProvenance reader shape.
func (r *ChangeRecorder) ChangeRecords(ctx context.Context) ([]ChangeRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT edit_id, op_type, target_node_id, old_name, new_name, touched_files, actor, recorded_at, undo_token, snapshot_ref, reverses_edit_id
FROM change_record
ORDER BY recorded_at, edit_id`)
	if err != nil {
		return nil, fmt.Errorf("ingest: query change_record: %w", err)
	}
	defer rows.Close()
	var out []ChangeRecord
	for rows.Next() {
		rec, err := scanChangeRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// ChangeRecordByUndoToken returns the change_record for an undo token, or a
// not-found error. It is the lookup the Undo saga uses to recover the snapshot
// ref + captured source bytes.
func (r *ChangeRecorder) ChangeRecordByUndoToken(ctx context.Context, token string) (ChangeRecord, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT edit_id, op_type, target_node_id, old_name, new_name, touched_files, actor, recorded_at, undo_token, snapshot_ref, reverses_edit_id
FROM change_record
WHERE undo_token = ?`, token)
	rec, err := scanChangeRecord(row)
	if err == sql.ErrNoRows {
		return ChangeRecord{}, fmt.Errorf("%w: no change record for undo token", ErrInvalidOp)
	}
	if err != nil {
		return ChangeRecord{}, err
	}
	return rec, nil
}

// rowScanner abstracts *sql.Row and *sql.Rows so one scan helper serves both.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanChangeRecord(s rowScanner) (ChangeRecord, error) {
	var rec ChangeRecord
	var tf string
	var reverses sql.NullString
	if err := s.Scan(&rec.EditID, &rec.OpType, &rec.TargetNodeID, &rec.OldName, &rec.NewName,
		&tf, &rec.Actor, &rec.RecordedAt, &rec.UndoToken, &rec.SnapshotRef, &reverses); err != nil {
		return ChangeRecord{}, err
	}
	if err := json.Unmarshal([]byte(tf), &rec.TouchedFiles); err != nil {
		return ChangeRecord{}, fmt.Errorf("ingest: decode touched_files: %w", err)
	}
	if reverses.Valid {
		rec.ReversesEditID = reverses.String
	}
	return rec, nil
}
