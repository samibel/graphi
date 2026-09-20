package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/eval/retrieval"
)

// TestCheckTargets_ExitsNonZeroAndNamesTheMiss is AC-7(a)'s failing-branch
// test. A gate seen only passing is a gate nobody has seen bite, so the world
// this runs against is the committed one with exactly one number moved.
func TestCheckTargets_ExitsNonZeroAndNamesTheMiss(t *testing.T) {
	chdirRoot(t)
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	world := t.TempDir()
	for _, rel := range []string{
		retrieval.TargetsFilePath,
		retrieval.GateReportPath,
		retrieval.BundleCoverageMeasurementPath,
		retrieval.QrelBlindSmokeOutcomePath,
	} {
		copyInto(t, root, world, rel)
	}

	// Break exactly one target: drive the gated baseline's nl_behaviour
	// ndcg@10 to zero. That stratum passes in the committed evidence, so a
	// miss here can only come from this edit.
	reportPath := filepath.Join(world, filepath.FromSlash(retrieval.GateReportPath))
	raw, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	var rep retrieval.Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatal(err)
	}
	touched := 0
	for i := range rep.Reproducible.Baselines {
		if rep.Reproducible.Baselines[i].Name != retrieval.GateBaseline {
			continue
		}
		b := &rep.Reproducible.Baselines[i]
		for j := range b.Queries {
			if b.Queries[j].Stratum == retrieval.StratumNLBehaviour {
				b.Queries[j].Metrics.NDCG10 = 0
				touched++
			}
		}
		b.Overall, b.Strata, b.Splits = retrieval.AggregateAll(b.Queries, rep.Reproducible.TokenBudgets)
	}
	if touched == 0 {
		t.Fatal("the committed gate report carries no nl_behaviour query for the gated baseline")
	}
	out, err := retrieval.MarshalReport(&rep)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reportPath, out, 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runCheckTargets(world, retrieval.GateReportPath, &stdout, &stderr)
	if code == exitOK {
		t.Fatalf("check-targets exited 0 on a report that misses a target:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "MISS "+retrieval.StratumNLBehaviour+" fusion_target") {
		t.Errorf("the output does not name the missed target:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "first miss") {
		t.Errorf("stderr does not name the first miss:\n%s", stderr.String())
	}
}

// TestCheckTargets_RefusesTheDerivationReport is AC-8 at the command boundary:
// a bar checked against the observations it was computed from is arithmetic,
// not a gate.
func TestCheckTargets_RefusesTheDerivationReport(t *testing.T) {
	chdirRoot(t)
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	targetsRaw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(retrieval.TargetsFilePath)))
	if err != nil {
		t.Fatal(err)
	}
	var tg retrieval.Targets
	if err := json.Unmarshal(targetsRaw, &tg); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCheckTargets(root, tg.DerivedFrom.Report, &stdout, &stderr); code == exitOK {
		t.Fatalf("check-targets graded the targets against their own derivation report:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "DERIVED from") {
		t.Errorf("the refusal does not say why: %s", stderr.String())
	}
}

func copyInto(t *testing.T, root, world, rel string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(world, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
