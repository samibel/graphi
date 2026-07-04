package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/samibel/graphi/internal/version"
)

func main() {
	var (
		baselinePath = flag.String("baseline", "docs/mcp-tool-baseline.json", "path to prior-release MCP tool baseline")
		docsDir      = flag.String("docs", "docs", "docs directory for published scorecard")
		publish      = flag.Bool("publish", false, "write scorecard evidence to docs/ after a passing run")
		versionStr   = flag.String("version", version.Version, "release version")
	)
	flag.Parse()

	commit := "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				commit = s.Value
				break
			}
		}
	}

	result, err := Run(DefaultGates(), DefaultEvalReport, DefaultUX, *baselinePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "release-gate: %v\n", err)
		os.Exit(2)
	}

	fmt.Print(FormatVerdict(result))

	if !result.Pass {
		os.Exit(1)
	}

	if *publish {
		if err := Publish(result, *docsDir, *versionStr, commit); err != nil {
			fmt.Fprintf(os.Stderr, "release-gate: publish: %v\n", err)
			os.Exit(2)
		}
		fmt.Println("Published docs/release-scorecard.json and docs/release-scorecard.md")
	}
}
