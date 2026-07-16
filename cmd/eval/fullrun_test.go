package main

// SW-123 (EVAL-02): hermetic gate for the full-run harness. It measures the
// LOCAL tier-1 hero fixture (no network, no clone) end-to-end and pins the
// report shape the CI evidence runs produce — so a harness regression is a
// test failure here, not a broken artifact discovered after a 30-minute
// guava job.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/internal/evalreport"
)

func TestFullRun_HermeticFixture_ProducesCompleteEvidence(t *testing.T) {
	root := repoRoot(t)
	outPath := filepath.Join(t.TempDir(), "report.json")

	code := runFullRun(
		filepath.Join(root, "corpus", "manifest.json"),
		"tier1-fixture-hero-go",
		t.TempDir(),
		"test",
		outPath,
		"",
	)
	if code != 0 {
		t.Fatalf("runFullRun exit code = %d, want 0", code)
	}

	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var rep evalreport.FullRunReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatalf("parse report: %v", err)
	}

	if rep.RunnerClass != "test" {
		t.Errorf("runner_class = %q, want %q", rep.RunnerClass, "test")
	}
	r := rep.Repo
	if !r.Pass || len(r.Failures) > 0 {
		t.Fatalf("run did not pass: %+v", r.Failures)
	}
	if r.Index.Nodes == 0 || r.Index.Edges == 0 || r.Index.Files != 3 {
		t.Errorf("index counts implausible: %+v", r.Index)
	}
	if r.Index.DBSizeBytes == 0 {
		t.Error("db_size_bytes is zero — the on-disk store was not measured")
	}
	if r.Index.PeakRSSMB == 0 {
		t.Error("peak_rss_mb is zero — getrusage was not sampled")
	}
	if r.StablePeakRSSMB == 0 || r.StablePeakRSSMB < r.Index.PeakRSSMB {
		t.Errorf("stable_peak_rss_mb = %d, index peak = %d; complete-session MAXRSS not recorded", r.StablePeakRSSMB, r.Index.PeakRSSMB)
	}

	for _, class := range []string{"structural", "search", "agent_tools"} {
		if r.WarmSamples[class] == 0 {
			t.Errorf("op class %q has no warm samples", class)
		}
		if len(r.WarmOps[class]) == 0 {
			t.Errorf("op class %q lists no ops", class)
		}
	}
	foundImpact := false
	for _, op := range r.WarmOps["structural"] {
		if op == "impact" {
			foundImpact = true
		}
	}
	if !foundImpact {
		t.Error("stable impact operation is absent from the warm suite")
	}
	// Per-op resolution must cover every pooled op (ADR 0003 U2 needs to see
	// which op dominates a class).
	for class, ops := range r.WarmOps {
		for _, op := range ops {
			if _, ok := r.WarmP95USPerOp[op]; !ok {
				t.Errorf("class %q op %q missing from warm_p95_us_per_op", class, op)
			}
		}
	}
	if len(r.Searches) == 0 || !r.Searches[0].Pass {
		t.Errorf("manifest search assertions not verified: %+v", r.Searches)
	}
	if len(r.StableChecks) != 12 {
		t.Fatalf("stable semantic checks = %d, want exactly 12: %+v", len(r.StableChecks), r.StableChecks)
	}
	for _, check := range r.StableChecks {
		if !check.Pass || check.Samples == 0 || len(check.Outcomes) == 0 {
			t.Errorf("stable operation was measured without semantic proof: %+v", check)
		}
	}
	if len(r.SemanticChecks) == 0 || !r.SemanticChecks[0].Pass {
		t.Errorf("manifest confirmed-edge assertion not enforced: %+v", r.SemanticChecks)
	}
}

func TestRenderedAndContractOutcomeFailClosed(t *testing.T) {
	if got := renderedOutcome([]string{"not-an-outcome"}); got != "" {
		t.Fatalf("renderedOutcome malformed = %q, want empty", got)
	}
	if _, err := contractOutcome(nil, nil); err == nil {
		t.Fatal("nil agent contract must fail semantic validation")
	}
}

func TestFullRun_UnknownRepoIsAUsageError(t *testing.T) {
	root := repoRoot(t)
	code := runFullRun(filepath.Join(root, "corpus", "manifest.json"), "no-such-repo", t.TempDir(), "test", filepath.Join(t.TempDir(), "r.json"), "")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
}
