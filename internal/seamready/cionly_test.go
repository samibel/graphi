package seamready_test

// SW-254 AC-6 — the readiness tool is UNRANKED tooling and must stay that way.
//
// It reads CI run ids, test symbols and a threshold that lives in a yaml file
// under docs/, none of which belongs in a shipped binary. "It lives under
// internal/ and only cmd/seamready imports it" is a convention, and a
// convention is not a guard — so the property is asserted against the real
// dependency graph the linker uses, the way internal/jvmgroundtruth's
// cionly_test.go asserts the JVM oracle stays out of the product.

import (
	"os/exec"
	"strings"
	"testing"
)

const toolPkg = "github.com/samibel/graphi/internal/seamready"

// TestAX14_ToolIsAbsentFromTheShippedBinary asserts nothing under cmd/graphi,
// core/, engine/ or surfaces/ depends on this package — transitively, through
// `go list -deps`, which is the same closure the compiler links.
func TestAX14_ToolIsAbsentFromTheShippedBinary(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain unavailable")
	}
	// Module-qualified, never `./cmd/graphi`: a test's working directory is its
	// own package directory, so a relative pattern resolves to nothing.
	for _, target := range []string{
		"github.com/samibel/graphi/cmd/graphi",
		"github.com/samibel/graphi/surfaces/...",
		"github.com/samibel/graphi/engine/...",
		"github.com/samibel/graphi/core/...",
	} {
		out, err := exec.Command("go", "list", "-deps", target).CombinedOutput()
		if err != nil {
			t.Fatalf("go list -deps %s: %v\n%s", target, err, out)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.TrimSpace(line) == toolPkg {
				t.Fatalf("%s depends on %s — unranked tooling would ship", target, toolPkg)
			}
		}
	}
}
