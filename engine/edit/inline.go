package edit

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/ingest"
)

// InlineOp is the high-level request to inline a symbol: substitute its
// definition's value at every reference site and remove the now-dead
// declaration. The target is a resolved NodeId; the planner derives the local
// identifier and the value span from the graph + source. DryRun computes and
// returns the plan without mutating.
type InlineOp struct {
	TargetSymbol string
	DryRun       bool
}

// InlineOutcome is the explicit terminal status of an inline request, mirroring
// the typed-envelope convention used across graphi (query/diagnostic): a
// non-error marker the surface reads instead of inspecting errors.
type InlineOutcome string

const (
	// InlineApplied — the inline was planned and (unless DryRun) committed.
	InlineApplied InlineOutcome = "applied"
	// InlineBlocked — inlining could not be proven safe; BlockReason explains why
	// and NO mutation was performed (fail-safe).
	InlineBlocked InlineOutcome = "blocked"
	// InlineUnavailable — the target could not be resolved / its source is
	// unavailable; graceful skip, never an error.
	InlineUnavailable InlineOutcome = "unavailable"
)

// InlineBlockReason is the closed set of typed reasons an inline is blocked. The
// gate is fail-safe: anything not provably safe blocks rather than emitting a
// possibly-wrong edit.
type InlineBlockReason string

const (
	// BlockUnresolvedReference — at least one inbound reference to the target is
	// unresolved (heuristic tier); substituting could miss or corrupt a site.
	BlockUnresolvedReference InlineBlockReason = "unresolved_reference"
	// BlockAddressTaken — the symbol's address is taken (&sym); inlining its value
	// would change pointer identity.
	BlockAddressTaken InlineBlockReason = "address_taken"
	// BlockSideEffectMultiEval — the value contains a call and there are ≥2 sites,
	// so inlining would evaluate the side effect more than once.
	BlockSideEffectMultiEval InlineBlockReason = "side_effecting_multi_eval"
	// BlockUnsupportedShape — the definition is not a single-line value the engine
	// can extract safely (e.g. a function body, a multi-line or block initializer).
	BlockUnsupportedShape InlineBlockReason = "unsupported_shape"
)

// InlineResult is the canonical typed envelope returned by ApplyInline.
type InlineResult struct {
	Outcome        InlineOutcome     `json:"outcome"`
	TargetNodeID   string            `json:"target_node_id"`
	BlockReason    InlineBlockReason `json:"block_reason,omitempty"`
	BlockDetail    string            `json:"block_detail,omitempty"`
	TouchedFiles   []string          `json:"touched_files"`
	ImpactFiles    []string          `json:"impact_files"`
	ReferenceSites int               `json:"reference_sites"`
	EditID         string            `json:"edit_id,omitempty"`
	Truncated      bool              `json:"truncated,omitempty"`
	PlannedOps     []EditOp          `json:"-"`
	DryRun         bool              `json:"dry_run,omitempty"`
}

// ApplyInline inlines op.TargetSymbol. It resolves the target, runs the
// fail-safe safe-block gate (unresolved references, address-taken, side-effecting
// multi-eval, unsupported shapes), enumerates reference sites through the EP-004
// blast radius, and applies the substitutions + declaration removal in ONE atomic
// EP-006 saga (applyBatch) so every reference is rewritten or none is. The
// post-edit graph re-indexes byte-identical to a full re-index (the saga's
// consistency gate enforces this and rolls back otherwise).
func (a *Applier) ApplyInline(ctx context.Context, op InlineOp) (InlineResult, error) {
	out := InlineResult{Outcome: InlineBlocked, TargetNodeID: op.TargetSymbol, DryRun: op.DryRun}

	if strings.TrimSpace(op.TargetSymbol) == "" {
		return out, fmt.Errorf("%w: empty target symbol", ErrInvalidOp)
	}

	target, err := a.store.GetNode(ctx, model.NodeId(op.TargetSymbol))
	if err != nil {
		out.Outcome = InlineUnavailable
		out.BlockDetail = fmt.Sprintf("target symbol %s not found", op.TargetSymbol)
		return out, nil
	}

	// Gate 1: unresolved references. Any inbound reference at heuristic tier means
	// the resolver could not confirm a use site — fail safe.
	if reason, ok, err := a.hasUnresolvedInbound(ctx, op.TargetSymbol); err != nil {
		return out, err
	} else if ok {
		out.BlockReason = BlockUnresolvedReference
		out.BlockDetail = reason
		return out, nil
	}

	// Resolve the local identifier and the single-line value from source.
	localName := lastSegment(target.QualifiedName())
	if localName == "" {
		out.BlockReason = BlockUnsupportedShape
		out.BlockDetail = "could not derive local identifier from qualified name"
		return out, nil
	}
	declRel := target.SourcePath()
	declAbs := joinRoot(a.root, declRel)
	content, err := os.ReadFile(declAbs) //nolint:gosec // declRel is an in-graph source path under root
	if err != nil {
		out.Outcome = InlineUnavailable
		out.BlockDetail = fmt.Sprintf("read declaration file %s: %v", declRel, err)
		return out, nil
	}
	declSpan, ok := lineByteSpan(content, target.Line())
	if !ok {
		out.BlockReason = BlockUnsupportedShape
		out.BlockDetail = fmt.Sprintf("declaration line %d out of range in %s", target.Line(), declRel)
		return out, nil
	}
	value, ok := extractSingleLineValue(content[declSpan.Start:declSpan.End], localName)
	if !ok {
		out.BlockReason = BlockUnsupportedShape
		out.BlockDetail = "definition is not a single-line extractable value (function body, block, or multi-line initializer)"
		return out, nil
	}

	// EP-004 blast radius: the files that may reference the target.
	files, truncated, err := a.blastRadius(ctx, op.TargetSymbol)
	if err != nil {
		return out, err
	}
	out.ImpactFiles = files
	out.Truncated = truncated

	// Gate 2: address-taken anywhere in the blast radius.
	if taken, where, err := addressTaken(a.root, files, localName); err != nil {
		return out, err
	} else if taken {
		out.BlockReason = BlockAddressTaken
		out.BlockDetail = fmt.Sprintf("address of %q taken in %s", localName, where)
		return out, nil
	}

	// Enumerate reference sites (excluding the declaration line itself).
	refOps, count, err := inlineReferenceOps(a.root, files, declRel, declSpan, localName, []byte(value))
	if err != nil {
		return out, err
	}
	out.ReferenceSites = count

	// Gate 3: side-effecting multi-eval. A value containing a call evaluated at ≥2
	// sites would run the side effect more than once.
	if count >= 2 && containsCall(value) {
		out.BlockReason = BlockSideEffectMultiEval
		out.BlockDetail = fmt.Sprintf("value %q contains a call and would be evaluated at %d sites", value, count)
		return out, nil
	}

	// Build the op list: all reference substitutions + the declaration removal.
	ops := append(refOps, EditOp{
		TargetNodeID: op.TargetSymbol,
		FilePath:     declRel,
		ByteSpan:     declSpan,
		Replacement:  []byte{},
	})
	out.PlannedOps = ops

	if op.DryRun {
		out.TouchedFiles = touchedFilesOf(ops)
		out.Outcome = InlineApplied
		return out, nil
	}

	res, arts, err := a.applyBatch(ctx, ops, ingest.EditOpInline)
	if err != nil {
		out.Outcome = InlineBlocked
		out.BlockDetail = err.Error()
		return out, err
	}
	arts.discard()
	out.Outcome = InlineApplied
	out.TouchedFiles = res.TouchedFiles
	out.EditID = res.EditID
	return out, nil
}

// hasUnresolvedInbound reports whether any inbound reference edge to target is at
// heuristic tier (an unresolved/unconfirmed reference).
func (a *Applier) hasUnresolvedInbound(ctx context.Context, target string) (detail string, blocked bool, err error) {
	edges, err := a.store.Edges(ctx, graphstore.Query{})
	if err != nil {
		return "", false, fmt.Errorf("%w: list edges: %v", ErrInvalidOp, err)
	}
	for _, e := range edges {
		if string(e.To()) == target && e.Tier() == model.TierHeuristic {
			return fmt.Sprintf("inbound %s edge from %s is unresolved (heuristic)", e.Kind(), e.From()), true, nil
		}
	}
	return "", false, nil
}

// inlineReferenceOps scans the blast-radius files for whole-identifier
// occurrences of localName and emits a substitution EditOp (name → value) for
// each, EXCLUDING any occurrence inside the declaration line (declSpan in
// declRel), which the declaration-removal op handles. Returns the ops and the
// reference-site count.
func inlineReferenceOps(root string, files []string, declRel string, declSpan Span, localName string, value []byte) ([]EditOp, int, error) {
	needle := []byte(localName)
	var ops []EditOp
	sorted := make([]string, len(files))
	copy(sorted, files)
	sort.Strings(sorted)
	for _, rel := range sorted {
		abs := joinRoot(root, rel)
		content, err := os.ReadFile(abs) //nolint:gosec // rel is an in-graph source path under root
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, 0, fmt.Errorf("%w: read %s: %v", ErrInvalidOp, rel, err)
		}
		for _, span := range identifierSpans(content, needle) {
			// Skip the declaration line's own occurrences; the removal op covers it.
			if rel == declRel && span.Start >= declSpan.Start && span.Start < declSpan.End {
				continue
			}
			ops = append(ops, EditOp{
				FilePath:    rel,
				ByteSpan:    span,
				Replacement: value,
			})
		}
	}
	return ops, len(ops), nil
}

// addressTaken reports whether localName appears with its address taken (&name)
// anywhere in the blast radius — a case where inlining the value would change
// pointer identity, so the inline must block.
func addressTaken(root string, files []string, localName string) (bool, string, error) {
	needle := []byte(localName)
	sorted := make([]string, len(files))
	copy(sorted, files)
	sort.Strings(sorted)
	for _, rel := range sorted {
		abs := joinRoot(root, rel)
		content, err := os.ReadFile(abs) //nolint:gosec // rel is an in-graph source path under root
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, "", fmt.Errorf("%w: read %s: %v", ErrInvalidOp, rel, err)
		}
		for _, span := range identifierSpans(content, needle) {
			// An '&' immediately preceding the identifier (skipping no spaces — Go
			// address-of binds tightly) means the address is taken.
			if span.Start > 0 && content[span.Start-1] == '&' {
				return true, rel, nil
			}
		}
	}
	return false, "", nil
}

// extractSingleLineValue pulls the right-hand value from a single-line
// declaration (const/var/:=/type-alias). It returns ok=false for shapes it
// cannot safely extract — function bodies, blocks, or multi-line initializers —
// so the caller blocks them. The line must mention localName on its left side.
func extractSingleLineValue(line []byte, localName string) (string, bool) {
	s := strings.TrimRight(string(line), "\n")
	// A brace means a block/body/composite — not a single-line scalar value.
	if strings.ContainsAny(s, "{}") {
		return "", false
	}
	var lhs, rhs string
	if i := strings.Index(s, ":="); i >= 0 {
		lhs, rhs = s[:i], s[i+2:]
	} else if i := assignmentIndex(s); i >= 0 {
		lhs, rhs = s[:i], s[i+1:]
	} else {
		return "", false
	}
	if !strings.Contains(lhs, localName) {
		return "", false
	}
	// Strip a trailing line comment and trailing punctuation.
	if c := strings.Index(rhs, "//"); c >= 0 {
		rhs = rhs[:c]
	}
	rhs = strings.TrimSpace(rhs)
	rhs = strings.TrimRight(rhs, ";,")
	rhs = strings.TrimSpace(rhs)
	if rhs == "" {
		return "", false
	}
	return rhs, true
}

// assignmentIndex returns the index of a single '=' assignment operator in s, or
// -1. It skips '==', '!=', '<=', '>=' and the ':=' / declaration forms (handled
// by the caller).
func assignmentIndex(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] != '=' {
			continue
		}
		prev := byte(0)
		if i > 0 {
			prev = s[i-1]
		}
		next := byte(0)
		if i+1 < len(s) {
			next = s[i+1]
		}
		if prev == '=' || prev == '!' || prev == '<' || prev == '>' || prev == ':' || next == '=' {
			continue
		}
		return i
	}
	return -1
}

// containsCall reports whether v looks like it contains a function/method call —
// an open parenthesis. Used to gate side-effecting multi-eval.
func containsCall(v string) bool { return strings.Contains(v, "(") }

// lineByteSpan returns the [Start,End) byte span of the 1-based line in content,
// including its trailing newline. ok=false if the line is out of range.
func lineByteSpan(content []byte, line int) (Span, bool) {
	if line < 1 {
		return Span{}, false
	}
	start := 0
	for cur := 1; cur < line; cur++ {
		idx := bytes.IndexByte(content[start:], '\n')
		if idx < 0 {
			return Span{}, false
		}
		start += idx + 1
	}
	if start > len(content) {
		return Span{}, false
	}
	nl := bytes.IndexByte(content[start:], '\n')
	end := len(content)
	if nl >= 0 {
		end = start + nl + 1
	}
	return Span{Start: start, End: end}, true
}

// lastSegment returns the final segment of a qualified name (the local
// identifier), splitting on the last '.' or '/' so both "pkg.Foo" and "pkg/Foo"
// yield "Foo".
func lastSegment(qn string) string {
	if i := strings.LastIndexAny(qn, "./"); i >= 0 {
		return qn[i+1:]
	}
	return qn
}

// touchedFilesOf returns the sorted, de-duplicated set of files an op list edits.
func touchedFilesOf(ops []EditOp) []string {
	set := map[string]struct{}{}
	for _, o := range ops {
		set[o.FilePath] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}
