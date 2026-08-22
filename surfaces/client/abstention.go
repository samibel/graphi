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
// generation, a sidecar predating the skip table, and a GENERATION that carries
// no abstention provenance all produce a VISIBLE unavailability notice, because
// a silent absence here would read as an all-clear.
//
// THE RECORD IS BOUND TO A GENERATION AND A REPOSITORY, AND BOTH BINDINGS ARE
// LOAD-BEARING (W0.g review round 1).
//
//   - REPOSITORY. Every entry point takes the caller's root/db/meta and the
//     surfaces pass the one their SESSION is bound to. An earlier revision let
//     MCP leave them empty, which resolved the record from the server process's
//     working directory: a session bound to repository A, launched with cwd in
//     repository B, published B's abstention counters as A's. A surface whose
//     purpose is honest abstention emitting another repository's numbers is the
//     worst failure this file can have, so the paths are inputs, never ambient.
//   - GENERATION. Availability is read from the generation's own provenance row
//     (ingest.SkipRegistrants), never from the sidecar's schema version.
//     Migrating a store CREATES the skip table; it does not RECORD anything, so
//     gating on the schema flipped every pre-existing generation to
//     "available, nothing skipped" the moment a `graphi sync` migrated it.
//
// And the same idea closes the last ambiguity: Registrants says WHO recorded
// the pass, so an empty counter list is readable as "these binders ran and
// abstained from nothing" rather than "no binder ran". The trust report's
// capabilities block cannot serve that purpose — it is derived from the
// registry of the process doing the READING, so it happily reports
// java: typed-confirmed over a generation the java binder never touched.

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
//
// Registrants is what makes an empty Languages list READABLE, and it is the
// reason this block is trustworthy at all. It names the semantic registrants
// that actually recorded THIS generation, so:
//
//	available, registrants [go java], languages []  -> both binders ran and
//	                                                   abstained from nothing
//	available, registrants [go],      languages []  -> no JVM binder ran; the
//	                                                   Java code was never bound
//	available:false + reason                        -> the record cannot be read
//
// Without it the first two are the same bytes, and the second is by far the
// more common — the JVM registrants are opt-in until WP-J11. Do NOT substitute
// the document's capabilities block for this: capabilities is derived from the
// registry of the process READING the report, not from the pass that wrote the
// generation, so it reports java as typed-confirmed over a store indexed
// without the binder at all.
type AbstentionFacts struct {
	Available         bool                 `json:"available"`
	UnavailableReason string               `json:"unavailable_reason"`
	Scope             string               `json:"scope"`
	Registrants       []string             `json:"registrants"`
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

// unavailableReasonFor names WHY the abstention record could not be read, in
// terms a user can act on. The two unavailability shapes have different
// remedies and must not share one sentence: an old sidecar is fixed by opening
// the repository read-write once, while an unrecorded generation is fixed only
// by a pass that actually re-runs the binders.
func unavailableReasonFor(err error) string {
	switch {
	case errors.Is(err, ingest.ErrTrustSkipProvenanceMissing):
		return "this graph generation carries no abstention record — it was indexed before the record existed (or by a build that never wrote one), and migrating the sidecar creates the table without re-running the binders, so only a full re-index (`graphi rebuild`) can record it"
	case errors.Is(err, ingest.ErrTrustEvidenceUnavailable):
		return "this store's evidence sidecar predates the abstention record and never migrates when observed read-only"
	default:
		return "the abstention record could not be read from the evidence sidecar"
	}
}

// readAbstentionFacts reads the whole generation's named skip counters, keyed
// by the registrant that recorded them, together with the provenance that says
// which registrants recorded the generation at all. The trust report's block.
func readAbstentionFacts(ctx context.Context, root, dbPath, metaDir string) AbstentionFacts {
	out := AbstentionFacts{Scope: abstentionScopeNote, Registrants: []string{}, Languages: []AbstentionLanguage{}}
	h, reason := openEvidence(ctx, root, dbPath, metaDir)
	if h == nil {
		out.UnavailableReason = reason
		return out
	}
	defer h.close()

	// Provenance FIRST: it decides availability. Reading the counters first and
	// treating "no rows" as an answer is exactly the inversion that turned a
	// migration into a false all-clear.
	registrants, err := h.ro.SkipRegistrants(ctx, h.gen)
	if err != nil {
		out.UnavailableReason = unavailableReasonFor(err)
		return out
	}
	byLang, err := h.ro.LanguageSkips(ctx, h.gen)
	if err != nil {
		out.UnavailableReason = unavailableReasonFor(err)
		return out
	}
	out.Available = true
	out.Registrants = append(out.Registrants, registrants...)
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
	skips    []AbstentionSkip
	total    int
	packages []string
	// registrants are the semantic registrants that recorded THIS generation
	// (ingest.SkipRegistrants) — the provenance that makes a quiet notice
	// interpretable.
	registrants []string
	// unclaimed are the covered packages NO semantic registrant holds evidence
	// for under this generation. They are reported, not dropped: a directory
	// nobody examined abstains from nothing only in the sense that nobody was
	// there to abstain, and silence over it is the same all-clear this file
	// exists to refuse.
	unclaimed         []string
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

	// Provenance decides availability, and it is asked BEFORE the counters:
	// a generation with no abstention record must read "cannot answer", never
	// "answered, nothing there".
	registrants, err := h.ro.SkipRegistrants(ctx, h.gen)
	if err != nil {
		return packageAbstention{unavailableReason: unavailableReasonFor(err)}
	}
	out.registrants = registrants

	byLang, err := h.ro.LanguageSkips(ctx, h.gen)
	if err != nil {
		return packageAbstention{unavailableReason: unavailableReasonFor(err)}
	}

	// ONE query for every covered package's registrants, instead of one
	// PackageEvidence read per package each re-running one skip query per
	// language — that shape re-derived, O(packages x languages) times, the
	// byLang map this function already holds.
	claimed, err := h.ro.PackageRegistrants(ctx, h.gen, pkgKeys)
	if err != nil {
		return packageAbstention{unavailableReason: "this store's evidence sidecar cannot serve package evidence"}
	}

	covered := map[string]struct{}{}
	for _, key := range pkgKeys {
		langs, ok := claimed[key]
		if !ok {
			// No semantic registrant holds a row for this directory. That is
			// NOT "abstained from nothing" — it is "never examined", and the
			// notice below says so rather than letting silence imply the first.
			out.unclaimed = append(out.unclaimed, key)
			continue
		}
		// A package is NAMED only when one of its registrants actually recorded
		// a named skip: listing a package whose language abstained from nothing
		// would attach the notice to a directory with no abstention behind it.
		abstained := false
		for _, l := range langs {
			if len(byLang[l]) > 0 {
				abstained = true
			}
		}
		if !abstained {
			continue
		}
		out.packages = append(out.packages, key)
		for _, l := range langs {
			covered[l] = struct{}{}
		}
	}
	sort.Strings(out.unclaimed)
	if len(out.packages) == 0 {
		return packageAbstention{registrants: out.registrants, unclaimed: out.unclaimed}
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
//   - an EMPTY result covering packages NO semantic registrant examined — an
//     entry naming the gap and the registrants that did record the generation.
//     A package with no evidence row produces no abstention record for the same
//     reason an unread book contains no typos, so silence over it is not an
//     all-clear. This entry is deliberately confined to the empty case: on a
//     found result the reader already has edges to judge, while "no callers" is
//     the answer a user acts on, and it is the one AC-6 governs. Emitting it on
//     every found result would fire on every Python or TypeScript result in the
//     repository — no binder exists for those languages at all — and noise on
//     every result is how a real notice stops being read.
func abstentionLimitations(pa packageAbstention, emptyResult bool) []string {
	if pa.unavailableReason != "" {
		return []string{fmt.Sprintf(
			"abstention accounting UNAVAILABLE (%s): the semantic binders' named skip counters could not be read for this result, so the absence of an abstention notice here is not evidence that nothing was skipped",
			pa.unavailableReason)}
	}
	var out []string
	if pa.total > 0 {
		parts := make([]string, 0, len(pa.skips))
		for _, s := range pa.skips {
			parts = append(parts, fmt.Sprintf("%s %d", s.Name, s.Count))
		}
		// SCOPE BEFORE ATTRIBUTION, deliberately. The earlier wording said the
		// covered packages "carry a recorded abstention row" and only retracted
		// the per-package reading in the following clause — a reader who stops
		// at the first sentence has been told something untrue. The limit is
		// now stated before the packages are named, and the packages are named
		// as the GATE that fired, never as carriers of the counts.
		out = append(out, fmt.Sprintf(
			"semantic binder abstention: %d call/reference sites were refused under named reasons (%s). Scope first: %s, so they are reported beside this result and NOT counted inside it. %s are named because they are accounted by a language that abstained somewhere in the repository — not because the refused sites are in them. A refused site produces no edge at all, so this result may be reduced by abstention.",
			pa.total, strings.Join(parts, ", "), abstentionScopeNote, renderPackageList(pa.packages)))
		if emptyResult {
			out = append(out, fmt.Sprintf(
				"this result is EMPTY while %d sites stand refused under named reasons — abstained emptiness is not proven emptiness, and this must not be read as \"no such relationship exists\"",
				pa.total))
		}
	}
	if emptyResult && len(pa.unclaimed) > 0 {
		out = append(out, fmt.Sprintf(
			"this result is EMPTY and %s covered by it carry NO semantic-registrant evidence under this graph generation (the registrants that recorded it: %s). No binder examined them, so nothing there could have been recorded as abstained — this emptiness is unexamined, not proven, and must not be read as \"no such relationship exists\".",
			renderPackageList(pa.unclaimed), renderRegistrants(pa.registrants)))
	}
	return out
}

// renderRegistrants names the registrants that recorded the generation, and
// says "none" rather than printing an empty list — the case where no semantic
// registrant ran at all is the one a reader most needs to notice.
func renderRegistrants(langs []string) string {
	if len(langs) == 0 {
		return "none — no semantic registrant ran over this generation"
	}
	return strings.Join(langs, ", ")
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
