package main

// SW-123 (EVAL-02): the full-run measurement harness. One invocation measures
// ONE pinned corpus repository end-to-end — clone (fail-closed SHA pin) →
// cold full index (wallclock, peak RSS, DB size) → warm per-op-class p95 over
// the same in-process session — and emits the raw internal/evalreport JSON
// the ADR 0003 U5 budgets are frozen from.
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
	"cold index timed around IngestAll; peak RSS = getrusage MAXRSS sampled after the index pass; " +
	"warm p95 pooled per op class over the recorded warm_ops/warm_samples"

// runFullRun executes the full measurement for one manifest entry and writes
// the report to outPath. Returns the process exit code.
func runFullRun(manifestPath, repoName, workDir, runnerClass, outPath string) int {
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
	// session backend), timed; peak RSS sampled immediately afterwards.
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

	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return fail("nodes", err)
	}
	edges, err := store.Edges(ctx, graphstore.Query{})
	if err != nil {
		return fail("edges", err)
	}
	run.Index.Nodes, run.Index.Edges = len(nodes), len(edges)
	files := map[string]struct{}{}
	for _, n := range nodes {
		if p := n.SourcePath(); p != "" {
			files[p] = struct{}{}
		}
	}
	run.Index.Files = len(files)

	// 3. Warm per-op-class p95 over the same open store, driven through the
	// same FixtureEngine the hero suite uses.
	eng := scenario.NewFixtureEngine(resolve.Deps{Query: query.New(store), Search: search.New(store)})
	sample := sampleSymbols(nodes, fullRunSymbolSample)
	if len(sample) == 0 {
		return fail("sample", fmt.Errorf("index produced no function/method symbols to measure"))
	}

	perOp := map[string][]time.Duration{}
	opClass := map[string]string{}
	timeOp := func(class, op string, invoke func() error) {
		start := time.Now()
		err := invoke()
		d := time.Since(start)
		if err != nil {
			run.Failures = append(run.Failures, fmt.Sprintf("warm %s: %v", op, err))
			return
		}
		perOp[op] = append(perOp[op], d)
		opClass[op] = class
	}

	for _, id := range sample {
		ref := string(id)
		for _, op := range []string{scenario.OpDefinition, scenario.OpCallers, scenario.OpCallees, scenario.OpReferences} {
			op := op
			timeOp("structural", op, func() error { _, _, err := eng.Invoke(op, map[string]string{"symbol": ref}); return err })
		}
		timeOp("structural", scenario.OpNeighborhood, func() error {
			_, _, err := eng.Invoke(scenario.OpNeighborhood, map[string]string{"symbol": ref, "depth": "1"})
			return err
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
			timeOp("search", scenario.OpSearch, func() error {
				_, _, err := eng.Invoke(scenario.OpSearch, map[string]string{"query": q})
				return err
			})
		}
	}

	agentSample := sample
	if len(agentSample) > fullRunAgentToolSample {
		agentSample = agentSample[:fullRunAgentToolSample]
	}
	for _, id := range agentSample {
		ref := string(id)
		for _, op := range []string{scenario.OpExplainSymbol, scenario.OpChangeRisk, scenario.OpRelatedFiles} {
			op := op
			timeOp("agent_tools", op, func() error { _, err := eng.InvokeContract(ctx, op, map[string]string{"symbol": ref}); return err })
		}
	}
	for i := 0; i < fullRunBriefIters; i++ {
		timeOp("agent_tools", scenario.OpAgentBrief, func() error {
			_, err := eng.InvokeContract(ctx, scenario.OpAgentBrief, map[string]string{"topic": e.Name})
			return err
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

// sampleSymbols returns up to n function/method node ids in canonical NodeId
// order (nodes arrive canonically ordered from the store), so the sample is
// deterministic per index state.
func sampleSymbols(nodes []model.Node, n int) []model.NodeId {
	out := make([]model.NodeId, 0, n)
	for _, node := range nodes {
		switch node.Kind() {
		case "function", "method":
			out = append(out, node.ID())
			if len(out) == n {
				return out
			}
		}
	}
	return out
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
