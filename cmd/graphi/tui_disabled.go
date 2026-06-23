//go:build !tui

package main

import (
	"fmt"
	"os"
)

// runTUI is the default-build stub for the interactive terminal surface. The
// real implementation (tui_enabled.go) is compiled only under the `tui` build
// tag, so the default local-first binary excludes the Bubble Tea dependency tree
// and stays within the budget-gated size ceiling. Rebuild with the tag to enable
// it: `go build -tags tui ./cmd/graphi`.
func runTUI(_ []string) int {
	fmt.Fprintln(os.Stderr, "graphi: this build was compiled without the TUI surface; rebuild with: go build -tags tui ./cmd/graphi")
	return 2
}
