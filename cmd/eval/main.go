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
	out := flag.String("out", "", "write the JSON scorecard report here")
	format := flag.String("format", "json", "report format: json or markdown")
	tier := flag.Int("tier", 0, "run only scenarios whose fixture is in this corpus tier (0 = all)")
	updateBaseline := flag.Bool("update-baseline", false, "write the current report to docs/eval-baseline.json (human-approved PR only)")

	flag.Parse()

	if *manifest != "" {
		os.Exit(runScorecardReport(*manifest, *out, *format, *tier, *updateBaseline))
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

func runScorecardReport(manifestPath, outPath, format string, tier int, updateBaseline bool) int {
	version := "0.0.0-dev"
	commit := resolveCommit()

	corpusVersion, fixturePaths, err := loadCorpusManifest(manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: load manifest: %v\n", err)
		return 2
	}

	// Execute every scenario in the manifest's sibling scenarios directory
	// against its fixture graph (real runs, not placeholders).
	scenarioDir := filepath.Join(filepath.Dir(manifestPath), "scenarios")
	scenarios, err := runScenarios(scenarioDir, filepath.Dir(filepath.Dir(manifestPath)), fixturePaths, tier)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: run scenarios: %v\n", err)
		return 2
	}

	// Derive measured area scores from the run; carry unmeasured areas from
	// the baseline and say so.
	areaScores, provenance, carryWarnings := evalreport.DeriveAreaScores(scenarios, nil)
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
	}
	report.PerfWarnings = append(report.PerfWarnings, carryWarnings...)

	baseline := evalreport.DefaultBaseline()
	if raw, err := os.ReadFile("docs/eval-baseline.json"); err == nil {
		var loaded evalreport.BaselineRecord
		if err := json.Unmarshal(raw, &loaded); err == nil {
			baseline = loaded
		}
	}
	regs, warnings := evalreport.DiffAgainstBaseline(report, baseline)
	report.RegressionsVsBaseline = regs
	report.PerfWarnings = append(report.PerfWarnings, warnings...)

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

	if report.HasTier1Regression() {
		fmt.Fprintf(os.Stderr, "eval: FAIL - Tier-1 correctness regression detected\n")
		return 1
	}
	fmt.Fprintf(os.Stderr, "eval: PASS - no Tier-1 correctness regression (scorecard %.1f/%d)\n", score.Overall, 100)
	return 0
}

// fixtureInfo is one local-fixture manifest entry the scenario runner can use.
type fixtureInfo struct {
	Path string
	Tier int
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
		eng, ok := engines[fx.Path]
		if !ok {
			eng, err = buildFixtureEngine(filepath.Join(root, filepath.FromSlash(fx.Path)))
			if err != nil {
				return nil, fmt.Errorf("%s: fixture %s: %w", f, fx.Path, err)
			}
			engines[fx.Path] = eng
		}
		runner := scenario.Runner{Engine: eng}
		res := runner.Run(s)
		results = append(results, evalreport.PerScenarioResult{
			ID:            s.ID,
			FixtureRef:    s.FixtureRef,
			Operation:     s.Operation.Name,
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
