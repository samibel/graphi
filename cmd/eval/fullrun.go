package main

// SW-123 (EVAL-02): the full-run measurement harness. One invocation measures
// ONE pinned corpus repository end-to-end — clone (fail-closed SHA pin) →
// cold full index (wallclock, peak RSS, DB size) → warm per-op-class p95 over
// the same in-process session — and emits the raw internal/evalreport JSON
// used for fail-closed compatibility limits and, once the same harness method
// has a pinned reference run, comparable performance ratchets.
//
// One repo per process is deliberate: getrusage MAXRSS is a process-lifetime
// peak, so batching repos would attribute the largest repo's peak to every
// later one. The eval-full workflow runs a matrix job per repo.
//
// The measurement model is the in-process session (the long-lived MCP shape):
// engine services over an open SQLite store, operations invoked through the
// same engine/scenario.FixtureEngine the hero suite executes — no divergent
// invocation path to keep honest.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/scenario"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/internal/corpus"
	"github.com/samibel/graphi/internal/evalreport"
)

// DEFAULT-PATH warm sample counts. They bound the wallclock of the largest
// repo (guava) and are what `-full-run` executes when no FR-8 floor is asked
// for; the concrete ops and counts are recorded in the report so runs are
// interpretable.
//
// SW-125 did NOT re-tune these. FR-8's 1000-executions-per-class floor is
// requested explicitly with -query-executions and runs through eval-full.yml;
// leaving the default counts alone is what keeps the PR path unchanged (AC-8),
// and query_latency.sufficient reports honestly that a default run is below
// the floor rather than letting the two look alike.
const (
	fullRunSymbolSample    = 25 // symbols driven through the structural ops
	fullRunSearchIters     = 20 // timed iterations per manifest search query
	fullRunAgentToolSample = 10 // symbols driven through explain/risk/related
	fullRunBriefIters      = 3  // agent_brief assemblies (project-wide scan)
)

const fullRunNotes = "in-process session model: engine services over one open SQLite store; " +
	"cold index timed around IngestAll; index and post-stable-suite peak RSS = getrusage MAXRSS; " +
	"deterministic degree-stratified symbol sample, published verbatim in query_latency.symbol_sample; " +
	"all 12 stable operations covered with an explicit operation-to-class mapping; " +
	"warm p50 AND p95 pooled per op class over the recorded warm_ops/warm_samples, with every " +
	"individual measurement retained in query_latency.operations[].samples_us"

// fullRunOptions is one full-run invocation. It is a struct rather than a
// positional list because SW-124 adds the cold-state protocol to what was
// already seven strings, and a misordered pair of paths is a silent wrong
// measurement rather than a compile error.
type fullRunOptions struct {
	manifestPath string
	repoName     string
	workDir      string
	runnerClass  string
	outPath      string
	budgetPath   string
	scenarioPath string
	// dropCaches asks for the page cache to be dropped between the clone and
	// the timed index (SW-124 AC-1). It is a request, not a claim: what the
	// protocol actually achieved is recorded in the run's ColdState.
	dropCaches bool
	// queryExecutions is SW-125's FR-8 floor request: at least this many timed
	// executions per query class AND per §12.2 gate pool. 0 is the default
	// path — the historical fixed sample counts, byte-unchanged — which the
	// report then honestly marks as undersampled rather than gate-ready.
	queryExecutions int
	// incrementalChanges is SW-126's FR-8 floor request: run this many
	// incremental changes against the measured checkout and report freshness and
	// incremental-update percentiles. 0 is the default path, where the phase does
	// not run at all — it MUTATES the checkout, so a run that performs it is
	// measuring a different tree from the one every other number describes.
	incrementalChanges int
	// candidatePath is the evidence index the frozen candidate is cited from,
	// used to decide whether this run's numbers are about the candidate at all.
	// It is read only when a reference-scenario contract is supplied, i.e. only
	// when there are gates to read; an unreadable index makes every gate
	// UNKNOWN rather than failing the measurement.
	candidatePath string
}

// runFullRun executes the full measurement for one manifest entry and writes
// the report to o.outPath. Returns the process exit code.
//
// o.scenarioPath, when set, is the SW-123 reference-scenario contract: the run
// is validated against it and stamped with the runner class's declared ROLE,
// so a comparison-class report can never be mistaken for reference evidence.
// A runner class the contract does not declare fails the run closed — that is
// how numbers from unnamed machines stop sitting beside reference values with
// equal standing.
func runFullRun(o fullRunOptions) int {
	ctx := context.Background()

	class, isReference, err := resolveRunnerClass(o.scenarioPath, o.manifestPath, o.runnerClass, o.repoName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: %v\n", err)
		return 2
	}

	m, err := corpus.LoadManifest(o.manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: load manifest: %v\n", err)
		return 2
	}
	var entry *corpus.Entry
	for i := range m.Entries {
		if m.Entries[i].Name == o.repoName {
			entry = &m.Entries[i]
			break
		}
	}
	if entry == nil {
		names := make([]string, 0, len(m.Entries))
		for _, e := range m.Entries {
			names = append(names, e.Name)
		}
		fmt.Fprintf(os.Stderr, "eval: -full-run %q not in manifest (have: %s)\n", o.repoName, strings.Join(names, ", "))
		return 2
	}

	workDir := o.workDir
	if workDir == "" {
		workDir, err = os.MkdirTemp("", "graphi-eval-full")
		if err != nil {
			fmt.Fprintf(os.Stderr, "eval: workdir: %v\n", err)
			return 2
		}
		defer os.RemoveAll(workDir)
	}

	// The contract, when one was supplied, is already validated by
	// resolveRunnerClass; it is re-read here because SW-125's execution plan
	// needs the gates' operation pools BEFORE anything is measured — the number
	// of executions a run performs is decided by what the gates will be read
	// over, not discovered afterwards.
	var contract *referenceScenario
	if o.scenarioPath != "" {
		if rs, err := loadReferenceScenario(o.scenarioPath); err == nil {
			contract = &rs
		}
	}
	plan := newQueryLatencyPlan(o.queryExecutions, contract)

	run := fullRepoRun(ctx, *entry, filepath.Dir(o.manifestPath), workDir, coldProtocol{
		drop:             o.dropCaches,
		requiredProtocol: class.CacheState,
		// The reference class declares a drop-caches protocol; the comparison
		// class declares its cache state uncontrolled. A reference-class run
		// that did not drop the cache is therefore not cold BY ITS OWN
		// DECLARATION, and says so rather than being quietly published.
		dropRequired: class.Role == roleReference,
	}, plan, o.incrementalChanges)

	// SW-125: read the §12.2 warm-latency gates against what was measured. The
	// provenance facts are final by now, and every one of them can only make a
	// gate UNKNOWN — none of them can turn a missed threshold into a pass.
	measuredSHA := resolveCommit()
	prov := gateProvenance{
		repo:              o.repoName,
		runnerClass:       o.runnerClass,
		runnerRole:        class.Role,
		referenceScenario: isReference,
		measuredSHA:       measuredSHA,
		worktreeDirty:     strings.HasSuffix(measuredSHA, "+dirty"),
	}
	if o.scenarioPath != "" {
		sha, source, err := loadCandidateSHA(o.candidatePath)
		switch {
		case err != nil:
			prov.candidateError = err.Error()
		default:
			prov.candidateSHA, prov.candidateSource = sha, source
			prov.candidateMatch = !prov.worktreeDirty && strings.EqualFold(strings.TrimSuffix(measuredSHA, "+dirty"), sha)
		}
	}
	if run.QueryLatency != nil {
		run.QueryLatency.Gates = readQueryGates(o.scenarioPath, run.QueryLatency, prov)
		run.QueryLatency.Status = queryLatencyStatus(run.QueryLatency)
	}
	// SW-126: the freshness gate reads the SAME provenance value. One run has one
	// answer to "is this about the frozen candidate on the reference scenario",
	// and two harnesses computing it separately would eventually disagree about
	// which is authoritative.
	if run.Incremental != nil {
		run.Incremental.Gates = readFreshnessGates(o.scenarioPath, run.Incremental, prov)
		run.Incremental.Status = incrementalStatus(run.Incremental)
	}
	if o.budgetPath != "" {
		run.BudgetSource = o.budgetPath
		if run.Pass {
			checks, err := checkFullRunBudgets(o.budgetPath, o.runnerClass, run)
			if err != nil {
				run.Failures = append(run.Failures, "budgets: "+err.Error())
			} else {
				run.BudgetChecks = checks
				for _, check := range checks {
					if !check.Pass {
						run.Failures = append(run.Failures, fmt.Sprintf("budget %s exceeded: %.3f %s > %.3f %s", check.Name, check.Measured, check.Unit, check.Budget, check.Unit))
					}
				}
			}
			run.Pass = len(run.Failures) == 0
		}
	}

	report := evalreport.FullRunReport{
		Header:            evalreport.NewHeader("0.0.0-dev", measuredSHA),
		RunnerClass:       o.runnerClass,
		RunnerRole:        class.Role,
		ReferenceScenario: isReference,
		ScenarioSource:    o.scenarioPath,
		Notes:             fullRunNotes,
		Repo:              run,
		Cgroup:            observedCgroupLimits(),
	}
	report.Header.CorpusVersion = manifestVersion(o.manifestPath)
	if o.scenarioPath != "" && !isReference {
		fmt.Fprintf(os.Stderr, "eval: NOTE - %s on runner class %s (%s) is NOT the reference scenario; these numbers are not reference evidence and freeze no budget\n",
			o.repoName, o.runnerClass, class.Role)
	}

	outPath := o.outPath
	if outPath == "" {
		outPath = "eval-full-" + o.repoName + ".json"
	}
	if err := evalreport.WriteFullRunJSON(report, outPath); err != nil {
		fmt.Fprintf(os.Stderr, "eval: write report: %v\n", err)
		return 2
	}
	fmt.Fprintf(os.Stderr, "eval: wrote full-run report to %s\n", outPath)
	printQueryLatencySummary(os.Stderr, run.QueryLatency)
	printIncrementalSummary(os.Stderr, run.Incremental)

	if !run.Pass {
		fmt.Fprintf(os.Stderr, "eval: FAIL - full run over %s: %s\n", o.repoName, strings.Join(run.Failures, "; "))
		return 1
	}
	// A FAILED §12.2 warm gate fails the run. UNKNOWN does not: following
	// `cmd/evidence -check` and SW-124's series, an UNKNOWN row is honest
	// reporting rather than a broken job, and it cannot be mistaken for green
	// because the artifact and the summary above both say UNKNOWN.
	if run.QueryLatency != nil && run.QueryLatency.Status == evalreport.StatusFail {
		fmt.Fprintf(os.Stderr, "eval: FAIL - query-latency gate over %s\n", o.repoName)
		return 1
	}
	if run.Incremental != nil && run.Incremental.Status == evalreport.StatusFail {
		fmt.Fprintf(os.Stderr, "eval: FAIL - freshness gate over %s\n", o.repoName)
		return 1
	}
	fmt.Fprintf(os.Stderr, "eval: PASS - full run over %s (index %dms, rss %dMB, db %dB)\n",
		o.repoName, run.Index.WallclockMS, run.Index.PeakRSSMB, run.Index.DBSizeBytes)
	return 0
}

// printQueryLatencySummary makes the measurement readable in the job log: what
// was executed, whether it met FR-8's floor, and how every gate read.
func printQueryLatencySummary(w *os.File, s *evalreport.QueryLatencySeries) {
	if s == nil {
		return
	}
	fmt.Fprintf(w, "eval: query latency over %s — %d timed executions, floor %d per class (sufficient=%v)\n",
		s.Repo, s.TotalExecutions, s.Minimum, s.Sufficient)
	for _, c := range s.Classes {
		fmt.Fprintf(w, "eval:   class %-12s n=%-6d p50 %.2f ms  p95 %.2f ms  (sufficient=%v)\n",
			c.Class, c.N, float64(c.P50US)/1000, float64(c.P95US)/1000, c.Sufficient)
	}
	for _, g := range s.Gates {
		fmt.Fprintf(w, "eval:   gate %-26s %-8s %s\n", g.ID, g.Status, g.Reason)
	}
	for _, warning := range s.Warnings {
		fmt.Fprintf(w, "eval:   WARNING %s\n", warning)
	}
}

// resolveRunnerClass validates the reference-scenario contract and resolves the
// declared runner class. An empty scenarioPath means "no contract supplied"
// (the hermetic fixture path, and the pre-SW-123 behavior); anything else is
// enforced fail-closed.
//
// It returns the whole class rather than only its role because SW-124 needs the
// class's DECLARED cache-state protocol to say whether a run met it.
func resolveRunnerClass(scenarioPath, manifestPath, runnerClassID, repoName string) (class runnerClass, isReference bool, err error) {
	if scenarioPath == "" {
		return runnerClass{}, false, nil
	}
	rs, err := loadReferenceScenario(scenarioPath)
	if err != nil {
		return runnerClass{}, false, fmt.Errorf("reference scenario: %w", err)
	}
	repos, err := corpusRepoNames(manifestPath)
	if err != nil {
		return runnerClass{}, false, fmt.Errorf("corpus manifest: %w", err)
	}
	if err := validateReferenceScenario(rs, repos); err != nil {
		return runnerClass{}, false, fmt.Errorf("reference scenario %s: %w", scenarioPath, err)
	}
	for _, c := range rs.RunnerClasses {
		if c.ID == runnerClassID {
			return c, c.Role == roleReference && repoName == rs.ReferenceScenario.Repo, nil
		}
	}
	declared := make([]string, 0, len(rs.RunnerClasses))
	for _, c := range rs.RunnerClasses {
		declared = append(declared, c.ID+"("+c.Role+")")
	}
	return runnerClass{}, false, fmt.Errorf("runner class %q is not declared in %s (declared: %s); an undeclared machine's numbers must not stand beside reference values",
		runnerClassID, scenarioPath, strings.Join(declared, ", "))
}

// manifestVersion re-reads the manifest's version stamp (LoadManifest does not
// carry it; the eval scorecard loader does).
func manifestVersion(path string) int {
	v, _, err := loadCorpusManifest(path)
	if err != nil {
		return 0
	}
	return v
}

// fullRepoRun performs clone → cold-state preparation → index → warm
// measurement for one entry. Every failure is recorded in the returned run;
// fatal stages stop the pipeline.
//
// plan is SW-125's query-latency plan: how large the symbol sample is and how
// many timed executions each operation runs. It is resolved by the caller
// before any measurement so the artifact can state what the run intended to
// measure, not only what it happened to produce.
//
// incrementalChanges is SW-126's requested change count, and 0 means the phase
// does not run. It is LAST in the pipeline for a reason: it is the only stage
// that mutates the checkout, so every other measurement in this report is taken
// over the pinned tree exactly as cloned.
func fullRepoRun(ctx context.Context, e corpus.Entry, manifestDir, workDir string, cp coldProtocol, plan queryLatencyPlan, incrementalChanges int) evalreport.FullRepoRun {
	run := evalreport.FullRepoRun{Name: e.Name, Ref: e.Ref, Tier: e.Tier}
	fail := func(stage string, err error) evalreport.FullRepoRun {
		run.Failures = append(run.Failures, fmt.Sprintf("%s: %v", stage, err))
		return run
	}

	// 1. Materialize the tree: shallow-clone URL entries at the pinned ref and
	// verify the SHA pin fail-closed; local path entries resolve against the
	// repo root (hermetic smoke path).
	var repoDir string
	switch {
	case e.URL != "":
		repoDir = filepath.Join(workDir, e.Name)
		cloneStart := time.Now()
		if err := cloneAt(ctx, e.URL, e.Ref, repoDir); err != nil {
			return fail("clone", err)
		}
		run.CloneMS = time.Since(cloneStart).Milliseconds()
		head, err := gitHead(ctx, repoDir)
		if err != nil {
			return fail("head", err)
		}
		run.SHA = head
		if e.SHA != "" && !strings.EqualFold(e.SHA, head[:min(len(e.SHA), len(head))]) {
			return fail("pin", fmt.Errorf("checkout HEAD %s does not match pinned sha %s (tag re-pointed?)", head, e.SHA))
		}
	case e.Path != "":
		repoDir = filepath.Join(filepath.Dir(manifestDir), filepath.FromSlash(e.Path))
	default:
		return fail("entry", fmt.Errorf("neither url nor path set"))
	}

	// 2. Cold full index into a fresh on-disk SQLite store (the shipped
	// session backend), timed; index-only peak RSS sampled immediately afterwards.
	dbPath := filepath.Join(workDir, e.Name+".db")
	metaDir := filepath.Join(workDir, e.Name+"-meta")

	// 2a. Produce and VERIFY coldness before the store is opened — after the
	// clone, because a clone re-warms the page cache with exactly the files
	// about to be indexed, and dropping the cache before it would measure a
	// warm index while claiming a cold one.
	run.Cold = prepareCold(ctx, cp, dbPath, metaDir)

	store, err := graphstore.OpenSQLite(dbPath)
	if err != nil {
		return fail("open store", err)
	}
	defer store.Close()
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), metaDir)
	if err != nil {
		return fail("ingest.New", err)
	}
	defer ing.Close()

	indexStart := time.Now()
	if err := ing.IngestAll(ctx, repoDir); err != nil {
		return fail("index", err)
	}
	run.Index.WallclockMS = time.Since(indexStart).Milliseconds()
	run.Index.PeakRSSMB = peakRSSMB()

	aggregate, ok := any(store).(graphstore.BriefAggregatePort)
	if !ok {
		return fail("inventory", fmt.Errorf("BriefAggregatePort unavailable"))
	}
	stats, err := aggregate.BriefStats(ctx, 0)
	if err != nil {
		return fail("inventory", err)
	}
	run.Index.Nodes, run.Index.Edges, run.Index.Files = stats.TotalNodes, stats.TotalEdges, len(stats.Files)

	// 3. Warm query-latency measurement over the same open store, driven
	// through the same FixtureEngine the hero suite uses.
	eng := scenario.NewFixtureEngine(resolve.Deps{Query: query.New(store), Search: search.New(store)})
	eng.RepoRoot = repoDir
	eng.ProjectName = e.Name
	sampler, ok := any(store).(graphstore.DegreeSamplePort)
	if !ok {
		return fail("sample", fmt.Errorf("DegreeSamplePort unavailable"))
	}
	sampleNodes, err := sampler.DegreeStratifiedSymbols(ctx, plan.symbolSample)
	if err != nil {
		return fail("sample", err)
	}
	if len(sampleNodes) == 0 {
		return fail("sample", fmt.Errorf("index produced no function/method symbols to measure"))
	}

	// SW-125 AC-5: the sample is published verbatim, in order, with a digest,
	// so a second run can be shown to have measured the same question. The
	// determinism is the store's — DegreeStratifiedSymbols is a total order
	// over a fixed graph — and this is where it becomes checkable.
	symbolIDs := make([]string, 0, len(sampleNodes))
	for _, n := range sampleNodes {
		symbolIDs = append(symbolIDs, string(n.ID()))
	}
	agentSymbols := min(plan.agentSymbols, len(symbolIDs))
	symbolSample := evalreport.QuerySymbolSample{
		Requested:    plan.symbolSample,
		Returned:     len(symbolIDs),
		Method:       querySampleMethod,
		Digest:       evalreport.SampleDigest(symbolIDs),
		SymbolIDs:    symbolIDs,
		AgentSymbols: agentSymbols,
	}

	perOp := map[string][]time.Duration{}
	stableChecks := map[string]*evalreport.StableOperationCheck{}
	checkFor := func(op, requirement string) *evalreport.StableOperationCheck {
		check := stableChecks[op]
		if check == nil {
			check = &evalreport.StableOperationCheck{
				Operation: op, Requirement: requirement, Outcomes: map[string]int{}, Pass: true,
			}
			stableChecks[op] = check
		}
		return check
	}
	recordOutcome := func(op, requirement, outcome string, allowed ...string) bool {
		check := checkFor(op, requirement)
		check.Samples++
		if outcome == "" {
			outcome = "missing"
		}
		check.Outcomes[outcome]++
		for _, want := range allowed {
			if outcome == want {
				return true
			}
		}
		if check.Pass {
			run.Failures = append(run.Failures, fmt.Sprintf("stable %s returned outcome %q; require %s", op, outcome, requirement))
		}
		check.Pass = false
		return false
	}
	// observe is the semantic gate every timed execution passes through: a
	// measurement counts only after the response resolved to an
	// operation-appropriate outcome, because a fast wrong answer is not a fast
	// answer. The rule and its wording predate SW-125; only its call site moved
	// out of the (now deleted) timeOp closure, so that timing lives in
	// executeWarmOperation where the untimed/timed boundary is explicit.
	observe := func(op string) func(warmExecution, string, error) bool {
		return func(x warmExecution, outcome string, err error) bool {
			if err != nil {
				check := checkFor(op, x.requirement)
				check.Samples++
				check.Outcomes["error"]++
				if check.Pass {
					run.Failures = append(run.Failures, fmt.Sprintf("warm %s: %v", op, err))
				}
				check.Pass = false
				return false
			}
			return recordOutcome(op, x.requirement, outcome, x.allowed...)
		}
	}

	indexOutcome := "empty"
	if run.Index.Nodes > 0 && run.Index.Files > 0 {
		indexOutcome = "found"
	}
	recordOutcome(scenario.OpIndex, "successful ingest with non-empty node and file inventory", indexOutcome, "found")

	// The manifest's search promises are asserted ONCE, untimed, before any
	// measurement — the promise is a property of the index, not something worth
	// re-checking a thousand times, and asserting it here keeps it out of the
	// timed region (AC-6).
	for _, s := range e.Searches {
		q := s.Query
		lines, _, err := eng.Invoke(scenario.OpSearch, map[string]string{"query": q})
		if err != nil {
			run.Failures = append(run.Failures, fmt.Sprintf("search %q: %v", q, err))
			continue
		}
		found := len(lines) > 0 && strings.HasSuffix(lines[0], "found")
		run.Searches = append(run.Searches, evalreport.SearchCheck{
			Query: q, Matches: len(lines) - 1, Pass: !s.ExpectNonEmpty || found,
		})
		if s.ExpectNonEmpty && !found {
			run.Failures = append(run.Failures, fmt.Sprintf("search %q: expected non-empty, got none", q))
		}
	}

	for _, assertion := range e.ConfirmedEdges {
		check := evaluateConfirmedEdge(ctx, eng, assertion)
		run.SemanticChecks = append(run.SemanticChecks, check)
		if !check.Pass {
			run.Failures = append(run.Failures, fmt.Sprintf("semantic %s: %s (observed %s)", check.Name, check.Requirement, check.Observed))
		}
	}

	// The measured operations, built once and then driven by the plan. Building
	// them declaratively is what makes "all twelve stable operations are
	// covered" checkable against the taxonomy instead of against a reading of
	// the loop below.
	warmOps := buildWarmOperations(ctx, eng, e, symbolIDs, agentSymbols)
	for i := range warmOps {
		warmOps[i].executions = plan.executionsFor(warmOps[i].op, len(symbolIDs), agentSymbols, len(e.Searches))
		warmOps[i].warmup = plan.warmup
	}
	sort.Slice(warmOps, func(i, j int) bool { return warmOps[i].op < warmOps[j].op })

	warmupOf := map[string]int{}
	for _, w := range warmOps {
		warmupOf[w.op] = w.warmup
		perOp[w.op] = append(perOp[w.op], executeWarmOperation(w, observe(w.op))...)
	}

	run.WarmP95US = map[string]int64{}
	run.WarmP95USPerOp = map[string]int64{}
	run.WarmP50US = map[string]int64{}
	run.WarmP50USPerOp = map[string]int64{}
	run.WarmSamples = map[string]int{}
	run.WarmOps = map[string][]string{}
	classes := map[string][]time.Duration{}
	classOps := map[string]map[string]struct{}{}
	for op, ds := range perOp {
		if len(ds) == 0 {
			continue
		}
		run.WarmP95USPerOp[op] = p95US(ds)
		run.WarmP50USPerOp[op] = p50US(ds)
		class := queryClassOf[op]
		classes[class] = append(classes[class], ds...)
		if classOps[class] == nil {
			classOps[class] = map[string]struct{}{}
		}
		classOps[class][op] = struct{}{}
	}
	for class, ds := range classes {
		run.WarmP95US[class] = p95US(ds)
		run.WarmP50US[class] = p50US(ds)
		run.WarmSamples[class] = len(ds)
		run.WarmOps[class] = sortedKeys(classOps[class])
	}
	run.QueryLatency = buildQueryLatencySeries(e.Name, plan, symbolSample, perOp, warmupOf)

	stableNames := make([]string, 0, len(stableChecks))
	for op := range stableChecks {
		stableNames = append(stableNames, op)
	}
	sort.Strings(stableNames)
	for _, op := range stableNames {
		run.StableChecks = append(run.StableChecks, *stableChecks[op])
	}
	run.StablePeakRSSMB = peakRSSMB()

	// 4. SW-126's freshness and incremental measurement, LAST because it is the
	// only stage that mutates the checkout: everything above measured the pinned
	// tree exactly as cloned. It runs only when explicitly requested, so the
	// default and PR paths are byte-unchanged.
	//
	// A setup failure is recorded as a run failure rather than silently skipped —
	// a run asked for a hundred changes and produced none must not look like a
	// run that was never asked.
	if incrementalChanges > 0 && e.URL == "" {
		// AC-5, fail closed. A local-path manifest entry is indexed IN PLACE —
		// repoDir is the checked-in fixture directory, not a copy — so applying
		// changes here would rewrite the repository's own pinned fixtures. It
		// would also be the wrong measurement: FR-8's incremental evidence is
		// over a pinned real repository, and the fixture path belongs to the PR
		// gate (cmd/eval/perf.go). Both reasons point the same way, so the
		// harness refuses rather than measuring the cheaper wrong thing.
		run.Failures = append(run.Failures, fmt.Sprintf(
			"incremental: %q is a local-path fixture entry indexed in place; the freshness measurement applies changes to the "+
				"tree and must only run against a cloned pinned repository (the fixture incremental smoke check lives in perf.go)", e.Name))
	} else if incrementalChanges > 0 {
		series, err := measureIncremental(ctx, incrementalSetup{
			repo:     e.Name,
			root:     repoDir,
			store:    store,
			ing:      ing,
			querySvc: eng.Deps.Query,
			searcher: eng.Deps.Search,
			changes:  incrementalChanges,
		})
		if err != nil {
			run.Failures = append(run.Failures, "incremental: "+err.Error())
		} else {
			run.Incremental = series
			// The store now holds this run's own changes, so the DB size stat
			// below is no longer a cold-index sample. Saying so in the artifact is
			// what keeps a mutated run's db_size from being read as one — the
			// db_size gate is measured by the cold series, which never requests
			// changes, and a guard test keeps it that way.
			series.Warnings = append(series.Warnings, fmt.Sprintf(
				"this run applied %d change(s) to the checkout before index.db_size_bytes was measured: that figure is NOT a cold-index DB-size sample for this report",
				len(series.Changes)))
		}
	}

	// 5. DB size after a clean close (WAL checkpointed into the main file).
	if err := store.Close(); err != nil {
		return fail("close store", err)
	}
	fi, err := os.Stat(dbPath)
	if err != nil {
		return fail("stat db", err)
	}
	run.Index.DBSizeBytes = fi.Size()

	run.Pass = len(run.Failures) == 0
	return run
}

func renderedOutcome(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	outcome, ok := strings.CutPrefix(lines[0], "outcome:")
	if !ok {
		return ""
	}
	return outcome
}

func contractOutcome(result *contract.Result, err error) (string, error) {
	if err != nil {
		return "", err
	}
	if err := contract.ValidateResult(result); err != nil {
		return "", fmt.Errorf("invalid contract envelope: %w", err)
	}
	return string(result.Outcome), nil
}

func evaluateConfirmedEdge(ctx context.Context, eng *scenario.FixtureEngine, assertion corpus.ConfirmedEdge) evalreport.SemanticCheck {
	check := evalreport.SemanticCheck{
		Name:        "confirmed:" + assertion.Operation + ":" + assertion.SymbolQuery,
		Requirement: fmt.Sprintf("at least %d confirmed edge(s)", assertion.Min),
	}
	resp, err := eng.Deps.Search.Search(ctx, assertion.SymbolQuery, 25)
	if err != nil {
		check.Observed = "search error: " + err.Error()
		return check
	}
	var anchor string
	for _, match := range resp.Matches {
		bare := match.QualifiedName
		if i := strings.LastIndexByte(bare, '.'); i >= 0 {
			bare = bare[i+1:]
		}
		if bare == assertion.SymbolQuery {
			anchor = match.NodeID
			break
		}
	}
	if anchor == "" {
		check.Observed = fmt.Sprintf("no exact anchor among %d search match(es)", len(resp.Matches))
		return check
	}
	var result query.Result
	switch assertion.Operation {
	case scenario.OpCallers:
		result, err = eng.Deps.Query.Callers(ctx, model.NodeId(anchor))
	case scenario.OpCallees:
		result, err = eng.Deps.Query.Callees(ctx, model.NodeId(anchor))
	case scenario.OpReferences:
		result, err = eng.Deps.Query.References(ctx, model.NodeId(anchor))
	default:
		err = fmt.Errorf("unsupported confirmed-edge operation %q", assertion.Operation)
	}
	if err != nil {
		check.Observed = "query error: " + err.Error()
		return check
	}
	confirmed := 0
	for _, edge := range result.Edges {
		if edge.Tier == "confirmed" {
			confirmed++
		}
	}
	check.Observed = fmt.Sprintf("%d confirmed edge(s) of %d total", confirmed, len(result.Edges))
	check.Pass = confirmed >= assertion.Min
	return check
}

// cloneAt shallow-clones url at ref into dir (fresh every run — a stale cached
// checkout would silently un-pin the corpus, mirroring internal/corpus).
func cloneAt(ctx context.Context, url, ref, dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--branch", ref, "--single-branch", url, dir)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone %s @ %s: %v\n%s", url, ref, err, out)
	}
	return nil
}

func gitHead(ctx context.Context, dir string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// peakRSSMB reads the process's peak resident set via getrusage. Linux
// reports MAXRSS in KiB, darwin in bytes; the eval runners are linux.
func peakRSSMB() int64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	kb := int64(ru.Maxrss)
	if runtime.GOOS == "darwin" {
		kb /= 1024
	}
	return kb / 1024
}

// p95US returns the 95th-percentile latency in microseconds (nearest-rank).
// Microseconds, not milliseconds: the selective-read stable ops are routinely
// sub-millisecond even on real repos, and a 0ms budget cannot ratchet.
//
// SW-124 moved the arithmetic into internal/evalreport so the cold-run p50 and
// this p95 are literally the same nearest-rank implementation. Two percentile
// functions in one harness is one definition too many: they would eventually
// disagree about even sample counts, and the disagreement would show up as an
// unexplainable gate result rather than as a test failure.
func p95US(ds []time.Duration) int64 {
	return percentileUS(ds, 95)
}

// p50US is SW-125's half of the same arithmetic: PRD §12.2 gates on p50 as
// well as p95, and both go through percentileUS so they cannot disagree about
// what nearest rank means for an even sample count.
func p50US(ds []time.Duration) int64 {
	return percentileUS(ds, 50)
}

func percentileUS(ds []time.Duration, p int) int64 {
	us := make([]int64, len(ds))
	for i, d := range ds {
		us[i] = d.Microseconds()
	}
	return evalreport.PercentileInt64(us, p)
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
