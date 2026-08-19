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
// typed-confirmed, with zero GA-LANG-* evidence rows (go is grandfathered —
// its GA evidence is the P0/P1 program record).
func TestCheckGALanguages_GoDayOne(t *testing.T) {
	rep := CheckGALanguages(
		[]Capability{gaRow("go", "typed-confirmed")},
		[]trust.Capability{{Language: "go", Level: trust.CapabilityTypedConfirmed}},
		nil,
	)
	if !rep.Pass() {
		t.Fatalf("day-one go row must pass, got violations: %v", rep.Violations)
	}
	if len(rep.Languages) != 1 || rep.Languages[0] != "go" {
		t.Fatalf("Languages = %v, want [go]", rep.Languages)
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
// violations on the day they are created.
func TestCheckGALanguages_BornUnknownIsInvisibleWithoutAMatrixRow(t *testing.T) {
	derived := []trust.Capability{
		{Language: "go", Level: trust.CapabilityTypedConfirmed},
		{Language: "java", Level: trust.CapabilityCrossFileHeuristic},
		{Language: "kotlin", Level: trust.CapabilityCrossFileHeuristic},
	}
	// The checked-in shape after SW-174: only `go` has a matrix row; java and
	// kotlin have eighteen UNKNOWN evidence rows and no matrix row at all.
	gates := append(gaLangGates("java"), gaLangGates("kotlin")...)
	rep := CheckGALanguages([]Capability{gaRow("go", "typed-confirmed")}, derived, gates)
	if !rep.Pass() {
		t.Fatalf("UNKNOWN rows for a language with no ga-language matrix row must be invisible to the check, got: %v", rep.Violations)
	}
	if len(rep.Languages) != 1 || rep.Languages[0] != "go" {
		t.Fatalf("Languages = %v, want [go] — only matrix rows are inspected", rep.Languages)
	}
}

// TestCheckGALanguages_GrandfatheringDoesNotCoverUnknownRows is the trap SW-174
// found by measurement, pinned so nobody rediscovers it in CI.
//
// go's grandfathering (gaGrandfatheredLanguage) exempts it from ONE rule only —
// the `found == 0` vacuity rule at galang.go:133, i.e. go may carry no
// GA-LANG-* rows at all. It does NOT exempt go from the per-row rule at
// galang.go:129-131. So the moment go's own rows are created born UNKNOWN,
// while its day-one ga-language matrix row still stands, the check fails once
// PER ROW — nine violations, not one, and not zero.
//
// That is why SW-174 landed java's and kotlin's rows and NOT go's: the second
// half of the ordering constraint (matrix row last, only once every row reads
// PASS) has no legal move for a language whose matrix row already exists.
// Resolving it is an owner decision (SW-174 AC-6); this test states the
// mechanism so the decision is made against a fact rather than a memory.
func TestCheckGALanguages_GrandfatheringDoesNotCoverUnknownRows(t *testing.T) {
	derived := []trust.Capability{{Language: "go", Level: trust.CapabilityTypedConfirmed}}
	matrix := []Capability{gaRow("go", "typed-confirmed")}

	// Control: grandfathering IS in force — zero rows still passes.
	if rep := CheckGALanguages(matrix, derived, nil); !rep.Pass() {
		t.Fatalf("go with zero GA-LANG-* rows must pass (grandfathered), got %v", rep.Violations)
	}

	rep := CheckGALanguages(matrix, derived, gaLangGates("go"))
	if rep.Pass() {
		t.Fatal("nine UNKNOWN GA-LANG-go-* rows under the existing go matrix row must FAIL — grandfathering does not reach the per-row rule")
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
