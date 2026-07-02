package analysis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis/taint"
	"github.com/samibel/graphi/engine/query"
)

// PriskAnalyzerName is the dispatch key for the PR-risk scorer in the registry.
// EP-007 story 1/5 (SW-039): a composite, read-only scorer that combines EP-004
// impact signals with EP-005 taint/PDG signals into a deterministic per-region
// risk record. It NEVER recomputes impact or taint — it consumes their results
// through the signalProvider seam.
const PriskAnalyzerName = "pr-risk"

// RiskSchemaVersion versions the RiskReport JSON shape. Bump when the shape
// changes (e.g. a new evidence kind), so downstream stories (SW-040..042) can
// pin a known contract. The degraded record is a documented VARIANT of this
// same schema, not a separate error shape.
const RiskSchemaVersion = 1

// ScorerVersion identifies the scoring LOGIC version. It is echoed in every
// record so a stored/audited score can be tied to the algorithm that produced
// it (determinism is an integrity property — the score feeds the SW-042 gate).
const ScorerVersion = "pr-risk/1"

// MaxDiffBytes bounds the size of an accepted unified-diff payload. The PR diff
// is UNTRUSTED input; a hostile/huge diff must not be able to OOM the scorer.
// Mirrors the bounded-output discipline of DefaultMaxNodes.
const MaxDiffBytes = 4 << 20 // 4 MiB

// scoreScale is the fixed-point denominator: scores are stored as integers in
// units of 1/scoreScale and rendered canonically (e.g. 730 -> "0.730"), so the
// contract is byte-stable across runs and machines (no float formatting drift).
const scoreScale = 1000

// Evidence kinds — a stable, enumerated, versioned vocabulary. Adding a kind is
// a RiskSchemaVersion bump. Consumers switch on these rather than string-parse.
const (
	EvidenceImpactBlastRadius = "impact-blast-radius"
	EvidenceImpactCentrality  = "impact-centrality"
	EvidenceTaintPath         = "taint-path"
	EvidenceTruncation        = "truncation"
	EvidenceUnresolved        = "unresolved"
)

// Provenance/redaction levels. "full" carries verbatim taint source/sink/step
// provenance; "summary" lets downstream publishers (SW-042 PR comment) emit a
// non-sensitive readout without leaking sensitive code paths.
const (
	ProvenanceFull    = "full"
	ProvenanceSummary = "summary"
)

// Confidence levels for a record. Reduced (from "high") when a consumed impact
// signal is Truncated — a truncated impact set must surface as reduced
// confidence, never a silently lower score.
const (
	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
)

// weightTable is the DOCUMENTED, fixed scoring weight model. It is hashed into
// weights_hash so any change is auditable and reproducible.
//
//		score = impactFloor(bucket) + taintTerm(exposed)
//
//	  - impactFloor maps the discrete impact bucket to a base score; it is the
//	    impact-only FLOOR — a missing taint signal never drops a region below it
//	    (taint absence != safe).
//	  - taintTerm is strictly positive only when the region is taint-exposed, so
//	    at EQUAL impact bucket a taint-exposed region scores STRICTLY higher.
type weightTable struct {
	// BucketStep is the fixed-point score awarded per impact bucket.
	BucketStep int `json:"bucket_step"`
	// TaintTerm is the strictly-positive fixed-point bonus for taint exposure.
	TaintTerm int `json:"taint_term"`
	// MaxBucket caps the impact bucket so the score never overflows the [0,1]
	// fixed-point range even at max-risk (impact + taint).
	MaxBucket int `json:"max_bucket"`
	// CentralityStep is the per-bucketed-centrality fixed-point increment folded
	// into the impact floor (blast radius is the primary signal; centrality is a
	// secondary additive signal).
	CentralityStep int `json:"centrality_step"`
}

// defaultWeights is the fixed weight model for ScorerVersion "pr-risk/1".
// Chosen so the score stays within [0,1] fixed-point at the MaxBucket+taint
// maximum: 6*100 + 6*30 + 200 = 980 < 1000.
var defaultWeights = weightTable{
	BucketStep:     100,
	TaintTerm:      200,
	MaxBucket:      6,
	CentralityStep: 30,
}

// weightsHash returns the deterministic fingerprint of the weight model. Echoed
// in every record (mirrors taint.ConfigHash / model.IdentitySchemaVersion).
func (w weightTable) hash() string {
	b, _ := json.Marshal(w)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// EvidenceItem is one self-describing, enumerated piece of evidence contributing
// to a region's score. Provenance is copied VERBATIM from the consumed signal
// (never re-derived), matching the analyzer-wide provenance rule. Optional
// fields stay omitted per kind so the JSON is compact and stable.
type EvidenceItem struct {
	Kind   string   `json:"kind"`             // enumerated evidence kind
	Tier   string   `json:"tier,omitempty"`   // provenance confidence tier (verbatim)
	Reason string   `json:"reason,omitempty"` // verbatim reason / human note
	Score  string   `json:"score,omitempty"`  // fixed-point rendered contribution
	Source string   `json:"source,omitempty"` // taint source (full provenance only)
	Sink   string   `json:"sink,omitempty"`   // taint sink (full provenance only)
	Steps  []string `json:"steps,omitempty"`  // taint path steps (full provenance only)
	Refs   []string `json:"refs,omitempty"`   // referenced node/edge ids
}

// RiskRecord is the deterministic, versioned per-region risk score plus the
// evidence that produced it. The degraded/unresolved case is the SAME shape with
// Degraded=true and UnresolvedID set (a documented variant, not an error shape).
type RiskRecord struct {
	Region          model.NodeId   `json:"region"`                  // EP-001 identity key (empty when degraded)
	Score           string         `json:"score"`                   // canonical fixed-point decimal string
	Confidence      string         `json:"confidence"`              // high | medium (reduced on truncation)
	Degraded        bool           `json:"degraded"`                // true when the region is unresolved
	UnresolvedID    string         `json:"unresolved_id,omitempty"` // raw changed-node ref when degraded
	ProvenanceLevel string         `json:"provenance_level"`        // full | summary (redaction gate)
	Evidence        []EvidenceItem `json:"evidence"`                // enumerated, provenance-verbatim
}

// RiskReport is the full versioned payload emitted over MCP(stdio) and CLI.
// Regions are emitted in canonical model.NodeId-sorted order (degraded records,
// keyed by unresolved id, sort after resolved ones) for byte-stable output.
type RiskReport struct {
	SchemaVersion         int          `json:"schema_version"`
	ScorerVersion         string       `json:"scorer_version"`
	WeightsHash           string       `json:"weights_hash"`
	IdentitySchemaVersion uint32       `json:"identity_schema_version"`
	Outcome               string       `json:"outcome"` // found | empty (empty diff / all-unresolved still completes)
	Regions               []RiskRecord `json:"regions"`
}

// signalProvider is the injectable seam through which the scorer consumes EP-004
// impact/metrics and EP-005 taint RESULTS without recomputing them and without
// analyzer-to-analyzer hard coupling. The default implementation wraps the real
// impact analyzer and taint analyzer; unit tests inject a stub so the combiner
// is exercised with fixed signals.
type signalProvider interface {
	// Impact returns the EP-004 impact result for a resolved region symbol. A
	// missing symbol must be returned as a not-found Analysis, never an error.
	Impact(ctx context.Context, r query.Reader, region model.NodeId) (Analysis, error)
	// Taint returns the EP-005 taint result for the whole graph once (the scorer
	// indexes findings by node id). It is computed at most once per scoring run.
	Taint(ctx context.Context, r query.Reader) (taint.TaintResult, error)
}

// defaultSignalProvider wraps the real EP-004/EP-005 analyzers. It RUNS them
// (consuming their results) but the scorer itself never re-implements traversal
// or taint propagation — that logic stays in impact.go / taint.
type defaultSignalProvider struct {
	impact impactAnalyzer
	taint  *taint.Analyzer
}

func newDefaultSignalProvider() defaultSignalProvider {
	return defaultSignalProvider{
		impact: impactAnalyzer{},
		taint:  taint.New(taint.DefaultConfig(), taint.DefaultCaps(), nil),
	}
}

func (p defaultSignalProvider) Impact(ctx context.Context, r query.Reader, region model.NodeId) (Analysis, error) {
	return p.impact.Analyze(ctx, r, Params{Symbol: region, Direction: Forward})
}

func (p defaultSignalProvider) Taint(ctx context.Context, r query.Reader) (taint.TaintResult, error) {
	return p.taint.Run(ctx, r)
}

// priskAnalyzer is the registered PR-risk scorer. It holds only the injectable
// signal seam and the (fixed) weight model; it is stateless per call and safe
// for concurrent use (read-only Reader, no mutation).
type priskAnalyzer struct {
	provider signalProvider
	weights  weightTable
}

// newPriskAnalyzer builds the production scorer wired to the real signals.
func newPriskAnalyzer() priskAnalyzer {
	return priskAnalyzer{provider: newDefaultSignalProvider(), weights: defaultWeights}
}

func (priskAnalyzer) Name() string { return PriskAnalyzerName }

// Analyze maps the PR diff (carried in Params.Diff) onto the graph, scores each
// changed region by combining consumed impact + taint signals, and returns a
// versioned RiskReport carried on the generic Analysis envelope's RiskReport
// field. It never fails on an unresolved region (it emits a degraded record) and
// performs ZERO outbound network activity (pure graph reads).
func (a priskAnalyzer) Analyze(ctx context.Context, r query.Reader, p Params) (Analysis, error) {
	prov := a.provider
	if prov == nil {
		prov = newDefaultSignalProvider()
	}
	weights := a.weights
	if weights == (weightTable{}) {
		weights = defaultWeights
	}

	report, err := a.score(ctx, r, p, prov, weights)
	if err != nil {
		return Analysis{}, err
	}
	return Analysis{
		Analyzer:   PriskAnalyzerName,
		Outcome:    query.Outcome(report.Outcome),
		Symbol:     p.Symbol,
		RiskReport: &report,
	}, nil
}

// changedRef is one changed node parsed from the diff: a file path and a
// qualified-name (or line) hint used to resolve it to a model.NodeId.
type changedRef struct {
	raw  string // the verbatim reference for degraded reporting
	file string // normalized repo-relative path
	name string // qualified-name hint (may be empty)
	line int    // 1-based line hint (0 = unknown)
}

// score is the core combiner. It is split out so it can be unit-tested directly
// and so Analyze stays a thin adapter.
func (a priskAnalyzer) score(ctx context.Context, r query.Reader, p Params, prov signalProvider, weights weightTable) (RiskReport, error) {
	provLevel := provenanceLevel(p)
	wh := weights.hash()

	report := RiskReport{
		SchemaVersion:         RiskSchemaVersion,
		ScorerVersion:         ScorerVersion,
		WeightsHash:           wh,
		IdentitySchemaVersion: model.IdentitySchemaVersion,
		Outcome:               string(query.OutcomeEmpty),
		Regions:               []RiskRecord{},
	}

	refs, err := parseDiff(p.Diff)
	if err != nil {
		return RiskReport{}, err
	}
	if len(refs) == 0 {
		return report, nil // empty diff: completes, no regions, outcome=empty
	}

	// Resolve each changed ref to a NodeId (or mark it unresolved). Duplicate
	// changed nodes collapse deterministically to one region (merged evidence).
	resolved := map[model.NodeId][]changedRef{}
	var resolvedOrder []model.NodeId
	unresolved := map[string]struct{}{}
	var unresolvedOrder []string

	for _, ref := range refs {
		id, ok, err := resolveRef(ctx, r, ref)
		if err != nil {
			return RiskReport{}, err
		}
		if !ok {
			if _, seen := unresolved[ref.raw]; !seen {
				unresolved[ref.raw] = struct{}{}
				unresolvedOrder = append(unresolvedOrder, ref.raw)
			}
			continue
		}
		if _, seen := resolved[id]; !seen {
			resolvedOrder = append(resolvedOrder, id)
		}
		resolved[id] = append(resolved[id], ref)
	}

	// Consume the taint result ONCE (EP-005), indexed by the node ids that lie
	// on any source->sink path, with the verbatim Finding for provenance.
	taintRes, err := prov.Taint(ctx, r)
	if err != nil {
		return RiskReport{}, err
	}
	taintByNode := indexTaint(taintRes)

	records := make([]RiskRecord, 0, len(resolvedOrder)+len(unresolvedOrder))

	for _, id := range resolvedOrder {
		rec, err := a.scoreRegion(ctx, r, id, prov, weights, taintByNode, provLevel)
		if err != nil {
			return RiskReport{}, err
		}
		records = append(records, rec)
	}

	// Unresolved refs become degraded, flagged records (documented variant of the
	// same schema), never failures.
	for _, raw := range unresolvedOrder {
		records = append(records, RiskRecord{
			Region:          "",
			Score:           renderFixed(0),
			Confidence:      ConfidenceMedium,
			Degraded:        true,
			UnresolvedID:    raw,
			ProvenanceLevel: provLevel,
			Evidence: []EvidenceItem{{
				Kind:   EvidenceUnresolved,
				Reason: "changed node could not be resolved to the indexed graph",
				Refs:   []string{raw},
			}},
		})
	}

	sortRiskRecords(records)
	report.Regions = records
	if len(records) > 0 {
		report.Outcome = string(query.OutcomeFound)
	}
	return report, nil
}

// scoreRegion scores a single resolved region by consuming its impact result and
// the pre-indexed taint exposure, applying the monotonic combiner.
func (a priskAnalyzer) scoreRegion(
	ctx context.Context,
	r query.Reader,
	id model.NodeId,
	prov signalProvider,
	weights weightTable,
	taintByNode map[model.NodeId]taint.Finding,
	provLevel string,
) (RiskRecord, error) {
	imp, err := prov.Impact(ctx, r, id)
	if err != nil {
		return RiskRecord{}, err
	}

	evidence := make([]EvidenceItem, 0, 4)
	confidence := ConfidenceHigh

	// Impact bucket: blast-radius size is the primary signal; bucket it so
	// "equal impact" is well-defined and the strict taint tie-break is provable.
	bucket := blastRadiusBucket(len(imp.Nodes), weights.MaxBucket)
	if len(imp.Nodes) > 0 {
		ev := EvidenceItem{
			Kind:   EvidenceImpactBlastRadius,
			Reason: fmt.Sprintf("blast radius of %d dependent node(s) (bucket %d)", len(imp.Nodes), bucket),
		}
		// Copy the best (leading, already canonically sorted) reaching-edge tier
		// and a bounded set of reached-node ids VERBATIM as provenance.
		ev.Tier = string(imp.Nodes[0].ReachedVia.Tier)
		ev.Refs = boundedNodeRefs(imp.Nodes)
		evidence = append(evidence, ev)
	}

	// Centrality: a secondary additive impact signal (EP-004 metrics NodeScore).
	centBucket := centralityBucket(imp.Metrics, id)
	if centBucket > 0 {
		evidence = append(evidence, EvidenceItem{
			Kind:   EvidenceImpactCentrality,
			Score:  renderFixed(centBucket * weights.CentralityStep),
			Reason: fmt.Sprintf("centrality signal (bucket %d)", centBucket),
		})
	}

	// Truncation: a truncated impact set lowers confidence and is NAMED in
	// evidence, never a silently lower score.
	if imp.Truncated {
		confidence = ConfidenceMedium
		evidence = append(evidence, EvidenceItem{
			Kind:   EvidenceTruncation,
			Reason: "consumed impact signal was truncated; confidence reduced",
		})
	}

	// Taint exposure (EP-005): strictly-positive term, applied ONLY when the
	// region lies on a source->sink path. Provenance copied verbatim, redacted
	// to a summary when the provenance level is "summary".
	taintExposed := false
	if f, ok := taintByNode[id]; ok {
		taintExposed = true
		evidence = append(evidence, taintEvidence(f, provLevel))
	}

	// Monotonic combiner: impact-only floor + strictly-positive taint term.
	raw := impactFloor(bucket, centBucket, weights)
	if taintExposed {
		raw += weights.TaintTerm
	}
	if raw > scoreScale {
		raw = scoreScale
	}

	return RiskRecord{
		Region:          id,
		Score:           renderFixed(raw),
		Confidence:      confidence,
		Degraded:        false,
		ProvenanceLevel: provLevel,
		Evidence:        evidence,
	}, nil
}

// impactFloor is the impact-only score floor for a region: the bucketed blast
// radius plus the bucketed centrality. A missing taint signal never reduces a
// region below this value (taint absence != safe).
func impactFloor(bucket, centBucket int, w weightTable) int {
	return bucket*w.BucketStep + centBucket*w.CentralityStep
}

// blastRadiusBucket maps a blast-radius node count to a discrete integer bucket
// in [0, maxBucket]. Bucketing makes "equal impact" well-defined and the strict
// taint tie-break robust against float/size jitter. Thresholds are powers-of-two
// style boundaries (documented, deterministic).
func blastRadiusBucket(n, maxBucket int) int {
	bucket := 0
	switch {
	case n <= 0:
		bucket = 0
	case n <= 1:
		bucket = 1
	case n <= 3:
		bucket = 2
	case n <= 7:
		bucket = 3
	case n <= 15:
		bucket = 4
	case n <= 31:
		bucket = 5
	default:
		bucket = 6
	}
	if bucket > maxBucket {
		bucket = maxBucket
	}
	return bucket
}

// centralityBucket extracts the region's centrality score (if any) from the
// consumed metrics and buckets it to a small integer. Returns 0 when the region
// carries no centrality signal.
func centralityBucket(metrics []NodeScore, id model.NodeId) int {
	for _, m := range metrics {
		if m.Node.ID != id {
			continue
		}
		switch {
		case m.Score <= 0:
			return 0
		case m.Score < 0.25:
			return 1
		case m.Score < 0.5:
			return 2
		default:
			return 3
		}
	}
	return 0
}

// boundedNodeRefs copies up to a bounded number of reached-node ids verbatim as
// provenance refs. Bounding keeps the record size stable and the output token
// cost controlled even for a huge blast radius.
func boundedNodeRefs(nodes []ReachedNode) []string {
	const maxRefs = 8
	out := make([]string, 0, maxRefs)
	for i, rn := range nodes {
		if i >= maxRefs {
			break
		}
		out = append(out, string(rn.Node.ID))
	}
	return out
}

// indexTaint builds a node-id -> Finding index over every node that lies on any
// taint source->sink path. When several findings touch a node, the canonical
// (sorted) first finding is kept so the chosen provenance is deterministic.
func indexTaint(res taint.TaintResult) map[model.NodeId]taint.Finding {
	out := map[model.NodeId]taint.Finding{}
	// Stable order: sort findings by source, then sink, then path length.
	findings := make([]taint.Finding, len(res.Findings))
	copy(findings, res.Findings)
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.SourceID != b.SourceID {
			return a.SourceID < b.SourceID
		}
		if a.SinkID != b.SinkID {
			return a.SinkID < b.SinkID
		}
		return a.PathLength < b.PathLength
	})
	for _, f := range findings {
		mark := func(id model.NodeId) {
			if id == "" {
				return
			}
			if _, seen := out[id]; !seen {
				out[id] = f
			}
		}
		mark(f.SourceID)
		mark(f.SinkID)
		for _, st := range f.Path {
			mark(st.NodeID)
		}
	}
	return out
}

// taintEvidence builds a taint-path evidence item, copying provenance verbatim
// at "full" level and redacting source/sink/steps at "summary" level so a
// downstream publisher (SW-042) can emit a non-sensitive readout by default.
func taintEvidence(f taint.Finding, level string) EvidenceItem {
	ev := EvidenceItem{
		Kind:   EvidenceTaintPath,
		Reason: "changed region lies on a source->sink taint path",
	}
	if len(f.Path) > 0 {
		ev.Tier = string(f.Path[0].Tier)
	}
	if level == ProvenanceSummary {
		// Redacted: only the fact of exposure and the path length, no names/paths.
		ev.Reason = fmt.Sprintf("changed region lies on a taint path (length %d) [provenance redacted]", f.PathLength)
		return ev
	}
	ev.Source = f.SourceName
	ev.Sink = f.SinkName
	steps := make([]string, 0, len(f.Path))
	for _, st := range f.Path {
		steps = append(steps, string(st.NodeID))
	}
	ev.Steps = steps
	return ev
}

// provenanceLevel reads the requested redaction level from Params.Concept-free
// dedicated field. "summary" redacts; anything else is full.
func provenanceLevel(p Params) string {
	if strings.EqualFold(strings.TrimSpace(p.Provenance), ProvenanceSummary) {
		return ProvenanceSummary
	}
	return ProvenanceFull
}

// resolveRef maps a changed ref onto a model.NodeId using EP-001 identity. It
// resolves by scanning the graph's nodes for one whose normalized source path
// matches the changed file and whose qualified name / line hint matches. An
// unmatched ref returns ok=false (-> degraded record), NEVER an error.
func resolveRef(ctx context.Context, r query.Reader, ref changedRef) (model.NodeId, bool, error) {
	// Fast path: the ref already IS a node id (16-char lowercase hex) present in
	// the graph. This keeps the contract simple for callers that pre-resolve.
	if looksLikeNodeID(ref.raw) {
		if _, err := r.GetNode(ctx, model.NodeId(ref.raw)); err == nil {
			return model.NodeId(ref.raw), true, nil
		} else if !errors.Is(err, graphstore.ErrNotFound) {
			return "", false, err
		}
	}

	if ref.file == "" {
		return "", false, nil
	}
	wantPath := model.NormalizePath(ref.file)

	nodes, err := r.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return "", false, err
	}
	var best model.Node
	found := false
	for _, n := range nodes {
		if n.SourcePath() != wantPath {
			continue
		}
		// Name hint: prefer an exact qualified-name match.
		if ref.name != "" {
			if n.QualifiedName() == ref.name || strings.HasSuffix(n.QualifiedName(), "."+ref.name) {
				return n.ID(), true, nil
			}
			continue
		}
		// Line hint: pick the node whose line is the closest at-or-before the
		// changed line (deterministic: ties broken by canonical node id).
		if ref.line > 0 {
			if n.Line() <= ref.line {
				if !found || n.Line() > best.Line() ||
					(n.Line() == best.Line() && n.ID() < best.ID()) {
					best = n
					found = true
				}
			}
			continue
		}
		// No name or line hint: file-level match — pick the canonical-lowest id.
		if !found || n.ID() < best.ID() {
			best = n
			found = true
		}
	}
	if found {
		return best.ID(), true, nil
	}
	return "", false, nil
}

// looksLikeNodeID reports whether s has the shape of a model NodeId (16-char
// lowercase hex). Pure string check, no I/O.
func looksLikeNodeID(s string) bool {
	if len(s) != 16 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// renderFixed renders an integer fixed-point score (units of 1/scoreScale) as a
// canonical 3-decimal string (e.g. 730 -> "0.730"). Byte-stable across runs.
func renderFixed(v int) string {
	if v < 0 {
		v = 0
	}
	whole := v / scoreScale
	frac := v % scoreScale
	return strconv.Itoa(whole) + "." + fmt.Sprintf("%03d", frac)
}

// sortRiskRecords orders records canonically: resolved (non-degraded) regions
// first in NodeId order, then degraded records in unresolved-id order. This is
// the ONLY place record ordering is decided; combined with MarshalRisk it makes
// the report byte-identical regardless of map-iteration order.
func sortRiskRecords(recs []RiskRecord) {
	sort.SliceStable(recs, func(i, j int) bool {
		a, b := recs[i], recs[j]
		if a.Degraded != b.Degraded {
			return !a.Degraded // resolved first
		}
		if a.Degraded {
			return a.UnresolvedID < b.UnresolvedID
		}
		return a.Region < b.Region
	})
}

// MarshalRisk is the single canonical serializer for a RiskReport, shared by
// every surface (CLI, MCP). It re-sorts defensively, disables HTML escaping, and
// trims the trailing newline — byte-for-byte stable across runs and surfaces
// (mirrors analysis.Marshal). Empty slices are materialized (never null) so the
// shape is stable.
func MarshalRisk(rep RiskReport) ([]byte, error) {
	out := rep
	recs := make([]RiskRecord, len(rep.Regions))
	copy(recs, rep.Regions)
	sortRiskRecords(recs)
	for i := range recs {
		if recs[i].Evidence == nil {
			recs[i].Evidence = []EvidenceItem{}
		}
	}
	if recs == nil {
		recs = []RiskRecord{}
	}
	out.Regions = recs

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, fmt.Errorf("analysis: marshal risk report: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
