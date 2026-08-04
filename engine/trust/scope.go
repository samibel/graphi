package trust

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
)

// ErrSelectiveLookupUnavailable is the typed sentinel ResolveScope wraps when
// the store does not implement graphstore.SymbolLookupPort (the package face
// of the contract doc §4 class ErrTrustSelectiveLookupUnavailable). Target
// resolution refuses to fall back to whole-graph scans — the PRD §27
// acceptance criterion "selektive Reads werden genutzt" is a hard boundary,
// not a fast path.
var ErrSelectiveLookupUnavailable = errors.New("trust: selective symbol lookup unavailable")

// maxTargetCandidates bounds the candidate list an ambiguous target reports
// (PRD §27: "gibt bounded Candidates zurück").
const maxTargetCandidates = 5

// Target is the raw --target / MCP target argument exactly as the user
// supplied it, before resolution. The type exists so surfaces hand the
// unparsed value around without pre-guessing its kind — ResolveScope(ctx,
// store, t.Raw) decides symbol vs file, fail closed.
type Target struct {
	Raw string
}

// ResolveScope resolves a raw target string to the scope an assessment runs
// against, over the live store's selective lookups only (SymbolLookupPort;
// never a whole-graph scan). Resolution order, first hit wins:
//
//  1. Empty raw (after whitespace trim) → repository scope, no findings.
//  2. Exact QualifiedName equality. One hit → symbol scope carrying the
//     node's ID, path, and qualified name. Several hits → the target is never
//     auto-picked (PRD §27): scope keeps kind "symbol" with ID empty, plus a
//     TARGET_AMBIGUOUS finding whose message carries a sorted candidate list
//     bounded at maxTargetCandidates.
//  3. Exact SourcePath equality on the model.NormalizePath-normalized raw
//     (normalization in the caller, per the port contract) → file scope.
//  4. Nothing matched → TARGET_NOT_FOUND, never an empty healthy scope
//     (PRD §27 "wird nicht als leerer Scope behandelt").
//
// Package scope resolution is deliberately deferred in v1 (the contract's
// leaves-open discipline: no QN-prefix probing is invented here). A
// package-looking raw — it contains "/" and matched neither a qualified name
// nor an indexed file path — therefore resolves as not-found with kind
// "package" recorded on the scope and a SCOPE_EVIDENCE_UNAVAILABLE finding
// attached documenting the gap. The returned findings are in canonical order;
// a non-nil error is an operational store failure (or the missing lookup
// port), never a resolution outcome.
func ResolveScope(ctx context.Context, store graphstore.Graphstore, raw string) (ScopeRef, []Finding, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ScopeRef{Kind: ScopeRepository}, []Finding{}, nil
	}
	lookup, ok := store.(graphstore.SymbolLookupPort)
	if !ok {
		return ScopeRef{}, nil, fmt.Errorf("%w: store %T does not implement graphstore.SymbolLookupPort", ErrSelectiveLookupUnavailable, store)
	}

	nodes, err := lookup.QualifiedName(ctx, raw)
	if err != nil {
		return ScopeRef{}, nil, fmt.Errorf("trust: resolve target %q: %w", raw, err)
	}
	switch {
	case len(nodes) == 1:
		n := nodes[0]
		scope := ScopeRef{
			Kind:   ScopeSymbol,
			ID:     string(n.ID()),
			Path:   n.SourcePath(),
			Symbol: n.QualifiedName(),
		}
		return scope, []Finding{}, nil
	case len(nodes) > 1:
		scope := ScopeRef{Kind: ScopeSymbol, Symbol: raw}
		f, err := NewFinding(FindingTargetAmbiguous, scope,
			strconv.Itoa(len(nodes)), "1", ambiguousTargetMessage(raw, nodes))
		if err != nil {
			return ScopeRef{}, nil, err
		}
		return scope, []Finding{f}, nil
	}

	normalized := model.NormalizePath(raw)
	files, err := lookup.SourcePath(ctx, normalized)
	if err != nil {
		return ScopeRef{}, nil, fmt.Errorf("trust: resolve target path %q: %w", normalized, err)
	}
	if len(files) > 0 {
		return ScopeRef{Kind: ScopeFile, Path: normalized}, []Finding{}, nil
	}

	return notFoundScope(raw)
}

// ambiguousTargetMessage renders the bounded, sorted candidate list for a
// TARGET_AMBIGUOUS finding. Candidates are "kind path" pairs (the qualified
// name is identical across all hits by construction), sorted ascending and
// capped at maxTargetCandidates with an explicit truncation marker.
func ambiguousTargetMessage(raw string, nodes []model.Node) string {
	cands := make([]string, 0, len(nodes))
	for _, n := range nodes {
		cands = append(cands, n.Kind()+" "+n.SourcePath())
	}
	sort.Strings(cands)
	truncated := len(cands) > maxTargetCandidates
	if truncated {
		cands = cands[:maxTargetCandidates]
	}
	list := strings.Join(cands, ", ")
	if truncated {
		list += ", ..."
	}
	return fmt.Sprintf("target %q matches %d symbols; candidates: %s", raw, len(nodes), list)
}

// notFoundScope builds the fail-closed not-found outcome. A raw containing
// "/" is treated as package-looking (the deliberately simple v1 rule — no
// prefix scans): the scope records kind "package" with ID empty and a
// SCOPE_EVIDENCE_UNAVAILABLE finding documents that package scope resolution
// is a v1 leaves-open item. Anything else records the asked symbol. In both
// shapes the empty ID marks the scope unresolved — TARGET_NOT_FOUND is always
// present, and the findings come back in canonical order (error before
// warning).
func notFoundScope(raw string) (ScopeRef, []Finding, error) {
	scope := ScopeRef{Kind: ScopeSymbol, Symbol: raw}
	packageLooking := strings.Contains(raw, "/")
	if packageLooking {
		scope = ScopeRef{Kind: ScopePackage, Package: raw}
	}
	notFound, err := NewFinding(FindingTargetNotFound, scope, "0", "1",
		fmt.Sprintf("target %q matches no symbol qualified name and no indexed file path", raw))
	if err != nil {
		return ScopeRef{}, nil, err
	}
	findings := []Finding{notFound}
	if packageLooking {
		unavailable, err := NewFinding(FindingScopeEvidenceUnavailable, scope, "", "",
			fmt.Sprintf("package scope resolution is not available in v1; package-looking target %q resolves as not-found", raw))
		if err != nil {
			return ScopeRef{}, nil, err
		}
		findings = append(findings, unavailable)
	}
	return scope, findings, nil
}
