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
