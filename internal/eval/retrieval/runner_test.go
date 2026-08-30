package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samibel/graphi/engine/search"
)

const (
	fixtureRepo    = "testdata/fixture-repo"
	fixtureDataset = "testdata/datasets/fixture-v1.json"

	embedEnvSelector = "GRAPHI_EMBEDDER"

	// fixtureQueries is the size of fixture-v1.json (7 dev + 3 holdout).
	fixtureQueries = 10
)

func fixedNow() time.Time { return time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC) }

func runFixture(t *testing.T, baselines ...Baseline) *Result {
	t.Helper()
	ds, err := LoadDataset(fixtureDataset)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), Options{
		RepoRoot: fixtureRepo, RepoName: "fixture", Dataset: ds, Baselines: baselines,
		RunnerClass: "test", CandidateSHA: "test-sha", Repeats: 2, WorkDir: t.TempDir(), Now: fixedNow,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

func baseline(t *testing.T, r *Report, name Baseline) BaselineResult {
	t.Helper()
	for _, b := range r.Reproducible.Baselines {
		if b.Name == name {
			return b
		}
	}
	t.Fatalf("baseline %s not in report", name)
	return BaselineResult{}
}

func performance(t *testing.T, r *Report, name Baseline) BaselinePerformance {
	t.Helper()
	for _, p := range r.Performance {
		if p.Baseline == name {
			return p
		}
	}
	t.Fatalf("performance for %s not in report", name)
	return BaselinePerformance{}
}

// AC-3, AC-4, AC-6, AC-10: the four baselines over the hermetic fixture.
func TestRun_FixtureRepoAllBaselines(t *testing.T) {
	res := runFixture(t)
	r := res.Report

	if r.FormatVersion != FormatVersion || r.HarnessVersion != HarnessVersion || r.ScorerVersion != ScorerVersion {
		t.Errorf("versions = %d/%s/%s", r.FormatVersion, r.HarnessVersion, r.ScorerVersion)
	}
	rep := r.Reproducible
	if rep.CandidateSHA != "test-sha" || rep.RunnerClass != "test" || rep.TokenizerID != TokenizerID || len(rep.Dataset.SHA256) != 64 {
		t.Errorf("header = %+v", rep)
	}
	if rep.Dataset.Queries != fixtureQueries || rep.Dataset.Dev != 7 || rep.Dataset.Holdout != 3 {
		t.Errorf("dataset counts = %+v", rep.Dataset)
	}
	if rep.Repo.Nodes == 0 || rep.Repo.Files < 11 {
		t.Errorf("repo inventory = %+v, want the indexed fixture", rep.Repo)
	}
	if got := len(rep.Baselines); got != len(AllBaselines) {
		t.Fatalf("%d baselines, want %d", got, len(AllBaselines))
	}
	for i, b := range rep.Baselines {
		if b.Name != AllBaselines[i] {
			t.Errorf("baseline %d = %s, want %s (report order is fixed)", i, b.Name, AllBaselines[i])
		}
	}

	t.Run("lexical runs through engine/search and answers the exact identifier", func(t *testing.T) {
		lex := baseline(t, r, BaselineLexical)
		if lex.Status != BaselineStatusOK || len(lex.Queries) != fixtureQueries {
			t.Fatalf("lexical = %s with %d queries", lex.Status, len(lex.Queries))
		}
		q := lex.Queries[0]
		if q.ID != "fx-01" || len(q.Hits) == 0 || q.Hits[0].QualifiedName != "auth.ValidateToken" || q.Hits[0].Path != "auth/token.go" {
			t.Errorf("fx-01 top hit = %+v", q.Hits)
		}
		if q.Metrics.Top1 != 1 || q.Metrics.MRR10 != 1 {
			t.Errorf("fx-01 metrics = %+v", q.Metrics)
		}
		for _, q := range lex.Queries {
			if len(q.Hits) > TopK {
				t.Errorf("%s: %d hits, more than top_k", q.ID, len(q.Hits))
			}
			for _, h := range q.Hits {
				if h.Tokens <= 0 || h.Rank == 0 {
					t.Errorf("%s: hit without tokens/rank: %+v", q.ID, h)
				}
			}
		}
		if lex.Overall.Status != StatusMeasured || lex.Overall.Scored != fixtureQueries-2 {
			t.Errorf("lexical overall = %+v", lex.Overall)
		}
		for _, s := range Strata {
			if _, ok := lex.Strata[s]; !ok {
				t.Errorf("stratum %s missing from the aggregate", s)
			}
		}
		if lex.Strata[StratumNoHit].Scored != 0 || lex.Strata[StratumNoHit].Queries != 2 {
			t.Errorf("no_hit stratum = %+v", lex.Strata[StratumNoHit])
		}
		if lex.Splits[SplitHoldout].Queries != 3 || lex.Splits[SplitDev].Queries != 7 {
			t.Errorf("splits = %+v", lex.Splits)
		}
		if !strings.Contains(lex.Method, "engine/search") {
			t.Errorf("method = %q", lex.Method)
		}
	})

	t.Run("hybrid_v1 runs through search_hybrid/1", func(t *testing.T) {
		hy := baseline(t, r, BaselineHybridV1)
		if hy.Status != BaselineStatusOK || len(hy.Queries) != fixtureQueries {
			t.Fatalf("hybrid = %s with %d queries", hy.Status, len(hy.Queries))
		}
		if !strings.Contains(hy.Method, "search_hybrid/1") {
			t.Errorf("method = %q", hy.Method)
		}
		found := false
		for _, q := range hy.Queries {
			if q.ID == "fx-03" {
				for _, h := range q.Hits {
					if h.QualifiedName == "auth.ValidateToken" {
						found = true
					}
				}
			}
		}
		if !found {
			t.Error("search_hybrid did not surface auth.ValidateToken for the auth NL query; the seam is not wired to the real graph")
		}
	})

	t.Run("semantic_name_only is unavailable on the default build with the typed reason (AC-6)", func(t *testing.T) {
		// The runner wires embed.NewDefaultRegistry(), which registers no
		// embedder whatever the environment says; the report below was
		// produced with that registry. Pin the precondition anyway so the
		// test states the build it describes.
		if os.Getenv(embedEnvSelector) != "" {
			t.Logf("%s=%q is set in this shell; the default registry ignores it", embedEnvSelector, os.Getenv(embedEnvSelector))
		}
		sem := baseline(t, r, BaselineSemanticNameOnly)
		if sem.Status != BaselineStatusUnavailable {
			t.Fatalf("status = %s, want unavailable", sem.Status)
		}
		if sem.Reason != search.UnavailableReason {
			t.Errorf("reason = %q, want the engine's typed %q", sem.Reason, search.UnavailableReason)
		}
		if len(sem.Queries) != 0 {
			t.Errorf("an unavailable baseline must carry no per-query results, got %d", len(sem.Queries))
		}
		if res.Raw.Hits[BaselineSemanticNameOnly].Collected || res.Raw.Latency[BaselineSemanticNameOnly].Collected {
			t.Error("an unavailable baseline must not claim collected raw samples")
		}
		if sem.Overall.Status != StatusUnknown || len(sem.Overall.Metrics) != 0 {
			t.Errorf("unavailable aggregate = %+v, want UNKNOWN with no numbers", sem.Overall)
		}
		p := performance(t, r, BaselineSemanticNameOnly)
		for name, m := range map[string]Measure{"index_ms": p.IndexMS, "p50": p.QueryP50US, "p95": p.QueryP95US, "rss": p.PeakRSSMB, "sidecar": p.VectorSidecarBytes} {
			if m.Status != StatusUnknown || m.Value != nil {
				t.Errorf("%s = %+v, want UNKNOWN with no value", name, m)
			}
		}
	})

	t.Run("oracle_upper_bound is the scorer's ceiling", func(t *testing.T) {
		or := baseline(t, r, BaselineOracle)
		if or.Status != BaselineStatusOK {
			t.Fatalf("oracle = %s", or.Status)
		}
		for _, m := range []string{MetricTop1, MetricRecall5, MetricRecall10, MetricMRR10, MetricNDCG10} {
			if got := or.Overall.Metrics[m]; got != 1 {
				t.Errorf("oracle %s = %v, want 1", m, got)
			}
		}
		if got := or.Strata[StratumNoHit].Metrics[MetricNegativeHitRate5]; got != 0 {
			t.Errorf("oracle negative hit rate = %v, want 0 (it never returns a grade-0 span)", got)
		}
		p := performance(t, r, BaselineOracle)
		if p.IndexMS.Status != StatusNotApplicable {
			t.Errorf("oracle index_ms = %+v", p.IndexMS)
		}
	})

	t.Run("performance is measured for the indexed baselines (AC-4)", func(t *testing.T) {
		p := performance(t, r, BaselineLexical)
		if p.IndexMS.Status != StatusMeasured || p.IndexMS.Value == nil {
			t.Errorf("index_ms = %+v", p.IndexMS)
		}
		if p.QueryP50US.Status != StatusMeasured || p.QueryP95US.Status != StatusMeasured || p.LatencySamples != fixtureQueries*2 {
			t.Errorf("latency = p50 %+v p95 %+v samples %d", p.QueryP50US, p.QueryP95US, p.LatencySamples)
		}
		if p.PeakRSSMB.Status != StatusMeasured || p.PeakRSSMB.Value == nil || *p.PeakRSSMB.Value <= 0 {
			t.Errorf("peak_rss_mb = %+v", p.PeakRSSMB)
		}
		if p.VectorSidecarBytes.Status != StatusNotApplicable {
			t.Errorf("vector_sidecar_bytes = %+v", p.VectorSidecarBytes)
		}
	})

	t.Run("raw samples carry the hits and the timings and nothing derived", func(t *testing.T) {
		hits := res.Raw.Hits[BaselineLexical]
		if !hits.Collected || len(hits.Queries) != fixtureQueries || hits.Series != RawSeriesHits {
			t.Errorf("raw hits = %+v", hits)
		}
		lat := res.Raw.Latency[BaselineLexical]
		if lat.Samples != fixtureQueries*2 || lat.IndexMS == nil || lat.PeakRSSMB == nil {
			t.Errorf("raw latency = samples %d index %v rss %v", lat.Samples, lat.IndexMS, lat.PeakRSSMB)
		}
		b, _ := json.Marshal(hits)
		for _, derived := range []string{"recall", "mrr", "ndcg"} {
			if strings.Contains(strings.ToLower(string(b)), derived) {
				t.Errorf("raw hit set carries a derived %s figure", derived)
			}
		}
	})

	t.Run("the environment block is present and separate", func(t *testing.T) {
		if r.Environment.GeneratedAt != "2026-08-30T12:00:00Z" || r.Environment.OS == "" || r.Environment.GoVersion == "" {
			t.Errorf("environment = %+v", r.Environment)
		}
		if len(r.Environment.Missing()) != 0 {
			t.Errorf("missing = %v", r.Environment.Missing())
		}
		rep, err := ReproducibleBytes(r)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(rep, []byte("generated_at")) || bytes.Contains(rep, []byte("_us")) {
			t.Error("the reproducible section leaks a timestamp or a timing")
		}
	})
}

// Determinism: the same fixture + dataset must produce byte-identical
// reproducible bytes across two runs (test notes; AC-5/AC-10).
func TestRun_ReproducibleSectionIsByteIdenticalAcrossRuns(t *testing.T) {
	a, err := ReproducibleBytes(runFixture(t).Report)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ReproducibleBytes(runFixture(t).Report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("reproducible sections differ between two runs:\n--- run 1\n%s\n--- run 2\n%s", a, b)
	}
	full, err := MarshalReport(runFixture(t).Report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(full, []byte("\n")) || !json.Valid(full) {
		t.Error("MarshalReport must emit valid JSON with a trailing newline")
	}
}

func TestRun_FailsClosed(t *testing.T) {
	ds, err := LoadDataset(fixtureDataset)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("a judgement that does not resolve is an error, not a zero", func(t *testing.T) {
		stale := *ds
		copied := *ds.Dataset
		copied.Queries = append([]Query(nil), ds.Dataset.Queries...)
		q := copied.Queries[0]
		q.Judgements = append([]Judgement(nil), q.Judgements...)
		q.Judgements[0].EndLine = 9999
		copied.Queries[0] = q
		stale.Dataset = &copied
		_, err := Run(context.Background(), Options{RepoRoot: fixtureRepo, RepoName: "fixture", Dataset: &stale, WorkDir: t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "fx-01") {
			t.Errorf("Run with a stale span = %v, want an error naming fx-01", err)
		}
	})
	t.Run("an unknown baseline is refused by name", func(t *testing.T) {
		if _, err := ParseBaselines([]string{"lexical", "bm42"}); err == nil || !strings.Contains(err.Error(), "bm42") {
			t.Errorf("ParseBaselines = %v", err)
		}
		bs, err := ParseBaselines(nil)
		if err != nil || len(bs) != 4 {
			t.Errorf("ParseBaselines(nil) = %v, %v", bs, err)
		}
	})
	t.Run("a missing repository root is an error", func(t *testing.T) {
		if _, err := Run(context.Background(), Options{RepoRoot: filepath.Join(t.TempDir(), "nope"), Dataset: ds}); err == nil {
			t.Error("Run over a missing root = nil error")
		}
	})
	t.Run("a subset of baselines runs only those", func(t *testing.T) {
		res := runFixture(t, BaselineOracle)
		if len(res.Report.Reproducible.Baselines) != 1 || res.Report.Reproducible.Baselines[0].Name != BaselineOracle {
			t.Errorf("baselines = %+v", res.Report.Reproducible.Baselines)
		}
	})
}
