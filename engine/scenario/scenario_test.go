package scenario

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type stubEngine struct {
	out  []string
	conf *float64
	err  error
}

func (s *stubEngine) Invoke(operation string, args map[string]string) ([]string, *float64, error) {
	if s.err != nil {
		return nil, nil, s.err
	}
	return s.out, s.conf, nil
}

func TestScenarioValidation(t *testing.T) {
	cases := []struct {
		name string
		s    Scenario
		want string
	}{
		{"missing id", Scenario{FixtureRef: "f", Operation: Operation{Name: "search"}, Expect: []Expect{{Kind: ExpectOutcome, Value: "ok"}}}, "missing id"},
		{"missing fixture", Scenario{ID: "x", Operation: Operation{Name: "search"}, Expect: []Expect{{Kind: ExpectOutcome, Value: "ok"}}}, "missing fixture_ref"},
		{"path fixture", Scenario{ID: "x", FixtureRef: "fixtures/thing", Operation: Operation{Name: "search"}, Expect: []Expect{{Kind: ExpectOutcome, Value: "ok"}}}, "not a path"},
		{"missing op", Scenario{ID: "x", FixtureRef: "f", Expect: []Expect{{Kind: ExpectOutcome, Value: "ok"}}}, "missing operation"},
		{"no expects", Scenario{ID: "x", FixtureRef: "f", Operation: Operation{Name: "search"}}, "at least one expect"},
		{"valid", Scenario{ID: "x", FixtureRef: "f", Operation: Operation{Name: "search"}, Expect: []Expect{{Kind: ExpectOutcome, Value: "ok"}}}, ""},
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
		Expect: []Expect{
			{Kind: ExpectSymbol, Value: "alpha", Match: MatchExact},
			{Kind: ExpectSymbol, Value: "gamma", Match: MatchExact},
		},
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
	s.Expect = []Expect{{Kind: ExpectSymbol, Value: "alpha", Match: MatchExact}}
	res = r.Run(s)
	if res.Outcome != "pass" {
		t.Fatalf("expected pass, got %s", res.Outcome)
	}
	if !res.AnchorPresent {
		t.Fatal("expected anchor_present true")
	}
}

func TestRunner_ContainsMatch(t *testing.T) {
	r := &Runner{Engine: &stubEngine{out: []string{"alpha/beta"}}}
	s := Scenario{
		ID:         "s2",
		FixtureRef: "f2",
		Operation:  Operation{Name: "search"},
		Expect:     []Expect{{Kind: ExpectFile, Value: "beta", Match: MatchContains}},
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
		Expect:     []Expect{{Kind: ExpectOutcome, Value: "ok"}},
	}
	res := r.Run(s)
	if res.Outcome != "error" {
		t.Fatalf("expected error, got %s", res.Outcome)
	}
	if res.Confidence != nil {
		t.Fatal("expected nil confidence on error")
	}
}

func TestRunner_ConfidencePassThrough(t *testing.T) {
	c := 0.95
	r := &Runner{Engine: &stubEngine{out: []string{"ok"}, conf: &c}}
	s := Scenario{
		ID:         "s4",
		FixtureRef: "f4",
		Operation:  Operation{Name: "search"},
		Expect:     []Expect{{Kind: ExpectOutcome, Value: "ok", Match: MatchExact}},
	}
	res := r.Run(s)
	if res.Confidence == nil || *res.Confidence != 0.95 {
		t.Fatalf("confidence not passed through: %v", res.Confidence)
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
}
