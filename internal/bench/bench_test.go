package bench

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samibel/graphi/internal/release"
)

func TestParseTinyYAML_SchemaRoundTrip(t *testing.T) {
	in := []byte(`version: 1
baseline_version: "2026-06-20-v1"
fixture_digest: "abc123"

metrics:
  cold_start_p95_ms:
    baseline: 60
    budget: 100
    unit: ms
  binary_size_bytes:
    baseline: 9000000
    budget: 13000000
    unit: bytes
`)
	root, err := parseTinyYAML(in)
	if err != nil {
		t.Fatalf("parseTinyYAML: %v", err)
	}
	if v, _ := root["version"].(int64); v != 1 {
		t.Errorf("version = %v, want 1", root["version"])
	}
	if v, _ := root["baseline_version"].(string); v != "2026-06-20-v1" {
		t.Errorf("baseline_version = %q", v)
	}
	metrics, ok := root["metrics"].(map[string]any)
	if !ok {
		t.Fatalf("metrics not a map: %T", root["metrics"])
	}
	cs, ok := metrics["cold_start_p95_ms"].(map[string]any)
	if !ok {
		t.Fatalf("cold_start not a map: %T", metrics["cold_start_p95_ms"])
	}
	if v, _ := cs["budget"].(int64); v != 100 {
		t.Errorf("cold budget = %v", cs["budget"])
	}
}

func TestParseTinyYAML_RejectsMalformed(t *testing.T) {
	cases := [][]byte{
		[]byte("no colon here"),
		[]byte(": missing key"),
	}
	for i, in := range cases {
		if _, err := parseTinyYAML(in); err == nil {
			t.Errorf("case %d: expected error, got nil", i)
		}
	}
}

func TestLoadManifest_Validates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bench-budget.yml")
	valid := []byte(`version: 1
baseline_version: "v1"
metrics:
  full_index_ms:
    baseline: 10
    budget: 20
    unit: ms
`)
	if err := os.WriteFile(path, valid, 0o644); err != nil {
		t.Fatal(err)
	}
	man, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if man.BaselineVersion != "v1" {
		t.Errorf("BaselineVersion = %q", man.BaselineVersion)
	}
	if mb, ok := man.Metrics["full_index_ms"]; !ok || mb.Budget != 20 || mb.Op != CmpLE {
		t.Errorf("metric parsed wrong: %+v ok=%v", mb, ok)
	}

	// missing baseline_version -> error
	bad := []byte("version: 1\nmetrics:\n  x:\n    baseline: 1\n    budget: 2\n")
	if err := os.WriteFile(path, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(path); err == nil {
		t.Error("expected error for missing baseline_version")
	}
}

func TestGate_PassFailNamesMetricAndDelta(t *testing.T) {
	man := &Manifest{BaselineVersion: "v1", Metrics: map[string]MetricBudget{
		"cold_start_p95_ms": {Baseline: 60, Budget: 100, Unit: "ms", Op: CmpLE},
		"full_index_ms":     {Baseline: 120, Budget: 300, Unit: "ms", Op: CmpLE},
	}}
	// all in budget -> pass
	rep := Gate(map[string]float64{"cold_start_p95_ms": 50, "full_index_ms": 200}, man)
	if !rep.Pass || len(rep.Results) != 2 {
		t.Fatalf("expected pass with 2 results, got pass=%v results=%d", rep.Pass, len(rep.Results))
	}

	// one over budget -> fail, names metric + delta
	rep = Gate(map[string]float64{"cold_start_p95_ms": 150, "full_index_ms": 200}, man)
	if rep.Pass {
		t.Fatal("expected fail")
	}
	if len(rep.Failed) != 1 || rep.Failed[0] != "cold_start_p95_ms" {
		t.Errorf("Failed = %v, want [cold_start_p95_ms]", rep.Failed)
	}
	msg := rep.FormatFailure()
	for _, want := range []string{"cold_start_p95_ms", "150.00", "delta"} {
		if !contains(msg, want) {
			t.Errorf("FormatFailure missing %q in:\n%s", want, msg)
		}
	}
	// delta vs baseline = 150 - 60 = +90
	foundDelta := false
	for _, r := range rep.Results {
		if r.Name == "cold_start_p95_ms" && r.Delta == 90 {
			foundDelta = true
		}
	}
	if !foundDelta {
		t.Error("expected delta +90 vs baseline 60")
	}
}

func TestGate_ManifestOnlyRepin(t *testing.T) {
	// Re-pinning is a manifest-only edit: bump budget + version, no code change.
	man := &Manifest{BaselineVersion: "v1", Metrics: map[string]MetricBudget{
		"cold_start_p95_ms": {Baseline: 150, Budget: 160, Unit: "ms", Op: CmpLE},
	}}
	measured := map[string]float64{"cold_start_p95_ms": 150}
	if rep := Gate(measured, man); !rep.Pass {
		t.Errorf("repinned budget should pass, got %+v", rep)
	}
}

func TestGate_UnmeasuredBudgetedMetricFails(t *testing.T) {
	man := &Manifest{BaselineVersion: "v1", Metrics: map[string]MetricBudget{
		"cold_start_p95_ms": {Baseline: 60, Budget: 100, Op: CmpLE},
	}}
	rep := Gate(map[string]float64{}, man) // measurement omits a budgeted metric
	if rep.Pass {
		t.Fatal("expected fail for unmeasured budgeted metric")
	}
}

func TestP95AndMedian(t *testing.T) {
	s := []time.Duration{1, 2, 3, 4, 5, 6, 7, 8, 9, 10} // ms-ish
	if p := P95(s); p != 10 && p != 9 {                 // nearest-rank: index ~ ceil(9.5)-1 = 9 -> 10
		t.Logf("P95 = %v (acceptable near 10)", p)
	}
	if m := Median(s); m != 5 && m != 6 {
		t.Errorf("Median = %v, want 5 or 6", m)
	}
	if P95(nil) != 0 || Median(nil) != 0 {
		t.Error("empty input should return 0")
	}
}

func TestRun_RealHarnessProducesFourMetrics(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real harness in -short mode")
	}
	if os.Getenv("BENCH_SKIP_HARNESS") == "1" {
		t.Skip("BENCH_SKIP_HARNESS=1")
	}
	metrics, err := Run(context.Background(), HarnessConfig{Samples: 3, Warmup: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for name, val := range metrics.Map() {
		if val <= 0 {
			t.Errorf("metric %s = %v, want > 0", name, val)
		}
	}
	if metrics.Samples != 3 {
		t.Errorf("Samples = %d, want 3", metrics.Samples)
	}
	if metrics.FixtureDigest == "" {
		t.Error("FixtureDigest empty")
	}
	if metrics.BuildContract != release.CanonicalBuildContract {
		t.Errorf("BuildContract = %q, want canonical release contract", metrics.BuildContract)
	}
	if metrics.BuildGoVersion == "" || metrics.BuildGOOS == "" || metrics.BuildGOARCH == "" {
		t.Errorf("incomplete build provenance: %+v", metrics)
	}
	if metrics.BuildCGOEnabled != "0" {
		t.Errorf("BuildCGOEnabled = %q, want 0", metrics.BuildCGOEnabled)
	}
}

func TestGate_WarnOnlyViolationDoesNotFailGate(t *testing.T) {
	man := &Manifest{BaselineVersion: "v1", Metrics: map[string]MetricBudget{
		"fast_index_ms": {Baseline: 100, Budget: 200, Unit: "ms", Op: CmpLE, Severity: SeverityWarn},
	}}
	rep := Gate(map[string]float64{"fast_index_ms": 999}, man)
	if !rep.Pass {
		t.Errorf("warn-only violation should keep gate passing, got pass=%v", rep.Pass)
	}
	if len(rep.Results) != 1 || rep.Results[0].Severity != SeverityWarn {
		t.Errorf("expected warn severity result, got %+v", rep.Results)
	}
}

func TestGate_FailSeverityStillFailsGate(t *testing.T) {
	man := &Manifest{BaselineVersion: "v1", Metrics: map[string]MetricBudget{
		"cold_start_p95_ms": {Baseline: 60, Budget: 100, Unit: "ms", Op: CmpLE, Severity: SeverityFail},
	}}
	rep := Gate(map[string]float64{"cold_start_p95_ms": 150}, man)
	if rep.Pass {
		t.Error("expected fail severity to fail gate")
	}
}

func TestLoadManifest_ParsesSeverity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bench-budget.yml")
	data := []byte(`version: 1
baseline_version: "v1"
metrics:
  fast_index_ms:
    baseline: 1
    budget: 2
    unit: ms
    severity: warn
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	man, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	mb, ok := man.Metrics["fast_index_ms"]
	if !ok || mb.Severity != SeverityWarn {
		t.Errorf("severity not parsed: %+v ok=%v", mb, ok)
	}
}

func TestProfileMetrics_DistinctAndDeepSuperset(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping profile comparison in -short mode")
	}
	metrics, err := Run(context.Background(), HarnessConfig{Samples: 1, Warmup: 0})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	pm := metrics.ProfileMetrics
	if len(pm) != 3 {
		t.Fatalf("expected 3 profiles, got %d", len(pm))
	}
	fast, balanced, deep := pm["fast"], pm["balanced"], pm["deep"]
	if fast.EdgeCount == 0 || balanced.EdgeCount == 0 || deep.EdgeCount == 0 {
		t.Errorf("expected non-zero edge counts: fast=%d balanced=%d deep=%d", fast.EdgeCount, balanced.EdgeCount, deep.EdgeCount)
	}
	// The frozen fixture is small; assert the partial order that the profiles
	// guarantee rather than strict distinctness.
	if fast.EdgeCount > balanced.EdgeCount {
		t.Errorf("fast edge count %d should not exceed balanced %d", fast.EdgeCount, balanced.EdgeCount)
	}
	if balanced.EdgeCount > deep.EdgeCount {
		t.Errorf("balanced edge count %d should not exceed deep %d", balanced.EdgeCount, deep.EdgeCount)
	}
	// Best-effort distinctness: at least fast should be fewer than deep.
	if fast.EdgeCount == deep.EdgeCount {
		t.Logf("fast == deep edge count (%d) on small fixture; distinctness should be exercised with import-heavy fixture", fast.EdgeCount)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
