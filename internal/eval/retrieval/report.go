package retrieval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// Versions. FormatVersion pins the JSON shape; HarnessVersion the measurement
// method (what a hit is, how it is charged, which seams run); ScorerVersion
// the arithmetic (Evaluate/Aggregate/PercentileInt64). They move
// independently, as in internal/evalreport, and a reader refuses a version it
// does not know rather than half-reading it.
// FormatVersion 2 added `dataset.query_ids` to the report citation (SW-258 review
// round 3). The field is load-bearing — `Reproduce` compares it against the dataset
// rebuilt from bytes, and that check is what catches a query removed coherently from
// dataset, report and raw. A v1 report has no such invariant to check, so it is
// REFUSED rather than read as if it did: accepting it would let the older, weaker
// shape pass a gate written for the stronger one.
const (
	FormatVersion  = 2
	HarnessVersion = "retrieval-eval/2"
	ScorerVersion  = "retrieval-aggregate/1"
)

// TokenizerID stamps the counter every token figure was taken with:
// internal/eval's whitespace tokenizer (strings.Fields).
const TokenizerID = "whitespace-fields-v1"

// HitContextWindowLines is how much source a hit is charged for under the
// token budgets: the window a reader opens after a search hit, from the
// hit's line for this many lines (clipped at end of file). It is a fixed
// rule, not a judgement-dependent one, so a baseline cannot be charged less
// for being right.
const HitContextWindowLines = 40

// TokenBudgets are the context budgets Recall is reported under (AC-3).
var TokenBudgets = []int{600, 1200, 2000}

// MatchingRule is the rule SpanMatches implements, stated in the report so
// the artifact explains its own scoring.
const MatchingRule = "a hit matches a judged span when hit.path == span.path and span.start_line <= hit.line <= span.end_line; " +
	"hit.line is the node's declaration line as the engine reports it (the oracle uses the span's start_line); " +
	"each judged span is credited once per ranking for recall and DCG"

// Report is the versioned artifact. Everything under Reproducible is a pure
// function of (candidate SHA, repository at its SHA, dataset bytes, harness +
// scorer version) and is byte-identical across runs; Performance carries the
// timing/RSS measures that vary and are recomputed from raw samples;
// Environment carries wall-clock and machine facts and is checked for
// presence only.
type Report struct {
	FormatVersion  int    `json:"format_version"`
	HarnessVersion string `json:"harness_version"`
	ScorerVersion  string `json:"scorer_version"`

	Reproducible Reproducible          `json:"reproducible"`
	Performance  []BaselinePerformance `json:"performance"`
	Environment  Environment           `json:"environment"`
}

// Reproducible is the deterministic section of the report.
type Reproducible struct {
	CandidateSHA string     `json:"candidate_sha"`
	RunnerClass  string     `json:"runner_class"`
	Repo         RepoRef    `json:"repo"`
	Dataset      DatasetRef `json:"dataset"`

	TokenizerID           string `json:"tokenizer_id"`
	TopK                  int    `json:"top_k"`
	TokenBudgets          []int  `json:"token_budgets"`
	HitContextWindowLines int    `json:"hit_context_window_lines"`
	RelevantMinGrade      int    `json:"relevant_min_grade"`
	MatchingRule          string `json:"matching_rule"`

	// SpanMethodShare (SW-260 AC-9) is the fraction of SemanticDocument v2
	// documents per span method — keys `ast` (exact parser span) and `window`
	// (the bounded fallback) — over the indexed files, so the fallback share is
	// visible in every eval run. Computed by the runner from the same parsers
	// and document builder `graphi index --semantic` uses (engine/embed);
	// omitted only by a report written before the field existed.
	SpanMethodShare map[string]float64 `json:"span_method_share,omitempty"`

	Baselines []BaselineResult `json:"baselines"`
}

// RepoRef names the measured checkout and the graph the index produced.
type RepoRef struct {
	Name  string `json:"name"`
	SHA   string `json:"sha,omitempty"`
	Nodes int    `json:"nodes"`
	Edges int    `json:"edges"`
	Files int    `json:"files"`
}

// DatasetRef identifies the dataset by content, not by path: its sha256
// (recomputed from the bytes by LoadDataset, never copied from an index), its
// counts and the sorted list of every query id it carries.
type DatasetRef struct {
	ID            string   `json:"id"`
	File          string   `json:"file"`
	SHA256        string   `json:"sha256"`
	EvidenceClass string   `json:"evidence_class"`
	Queries       int      `json:"queries"`
	Dev           int      `json:"dev_queries"`
	Holdout       int      `json:"holdout_queries"`
	QueryIDs      []string `json:"query_ids"`
}

// DatasetRefOf is THE dataset citation a report carries. The runner publishes
// it and the aggregate rebuilds it from the run directory's dataset copy and
// compares it exactly, so a report cannot cite one dataset and be scored
// against another. File is the label the run was given (the dataset's file
// name at run time) and is carried, not compared.
func DatasetRefOf(ds *Loaded, file string) DatasetRef {
	d := ds.Dataset
	ref := DatasetRef{ID: d.ID, File: file, SHA256: ds.SHA256, EvidenceClass: d.EvidenceClass,
		Queries: len(d.Queries), QueryIDs: make([]string, 0, len(d.Queries))}
	for _, q := range d.Queries {
		ref.QueryIDs = append(ref.QueryIDs, q.ID)
		if q.Split == SplitHoldout {
			ref.Holdout++
		} else {
			ref.Dev++
		}
	}
	sort.Strings(ref.QueryIDs)
	return ref
}

// Baseline statuses.
const (
	BaselineStatusOK          = "ok"
	BaselineStatusUnavailable = "unavailable"
)

// BaselineResult is one baseline's rankings and scores. An unavailable
// baseline (AC-6) carries its typed reason and no queries; its aggregates
// read UNKNOWN because nothing was measured, never zero.
type BaselineResult struct {
	Name   Baseline `json:"name"`
	Status string   `json:"status"`
	Reason string   `json:"reason,omitempty"`
	// Method names the engine seam and its version stamp, so a report says
	// what ranked, not only how well.
	Method  string        `json:"method"`
	Queries []QueryResult `json:"queries"`

	Overall AggregateMetrics            `json:"overall"`
	Strata  map[string]AggregateMetrics `json:"strata"`
	Splits  map[string]AggregateMetrics `json:"splits"`
}

// Measure is one performance figure with its status. A figure that could not
// be taken has Status UNKNOWN and no value (AC-4); one that does not apply to
// the baseline says so rather than reading zero.
type Measure struct {
	Status string   `json:"status"`
	Value  *float64 `json:"value,omitempty"`
	Unit   string   `json:"unit,omitempty"`
	Reason string   `json:"reason,omitempty"`
}

// StatusNotApplicable marks a measure the baseline has no instance of (the
// lexical index has no vector sidecar; the oracle builds no index).
const StatusNotApplicable = "not_applicable"

// Measured builds a measured Measure.
func Measured(v float64, unit string) Measure {
	return Measure{Status: StatusMeasured, Value: &v, Unit: unit}
}

// Unknown builds an UNKNOWN Measure with the reason it could not be taken.
func Unknown(reason string) Measure { return Measure{Status: StatusUnknown, Reason: reason} }

// NotApplicable builds a not_applicable Measure with the reason.
func NotApplicable(reason string) Measure {
	return Measure{Status: StatusNotApplicable, Reason: reason}
}

// BaselinePerformance is AC-4's per-baseline block.
type BaselinePerformance struct {
	Baseline           Baseline `json:"baseline"`
	IndexMS            Measure  `json:"index_ms"`
	QueryP50US         Measure  `json:"query_p50_us"`
	QueryP95US         Measure  `json:"query_p95_us"`
	LatencySamples     int      `json:"latency_samples"`
	PeakRSSMB          Measure  `json:"peak_rss_mb"`
	VectorSidecarBytes Measure  `json:"vector_sidecar_bytes"`
}

// Environment is the non-reproducible metadata: when and on what.
type Environment struct {
	GeneratedAt string `json:"generated_at"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	GoVersion   string `json:"go_version"`
	CPUCount    int    `json:"cpu_count"`
	Notes       string `json:"notes,omitempty"`
}

// requiredEnvironment lists the environment fields an aggregate needs
// present before a report is publishable, in report order. Missing is driven
// by this table, so the list and the check cannot drift apart.
var requiredEnvironment = []struct {
	name string
	get  func(Environment) string
}{
	{"generated_at", func(e Environment) string { return e.GeneratedAt }},
	{"os", func(e Environment) string { return e.OS }},
	{"arch", func(e Environment) string { return e.Arch }},
	{"go_version", func(e Environment) string { return e.GoVersion }},
}

// Missing lists the required environment fields that are empty, in
// requiredEnvironment order.
func (e Environment) Missing() []string {
	var out []string
	for _, f := range requiredEnvironment {
		if f.get(e) == "" {
			out = append(out, f.name)
		}
	}
	return out
}

// MarshalReport renders the report as stable indented JSON with a trailing
// newline. Struct field order fixes the key order; maps serialize sorted.
func MarshalReport(r *Report) ([]byte, error) {
	return marshalStable(r)
}

// ReproducibleBytes renders only the deterministic section, which is what
// two runs over the same inputs must agree on byte for byte.
func ReproducibleBytes(r *Report) ([]byte, error) {
	return marshalStable(r.Reproducible)
}

func marshalStable(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	// Report text is repository-controlled (paths, names); keeping < > & as
	// they are makes the artifact readable and diffable.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("retrieval: marshal report: %w", err)
	}
	return buf.Bytes(), nil
}

// CheckReportVersion refuses a report this build cannot interpret.
func CheckReportVersion(r *Report) error {
	if r.FormatVersion != FormatVersion {
		return fmt.Errorf("retrieval: report format_version %d is not the supported %d", r.FormatVersion, FormatVersion)
	}
	if r.HarnessVersion != HarnessVersion {
		return fmt.Errorf("retrieval: report harness_version %q is not %q; numbers from another method are not comparable", r.HarnessVersion, HarnessVersion)
	}
	if r.ScorerVersion != ScorerVersion {
		return fmt.Errorf("retrieval: report scorer_version %q is not %q", r.ScorerVersion, ScorerVersion)
	}
	return nil
}
