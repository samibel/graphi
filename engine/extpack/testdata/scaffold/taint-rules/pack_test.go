// Contract test for the example.taint-rules rule pack.
//
// It runs graphi's own conformance harness — the same one graphi proves its
// built-in contributions with. Add this package to a Go module that requires
// github.com/samibel/graphi and run `go test ./...`.
package pack_test

import (
	"testing"

	"github.com/samibel/graphi/engine/extpack/conformance"
)

func TestPackPassesTheGraphiConformanceHarness(t *testing.T) {
	if err := conformance.VerifyPack(".").Err(); err != nil {
		t.Fatalf("example.taint-rules is not conformant:\n%v", err)
	}
}
