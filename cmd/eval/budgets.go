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
	// HistoricalCeilings is the SW-191 (EVALBUDGET-001) comparison-class block.
	// It is a SEPARATE selection and a separate per-repo table, deliberately not
	// merged into real_repos: the reference-class path must be unable to reach
	// it, and a reader must be able to see at a glance which figures came off a
	// developer machine.
	HistoricalCeilings *historicalCeilingBlock `json:"historical_ceilings,omitempty"`
}

// comparisonRunnerClass is the declared non-reference machine class. A budget
// derived from it bounds a run and never freezes one.
const comparisonRunnerClass = "local-sandbox"

// historicalCeilingSchemaVersion is the accepted stamp for the ceiling block.
const historicalCeilingSchemaVersion = 1

// historicalCeilingBlock is a set of NON-RATCHETING upper limits measured on
// the comparison class.
//
// WHY IT CARRIES ITS OWN runner_class AND ratcheting FIELDS rather than
// inheriting the manifest's. The manifest's runner_class names the REFERENCE
// class its real_repos figures belong to; the ceiling block's names the class
// its own figures were measured on. They are different machines and different
// claims, and a block that inherited would be one edit away from silently
// becoming a reference-class ratchet — which is precisely the substitution the
// gate exists to prevent. Both are asserted on every use.
type historicalCeilingBlock struct {
	SchemaVersion int    `json:"schema_version"`
	RunnerClass   string `json:"runner_class"`
	RunnerRole    string `json:"runner_role"`
	// Ratcheting must be present and false. A ceiling that ratchets is a
	// contradiction, and the absent-field default (false) is NOT relied on:
	// see comparisonCeilings, which requires the declaration to be
	// written down rather than assumed.
	Ratcheting *bool                     `json:"ratcheting"`
	Notes      string                    `json:"notes"`
	Selection  []string                  `json:"selection"`
	PerRepo    map[string]fullRepoBudget `json:"per_repo"`
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
	if manifest.RunnerClass == "" {
		return nil, fmt.Errorf("budget manifest declares no runner_class; SW-191 requires an explicit reference-class runner_class")
	}
	// SW-191 (EVALBUDGET-001 closure). The comparison class is now an ACCEPTED
	// budget source, but it is served from its OWN block and never from
	// real_repos: a historical ceiling is an upper compatibility limit recorded
	// on a developer machine, not a ratchet that pins a measurement to the
	// reference class, and the two must not share a table. The routing below is
	// the whole of the acceptance — there is no path on which a
	// comparison-class run reads a reference-class figure, and none on which a
	// reference-class run reads a ceiling.
	selection := manifest.RealRepos.Selection
	perRepo := manifest.RealRepos.PerRepo
	switch runnerClass {
	case manifest.RunnerClass:
		// The reference class reads real_repos, exactly as before SW-191.
	case comparisonRunnerClass:
		block, err := manifest.comparisonCeilings()
		if err != nil {
			return nil, err
		}
		selection, perRepo = block.Selection, block.PerRepo
	default:
		return nil, fmt.Errorf("runner class %q does not match budget runner %q and is not the comparison class %q",
			runnerClass, manifest.RunnerClass, comparisonRunnerClass)
	}
	if !slices.Contains(selection, run.Name) {
		return nil, fmt.Errorf("repo %q is not in budget selection", run.Name)
	}
	budget, ok := perRepo[run.Name]
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

// comparisonCeilings returns the manifest's historical-ceiling block after
// asserting every property that keeps it from becoming a reference-class
// ratchet. Every failure is fail-closed: a malformed or re-pointed block yields
// an error, never a silently-skipped check.
//
// THE ADVERSARIAL QUESTION this answers is "can the ceiling block be silently
// re-pointed at the reference class?" It cannot:
//
//   - the block must declare runner_class == the comparison class, so pointing
//     it at ubuntu-latest is refused rather than honoured;
//   - it must declare runner_role "comparison";
//   - it must declare ratcheting EXPLICITLY and false — the field is a pointer
//     so an omitted declaration is distinguishable from a written `false`, and
//     an omitted one is refused;
//   - the whole manifest must not itself be ratcheting, because a ratcheting
//     manifest carrying a comparison ceiling is a contradiction;
//   - and evaluateFullRunBudgets only ever reaches this block when the RUN was
//     on the comparison class, so a reference-class run cannot be scored
//     against it.
func (m fullBudgetManifest) comparisonCeilings() (*historicalCeilingBlock, error) {
	block := m.HistoricalCeilings
	if block == nil {
		return nil, fmt.Errorf("runner class %q is the comparison class, but the budget manifest carries no historical_ceilings block; "+
			"a comparison-class run is bounded by a non-ratcheting ceiling or by nothing at all", comparisonRunnerClass)
	}
	if block.SchemaVersion != historicalCeilingSchemaVersion {
		return nil, fmt.Errorf("historical_ceilings: unsupported schema_version %d (want %d)", block.SchemaVersion, historicalCeilingSchemaVersion)
	}
	if block.RunnerClass != comparisonRunnerClass {
		return nil, fmt.Errorf("historical_ceilings declares runner_class %q; the ceiling block records COMPARISON-class figures and may only declare %q "+
			"(re-pointing it at the reference class would turn developer-machine numbers into a reference-class ratchet)",
			block.RunnerClass, comparisonRunnerClass)
	}
	if block.RunnerRole != "comparison" {
		return nil, fmt.Errorf("historical_ceilings declares runner_role %q, want \"comparison\"", block.RunnerRole)
	}
	if block.Ratcheting == nil {
		return nil, fmt.Errorf("historical_ceilings does not declare `ratcheting`; the non-ratcheting property must be WRITTEN DOWN, not inferred from an absent field")
	}
	if *block.Ratcheting {
		return nil, fmt.Errorf("historical_ceilings declares ratcheting=true; a comparison-class ceiling bounds a run and never freezes one")
	}
	if m.Ratcheting {
		return nil, fmt.Errorf("the budget manifest declares ratcheting=true while carrying a historical_ceilings block; a ratcheting manifest has no comparison-class ceilings to serve")
	}
	if len(block.Selection) == 0 || len(block.PerRepo) == 0 {
		return nil, fmt.Errorf("historical_ceilings carries no selection or no per_repo table")
	}
	// Selection and per_repo must agree: a name in one and not the other is a
	// budget that reads as absent (fail-closed) or as unreachable (dead data).
	for _, name := range block.Selection {
		if _, ok := block.PerRepo[name]; !ok {
			return nil, fmt.Errorf("historical_ceilings selects %q with no per_repo entry", name)
		}
	}
	return block, nil
}
