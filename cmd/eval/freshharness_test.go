package main

// SW-126 (P0-C3): the end-to-end properties no unit test can prove — that the
// four change classes really execute against a real index, that the
// cross-package class really touches cross-package edges, that a real change
// really becomes answerable, and that a deliberately broken change survives
// into the report instead of vanishing (AC-6).
//
// These run over a small Go tree built in a temp dir: real parse, real
// incremental ingest, real search. No network, no clone — the pinned corpus
// repositories are what the workflow measures, and what a unit test needs is a
// tree with two packages and an edge between them.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/internal/evalreport"
)

// twoPackageTree writes a module with an alpha package and a beta package that
// calls into it, so the graph contains edges that cross a directory boundary.
func twoPackageTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/fresh\n\ngo 1.22\n",
		"alpha/alpha.go": `package alpha

// Alpha is the cross-package target.
func Alpha() int { return 1 }

// AlphaTwo is a second exported symbol.
func AlphaTwo() int { return Alpha() + 1 }
`,
		"alpha/more.go": `package alpha

// AlphaThree keeps the alpha directory multi-file.
func AlphaThree() int { return AlphaTwo() }
`,
		"beta/beta.go": `package beta

import "example.com/fresh/alpha"

// Beta calls across the package boundary.
func Beta() int { return alpha.Alpha() }

// BetaTwo calls across the package boundary again.
func BetaTwo() int { return alpha.AlphaTwo() + alpha.Alpha() }
`,
		"beta/other.go": `package beta

import "example.com/fresh/alpha"

// BetaThree is a third cross-package caller.
func BetaThree() int { return alpha.Alpha() }
`,
	}
	for rel, body := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// indexedTree indexes root into a fresh on-disk store and returns the setup the
// harness measures over.
func indexedTree(t *testing.T, root string, changes int) (incrementalSetup, func()) {
	t.Helper()
	work := t.TempDir()
	store, err := graphstore.OpenSQLite(filepath.Join(work, "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), filepath.Join(work, "meta"))
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := ing.IngestAll(context.Background(), root); err != nil {
		_ = ing.Close()
		_ = store.Close()
		t.Fatal(err)
	}
	deps := resolve.Deps{Query: query.New(store), Search: search.New(store)}
	return incrementalSetup{
			repo: "fresh-fixture", root: root, store: store, ing: ing,
			querySvc: deps.Query, searcher: deps.Search, changes: changes,
		}, func() {
			_ = ing.Close()
			_ = store.Close()
		}
}

// AC-1/AC-2/AC-3/AC-4/AC-7 over a real index: a defined sequence runs, all four
// classes complete, both distributions exist, freshness contains the update,
// and every change keeps its own measurement.
func TestIncrementalHarness_MeasuresARealTreeAcrossAllFourClasses(t *testing.T) {
	setup, cleanup := indexedTree(t, twoPackageTree(t), 12)
	defer cleanup()

	series, err := measureIncremental(context.Background(), setup)
	if err != nil {
		t.Fatalf("measureIncremental: %v", err)
	}
	if series.Failed != 0 {
		t.Fatalf("%d change(s) failed over a healthy tree: %v", series.Failed, series.Warnings)
	}
	if series.Completed != 12 {
		t.Fatalf("completed %d of 12 changes", series.Completed)
	}
	if !series.ClassesCovered {
		t.Fatalf("not every required class completed: %+v", series.Coverage)
	}
	for _, row := range series.Coverage {
		if row.Required && row.Steps == 0 {
			t.Errorf("class %s never ran", row.Class)
		}
	}

	if series.Update.N != 12 || series.Freshness.N != 12 {
		t.Fatalf("distributions have n = %d update / %d freshness, want 12 each", series.Update.N, series.Freshness.N)
	}
	if series.Update.P50US <= 0 || series.Update.P95US <= 0 {
		t.Errorf("incremental update p50/p95 = %d/%d µs — a real re-ingest is not free", series.Update.P50US, series.Update.P95US)
	}
	if series.Freshness.P50US < series.Update.P50US || series.Freshness.P95US < series.Update.P95US {
		t.Errorf("freshness (%d/%d) is below the update it contains (%d/%d)",
			series.Freshness.P50US, series.Freshness.P95US, series.Update.P50US, series.Update.P95US)
	}
	for _, c := range series.Changes {
		if !c.UpdateMeasured || !c.FreshnessMeasured {
			t.Errorf("change %d retained no measurement: %+v", c.Step, c)
		}
		if c.FreshnessUS < c.UpdateUS {
			t.Errorf("change %d: freshness %d < update %d", c.Step, c.FreshnessUS, c.UpdateUS)
		}
		if c.Expect == "" {
			t.Errorf("change %d states no convergence criterion", c.Step)
		}
	}
	// AC-7: the published statistics recompute from the retained samples.
	recomputed := evalreport.RecomputeIncremental(series.Changes)
	if series.Update != recomputed.Update || series.Freshness != recomputed.Freshness {
		t.Error("the published distributions do not recompute from the retained changes")
	}
	// AC-4: the artifact explains its own definition to a reader who has only
	// the JSON.
	for name, note := range map[string]string{
		"freshness_definition": series.FreshnessDefinition,
		"timing_method":        series.TimingMethod,
		"aggregate_method":     series.AggregateMethod,
		"scope_limitation":     series.ScopeLimitation,
	} {
		if note == "" {
			t.Errorf("the series publishes no %s", name)
		}
	}
	// AC-1: below FR-8's floor, and honest about it.
	if series.Sufficient {
		t.Error("12 changes must not read as a sufficient FR-8 measurement")
	}
}

// AC-2's teeth: the cross-package class is chosen from the GRAPH and publishes
// the inbound edges that qualified it. A label without this evidence would
// prove nothing.
func TestIncrementalHarness_CrossPackageClassCarriesItsEvidence(t *testing.T) {
	setup, cleanup := indexedTree(t, twoPackageTree(t), 8)
	defer cleanup()

	series, err := measureIncremental(context.Background(), setup)
	if err != nil {
		t.Fatalf("measureIncremental: %v", err)
	}
	evidence := series.Sequence.CrossPackage
	if !evidence.Satisfied {
		t.Fatalf("no cross-package target over a two-package tree with real cross-directory calls: %s", evidence.Reason)
	}
	if evidence.Method == "" {
		t.Error("the selection method must be stated so the claim can be checked")
	}
	for _, target := range evidence.Targets {
		if target.InboundFromOtherDirs <= 0 {
			t.Errorf("target %s qualified with %d inbound edges from other directories", target.Path, target.InboundFromOtherDirs)
		}
		if len(target.ExampleSources) == 0 {
			t.Errorf("target %s publishes no example neighbour to spot-check", target.Path)
		}
		for _, src := range target.ExampleSources {
			if filepath.Dir(src) == filepath.Dir(target.Path) {
				t.Errorf("target %s cites %s as cross-directory evidence, but they share a directory", target.Path, src)
			}
		}
	}
	// The class really ran against one of those files.
	var ran bool
	for _, c := range series.Changes {
		if c.Class != evalreport.ChangeClassCrossPackage {
			continue
		}
		ran = true
		var known bool
		for _, target := range evidence.Targets {
			if target.Path == c.Path {
				known = true
			}
		}
		if !known {
			t.Errorf("cross-package change %d targeted %s, which is not in the published evidence", c.Step, c.Path)
		}
	}
	if !ran {
		t.Error("the cross-package class never executed")
	}
}

// AC-1 end to end: two independent indexes of the same tree plan the same
// sequence, so PRD §16's two consecutive runs measure the same question.
func TestIncrementalHarness_SequenceIsReproducibleAcrossIndexes(t *testing.T) {
	var digests []string
	for range 2 {
		setup, cleanup := indexedTree(t, twoPackageTree(t), 8)
		series, err := measureIncremental(context.Background(), setup)
		cleanup()
		if err != nil {
			t.Fatalf("measureIncremental: %v", err)
		}
		digests = append(digests, series.Sequence.Digest)
		if series.Sequence.Steps != 8 || series.Sequence.SourceFiles == 0 {
			t.Fatalf("sequence info = %+v", series.Sequence)
		}
	}
	if digests[0] != digests[1] {
		t.Fatalf("the change sequence drifted between two indexes of the same tree: %s vs %s", digests[0], digests[1])
	}
}

// AC-6 against the PRODUCTION target: a deliberately failing change is visible
// in the report, keeps its place in the sequence, and does not stop the
// measurement. The failure is a real filesystem error through the real code
// path, not a stub.
func TestIncrementalHarness_DeliberatelyFailingChangeStaysInTheReport(t *testing.T) {
	root := twoPackageTree(t)
	setup, cleanup := indexedTree(t, root, 0)
	defer cleanup()

	in := changeSequenceInput{
		files:        []string{"alpha/alpha.go", "beta/beta.go"},
		packages:     map[string]string{"alpha": "alpha", "beta": "beta"},
		crossPackage: crossPackageTargets(context.Background(), setup.store, setup.querySvc),
		count:        4,
	}
	steps := buildChangeSequence(in)
	// One extra step the tree cannot satisfy: a modify of a file that is not
	// there. It goes in the MIDDLE, so the test also proves the sequence keeps
	// running past a failure.
	broken := changeStep{
		index: len(steps) + 1, class: evalreport.ChangeClassModify,
		path: "alpha/deliberately_absent.go", symbol: "GraphiEvalMissing",
		expect: "this change cannot succeed and must appear in the report",
	}
	steps = append(steps[:2], append([]changeStep{broken}, steps[2:]...)...)

	driver := newIncrementalDriver(repoIncrementalTarget{root: root, ing: setup.ing, searcher: setup.searcher})
	samples := driver.run(context.Background(), steps)
	series := buildIncrementalSeries("fresh-fixture", len(steps), in, steps, samples)

	if len(series.Changes) != len(steps) {
		t.Fatalf("the report retained %d of %d attempted changes", len(series.Changes), len(steps))
	}
	if series.Failed != 1 {
		t.Fatalf("failed = %d, want exactly the one deliberately broken change: %v", series.Failed, series.Warnings)
	}
	failed := series.Changes[2]
	if failed.Path != broken.path || failed.Status != evalreport.ChangeFailed {
		t.Fatalf("the broken change is not where it was attempted: %+v", failed)
	}
	if failed.FailedStage != evalreport.ChangeStageApply || failed.Error == "" {
		t.Errorf("the broken change does not say what went wrong: %+v", failed)
	}
	if failed.UpdateMeasured || failed.FreshnessMeasured {
		t.Error("a change that never applied must contribute no latency")
	}
	// It is counted in the coverage table (the attempt happened) and warned
	// about, and it stops the series reading green.
	var warned bool
	for _, w := range series.Warnings {
		if strings.Contains(w, broken.path) {
			warned = true
		}
	}
	if !warned {
		t.Errorf("the failure is not in the warnings: %v", series.Warnings)
	}
	if incrementalStatus(series) == evalreport.StatusPass {
		t.Error("a series containing a failed change must not read PASS")
	}
	// And the changes after it still completed.
	if series.Completed != len(steps)-1 {
		t.Errorf("completed %d of %d: the failure truncated the sequence", series.Completed, len(steps)-1)
	}
}

// A repository the harness cannot plan a sequence over produces an error, not a
// silently empty series that would read as instant freshness.
func TestIncrementalHarness_UnplannableRepositoryIsAnError(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "readme.md"), []byte("# no go here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setup, cleanup := indexedTree(t, root, 10)
	defer cleanup()

	series, err := measureIncremental(context.Background(), setup)
	if err == nil {
		t.Fatalf("a tree with no Go source planned a sequence anyway: %+v", series)
	}
	if series != nil {
		t.Error("a failed plan must not produce a series")
	}
}

// AC-5, fail closed: a local-path fixture entry is indexed IN PLACE, so asking
// for changes against one would rewrite the repository's own checked-in
// fixtures. The harness refuses, loudly, instead of measuring the cheaper wrong
// thing — and the fixture tree is still byte-identical afterwards.
func TestIncrementalHarness_RefusesToMutateAnInTreeFixture(t *testing.T) {
	root := repoRoot(t)
	fixture := filepath.Join(root, "corpus", "fixtures", "hero-go")
	before := treeSnapshot(t, fixture)

	outPath := filepath.Join(t.TempDir(), "report.json")
	code := runFullRun(fullRunOptions{
		manifestPath:       filepath.Join(root, "corpus", "manifest.json"),
		repoName:           "tier1-fixture-hero-go",
		workDir:            t.TempDir(),
		runnerClass:        "test",
		outPath:            outPath,
		incrementalChanges: 100,
	})
	if code == 0 {
		t.Fatal("the run must not succeed: changing an in-tree fixture is refused, not skipped")
	}

	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	var rep evalreport.FullRunReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Repo.Incremental != nil {
		t.Error("a refused measurement must publish no series")
	}
	var explained bool
	for _, f := range rep.Repo.Failures {
		if strings.Contains(f, "incremental:") && strings.Contains(f, "local-path") {
			explained = true
		}
	}
	if !explained {
		t.Errorf("the refusal is not in the report's failures: %v", rep.Repo.Failures)
	}

	if after := treeSnapshot(t, fixture); after != before {
		t.Fatalf("the checked-in fixture was modified by an eval run:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// treeSnapshot renders a directory's file names and contents, so a mutation of
// any kind shows up as a string difference.
func treeSnapshot(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString(e.Name())
		b.WriteString("\n")
		b.Write(raw)
		b.WriteString("\n---\n")
	}
	return b.String()
}

// The default path is byte-unchanged: no flag, no series, no mutation.
func TestIncrementalHarness_DefaultRunPublishesNoSeries(t *testing.T) {
	rep := runFixtureFullRun(t, 0)
	if rep.Repo.Incremental != nil {
		t.Error("the default full-run path must not measure incremental changes: it would mutate the measured tree")
	}
}

// The workflow guard: the PR path and the other eval-full jobs must not acquire
// the change flag, and the >=100-change measurement must live in a job of its
// own against the reference scenario.
func TestIncrementalHarness_WorkflowsKeepTheChangesOutOfThePRPath(t *testing.T) {
	root := repoRoot(t)

	evalYML := readWorkflow(t, filepath.Join(root, ".github", "workflows", "eval.yml"))
	if strings.Contains(evalYML, "-incremental-changes") {
		t.Error("eval.yml (the PR gate) must not mutate a checkout: the fixture smoke check is the PR-time incremental signal")
	}

	jobs := workflowJobs(t, readWorkflow(t, filepath.Join(root, ".github", "workflows", "eval-full.yml")))
	for _, job := range []string{"full-run", "hero-suite", "cold-index-series", "query-latency-series"} {
		body, ok := jobs[job]
		if !ok {
			t.Fatalf("eval-full.yml no longer has a %s job", job)
		}
		if strings.Contains(body, "-incremental-changes") {
			t.Errorf("the %s job must not apply changes: mutating the checkout changes what every other number in it means", job)
		}
	}

	series, ok := jobs["freshness-series"]
	if !ok {
		t.Fatal("eval-full.yml has no freshness-series job: FR-8's 100 incremental changes are not exercised anywhere")
	}
	for _, want := range []string{"-incremental-changes 100", "grpc-go", "-reference-scenario", "-candidate", "-runner-class ubuntu-latest"} {
		if !strings.Contains(series, want) {
			t.Errorf("the freshness-series job does not pass %s", want)
		}
	}
	// grpc-go is not in the fail-closed budget selection; passing -budgets there
	// would fail the run for a configuration reason and hide the gate.
	if strings.Contains(series, "-budgets") {
		t.Error("the freshness series must be read against the reference-scenario gates, not the historical hero budgets")
	}
}
