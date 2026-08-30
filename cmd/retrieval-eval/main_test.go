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

// repoRoot is the repository root; the binary's relative defaults (the
// fixture path, corpus/manifest.json) are anchored there.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func chdirRoot(t *testing.T) {
	t.Helper()
	root := repoRoot(t)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
}

const fixtureDataset = "internal/eval/retrieval/testdata/datasets/fixture-v1.json"

// AC-5 / AC-10 end to end: the dispatch entry point runs the fixture through
// the real seams, exports raw samples, and -aggregate reproduces them.
func TestRetrievalEval_FixtureRunExportAndAggregate(t *testing.T) {
	chdirRoot(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "report.json")
	raw := filepath.Join(dir, "raw")
	var stderr bytes.Buffer

	code := run([]string{"-manifest", "corpus/manifest.json", "-repo", FixtureRepoName, "-dataset", fixtureDataset,
		"-out", out, "-export-raw", raw, "-runner-class", "test", "-repeats", "2", "-date", "2026-08-30"}, &bytes.Buffer{}, &stderr)
	if code != exitOK {
		t.Fatalf("run exit %d\n%s", code, stderr.String())
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var report retrieval.Report
	if err := json.Unmarshal(b, &report); err != nil {
		t.Fatal(err)
	}
	if report.Reproducible.Repo.Name != FixtureRepoName || report.Reproducible.RunnerClass != "test" || len(report.Reproducible.Baselines) != 4 {
		t.Errorf("report header = %+v", report.Reproducible)
	}
	if !strings.Contains(stderr.String(), "semantic_name_only") || !strings.Contains(stderr.String(), "unavailable") {
		t.Errorf("summary does not report the unavailable baseline:\n%s", stderr.String())
	}

	t.Run("aggregate reproduces the untouched run", func(t *testing.T) {
		var w bytes.Buffer
		if code := run([]string{"-aggregate", raw}, &bytes.Buffer{}, &w); code != retrieval.ExitReproduced {
			t.Fatalf("aggregate exit %d\n%s", code, w.String())
		}
		if !strings.Contains(w.String(), "PASS") {
			t.Errorf("aggregate output:\n%s", w.String())
		}
		if _, err := os.Stat(filepath.Join(raw, AggregateFile)); err != nil {
			t.Errorf("aggregate.json not written: %v", err)
		}
	})

	t.Run("aggregate exits 1 on a nudged report", func(t *testing.T) {
		nudged := filepath.Join(t.TempDir(), "raw")
		if code := run([]string{"-manifest", "corpus/manifest.json", "-repo", FixtureRepoName, "-dataset", fixtureDataset,
			"-out", filepath.Join(filepath.Dir(nudged), "r.json"), "-export-raw", nudged, "-repeats", "1"}, &bytes.Buffer{}, &bytes.Buffer{}); code != exitOK {
			t.Fatal("second run failed")
		}
		p := filepath.Join(nudged, retrieval.ReportFile)
		rb, _ := os.ReadFile(p)
		var r retrieval.Report
		_ = json.Unmarshal(rb, &r)
		r.Reproducible.Baselines[0].Overall.Metrics[retrieval.MetricRecall10] += 0.01
		nb, _ := retrieval.MarshalReport(&r)
		_ = os.WriteFile(p, nb, 0o644)
		ib, _ := os.ReadFile(filepath.Join(nudged, retrieval.RunIndexFile))
		var idx retrieval.RunIndex
		_ = json.Unmarshal(ib, &idx)
		idx.ReportSHA256 = retrieval.SHA256Hex(nb)
		ob, _ := json.MarshalIndent(idx, "", "  ")
		_ = os.WriteFile(filepath.Join(nudged, retrieval.RunIndexFile), ob, 0o644)

		var w bytes.Buffer
		if code := run([]string{"-aggregate", nudged}, &bytes.Buffer{}, &w); code != retrieval.ExitDiscrepancy {
			t.Errorf("aggregate exit %d, want %d\n%s", code, retrieval.ExitDiscrepancy, w.String())
		}
	})

	t.Run("derive writes targets and budgets citing the report", func(t *testing.T) {
		targets := filepath.Join(dir, "targets.json")
		budgets := filepath.Join(dir, "budgets.json")
		var w bytes.Buffer
		if code := run([]string{"-derive", "-targets-report", out, "-budget-small", out,
			"-targets-out", targets, "-budgets-out", budgets, "-date", "2026-08-30"}, &bytes.Buffer{}, &w); code != exitOK {
			t.Fatalf("derive exit %d\n%s", code, w.String())
		}
		tb, err := os.ReadFile(targets)
		if err != nil {
			t.Fatal(err)
		}
		var tg retrieval.Targets
		if err := json.Unmarshal(tb, &tg); err != nil {
			t.Fatal(err)
		}
		if tg.DerivedFrom.Report != out || tg.DerivedFrom.SHA256 != retrieval.SHA256Hex(b) || tg.ImmutableUntil != retrieval.ImmutableUntil {
			t.Errorf("targets derived_from = %+v", tg.DerivedFrom)
		}
		bb, err := os.ReadFile(budgets)
		if err != nil {
			t.Fatal(err)
		}
		var bg retrieval.Budgets
		if err := json.Unmarshal(bb, &bg); err != nil {
			t.Fatal(err)
		}
		if bg.Fixtures[retrieval.FixtureSmall].Status != retrieval.StatusMeasured || bg.Fixtures[retrieval.FixtureLarge].Status != retrieval.StatusUnknown {
			t.Errorf("budgets = %+v", bg.Fixtures)
		}
	})
}

func TestRetrievalEval_UsageErrors(t *testing.T) {
	chdirRoot(t)
	cases := []struct {
		name string
		args []string
		want int
		msg  string
	}{
		{"no repo", []string{"-dataset", fixtureDataset, "-out", "x"}, exitUsage, "-repo"},
		{"unknown baseline", []string{"-repo", FixtureRepoName, "-dataset", fixtureDataset, "-out", "x", "-baseline", "bm42"}, exitUsage, "bm42"},
		{"dataset for another repo", []string{"-repo", "cobra", "-dataset", fixtureDataset, "-out", "x"}, exitUsage, "judged against"},
		{"repo not in manifest", []string{"-repo", "nope", "-dataset", fixtureDataset, "-out", "x"}, exitUsage, "judged against"},
		{"aggregate over a missing dir", []string{"-aggregate", filepath.Join(t.TempDir(), "missing")}, retrieval.ExitUsage, "run.json"},
		{"derive without outputs", []string{"-derive"}, exitUsage, "-targets-out"},
		{"budgets without a report", []string{"-derive", "-budgets-out", filepath.Join(t.TempDir(), "b.json")}, exitUsage, "-budget-small"},
		{"unknown flag", []string{"-frobnicate"}, exitUsage, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var w bytes.Buffer
			if code := run(tc.args, &bytes.Buffer{}, &w); code != tc.want {
				t.Errorf("exit %d, want %d\n%s", code, tc.want, w.String())
			}
			if tc.msg != "" && !strings.Contains(w.String(), tc.msg) {
				t.Errorf("output does not mention %q:\n%s", tc.msg, w.String())
			}
		})
	}
}

// A URL-pinned entry is never cloned: an absent checkout fails closed with
// the instruction, and a checkout at another sha is refused.
func TestResolveRepo_PinnedEntryNeedsALocalCheckoutAtTheSHA(t *testing.T) {
	chdirRoot(t)
	ctx := t.Context()
	if _, _, err := resolveRepo(ctx, "corpus/manifest.json", "cobra", filepath.Join(t.TempDir(), "absent")); err == nil || !strings.Contains(err.Error(), "never clones") {
		t.Errorf("absent checkout: err = %v", err)
	}
	wrong := t.TempDir()
	if _, _, err := resolveRepo(ctx, "corpus/manifest.json", "cobra", wrong); err == nil {
		t.Error("a directory that is not a git checkout must be refused")
	}
	root, sha, err := resolveRepo(ctx, "corpus/manifest.json", "tier1-fixture-hero-go", "")
	if err != nil || sha != "" || !strings.HasSuffix(filepath.ToSlash(root), "corpus/fixtures/hero-go") {
		t.Errorf("local-path entry = %q, %q, %v", root, sha, err)
	}
	root, sha, err = resolveRepo(ctx, "corpus/manifest.json", FixtureRepoName, "")
	if err != nil || sha != "" || root != filepath.FromSlash(fixtureRepoPath) {
		t.Errorf("fixture = %q, %q, %v", root, sha, err)
	}
}
