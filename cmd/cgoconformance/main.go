// Command cgoconformance runs the CGo-free build conformance gate for the
// default graphi binary and emits the named check "cgo-free-conformance". It is
// the integration embodiment of story SW-009's acceptance criteria and is
// invoked by the .github/workflows/cgoconformance.yml CI workflow as a distinct,
// named check in the build summary.
//
// Usage:
//
//	go run ./cmd/cgoconformance [-target ./cmd/graphi/] [-test-target ./...] [-cgo 0]
//
// Exit code is 0 on PASS and 1 on FAIL; a JSON named-check record is printed on
// stdout.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/samibel/graphi/internal/cgoconformance"
)

func main() {
	target := flag.String("target", cgoconformance.DefaultBuildTarget, "default-graph build target")
	testTarget := flag.String("test-target", "./...", "test selector run under CGO_ENABLED=0")
	cgo := flag.String("cgo", "0", "enforced CGO_ENABLED value")
	timeout := flag.Duration("timeout", 10*time.Minute, "overall gate timeout")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	res := cgoconformance.Run(ctx, cgoconformance.GateConfig{
		Target:     *target,
		TestTarget: *testTarget,
		CGOEnabled: *cgo,
		Stdout:     os.Stdout,
	})

	if err := json.NewEncoder(os.Stdout).Encode(res); err != nil {
		fmt.Fprintf(os.Stderr, "encode result: %v\n", err)
	}
	if res.Status != cgoconformance.StatusPass {
		fmt.Fprintf(os.Stderr, "[%s] %s: %s\n", res.Name, res.Status, res.Reason)
		os.Exit(1)
	}
}
