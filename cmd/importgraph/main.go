// Command importgraph records graphi's module-internal package import graph as a
// deterministic JSON artifact (ARCH-P0, architecture Phase 0 baseline).
//
//	go run ./cmd/importgraph -commit <sha>            # print the summary only
//	go run ./cmd/importgraph -commit <sha> \
//	    -out docs/rc/import-graph.json                 # write the artifact
//
// The artifact is a ONE-SHOT baseline, not a CI gate: every pull request changes
// the import graph, so a freshness check here would be pure noise. Enforcement
// stays with cmd/layerguard. This records what the graph looked like at the
// baseline revision, so a later architecture phase can show — rather than assert —
// that the import edges it promised to remove are gone.
//
// -commit is required and is not read from git on purpose: the artifact describes
// one specific reviewed revision, and re-rendering it must never silently
// re-stamp it with whatever HEAD happens to be.
//
// Exit codes mirror cmd/layerguard and cmd/coverage: 0 = ok, 2 = internal error.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/samibel/graphi/internal/importgraph"
)

func main() {
	var (
		commit = flag.String("commit", "", "baseline revision the artifact describes (required)")
		out    = flag.String("out", "", "write the JSON artifact to this path (default: summary to stdout only)")
		root   = flag.String("root", "", "module root (default: resolved via `go env GOMOD`)")
	)
	flag.Parse()

	if *commit == "" {
		fmt.Fprintln(os.Stderr, "importgraph: -commit is required (the revision this baseline describes)")
		os.Exit(2)
	}

	dir := *root
	if dir == "" {
		resolved, err := importgraph.ModuleRoot()
		if err != nil {
			fmt.Fprintf(os.Stderr, "importgraph: %v\n", err)
			os.Exit(2)
		}
		dir = resolved
	}

	snapshot, err := importgraph.Build(context.Background(), dir, *commit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "importgraph: %v\n", err)
		os.Exit(2)
	}
	fmt.Print(importgraph.Summary(snapshot))

	if *out == "" {
		return
	}
	rendered, err := importgraph.Render(snapshot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "importgraph: %v\n", err)
		os.Exit(2)
	}
	// A relative -out resolves against the working directory, NOT the scanned
	// -root. Those differ whenever -root points at another checkout (scanning a
	// pristine tree while writing into this one), and silently writing into the
	// scanned tree would leave the artifact where nobody looks for it.
	if err := os.WriteFile(filepath.FromSlash(*out), rendered, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "importgraph: write %s: %v\n", *out, err)
		os.Exit(2)
	}
	fmt.Printf("wrote %s (%d packages)\n", *out, len(snapshot.Packages))
}
