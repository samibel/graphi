package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"

	"github.com/samibel/graphi/internal/evalreport"
)

type budgetThreshold struct {
	Baseline float64 `json:"baseline"`
	Budget   float64 `json:"budget"`
}

type fullRepoBudget struct {
	IndexWallclockMS budgetThreshold            `json:"index_wallclock_ms"`
	PeakRSSMB        budgetThreshold            `json:"peak_rss_mb"`
	DBSizeMB         budgetThreshold            `json:"db_size_mb"`
	WarmP95US        map[string]budgetThreshold `json:"warm_p95_us"`
}

// budgetSchemaVersion is the accepted schema stamp. v3 (SW-123) adds the
// historical/ratcheting declaration: the file must say out loud whether its
// numbers are comparable post-change ratchets or historical compatibility
// ceilings. v2 files are rejected rather than assumed to be ratchets.
const budgetSchemaVersion = 3

type fullBudgetManifest struct {
	SchemaVersion int    `json:"schema_version"`
	RunnerClass   string `json:"runner_class"`
	// Historical and Ratcheting are the SW-123 (P0-A4) declaration. They are
	// mutually exclusive: a historical artifact records compatibility ceilings
	// from a retired harness and cannot ratchet, and HistoricalReason must say
	// why. Silence here is what let all-zero latency budgets read as green.
	Historical       bool   `json:"historical"`
	Ratcheting       bool   `json:"ratcheting"`
	HistoricalReason string `json:"historical_reason,omitempty"`
	RealRepos        struct {
		Selection []string                  `json:"selection"`
		PerRepo   map[string]fullRepoBudget `json:"per_repo"`
	} `json:"real_repos"`
}

// loadBudgetManifest reads, zero-checks and declaration-checks the budget
// artifact. Every failure mode here is a configuration failure, and every one
// of them is an error rather than a skip.
func loadBudgetManifest(path string) (fullBudgetManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fullBudgetManifest{}, fmt.Errorf("read %s: %w", path, err)
	}
	var manifest fullBudgetManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return fullBudgetManifest{}, fmt.Errorf("parse %s: %w", path, err)
	}
	// SW-123 (AC-5): no threshold in a budget artifact may be a bare zero — a
	// zero budget renders as met while carrying no signal, which is worse than
	// having no budget at all.
	if err := validateNoSilentZeroBudgets(raw); err != nil {
		return fullBudgetManifest{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := validateBudgetDeclaration(manifest); err != nil {
		return fullBudgetManifest{}, fmt.Errorf("%s: %w", path, err)
	}
	return manifest, nil
}

// validateBudgetDeclaration enforces the SW-123 schema-v3 declaration: the
// artifact must say whether its numbers are comparable ratchets or historical
// ceilings, and it cannot claim both.
func validateBudgetDeclaration(manifest fullBudgetManifest) error {
	if manifest.SchemaVersion != budgetSchemaVersion {
		return fmt.Errorf("unsupported schema_version %d (want %d)", manifest.SchemaVersion, budgetSchemaVersion)
	}
	if manifest.Historical && manifest.Ratcheting {
		return fmt.Errorf("budget manifest declares historical and ratcheting; it is one or the other")
	}
	if manifest.Historical && manifest.HistoricalReason == "" {
		return fmt.Errorf("historical budget manifest records no historical_reason")
	}
	return nil
}

// checkFullRunBudgets loads and enforces the checked-in real-repository
// ceilings. A missing repo, runner mismatch, absent metric, or non-positive
// threshold is a configuration failure: the gate is fail-closed.
func checkFullRunBudgets(path, runnerClass string, run evalreport.FullRepoRun) ([]evalreport.PerfCheck, error) {
	manifest, err := loadBudgetManifest(path)
	if err != nil {
		return nil, err
	}
	return evaluateFullRunBudgets(manifest, runnerClass, run)
}

func evaluateFullRunBudgets(manifest fullBudgetManifest, runnerClass string, run evalreport.FullRepoRun) ([]evalreport.PerfCheck, error) {
	if err := validateBudgetDeclaration(manifest); err != nil {
		return nil, err
	}
	if manifest.RunnerClass == "" || runnerClass != manifest.RunnerClass {
		return nil, fmt.Errorf("runner class %q does not match budget runner %q", runnerClass, manifest.RunnerClass)
	}
	if !slices.Contains(manifest.RealRepos.Selection, run.Name) {
		return nil, fmt.Errorf("repo %q is not in budget selection", run.Name)
	}
	budget, ok := manifest.RealRepos.PerRepo[run.Name]
	if !ok {
		return nil, fmt.Errorf("repo %q has no per_repo budget", run.Name)
	}
	if run.Index.WallclockMS <= 0 || run.Index.PeakRSSMB <= 0 || run.StablePeakRSSMB <= 0 || run.Index.DBSizeBytes <= 0 {
		return nil, fmt.Errorf("repo %q has incomplete index/RSS/size measurements", run.Name)
	}

	type metric struct {
		name     string
		measured float64
		limit    budgetThreshold
		unit     string
	}
	metrics := []metric{
		{"index_wallclock_ms", float64(run.Index.WallclockMS), budget.IndexWallclockMS, "ms"},
		// The existing peak_rss_mb ratchet now gates the stricter full-session
		// sample, not merely the pre-query index sample.
		{"stable_peak_rss_mb", float64(run.StablePeakRSSMB), budget.PeakRSSMB, "MB"},
		{"db_size_mb", float64(run.Index.DBSizeBytes) / (1024 * 1024), budget.DBSizeMB, "MiB"},
	}
	for _, class := range []string{"structural", "search", "agent_tools"} {
		threshold, exists := budget.WarmP95US[class]
		if !exists {
			return nil, fmt.Errorf("repo %q missing warm_p95_us.%s budget", run.Name, class)
		}
		measured, exists := run.WarmP95US[class]
		if !exists || run.WarmSamples[class] <= 0 || len(run.WarmOps[class]) == 0 {
			return nil, fmt.Errorf("repo %q missing measured warm class %s", run.Name, class)
		}
		metrics = append(metrics, metric{"warm_p95_us." + class, float64(measured), threshold, "us"})
	}

	checks := make([]evalreport.PerfCheck, 0, len(metrics))
	for _, metric := range metrics {
		if metric.limit.Budget <= 0 {
			return nil, fmt.Errorf("repo %q metric %s has non-positive budget %.3f", run.Name, metric.name, metric.limit.Budget)
		}
		checks = append(checks, evalreport.PerfCheck{
			Name:     metric.name,
			Measured: metric.measured,
			Budget:   metric.limit.Budget,
			Unit:     metric.unit,
			Pass:     metric.measured <= metric.limit.Budget,
		})
	}
	return checks, nil
}
