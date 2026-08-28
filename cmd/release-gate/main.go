package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/samibel/graphi/internal/version"
)

// contextFlagDefault is the -context flag's zero value. It is a named
// constant so that AC-6 — "the default context, when none is passed, must be
// the strict one" — is pinned by a test against the value main actually uses,
// rather than by a test that re-types the empty string and hopes.
const contextFlagDefault = ""

func main() {
	var (
		baselinePath = flag.String("baseline", "docs/mcp-tool-baseline.json", "path to prior-release MCP tool baseline")
		docsDir      = flag.String("docs", "docs", "docs directory for published scorecard")
		publish      = flag.Bool("publish", false, "write scorecard evidence to docs/ after a passing run")
		versionStr   = flag.String("version", version.Version, "release version")
		contextFlag  = flag.String("context", contextFlagDefault, "execution `context`: pr or release. "+
			"Only one thing depends on it — whether an UNVERIFIED gate blocks. "+
			"Empty means release, the strict one: a forgotten flag must fail safe.")
	)
	flag.Parse()

	// The context is a fact the caller states; policy.go decides what it
	// means. An unrecognised value is refused here rather than defaulted
	// silently, so a typo in a workflow cannot become a policy.
	gateContext, err := ParseContext(*contextFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "release-gate: %v\n", err)
		os.Exit(2)
	}

	commit := "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				commit = s.Value
				break
			}
		}
	}

	result, err := Run(gateContext, DefaultGates(), DefaultEvalReport, DefaultUX, *baselinePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "release-gate: %v\n", err)
		os.Exit(2)
	}

	fmt.Print(FormatVerdict(result))
	// Reporting must never change a verdict: a summary that cannot be written
	// is a diagnostic, not a gate.
	if err := WriteStepSummary(result); err != nil {
		fmt.Fprintf(os.Stderr, "release-gate: check summary not written: %v\n", err)
	}

	if !result.Pass {
		os.Exit(1)
	}

	if *publish {
		err := Publish(result, *docsDir, *versionStr, commit)
		var refused *PublishRefusedError
		switch {
		case err == nil:
			fmt.Println("Published docs/release-scorecard.json and docs/release-scorecard.md")
		case errors.As(err, &refused) && !gateContext.Blocks(StateUnverified):
			// A pull request with an unverified gate: the change is not
			// refused, but neither is a PASS scorecard written over a
			// measurement nobody took. Both halves of that are the policy.
			fmt.Fprintf(os.Stderr, "release-gate: %v\n", err)
		default:
			fmt.Fprintf(os.Stderr, "release-gate: publish: %v\n", err)
			os.Exit(2)
		}
	}
}
