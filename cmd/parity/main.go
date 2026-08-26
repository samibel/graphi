// Command parity runs the real-repository full-vs-incremental parity matrix
// (internal/parity): it builds (or accepts) a graphi binary, applies every
// declared change class as a real edit to real source in a pinned clone, and
// asserts snapshot-byte equality between the incrementally-updated graph and a
// fresh full index of the same final tree.
//
// Usage:
//
//	go run ./cmd/parity -manifest corpus/manifest.json -max-tier 1 -report parity.json
//	go run ./cmd/parity -max-tier 1 -classes move_symbol      # iterate one row
//	go run ./cmd/parity -runner-class ubuntu-latest -report parity.json
//
// Two reports from two dispatches are compared with:
//
//	go run ./cmd/parity -verdict-diff a.json,b.json   # verdicts agree
//	go run ./cmd/parity -counts-diff a.json,b.json    # per-row counts + snapshot digests agree (the determinism gate)
//	go run ./cmd/parity -refusal-diff a.json,b.json   # the two runs REFUSE for the same reasons
//
// -refusal-diff is the only one of the three that says anything when both runs
// are unpublishable, which is the state the JVM matrix is in. Its exit 0 means
// "the refusal is deterministic" and NEVER "the run is publishable" — the two
// gates above keep sole ownership of that word.
//
// Exit codes mirror cmd/corpus: 0 = every executed row passed, 1 = at least one
// row FAILED (which is legitimate published evidence, not a harness fault),
// 2 = harness/internal error or an incomplete run.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/samibel/graphi/internal/corpus"
	"github.com/samibel/graphi/internal/parity"
	"github.com/samibel/graphi/internal/parityreport"
)

func main() { os.Exit(run()) }

func run() int {
	manifest := flag.String("manifest", "corpus/manifest.json", "corpus manifest path")
	family := flag.String("family", "go",
		"language family to run: \"go\" (the PRD FR-7 matrix over the Go pins), \"jvm\" "+
			"(WP-J7: the JVM change classes over the guava / okio / kotlinx.serialization pins, "+
			"crossed over binder{off,on} x profile{resolved default, fast}), or one of "+
			strings.Join(parity.ParseDetLanguages(), " / ")+" (W5.n: that language's real-repository "+
			"PARSE-DETERMINISM matrix, crossed over store{sqlite} x profile{resolved default, fast})")
	classesPath := flag.String("classes-file", "", "declared change-class matrix (default: the family's own table)")
	binary := flag.String("binary", "", "graphi binary to drive (default: build ./cmd/graphi into the workdir)")
	workdir := flag.String("workdir", "", "work dir for clones, stores and snapshots (default: a temp dir)")
	report := flag.String("report", "", "when set, write the machine-readable JSON report here")
	maxTier := flag.Int("max-tier", parity.MaxSupportedTier,
		"run only repositories with tier <= this value (hard ceiling 3; tier 4 is SW-145's named-machine stress target and is excluded by construction)")
	classes := flag.String("classes", "", "comma-separated class ids to run (default: all); a filtered run is never publishable")
	runnerClass := flag.String("runner-class", "", "machine class this run happened on (e.g. ubuntu-latest); required to publish")
	timeout := flag.Duration("class-timeout", 15*time.Minute, "per-class timeout")
	lifecycleRepeat := flag.Int("lifecycle-repeat", parity.DefaultLifecycleRepeat,
		"how many times each lifecycle journey (branch switch, interrupted full pass, restart and recovery) "+
			"runs per kill point; every repetition is published, and a row FAILS if any of them diverged — "+
			"this can never retry a row into green")
	verdictDiff := flag.String("verdict-diff", "", "compare the verdict sets of two reports (a.json,b.json) and exit non-zero if they differ")
	countsDiff := flag.String("counts-diff", "", "compare per-row node/edge counts and snapshot digests of two reports (a.json,b.json) and exit non-zero if they differ — the Wave-0 determinism gate that -verdict-diff is structurally blind to")
	refusalDiff := flag.String("refusal-diff", "", "compare the not_publishable_because REASON SETS of two reports (a.json,b.json) and print the shared list — exit 0 means the refusal is DETERMINISTIC, never that the run is publishable")
	allowLocal := flag.Bool("allow-local", false,
		"admit manifest entries that point at a LOCAL PATH instead of a URL clone (dispatch-only; "+
			"the hermetic tests open this door, the production runner keeps it shut — using it on a PR-gate run would let a row silently fall back to a fixture)")
	flag.Parse()

	if *verdictDiff != "" {
		return compareVerdictSets(*verdictDiff)
	}
	if *countsDiff != "" {
		return compareCountsSets(*countsDiff)
	}
	if *refusalDiff != "" {
		return compareRefusalSets(*refusalDiff)
	}

	// The family decides which declared class table is authoritative. Defaulting
	// -classes-file per family (rather than to the Go table for both) is what
	// stops a `-family jvm` run from silently measuring the Go row set and
	// publishing it under a JVM heading.
	switch {
	case *family == "go" || *family == "jvm":
	case parity.IsParseDetLanguage(*family):
	default:
		fmt.Fprintf(os.Stderr, "parity: -family must be \"go\", \"jvm\" or one of %s, got %q\n",
			strings.Join(parity.ParseDetLanguages(), " / "), *family)
		return 2
	}
	if *classesPath == "" {
		switch {
		case *family == "jvm":
			*classesPath = parity.ClassesPathJVM
		case parity.IsParseDetLanguage(*family):
			*classesPath = parity.ParseDetClassesPath(*family)
		default:
			*classesPath = parity.ClassesPath
		}
	}

	rows, err := parity.LoadClasses(*classesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parity: %v\n", err)
		return 2
	}
	m, err := corpus.LoadManifest(*manifest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parity: %v\n", err)
		return 2
	}

	wd := *workdir
	if wd == "" {
		td, terr := os.MkdirTemp("", "graphi-parity-*")
		if terr != nil {
			fmt.Fprintf(os.Stderr, "parity: temp workdir: %v\n", terr)
			return 2
		}
		defer os.RemoveAll(td)
		wd = td
	}

	bin := *binary
	if bin == "" {
		bin = filepath.Join(wd, exeName("graphi"))
		build := exec.Command("go", "build", "-o", bin, "./cmd/graphi")
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, berr := build.CombinedOutput(); berr != nil {
			fmt.Fprintf(os.Stderr, "parity: build graphi: %v\n%s\n", berr, out)
			return 2
		}
	}

	ctx := context.Background()
	prov := parity.CollectProvenance(ctx, ".")

	r := &parity.Runner{
		Binary:          bin,
		WorkDir:         wd,
		MaxTier:         *maxTier,
		Classes:         splitList(*classes),
		PerClassTimeout: *timeout,
		RunnerClass:     *runnerClass,
		LifecycleRepeat: *lifecycleRepeat,
		AllowLocal:      *allowLocal,
		Log: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		},
	}
	run := r.Run
	switch {
	case *family == "jvm":
		run = r.RunJVM
	case parity.IsParseDetLanguage(*family):
		r.ParseDetLang = *family
		run = r.RunParseDeterminism
	}
	rep, err := run(ctx, m, rows, prov)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parity: %v\n", err)
		return 2
	}

	if *report != "" {
		if err := parityreport.Write(rep, *report); err != nil {
			fmt.Fprintf(os.Stderr, "parity: %v\n", err)
			return 2
		}
	}
	return printAndScore(rep)
}

func printAndScore(rep parityreport.Report) int {
	fmt.Printf("\nparity matrix — %s\n", rep.Provenance.Statement)
	if rep.Family != "" {
		fmt.Printf("  family        %s (%s)\n", rep.Family, rep.MatrixSource)
	}
	fmt.Printf("  run sha       %s\n", rep.Provenance.RunSHA)
	fmt.Printf("  runner class  %s\n", orNone(rep.Provenance.RunnerClass))
	fmt.Printf("  max tier      %d\n\n", rep.MaxTier)
	for _, c := range rep.Classes {
		where := c.Repo
		if where == "" {
			where = "-"
		}
		width := 24
		switch {
		case rep.Family == parityreport.FamilyJVM:
			width = 52 // the axis suffix is part of the id
		case strings.HasPrefix(rep.Family, "parsedet-"):
			width = 62 // <lang>_reparse_identical_bytes[store=…,profile=…]
		}
		fmt.Printf("  %-*s %-9s %-10s %s\n", width, c.ID, c.Verdict, where, firstLine(c.Detail))
	}
	fmt.Println()
	// The lifecycle rows print their WHOLE SAMPLE, not a summary of it. A row
	// whose repetitions disagree is the finding; collapsing them to one line is
	// how that finding would be lost.
	for _, c := range rep.Classes {
		if len(c.Repetitions) == 0 {
			continue
		}
		fmt.Printf("  %s — %d repetition(s), %d distinct incremental snapshot(s), %d distinct full snapshot(s), reproducible=%v\n",
			c.ID, len(c.Repetitions), c.DistinctIncDigests, c.DistinctFullDigests, c.Reproducible)
		if c.RefA != "" || c.RefB != "" {
			fmt.Printf("      refs        %s  ->  %s\n", c.RefA, c.RefB)
		}
		if c.KillPoint != "" {
			fmt.Printf("      kill point  %s\n", firstLine(c.KillPoint))
		}
		for _, rp := range c.Repetitions {
			eq := "DIFFER"
			if rp.Equal {
				eq = "equal "
			}
			crashed := fmt.Sprintf("crashed-store %d/%d", rp.CrashedNodes, rp.CrashedEdges)
			if rp.CrashedStoreNote != "" {
				crashed = rp.CrashedStoreNote
			}
			fmt.Printf("      #%-2d %-12s %-9s %s inc %d/%d full %d/%d  lock %s->%s  %s  %s\n",
				rp.N, rp.KillPointID, rp.ADRKillPoint, eq, rp.IncNodes, rp.IncEdges,
				rp.FullNodes, rp.FullEdges, orDash(rp.LockDuringPass), orDash(rp.LockAfterKill),
				crashed, firstLine(rp.Detail))
		}
		if c.Control != "" {
			fmt.Printf("      control     %s\n", firstLine(c.Control))
		}
	}
	if len(rep.Classes) > 0 {
		fmt.Println()
	}
	for _, cl := range rep.CoverageLimits {
		fmt.Printf("  COVERAGE LIMIT  %-24s %s: %s\n", cl.Row, cl.Platform, cl.Reason)
	}
	for _, sc := range rep.StoreCounts {
		status := "PASS"
		if !sc.Pass {
			status = "FAIL"
		}
		fmt.Printf("  §12.3 counts  %-10s %-24s %-11s orphaned external nodes=%d stale linker edges=%d  %s\n",
			sc.Repo, sc.Class, sc.Side, sc.OrphanedExternalNodes, sc.StaleLinkerEdges, status)
	}
	printCompileCoveragePolicy(rep)
	fmt.Printf("\n  outcome     %s\n  complete    %v\n  publishable %v\n", rep.Outcome, rep.Complete, rep.Publishable)
	for _, why := range rep.NotPublishableBecause {
		fmt.Printf("    refused: %s\n", why)
	}
	fmt.Printf("\n  NOT compared (snapshot blind spot, docs/adr/0004-ingest-recovery-disposition.md:37):\n    %s\n",
		strings.Join(rep.NotCompared.Items, ", "))
	fmt.Printf("\n  %s\n", rep.GateNote)

	switch rep.Outcome {
	case parityreport.OutcomePass:
		return 0
	case parityreport.OutcomeFail:
		return 1
	default:
		return 2
	}
}

// printCompileCoveragePolicy names, on STDERR, what the compile-coverage gate
// decided for every JVM pin the run materialized (SW-204 AC-1).
//
// It is on stderr and not stdout on purpose: stdout is the matrix a reader
// publishes, stderr is the runner's own log, and a REFUSAL has to reach the
// operator watching the dispatch rather than only the file they read afterwards.
// The per-pin line is printed for accepted pins too — a gate that logs only when
// it fires leaves "did it even look?" unanswerable.
//
// This is a report of a decision already made in parityreport.Finalize. It
// decides nothing itself, and there is no flag by which it could.
//
// Its arms mirror, one for one, the compile-coverage arms of Finalize
// (internal/parityreport/report.go) — including the sanity guard that refuses a
// pin claiming coverage >= 1.0000 beside compiled < source. A log that narrates
// a pin as "accepted" while the gate appends a refusal for it is worse than no
// log, so the correspondence is held by a test rather than by discipline:
// TestPrintCompileCoveragePolicy_AgreesWithTheGate runs both over the same
// inputs and requires REFUSED on stderr exactly when Finalize refuses.
func printCompileCoveragePolicy(rep parityreport.Report) {
	if rep.Family != parityreport.FamilyJVM {
		return
	}
	fmt.Fprintf(os.Stderr, "parity: compile_coverage policy over %d materialized JVM pin(s) "+
		"(source: corpus/manifest.json; a pin with no figure is REFUSED, a figure below 1.0000 "+
		"is refused unless the manifest states an excluded_reason, and a figure of 1.0000 or "+
		"above beside compiled < source is REFUSED as self-contradictory):\n", len(rep.Repos))
	for _, rp := range rep.Repos {
		switch cc := rp.CompileCoverage; {
		case cc == nil:
			fmt.Fprintf(os.Stderr, "  %-24s REFUSED — no compile_coverage recorded for this pin\n", rp.Name)
		case cc.Coverage < 1.0 && cc.ExcludedReason == "":
			fmt.Fprintf(os.Stderr, "  %-24s REFUSED — %d/%d = %.4f with no excluded_reason\n",
				rp.Name, cc.CompiledFiles, cc.SourceFiles, cc.Coverage)
		case cc.Coverage >= 1.0 && cc.CompiledFiles < cc.SourceFiles:
			fmt.Fprintf(os.Stderr, "  %-24s REFUSED — %d/%d = %.4f is self-contradictory: a full "+
				"compile claimed beside counts that say it did not happen\n",
				rp.Name, cc.CompiledFiles, cc.SourceFiles, cc.Coverage)
		case cc.Coverage < 1.0:
			fmt.Fprintf(os.Stderr, "  %-24s accepted — %d/%d = %.4f, DOCUMENTED NEGATIVE: %s\n",
				rp.Name, cc.CompiledFiles, cc.SourceFiles, cc.Coverage, firstLine(cc.ExcludedReason))
		default:
			fmt.Fprintf(os.Stderr, "  %-24s accepted — %d/%d = %.4f\n",
				rp.Name, cc.CompiledFiles, cc.SourceFiles, cc.Coverage)
		}
	}
}

// compareVerdictSets applies §12.4's two-green-runs discipline to a reliability
// gate: publication is gated on TWO dispatches with IDENTICAL verdict sets.
//
// It compares VERDICTS, not report bytes. Two dispatches legitimately differ in
// timestamps, durations and clone paths; requiring byte-identical reports would
// make an irrelevant difference look like a disagreement, and a row that differs
// between two otherwise-identical dispatches is an ENVIRONMENT FINDING to be
// explained — never a flake to be retried away.
func compareVerdictSets(spec string) int {
	parts := strings.Split(spec, ",")
	if len(parts) != 2 {
		fmt.Fprintln(os.Stderr, "parity: -verdict-diff needs exactly two report paths: a.json,b.json")
		return 2
	}
	a, err := parityreport.Read(strings.TrimSpace(parts[0]))
	if err != nil {
		fmt.Fprintf(os.Stderr, "parity: %v\n", err)
		return 2
	}
	b, err := parityreport.Read(strings.TrimSpace(parts[1]))
	if err != nil {
		fmt.Fprintf(os.Stderr, "parity: %v\n", err)
		return 2
	}
	da, db := a.VerdictSetDigest(), b.VerdictSetDigest()
	fmt.Printf("run a: %s\n  %s\nrun b: %s\n  %s\n", a.Provenance.RunSHA, da, b.Provenance.RunSHA, db)
	if da != db {
		fmt.Println("\nparity: VERDICT SETS DIFFER — this is an environment finding to be explained, not a flake to retry away.")
		as, bs := a.VerdictSet(), b.VerdictSet()
		for id, av := range as {
			if bv := bs[id]; bv != av {
				fmt.Printf("  %-24s a=%s b=%s\n", id, av, bv)
			}
		}
		return 1
	}
	if !a.Publishable || !b.Publishable {
		fmt.Println("\nparity: verdict sets agree, but at least one run is NOT publishable — publication refused.")
		return 2
	}
	fmt.Println("\nparity: two dispatches agree on every verdict and both are publishable.")
	return 0
}

// compareCountsSets is the Wave-0 DETERMINISM gate: two dispatches must agree
// on per-row node/edge COUNTS and snapshot DIGESTS, not merely on verdicts.
//
// It exists because `-verdict-diff` was demonstrated structurally blind to
// PARITY-002's non-deterministic half: on grpc-go all six published executions
// agreed the row FAILs while the incremental edge count flapped 69939/69940
// between dispatches (docs/rc/parity-matrix-real-repo.md records this
// verbatim). A verdict agreement over flapping counts is not determinism.
// This gate compares what the counts prove and keeps -verdict-diff's
// discipline: a differing row is an ENVIRONMENT FINDING to be explained,
// never a flake to be retried away.
func compareCountsSets(spec string) int {
	parts := strings.Split(spec, ",")
	if len(parts) != 2 {
		fmt.Fprintln(os.Stderr, "parity: -counts-diff needs exactly two report paths: a.json,b.json")
		return 2
	}
	a, err := parityreport.Read(strings.TrimSpace(parts[0]))
	if err != nil {
		fmt.Fprintf(os.Stderr, "parity: %v\n", err)
		return 2
	}
	b, err := parityreport.Read(strings.TrimSpace(parts[1]))
	if err != nil {
		fmt.Fprintf(os.Stderr, "parity: %v\n", err)
		return 2
	}
	da, db := a.CountsSetDigest(), b.CountsSetDigest()
	fmt.Printf("run a: %s\nrun b: %s\n", a.Provenance.RunSHA, b.Provenance.RunSHA)
	if da != db {
		fmt.Println("\nparity: COUNTS DIFFER between the two dispatches — non-determinism (or a row-set mismatch); an environment finding to be explained, not a flake to retry away.")
		as, bs := a.CountsSet(), b.CountsSet()
		ids := make([]string, 0, len(as)+len(bs))
		seen := map[string]bool{}
		for id := range as {
			ids = append(ids, id)
			seen[id] = true
		}
		for id := range bs {
			if !seen[id] {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			av, aok := as[id]
			bv, bok := bs[id]
			switch {
			case !aok:
				fmt.Printf("  %-28s only in run b: %s\n", id, bv)
			case !bok:
				fmt.Printf("  %-28s only in run a: %s\n", id, av)
			case av != bv:
				fmt.Printf("  %-28s\n    a: %s\n    b: %s\n", id, av, bv)
			}
		}
		return 1
	}
	if !a.Publishable || !b.Publishable {
		fmt.Println("\nparity: counts agree, but at least one run is NOT publishable — publication refused.")
		return 2
	}
	fmt.Println("\nparity: two dispatches agree on every per-row count and snapshot digest, and both are publishable.")
	return 0
}

// compareRefusalSets is the THIRD diff mode (SW-204 AC-4a): two dispatches must
// refuse publication for bit-identically the same reasons.
//
// WHY IT EXISTS. -verdict-diff and -counts-diff both stop at
// "at least one run is NOT publishable — publication refused" and exit 2. That
// is correct and stays untouched, but it means the two gates say NOTHING about a
// pair of runs that both refuse — and the JVM matrix both refuses and will keep
// refusing until the corpus hosts the two source shapes the 8 SKIPPED cells
// need. "The two dispatches refuse for the same reasons" is a real determinism
// property of the harness, and before this mode it had no instrument.
//
// WHAT EXIT 0 MEANS, AND WHAT IT DOES NOT. Exit 0 means the refusal is
// DETERMINISTIC. It never means the run is publishable, and this function
// deliberately does not read, print or consider Publishable as a pass condition
// — a mode that could exit 0 on "publishable" would be a fourth publication gate
// wearing a diagnostic's name.
//
// THERE IS NO ALLOWLIST AND THERE MUST NEVER BE ONE. No flag, list or
// environment variable may mark a reason acceptable and drop it from the
// comparison or from the gate. internal/parityreport/report.go says why in the
// coverage-limit comment: "it does not make the run publishable, or 'record the
// limit' would become the cheap way past the gate." A waiver flag here would
// rebuild that escape one flag over, and SW-204 AC-4c requires a reviewer to
// reject any such mechanism on sight.
func compareRefusalSets(spec string) int {
	parts := strings.Split(spec, ",")
	if len(parts) != 2 {
		fmt.Fprintln(os.Stderr, "parity: -refusal-diff needs exactly two report paths: a.json,b.json")
		return 2
	}
	a, err := parityreport.Read(strings.TrimSpace(parts[0]))
	if err != nil {
		fmt.Fprintf(os.Stderr, "parity: %v\n", err)
		return 2
	}
	b, err := parityreport.Read(strings.TrimSpace(parts[1]))
	if err != nil {
		fmt.Fprintf(os.Stderr, "parity: %v\n", err)
		return 2
	}
	da, db := a.RefusalSetDigest(), b.RefusalSetDigest()
	fmt.Printf("run a: %s  publishable=%v  refusals=%d\nrun b: %s  publishable=%v  refusals=%d\n",
		a.Provenance.RunSHA, a.Publishable, len(a.RefusalSet()),
		b.Provenance.RunSHA, b.Publishable, len(b.RefusalSet()))
	if da != db {
		fmt.Println("\nparity: REFUSAL SETS DIFFER between the two dispatches — the harness refuses " +
			"non-deterministically; an environment finding to be explained, not a flake to retry away.")
		as, bs := a.RefusalSet(), b.RefusalSet()
		inB := map[string]bool{}
		for _, why := range bs {
			inB[why] = true
		}
		inA := map[string]bool{}
		for _, why := range as {
			inA[why] = true
		}
		for _, why := range as {
			if !inB[why] {
				fmt.Printf("  only in run a: %s\n", why)
			}
		}
		for _, why := range bs {
			if !inA[why] {
				fmt.Printf("  only in run b: %s\n", why)
			}
		}
		return 1
	}
	shared := a.RefusalSet()
	fmt.Printf("\nshared refusal set (%d):\n", len(shared))
	for _, why := range shared {
		fmt.Printf("  %s\n", why)
	}
	coverage := 0
	for _, why := range shared {
		if strings.HasPrefix(why, parityreport.ReasonPrefixCompileCoverage) {
			coverage++
		}
	}
	fmt.Printf("\n  compile_coverage refusals in the shared set: %d\n", coverage)
	if len(shared) == 0 {
		fmt.Println("\nparity: neither dispatch recorded a refusal. This mode compares refusals and " +
			"therefore certifies NOTHING here — -verdict-diff and -counts-diff are the publication gates.")
		return 0
	}
	fmt.Println("\nparity: the two dispatches refuse for bit-identically the same reasons — the refusal " +
		"is DETERMINISTIC. This is NOT a publication: both runs are refused, and exit 0 here says only " +
		"that they are refused identically.")
	return 0
}

func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func orNone(s string) string {
	if s == "" {
		return "(none — this run is not publishable)"
	}
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 110 {
		s = s[:110] + "…"
	}
	return s
}
