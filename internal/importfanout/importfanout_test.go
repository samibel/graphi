package importfanout_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/internal/goldenfile"
	"github.com/samibel/graphi/internal/importfanout"
)

// BaselinePath is the checked-in AX-00 (SW-220) baseline record, relative to the
// module root. Keeping it under docs/rc/ puts it beside the other RC baseline
// artifacts rather than hiding it in a testdata directory nobody browses.
const baselineRelPath = "docs/rc/ax00-import-fanout.json"

// measuredPackage is the hub the extension-kernel plan is about.
const measuredPackage = "surfaces/client"

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found walking up from the test directory")
		}
		dir = parent
	}
}

func encode(t *testing.T, v any) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

// TestAX00_SurfacesClientImportFanout_Reported is AC-5.
//
// It is deliberately NON-BLOCKING on the number itself: a fan-out change is
// reported, never failed. What it DOES fail on is the measurement becoming
// unreadable — an unmeasurable package or a missing/corrupt baseline, because a
// metric nobody can compute is worse than no metric (it looks like coverage).
//
// Re-record the baseline deliberately, after reviewing why it moved:
//
//	GRAPHI_UPDATE_GOLDEN=1 go test ./internal/importfanout
func TestAX00_SurfacesClientImportFanout_Reported(t *testing.T) {
	root := moduleRoot(t)
	current, err := importfanout.Measure(filepath.Join(root, filepath.FromSlash(measuredPackage)), measuredPackage)
	if err != nil {
		t.Fatalf("measuring %s: %v", measuredPackage, err)
	}

	baselinePath := filepath.Join(root, filepath.FromSlash(baselineRelPath))

	if goldenfile.UpdateRequested() {
		if err := os.WriteFile(baselinePath, encode(t, current), 0o644); err != nil {
			t.Fatalf("write baseline %s: %v", baselinePath, err)
		}
		t.Logf("baseline %s RE-RECORDED (%s=1): fan-out %d over %d files", baselineRelPath, goldenfile.UpdateEnvVar, current.Fanout, current.Files)
		return
	}

	raw, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatalf("AX-00 fan-out baseline %s is missing or unreadable (%v). The metric is non-blocking, but it must exist: re-record it with `%s=1 go test ./internal/importfanout`.", baselineRelPath, err, goldenfile.UpdateEnvVar)
	}
	var baseline importfanout.Result
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatalf("AX-00 fan-out baseline %s is corrupt: %v", baselineRelPath, err)
	}
	if baseline.Package != measuredPackage {
		t.Fatalf("AX-00 fan-out baseline %s records package %q, but this test measures %q", baselineRelPath, baseline.Package, measuredPackage)
	}

	// The report. Not an assertion — this line IS the deliverable.
	t.Log(importfanout.Compare(baseline, current).Format(measuredPackage))
}

func TestMeasure_CountsDistinctInternalImportsAndIgnoresTestFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("a.go", "package p\n\nimport (\n\t\"fmt\"\n\t\"github.com/samibel/graphi/engine/query\"\n\t\"github.com/samibel/graphi/core/model\"\n\t\"github.com/other/thing\"\n)\n\nvar _ = fmt.Sprint\n")
	// A second file re-importing engine/query must NOT double count.
	write("b.go", "package p\n\nimport \"github.com/samibel/graphi/engine/query\"\n")
	// A test file's imports are not production coupling.
	write("a_test.go", "package p\n\nimport \"github.com/samibel/graphi/engine/search\"\n")
	// A non-Go file must be ignored entirely.
	write("notes.md", "github.com/samibel/graphi/engine/ingest\n")

	got, err := importfanout.Measure(dir, "some/pkg")
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if got.Fanout != 2 {
		t.Errorf("fanout = %d, want 2 (engine/query + core/model; stdlib, third-party, _test.go and .md excluded); imports=%v", got.Fanout, got.Imports)
	}
	want := []string{"core/model", "engine/query"}
	if len(got.Imports) != len(want) {
		t.Fatalf("imports = %v, want %v", got.Imports, want)
	}
	for i := range want {
		if got.Imports[i] != want[i] {
			t.Errorf("imports = %v, want %v (sorted)", got.Imports, want)
			break
		}
	}
	if got.Files != 2 {
		t.Errorf("files = %d, want 2 (a.go + b.go)", got.Files)
	}
	if got.Package != "some/pkg" {
		t.Errorf("package = %q, want %q", got.Package, "some/pkg")
	}
}

// TestMeasure_EmptyDirectoryIsAnErrorNotZero pins the honesty rule: a
// measurement that cannot see any code must not report a fan-out of 0, which
// would read as "perfectly decoupled".
func TestMeasure_EmptyDirectoryIsAnErrorNotZero(t *testing.T) {
	if _, err := importfanout.Measure(t.TempDir(), "empty/pkg"); err == nil {
		t.Fatal("Measure over a directory with no Go files returned no error — a broken measurement must not look like a fan-out of zero")
	}
}

func TestCompare_ReportsAddedAndRemovedWithoutAVerdict(t *testing.T) {
	baseline := importfanout.Result{Package: "p", Fanout: 3, Imports: []string{"a", "b", "c"}}
	current := importfanout.Result{Package: "p", Fanout: 3, Imports: []string{"a", "b", "d"}}

	d := importfanout.Compare(baseline, current)
	if len(d.Added) != 1 || d.Added[0] != "d" {
		t.Errorf("added = %v, want [d]", d.Added)
	}
	if len(d.Removed) != 1 || d.Removed[0] != "c" {
		t.Errorf("removed = %v, want [c]", d.Removed)
	}
	report := d.Format("p")
	for _, want := range []string{"NON-BLOCKING", "added:   d", "removed: c", "not a gate"} {
		if !bytes.Contains([]byte(report), []byte(want)) {
			t.Errorf("report missing %q:\n%s", want, report)
		}
	}
	for _, forbidden := range []string{"PASS", "FAIL"} {
		if bytes.Contains([]byte(report), []byte(forbidden)) {
			t.Errorf("report renders a verdict %q; this metric has no threshold:\n%s", forbidden, report)
		}
	}
}
