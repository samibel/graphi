package conformance_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/samibel/graphi/core/profile"
)

// jvmParityClassesPath is docs/rc/parity-classes-jvm.yaml relative to this
// package's directory (go test's working directory).
const jvmParityClassesPath = "../../docs/rc/parity-classes-jvm.yaml"

// loadJVMParityClasses parses the JVM matrix. It reuses parityRow (the shared
// row shape) and yaml.Unmarshal — a test-only import, exactly as
// paritymatrix_test.go documents at length.
func loadJVMParityClasses(t *testing.T) []parityRow {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(jvmParityClassesPath))
	if err != nil {
		t.Fatalf("read %s: %v", jvmParityClassesPath, err)
	}
	var doc struct {
		ParityClasses []parityRow `yaml:"parity_classes"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", jvmParityClassesPath, err)
	}
	if len(doc.ParityClasses) == 0 {
		t.Fatalf("%s declared no parity_classes rows", jvmParityClassesPath)
	}
	return doc.ParityClasses
}

// TestJVMParityMatrix_DriftGuard binds docs/rc/parity-classes-jvm.yaml to the
// harness table jvmChangeClassTable(), so a class declared required with no
// harness row (MISSING), a harness row for an undeclared id (PHANTOM), or a
// deferred row that nonetheless has a harness row (DEFERRED) fails the build.
// This is the JVM twin of TestParityMatrix_DriftGuard, kept deliberately
// focused on the three directions the wave-1 subset needs.
func TestJVMParityMatrix_DriftGuard(t *testing.T) {
	rows := loadJVMParityClasses(t)

	declared := map[string]parityRow{}
	for _, r := range rows {
		if _, dup := declared[r.ID]; dup {
			t.Fatalf("duplicate id %q in %s", r.ID, jvmParityClassesPath)
		}
		declared[r.ID] = r
	}

	harness := map[string]bool{}
	for _, row := range jvmChangeClassTable() {
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

// jvmRequiredRowOwners is the CLOSED set of work packages permitted to own a
// harness_row: "required" row in docs/rc/parity-classes-jvm.yaml — the JVM twin
// of paritymatrix_test.go's requiredRowOwners.
//
// WHY IT IS BEING ADDED BY SW-170, WHICH DID NOT SET OUT TO BUILD A GUARD.
// SW-170's AC-5 says that when the class count changes, "the pinned KIND count
// and requiredRowOwners shall be updated in the same change; these guards break
// on harness edits deliberately". Measured while adding the two W0.h rows: they
// did NOT break, and they could not have — both live in
// TestParityMatrix_SchemaAndDriftGuard, which loads docs/rc/parity-classes.yaml
// (the GO matrix) and never reads this file. The JVM matrix has been carrying an
// open owner field and an unpinned row count since it was created. Reporting the
// AC as vacuously satisfied would have left that true; reporting it as satisfied
// by guards that never ran would have been false. So the guards the AC describes
// now exist for the file the story actually changed.
var jvmRequiredRowOwners = map[string]bool{
	"WP-J5":  true, // the wave-1 add/modify/call + signature rows
	"WP-J5b": true, // rename cascade, supertype re-point, nested→top-level
	"WP-J5c": true, // import shadowing, same-package symbol move
	"M2.2":   true, // jvm_delete_file, promoted from deferred
	"W0.h":   true, // the two mixed-language-directory rows (ADR 0008 D9)
}

// jvmRequiredRowOwnerList renders jvmRequiredRowOwners deterministically.
func jvmRequiredRowOwnerList() []string {
	out := make([]string, 0, len(jvmRequiredRowOwners))
	for k := range jvmRequiredRowOwners {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestJVMParityMatrix_KindCountAndOwners pins the JVM matrix's row count by KIND
// and its owner set. The count stays a PINNED literal rather than a `>=` bound
// for the same reason the Go matrix's does: the guard's whole job is that no row
// can be added, removed or re-kinded unnoticed, and a bound forfeits exactly
// that. Adding a class means updating this number and saying why, here.
//
// 13 + 0: the eleven wave-1 classes (WP-J5/J5b/J5c plus the promoted
// jvm_delete_file) plus the two mixed-language-directory classes W0.h added for
// ADR 0008 ruling D9. This table has never carried a crash_condition — those
// live in the Go matrix, which owns the two Delta PRD §9 rows.
func TestJVMParityMatrix_KindCountAndOwners(t *testing.T) {
	rows := loadJVMParityClasses(t)
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
	// One literal, read twice — the condition and the message can never drift
	// apart and quietly report a number nobody is checking.
	const wantClasses, wantCrash = 13, 0
	if classes != wantClasses || crash != wantCrash {
		t.Errorf("KIND: %s has %d change_class + %d crash_condition rows; want %d + %d",
			jvmParityClassesPath, classes, crash, wantClasses, wantCrash)
	}
	for _, r := range rows {
		if r.HarnessRow != harnessRequired {
			continue
		}
		if !jvmRequiredRowOwners[r.Owner] {
			t.Errorf("OWNER: %q is harness_row: %q so owner must be one of %v; got %q — "+
				"a required row may not arrive with an empty or invented owner",
				r.ID, harnessRequired, jvmRequiredRowOwnerList(), r.Owner)
		}
	}
}

// TestJVMParityMatrix_RequiredRowsAreProven pins that every required row reads
// verdict PROVEN (the harness proves them on both stores) and every deferred
// row reads ABSENT — the honesty rule that a deferred placeholder never claims
// a verdict it did not earn.
func TestJVMParityMatrix_RequiredRowsAreProven(t *testing.T) {
	for _, r := range loadJVMParityClasses(t) {
		switch r.HarnessRow {
		case harnessRequired:
			if r.Verdict != verdictProven {
				t.Errorf("required row %q reads verdict %q, want PROVEN", r.ID, r.Verdict)
			}
			if r.TestName != "TestJVMFullVsIncremental_ByteParity" {
				t.Errorf("required row %q must cite the JVM parity gate, got %q", r.ID, r.TestName)
			}
		case harnessDeferred:
			if r.Verdict != verdictAbsent {
				t.Errorf("deferred row %q reads verdict %q, want ABSENT", r.ID, r.Verdict)
			}
		}
	}
}

// TestJVMParityMatrix_Verdict binds the JVM matrix's verdict to what its harness
// row actually proves — the VERDICT direction (paritymatrix_test.go:286) mirrored
// for the JVM family. Required rows claim PROVEN only when TestJVMFullVsIncremental_ByteParity
// ran them; deferred rows claim ABSENT and cite nothing.
//
// The CITATION sub-assertion closes the same attack the Go guard's CITATION
// closes: five coordinated edits would otherwise buy a free PROVEN by setting
// harness_row: "deferred" yet pointing at the harness driver. With CITATION in
// place, that contradiction is mechanically caught.
func TestJVMParityMatrix_Verdict(t *testing.T) {
	const harnessFile = "engine/conformance/jvmparity_test.go"
	const harnessName = "TestJVMFullVsIncremental_ByteParity"
	for _, r := range loadJVMParityClasses(t) {
		switch r.HarnessRow {
		case harnessRequired:
			if r.Verdict == verdictAbsent {
				t.Errorf("VERDICT: JVM %q has a harness table row but still reads verdict: %q in %s",
					r.ID, r.Verdict, jvmParityClassesPath)
			}
			if r.TestFile != harnessFile {
				t.Errorf("VERDICT: JVM %q is proven by the harness table but test_file reads %q; want %q",
					r.ID, r.TestFile, harnessFile)
			}
			if r.TestName != harnessName {
				t.Errorf("VERDICT: JVM %q test_name reads %q; want %q", r.ID, r.TestName, harnessName)
			}
			if r.TestLine == "" {
				t.Errorf("VERDICT: JVM %q is a required row but test_line is empty; cite the line of %s in %s",
					r.ID, harnessName, harnessFile)
			}
			if r.KnownDefect != "" && r.Verdict == verdictProven {
				t.Errorf("VERDICT: JVM %q declares known_defect %q so it may NOT read verdict: %q",
					r.ID, r.KnownDefect, verdictProven)
			}
		case harnessDeferred:
			if r.TestFile == harnessFile || r.TestName == harnessName {
				t.Errorf("CITATION: JVM %q is harness_row: %q yet cites the harness driver "+
					"(test_file=%q test_name=%q). A deferred row cannot claim a verdict the "+
					"harness produced; either give it a real table row and mark %q, or cite the "+
					"proof that actually covers it.",
					r.ID, harnessDeferred, r.TestFile, r.TestName, harnessRequired)
			}
		}
	}
}

// TestJVMParityMatrix_Axis binds the JVM matrix's profile/store claim to the
// axes the harness actually runs — the AXIS direction (paritymatrix_test.go:460)
// mirrored for the JVM family. Without it, deleting balanced from parityProfiles()
// would leave this guard green while 13 rows kept publishing "proven under both
// profiles" — the same hole review round 1 demonstrated on the Go side.
//
// THIS TEST IS THE FAIL-ON-MUTATION SANITY CHECK the SW-189 ticket asks for:
// removing the balanced entry from parityProfiles() makes this test go red, by
// construction.
func TestJVMParityMatrix_Axis(t *testing.T) {
	profilesSeen := map[profile.Profile]bool{}
	for _, pr := range parityProfiles() {
		profilesSeen[pr.p] = true
	}
	if !profilesSeen[""] || !profilesSeen[profile.Balanced] {
		t.Errorf("AXIS: parityProfiles() runs {default=%v, balanced=%v}; the JVM matrix claims profile: \"both\" "+
			"on every required row, so both must run — a narrowed axis must fail the family guards too",
			profilesSeen[""], profilesSeen[profile.Balanced])
	}
	if n := len(parityBackends()); n != 2 {
		t.Errorf("AXIS: parityBackends() runs %d backend(s); the JVM matrix claims store: \"both\", so 2 are required", n)
	}
	for _, r := range loadJVMParityClasses(t) {
		if r.HarnessRow != harnessRequired {
			continue
		}
		if r.Store != "both" {
			t.Errorf("AXIS: JVM %q is harness_row: %q but reads store: %q; want \"both\"", r.ID, harnessRequired, r.Store)
		}
		if r.Profile != "both" {
			t.Errorf("AXIS: JVM %q is harness_row: %q but reads profile: %q; want \"both\"", r.ID, harnessRequired, r.Profile)
		}
	}
}

// TestJVMParityMatrix_Vocabulary enforces the closed vocabularies on every JVM
// row — the VOCABULARY direction (paritymatrix_test.go:483) mirrored for the JVM
// family. The shared constants (legalFixtures, legalStores, legalProfiles,
// legalAssertions, legalHarnessRows, legalVerdicts, legalKinds) live in
// paritymatrix_test.go and are the SAME closed set the Go guard enforces; this
// guard is a structural mirror for the JVM YAML's claim.
func TestJVMParityMatrix_Vocabulary(t *testing.T) {
	oneOf := func(v string, legal []string) bool {
		for _, l := range legal {
			if v == l {
				return true
			}
		}
		return false
	}
	for _, r := range loadJVMParityClasses(t) {
		check := func(field, value string, legal []string, allowEmpty bool) {
			if value == "" && allowEmpty {
				return
			}
			if !oneOf(value, legal) {
				t.Errorf("VOCABULARY: JVM %q has %s: %q, which is outside the declared vocabulary %v",
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
			t.Errorf("VOCABULARY: JVM %q claims assertion: %q with store: %q — bytes can only come from a store",
				r.ID, r.Assertion, storeNone)
		}
	}
}
