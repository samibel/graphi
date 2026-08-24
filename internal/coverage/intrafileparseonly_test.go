package coverage

// SW-185 (language-GA program, Wave 3 residual) — THE LIGHT GATE SET for the
// intra-file-only AND parse-only languages.
//
// Story context. SW-184 closed the cross-file-heuristic half of the Wave 3
// residual (the 9 languages still standing at `cross-file-heuristic` after
// SW-174/SW-181/SW-182). SW-185 closes the OTHER half — the 6 languages whose
// declared capability is BELOW cross-file-heuristic. Per SW-183's audit
// (docs/rc/capability-audit-2026-08-19.md, rows 17–22, AC-1/AC-8), the closed
// set is:
//
//	intra-file-only (5): css, hcl, markdown, toml, yaml
//	parse-only      (1): json
//
// These are the languages where graphi ships a parser but the language itself
// defines no cross-file mechanism the product could resolve — CSS `@import`
// exists in the source but graphi deliberately declines the relation
// (parser_css.go:94, "no import system (Imports empty)"); Markdown's
// reference-style links stay within the file; YAML/TOML/HCL have no include
// directive at all (the `include:` key in YAML is an ordinary mapping, not a
// language-level include); JSON mints no node of any kind (RFC 8259, parse-only
// by construction). The audit measured each — rows 17–22 — and recorded
// "holds" for every one. SW-185 ships the LIGHT GA scaffold for these six.
//
// What "light" means here. The same light scaffold SW-184 shipped for the
// cross-file-heuristic residual: the GA-LANG-<lang>-G1..G9 rows (born UNKNOWN),
// a ga-language matrix row per language, and this guard. The per-language
// parity-class YAML, the conformance table, the hero fixture, the perf wiring
// and the per-language real-repository parity measurement are the per-language
// follow-on stories (each named on the G rows' `next_action` field). The
// honest-UNKNOWN state is the SAME shape SW-184 shipped: rows born UNKNOWN,
// the matrix row added in the same change per the ordering constraint at
// CheckGALanguages / galang.go:129-131, and the `go run ./cmd/coverage
// -check` violation count reads ONE VIOLATION PER UNKNOWN ROW — the EXPECTED
// state at SW-185 close, not a regression.
//
// What the level changes here. The cross-file-heuristic residual carries
// G2SUB (a heuristic resolver's contract, the substituted gate at lower
// tiers). The intra-file-only / parse-only languages carry a DIFFERENT
// substitution: G2 itself is also substituted at these levels, but the
// substitution is the language's own language-spec bound (no cross-file
// contract to assert at all — the contract is "this language cannot express
// the relation"), and the gate name is unchanged. The two reading-shape
// differences:
//
//  1. `related_files` on these languages returns an honest empty (the
//     `hero-03-search-empty` pattern documented in
//     docs/plan/2026-08-per-language-ga-program-v1.md §10.4 and
//     docs/plan/2026-08-per-language-ga-template-v1.md §5.5) — not an error,
//     not a guess, and the trust report names the language's level so the
//     absence is legible as a capability boundary rather than a finding.
//  2. The applicability disposition (AC-3, the per-language `change-class`
//     reduction) is the load-bearing half of the G3 row at these levels.
//     `nil` is no longer an acceptable drift-guard answer for
//     intra-file-only / parse-only — every Go change class must declare
//     `applicable`, `adapted` or `not_applicable` with a reason, so that a
//     class which does not apply is recorded as inapplicable rather than
//     absent.
//
// What this guard pins. Five properties that together make the residual
// scaffold an honest UNKNOWN rather than a vacuous green:
//
//  1. The closed residual language set matches SW-183's audit rows 17–22:
//     five intra-file-only (css, hcl, markdown, toml, yaml) plus one
//     parse-only (json). The audit is the source of truth; the
//     pre-audit list is not. (AC-1)
//  2. Every residual language has GA-LANG-<lang>-{G1,G2,G3..G9} rows in
//     docs/rc/evidence-index.yaml — NINE rows per language (no G2SUB at
//     these levels; the language spec itself is the substituted gate, and
//     G2 keeps its canonical name so the row id matches the closed row
//     set). The closed row set per language is G1, G2, G3, G4, G5, G6,
//     G7, G8, G9. (AC-2: parity-class YAML + applicability disposition
//     are the per-language follow-on; the scaffold ships the row at
//     UNKNOWN.)
//  3. Every residual language has a `category: ga-language` row in
//     docs/coverage-matrix.yaml with the correct capability level
//     (`intra-file-only` for the five; `parse-only` for json) and
//     `status: shipped`. This is the matrix-side GA claim, and it is the
//     only place a language's GA declaration lives.
//  4. The ordering constraint at CheckGALanguages / galang.go:129-131:
//     evidence rows are born UNKNOWN FIRST while the language is absent
//     from the matrix; the matrix row appears LAST in the same change.
//     Until every GA-LANG row reads PASS with evidence URI and sha, the
//     matrix row is a pending GA claim and the CheckGALanguages output
//     legitimately fails with one violation per UNKNOWN row — that is the
//     EXPECTED state at SW-185 close.
//  5. Every GA-LANG-<lang>-* row for every residual language reads
//     `status: UNKNOWN` at SW-185 close — the scaffold is not a
//     discharged claim, by the spec's own rule that AC-8's "may finish
//     without a GA declaration" is an outcome, not a failure.
//
// What this guard does NOT pin. The per-language parity-class YAML, the
// applicability disposition, the conformance table, the hero fixture, the
// perf wiring, the per-language real-repository parity measurement, and
// the hero-gate assertions — those are the per-language follow-on stories
// (each named on the G rows' `next_action` field). This file pins the
// SCAFFOLD; the rest of the SW-180 asset set arrives when each language
// is graded to GA by its own owner-gated work.
//
// The test name "light gate set" mirrors the SW-184 ticket and the
// unconditional half of its language statement ("a gate discharged on one
// language shall not be recorded as discharged for another"). C and C++
// share a resolver path but each carries its own evidence rows (SW-184
// AC-4); nothing analogous applies here — the six residual languages each
// have their own parser (`core/parse/parser_css.go`, `parser_markdown.go`,
// etc.) and their own capability level, and the per-language assertion
// here is per-language for the same reason.
//
// Implementation note. The loader deliberately uses TEXT-BASED scanning
// of docs/rc/evidence-index.yaml and not a generic YAML unmarshal, for
// the same reason SW-184 made that choice: the scaffold-level checks
// (language set, matrix row, evidence row, status UNKNOWN) only need the
// row id and the row's `status:` field, and the evidence-index is a
// CONSTRAINED SUBSET of YAML where each gate is one flat block preceded
// by `- id: GA-LANG-...`. The text-based scan is robust to the
// surrounding YAML's tolerance for embedded quotes / parentheses /
// backticks in `threshold:` and `current:`, which is load-bearing: the
// same pre-existing typescript G2SUB threshold at line 781 (which embeds
// `"pkg"` and `"pkg.fn"` inside a flow-style double-quoted scalar) is
// what forced SW-184 onto the text-based path, and reusing that path
// here keeps the SW-185 build from silently editing unrelated lines.
// The helpers (loadEvidenceIndexGALangRows, stripGAIDLine,
// stripStatusLine, splitGALangRowID, uniqSortedStrings) are the SAME
// helpers SW-184 ships; they live in crossfileheuristicresidual_test.go
// and are reachable here because both files are in the same package.

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/trust"
)

// residualIntraFileParseOnlyLanguages is the CLOSED SET of languages
// SW-185 grades, taken from the SW-183 audit output:
//
//   - docs/rc/capability-audit-2026-08-19.md, rows 17, 18, 19, 20, 21, 22
//     (the five intra-file-only rows and the one parse-only row).
//
// SW-183 measured no cross-file edge for any entry here — these are the
// languages whose declared capability is BELOW cross-file-heuristic. SW-185
// promotes the audit's row-level verdict ("holds") to a per-language GA
// SCAFFOLD: the matrix row, the nine GA-LANG-* evidence rows, and this
// guard.
//
// IF the audit re-grades one of these languages UPWARD at a later round
// (which would land in SW-185's record), the set must change HERE in the
// same change, and the affected matrix row + evidence rows must be
// re-marked rather than silently rewritten.
var residualIntraFileParseOnlyLanguages = []struct {
	Lang  string
	Level trust.CapabilityLevel
}{
	{"css", trust.CapabilityIntraFileOnly},
	{"hcl", trust.CapabilityIntraFileOnly},
	{"markdown", trust.CapabilityIntraFileOnly},
	{"toml", trust.CapabilityIntraFileOnly},
	{"yaml", trust.CapabilityIntraFileOnly},
	{"json", trust.CapabilityParseOnly},
}

// residualIntraFileParseOnlyRowGates is the closed set of
// GA-LANG-<lang>-* row ids each residual language MUST carry at SW-185
// close, in canonical order. The order is the published convention
// (G1, G2, G3 .. G9) so the diff between languages diffs side by side.
//
// Unlike SW-184's cross-file-heuristic residual, G2 is NOT SUBSTITUTED
// here. The G2 substitution at SW-184 documented the heuristic
// resolver's NEVER-CONFIRMED contract — a property the cross-file-
// heuristic languages carry. The intra-file-only / parse-only languages
// carry a DIFFERENT shape: the language itself defines no cross-file
// mechanism to resolve, so the G2 row documents the language-spec
// abstention (no cross-file contract at all) rather than a substituted
// resolver contract. The row id keeps the canonical G2 name; the
// `current` field carries the language-spec bound; the G2:G2SUB
// distinction is a property of SW-184's level, not a property of the
// row id format.
var residualIntraFileParseOnlyRowGates = []string{
	"G1", "G2", "G3", "G4", "G5", "G6", "G7", "G8", "G9",
}

// intraFileParseOnlyMatrixPath is the relative path to the coverage
// matrix YAML, resolved against this package's directory (the go test
// working directory). Mirrors crossfileheuristicresidual_test.go's
// matrix-path const and the engine/conformance/pythonparity_matrix_test.go
// precedent.
const intraFileParseOnlyMatrixPath = "../../docs/coverage-matrix.yaml"

// intraFileParseOnlyEvidencePath is the relative path to the
// evidence-index YAML, resolved the same way. Mirrors the const in
// crossfileheuristicresidual_test.go.
const intraFileParseOnlyEvidencePath = "../../docs/rc/evidence-index.yaml"

// intraFileParseOnlyWithdrawalsPath is the relative path to the
// SW-178 matrix-discipline withdrawals document, the same file the
// cross-file-heuristic tests reference. The intra-file-only / parse-
// only tests classify each residual language against this doc to
// distinguish ACTIVE (matrix row present) from WITHDRAWN (matrix row
// absent + re-introducer recorded) states.
const intraFileParseOnlyWithdrawalsPath = "../../docs/rc/ga-language-withdrawals-2026-08-21.md"

// TestIntraFileParseOnly_LanguageSetFromAudit pins AC-1 / AC-4: the
// closed set of languages SW-185 grades matches the SW-183 audit rows
// 17–22 (the five intra-file-only rows plus the one parse-only row).
// This is the "language set shall be taken from SW-183's audit output"
// requirement, and the closed-set property — the same six languages, no
// more, no fewer, and the level the matrix row carries matches the
// audit's measured level per row.
//
// The assert is on the NAMED SET plus the per-language level, not on a
// recomputed derivation: a future SW-183 run that re-grades one of these
// languages UPWARD (cross-file-heuristic, for example) is a CHANGE to
// this guard (and a CHANGE to the matrix rows and the evidence rows),
// not a silent green. That is the discipline the spec records in its
// "audit re-grade" path — a re-grade moves the scaffold, and the moves
// are visible here.
func TestIntraFileParseOnly_LanguageSetFromAudit(t *testing.T) {
	if got, want := len(residualIntraFileParseOnlyLanguages), 6; got != want {
		t.Fatalf("residual language set has %d entries, want %d; the SW-185 "+
			"language set must be re-derived from the SW-183 audit if this fails "+
			"and the new set updated here in the same change", got, want)
	}
	for _, entry := range residualIntraFileParseOnlyLanguages {
		if entry.Lang == "" {
			t.Fatalf("VACUOUS: residualIntraFileParseOnlyLanguages contains an empty entry")
		}
		if entry.Level == "" {
			t.Fatalf("VACUOUS: residualIntraFileParseOnlyLanguages[%q] has empty level", entry.Lang)
		}
		if !entry.Level.Valid() {
			t.Fatalf("residualIntraFileParseOnlyLanguages[%q] has invalid level %q (must be "+
				"one of engine/trust's closed levels)", entry.Lang, entry.Level)
		}
	}
	// The intra-file-only / parse-only set is the SHIPPED set; the
	// derivation carries these levels for every one of them today, and
	// SW-183's audit measured them all at the declared level. A future
	// re-grade UPWARD (cross-file-heuristic) would land here as a
	// removed entry, NOT as a silent inclusion in both sets.
	for _, entry := range residualIntraFileParseOnlyLanguages {
		switch entry.Level {
		case trust.CapabilityIntraFileOnly, trust.CapabilityParseOnly:
			// expected
		default:
			t.Errorf("residual entry %q has level %q; SW-185 covers only the "+
				"intra-file-only and parse-only groups — cross-file-heuristic is SW-184's "+
				"residual, typed-confirmed is the go grandfather row", entry.Lang, entry.Level)
		}
	}
	// Uniqueness: no language appears twice with different levels.
	seen := map[string]trust.CapabilityLevel{}
	for _, entry := range residualIntraFileParseOnlyLanguages {
		if prev, ok := seen[entry.Lang]; ok && prev != entry.Level {
			t.Errorf("residual language %q appears twice with different levels: %q vs %q",
				entry.Lang, prev, entry.Level)
		}
		seen[entry.Lang] = entry.Level
	}
}

// TestIntraFileParseOnly_MatrixRowsExist pins the matrix-side GA claim
// for each residual language under the SW-178 matrix-discipline. Per
// SW-178, a `category: ga-language` matrix row is in place ONLY when
// its gate is green — every GA-LANG-<lang>-* row reads PASS — and
// absent (commented out per SW-178 §D) only when the language has a
// re-introducer story id in
// docs/rc/ga-language-withdrawals-2026-08-21.md.
//
// The test asserts the three valid states per residual language:
//
//  1. ACTIVE — the matrix row is present with `capability:` matching
//     the residual set's level (intra-file-only or parse-only),
//     `status: shipped`, `tier: labs`. The level is asserted per-
//     language — a `yaml` row that carried `cross-file-heuristic`
//     would match the row-existence check but fail the level check,
//     which is the property the audit (re-grade) path requires.
//  2. WITHDRAWN — the matrix row is absent, AND every
//     GA-LANG-<lang>-{G1,G2,G3..G9} evidence row reads UNKNOWN, AND
//     the language is recorded in the SW-178 withdrawals doc with a
//     non-empty re-introducer story id.
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
func TestIntraFileParseOnly_MatrixRowsExist(t *testing.T) {
	caps, err := LoadMatrix(intraFileParseOnlyMatrixPath)
	if err != nil {
		t.Fatalf("read coverage matrix %s: %v", intraFileParseOnlyMatrixPath, err)
	}

	matrixGALangs := map[string]Capability{}
	for _, c := range caps {
		if c.Category != CategoryGALanguage {
			continue
		}
		matrixGALangs[c.ID] = c
	}

	withdrawn, err := loadWithdrawnLanguages(intraFileParseOnlyWithdrawalsPath)
	if err != nil {
		t.Fatalf("read withdrawals doc %s: %v", intraFileParseOnlyWithdrawalsPath, err)
	}

	var (
		activeAndWithdrawn   []string
		withdrawnBadContract []string
		unauthorized         []string
	)

	residualSet := map[string]bool{}
	for _, entry := range residualIntraFileParseOnlyLanguages {
		residualSet[entry.Lang] = true
	}

	for _, entry := range residualIntraFileParseOnlyLanguages {
		switch classifyResidualLang(entry.Lang, matrixGALangs, withdrawn) {
		case stateActive:
			row := matrixGALangs[entry.Lang]
			if row.Status != StatusShipped {
				t.Errorf("active ga-language row %q: status %q, want %q (a GA "+
					"language must be shipped; planned/partial cannot be GA — galang.go:108)",
					entry.Lang, row.Status, StatusShipped)
			}
			if row.CapabilityLevel != string(entry.Level) {
				t.Errorf("active ga-language row %q: capability %q, want %q (the "+
					"residual set's measured level per SW-183 audit; a mismatch means the "+
					"audit re-graded the language and the matrix row was not updated)",
					entry.Lang, row.CapabilityLevel, entry.Level)
			}
			if row.Tier != TierLabs {
				t.Errorf("active ga-language row %q: tier %q, want %q (tier is "+
					"structural — ga-language rows are NOT operation ids and carry no "+
					"information about GA, so they read %q by definition; coverage-matrix.yaml:30-37)",
					entry.Lang, row.Tier, TierLabs, TierLabs)
			}
		case stateWithdrawn:
			// SW-178 WITHDRAWN contract under the SW-199 slice
			// decomposition: the matrix row is absent (by the state
			// classifier), AND the language is recorded in the
			// withdrawals doc with a re-introducer story id. The
			// matrix-row-absence invariant is load-bearing. Evidence
			// rows can flip to PASS for gates measured by their
			// respective shipped slices (G3 from SW-199, G4 from
			// SW-200, G5 from SW-201, etc.); the matrix row stays
			// absent until all nine gates are PASS — that is the
			// ACTIVE half held by TestIntraFileParseOnly_OrderingConstraint.
			//
			// THIS IS THE SAME AMENDMENT SW-194 ALREADY MADE to the
			// sibling guard for the cross-file-heuristic residual
			// (crossfileheuristicresidual_test.go:305-322), applied
			// here for the same reason and in the same shape: the
			// "all rows UNKNOWN" clause was the SW-185-close SNAPSHOT,
			// not the matrix discipline. The withdrawals doc says so
			// itself — "The PASS rows that flipped in PR #128 are NOT
			// removed. They live in docs/rc/evidence-index.yaml and
			// will be re-bound when the matrix rows are
			// re-introduced" — and re-introduction is defined there as
			// happening "once its G1..G9 evidence rows are all PASS",
			// which is unreachable if no row may ever leave UNKNOWN
			// while withdrawn.
			//
			// We still pin the row-set (every residual language
			// carries the closed set G1, G2, G3..G9 — not missing
			// rows) so a DELETION of evidence rows cannot silently
			// launder a gate to PASS; we do NOT pin "all rows UNKNOWN"
			// because that is the per-slice disposition, not the
			// matrix discipline.
			gateStatus := evidenceRowsByID(t, intraFileParseOnlyEvidencePath, entry.Lang)
			var missing []string
			for _, g := range residualIntraFileParseOnlyRowGates {
				if _, ok := gateStatus[g]; !ok {
					missing = append(missing, g)
				}
			}
			if len(missing) > 0 {
				withdrawnBadContract = append(withdrawnBadContract,
					fmt.Sprintf("%s (missing evidence rows: %v — every residual "+
						"language must carry the closed set G1, G2, G3..G9 even "+
						"when WITHDRAWN)", entry.Lang, missing))
			}
			if w, ok := withdrawn[entry.Lang]; !ok || strings.TrimSpace(w.Reintroducer) == "" {
				withdrawnBadContract = append(withdrawnBadContract,
					fmt.Sprintf("%s (no re-introducer story id in withdrawals doc)", entry.Lang))
			}
		case stateUnauthorized:
			// Distinguish "both" from "neither" for clearer error
			// messages — a "both" failure is a SW-178 violation; a
			// "neither" failure is a scaffold that never landed.
			_, inMatrix := matrixGALangs[entry.Lang]
			_, inWithdrawals := withdrawn[entry.Lang]
			if inMatrix && inWithdrawals {
				activeAndWithdrawn = append(activeAndWithdrawn, entry.Lang)
			} else {
				unauthorized = append(unauthorized, entry.Lang)
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
		if residualSet[lang] {
			continue // SW-185 residual set; state checked above
		}
		extra = append(extra, lang)
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		t.Errorf("ga-language rows in the matrix for languages outside the SW-185 "+
			"residual and the `go` grandfather %v — the matrix change for "+
			"this story must NOT add ga-language rows the residual scope did not authorise",
			extra)
	}
}

// TestIntraFileParseOnly_GALangRowsExist pins AC-2's evidence
// scaffolding: every residual language carries the closed set of nine
// GA-LANG-<lang>-G<n> rows in docs/rc/evidence-index.yaml (G1, G2,
// G3..G9). At SW-185's level, G2 is NOT substituted — the language
// spec itself is the substituted gate, and the row id keeps the
// canonical G2 name. The substitution of SW-184's G2SUB is a property
// of the cross-file-heuristic LEVEL (a heuristic resolver's NEVER-
// CONFIRMED contract); the absence here is a property of the
// intra-file-only / parse-only levels (no cross-file mechanism to
// resolve at all).
//
// Asserted both directions: every language has all nine rows; no row
// is duplicated (a duplicate G1 for one language is a typo that would
// silently hide a missing G9 from a manual review).
func TestIntraFileParseOnly_GALangRowsExist(t *testing.T) {
	rows, err := loadEvidenceIndexGALangRows(intraFileParseOnlyEvidencePath)
	if err != nil {
		t.Fatalf("load evidence index: %v", err)
	}

	want := map[string]map[string]bool{}
	for _, entry := range residualIntraFileParseOnlyLanguages {
		want[entry.Lang] = map[string]bool{}
		for _, g := range residualIntraFileParseOnlyRowGates {
			want[entry.Lang][g] = true
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
			continue // java/kotlin/python/typescript/tsx/javascript/bash/c/c_sharp/cpp/lua/php/ruby/rust/sql rows live elsewhere
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
	for _, entry := range residualIntraFileParseOnlyLanguages {
		for _, g := range residualIntraFileParseOnlyRowGates {
			if !got[entry.Lang][g] {
				missingPerLanguage = append(missingPerLanguage, entry.Lang)
				missingGates = append(missingGates, g)
			}
		}
	}
	if len(missingPerLanguage) > 0 {
		uniqLanguages := uniqSortedStrings(missingPerLanguage)
		uniqGates := uniqSortedStrings(missingGates)
		t.Errorf("missing GA-LANG row(s) for residual language(s) %v\n"+
			"every residual language must carry the closed set G1, G2, G3..G9 "+
			"(G2 is NOT SUBSTITUTED at the intra-file-only / parse-only levels — "+
			"the language spec itself is the substituted gate, and the row id keeps "+
			"the canonical G2 name). Missing row id pattern: "+
			"GA-LANG-<lang>-%s.\n"+
			"Without the row, the gate sees a phantom gap and CheckGALanguages "+
			"reports the language as insufficiently evidenced (galang.go:133).",
			uniqLanguages, uniqGates)
	}
}

// TestIntraFileParseOnly_AllRowsUnknown pins the MATRIX-ROW-ABSENCE
// invariant at the SW-199 slice-decomposition layer. Every residual
// language's `category: ga-language` matrix row MUST be ABSENT in
// docs/coverage-matrix.yaml — the WITHDRAWN discipline under SW-178
// (docs/rc/ga-language-withdrawals-2026-08-21.md).
//
// The pre-SW-199 form of this guard asserted "every GA-LANG-<lang>-*
// row reads UNKNOWN". That assertion pinned the SW-185-CLOSE SNAPSHOT,
// and it is retired here for exactly the reason SW-194 retired the
// identical clause in the sibling guard for the cross-file-heuristic
// residual (crossfileheuristicresidual_test.go:474-494): a slice
// decomposition lets evidence rows flip to PASS for the gates their
// respective slices measured (G3 from SW-199, G4 from SW-200, G5 from
// SW-201, ...) while the matrix row stays absent. Keeping the old
// clause would have made this guard's own stated re-introduction path
// — the withdrawals doc's "once its G1..G9 evidence rows are all PASS"
// — unreachable, because no row could ever leave UNKNOWN while the
// language is withdrawn.
//
// The matrix-row-absence check is the load-bearing invariant at this
// layer: a row that uncomments without all 9 gates PASS is the
// stale-row condition SW-178 was introduced to prevent. The ACTIVE
// ordering constraint (all 9 gates PASS in order) is held by
// TestIntraFileParseOnly_OrderingConstraint, the per-row evidence
// presence by TestIntraFileParseOnly_GALangRowsExist, and the
// no-unbacked-PASS rule by `cmd/evidence -check` (every PASS row must
// carry an evidence URI and a resolving blob sha). Together they cover
// what this guard used to cover before the slice decomposition.
func TestIntraFileParseOnly_AllRowsUnknown(t *testing.T) {
	caps, err := LoadMatrix(intraFileParseOnlyMatrixPath)
	if err != nil {
		t.Fatalf("read coverage matrix %s: %v", intraFileParseOnlyMatrixPath, err)
	}

	matrixGALangs := map[string]Capability{}
	for _, c := range caps {
		if c.Category != CategoryGALanguage {
			continue
		}
		matrixGALangs[c.ID] = c
	}

	var present []string
	for _, entry := range residualIntraFileParseOnlyLanguages {
		if _, ok := matrixGALangs[entry.Lang]; ok {
			present = append(present, entry.Lang)
		}
	}
	if len(present) > 0 {
		t.Errorf("residual languages have their ga-language matrix row PRESENT "+
			"in docs/coverage-matrix.yaml: %v. The SW-178 WITHDRAWN discipline "+
			"(docs/rc/ga-language-withdrawals-2026-08-21.md) requires the matrix "+
			"row to be ABSENT until every GA-LANG-<lang>-* evidence row reads "+
			"PASS — a matrix row uncommented under partial evidence is the "+
			"stale-row condition SW-178 was introduced to prevent. The ACTIVE "+
			"ordering invariant is held by TestIntraFileParseOnly_OrderingConstraint.",
			present)
	}
}

// TestIntraFileParseOnly_OrderingConstraint pins AC-2's ordering
// constraint — the rows-born-UNKNOWN-first / matrix-row-LAST rule
// from galang.go:129-131 — for ACTIVE residual languages under the
// SW-178 matrix-discipline.
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
func TestIntraFileParseOnly_OrderingConstraint(t *testing.T) {
	caps, err := LoadMatrix(intraFileParseOnlyMatrixPath)
	if err != nil {
		t.Fatalf("read coverage matrix %s: %v", intraFileParseOnlyMatrixPath, err)
	}

	matrixGALangs := map[string]Capability{}
	for _, c := range caps {
		if c.Category != CategoryGALanguage {
			continue
		}
		matrixGALangs[c.ID] = c
	}

	withdrawn, err := loadWithdrawnLanguages(intraFileParseOnlyWithdrawalsPath)
	if err != nil {
		t.Fatalf("read withdrawals doc %s: %v", intraFileParseOnlyWithdrawalsPath, err)
	}

	var (
		activeNotAllPass []string
		unauthorized     []string
	)
	for _, entry := range residualIntraFileParseOnlyLanguages {
		switch classifyResidualLang(entry.Lang, matrixGALangs, withdrawn) {
		case stateActive:
			// ACTIVE: rows-born-UNKNOWN-first / matrix-row-LAST
			// (galang.go:129-131) requires the matrix row to be in
			// place ONLY when all nine evidence rows PASS. A matrix
			// row with non-PASS evidence is the stale-row condition
			// SW-178 was introduced to prevent.
			gateStatus := evidenceRowsByID(t, intraFileParseOnlyEvidencePath, entry.Lang)
			var notPass, missing []string
			for _, g := range residualIntraFileParseOnlyRowGates {
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
						entry.Lang, missing, notPass))
			}
		case stateWithdrawn:
			// WITHDRAWN: exempt — by the SW-178 withdrawal contract
			// the matrix row is absent, so the structural ordering is
			// satisfied vacuously. (The withdrawal contract itself —
			// matrix row absent + the closed evidence row-set present
			// + re-introducer recorded — is checked in
			// TestIntraFileParseOnly_MatrixRowsExist. SW-199 retired
			// the "all evidence rows UNKNOWN" clause this comment used
			// to name; see that test and
			// TestIntraFileParseOnly_AllRowsUnknown for why.)
		case stateUnauthorized:
			unauthorized = append(unauthorized, entry.Lang)
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
	if _, err := os.Stat(intraFileParseOnlyMatrixPath); err != nil {
		t.Errorf("matrix path %s is not reachable from the test cwd: %v", intraFileParseOnlyMatrixPath, err)
	}
}

// isIntraFileParseOnlyLang is the package-local helper that answers
// "is this language id in the SW-185 residual set?" — the analog of
// SW-184's isResidualLang, kept private because it is only meaningful
// inside this test file's set definition.
func isIntraFileParseOnlyLang(lang string) bool {
	for _, e := range residualIntraFileParseOnlyLanguages {
		if e.Lang == lang {
			return true
		}
	}
	return false
}
