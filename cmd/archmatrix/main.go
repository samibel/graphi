// Command archmatrix maintains the ARCH-P0 migration matrix: the inventory that
// maps every surfaces/client.Client method onto the bounded context, application
// service, and phase that will own it.
//
//	go run ./cmd/archmatrix -check      # CI guard: compare the matrix against the
//	                                    # live interface, the live error sentinels,
//	                                    # and the live compatibility stubs; also
//	                                    # verify the rendered Markdown is fresh.
//	go run ./cmd/archmatrix -generate   # regenerate docs/migration-matrix.md
//
// Exit codes mirror cmd/layerguard and cmd/coverage: 0 = clean, 1 = drift/stale,
// 2 = internal error. It builds CGo-free and is a CI concern, not a runtime
// surface.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/samibel/graphi/internal/archmatrix"
)

func main() {
	var (
		check    = flag.Bool("check", false, "fail (exit 1) if the matrix has drifted from the live client contract or the rendered table is stale")
		generate = flag.Bool("generate", false, "regenerate docs/migration-matrix.md from docs/migration-matrix.yaml")
		root     = flag.String("root", "", "module root (default: resolved via `go env GOMOD`)")
	)
	flag.Parse()

	if *check == *generate {
		fmt.Fprintln(os.Stderr, "archmatrix: exactly one of -check or -generate is required")
		os.Exit(2)
	}

	dir := *root
	if dir == "" {
		resolved, err := archmatrix.ModuleRoot()
		if err != nil {
			fmt.Fprintf(os.Stderr, "archmatrix: %v\n", err)
			os.Exit(2)
		}
		dir = resolved
	}
	yamlPath := filepath.Join(dir, filepath.FromSlash(archmatrix.MatrixYAMLPath))
	mdPath := filepath.Join(dir, filepath.FromSlash(archmatrix.MatrixMDPath))

	matrix, err := archmatrix.Load(yamlPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "archmatrix: %v\n", err)
		os.Exit(2)
	}
	usage, err := archmatrix.ScanSurfaceUsage(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "archmatrix: %v\n", err)
		os.Exit(2)
	}
	refs, err := archmatrix.ScanSentinelRefs(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "archmatrix: %v\n", err)
		os.Exit(2)
	}
	rendered := archmatrix.RenderMarkdown(matrix, usage, refs)

	if *generate {
		if err := os.WriteFile(mdPath, []byte(rendered), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "archmatrix: write %s: %v\n", mdPath, err)
			os.Exit(2)
		}
		fmt.Printf("archmatrix: regenerated %s (%d methods, %d sentinels)\n",
			archmatrix.MatrixMDPath, len(matrix.Methods), len(matrix.Sentinels))
		return
	}

	// -check
	sentinels, err := archmatrix.LiveSentinels(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "archmatrix: %v\n", err)
		os.Exit(2)
	}
	report := archmatrix.Check(matrix, archmatrix.LiveMethods(), sentinels)
	fmt.Print(report.Format())

	stubScan, err := archmatrix.ScanStubs(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "archmatrix: %v\n", err)
		os.Exit(2)
	}
	stubProblems := archmatrix.CheckStubs(matrix, stubScan)
	for _, problem := range stubProblems {
		fmt.Printf("  stub drift: %s\n", problem)
	}
	if len(stubProblems) == 0 {
		fmt.Println("compatibility-stub check PASS — declared implementation statuses match the source.")
	}

	stale := false
	if current, rerr := os.ReadFile(mdPath); rerr != nil || string(current) != rendered {
		fmt.Fprintf(os.Stderr, "archmatrix: %s is missing or stale — run `go run ./cmd/archmatrix -generate`\n",
			archmatrix.MatrixMDPath)
		stale = true
	}

	if !report.Pass() || len(stubProblems) > 0 || stale {
		os.Exit(1)
	}
}
