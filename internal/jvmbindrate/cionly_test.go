package jvmbindrate_test

import (
	"os/exec"
	"strings"
	"testing"
)

// SW-175 — the binding-rate harness is CI-ONLY and must stay that way.
//
// "It lives under internal/ and only tests import it" is a convention, and a
// convention is not a guard: one import in a surface would ship a measurement
// harness inside a product binary that must stay small, CGo-free and
// toolchain-free. The property is therefore asserted against the real
// dependency graph the linker uses, exactly as internal/jvmgroundtruth does.
const bindratePkg = "github.com/samibel/graphi/internal/jvmbindrate"

func TestHarnessIsAbsentFromTheShippedBinary(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain unavailable")
	}
	// Module-qualified, never a relative pattern: a test's working directory
	// is its own package directory, so `./cmd/...` would resolve to nothing
	// and — with a skip on error — silently disarm this guard.
	for _, target := range []string{
		"github.com/samibel/graphi/cmd/...",
		"github.com/samibel/graphi/surfaces/...",
		"github.com/samibel/graphi/engine/...",
		"github.com/samibel/graphi/core/...",
	} {
		out, err := exec.Command("go", "list", "-deps", target).CombinedOutput()
		if err != nil {
			t.Fatalf("go list -deps %s: %v\n%s", target, err, out)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.TrimSpace(line) == bindratePkg {
				t.Fatalf("%s depends on %s — the CI-only harness would ship", target, bindratePkg)
			}
		}
	}
}

// TestHarnessMeasuresTheProductNotACopy is the other direction, and it is the
// one that matters for drift. The whole value of the published rate is that its
// NUMERATOR is the shipped binder's own output. If this package ever grew its
// own table builder or its own body walker it would start measuring a private
// reimplementation and the figure would silently stop being about the product.
//
// So the import set is pinned: engine/jvmresolve (the binder's public API) and
// the tree-sitter grammars (the denominator's parse), and nothing else from
// this module.
func TestHarnessMeasuresTheProductNotACopy(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", "{{join .Imports \"\\n\"}}", bindratePkg).Output()
	if err != nil {
		t.Skipf("go list unavailable in this environment: %v", err)
	}
	allowed := map[string]bool{
		"github.com/samibel/graphi/engine/jvmresolve": true,
	}
	sawBinder := false
	for _, imp := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		imp = strings.TrimSpace(imp)
		if imp == "" || !strings.HasPrefix(imp, "github.com/samibel/graphi/") {
			continue
		}
		if !allowed[imp] {
			t.Errorf("unexpected in-module import %q — the harness must read the binder's public API and nothing else, or it stops measuring the product", imp)
		}
		if imp == "github.com/samibel/graphi/engine/jvmresolve" {
			sawBinder = true
		}
	}
	if !sawBinder {
		t.Fatal("the harness does not import engine/jvmresolve — its numerator cannot be the shipped binder's")
	}
}
