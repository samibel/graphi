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

	for _, class := range []string{"structural", "search", "agent_tools"} {
		if r.WarmSamples[class] == 0 {
			t.Errorf("op class %q has no warm samples", class)
		}
		if len(r.WarmOps[class]) == 0 {
			t.Errorf("op class %q lists no ops", class)
		}
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
}

func TestFullRun_UnknownRepoIsAUsageError(t *testing.T) {
	root := repoRoot(t)
	code := runFullRun(filepath.Join(root, "corpus", "manifest.json"), "no-such-repo", t.TempDir(), "test", filepath.Join(t.TempDir(), "r.json"))
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
}
