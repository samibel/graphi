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
	names := map[string]map[Baseline]bool{}
	for _, m := range agg.Metrics {
		if names[m.Metric] == nil {
			names[m.Metric] = map[Baseline]bool{}
		}
		names[m.Metric][m.Baseline] = true
	}
	for _, want := range []string{"status", "query_set", "raw.hits.query_set", "raw.latency.query_set",
		"query.fx-01.hits", "query.fx-01.metrics", "overall", "strata.nl_behaviour", "splits.holdout",
		"query_p50_us", "query_p95_us", "index_ms", "peak_rss_mb", "vector_sidecar_bytes", "latency_samples"} {
		if !names[want][BaselineLexical] {
			t.Errorf("metric %s was not checked for lexical", want)
		}
	}
	// The unavailable baseline's whole shape is checked against BOTH raw
	// records, not skipped.
	for _, want := range []string{"status", "reason", "raw.latency.reason", "query_set", "raw.hits.query_set", "raw.hits.samples",
		"raw.latency.query_set", "raw.latency.samples", "overall", "strata.no_hit", "splits.dev",
		"index_ms", "query_p50_us", "query_p95_us", "peak_rss_mb", "vector_sidecar_bytes", "latency_samples"} {
		if !names[want][BaselineSemanticNameOnly] {
			t.Errorf("metric %s was not checked for the unavailable semantic_name_only baseline", want)
		}
	}
	// The closed-world checks: dataset citation and the baseline universe on
	// every side.
	for _, want := range []string{"dataset", "baseline_set", "performance_set", "raw.hits.series_set", "raw.latency.series_set"} {
		if !names[want][runLevel] {
			t.Errorf("run-level check %s was not made", want)
		}
	}
	if got := run.Report.Reproducible.Dataset; len(got.QueryIDs) != fixtureQueries || got.SHA256 != run.Dataset.SHA256 {
		t.Errorf("dataset citation = %+v, want %d sorted query ids and the recomputed sha256 %s", got, fixtureQueries, run.Dataset.SHA256)
	}
}

// restampIndex edits run.json so a rewritten artifact is read back rather
// than refused by digest: the ARITHMETIC, not the checksum, must catch it.
func restampIndex(t *testing.T, dir string, mut func(*RunIndex)) {
	t.Helper()
	idxRaw, err := os.ReadFile(filepath.Join(dir, RunIndexFile))
	if err != nil {
		t.Fatal(err)
	}
	var idx RunIndex
	if err := json.Unmarshal(idxRaw, &idx); err != nil {
		t.Fatal(err)
	}
	mut(&idx)
	idxOut, err := marshalStable(idx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, RunIndexFile), idxOut, 0o644); err != nil {
		t.Fatal(err)
	}
}

// rewriteReport edits report.json in place and re-stamps its digest.
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
	restampIndex(t, dir, func(idx *RunIndex) { idx.ReportSHA256 = SHA256Hex(out) })
}

// rewriteDataset edits the dataset copy in place and re-stamps its digest.
func rewriteDataset(t *testing.T, dir string, mut func(*Dataset)) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, DatasetFile))
	if err != nil {
		t.Fatal(err)
	}
	var d Dataset
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	mut(&d)
	out, err := marshalStable(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DatasetFile), out, 0o644); err != nil {
		t.Fatal(err)
	}
	restampIndex(t, dir, func(idx *RunIndex) { idx.DatasetSHA256 = SHA256Hex(out) })
}

// rewriteRawHits / rewriteRawLatency edit one raw series file in place and
// re-stamp its digest, sample count and collected flag in the run index.
func rewriteRawHits(t *testing.T, dir string, b Baseline, mut func(*RawHitSet)) {
	t.Helper()
	name := RawFileName(RawSeriesHits, b)
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	var set RawHitSet
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatal(err)
	}
	mut(&set)
	out, err := marshalStable(set)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), out, 0o644); err != nil {
		t.Fatal(err)
	}
	restampRaw(t, dir, name, SHA256Hex(out), set.Collected, set.Samples)
}

func rewriteRawLatency(t *testing.T, dir string, b Baseline, mut func(*RawLatencySet)) {
	t.Helper()
	name := RawFileName(RawSeriesLatency, b)
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	var set RawLatencySet
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatal(err)
	}
	mut(&set)
	out, err := marshalStable(set)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), out, 0o644); err != nil {
		t.Fatal(err)
	}
	restampRaw(t, dir, name, SHA256Hex(out), set.Collected, set.Samples)
}

func restampRaw(t *testing.T, dir, file, digest string, collected bool, samples int) {
	t.Helper()
	restampIndex(t, dir, func(idx *RunIndex) {
		for i := range idx.Raw {
			if idx.Raw[i].File == file {
				idx.Raw[i].Digest, idx.Raw[i].Collected, idx.Raw[i].Samples = digest, collected, samples
			}
		}
	})
}

// readRawLatency reads one raw latency series back from the run directory.
func readRawLatency(t *testing.T, dir string, b Baseline) RawLatencySet {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(RawFileName(RawSeriesLatency, b))))
	if err != nil {
		t.Fatal(err)
	}
	var set RawLatencySet
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatal(err)
	}
	return set
}

// removeQueryEverywhere is the coordinated tamper: one query dropped from
// the dataset copy, from every raw series and from every ok baseline of the
// report, with every statistic and performance block recomputed through the
// production functions and every digest re-stamped. Only the dataset
// citation the report still carries can catch it.
func removeQueryEverywhere(t *testing.T, dir, id string) {
	t.Helper()
	rewriteDataset(t, dir, func(d *Dataset) {
		kept := d.Queries[:0]
		for _, q := range d.Queries {
			if q.ID != id {
				kept = append(kept, q)
			}
		}
		d.Queries = kept
	})
	for _, b := range AllBaselines {
		rewriteRawHits(t, dir, b, func(s *RawHitSet) {
			kept := s.Queries[:0]
			s.Samples = 0
			for _, q := range s.Queries {
				if q.ID != id {
					kept = append(kept, q)
					s.Samples += len(q.Hits)
				}
			}
			s.Queries = kept
		})
		rewriteRawLatency(t, dir, b, func(s *RawLatencySet) {
			kept := s.Queries[:0]
			s.Samples = 0
			for _, q := range s.Queries {
				if q.ID != id {
					kept = append(kept, q)
					s.Samples += len(q.SamplesUS)
				}
			}
			s.Queries = kept
		})
	}
	rewriteReport(t, dir, func(r *Report) {
		for i := range r.Reproducible.Baselines {
			b := &r.Reproducible.Baselines[i]
			if b.Status != BaselineStatusOK {
				continue
			}
			kept := b.Queries[:0]
			for _, q := range b.Queries {
				if q.ID != id {
					kept = append(kept, q)
				}
			}
			b.Queries = kept
			b.Overall, b.Strata, b.Splits = AggregateAll(b.Queries, r.Reproducible.TokenBudgets)
		}
		for i := range r.Performance {
			r.Performance[i] = PerformanceFromRaw(r.Performance[i].Baseline, readRawLatency(t, dir, r.Performance[i].Baseline))
		}
	})
}

func findBaseline(t *testing.T, r *Report, name Baseline) *BaselineResult {
	t.Helper()
	for i := range r.Reproducible.Baselines {
		if r.Reproducible.Baselines[i].Name == name {
			return &r.Reproducible.Baselines[i]
		}
	}
	t.Fatalf("baseline %s not in report", name)
	return nil
}

func findPerformance(t *testing.T, r *Report, name Baseline) *BaselinePerformance {
	t.Helper()
	for i := range r.Performance {
		if r.Performance[i].Baseline == name {
			return &r.Performance[i]
		}
	}
	t.Fatalf("performance for %s not in report", name)
	return nil
}

// Every way a run directory can drift from its raw samples, in one table:
// report edits go through rewriteReport (digest re-stamped) so the arithmetic
// is what catches them; raw edits are refused by digest; absences are
// INCOMPLETE, deliberately not discrepancies.
func TestAggregate_DetectsDrift(t *testing.T) {
	cases := []struct {
		name   string
		tamper func(t *testing.T, dir string)
		// wantReadErr: ReadRunDir itself must refuse the directory with this text.
		wantReadErr string
		wantExit    int
		// wantDiscrepancies: every substring must appear in the discrepancy list.
		wantDiscrepancies []string
		check             func(t *testing.T, agg AggregateReport)
	}{
		{
			name: "a nudged aggregate is a discrepancy, exit 1",
			tamper: func(t *testing.T, dir string) {
				rewriteReport(t, dir, func(r *Report) {
					findBaseline(t, r, BaselineLexical).Overall.Metrics[MetricNDCG10] += 0.001
				})
			},
			wantExit:          ExitDiscrepancy,
			wantDiscrepancies: []string{"lexical overall"},
			check: func(t *testing.T, agg AggregateReport) {
				if agg.Discrepant != 1 || len(agg.Discrepancies) != 1 {
					t.Errorf("discrepancies = %v, want exactly the nudged aggregate", agg.Discrepancies)
				}
			},
		},
		{
			name: "a nudged per-query metric is a discrepancy",
			tamper: func(t *testing.T, dir string) {
				rewriteReport(t, dir, func(r *Report) {
					findBaseline(t, r, BaselineLexical).Queries[0].Metrics.MRR10 = 0.5
				})
			},
			wantExit:          ExitDiscrepancy,
			wantDiscrepancies: []string{"lexical query.fx-01.metrics"},
		},
		{
			name: "a report hit list that differs from the raw hits is a discrepancy",
			tamper: func(t *testing.T, dir string) {
				rewriteReport(t, dir, func(r *Report) {
					q := &findBaseline(t, r, BaselineLexical).Queries[0]
					q.Hits = q.Hits[:len(q.Hits)-1]
				})
			},
			wantExit:          ExitDiscrepancy,
			wantDiscrepancies: []string{"lexical query.fx-01.hits"},
		},
		{
			name: "a nudged latency percentile is a discrepancy",
			tamper: func(t *testing.T, dir string) {
				rewriteReport(t, dir, func(r *Report) {
					p := findPerformance(t, r, BaselineLexical)
					v := *p.QueryP95US.Value + 1
					p.QueryP95US.Value = &v
				})
			},
			wantExit:          ExitDiscrepancy,
			wantDiscrepancies: []string{"lexical query_p95_us"},
		},
		{
			name: "a changed vector-sidecar measure is a discrepancy",
			tamper: func(t *testing.T, dir string) {
				rewriteReport(t, dir, func(r *Report) {
					findPerformance(t, r, BaselineLexical).VectorSidecarBytes = Measured(4096, "bytes")
				})
			},
			wantExit:          ExitDiscrepancy,
			wantDiscrepancies: []string{"lexical vector_sidecar_bytes"},
		},
		{
			name: "an untaken measure whose status or reason changes is a discrepancy",
			tamper: func(t *testing.T, dir string) {
				rewriteReport(t, dir, func(r *Report) {
					findPerformance(t, r, BaselineHybridV1).IndexMS = Unknown("not timed")
					findPerformance(t, r, BaselineOracle).VectorSidecarBytes = NotApplicable("edited reason")
				})
			},
			wantExit:          ExitDiscrepancy,
			wantDiscrepancies: []string{"hybrid_v1 index_ms", "oracle_upper_bound vector_sidecar_bytes"},
		},
		{
			name: "an unavailable baseline republished as ok with zero scores is a discrepancy (AC-6)",
			tamper: func(t *testing.T, dir string) {
				rewriteReport(t, dir, func(r *Report) {
					b := findBaseline(t, r, BaselineSemanticNameOnly)
					b.Status, b.Reason = BaselineStatusOK, ""
					b.Overall = AggregateMetrics{Queries: fixtureQueries, Scored: fixtureQueries - 2, Status: StatusMeasured,
						Metrics: map[string]float64{MetricNDCG10: 0, MetricRecall10: 0, MetricMRR10: 0, MetricTop1: 0}}
					p := findPerformance(t, r, BaselineSemanticNameOnly)
					p.IndexMS, p.QueryP50US, p.QueryP95US = Measured(0, "ms"), Measured(0, "us"), Measured(0, "us")
					p.PeakRSSMB, p.VectorSidecarBytes = Measured(0, "MB"), Measured(0, "bytes")
				})
			},
			wantExit: ExitDiscrepancy,
			wantDiscrepancies: []string{"semantic_name_only status", "semantic_name_only query_set",
				"semantic_name_only index_ms", "semantic_name_only query_p95_us", "semantic_name_only peak_rss_mb", "semantic_name_only vector_sidecar_bytes"},
		},
		{
			name: "an unavailable baseline whose UNKNOWN figures read zero is a discrepancy (AC-6)",
			tamper: func(t *testing.T, dir string) {
				rewriteReport(t, dir, func(r *Report) {
					b := findBaseline(t, r, BaselineSemanticNameOnly)
					b.Overall = AggregateMetrics{Queries: 0, Scored: 0, Status: StatusMeasured, Metrics: map[string]float64{MetricNDCG10: 0}}
					b.Strata[StratumNLBehaviour] = b.Overall
					p := findPerformance(t, r, BaselineSemanticNameOnly)
					p.IndexMS, p.QueryP50US, p.QueryP95US = Measured(0, "ms"), Measured(0, "us"), Measured(0, "us")
					p.PeakRSSMB, p.VectorSidecarBytes = Measured(0, "MB"), Measured(0, "bytes")
				})
			},
			wantExit: ExitDiscrepancy,
			wantDiscrepancies: []string{"semantic_name_only overall", "semantic_name_only strata.nl_behaviour", "semantic_name_only index_ms",
				"semantic_name_only query_p50_us", "semantic_name_only query_p95_us", "semantic_name_only peak_rss_mb", "semantic_name_only vector_sidecar_bytes"},
		},
		{
			name: "an unavailable baseline's reason that differs from the raw record is a discrepancy",
			tamper: func(t *testing.T, dir string) {
				rewriteReport(t, dir, func(r *Report) {
					findBaseline(t, r, BaselineSemanticNameOnly).Reason = "embedder disabled by policy"
				})
			},
			wantExit:          ExitDiscrepancy,
			wantDiscrepancies: []string{"semantic_name_only reason"},
		},
		{
			name: "an unavailable baseline that grows queries is a discrepancy",
			tamper: func(t *testing.T, dir string) {
				rewriteReport(t, dir, func(r *Report) {
					b := findBaseline(t, r, BaselineSemanticNameOnly)
					b.Queries = append(b.Queries, findBaseline(t, r, BaselineLexical).Queries[0])
				})
			},
			wantExit:          ExitDiscrepancy,
			wantDiscrepancies: []string{"semantic_name_only query_set"},
		},
		{
			name: "a baseline republished as unavailable while its raw records carry samples is a discrepancy",
			tamper: func(t *testing.T, dir string) {
				rewriteReport(t, dir, func(r *Report) {
					b := findBaseline(t, r, BaselineHybridV1)
					*b = unavailableBaseline(b.Name, b.Method, "no embedder configured")
					*findPerformance(t, r, BaselineHybridV1) = unavailablePerformance(BaselineHybridV1, "no embedder configured")
				})
			},
			wantExit: ExitDiscrepancy,
			wantDiscrepancies: []string{"hybrid_v1 status", "hybrid_v1 reason", "hybrid_v1 raw.hits.query_set", "hybrid_v1 raw.hits.samples",
				"hybrid_v1 index_ms", "hybrid_v1 query_p95_us", "hybrid_v1 peak_rss_mb", "hybrid_v1 vector_sidecar_bytes", "hybrid_v1 latency_samples"},
		},
		{
			name: "a query omitted from the report with the aggregates recomputed is a discrepancy",
			tamper: func(t *testing.T, dir string) {
				rewriteReport(t, dir, func(r *Report) {
					b := findBaseline(t, r, BaselineLexical)
					kept := make([]QueryResult, 0, len(b.Queries)-1)
					for _, q := range b.Queries {
						if q.ID != "fx-03" {
							kept = append(kept, q)
						}
					}
					b.Queries = kept
					b.Overall, b.Strata, b.Splits = AggregateAll(b.Queries, r.Reproducible.TokenBudgets)
				})
			},
			wantExit:          ExitDiscrepancy,
			wantDiscrepancies: []string{"lexical query_set", "lexical overall", "lexical strata.nl_behaviour"},
		},
		{
			name: "a query added to the report is a discrepancy",
			tamper: func(t *testing.T, dir string) {
				rewriteReport(t, dir, func(r *Report) {
					b := findBaseline(t, r, BaselineLexical)
					extra := b.Queries[0]
					extra.ID = "fx-99"
					b.Queries = append(b.Queries, extra)
					b.Overall, b.Strata, b.Splits = AggregateAll(b.Queries, r.Reproducible.TokenBudgets)
				})
			},
			wantExit:          ExitDiscrepancy,
			wantDiscrepancies: []string{"lexical query_set"},
		},
		{
			name: "a query dropped from the dataset copy is a discrepancy on every side",
			tamper: func(t *testing.T, dir string) {
				rewriteDataset(t, dir, func(d *Dataset) { d.Queries = d.Queries[:len(d.Queries)-1] })
			},
			wantExit:          ExitDiscrepancy,
			wantDiscrepancies: []string{"lexical query_set", "lexical raw.hits.query_set", "lexical raw.latency.query_set", "oracle_upper_bound query_set"},
		},
		{
			name: "a query removed coherently from dataset, report and every raw series (statistics recomputed) is caught by the dataset citation",
			tamper: func(t *testing.T, dir string) {
				removeQueryEverywhere(t, dir, "fx-10")
			},
			wantExit:          ExitDiscrepancy,
			wantDiscrepancies: []string{"run dataset"},
			check: func(t *testing.T, agg AggregateReport) {
				if agg.Discrepant != 1 {
					t.Errorf("discrepancies = %v, want exactly the dataset citation (everything else was made consistent)", agg.Discrepancies)
				}
			},
		},
		{
			name: "a baseline removed from the report, its performance block and the raw index is a discrepancy on every side",
			tamper: func(t *testing.T, dir string) {
				rewriteReport(t, dir, func(r *Report) {
					var bs []BaselineResult
					for _, b := range r.Reproducible.Baselines {
						if b.Name != BaselineHybridV1 {
							bs = append(bs, b)
						}
					}
					r.Reproducible.Baselines = bs
					var ps []BaselinePerformance
					for _, p := range r.Performance {
						if p.Baseline != BaselineHybridV1 {
							ps = append(ps, p)
						}
					}
					r.Performance = ps
				})
				restampIndex(t, dir, func(idx *RunIndex) {
					var refs []RawFileRef
					for _, ref := range idx.Raw {
						if ref.Baseline != BaselineHybridV1 {
							refs = append(refs, ref)
						}
					}
					idx.Raw = refs
				})
				for _, series := range []string{RawSeriesHits, RawSeriesLatency} {
					if err := os.Remove(filepath.Join(dir, filepath.FromSlash(RawFileName(series, BaselineHybridV1)))); err != nil {
						t.Fatal(err)
					}
				}
			},
			wantExit:          ExitDiscrepancy,
			wantDiscrepancies: []string{"run baseline_set", "run performance_set", "run raw.hits.series_set", "run raw.latency.series_set"},
		},
		{
			name: "an extra performance block is a discrepancy",
			tamper: func(t *testing.T, dir string) {
				rewriteReport(t, dir, func(r *Report) {
					r.Performance = append(r.Performance, unavailablePerformance("bm42", "never ran"))
				})
			},
			wantExit:          ExitDiscrepancy,
			wantDiscrepancies: []string{"run performance_set"},
		},
		{
			name: "an unavailable latency record that carries samples is a discrepancy",
			tamper: func(t *testing.T, dir string) {
				rewriteRawLatency(t, dir, BaselineSemanticNameOnly, func(s *RawLatencySet) {
					s.Queries = []RawQueryLatency{{ID: "fx-01", SamplesUS: []int64{1, 2, 3}}}
					s.Samples = 3
				})
			},
			wantExit:          ExitDiscrepancy,
			wantDiscrepancies: []string{"semantic_name_only raw.latency.query_set", "semantic_name_only raw.latency.samples", "semantic_name_only latency_samples"},
		},
		{
			name: "an unavailable latency reason that differs from the hit record's (performance reasons edited to match) is a discrepancy",
			tamper: func(t *testing.T, dir string) {
				const other = "embedder disabled by policy"
				rewriteRawLatency(t, dir, BaselineSemanticNameOnly, func(s *RawLatencySet) {
					s.Reason = other
					u := Unknown("baseline unavailable: " + other)
					s.IndexMS, s.PeakRSSMB, s.VectorSidecarBytes = u, u, u
				})
				rewriteReport(t, dir, func(r *Report) {
					*findPerformance(t, r, BaselineSemanticNameOnly) = unavailablePerformance(BaselineSemanticNameOnly, other)
				})
			},
			wantExit:          ExitDiscrepancy,
			wantDiscrepancies: []string{"semantic_name_only raw.latency.reason"},
			check: func(t *testing.T, agg AggregateReport) {
				if agg.Discrepant != 1 {
					t.Errorf("discrepancies = %v, want exactly the reason parity (the performance block was made consistent with the edited record)", agg.Discrepancies)
				}
			},
		},
		{
			name: "an edited raw file is refused by digest before any arithmetic",
			tamper: func(t *testing.T, dir string) {
				p := filepath.Join(dir, filepath.FromSlash(RawFileName(RawSeriesHits, BaselineLexical)))
				raw, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, append(raw, '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantReadErr: "digest",
		},
		{
			name: "a run index from another harness version is refused",
			tamper: func(t *testing.T, dir string) {
				restampIndex(t, dir, func(idx *RunIndex) { idx.HarnessVersion = "retrieval-eval/0" })
			},
			wantReadErr: "retrieval-eval/0",
		},
		{
			name: "a missing raw file is INCOMPLETE (exit 3), deliberately not a discrepancy",
			tamper: func(t *testing.T, dir string) {
				if err := os.Remove(filepath.Join(dir, filepath.FromSlash(RawFileName(RawSeriesHits, BaselineHybridV1)))); err != nil {
					t.Fatal(err)
				}
			},
			wantExit: ExitIncomplete,
			check: func(t *testing.T, agg AggregateReport) {
				if agg.Discrepant != 0 || agg.Unknown == 0 || agg.Publishable || len(agg.MissingRaw) != 1 {
					t.Errorf("aggregate = discrepant %d unknown %d publishable %v missing raw %v", agg.Discrepant, agg.Unknown, agg.Publishable, agg.MissingRaw)
				}
			},
		},
		{
			name: "an undocumented environment blocks publication without being a discrepancy",
			tamper: func(t *testing.T, dir string) {
				rewriteReport(t, dir, func(r *Report) { r.Environment.GoVersion = "" })
			},
			wantExit: ExitIncomplete,
			check: func(t *testing.T, agg AggregateReport) {
				if agg.Discrepant != 0 || agg.Publishable || agg.EnvironmentComplete {
					t.Errorf("aggregate = %+v", agg)
				}
				if len(agg.MissingEnvironment) != 1 || agg.MissingEnvironment[0] != "go_version" {
					t.Errorf("missing environment = %v", agg.MissingEnvironment)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, _, _ := exportFixtureRun(t)
			tc.tamper(t, dir)
			run, err := ReadRunDir(dir)
			if tc.wantReadErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantReadErr) {
					t.Fatalf("ReadRunDir = %v, want a refusal mentioning %q", err, tc.wantReadErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ReadRunDir: %v", err)
			}
			agg := Reproduce(run)
			if agg.ExitCode() != tc.wantExit {
				t.Errorf("exit %d, want %d (status %s, discrepancies %v)", agg.ExitCode(), tc.wantExit, agg.Status, agg.Discrepancies)
			}
			if tc.wantExit == ExitDiscrepancy && (agg.Status != CheckDiscrepant || agg.Publishable) {
				t.Errorf("status %s publishable %v, want DISCREPANCY and unpublishable", agg.Status, agg.Publishable)
			}
			joined := strings.Join(agg.Discrepancies, "\n")
			for _, want := range tc.wantDiscrepancies {
				if !strings.Contains(joined, want) {
					t.Errorf("discrepancies do not mention %q:\n%s", want, joined)
				}
			}
			if tc.check != nil {
				tc.check(t, agg)
			}
		})
	}
}

func TestWriteRunDir_RefusesAForeignDataset(t *testing.T) {
	res := runFixture(t, BaselineOracle)
	other := &Loaded{SHA256: "0000", Raw: []byte("{}")}
	if _, err := WriteRunDir(t.TempDir(), res, other, "2026-08-30"); err == nil {
		t.Error("WriteRunDir with a dataset the report was not scored against must fail")
	}
}
