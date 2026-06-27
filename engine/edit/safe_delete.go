package edit

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/ingest"
)

// referencingEdgeKinds are the edge kinds that count as a LIVE reference for the
// safe-delete gate. An inbound edge of any of these kinds blocks deletion.
// "defines" (structural container) is intentionally excluded — a parent defining
// the target does not keep it referenced.
var referencingEdgeKinds = map[string]bool{
	"calls":      true,
	"references": true,
	"implements": true,
	"inherits":   true,
	"overrides":  true,
}

// SafeDeleteOp requests removal of a symbol, gated on a reference-safety check.
type SafeDeleteOp struct {
	TargetSymbol string
	DryRun       bool
}

// SafeDeleteOutcome is the explicit terminal status of a safe-delete request.
type SafeDeleteOutcome string

const (
	// SafeDeleteApplied — the gate cleared and (unless DryRun) the symbol was removed.
	SafeDeleteApplied SafeDeleteOutcome = "applied"
	// SafeDeleteBlocked — at least one blocking reference exists; BlockingRefs lists
	// them and NO mutation was performed (fail-safe).
	SafeDeleteBlocked SafeDeleteOutcome = "blocked"
	// SafeDeleteUnavailable — the target could not be resolved (e.g. not yet
	// ingested); graceful skip, never an error.
	SafeDeleteUnavailable SafeDeleteOutcome = "unavailable"
)

// RefReason is the closed set of typed reasons a reference blocks deletion.
type RefReason string

const (
	// ReasonLiveReference — an ordinary resolved (derived/confirmed) reference.
	ReasonLiveReference RefReason = "live_reference"
	// ReasonTestReference — a reference whose referrer is a test file. Test-only
	// references are LIVE and still block.
	ReasonTestReference RefReason = "test_reference"
	// ReasonUnresolved — a low-confidence (heuristic-tier) reference. Fail-safe:
	// treated as blocking even though it could not be confirmed.
	ReasonUnresolved RefReason = "unresolved"
)

// BlockingRef is one reference that blocks a safe-delete, with the referrer's
// identity/location, the edge kind, its confidence tier, and a typed reason.
type BlockingRef struct {
	Symbol   model.NodeId `json:"symbol_id"`
	File     string       `json:"file"`
	Line     int          `json:"line"`
	EdgeKind string       `json:"edge_kind"`
	Tier     string       `json:"tier"`
	Reason   RefReason    `json:"reason"`
}

// SafeDeleteResult is the canonical typed envelope returned by ApplySafeDelete.
// BlockingRefs is non-nil (possibly empty); NewlyDead lists symbols that lose
// their last live reference when the target is removed — surfaced as an advisory,
// never auto-deleted.
type SafeDeleteResult struct {
	Outcome      SafeDeleteOutcome `json:"outcome"`
	TargetNodeID string            `json:"target_node_id"`
	BlockingRefs []BlockingRef     `json:"blocking_refs"`
	NewlyDead    []model.NodeId    `json:"newly_dead"`
	TouchedFiles []string          `json:"touched_files"`
	EditID       string            `json:"edit_id,omitempty"`
	DryRun       bool              `json:"dry_run,omitempty"`
}

// ApplySafeDelete removes op.TargetSymbol only after a reference-safety gate
// clears. It enumerates every LIVE inbound reference through the graph; if any
// exist (including low-confidence/unresolved and test-only references, which are
// fail-safe blocking), it returns SafeDeleteBlocked with the complete report and
// performs NO mutation. When the gate clears, it removes the declaration via the
// EP-006 saga (byte-identical re-index enforced) and surfaces any symbols made
// newly-dead by the removal as an advisory (never auto-deleted).
func (a *Applier) ApplySafeDelete(ctx context.Context, op SafeDeleteOp) (SafeDeleteResult, error) {
	out := SafeDeleteResult{Outcome: SafeDeleteBlocked, TargetNodeID: op.TargetSymbol, BlockingRefs: []BlockingRef{}, NewlyDead: []model.NodeId{}, DryRun: op.DryRun}

	if strings.TrimSpace(op.TargetSymbol) == "" {
		return out, fmt.Errorf("%w: empty target symbol", ErrInvalidOp)
	}
	target, err := a.store.GetNode(ctx, model.NodeId(op.TargetSymbol))
	if err != nil {
		out.Outcome = SafeDeleteUnavailable
		return out, nil
	}

	edges, err := a.store.Edges(ctx, graphstore.Query{})
	if err != nil {
		return out, fmt.Errorf("%w: list edges: %v", ErrInvalidOp, err)
	}

	// Reference-safety gate: collect every inbound live reference as blocking.
	blocking := []BlockingRef{}
	for _, e := range edges {
		if string(e.To()) != op.TargetSymbol || !referencingEdgeKinds[e.Kind()] {
			continue
		}
		referrer, rerr := a.store.GetNode(ctx, e.From())
		file, line := "", 0
		if rerr == nil {
			file, line = referrer.SourcePath(), referrer.Line()
		}
		blocking = append(blocking, BlockingRef{
			Symbol:   e.From(),
			File:     file,
			Line:     line,
			EdgeKind: e.Kind(),
			Tier:     string(e.Tier()),
			Reason:   refReason(e.Tier(), file),
		})
	}
	sortBlockingRefs(blocking)
	out.BlockingRefs = blocking
	if len(blocking) > 0 {
		return out, nil
	}

	// Gate cleared. Compute newly-dead cascade (advisory only).
	out.NewlyDead = newlyDead(edges, op.TargetSymbol)

	// Plan the declaration removal.
	declRel := target.SourcePath()
	declAbs := joinRoot(a.root, declRel)
	content, err := os.ReadFile(declAbs) //nolint:gosec // declRel is an in-graph source path under root
	if err != nil {
		out.Outcome = SafeDeleteUnavailable
		return out, nil
	}
	declSpan, ok := lineByteSpan(content, target.Line())
	if !ok {
		out.Outcome = SafeDeleteUnavailable
		return out, nil
	}
	delOp := EditOp{TargetNodeID: op.TargetSymbol, FilePath: declRel, ByteSpan: declSpan, Replacement: []byte{}}

	if op.DryRun {
		out.TouchedFiles = []string{declRel}
		out.Outcome = SafeDeleteApplied
		return out, nil
	}

	res, arts, err := a.applyBatch(ctx, []EditOp{delOp}, ingest.EditOpSafeDelete)
	if err != nil {
		out.Outcome = SafeDeleteBlocked
		return out, err
	}
	arts.discard()
	out.Outcome = SafeDeleteApplied
	out.TouchedFiles = res.TouchedFiles
	out.EditID = res.EditID
	return out, nil
}

// refReason classifies a blocking reference: unresolved (heuristic tier) wins,
// then test-reference (referrer in a test file), else an ordinary live reference.
func refReason(tier model.ConfidenceTier, referrerFile string) RefReason {
	if tier == model.TierHeuristic {
		return ReasonUnresolved
	}
	if isTestPath(referrerFile) {
		return ReasonTestReference
	}
	return ReasonLiveReference
}

// isTestPath reports whether p looks like a test source file.
func isTestPath(p string) bool {
	return strings.Contains(p, "_test.") || strings.Contains(p, "/test/") || strings.HasPrefix(p, "test/")
}

// newlyDead returns the symbols that lose their last live inbound reference when
// target is removed: target's outbound live-ref targets that have no OTHER live
// inbound reference. Computed from the pre-delete edge set; sorted + de-duplicated.
func newlyDead(edges []model.Edge, target string) []model.NodeId {
	inboundOther := map[model.NodeId]int{}
	outbound := map[model.NodeId]struct{}{}
	for _, e := range edges {
		if !referencingEdgeKinds[e.Kind()] {
			continue
		}
		if string(e.From()) == target {
			outbound[e.To()] = struct{}{}
			continue
		}
		inboundOther[e.To()]++
	}
	var dead []model.NodeId
	for x := range outbound {
		if inboundOther[x] == 0 && string(x) != target {
			dead = append(dead, x)
		}
	}
	sort.Slice(dead, func(i, j int) bool { return dead[i] < dead[j] })
	return dead
}

// sortBlockingRefs applies a stable total order to the blocking-reference report:
// referrer symbol, then file, line, edge kind, reason. No map-iteration order.
func sortBlockingRefs(refs []BlockingRef) {
	sort.Slice(refs, func(i, j int) bool {
		a, b := refs[i], refs[j]
		if a.Symbol != b.Symbol {
			return a.Symbol < b.Symbol
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.EdgeKind != b.EdgeKind {
			return a.EdgeKind < b.EdgeKind
		}
		return a.Reason < b.Reason
	})
}
