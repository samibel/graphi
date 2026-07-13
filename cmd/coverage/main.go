// Command coverage is the FU-4 capability-coverage tool (story SW-060). It has
// two modes over the checked-in capability matrix:
//
//	go run ./cmd/coverage -check       # CI guard: derive the LIVE capability set
//	                                   # from the registries, compare against
//	                                   # docs/coverage-matrix.yaml, and exit 1 on
//	                                   # any drift (printing a precise diff).
//	go run ./cmd/coverage -generate    # regenerate docs/coverage-matrix.md from
//	                                   # docs/coverage-matrix.yaml (one command).
//
// Exit codes mirror cmd/layerguard: 0 = clean, 1 = drift/stale, 2 = internal
// error. It builds CGo-free and is a CI concern, not a runtime surface.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/samibel/graphi/internal/coverage"
)

func main() {
	var (
		check    = flag.Bool("check", false, "run the drift guard: fail (exit 1) if the matrix has drifted from the live registries")
		generate = flag.Bool("generate", false, "regenerate docs/coverage-matrix.md from docs/coverage-matrix.yaml")
		root     = flag.String("root", "", "module root (default: resolved via `go env GOMOD`)")
	)
	flag.Parse()

	if *check == *generate {
		fmt.Fprintln(os.Stderr, "coverage: exactly one of -check or -generate is required")
		os.Exit(2)
	}

	dir, err := resolveRoot(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "coverage: %v\n", err)
		os.Exit(2)
	}
	yamlPath := filepath.Join(dir, filepath.FromSlash(coverage.MatrixYAMLPath))
	mdPath := filepath.Join(dir, filepath.FromSlash(coverage.MatrixMDPath))

	caps, err := coverage.LoadMatrix(yamlPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "coverage: %v\n", err)
		os.Exit(2)
	}

	if *generate {
		out := coverage.RenderMarkdown(caps)
		if err := os.WriteFile(mdPath, []byte(out), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "coverage: write %s: %v\n", mdPath, err)
			os.Exit(2)
		}
		fmt.Printf("coverage: regenerated %s (%d capabilities)\n", coverage.MatrixMDPath, len(caps))
		return
	}

	// -check
	live, err := coverage.Enumerate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "coverage: enumerate live capabilities: %v\n", err)
		os.Exit(2)
	}
	rep := coverage.Check(live, caps)
	fmt.Print(rep.Format())

	// SCOPE-01 (SW-111): the `tier: stable` set must equal exactly the frozen 12
	// operations. This EXTENDS the coverage gate — a 13th stable row or a missing
	// one fails -check alongside live-drift.
	stableRep := coverage.CheckStableTier(caps)
	fmt.Print(stableRep.Format())

	// Also verify the rendered .md is fresh, so -check is the single CI gate.
	if current, rerr := os.ReadFile(mdPath); rerr == nil {
		if want := coverage.RenderMarkdown(caps); string(current) != want {
			fmt.Fprintf(os.Stderr, "coverage: %s is stale — run `go run ./cmd/coverage -generate`\n", coverage.MatrixMDPath)
			os.Exit(1)
		}
	}

	if !rep.Pass() || !stableRep.Pass() {
		os.Exit(1)
	}
}

func resolveRoot(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	return coverage.ModuleRoot()
}
