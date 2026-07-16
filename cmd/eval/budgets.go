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

type fullBudgetManifest struct {
	SchemaVersion int    `json:"schema_version"`
	RunnerClass   string `json:"runner_class"`
	RealRepos     struct {
		Selection []string                  `json:"selection"`
		PerRepo   map[string]fullRepoBudget `json:"per_repo"`
	} `json:"real_repos"`
}

// checkFullRunBudgets loads and enforces the checked-in real-repository
// ratchets. A missing repo, runner mismatch, absent metric, or non-positive
// threshold is a configuration failure: the gate is fail-closed.
func checkFullRunBudgets(path, runnerClass string, run evalreport.FullRepoRun) ([]evalreport.PerfCheck, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var manifest fullBudgetManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return evaluateFullRunBudgets(manifest, runnerClass, run)
}

func evaluateFullRunBudgets(manifest fullBudgetManifest, runnerClass string, run evalreport.FullRepoRun) ([]evalreport.PerfCheck, error) {
	if manifest.SchemaVersion != 2 {
		return nil, fmt.Errorf("unsupported schema_version %d (want 2)", manifest.SchemaVersion)
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
