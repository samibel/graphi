// Command retrieval-eval runs the retrieval-quality evaluation harness
// (internal/eval/retrieval, story SW-258) over one repository and one graded
// dataset, through the real engine seams, and writes the versioned JSON
// report. Following cmd/eval's discipline, `-export-raw` writes the raw
// samples beside the report and `-aggregate <dir>` recomputes every
// published number from them, exiting non-zero on a discrepancy.
//
// The PR-time run is `go test ./internal/eval/retrieval` over the checked-in
// fixture; this binary is the dispatch for the pinned public repositories.
// It never clones: a pinned repository is read from a local checkout that
// must already sit at the pinned sha.
//
// Embedder selector grammar (`-embedder` / `GRAPHI_EMBEDDER`).
//
// Three accepted forms (AC-3, SW-269):
//
//   - omit `-embedder` (or pass empty) — the intentional lexical-only
//     run; the report's `reproducible.embedder_spec` carries
//     `lexical_only: true` and the run exits 0 with the semantic
//     baselines reported as unavailable with the typed reason.
//   - `ollama:host:port` — loopback-only Ollama endpoint
//     (`127.0.0.1:11434` is the loopback default; a non-loopback host
//     is REFUSED at construction, so the harness never dials off-box).
//     The default model (`nomic-embed-text`) is the loopback server's
//     choice, NOT a selector segment — what comes after the first
//     colon is the endpoint, never a model name.
//   - `static:<model>@<revision>` — the production static embedder
//     (SW-262). The fixed `<model>` is `potion-code-16M-v2` and the
//     fixed `<revision>` is the SHA-pinned commit digest (see
//     engine/embed/static/pins.go); any other value is REFUSED.
//   - `onnx:<model>` — build-tag-gated; ONLY present under
//     `//go:build embed_onnx`, so the CGo-free default build has no
//     constructor for it.
//
// A selector that does not match one of those three forms is
// CONSTRUCTION FAILURE: the harness exits 1, names the selector and the
// reason, and writes no publishable report. An empty `-embedder` is
// the ONLY way to opt into unavailable semantic baselines — there is
// no other silent downgrade path. (SW-263 reviewer ruling: the help
// text previously advertised an invalid form, and a non-empty selector
// that failed to construct was silently downgraded to an unavailable
// semantic service and the run exited zero; that failure shape is
// removed in SW-269, see story at projects/graphi/stories/SW-269.)
//
// Usage:
//
//	go run ./cmd/retrieval-eval -manifest corpus/manifest.json -repo <name> -dataset <path> -out <report.json> \
//	    [-baseline <name>]... [-export-raw <dir>] [-runner-class local] [-checkout <dir>] [-embedder <selector>]
//	go run ./cmd/retrieval-eval -field-parity -manifest corpus/manifest.json -repo <name> -dataset <path> -out <report.json> \
//	    -export-raw <dir> -checkout <dir> -embedder <selector>
//	go run ./cmd/retrieval-eval -aggregate <dir>
//	go run ./cmd/retrieval-eval -check-claim '<candidate sentence>'
//	go run ./cmd/retrieval-eval -derive -targets-report <report.json> -budget-small <report.json> \
//	    [-budget-medium <report.json>] [-budget-large <report.json>] -targets-out <path> -budgets-out <path>
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	// Importing the ollama package registers its loopback scheme into
	// embed.DefaultConstructors via init(); the harness never constructs
	// an embedder itself — the runner does, after resolving the
	// -embedder selector. Importing for side effects only; the blank
	// identifier silences the unused-import lint.
	_ "github.com/samibel/graphi/engine/embed/ollama"
	// Importing the static package registers the SW-262
	// `static:<model>@<revision>` scheme (engine/embed/static's init).
	// Without it, the production static embedder is unreachable from this
	// harness even when its artifact is installed and verified.
	_ "github.com/samibel/graphi/engine/embed/static"
	"github.com/samibel/graphi/internal/corpus"
	"github.com/samibel/graphi/internal/eval/retrieval"
)

// FixtureRepoName is the built-in repository name for the hermetic fixture.
const FixtureRepoName = "fixture"

// fixtureRepoPath is the fixture's location relative to the repository root.
const fixtureRepoPath = "internal/eval/retrieval/testdata/fixture-repo"

// Exit codes for the run and derive modes; the aggregate mode uses
// retrieval.Exit*.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// multiFlag collects a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("retrieval-eval", flag.ContinueOnError)
	fs.SetOutput(stderr)

	manifest := fs.String("manifest", "corpus/manifest.json", "corpus manifest the repository is pinned in")
	repo := fs.String("repo", "", "manifest entry to measure, or \""+FixtureRepoName+"\" for the checked-in fixture repo")
	checkout := fs.String("checkout", "", "local checkout of a URL-pinned manifest entry (default $HOME/.cache/graphi/corpus/<repo>); it must already be at the pinned sha — this tool never clones")
	dataset := fs.String("dataset", "", "graded dataset JSON (internal/eval/retrieval/testdata/datasets/<repo>-v1.json)")
	out := fs.String("out", "", "write the JSON report here (run mode); write aggregate.json here (aggregate mode; default <dir>/aggregate.json)")
	var baselines multiFlag
	fs.Var(&baselines, "baseline", "baseline to run (repeatable); default all of "+strings.Join(baselineNames(), ", "))
	exportRaw := fs.String("export-raw", "", "after the run, write the raw-sample run directory here (report, dataset copy, raw/hits-*.json, raw/latency-*.json)")
	fieldParity := fs.Bool("field-parity", false, "SW-272 evaluator-only control: select every and only dev/nl_behaviour query at exact grade 3 and run the fixed six-cell field-parity baseline set; cannot be combined with -baseline")
	aggregate := fs.String("aggregate", "", "recompute every published metric in this run directory from its raw samples; exit 0 reproduced, 1 discrepancy, 2 unreadable, 3 incomplete")
	checkClaim := fs.String("check-claim", "", "validate a candidate savings sentence against the frozen scope contract; this checks wording only and never generates or publishes a claim")
	runnerClass := fs.String("runner-class", "local", "machine class stamped into the report")
	repeats := fs.Int("repeats", retrieval.DefaultRepeats, "timed executions per query and baseline")
	date := fs.String("date", "", "date stamped into the run directory and derived files (default today, UTC)")
	embedder := fs.String("embedder", "", "embedder selector — one of: empty (lexical-only by intent; fusion/semantic baselines report unavailable with the typed reason), `ollama:host:port` (loopback only), `static:<model>@<revision>` (production), or `onnx:<model>` (under //go:build embed_onnx). A non-empty selector that fails to construct, register, generate, reload or serve causes exit 1 and NO publishable report")

	derive := fs.Bool("derive", false, "derive docs/eval/retrieval-targets.json and -budgets.json from finished reports")
	targetsReport := fs.String("targets-report", "", "derive mode: the report the targets are taken from")
	budgetReports := map[string]*string{
		retrieval.FixtureSmall:  fs.String("budget-small", "", "derive mode: the report measured over the small fixture class"),
		retrieval.FixtureMedium: fs.String("budget-medium", "", "derive mode: the report measured over the medium fixture class"),
		retrieval.FixtureLarge:  fs.String("budget-large", "", "derive mode: the report measured over the large fixture class"),
	}
	targetsOut := fs.String("targets-out", "", "derive mode: write the targets file here")
	budgetsOut := fs.String("budgets-out", "", "derive mode: write the budgets file here")

	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *date == "" {
		*date = time.Now().UTC().Format("2006-01-02")
	}

	switch {
	case *checkClaim != "":
		if err := retrieval.CheckClaimSentence(*checkClaim); err != nil {
			fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
			return exitError
		}
		fmt.Fprintf(stderr, "retrieval-eval: claim accepted by %s\n", retrieval.MeasurementContractVersion)
		return exitOK
	case *aggregate != "":
		return runAggregate(*aggregate, *out, stderr)
	case *derive:
		var budgets []budgetReport
		for _, class := range retrieval.FixtureClasses {
			if p := *budgetReports[class]; p != "" {
				budgets = append(budgets, budgetReport{class: class, path: p})
			}
		}
		return runDerive(*targetsReport, budgets, *targetsOut, *budgetsOut, *date, stderr)
	}

	if *repo == "" || *dataset == "" || *out == "" {
		fmt.Fprintln(stderr, "retrieval-eval: -repo, -dataset and -out are required (or use -aggregate <dir> / -derive)")
		return exitUsage
	}
	if *fieldParity && len(baselines) > 0 {
		fmt.Fprintln(stderr, "retrieval-eval: -field-parity selects its fixed six-baseline closed world and cannot be combined with -baseline")
		return exitUsage
	}
	var names []retrieval.Baseline
	var err error
	if *fieldParity {
		names = append([]retrieval.Baseline(nil), retrieval.FieldParityBaselines...)
	} else {
		names, err = retrieval.ParseBaselines(baselines)
	}
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitUsage
	}

	ds, err := retrieval.LoadDataset(*dataset)
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitUsage
	}
	if ds.Dataset.Repo != *repo {
		fmt.Fprintf(stderr, "retrieval-eval: dataset %s is judged against repo %q, not %q\n", *dataset, ds.Dataset.Repo, *repo)
		return exitUsage
	}
	if *fieldParity {
		ds, err = retrieval.SelectFieldParityDevNLBehaviour(ds)
		if err != nil {
			fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
			return exitUsage
		}
	}

	ctx := context.Background()
	root, sha, err := resolveRepo(ctx, *manifest, *repo, *checkout)
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitUsage
	}
	if ds.Dataset.RepoSHA != "" && !strings.EqualFold(ds.Dataset.RepoSHA, sha) {
		fmt.Fprintf(stderr, "retrieval-eval: dataset %s cites sha %s but the checkout is at %s\n", *dataset, ds.Dataset.RepoSHA, sha)
		return exitUsage
	}

	res, err := retrieval.Run(ctx, retrieval.Options{
		RepoRoot: root, RepoName: *repo, RepoSHA: sha,
		Dataset: ds, Baselines: names,
		RunnerClass: *runnerClass, CandidateSHA: resolveCommit(),
		EmbedderSelector: *embedder,
		Repeats:          *repeats, Log: stderr,
	})
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	raw, err := retrieval.MarshalReport(res.Report)
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	if err := os.WriteFile(*out, raw, 0o644); err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: write report: %v\n", err)
		return exitError
	}
	fmt.Fprintf(stderr, "retrieval-eval: wrote %s\n", *out)
	if *exportRaw != "" {
		if _, err := retrieval.WriteRunDir(*exportRaw, res, ds, *date); err != nil {
			fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
			return exitError
		}
		fmt.Fprintf(stderr, "retrieval-eval: wrote the raw-sample run directory to %s\n", *exportRaw)
	}
	printSummary(stderr, res.Report)
	return exitOK
}

func baselineNames() []string {
	out := make([]string, 0, len(retrieval.AllBaselines))
	for _, b := range retrieval.AllBaselines {
		out = append(out, string(b))
	}
	return out
}

// resolveRepo locates the checkout to index and the sha the report cites.
// The built-in fixture has no sha (its pin is the tree). A manifest entry
// with a local Path is anchored at the repository root, as cmd/eval does; a
// URL entry must already be checked out locally AT the pinned sha — a
// missing or mis-pinned checkout fails closed, and nothing is cloned.
func resolveRepo(ctx context.Context, manifestPath, name, checkout string) (root, sha string, err error) {
	if name == FixtureRepoName {
		return filepath.FromSlash(fixtureRepoPath), "", nil
	}
	m, err := corpus.LoadManifest(manifestPath)
	if err != nil {
		return "", "", err
	}
	var entry *corpus.Entry
	for i := range m.Entries {
		if m.Entries[i].Name == name {
			entry = &m.Entries[i]
			break
		}
	}
	if entry == nil {
		return "", "", fmt.Errorf("-repo %q is not in %s (and is not %q)", name, manifestPath, FixtureRepoName)
	}
	if entry.Path != "" {
		return filepath.Join(filepath.Dir(filepath.Dir(manifestPath)), filepath.FromSlash(entry.Path)), "", nil
	}
	if entry.URL == "" {
		return "", "", fmt.Errorf("-repo %q has neither a path nor a url in the manifest", name)
	}
	if entry.SHA == "" {
		return "", "", fmt.Errorf("-repo %q has no pinned sha in the manifest; a retrieval dataset needs a pin to cite", name)
	}
	if checkout == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", fmt.Errorf("no -checkout and no home directory: %w", err)
		}
		checkout = filepath.Join(home, ".cache", "graphi", "corpus", name)
	}
	if fi, err := os.Stat(checkout); err != nil || !fi.IsDir() {
		return "", "", fmt.Errorf("checkout of %s not found at %s; clone %s at %s there (this tool never clones)", name, checkout, entry.URL, entry.Ref)
	}
	head, err := retrieval.CheckoutHEAD(ctx, checkout)
	if err != nil {
		return "", "", err
	}
	if !strings.EqualFold(head, entry.SHA) && !strings.HasPrefix(strings.ToLower(head), strings.ToLower(entry.SHA)) {
		return "", "", fmt.Errorf("checkout %s is at %s, not the pinned %s (%s)", checkout, head, entry.SHA, entry.Ref)
	}
	return checkout, head, nil
}

// resolveCommit records the exact Git revision and marks a dirty worktree so
// a locally generated report cannot be presented as evidence for the clean
// commit it started from (cmd/eval's rule).
func resolveCommit() string {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	commit := strings.TrimSpace(string(out))
	if status, err := exec.Command("git", "status", "--porcelain", "--untracked-files=normal").Output(); err == nil && len(status) > 0 {
		commit += "+dirty"
	}
	return commit
}

func printSummary(w io.Writer, r *retrieval.Report) {
	rep := r.Reproducible
	fmt.Fprintf(w, "retrieval-eval: %s @ %s, dataset %s (%d queries), candidate %s, runner %s\n",
		rep.Repo.Name, orUnknown(rep.Repo.SHA), rep.Dataset.ID, rep.Dataset.Queries, rep.CandidateSHA, rep.RunnerClass)
	for _, b := range rep.Baselines {
		if b.Status != retrieval.BaselineStatusOK {
			fmt.Fprintf(w, "retrieval-eval:   %-20s %s: %s\n", b.Name, b.Status, b.Reason)
			continue
		}
		m := b.Overall.Metrics
		fmt.Fprintf(w, "retrieval-eval:   %-20s top1 %.3f  recall@5 %.3f  recall@10 %.3f  mrr@10 %.3f  ndcg@10 %.3f  (%d/%d scored)\n",
			b.Name, m[retrieval.MetricTop1], m[retrieval.MetricRecall5], m[retrieval.MetricRecall10], m[retrieval.MetricMRR10], m[retrieval.MetricNDCG10],
			b.Overall.Scored, b.Overall.Queries)
	}
	for _, p := range r.Performance {
		fmt.Fprintf(w, "retrieval-eval:   %-20s index %s  p50 %s  p95 %s  rss %s\n", p.Baseline,
			measure(p.IndexMS), measure(p.QueryP50US), measure(p.QueryP95US), measure(p.PeakRSSMB))
	}
}

func measure(m retrieval.Measure) string {
	if m.Status != retrieval.StatusMeasured || m.Value == nil {
		return m.Status
	}
	return fmt.Sprintf("%g%s", *m.Value, m.Unit)
}

func orUnknown(s string) string {
	if s == "" {
		return "(in-tree fixture)"
	}
	return s
}
