package retrieval

import (
	"math"
	"testing"
)

func span(path string, start, end, grade int) Judgement {
	return Judgement{Path: path, StartLine: start, EndLine: end, Anchor: "x", Grade: grade,
		Reason: "r", Annotator: "a", Reviewer: "o"}
}

func hit(rank int, path string, line, tokens int) Hit {
	return Hit{Rank: rank, Path: path, Line: line, Tokens: tokens}
}

// The matching rule, stated once (README "Matching rule") and tested once.
func TestSpanMatches(t *testing.T) {
	j := span("a/b.go", 10, 20, 3)
	cases := []struct {
		name string
		path string
		line int
		want bool
	}{
		{"inside", "a/b.go", 15, true},
		{"on start line", "a/b.go", 10, true},
		{"on end line", "a/b.go", 20, true},
		{"one before", "a/b.go", 9, false},
		{"one after", "a/b.go", 21, false},
		{"other file same line", "a/c.go", 15, false},
		{"path is compared exactly, not by suffix", "x/a/b.go", 15, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SpanMatches(tc.path, tc.line, j); got != tc.want {
				t.Errorf("SpanMatches(%q,%d) = %v, want %v", tc.path, tc.line, got, tc.want)
			}
		})
	}
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestEvaluate_HandComputedExamples(t *testing.T) {
	budgets := []int{600, 1200, 2000}
	q := Query{ID: "q", Stratum: StratumNLBehaviour, Judgements: []Judgement{
		span("auth/token.go", 40, 55, 3), // exact answer
		span("auth/token.go", 27, 38, 2), // directly relevant
		span("auth/token_test.go", 1, 30, 1),
		span("cmd/app/main.go", 1, 40, 0),
	}}

	t.Run("perfect ranking scores 1 everywhere", func(t *testing.T) {
		// The ideal ordering is EVERY judged span by grade, marginal ones
		// included, so a perfect ranking also surfaces the grade-1 test.
		hits := []Hit{hit(1, "auth/token.go", 45, 300), hit(2, "auth/token.go", 30, 300), hit(3, "auth/token_test.go", 5, 300)}
		m := Evaluate(hits, q, DefaultRelevantMinGrade, budgets)
		if !m.Scored || m.RelevantSpans != 2 {
			t.Fatalf("scored=%v relevant=%d", m.Scored, m.RelevantSpans)
		}
		for name, got := range map[string]float64{"top1": m.Top1, "r5": m.Recall5, "r10": m.Recall10, "mrr": m.MRR10, "ndcg": m.NDCG10} {
			if !approx(got, 1) {
				t.Errorf("%s = %v, want 1", name, got)
			}
		}
		if m.FirstRelevantRank == nil || *m.FirstRelevantRank != 1 {
			t.Errorf("first_relevant_rank = %v, want 1", m.FirstRelevantRank)
		}
		if !approx(m.RecallAtTokens["600"], 1) || !approx(m.RecallAtTokens["2000"], 1) {
			t.Errorf("recall_at_tokens = %v", m.RecallAtTokens)
		}
	})

	t.Run("relevant at rank 3 after two misses", func(t *testing.T) {
		hits := []Hit{
			hit(1, "cmd/app/main.go", 5, 500), // negative example, grade 0
			hit(2, "store/store.go", 3, 500),  // unjudged
			hit(3, "auth/token.go", 41, 500),  // grade 3
		}
		m := Evaluate(hits, q, DefaultRelevantMinGrade, budgets)
		if !approx(m.Top1, 0) {
			t.Errorf("top1 = %v", m.Top1)
		}
		if !approx(m.Recall5, 0.5) || !approx(m.Recall10, 0.5) {
			t.Errorf("recall@5 = %v recall@10 = %v, want 0.5", m.Recall5, m.Recall10)
		}
		if !approx(m.MRR10, 1.0/3) {
			t.Errorf("mrr@10 = %v, want 1/3", m.MRR10)
		}
		// DCG = (2^3-1)/log2(4) = 7/2 ; IDCG over the judged grades 3,2,1,0 =
		// 7/log2(2) + 3/log2(3) + 1/log2(4) + 0.
		wantNDCG := (7.0 / math.Log2(4)) / (7.0 + 3.0/math.Log2(3) + 1.0/math.Log2(4))
		if !approx(m.NDCG10, wantNDCG) {
			t.Errorf("ndcg@10 = %v, want %v", m.NDCG10, wantNDCG)
		}
		if m.FirstRelevantRank == nil || *m.FirstRelevantRank != 3 {
			t.Errorf("first_relevant_rank = %v, want 3", m.FirstRelevantRank)
		}
		// 500+500 = 1000 <= 1200 keeps two hits (recall 0); 1500 <= 2000 keeps three.
		if !approx(m.RecallAtTokens["600"], 0) || !approx(m.RecallAtTokens["1200"], 0) || !approx(m.RecallAtTokens["2000"], 0.5) {
			t.Errorf("recall_at_tokens = %v", m.RecallAtTokens)
		}
	})

	t.Run("a hit matching two overlapping spans credits each span once and takes the best grade", func(t *testing.T) {
		q2 := Query{ID: "q2", Stratum: StratumAmbiguous, Judgements: []Judgement{
			span("s.go", 1, 50, 2), span("s.go", 10, 20, 3),
		}}
		hits := []Hit{hit(1, "s.go", 15, 10), hit(2, "s.go", 16, 10)}
		m := Evaluate(hits, q2, DefaultRelevantMinGrade, budgets)
		if !approx(m.Recall10, 1) {
			t.Errorf("recall@10 = %v, want 1 (both spans covered by the first hit)", m.Recall10)
		}
		// Hit 1 credits the grade-3 span, hit 2 credits the remaining grade-2 span:
		// DCG = 7/1 + 3/log2(3) == IDCG, so NDCG must be exactly 1.
		if !approx(m.NDCG10, 1) {
			t.Errorf("ndcg@10 = %v, want 1", m.NDCG10)
		}
		// The per-hit grade is the best grade the hit MATCHES (both hits sit in
		// both spans); the credit-once rule applies to recall and DCG only.
		if len(m.HitGrades) != 2 || m.HitGrades[0] != 3 || m.HitGrades[1] != 3 {
			t.Errorf("hit_grades = %v, want [3 3]", m.HitGrades)
		}
	})

	t.Run("an empty ranking scores zero, not NaN, and has no first-relevant rank", func(t *testing.T) {
		m := Evaluate(nil, q, DefaultRelevantMinGrade, budgets)
		if !m.Scored || m.Recall10 != 0 || m.MRR10 != 0 || m.NDCG10 != 0 || m.Top1 != 0 {
			t.Errorf("empty ranking: %+v", m)
		}
		if m.FirstRelevantRank != nil {
			t.Errorf("first_relevant_rank = %d, want none", *m.FirstRelevantRank)
		}
		if math.IsNaN(m.RecallAtTokens["600"]) {
			t.Error("recall_at_tokens is NaN")
		}
	})

	t.Run("ranks beyond 10 do not count for the @10 metrics but do for first-relevant rank", func(t *testing.T) {
		var hits []Hit
		for i := 1; i <= 11; i++ {
			hits = append(hits, hit(i, "unjudged.go", i, 10))
		}
		hits[10] = hit(11, "auth/token.go", 45, 10)
		m := Evaluate(hits, q, DefaultRelevantMinGrade, budgets)
		if m.Recall10 != 0 || m.MRR10 != 0 || m.NDCG10 != 0 {
			t.Errorf("rank 11 leaked into @10 metrics: %+v", m)
		}
		if m.FirstRelevantRank == nil || *m.FirstRelevantRank != 11 {
			t.Errorf("first_relevant_rank = %v, want 11", m.FirstRelevantRank)
		}
	})

	t.Run("a no_hit query is not scored on recall but reports a negative hit", func(t *testing.T) {
		nh := Query{ID: "n", Stratum: StratumNoHit, Judgements: []Judgement{span("cmd/app/main.go", 1, 40, 0)}}
		m := Evaluate([]Hit{hit(1, "cmd/app/main.go", 3, 10)}, nh, DefaultRelevantMinGrade, budgets)
		if m.Scored {
			t.Error("a query without relevant spans must not be scored on recall")
		}
		if m.NegativeHitAt5 == nil || !*m.NegativeHitAt5 {
			t.Errorf("negative_hit_at_5 = %v, want true", m.NegativeHitAt5)
		}
		m = Evaluate([]Hit{hit(1, "store/store.go", 3, 10)}, nh, DefaultRelevantMinGrade, budgets)
		if m.NegativeHitAt5 == nil || *m.NegativeHitAt5 {
			t.Errorf("negative_hit_at_5 = %v, want false", m.NegativeHitAt5)
		}
	})
}

func TestAggregate_MeansOverScoredQueriesOnly(t *testing.T) {
	budgets := []int{600}
	scored := func(id string, r10 float64) QueryResult {
		r := 1
		return QueryResult{ID: id, Stratum: StratumNLBehaviour, Split: SplitDev, Metrics: QueryMetrics{
			Scored: true, Top1: r10, Recall5: r10, Recall10: r10, MRR10: r10, NDCG10: r10,
			FirstRelevantRank: &r, RecallAtTokens: map[string]float64{"600": r10},
		}}
	}
	neg := true
	noHit := QueryResult{ID: "n", Stratum: StratumNoHit, Split: SplitDev, Metrics: QueryMetrics{
		Scored: false, NegativeHitAt5: &neg, RecallAtTokens: map[string]float64{"600": 0},
	}}
	agg := Aggregate([]QueryResult{scored("a", 1), scored("b", 0.5), noHit}, budgets)
	if agg.Queries != 3 || agg.Scored != 2 {
		t.Fatalf("queries=%d scored=%d", agg.Queries, agg.Scored)
	}
	if !approx(agg.Metrics[MetricRecall10], 0.75) || !approx(agg.Metrics[MetricNDCG10], 0.75) {
		t.Errorf("means = %v, want 0.75", agg.Metrics)
	}
	if !approx(agg.Metrics[MetricFirstRelevantFound], 1) || !approx(agg.Metrics[MetricFirstRelevantRankMean], 1) {
		t.Errorf("first-relevant metrics = %v", agg.Metrics)
	}
	if !approx(agg.Metrics[MetricNegativeHitRate5], 1) {
		t.Errorf("negative hit rate = %v, want 1 (one no_hit query, one negative hit)", agg.Metrics[MetricNegativeHitRate5])
	}
	if !approx(agg.Metrics[RecallAtTokensMetric(600)], 0.75) {
		t.Errorf("recall@600tok = %v", agg.Metrics)
	}
	if agg.Status != StatusMeasured {
		t.Errorf("status = %s", agg.Status)
	}

	t.Run("no scored queries and no no_hit queries is UNKNOWN, never zero", func(t *testing.T) {
		empty := Aggregate(nil, budgets)
		if empty.Status != StatusUnknown || len(empty.Metrics) != 0 {
			t.Errorf("empty aggregate = %+v", empty)
		}
	})
	t.Run("only no_hit queries carries the negative rate and nothing else", func(t *testing.T) {
		only := Aggregate([]QueryResult{noHit}, budgets)
		if only.Status != StatusMeasured || len(only.Metrics) != 1 {
			t.Errorf("no_hit-only aggregate = %+v", only)
		}
	})
}

func TestPercentile_NearestRank(t *testing.T) {
	xs := []int64{50, 10, 40, 20, 30}
	if p := PercentileInt64(xs, 50); p != 30 {
		t.Errorf("p50 = %d, want 30", p)
	}
	if p := PercentileInt64(xs, 95); p != 50 {
		t.Errorf("p95 = %d, want 50", p)
	}
	if p := PercentileInt64([]int64{7}, 95); p != 7 {
		t.Errorf("single sample p95 = %d, want 7", p)
	}
	if p := PercentileInt64(nil, 50); p != 0 {
		t.Errorf("empty p50 = %d, want 0", p)
	}
	if xs[0] != 50 {
		t.Error("PercentileInt64 must not sort its input in place")
	}
}
