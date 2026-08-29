// Command seamready prints graphi's executor-seam cutover readiness (SW-254,
// AX-14): for every operation in client.MigratedOperations(), one verdict —
// READY, NOT_READY or UNKNOWN — over the six cutover criteria, with the
// artifact each row rests on.
//
// It is an ASSESSMENT, not a gate: it enforces nothing by default and flips
// nothing ever. READY is the precondition for a flip story; UNKNOWN is the
// absence of an artifact and never counts as PASS.
//
// Usage (from the repository root):
//
//	go run ./cmd/seamready                       # the text form
//	go run ./cmd/seamready -json                 # the machine form
//	go run ./cmd/seamready -require-ready <op>   # exit 1 unless <op> is READY
//	                                             # (for the future flip story's CI)
//
// Exit codes: 0 rendered, 1 -require-ready not met, 2 usage or I/O error.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/samibel/graphi/cmd/internal/runtime"
	"github.com/samibel/graphi/internal/seamready"
	"github.com/samibel/graphi/internal/state"
)

func main() {
	jsonOut := flag.Bool("json", false, "emit the seam-readiness-v1 JSON document instead of text")
	declPath := flag.String("declaration", seamready.DeclarationPath, "declared artifacts (yaml), relative to -repo")
	repo := flag.String("repo", ".", "repository root: where git, the source tree and the declaration are read")
	stateDir := flag.String("state-dir", state.StateDir(), "graphi state directory holding the divergence record")
	require := flag.String("require-ready", "", "exit 1 unless this operation's verdict is READY")
	flag.Parse()

	raw, err := os.ReadFile(joinRepo(*repo, *declPath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "seamready: read declaration: %v\n", err)
		os.Exit(2)
	}
	decl, err := seamready.ParseDeclaration(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "seamready: %v\n", err)
		os.Exit(2)
	}
	// The kill-switch variable name belongs to the composition root, which
	// internal/seamready cannot import; it is handed in here so c6 checks the
	// spelling the product actually reads.
	src := seamready.LiveSources(*repo, *stateDir).WithRuntime(runtime.EnvCanaryModeFor)
	a, err := seamready.Evaluate(decl, src)
	if err != nil {
		fmt.Fprintf(os.Stderr, "seamready: %v\n", err)
		os.Exit(2)
	}

	if *jsonOut {
		out, err := a.JSON()
		if err != nil {
			fmt.Fprintf(os.Stderr, "seamready: render json: %v\n", err)
			os.Exit(2)
		}
		os.Stdout.Write(out)
	} else {
		fmt.Print(a.Text())
	}

	if *require == "" {
		return
	}
	for _, o := range a.Operations {
		if o.Operation == *require {
			if o.Verdict == seamready.VerdictReady {
				return
			}
			fmt.Fprintf(os.Stderr, "seamready: %s is %s, not READY\n", o.Operation, o.Verdict)
			os.Exit(1)
		}
	}
	fmt.Fprintf(os.Stderr, "seamready: %q is not an operation on the executor seam\n", *require)
	os.Exit(2)
}

func joinRepo(repo, p string) string {
	if len(p) > 0 && p[0] == '/' {
		return p
	}
	return repo + "/" + p
}
