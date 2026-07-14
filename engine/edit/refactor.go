package edit

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/samibel/graphi/engine/ingest"
)

// RefactorKind is the closed set of graph-aware refactor operations SW-036
// supports. Each is built on the same multi-op atomic saga (applyBatch); they
// differ only in how the EP-004 blast radius is turned into the []EditOp the saga
// applies (see planner.go).
type RefactorKind string

const (
	// RefactorRename changes a symbol's name at its definition and at EVERY
	// reference resolved through the dependency graph. The renamed node mints a
	// new NodeId (QualifiedName is an identity field); the old node is deleted by
	// the incremental re-index (Slice 1).
	RefactorRename RefactorKind = "rename"
	// RefactorExtract would pull a region into a new symbol. It is NOT
	// implemented: no extract planner exists, and the former fallback (a blind
	// OldName→NewName rewrite identical to rename) silently violated the
	// advertised semantics. It fail-closes with ErrNotImplemented before any
	// graph read or source write (SW-112 / SAFE-01).
	RefactorExtract RefactorKind = "extract"
	// RefactorMove would relocate a symbol to a different file. It is NOT
	// implemented: DestinationFile was never honored and the former fallback
	// rewrote names like rename. It fail-closes with ErrNotImplemented before
	// any graph read or source write (SW-112 / SAFE-01).
	RefactorMove RefactorKind = "move"
	// RefactorSignatureChange alters a function/method signature at its definition
	// and at every call site / dependent edge.
	RefactorSignatureChange RefactorKind = "signature_change"
)

// RefactorOp is the high-level, surface-facing request a refactor consumer
// (SW-038's MCP/CLI surface) constructs. It names the operation, the target
// symbol (a resolved NodeId), and the operation-specific payload, and never
// carries byte spans: the planner resolves the EP-004 blast radius into the
// concrete []EditOp the saga applies. The struct is shaped for SW-038 — a stable
// Kind enum, an optional DryRun preview that returns the computed impact set
// without mutating, and a Result carrying TouchedFiles and the reserved
// UndoToken — so the surface can wrap it without a breaking contract change.
type RefactorOp struct {
	// Kind selects the operation (rename/extract/move/signature-change).
	Kind RefactorKind
	// TargetSymbol is the resolved NodeId of the symbol being refactored. It is
	// the seed fed to EP-004 impact resolution to compute the blast radius.
	TargetSymbol string
	// OldName is the current spelling of the symbol as it appears in source. The
	// span-resolution planner uses it to locate the exact byte ranges to rewrite.
	OldName string
	// NewName is the replacement spelling (rename / signature-change). For move it
	// may be empty.
	NewName string
	// DestinationFile is the repo-relative destination path for a move. Unused by
	// the other kinds.
	DestinationFile string
	// DryRun, when true, computes and returns the impact set + the planned EditOps
	// WITHOUT mutating any source or graph. This is SW-038's preview seam.
	DryRun bool
}

// RefactorResult is the structured outcome of ApplyRefactor. It embeds the saga
// Result (Outcome / TouchedFiles / reserved UndoToken) and adds the EP-004 impact
// preview the surface (SW-038) needs: the resolved blast-radius files and whether
// the impact set was truncated at the analyzer cap. For a DryRun, Outcome is
// OutcomeApplied-shaped but NO mutation occurred (PlannedOps is populated and
// TouchedFiles lists what WOULD be edited).
type RefactorResult struct {
	Result
	// ImpactFiles is the de-duplicated, sorted set of source files in the EP-004
	// forward blast radius (definition file + dependents).
	ImpactFiles []string `json:"impact_files"`
	// Truncated reports the EP-004 impact set hit the analyzer's node cap, so the
	// blast radius may be incomplete; surfaces should warn rather than silently
	// under-edit.
	Truncated bool `json:"truncated,omitempty"`
	// PlannedOps is the concrete []EditOp the planner resolved. Populated for a
	// DryRun (preview) and otherwise informational.
	PlannedOps []EditOp `json:"-"`
	// DryRun echoes whether this was a preview (no mutation performed).
	DryRun bool `json:"dry_run,omitempty"`
}

// ApplyRefactor is the graph-aware refactor entry point: it resolves the EP-004
// blast radius for op.TargetSymbol, plans the concrete []EditOp via the
// span-resolution layer, and applies them all in ONE atomic multi-op saga
// (applyBatch) so every reference across every file is rewritten or none is.
// rename/signature-change mint a new NodeId for the target; the incremental
// re-index deletes the old node (Slice 1), so the result is byte-identical to a
// full re-index. extract/move are not implemented and are rejected with
// ErrNotImplemented before the blast-radius read (SW-112 / SAFE-01).
//
// When op.DryRun is set, ApplyRefactor computes the impact set + planned ops and
// returns them WITHOUT mutating any source or graph — the preview seam SW-038
// consumes before committing a refactor.
func (a *Applier) ApplyRefactor(ctx context.Context, op RefactorOp) (RefactorResult, error) {
	out := RefactorResult{Result: Result{TargetNodeID: op.TargetSymbol, Outcome: OutcomeRolledBack}, DryRun: op.DryRun}

	if err := validateRefactorOp(op); err != nil {
		return out, err
	}

	files, truncated, err := a.blastRadius(ctx, op.TargetSymbol)
	if err != nil {
		return out, err
	}
	out.ImpactFiles = files
	out.Truncated = truncated

	ops, err := a.planRefactor(op, files)
	if err != nil {
		return out, err
	}
	out.PlannedOps = ops

	if op.DryRun {
		// Preview only: report what WOULD be edited; perform no mutation.
		touched := make(map[string]struct{}, len(ops))
		for _, o := range ops {
			touched[o.FilePath] = struct{}{}
		}
		tf := make([]string, 0, len(touched))
		for f := range touched {
			tf = append(tf, f)
		}
		sort.Strings(tf)
		out.TouchedFiles = tf
		out.Outcome = OutcomeApplied // "preview computed"; no mutation happened
		return out, nil
	}

	// Map the refactor kind onto the closed ingest op-type enum; the saga mints
	// the edit id + timestamp once and threads the provenance through the
	// incremental ingest pass (SW-037).
	opType, err := opTypeForRefactorKind(op.Kind)
	if err != nil {
		return out, err
	}
	res, arts, err := a.applyBatch(ctx, ops, opType)
	out.Result = res
	out.Result.TargetNodeID = op.TargetSymbol
	if err != nil {
		return out, err
	}
	// On success applyBatch hands back the pre-edit snapshot + captured source
	// bytes (SW-038): they are NOT discarded here so an undo recorder can persist
	// them. When no recorder consumes them (a bare ApplyRefactor call) we discard
	// them now, preserving the original SW-036 success behavior (no snapshot leak).
	arts.discard()
	return out, nil
}

// sagaArtifacts carries the pre-edit rollback anchors a successful multi-op saga
// produces (SW-038): the on-disk graph snapshot and the captured pre-edit source
// bytes of every touched file, keyed by repo-relative path. They are the inputs
// an undo recorder persists keyed by an undo token. On rollback the saga itself
// removes the snapshot dir, so a faulted edit never yields artifacts.
type sagaArtifacts struct {
	snapDir   string
	snapPath  string
	originals map[string][]byte
}

// discard removes the snapshot dir. Called when no recorder consumes the
// artifacts, so a bare (non-recording) successful refactor leaves no orphan
// snapshot — preserving the pre-SW-038 behavior.
func (s *sagaArtifacts) discard() {
	if s != nil && s.snapDir != "" {
		_ = os.RemoveAll(s.snapDir)
	}
}

// planRefactor turns a high-level RefactorOp + its blast-radius files into the
// concrete []EditOp the saga applies. rename and signature-change rewrite
// occurrences of OldName→NewName across the blast radius via the span-resolution
// layer; the two differ in which identity field changes (and therefore which old
// node the re-index deletes), which is realized purely by the resulting source
// bytes. extract and move have no planner and fail closed here as a second line
// of defense — validateRefactorOp already rejects them before any graph read.
func (a *Applier) planRefactor(op RefactorOp, files []string) ([]EditOp, error) {
	switch op.Kind {
	case RefactorRename, RefactorSignatureChange:
		return planNameRewrite(a.root, files, op.OldName, op.NewName)
	case RefactorExtract, RefactorMove:
		return nil, notImplementedRefactor(op.Kind)
	default:
		return nil, fmt.Errorf("%w: unknown refactor kind %q", ErrInvalidOp, op.Kind)
	}
}

// notImplementedRefactor is the single typed rejection for the advertised-but-
// unimplemented refactor kinds (SW-112 / SAFE-01). Every surface (CLI, MCP,
// daemon) funnels through it, so the failure is identical everywhere and callers
// can match errors.Is(err, ErrNotImplemented).
func notImplementedRefactor(k RefactorKind) error {
	return fmt.Errorf("%w: %q is not implemented and fails closed — no source or graph mutation was performed (rename and signature_change are the supported kinds)", ErrNotImplemented, k)
}

// validateRefactorOp checks the structural invariants of a RefactorOp before any
// graph read or mutation. Unimplemented kinds (extract, move) are rejected here
// — before the blast-radius graph read — so a rejected request provably touches
// nothing. Operation-specific payload requirements (a non-empty NewName for the
// name-rewriting kinds) are enforced here so a malformed request fails fast and
// identically across surfaces.
func validateRefactorOp(op RefactorOp) error {
	if strings.TrimSpace(op.TargetSymbol) == "" {
		return fmt.Errorf("%w: empty target symbol", ErrInvalidOp)
	}
	switch op.Kind {
	case RefactorRename, RefactorSignatureChange:
		if strings.TrimSpace(op.OldName) == "" {
			return fmt.Errorf("%w: %s requires OldName", ErrInvalidOp, op.Kind)
		}
		if strings.TrimSpace(op.NewName) == "" {
			return fmt.Errorf("%w: %s requires NewName", ErrInvalidOp, op.Kind)
		}
	case RefactorExtract, RefactorMove:
		return notImplementedRefactor(op.Kind)
	default:
		return fmt.Errorf("%w: unknown refactor kind %q", ErrInvalidOp, op.Kind)
	}
	return nil
}

// faultBatchK names the multi-file fault seam: a forced failure on writing file
// K of N, exercising the all-or-nothing multi-file compensation (AC-3). It is
// distinct from the inherited single-file seams.
const FaultBatchFileK = "batch_file_k"

// SetBatchFaultHook arms a one-shot fault that makes the batch saga fail while
// writing the k-th (1-based) touched file. Test-only; mirrors SetFaultHook. A
// non-positive k clears the armed batch fault.
func (a *Applier) SetBatchFaultHook(k int) {
	a.batchFailK = k
}

// applyBatch is the multi-op atomic saga: it generalizes the single-file Apply
// saga to N files / N EditOps so a refactor that touches many files commits all
// of them or none. The unit of atomicity is the whole batch:
//
//  1. validate + resolve every op's path and pre-compute its new file content
//     (no side effects yet),
//  2. Snapshot the pre-edit graph once (graph rollback anchor),
//  3. capture the original bytes of EVERY touched file once (source anchors),
//  4. atomically write all touched files,
//  5. ONE IngestChanged(root, allRelPaths) — the signature is already multi-file
//     and cascades reverse-deps, so a single incremental pass covers the batch,
//  6. ONE consistency check (incremental == full) at the end, never per file,
//  7. success: discard; any failure: compensate ALL files (restore captured
//     bytes) + Load the pre-edit snapshot — no partial source or graph state.
//
// Multiple EditOps may target the same file (e.g. several references in one
// file); they are grouped per file and applied span-by-span from the END of the
// file backwards so earlier byte offsets stay valid as later spans are rewritten.
func (a *Applier) applyBatch(ctx context.Context, ops []EditOp, opType ingest.EditOpType) (Result, *sagaArtifacts, error) {
	res := Result{Outcome: OutcomeRolledBack}
	if len(ops) == 0 {
		return res, nil, fmt.Errorf("%w: empty refactor (no edit ops)", ErrInvalidOp)
	}

	// Step 1: validate every op and group by resolved file. No mutation yet, so a
	// validation failure needs no rollback.
	type fileEdit struct {
		absPath string
		relPath string
		ops     []EditOp
	}
	byFile := make(map[string]*fileEdit)
	var relOrder []string
	for _, op := range ops {
		rel, abs, err := a.resolvePath(op.FilePath)
		if err != nil {
			return res, nil, err
		}
		fe, ok := byFile[rel]
		if !ok {
			fe = &fileEdit{absPath: abs, relPath: rel}
			byFile[rel] = fe
			relOrder = append(relOrder, rel)
		}
		fe.ops = append(fe.ops, op)
	}
	sort.Strings(relOrder)

	// Pre-compute new content for every touched file and capture its originals.
	// Capturing here (before any write) makes the originals the rollback anchors.
	originals := make(map[string][]byte, len(byFile))
	newContents := make(map[string][]byte, len(byFile))
	for _, rel := range relOrder {
		fe := byFile[rel]
		original, err := os.ReadFile(fe.absPath) //nolint:gosec // abs sanitized within root
		if err != nil {
			return res, nil, fmt.Errorf("%w: read target %s: %v", ErrInvalidOp, rel, err)
		}
		originals[rel] = original
		newContent, err := applySpans(original, fe.ops)
		if err != nil {
			return res, nil, err
		}
		newContents[rel] = newContent
	}

	// Step 2: snapshot the pre-edit graph once (graph rollback anchor). The
	// snapshot dir is NOT unconditionally removed (SW-038): on rollback compensate
	// removes it; on success the artifacts are returned to the caller so an undo
	// recorder can persist the pre-edit snapshot + captured source bytes. A bare
	// (non-recording) caller discards the artifacts itself (sagaArtifacts.discard),
	// so the pre-SW-038 "no orphan snapshot on success" behavior is preserved.
	snapDir, err := os.MkdirTemp("", "graphi-refactor-snap-*")
	if err != nil {
		return res, nil, fmt.Errorf("%w: create snapshot dir: %v", ErrWrite, err)
	}
	snapPath := snapDir + string(os.PathSeparator) + "pre-refactor.snapshot"
	if err := a.store.Snapshot(ctx, snapPath); err != nil {
		_ = os.RemoveAll(snapDir)
		return res, nil, fmt.Errorf("%w: snapshot pre-refactor graph: %v", ErrWrite, err)
	}

	// written tracks files we have already overwritten, so compensation restores
	// exactly those (and any not-yet-written file is already at its original).
	written := make([]string, 0, len(relOrder))
	compensate := func(cause error) (Result, *sagaArtifacts, error) {
		// A faulted edit leaves NO durable undo artifacts: remove the snapshot dir
		// here so a rolled-back edit never strands an orphan snapshot (AC-2).
		defer os.RemoveAll(snapDir)
		var rbErrs []string
		for _, rel := range written {
			if rerr := writeFileAtomic(byFile[rel].absPath, originals[rel]); rerr != nil {
				rbErrs = append(rbErrs, fmt.Sprintf("restore %s: %v", rel, rerr))
			}
		}
		if rerr := a.store.Load(ctx, snapPath); rerr != nil {
			rbErrs = append(rbErrs, fmt.Sprintf("restore graph: %v", rerr))
		}
		if len(rbErrs) > 0 {
			return res, nil, fmt.Errorf("%w: %s (original fault: %v)", ErrRollback, strings.Join(rbErrs, "; "), cause)
		}
		return res, nil, cause
	}

	// Step 4: atomic write of all touched files, in deterministic order. The
	// fail-on-K seam fires while writing the k-th file (1-based).
	for idx, rel := range relOrder {
		if a.batchFailK > 0 && idx+1 == a.batchFailK {
			a.batchFailK = 0
			// Mark this file as written-attempted so compensation restores any
			// files already written before K; this file's write is skipped.
			return compensate(fmt.Errorf("%w: injected fault writing file %d of %d (%s)", ErrWrite, idx+1, len(relOrder), rel))
		}
		if err := writeFileAtomic(byFile[rel].absPath, newContents[rel]); err != nil {
			written = append(written, rel) // partial write possible; restore it too
			return compensate(fmt.Errorf("%w: %s: %v", ErrWrite, rel, err))
		}
		written = append(written, rel)
	}

	// Step 5: ONE incremental re-index over all touched files (+ cascade).
	if a.fault == faultDuringReindex {
		a.setFault(faultNone)
		return compensate(fmt.Errorf("%w: injected re-index fault", ErrReindex))
	}
	prov, err := a.newEditProvenance(opType)
	if err != nil {
		return compensate(err)
	}
	if err := a.ingester.IngestChangedWithProvenance(ctx, a.root, relOrder, prov); err != nil {
		return compensate(fmt.Errorf("%w: %v", ErrReindex, err))
	}
	res.TouchedFiles = relOrder
	res.EditID = prov.EditID

	// Step 6: ONE consistency gate at the end of the batch (never per file).
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

	// Step 7: commit. Hand the pre-edit snapshot + captured originals to the
	// caller as undo artifacts (SW-038); the caller persists or discards them.
	res.Outcome = OutcomeApplied
	return res, &sagaArtifacts{snapDir: snapDir, snapPath: snapPath, originals: originals}, nil
}

// applySpans applies all ops for ONE file. The ops are sorted by descending
// Start so each replacement leaves the byte offsets of the not-yet-applied
// (earlier) spans valid. Overlapping spans within a single file are rejected as
// an invalid op, since their combined result would be order-dependent.
func applySpans(src []byte, ops []EditOp) ([]byte, error) {
	if len(ops) == 1 {
		return applySpan(src, ops[0].ByteSpan, ops[0].Replacement)
	}
	sorted := make([]EditOp, len(ops))
	copy(sorted, ops)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ByteSpan.Start > sorted[j].ByteSpan.Start })
	// Reject overlaps: after sorting by descending Start, each span's End must not
	// exceed the next (lower-Start) span's Start.
	for i := 0; i+1 < len(sorted); i++ {
		if sorted[i+1].ByteSpan.End > sorted[i].ByteSpan.Start {
			return nil, fmt.Errorf("%w: overlapping edit spans [%d,%d) and [%d,%d) in one file",
				ErrInvalidOp, sorted[i+1].ByteSpan.Start, sorted[i+1].ByteSpan.End,
				sorted[i].ByteSpan.Start, sorted[i].ByteSpan.End)
		}
	}
	out := src
	for _, op := range sorted {
		next, err := applySpan(out, op.ByteSpan, op.Replacement)
		if err != nil {
			return nil, err
		}
		out = next
	}
	return out, nil
}
