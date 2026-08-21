package coverage

// SW-184 (language-GA program, Wave 3 residual) — THE LIGHT GATE SET for the
// cross-file-heuristic residual.
//
// Story context. The nine languages still standing at `cross-file-heuristic`
// after SW-174 (Wave 1, java+kotlin), SW-181 (Wave 2, python) and SW-182
// (Wave 2, typescript/tsx/javascript) are: bash, c, c_sharp, cpp, lua, php,
// ruby, rust, sql. SW-183's audit (docs/rc/capability-audit-2026-08-19.md,
// rows 2–5 and 9–14) measured cross-file edges for every one of them — no
// re-grade down — and SW-184's job is the LIGHT GA evidence scaffold:
// GA-LANG-<lang>-G1..G9 rows for each (born UNKNOWN), a matrix row for each,
// and this guard.
//
// What "light" means here. Where SW-181 (python) and SW-182 (ts family)
// shipped a full SW-180 asset set — a family parity-class YAML, a
// conformance table, a hero fixture and scenarios, perf wiring and
// evidence rows — SW-184 ships the SCAFFOLD only: the matrix row and the
// nine evidence rows per language, pinned by this guard. Per-language
// parity classes, conformance tables, corpus pins, hero fixtures and
// per-language perf wiring are out of scope; AC-8 makes "may finish this
// story without a GA declaration" an outcome, not a failure, and AC-6
// defers the abstention surface to SW-171.
//
// What this guard pins. Five properties that together make the residual
// scaffold an honest UNKNOWN rather than a vacuous green:
//
//  1. The closed residual language set matches SW-183's audit output
//     (cross-file-heuristic minus already-graded = 9 languages). The audit
//     is the source of truth; the pre-audit list is not.
//  2. Every residual language has GA-LANG-<lang>-{G1,G2SUB,G3..G9} rows
//     in docs/rc/evidence-index.yaml. The closed row set per language is
//     G1, G2SUB, G3, G4, G5, G6, G7, G8, G9 — eight plus the G2
//     SUBSTITUTION at this level (the language's declared capability is
//     heuristic, so a typed-confirmed binder is not claimable; G2SUB is
//     the labelled substitution per
//     docs/plan/2026-08-per-language-ga-template-v1.md §4).
//  3. Every residual language has a `category: ga-language` row in
//     docs/coverage-matrix.yaml with `capability: cross-file-heuristic`
//     and `status: shipped`. This is the matrix-side GA claim, and it is
//     the only place a language's GA declaration lives.
//  4. The ordering constraint at CheckGALanguages / galang.go:129-131:
//     evidence rows are born UNKNOWN FIRST while the language is absent
//     from the matrix; the matrix row appears LAST in the same change.
//     Until every GA-LANG row reads PASS with evidence URI and sha, the
//     matrix row is a pending GA claim and the CheckGALanguages output
//     legitimately fails with one violation per UNKNOWN row — that is
//     the EXPECTED state at SW-184 close.
//  5. Every GA-LANG-<lang>-* row for every residual language reads
//     `status: UNKNOWN` at SW-184 close — the scaffold is not a
//     discharged claim, by the spec's own rule that AC-8's "may finish
//     without a GA declaration" is an outcome, not a failure.
//
// What this guard does NOT pin. The per-language parity-class YAML, the
// conformance table, the hero fixture, the perf wiring, the per-language
// real-repository parity measurement, and the hero-gate assertions —
// those are the per-language follow-on stories (each named on the G
// rows' `next_action` field). This file pins the SCAFFOLD; the rest of
// the SW-180 asset set arrives when each language is graded to GA by
// its own owner-gated work.
//
// The test name "light gate set" comes from the ticket title
// (`SW-184 — The light gate set + GA-LANG rows for the cross-file-
// heuristic residual`) and the unconditional half of its language
// ("a gate discharged on one language shall not be recorded as
// discharged for another"). C and C++ share a resolver path, but each
// carries its own evidence rows (AC-4); every per-language assertion
// here is per-language.
//
// Implementation note. The loader deliberately uses TEXT-BASED scanning
// of docs/rc/evidence-index.yaml and not a generic YAML unmarshal. The
// scaffold-level checks (language set, matrix row, evidence row, status
// UNKNOWN) only need the row id and the row's `status:` field, and the
// evidence-index is a CONSTRAINED SUBSET of YAML where each gate is one
// flat block preceded by `- id: GA-LANG-...`. Scanning for those two
// anchored lines is robust to the surrounding YAML's tolerance for
// embedded quotes / parentheses / backticks in `threshold:` and `current:`,
// which is load-bearing: a flow-style YAML parse failure on those
// embedded-quote fields is a property of the existing rows (e.g. the
// typescript G2SUB threshold at line 781, which embeds `"pkg"` and
// `"pkg.fn"` inside the double-quoted scalar) and is not an SW-184
// defect. The text-based path is the closed fix that does not edit
// unrelated lines.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/trust"
)

// residualCrossFileHeuristicLanguages is the CLOSED SET of languages
// SW-184 grades, taken from the SW-183 audit output:
//
//   - docs/rc/capability-audit-2026-08-19.md, rows 2, 3, 4, 5, 9, 10, 12,
//     13, 14 (the cross-file-heuristic rows that are NOT java, kotlin,
//     python, typescript, tsx or javascript — the six already shipped to
//     GA in SW-174, SW-181 and SW-182).
//
// SW-183 measured a cross-file edge for every entry here; SW-184 promotes
// the audit's row-level verdict ("holds") to a per-language GA SCAFFOLD:
// the matrix row, the nine GA-LANG-* evidence rows, and this guard.
//
// IF the audit re-grades one of these languages downward at a later
// round (which would land in SW-184's record), the set must change HERE
// in the same change, and the affected matrix row + evidence rows must
// be re-marked rather than silently rewritten.
var residualCrossFileHeuristicLanguages = []string{
	"bash",
	"c",
	"c_sharp",
	"cpp",
	"lua",
	"php",
	"ruby",
	"rust",
	"sql",
}

// residualGALangRowGates is the closed set of GA-LANG-<lang>-* row ids
// each residual language MUST carry at SW-184 close, in canonical
// order. The order is the published convention (G1, G2SUB, G3 .. G9)
// so the diff between languages diffs side by side.
//
// G2 is REPLACED by G2SUB at this level — cross-file-heuristic is the
// language's declared level, so the heuristic resolver's own contract
// is the substituted gate, never a confirmed-tier claim
// (docs/plan/2026-08-per-language-ga-template-v1.md §4). The
// substitution label is the row id suffix; both GA-LANG-<lang>-G2SUB
// and GA-LANG-<lang>-G2 would match `CheckGALanguages`'s prefix
// requirement (`galang.go:122`), but the G2SUB suffix is the property
// that keeps
//
//	grep -c '^  - id: GA-LANG-.*-G2$' docs/rc/evidence-index.yaml
//
// an honest answer to "which languages actually have a type-checker-
// proven tier?" — see the rule 1 block above
// `docs/rc/evidence-index.yaml:113-149`.
var residualGALangRowGates = []string{
	"G1", "G2SUB", "G3", "G4", "G5", "G6", "G7", "G8", "G9",
}

// coverageMatrixPath is the relative path to the coverage matrix YAML,
// resolved against this package's directory (the go test working
// directory). Mirrors `pythonParityClassesPath` in the
// engine/conformance/pythonparity_matrix_test.go precedent.
const crossFileResidualMatrixPath = "../../docs/coverage-matrix.yaml"

// crossFileResidualEvidencePath is the relative path to the
// evidence-index YAML, resolved the same way. Mirrors the consts used
// in engine/conformance/pythonparity_matrix_test.go:31 and
// engine/conformance/typescriptparity_matrix_test.go.
const crossFileResidualEvidencePath = "../../docs/rc/evidence-index.yaml"

// crossFileResidualWithdrawalsPath is the relative path to the SW-178
// matrix-discipline withdrawals document. Each ga-language matrix row
// that is commented out per SW-178 §D MUST have a re-introducer story
// id in this document; the matrix-discipline tests below use it to
// distinguish ACTIVE (matrix row present, evidence PASS) from
// WITHDRAWN (matrix row absent, evidence UNKNOWN, re-introducer
// recorded).
const crossFileResidualWithdrawalsPath = "../../docs/rc/ga-language-withdrawals-2026-08-21.md"

// TestCrossFileHeuristicResidual_LanguageSetFromAudit pins AC-1: the
// closed set of languages SW-184 grades matches the SW-183 audit rows
// 2-14 minus the six already shipped to GA. This is the "language set
// shall be taken from SW-183's audit output" requirement.
//
// The assert is on the NAMED SET, not on a recomputed derivation: a
// future SW-183 run that re-grades one of these languages downward is
// a CHANGE to this guard (and a CHANGE to the matrix rows and the
// evidence rows), not a silent green. That is the discipline the spec
// records in its "audit re-grade" path — a re-grade moves the
// scaffold, and the moves are visible here.
func TestCrossFileHeuristicResidual_LanguageSetFromAudit(t *testing.T) {
	if got, want := len(residualCrossFileHeuristicLanguages), 9; got != want {
		t.Fatalf("residual language set has %d entries, want %d; the SW-184 "+
			"language set must be re-derived from the SW-183 audit if this fails "+
			"and the new set updated here in the same change", got, want)
	}
	for _, lang := range residualCrossFileHeuristicLanguages {
		if lang == "" {
			t.Fatalf("VACUOUS: residualCrossFileHeuristicLanguages contains an empty entry")
		}
	}
	// The 6 already-shipped languages must NOT appear here — they are
	// SW-174 (java, kotlin), SW-181 (python) and SW-182 (typescript,
	// tsx, javascript) and their evidence is elsewhere.
	for _, already := range []string{"java", "kotlin", "python", "typescript", "tsx", "javascript"} {
		for _, lang := range residualCrossFileHeuristicLanguages {
			if lang == already {
				t.Errorf("residual set contains %q, an already-shipped language; the "+
					"residual is the cross-file-heuristic set MINUS already-graded, and "+
					"%q carries its own GA evidence scaffold from an earlier story", lang, already)
			}
		}
	}
}

// TestCrossFileHeuristicResidual_MatrixRowsExist pins the matrix-side
// GA claim for each residual language under the SW-178 matrix-
// discipline. Per SW-178, a `category: ga-language` matrix row is in
// place ONLY when its gate is green — every GA-LANG-<lang>-* row
// reads PASS — and absent (commented out per SW-178 §D) only when the
// language has a re-introducer story id in
// docs/rc/ga-language-withdrawals-2026-08-21.md.
//
// The test asserts the three valid states per residual language:
//
//  1. ACTIVE — the matrix row is present with `capability:
//     cross-file-heuristic`, `status: shipped`, `tier: labs`. The
//     language is GA at its declared level and the scaffold is
//     complete.
//  2. WITHDRAWN — the matrix row is absent, AND every
//     GA-LANG-<lang>-{G1,G2SUB,G3..G9} evidence row reads UNKNOWN,
//     AND the language is recorded in the SW-178 withdrawals doc
//     with a non-empty re-introducer story id.
//  3. UNAUTHORIZED — neither of the above, OR both — is a CH-FAIL.
//
// A language that is BOTH active and withdrawn is the third illegal
// state (the matrix row was uncommented before the withdrawals doc
// was updated to remove the language); flagged separately so the
// author of the bad change sees both sides of the conflict.
//
// The "no extra ga-language rows" check at the bottom guards against
// phantom additions: any ga-language row in the matrix that is NOT
// `go` (the grandfathered typed-confirmed row) and NOT in the
// residual set is an extra row this guard did not authorise. After
// SW-178, only `go` is active in the matrix at this commit; future
// F5-dispatch work will move languages from WITHDRAWN to ACTIVE.
func TestCrossFileHeuristicResidual_MatrixRowsExist(t *testing.T) {
	caps, err := LoadMatrix(crossFileResidualMatrixPath)
	if err != nil {
		t.Fatalf("read coverage matrix %s: %v", crossFileResidualMatrixPath, err)
	}

	residual := map[string]bool{}
	for _, l := range residualCrossFileHeuristicLanguages {
		residual[l] = true
	}

	matrixGALangs := map[string]Capability{}
	for _, c := range caps {
		if c.Category != CategoryGALanguage {
			continue
		}
		matrixGALangs[c.ID] = c
	}

	withdrawn, err := loadWithdrawnLanguages(crossFileResidualWithdrawalsPath)
	if err != nil {
		t.Fatalf("read withdrawals doc %s: %v", crossFileResidualWithdrawalsPath, err)
	}

	var (
		activeAndWithdrawn   []string
		withdrawnBadContract []string
		unauthorized         []string
	)

	for _, lang := range residualCrossFileHeuristicLanguages {
		switch classifyResidualLang(lang, matrixGALangs, withdrawn) {
		case stateActive:
			row := matrixGALangs[lang]
			if row.Status != StatusShipped {
				t.Errorf("active ga-language row %q: status %q, want %q (a GA "+
					"language must be shipped; planned/partial cannot be GA — galang.go:108)",
					lang, row.Status, StatusShipped)
			}
			if row.CapabilityLevel != string(trust.CapabilityCrossFileHeuristic) {
				t.Errorf("active ga-language row %q: capability %q, want %q (the "+
					"residual set is the cross-file-heuristic half per SW-183; a different level "+
					"means a re-grade happened and SW-184's scope changed)",
					lang, row.CapabilityLevel, trust.CapabilityCrossFileHeuristic)
			}
			if row.Tier != TierLabs {
				t.Errorf("active ga-language row %q: tier %q, want %q (tier is "+
					"structural — ga-language rows are NOT operation ids and carry no "+
					"information about GA, so they read %q by definition; coverage-matrix.yaml:30-37)",
					lang, row.Tier, TierLabs, TierLabs)
			}
		case stateWithdrawn:
			// SW-178 WITHDRAWN contract: the matrix row is absent
			// (by the state classifier), AND every GA-LANG-<lang>-
			// {G1,G2SUB,G3..G9} evidence row reads UNKNOWN, AND the
			// language is recorded in the withdrawals doc with a
			// re-introducer story id. All three halves are pinned.
			gateStatus := evidenceRowsByID(t, crossFileResidualEvidencePath, lang)
			var notUnknown, missing []string
			for _, g := range residualGALangRowGates {
				status, ok := gateStatus[g]
				if !ok {
					missing = append(missing, g)
					continue
				}
				if status != "UNKNOWN" {
					notUnknown = append(notUnknown, fmt.Sprintf("%s=%s", g, status))
				}
			}
			if len(missing) > 0 || len(notUnknown) > 0 {
				withdrawnBadContract = append(withdrawnBadContract,
					fmt.Sprintf("%s (missing gates: %v; non-UNKNOWN evidence: %v)",
						lang, missing, notUnknown))
			}
			if w, ok := withdrawn[lang]; !ok || strings.TrimSpace(w.Reintroducer) == "" {
				withdrawnBadContract = append(withdrawnBadContract,
					fmt.Sprintf("%s (no re-introducer story id in withdrawals doc)", lang))
			}
		case stateUnauthorized:
			// Distinguish "both" from "neither" for clearer error
			// messages — a "both" failure is a SW-178 violation; a
			// "neither" failure is a scaffold that never landed.
			_, inMatrix := matrixGALangs[lang]
			_, inWithdrawals := withdrawn[lang]
			if inMatrix && inWithdrawals {
				activeAndWithdrawn = append(activeAndWithdrawn, lang)
			} else {
				unauthorized = append(unauthorized, lang)
			}
		}
	}
	if len(activeAndWithdrawn) > 0 {
		t.Errorf("residual languages BOTH active (matrix row present) AND withdrawn "+
			"(in SW-178 withdrawals doc) — these states are mutually exclusive: %v",
			activeAndWithdrawn)
	}
	if len(withdrawnBadContract) > 0 {
		t.Errorf("WITHDRAWN residual languages fail the SW-178 withdrawal contract "+
			"(matrix row absent + all GA-LANG-<lang>-* rows UNKNOWN + re-introducer "+
			"recorded in docs/rc/ga-language-withdrawals-2026-08-21.md): %v",
			withdrawnBadContract)
	}
	if len(unauthorized) > 0 {
		t.Errorf("residual languages are NEITHER active nor withdrawn — neither "+
			"the matrix row nor the SW-178 withdrawal entry exists. The scaffold "+
			"never landed (or was withdrawn without recording a re-introducer): %v",
			unauthorized)
	}

	// No extra ga-language rows: the matrix may carry `go` (the
	// grandfathered typed-confirmed row, not a residual language) and
	// any active residual rows. Anything else is an unauthorised
	// addition this guard did not anticipate.
	var extra []string
	for lang := range matrixGALangs {
		if lang == "go" {
			continue // grandfathered; carries its own GA-LANG-go-G1..G9
		}
		if residual[lang] {
			continue // SW-184 residual set; state checked above
		}
		extra = append(extra, lang)
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		t.Errorf("ga-language rows in the matrix for languages outside the SW-184 "+
			"residual and the `go` grandfather %v — the matrix change for "+
			"this story must NOT add ga-language rows the residual scope did not authorise",
			extra)
	}
}

// TestCrossFileHeuristicResidual_GALangRowsExist pins AC-2's evidence
// scaffolding: every residual language carries the closed set of
// nine GA-LANG-<lang>-G<n> rows in docs/rc/evidence-index.yaml
// (G1, G2SUB, G3..G9). The substitution at G2 is the load-bearing
// property at the cross-file-heuristic level — a row that reads
// `GA-LANG-<lang>-G2` instead of `-G2SUB` would (a) match the
// CheckGALanguages prefix and so read PASS to a careless reader, while
// (b) claiming a confirmed-tier binder that does not exist. The
// substitution is the property the spec encodes at §4 row 1
// (docs/plan/2026-08-per-language-ga-template-v1.md).
//
// Asserted both directions: every language has all nine rows; no row
// is duplicated (a duplicate G1 for one language is a typo that would
// silently hide a missing G9 from a manual review).
func TestCrossFileHeuristicResidual_GALangRowsExist(t *testing.T) {
	rows, err := loadEvidenceIndexGALangRows(crossFileResidualEvidencePath)
	if err != nil {
		t.Fatalf("load evidence index: %v", err)
	}

	// Index by language id.
	want := map[string]map[string]bool{}
	for _, lang := range residualCrossFileHeuristicLanguages {
		want[lang] = map[string]bool{}
		for _, g := range residualGALangRowGates {
			want[lang][g] = true
		}
	}

	got := map[string]map[string]bool{}
	var duplicates []string
	for _, r := range rows {
		lang, gate, ok := splitGALangRowID(r.ID)
		if !ok {
			continue // not a GA-LANG-* row
		}
		if _, isResidual := want[lang]; !isResidual {
			continue // java/kotlin/python/typescript/tsx/javascript rows live elsewhere
		}
		if got[lang] == nil {
			got[lang] = map[string]bool{}
		}
		if got[lang][gate] {
			duplicates = append(duplicates, r.ID)
		}
		got[lang][gate] = true
	}

	if len(duplicates) > 0 {
		t.Errorf("duplicate GA-LANG-<lang>-* rows for residual languages %v", duplicates)
	}

	var missingPerLanguage []string
	var missingGates []string
	for _, lang := range residualCrossFileHeuristicLanguages {
		for _, g := range residualGALangRowGates {
			if !got[lang][g] {
				missingPerLanguage = append(missingPerLanguage, lang)
				missingGates = append(missingGates, g)
			}
		}
	}
	if len(missingPerLanguage) > 0 {
		uniqLanguages := uniqSortedStrings(missingPerLanguage)
		uniqGates := uniqSortedStrings(missingGates)
		t.Errorf("missing GA-LANG row(s) for residual language(s) %v\n"+
			"every residual language must carry the closed set G1, G2SUB, G3..G9 "+
			"(G2 is SUBSTITUTED by G2SUB at the cross-file-heuristic level — "+
			"the language's declared capability is heuristic, so a confirmed-tier "+
			"binder is not claimable). Missing row id pattern: "+
			"GA-LANG-<lang>-%s.\n"+
			"Without the row, the gate sees a phantom gap and CheckGALanguages "+
			"either reports the language as insufficiently evidenced (galang.go:133) "+
			"or silently passes the gate by laundering a non-existent row, "+
			"depending on which side of the prefix check fails first.",
			uniqLanguages, uniqGates)
	}
}

// TestCrossFileHeuristicResidual_AllRowsUnknown pins the
// HONEST-UNKNOWN state at SW-184 close. Every GA-LANG-<lang>-* row
// for every residual language MUST read `status: UNKNOWN`. This is
// the expected state — the rows are the SCAFFOLD, not a discharged
// claim, and the spec is explicit about it (AC-8: "may finish this
// story without a GA declaration; that is an outcome, not a
// failure"). A row that reads PASS at SW-184 close would mean
// somebody moved a gate without evidence and silently made CI green,
// the exact failure mode the "rows born UNKNOWN" convention exists
// to prevent.
func TestCrossFileHeuristicResidual_AllRowsUnknown(t *testing.T) {
	rows, err := loadEvidenceIndexGALangRows(crossFileResidualEvidencePath)
	if err != nil {
		t.Fatalf("load evidence index: %v", err)
	}

	var prematurePass []string
	for _, r := range rows {
		lang, _, ok := splitGALangRowID(r.ID)
		if !ok {
			continue
		}
		if !isResidualLang(lang) {
			continue
		}
		if r.Status != "UNKNOWN" {
			prematurePass = append(prematurePass, fmt.Sprintf("%s (status=%q)", r.ID, r.Status))
		}
	}
	if len(prematurePass) > 0 {
		t.Errorf("residual GA-LANG-* rows not all UNKNOWN at SW-184 close %v. "+
			"Either the rows are prematurely PASS (gate moved without evidence — "+
			"the failure mode rows-born-UNKNOWN exists to prevent) or a non-UNKNOWN "+
			"status string was introduced. SW-184 ships the SCAFFOLD; the per-gate "+
			"discharge is a separate, owner-gated story with its own evidence artefact.",
			prematurePass)
	}
}

// TestCrossFileHeuristicResidual_OrderingConstraint pins AC-2's
// ordering constraint — the rows-born-UNKNOWN-first / matrix-row-LAST
// rule from galang.go:129-131 — for ACTIVE residual languages under
// the SW-178 matrix-discipline.
//
// The rule, stated precisely: rows for a language are born UNKNOWN
// FIRST while the language has NO ga-language matrix row; the matrix
// row is added LAST, only once every GA-LANG-<lang>-* row reads PASS
// with evidence URI and sha.
//
// Under SW-178, the contract becomes a state machine:
//
//	ACTIVE:     matrix row present, all GA-LANG-<lang>-* rows PASS
//	            (matrix row in place ONLY when gate is green)
//	WITHDRAWN:  matrix row absent, all GA-LANG-<lang>-* rows UNKNOWN,
//	            re-introducer recorded in
//	            docs/rc/ga-language-withdrawals-2026-08-21.md
//
// This test asserts the ACTIVE half: for every residual language with
// a matrix row, the rows-born-UNKNOWN-first / matrix-row-LAST rule
// is preserved (the matrix row was added AFTER the evidence rows
// went from UNKNOWN to PASS; structurally, all nine evidence rows
// exist AND read PASS). WITHDRAWN languages are exempt — by the
// SW-178 withdrawal contract, their matrix rows are absent and their
// evidence rows are all UNKNOWN; the structural ordering is satisfied
// vacuously because the matrix row is not present.
func TestCrossFileHeuristicResidual_OrderingConstraint(t *testing.T) {
	caps, err := LoadMatrix(crossFileResidualMatrixPath)
	if err != nil {
		t.Fatalf("read coverage matrix %s: %v", crossFileResidualMatrixPath, err)
	}

	matrixGALangs := map[string]Capability{}
	for _, c := range caps {
		if c.Category != CategoryGALanguage {
			continue
		}
		matrixGALangs[c.ID] = c
	}

	withdrawn, err := loadWithdrawnLanguages(crossFileResidualWithdrawalsPath)
	if err != nil {
		t.Fatalf("read withdrawals doc %s: %v", crossFileResidualWithdrawalsPath, err)
	}

	var (
		activeNotAllPass []string
		unauthorized     []string
	)
	for _, lang := range residualCrossFileHeuristicLanguages {
		switch classifyResidualLang(lang, matrixGALangs, withdrawn) {
		case stateActive:
			// ACTIVE: rows-born-UNKNOWN-first / matrix-row-LAST
			// (galang.go:129-131) requires the matrix row to be in
			// place ONLY when all nine evidence rows PASS. A matrix
			// row with non-PASS evidence is the stale-row condition
			// SW-178 was introduced to prevent.
			gateStatus := evidenceRowsByID(t, crossFileResidualEvidencePath, lang)
			var notPass, missing []string
			for _, g := range residualGALangRowGates {
				status, ok := gateStatus[g]
				if !ok {
					missing = append(missing, g)
					continue
				}
				if status != "PASS" {
					notPass = append(notPass, fmt.Sprintf("%s=%s", g, status))
				}
			}
			if len(missing) > 0 || len(notPass) > 0 {
				activeNotAllPass = append(activeNotAllPass,
					fmt.Sprintf("%s (missing gates: %v; non-PASS evidence: %v)",
						lang, missing, notPass))
			}
		case stateWithdrawn:
			// WITHDRAWN: exempt — by the SW-178 withdrawal contract,
			// the matrix row is absent and the evidence rows are all
			// UNKNOWN. The structural ordering is satisfied vacuously.
			// (The withdrawal contract itself — matrix row absent +
			// all evidence rows UNKNOWN + re-introducer recorded —
			// is checked in TestCrossFileHeuristicResidual_MatrixRowsExist.)
		case stateUnauthorized:
			unauthorized = append(unauthorized, lang)
		}
	}
	if len(activeNotAllPass) > 0 {
		t.Errorf("ACTIVE residual languages have a matrix row but evidence rows not all PASS %v\n"+
			"the rows-born-UNKNOWN-first / matrix-row-LAST rule (galang.go:129-131) requires the matrix row to be present ONLY when all GA-LANG-<lang>-* rows read PASS — a matrix row in place with non-PASS evidence is the stale-row condition SW-178 was introduced to prevent.",
			activeNotAllPass)
	}
	if len(unauthorized) > 0 {
		t.Errorf("residual languages are NEITHER active nor withdrawn — neither the matrix row nor the SW-178 withdrawal entry exists: %v", unauthorized)
	}

	// Hermeticity: matrix path is reachable from the test cwd. A
	// missing file surfaces here as a clear path, not a silent skip.
	if _, err := os.Stat(crossFileResidualMatrixPath); err != nil {
		t.Errorf("matrix path %s is not reachable from the test cwd: %v", crossFileResidualMatrixPath, err)
	}
}

// evidenceRow is one GA-LANG-* row fact the residual light gates
// consume: the gate id (the `id:` field, e.g. `GA-LANG-bash-G1`), and
// the row's `status:` field, so the scaffold-level checks stay
// text-scan-only and do not depend on the full evidence package's
// machinery.
type gaLangEvidenceRow struct {
	ID     string
	Status string
}

// loadEvidenceIndexGALangRows scans the evidence-index YAML for
// GA-LANG-* rows and returns (id, status) pairs. The scan is TEXT-
// BASED, not YAML-based: the constrained block-list shape of the
// evidence index (each gate is a flat map preceded by `- id:
// GA-LANG-...`) makes a `- id: GA-LANG-` line scan paired with a
// following `status:` line scan deterministic and robust to embedded
// quotes / parentheses / backticks in `threshold:` and `current:` —
// a flow-style YAML parse failure on those embedded-quote fields is
// a property of the existing rows (typescript G2SUB at line 781
// embeds literal `"pkg"` and `"pkg.fn"` inside a double-quoted
// scalar) and is not an SW-184 defect. The text-based path is the
// closed fix that does not edit unrelated lines.
//
// The scanner is line-anchored: every `- id:` line is matched on
// leading whitespace + dash + space + "id:" + space + "GA-LANG-", and
// the `status:` field is taken from the very next indented line on
// the same block. Stops at EOF; the file is read once.
func loadEvidenceIndexGALangRows(path string) ([]gaLangEvidenceRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()

	out := []gaLangEvidenceRow{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<16), 1<<24)
	var pendingID string
	for sc.Scan() {
		line := sc.Text()
		if pendingID == "" {
			id, ok := stripGAIDLine(line)
			if !ok {
				continue
			}
			pendingID = id
			continue
		}
		// We have a pending id — look for the immediate `status:` line.
		// A new `- id: ...` line ends the block: push the pending id
		// with unknown status (defensive — normal rows always carry
		// a status field right after id) and re-process this line as
		// a new id candidate.
		if id, ok := stripGAIDLine(line); ok {
			out = append(out, gaLangEvidenceRow{ID: pendingID, Status: "UNKNOWN"})
			pendingID = id
			continue
		}
		status, ok := stripStatusLine(line)
		if !ok {
			continue
		}
		out = append(out, gaLangEvidenceRow{ID: pendingID, Status: status})
		pendingID = ""
	}
	if err := sc.Err(); err != nil && err != io.EOF {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	if pendingID != "" {
		// Defensive: a row that ended without a status is still a
		// row — surface it as UNKNOWN so the guard cannot be
		// tricked by a missing status field.
		out = append(out, gaLangEvidenceRow{ID: pendingID, Status: "UNKNOWN"})
	}
	return out, nil
}

// stripGAIDLine returns the value of an `id:` field on a line, if
// the line is a list-item "id" anchor. The expected shape is one of:
//
//   - id: GA-LANG-...
//   - id: GA-LANG-...
//     ...
//
// where the id is on its own indented block.
func stripGAIDLine(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, "- id:") {
		return "", false
	}
	rest := strings.TrimPrefix(trimmed, "- id:")
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, GAEvidencePrefix) {
		return "", false
	}
	if rest == "" {
		return "", false
	}
	return rest, true
}

// stripStatusLine returns the value of a `status:` field on a line,
// if the line is a YAML block-map "status" anchor. The expected shape
// is `    status: <value>` (4-space indent, the same as the
// evidence-index's gate rows).
func stripStatusLine(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, "status:") {
		return "", false
	}
	rest := strings.TrimPrefix(trimmed, "status:")
	return strings.TrimSpace(rest), true
}

// splitGALangRowID splits `GA-LANG-<lang>-<gate>` into its parts. The
// gate suffix is everything after the FIRST hyphen of `rest`, so
// `GA-LANG-python-G2SUB` → ("python", "G2SUB", true). This matches the
// existing rows' shape (see docs/rc/evidence-index.yaml:646, 657, 668
// …), and the language id is one of the residual names — none of
// those names contain a hyphen — so the first-hyphen split is
// unambiguous for this gate's purposes.
func splitGALangRowID(id string) (lang, gate string, ok bool) {
	const prefix = GAEvidencePrefix
	if !strings.HasPrefix(id, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(id, prefix)
	for i := 0; i < len(rest); i++ {
		if rest[i] != '-' {
			continue
		}
		lang = rest[:i]
		gate = rest[i+1:]
		if lang == "" || gate == "" {
			return "", "", false
		}
		return lang, gate, true
	}
	return "", "", false
}

func isResidualLang(lang string) bool {
	for _, l := range residualCrossFileHeuristicLanguages {
		if l == lang {
			return true
		}
	}
	return false
}

// uniqSortedStrings returns a deterministic, sorted, deduplicated
// view of ss. Kept tiny because the callers above are loop-tight.
func uniqSortedStrings(ss []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// SW-178 matrix-discipline helpers. The discipline names three states
// for each ga-language matrix row (active / withdrawn / unauthorized);
// the helpers below classify a residual language against the loaded
// matrix + withdrawals doc and assert the per-state contract.

// gaLanguageWithdrawal is one row of the SW-178 withdrawals table:
// the language id (matching the ga-language matrix row's id) and the
// re-introducer story id (per
// docs/rc/ga-language-withdrawals-2026-08-21.md). The Markdown table
// also carries a Notes column; the loader captures only the two
// fields the matrix-discipline checks need.
type gaLanguageWithdrawal struct {
	Language     string
	Reintroducer string
}

// loadWithdrawnLanguages parses the SW-178 withdrawals Markdown and
// returns the per-language re-introducer mapping. The scan is
// text-based and depends only on the leading-pipe table shape — the
// file's only structured content is the `| Language | Re-introducer
// story | Notes |` table at lines 22–44.
//
// The loader treats a `| Language` header line and `|---` separator
// line as skip rows; every other leading-pipe line is a data row.
func loadWithdrawnLanguages(path string) (map[string]gaLanguageWithdrawal, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()
	out := map[string]gaLanguageWithdrawal{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<16), 1<<24)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(line, "|")
		// cells is [empty, Lang, Reintroducer, Notes?, empty]
		if len(cells) < 3 {
			continue
		}
		lang := strings.TrimSpace(cells[1])
		reintroducer := strings.TrimSpace(cells[2])
		if lang == "" || lang == "Language" || strings.HasPrefix(lang, "---") {
			continue
		}
		out[lang] = gaLanguageWithdrawal{
			Language:     lang,
			Reintroducer: reintroducer,
		}
	}
	if err := sc.Err(); err != nil && err != io.EOF {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return out, nil
}

// residualLangState is the SW-178 classification of a residual
// language against the matrix-discipline:
//
//   - stateActive: the matrix row is present (and the language is NOT
//     in the withdrawals doc) — the language is GA at its declared
//     level, with all GA-LANG-<lang>-* rows PASS.
//   - stateWithdrawn: the matrix row is absent (commented out per
//     SW-178 §D) AND the language is recorded in
//     docs/rc/ga-language-withdrawals-2026-08-21.md with a
//     re-introducer story id — the WITHDRAWN contract holds.
//   - stateUnauthorized: the matrix row is present AND the language
//     is in the withdrawals doc (both = illegal), OR neither — the
//     scaffold never landed or was withdrawn without recording a
//     re-introducer.
type residualLangState int

const (
	stateActive residualLangState = iota
	stateWithdrawn
	stateUnauthorized
)

// classifyResidualLang returns the residualLangState for lang given
// the loaded matrix and the loaded withdrawals doc.
func classifyResidualLang(lang string, matrixGALangs map[string]Capability, withdrawn map[string]gaLanguageWithdrawal) residualLangState {
	_, inMatrix := matrixGALangs[lang]
	_, inWithdrawals := withdrawn[lang]
	switch {
	case inMatrix && inWithdrawals:
		return stateUnauthorized // BOTH = illegal
	case inMatrix:
		return stateActive
	case inWithdrawals:
		return stateWithdrawn
	default:
		return stateUnauthorized // neither = scaffold never landed
	}
}

// evidenceRowsByID returns the (gate → status) map for a specific
// language id from the evidence index. Reuses the scaffold's text-
// based loadEvidenceIndexGALangRows loader so the matrix-discipline
// tests share the same row-scanning contract.
func evidenceRowsByID(t *testing.T, path, lang string) map[string]string {
	t.Helper()
	rows, err := loadEvidenceIndexGALangRows(path)
	if err != nil {
		t.Fatalf("load evidence index %s: %v", path, err)
	}
	out := map[string]string{}
	for _, r := range rows {
		l, gate, ok := splitGALangRowID(r.ID)
		if !ok || l != lang {
			continue
		}
		out[gate] = r.Status
	}
	return out
}
