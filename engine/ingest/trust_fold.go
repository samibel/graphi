package ingest

// Per-language semantic trust facts and their fold back into the single-slot
// consumers (ADR 0007; the WP-J3 entry criterion of the language-GA program).
//
// trust.Snapshot schema v1 carries ONE TypeResolutionFacts value and the
// package-evidence table carries no language column — both shapes predate the
// registry seam and their bytes are contracts (the snapshot's canonical Encode
// IS the digest; the evidence rows are read back by generation). Widening
// those shapes is a schema decision the language-GA program takes with WP-J3,
// not something to smuggle into a refactor. So the Ingester keeps per-language
// facts internally (semanticRuns) and folds them here, with one hard rule:
//
//	THE SINGLE-LANGUAGE FOLD IS THE IDENTITY.
//
// With the Go-only registry every published byte is exactly what the pre-seam
// single-slot fields produced. The multi-language folds below are the honest
// whole-repo aggregation (summed counters, re-merged bounded sample,
// language-sorted row concatenation) — and the KNOWN LIMIT that a directory
// holding two languages' units would produce two rows with one PackageKey is
// recorded at combinedPackageEvidence, because fixing it needs the schema
// decision above.

import (
	"sort"

	"github.com/samibel/graphi/engine/trust"
)

// semanticRun is one language's completed third-phase recompute, folded at the
// one point the full typeresolve.Result exists transiently.
type semanticRun struct {
	facts    trust.TypeResolutionFacts
	evidence []PackageEvidence
}

// recordSemanticRun stores a completed run under its language. Lazy map init:
// resetTrustSignals clears to nil so a skipped pass holds no allocation.
func (i *Ingester) recordSemanticRun(lang string, run semanticRun) {
	if i.semanticRuns == nil {
		i.semanticRuns = map[string]semanticRun{}
	}
	i.semanticRuns[lang] = run
}

// semanticLanguages returns the languages with a completed run, sorted — the
// deterministic fold order.
func (i *Ingester) semanticLanguages() []string {
	langs := make([]string, 0, len(i.semanticRuns))
	for l := range i.semanticRuns {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	return langs
}

// combinedTypeResolutionFacts folds the per-language facts into the one
// TypeResolutionFacts slot trust.Snapshot schema v1 carries. Zero runs yield
// the zero value (a skipped pass claims no facts — the pre-seam behavior);
// one run is returned UNCHANGED (the identity rule above); several runs sum
// their counters and re-merge the bounded DegradedUnits sample under its
// existing (Dir, Name) order and MaxDegradedUnits cap.
func (i *Ingester) combinedTypeResolutionFacts() trust.TypeResolutionFacts {
	langs := i.semanticLanguages()
	switch len(langs) {
	case 0:
		return trust.TypeResolutionFacts{}
	case 1:
		return i.semanticRuns[langs[0]].facts
	}
	out := trust.TypeResolutionFacts{DegradedUnits: []trust.DegradedUnit{}}
	for _, l := range langs {
		f := i.semanticRuns[l].facts
		out.UnitsTotal += f.UnitsTotal
		out.UnitsDegraded += f.UnitsDegraded
		out.SkippedFiles += f.SkippedFiles
		out.DroppedIntents += f.DroppedIntents
		out.ConfirmedEdges += f.ConfirmedEdges
		out.TypeErrors += f.TypeErrors
		out.DegradedUnits = append(out.DegradedUnits, f.DegradedUnits...)
	}
	sort.Slice(out.DegradedUnits, func(a, b int) bool {
		if out.DegradedUnits[a].Dir != out.DegradedUnits[b].Dir {
			return out.DegradedUnits[a].Dir < out.DegradedUnits[b].Dir
		}
		return out.DegradedUnits[a].Name < out.DegradedUnits[b].Name
	})
	if len(out.DegradedUnits) > trust.MaxDegradedUnits {
		out.DegradedUnits = out.DegradedUnits[:trust.MaxDegradedUnits]
	}
	return out
}

// combinedPackageEvidence concatenates the per-language evidence rows in
// sorted-language order; one language returns its rows unchanged.
//
// KNOWN LIMIT (multi-language, WP-J3): PackageKey is a bare directory, so a
// directory holding units of TWO languages would contribute one row per
// language under the SAME key, and insertPackageEvidenceTx's INSERT OR
// REPLACE would keep only the later one. Harmless while one registrant
// exists; the fix is a language column in trust_package_evidence — a schema
// decision WP-J3 must take BEFORE registering a second language, tracked in
// the language-GA program.
func (i *Ingester) combinedPackageEvidence() []PackageEvidence {
	langs := i.semanticLanguages()
	if len(langs) == 1 {
		return i.semanticRuns[langs[0]].evidence
	}
	var out []PackageEvidence
	for _, l := range langs {
		out = append(out, i.semanticRuns[l].evidence...)
	}
	return out
}

// semanticEvidenceReady reports whether this pass's package-evidence rows may
// replace the persisted set WHOLESALE on the live (incremental) path: at
// least one resolver completed a whole-repo recompute AND none skipped on a
// failed re-read. A pass that skipped every resolver (kill switch, fast
// profile, no subjects) must leave the persisted rows alone — zero rows from
// a skipped resolver are absent evidence, not evidence of zero packages. A
// read-failure pass must too, because the failed language cannot contribute
// rows and a replace would delete its persisted evidence.
func (i *Ingester) semanticEvidenceReady() bool {
	return len(i.semanticRuns) > 0 && !i.semanticReadFailure
}
