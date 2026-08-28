// Command seamreach runs graphi's executor-seam reachability gate (SW-248
// AC-5). It fails CI (exit 1) when an operation dual-runs on the seam that no
// shipped surface profile can reach, or when the live matrix has drifted from
// the checked-in declaration.
//
// Usage:
//
//	go run ./cmd/seamreach                       # print the matrix, enforce nothing
//	go run ./cmd/seamreach -check                # the CI gate
//	go run ./cmd/seamreach -generate             # rewrite the declaration
//	go run ./cmd/seamreach -check -inject-unreachable demo_op
//	                                             # prove the gate bites
//
// Exit codes: 0 pass, 1 gate failure, 2 usage or I/O error.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/samibel/graphi/internal/seamreach"
)

// declarationPath is where -generate writes, relative to the repository root —
// which is where `go run ./cmd/seamreach` is invoked from.
const declarationPath = "internal/seamreach/reachability.txt"

func main() {
	check := flag.Bool("check", false, "enforce the invariant and the declaration; exit 1 on failure")
	generate := flag.Bool("generate", false, "rewrite "+declarationPath+" from the live matrix")
	// The injection exists so the gate can be watched failing. A check nobody
	// has seen fail is a claim about a check, and this one was written because
	// an absent check let a defect ship — so its own evidence is a run that
	// goes red on demand, not an assertion that it would.
	inject := flag.String("inject-unreachable", "",
		"add a synthetic shadow-mode operation no profile reaches, to demonstrate the gate failing")
	flag.Parse()

	seam := seamreach.Live()
	injected := *inject != ""
	if injected {
		seam = seam.WithUnreachable(*inject)
	}
	rep := seamreach.Check(seam)

	if *generate {
		if injected {
			fmt.Fprintln(os.Stderr, "seamreach: -generate and -inject-unreachable are mutually exclusive")
			os.Exit(2)
		}
		if err := os.WriteFile(declarationPath, []byte(rep.Matrix()), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "seamreach: write %s: %v\n", declarationPath, err)
			os.Exit(2)
		}
		fmt.Printf("seamreach: wrote %s\n", declarationPath)
		return
	}

	// An injected run judges the INVARIANT only. Comparing a deliberately
	// doctored matrix against the declaration would fail for the wrong reason
	// and teach nothing about the rule being demonstrated.
	declared := seamreach.Declaration()
	if injected {
		declared = ""
	}
	fmt.Print(rep.Format(declared))
	if !*check {
		return
	}
	if !rep.Pass() || (declared != "" && !rep.MatchesDeclaration(declared)) {
		os.Exit(1)
	}
}
