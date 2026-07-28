package main

// SW-126 (P0-C3): the freshness and incremental-update harness.
//
// FR-8 asks for at least 100 incremental changes with incremental-update p50/p95
// AND freshness p50/p95. The repository measured one change, once, over a copied
// tier-1 fixture (`cmd/eval/perf.go`), and did not measure freshness at all.
//
// THREE THINGS THIS FILE OWNS.
//
//  1. THE TWO CLOCKS. `update` is the incremental ingest call and nothing else.
//     `freshness` starts the instant the file mutation is durably on disk and
//     stops at the first query that answers the new state — so it CONTAINS the
//     update, and freshness >= update holds for every completed change. Both
//     read through an injected clock so a test can pin exact microseconds
//     instead of racing a real one; a freshness test that flakes is a defect
//     here, consistent with the project's stance on conformance flakes.
//
//  2. THE FAILURE RULE (AC-6). A change that could not be applied, whose sync
//     errored, or that never became answerable is RETAINED as a sample with its
//     stage and its error, counted in changes_failed, and it degrades the
//     verdict. What it cannot do is contribute a latency it does not have. The
//     sequence therefore keeps running after a failure — one broken change must
//     not silently truncate a hundred-change measurement.
//
//  3. THE SEAM. incrementalTarget is the only thing that touches a repository,
//     so the driver's arithmetic and its failure classification are testable
//     without a clone, an index or a wall clock. The production implementation
//     (repoIncrementalTarget) is the one that writes files and calls
//     ingest.IngestChanged — and it only ever calls the SHIPPED ingest API:
//     AC-8, this story measures the incremental path, it does not alter it.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/internal/evalreport"
)

// freshnessStory is the contract's `measured_by` value for the gate this
// harness answers. A constant so the gate selection cannot drift from the story
// that owns it.
const freshnessStory = "SW-126"

// Convergence probing. A change that the ingest call already applied answers on
// the FIRST probe; the budget exists for the case where it does not, so that
// "never converged" is a bounded observation rather than a hang.
//
// The delay is deliberately short relative to the 2 s freshness gate: a coarse
// poll would quantise every non-instant freshness figure up to the poll
// interval and publish the harness's own granularity as the product's latency.
const (
	freshnessMaxProbes  = 40
	freshnessProbeDelay = 25 * time.Millisecond
)

// incrementalCrossPackageSample bounds how many high-degree symbols are
// inspected when looking for cross-package targets, and how many targets are
// kept. Bounded because the inspection issues two graph queries per candidate;
// deterministic because DegreeStratifiedSymbols is a total order over a fixed
// graph.
const (
	incrementalCrossPackageSample  = 50
	incrementalCrossPackageTargets = 5
	// crossPackageExampleSources caps the example neighbour paths published per
	// target — enough to spot-check the count, not enough to bloat the artifact.
	crossPackageExampleSources = 3
)

// incrementalTarget is the repository under measurement. Every method is a
// stage the driver can attribute a failure to.
type incrementalTarget interface {
	// apply mutates the working tree. It returns when the change is durably on
	// disk — which is where the freshness clock starts.
	apply(ctx context.Context, s changeStep) error
	// update runs the incremental ingest for the changed path. This call, and
	// only this call, is what update_us times.
	update(ctx context.Context, s changeStep) error
	// answers probes whether a query answers the NEW state (the step's
	// `expect`). It reports the observation, not an assertion: false is "not
	// yet", an error is a broken probe.
	answers(ctx context.Context, s changeStep) (bool, error)
}

// incrementalDriver executes a sequence against a target. The clock and the
// sleep are parameters so the whole thing is deterministic under test.
type incrementalDriver struct {
	target incrementalTarget
	now    func() time.Time
	sleep  func(time.Duration)
	// maxProbes and probeDelay bound the convergence wait per change.
	maxProbes  int
	probeDelay time.Duration
}

func newIncrementalDriver(target incrementalTarget) incrementalDriver {
	return incrementalDriver{
		target:     target,
		now:        time.Now,
		sleep:      time.Sleep,
		maxProbes:  freshnessMaxProbes,
		probeDelay: freshnessProbeDelay,
	}
}

// run executes every step and returns one sample per step — including the
// failed ones, in sequence order, always len(steps) of them.
func (d incrementalDriver) run(ctx context.Context, steps []changeStep) []evalreport.ChangeSample {
	samples := make([]evalreport.ChangeSample, 0, len(steps))
	for _, step := range steps {
		samples = append(samples, d.runStep(ctx, step))
	}
	return samples
}

// runStep is one change. The order of operations IS the measurement definition:
// mutate → start the freshness clock → time the update → probe until the new
// state is answerable.
func (d incrementalDriver) runStep(ctx context.Context, step changeStep) evalreport.ChangeSample {
	sample := evalreport.ChangeSample{
		Step:   step.index,
		Class:  step.class,
		Path:   step.path,
		Symbol: step.symbol,
		Expect: step.expect,
		Status: evalreport.ChangeCompleted,
	}
	fail := func(stage string, err error) evalreport.ChangeSample {
		sample.Status = evalreport.ChangeFailed
		sample.FailedStage = stage
		sample.Error = err.Error()
		return sample
	}

	if err := d.target.apply(ctx, step); err != nil {
		return fail(evalreport.ChangeStageApply, err)
	}
	// The file change is now durably on disk. Everything after this instant is
	// the interval a user experiences as staleness.
	changedAt := d.now()

	if err := d.target.update(ctx, step); err != nil {
		// No update latency: a sync that errored did not perform an update, and
		// recording how long it took to fail as an update time would put a
		// failure into the performance distribution.
		return fail(evalreport.ChangeStageUpdate, err)
	}
	updatedAt := d.now()
	sample.UpdateUS = microsBetween(changedAt, updatedAt)
	sample.UpdateMeasured = true

	for probe := 1; probe <= d.maxProbes; probe++ {
		sample.Probes = probe
		answered, err := d.target.answers(ctx, step)
		if err != nil {
			return fail(evalreport.ChangeStageConverge, err)
		}
		if answered {
			sample.FreshnessUS = microsBetween(changedAt, d.now())
			sample.FreshnessMeasured = true
			return sample
		}
		if probe < d.maxProbes {
			d.sleep(d.probeDelay)
		}
	}
	return fail(evalreport.ChangeStageConverge, fmt.Errorf(
		"the change did not become answerable within %d probes over ~%s (expected: %s)",
		d.maxProbes, time.Duration(d.maxProbes)*d.probeDelay, step.expect))
}

// microsBetween is the elapsed microseconds, clamped at zero. Clamped because a
// non-monotonic injected clock is a test bug, and a negative latency in the
// artifact would be worse than a zero: it would be a number nobody could act on.
func microsBetween(start, end time.Time) int64 {
	d := end.Sub(start)
	if d < 0 {
		return 0
	}
	return d.Microseconds()
}

// buildIncrementalSeries assembles the artifact from the retained samples.
// Every statistic it publishes comes from evalreport.RecomputeIncremental,
// which is the same function a consumer uses to reproduce them (AC-7).
func buildIncrementalSeries(repo string, requested int, in changeSequenceInput, steps []changeStep, samples []evalreport.ChangeSample) *evalreport.IncrementalSeries {
	series := &evalreport.IncrementalSeries{
		Repo:                repo,
		Requested:           requested,
		Minimum:             evalreport.IncrementalChangeMinimum,
		Sequence:            changeSequenceInfo(in, steps),
		Coverage:            evalreport.ChangeClassCoverageOf(samples),
		Changes:             samples,
		FreshnessDefinition: evalreport.FreshnessDefinitionNote,
		TimingMethod:        evalreport.IncrementalTimingMethodNote,
		AggregateMethod:     evalreport.IncrementalAggregateMethodNote,
		ScopeLimitation:     evalreport.IncrementalScopeLimitation,
		Notes:               evalreport.IncrementalNotes,
	}
	for _, s := range samples {
		if s.Status == evalreport.ChangeCompleted {
			series.Completed++
			continue
		}
		series.Failed++
		series.Warnings = append(series.Warnings, fmt.Sprintf(
			"change %d (%s %s) failed at the %s stage: %s", s.Step, s.Class, s.Path, s.FailedStage, s.Error))
	}

	recomputed := evalreport.RecomputeIncremental(samples)
	series.Update = recomputed.Update
	series.Freshness = recomputed.Freshness
	for _, row := range series.Coverage {
		if row.Steps == 0 {
			continue
		}
		series.PerClass = append(series.PerClass, recomputed.Classes[row.Class])
	}

	series.Sufficient = series.Completed >= series.Minimum
	if !series.Sufficient {
		series.Warnings = append(series.Warnings, fmt.Sprintf(
			"only %d change(s) completed, below FR-8's floor of %d: every gate read over this series is UNKNOWN, not PASS",
			series.Completed, series.Minimum))
	}
	series.ClassesCovered = evalreport.AllRequiredClassesCovered(samples)
	if !series.ClassesCovered {
		series.Warnings = append(series.Warnings, "the sequence did not complete a change in every required class ("+
			strings.Join(evalreport.RequiredChangeClasses, ", ")+"): it is not the change sequence FR-8 describes")
	}
	if !in.crossPackage.Satisfied && in.crossPackage.Reason != "" {
		series.Warnings = append(series.Warnings, "no cross-package change class: "+in.crossPackage.Reason)
	}
	return series
}

// incrementalStatus applies PRD §8.2 to the series: FAIL beats UNKNOWN beats
// PASS. Anything unmeasured — an insufficient sequence, a missing class, a
// failed change — stops it reading green, and a failed gate is a failure even
// when another was unmeasured.
func incrementalStatus(s *evalreport.IncrementalSeries) string {
	if s == nil {
		return evalreport.StatusUnknown
	}
	for _, g := range s.Gates {
		if g.Status == evalreport.StatusFail {
			return evalreport.StatusFail
		}
	}
	if !s.Sufficient || !s.ClassesCovered || s.Failed > 0 || len(s.Gates) == 0 {
		return evalreport.StatusUnknown
	}
	for _, g := range s.Gates {
		if g.Status != evalreport.StatusPass {
			return evalreport.StatusUnknown
		}
	}
	return evalreport.StatusPass
}

// printIncrementalSummary makes the measurement readable in the job log.
func printIncrementalSummary(w *os.File, s *evalreport.IncrementalSeries) {
	if s == nil {
		return
	}
	fmt.Fprintf(w, "eval: incremental over %s — %d/%d changes completed, %d failed (floor %d, sufficient=%v, classes=%v)\n",
		s.Repo, s.Completed, s.Requested, s.Failed, s.Minimum, s.Sufficient, s.ClassesCovered)
	fmt.Fprintf(w, "eval:   update    p50 %.2f ms  p95 %.2f ms  (n=%d)\n",
		float64(s.Update.P50US)/1000, float64(s.Update.P95US)/1000, s.Update.N)
	fmt.Fprintf(w, "eval:   freshness p50 %.2f ms  p95 %.2f ms  (n=%d)\n",
		float64(s.Freshness.P50US)/1000, float64(s.Freshness.P95US)/1000, s.Freshness.N)
	for _, c := range s.PerClass {
		fmt.Fprintf(w, "eval:   class %-14s n=%-4d update p95 %.2f ms  freshness p95 %.2f ms\n",
			c.Class, c.Changes, float64(c.Update.P95US)/1000, float64(c.Freshness.P95US)/1000)
	}
	for _, g := range s.Gates {
		fmt.Fprintf(w, "eval:   gate %-20s %-8s %s\n", g.ID, g.Status, g.Reason)
	}
	for _, warning := range s.Warnings {
		fmt.Fprintf(w, "eval:   WARNING %s\n", warning)
	}
}

// ---------------------------------------------------------------------------
// The production target: a real checkout, a real ingester, a real query engine.
// ---------------------------------------------------------------------------

// repoIncrementalTarget applies changes to a checked-out repository and drives
// the SHIPPED incremental ingest over it.
//
// It calls ingest.IngestChanged — the same entry point `graphi sync` uses over
// the auto-managed store — and engine/search for the probe. Nothing in
// engine/ or core/ is touched by this story: AC-8 is a property of this type
// having no privileged access, not a promise.
type repoIncrementalTarget struct {
	root     string
	ing      *ingest.Ingester
	searcher *search.Service
}

func (t repoIncrementalTarget) apply(ctx context.Context, s changeStep) error {
	abs := filepath.Join(t.root, filepath.FromSlash(s.path))
	switch s.class {
	case evalreport.ChangeClassDelete:
		if err := os.Remove(abs); err != nil {
			return fmt.Errorf("remove %s: %w", s.path, err)
		}
		return nil
	case evalreport.ChangeClassAdd:
		if err := os.WriteFile(abs, addedFileContent(s), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", s.path, err)
		}
		return nil
	default:
		existing, err := os.ReadFile(abs)
		if err != nil {
			return fmt.Errorf("read %s: %w", s.path, err)
		}
		if err := os.WriteFile(abs, modifiedFileContent(existing, s), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", s.path, err)
		}
		return nil
	}
}

func (t repoIncrementalTarget) update(ctx context.Context, s changeStep) error {
	return t.ing.IngestChanged(ctx, t.root, []string{s.path})
}

// answers probes the graph the way a user would: a search for the symbol. For
// an add, modify or cross-package change the new symbol must be findable; for a
// delete the removed file's symbol must be gone.
//
// A search — rather than a direct store lookup — because the FTS row is part of
// what has to be fresh: a graph that answers `callers` but not `search` is not
// yet answering the new state from the user's point of view.
func (t repoIncrementalTarget) answers(ctx context.Context, s changeStep) (bool, error) {
	resp, err := t.searcher.Search(ctx, s.symbol, 25)
	if err != nil {
		return false, fmt.Errorf("search %s: %w", s.symbol, err)
	}
	found := false
	for _, match := range resp.Matches {
		if bareName(match.QualifiedName) == s.symbol {
			found = true
			break
		}
	}
	if s.class == evalreport.ChangeClassDelete {
		return !found, nil
	}
	return found, nil
}

func bareName(qualified string) string {
	if i := strings.LastIndexByte(qualified, '.'); i >= 0 {
		return qualified[i+1:]
	}
	return qualified
}

// ---------------------------------------------------------------------------
// Building the sequence input from a real indexed repository.
// ---------------------------------------------------------------------------

// incrementalSetup is everything needed to plan and run the measurement over an
// indexed repository.
type incrementalSetup struct {
	repo     string
	root     string
	store    graphstore.Graphstore
	ing      *ingest.Ingester
	querySvc *query.Service
	searcher *search.Service
	changes  int
}

// measureIncremental plans and executes the whole measurement. It returns nil
// only when nothing could be planned; every other outcome — including a
// sequence where every change failed — produces a series, because "we tried and
// it broke" is evidence and silence is not.
func measureIncremental(ctx context.Context, setup incrementalSetup) (*evalreport.IncrementalSeries, error) {
	files, packages, err := incrementalSourceFiles(ctx, setup.store, setup.root)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("the index contains no modifiable Go source files to change")
	}
	in := changeSequenceInput{
		files:        files,
		packages:     packages,
		crossPackage: crossPackageTargets(ctx, setup.store, setup.querySvc),
		count:        setup.changes,
	}
	steps := buildChangeSequence(in)
	if len(steps) == 0 {
		return nil, errors.New("the change sequence is empty")
	}
	driver := newIncrementalDriver(repoIncrementalTarget{
		root: setup.root, ing: setup.ing, searcher: setup.searcher,
	})
	samples := driver.run(ctx, steps)
	return buildIncrementalSeries(setup.repo, setup.changes, in, steps, samples), nil
}

// incrementalSourceFiles is the canonical, deterministic list of files the
// sequence may change, plus the package clause each directory declares.
//
// The list comes from the GRAPH, not from a filesystem walk: a file the index
// never ingested cannot demonstrate an incremental update, and using the graph
// means the sequence targets exactly what ingest considers source.
func incrementalSourceFiles(ctx context.Context, store graphstore.Graphstore, root string) ([]string, map[string]string, error) {
	aggregate, ok := any(store).(graphstore.BriefAggregatePort)
	if !ok {
		return nil, nil, errors.New("BriefAggregatePort unavailable: the indexed file inventory cannot be read")
	}
	stats, err := aggregate.BriefStats(ctx, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("file inventory: %w", err)
	}
	packages := map[string]string{}
	var files []string
	for _, f := range stats.Files {
		if !modifiableGoFile(f.Path) {
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(f.Path))
		raw, err := os.ReadFile(abs)
		if err != nil {
			// An indexed path that is not readable now is not a candidate. It is
			// skipped rather than fatal: the sequence needs SOME files, not all.
			continue
		}
		dir := path.Dir(f.Path)
		if _, known := packages[dir]; !known {
			if pkg := goPackageClause(raw); pkg != "" {
				packages[dir] = pkg
			}
		}
		if packages[dir] == "" {
			// Without a package clause an added file in this directory would not
			// parse, so the directory contributes no targets at all.
			continue
		}
		files = append(files, f.Path)
	}
	sort.Strings(files)
	return files, packages, nil
}

// modifiableGoFile is the filter: Go source the sequence may edit. Test files
// are excluded because a `_test.go` change exercises a different ingest shape
// and would make the classes incomparable; vendored and generated trees are
// excluded because they are not what a user edits.
func modifiableGoFile(p string) bool {
	if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		switch part {
		case "vendor", "testdata", "third_party", "node_modules":
			return false
		}
	}
	return true
}

// goPackageClause reads the package name off a Go file's first package line.
// A line scan rather than go/parser: the harness only needs the identifier, and
// a file that does not parse is one this sequence should not be adding siblings
// to anyway.
func goPackageClause(raw []byte) string {
	for _, line := range strings.Split(string(raw), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "package ")
		if !ok {
			continue
		}
		name := strings.TrimSpace(rest)
		if i := strings.IndexAny(name, " \t/"); i >= 0 {
			name = name[:i]
		}
		if name != "" {
			return name
		}
	}
	return ""
}

// crossPackageTargets finds the files whose symbols really are referenced from
// other directories, so AC-2's cross-package class is evidence rather than a
// label.
//
// The candidates are the degree-stratified symbol sample — the same
// deterministic ordering SW-125's latency sample uses — and a candidate
// qualifies when its callers or referencing symbols include at least one whose
// source file lives in another directory. The qualifying count and a few
// example paths are published, so the claim can be spot-checked.
//
// Directory, not Go package: the harness does not resolve import paths, and in
// Go a directory IS a package. Saying "other directory" in the artifact keeps
// the claim exactly as strong as the evidence.
func crossPackageTargets(ctx context.Context, store graphstore.Graphstore, querySvc *query.Service) evalreport.CrossPackageEvidence {
	out := evalreport.CrossPackageEvidence{
		Method: "candidates are core/graphstore DegreeSamplePort.DegreeStratifiedSymbols (degree DESC, node id ASC — a total " +
			"order over a fixed graph); a candidate qualifies when engine/query callers+references return at least one " +
			"neighbour whose source file is in a DIFFERENT directory, which in Go is a different package. The qualifying " +
			"count and example neighbour paths are published per target.",
	}
	sampler, ok := any(store).(graphstore.DegreeSamplePort)
	if !ok {
		out.Reason = "DegreeSamplePort unavailable: cross-package candidates cannot be ranked from the graph"
		return out
	}
	candidates, err := sampler.DegreeStratifiedSymbols(ctx, incrementalCrossPackageSample)
	if err != nil {
		out.Reason = "the degree-stratified sample could not be read: " + err.Error()
		return out
	}
	seen := map[string]bool{}
	for _, node := range candidates {
		out.Examined++
		src := node.SourcePath()
		if !modifiableGoFile(src) || seen[src] {
			continue
		}
		target, ok := qualifyCrossPackage(ctx, querySvc, node)
		if !ok {
			continue
		}
		seen[src] = true
		out.Targets = append(out.Targets, target)
		if len(out.Targets) >= incrementalCrossPackageTargets {
			break
		}
	}
	out.Satisfied = len(out.Targets) > 0
	if !out.Satisfied {
		out.Reason = fmt.Sprintf("none of the %d highest-degree symbols inspected had an inbound edge from another directory; "+
			"this repository exposes no cross-package edges to the harness", out.Examined)
	}
	return out
}

// qualifyCrossPackage inspects ONE candidate symbol and returns its evidence.
func qualifyCrossPackage(ctx context.Context, querySvc *query.Service, node model.Node) (evalreport.CrossPackageTarget, bool) {
	target := evalreport.CrossPackageTarget{Path: node.SourcePath(), Symbol: node.QualifiedName()}
	dir := path.Dir(node.SourcePath())
	seen := map[string]bool{}
	for _, result := range neighbourResults(ctx, querySvc, node.ID()) {
		for _, n := range result.Nodes {
			if n.ID == node.ID() || n.SourcePath == "" || path.Dir(n.SourcePath) == dir || seen[n.SourcePath] {
				continue
			}
			seen[n.SourcePath] = true
			target.InboundFromOtherDirs++
			if len(target.ExampleSources) < crossPackageExampleSources {
				target.ExampleSources = append(target.ExampleSources, n.SourcePath)
			}
		}
	}
	sort.Strings(target.ExampleSources)
	return target, target.InboundFromOtherDirs > 0
}

// neighbourResults is the inbound view of a symbol: who calls it and who
// references it. A query error yields no neighbours rather than failing the
// selection — an unqualified candidate simply is not chosen, and the next one
// is inspected.
func neighbourResults(ctx context.Context, querySvc *query.Service, id model.NodeId) []query.Result {
	var out []query.Result
	if r, err := querySvc.Callers(ctx, id); err == nil {
		out = append(out, r)
	}
	if r, err := querySvc.References(ctx, id); err == nil {
		out = append(out, r)
	}
	return out
}
