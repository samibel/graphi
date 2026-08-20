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
// GA claim for each residual language. The matrix row is a pending GA
// declaration at SW-184 close — CheckGALanguages will report one
// violation per UNKNOWN evidence row, which is the EXPECTED state,
// but the row itself MUST exist (the spec's AC-2: `GA-LANG-<lang>-G1..G9`
// rows for each language). A language that appears in the residual
// set but not in the matrix has no scaffold at all, vacuous or
// otherwise.
//
// Both directions are asserted: every residual language has a matrix
// row, and no extra ga-language row exists that the residual set did
// not authorize (otherwise a future addition silently leaks in).
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

	var missing []string
	for _, lang := range residualCrossFileHeuristicLanguages {
		row, ok := matrixGALangs[lang]
		if !ok {
			missing = append(missing, lang)
			continue
		}
		if row.Status != StatusShipped {
			t.Errorf("ga-language row %q: status %q, want %q (a GA "+
				"language must be shipped; planned/partial cannot be GA — galang.go:108)",
				lang, row.Status, StatusShipped)
		}
		if row.CapabilityLevel != string(trust.CapabilityCrossFileHeuristic) {
			t.Errorf("ga-language row %q: capability %q, want %q (the "+
				"residual set is the cross-file-heuristic half per SW-183; a different level "+
				"means a re-grade happened and SW-184's scope changed)",
				lang, row.CapabilityLevel, trust.CapabilityCrossFileHeuristic)
		}
		if row.Tier != TierLabs {
			t.Errorf("ga-language row %q: tier %q, want %q (tier is "+
				"structural — ga-language rows are NOT operation ids and carry no "+
				"information about GA, so they read %q by definition; coverage-matrix.yaml:30-37)",
				lang, row.Tier, TierLabs, TierLabs)
		}
	}
	if len(missing) > 0 {
		t.Errorf("ga-language rows missing for residual languages %v — "+
			"every SW-184 language needs a `category: ga-language` row in "+
			"docs/coverage-matrix.yaml with `capability: cross-file-heuristic`. "+
			"Without a row, the language cannot be GA at any gate value and "+
			"the evidence rows are invisible to CheckGALanguages (galang.go:96-98).",
			missing)
	}

	// No residual UNRELATED ga-language rows: the matrix has only the
	// `go` grandfather row + the 6 already-shipped (java/kotlin/python/
	// typescript/tsx/javascript) + the 9 residual = 16. If this number
	// changes outside a SW-184 follow-on, the assertion below fails
	// loudly.
	var extra []string
	for lang := range matrixGALangs {
		if lang == "go" {
			continue // grandfathered; carries no GA-LANG-* rows
		}
		if residual[lang] {
			continue
		}
		switch lang {
		case "java", "kotlin", "python", "typescript", "tsx", "javascript":
			continue // already shipped by SW-174 / SW-181 / SW-182
		default:
			extra = append(extra, lang)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		t.Errorf("ga-language rows in the matrix for languages outside the SW-184 "+
			"residual and the already-shipped set %v — the matrix change for "+
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
// ordering constraint — the rule from
// CheckGALanguages / galang.go:129-131:
//
//	Rows for a language are born UNKNOWN FIRST while the language has
//	NO ga-language matrix row; the matrix row is added LAST, only
//	once every GA-LANG-<lang>-* row reads PASS with evidence URI
//	and sha.
//
// This test pins that the matrix row exists for each residual
// language (the scaffold is structurally complete) AND that the
// per-language evidence rows all read UNKNOWN. The combination is
// what makes the `go run ./cmd/coverage -check` RED-with-the-right-
// violations shape (one violation per UNKNOWN row) rather than
// RED-with-the-wrong-shape (phantom rows, missing rows, premature
// PASS). The "ordering constraint" is the property, not the file
// position — the test reads the YAMLs and asserts the structural
// shape.
func TestCrossFileHeuristicResidual_OrderingConstraint(t *testing.T) {
	caps, err := LoadMatrix(crossFileResidualMatrixPath)
	if err != nil {
		t.Fatalf("read coverage matrix %s: %v", crossFileResidualMatrixPath, err)
	}

	matrixResidualSet := map[string]bool{}
	for _, c := range caps {
		if c.Category != CategoryGALanguage {
			continue
		}
		if isResidualLang(c.ID) {
			matrixResidualSet[c.ID] = true
		}
	}

	rows, err := loadEvidenceIndexGALangRows(crossFileResidualEvidencePath)
	if err != nil {
		t.Fatalf("load evidence index: %v", err)
	}

	var perLangEvidence, perLangMatrixMissing []string
	for _, lang := range residualCrossFileHeuristicLanguages {
		evidencePresent := false
		for _, r := range rows {
			l, _, ok := splitGALangRowID(r.ID)
			if !ok || l != lang {
				continue
			}
			evidencePresent = true
			break
		}
		if !evidencePresent {
			perLangEvidence = append(perLangEvidence, lang)
		}
		if !matrixResidualSet[lang] {
			perLangMatrixMissing = append(perLangMatrixMissing, lang)
		}
	}

	// The structural half: every residual language must appear in
	// BOTH the evidence index (with all nine rows; pinned above) and
	// the coverage matrix. Lopsided growth (evidence rows without a
	// matrix row, or a matrix row without evidence rows) violates the
	// ordering constraint in either direction.
	if len(perLangEvidence) > 0 {
		t.Errorf("residual languages with NO GA-LANG-* evidence rows in docs/rc/evidence-index.yaml %v\n"+
			"the ordering constraint at galang.go:129-131 is rows-first, matrix-row-LAST; "+
			"a matrix row without evidence rows inverts it and would let CheckGALanguages "+
			"report the row as having no evidence (galang.go:133-134).", perLangEvidence)
	}
	if len(perLangMatrixMissing) > 0 {
		t.Errorf("residual languages present in evidence rows but MISSING from the coverage "+
			"matrix %v\n"+
			"the ordering constraint at galang.go:129-131 is rows-FIRST, matrix-row-LAST "+
			"in the SAME change. A language with evidence rows but no matrix row never "+
			"becomes GA — it lives in the index but never reaches the user-facing surface.",
			perLangMatrixMissing)
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
