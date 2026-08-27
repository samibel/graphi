package main

import (
	"os/exec"
	"strings"
	"testing"
)

// SW-230 round 1 — the gate that would have caught the defect that retracted
// this story's approval.
//
// # What happened
//
// SW-230's first round used text/template to render four scaffold files. The
// shipped binary grew 3,459,504 bytes and blew binary_size_bytes
// (bench/bench-budget.yml). 3,322,416 of those bytes — 96% — were text/template,
// and NOT because the templating engine is large. text/template reaches struct
// fields through reflect.Value.MethodByName, and the Go linker switches OFF dead
// method elimination for the WHOLE PROGRAM as soon as that call is reachable:
// every exported method of every reachable type is retained. In a binary this
// size the multiplier is megabytes. The conformance harness that was suspected
// of the weight cost 34,240 bytes — 1%.
//
// # Why a `go list` test and not a size assertion
//
// binary_size_bytes is a CI-only gate: it needs a release build and a linux
// runner, so testgate, layerguard, coverage and race all ran green over a binary
// that was 7% over budget. A size number cannot be asserted here — the local
// build is a different GOOS/GOARCH and a different absolute figure. The CAUSE
// can be: reflective templating is a named, enumerable set of packages, and its
// presence in the shipped import closure is exactly the signal. This test costs
// one `go list` and moves the discovery from a CI failure to a local one.
//
// # Deliberately NOT `-test`
//
// This guards the SHIPPED binary. Test binaries are free to use text/template —
// cmd/gen-install, cmd/gen-packaging and internal/evalreport all do, and none of
// them ships. Passing -test here would forbid that for no benefit. (Contrast
// internal/parity's TestNoIngestInProcess, where -test IS the point because the
// forbidden dependency would be hidden in the test binary it measures.)
var reflectiveTemplatePackages = map[string]string{
	"text/template":       "reaches values via reflect.Value.MethodByName",
	"text/template/parse": "the parser half of text/template",
	"html/template":       "wraps text/template, same reflective value walk",
}

// TestSW230_TheShippedBinaryLinksNoReflectiveTemplateEngine fails if the default
// `graphi` import closure grows one of them back.
//
// If you need to render text in a shipped verb, do what engine/extpack's
// scaffold does: substitute pre-formatted strings (extpack.renderScaffold), and
// make the renderer refuse constructs it does not implement. If a real template
// engine genuinely becomes necessary, this list is the place to record the
// decision — and re-pin binary_size_bytes in the same change, with the measured
// number, rather than discovering it in release-gate.
func TestSW230_TheShippedBinaryLinksNoReflectiveTemplateEngine(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/samibel/graphi/cmd/graphi").Output()
	if err != nil {
		t.Fatalf("go list -deps ./cmd/graphi: %v", err)
	}
	deps := strings.Fields(string(out))

	// Non-vacuity: a `go list` that returned nothing useful must not read as a
	// clean bill of health. encoding/json is unconditionally in this closure.
	sawControl := false
	for _, d := range deps {
		if d == "encoding/json" {
			sawControl = true
		}
	}
	if !sawControl {
		t.Fatalf("go list returned %d packages and none of them was encoding/json — "+
			"the scan is broken, not the binary", len(deps))
	}

	for _, d := range deps {
		if why, forbidden := reflectiveTemplatePackages[d]; forbidden {
			t.Errorf("the shipped graphi binary now links %q (%s).\n"+
				"That defeats the linker's dead-method elimination program-wide: measured at "+
				"+3,322,416 bytes when SW-230 did it, which broke binary_size_bytes in release-gate.\n"+
				"Render with string substitution instead (see engine/extpack/scaffold.go), or make this "+
				"a deliberate, re-pinned budget decision.", d, why)
		}
	}
}
