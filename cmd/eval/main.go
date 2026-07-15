// Command eval runs the token-parity eval harness with the CI-gated per-
// capability coverage matrix (story SW-012). It loads the frozen labeled eval
// set, measures graphi-vs-baseline token ratios per case with the deterministic
// offline tokenizer, emits a version-stamped report, enforces the per-capability
// coverage matrix and drift gate, and (in -claim-validate mode) gates the public
// "~50x fewer tokens" claim on the measured aggregate — resolving open question
// OQ4 with evidence rather than assertion. Hermetic: zero non-loopback network,
// no telemetry, CGo-disabled, deterministic byte-identical re-runs.
//
// In scorecard-report mode (-manifest), it emits the EP-019 scorecard report as
// JSON and Markdown, diffs against docs/eval-baseline.json, and exits non-zero
// on a Tier-1 correctness regression.
//
// Usage:
//
//	go run ./cmd/eval [-claim-validate] [-threshold 50]
//	go run ./cmd/eval -manifest corpus/manifest.json -out report.json -format markdown
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/scenario"
	"github.com/samibel/graphi/engine/scorecard"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/internal/eval"
	"github.com/samibel/graphi/internal/evalreport"
)

func main() {
	claimValidate := flag.Bool("claim-validate", false, "gate the public ~50x claim on the measured aggregate (exit non-zero if held back)")
	threshold := flag.Float64("threshold", eval.DefaultClaimThreshold, "claim threshold (default ~50x)")

	// EP-019 scorecard report flags.
	manifest := flag.String("manifest", "", "corpus/scenario manifest path (scorecard-report mode)")
	scenariosDir := flag.String("scenarios", "", "scenario directory override (default: the manifest's sibling scenarios/ directory; SW-122: pass corpus/hero for the hero-task suite)")
	out := flag.String("out", "", "write the JSON scorecard report here")
	format := flag.String("format", "json", "report format: json or markdown")
	tier := flag.Int("tier", 0, "run only scenarios whose fixture is in this corpus tier (0 = all)")
	updateBaseline := flag.Bool("update-baseline", false, "write the current report to docs/eval-baseline.json (human-approved PR only)")

	// SW-123 (EVAL-02) full-run flags.
	fullRun := flag.String("full-run", "", "measure ONE manifest entry end-to-end (clone, index, warm p95) and emit the raw evidence JSON")
	workDir := flag.String("workdir", "", "full-run working directory (default: a fresh temp dir, removed afterwards)")
	runnerClass := flag.String("runner-class", "local", "machine class stamped into the full-run report (CI passes ubuntu-latest; budgets are only frozen from the reference class)")

	flag.Parse()

	if *fullRun != "" {
		if *manifest == "" {
			fmt.Fprintln(os.Stderr, "eval: -full-run requires -manifest")
			os.Exit(2)
		}
		os.Exit(runFullRun(*manifest, *fullRun, *workDir, *runnerClass, *out))
	}

	if *manifest != "" {
		os.Exit(runScorecardReport(*manifest, *scenariosDir, *out, *format, *tier, *updateBaseline))
	}

	// Original token-parity eval mode.
	ds, err := eval.LoadDataset()
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: load dataset: %v\n", err)
		os.Exit(2)
	}
	rep, err := eval.Run(ds, *claimValidate, *threshold)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: run: %v\n", err)
		os.Exit(2)
	}

	if err := json.NewEncoder(os.Stdout).Encode(rep); err != nil {
		fmt.Fprintf(os.Stderr, "eval: encode report: %v\n", err)
		os.Exit(2)
	}

	if !rep.Pass {
		fmt.Fprintf(os.Stderr, "%s FAILED:\n", rep.Name)
		for _, v := range rep.Violations {
			fmt.Fprintf(os.Stderr, "  - %s\n", v)
		}
		os.Exit(1)
	}
	verdict := "held back"
	if rep.ClaimSupported {
		verdict = "supported"
	}
	fmt.Fprintf(os.Stderr, "%s PASS (aggregate=%.2fx, claim %s, threshold=%.0fx)\n",
		rep.Name, rep.AggregateRatio, verdict, rep.ClaimThreshold)
}

// resolveCommit reads .git/HEAD and, when it is a symbolic ref, follows it to
// the actual SHA. Falls back to the raw HEAD content, then "unknown".
func resolveCommit() string {
	b, err := os.ReadFile(".git/HEAD")
	if err != nil {
		return "unknown"
	}
	head := strings.TrimSpace(string(b))
	if ref, ok := strings.CutPrefix(head, "ref: "); ok {
		if sha, err := os.ReadFile(filepath.Join(".git", filepath.FromSlash(ref))); err == nil {
			return strings.TrimSpace(string(sha))
		}
		return head
	}
	return head
}

func runScorecardReport(manifestPath, scenariosDir, outPath, format string, tier int, updateBaseline bool) int {
	version := "0.0.0-dev"
	commit := resolveCommit()

	corpusVersion, fixturePaths, err := loadCorpusManifest(manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: load manifest: %v\n", err)
		return 2
	}
	addBuiltinFixtures(fixturePaths)

	// Execute every scenario in the manifest's sibling scenarios directory
	// against its fixture graph (real runs, not placeholders). -scenarios
	// selects an alternative suite over the same manifest (corpus/hero); the
	// checked-in baseline belongs to the DEFAULT suite, so alternative suites
	// neither diff against it nor overwrite it (their own baselines/budgets
	// are frozen from CI runs in EVAL-02, per ADR 0003 U5).
	defaultSuite := scenariosDir == ""
	scenarioDir := scenariosDir
	if defaultSuite {
		scenarioDir = filepath.Join(filepath.Dir(manifestPath), "scenarios")
	} else if updateBaseline {
		fmt.Fprintf(os.Stderr, "eval: -update-baseline only applies to the default scenario suite (docs/eval-baseline.json), not %s\n", scenarioDir)
		return 2
	}
	scenarios, err := runScenarios(scenarioDir, filepath.Dir(filepath.Dir(manifestPath)), fixturePaths, tier)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: run scenarios: %v\n", err)
		return 2
	}

	// Derive measured area scores from the run; carry unmeasured areas from
	// the baseline and say so.
	areaScores, provenance, carryWarnings := evalreport.DeriveAreaScores(scenarios, nil)

	// Signal quality: the formula-based measurement over the ground-truth
	// fixture supersedes the plain scenario pass fraction.
	signalMetrics, err := measureSignal()
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: measure signal: %v\n", err)
		return 2
	}
	areaScores[scorecard.AreaSignal] = signalMetrics.Score
	provenance[scorecard.AreaSignal] = "measured"

	// Performance: budget checks over the tier-1 fixture.
	perfMetrics, err := measurePerformance()
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: measure performance: %v\n", err)
		return 2
	}
	areaScores[scorecard.AreaPerformance] = perfMetrics.Score
	provenance[scorecard.AreaPerformance] = "measured"

	// Setup/trust: doctor behavior over controlled fixtures.
	setupScore, setupMetrics, err := measureSetupTrust()
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: measure setup/trust: %v\n", err)
		return 2
	}
	areaScores[scorecard.AreaSetupTrust] = setupScore
	provenance[scorecard.AreaSetupTrust] = "measured"

	carryWarnings = carryWarnings[:0]
	for area, prov := range provenance {
		if prov == "carried" {
			carryWarnings = append(carryWarnings, fmt.Sprintf("area %s carried from baseline (%.0f), not measured by this run", area, areaScores[area]))
		}
	}
	sort.Strings(carryWarnings)

	score := evalreport.DefaultScorecard()
	if computed, err := scorecard.Calculate(areaScores); err == nil {
		score = computed
	}

	header := evalreport.NewHeader(version, commit)
	header.CorpusVersion = corpusVersion
	report := evalreport.Report{
		Header:             header,
		PerRepoMetrics:     []evalreport.PerRepoMetric{},
		PerScenarioResults: scenarios,
		Scorecard:          score,
		AreaProvenance:     provenance,
		Baseline:           65.0,
		Target:             90.0,
		SignalMetrics:      &signalMetrics,
		PerfMetrics:        &perfMetrics,
		SetupTrustMetrics:  setupMetrics,
	}
	report.PerfWarnings = append(report.PerfWarnings, carryWarnings...)

	baseline := evalreport.DefaultBaseline()
	if defaultSuite {
		if raw, err := os.ReadFile("docs/eval-baseline.json"); err == nil {
			var loaded evalreport.BaselineRecord
			if err := json.Unmarshal(raw, &loaded); err == nil {
				baseline = loaded
			}
		}
		regs, warnings := evalreport.DiffAgainstBaseline(report, baseline)
		report.RegressionsVsBaseline = regs
		report.PerfWarnings = append(report.PerfWarnings, warnings...)
	} else {
		report.PerfWarnings = append(report.PerfWarnings,
			fmt.Sprintf("suite %s not diffed against docs/eval-baseline.json (baseline covers the default suite only)", scenarioDir))
	}

	if updateBaseline {
		b := evalreport.BaselineRecord{
			Version:  baseline.Version + 1,
			Baseline: score.Overall,
			Target:   90.0,
		}
		for _, s := range scenarios {
			b.Scenarios = append(b.Scenarios, evalreport.BaselineScenario{
				ID:      s.ID,
				Outcome: s.Outcome,
				Tier1:   s.Tier1,
			})
		}
		raw, err := json.MarshalIndent(b, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "eval: marshal baseline: %v\n", err)
			return 2
		}
		if err := os.WriteFile("docs/eval-baseline.json", append(raw, '\n'), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "eval: write baseline: %v\n", err)
			return 2
		}
		fmt.Fprintln(os.Stderr, "eval: updated docs/eval-baseline.json")
	}

	if outPath != "" {
		if err := evalreport.WriteJSON(report, outPath); err != nil {
			fmt.Fprintf(os.Stderr, "eval: write json: %v\n", err)
			return 2
		}
		fmt.Fprintf(os.Stderr, "eval: wrote JSON report to %s\n", outPath)
	}

	if format == "markdown" || outPath != "" {
		mdPath := outPath
		if mdPath == "" {
			mdPath = "report.md"
		} else if filepath.Ext(mdPath) == ".json" {
			mdPath = mdPath[:len(mdPath)-5] + ".md"
		}
		if err := evalreport.WriteMarkdown(report, mdPath); err != nil {
			fmt.Fprintf(os.Stderr, "eval: write markdown: %v\n", err)
			return 2
		}
		fmt.Fprintf(os.Stderr, "eval: wrote Markdown report to %s\n", mdPath)
	}

	if !defaultSuite {
		// Alternative suites (corpus/hero) gate directly on their own scenario
		// outcomes: every tier-1 scenario must pass.
		failed := 0
		for _, s := range scenarios {
			if s.Tier1 && s.Outcome != "pass" {
				failed++
				fmt.Fprintf(os.Stderr, "eval: scenario %s: %s\n", s.ID, s.Outcome)
			}
		}
		if failed > 0 {
			fmt.Fprintf(os.Stderr, "eval: FAIL - %d/%d tier-1 scenarios in %s did not pass\n", failed, len(scenarios), scenarioDir)
			return 1
		}
		fmt.Fprintf(os.Stderr, "eval: PASS - all %d scenarios in %s pass\n", len(scenarios), scenarioDir)
		return 0
	}

	if report.HasTier1Regression() {
		fmt.Fprintf(os.Stderr, "eval: FAIL - Tier-1 correctness regression detected\n")
		return 1
	}
	fmt.Fprintf(os.Stderr, "eval: PASS - no Tier-1 correctness regression (scorecard %.1f/%d)\n", score.Overall, 100)
	return 0
}

// addBuiltinFixtures registers the synthetic ground-truth fixtures (exact
// hand-built graphs) alongside the manifest-declared ingested fixtures, under
// tier 1.
func addBuiltinFixtures(fixtures map[string]fixtureInfo) {
	fixtures[SyntheticSignalFixture] = fixtureInfo{Tier: 1, Build: signalEngine}
}

// fixtureInfo is one fixture the scenario runner can use: either an ingested
// local directory (Path) or a built-in synthetic graph (Build).
type fixtureInfo struct {
	Path string
	Tier int
	// Build constructs a synthetic fixture engine; takes precedence over Path.
	Build func() (*scenario.FixtureEngine, error)
}

// loadCorpusManifest reads the corpus manifest and returns its version stamp
// plus the fixture_ref → fixture index for local fixtures.
func loadCorpusManifest(path string) (int, map[string]fixtureInfo, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, nil, err
	}
	var m struct {
		Version int `json:"version"`
		Entries []struct {
			Name string `json:"name"`
			Path string `json:"path"`
			Tier int    `json:"tier"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	fixtures := map[string]fixtureInfo{}
	for _, e := range m.Entries {
		if e.Path != "" {
			fixtures[e.Name] = fixtureInfo{Path: e.Path, Tier: e.Tier}
		}
	}
	return m.Version, fixtures, nil
}

// runScenarios loads every scenario file in dir and executes it against its
// fixture graph via the shared scenario runner. Fixture paths in the manifest
// are repo-root-relative; root anchors them. When tier > 0, scenarios whose
// fixture belongs to another corpus tier are omitted from the results (they
// were not run); a fixture_ref that never existed in the manifest is an error.
func runScenarios(dir, root string, fixturePaths map[string]fixtureInfo, tier int) ([]evalreport.PerScenarioResult, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	jsonFiles, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	files = append(files, jsonFiles...)
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("no scenario files in %s", dir)
	}

	engines := map[string]*scenario.FixtureEngine{}
	var results []evalreport.PerScenarioResult
	for _, f := range files {
		s, err := scenario.LoadScenario(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		fx, ok := fixturePaths[s.FixtureRef]
		if !ok {
			return nil, fmt.Errorf("%s: fixture_ref %q not in manifest", f, s.FixtureRef)
		}
		if tier > 0 && fx.Tier != tier {
			continue
		}
		engineKey := fx.Path
		if fx.Build != nil {
			engineKey = "synthetic:" + s.FixtureRef
		}
		eng, ok := engines[engineKey]
		if !ok {
			if fx.Build != nil {
				eng, err = fx.Build()
			} else {
				eng, err = buildFixtureEngine(filepath.Join(root, filepath.FromSlash(fx.Path)))
			}
			if err != nil {
				return nil, fmt.Errorf("%s: fixture %s: %w", f, engineKey, err)
			}
			engines[engineKey] = eng
		}
		runner := scenario.Runner{Engine: eng}
		res := runner.Run(s)
		results = append(results, evalreport.PerScenarioResult{
			ID:            s.ID,
			FixtureRef:    s.FixtureRef,
			Operation:     s.Operation.Name,
			Area:          s.Area,
			Outcome:       res.Outcome,
			ResultSize:    res.AnswerSize,
			Evidence:      res.Evidence,
			Confidence:    res.Confidence,
			LatencyMS:     res.LatencyMS,
			AnchorPresent: res.AnchorPresent,
			Tier1:         fx.Tier == 1,
		})
	}
	return results, nil
}

// buildFixtureEngine ingests the fixture directory into an in-memory graph
// and wraps the shared engine services around it.
func buildFixtureEngine(fixtureDir string) (*scenario.FixtureEngine, error) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	ing, err := ingest.New(store, parse.NewDefaultRegistry(), "")
	if err != nil {
		return nil, err
	}
	defer ing.Close()
	if err := ing.IngestAll(ctx, fixtureDir); err != nil {
		return nil, err
	}
	deps := resolve.Deps{Query: query.New(store), Search: search.New(store)}
	return scenario.NewFixtureEngine(deps), nil
}
