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

// Warm-measurement sample caps. They bound the wallclock of the largest repo
// (guava) while keeping enough samples for a meaningful p95; the concrete ops
// and counts are recorded in the report so runs are interpretable.
const (
	fullRunSymbolSample    = 25 // symbols driven through the structural ops
	fullRunSearchIters     = 20 // timed iterations per manifest search query
	fullRunAgentToolSample = 10 // symbols driven through explain/risk/related
	fullRunBriefIters      = 3  // agent_brief assemblies (project-wide scan)
)

const fullRunNotes = "in-process session model: engine services over one open SQLite store; " +
	"cold index timed around IngestAll; index and post-stable-suite peak RSS = getrusage MAXRSS; " +
	"degree-stratified symbol sample; all 12 stable operations covered; warm p95 pooled per op class over the recorded warm_ops/warm_samples"

// runFullRun executes the full measurement for one manifest entry and writes
// the report to outPath. Returns the process exit code.
func runFullRun(manifestPath, repoName, workDir, runnerClass, outPath, budgetPath string) int {
	ctx := context.Background()

	m, err := corpus.LoadManifest(manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: load manifest: %v\n", err)
		return 2
	}
	var entry *corpus.Entry
	for i := range m.Entries {
		if m.Entries[i].Name == repoName {
			entry = &m.Entries[i]
			break
		}
	}
	if entry == nil {
		names := make([]string, 0, len(m.Entries))
		for _, e := range m.Entries {
			names = append(names, e.Name)
		}
		fmt.Fprintf(os.Stderr, "eval: -full-run %q not in manifest (have: %s)\n", repoName, strings.Join(names, ", "))
		return 2
	}

	if workDir == "" {
		workDir, err = os.MkdirTemp("", "graphi-eval-full")
		if err != nil {
			fmt.Fprintf(os.Stderr, "eval: workdir: %v\n", err)
			return 2
		}
		defer os.RemoveAll(workDir)
	}

	run := fullRepoRun(ctx, *entry, filepath.Dir(manifestPath), workDir)
	if budgetPath != "" {
		run.BudgetSource = budgetPath
		if run.Pass {
			checks, err := checkFullRunBudgets(budgetPath, runnerClass, run)
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
		Header:      evalreport.NewHeader("0.0.0-dev", resolveCommit()),
		RunnerClass: runnerClass,
		Notes:       fullRunNotes,
		Repo:        run,
	}
	report.Header.CorpusVersion = manifestVersion(manifestPath)

	if outPath == "" {
		outPath = "eval-full-" + repoName + ".json"
	}
	if err := evalreport.WriteFullRunJSON(report, outPath); err != nil {
		fmt.Fprintf(os.Stderr, "eval: write report: %v\n", err)
		return 2
	}
	fmt.Fprintf(os.Stderr, "eval: wrote full-run report to %s\n", outPath)

	if !run.Pass {
		fmt.Fprintf(os.Stderr, "eval: FAIL - full run over %s: %s\n", repoName, strings.Join(run.Failures, "; "))
		return 1
	}
	fmt.Fprintf(os.Stderr, "eval: PASS - full run over %s (index %dms, rss %dMB, db %dB)\n",
		repoName, run.Index.WallclockMS, run.Index.PeakRSSMB, run.Index.DBSizeBytes)
	return 0
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

// fullRepoRun performs clone → index → warm measurement for one entry. Every
// failure is recorded in the returned run; fatal stages stop the pipeline.
func fullRepoRun(ctx context.Context, e corpus.Entry, manifestDir, workDir string) evalreport.FullRepoRun {
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
	store, err := graphstore.OpenSQLite(dbPath)
	if err != nil {
		return fail("open store", err)
	}
	defer store.Close()
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), filepath.Join(workDir, e.Name+"-meta"))
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

	// 3. Warm per-op-class p95 over the same open store, driven through the
	// same FixtureEngine the hero suite uses.
	eng := scenario.NewFixtureEngine(resolve.Deps{Query: query.New(store), Search: search.New(store)})
	eng.RepoRoot = repoDir
	eng.ProjectName = e.Name
	sampler, ok := any(store).(graphstore.DegreeSamplePort)
	if !ok {
		return fail("sample", fmt.Errorf("DegreeSamplePort unavailable"))
	}
	sampleNodes, err := sampler.DegreeStratifiedSymbols(ctx, fullRunSymbolSample)
	if err != nil {
		return fail("sample", err)
	}
	if len(sampleNodes) == 0 {
		return fail("sample", fmt.Errorf("index produced no function/method symbols to measure"))
	}

	perOp := map[string][]time.Duration{}
	opClass := map[string]string{}
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
	timeOp := func(class, op, requirement string, allowed []string, invoke func() (string, error)) {
		start := time.Now()
		outcome, err := invoke()
		d := time.Since(start)
		if err != nil {
			check := checkFor(op, requirement)
			check.Samples++
			check.Outcomes["error"]++
			if check.Pass {
				run.Failures = append(run.Failures, fmt.Sprintf("warm %s: %v", op, err))
			}
			check.Pass = false
			return
		}
		if !recordOutcome(op, requirement, outcome, allowed...) {
			return
		}
		perOp[op] = append(perOp[op], d)
		opClass[op] = class
	}

	indexOutcome := "empty"
	if run.Index.Nodes > 0 && run.Index.Files > 0 {
		indexOutcome = "found"
	}
	recordOutcome(scenario.OpIndex, "successful ingest with non-empty node and file inventory", indexOutcome, "found")

	for _, sampled := range sampleNodes {
		ref := string(sampled.ID())
		for _, op := range []string{scenario.OpDefinition, scenario.OpCallers, scenario.OpCallees, scenario.OpReferences} {
			op := op
			allowed := []string{"found", "empty"}
			requirement := "resolved symbol; found or legitimately empty"
			if op == scenario.OpDefinition {
				allowed = []string{"found"}
				requirement = "resolved symbol with at least one definition"
			}
			timeOp("structural", op, requirement, allowed, func() (string, error) {
				lines, _, err := eng.Invoke(op, map[string]string{"symbol": ref})
				return renderedOutcome(lines), err
			})
		}
		timeOp("structural", scenario.OpNeighborhood, "resolved symbol; bounded neighborhood outcome", []string{"found", "empty"}, func() (string, error) {
			lines, _, err := eng.Invoke(scenario.OpNeighborhood, map[string]string{"symbol": ref, "depth": "1"})
			return renderedOutcome(lines), err
		})
		timeOp("structural", scenario.OpImpact, "resolved symbol; bounded impact outcome", []string{"found", "empty"}, func() (string, error) {
			lines, _, err := eng.Invoke(scenario.OpImpact, map[string]string{"symbol": ref, "direction": "reverse", "max_nodes": "256"})
			return renderedOutcome(lines), err
		})
	}

	for _, s := range e.Searches {
		q := s.Query
		// Untimed assertion pass first (fail-closed on the manifest promise),
		// then pure timed iterations so the p95 measures nothing but the op.
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
		for range fullRunSearchIters {
			allowed := []string{"found", "empty"}
			requirement := "valid search outcome"
			if s.ExpectNonEmpty {
				allowed = []string{"found"}
				requirement = "manifest-promised non-empty search"
			}
			timeOp("search", scenario.OpSearch, requirement, allowed, func() (string, error) {
				lines, _, err := eng.Invoke(scenario.OpSearch, map[string]string{"query": q})
				return renderedOutcome(lines), err
			})
		}
	}

	for _, assertion := range e.ConfirmedEdges {
		check := evaluateConfirmedEdge(ctx, eng, assertion)
		run.SemanticChecks = append(run.SemanticChecks, check)
		if !check.Pass {
			run.Failures = append(run.Failures, fmt.Sprintf("semantic %s: %s (observed %s)", check.Name, check.Requirement, check.Observed))
		}
	}

	agentSample := sampleNodes
	if len(agentSample) > fullRunAgentToolSample {
		agentSample = agentSample[:fullRunAgentToolSample]
	}
	for _, sampled := range agentSample {
		ref := string(sampled.ID())
		for _, op := range []string{scenario.OpExplainSymbol, scenario.OpChangeRisk, scenario.OpRelatedFiles} {
			op := op
			allowed := []string{"found"}
			requirement := "resolved target with a valid found envelope"
			if op == scenario.OpRelatedFiles {
				allowed = []string{"found", "empty"}
				requirement = "resolved target; found or legitimately empty related-file set"
			}
			timeOp("agent_tools", op, requirement, allowed, func() (string, error) {
				result, err := eng.InvokeContract(ctx, op, map[string]string{"symbol": ref})
				return contractOutcome(result, err)
			})
		}
	}
	for i := 0; i < fullRunBriefIters; i++ {
		topic := string(sampleNodes[i%len(sampleNodes)].ID())
		timeOp("agent_tools", scenario.OpAgentBrief, "resolved topic with a valid found/partial project brief", []string{"found", "partial"}, func() (string, error) {
			result, err := eng.InvokeContract(ctx, scenario.OpAgentBrief, map[string]string{"topic": topic})
			return contractOutcome(result, err)
		})
	}

	run.WarmP95US = map[string]int64{}
	run.WarmP95USPerOp = map[string]int64{}
	run.WarmSamples = map[string]int{}
	run.WarmOps = map[string][]string{}
	classes := map[string][]time.Duration{}
	classOps := map[string]map[string]struct{}{}
	for op, ds := range perOp {
		run.WarmP95USPerOp[op] = p95US(ds)
		class := opClass[op]
		classes[class] = append(classes[class], ds...)
		if classOps[class] == nil {
			classOps[class] = map[string]struct{}{}
		}
		classOps[class][op] = struct{}{}
	}
	for class, ds := range classes {
		run.WarmP95US[class] = p95US(ds)
		run.WarmSamples[class] = len(ds)
		run.WarmOps[class] = sortedKeys(classOps[class])
	}
	stableNames := make([]string, 0, len(stableChecks))
	for op := range stableChecks {
		stableNames = append(stableNames, op)
	}
	sort.Strings(stableNames)
	for _, op := range stableNames {
		run.StableChecks = append(run.StableChecks, *stableChecks[op])
	}
	run.StablePeakRSSMB = peakRSSMB()

	// 4. DB size after a clean close (WAL checkpointed into the main file).
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
func p95US(ds []time.Duration) int64 {
	if len(ds) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(ds))
	copy(sorted, ds)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := (95*len(sorted) + 99) / 100 // ceil(0.95n), 1-based
	return sorted[rank-1].Microseconds()
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
