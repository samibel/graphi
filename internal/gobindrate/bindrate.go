// Package gobindrate — main Report type, Run function, and rendered report
// serialization (the SW-187 deliverable). See walk_ast.go for the independent
// AST denominator and classify.go for per-call-site classification.
//
// Run is the only entry point. It walks the source set twice (once for the
// denominator, once via engine/typeresolve.Resolve for the numerator +
// resolver-level accounting), classifies every call site against go/types'
// resolved-object map, and renders the closed 11-row histogram. The
// rendered report's SHA-256 is the reproducibility token: two Run calls
// against the same files map produce the same SHA, byte-for-byte.
package gobindrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/model"
	corparse "github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/typeresolve"
)

// Report is the published binding-rate report for one Go repository snapshot.
type Report struct {
	// ASTDenominator is the recursive count of *ast.CallExpr nodes from
	// WalkASTCounts — the SW-187 denominator, independent of the binder.
	ASTDenominator int
	// ASTFiles is the number of non-test .go files the walker visited.
	ASTFiles int
	// BoundSites is the count of bound call sites the resolver committed to
	// confirmed-tier `calls` edges: sum of len(Edge.Evidence()) over Edges
	// with Kind == "calls". This is the numerator of the published rate.
	BoundSites int
	// BoundCallEdges is the number of distinct confirmed `calls` edges
	// (dedup'd at (from,to,kind)). Phase C count; the Phase B → C collapse
	// is published alongside.
	BoundCallEdges int
	// Rate is BoundSites / ASTDenominator, expressed as a fraction in
	// [0, 1]. The published Go figure is a percentage (e.g. 14.00%); callers
	// multiply by 100.
	Rate float64
	// Histogram is the closed 11-row skip vocabulary, sorted in render order
	// (resolver-level rows first, then AST-shape buckets, both alphabetically
	// within their group).
	Histogram []HistogramRow
	// EdgeCounts maps each edge kind produced by the resolver to its
	// (edges, sites) pair, dedup'd at (from,to,kind) granularity.
	EdgeCounts map[string]EdgeCount
	// ReportSHA256 is the SHA-256 of the rendered report (excluding the
	// sha256 line itself). The two-run reproducibility assertion is this
	// field's byte-equality across two consecutive Run calls against the
	// same files map.
	ReportSHA256 string
	// Rendered is the canonical, printable form of the report. SHA-256 is
	// computed over this string with the trailing sha256 line removed.
	Rendered string
}

// HistogramRow is one row of the skip histogram.
type HistogramRow struct {
	Reason Reason
	Count  int
}

// EdgeCount is the (edges, sites) pair for one edge kind.
type EdgeCount struct {
	// Edges is the number of distinct edges of this kind (dedup'd).
	Edges int
	// Sites is the sum of evidence entries across those edges.
	Sites int
}

// Run measures the Go confirmed-tier binding rate over the source set in
// files. It is pure and deterministic: identical files map yields byte-
// identical Report.Rendered (and therefore Report.ReportSHA256) — the
// SW-187 AC-4 reproducibility assertion.
//
// The error return is construction-plumbing only; per-file or per-unit
// failures NEVER surface as errors — they degrade the unit and the
// measurement continues, the same way engine/typeresolve.Resolve behaves.
//
// The skip histogram is a closed 11-row vocabulary (6 AST-shape buckets +
// 5 resolver-level accounting rows), enumerated by AllReasons(). Every
// non-bound call falls into exactly one AST-shape bucket, so bound +
// sum(AST-shape) == ASTDenominator for every well-formed corpus.
func Run(files map[string][]byte) (Report, error) {
	// 1. Independent denominator.
	denom := WalkASTCounts(files)

	// 2. Build committed node set via the real ingest-side extractor (no
	// private symbol extractor; same path the shipped binary uses).
	committed := map[model.NodeId]struct{}{}
	p := corparse.NewGoParser()
	for name, src := range files {
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		pr, err := p.Parse(context.Background(), name, src)
		if err != nil {
			continue
		}
		for _, n := range pr.Nodes {
			committed[n.ID()] = struct{}{}
		}
	}

	// 3. Run the resolver. The Edges + Units slices carry every published
	// fact: bound sites, edge kinds, dropped intents, type-errors count,
	// per-unit degradation reasons.
	res, err := typeresolve.Resolve(files, committed)
	if err != nil {
		return Report{}, fmt.Errorf("gobindrate: resolver failed: %w", err)
	}

	// 4. Numerator from resolver Edges.
	boundSites := 0
	boundCallEdges := 0
	byKind := map[string]*EdgeCount{}
	for _, e := range res.Edges {
		ec := byKind[e.Kind()]
		if ec == nil {
			zero := EdgeCount{}
			ec = &zero
			byKind[e.Kind()] = ec
		}
		ec.Edges++
		ec.Sites += len(e.Evidence())
		if e.Kind() == "calls" {
			boundSites += len(e.Evidence())
			boundCallEdges++
		}
	}
	edgeCounts := map[string]EdgeCount{}
	for k, v := range byKind {
		edgeCounts[k] = *v
	}

	// 5. Resolver-level accounting rows.
	totalTypeErrors := 0
	degradedCounts := map[string]int{}
	for _, u := range res.Units {
		totalTypeErrors += u.TypeErrors
		if u.Degraded != "" {
			degradedCounts[u.Degraded]++
		}
	}
	if _, ok := degradedCounts["type-check produced no package"]; !ok {
		degradedCounts["type-check produced no package"] = 0
	}
	if _, ok := degradedCounts["type-check panic"]; !ok {
		degradedCounts["type-check panic"] = 0
	}

	// 6. AST-shape histogram via go/types' resolved-object map.
	modules, parsedByFile, typeInfoByFile, _ := runPerUnitTypeInfo(files)
	hist := classifyAll(parsedByFile, typeInfoByFile, modules)
	// Merge AST-shape rows back into the closed vocabulary.
	boundInternal := hist[ReasonBoundInternalFunc]
	astShapeRows := map[Reason]int{
		ReasonSelectorQualifierNoResolvedObjectCrossPackage: hist[ReasonSelectorQualifierNoResolvedObjectCrossPackage],
		ReasonSelectorMethodNoResolvedObjectCrossPackage:    hist[ReasonSelectorMethodNoResolvedObjectCrossPackage],
		ReasonBareIdentNoResolvedObjectCrossPackage:         hist[ReasonBareIdentNoResolvedObjectCrossPackage],
		ReasonSelectorWithNonIdentReceiver:                  hist[ReasonSelectorWithNonIdentReceiver],
		ReasonCallPositionOther:                             hist[ReasonCallPositionOther],
		ReasonGenericCallSiteSkippedByCST:                   hist[ReasonGenericCallSiteSkippedByCST],
	}

	// 7. Render the closed 12-row vocabulary: bound_internal_func row (1),
	// then resolver-level rows (5), then AST-shape buckets (6), all
	// alphabetically within their group. The structural invariant is
	// bound_internal_func + sum(AST-shape) == ASTDenominator; the
	// resolver-level rows are environmental and not part of that sum.
	histogram := []HistogramRow{
		{Reason: ReasonBoundInternalFunc, Count: boundInternal},
		{Reason: ReasonGoTypesTypeErrors, Count: totalTypeErrors},
	}
	for _, r := range []Reason{
		ReasonSelectorQualifierNoResolvedObjectCrossPackage,
		ReasonSelectorMethodNoResolvedObjectCrossPackage,
		ReasonBareIdentNoResolvedObjectCrossPackage,
		ReasonSelectorWithNonIdentReceiver,
		ReasonCallPositionOther,
		ReasonGenericCallSiteSkippedByCST,
	} {
		histogram = append(histogram, HistogramRow{Reason: r, Count: astShapeRows[r]})
	}
	for _, r := range []Reason{
		ReasonFileDidNotParse,
		ReasonResolverDroppedIntents,
		ReasonUnitsDegradedTypeCheckPanic,
		ReasonUnitsDegradedTypeCheckNoPackage,
	} {
		var c int
		switch r {
		case ReasonFileDidNotParse:
			c = denom.ParseFailures
		case ReasonResolverDroppedIntents:
			c = res.DroppedIntents
		case ReasonUnitsDegradedTypeCheckPanic:
			c = degradedCounts["type-check panic"]
		case ReasonUnitsDegradedTypeCheckNoPackage:
			c = degradedCounts["type-check produced no package"]
		}
		histogram = append(histogram, HistogramRow{Reason: r, Count: c})
	}

	// 8. Compute rate.
	var rate float64
	if denom.CallSites > 0 {
		rate = float64(boundSites) / float64(denom.CallSites)
	}

	// 9. Render the report (deterministic).
	r := Report{
		ASTDenominator: denom.CallSites,
		ASTFiles:       denom.Files,
		BoundSites:     boundSites,
		BoundCallEdges: boundCallEdges,
		Rate:           rate,
		Histogram:      histogram,
		EdgeCounts:     edgeCounts,
	}
	rendered := r.render()
	sum := sha256.Sum256([]byte(rendered))
	r.Rendered = rendered
	r.ReportSHA256 = hex.EncodeToString(sum[:])
	return r, nil
}

// render produces the deterministic, printable form. The trailing
// report_sha256=<hash> line is appended by the caller (Run) so the SHA is
// not hashed over itself.
func (r Report) render() string {
	var b strings.Builder
	fmt.Fprintln(&b, "SW-187 Go binding rate")
	fmt.Fprintln(&b, "====================================================")
	fmt.Fprintf(&b, "ast_call_sites_denominator=%d\n", r.ASTDenominator)
	fmt.Fprintf(&b, "ast_files=%d\n", r.ASTFiles)
	fmt.Fprintf(&b, "bound_call_sites_to_internal_funcs=%d\n", r.BoundSites)
	fmt.Fprintf(&b, "bound_calls_edges_dedup=%d\n", r.BoundCallEdges)
	fmt.Fprintf(&b, "rate_bound_call_sites_per_denominator=%.4f\n", r.Rate)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Skip histogram (closed 12-row vocabulary)")
	// Deterministic row order: bound_internal_func + resolver-level rows
	// first (sorted alphabetically), then AST-shape buckets (sorted). The
	// bound + AST-shape sum equals ASTDenominator by construction; the
	// resolver-level rows are environmental and not part of that sum.
	resolverFirst := []HistogramRow{}
	astShape := []HistogramRow{}
	for _, hr := range r.Histogram {
		if isASTShapeReason(hr.Reason) {
			astShape = append(astShape, hr)
		} else {
			resolverFirst = append(resolverFirst, hr)
		}
	}
	sort.Slice(resolverFirst, func(i, j int) bool {
		return resolverFirst[i].Reason < resolverFirst[j].Reason
	})
	sort.Slice(astShape, func(i, j int) bool {
		return astShape[i].Reason < astShape[j].Reason
	})
	for _, hr := range resolverFirst {
		fmt.Fprintf(&b, "%s\t%d\n", hr.Reason, hr.Count)
	}
	for _, hr := range astShape {
		fmt.Fprintf(&b, "%s\t%d\n", hr.Reason, hr.Count)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Edge counts (per kind, dedup'd at (from,to,kind) granularity)")
	kinds := make([]string, 0, len(r.EdgeCounts))
	for k := range r.EdgeCounts {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		ec := r.EdgeCounts[k]
		fmt.Fprintf(&b, "%s_edges=%d (bound sites: %d)\n", k, ec.Edges, ec.Sites)
	}
	return b.String()
}

func isASTShapeReason(r Reason) bool {
	switch r {
	case ReasonSelectorQualifierNoResolvedObjectCrossPackage,
		ReasonSelectorMethodNoResolvedObjectCrossPackage,
		ReasonBareIdentNoResolvedObjectCrossPackage,
		ReasonSelectorWithNonIdentReceiver,
		ReasonCallPositionOther,
		ReasonGenericCallSiteSkippedByCST:
		return true
	}
	return false
}
