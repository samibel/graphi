package retrieval

import (
	"math"
	"sort"
	"strconv"

	"github.com/samibel/graphi/engine/trust"
)

// TopK is the ranking depth every baseline is asked for and the @k depth of
// the report's MRR/NDCG. Recall is additionally reported at 5.
const TopK = 10

// Hit is one ranked result as the scorer sees it: where it points (path +
// line, the two fields the matching rule reads), what it was (node identity,
// for the reader) and what it costs (whitespace tokens of its read window).
// Path, NodeID and QualifiedName are repository-controlled text and are
// bounded by BoundArtifactText before they are published.
type Hit struct {
	Rank          int    `json:"rank"`
	Path          string `json:"path"`
	Line          int    `json:"line"`
	NodeID        string `json:"node_id,omitempty"`
	Kind          string `json:"kind,omitempty"`
	QualifiedName string `json:"qualified_name,omitempty"`
	Tokens        int    `json:"tokens"`
}

// TruncationMarker ends a repository-controlled string that was cut at
// trust.MaxPathLength. It is engine/trust's marker: visible, and never
// produced by graphi itself, so a shortened value is never mistaken for a
// real one.
const TruncationMarker = "…[truncated]"

// BoundArtifactText bounds one repository-controlled string (a path, a node
// id, a qualified name) before it enters a report or raw artifact, the way
// trust.MaxPathLength bounds an emitted path (context/standards.md): a value
// within the bound is returned unchanged; a longer one is cut and marked.
// The runner scores the canonical value and publishes the bounded one; since
// a judged span path is itself validated to be within the bound and never to
// carry the marker, a bounded path matches exactly the spans its canonical
// value matches (none), so the two never score differently.
func BoundArtifactText(s string) string {
	if len(s) <= trust.MaxPathLength {
		return s
	}
	return s[:trust.MaxPathLength-len(TruncationMarker)] + TruncationMarker
}

// boundHit is the hit as it is published: every repository-controlled string
// bounded, everything else as scored.
func boundHit(h Hit) Hit {
	h.Path = BoundArtifactText(h.Path)
	h.NodeID = BoundArtifactText(h.NodeID)
	h.QualifiedName = BoundArtifactText(h.QualifiedName)
	return h
}

// SpanMatches is THE matching rule, defined once: a hit matches a judged span
// when its path equals the span's path exactly and its line lies within
// [start_line, end_line]. The hit's line is the node's declaration line (or,
// for the oracle, the span's start line). Documented in
// docs/eval/retrieval/README.md.
func SpanMatches(path string, line int, j Judgement) bool {
	return path == j.Path && line >= j.StartLine && line <= j.EndLine
}

// QueryMetrics is one query's score under one baseline.
type QueryMetrics struct {
	// Scored is false when the query has no relevant span (no_hit stratum):
	// recall-type metrics are undefined there and are reported as zero only
	// because JSON has no NaN — Aggregate excludes unscored queries.
	Scored        bool `json:"scored"`
	RelevantSpans int  `json:"relevant_spans"`

	Top1     float64 `json:"top1"`
	Recall5  float64 `json:"recall_at_5"`
	Recall10 float64 `json:"recall_at_10"`
	MRR10    float64 `json:"mrr_at_10"`
	NDCG10   float64 `json:"ndcg_at_10"`
	// FirstRelevantRank is 1-based over the whole ranking; null when no hit
	// was relevant.
	FirstRelevantRank *int `json:"first_relevant_rank"`
	// RecallAtTokens is recall over the prefix of the ranking whose cumulative
	// token cost fits the budget, keyed by the budget ("600", "1200", ...).
	RecallAtTokens map[string]float64 `json:"recall_at_tokens"`
	// NegativeHitAt5 is set for no_hit queries only: whether any of the top-5
	// hits matched one of the query's grade-0 negative-example spans.
	NegativeHitAt5 *bool `json:"negative_hit_at_5,omitempty"`
	// HitGrades is the matched grade per hit (0 = matched nothing relevant or
	// matched only grade-0 spans), so a reader can see WHY a query scored.
	HitGrades []int `json:"hit_grades"`
}

// Evaluate scores hits for q. minGrade is the relevance threshold; budgets
// are the token budgets for RecallAtTokens.
func Evaluate(hits []Hit, q Query, minGrade int, budgets []int) QueryMetrics {
	m := QueryMetrics{RecallAtTokens: map[string]float64{}, HitGrades: make([]int, 0, len(hits))}
	relevant := q.RelevantSpans(minGrade)
	m.RelevantSpans = len(relevant)
	m.Scored = len(relevant) > 0

	// Which relevant spans does each hit cover, and what is its best grade.
	// A span is credited at most once (the first hit that covers it) for
	// recall and DCG, so ten hits inside one function do not inflate either.
	covered := make([]bool, len(relevant))
	coveredBy := func(k int) int {
		n := 0
		for i := range relevant {
			if covered[i] {
				continue
			}
			for _, h := range hits[:min(k, len(hits))] {
				if SpanMatches(h.Path, h.Line, relevant[i]) {
					covered[i] = true
					n++
					break
				}
			}
		}
		return n
	}
	recallAt := func(k int) float64 {
		if len(relevant) == 0 {
			return 0
		}
		for i := range covered {
			covered[i] = false
		}
		return float64(coveredBy(k)) / float64(len(relevant))
	}

	// Per-hit grade: the best grade among ALL judged spans the hit matches
	// (negative examples included, so a 0 is a 0 and not an unjudged hit).
	for _, h := range hits {
		best := 0
		for _, j := range q.Judgements {
			if SpanMatches(h.Path, h.Line, j) && j.Grade > best {
				best = j.Grade
			}
		}
		m.HitGrades = append(m.HitGrades, best)
	}

	for i, g := range m.HitGrades {
		if g >= minGrade && len(relevant) > 0 {
			r := i + 1
			m.FirstRelevantRank = &r
			if r <= TopK {
				m.MRR10 = 1 / float64(r)
			}
			break
		}
	}
	if len(hits) > 0 && m.HitGrades[0] >= minGrade && len(relevant) > 0 {
		m.Top1 = 1
	}
	m.Recall5 = recallAt(5)
	m.Recall10 = recallAt(TopK)
	m.NDCG10 = ndcg(hits, q.Judgements, TopK)

	for _, b := range budgets {
		cum, k := 0, 0
		for _, h := range hits {
			if cum+h.Tokens > b {
				break
			}
			cum += h.Tokens
			k++
		}
		m.RecallAtTokens[strconv.Itoa(b)] = recallAt(k)
	}

	if q.Stratum == StratumNoHit {
		neg := false
		for _, h := range hits[:min(5, len(hits))] {
			for _, j := range q.Judgements {
				if SpanMatches(h.Path, h.Line, j) {
					neg = true
				}
			}
		}
		m.NegativeHitAt5 = &neg
	}
	return m
}

// ndcg computes NDCG@k with gain 2^grade-1 and log2(rank+1) discount. Each
// judged span's gain may be credited once; a hit credits the highest-grade
// span it matches that is still uncredited. The ideal ordering is every judged
// span sorted by grade descending.
func ndcg(hits []Hit, judgements []Judgement, k int) float64 {
	used := make([]bool, len(judgements))
	dcg := 0.0
	for i, h := range hits[:min(k, len(hits))] {
		best, bestIdx := -1, -1
		for j, jd := range judgements {
			if used[j] || !SpanMatches(h.Path, h.Line, jd) {
				continue
			}
			if jd.Grade > best {
				best, bestIdx = jd.Grade, j
			}
		}
		if bestIdx < 0 {
			continue
		}
		used[bestIdx] = true
		dcg += gain(best) / math.Log2(float64(i+2))
	}
	grades := make([]int, 0, len(judgements))
	for _, jd := range judgements {
		grades = append(grades, jd.Grade)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(grades)))
	idcg := 0.0
	for i, g := range grades[:min(k, len(grades))] {
		idcg += gain(g) / math.Log2(float64(i+2))
	}
	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}

func gain(grade int) float64 { return math.Pow(2, float64(grade)) - 1 }

// Aggregate metric names. They are the keys of Aggregate.Metrics and of the
// targets file, so they are constants rather than strings at each site.
const (
	MetricTop1                  = "top1"
	MetricRecall5               = "recall@5"
	MetricRecall10              = "recall@10"
	MetricMRR10                 = "mrr@10"
	MetricNDCG10                = "ndcg@10"
	MetricFirstRelevantFound    = "first_relevant_found"
	MetricFirstRelevantRankMean = "first_relevant_rank_mean"
	MetricNegativeHitRate5      = "negative_hit_rate@5"
)

// RecallAtTokensMetric names the recall-under-budget aggregate for a budget.
func RecallAtTokensMetric(budget int) string { return "recall@" + strconv.Itoa(budget) + "tok" }

// Measurement statuses. UNKNOWN is rendered literally so it can never be
// mistaken for a zero (AC-4).
const (
	StatusMeasured = "measured"
	StatusUnknown  = "UNKNOWN"
)

// QueryResult is one query's ranking under one baseline together with its
// score. The hits are the raw sample the score is recomputed from.
type QueryResult struct {
	ID      string       `json:"id"`
	Stratum string       `json:"stratum"`
	Split   string       `json:"split"`
	Hits    []Hit        `json:"hits"`
	Metrics QueryMetrics `json:"metrics"`
}

// AggregateMetrics is the mean of every metric over a set of queries. Metrics
// is a map so its keys serialize sorted; a metric that could not be computed
// (no scored query) is absent, and Status says UNKNOWN when nothing could be.
type AggregateMetrics struct {
	Queries int                `json:"queries"`
	Scored  int                `json:"scored"`
	Status  string             `json:"status"`
	Metrics map[string]float64 `json:"metrics,omitempty"`
}

// Aggregate averages the per-query metrics. Sums run in slice order so the
// floating-point result is reproducible bit for bit.
func Aggregate(results []QueryResult, budgets []int) AggregateMetrics {
	out := AggregateMetrics{Queries: len(results), Status: StatusUnknown}
	sum := map[string]float64{}
	found, rankSum, noHit, negHits := 0, 0, 0, 0
	for _, r := range results {
		m := r.Metrics
		if m.NegativeHitAt5 != nil {
			noHit++
			if *m.NegativeHitAt5 {
				negHits++
			}
		}
		if !m.Scored {
			continue
		}
		out.Scored++
		sum[MetricTop1] += m.Top1
		sum[MetricRecall5] += m.Recall5
		sum[MetricRecall10] += m.Recall10
		sum[MetricMRR10] += m.MRR10
		sum[MetricNDCG10] += m.NDCG10
		for _, b := range budgets {
			sum[RecallAtTokensMetric(b)] += m.RecallAtTokens[strconv.Itoa(b)]
		}
		if m.FirstRelevantRank != nil {
			found++
			rankSum += *m.FirstRelevantRank
		}
	}
	metrics := map[string]float64{}
	if out.Scored > 0 {
		n := float64(out.Scored)
		for k, v := range sum {
			metrics[k] = v / n
		}
		metrics[MetricFirstRelevantFound] = float64(found) / n
		if found > 0 {
			metrics[MetricFirstRelevantRankMean] = float64(rankSum) / float64(found)
		}
	}
	if noHit > 0 {
		metrics[MetricNegativeHitRate5] = float64(negHits) / float64(noHit)
	}
	if len(metrics) > 0 {
		out.Metrics = metrics
		out.Status = StatusMeasured
	}
	return out
}

// PercentileInt64 is the nearest-rank percentile over an observed sample set:
// always a sample, never an interpolation, so two derivations over the same
// samples agree bit for bit. It does not modify xs. Empty input yields 0; the
// caller decides whether that is UNKNOWN.
func PercentileInt64(xs []int64, p int) int64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := append([]int64(nil), xs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	if p < 1 {
		p = 1
	}
	if p > 100 {
		p = 100
	}
	rank := (p*len(sorted) + 99) / 100
	if rank < 1 {
		rank = 1
	}
	return sorted[rank-1]
}
