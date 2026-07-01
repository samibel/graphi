// Package edit implements graphi's atomic, graph-consistent source-edit
// primitive: the safe write path that every higher-level refactor (rename,
// extract, move — SW-036+) and the MCP/CLI surface (SW-038) build on.
//
// Scope (SW-035, refinement option-b): identity-preserving span replacement
// only. An EditOp replaces a byte span in one source file with new bytes that do
// NOT change the identity (Kind + QualifiedName + normalized SourcePath) of the
// targeted node. Because identity is preserved, the incremental re-index of the
// touched file produces no orphaned nodes/edges, so an incremental update
// converges byte-for-byte with a full re-index — which is exactly AC-3's
// invariant. Adding graphstore node/edge delete semantics and non-identity edits
// is a documented fast-follow required by SW-036; see docs/edit/atomic-edit.md.
//
// Atomicity: Apply runs a saga across three independent durability domains — the
// filesystem (source bytes), the graphstore (SQLite graph), and the ingest-meta
// SQLite sidecar (content cache, reverse-deps, dirty flags). There is no shared
// transaction spanning all three, so atomicity is achieved by capture-and-
// compensate: snapshot the pre-edit graph, capture the original source bytes,
// apply the source write, re-index, and verify; on ANY failure compensate in
// reverse (restore the original source bytes atomically, then Load the pre-edit
// graph snapshot), leaving no partial mutation. On success the snapshot is
// discarded. A crash mid-edit is recoverable via the EP-001 dirty-flag +
// (*ingest.Ingester).RecoverWithRoot path.
//
// Concurrency: single-writer-per-repo (config writing.default_mode:
// single_writer). The saga is NOT atomic against a concurrent edit or concurrent
// ingest; callers must serialize edits per repository.
//
// Layering: edit is an engine package. It depends DOWN only — onto
// core/graphstore, core/model, and engine/ingest — and is never reached from the
// MCP/CLI surface in this story. Security: every source write is path-sanitized
// to the repo root and written atomically (temp + fsync + rename); no
// eval/exec/shell and no outbound network.
package edit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/ingest"
)

// Outcome is the explicit terminal status of an Apply call. It mirrors the
// query package's Outcome convention so every EP-006 consumer (SW-036 refactors,
// SW-038 surface) reads a stable, surface-agnostic marker rather than inspecting
// errors. Applied and RolledBack are both non-partial terminal states.
type Outcome string

const (
	// OutcomeApplied — the source write and incremental re-index both landed and
	// the consistency check passed. The edit is committed.
	OutcomeApplied Outcome = "applied"
	// OutcomeRolledBack — the edit failed partway and was fully compensated: the
	// source bytes and the graph were restored to their exact pre-edit state. No
	// partial mutation survives. The accompanying error explains the fault.
	OutcomeRolledBack Outcome = "rolled_back"
)

// EditOp is the validated input contract for a single atomic edit. It is the
// type every EP-006 consumer constructs, so its field names are stable and it
// carries its own byte span: core/model.Node exposes only Line()/Column() and no
// byte offset, so the resolved span must be supplied by the caller (typically
// derived from the parse/resolve layer in SW-036).
//
// The edit is identity-preserving: Replacement must not change the targeted
// node's Kind, QualifiedName, or normalized SourcePath. Enforcing that semantic
// is the caller's responsibility (and SW-036's resolve layer); this primitive
// enforces the structural invariants (valid span, in-root path, atomic write,
// commit-or-rollback).
type EditOp struct {
	// TargetNodeID is the resolved node the edit targets. It is recorded on the
	// Result for provenance and consumed by SW-036's identity-preservation check;
	// this primitive does not resolve it.
	TargetNodeID string
	// FilePath is the repo-relative (or absolute-within-root) path of the source
	// file to edit. It is sanitized against the repo root before any write.
	FilePath string
	// ByteSpan is the [Start,End) byte range in the current file content to
	// replace. Start==End is a pure insertion; an empty Replacement over a
	// non-empty span is a deletion. Offsets are validated against the file length.
	ByteSpan Span
	// Replacement is the bytes written in place of ByteSpan. May be empty.
	Replacement []byte
}

// Span is a half-open [Start,End) byte range within a source file.
type Span struct {
	Start int
	End   int
}

// Result is the structured outcome of Apply. It mirrors the query result
// conventions and reserves room for SW-038's undo token without a breaking
// change. TouchedFiles lists the repo-relative paths re-indexed (the edited file
// plus its reverse-dependency cascade), in the order ingest reports them.
type Result struct {
	Outcome      Outcome  `json:"outcome"`
	TargetNodeID string   `json:"target_node_id"`
	TouchedFiles []string `json:"touched_files"`
	// EditID is the saga-minted identifier of this edit (SW-037). It is the key
	// under which the edit_provenance side-channel records every affected
	// NodeId/EdgeId, and is surfaced here so SW-038's audit/undo can reference the
	// edit without a breaking contract change. Empty for a rolled-back edit (no
	// provenance was committed).
	EditID string `json:"edit_id,omitempty"`
	// UndoToken is reserved for SW-038. It is empty in SW-035; the field exists
	// now so adding undo later is not a breaking contract change.
	UndoToken string `json:"undo_token,omitempty"`
}

// Errors returned by Apply. They are typed sentinels so callers and tests can
// match with errors.Is and distinguish the three AC-2 fault modes.
var (
	// ErrInvalidOp is returned for a structurally invalid EditOp (bad span, empty
	// path, span out of range) before any mutation is attempted.
	ErrInvalidOp = errors.New("edit: invalid edit op")
	// ErrWrite is returned when the atomic source write fails. The graph is
	// untouched; nothing to roll back on the graph side.
	ErrWrite = errors.New("edit: source write failed")
	// ErrReindex is returned when the incremental re-index fails (e.g. a parse
	// failure on the edited file). Source and graph are rolled back.
	ErrReindex = errors.New("edit: re-index failed")
	// ErrInconsistent is returned when the post-edit consistency check finds the
	// incremental graph diverges from a full re-index. Source and graph are
	// rolled back.
	ErrInconsistent = errors.New("edit: post-edit consistency check failed")
	// ErrRollback wraps a failure encountered WHILE compensating. It is the
	// highest-severity case (the store may be left in an indeterminate state) and
	// is surfaced explicitly so callers never mistake it for a clean rollback.
	ErrRollback = errors.New("edit: rollback failed")
)

// faultStage names a saga step at which a test fault can be injected.
type faultStage int

const (
	faultNone faultStage = iota
	// faultAfterSourceWrite injects a failure after the atomic source write but
	// before re-index — exercises AC-2's "write error" rollback (source already
	// changed on disk, graph untouched).
	faultAfterSourceWrite
	// faultDuringReindex makes IngestChanged appear to fail — exercises AC-2's
	// "parse failure" rollback path through the saga without needing a broken
	// parser (a broken parser is also tested directly).
	faultDuringReindex
	// faultConsistencyCheck forces the consistency check to report divergence —
	// exercises AC-2's "index inconsistency" rollback.
	faultConsistencyCheck
)

// Applier orchestrates atomic edits against one repository. It owns the
// authoritative graphstore and the EP-001 Ingester. Construct one per repo;
// it assumes single-writer access (see package doc).
type Applier struct {
	store    graphstore.Graphstore
	ingester *ingest.Ingester
	root     string
	// fullStore builds a throwaway full re-index for the consistency check. It is
	// a fresh store per check (created via newConsistencyChecker).
	checker ConsistencyChecker

	// fault is a one-shot test seam, mirroring ingest.SetFailAfterDirtyMarkHook.
	fault faultStage

	// batchFailK is the multi-file fault seam: when >0, the batch saga fails while
	// writing the k-th (1-based) touched file, exercising all-or-nothing
	// multi-file compensation. Consumed by SetBatchFaultHook/applyBatch.
	batchFailK int

	// clock is the SW-037 provenance clock seam. When nil, time.Now().UTC() is
	// used; tests inject a deterministic clock to pin recorded timestamps.
	clock func() time.Time
}

// SetClock injects a deterministic clock for edit-provenance timestamps.
// Test-only; when unset the saga uses time.Now().UTC().
func (a *Applier) SetClock(fn func() time.Time) { a.clock = fn }

// NewApplier constructs an Applier for the repository rooted at root. store is
// the authoritative graphstore that ingester writes to; both must already be
// populated by an initial IngestAll. checker performs the byte-identical
// incremental-vs-full consistency check after each edit (see consistency.go);
// pass nil to use the default checker.
func NewApplier(store graphstore.Graphstore, ingester *ingest.Ingester, root string, checker ConsistencyChecker) (*Applier, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: nil store", ErrInvalidOp)
	}
	if ingester == nil {
		return nil, fmt.Errorf("%w: nil ingester", ErrInvalidOp)
	}
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("%w: empty root", ErrInvalidOp)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve root: %v", ErrInvalidOp, err)
	}
	return &Applier{
		store:    store,
		ingester: ingester,
		root:     filepath.Clean(abs),
		checker:  checker,
	}, nil
}

func (a *Applier) setFault(stage faultStage) { a.fault = stage }

// Fault stage identifiers for the exported test seam. They name the three AC-2
// rollback modes a test can inject deterministically, mirroring
// (*ingest.Ingester).SetFailAfterDirtyMarkHook.
const (
	// FaultAfterSourceWrite injects a failure after the atomic source write but
	// before re-index (AC-2 "write error" rollback).
	FaultAfterSourceWrite = "after_source_write"
	// FaultDuringReindex injects a re-index failure (AC-2 "parse failure" path).
	FaultDuringReindex = "during_reindex"
	// FaultConsistencyCheck forces the consistency gate to report divergence
	// (AC-2 "index inconsistency" rollback).
	FaultConsistencyCheck = "consistency_check"
)

// SetFaultHook arms a one-shot fault at the named saga stage. Test-only; mirrors
// (*ingest.Ingester).SetFailAfterDirtyMarkHook. The fault is consumed by the next
// Apply call. An unknown or empty name clears any armed fault.
func (a *Applier) SetFaultHook(stage string) {
	switch stage {
	case FaultAfterSourceWrite:
		a.setFault(faultAfterSourceWrite)
	case FaultDuringReindex:
		a.setFault(faultDuringReindex)
	case FaultConsistencyCheck:
		a.setFault(faultConsistencyCheck)
	default:
		a.setFault(faultNone)
	}
}

// Apply executes the atomic edit saga for op and returns a Result. On success
// the Result.Outcome is OutcomeApplied and err is nil. On any failure the source
// bytes and the graph are compensated back to their exact pre-edit state, the
// Result.Outcome is OutcomeRolledBack, and a typed error (ErrWrite / ErrReindex /
// ErrInconsistent, or ErrRollback if compensation itself failed) is returned.
//
// Saga order (each step is reversible by the one before it):
//
//	1. validate op            (no side effects)
//	2. Snapshot pre-edit graph to a temp file       — graph rollback anchor
//	3. capture original source bytes                — source rollback anchor
//	4. atomic source write (temp + fsync + rename)  — the source mutation
//	5. IngestChanged over the edited file + cascade — the graph mutation
//	6. consistency check (incremental == full)      — the invariant gate
//	7. success: discard snapshot
//	   failure of 4/5/6: compensate in reverse (restore source, Load snapshot)
func (a *Applier) Apply(ctx context.Context, op EditOp) (Result, error) {
	res := Result{TargetNodeID: op.TargetNodeID, Outcome: OutcomeRolledBack}

	// Step 1: validate. Sanitize the path to the repo root and resolve the
	// absolute on-disk path. No mutation has happened yet, so a validation
	// failure needs no rollback.
	relPath, absPath, err := a.resolvePath(op.FilePath)
	if err != nil {
		return res, err
	}
	original, err := os.ReadFile(absPath) //nolint:gosec // absPath is sanitized within root
	if err != nil {
		return res, fmt.Errorf("%w: read target %s: %v", ErrInvalidOp, relPath, err)
	}
	newContent, err := applySpan(original, op.ByteSpan, op.Replacement)
	if err != nil {
		return res, err
	}

	// Step 2: snapshot the pre-edit graph. This is the graph rollback anchor.
	snapDir, err := os.MkdirTemp("", "graphi-edit-snap-*")
	if err != nil {
		return res, fmt.Errorf("%w: create snapshot dir: %v", ErrWrite, err)
	}
	defer os.RemoveAll(snapDir)
	snapPath := filepath.Join(snapDir, "pre-edit.snapshot")
	if err := a.store.Snapshot(ctx, snapPath); err != nil {
		return res, fmt.Errorf("%w: snapshot pre-edit graph: %v", ErrWrite, err)
	}

	// Step 3 + 4: capture original bytes (already in `original`) and write the new
	// content atomically. compensate() restores `original` and Loads the snapshot.
	compensate := func(cause error) (Result, error) {
		var rbErrs []string
		if rerr := writeFileAtomic(absPath, original); rerr != nil {
			rbErrs = append(rbErrs, fmt.Sprintf("restore source: %v", rerr))
		}
		if rerr := a.store.Load(ctx, snapPath); rerr != nil {
			rbErrs = append(rbErrs, fmt.Sprintf("restore graph: %v", rerr))
		}
		if len(rbErrs) > 0 {
			return res, fmt.Errorf("%w: %s (original fault: %v)", ErrRollback, strings.Join(rbErrs, "; "), cause)
		}
		return res, cause
	}

	if err := writeFileAtomic(absPath, newContent); err != nil {
		// Source write failed: nothing landed on disk under the final name (atomic
		// rename), the graph is untouched. No compensation needed beyond returning.
		return res, fmt.Errorf("%w: %s: %v", ErrWrite, relPath, err)
	}

	// Injected fault: source is written, graph not yet re-indexed. Compensate.
	if a.takeFault() == faultAfterSourceWrite {
		return compensate(fmt.Errorf("%w: injected fault after source write", ErrWrite))
	}

	// Step 5: incremental re-index of the edited file + its reverse-dep cascade,
	// carrying the per-edit provenance. The edit id is minted ONCE and the
	// timestamp captured ONCE here in the saga (a single value for the whole
	// touched set); ingest records it against every affected NodeId/EdgeId in the
	// edit_provenance side-channel, atomically in Phase 2 of the dirty-flag tx.
	if a.fault == faultDuringReindex {
		a.setFault(faultNone)
		return compensate(fmt.Errorf("%w: injected re-index fault", ErrReindex))
	}
	prov, err := a.newEditProvenance(ingest.EditOpApply)
	if err != nil {
		return compensate(err)
	}
	// A tolerant ingest no longer aborts a FULL index on a parse/syntax error, but
	// the incremental IngestChanged path still returns a hard error when a file it
	// was asked to reprocess is unparseable (see ingestChanged's SkipParseError
	// elevation). So an edit that produces source the parser rejects surfaces here
	// as a re-index error and rolls back atomically — the meta DB transaction is
	// rolled back too, keeping it consistent with the compensated graphstore.
	if err := a.ingester.IngestChangedWithProvenance(ctx, a.root, []string{relPath}, prov); err != nil {
		return compensate(fmt.Errorf("%w: %v", ErrReindex, err))
	}
	res.TouchedFiles = []string{relPath}
	res.EditID = prov.EditID

	// Step 6: consistency gate — the incrementally-updated graph must be
	// byte-identical to a full re-index of the post-edit source.
	if a.fault == faultConsistencyCheck {
		a.setFault(faultNone)
		return compensate(fmt.Errorf("%w: injected inconsistency", ErrInconsistent))
	}
	checker := a.checker
	if checker == nil {
		checker = DefaultConsistencyChecker
	}
	if err := checker.Check(ctx, a.store, a.ingester, a.root); err != nil {
		return compensate(fmt.Errorf("%w: %v", ErrInconsistent, err))
	}

	// Step 7: commit. The defer removes the snapshot dir.
	res.Outcome = OutcomeApplied
	return res, nil
}

// resolvePath sanitizes p against the repo root and returns the repo-relative
// POSIX path plus the cleaned absolute on-disk path. It enforces the security
// invariant that no write escapes the repo root (mirrors ingest.sanitizePath).
func (a *Applier) resolvePath(p string) (rel string, abs string, err error) {
	if strings.TrimSpace(p) == "" {
		return "", "", fmt.Errorf("%w: empty file path", ErrInvalidOp)
	}
	if !filepath.IsAbs(p) {
		abs = filepath.Join(a.root, filepath.FromSlash(p))
	} else {
		abs = filepath.Clean(p)
	}
	rel, rerr := filepath.Rel(a.root, abs)
	if rerr != nil {
		return "", "", fmt.Errorf("%w: path outside root: %v", ErrInvalidOp, rerr)
	}
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", "", fmt.Errorf("%w: escaped path %q", ErrInvalidOp, p)
	}
	return rel, abs, nil
}

func (a *Applier) takeFault() faultStage {
	f := a.fault
	if f == faultAfterSourceWrite {
		a.fault = faultNone
	}
	return f
}

// applySpan returns src with span replaced by repl. It validates the span lies
// within [0,len(src)] and that Start<=End, returning ErrInvalidOp otherwise. The
// result is a fresh slice; src is not mutated, preserving it as the rollback
// anchor.
func applySpan(src []byte, span Span, repl []byte) ([]byte, error) {
	if span.Start < 0 || span.End < 0 || span.Start > span.End {
		return nil, fmt.Errorf("%w: invalid span [%d,%d)", ErrInvalidOp, span.Start, span.End)
	}
	if span.End > len(src) {
		return nil, fmt.Errorf("%w: span end %d exceeds file length %d", ErrInvalidOp, span.End, len(src))
	}
	out := make([]byte, 0, len(src)-(span.End-span.Start)+len(repl))
	out = append(out, src[:span.Start]...)
	out = append(out, repl...)
	out = append(out, src[span.End:]...)
	return out, nil
}
