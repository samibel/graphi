package main

// `-check-targets` — make docs/eval/retrieval-targets.json executable.
//
// Before SW-282 the file was written by `-derive` and read back only inside
// internal/eval/retrieval/targets_test.go, and cmd/release-gate's required
// gates did not include it. A target no command evaluates is a note. This mode
// evaluates every target in the file against a named report plus the committed
// coverage measurement and qrel-blind smoke outcome, prints one line per
// target, and exits non-zero on the first miss.

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/samibel/graphi/internal/eval/retrieval"
)

func runCheckTargets(root, reportPath string, stdout, stderr io.Writer) int {
	in := retrieval.TargetCheckInputs{RepoRoot: root, ReportPath: reportPath}
	// The PINNED gate report is additionally bound to the reviewed candidate
	// SHA: naming the reviewed run and then handing it a report from a
	// different tree is the stale-evidence failure the SW-263 review rejected.
	// A report at any other path is evaluated as named — that is what naming a
	// report on the command line means.
	if sameRepoPath(root, reportPath, retrieval.GateReportPath) {
		in.RequireCandidateSHA = retrieval.GateCandidateSHA
	}
	res, err := retrieval.CheckTargets(in)
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: check-targets: %v\n", err)
		return exitError
	}
	fmt.Fprint(stdout, retrieval.FormatTargetCheck(res))
	if res.MissCount > 0 {
		fmt.Fprintf(stderr, "retrieval-eval: check-targets: %d of %d target(s) missed; first miss: %s\n",
			res.MissCount, len(res.Checks), res.FirstMissName)
		return exitError
	}
	return exitOK
}

// sameRepoPath reports whether two paths, one possibly relative to the
// repository root, name the same file.
func sameRepoPath(root, a, b string) bool {
	resolve := func(p string) string {
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, filepath.FromSlash(p))
		}
		if abs, err := filepath.Abs(p); err == nil {
			return filepath.Clean(abs)
		}
		return filepath.Clean(p)
	}
	return resolve(a) == resolve(b)
}
