package analysis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// PrQuestionsAnalyzerName is the dispatch key for the graph-derived reviewer
// question generator in the registry. EP-007 story 3/5 (SW-041): a composite,
// read-only, DETERMINISTIC generator that turns the upstream graph findings —
// the SW-039 pr-risk RiskReport and the SW-040 pr-signals SignalReport — into a
// fixed set of templated reviewer questions. It NEVER recomputes scoring or
// signal detection and NEVER calls an LLM or the network: it CONSUMES the two
// sibling REPORTS through the questionSource seam, exactly as pr-risk consumes
// impact/taint via signalProvider and pr-signals consumes metrics/pdg/churn via
// signalSource.
const PrQuestionsAnalyzerName = "pr-questions"

// QuestionSchemaVersion versions the QuestionReport JSON shape. Bump when the
// shape changes (e.g. a new question field or evidence field) so the downstream
// SW-042 comment/gate can pin a known contract.
const QuestionSchemaVersion = 1

// GeneratorVersion identifies the rule/template LOGIC version, echoed in every
// report so a stored/audited question set can be tied to the rule set that
// produced it (determinism is an integrity property — questions feed the SW-042
// gate).
const GeneratorVersion = "pr-questions/1"

// Rule ids — a stable, enumerated, versioned vocabulary identifying which fixed
// rule/template produced a question. Consumers (and ordering) switch on these
// rather than string-parse the text. Adding a rule id is a QuestionSchemaVersion
// bump.
const (
	// RuleHubCallers fires on a changed region carrying a SW-040 hub signal: many
	// callers depend on the symbol, so the reviewer is asked to confirm call-site
	// coverage.
	RuleHubCallers = "hub-callers"
	// RuleRiskSurprise fires on a changed region whose SW-039 risk score exceeds
	// the high-risk threshold AND which carries a SW-040 surprise signal: a risky
	// change in an unexpected place warrants a focused question.
	RuleRiskSurprise = "risk-surprise"
)

// questionConfig is the DOCUMENTED, fixed-by-default threshold model. It is
// hashed into ConfigHash so any change is auditable and reproducible (mirrors
// the SW-039 weightsHash / SW-040 signalConfig discipline).
type questionConfig struct {
	// HighRiskThreshold is the EXCLUSIVE lower bound, in fixed-point units (units
	// of 1/scoreScale, the same scale RiskRecord.Score is rendered in), a region's
	// risk score must EXCEED for the risk+surprise rule to fire. Configurable.
	HighRiskThreshold int `json:"high_risk_threshold"`
}

// defaultQuestionConfig is the fixed threshold model for GeneratorVersion
// "pr-questions/1". HighRiskThreshold=500 ("0.500") treats a region scoring
// strictly above the midpoint as high-risk — high enough that a plain
// impact-only floor does not trip it, low enough that an impact+taint or
// impact+centrality region does.
var defaultQuestionConfig = questionConfig{
	HighRiskThreshold: 500,
}

// hash returns the deterministic fingerprint of the threshold model. Echoed in
// every report (mirrors weightTable.hash / signalConfig.hash).
func (c questionConfig) hash() string {
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// EvidenceRef is the explicit, REQUIRED-non-empty reference back to the exact
// node/edge/signal that triggered a question. Every emitted ReviewerQuestion
// carries one whose isEmpty() is false (enforced at generation time and asserted
// by tests). Provenance fields are copied VERBATIM from the consumed sibling
// reports — never re-derived. Optional fields stay omitted so the JSON is
// compact and stable.
type EvidenceRef struct {
	Region    model.NodeId `json:"region"`               // EP-001 identity of the triggering region
	RiskScore string       `json:"risk_score,omitempty"` // verbatim fixed-point risk score (when risk-driven)
	Signals   []string     `json:"signals,omitempty"`    // triggering SW-040 signal kinds
	Refs      []string     `json:"refs,omitempty"`       // bounded node/edge id provenance (verbatim)
}

// isEmpty reports whether the reference carries NO usable provenance. A question
// with an empty evidence reference is a contract violation (AC1/AC3: every
// question must contain a non-empty evidence reference) and is never emitted.
func (e EvidenceRef) isEmpty() bool {
	return e.Region == "" && e.RiskScore == "" && len(e.Signals) == 0 && len(e.Refs) == 0
}

// ReviewerQuestion is one deterministic, template-produced reviewer question: the
// stable rule id that produced it, the region it concerns, the rendered question
// text, and the required non-empty evidence reference.
type ReviewerQuestion struct {
	RuleID   string       `json:"rule_id"`  // enumerated rule/template id
	Region   model.NodeId `json:"region"`   // EP-001 identity of the concerned region
	Text     string       `json:"text"`     // rendered, template-based question text
	Evidence EvidenceRef  `json:"evidence"` // required non-empty triggering reference
}

// QuestionReport is the full versioned payload emitted over MCP(stdio) and CLI.
// Questions are emitted in canonical (region NodeId, rule id, text) order for
// byte-stable output.
type QuestionReport struct {
	SchemaVersion         int                `json:"schema_version"`
	GeneratorVersion      string             `json:"generator_version"`
	ConfigHash            string             `json:"config_hash"`
	IdentitySchemaVersion uint32             `json:"identity_schema_version"`
	Outcome               string             `json:"outcome"` // found | empty
	Questions             []ReviewerQuestion `json:"questions"`
}

// questionSource is the injectable seam through which the generator consumes the
// SW-039 RiskReport and the SW-040 SignalReport RESULTS without recomputing them
// and without analyzer-to-analyzer hard coupling beyond the two sibling reports.
// The default implementation wraps the real pr-risk scorer and pr-signals
// detector; unit tests inject a stub so the rule engine is exercised with fixed
// reports (no LLM, no network — the seam is purely in-process).
type questionSource interface {
	// Risk returns the SW-039 per-region RiskReport for the PR diff carried in p.
	Risk(ctx context.Context, r query.Reader, p Params) (RiskReport, error)
	// Signals returns the SW-040 per-region SignalReport for the same diff.
	Signals(ctx context.Context, r query.Reader, p Params) (SignalReport, error)
}

// defaultQuestionSource wraps the real SW-039 pr-risk scorer and SW-040
// pr-signals detector. It RUNS them (consuming their versioned reports) but the
// generator itself never re-implements scoring, signal detection, traversal, or
// any graph algorithm — that logic stays in prisk.go / prsignals.go.
type defaultQuestionSource struct {
	risk    priskAnalyzer
	signals prSignalsAnalyzer
}

// newDefaultQuestionSource builds the production source wired to the two sibling
// analyzers with their fixed default models.
func newDefaultQuestionSource() defaultQuestionSource {
	return defaultQuestionSource{
		risk:    newPriskAnalyzer(),
		signals: newPrSignalsAnalyzer(),
	}
}

func (s defaultQuestionSource) Risk(ctx context.Context, r query.Reader, p Params) (RiskReport, error) {
	a, err := s.risk.Analyze(ctx, r, p)
	if err != nil {
		return RiskReport{}, err
	}
	if a.RiskReport == nil {
		return RiskReport{}, nil
	}
	return *a.RiskReport, nil
}

func (s defaultQuestionSource) Signals(ctx context.Context, r query.Reader, p Params) (SignalReport, error) {
	a, err := s.signals.Analyze(ctx, r, p)
	if err != nil {
		return SignalReport{}, err
	}
	if a.SignalReport == nil {
		return SignalReport{}, nil
	}
	return *a.SignalReport, nil
}

// prQuestionsAnalyzer is the registered reviewer-question generator. It holds
// only the injectable seam and the (fixed) threshold config; it is stateless per
// call and safe for concurrent use (read-only Reader, no mutation).
type prQuestionsAnalyzer struct {
	source questionSource
	config questionConfig
}

// newPrQuestionsAnalyzer builds the production generator wired to the real
// sibling reports.
func newPrQuestionsAnalyzer() prQuestionsAnalyzer {
	return prQuestionsAnalyzer{source: newDefaultQuestionSource(), config: defaultQuestionConfig}
}

func (prQuestionsAnalyzer) Name() string { return PrQuestionsAnalyzerName }

// Analyze consumes the two sibling reports for the PR diff (carried in
// Params.Diff), applies the fixed rule/template set, and returns a versioned
// QuestionReport carried on the generic Analysis envelope's QuestionReport
// field. It performs ZERO outbound network activity and uses NO LLM (pure rule
// evaluation over consumed, in-process reports).
func (a prQuestionsAnalyzer) Analyze(ctx context.Context, r query.Reader, p Params) (Analysis, error) {
	src := a.source
	if src == nil {
		src = newDefaultQuestionSource()
	}
	cfg := a.config
	if cfg == (questionConfig{}) {
		cfg = defaultQuestionConfig
	}

	report, err := a.generate(ctx, r, p, src, cfg)
	if err != nil {
		return Analysis{}, err
	}
	return Analysis{
		Analyzer:       PrQuestionsAnalyzerName,
		Outcome:        query.Outcome(report.Outcome),
		Symbol:         p.Symbol,
		QuestionReport: &report,
	}, nil
}

// generate is the core rule engine. It is split out so it can be unit-tested
// directly and so Analyze stays a thin adapter (mirrors priskAnalyzer.score /
// prSignalsAnalyzer.detect).
func (a prQuestionsAnalyzer) generate(ctx context.Context, r query.Reader, p Params, src questionSource, cfg questionConfig) (QuestionReport, error) {
	report := QuestionReport{
		SchemaVersion:         QuestionSchemaVersion,
		GeneratorVersion:      GeneratorVersion,
		ConfigHash:            cfg.hash(),
		IdentitySchemaVersion: model.IdentitySchemaVersion,
		Outcome:               string(query.OutcomeEmpty),
		Questions:             []ReviewerQuestion{},
	}

	// An empty/whitespace diff completes with an empty report (never an error),
	// mirroring the sibling analyzers. parseDiff is the bounded, sanitized,
	// pure-string parser reused from SW-039 (no shell-out / git-exec / network).
	refs, err := parseDiff(p.Diff)
	if err != nil {
		return QuestionReport{}, err
	}
	if len(refs) == 0 {
		return report, nil
	}

	// Consume the two sibling REPORTS exactly once each (never per-region
	// recompute) and index their records by region NodeId.
	risk, err := src.Risk(ctx, r, p)
	if err != nil {
		return QuestionReport{}, err
	}
	signals, err := src.Signals(ctx, r, p)
	if err != nil {
		return QuestionReport{}, err
	}

	riskByRegion := indexRiskRecords(risk)
	sigByRegion := indexSignalRecords(signals)

	// Build the canonical, deduplicated region order from BOTH reports so a region
	// present in either report is considered exactly once. Degraded records (empty
	// Region) are skipped — there is nothing to ask a targeted question about.
	regions := unionRegions(riskByRegion, sigByRegion)

	questions := make([]ReviewerQuestion, 0, len(regions))
	for _, id := range regions {
		riskRec, hasRisk := riskByRegion[id]
		sigRec := sigByRegion[id] // zero value (no signals) when absent

		// Apply the fixed, ordered rule list. Each rule is a pure function of the
		// consumed records + config; it either emits a question (with a non-empty
		// evidence reference) or it does not.
		questions = append(questions, hubRule(id, sigRec)...)
		questions = append(questions, riskSurpriseRule(id, riskRec, hasRisk, sigRec, cfg)...)
	}

	// Defensive: never emit a question with an empty evidence reference (AC1/AC3
	// invariant). This cannot happen given the rules below, but the filter makes
	// the invariant structural.
	questions = dropEmptyEvidence(questions)

	sortQuestions(questions)
	report.Questions = questions
	if len(questions) > 0 {
		report.Outcome = string(query.OutcomeFound)
	}
	return report, nil
}

// hubRule emits a hub-focused question when the region carries a SW-040 hub
// signal. The question names the region and its in-degree/hub score and carries
// an evidence reference to the region NodeId and the hub signal (kind + score).
func hubRule(id model.NodeId, sigRec SignalRecord) []ReviewerQuestion {
	flag, ok := firstSignal(sigRec, SignalHub)
	if !ok {
		return nil
	}
	score := strings.TrimSpace(flag.Score)
	text := fmt.Sprintf(
		"This changed symbol is a hub (high fan-in: degree %s). Many call sites depend on it — are all callers covered by this change?",
		emptyDash(score),
	)
	refs := []string{string(id)}
	if score != "" {
		refs = append(refs, "hub-degree="+score)
	}
	return []ReviewerQuestion{{
		RuleID: RuleHubCallers,
		Region: id,
		Text:   text,
		Evidence: EvidenceRef{
			Region:  id,
			Signals: []string{SignalHub},
			Refs:    refs,
		},
	}}
}

// riskSurpriseRule emits a risk+surprise question when the region's SW-039 risk
// score EXCEEDS the high-risk threshold AND the region carries a SW-040 surprise
// signal. The question links the specific surprise signal/refs and the numeric
// risk value. A region below the threshold, or one with no surprise signal,
// emits nothing — this is the threshold/no-signal gating (AC2).
func riskSurpriseRule(id model.NodeId, riskRec RiskRecord, hasRisk bool, sigRec SignalRecord, cfg questionConfig) []ReviewerQuestion {
	if !hasRisk {
		return nil
	}
	scoreUnits, ok := parseFixed(riskRec.Score)
	if !ok || scoreUnits <= cfg.HighRiskThreshold {
		return nil // below (or at) the high-risk threshold: gated out
	}
	flag, ok := firstSignal(sigRec, SignalSurprise)
	if !ok {
		return nil // high risk but no surprise: not this rule's question
	}

	detail := strings.TrimSpace(flag.Detail)
	refs := []string{string(id), "risk=" + riskRec.Score}
	if detail != "" {
		refs = append(refs, detail)
	}
	refs = append(refs, flag.Refs...)

	text := fmt.Sprintf(
		"This changed region is high-risk (risk %s, above threshold %s) and carries a surprise signal (%s). Has this unexpected, risky change been reviewed with extra care?",
		riskRec.Score, renderFixed(cfg.HighRiskThreshold), emptyDash(detail),
	)
	return []ReviewerQuestion{{
		RuleID: RuleRiskSurprise,
		Region: id,
		Text:   text,
		Evidence: EvidenceRef{
			Region:    id,
			RiskScore: riskRec.Score,
			Signals:   []string{SignalSurprise},
			Refs:      refs,
		},
	}}
}

// indexRiskRecords builds a region NodeId -> RiskRecord index over the consumed
// RiskReport, skipping degraded records (empty Region). When the same region
// appears more than once (it should not), the first canonical record wins.
func indexRiskRecords(rep RiskReport) map[model.NodeId]RiskRecord {
	out := make(map[model.NodeId]RiskRecord, len(rep.Regions))
	for _, rec := range rep.Regions {
		if rec.Degraded || rec.Region == "" {
			continue
		}
		if _, seen := out[rec.Region]; !seen {
			out[rec.Region] = rec
		}
	}
	return out
}

// indexSignalRecords builds a region NodeId -> SignalRecord index over the
// consumed SignalReport, skipping degraded records (empty Region).
func indexSignalRecords(rep SignalReport) map[model.NodeId]SignalRecord {
	out := make(map[model.NodeId]SignalRecord, len(rep.Regions))
	for _, rec := range rep.Regions {
		if rec.Degraded || rec.Region == "" {
			continue
		}
		if _, seen := out[rec.Region]; !seen {
			out[rec.Region] = rec
		}
	}
	return out
}

// unionRegions returns the sorted, deduplicated set of region NodeIds present in
// either index. Sorting here makes the per-region rule application order
// canonical regardless of map-iteration order.
func unionRegions(risk map[model.NodeId]RiskRecord, sig map[model.NodeId]SignalRecord) []model.NodeId {
	set := make(map[model.NodeId]struct{}, len(risk)+len(sig))
	for id := range risk {
		set[id] = struct{}{}
	}
	for id := range sig {
		set[id] = struct{}{}
	}
	out := make([]model.NodeId, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// firstSignal returns the first SignalFlag of the given kind on a record (the
// record's flags are already canonically sorted by the SW-040 detector).
func firstSignal(rec SignalRecord, kind string) (SignalFlag, bool) {
	for _, f := range rec.Signals {
		if f.Kind == kind {
			return f, true
		}
	}
	return SignalFlag{}, false
}

// dropEmptyEvidence removes any question whose evidence reference is empty,
// enforcing the AC1/AC3 invariant that every emitted question carries a
// non-empty evidence reference.
func dropEmptyEvidence(qs []ReviewerQuestion) []ReviewerQuestion {
	out := qs[:0]
	for _, q := range qs {
		if !q.Evidence.isEmpty() {
			out = append(out, q)
		}
	}
	return out
}

// parseFixed is the pure inverse of renderFixed: it parses a canonical
// fixed-point decimal string (e.g. "0.730") back to its integer units (730).
// String-based (not float) so the threshold comparison is drift-free and
// byte-deterministic. Returns ok=false for a malformed value.
func parseFixed(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	dot := strings.IndexByte(s, '.')
	if dot < 0 {
		// Whole number with no fractional part.
		whole, err := strconv.Atoi(s)
		if err != nil || whole < 0 {
			return 0, false
		}
		return whole * scoreScale, true
	}
	wholePart := s[:dot]
	fracPart := s[dot+1:]
	if wholePart == "" || len(fracPart) != 3 {
		return 0, false
	}
	whole, err := strconv.Atoi(wholePart)
	if err != nil || whole < 0 {
		return 0, false
	}
	frac, err := strconv.Atoi(fracPart)
	if err != nil || frac < 0 || frac >= scoreScale {
		return 0, false
	}
	return whole*scoreScale + frac, true
}

// emptyDash renders an empty string as a stable placeholder so the templated
// text is byte-stable even when an optional field is absent.
func emptyDash(s string) string {
	if s == "" {
		return "n/a"
	}
	return s
}

// ruleRank fixes a stable ordering for rule ids so that, within a region, hub
// questions precede risk+surprise questions deterministically.
func ruleRank(ruleID string) int {
	switch ruleID {
	case RuleHubCallers:
		return 0
	case RuleRiskSurprise:
		return 1
	default:
		return 2
	}
}

// sortQuestions orders questions canonically: by region NodeId, then rule rank,
// then question text. This is the ONLY place question ordering is decided;
// combined with MarshalQuestions it makes the report byte-identical regardless
// of map-iteration or rule-evaluation order.
func sortQuestions(qs []ReviewerQuestion) {
	sort.SliceStable(qs, func(i, j int) bool {
		a, b := qs[i], qs[j]
		if a.Region != b.Region {
			return a.Region < b.Region
		}
		if ra, rb := ruleRank(a.RuleID), ruleRank(b.RuleID); ra != rb {
			return ra < rb
		}
		return a.Text < b.Text
	})
}

// MarshalQuestions is the single canonical serializer for a QuestionReport,
// shared by every surface (CLI, MCP). It re-sorts defensively, disables HTML
// escaping, and trims the trailing newline — byte-for-byte stable across runs
// and surfaces (mirrors MarshalRisk / MarshalSignals). Empty slices are
// materialized (never null) so the shape is stable.
func MarshalQuestions(rep QuestionReport) ([]byte, error) {
	out := rep
	qs := make([]ReviewerQuestion, len(rep.Questions))
	copy(qs, rep.Questions)
	sortQuestions(qs)
	if qs == nil {
		qs = []ReviewerQuestion{}
	}
	out.Questions = qs

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, fmt.Errorf("analysis: marshal question report: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
