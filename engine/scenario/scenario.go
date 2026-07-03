package scenario

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Operation names for the scenario runner.
const (
	OpDefinition = "definition"
	OpReferences = "references"
	OpCallers    = "callers"
	OpSearch     = "search"
)

// MatchKind for anchored expectations.
type MatchKind string

const (
	MatchExact    MatchKind = "exact"
	MatchContains MatchKind = "contains"
)

// ExpectKind for the anchor type.
type ExpectKind string

const (
	ExpectSymbol  ExpectKind = "symbol"
	ExpectFile    ExpectKind = "file"
	ExpectLine    ExpectKind = "line"
	ExpectOutcome ExpectKind = "outcome"
)

// Operation declares a graphi operation and typed arguments.
type Operation struct {
	Name string            `json:"name" yaml:"name"`
	Args map[string]string `json:"args" yaml:"args"`
}

// Expect is one anchored expectation.
type Expect struct {
	Kind  ExpectKind `json:"kind" yaml:"kind"`
	Value string     `json:"value" yaml:"value"`
	Match MatchKind  `json:"match" yaml:"match"`
}

// Scenario binds a fixture to an operation and expectations.
type Scenario struct {
	ID          string    `json:"id" yaml:"id"`
	FixtureRef  string    `json:"fixture_ref" yaml:"fixture_ref"`
	Operation   Operation `json:"operation" yaml:"operation"`
	Expect      []Expect  `json:"expect" yaml:"expect"`
	Description string    `json:"description,omitempty" yaml:"description,omitempty"`
}

// LoadScenario reads a scenario from YAML or JSON.
func LoadScenario(path string) (Scenario, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Scenario{}, fmt.Errorf("scenario: read %q: %w", path, err)
	}
	var s Scenario
	if strings.HasSuffix(strings.ToLower(path), ".json") {
		if err := json.Unmarshal(raw, &s); err != nil {
			return Scenario{}, fmt.Errorf("scenario: parse json %q: %w", path, err)
		}
	} else {
		if err := yaml.Unmarshal(raw, &s); err != nil {
			return Scenario{}, fmt.Errorf("scenario: parse yaml %q: %w", path, err)
		}
	}
	if err := s.Validate(); err != nil {
		return Scenario{}, err
	}
	return s, nil
}

// Validate checks that the scenario is well-formed.
func (s Scenario) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("scenario: missing id")
	}
	if s.FixtureRef == "" {
		return fmt.Errorf("scenario %q: missing fixture_ref", s.ID)
	}
	if strings.Contains(s.FixtureRef, string(os.PathSeparator)) || strings.Contains(s.FixtureRef, ".") {
		return fmt.Errorf("scenario %q: fixture_ref must be a manifest identifier, not a path", s.ID)
	}
	if s.Operation.Name == "" {
		return fmt.Errorf("scenario %q: missing operation name", s.ID)
	}
	if len(s.Expect) == 0 {
		return fmt.Errorf("scenario %q: need at least one expect anchor", s.ID)
	}
	for i, e := range s.Expect {
		if e.Value == "" {
			return fmt.Errorf("scenario %q: expect[%d] has empty value", s.ID, i)
		}
		switch e.Match {
		case MatchExact, MatchContains:
		case "":
			// default to exact
		default:
			return fmt.Errorf("scenario %q: expect[%d] invalid match %q", s.ID, i, e.Match)
		}
		switch e.Kind {
		case ExpectSymbol, ExpectFile, ExpectLine, ExpectOutcome:
		default:
			return fmt.Errorf("scenario %q: expect[%d] invalid kind %q", s.ID, i, e.Kind)
		}
	}
	return nil
}

// Result is the outcome of one scenario run.
type Result struct {
	Outcome      string    `json:"outcome"`
	ResultSize   int       `json:"result_size"`
	Evidence     []string  `json:"evidence"`
	Confidence   *float64  `json:"confidence,omitempty"`
	LatencyMS    int64     `json:"latency_ms"`
	AnchorPresent bool     `json:"anchor_present"`
}

// Engine is the minimal interface the runner needs from graphi.
type Engine interface {
	// Invoke runs the named operation with the given args and returns raw evidence lines.
	Invoke(operation string, args map[string]string) ([]string, *float64, error)
}

// Runner executes scenarios against an engine.
type Runner struct {
	Engine Engine
}

// Run executes one scenario and returns its result.
func (r *Runner) Run(s Scenario) Result {
	start := time.Now()
	outcome := "pass"
	anchorPresent := true

	evidence, conf, err := r.Engine.Invoke(s.Operation.Name, s.Operation.Args)
	if err != nil {
		outcome = "error"
		anchorPresent = false
		evidence = []string{err.Error()}
	}

	// Sort evidence for determinism.
	sortedEvidence := make([]string, len(evidence))
	copy(sortedEvidence, evidence)
	sort.Strings(sortedEvidence)

	resultSize := len(sortedEvidence)

	for _, e := range s.Expect {
		found := false
		for _, line := range sortedEvidence {
			if matches(e, line) {
				found = true
				break
			}
		}
		if !found {
			anchorPresent = false
			if outcome == "pass" {
				outcome = "fail"
			}
		}
	}

	return Result{
		Outcome:       outcome,
		ResultSize:    resultSize,
		Evidence:      sortedEvidence,
		Confidence:    conf,
		LatencyMS:     time.Since(start).Milliseconds(),
		AnchorPresent: anchorPresent,
	}
}

func matches(e Expect, line string) bool {
	switch e.Match {
	case MatchContains:
		return strings.Contains(line, e.Value)
	default:
		return line == e.Value
	}
}

// CoverageReport lists operations with zero scenarios.
func CoverageReport(scenarios []Scenario, ops []string) map[string]bool {
	covered := make(map[string]bool)
	for _, s := range scenarios {
		covered[s.Operation.Name] = true
	}
	gaps := make(map[string]bool)
	for _, op := range ops {
		if !covered[op] {
			gaps[op] = true
		}
	}
	return gaps
}
