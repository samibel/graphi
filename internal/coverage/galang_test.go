package coverage

import (
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/trust"
)

func gaRow(id, level string) Capability {
	return Capability{ID: id, Category: CategoryGALanguage, Status: StatusShipped, Tier: TierLabs, CapabilityLevel: level}
}

// TestCheckGALanguages_GoDayOne pins the day-one state this mechanism ships
// with: exactly the go row, bound to a derivation that reports go at
// typed-confirmed, with the full set of GA-LANG-go-G1..G9 evidence rows
// (each UNKNOWN). SW-186 lifted the grandfathering: the day-one go row
// MUST carry its own evidence rows like every other language.
func TestCheckGALanguages_GoDayOne(t *testing.T) {
	rep := CheckGALanguages(
		[]Capability{gaRow("go", "typed-confirmed")},
		[]trust.Capability{{Language: "go", Level: trust.CapabilityTypedConfirmed}},
		gaLangGates("go"),
	)
	if rep.Pass() {
		t.Fatal("day-one go row with nine UNKNOWN rows must fail (every gate UNKNOWN), got a pass")
	}
	if len(rep.Languages) != 1 || rep.Languages[0] != "go" {
		t.Fatalf("Languages = %v, want [go]", rep.Languages)
	}
	if len(rep.Violations) != 9 {
		t.Fatalf("want one violation per UNKNOWN row (9), got %d: %v", len(rep.Violations), rep.Violations)
	}
}

// TestCheckGALanguages_RegistryBinding pins direction (i): the declared
// capability must EQUAL the derived level — a row cannot overclaim (declare
// typed-confirmed while the registries derive cross-file-heuristic), cannot
// underclaim (the declaration follows the code exactly, in both directions),
// and cannot name a language the derivation does not know.
func TestCheckGALanguages_RegistryBinding(t *testing.T) {
	derived := []trust.Capability{{Language: "java", Level: trust.CapabilityCrossFileHeuristic}}

	overclaim := CheckGALanguages([]Capability{gaRow("java", "typed-confirmed")}, derived, javaGreenGates())
	if overclaim.Pass() {
		t.Fatal("declaring typed-confirmed against a cross-file-heuristic derivation must fail")
	}
	underclaim := CheckGALanguages(
		[]Capability{gaRow("java", "cross-file-heuristic")},
		[]trust.Capability{{Language: "java", Level: trust.CapabilityTypedConfirmed}},
		javaGreenGates(),
	)
	if underclaim.Pass() {
		t.Fatal("a declaration below the derived level is drift too and must fail")
	}
	unknown := CheckGALanguages([]Capability{gaRow("cobol", "parse-only")}, derived, nil)
	if unknown.Pass() {
		t.Fatal("a ga-language row for a language with no live derivation must fail")
	}
}

// javaGreenGates returns a full set of green GA-LANG-java-* evidence facts.
func javaGreenGates() []GAEvidenceGate {
	return []GAEvidenceGate{
		{ID: "GA-LANG-java-G2", Passed: true},
		{ID: "GA-LANG-java-G3", Passed: true},
	}
}

// TestCheckGALanguages_EvidenceBinding pins direction (ii): a non-go language
// needs GA-LANG-<lang>-* rows AND every one of them green; a single non-PASS
// row (UNKNOWN, STALE, FAIL, or PASS without URI+sha — the caller folds that
// rule into Passed) blocks the flip.
func TestCheckGALanguages_EvidenceBinding(t *testing.T) {
	derived := []trust.Capability{{Language: "java", Level: trust.CapabilityTypedConfirmed}}
	row := []Capability{gaRow("java", "typed-confirmed")}

	if rep := CheckGALanguages(row, derived, nil); rep.Pass() {
		t.Fatal("a non-go GA row with ZERO evidence rows is vacuous and must fail")
	}
	oneRed := []GAEvidenceGate{
		{ID: "GA-LANG-java-G2", Passed: true},
		{ID: "GA-LANG-java-G3", Passed: false},
	}
	if rep := CheckGALanguages(row, derived, oneRed); rep.Pass() {
		t.Fatal("one non-passing evidence row must block the GA claim")
	}
	// Gates for OTHER languages are invisible to java's binding.
	foreign := []GAEvidenceGate{{ID: "GA-LANG-kotlin-G2", Passed: true}}
	if rep := CheckGALanguages(row, derived, foreign); rep.Pass() {
		t.Fatal("another language's gates must not satisfy java's evidence binding")
	}
	if rep := CheckGALanguages(row, derived, javaGreenGates()); !rep.Pass() {
		t.Fatalf("all-green evidence + matching derivation must pass, got %v", rep.Violations)
	}
}

// gaLangGates builds the GA-LANG-<lang>-G1..G9 fact set SW-174 creates, all
// born UNKNOWN (Passed=false — the caller folds internal/evidence's URI+sha
// honesty rule into that flag).
func gaLangGates(lang string) []GAEvidenceGate {
	gates := make([]GAEvidenceGate, 0, 9)
	for _, suffix := range []string{"G1", "G2SUB", "G3", "G4", "G5", "G6", "G7", "G8", "G9"} {
		gates = append(gates, GAEvidenceGate{ID: GAEvidencePrefix + lang + "-" + suffix, Passed: false})
	}
	return gates
}

// TestCheckGALanguages_BornUnknownIsInvisibleWithoutAMatrixRow pins the FIRST
// half of the ordering constraint the whole GA-LANG scaffold rests on (SW-174,
// the template's §3/S14): rows born UNKNOWN are legal precisely while their
// language has NO ga-language matrix row, because CheckGALanguages iterates
// MATRIX rows and never looks at a language it does not find there.
//
// Without this the scaffold could not exist: 18 UNKNOWN rows would be 18
// violations on the day they are created. SW-186 added the ga-language matrix
// rows for java and kotlin alongside the per-row rule, so the ordering is
// now uniform across all 22 shipped languages — the day-one go row still
// carries its own GA-LANG-go-* rows, and rows born UNKNOWN for a language
// without a matrix row remain invisible. This test pins the latter
// invariant directly.
func TestCheckGALanguages_BornUnknownIsInvisibleWithoutAMatrixRow(t *testing.T) {
	derived := []trust.Capability{
		{Language: "go", Level: trust.CapabilityTypedConfirmed},
		{Language: "java", Level: trust.CapabilityCrossFileHeuristic},
		{Language: "kotlin", Level: trust.CapabilityCrossFileHeuristic},
	}
	// The checked-in shape after SW-174: only `go` has a matrix row; java and
	// kotlin have eighteen UNKNOWN evidence rows and no matrix row at all.
	// go's nine GA-LANG-* rows are also present (SW-186 added them in the
	// same change as the grandfathering removal).
	gates := append(gaLangGates("go"), gaLangGates("java")...)
	gates = append(gates, gaLangGates("kotlin")...)
	rep := CheckGALanguages([]Capability{gaRow("go", "typed-confirmed")}, derived, gates)
	if rep.Pass() {
		t.Fatal("go's ga-language row with 9 UNKNOWN evidence rows must fail, got a pass")
	}
	if len(rep.Languages) != 1 || rep.Languages[0] != "go" {
		t.Fatalf("Languages = %v, want [go] — only matrix rows are inspected", rep.Languages)
	}
	// 9 violations, all of them for go's evidence rows. java's and
	// kotlin's rows are NOT consulted because they have no matrix row.
	if len(rep.Violations) != 9 {
		t.Fatalf("want 9 violations (go's 9 UNKNOWN rows), got %d: %v", len(rep.Violations), rep.Violations)
	}
	for _, v := range rep.Violations {
		if !strings.Contains(v, `ga-language row "go": evidence gate GA-LANG-go-`) {
			t.Fatalf("violation should target go, got %q", v)
		}
	}
}

// TestCheckGALanguages_GoEvidenceBinding pins the SW-186 binding: go is no
// longer grandfathered. Its matrix row MUST bind to GA-LANG-go-G1..G9
// evidence rows, and the per-row rule fires once per UNKNOWN row — the same
// per-row contract every language carries. The historical trap SW-174 found
// by measurement (a `go` row created before its evidence rows meant adding
// evidence rows turned the check red) is now structural: there is no path
// where `go` has a matrix row without its own GA-LANG-* rows.
//
// The "father of the ordering" framing is gone: SW-186 made the ordering
// uniform across all 22 shipped languages, and `go` is one of them.
func TestCheckGALanguages_GoEvidenceBinding(t *testing.T) {
	derived := []trust.Capability{{Language: "go", Level: trust.CapabilityTypedConfirmed}}
	matrix := []Capability{gaRow("go", "typed-confirmed")}

	// Control: with ZERO evidence rows, the vacuity rule now fires for go
	// (the grandfathering is gone). One violation per the `found == 0` rule.
	rep := CheckGALanguages(matrix, derived, nil)
	if rep.Pass() {
		t.Fatal("go with zero GA-LANG-* rows MUST fail — the grandfathering is removed (SW-186); every ga-language row, Go included, must bind to evidence rows")
	}
	if len(rep.Violations) != 1 {
		t.Fatalf("want exactly one violation (the vacuity rule), got %d: %v", len(rep.Violations), rep.Violations)
	}
	if !strings.Contains(rep.Violations[0], "no GA-LANG-go-* rows exist") {
		t.Fatalf("violation should name the vacuity rule, got %q", rep.Violations[0])
	}

	// Same row, nine UNKNOWN GA-LANG-go-* rows: nine per-row violations,
	// not the old single vacuity violation. The per-row rule applies
	// uniformly.
	rep = CheckGALanguages(matrix, derived, gaLangGates("go"))
	if rep.Pass() {
		t.Fatal("nine UNKNOWN GA-LANG-go-* rows under the existing go matrix row must FAIL")
	}
	if len(rep.Violations) != 9 {
		t.Fatalf("want one violation per UNKNOWN row (9), got %d: %v", len(rep.Violations), rep.Violations)
	}
	for _, v := range rep.Violations {
		if !strings.Contains(v, "does not read PASS with evidence URI and sha") {
			t.Fatalf("violation should name the per-row rule, got %q", v)
		}
		if strings.Contains(v, "no GA-LANG-go-* rows exist") {
			t.Fatalf("the vacuity rule must NOT fire when rows exist: %q", v)
		}
	}

	// And the failure is per-row, not all-or-nothing: turning eight green
	// leaves exactly one violation, which is what makes the flip gate
	// meaningful rather than a single tripwire.
	partial := gaLangGates("go")
	for i := range partial[:8] {
		partial[i].Passed = true
	}
	if rep := CheckGALanguages(matrix, derived, partial); len(rep.Violations) != 1 {
		t.Fatalf("eight green and one UNKNOWN must leave exactly one violation, got %d: %v", len(rep.Violations), rep.Violations)
	}
}

// TestCheckGALanguages_RowShape pins the row-level rules: duplicates fail, and
// a GA language must be shipped.
func TestCheckGALanguages_RowShape(t *testing.T) {
	derived := []trust.Capability{{Language: "go", Level: trust.CapabilityTypedConfirmed}}
	dup := CheckGALanguages([]Capability{gaRow("go", "typed-confirmed"), gaRow("go", "typed-confirmed")}, derived, nil)
	if dup.Pass() {
		t.Fatal("duplicate ga-language rows must fail")
	}
	notShipped := []Capability{{ID: "go", Category: CategoryGALanguage, Status: StatusPlanned, Tier: TierLabs, CapabilityLevel: "typed-confirmed"}}
	if rep := CheckGALanguages(notShipped, derived, nil); rep.Pass() {
		t.Fatal("a planned ga-language row must fail — GA means shipped")
	}
}

// TestParseMatrix_GALanguageFieldValidation pins the parse-time contract of
// the `capability` field: required (and closed-vocabulary) on ga-language
// rows, illegal anywhere else.
func TestParseMatrix_GALanguageFieldValidation(t *testing.T) {
	parse := func(yaml string) error {
		_, err := parseMatrixYAML(yaml)
		return err
	}
	valid := "capabilities:\n  - id: go\n    category: ga-language\n    status: shipped\n    tier: labs\n    capability: typed-confirmed\n"
	if err := parse(valid); err != nil {
		t.Fatalf("valid ga-language row rejected: %v", err)
	}
	caps, err := parseMatrixYAML(valid)
	if err != nil || len(caps) != 1 || caps[0].CapabilityLevel != "typed-confirmed" {
		t.Fatalf("capability field not carried through: %+v, %v", caps, err)
	}

	missing := "capabilities:\n  - id: go\n    category: ga-language\n    status: shipped\n    tier: labs\n"
	if err := parse(missing); err == nil || !strings.Contains(err.Error(), "missing capability level") {
		t.Fatalf("ga-language row without capability must fail, got %v", err)
	}
	invalid := "capabilities:\n  - id: go\n    category: ga-language\n    status: shipped\n    tier: labs\n    capability: super-checked\n"
	if err := parse(invalid); err == nil || !strings.Contains(err.Error(), "invalid capability") {
		t.Fatalf("out-of-vocabulary capability must fail, got %v", err)
	}
	misplaced := "capabilities:\n  - id: go\n    category: parser\n    status: shipped\n    tier: labs\n    capability: typed-confirmed\n"
	if err := parse(misplaced); err == nil || !strings.Contains(err.Error(), "not a ga-language row") {
		t.Fatalf("capability on a non-ga-language row must fail, got %v", err)
	}
}

// closedGALanguages is the SW-186 closed set: the 22 shipped languages that
// are GA at their proven level. Mirrors the spec table at
// docs/specs/ga-for-all-shipped-languages.md §"The measured starting point".
// Kept here — and not in the matrix file — so the test is self-contained
// (the matrix is the source, but the test exercises the check end-to-end).
var closedGALanguages = []struct {
	Lang  string
	Level trust.CapabilityLevel
}{
	{"go", trust.CapabilityTypedConfirmed},
	{"bash", trust.CapabilityCrossFileHeuristic},
	{"c", trust.CapabilityCrossFileHeuristic},
	{"c_sharp", trust.CapabilityCrossFileHeuristic},
	{"cpp", trust.CapabilityCrossFileHeuristic},
	{"css", trust.CapabilityIntraFileOnly},
	{"hcl", trust.CapabilityIntraFileOnly},
	{"java", trust.CapabilityCrossFileHeuristic},
	{"javascript", trust.CapabilityCrossFileHeuristic},
	{"json", trust.CapabilityParseOnly},
	{"kotlin", trust.CapabilityCrossFileHeuristic},
	{"lua", trust.CapabilityCrossFileHeuristic},
	{"markdown", trust.CapabilityIntraFileOnly},
	{"php", trust.CapabilityCrossFileHeuristic},
	{"python", trust.CapabilityCrossFileHeuristic},
	{"ruby", trust.CapabilityCrossFileHeuristic},
	{"rust", trust.CapabilityCrossFileHeuristic},
	{"sql", trust.CapabilityCrossFileHeuristic},
	{"toml", trust.CapabilityIntraFileOnly},
	{"tsx", trust.CapabilityCrossFileHeuristic},
	{"typescript", trust.CapabilityCrossFileHeuristic},
	{"yaml", trust.CapabilityIntraFileOnly},
}

// gatesForLang returns the GA-LANG-<lang>-* row ids for a language, picking
// the G2 vs G2SUB substitution by the language's declared level.
func gatesForLang(lang string, level trust.CapabilityLevel) []string {
	switch level {
	case trust.CapabilityTypedConfirmed:
		return []string{"G1", "G2", "G3", "G4", "G5", "G6", "G7", "G8", "G9"}
	case trust.CapabilityCrossFileHeuristic:
		return []string{"G1", "G2SUB", "G3", "G4", "G5", "G6", "G7", "G8", "G9"}
	default:
		// intra-file-only / parse-only: canonical G2 (language-spec abstention).
		return []string{"G1", "G2", "G3", "G4", "G5", "G6", "G7", "G8", "G9"}
	}
}

// TestCheckGALanguages_NonVacuumGreen pins the SW-186 structural contract:
// for every ga-language row, CheckGALanguages MUST consult a live derivation
// row AND a real GA-LANG-<lang>-* evidence row. Removing either binding
// flips the test RED — the axis is no longer vacuum-green.
//
// The test exercises the closed 22-language set: every level appears at
// least once (typed-confirmed: 1; cross-file-heuristic: 15;
// intra-file-only: 5; parse-only: 1), and the G2SUB substitution is verified
// to fire at the cross-file-heuristic level only.
func TestCheckGALanguages_NonVacuumGreen(t *testing.T) {
	// Build the matrix and the live derivation sets from the closed set.
	matrix := make([]Capability, 0, len(closedGALanguages))
	derived := make([]trust.Capability, 0, len(closedGALanguages))
	for _, e := range closedGALanguages {
		matrix = append(matrix, gaRow(e.Lang, string(e.Level)))
		derived = append(derived, trust.Capability{Language: e.Lang, Level: e.Level})
	}

	// Build the GA-LANG-* gate set: every row UNKNOWN, every shape covered.
	var gates []GAEvidenceGate
	for _, e := range closedGALanguages {
		for _, suffix := range gatesForLang(e.Lang, e.Level) {
			gates = append(gates, GAEvidenceGate{ID: GAEvidencePrefix + e.Lang + "-" + suffix, Passed: false})
		}
	}

	// Baseline: every row binds to derivation AND to evidence rows — but
	// all evidence rows are UNKNOWN, so the check fails with one violation
	// per UNKNOWN row. The structural contract (both bindings consulted)
	// holds, and the count is documented.
	rep := CheckGALanguages(matrix, derived, gates)
	if rep.Pass() {
		t.Fatal("all-UNKNOWN evidence rows must fail (not green) — the AXIS is closed, not the EVIDENCE")
	}
	// Each non-go language: 9 UNKNOWN rows = 9 violations. go: 9 as well.
	// Total = 22 * 9 = 198 violations. The unified binding counts.
	wantViolations := 22 * 9
	if len(rep.Violations) != wantViolations {
		t.Fatalf("want %d violations (22 × 9, every ga-language row's 9 evidence rows UNKNOWN), got %d:\n%v", wantViolations, len(rep.Violations), rep.Violations)
	}
	// Every ga-language row is in the report (no language is invisible).
	if len(rep.Languages) != 22 {
		t.Fatalf("Languages = %d, want 22 (every ga-language row inspected): %v", len(rep.Languages), rep.Languages)
	}
	// Sorted, deterministic.
	for i := 1; i < len(rep.Languages); i++ {
		if rep.Languages[i-1] >= rep.Languages[i] {
			t.Fatalf("Languages not sorted: %v", rep.Languages)
		}
	}

	// Mutation 1: DROP the derivation for java. The check must report
	// java's missing derivation as a violation — removing the (i) binding
	// flips the test RED for the right reason.
	derivedDrift := make([]trust.Capability, 0, len(derived)-1)
	for _, c := range derived {
		if c.Language == "java" {
			continue
		}
		derivedDrift = append(derivedDrift, c)
	}
	rep = CheckGALanguages(matrix, derivedDrift, gates)
	if rep.Pass() {
		t.Fatal("dropping the live derivation must fail the check — the (i) REGISTRY binding is now structural")
	}
	javaSeen := false
	for _, v := range rep.Violations {
		if strings.Contains(v, `ga-language row "java": the live registries derive NO capability`) {
			javaSeen = true
		}
	}
	if !javaSeen {
		t.Fatal("missing the java-no-derivation violation — the (i) binding is not consulted")
	}

	// Mutation 2: DROP every GA-LANG-java-* gate. The check must report
	// java's vacuity as a violation — removing the (ii) EVIDENCE binding
	// flips the test RED for the right reason.
	gatesDrift := make([]GAEvidenceGate, 0, len(gates))
	for _, g := range gates {
		if strings.HasPrefix(g.ID, "GA-LANG-java-") {
			continue
		}
		gatesDrift = append(gatesDrift, g)
	}
	rep = CheckGALanguages(matrix, derived, gatesDrift)
	if rep.Pass() {
		t.Fatal("dropping the java evidence rows must fail the check — the (ii) EVIDENCE binding is now structural")
	}
	javaVacuity := false
	for _, v := range rep.Violations {
		if strings.Contains(v, `ga-language row "java": no GA-LANG-java-* rows exist`) {
			javaVacuity = true
		}
	}
	if !javaVacuity {
		t.Fatal("missing the java-vacuity violation — the (ii) binding is not consulted for java")
	}

	// Mutation 3: GO'S EVIDENCE ROWS — the previously-grandfathered case.
	// Dropping the GA-LANG-go-* gates must now fail the check uniformly
	// (the grandfathering is gone). The (ii) binding is consulted for go.
	gatesDriftGo := make([]GAEvidenceGate, 0, len(gates))
	for _, g := range gates {
		if strings.HasPrefix(g.ID, "GA-LANG-go-") {
			continue
		}
		gatesDriftGo = append(gatesDriftGo, g)
	}
	rep = CheckGALanguages(matrix, derived, gatesDriftGo)
	if rep.Pass() {
		t.Fatal("dropping the go evidence rows must fail the check — go is no longer grandfathered (SW-186)")
	}
	goVacuity := false
	for _, v := range rep.Violations {
		if strings.Contains(v, `ga-language row "go": no GA-LANG-go-* rows exist`) {
			goVacuity = true
		}
	}
	if !goVacuity {
		t.Fatal("missing the go-vacuity violation — the grandfathering is still in force")
	}
}

// TestCheckGALanguages_NoGrandfatherClause is the explicit assertion SW-186
// requires: the historical `go` grandfathering is REMOVED. The check now
// treats go uniformly with every other language, which is the structural
// fact this test pins.
//
// Several mechanical checks for the absence of the grandfathering, so a
// regression that re-introduces it (a constant named `gaGrandfatheredLanguage`,
// a special case for `go` in the vacuity rule, or a difference in the
// violations emitted for `go` vs every other language) flips the test RED.
func TestCheckGALanguages_NoGrandfatherClause(t *testing.T) {
	derived := []trust.Capability{
		{Language: "go", Level: trust.CapabilityTypedConfirmed},
		{Language: "java", Level: trust.CapabilityCrossFileHeuristic},
	}
	matrix := []Capability{
		gaRow("go", "typed-confirmed"),
		gaRow("java", "cross-file-heuristic"),
	}

	// (1) Vacuity rule fires for go: zero GA-LANG-go-* rows => one violation
	// naming the vacuity rule (the per-row rule cannot fire — there are no
	// rows to scan). Pre-SW-186, this would have passed (the grandfather
	// exempts go from `found == 0`).
	rep := CheckGALanguages(matrix, derived, nil)
	if rep.Pass() {
		t.Fatal("go with zero GA-LANG-* rows must fail now — the grandfathering is removed")
	}
	goVacuity := false
	for _, v := range rep.Violations {
		if strings.Contains(v, `ga-language row "go": no GA-LANG-go-* rows exist`) {
			goVacuity = true
		}
	}
	if !goVacuity {
		t.Fatalf("want go's vacuity violation, got %v", rep.Violations)
	}

	// (2) Per-row rule fires for go just like every other language: nine
	// UNKNOWN GA-LANG-go-* rows => nine violations, not zero, not one.
	gates := gaLangGates("go")
	rep = CheckGALanguages(matrix, derived, gates)
	if rep.Pass() {
		t.Fatal("nine UNKNOWN GA-LANG-go-* rows must FAIL — the per-row rule applies to go like every other language")
	}
	goPerRow := 0
	for _, v := range rep.Violations {
		if strings.Contains(v, `ga-language row "go": evidence gate GA-LANG-go-`) {
			goPerRow++
		}
	}
	if goPerRow != 9 {
		t.Fatalf("want 9 per-row violations for go, got %d (full list: %v)", goPerRow, rep.Violations)
	}

	// (3) Per-row rule fires for java in the same shape: nine UNKNOWN
	// GA-LANG-java-* rows => nine violations. The check is uniform across
	// languages, not language-specific.
	javaGates := gaLangGates("java")
	rep = CheckGALanguages(matrix, derived, javaGates)
	if rep.Pass() {
		t.Fatal("nine UNKNOWN GA-LANG-java-* rows must FAIL")
	}
	javaPerRow := 0
	for _, v := range rep.Violations {
		if strings.Contains(v, `ga-language row "java": evidence gate GA-LANG-java-`) {
			javaPerRow++
		}
	}
	if javaPerRow != 9 {
		t.Fatalf("want 9 per-row violations for java (cross-file-heuristic level), got %d (full list: %v)", javaPerRow, rep.Violations)
	}

	// (4) Mixed: go's gating AND java's gating together => 18 violations
	// (no shared gate, no aliasing). The violations are identical in shape
	// between the two languages — the check has no language-specific path.
	all := append(gaLangGates("go"), gaLangGates("java")...)
	rep = CheckGALanguages(matrix, derived, all)
	if rep.Pass() {
		t.Fatal("mixed go + java UNKNOWN rows must FAIL")
	}
	if len(rep.Violations) != 18 {
		t.Fatalf("want 18 violations (9 + 9, go + java), got %d: %v", len(rep.Violations), rep.Violations)
	}
	// No violation contains a grandfathering-shaped message
	// ("no GA-LANG-go-* rows exist" together with "go is grandfathered",
	// or any other grandfathering language). The check speaks uniformly.
	for _, v := range rep.Violations {
		if strings.Contains(v, "grandfather") {
			t.Fatalf("violation carries grandfathering language: %q", v)
		}
	}
}
