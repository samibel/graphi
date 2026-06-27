package analysis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis/githistory"
	"github.com/samibel/graphi/engine/query"
)

// SuggestReviewersAnalyzerName is the dispatch key for the SW-107 reviewer
// recommender (EP-018 story 3/4). It is a composite, read-only, DETERMINISTIC
// analyzer that takes a PR (or an explicit touched symbol/file set, handed in via
// Params.Diff and reused through the EP-007 parseDiff/resolveRef kernel) and
// proposes a ranked candidate-reviewer list derived purely from the LOCAL graph
// plus the LOCAL git-history signals — ZERO engine egress. Each candidate carries
// a transparent per-signal breakdown over three signals:
//
//   - ownership            — authorship concentration over the touched FILES
//     (file-granular: per-symbol authorship is not available in the codebase).
//   - recency_decayed_churn — recent change density of the touched FILES, decayed
//     from a FIXED reference timestamp captured from the commit window (never
//     call-time now()). File-granular.
//   - subgraph_proximity   — callers/callees/contract neighbors of the touched
//     SYMBOLS (impact adjacency over dependencyKinds, weighted by metrics.go
//     centrality), with the proximity weight attributed to each neighbor's file
//     owner. Symbol-granular subgraph; owner attribution is file-granular.
//
// The composite is a fixed-weight integer fold of the three (no floats → no
// non-associativity / formatting drift). Candidates are ranked composite DESC,
// tie-broken on reviewer identity ASC (a stable canonical total order).
const SuggestReviewersAnalyzerName = "suggest-reviewers"

// ReviewersSchemaVersion versions the ReviewerReport JSON shape.
const ReviewersSchemaVersion = 1

// ReviewersAnalyzerVersion identifies the ranking LOGIC version, echoed in every
// report so a stored/audited recommendation ties to the algorithm that produced it.
const ReviewersAnalyzerVersion = "suggest-reviewers/1"

// reviewerWeights is the DOCUMENTED, fixed-by-default integer weight model folding
// the three reviewer signals into one composite. Integer only (no float
// non-associativity / nondeterminism). It is hashed into weights_hash so any
// change is auditable and reproducible (mirrors the EP-007 weightTable.hash
// discipline).
//
//	composite = Ownership*ownershipPoints
//	          + RecencyChurn*recencyDecayedChurnPoints
//	          + Proximity*subgraphProximityPoints
type reviewerWeights struct {
	Ownership    int `json:"ownership"`
	RecencyChurn int `json:"recency_churn"`
	Proximity    int `json:"proximity"`
}

// defaultReviewerWeights is the fixed weight model for ReviewersAnalyzerVersion
// "suggest-reviewers/1". Ownership leads (who owns the touched files), recency
// next (who touched them recently), proximity last (who owns the affected
// subgraph). Documented and version-pinned.
var defaultReviewerWeights = reviewerWeights{
	Ownership:    10,
	RecencyChurn: 8,
	Proximity:    5,
}

func (w reviewerWeights) hash() string {
	b, _ := json.Marshal(w)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// ReviewerSignalBreakdown is the transparent per-candidate contribution of each
// of the three signals, emitted alongside the composite so the ranking is
// auditable and never over-claimed. All integer (bucketed/counted), never float.
type ReviewerSignalBreakdown struct {
	Ownership           int `json:"ownership"`             // authorship-concentration points over touched files (file-granular)
	RecencyDecayedChurn int `json:"recency_decayed_churn"` // recency-decayed change density over touched files (file-granular)
	SubgraphProximity   int `json:"subgraph_proximity"`    // affected-subgraph neighbor ownership points (symbol-granular subgraph)
}

// ReviewerCandidate is one ranked candidate reviewer: the reviewer identity, the
// composite score, and the transparent per-signal breakdown.
type ReviewerCandidate struct {
	Reviewer  string                  `json:"reviewer"`
	Composite int                     `json:"composite"`
	Signals   ReviewerSignalBreakdown `json:"signals"`
}

// ReviewerReport is the full versioned payload emitted over every surface.
// Candidates are emitted in a TOTAL ORDER — composite DESC, then reviewer
// identity ASC — so an identical touched set + graph + history yields
// byte-identical output. SignalGranularity surfaces the honest per-signal
// granularity (file vs symbol) so a consumer is never misled into thinking a
// per-symbol author was identified.
type ReviewerReport struct {
	SchemaVersion         int                 `json:"schema_version"`
	AnalyzerVersion       string              `json:"analyzer_version"`
	WeightsHash           string              `json:"weights_hash"`
	IdentitySchemaVersion uint32              `json:"identity_schema_version"`
	Outcome               string              `json:"outcome"` // found | empty
	SignalGranularity     map[string]string   `json:"signal_granularity"`
	TouchedSymbols        []string            `json:"touched_symbols"`
	TouchedFiles          []string            `json:"touched_files"`
	Candidates            []ReviewerCandidate `json:"candidates"`
}

// reviewerSignalGranularity is the fixed, documented honesty label set surfaced in
// every report: ownership/churn are file-granular (per-symbol authorship is
// unavailable in the codebase), proximity is symbol-granular over the affected
// subgraph with owner attribution at file granularity.
var reviewerSignalGranularity = map[string]string{
	"ownership":             "file",
	"recency_decayed_churn": "file",
	"subgraph_proximity":    "symbol",
}

// suggestReviewersAnalyzer is the registered reviewer recommender. It holds an
// injectable git-history provider seam (nil by default — the production provider
// is injected by the caller, mirroring gitHistoryAdapter/prsignals) and the fixed
// weight model. It is stateless per call and performs ZERO outbound network
// activity (pure graph reads + an injected local git-history provider).
type suggestReviewersAnalyzer struct {
	provider githistory.GitProvider
	weights  reviewerWeights
}

// newSuggestReviewersAnalyzer builds the production recommender with a nil git
// provider (no git access on the hot path), so with no injected history it
// returns a stable empty candidate list — never an error. A caller injects a real
// provider after construction (as the other history-consuming analyzers do).
func newSuggestReviewersAnalyzer() suggestReviewersAnalyzer {
	return suggestReviewersAnalyzer{provider: nil, weights: defaultReviewerWeights}
}

func (suggestReviewersAnalyzer) Name() string { return SuggestReviewersAnalyzerName }

// Analyze resolves the touched set (Params.Diff), computes the three reviewer
// signals over the local graph + injected git history, folds them into the
// fixed-weight composite, and returns a versioned ReviewerReport on the generic
// Analysis envelope. It never fetches anything and never fails on a missing
// signal (it degrades to a stable empty/low-signal result).
func (a suggestReviewersAnalyzer) Analyze(ctx context.Context, r query.Reader, p Params) (Analysis, error) {
	weights := a.weights
	if weights == (reviewerWeights{}) {
		weights = defaultReviewerWeights
	}
	report, err := a.suggest(ctx, r, p, weights)
	if err != nil {
		return Analysis{}, err
	}
	return Analysis{
		Analyzer:  SuggestReviewersAnalyzerName,
		Outcome:   query.Outcome(report.Outcome),
		Symbol:    p.Symbol,
		Reviewers: &report,
	}, nil
}

// suggest is the core, split out for direct unit-testing.
func (a suggestReviewersAnalyzer) suggest(ctx context.Context, r query.Reader, p Params, weights reviewerWeights) (ReviewerReport, error) {
	report := ReviewerReport{
		SchemaVersion:         ReviewersSchemaVersion,
		AnalyzerVersion:       ReviewersAnalyzerVersion,
		WeightsHash:           weights.hash(),
		IdentitySchemaVersion: model.IdentitySchemaVersion,
		Outcome:               string(query.OutcomeEmpty),
		SignalGranularity:     reviewerSignalGranularity,
		TouchedSymbols:        []string{},
		TouchedFiles:          []string{},
		Candidates:            []ReviewerCandidate{},
	}

	// (1) Resolve the touched set ONCE via the reused EP-007 kernel: parseDiff for
	// the changed refs, resolveRef for the precise path→symbol resolution. Touched
	// FILES drive the file-granular ownership/churn; touched SYMBOLS drive the
	// per-symbol subgraph proximity.
	refs, err := parseDiff(p.Diff)
	if err != nil {
		return ReviewerReport{}, err
	}
	touchedFiles := map[string]struct{}{}
	touchedSymbols := map[model.NodeId]struct{}{}
	for _, ref := range refs {
		if ref.file != "" {
			if path := model.NormalizePath(ref.file); path != "" {
				touchedFiles[path] = struct{}{}
			}
		}
		id, ok, err := resolveRef(ctx, r, ref)
		if err != nil {
			return ReviewerReport{}, err
		}
		if ok {
			touchedSymbols[id] = struct{}{}
		}
	}
	report.TouchedFiles = sortedStrSet(touchedFiles)
	report.TouchedSymbols = sortedIDSet(touchedSymbols)

	if len(touchedFiles) == 0 && len(touchedSymbols) == 0 {
		return report, nil // empty touched set: completes empty, never an error
	}

	// (2) Pull the windowed commit set ONCE from the injected provider. A nil/empty
	// provider yields no history → a stable empty/low-signal result (degenerate
	// input AC-7), never an error.
	var commits []githistory.Commit
	if a.provider != nil {
		commits, err = a.provider.Log(ctx, 0, time.Time{})
		if err != nil {
			return ReviewerReport{}, err
		}
	}

	// (3) FIXED recency-decay reference: the latest commit timestamp IN the window
	// (deterministic, derived from input data — NEVER call-time now()). Absent
	// commits, the reference is the zero time and every decay bucket is 0.
	var reference time.Time
	for _, c := range commits {
		if c.Timestamp.After(reference) {
			reference = c.Timestamp
		}
	}

	// (4) Per-file authorship: last author (newest commit touching the file) and the
	// ownership + recency-decayed-churn points per author. Commits are
	// reverse-chronological (newest first), so the first commit seen per file is its
	// most-recent modifier (the blame attribution).
	lastAuthorByPath := map[string]string{}
	ownershipPts := map[string]int{}
	recencyPts := map[string]int{}
	for _, c := range commits {
		author := strings.TrimSpace(c.Author)
		if author == "" {
			continue
		}
		decay := decayBucket(reference, c.Timestamp)
		for _, f := range c.FilesChanged {
			path := model.NormalizePath(f)
			if path == "" {
				continue
			}
			if _, seen := lastAuthorByPath[path]; !seen {
				lastAuthorByPath[path] = author
			}
			if _, touched := touchedFiles[path]; !touched {
				continue
			}
			ownershipPts[author]++      // authorship concentration over touched files
			recencyPts[author] += decay // recency-decayed change density
		}
	}

	// (5) Subgraph proximity: the affected-subgraph neighbors (callers/callees/
	// contract neighbors over dependencyKinds) of each touched SYMBOL, with the
	// proximity weight attributed to each neighbor's file owner and weighted by
	// metrics.go centrality. Built ONCE.
	proximityPts, err := a.proximity(ctx, r, touchedSymbols, lastAuthorByPath)
	if err != nil {
		return ReviewerReport{}, err
	}

	// (6) Fold into the composite over the union of every reviewer that earned any
	// signal. Deterministic union enumeration (sorted) feeds the stable total order.
	reviewers := map[string]struct{}{}
	for a := range ownershipPts {
		reviewers[a] = struct{}{}
	}
	for a := range recencyPts {
		reviewers[a] = struct{}{}
	}
	for a := range proximityPts {
		reviewers[a] = struct{}{}
	}
	candidates := make([]ReviewerCandidate, 0, len(reviewers))
	for _, rv := range sortedStrSetMap(reviewers) {
		own := ownershipPts[rv]
		rec := recencyPts[rv]
		prox := proximityPts[rv]
		composite := weights.Ownership*own + weights.RecencyChurn*rec + weights.Proximity*prox
		candidates = append(candidates, ReviewerCandidate{
			Reviewer:  rv,
			Composite: composite,
			Signals: ReviewerSignalBreakdown{
				Ownership:           own,
				RecencyDecayedChurn: rec,
				SubgraphProximity:   prox,
			},
		})
	}
	sortReviewerCandidates(candidates)
	report.Candidates = candidates
	if len(candidates) > 0 {
		report.Outcome = string(query.OutcomeFound)
	}
	return report, nil
}

// proximity attributes affected-subgraph proximity weight to reviewers. For each
// touched symbol it gathers its dependency-graph neighbors (callers/callees/
// contract neighbors), and for each distinct neighbor symbol it credits the
// neighbor file's owner (last author) with 1 + the neighbor's centrality bucket.
// Built once over the whole graph; pure local reads, zero egress.
func (a suggestReviewersAnalyzer) proximity(ctx context.Context, r query.Reader, touched map[model.NodeId]struct{}, lastAuthorByPath map[string]string) (map[string]int, error) {
	out := map[string]int{}
	if len(touched) == 0 || len(lastAuthorByPath) == 0 {
		return out, nil
	}

	nodes, err := r.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return nil, err
	}
	fileOf := make(map[model.NodeId]string, len(nodes))
	for _, n := range nodes {
		fileOf[n.ID()] = model.NormalizePath(n.SourcePath())
	}

	edges, err := r.Edges(ctx, graphstore.Query{})
	if err != nil {
		return nil, err
	}
	want := make(map[string]struct{}, len(dependencyKinds))
	for _, k := range dependencyKinds {
		want[k] = struct{}{}
	}
	// Undirected dependency-neighbor adjacency: a touched symbol's neighbors are
	// both the targets it points at (callees / referenced contracts) and the
	// sources pointing at it (callers / dependents).
	neigh := map[model.NodeId]map[model.NodeId]struct{}{}
	add := func(x, y model.NodeId) {
		if neigh[x] == nil {
			neigh[x] = map[model.NodeId]struct{}{}
		}
		neigh[x][y] = struct{}{}
	}
	for _, e := range edges {
		if _, ok := want[e.Kind()]; !ok {
			continue
		}
		add(e.From(), e.To())
		add(e.To(), e.From())
	}

	// Centrality buckets ONCE (reuse the metrics analyzer; never recompute).
	metricRes, err := (metricsAnalyzer{}).Analyze(ctx, r, Params{})
	if err != nil {
		return nil, err
	}
	centByNode := buildCentralityBuckets(metricRes.Metrics)

	// Collect the distinct affected-subgraph neighbor set (excluding the touched
	// symbols themselves), then attribute each neighbor's weight to its file owner.
	seen := map[model.NodeId]struct{}{}
	touchedIDs := sortedIDSetMap(touched)
	for _, s := range touchedIDs {
		nbrs := neigh[s]
		ids := make([]model.NodeId, 0, len(nbrs))
		for id := range nbrs {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, nb := range ids {
			if _, isTouched := touched[nb]; isTouched {
				continue
			}
			if _, dup := seen[nb]; dup {
				continue
			}
			seen[nb] = struct{}{}
			owner := lastAuthorByPath[fileOf[nb]]
			if owner == "" {
				continue // neighbor's file has no known author: not attributable
			}
			out[owner] += 1 + centByNode[nb]
		}
	}
	return out, nil
}

// decayBucket maps a commit's age (reference − commit time) to a small integer
// recency weight. Recent commits weigh more; integer-only (no float drift), with
// fixed, documented, version-pinned thresholds so the determination is byte-stable.
// A commit at or after the reference (or a zero reference) is treated as age 0.
func decayBucket(reference, commit time.Time) int {
	if reference.IsZero() || commit.IsZero() {
		return 0
	}
	age := reference.Sub(commit)
	if age < 0 {
		age = 0
	}
	switch {
	case age <= 7*24*time.Hour:
		return 4
	case age <= 30*24*time.Hour:
		return 3
	case age <= 90*24*time.Hour:
		return 2
	case age <= 365*24*time.Hour:
		return 1
	default:
		return 0
	}
}

// sortReviewerCandidates enforces the TOTAL ORDER: composite DESC, then reviewer
// identity ASC. This is the ONLY place candidate ordering is decided; combined
// with MarshalReviewers it makes the report byte-identical regardless of
// map-iteration order, and the identity tie-break guarantees equal-composite
// reviewers retain a fixed position.
func sortReviewerCandidates(cs []ReviewerCandidate) {
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].Composite != cs[j].Composite {
			return cs[i].Composite > cs[j].Composite
		}
		return cs[i].Reviewer < cs[j].Reviewer
	})
}

// sortedStrSet returns the sorted keys of a string set (never nil).
func sortedStrSet(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortedStrSetMap is sortedStrSet for a reviewer-identity set (same behavior;
// named distinctly for call-site clarity).
func sortedStrSetMap(m map[string]struct{}) []string { return sortedStrSet(m) }

// sortedIDSet returns the sorted string forms of a NodeId set (never nil).
func sortedIDSet(m map[model.NodeId]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, string(k))
	}
	sort.Strings(out)
	return out
}

// sortedIDSetMap returns the sorted NodeId keys of a set (never nil).
func sortedIDSetMap(m map[model.NodeId]struct{}) []model.NodeId {
	out := make([]model.NodeId, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// MarshalReviewers is the single canonical serializer for a ReviewerReport, shared
// by every surface. It re-sorts defensively (total order), disables HTML escaping,
// and trims the trailing newline — byte-for-byte stable across runs and surfaces
// (mirrors MarshalRisk / MarshalTriage / MarshalConflicts). Empty slices are
// materialized (never null) so the shape is stable. No timestamp / wall-clock /
// float / map-iteration leakage (the granularity map is encoded by Go's JSON
// encoder in sorted key order).
func MarshalReviewers(rep ReviewerReport) ([]byte, error) {
	out := rep
	cs := make([]ReviewerCandidate, len(rep.Candidates))
	copy(cs, rep.Candidates)
	sortReviewerCandidates(cs)
	if cs == nil {
		cs = []ReviewerCandidate{}
	}
	out.Candidates = cs

	ts := make([]string, len(rep.TouchedSymbols))
	copy(ts, rep.TouchedSymbols)
	sort.Strings(ts)
	if ts == nil {
		ts = []string{}
	}
	out.TouchedSymbols = ts

	tf := make([]string, len(rep.TouchedFiles))
	copy(tf, rep.TouchedFiles)
	sort.Strings(tf)
	if tf == nil {
		tf = []string{}
	}
	out.TouchedFiles = tf

	if out.SignalGranularity == nil {
		out.SignalGranularity = reviewerSignalGranularity
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, fmt.Errorf("analysis: marshal reviewer report: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
