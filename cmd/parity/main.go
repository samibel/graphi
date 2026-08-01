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
//	go run ./cmd/parity -verdict-diff a.json,b.json
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
	"strings"
	"time"

	"github.com/samibel/graphi/internal/corpus"
	"github.com/samibel/graphi/internal/parity"
	"github.com/samibel/graphi/internal/parityreport"
)

func main() { os.Exit(run()) }

func run() int {
	manifest := flag.String("manifest", "corpus/manifest.json", "corpus manifest path")
	classesPath := flag.String("classes-file", parity.ClassesPath, "declared change-class matrix")
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
	flag.Parse()

	if *verdictDiff != "" {
		return compareVerdictSets(*verdictDiff)
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
		Log: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		},
	}
	rep, err := r.Run(ctx, m, rows, prov)
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
	fmt.Printf("  run sha       %s\n", rep.Provenance.RunSHA)
	fmt.Printf("  runner class  %s\n", orNone(rep.Provenance.RunnerClass))
	fmt.Printf("  max tier      %d\n\n", rep.MaxTier)
	for _, c := range rep.Classes {
		where := c.Repo
		if where == "" {
			where = "-"
		}
		fmt.Printf("  %-24s %-9s %-10s %s\n", c.ID, c.Verdict, where, firstLine(c.Detail))
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
