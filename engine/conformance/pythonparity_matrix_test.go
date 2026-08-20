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
