package client

// This file is the SHARED legible-abstention reader (W0.g): the ONE place the
// semantic binders' NAMED skip counters are read out of the generation-bound
// ingest evidence sidecar and turned into user-facing facts. Both compositions
// that surface abstention ride it — `strict_query` (query_strict.go) and the
// trust report (trust_report.go) — so CLI and MCP are byte-identical by
// construction for the same reason every other capability here is: one
// composition per capability, not one per surface.
//
// WHAT ABSTENTION IS, AND WHY IT GETS A SURFACE AT ALL. The JVM binder never
// guesses: every receiver it cannot type from a DECLARED form is skipped under
// a named counter (engine/jvmresolve/body_java.go). That is the honest choice —
// a guess would be a soundness hole — but until this file existed none of it
// reached a user, so an agent asking `callers` on a Java method got a confident
// list and no signal that call sites had been refused. Silent under-reporting
// is exactly the confidence laundering this surface's sibling rules forbid.
//
// THE SCOPE LIMIT IS PART OF THE DATA, NOT A FOOTNOTE. The counters are
// repository-global per language and carry NO file, package, symbol or
// call-site attribution — for java_receiver_untyped and java_receiver_external
// the callee is undeterminable by definition, so no site exists to attribute
// them to even in principle. Every text this file emits therefore states the
// limit inline. A reader who takes a roll-up here for a per-symbol accounting
// has been misled, and preventing that is a requirement, not a nicety.
//
// FAIL CLOSED. "The sidecar cannot tell me" and "nothing was skipped" are
// different answers and are never collapsed: an unreadable store, a missing
// generation, or a sidecar predating the schema-5 skip table all produce a
// VISIBLE unavailability notice, because a silent absence here would read as
// an all-clear.

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/trust"
	"github.com/samibel/graphi/internal/state"
)

// abstentionScopeNote is the sentence every abstention surface carries
// verbatim. It is a constant so the two surfaces cannot drift into stating
// different scopes for the same numbers.
const abstentionScopeNote = "these counters are repository-global per language and carry no file, package, symbol or call-site attribution — they are not a per-symbol or per-package accounting"

// maxAbstentionPackages bounds the covered-package list a strict-query notice
// names. Repository-controlled text is length-bounded before it reaches an
// artifact; capping the count alone is not enough, so each path is also capped
// at trust.MaxPathLength.
const maxAbstentionPackages = 5

// AbstentionSkip is one named skip reason and its count.
type AbstentionSkip struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// AbstentionLanguage is one registrant's whole-repository abstention record.
type AbstentionLanguage struct {
	Language string           `json:"language"`
	Total    int              `json:"total"`
	Skips    []AbstentionSkip `json:"skips"`
}

// AbstentionFacts is the wire block the trust report carries. Available
// distinguishes "read successfully" from "could not be read"; an available
// block with an EMPTY Languages list means no registrant recorded a named skip
// under this generation — which is a fact about the pass, not a promise that
// nothing was skipped by any other mechanism. Scope always restates the
// attribution limit so the numbers cannot travel without it.
type AbstentionFacts struct {
	Available         bool                 `json:"available"`
	UnavailableReason string               `json:"unavailable_reason"`
	Scope             string               `json:"scope"`
	Languages         []AbstentionLanguage `json:"languages"`
}

// evidenceHandle is one opened read-only (store, sidecar, generation) triple.
// Both readers below need the same three, and opening them once keeps a read
// from straddling a concurrent write.
type evidenceHandle struct {
	store graphstore.Graphstore
	ro    *ingest.Ingester
	gen   string
}

func (h *evidenceHandle) close() {
	if h == nil {
		return
	}
	if h.ro != nil {
		_ = h.ro.Close()
	}
	if h.store != nil {
		_ = h.store.Close()
	}
}

// openEvidence resolves and opens the repository's graph store and evidence
// sidecar READ-ONLY (a pure observer creates and repairs nothing) and reads
// the live full-pass generation. Every failure returns a NAMED reason rather
// than a bare nil, because the reason is what the surfaces print — an
// unavailability a user cannot act on is barely better than silence.
//
// The store path is resolved exactly as the freshness probe resolves it
// (state.Resolve WITHOUT Ensure) when the caller passes none, so a report and
// a query over the same repository read the same store.
func openEvidence(ctx context.Context, root, dbPath, metaDir string) (*evidenceHandle, string) {
	if dbPath == "" {
		p, err := state.Resolve(root)
		if err != nil {
			return nil, "no repository state directory could be resolved for this root"
		}
		dbPath = p.DB
	}
	store, err := graphstore.OpenSQLiteReadOnly(dbPath)
	if err != nil {
		return nil, "the graph store could not be opened read-only (the repository may not be indexed)"
	}
	h := &evidenceHandle{store: store}
	gen, err := liveGeneration(ctx, store)
	if err != nil {
		h.close()
		return nil, "the graph store's full-pass generation could not be read"
	}
	if gen == "" {
		h.close()
		return nil, "no full pass has certified this store, so no generation-bound evidence exists"
	}
	h.gen = gen
	h.ro = openEvidenceSidecar(store, TrustReportOptions{Root: root, DBPath: dbPath, MetaDir: metaDir})
	if h.ro == nil {
		h.close()
		return nil, "the ingest evidence sidecar could not be opened"
	}
	return h, ""
}

// readAbstentionFacts reads the whole generation's named skip counters, keyed
// by the registrant that recorded them. The trust report's block.
func readAbstentionFacts(ctx context.Context, root, dbPath, metaDir string) AbstentionFacts {
	out := AbstentionFacts{Scope: abstentionScopeNote, Languages: []AbstentionLanguage{}}
	h, reason := openEvidence(ctx, root, dbPath, metaDir)
	if h == nil {
		out.UnavailableReason = reason
		return out
	}
	defer h.close()

	byLang, err := h.ro.LanguageSkips(ctx, h.gen)
	switch {
	case errors.Is(err, ingest.ErrTrustEvidenceUnavailable):
		out.UnavailableReason = "this store's evidence sidecar predates the abstention record (schema 5) and never migrates when observed read-only"
		return out
	case err != nil:
		out.UnavailableReason = "the abstention record could not be read from the evidence sidecar"
		return out
	}
	out.Available = true
	langs := make([]string, 0, len(byLang))
	for l := range byLang {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	for _, l := range langs {
		entry := AbstentionLanguage{Language: l, Skips: sortedSkips(byLang[l])}
		for _, s := range entry.Skips {
			entry.Total += s.Count
		}
		out.Languages = append(out.Languages, entry)
	}
	return out
}

// sortedSkips renders one counter map in canonical (name-ascending) order,
// dropping non-positive counts so a zero can never be published as an observed
// abstention. Never nil.
func sortedSkips(m map[string]int) []AbstentionSkip {
	out := make([]AbstentionSkip, 0, len(m))
	for name, n := range m {
		if n > 0 {
			out = append(out, AbstentionSkip{Name: name, Count: n})
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}

// packageAbstention is the strict-query gate's outcome: the union of the named
// skip counters carried by the evidence rows of the packages THIS RESULT
// COVERS, plus those packages' keys.
//
// The union is exact — each covered row already carries its own language's
// counters — but it stays repository-global, which is why every caller prints
// abstentionScopeNote beside it. The package list says which packages caused
// the notice to fire; it does NOT say the skips happened in them, and the
// rendered text is explicit about that distinction.
type packageAbstention struct {
	skips             []AbstentionSkip
	total             int
	packages          []string
	unavailableReason string
}

// readPackageAbstention gates on the packages a result covers: only a package
// with a recorded abstention row makes the notice fire. pkgKeys must be the
// result's distinct package directories.
//
// anchor is the queried symbol's node id, and it is not an optimization — it
// is what keeps the EMPTY result from being the one case that says nothing. A
// result with no edges also has no nodes, so a gate built on the result's
// nodes alone falls silent exactly where abstention matters most ("no callers"
// is the answer a user is most likely to act on). The anchor's own package is
// therefore resolved from the store and folded into the gate.
//
// A package with no evidence row is simply not covered (a directory no
// semantic registrant claimed abstains from nothing by construction); a row
// whose sidecar cannot serve the skip table makes the whole read UNAVAILABLE,
// because a partial answer here would understate.
func readPackageAbstention(ctx context.Context, root, dbPath, metaDir string, pkgKeys []string, anchor model.NodeId) packageAbstention {
	var out packageAbstention
	if len(pkgKeys) == 0 && anchor == "" {
		return out
	}
	h, reason := openEvidence(ctx, root, dbPath, metaDir)
	if h == nil {
		out.unavailableReason = reason
		return out
	}
	defer h.close()

	if anchor != "" {
		if n, err := h.store.GetNode(ctx, anchor); err == nil && n.SourcePath() != "" {
			pkgKeys = addPackageKey(pkgKeys, path.Dir(n.SourcePath()))
		}
	}
	if len(pkgKeys) == 0 {
		return out
	}

	byLang, err := h.ro.LanguageSkips(ctx, h.gen)
	if errors.Is(err, ingest.ErrTrustEvidenceUnavailable) {
		return packageAbstention{unavailableReason: "this store's evidence sidecar predates the abstention record (schema 5) and never migrates when observed read-only"}
	}
	if err != nil {
		return packageAbstention{unavailableReason: "the abstention record could not be read from the evidence sidecar"}
	}

	covered := map[string]struct{}{}
	for _, key := range pkgKeys {
		pe, err := h.ro.PackageEvidence(ctx, h.gen, key)
		if errors.Is(err, ingest.ErrTrustEvidenceUnavailable) {
			return packageAbstention{unavailableReason: "this store's evidence sidecar cannot serve package evidence"}
		}
		if err != nil {
			continue // no row for this directory: nothing claimed, nothing abstained
		}
		if !pe.SkipsAvailable {
			return packageAbstention{unavailableReason: "this store's evidence sidecar predates the abstention record (schema 5) and never migrates when observed read-only"}
		}
		if len(pe.NamedSkips) == 0 {
			continue
		}
		out.packages = append(out.packages, key)
		for _, l := range pe.Languages {
			covered[l] = struct{}{}
		}
	}
	if len(out.packages) == 0 {
		return packageAbstention{}
	}
	// Sum over the covered LANGUAGES, never over the covered packages. The
	// counters are repository-global per language, so every package row of one
	// language carries the SAME numbers — adding them up per package would
	// multiply a repo-global total by however many of its packages the result
	// happened to touch and report the product as a count of skips. Summing
	// across distinct languages is correct and necessary: the emitter's
	// identity-gap names (emit_from_no_node, emit_to_no_node) are shared by the
	// java and kotlin registrants, so a max would under-report them.
	union := map[string]int{}
	for lang := range covered {
		for name, n := range byLang[lang] {
			union[name] += n
		}
	}
	out.skips = sortedSkips(union)
	for _, s := range out.skips {
		out.total += s.Count
	}
	sort.Strings(out.packages)
	return out
}

// addPackageKey appends key unless already present, keeping the sorted order
// resultPackages established.
func addPackageKey(keys []string, key string) []string {
	for _, k := range keys {
		if k == key {
			return keys
		}
	}
	keys = append(keys, key)
	sort.Strings(keys)
	return keys
}

// abstentionLimitations renders the strict-query envelope's Limitations
// entries. Three shapes, and which one fires is itself information:
//
//   - UNAVAILABLE — the record could not be read. Stated loudly, because a
//     silent omission would read as "nothing was skipped".
//   - abstention present — the counts, the reasons, the covered packages, and
//     the scope limit, in one entry.
//   - result reduced to nothing while abstention is present — an ADDITIONAL
//     entry saying the emptiness may be abstention rather than absence. This
//     is the "never launder confidence" rule applied to the abstention axis:
//     the tier filter already refuses to let filtered emptiness read as proven
//     emptiness, and abstained emptiness gets the same treatment.
func abstentionLimitations(pa packageAbstention, emptyResult bool) []string {
	if pa.unavailableReason != "" {
		return []string{fmt.Sprintf(
			"abstention accounting UNAVAILABLE (%s): the semantic binders' named skip counters could not be read for this result, so the absence of an abstention notice here is not evidence that nothing was skipped",
			pa.unavailableReason)}
	}
	if pa.total == 0 {
		return nil
	}
	parts := make([]string, 0, len(pa.skips))
	for _, s := range pa.skips {
		parts = append(parts, fmt.Sprintf("%s %d", s.Name, s.Count))
	}
	out := []string{fmt.Sprintf(
		"semantic binder abstention: %d call/reference sites were refused under named reasons (%s). This result covers %s, which carry a recorded abstention row — but %s, so they are reported beside this result, NOT counted inside it. A refused site produces no edge at all, so this result may be reduced by abstention.",
		pa.total, strings.Join(parts, ", "), renderPackageList(pa.packages), abstentionScopeNote)}
	if emptyResult {
		out = append(out, fmt.Sprintf(
			"this result is EMPTY while %d sites stand refused under named reasons — abstained emptiness is not proven emptiness, and this must not be read as \"no such relationship exists\"",
			pa.total))
	}
	return out
}

// renderPackageList names the covered packages, bounded in both count and
// per-path length, with the elision stated rather than silent.
func renderPackageList(pkgs []string) string {
	shown := pkgs
	suffix := ""
	if len(shown) > maxAbstentionPackages {
		suffix = fmt.Sprintf(" and %d more", len(shown)-maxAbstentionPackages)
		shown = shown[:maxAbstentionPackages]
	}
	quoted := make([]string, 0, len(shown))
	for _, p := range shown {
		quoted = append(quoted, fmt.Sprintf("%q", boundPath(p)))
	}
	return fmt.Sprintf("%d package(s) (%s%s)", len(pkgs), strings.Join(quoted, ", "), suffix)
}

// boundPath caps one repository-controlled path at trust.MaxPathLength with a
// visible truncation marker.
func boundPath(p string) string {
	if len(p) <= trust.MaxPathLength {
		return p
	}
	const marker = "…(truncated)"
	return p[:trust.MaxPathLength-len(marker)] + marker
}
