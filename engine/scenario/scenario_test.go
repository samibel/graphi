package scenario

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type stubEngine struct {
	out   []string
	conf  *float64
	err   error
	delay time.Duration
}

func (s *stubEngine) Invoke(operation string, args map[string]string) ([]string, *float64, error) {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if s.err != nil {
		return nil, nil, s.err
	}
	return s.out, s.conf, nil
}

func anchors(a ...Expect) ExpectBlock { return ExpectBlock{Anchors: a} }

func TestScenarioValidation(t *testing.T) {
	ok := anchors(Expect{Kind: ExpectOutcome, Value: "ok"})
	cases := []struct {
		name string
		s    Scenario
		want string
	}{
		{"missing id", Scenario{FixtureRef: "f", Operation: Operation{Name: "search"}, Expect: ok}, "missing id"},
		{"missing fixture", Scenario{ID: "x", Operation: Operation{Name: "search"}, Expect: ok}, "missing fixture_ref"},
		{"path fixture", Scenario{ID: "x", FixtureRef: "fixtures/thing", Operation: Operation{Name: "search"}, Expect: ok}, "not a path"},
		{"missing op", Scenario{ID: "x", FixtureRef: "f", Expect: ok}, "missing operation"},
		{"unknown op", Scenario{ID: "x", FixtureRef: "f", Operation: Operation{Name: "explode"}, Expect: ok}, "unknown operation"},
		{"no expects", Scenario{ID: "x", FixtureRef: "f", Operation: Operation{Name: "search"}}, "at least one expect"},
		{"bad expected outcome", Scenario{ID: "x", FixtureRef: "f", Operation: Operation{Name: "search"}, Expect: ExpectBlock{Outcome: "sideways"}}, "not a known outcome"},
		{"negative latency", Scenario{ID: "x", FixtureRef: "f", Operation: Operation{Name: "search"}, Expect: ExpectBlock{Outcome: "found", MaxLatencyMS: -1}}, "must not be negative"},
		{"valid legacy", Scenario{ID: "x", FixtureRef: "f", Operation: Operation{Name: "search"}, Expect: ok}, ""},
		{"valid prd", Scenario{ID: "x", FixtureRef: "f", Operation: Operation{Name: "explain_symbol"}, Expect: ExpectBlock{Outcome: "found", ContainsPath: "a.go", MaxLatencyMS: 500, HasEvidence: true}}, ""},
	}
	for _, c := range cases {
		err := c.s.Validate()
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: unexpected error: %v", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want contains %q", c.name, err, c.want)
		}
	}
}

func TestRunner_PassAndFail(t *testing.T) {
	r := &Runner{Engine: &stubEngine{out: []string{"alpha", "beta"}}}
	s := Scenario{
		ID:         "s1",
		FixtureRef: "f1",
		Operation:  Operation{Name: "search"},
		Expect: anchors(
			Expect{Kind: ExpectSymbol, Value: "alpha", Match: MatchExact},
			Expect{Kind: ExpectSymbol, Value: "gamma", Match: MatchExact},
		),
	}
	res := r.Run(s)
	if res.Outcome != "fail" {
		t.Fatalf("expected fail, got %s", res.Outcome)
	}
	if res.AnchorPresent {
		t.Fatal("expected anchor_present false")
	}
	if res.ResultSize != 2 {
		t.Fatalf("result_size = %d, want 2", res.ResultSize)
	}

	// Pass case.
	s.Expect = anchors(Expect{Kind: ExpectSymbol, Value: "alpha", Match: MatchExact})
	res = r.Run(s)
	if res.Outcome != "pass" {
		t.Fatalf("expected pass, got %s", res.Outcome)
	}
	if !res.AnchorPresent {
		t.Fatal("expected anchor_present true")
	}
	if res.OpOutcome != "found" {
		t.Fatalf("op_outcome = %q, want found", res.OpOutcome)
	}
	if res.AnswerSize == 0 {
		t.Fatal("expected non-zero answer_size")
	}
}

func TestRunner_ContainsMatch(t *testing.T) {
	r := &Runner{Engine: &stubEngine{out: []string{"alpha/beta"}}}
	s := Scenario{
		ID:         "s2",
		FixtureRef: "f2",
		Operation:  Operation{Name: "search"},
		Expect:     anchors(Expect{Kind: ExpectFile, Value: "beta", Match: MatchContains}),
	}
	res := r.Run(s)
	if res.Outcome != "pass" {
		t.Fatalf("expected pass, got %s", res.Outcome)
	}
}

func TestRunner_EngineError(t *testing.T) {
	r := &Runner{Engine: &stubEngine{err: errors.New("offline guard triggered")}}
	s := Scenario{
		ID:         "s3",
		FixtureRef: "f3",
		Operation:  Operation{Name: "search"},
		Expect:     anchors(Expect{Kind: ExpectOutcome, Value: "ok"}),
	}
	res := r.Run(s)
	if res.Outcome != "error" {
		t.Fatalf("expected error, got %s", res.Outcome)
	}
	if res.Confidence != nil {
		t.Fatal("expected nil confidence on error")
	}
	if res.OpOutcome != "error" {
		t.Fatalf("op_outcome = %q, want error", res.OpOutcome)
	}
}

func TestRunner_ConfidencePassThrough(t *testing.T) {
	c := 0.95
	r := &Runner{Engine: &stubEngine{out: []string{"ok"}, conf: &c}}
	s := Scenario{
		ID:         "s4",
		FixtureRef: "f4",
		Operation:  Operation{Name: "search"},
		Expect:     anchors(Expect{Kind: ExpectOutcome, Value: "ok", Match: MatchExact}),
	}
	res := r.Run(s)
	if res.Confidence == nil || *res.Confidence != 0.95 {
		t.Fatalf("confidence not passed through: %v", res.Confidence)
	}
}

func TestRunner_OutcomeAnchorMatchesOpOutcome(t *testing.T) {
	// A search with no matches derives the "empty" op outcome; the legacy
	// outcome anchor must match it even though no evidence line says "empty".
	r := &Runner{Engine: &stubEngine{}}
	s := Scenario{
		ID:         "s5",
		FixtureRef: "f5",
		Operation:  Operation{Name: "search"},
		Expect:     anchors(Expect{Kind: ExpectOutcome, Value: "empty", Match: MatchExact}),
	}
	res := r.Run(s)
	if res.Outcome != "pass" {
		t.Fatalf("expected pass, got %s (%v)", res.Outcome, res.Failures)
	}
	if res.OpOutcome != "empty" {
		t.Fatalf("op_outcome = %q, want empty", res.OpOutcome)
	}
}

func TestRunner_ExpectedOutcomeMismatchFails(t *testing.T) {
	r := &Runner{Engine: &stubEngine{out: []string{"hit"}}}
	s := Scenario{
		ID:         "s6",
		FixtureRef: "f6",
		Operation:  Operation{Name: "search"},
		Expect:     ExpectBlock{Outcome: "empty"},
	}
	res := r.Run(s)
	if res.Outcome != "fail" {
		t.Fatalf("expected fail, got %s", res.Outcome)
	}
	if len(res.Failures) != 1 || !strings.Contains(res.Failures[0], `want "empty"`) {
		t.Fatalf("unexpected failures: %v", res.Failures)
	}
}

func TestRunner_MaxLatencyExceededFails(t *testing.T) {
	r := &Runner{Engine: &stubEngine{out: []string{"hit"}, delay: 25 * time.Millisecond}}
	s := Scenario{
		ID:         "s7",
		FixtureRef: "f7",
		Operation:  Operation{Name: "search"},
		Expect:     ExpectBlock{Outcome: "found", MaxLatencyMS: 1},
	}
	res := r.Run(s)
	if res.Outcome != "fail" {
		t.Fatalf("expected fail, got %s", res.Outcome)
	}
	found := false
	for _, f := range res.Failures {
		if strings.Contains(f, "max_latency_ms") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a latency failure, got %v", res.Failures)
	}
}

func TestRunner_HasEvidenceFailsOnEmpty(t *testing.T) {
	r := &Runner{Engine: &stubEngine{}}
	s := Scenario{
		ID:         "s8",
		FixtureRef: "f8",
		Operation:  Operation{Name: "search"},
		Expect:     ExpectBlock{HasEvidence: true},
	}
	res := r.Run(s)
	if res.Outcome != "fail" {
		t.Fatalf("expected fail, got %s", res.Outcome)
	}
	if len(res.Failures) != 1 || !strings.Contains(res.Failures[0], "evidence") {
		t.Fatalf("unexpected failures: %v", res.Failures)
	}
}

func TestCoverageReport(t *testing.T) {
	scenarios := []Scenario{
		{Operation: Operation{Name: "search"}},
		{Operation: Operation{Name: "definition"}},
	}
	ops := []string{"search", "definition", "references"}
	gaps := CoverageReport(scenarios, ops)
	if !gaps["references"] {
		t.Fatal("expected references to be uncovered")
	}
	if gaps["search"] {
		t.Fatal("expected search to be covered")
	}
}

func TestLoadScenarioYAML(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.yaml")
	body := `id: yaml-scenario
fixture_ref: tier1-fixture
operation:
  name: search
  args:
    query: hello
expect:
  - kind: symbol
    value: hello
    match: exact
`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := LoadScenario(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if s.ID != "yaml-scenario" || s.FixtureRef != "tier1-fixture" {
		t.Fatalf("unexpected scenario: %+v", s)
	}
	if len(s.Expect.Anchors) != 1 || s.Expect.Anchors[0].Value != "hello" {
		t.Fatalf("unexpected anchors: %+v", s.Expect.Anchors)
	}
}

func TestLoadScenarioYAML_PRDExpectations(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.yaml")
	body := `id: prd-scenario
fixture_ref: tier1
operation:
  name: explain_symbol
  args:
    symbol: fixture.Hello
expect:
  outcome: found
  contains_path: sample.go
  max_latency_ms: 2000
  has_evidence: true
  anchors:
    - kind: symbol
      value: Hello
      match: contains
`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := LoadScenario(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	e := s.Expect
	if e.Outcome != "found" || e.ContainsPath != "sample.go" || e.MaxLatencyMS != 2000 || !e.HasEvidence {
		t.Fatalf("unexpected expect block: %+v", e)
	}
	if len(e.Anchors) != 1 || e.Anchors[0].Value != "Hello" {
		t.Fatalf("unexpected anchors: %+v", e.Anchors)
	}
}

func TestLoadScenarioJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	body := `{"id":"json-scenario","fixture_ref":"tier1-fixture","operation":{"name":"search","args":{}},"expect":[{"kind":"symbol","value":"hello","match":"exact"}]}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := LoadScenario(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if s.ID != "json-scenario" {
		t.Fatalf("unexpected scenario: %+v", s)
	}
	if len(s.Expect.Anchors) != 1 {
		t.Fatalf("unexpected anchors: %+v", s.Expect.Anchors)
	}
}

func TestLoadScenarioJSON_PRDExpectations(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	body := `{"id":"json-prd","fixture_ref":"tier1","operation":{"name":"change_risk","args":{"target":"fixture.Hello"}},"expect":{"outcome":"found","contains_path":"sample.go","max_latency_ms":1500,"has_evidence":true}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := LoadScenario(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	e := s.Expect
	if e.Outcome != "found" || e.ContainsPath != "sample.go" || e.MaxLatencyMS != 1500 || !e.HasEvidence {
		t.Fatalf("unexpected expect block: %+v", e)
	}
}

func TestExpectBlockJSONRoundTrip(t *testing.T) {
	// Legacy anchors-only blocks must round-trip through the array form so
	// existing consumers keep seeing the original schema.
	legacy := anchors(Expect{Kind: ExpectSymbol, Value: "x", Match: MatchExact})
	b, err := legacy.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if b[0] != '[' {
		t.Fatalf("legacy block should marshal as array, got %s", b)
	}
	prd := ExpectBlock{Outcome: "found", HasEvidence: true}
	b, err = prd.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if b[0] != '{' {
		t.Fatalf("PRD block should marshal as object, got %s", b)
	}
}
