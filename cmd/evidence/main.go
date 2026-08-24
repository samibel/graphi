// Command evidence is the M0-05 (SW-119) evidence-index + gate-dashboard tool. It
// has four modes over the checked-in evidence index:
//
//	go run ./cmd/evidence -check       # CI guard: fail (exit 1) if a PASS row is
//	                                   # not backed by an Evidence URI AND a
//	                                   # SHA/Digest, if a status is invalid, if
//	                                   # docs/rc/evidence-index.md is stale, or if
//	                                   # any citation does not resolve (SW-205).
//	go run ./cmd/evidence -generate    # regenerate docs/rc/evidence-index.md from
//	                                   # docs/rc/evidence-index.yaml (one command).
//	go run ./cmd/evidence -check-citations   # the citation rules alone — a focused
//	                                   # human-facing subset of -check, never the
//	                                   # only place the rules run (SW-205 AC-9).
//	go run ./cmd/evidence -record-citations  # re-record stale blob shas from HEAD
//	                                   # and print every row touched (AC-12).
//
// Exit codes mirror cmd/coverage and cmd/layerguard: 0 = clean, 1 = violation/
// stale, 2 = internal error. It builds CGo-free, imports only the standard library
// (no dependency on the default build path) plus the `git` binary that CI and every
// dev already has, and is a CI/tooling binary — like layerguard and coverage — not
// a graphi subcommand, parser, analyzer, MCP tool or surface, so it needs no
// coverage-matrix row.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/samibel/graphi/internal/evidence"
)

func main() {
	var (
		check      = flag.Bool("check", false, "run the honesty guard: fail (exit 1) if a PASS row lacks evidence, a status is invalid, a citation does not resolve, or the .md is stale")
		generate   = flag.Bool("generate", false, "regenerate docs/rc/evidence-index.md from docs/rc/evidence-index.yaml")
		checkCites = flag.Bool("check-citations", false, "run only the citation rules (a focused subset of -check; CI does not need this flag)")
		recordCite = flag.Bool("record-citations", false, "re-record stale blob shas from HEAD and print every row touched")
		dryRun     = flag.Bool("dry-run", false, "with -record-citations: compute and print the plan without writing")
		root       = flag.String("root", "", "module root (default: resolved via `go env GOMOD`)")
	)
	flag.Parse()

	modes := 0
	for _, m := range []bool{*check, *generate, *checkCites, *recordCite} {
		if m {
			modes++
		}
	}
	if modes != 1 {
		fmt.Fprintln(os.Stderr, "evidence: exactly one of -check, -generate, -check-citations or -record-citations is required")
		os.Exit(2)
	}

	dir, err := resolveRoot(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "evidence: %v\n", err)
		os.Exit(2)
	}
	yamlPath := filepath.Join(dir, filepath.FromSlash(evidence.EvidenceYAMLPath))
	mdPath := filepath.Join(dir, filepath.FromSlash(evidence.EvidenceMDPath))

	if *recordCite {
		touched, err := evidence.RecordCitations(dir, *dryRun)
		if err != nil {
			fmt.Fprintf(os.Stderr, "evidence: %v\n", err)
			os.Exit(2)
		}
		fmt.Print(evidence.FormatRerecords(touched))
		return
	}

	idx, err := evidence.Load(yamlPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "evidence: %v\n", err)
		os.Exit(2)
	}

	if *checkCites {
		crep, err := citations(dir, idx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "evidence: %v\n", err)
			os.Exit(2)
		}
		fmt.Print(crep.FormatCitations())
		if !crep.Pass() {
			os.Exit(1)
		}
		return
	}

	if *generate {
		// The honesty rule holds at generate time too: refuse to write a dashboard
		// that would render an unbacked PASS — or one that would render a citation
		// nobody can follow (AC-10). A false citation in a nicer table is the same
		// failure.
		if rep := evidence.Check(idx); !rep.Pass() {
			fmt.Fprint(os.Stderr, rep.Format())
			os.Exit(1)
		}
		crep, err := citations(dir, idx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "evidence: %v\n", err)
			os.Exit(2)
		}
		if !crep.Pass() {
			fmt.Fprint(os.Stderr, crep.FormatCitations())
			os.Exit(1)
		}
		out := evidence.RenderMarkdown(idx)
		if err := os.WriteFile(mdPath, []byte(out), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "evidence: write %s: %v\n", mdPath, err)
			os.Exit(2)
		}
		fmt.Printf("evidence: regenerated %s (%d gates)\n", evidence.EvidenceMDPath, len(idx.Gates))
		return
	}

	// -check — one gate, not two (AC-9): the freshness rule, the honesty rule and
	// the citation rules all run here, and CI invokes nothing else.
	rep := evidence.Check(idx)
	fmt.Print(rep.Format())

	crep, cerr := citations(dir, idx)
	if cerr != nil {
		fmt.Fprintf(os.Stderr, "evidence: %v\n", cerr)
		os.Exit(2)
	}
	fmt.Print(crep.FormatCitations())

	// The rendered .md must be fresh — byte-identical to a regeneration from the
	// checked YAML — so -check is the single CI gate. A missing .md fails too.
	stale := false
	current, rerr := os.ReadFile(mdPath)
	if rerr != nil || string(current) != evidence.RenderMarkdown(idx) {
		fmt.Fprintf(os.Stderr, "evidence: %s is missing or stale — run `go run ./cmd/evidence -generate`\n", evidence.EvidenceMDPath)
		stale = true
	}

	if stale || !rep.Pass() || !crep.Pass() {
		os.Exit(1)
	}
	fmt.Printf("evidence-index freshness check PASS — %s matches the checked source.\n", evidence.EvidenceMDPath)
}

// citations loads the AC-11 ratchet and runs the citation sweep.
func citations(dir string, idx evidence.Index) (evidence.CitationReport, error) {
	gf, err := evidence.LoadGrandfather(dir)
	if err != nil {
		return evidence.CitationReport{}, err
	}
	return evidence.CheckCitations(dir, idx, gf)
}

func resolveRoot(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	return evidence.ModuleRoot()
}
