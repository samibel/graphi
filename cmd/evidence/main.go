// Command evidence is the M0-05 (SW-119) evidence-index + gate-dashboard tool. It
// has two modes over the checked-in evidence index:
//
//	go run ./cmd/evidence -check       # CI guard: fail (exit 1) if a PASS row is
//	                                   # not backed by an Evidence URI AND a
//	                                   # SHA/Digest, if a status is invalid, or if
//	                                   # docs/rc/evidence-index.md is stale.
//	go run ./cmd/evidence -generate    # regenerate docs/rc/evidence-index.md from
//	                                   # docs/rc/evidence-index.yaml (one command).
//
// Exit codes mirror cmd/coverage and cmd/layerguard: 0 = clean, 1 = violation/
// stale, 2 = internal error. It builds CGo-free, imports only the standard library
// (no dependency on the default build path), and is a CI/tooling binary — like
// layerguard and coverage — not a graphi subcommand, parser, analyzer, MCP tool or
// surface, so it needs no coverage-matrix row.
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
		check    = flag.Bool("check", false, "run the honesty guard: fail (exit 1) if a PASS row lacks evidence, a status is invalid, or the .md is stale")
		generate = flag.Bool("generate", false, "regenerate docs/rc/evidence-index.md from docs/rc/evidence-index.yaml")
		root     = flag.String("root", "", "module root (default: resolved via `go env GOMOD`)")
	)
	flag.Parse()

	if *check == *generate {
		fmt.Fprintln(os.Stderr, "evidence: exactly one of -check or -generate is required")
		os.Exit(2)
	}

	dir, err := resolveRoot(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "evidence: %v\n", err)
		os.Exit(2)
	}
	yamlPath := filepath.Join(dir, filepath.FromSlash(evidence.EvidenceYAMLPath))
	mdPath := filepath.Join(dir, filepath.FromSlash(evidence.EvidenceMDPath))

	idx, err := evidence.Load(yamlPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "evidence: %v\n", err)
		os.Exit(2)
	}

	if *generate {
		// The honesty rule holds at generate time too: refuse to write a dashboard
		// that would render an unbacked PASS.
		if rep := evidence.Check(idx); !rep.Pass() {
			fmt.Fprint(os.Stderr, rep.Format())
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

	// -check
	rep := evidence.Check(idx)
	fmt.Print(rep.Format())

	// The rendered .md must be fresh — byte-identical to a regeneration from the
	// checked YAML — so -check is the single CI gate. A missing .md fails too.
	current, rerr := os.ReadFile(mdPath)
	if rerr != nil || string(current) != evidence.RenderMarkdown(idx) {
		fmt.Fprintf(os.Stderr, "evidence: %s is missing or stale — run `go run ./cmd/evidence -generate`\n", evidence.EvidenceMDPath)
		os.Exit(1)
	}

	if !rep.Pass() {
		os.Exit(1)
	}
	fmt.Printf("evidence-index freshness check PASS — %s matches the checked source.\n", evidence.EvidenceMDPath)
}

func resolveRoot(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	return evidence.ModuleRoot()
}
