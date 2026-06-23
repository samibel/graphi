package analysis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis/githistory"
	"github.com/samibel/graphi/engine/analysis/pdg"
	"github.com/samibel/graphi/engine/query"
)

// PrSignalsAnalyzerName is the dispatch key for the hub/bridge/surprise signal
// detector in the registry. EP-007 story 2/5 (SW-040): a composite, read-only
// detector that annotates the changed nodes of a PR diff with graph signals
// derived from existing infrastructure. It NEVER recomputes centrality, PDG, or
// git history — it CONSUMES their results through the signalSource seam, exactly
// as the SW-039 pr-risk scorer consumes impact/taint through signalProvider.
const PrSignalsAnalyzerName = "pr-signals"

// SignalSchemaVersion versions the SignalReport JSON shape. Bump when the shape
// changes (e.g. a new signal kind or evidence field) so downstream stories
// (SW-041 questions, SW-042 gate) can pin a known contract. The degraded record
// is a documented VARIANT of this same schema, not a separate error shape.
const SignalSchemaVersion = 1

// DetectorVersion identifies the detection LOGIC version, echoed in every report
// so a stored/audited annotation can be tied to the algorithm that produced it
// (determinism is an integrity property — these signals feed the SW-042 gate).
const DetectorVersion = "pr-signals/1"

// Signal kinds — a stable, enumerated, versioned vocabulary. Consumers switch on
// these rather than string-parse. Adding a kind is a SignalSchemaVersion bump.
const (
	// SignalHub marks a changed node whose consumed centrality / fan-in-out
	// (degree) exceeds the configurable hub threshold.
	SignalHub = "hub"
	// SignalBridge marks a changed node that is an articulation point / cut
	// vertex between modules (removing it disconnects them).
	SignalBridge = "bridge"
	// SignalSurprise marks a changed region that is rarely modified (low churn)
	// or unexpectedly coupled (a dependence edge crossing module boundaries).
	SignalSurprise = "surprise"
)

// Surprise sub-reasons — the enumerated contributing reason for a surprise flag.
const (
	// SurpriseLowChurn: the region's file has historically low churn.
	SurpriseLowChurn = "surprise-low-churn"
	// SurpriseUnexpectedCoupling: the region has a dependence edge to a node in
	// a different source file / module than its own.
	SurpriseUnexpectedCoupling = "surprise-unexpected-coupling"
)

// signalConfig is the DOCUMENTED, fixed-by-default threshold model. It is hashed
// into ConfigHash so any change is auditable and reproducible (mirrors the
// SW-039 weightsHash discipline). Thresholds are configurable per AC1.
type signalConfig struct {
	// HubDegreeThreshold is the inclusive lower bound on a node's consumed hub
	// (undirected degree) score for it to be classified hub. Configurable.
	HubDegreeThreshold float64 `json:"hub_degree_threshold"`
	// SurpriseChurnMax is the inclusive upper bound on a region file's historical
	// commit count for it to be flagged surprise(low-churn). A file with no churn
	// signal at all (0 commits known) is NOT flagged, to avoid false positives on
	// graphs without git history; see lowChurnSurprise.
	SurpriseChurnMax int `json:"surprise_churn_max"`
}

// defaultSignalConfig is the fixed threshold model for DetectorVersion
// "pr-signals/1". HubDegreeThreshold=3 classifies a node with degree >= 3 as a
// hub (a clearly fanned-in/out node) while leaving leaves (degree 1) and small
// nodes unflagged. SurpriseChurnMax=1 treats a file touched at most once in the
// window as rarely modified.
var defaultSignalConfig = signalConfig{
	HubDegreeThreshold: 3,
	SurpriseChurnMax:   1,
}

// hash returns the deterministic fingerprint of the threshold model. Echoed in
// every report (mirrors weightTable.hash / model.IdentitySchemaVersion).
func (c signalConfig) hash() string {
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// SignalFlag is one detected signal on a changed region: an enumerated kind, a
// verbatim human-readable contributing reason, an optional finer detail (the
// surprise sub-reason), an optional fixed-point score, and bounded provenance
// refs. Optional fields stay omitted per kind so the JSON is compact and stable.
type SignalFlag struct {
	Kind   string   `json:"kind"`             // enumerated signal kind
	Reason string   `json:"reason"`           // verbatim contributing reason
	Detail string   `json:"detail,omitempty"` // surprise sub-reason (enumerated)
	Score  string   `json:"score,omitempty"`  // fixed-point rendered metric value
	Refs   []string `json:"refs,omitempty"`   // bounded node-id / path provenance
}

// SignalRecord is the deterministic per-region annotation: the EP-001 identity
// of the changed region plus the signals detected on it. The degraded/unresolved
// case is the SAME shape with Degraded=true and UnresolvedID set (a documented
// variant, not an error shape). A changed-but-unremarkable region is a record
// with an empty Signals list — it is still emitted so the caller sees coverage.
type SignalRecord struct {
	Region          model.NodeId `json:"region"`                  // EP-001 identity (empty when degraded)
	Degraded        bool         `json:"degraded"`                // true when the region is unresolved
	UnresolvedID    string       `json:"unresolved_id,omitempty"` // raw changed-node ref when degraded
	ProvenanceLevel string       `json:"provenance_level"`        // full | summary (redaction gate)
	Signals         []SignalFlag `json:"signals"`                 // enumerated, provenance-bounded
}

// SignalReport is the full versioned payload emitted over MCP(stdio) and CLI.
// Regions are emitted in canonical model.NodeId-sorted order (degraded records,
// keyed by unresolved id, sort after resolved ones) for byte-stable output.
type SignalReport struct {
	SchemaVersion         int            `json:"schema_version"`
	DetectorVersion       string         `json:"detector_version"`
	ConfigHash            string         `json:"config_hash"`
	IdentitySchemaVersion uint32         `json:"identity_schema_version"`
	Outcome               string         `json:"outcome"` // found | empty
	Regions               []SignalRecord `json:"regions"`
}

// signalSource is the injectable seam through which the detector consumes EP-004
// metrics (hub/bridge/centrality), EP-005 PDG (coupling), and git-history churn
// RESULTS without recomputing them and without analyzer-to-analyzer hard
// coupling. The default implementation wraps the real metrics analyzer, the PDG
// analyzer, and a git-history churn provider; unit tests inject a stub so the
// detector is exercised with fixed signals.
type signalSource interface {
	// Metrics returns the EP-004 metric NodeScores (hub, bridge, centrality) for
	// the whole graph once (the detector indexes by node id + kind). Computed at
	// most once per detection run.
	Metrics(ctx context.Context, r query.Reader) ([]NodeScore, error)
	// PDG returns the EP-005 program-dependence edges for the whole graph once
	// (the detector indexes cross-file edges by node id). Computed at most once.
	PDG(ctx context.Context, r query.Reader) (pdg.PDGResult, error)
	// Churn returns per-file historical churn scores. A nil/absent git provider
	// yields an empty slice (graceful: no low-churn surprises, never an error).
	Churn(ctx context.Context, r query.Reader) ([]githistory.ChurnScore, error)
}

// defaultSignalSource wraps the real EP-004 metrics analyzer, the EP-005 PDG
// analyzer, and a git-history churn provider. It RUNS them (consuming their
// results); the detector itself never re-implements degree, articulation-point,
// PDG, or git-log logic — that stays in metrics.go / pdg / githistory.
type defaultSignalSource struct {
	metrics metricsAnalyzer
	pdg     *pdg.Analyzer
	hist    *githistory.Analyzer
}

// newDefaultSignalSource builds the production source. The git provider is nil
// by default (no local git access on the hot path), so Churn returns empty and
// low-churn surprises are simply not produced — callers wanting churn signals
// inject a provider via newDefaultSignalSourceWithProvider.
func newDefaultSignalSource() defaultSignalSource {
	return defaultSignalSource{
		metrics: metricsAnalyzer{},
		pdg:     pdg.New(pdg.DefaultConfig()),
		hist:    githistory.New(nil, githistory.Config{}),
	}
}

func (s defaultSignalSource) Metrics(ctx context.Context, r query.Reader) ([]NodeScore, error) {
	a, err := s.metrics.Analyze(ctx, r, Params{})
	if err != nil {
		return nil, err
	}
	return a.Metrics, nil
}

func (s defaultSignalSource) PDG(ctx context.Context, r query.Reader) (pdg.PDGResult, error) {
	if s.pdg == nil {
		return pdg.PDGResult{}, nil
	}
	return s.pdg.Run(ctx, r)
}

func (s defaultSignalSource) Churn(ctx context.Context, _ query.Reader) ([]githistory.ChurnScore, error) {
	if s.hist == nil {
		return nil, nil
	}
	res, err := s.hist.Run(ctx)
	if err != nil {
		return nil, err
	}
	return res.ChurnScores, nil
}

// prSignalsAnalyzer is the registered hub/bridge/surprise detector. It holds only
// the injectable signal seam and the (fixed) threshold config; it is stateless
// per call and safe for concurrent use (read-only Reader, no mutation).
type prSignalsAnalyzer struct {
	source signalSource
	config signalConfig
}

// newPrSignalsAnalyzer builds the production detector wired to the real signals.
func newPrSignalsAnalyzer() prSignalsAnalyzer {
	return prSignalsAnalyzer{source: newDefaultSignalSource(), config: defaultSignalConfig}
}

func (prSignalsAnalyzer) Name() string { return PrSignalsAnalyzerName }

// Analyze maps the PR diff (carried in Params.Diff) onto the graph, annotates
// each changed region with hub/bridge/surprise signals derived from consumed
// metrics/PDG/churn, and returns a versioned SignalReport carried on the generic
// Analysis envelope's SignalReport field. It never fails on an unresolved region
// (it emits a degraded record) and performs ZERO outbound network activity
// (pure graph reads + injected, local signal sources).
func (a prSignalsAnalyzer) Analyze(ctx context.Context, r query.Reader, p Params) (Analysis, error) {
	src := a.source
	if src == nil {
		src = newDefaultSignalSource()
	}
	cfg := a.config
	if cfg == (signalConfig{}) {
		cfg = defaultSignalConfig
	}

	report, err := a.detect(ctx, r, p, src, cfg)
	if err != nil {
		return Analysis{}, err
	}
	return Analysis{
		Analyzer:     PrSignalsAnalyzerName,
		Outcome:      query.Outcome(report.Outcome),
		Symbol:       p.Symbol,
		SignalReport: &report,
	}, nil
}

// detect is the core annotator. It is split out so it can be unit-tested directly
// and so Analyze stays a thin adapter (mirrors priskAnalyzer.score / Analyze).
func (a prSignalsAnalyzer) detect(ctx context.Context, r query.Reader, p Params, src signalSource, cfg signalConfig) (SignalReport, error) {
	provLevel := provenanceLevel(p)

	report := SignalReport{
		SchemaVersion:         SignalSchemaVersion,
		DetectorVersion:       DetectorVersion,
		ConfigHash:            cfg.hash(),
		IdentitySchemaVersion: model.IdentitySchemaVersion,
		Outcome:               string(query.OutcomeEmpty),
		Regions:               []SignalRecord{},
	}

	refs, err := parseDiff(p.Diff)
	if err != nil {
		return SignalReport{}, err
	}
	if len(refs) == 0 {
		return report, nil // empty diff: completes, no regions, outcome=empty
	}

	// Resolve each changed ref to a NodeId (or mark it unresolved). Duplicate
	// changed nodes collapse deterministically to one region.
	resolved := map[model.NodeId]struct{}{}
	var resolvedOrder []model.NodeId
	unresolved := map[string]struct{}{}
	var unresolvedOrder []string

	for _, ref := range refs {
		id, ok, err := resolveRef(ctx, r, ref)
		if err != nil {
			return SignalReport{}, err
		}
		if !ok {
			if _, seen := unresolved[ref.raw]; !seen {
				unresolved[ref.raw] = struct{}{}
				unresolvedOrder = append(unresolvedOrder, ref.raw)
			}
			continue
		}
		if _, seen := resolved[id]; !seen {
			resolved[id] = struct{}{}
			resolvedOrder = append(resolvedOrder, id)
		}
	}

	// Consume each signal source EXACTLY ONCE (never per-region recompute) and
	// index by node id, mirroring how pr-risk consumes taint once via indexTaint.
	metricScores, err := src.Metrics(ctx, r)
	if err != nil {
		return SignalReport{}, err
	}
	hubByNode, bridgeByNode := indexMetrics(metricScores)

	pdgRes, err := src.PDG(ctx, r)
	if err != nil {
		return SignalReport{}, err
	}
	couplingByNode := indexCrossFileCoupling(ctx, r, pdgRes)

	churn, err := src.Churn(ctx, r)
	if err != nil {
		return SignalReport{}, err
	}
	churnByPath := indexChurn(churn)

	records := make([]SignalRecord, 0, len(resolvedOrder)+len(unresolvedOrder))

	for _, id := range resolvedOrder {
		rec, err := a.annotateRegion(ctx, r, id, cfg, provLevel, hubByNode, bridgeByNode, couplingByNode, churnByPath)
		if err != nil {
			return SignalReport{}, err
		}
		records = append(records, rec)
	}

	// Unresolved refs become degraded, flagged records (documented variant of the
	// same schema), never failures.
	for _, raw := range unresolvedOrder {
		records = append(records, SignalRecord{
			Region:          "",
			Degraded:        true,
			UnresolvedID:    raw,
			ProvenanceLevel: provLevel,
			Signals:         []SignalFlag{},
		})
	}

	sortSignalRecords(records)
	report.Regions = records
	if len(records) > 0 {
		report.Outcome = string(query.OutcomeFound)
	}
	return report, nil
}

// annotateRegion attaches the detected hub/bridge/surprise signals to a single
// resolved region by consuming the pre-indexed metric, coupling, and churn
// signals. It never recomputes any of them.
func (a prSignalsAnalyzer) annotateRegion(
	ctx context.Context,
	r query.Reader,
	id model.NodeId,
	cfg signalConfig,
	provLevel string,
	hubByNode map[model.NodeId]float64,
	bridgeByNode map[model.NodeId]struct{},
	couplingByNode map[model.NodeId][]string,
	churnByPath map[string]int,
) (SignalRecord, error) {
	signals := make([]SignalFlag, 0, 3)

	// Hub: consumed undirected-degree score over the configurable threshold.
	if deg, ok := hubByNode[id]; ok && deg >= cfg.HubDegreeThreshold {
		signals = append(signals, SignalFlag{
			Kind:   SignalHub,
			Reason: fmt.Sprintf("changed node has high fan-in/out (degree %s >= threshold %s)", renderDegree(deg), renderDegree(cfg.HubDegreeThreshold)),
			Score:  renderDegree(deg),
		})
	}

	// Bridge: consumed articulation-point set (removing the node disconnects the
	// modules it joins — the cut-vertex property the metrics analyzer computed).
	if _, ok := bridgeByNode[id]; ok {
		signals = append(signals, SignalFlag{
			Kind:   SignalBridge,
			Reason: "changed node is an articulation point / cut vertex between modules (removing it disconnects them)",
		})
	}

	// Surprise (unexpected coupling): a consumed PDG dependence edge whose other
	// endpoint lives in a DIFFERENT source file/module than the region.
	if endpoints, ok := couplingByNode[id]; ok && len(endpoints) > 0 {
		flag := SignalFlag{
			Kind:   SignalSurprise,
			Detail: SurpriseUnexpectedCoupling,
			Reason: "changed region is unexpectedly coupled to a different module via a program-dependence edge",
		}
		if provLevel != ProvenanceSummary {
			flag.Refs = endpoints
		} else {
			flag.Reason = fmt.Sprintf("changed region is unexpectedly coupled to %d node(s) in other modules [provenance redacted]", len(endpoints))
		}
		signals = append(signals, flag)
	}

	// Surprise (low churn): the region's file is rarely modified. Only flagged
	// when a churn signal for the file EXISTS and is at-or-below the threshold,
	// so graphs without git history never produce spurious low-churn surprises.
	if path, ok := regionPath(ctx, r, id); ok {
		if commits, known := churnByPath[path]; known && commits <= cfg.SurpriseChurnMax {
			flag := SignalFlag{
				Kind:   SignalSurprise,
				Detail: SurpriseLowChurn,
				Reason: fmt.Sprintf("changed region is in a rarely-modified file (%d commit(s) <= threshold %d)", commits, cfg.SurpriseChurnMax),
			}
			if provLevel != ProvenanceSummary {
				flag.Refs = []string{path}
			} else {
				flag.Reason = fmt.Sprintf("changed region is in a rarely-modified file (%d commit(s) <= threshold %d) [path redacted]", commits, cfg.SurpriseChurnMax)
			}
			signals = append(signals, flag)
		}
	}

	sortSignalFlags(signals)
	return SignalRecord{
		Region:          id,
		Degraded:        false,
		ProvenanceLevel: provLevel,
		Signals:         signals,
	}, nil
}

// indexMetrics splits the consumed metric NodeScores into a hub-degree index and
// a bridge (articulation-point) set, keyed by node id. The hub index keeps the
// node's degree score (MetricHub); the bridge set holds every articulation point
// (MetricBridge). Centrality is folded into hub via degree (degree IS the
// fan-in/out signal the AC names), so it is not separately indexed.
func indexMetrics(scores []NodeScore) (map[model.NodeId]float64, map[model.NodeId]struct{}) {
	hub := map[model.NodeId]float64{}
	bridge := map[model.NodeId]struct{}{}
	for _, s := range scores {
		switch s.Kind {
		case MetricHub:
			hub[s.Node.ID] = s.Score
		case MetricBridge:
			bridge[s.Node.ID] = struct{}{}
		}
	}
	return hub, bridge
}

// indexCrossFileCoupling builds a node-id -> []endpoint-id index over PDG
// dependence edges whose two endpoints live in DIFFERENT source files. The
// region's file is looked up from the graph; an endpoint in another file is
// recorded as an unexpected-coupling target. Same-file dependence is NOT
// surprising and is skipped. Endpoint lists are deterministically sorted and
// bounded so the output is byte-stable and token-bounded.
func indexCrossFileCoupling(ctx context.Context, r query.Reader, res pdg.PDGResult) map[model.NodeId][]string {
	// File of each PDG-participating node, taken from the PDG node metadata.
	fileOf := make(map[model.NodeId]string, len(res.Nodes))
	for _, n := range res.Nodes {
		fileOf[n.ID] = model.NormalizePath(n.SourcePath)
	}
	lookupFile := func(id model.NodeId) string {
		if f, ok := fileOf[id]; ok {
			return f
		}
		if n, err := r.GetNode(ctx, id); err == nil {
			f := model.NormalizePath(n.SourcePath())
			fileOf[id] = f
			return f
		}
		return ""
	}

	out := map[model.NodeId]map[string]struct{}{}
	add := func(a, b model.NodeId) {
		fa, fb := lookupFile(a), lookupFile(b)
		if fa == "" || fb == "" || fa == fb {
			return // unknown file or same-file coupling: not surprising
		}
		if out[a] == nil {
			out[a] = map[string]struct{}{}
		}
		out[a][string(b)] = struct{}{}
	}

	for _, e := range append(append([]pdg.DepEdge{}, res.DataDepEdges...), res.ControlDepEdges...) {
		add(e.From, e.To)
		add(e.To, e.From)
	}

	// Materialize into sorted, bounded slices for determinism.
	const maxEndpoints = 8
	idx := make(map[model.NodeId][]string, len(out))
	for id, set := range out {
		eps := make([]string, 0, len(set))
		for ep := range set {
			eps = append(eps, ep)
		}
		sort.Strings(eps)
		if len(eps) > maxEndpoints {
			eps = eps[:maxEndpoints]
		}
		idx[id] = eps
	}
	return idx
}

// indexChurn builds a normalized-path -> commit-count index from the consumed
// churn scores. Paths are normalized so they compare equal to the region path.
func indexChurn(scores []githistory.ChurnScore) map[string]int {
	out := make(map[string]int, len(scores))
	for _, c := range scores {
		out[model.NormalizePath(c.Path)] = c.Commits
	}
	return out
}

// regionPath returns the normalized source path of a resolved region node.
func regionPath(ctx context.Context, r query.Reader, id model.NodeId) (string, bool) {
	n, err := r.GetNode(ctx, id)
	if err != nil {
		return "", false
	}
	return model.NormalizePath(n.SourcePath()), true
}

// renderDegree renders a degree/score value as a compact, byte-stable decimal.
// Degrees are whole numbers in practice; rendering through strconv keeps the
// output stable across runs (no float formatting drift in the common case).
func renderDegree(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%.3f", v)
}

// signalKindRank fixes a stable ordering for signal kinds within a record (hub,
// then bridge, then surprise) so the serialized list is byte-stable regardless
// of detection order.
func signalKindRank(kind string) int {
	switch kind {
	case SignalHub:
		return 0
	case SignalBridge:
		return 1
	case SignalSurprise:
		return 2
	default:
		return 3
	}
}

// sortSignalFlags orders a record's flags by kind rank, then by detail, then by
// reason — a deterministic total order.
func sortSignalFlags(flags []SignalFlag) {
	sort.SliceStable(flags, func(i, j int) bool {
		a, b := flags[i], flags[j]
		if ra, rb := signalKindRank(a.Kind), signalKindRank(b.Kind); ra != rb {
			return ra < rb
		}
		if a.Detail != b.Detail {
			return a.Detail < b.Detail
		}
		return a.Reason < b.Reason
	})
}

// sortSignalRecords orders records canonically: resolved (non-degraded) regions
// first in NodeId order, then degraded records in unresolved-id order. This is
// the ONLY place record ordering is decided; combined with MarshalSignals it
// makes the report byte-identical regardless of map-iteration order.
func sortSignalRecords(recs []SignalRecord) {
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

// MarshalSignals is the single canonical serializer for a SignalReport, shared by
// every surface (CLI, MCP). It re-sorts defensively, disables HTML escaping, and
// trims the trailing newline — byte-for-byte stable across runs and surfaces
// (mirrors analysis.Marshal / MarshalRisk). Empty slices are materialized (never
// null) so the shape is stable.
func MarshalSignals(rep SignalReport) ([]byte, error) {
	out := rep
	recs := make([]SignalRecord, len(rep.Regions))
	copy(recs, rep.Regions)
	sortSignalRecords(recs)
	for i := range recs {
		if recs[i].Signals == nil {
			recs[i].Signals = []SignalFlag{}
		} else {
			sortSignalFlags(recs[i].Signals)
		}
	}
	if recs == nil {
		recs = []SignalRecord{}
	}
	out.Regions = recs

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, fmt.Errorf("analysis: marshal signal report: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
