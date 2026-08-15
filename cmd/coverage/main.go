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
	"github.com/samibel/graphi/internal/evidence"
	"github.com/samibel/graphi/surfaces/client"
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
	jsonPath := filepath.Join(dir, filepath.FromSlash(coverage.MatrixJSONPath))

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
		// CAP-01 (SW-117): the machine-readable manifest is generated alongside
		// the human-readable matrix from the same checked source.
		jb, err := coverage.RenderJSON(caps)
		if err != nil {
			fmt.Fprintf(os.Stderr, "coverage: %v\n", err)
			os.Exit(2)
		}
		if err := os.WriteFile(jsonPath, jb, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "coverage: write %s: %v\n", jsonPath, err)
			os.Exit(2)
		}
		fmt.Printf("coverage: regenerated %s and %s (%d capabilities)\n", coverage.MatrixMDPath, coverage.MatrixJSONPath, len(caps))
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
	profileRep := coverage.CheckMCPDefaultProfile(caps)
	fmt.Print(profileRep.Format())

	// WP-J1 (ADR 0007): the GA LANGUAGE AXIS. Every `category: ga-language`
	// row must be bound to the live capability derivation (the same one the
	// trust report serves) and to green GA-LANG-* evidence rows — the axis
	// docs/stability-tiers.md used to carry in prose alone.
	idx, err := evidence.Load(filepath.Join(dir, filepath.FromSlash(evidence.EvidenceYAMLPath)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "coverage: load evidence index: %v\n", err)
		os.Exit(2)
	}
	gaGates := make([]coverage.GAEvidenceGate, 0, len(idx.Gates))
	for _, g := range idx.Gates {
		gaGates = append(gaGates, coverage.GAEvidenceGate{
			ID: g.ID,
			// internal/evidence's WP0 honesty rule, folded here: PASS counts
			// only with both evidence URI and sha.
			Passed: g.Status == evidence.StatusPass && g.EvidenceURI != "" && g.SHA != "",
		})
	}
	gaRep := coverage.CheckGALanguages(caps, client.LanguageCapabilities(), gaGates)
	fmt.Print(gaRep.Format())

	// Also verify the rendered .md is fresh, so -check is the single CI gate.
	if current, rerr := os.ReadFile(mdPath); rerr == nil {
		if want := coverage.RenderMarkdown(caps); string(current) != want {
			fmt.Fprintf(os.Stderr, "coverage: %s is stale — run `go run ./cmd/coverage -generate`\n", coverage.MatrixMDPath)
			os.Exit(1)
		}
	}

	// CAP-01 (SW-117): the generated JSON manifest must exist and be fresh —
	// byte-identical to a regeneration from the checked matrix. Unlike the .md
	// staleness check above, a MISSING manifest fails too: downstream consumers
	// (the REL-01 release-profile join) depend on the artifact existing.
	wantJSON, err := coverage.RenderJSON(caps)
	if err != nil {
		fmt.Fprintf(os.Stderr, "coverage: %v\n", err)
		os.Exit(2)
	}
	currentJSON, rerr := os.ReadFile(jsonPath)
	if rerr != nil || string(currentJSON) != string(wantJSON) {
		fmt.Fprintf(os.Stderr, "coverage: %s is missing or stale — run `go run ./cmd/coverage -generate`\n", coverage.MatrixJSONPath)
		os.Exit(1)
	}
	fmt.Printf("capability-manifest check PASS — %s matches the checked matrix.\n", coverage.MatrixJSONPath)

	if !rep.Pass() || !stableRep.Pass() || !profileRep.Pass() || !gaRep.Pass() {
		os.Exit(1)
	}
}

func resolveRoot(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	return coverage.ModuleRoot()
}
