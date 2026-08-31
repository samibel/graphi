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
	if report.Reproducible.Repo.Name != FixtureRepoName || report.Reproducible.RunnerClass != "test" || len(report.Reproducible.Baselines) != 7 {
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
		ib, _ := os.ReadFile(filepath.Join(nudged, retrieval.RunIndexFile))
		var idx retrieval.RunIndex
		_ = json.Unmarshal(ib, &idx)
		p := filepath.Join(nudged, idx.Report)
		rb, _ := os.ReadFile(p)
		var r retrieval.Report
		_ = json.Unmarshal(rb, &r)
		r.Reproducible.Baselines[0].Overall.Metrics[retrieval.MetricRecall10] += 0.01
		nb, _ := retrieval.MarshalReport(&r)
		_ = os.WriteFile(p, nb, 0o644)
		idx.ReportSHA256 = retrieval.SHA256Hex(nb)
		ob, _ := json.MarshalIndent(idx, "", "  ")
		_ = os.WriteFile(filepath.Join(nudged, retrieval.RunIndexFile), ob, 0o644)

		var w bytes.Buffer
		if code := run([]string{"-aggregate", nudged}, &bytes.Buffer{}, &w); code != retrieval.ExitDiscrepancy {
			t.Errorf("aggregate exit %d, want %d\n%s", code, retrieval.ExitDiscrepancy, w.String())
		}
	})

	t.Run("aggregate exits 1 on a query removed coherently from dataset, report and raw", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "raw")
		if code := run([]string{"-manifest", "corpus/manifest.json", "-repo", FixtureRepoName, "-dataset", fixtureDataset,
			"-out", filepath.Join(filepath.Dir(dir), "r.json"), "-export-raw", dir, "-repeats", "1"}, &bytes.Buffer{}, &bytes.Buffer{}); code != exitOK {
			t.Fatal("export run failed")
		}
		removeQueryEverywhere(t, dir, "fx-10")
		var w bytes.Buffer
		if code := run([]string{"-aggregate", dir}, &bytes.Buffer{}, &w); code != retrieval.ExitDiscrepancy {
			t.Errorf("aggregate exit %d, want %d\n%s", code, retrieval.ExitDiscrepancy, w.String())
		}
		if !strings.Contains(w.String(), "run dataset: published") {
			t.Errorf("the dataset citation must be what catches a coordinated removal:\n%s", w.String())
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

// readJSON / writeJSON are the run-directory file helpers the tamper below
// needs; every write goes through the same stable encoder the harness uses.
func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t *testing.T, path string, v any) []byte {
	t.Helper()
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, '\n')
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return out
}

// removeQueryEverywhere is the coordinated structural tamper through the
// public API: one query dropped from the dataset copy, every raw series and
// every ok baseline, every statistic and performance block recomputed with
// the production functions, every digest re-stamped in run.json — so that
// only the dataset citation the report still carries can catch it.
func removeQueryEverywhere(t *testing.T, dir, id string) {
	t.Helper()
	var idx retrieval.RunIndex
	readJSON(t, filepath.Join(dir, retrieval.RunIndexFile), &idx)

	var ds retrieval.Dataset
	readJSON(t, filepath.Join(dir, idx.Dataset), &ds)
	kept := ds.Queries[:0]
	for _, q := range ds.Queries {
		if q.ID != id {
			kept = append(kept, q)
		}
	}
	ds.Queries = kept
	idx.DatasetSHA256 = retrieval.SHA256Hex(writeJSON(t, filepath.Join(dir, idx.Dataset), &ds))

	latency := map[retrieval.Baseline]retrieval.RawLatencySet{}
	for i := range idx.Raw {
		ref := &idx.Raw[i]
		p := filepath.Join(dir, filepath.FromSlash(ref.File))
		switch ref.Series {
		case retrieval.RawSeriesHits:
			var set retrieval.RawHitSet
			readJSON(t, p, &set)
			set.Samples = 0
			qs := set.Queries[:0]
			for _, q := range set.Queries {
				if q.ID != id {
					qs = append(qs, q)
					set.Samples += len(q.Hits)
				}
			}
			set.Queries = qs
			ref.Digest, ref.Samples = retrieval.SHA256Hex(writeJSON(t, p, &set)), set.Samples
		case retrieval.RawSeriesLatency:
			var set retrieval.RawLatencySet
			readJSON(t, p, &set)
			set.Samples = 0
			qs := set.Queries[:0]
			for _, q := range set.Queries {
				if q.ID != id {
					qs = append(qs, q)
					set.Samples += len(q.SamplesUS)
				}
			}
			set.Queries = qs
			ref.Digest, ref.Samples = retrieval.SHA256Hex(writeJSON(t, p, &set)), set.Samples
			latency[ref.Baseline] = set
		}
	}

	var r retrieval.Report
	readJSON(t, filepath.Join(dir, idx.Report), &r)
	for i := range r.Reproducible.Baselines {
		b := &r.Reproducible.Baselines[i]
		if b.Status != retrieval.BaselineStatusOK {
			continue
		}
		qs := b.Queries[:0]
		for _, q := range b.Queries {
			if q.ID != id {
				qs = append(qs, q)
			}
		}
		b.Queries = qs
		b.Overall, b.Strata, b.Splits = retrieval.AggregateAll(b.Queries, r.Reproducible.TokenBudgets)
	}
	for i := range r.Performance {
		r.Performance[i] = retrieval.PerformanceFromRaw(r.Performance[i].Baseline, latency[r.Performance[i].Baseline])
	}
	rb, err := retrieval.MarshalReport(&r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, idx.Report), rb, 0o644); err != nil {
		t.Fatal(err)
	}
	idx.ReportSHA256 = retrieval.SHA256Hex(rb)
	writeJSON(t, filepath.Join(dir, retrieval.RunIndexFile), &idx)
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
