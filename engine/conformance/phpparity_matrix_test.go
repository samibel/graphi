package conformance_test

// SW-194b (W5.h, php slice, second half): the PHP parity-class matrix
// drift guard. Binds docs/rc/parity-classes-php.yaml to the harness table
// phpChangeClassTable() in phpparity_test.go. Same closed 7-direction
// shape as the Bash / Python / JVM / TS family matrices.

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/samibel/graphi/core/profile"
)

const phpParityClassesPath = "../../docs/rc/parity-classes-php.yaml"

func loadPhpParityClasses(t *testing.T) []parityRow {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(phpParityClassesPath))
	if err != nil {
		t.Fatalf("read %s: %v", phpParityClassesPath, err)
	}
	var doc struct {
		ParityClasses []parityRow `yaml:"parity_classes"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", phpParityClassesPath, err)
	}
	if len(doc.ParityClasses) == 0 {
		t.Fatalf("%s declared no parity_classes rows", phpParityClassesPath)
	}
	return doc.ParityClasses
}

func TestPhpParityMatrix_DriftGuard(t *testing.T) {
	rows := loadPhpParityClasses(t)

	declared := map[string]parityRow{}
	for _, r := range rows {
		if _, dup := declared[r.ID]; dup {
			t.Fatalf("duplicate id %q in %s", r.ID, phpParityClassesPath)
		}
		declared[r.ID] = r
	}

	harness := map[string]bool{}
	for _, row := range phpChangeClassTable() {
		if harness[row.id] {
			t.Fatalf("duplicate harness row id %q", row.id)
		}
		harness[row.id] = true
	}

	var missing, misdeferred []string
	for _, r := range rows {
		switch r.HarnessRow {
		case harnessRequired:
			if !harness[r.ID] {
				missing = append(missing, r.ID)
			}
		case harnessDeferred:
			if harness[r.ID] {
				misdeferred = append(misdeferred, r.ID+" (has a harness row but is marked deferred)")
			}
			if r.DeferredTo == "" {
				misdeferred = append(misdeferred, r.ID+" (deferred with no deferred_to owner)")
			}
		default:
			t.Errorf("row %q has harness_row %q, want required|deferred", r.ID, r.HarnessRow)
		}
	}

	var phantom []string
	for id := range harness {
		if _, ok := declared[id]; !ok {
			phantom = append(phantom, id)
		}
	}

	sort.Strings(missing)
	sort.Strings(phantom)
	sort.Strings(misdeferred)
	if len(missing) > 0 {
		t.Errorf("MISSING — required classes with no harness row: %v", missing)
	}
	if len(phantom) > 0 {
		t.Errorf("PHANTOM — harness rows for undeclared ids: %v", phantom)
	}
	if len(misdeferred) > 0 {
		t.Errorf("DEFERRED — malformed deferred rows: %v", misdeferred)
	}
}

var phpRequiredRowOwners = map[string]bool{
	"W5-RES.f": true,
}

func phpRequiredRowOwnerList() []string {
	out := make([]string, 0, len(phpRequiredRowOwners))
	for k := range phpRequiredRowOwners {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestPhpParityMatrix_KindCountAndOwners(t *testing.T) {
	rows := loadPhpParityClasses(t)
	var classes, crash int
	for _, r := range rows {
		switch r.Kind {
		case kindChangeClass:
			classes++
		case kindCrashCondition:
			crash++
		default:
			t.Errorf("row %q has kind %q, want %s|%s", r.ID, r.Kind, kindChangeClass, kindCrashCondition)
		}
	}
	const wantClasses, wantCrash = 8, 0
	if classes != wantClasses || crash != wantCrash {
		t.Errorf("KIND: %s has %d change_class + %d crash_condition rows; want %d + %d",
			phpParityClassesPath, classes, crash, wantClasses, wantCrash)
	}
	for _, r := range rows {
		if r.HarnessRow != harnessRequired {
			continue
		}
		if !phpRequiredRowOwners[r.Owner] {
			t.Errorf("OWNER: %q is harness_row: %q so owner must be one of %v; got %q",
				r.ID, harnessRequired, phpRequiredRowOwnerList(), r.Owner)
		}
	}
}

func TestPhpParityMatrix_RequiredRowsAreProven(t *testing.T) {
	for _, r := range loadPhpParityClasses(t) {
		switch r.HarnessRow {
		case harnessRequired:
			if r.Verdict != verdictProven {
				t.Errorf("required row %q reads verdict %q, want PROVEN", r.ID, r.Verdict)
			}
			if r.TestName != "TestPhpFullVsIncremental_ByteParity" {
				t.Errorf("required row %q must cite the PHP parity gate, got %q", r.ID, r.TestName)
			}
		case harnessDeferred:
			if r.Verdict != verdictAbsent {
				t.Errorf("deferred row %q reads verdict %q, want ABSENT", r.ID, r.Verdict)
			}
		}
	}
}

func TestPhpParityMatrix_Verdict(t *testing.T) {
	const harnessFile = "engine/conformance/phpparity_test.go"
	const harnessName = "TestPhpFullVsIncremental_ByteParity"
	for _, r := range loadPhpParityClasses(t) {
		switch r.HarnessRow {
		case harnessRequired:
			if r.Verdict == verdictAbsent {
				t.Errorf("VERDICT: Php %q has a harness table row but still reads verdict: %q in %s",
					r.ID, r.Verdict, phpParityClassesPath)
			}
			if r.TestFile != harnessFile {
				t.Errorf("VERDICT: Php %q is proven by the harness table but test_file reads %q; want %q",
					r.ID, r.TestFile, harnessFile)
			}
			if r.TestName != harnessName {
				t.Errorf("VERDICT: Php %q test_name reads %q; want %q", r.ID, r.TestName, harnessName)
			}
			if r.TestLine == "" {
				t.Errorf("VERDICT: Php %q is a required row but test_line is empty", r.ID)
			}
			if r.KnownDefect != "" && r.Verdict == verdictProven {
				t.Errorf("VERDICT: Php %q declares known_defect %q so it may NOT read verdict: %q",
					r.ID, r.KnownDefect, verdictProven)
			}
		case harnessDeferred:
			if r.TestFile == harnessFile || r.TestName == harnessName {
				t.Errorf("CITATION: Php %q is harness_row: %q yet cites the harness driver",
					r.ID, harnessDeferred)
			}
		}
	}
}

func TestPhpParityMatrix_Axis(t *testing.T) {
	profilesSeen := map[profile.Profile]bool{}
	for _, pr := range parityProfiles() {
		profilesSeen[pr.p] = true
	}
	if !profilesSeen[""] || !profilesSeen[profile.Balanced] {
		t.Errorf("AXIS: parityProfiles() runs {default=%v, balanced=%v}; the PHP matrix claims profile: \"both\" so both must run",
			profilesSeen[""], profilesSeen[profile.Balanced])
	}
	if n := len(parityBackends()); n != 2 {
		t.Errorf("AXIS: parityBackends() runs %d backend(s); the PHP matrix claims store: \"both\", so 2 are required", n)
	}
	for _, r := range loadPhpParityClasses(t) {
		if r.HarnessRow != harnessRequired {
			continue
		}
		if r.Store != "both" {
			t.Errorf("AXIS: Php %q is harness_row: %q but reads store: %q; want \"both\"", r.ID, harnessRequired, r.Store)
		}
		if r.Profile != "both" {
			t.Errorf("AXIS: Php %q is harness_row: %q but reads profile: %q; want \"both\"", r.ID, harnessRequired, r.Profile)
		}
	}
}

func TestPhpParityMatrix_Vocabulary(t *testing.T) {
	oneOf := func(v string, legal []string) bool {
		for _, l := range legal {
			if v == l {
				return true
			}
		}
		return false
	}
	for _, r := range loadPhpParityClasses(t) {
		check := func(field, value string, legal []string, allowEmpty bool) {
			if value == "" && allowEmpty {
				return
			}
			if !oneOf(value, legal) {
				t.Errorf("VOCABULARY: Php %q has %s: %q, which is outside the declared vocabulary %v",
					r.ID, field, value, legal)
			}
		}
		check("kind", r.Kind, legalKinds, false)
		check("verdict", r.Verdict, legalVerdicts, false)
		check("harness_row", r.HarnessRow, legalHarnessRows, false)
		check("fixture", r.Fixture, legalFixtures, false)
		check("store", r.Store, legalStores, false)
		check("profile", r.Profile, legalProfiles, false)
		check("assertion", r.Assertion, legalAssertions, false)
		if r.Store == storeNone &&
			(r.Assertion == assertionSnapshotBytes || r.Assertion == assertionEnvelopeBytes) {
			t.Errorf("VOCABULARY: Php %q claims assertion: %q with store: %q", r.ID, r.Assertion, storeNone)
		}
	}
}
