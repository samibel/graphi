// Command layerguard runs graphi's layer-direction guard (story SW-013). It
// scans the import graph of the ranked packages and fails CI (exit 1) naming any
// package + import path that violates `cmd → surfaces → engine → core`; on
// success it prints the verified allowed-edge set.
//
// Usage:
//
//	go run ./cmd/layerguard
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/samibel/graphi/internal/layerguard"
)

func main() {
	rep, err := layerguard.Check(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "layerguard: %v\n", err)
		os.Exit(2)
	}
	fmt.Print(rep.Format())
	if !rep.Pass {
		os.Exit(1)
	}
}
