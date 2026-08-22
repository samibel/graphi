package jvmgroundtruth_test

// SW-172 AC-6 — the export seam is CI-ONLY and must stay that way.
//
// The whole harness is a soundness ORACLE, not a product feature: it drives a
// JDK, it exists to accuse the binder, and nothing about it belongs in a
// shipped binary that must stay CGo-free and toolchain-free. "It lives under
// internal/ and only tests import it" is a convention, and a convention is not
// a guard — a single `import` in a surface would ship it, silently. So the
// property is asserted mechanically, against the real dependency graph the
// linker uses.

import (
	"os/exec"
	"strings"
	"testing"
)

const oraclePkg = "github.com/samibel/graphi/internal/jvmgroundtruth"

// TestOracleIsAbsentFromTheShippedBinary asserts the shipped command does not
// depend on this package — transitively, through `go list -deps`, which is the
// same closure the compiler links.
func TestOracleIsAbsentFromTheShippedBinary(t *testing.T) {
	// Module-qualified, never `./cmd/graphi`: a test's working directory is its
	// own package directory, so a relative pattern resolves to nothing and the
	// command fails — which, with a skip on error, would silently disarm this
	// guard. Only a MISSING toolchain skips; anything else fails.
	for _, target := range []string{
		"github.com/samibel/graphi/cmd/...",
		"github.com/samibel/graphi/surfaces/...",
		"github.com/samibel/graphi/engine/...",
		"github.com/samibel/graphi/core/...",
	} {
		if _, err := exec.LookPath("go"); err != nil {
			t.Skip("go toolchain unavailable")
		}
		out, err := exec.Command("go", "list", "-deps", target).CombinedOutput()
		if err != nil {
			t.Fatalf("go list -deps %s: %v\n%s", target, err, out)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.TrimSpace(line) == oraclePkg {
				t.Fatalf("%s depends on %s — the CI-only oracle would ship", target, oraclePkg)
			}
		}
	}
}

// TestOracleImportsNoShippedSurface is the other direction, and the one that
// matters for drift: the export reads the binder through engine/jvmresolve's
// PUBLIC api and core/model, and nothing else. If it ever reached for a
// surface or a command, it would stop measuring the product and start
// measuring a private copy of it.
func TestOracleImportsNoShippedSurface(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", "{{join .Imports \"\\n\"}}", oraclePkg).Output()
	if err != nil {
		t.Skipf("go list unavailable in this environment: %v", err)
	}
	allowed := map[string]bool{
		"github.com/samibel/graphi/engine/jvmresolve": true,
		"github.com/samibel/graphi/core/model":        true,
	}
	for _, imp := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		imp = strings.TrimSpace(imp)
		if imp == "" || !strings.HasPrefix(imp, "github.com/samibel/graphi/") {
			continue // stdlib
		}
		if !allowed[imp] {
			t.Fatalf("the oracle imports %s; it may read only the binder's public api and core/model", imp)
		}
	}
}
