package conformance_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
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
