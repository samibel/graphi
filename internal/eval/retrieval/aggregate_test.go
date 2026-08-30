package retrieval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func exportFixtureRun(t *testing.T) (dir string, res *Result, ds *Loaded) {
	t.Helper()
	ds, err := LoadDataset(fixtureDataset)
	if err != nil {
		t.Fatal(err)
	}
	res = runFixture(t)
	dir = filepath.Join(t.TempDir(), "run")
	if _, err := WriteRunDir(dir, res, ds, "2026-08-30"); err != nil {
		t.Fatalf("WriteRunDir: %v", err)
	}
	return dir, res, ds
}

// AC-5: a -aggregate path recomputes every published statistic from the raw
// samples, and a report that has drifted from its samples is a discrepancy.
func TestAggregate_RoundTripReproducesEveryPublishedNumber(t *testing.T) {
	dir, _, _ := exportFixtureRun(t)

	for _, f := range []string{RunIndexFile, ReportFile, DatasetFile,
		RawFileName(RawSeriesHits, BaselineLexical), RawFileName(RawSeriesLatency, BaselineOracle)} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			t.Errorf("run dir lacks %s: %v", f, err)
		}
	}

	run, err := ReadRunDir(dir)
	if err != nil {
		t.Fatalf("ReadRunDir: %v", err)
	}
	agg := Reproduce(run)
	if agg.Discrepant != 0 {
		t.Fatalf("discrepancies on an untouched run: %v", agg.Discrepancies)
	}
	if agg.Unknown != 0 || !agg.Complete || !agg.Publishable || agg.Status != "PASS" || agg.ExitCode() != ExitReproduced {
		t.Errorf("aggregate = unknown %d complete %v publishable %v status %s exit %d", agg.Unknown, agg.Complete, agg.Publishable, agg.Status, agg.ExitCode())
	}
	if agg.Checked < 8*3*2 {
		t.Errorf("only %d metrics checked; expected every query's hits and metrics for every ok baseline plus the aggregates", agg.Checked)
	}
	names := map[string]bool{}
	for _, m := range agg.Metrics {
		names[m.Metric] = true
	}
	for _, want := range []string{"query.fx-01.hits", "query.fx-01.metrics", "overall", "strata.nl_behaviour", "splits.holdout", "query_p50_us", "query_p95_us", "index_ms", "peak_rss_mb", "latency_samples"} {
		if !names[want] {
			t.Errorf("metric %s was not checked", want)
		}
	}
}

// rewriteReport edits report.json in place and re-stamps the run index digest
// so the read succeeds and the ARITHMETIC (not the checksum) catches it.
func rewriteReport(t *testing.T, dir string, mut func(*Report)) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ReportFile))
	if err != nil {
		t.Fatal(err)
	}
	var r Report
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	mut(&r)
	out, err := MarshalReport(&r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ReportFile), out, 0o644); err != nil {
		t.Fatal(err)
	}
	idxRaw, err := os.ReadFile(filepath.Join(dir, RunIndexFile))
	if err != nil {
		t.Fatal(err)
	}
	var idx RunIndex
	if err := json.Unmarshal(idxRaw, &idx); err != nil {
		t.Fatal(err)
	}
	idx.ReportSHA256 = SHA256Hex(out)
	idxOut, _ := marshalStable(idx)
	if err := os.WriteFile(filepath.Join(dir, RunIndexFile), idxOut, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAggregate_DetectsDrift(t *testing.T) {
	t.Run("a nudged aggregate is a discrepancy, exit 1", func(t *testing.T) {
		dir, _, _ := exportFixtureRun(t)
		rewriteReport(t, dir, func(r *Report) {
			r.Reproducible.Baselines[0].Overall.Metrics[MetricNDCG10] += 0.001
		})
		run, err := ReadRunDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		agg := Reproduce(run)
		if agg.Discrepant != 1 || agg.ExitCode() != ExitDiscrepancy || agg.Status != CheckDiscrepant {
			t.Errorf("aggregate = %+v", agg)
		}
		if len(agg.Discrepancies) != 1 || !strings.Contains(agg.Discrepancies[0], "lexical overall") {
			t.Errorf("discrepancies = %v", agg.Discrepancies)
		}
	})

	t.Run("a nudged per-query metric is a discrepancy", func(t *testing.T) {
		dir, _, _ := exportFixtureRun(t)
		rewriteReport(t, dir, func(r *Report) {
			r.Reproducible.Baselines[0].Queries[0].Metrics.MRR10 = 0.5
		})
		run, err := ReadRunDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if agg := Reproduce(run); agg.Discrepant == 0 || !strings.Contains(strings.Join(agg.Discrepancies, "\n"), "query.fx-01.metrics") {
			t.Errorf("aggregate = %+v", agg.Discrepancies)
		}
	})

	t.Run("a report hit list that differs from the raw hits is a discrepancy", func(t *testing.T) {
		dir, _, _ := exportFixtureRun(t)
		rewriteReport(t, dir, func(r *Report) {
			q := &r.Reproducible.Baselines[0].Queries[0]
			q.Hits = q.Hits[:len(q.Hits)-1]
		})
		run, err := ReadRunDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if agg := Reproduce(run); agg.Discrepant == 0 || !strings.Contains(strings.Join(agg.Discrepancies, "\n"), "query.fx-01.hits") {
			t.Errorf("aggregate = %+v", agg.Discrepancies)
		}
	})

	t.Run("a nudged latency percentile is a discrepancy", func(t *testing.T) {
		dir, _, _ := exportFixtureRun(t)
		rewriteReport(t, dir, func(r *Report) {
			v := *r.Performance[0].QueryP95US.Value + 1
			r.Performance[0].QueryP95US.Value = &v
		})
		run, err := ReadRunDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if agg := Reproduce(run); agg.Discrepant == 0 || !strings.Contains(strings.Join(agg.Discrepancies, "\n"), "query_p95_us") {
			t.Errorf("aggregate = %+v", agg.Discrepancies)
		}
	})

	t.Run("an edited raw file is refused by digest before any arithmetic", func(t *testing.T) {
		dir, _, _ := exportFixtureRun(t)
		p := filepath.Join(dir, filepath.FromSlash(RawFileName(RawSeriesHits, BaselineLexical)))
		raw, _ := os.ReadFile(p)
		if err := os.WriteFile(p, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadRunDir(dir); err == nil || !strings.Contains(err.Error(), "digest") {
			t.Errorf("ReadRunDir over edited raw = %v", err)
		}
	})

	t.Run("a missing raw file is INCOMPLETE (exit 3), deliberately not a discrepancy", func(t *testing.T) {
		dir, _, _ := exportFixtureRun(t)
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(RawFileName(RawSeriesHits, BaselineHybridV1)))); err != nil {
			t.Fatal(err)
		}
		run, err := ReadRunDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		agg := Reproduce(run)
		if agg.Discrepant != 0 || agg.Unknown == 0 || agg.Publishable || agg.ExitCode() != ExitIncomplete {
			t.Errorf("aggregate = discrepant %d unknown %d publishable %v exit %d", agg.Discrepant, agg.Unknown, agg.Publishable, agg.ExitCode())
		}
		if len(agg.MissingRaw) != 1 {
			t.Errorf("missing raw = %v", agg.MissingRaw)
		}
	})

	t.Run("an undocumented environment blocks publication without being a discrepancy", func(t *testing.T) {
		dir, _, _ := exportFixtureRun(t)
		rewriteReport(t, dir, func(r *Report) { r.Environment.GoVersion = "" })
		run, err := ReadRunDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		agg := Reproduce(run)
		if agg.Discrepant != 0 || agg.Publishable || agg.EnvironmentComplete || agg.ExitCode() != ExitIncomplete {
			t.Errorf("aggregate = %+v", agg)
		}
		if len(agg.MissingEnvironment) != 1 || agg.MissingEnvironment[0] != "go_version" {
			t.Errorf("missing environment = %v", agg.MissingEnvironment)
		}
	})

	t.Run("a run index from another harness version is refused", func(t *testing.T) {
		dir, _, _ := exportFixtureRun(t)
		idxRaw, _ := os.ReadFile(filepath.Join(dir, RunIndexFile))
		var idx RunIndex
		_ = json.Unmarshal(idxRaw, &idx)
		idx.HarnessVersion = "retrieval-eval/0"
		out, _ := marshalStable(idx)
		if err := os.WriteFile(filepath.Join(dir, RunIndexFile), out, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadRunDir(dir); err == nil || !strings.Contains(err.Error(), "retrieval-eval/0") {
			t.Errorf("ReadRunDir = %v", err)
		}
	})
}

func TestWriteRunDir_RefusesAForeignDataset(t *testing.T) {
	res := runFixture(t, BaselineOracle)
	other := &Loaded{SHA256: "0000", Raw: []byte("{}")}
	if _, err := WriteRunDir(t.TempDir(), res, other, "2026-08-30"); err == nil {
		t.Error("WriteRunDir with a dataset the report was not scored against must fail")
	}
}
