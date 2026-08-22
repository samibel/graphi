package conformance_test

// SW-194b (W5.h, bash slice, second half): the Bash parity-class matrix
// drift guard. Binds docs/rc/parity-classes-bash.yaml to the harness table
// bashChangeClassTable() in bashparity_test.go, so a class declared
// required with no harness row (MISSING), a harness row for an undeclared
// id (PHANTOM), or a deferred row that nonetheless has a harness row
// (DEFERRED) fails the build.
//
// SW-194b inherits the CLOSED 7-direction shape from W1.f's SW-189 closure
// (the JVM / Python / TS family matrices): MISSING, PHANTOM, KIND,
// VERDICT, OWNER, AXIS, VOCABULARY. This is the LESSON D-11 TAUGHT — the
// newer tables ship the closed shape, not the gap.

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/samibel/graphi/core/profile"
)

// bashParityClassesPath is docs/rc/parity-classes-bash.yaml relative to
// this package's directory (go test's working directory). Mirrors the
// Python / JVM / TS consts.
const bashParityClassesPath = "../../docs/rc/parity-classes-bash.yaml"

// loadBashParityClasses parses the Bash matrix. Reuses parityRow from
// paritymatrix_test.go and yaml.Unmarshal — a test-only import.
func loadBashParityClasses(t *testing.T) []parityRow {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(bashParityClassesPath))
	if err != nil {
		t.Fatalf("read %s: %v", bashParityClassesPath, err)
	}
	var doc struct {
		ParityClasses []parityRow `yaml:"parity_classes"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", bashParityClassesPath, err)
	}
	if len(doc.ParityClasses) == 0 {
		t.Fatalf("%s declared no parity_classes rows", bashParityClassesPath)
	}
	return doc.ParityClasses
}

// TestBashParityMatrix_DriftGuard is the Bash twin of
// TestPythonParityMatrix_DriftGuard, the MISSING/PHANTOM/DEFERRED
// direction. The four additional directions the SW-189 closure adds
// (VERDICT, AXIS, VOCABULARY, KIND-as-distinct-from-OWNER) are tested
// below — this is the CLOSED 7-direction shape, not the JVM/Python
// narrowing.
func TestBashParityMatrix_DriftGuard(t *testing.T) {
	rows := loadBashParityClasses(t)

	declared := map[string]parityRow{}
	for _, r := range rows {
		if _, dup := declared[r.ID]; dup {
			t.Fatalf("duplicate id %q in %s", r.ID, bashParityClassesPath)
		}
		declared[r.ID] = r
	}

	harness := map[string]bool{}
	for _, row := range bashChangeClassTable() {
		if harness[row.id] {
			t.Fatalf("duplicate harness row id %q", row.id)
		}
		harness[row.id] = true
	}

	// MISSING: every required row has a harness row.
	// DEFERRED: every deferred row has NO harness row and names a deferred_to.
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

	// PHANTOM: every harness row id is declared.
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

// bashRequiredRowOwners is the CLOSED set of work packages permitted to
// own a harness_row: "required" row in docs/rc/parity-classes-bash.yaml.
// Mirrors the JVM/Python/TS guards. The set stays CLOSED so a row cannot
// arrive with an empty or invented owner; adding one means naming it here.
var bashRequiredRowOwners = map[string]bool{
	"W5-RES.a": true, // the eight cross-file-heuristic bash rows
}

// bashRequiredRowOwnerList renders bashRequiredRowOwners deterministically.
func bashRequiredRowOwnerList() []string {
	out := make([]string, 0, len(bashRequiredRowOwners))
	for k := range bashRequiredRowOwners {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestBashParityMatrix_KindCountAndOwners pins the Bash matrix's row count
// by KIND and its owner set. The count stays a PINNED literal rather than
// a `>=` bound for the same reason the Go / JVM / Python matrices do: the
// guard's whole job is that no row can be added, removed or re-kinded
// unnoticed, and a bound forfeits exactly that.
//
// 8 + 0: the eight change-class rows SW-194b added. The Bash matrix does
// NOT carry a crash_condition row.
func TestBashParityMatrix_KindCountAndOwners(t *testing.T) {
	rows := loadBashParityClasses(t)
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
			bashParityClassesPath, classes, crash, wantClasses, wantCrash)
	}
	for _, r := range rows {
		if r.HarnessRow != harnessRequired {
			continue
		}
		if !bashRequiredRowOwners[r.Owner] {
			t.Errorf("OWNER: %q is harness_row: %q so owner must be one of %v; got %q — "+
				"a required row may not arrive with an empty or invented owner",
				r.ID, harnessRequired, bashRequiredRowOwnerList(), r.Owner)
		}
	}
}

// TestBashParityMatrix_RequiredRowsAreProven pins that every required row
// reads verdict PROVEN (the harness proves them on both stores) and every
// deferred row reads ABSENT — the honesty rule that a deferred placeholder
// never claims a verdict it did not earn.
func TestBashParityMatrix_RequiredRowsAreProven(t *testing.T) {
	for _, r := range loadBashParityClasses(t) {
		switch r.HarnessRow {
		case harnessRequired:
			if r.Verdict != verdictProven {
				t.Errorf("required row %q reads verdict %q, want PROVEN", r.ID, r.Verdict)
			}
			if r.TestName != "TestBashFullVsIncremental_ByteParity" {
				t.Errorf("required row %q must cite the Bash parity gate, got %q", r.ID, r.TestName)
			}
		case harnessDeferred:
			if r.Verdict != verdictAbsent {
				t.Errorf("deferred row %q reads verdict %q, want ABSENT", r.ID, r.Verdict)
			}
		}
	}
}

// TestBashParityMatrix_Verdict binds the Bash matrix's verdict to what its
// harness row actually proves — the VERDICT direction mirrored for the
// Bash family. Required rows claim PROVEN only when
// TestBashFullVsIncremental_ByteParity ran them; deferred rows claim
// ABSENT and cite nothing. The CITATION sub-assertion closes the same
// attack the Go guard closes (paritymatrix_test.go:392-405).
func TestBashParityMatrix_Verdict(t *testing.T) {
	const harnessFile = "engine/conformance/bashparity_test.go"
	const harnessName = "TestBashFullVsIncremental_ByteParity"
	for _, r := range loadBashParityClasses(t) {
		switch r.HarnessRow {
		case harnessRequired:
			if r.Verdict == verdictAbsent {
				t.Errorf("VERDICT: Bash %q has a harness table row but still reads verdict: %q in %s",
					r.ID, r.Verdict, bashParityClassesPath)
			}
			if r.TestFile != harnessFile {
				t.Errorf("VERDICT: Bash %q is proven by the harness table but test_file reads %q; want %q",
					r.ID, r.TestFile, harnessFile)
			}
			if r.TestName != harnessName {
				t.Errorf("VERDICT: Bash %q test_name reads %q; want %q", r.ID, r.TestName, harnessName)
			}
			if r.TestLine == "" {
				t.Errorf("VERDICT: Bash %q is a required row but test_line is empty; cite the line of %s in %s",
					r.ID, harnessName, harnessFile)
			}
			if r.KnownDefect != "" && r.Verdict == verdictProven {
				t.Errorf("VERDICT: Bash %q declares known_defect %q so it may NOT read verdict: %q",
					r.ID, r.KnownDefect, verdictProven)
			}
		case harnessDeferred:
			if r.TestFile == harnessFile || r.TestName == harnessName {
				t.Errorf("CITATION: Bash %q is harness_row: %q yet cites the harness driver "+
					"(test_file=%q test_name=%q). A deferred row cannot claim a verdict the "+
					"harness produced.",
					r.ID, harnessDeferred, r.TestFile, r.TestName)
			}
		}
	}
}

// TestBashParityMatrix_Axis binds the Bash matrix's profile/store claim to
// the axes the harness actually runs — the AXIS direction mirrored for the
// Bash family. Removing the balanced entry from parityProfiles() makes
// this test go red, by construction.
func TestBashParityMatrix_Axis(t *testing.T) {
	profilesSeen := map[profile.Profile]bool{}
	for _, pr := range parityProfiles() {
		profilesSeen[pr.p] = true
	}
	if !profilesSeen[""] || !profilesSeen[profile.Balanced] {
		t.Errorf("AXIS: parityProfiles() runs {default=%v, balanced=%v}; the Bash matrix claims profile: \"both\" "+
			"on every required row, so both must run",
			profilesSeen[""], profilesSeen[profile.Balanced])
	}
	if n := len(parityBackends()); n != 2 {
		t.Errorf("AXIS: parityBackends() runs %d backend(s); the Bash matrix claims store: \"both\", so 2 are required", n)
	}
	for _, r := range loadBashParityClasses(t) {
		if r.HarnessRow != harnessRequired {
			continue
		}
		if r.Store != "both" {
			t.Errorf("AXIS: Bash %q is harness_row: %q but reads store: %q; want \"both\"", r.ID, harnessRequired, r.Store)
		}
		if r.Profile != "both" {
			t.Errorf("AXIS: Bash %q is harness_row: %q but reads profile: %q; want \"both\"", r.ID, harnessRequired, r.Profile)
		}
	}
}

// TestBashParityMatrix_Vocabulary enforces the closed vocabularies on
// every Bash row — the VOCABULARY direction mirrored for the Bash
// family. The shared constants (legalKinds, legalVerdicts, legalHarnessRows,
// legalFixtures, legalStores, legalProfiles, legalAssertions) live in
// paritymatrix_test.go and are the SAME closed set the Go guard enforces.
func TestBashParityMatrix_Vocabulary(t *testing.T) {
	oneOf := func(v string, legal []string) bool {
		for _, l := range legal {
			if v == l {
				return true
			}
		}
		return false
	}
	for _, r := range loadBashParityClasses(t) {
		check := func(field, value string, legal []string, allowEmpty bool) {
			if value == "" && allowEmpty {
				return
			}
			if !oneOf(value, legal) {
				t.Errorf("VOCABULARY: Bash %q has %s: %q, which is outside the declared vocabulary %v",
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
			t.Errorf("VOCABULARY: Bash %q claims assertion: %q with store: %q — bytes can only come from a store",
				r.ID, r.Assertion, storeNone)
		}
	}
}
