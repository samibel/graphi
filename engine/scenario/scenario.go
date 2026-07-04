// Package scenario defines the eval-harness scenario schema and runner. A
// scenario binds a corpus fixture to one graphi operation (structural query,
// lexical search, or one of the EP-020 agent tools) and to a set of
// expectations. Expectations come in two forms that may be combined:
//
//   - anchored expectations (the original list form): a value that must appear
//     in the produced evidence lines, and
//   - the PRD expectation fields (mapping form): expect.outcome,
//     expect.contains_path, expect.max_latency_ms, expect.has_evidence.
//
// The runner records, per scenario: pass/fail outcome, the tool-level
// operation outcome, latency, evidence count, top confidence label, answer
// size, and anchor presence.
package scenario

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/samibel/graphi/engine/agenttools/contract"
)

// Operation names for the scenario runner.
const (
	OpDefinition = "definition"
	OpReferences = "references"
	OpCallers    = "callers"
	OpSearch     = "search"

	// EP-020 agent-tool operations, executed against the fixture store via the
	// engine/agenttools packages.
	OpExplainSymbol = "explain_symbol"
	OpRelatedFiles  = "related_files"
	OpChangeRisk    = "change_risk"
	OpAgentBrief    = "agent_brief"
)

// KnownOps returns every operation name the runner can execute.
func KnownOps() []string {
	return []string{
		OpDefinition, OpReferences, OpCallers, OpSearch,
		OpExplainSymbol, OpRelatedFiles, OpChangeRisk, OpAgentBrief,
	}
}

// IsAgentToolOp reports whether name is one of the four EP-020 agent-tool
// operations (which return the full C1 contract envelope).
func IsAgentToolOp(name string) bool {
	switch name {
	case OpExplainSymbol, OpRelatedFiles, OpChangeRisk, OpAgentBrief:
		return true
	}
	return false
}

func isKnownOp(name string) bool {
	for _, op := range KnownOps() {
		if op == name {
			return true
		}
	}
	return false
}

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

// ExpectBlock is the scenario's expectation set. It accepts two serialized
// forms under the `expect` key:
//
//	expect:                       # legacy anchored list
//	  - kind: symbol
//	    value: Hello
//
//	expect:                       # PRD expectation fields (mapping form)
//	  outcome: found
//	  contains_path: sample.go
//	  max_latency_ms: 2000
//	  has_evidence: true
//	  anchors:                    # optional anchored list inside the mapping
//	    - kind: symbol
//	      value: Hello
type ExpectBlock struct {
	// Anchors are the legacy anchored expectations.
	Anchors []Expect `json:"anchors,omitempty" yaml:"anchors,omitempty"`
	// Outcome, when non-empty, must equal the tool-level operation outcome
	// (found/partial/ambiguous/empty/unavailable/error/ok).
	Outcome string `json:"outcome,omitempty" yaml:"outcome,omitempty"`
	// ContainsPath, when non-empty, requires some item ref_id or evidence path
	// to contain the substring.
	ContainsPath string `json:"contains_path,omitempty" yaml:"contains_path,omitempty"`
	// MaxLatencyMS, when > 0, fails the scenario when the measured operation
	// latency exceeds it.
	MaxLatencyMS int `json:"max_latency_ms,omitempty" yaml:"max_latency_ms,omitempty"`
	// HasEvidence, when true, requires a non-empty evidence list.
	HasEvidence bool `json:"has_evidence,omitempty" yaml:"has_evidence,omitempty"`
	// Absent anchors must NOT match any evidence line (negative assertion).
	Absent []Expect `json:"absent,omitempty" yaml:"absent,omitempty"`
}

// IsZero reports whether the block declares no expectation at all.
func (b ExpectBlock) IsZero() bool {
	return len(b.Anchors) == 0 && b.Outcome == "" && b.ContainsPath == "" &&
		b.MaxLatencyMS == 0 && !b.HasEvidence && len(b.Absent) == 0
}

// hasPRDFields reports whether any PRD (non-anchor) field is set.
func (b ExpectBlock) hasPRDFields() bool {
	return b.Outcome != "" || b.ContainsPath != "" || b.MaxLatencyMS != 0 || b.HasEvidence || len(b.Absent) > 0
}

// expectFields is the mapping-form wire shape (avoids unmarshal recursion).
type expectFields struct {
	Anchors      []Expect `json:"anchors,omitempty" yaml:"anchors,omitempty"`
	Outcome      string   `json:"outcome,omitempty" yaml:"outcome,omitempty"`
	ContainsPath string   `json:"contains_path,omitempty" yaml:"contains_path,omitempty"`
	MaxLatencyMS int      `json:"max_latency_ms,omitempty" yaml:"max_latency_ms,omitempty"`
	HasEvidence  bool     `json:"has_evidence,omitempty" yaml:"has_evidence,omitempty"`
	Absent       []Expect `json:"absent,omitempty" yaml:"absent,omitempty"`
}

// UnmarshalYAML accepts both the legacy sequence form and the mapping form.
func (b *ExpectBlock) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.SequenceNode:
		return node.Decode(&b.Anchors)
	case yaml.MappingNode:
		var f expectFields
		if err := node.Decode(&f); err != nil {
			return err
		}
		*b = ExpectBlock(f)
		return nil
	default:
		return fmt.Errorf("scenario: expect must be a list of anchors or a mapping of expectation fields")
	}
}

// MarshalYAML emits the legacy sequence form when only anchors are set.
func (b ExpectBlock) MarshalYAML() (interface{}, error) {
	if !b.hasPRDFields() {
		return b.Anchors, nil
	}
	return expectFields(b), nil
}

// UnmarshalJSON accepts both the legacy array form and the object form.
func (b *ExpectBlock) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		return json.Unmarshal(trimmed, &b.Anchors)
	}
	var f expectFields
	if err := json.Unmarshal(trimmed, &f); err != nil {
		return err
	}
	*b = ExpectBlock(f)
	return nil
}

// MarshalJSON emits the legacy array form when only anchors are set.
func (b ExpectBlock) MarshalJSON() ([]byte, error) {
	if !b.hasPRDFields() {
		if b.Anchors == nil {
			return []byte("[]"), nil
		}
		return json.Marshal(b.Anchors)
	}
	return json.Marshal(expectFields(b))
}

// Scenario binds a fixture to an operation and expectations.
type Scenario struct {
	ID          string      `json:"id" yaml:"id"`
	FixtureRef  string      `json:"fixture_ref" yaml:"fixture_ref"`
	Operation   Operation   `json:"operation" yaml:"operation"`
	Expect      ExpectBlock `json:"expect" yaml:"expect"`
	Description string      `json:"description,omitempty" yaml:"description,omitempty"`
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

// validExpectedOutcomes is the closed vocabulary accepted by expect.outcome:
// the contract outcomes plus the structural-query "not_found".
var validExpectedOutcomes = map[string]bool{
	string(contract.OutcomeOK):          true,
	string(contract.OutcomeFound):       true,
	string(contract.OutcomePartial):     true,
	string(contract.OutcomeAmbiguous):   true,
	string(contract.OutcomeEmpty):       true,
	string(contract.OutcomeUnavailable): true,
	string(contract.OutcomeError):       true,
	"not_found":                         true,
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
	if !isKnownOp(s.Operation.Name) {
		return fmt.Errorf("scenario %q: unknown operation %q (known: %s)", s.ID, s.Operation.Name, strings.Join(KnownOps(), ", "))
	}
	if s.Expect.IsZero() {
		return fmt.Errorf("scenario %q: need at least one expect anchor or expectation field", s.ID)
	}
	if s.Expect.Outcome != "" && !validExpectedOutcomes[s.Expect.Outcome] {
		return fmt.Errorf("scenario %q: expect.outcome %q is not a known outcome", s.ID, s.Expect.Outcome)
	}
	if s.Expect.MaxLatencyMS < 0 {
		return fmt.Errorf("scenario %q: expect.max_latency_ms must not be negative", s.ID)
	}
	for i, e := range append(append([]Expect(nil), s.Expect.Anchors...), s.Expect.Absent...) {
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
	// Outcome is the scenario verdict: pass, fail, or error.
	Outcome    string   `json:"outcome"`
	ResultSize int      `json:"result_size"`
	Evidence   []string `json:"evidence"`
	Confidence *float64 `json:"confidence,omitempty"`
	LatencyMS  int64    `json:"latency_ms"`
	// AnchorPresent reports whether every anchored expectation was found
	// (vacuously true when the scenario declares none and the run succeeded).
	AnchorPresent bool `json:"anchor_present"`

	// OpOutcome is the tool-level operation outcome (found/partial/ambiguous/
	// empty/unavailable/error/ok — or found/empty derived for legacy ops).
	OpOutcome string `json:"op_outcome"`
	// EvidenceCount is the number of evidence citations produced.
	EvidenceCount int `json:"evidence_count"`
	// ConfidenceTop is the top confidence label of a contract result ("" for
	// legacy operations).
	ConfidenceTop string `json:"confidence_top,omitempty"`
	// AnswerSize is the serialized answer size in bytes.
	AnswerSize int `json:"answer_size"`
	// Failures lists the expectation checks that did not hold.
	Failures []string `json:"failures,omitempty"`
}

// Engine is the minimal interface the runner needs from graphi.
type Engine interface {
	// Invoke runs the named operation with the given args and returns raw evidence lines.
	Invoke(operation string, args map[string]string) ([]string, *float64, error)
}

// ContractInvoker is implemented by engines that can execute the agent-tool
// operations and return the full C1 contract envelope. The runner uses it for
// the agent-tool operations when available, falling back to Engine.Invoke.
type ContractInvoker interface {
	InvokeContract(ctx context.Context, operation string, args map[string]string) (*contract.Result, error)
}

// Runner executes scenarios against an engine.
type Runner struct {
	Engine Engine
}

// Run executes one scenario with a background context.
func (r *Runner) Run(s Scenario) Result {
	return r.RunContext(context.Background(), s)
}

// RunContext executes one scenario and returns its result.
func (r *Runner) RunContext(ctx context.Context, s Scenario) Result {
	ci, _ := r.Engine.(ContractInvoker)
	if IsAgentToolOp(s.Operation.Name) && ci != nil {
		return r.runContract(ctx, ci, s)
	}
	return r.runLegacy(s)
}

// runLegacy executes an operation through the plain Invoke seam.
func (r *Runner) runLegacy(s Scenario) Result {
	start := time.Now()
	evidence, conf, err := r.Engine.Invoke(s.Operation.Name, s.Operation.Args)
	latency := time.Since(start)

	res := Result{Confidence: conf, LatencyMS: latency.Milliseconds()}
	if err != nil {
		res.OpOutcome = string(contract.OutcomeError)
		evidence = []string{err.Error()}
	} else if len(evidence) > 0 {
		res.OpOutcome = string(contract.OutcomeFound)
	} else {
		res.OpOutcome = string(contract.OutcomeEmpty)
	}

	// Legacy operations may report a richer outcome as a marker line
	// ("outcome:<value>", emitted by FixtureEngine); consume it.
	kept := evidence[:0:0]
	for _, line := range evidence {
		if v, ok := strings.CutPrefix(line, outcomeMarker); ok {
			res.OpOutcome = v
			continue
		}
		kept = append(kept, line)
	}
	evidence = kept

	// Sort evidence for determinism.
	sorted := make([]string, len(evidence))
	copy(sorted, evidence)
	sort.Strings(sorted)

	res.Evidence = sorted
	res.ResultSize = len(sorted)
	res.EvidenceCount = len(sorted)
	for _, line := range sorted {
		res.AnswerSize += len(line) + 1
	}

	finish(&res, s, err != nil, func(needle string) bool {
		return anyContains(sorted, needle)
	})
	return res
}

// runContract executes an agent-tool operation through the contract seam.
func (r *Runner) runContract(ctx context.Context, ci ContractInvoker, s Scenario) Result {
	start := time.Now()
	cr, err := ci.InvokeContract(ctx, s.Operation.Name, s.Operation.Args)
	latency := time.Since(start)

	res := Result{LatencyMS: latency.Milliseconds()}
	if err != nil || cr == nil {
		if err == nil {
			err = fmt.Errorf("nil contract result")
		}
		res.OpOutcome = string(contract.OutcomeError)
		res.Evidence = []string{err.Error()}
		finish(&res, s, true, func(string) bool { return false })
		return res
	}

	res.OpOutcome = string(cr.Outcome)
	res.EvidenceCount = len(cr.Evidence)
	res.ConfidenceTop = cr.Confidence.Top
	if raw, mErr := json.Marshal(cr); mErr == nil {
		res.AnswerSize = len(raw)
	}

	lines := make([]string, 0, len(cr.Items)+len(cr.Evidence))
	for _, it := range cr.Items {
		lines = append(lines, fmt.Sprintf("item %s: %s", it.RefID, it.Reason))
	}
	for _, ev := range cr.Evidence {
		lines = append(lines, fmt.Sprintf("evidence %s:%d [%s]", ev.Path, ev.Line, ev.Role))
	}
	sort.Strings(lines)
	res.Evidence = lines
	res.ResultSize = len(cr.Items)

	finish(&res, s, false, func(needle string) bool {
		for _, it := range cr.Items {
			if strings.Contains(it.RefID, needle) {
				return true
			}
		}
		for _, ev := range cr.Evidence {
			if strings.Contains(ev.Path, needle) {
				return true
			}
		}
		return false
	})
	return res
}

// outcomeMarker prefixes an evidence line that carries a legacy operation's
// tool-level outcome instead of a real evidence citation.
const outcomeMarker = "outcome:"

// finish evaluates the scenario expectations against the collected result
// fields, filling Outcome, AnchorPresent, and Failures.
func finish(res *Result, s Scenario, hadError bool, containsPath func(string) bool) {
	res.AnchorPresent = !hadError

	for _, e := range s.Expect.Anchors {
		if anchorFound(e, res) {
			continue
		}
		res.AnchorPresent = false
		res.Failures = append(res.Failures, fmt.Sprintf("anchor %s %q not present", e.Kind, e.Value))
	}
	if want := s.Expect.Outcome; want != "" && res.OpOutcome != want {
		res.Failures = append(res.Failures, fmt.Sprintf("outcome %q, want %q", res.OpOutcome, want))
	}
	if p := s.Expect.ContainsPath; p != "" && !containsPath(p) {
		res.Failures = append(res.Failures, fmt.Sprintf("no item ref_id or evidence path contains %q", p))
	}
	if maxMS := s.Expect.MaxLatencyMS; maxMS > 0 && res.LatencyMS > int64(maxMS) {
		res.Failures = append(res.Failures, fmt.Sprintf("latency %dms exceeds max_latency_ms %d", res.LatencyMS, maxMS))
	}
	if s.Expect.HasEvidence && res.EvidenceCount == 0 {
		res.Failures = append(res.Failures, "expected non-empty evidence, got none")
	}
	for _, e := range s.Expect.Absent {
		if anchorFound(e, res) {
			res.Failures = append(res.Failures, fmt.Sprintf("absent anchor %s %q unexpectedly present", e.Kind, e.Value))
		}
	}

	switch {
	case hadError:
		res.Outcome = "error"
	case len(res.Failures) > 0:
		res.Outcome = "fail"
	default:
		res.Outcome = "pass"
	}
}

// anchorFound reports whether one anchored expectation holds: any evidence
// line matches, or — for outcome anchors — the tool-level outcome matches.
func anchorFound(e Expect, res *Result) bool {
	if e.Kind == ExpectOutcome && matches(e, res.OpOutcome) {
		return true
	}
	for _, line := range res.Evidence {
		if matches(e, line) {
			return true
		}
	}
	return false
}

func anyContains(lines []string, needle string) bool {
	for _, line := range lines {
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}

func matches(e Expect, line string) bool {
	switch e.Match {
	case MatchContains:
		return strings.Contains(line, e.Value)
	default:
		if line == e.Value {
			return true
		}
		// Exact symbol/file anchors also match a whitespace-separated field of
		// the evidence line, or a field's suffix after the last dot (so the
		// anchor "Hello" matches the qualified name "fixture.Hello").
		for _, field := range strings.Fields(line) {
			if field == e.Value {
				return true
			}
			if i := strings.LastIndex(field, "."); i >= 0 && field[i+1:] == e.Value {
				return true
			}
		}
		return false
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
