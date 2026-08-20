package conformance_test

// SW-181 (language-GA program G3): the Python parity-class matrix drift
// guard. Binds docs/rc/parity-classes-python.yaml to the harness table
// pyChangeClassTable() in pythonparity_test.go, so a class declared
// required with no harness row (MISSING), a harness row for an undeclared
// id (PHANTOM), or a deferred row that nonetheless has a harness row
// (DEFERRED) fails the build.
//
// The Python guard is the JVM twin's structure (jvmparity_matrix_test.go),
// focused on the three directions the wave-2 Python subset needs:
// MISSING, PHANTOM, DEFERRED. The four directions the JVM guard is MISSING
// (VERDICT, AXIS, VOCABULARY, KIND-as-distinct-from-OWNER) are not closed
// here — recording them is the template's TEMPL-P4, and this copy inherits
// the JVM's narrowing. The closing guard belongs to SW-184 (the light gate
// set + GA-LANG rows for the cross-file-heuristic residual) and is a
// separate product-byte change.

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/samibel/graphi/core/profile"
)

// pythonParityClassesPath is docs/rc/parity-classes-python.yaml relative to
// this package's directory (go test's working directory). Mirrors the JVM
// const in jvmparity_matrix_test.go.
const pythonParityClassesPath = "../../docs/rc/parity-classes-python.yaml"

// loadPythonParityClasses parses the Python matrix. Reuses parityRow from
// paritymatrix_test.go and yaml.Unmarshal — a test-only import.
func loadPythonParityClasses(t *testing.T) []parityRow {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(pythonParityClassesPath))
	if err != nil {
		t.Fatalf("read %s: %v", pythonParityClassesPath, err)
	}
	var doc struct {
		ParityClasses []parityRow `yaml:"parity_classes"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", pythonParityClassesPath, err)
	}
	if len(doc.ParityClasses) == 0 {
		t.Fatalf("%s declared no parity_classes rows", pythonParityClassesPath)
	}
	return doc.ParityClasses
}

// TestPythonParityMatrix_DriftGuard is the Python twin of
// TestJVMParityMatrix_DriftGuard. The three directions the guard runs
// (MISSING, PHANTOM, DEFERRED) are the three this matrix needs at SW-181
// gate — the other four directions (VERDICT, AXIS, VOCABULARY, KIND-as-
// distinct-from-OWNER) are documented in the YAML header as TEMPL-P4
// inherited and are CLOSED in SW-184, not here.
func TestPythonParityMatrix_DriftGuard(t *testing.T) {
	rows := loadPythonParityClasses(t)

	declared := map[string]parityRow{}
	for _, r := range rows {
		if _, dup := declared[r.ID]; dup {
			t.Fatalf("duplicate id %q in %s", r.ID, pythonParityClassesPath)
		}
		declared[r.ID] = r
	}

	harness := map[string]bool{}
	for _, row := range pyChangeClassTable() {
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

// pythonRequiredRowOwners is the CLOSED set of work packages permitted to
// own a harness_row: "required" row in docs/rc/parity-classes-python.yaml.
// Mirrors jvmRequiredRowOwners from jvmparity_matrix_test.go.
//
// WHY IT IS A CLOSED SET: SW-181's AC-1 says the Python matrix carries the
// full SW-180 asset set, and the asset set is owned by two packages —
// W2-PY.a (the basic add/modify/delete + cross-module call + selector
// rows) and W2-PY.b (the two ambiguity/move paragraphs that need the
// clause-keyed QN convention spelled out). Any other owner tagging a
// required row is a routing slip the closed set must reject.
var pythonRequiredRowOwners = map[string]bool{
	"W2-PY.a": true, // add/modify/delete + from-import + selector + relative skip
	"W2-PY.b": true, // ambiguous clauses + same-package move
}

// pythonRequiredRowOwnerList renders pythonRequiredRowOwners deterministically.
func pythonRequiredRowOwnerList() []string {
	out := make([]string, 0, len(pythonRequiredRowOwners))
	for k := range pythonRequiredRowOwners {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestPythonParityMatrix_KindCountAndOwners pins the Python matrix's row
// count by KIND and its owner set. The count stays a PINNED literal rather
// than a `>=` bound for the same reason the Go and JVM matrices do: the
// guard's whole job is that no row can be added, removed or re-kinded
// unnoticed, and a bound forfeits exactly that. Adding a class means
// updating this number and saying why, here.
//
// 8 + 0: the eight change-class rows SW-181 added, in the same order as
// the YAML. The Python matrix does NOT carry a crash_condition row — the
// two Delta PRD §9 crash rows live in the Go matrix, which owns them.
func TestPythonParityMatrix_KindCountAndOwners(t *testing.T) {
	rows := loadPythonParityClasses(t)
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
	// One literal, read twice — the condition and the message can never
	// drift apart and quietly report a number nobody is checking.
	const wantClasses, wantCrash = 8, 0
	if classes != wantClasses || crash != wantCrash {
		t.Errorf("KIND: %s has %d change_class + %d crash_condition rows; want %d + %d",
			pythonParityClassesPath, classes, crash, wantClasses, wantCrash)
	}
	for _, r := range rows {
		if r.HarnessRow != harnessRequired {
			continue
		}
		if !pythonRequiredRowOwners[r.Owner] {
			t.Errorf("OWNER: %q is harness_row: %q so owner must be one of %v; got %q — "+
				"a required row may not arrive with an empty or invented owner",
				r.ID, harnessRequired, pythonRequiredRowOwnerList(), r.Owner)
		}
	}
}

// TestPythonParityMatrix_RequiredRowsAreProven pins that every required row
// reads verdict PROVEN (the harness proves them on both stores) and every
// deferred row reads ABSENT — the honesty rule that a deferred placeholder
// never claims a verdict it did not earn.
func TestPythonParityMatrix_RequiredRowsAreProven(t *testing.T) {
	for _, r := range loadPythonParityClasses(t) {
		switch r.HarnessRow {
		case harnessRequired:
			if r.Verdict != verdictProven {
				t.Errorf("required row %q reads verdict %q, want PROVEN", r.ID, r.Verdict)
			}
			if r.TestName != "TestPythonFullVsIncremental_ByteParity" {
				t.Errorf("required row %q must cite the Python parity gate, got %q", r.ID, r.TestName)
			}
		case harnessDeferred:
			if r.Verdict != verdictAbsent {
				t.Errorf("deferred row %q reads verdict %q, want ABSENT", r.ID, r.Verdict)
			}
		}
	}
}

// TestPythonParityMatrix_Verdict binds the Python matrix's verdict to what its
// harness row actually proves — the VERDICT direction mirrored for the Python
// family. Required rows claim PROVEN only when
// TestPythonFullVsIncremental_ByteParity ran them; deferred rows claim ABSENT
// and cite nothing. The CITATION sub-assertion closes the same attack the Go
// guard closes (paritymatrix_test.go:392-405).
func TestPythonParityMatrix_Verdict(t *testing.T) {
	const harnessFile = "engine/conformance/pythonparity_test.go"
	const harnessName = "TestPythonFullVsIncremental_ByteParity"
	for _, r := range loadPythonParityClasses(t) {
		switch r.HarnessRow {
		case harnessRequired:
			if r.Verdict == verdictAbsent {
				t.Errorf("VERDICT: Python %q has a harness table row but still reads verdict: %q in %s",
					r.ID, r.Verdict, pythonParityClassesPath)
			}
			if r.TestFile != harnessFile {
				t.Errorf("VERDICT: Python %q is proven by the harness table but test_file reads %q; want %q",
					r.ID, r.TestFile, harnessFile)
			}
			if r.TestName != harnessName {
				t.Errorf("VERDICT: Python %q test_name reads %q; want %q", r.ID, r.TestName, harnessName)
			}
			if r.TestLine == "" {
				t.Errorf("VERDICT: Python %q is a required row but test_line is empty; cite the line of %s in %s",
					r.ID, harnessName, harnessFile)
			}
			if r.KnownDefect != "" && r.Verdict == verdictProven {
				t.Errorf("VERDICT: Python %q declares known_defect %q so it may NOT read verdict: %q",
					r.ID, r.KnownDefect, verdictProven)
			}
		case harnessDeferred:
			if r.TestFile == harnessFile || r.TestName == harnessName {
				t.Errorf("CITATION: Python %q is harness_row: %q yet cites the harness driver "+
					"(test_file=%q test_name=%q). A deferred row cannot claim a verdict the "+
					"harness produced.",
					r.ID, harnessDeferred, r.TestFile, r.TestName)
			}
		}
	}
}

// TestPythonParityMatrix_Axis binds the Python matrix's profile/store claim to
// the axes the harness actually runs — the AXIS direction mirrored for the
// Python family. Removing the balanced entry from parityProfiles() makes this
// test go red, by construction.
func TestPythonParityMatrix_Axis(t *testing.T) {
	profilesSeen := map[profile.Profile]bool{}
	for _, pr := range parityProfiles() {
		profilesSeen[pr.p] = true
	}
	if !profilesSeen[""] || !profilesSeen[profile.Balanced] {
		t.Errorf("AXIS: parityProfiles() runs {default=%v, balanced=%v}; the Python matrix claims profile: \"both\" "+
			"on every required row, so both must run",
			profilesSeen[""], profilesSeen[profile.Balanced])
	}
	if n := len(parityBackends()); n != 2 {
		t.Errorf("AXIS: parityBackends() runs %d backend(s); the Python matrix claims store: \"both\", so 2 are required", n)
	}
	for _, r := range loadPythonParityClasses(t) {
		if r.HarnessRow != harnessRequired {
			continue
		}
		if r.Store != "both" {
			t.Errorf("AXIS: Python %q is harness_row: %q but reads store: %q; want \"both\"", r.ID, harnessRequired, r.Store)
		}
		if r.Profile != "both" {
			t.Errorf("AXIS: Python %q is harness_row: %q but reads profile: %q; want \"both\"", r.ID, harnessRequired, r.Profile)
		}
	}
}

// TestPythonParityMatrix_Vocabulary enforces the closed vocabularies on every
// Python row — the VOCABULARY direction mirrored for the Python family.
func TestPythonParityMatrix_Vocabulary(t *testing.T) {
	oneOf := func(v string, legal []string) bool {
		for _, l := range legal {
			if v == l {
				return true
			}
		}
		return false
	}
	for _, r := range loadPythonParityClasses(t) {
		check := func(field, value string, legal []string, allowEmpty bool) {
			if value == "" && allowEmpty {
				return
			}
			if !oneOf(value, legal) {
				t.Errorf("VOCABULARY: Python %q has %s: %q, which is outside the declared vocabulary %v",
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
			t.Errorf("VOCABULARY: Python %q claims assertion: %q with store: %q — bytes can only come from a store",
				r.ID, r.Assertion, storeNone)
		}
	}
}
