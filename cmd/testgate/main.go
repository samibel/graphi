// Command testgate runs the default test suite under CGO_ENABLED=0 and asserts it
// is green EXCEPT exactly the two known internal/mcpconfig root-perms tests,
// expressed as an explicit, privilege-conditional expected-failure allowlist
// (SW-055 AC#3/AC#7). New regressions cannot hide behind the carve-out: a third
// failing test, or an allowlisted test that starts passing, fails the gate.
//
// Usage:
//
//	go run ./cmd/testgate            # runs `go test -json ./...` and evaluates
//	go test -json ./... | go run ./cmd/testgate -stdin
//
// Exit code is 0 when the run is green under the allowlist, 1 otherwise.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/samibel/graphi/internal/testgate"
)

func main() {
	stdin := flag.Bool("stdin", false, "read a `go test -json` stream from stdin instead of running go test")
	target := flag.String("target", "./...", "test target when running go test")
	timeout := flag.Duration("timeout", 15*time.Minute, "overall timeout when running go test")
	flag.Parse()

	euid := os.Geteuid()

	var stream *bytes.Reader
	if *stdin {
		buf := new(bytes.Buffer)
		if _, err := buf.ReadFrom(os.Stdin); err != nil {
			fmt.Fprintf(os.Stderr, "testgate: read stdin: %v\n", err)
			os.Exit(2)
		}
		stream = bytes.NewReader(buf.Bytes())
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "test", "-json", *target)
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		out, _ := cmd.Output() // non-zero exit is EXPECTED when carve-out tests fail; evaluate the stream
		stream = bytes.NewReader(out)
	}

	res, err := testgate.Evaluate(stream, euid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "testgate: evaluate: %v\n", err)
		os.Exit(2)
	}
	fmt.Print(testgate.FormatVerdict(res, euid))
	if !res.Green {
		os.Exit(1)
	}
}
