package main

// SW-123 (P0-A4): the reference-scenario contract.
//
// PRD §12.2 scopes every performance gate to "the defined reference scenario",
// and that scenario was defined nowhere: "cold index p50 <= 90 s" without a
// named repository and a named machine is a number, not a claim. A gate that
// cannot fail cannot pass either.
//
// docs/eval/reference-scenario.json is that definition in machine-readable
// form — one reference runner class, one reference scenario repository, and
// every §12.2 gate mapped BY NAME to a repository from the SW-122 corpus
// manifest. The harness stories (SW-124 … SW-130) read it instead of each
// restating the thresholds, so there is exactly one place a threshold or a
// scenario can be changed.
//
// This file measures nothing. It loads the contract, validates it fail-closed,
// and refuses the drift the artifact exists to prevent: a second class calling
// itself the reference, a gate pointing at a repository that is not pinned, a
// magnitude threshold of zero, or an OOM "gate" that is a statement of intent
// rather than a method.

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"

	"github.com/samibel/graphi/internal/corpus"
)

// referenceScenarioSchemaVersion is the artifact's schema stamp. A file that
// does not carry exactly this version is rejected rather than best-effort
// parsed: a silently half-understood contract is worse than none.
const referenceScenarioSchemaVersion = 1

// Runner-class roles. There are only two, and they are not symmetric: values
// from a comparison class are never reported as reference values (AC-2).
const (
	roleReference  = "reference"
	roleComparison = "comparison"
)

// oomGateID is the one §12.2 gate whose threshold is legitimately zero — it
// counts OOM kills, and zero kills is the assertion. Every other gate is a
// magnitude, where zero would be a missing value that renders as met.
const oomGateID = "oom_8gb_host"

// peakRSSGateID is the 2 GB reference gate the §17 stop rule must stay wider
// than.
const peakRSSGateID = "peak_rss"

// prdPerformanceGateIDs is PRD §12.2, in order, as identifiers. The artifact
// must map exactly this set: dropping a row would silently retire a gate, and
// inventing one would give P0 a threshold the PRD never agreed.
var prdPerformanceGateIDs = []string{
	"cold_index_p50",
	"cold_index_p95",
	peakRSSGateID,
	oomGateID,
	"db_size",
	"progress_stall_p95",
	"warm_search_p95",
	"caller_callee_impact_p95",
	"agent_context_p95",
	"freshness_p95",
}

// runnerClass documents one machine class. FR-8's acceptance criterion names
// the fields that must be documented (CPU, RAM, OS, kernel, Go version,
// filesystem, cache state); the validator requires all of them on the
// reference class, because an undocumented reference machine makes every
// number it produces unreproducible.
type runnerClass struct {
	ID   string `json:"id"`
	Role string `json:"role"`
	OS   string `json:"os"`
	Arch string `json:"arch,omitempty"`
	CPU  string `json:"cpu"`
	// CPUCores and RAMGB are the two figures a reader compares machines on;
	// they are numeric so a run-time capture can contradict them mechanically.
	CPUCores int     `json:"cpu_cores"`
	RAMGB    float64 `json:"ram_gb"`
	Kernel   string  `json:"kernel"`
	// GoVersion is the toolchain the measured binary is built with.
	GoVersion string `json:"go_version"`
	// Filesystem is the filesystem the workspace and the measured store live
	// on — index wallclock and DB size are both filesystem-dependent.
	Filesystem string `json:"filesystem"`
	// CacheState is the page/build cache protocol, not an observation: it says
	// what a "cold" run means on this class and how it is produced.
	CacheState string `json:"cache_state"`
	// Source records where the declared facts come from, and Verification how
	// a run confirms them. A declaration nobody can check is an assertion.
	Source       string `json:"source"`
	Verification string `json:"verification,omitempty"`
	Evidence     string `json:"evidence,omitempty"`
	Notes        string `json:"notes,omitempty"`
}

// gateMapping binds one PRD §12.2 gate to one pinned repository. Threshold and
// Unit are copied from the PRD — this story locates the numbers, it does not
// move them — and MeasuredBy names the story that will produce the value.
type gateMapping struct {
	ID        string `json:"id"`
	PRDMetric string `json:"prd_metric"`
	// Threshold is the PRD limit; Comparison is how a measurement is read
	// against it (all §12.2 rows are upper limits: "lte").
	Threshold  float64 `json:"threshold"`
	Unit       string  `json:"unit"`
	Comparison string  `json:"comparison"`
	// Repo is the reference scenario for THIS gate, by manifest entry name.
	Repo string `json:"repo"`
	// Operations lists the stable operations pooled into a query-latency
	// class, so "each query-latency class" is mapped, not just named.
	Operations []string `json:"operations,omitempty"`
	MeasuredBy string   `json:"measured_by"`
	Notes      string   `json:"notes,omitempty"`
}

// oomCheck is the 8 GB-host gate as a method: which host, how the limit is
// imposed, how the limit is verified, and what counts as the failure signal.
// Without all four, "no OOM on an 8 GB host" is an intention.
type oomCheck struct {
	GateID     string `json:"gate_id"`
	Repo       string `json:"repo"`
	Host       string `json:"host"`
	LimitBytes int64  `json:"limit_bytes"`
	Impose     string `json:"impose"`
	Verify     string `json:"verify"`
	// FailureSignal is what makes the gate FAIL. Stating it is what stops the
	// check from passing merely because nothing was observed.
	FailureSignal string `json:"failure_signal"`
	Notes         string `json:"notes,omitempty"`
}

// stopRuleObservation records a measured peak already known to bear on the
// program-wide stop rule, with its provenance and its confidence.
type stopRuleObservation struct {
	Repo        string  `json:"repo"`
	RunnerClass string  `json:"runner_class"`
	PeakRSSGB   float64 `json:"peak_rss_gb"`
	Source      string  `json:"source"`
	// Status says how far this observation can be trusted; it is never a gate
	// result.
	Status string `json:"status"`
}

// stopRule is PRD §17's program-wide 4 GB peak-RSS stop rule. It is strictly
// wider than the §12.2 gate and never a milder alternative to it.
type stopRule struct {
	ID           string                `json:"id"`
	ThresholdGB  float64               `json:"threshold_gb"`
	AppliesTo    string                `json:"applies_to"`
	Relation     string                `json:"relation"`
	Effect       string                `json:"effect"`
	Observations []stopRuleObservation `json:"observations,omitempty"`
}

// budgetsRef is the artifact's statement about docs/eval/hero-budgets.json.
// AC-5 allows exactly two states: re-baselined against the re-frozen
// candidate, or explicitly historical and non-ratcheting. The validator
// forbids the third state the repository was actually in — numbers that read
// as ratchets while carrying no signal.
type budgetsRef struct {
	Path        string `json:"path"`
	Historical  bool   `json:"historical"`
	Ratcheting  bool   `json:"ratcheting"`
	Reason      string `json:"reason"`
	BlockedOn   string `json:"rebaseline_blocked_on,omitempty"`
	StillGating string `json:"still_gating,omitempty"`
}

// referenceScenarioRepo names THE reference scenario and says why it is the
// one, including what the choice does not prove.
type referenceScenarioRepo struct {
	Repo           string `json:"repo"`
	CorpusManifest string `json:"corpus_manifest"`
	Rationale      string `json:"rationale"`
	Headroom       string `json:"headroom,omitempty"`
}

// referenceScenario is the whole contract.
type referenceScenario struct {
	SchemaVersion int    `json:"schema_version"`
	Notes         string `json:"notes,omitempty"`
	// ScopeLimitation carries FR-8's limitation inline, so a consumer that
	// reads only this file cannot publish the gates as universal guarantees.
	ScopeLimitation   string                `json:"scope_limitation"`
	RunnerClasses     []runnerClass         `json:"runner_classes"`
	ReferenceScenario referenceScenarioRepo `json:"reference_scenario"`
	Gates             []gateMapping         `json:"gates"`
	OOMCheck          oomCheck              `json:"oom_check"`
	StopRule          stopRule              `json:"stop_rule"`
	Budgets           budgetsRef            `json:"budgets"`
	// MeasuredNotGated records the FR-8 measurements that carry no §12.2 gate,
	// so "no gate" is a recorded decision rather than an omission a later
	// reader mistakes for coverage.
	MeasuredNotGated []string `json:"measured_not_gated,omitempty"`
}

// referenceClass returns the single class declared as the reference. Callers
// that have run validateReferenceScenario can rely on ok being true.
func (rs referenceScenario) referenceClass() (runnerClass, bool) {
	for _, c := range rs.RunnerClasses {
		if c.Role == roleReference {
			return c, true
		}
	}
	return runnerClass{}, false
}

// classRole reports the declared role of a runner-class id. An id the artifact
// does not declare has no role — the caller must fail closed rather than
// guess, which is how numbers from unnamed machines stop sitting beside
// reference values with equal standing.
func (rs referenceScenario) classRole(id string) (string, bool) {
	for _, c := range rs.RunnerClasses {
		if c.ID == id {
			return c.Role, true
		}
	}
	return "", false
}

// loadReferenceScenario reads and parses the artifact. A missing or malformed
// file is an error, never an empty contract.
func loadReferenceScenario(path string) (referenceScenario, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return referenceScenario{}, fmt.Errorf("read %s: %w", path, err)
	}
	var rs referenceScenario
	if err := json.Unmarshal(raw, &rs); err != nil {
		return referenceScenario{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return rs, nil
}

// corpusRepoNames returns the set of entry names in the corpus manifest. It
// goes through corpus.LoadManifest so the reference scenario can only point at
// repositories that pass the manifest's own (SW-122) validation.
func corpusRepoNames(manifestPath string) (map[string]bool, error) {
	m, err := corpus.LoadManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(m.Entries))
	for _, e := range m.Entries {
		names[e.Name] = true
	}
	return names, nil
}

// validateReferenceScenario enforces the whole contract. Every rule below is
// one way the artifact could quietly stop meaning anything.
func validateReferenceScenario(rs referenceScenario, repos map[string]bool) error {
	if rs.SchemaVersion != referenceScenarioSchemaVersion {
		return fmt.Errorf("unsupported schema_version %d (want %d)", rs.SchemaVersion, referenceScenarioSchemaVersion)
	}
	if rs.ScopeLimitation == "" {
		return fmt.Errorf("scope_limitation is empty: FR-8's limitation must travel with the gates")
	}

	if err := validateRunnerClasses(rs.RunnerClasses); err != nil {
		return err
	}

	if rs.ReferenceScenario.Repo == "" {
		return fmt.Errorf("reference_scenario.repo is empty")
	}
	if !repos[rs.ReferenceScenario.Repo] {
		return fmt.Errorf("reference_scenario.repo %q is not an entry in the corpus manifest", rs.ReferenceScenario.Repo)
	}
	if rs.ReferenceScenario.Rationale == "" {
		return fmt.Errorf("reference_scenario needs a rationale: an unexplained reference scenario cannot be argued with")
	}

	if err := validateGates(rs.Gates, repos); err != nil {
		return err
	}
	if err := validateOOMCheck(rs.OOMCheck, repos); err != nil {
		return err
	}
	if err := validateStopRule(rs.StopRule, rs.Gates); err != nil {
		return err
	}
	return validateBudgetsRef(rs.Budgets)
}

func validateRunnerClasses(classes []runnerClass) error {
	if len(classes) == 0 {
		return fmt.Errorf("no runner classes declared")
	}
	seen := map[string]bool{}
	references := 0
	for i, c := range classes {
		if c.ID == "" {
			return fmt.Errorf("runner class %d has no id", i)
		}
		if seen[c.ID] {
			return fmt.Errorf("runner class %q is declared twice", c.ID)
		}
		seen[c.ID] = true
		switch c.Role {
		case roleReference:
			references++
			if err := requireEnvironment(c); err != nil {
				return err
			}
		case roleComparison:
			if c.Notes == "" {
				return fmt.Errorf("comparison class %q must state that its numbers are never reference values", c.ID)
			}
		default:
			return fmt.Errorf("runner class %q has role %q (want %q or %q)", c.ID, c.Role, roleReference, roleComparison)
		}
		if c.Source == "" {
			return fmt.Errorf("runner class %q records no source for its declared environment", c.ID)
		}
	}
	if references != 1 {
		return fmt.Errorf("exactly one runner class must be the reference, found %d", references)
	}
	return nil
}

// requireEnvironment is FR-8's acceptance criterion, mechanically.
func requireEnvironment(c runnerClass) error {
	missing := []string{}
	if c.CPU == "" {
		missing = append(missing, "cpu")
	}
	if c.CPUCores <= 0 {
		missing = append(missing, "cpu_cores")
	}
	if c.RAMGB <= 0 {
		missing = append(missing, "ram_gb")
	}
	if c.OS == "" {
		missing = append(missing, "os")
	}
	if c.Kernel == "" {
		missing = append(missing, "kernel")
	}
	if c.GoVersion == "" {
		missing = append(missing, "go_version")
	}
	if c.Filesystem == "" {
		missing = append(missing, "filesystem")
	}
	if c.CacheState == "" {
		missing = append(missing, "cache_state")
	}
	if len(missing) > 0 {
		return fmt.Errorf("reference runner class %q is undocumented: missing %v (FR-8 requires CPU, RAM, OS, kernel, Go version, filesystem and cache state)", c.ID, missing)
	}
	return nil
}

func validateGates(gates []gateMapping, repos map[string]bool) error {
	seen := map[string]bool{}
	for _, g := range gates {
		if g.ID == "" {
			return fmt.Errorf("a gate has no id")
		}
		if seen[g.ID] {
			return fmt.Errorf("gate %q is declared twice", g.ID)
		}
		seen[g.ID] = true
		if !slices.Contains(prdPerformanceGateIDs, g.ID) {
			return fmt.Errorf("gate %q is not a PRD §12.2 row: this artifact locates the PRD's gates, it does not add any", g.ID)
		}
		if g.PRDMetric == "" {
			return fmt.Errorf("gate %q does not name its PRD metric", g.ID)
		}
		if g.Comparison != "lte" {
			return fmt.Errorf("gate %q comparison %q: every §12.2 row is an upper limit (lte)", g.ID, g.Comparison)
		}
		if g.Unit == "" {
			return fmt.Errorf("gate %q has no unit: a bare number is not a threshold", g.ID)
		}
		// A zero magnitude threshold is the failure this story exists to
		// remove: it renders as met while carrying no signal. The OOM gate is
		// the single legitimate zero — it counts kills.
		if g.Threshold < 0 || (g.Threshold == 0 && g.ID != oomGateID) {
			return fmt.Errorf("gate %q has threshold %v: a zero or negative magnitude budget silently reads as passing", g.ID, g.Threshold)
		}
		if !repos[g.Repo] {
			return fmt.Errorf("gate %q maps to repository %q, which is not an entry in the corpus manifest", g.ID, g.Repo)
		}
		if g.MeasuredBy == "" {
			return fmt.Errorf("gate %q names no story that will measure it", g.ID)
		}
	}
	for _, id := range prdPerformanceGateIDs {
		if !seen[id] {
			return fmt.Errorf("PRD §12.2 gate %q is not mapped to a repository", id)
		}
	}
	return nil
}

func validateOOMCheck(o oomCheck, repos map[string]bool) error {
	if o.GateID != oomGateID {
		return fmt.Errorf("oom_check.gate_id = %q, want %q", o.GateID, oomGateID)
	}
	if !repos[o.Repo] {
		return fmt.Errorf("oom_check maps to repository %q, which is not an entry in the corpus manifest", o.Repo)
	}
	if o.Host == "" {
		return fmt.Errorf("oom_check does not name the host the limit is imposed on")
	}
	if o.LimitBytes <= 0 {
		return fmt.Errorf("oom_check.limit_bytes = %d: the imposed limit must be an exact byte figure", o.LimitBytes)
	}
	if o.Impose == "" {
		return fmt.Errorf("oom_check does not say how the memory limit is imposed")
	}
	if o.Verify == "" {
		return fmt.Errorf("oom_check does not say how the imposed limit is verified: an unverified limit means the check proves nothing")
	}
	if o.FailureSignal == "" {
		return fmt.Errorf("oom_check does not say what makes it FAIL: a check without a failure signal always passes")
	}
	return nil
}

func validateStopRule(s stopRule, gates []gateMapping) error {
	if s.ID == "" {
		return fmt.Errorf("stop_rule has no id")
	}
	if s.ThresholdGB <= 0 {
		return fmt.Errorf("stop_rule.threshold_gb = %v", s.ThresholdGB)
	}
	if s.AppliesTo == "" {
		return fmt.Errorf("stop_rule does not say what it applies to")
	}
	for _, g := range gates {
		if g.ID != peakRSSGateID {
			continue
		}
		gateGB := g.Threshold
		if g.Unit == "MB" {
			gateGB = g.Threshold / 1024
		}
		if s.ThresholdGB <= gateGB {
			return fmt.Errorf("stop_rule %v GB is not wider than the %s gate (%v %s): the stop rule is never a milder alternative to the gate (FR-8)", s.ThresholdGB, peakRSSGateID, g.Threshold, g.Unit)
		}
	}
	for i, o := range s.Observations {
		if o.Repo == "" || o.Source == "" || o.Status == "" {
			return fmt.Errorf("stop_rule observation %d needs a repo, a source and a status", i)
		}
		if o.PeakRSSGB <= 0 {
			return fmt.Errorf("stop_rule observation %d (%s) records no peak", i, o.Repo)
		}
	}
	return nil
}

func validateBudgetsRef(b budgetsRef) error {
	if b.Path == "" {
		return fmt.Errorf("budgets.path is empty")
	}
	if b.Historical && b.Ratcheting {
		return fmt.Errorf("budgets cannot be historical AND ratcheting: %s is one or the other", b.Path)
	}
	if b.Historical && b.Reason == "" {
		return fmt.Errorf("historical budgets must record why they are not comparable ratchets")
	}
	return nil
}

// Default artifact locations for the operator check. They are the checked-in
// paths; the flags exist so a test or a fork can point elsewhere, not so the
// check can be run against nothing.
const (
	defaultReferenceScenarioPath = "docs/eval/reference-scenario.json"
	defaultCorpusManifestPath    = "corpus/manifest.json"
	defaultBudgetsPath           = "docs/eval/hero-budgets.json"
)

// runReferenceScenarioCheck is `go run ./cmd/eval -check-reference-scenario`:
// the operator-facing form of the drift tests. It answers one question — is
// the measurement contract still internally consistent and still consistent
// with the pinned corpus — and it fails closed on a missing or malformed
// artifact rather than reporting a contract it could not read.
//
// Exit codes: 0 green, 1 contract violation, 2 could not be read.
func runReferenceScenarioCheck(scenarioPath, manifestPath, budgetPath string, out io.Writer) int {
	rs, err := loadReferenceScenario(scenarioPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: reference scenario: %v\n", err)
		return 2
	}
	repos, err := corpusRepoNames(manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: corpus manifest: %v\n", err)
		return 2
	}
	if err := validateReferenceScenario(rs, repos); err != nil {
		fmt.Fprintf(os.Stderr, "eval: FAIL - reference scenario %s: %v\n", scenarioPath, err)
		return 1
	}
	budgets, err := loadBudgetManifest(budgetPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: budgets: %v\n", err)
		return 2
	}
	ref, _ := rs.referenceClass()
	if budgets.RunnerClass != ref.ID {
		fmt.Fprintf(os.Stderr, "eval: FAIL - %s records runner_class %q but the reference class is %q; budgets must never be frozen from a comparison run\n",
			budgetPath, budgets.RunnerClass, ref.ID)
		return 1
	}

	fmt.Fprintf(out, "reference runner class: %s (%s, %d vCPU, %g GB RAM)\n", ref.ID, ref.OS, ref.CPUCores, ref.RAMGB)
	fmt.Fprintf(out, "reference scenario:     %s (from %s)\n", rs.ReferenceScenario.Repo, rs.ReferenceScenario.CorpusManifest)
	for _, c := range rs.RunnerClasses {
		if c.Role == roleComparison {
			fmt.Fprintf(out, "comparison class:       %s (never reported as reference values)\n", c.ID)
		}
	}
	for _, g := range rs.Gates {
		fmt.Fprintf(out, "gate %-26s %s %-6g %-9s -> %s (%s)\n", g.ID, g.Comparison, g.Threshold, g.Unit, g.Repo, g.MeasuredBy)
	}
	fmt.Fprintf(out, "stop rule:              %g GB over %s\n", rs.StopRule.ThresholdGB, rs.StopRule.AppliesTo)
	fmt.Fprintf(out, "budgets:                %s (historical=%v ratcheting=%v)\n", budgets2Path(rs, budgetPath), budgets.Historical, budgets.Ratcheting)
	fmt.Fprintf(os.Stderr, "eval: PASS - reference scenario contract consistent (%d gates, all mapped to pinned repositories)\n", len(rs.Gates))
	return 0
}

// budgets2Path prefers the path the contract itself declares, so the summary
// reports the artifact the contract points at rather than whatever the caller
// happened to pass.
func budgets2Path(rs referenceScenario, fallback string) string {
	if rs.Budgets.Path != "" {
		return rs.Budgets.Path
	}
	return fallback
}

// validateNoSilentZeroBudgets is AC-5 applied structurally to the budget
// artifact: a numeric zero in a budget file is either dead data or a threshold
// that renders as met. Neither may be shipped, so the rule is "no zero
// anywhere", which needs no allow-list to stay honest.
func validateNoSilentZeroBudgets(raw []byte) error {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse budgets: %w", err)
	}
	return walkForZero("", doc)
}

func walkForZero(path string, v any) error {
	switch t := v.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(t)) {
			if err := walkForZero(path+"."+k, t[k]); err != nil {
				return err
			}
		}
	case []any:
		for i, item := range t {
			if err := walkForZero(fmt.Sprintf("%s[%d]", path, i), item); err != nil {
				return err
			}
		}
	case float64:
		if t == 0 {
			return fmt.Errorf("budget value %s is 0: a zero budget carries no signal and silently reads as passing (remove it or record the metric as unmeasured elsewhere)", path)
		}
	}
	return nil
}
