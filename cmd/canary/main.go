// Command canary is the SW-008 hermetic egress-denied canary + zero-telemetry
// static gate entrypoint.
//
// It is a CI concern, not a runtime surface: it builds CGo-free and exercises
// the graphi surface stack under loopback-only network isolation (or
// hard-fails when isolation is unavailable), then runs the static zero-telemetry
// gate over the default build graph.
//
// Usage:
//
//	graphi-canary runtime   # run the runtime egress canary, emit JSON artifact
//	graphi-canary gate      # run the static zero-telemetry gate
//	graphi-canary -tags grammar_subset,webui_embed -goos linux -goarch amd64 gate
//	graphi-canary all       # run both
//
// Exit codes: 0 on pass, non-zero on any fail/hard-fail so CI naturally gates.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/internal/canary"
)

func main() {
	buildTags := flag.String("tags", "", "comma-separated build tags for the exact static dependency graph")
	goos := flag.String("goos", "", "target GOOS for the static dependency graph (default host)")
	goarch := flag.String("goarch", "", "target GOARCH for the static dependency graph (default host)")
	flag.Parse()
	mode := "all"
	if flag.NArg() >= 1 {
		mode = flag.Arg(0)
	}

	switch mode {
	case "runtime":
		os.Exit(runRuntime())
	case "gate":
		os.Exit(runGate(parseBuildTags(*buildTags), *goos, *goarch))
	case "all":
		c := runGate(parseBuildTags(*buildTags), *goos, *goarch)
		if c == 0 {
			c = runRuntime()
		}
		os.Exit(c)
	default:
		fmt.Fprintln(os.Stderr, "usage: graphi-canary [runtime|gate|all]")
		os.Exit(2)
	}
}

func runRuntime() int {
	store := graphstore.NewMemStore()
	cfg := canary.RunConfig{
		Isolator: nil, // default platform isolator (hard-fails off-Linux)
		Driver:   canary.NewInProcessDriver(store, os.Stderr),
		Union:    canary.NewSurfaceUnion(),
	}
	art, err := canary.Run(context.Background(), cfg)
	b, _ := json.MarshalIndent(art, "", "  ")
	fmt.Println(string(b))
	if err != nil {
		fmt.Fprintf(os.Stderr, "canary: %v\n", err)
		return 1
	}
	return 0
}

func runGate(buildTags []string, goos, goarch string) int {
	res, err := canary.RunGate(canary.GateConfig{BuildTags: buildTags, GOOS: goos, GOARCH: goarch})
	b, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(b))
	if err != nil {
		fmt.Fprintf(os.Stderr, "canary gate: %v\n", err)
		return 1
	}
	if res.Verdict == "fail" {
		return 1
	}
	return 0
}

func parseBuildTags(raw string) []string {
	var tags []string
	for _, tag := range strings.Split(raw, ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}
