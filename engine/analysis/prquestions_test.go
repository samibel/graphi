package analysis

import (
	"bytes"
	"context"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// --- test fixtures -----------------------------------------------------------

// stubQuestionSource feeds FIXED RiskReport / SignalReport values to the
// generator so the rule engine is exercised in isolation without re-running the
// SW-039 scorer or SW-040 detector (and so threshold/gating cases are exact).
// It is purely in-process: no LLM, no network — by construction.
type stubQuestionSource struct {
	risk    RiskReport
	signals SignalReport
	calls   int
}

func (s *stubQuestionSource) Risk(_ context.Context, _ query.Reader, _ Params) (RiskReport, error) {
	s.calls++
	return s.risk, nil
}

func (s *stubQuestionSource) Signals(_ context.Context, _ query.Reader, _ Params) (SignalReport, error) {
	return s.signals, nil
}

// questionFor returns the first question for a region produced by the given rule
// (or fails the test).
func questionFor(t *testing.T, rep QuestionReport, id model.NodeId, ruleID string) ReviewerQuestion {
	t.Helper()
	for _, q := range rep.Questions {
		if q.Region == id && q.RuleID == ruleID {
			return q
		}
	}
	t.Fatalf("no question for region %s rule %s; got %+v", id, ruleID, rep.Questions)
	return ReviewerQuestion{}
}

// hasQuestion reports whether any question exists for the region+rule.
func hasQuestion(rep QuestionReport, id model.NodeId, ruleID string) bool {
	for _, q := range rep.Questions {
		if q.Region == id && q.RuleID == ruleID {
			return true
		}
	}
	return false
}

// --- AC1: hub-focused question with node + in-degree signal evidence ---------

func TestPrQuestionsHubFocused(t *testing.T) {
	store := graphstore.NewMemStore()
	idHub := mkNode(t, store, "function", "pkg.Hub", "pkg/hub.go", 10)

	src := &stubQuestionSource{
		signals: SignalReport{
			Outcome: string(query.OutcomeFound),
			Regions: []SignalRecord{{
				Region: idHub,
				Signals: []SignalFlag{{
					Kind:   SignalHub,
					Reason: "changed node has high fan-in/out (degree 5 >= threshold 3)",
					Score:  "5",
				}},
			}},
		},
	}
	a := prQuestionsAnalyzer{source: src, config: defaultQuestionConfig}

	res, err := a.Analyze(context.Background(), store, Params{Diff: "pkg/hub.go:Hub"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	rep := *res.QuestionReport

	q := questionFor(t, rep, idHub, RuleHubCallers)
	// Evidence references the exact node and its in-degree/hub signal (AC1).
	if q.Evidence.Region != idHub {
		t.Fatalf("hub question evidence region = %q, want %q", q.Evidence.Region, idHub)
	}
	if q.Evidence.isEmpty() {
		t.Fatalf("hub question must carry a non-empty evidence reference")
	}
	gotHubSignal := false
	for _, s := range q.Evidence.Signals {
		if s == SignalHub {
			gotHubSignal = true
		}
	}
	if !gotHubSignal {
		t.Fatalf("hub question evidence missing hub signal kind: %+v", q.Evidence)
	}
	if !containsRef(q.Evidence.Refs, "hub-degree=5") {
		t.Fatalf("hub question evidence missing in-degree ref: %+v", q.Evidence.Refs)
	}
	if !strings.Contains(q.Text, "hub") || !strings.Contains(q.Text, "5") {
		t.Fatalf("hub question text missing fan-in/degree readout: %q", q.Text)
	}
}

// --- AC2: risk+surprise question + threshold gating --------------------------

// TestPrQuestionsRiskSurprisePositive: a region whose risk EXCEEDS the threshold
// AND carries a surprise signal yields a risk+surprise question linking the
// surprise signal/refs and the risk value.
func TestPrQuestionsRiskSurprisePositive(t *testing.T) {
	store := graphstore.NewMemStore()
	idRisky := mkNode(t, store, "function", "pkg.Risky", "pkg/risky.go", 10)

	src := &stubQuestionSource{
		risk: RiskReport{
			Outcome: string(query.OutcomeFound),
			Regions: []RiskRecord{{
				Region:     idRisky,
				Score:      "0.730", // 730 units > default threshold 500
				Confidence: ConfidenceHigh,
			}},
		},
		signals: SignalReport{
			Outcome: string(query.OutcomeFound),
			Regions: []SignalRecord{{
				Region: idRisky,
				Signals: []SignalFlag{{
					Kind:   SignalSurprise,
					Detail: SurpriseUnexpectedCoupling,
					Reason: "changed region is unexpectedly coupled to a different module",
					Refs:   []string{"otherNodeId00000"},
				}},
			}},
		},
	}
	a := prQuestionsAnalyzer{source: src, config: defaultQuestionConfig}

	res, err := a.Analyze(context.Background(), store, Params{Diff: "pkg/risky.go:Risky"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	rep := *res.QuestionReport

	q := questionFor(t, rep, idRisky, RuleRiskSurprise)
	if q.Evidence.RiskScore != "0.730" {
		t.Fatalf("risk+surprise evidence risk_score = %q, want 0.730", q.Evidence.RiskScore)
	}
	if !containsRef(q.Evidence.Refs, "risk=0.730") {
		t.Fatalf("risk+surprise evidence missing risk ref: %+v", q.Evidence.Refs)
	}
	if !containsRef(q.Evidence.Refs, SurpriseUnexpectedCoupling) {
		t.Fatalf("risk+surprise evidence missing surprise detail ref: %+v", q.Evidence.Refs)
	}
	if !containsRef(q.Evidence.Refs, "otherNodeId00000") {
		t.Fatalf("risk+surprise evidence missing verbatim surprise ref: %+v", q.Evidence.Refs)
	}
	if !strings.Contains(q.Text, "0.730") || !strings.Contains(q.Text, "surprise") {
		t.Fatalf("risk+surprise text missing risk value / surprise: %q", q.Text)
	}
}

// TestPrQuestionsBelowThresholdNoSignalGated: a changed region BELOW the
// threshold with NO signals yields NO question (the core gating clause of AC2).
func TestPrQuestionsBelowThresholdNoSignalGated(t *testing.T) {
	store := graphstore.NewMemStore()
	idLow := mkNode(t, store, "function", "pkg.Low", "pkg/low.go", 10)

	src := &stubQuestionSource{
		risk: RiskReport{
			Outcome: string(query.OutcomeFound),
			Regions: []RiskRecord{{
				Region:     idLow,
				Score:      "0.100", // 100 units < threshold 500
				Confidence: ConfidenceHigh,
			}},
		},
		signals: SignalReport{ // no signals at all
			Outcome: string(query.OutcomeEmpty),
			Regions: []SignalRecord{{Region: idLow, Signals: []SignalFlag{}}},
		},
	}
	a := prQuestionsAnalyzer{source: src, config: defaultQuestionConfig}

	res, err := a.Analyze(context.Background(), store, Params{Diff: "pkg/low.go:Low"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	rep := *res.QuestionReport
	if len(rep.Questions) != 0 {
		t.Fatalf("expected NO questions for below-threshold no-signal region; got %+v", rep.Questions)
	}
	if rep.Outcome != string(query.OutcomeEmpty) {
		t.Fatalf("expected empty outcome, got %q", rep.Outcome)
	}
}

// TestPrQuestionsThresholdIsConfigurable: raising the threshold above the
// region's risk score suppresses the risk+surprise question — proving the
// threshold is genuinely configurable, not hard-coded.
func TestPrQuestionsThresholdIsConfigurable(t *testing.T) {
	store := graphstore.NewMemStore()
	idRisky := mkNode(t, store, "function", "pkg.Risky", "pkg/risky.go", 10)

	src := &stubQuestionSource{
		risk: RiskReport{
			Outcome: string(query.OutcomeFound),
			Regions: []RiskRecord{{Region: idRisky, Score: "0.730", Confidence: ConfidenceHigh}},
		},
		signals: SignalReport{
			Outcome: string(query.OutcomeFound),
			Regions: []SignalRecord{{
				Region:  idRisky,
				Signals: []SignalFlag{{Kind: SignalSurprise, Detail: SurpriseLowChurn, Reason: "rarely modified"}},
			}},
		},
	}
	// Threshold 800 units ("0.800") is ABOVE the region's 730 → no question.
	a := prQuestionsAnalyzer{source: src, config: questionConfig{HighRiskThreshold: 800}}

	res, err := a.Analyze(context.Background(), store, Params{Diff: "pkg/risky.go:Risky"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if hasQuestion(*res.QuestionReport, idRisky, RuleRiskSurprise) {
		t.Fatalf("expected NO risk+surprise question at threshold 0.800 (risk 0.730); threshold not honored")
	}
}

// TestPrQuestionsHighRiskNoSurpriseGated: a region above the threshold but WITHOUT
// a surprise signal does NOT trigger the risk+surprise rule.
func TestPrQuestionsHighRiskNoSurpriseGated(t *testing.T) {
	store := graphstore.NewMemStore()
	idRisky := mkNode(t, store, "function", "pkg.Risky", "pkg/risky.go", 10)
	src := &stubQuestionSource{
		risk: RiskReport{
			Outcome: string(query.OutcomeFound),
			Regions: []RiskRecord{{Region: idRisky, Score: "0.900", Confidence: ConfidenceHigh}},
		},
		signals: SignalReport{Outcome: string(query.OutcomeEmpty), Regions: []SignalRecord{}},
	}
	a := prQuestionsAnalyzer{source: src, config: defaultQuestionConfig}
	res, err := a.Analyze(context.Background(), store, Params{Diff: "pkg/risky.go:Risky"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if hasQuestion(*res.QuestionReport, idRisky, RuleRiskSurprise) {
		t.Fatalf("expected NO risk+surprise question without a surprise signal")
	}
}

// --- AC3: determinism + non-empty evidence ----------------------------------

// TestPrQuestionsDeterministicByteIdentical: the same findings generated twice
// yield a byte-identical serialized question set (content + ordering), and every
// question carries a non-empty evidence reference.
func TestPrQuestionsDeterministicByteIdentical(t *testing.T) {
	store := graphstore.NewMemStore()
	idHub := mkNode(t, store, "function", "pkg.Hub", "pkg/hub.go", 10)
	idRisky := mkNode(t, store, "function", "pkg.Risky", "pkg/risky.go", 10)

	mkSrc := func() *stubQuestionSource {
		return &stubQuestionSource{
			risk: RiskReport{
				Outcome: string(query.OutcomeFound),
				Regions: []RiskRecord{
					{Region: idRisky, Score: "0.730", Confidence: ConfidenceHigh},
					{Region: idHub, Score: "0.200", Confidence: ConfidenceHigh},
				},
			},
			signals: SignalReport{
				Outcome: string(query.OutcomeFound),
				Regions: []SignalRecord{
					{Region: idHub, Signals: []SignalFlag{{Kind: SignalHub, Score: "7"}}},
					{Region: idRisky, Signals: []SignalFlag{{Kind: SignalSurprise, Detail: SurpriseLowChurn, Reason: "rarely modified"}}},
				},
			},
		}
	}
	diff := "pkg/hub.go:Hub\npkg/risky.go:Risky"

	run := func() []byte {
		a := prQuestionsAnalyzer{source: mkSrc(), config: defaultQuestionConfig}
		res, err := a.Analyze(context.Background(), store, Params{Diff: diff})
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		b, err := MarshalQuestions(*res.QuestionReport)
		if err != nil {
			t.Fatalf("MarshalQuestions: %v", err)
		}
		// Every question must carry a non-empty evidence reference (AC3).
		for _, q := range res.QuestionReport.Questions {
			if q.Evidence.isEmpty() {
				t.Fatalf("question %q has empty evidence reference", q.RuleID)
			}
		}
		return b
	}

	first := run()
	second := run()
	if !bytes.Equal(first, second) {
		t.Fatalf("non-deterministic output:\nfirst:  %s\nsecond: %s", first, second)
	}
	// Sanity: both questions present and the report is versioned.
	if !bytes.Contains(first, []byte("\"generator_version\":\"pr-questions/1\"")) {
		t.Fatalf("output missing generator_version: %s", first)
	}
	if !bytes.Contains(first, []byte(RuleHubCallers)) || !bytes.Contains(first, []byte(RuleRiskSurprise)) {
		t.Fatalf("output missing expected rules: %s", first)
	}
}

// TestPrQuestionsEmptyDiff: an empty diff completes with an empty report.
func TestPrQuestionsEmptyDiff(t *testing.T) {
	store := graphstore.NewMemStore()
	a := prQuestionsAnalyzer{source: &stubQuestionSource{}, config: defaultQuestionConfig}
	res, err := a.Analyze(context.Background(), store, Params{Diff: "   "})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	rep := *res.QuestionReport
	if len(rep.Questions) != 0 || rep.Outcome != string(query.OutcomeEmpty) {
		t.Fatalf("expected empty report for empty diff; got %+v", rep)
	}
}

// TestPrQuestionsConsumesEachReportOnce: the generator consumes the risk report
// exactly once per generation (never per-region recompute).
func TestPrQuestionsConsumesEachReportOnce(t *testing.T) {
	store := graphstore.NewMemStore()
	idA := mkNode(t, store, "function", "pkg.A", "pkg/a.go", 1)
	idB := mkNode(t, store, "function", "pkg.B", "pkg/b.go", 1)
	src := &stubQuestionSource{
		signals: SignalReport{
			Regions: []SignalRecord{
				{Region: idA, Signals: []SignalFlag{{Kind: SignalHub, Score: "9"}}},
				{Region: idB, Signals: []SignalFlag{{Kind: SignalHub, Score: "9"}}},
			},
		},
	}
	a := prQuestionsAnalyzer{source: src, config: defaultQuestionConfig}
	if _, err := a.Analyze(context.Background(), store, Params{Diff: "pkg/a.go:A\npkg/b.go:B"}); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if src.calls != 1 {
		t.Fatalf("expected Risk consumed exactly once, got %d", src.calls)
	}
}

// TestParseFixedRoundTrip: parseFixed is the exact inverse of renderFixed for
// canonical fixed-point strings.
func TestParseFixedRoundTrip(t *testing.T) {
	cases := []int{0, 1, 100, 500, 730, 999, 1000, 1234}
	for _, v := range cases {
		s := renderFixed(v)
		got, ok := parseFixed(s)
		if !ok || got != v {
			t.Fatalf("parseFixed(renderFixed(%d)=%q) = %d, %v; want %d", v, s, got, ok, v)
		}
	}
	if _, ok := parseFixed("bogus"); ok {
		t.Fatalf("parseFixed should reject malformed input")
	}
	if _, ok := parseFixed("0.73"); ok {
		t.Fatalf("parseFixed should require 3 fractional digits (canonical form)")
	}
}

// TestPrQuestionsNoLLMNoNetwork: structural guarantee that the generator file
// imports no LLM/network/exec packages (AC3: no LLM/network calls).
func TestPrQuestionsNoLLMNoNetwork(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "prquestions.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse prquestions.go: %v", err)
	}
	forbidden := []string{"net/http", "net", "os/exec", "net/rpc"}
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, "\"")
		for _, bad := range forbidden {
			if path == bad {
				t.Fatalf("prquestions.go must not import %q (local-first, no-LLM, no-network)", bad)
			}
		}
	}
}

// TestPrQuestionsRegisteredInDefaultService: the generator is registered and
// dispatchable through the default service, returning a QuestionReport.
func TestPrQuestionsRegisteredInDefaultService(t *testing.T) {
	store := graphstore.NewMemStore()
	_ = mkNode(t, store, "function", "pkg.A", "pkg/a.go", 1)
	svc := NewDefaultService(store)
	found := false
	for _, n := range svc.Names() {
		if n == PrQuestionsAnalyzerName {
			found = true
		}
	}
	if !found {
		t.Fatalf("pr-questions not registered in default service; names=%v", svc.Names())
	}
	res, err := svc.Dispatch(context.Background(), PrQuestionsAnalyzerName, Params{Diff: "pkg/a.go:A"})
	if err != nil {
		t.Fatalf("Dispatch pr-questions: %v", err)
	}
	if res.QuestionReport == nil {
		t.Fatalf("expected a QuestionReport from pr-questions dispatch")
	}
	if res.QuestionReport.GeneratorVersion != GeneratorVersion {
		t.Fatalf("generator_version = %q, want %q", res.QuestionReport.GeneratorVersion, GeneratorVersion)
	}
}

// containsRef reports whether refs contains s.
func containsRef(refs []string, s string) bool {
	for _, r := range refs {
		if r == s {
			return true
		}
	}
	return false
}
