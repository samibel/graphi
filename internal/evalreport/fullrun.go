package evalreport

// SW-123 (EVAL-02): the full-run raw-evidence payload. One report = one pinned
// corpus repository measured end-to-end in ONE process (clone → index → warm
// query classes), so the recorded peak RSS is attributable to that repo alone.
// These reports are raw performance evidence. Historical reports may use an
// older measurement method, so budget comparability is established by the
// harness metadata, not merely by matching runner class. Reports are published
// as CI artifacts and may be committed under docs/eval/runs/.

import (
	"encoding/json"
	"fmt"
	"os"
)

// FullRunReport is the top-level envelope for one repo's full run.
type FullRunReport struct {
	Header Header `json:"header"`
	// RunnerClass names the machine class the numbers were recorded on
	// (e.g. "ubuntu-latest"). Budgets are only ever frozen from runs on the
	// reference runner class; anything else is a smoke run.
	RunnerClass string `json:"runner_class"`
	// RunnerRole is the class's DECLARED role in the SW-123 reference-scenario
	// contract — "reference" or "comparison". It is empty only when no
	// contract was supplied. Recording the role beside the class is what stops
	// a comparison run from later reading as reference evidence just because
	// its class name looked plausible.
	RunnerRole string `json:"runner_role,omitempty"`
	// ReferenceScenario is true only when BOTH the runner class is the
	// reference class AND the measured repository is the reference scenario.
	// Only such a run may be read against the PRD §12.2 gates.
	ReferenceScenario bool `json:"reference_scenario"`
	// ScenarioSource is the contract this run was validated against.
	ScenarioSource string `json:"scenario_source,omitempty"`
	// Notes documents the measurement model (in-process session, sample
	// sizes) so a reader can interpret the numbers without the source.
	Notes string      `json:"notes,omitempty"`
	Repo  FullRepoRun `json:"repo"`
	// RepoRunIndex says WHICH run of a cold series `Repo` is (1-based), and is
	// absent for the single-run path. Without it a reader of a repeated
	// measurement could mistake one arbitrary sample for the result; with it,
	// `repo` stays a comparable single sample and every distributional claim
	// lives in ColdSeries.
	RepoRunIndex int `json:"repo_run_index,omitempty"`
	// ColdSeries is SW-124's repeated cold-index measurement. It is a pointer
	// and absent by default: the single-run report shape the PR path and the
	// committed historical runs use is byte-unchanged when no series was asked
	// for.
	ColdSeries *ColdRunSeries `json:"cold_series,omitempty"`
	// Cgroup is the memory limit the measuring process itself ran under, read
	// from inside that process (Linux cgroup v2 only). It is what makes the
	// SW-123 OOM method verifiable rather than merely intended.
	Cgroup *CgroupLimits `json:"cgroup,omitempty"`
}

// FullRepoRun is the per-repository measurement set.
type FullRepoRun struct {
	Name string `json:"name"`
	// Ref/SHA document the pin actually checked out (empty for local-path
	// fixture entries, which need no clone).
	Ref     string `json:"ref,omitempty"`
	SHA     string `json:"sha,omitempty"`
	Tier    int    `json:"tier"`
	CloneMS int64  `json:"clone_ms,omitempty"`

	Index IndexMetrics `json:"index"`
	// Cold is the per-run evidence that this index really was cold: a store
	// that did not pre-exist and a page cache in the state the runner class
	// declares. SW-124 (AC-1) — coldness is verified per run, not assumed from
	// the fact that a fresh temp directory was requested.
	Cold ColdState `json:"cold"`

	// WarmP95US is the p95 latency in MICROSECONDS per operation class
	// (structural, search, agent_tools) over the warm, already-indexed store
	// in the same session. Microseconds because the selective-read stable ops
	// are routinely sub-millisecond and a 0ms value cannot ratchet.
	WarmP95US map[string]int64 `json:"warm_p95_us"`
	// WarmP95USPerOp resolves the class pools to the individual operations, so
	// a class regression is attributable (e.g. ADR 0003 U2: whether agent_brief
	// or explain_symbol dominates the agent_tools class).
	WarmP95USPerOp map[string]int64 `json:"warm_p95_us_per_op"`
	// WarmSamples is the number of timed invocations pooled per class.
	WarmSamples map[string]int `json:"warm_samples"`
	// WarmOps lists the concrete operations pooled into each class, so the
	// class p95 is interpretable and re-runnable.
	WarmOps map[string][]string `json:"warm_ops"`

	// Searches are the manifest's expect_nonempty smoke assertions re-checked
	// against this run's index.
	Searches []SearchCheck `json:"searches,omitempty"`

	// StablePeakRSSMB is getrusage MAXRSS sampled after the complete stable-op
	// warm suite. Because MAXRSS is process-lifetime, it covers both indexing
	// and stable reads and exposes accidental full-graph materialization.
	StablePeakRSSMB int64 `json:"stable_peak_rss_mb"`
	// BudgetSource and BudgetChecks make enforcement part of the evidence, not
	// an unauditable workflow-side comparison.
	BudgetSource string      `json:"budget_source,omitempty"`
	BudgetChecks []PerfCheck `json:"budget_checks,omitempty"`

	// StableChecks records semantic outcome validation for every frozen stable
	// operation. Latency samples count only after the response resolved to an
	// operation-appropriate outcome; "no Go error" alone is not correctness.
	StableChecks []StableOperationCheck `json:"stable_checks"`
	// SemanticChecks carries repository-specific gold assertions from the corpus
	// manifest (for example, a minimum number of confirmed caller edges).
	SemanticChecks []SemanticCheck `json:"semantic_checks,omitempty"`

	Pass     bool     `json:"pass"`
	Failures []string `json:"failures,omitempty"`
}

// StableOperationCheck summarizes the outcomes observed while exercising one
// stable operation during a real-repository run.
type StableOperationCheck struct {
	Operation   string         `json:"operation"`
	Requirement string         `json:"requirement"`
	Samples     int            `json:"samples"`
	Outcomes    map[string]int `json:"outcomes"`
	Pass        bool           `json:"pass"`
}

// SemanticCheck is a concrete repository-specific correctness assertion.
type SemanticCheck struct {
	Name        string `json:"name"`
	Requirement string `json:"requirement"`
	Observed    string `json:"observed"`
	Pass        bool   `json:"pass"`
}

// IndexMetrics captures the cold full-index measurement.
type IndexMetrics struct {
	WallclockMS int64 `json:"wallclock_ms"`
	// PeakRSSMB is the process's peak resident set (getrusage MAXRSS) sampled
	// immediately after the index pass. StablePeakRSSMB separately samples the
	// complete session after all stable operations.
	PeakRSSMB   int64 `json:"peak_rss_mb"`
	DBSizeBytes int64 `json:"db_size_bytes"`
	Nodes       int   `json:"nodes"`
	Edges       int   `json:"edges"`
	Files       int   `json:"files"`
}

// SearchCheck is one manifest search assertion outcome.
type SearchCheck struct {
	Query   string `json:"query"`
	Matches int    `json:"matches"`
	Pass    bool   `json:"pass"`
}

// WriteFullRunJSON writes the report as stable, indented JSON.
func WriteFullRunJSON(r FullRunReport, path string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("evalreport: marshal full-run report: %w", err)
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
