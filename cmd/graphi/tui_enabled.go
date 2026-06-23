//go:build tui

package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/tui"
)

// runTUI launches the interactive terminal surface (SW-047). It consumes the
// shared Engine over the SW-044 HTTP/SSE surface via the loopback HTTP/SSE client
// adapter — NOT an in-process client — so the TUI reuses the single
// network-facing contract and stays byte-identical to the web/VS Code surfaces
// (parity by construction). Local-first: -addr is loopback-only (fail closed);
// the TUI imports no engine/* package.
//
//	graphi tui [-addr http://127.0.0.1:8080]
//
// Start the server first with `graphi http -addr 127.0.0.1:PORT -root <repo>`,
// then point the TUI at the same loopback address.
//
// This file is compiled only under the `tui` build tag; the default binary uses
// the stub in tui_disabled.go so the Bubble Tea dependency tree stays out of the
// lean local-first build.
func runTUI(args []string) int {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	addr := fs.String("addr", "http://127.0.0.1:8080", "loopback HTTP/SSE engine address (host must be 127.0.0.1/localhost/::1)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: tui: %v\n", err)
		return 1
	}
	// NewHTTP fails closed on a non-loopback target (mirrors httpsrv.AssertLoopback),
	// so the TUI can never be pointed at a remote Engine.
	eng, err := client.NewHTTP(*addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: tui: %v\n", err)
		return 1
	}
	if err := tui.Run(context.Background(), eng, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: tui: %v\n", err)
		return 1
	}
	return 0
}
