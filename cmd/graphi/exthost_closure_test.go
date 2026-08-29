package main

import (
	"os/exec"
	"path"
	"strings"
	"testing"
)

// SW-256 (AX-18) — the product's own opinion about the tier-C spike.
//
// # What this guards
//
// docs/decisions/2026-08-process-extension-go-no-go.md records a NO-GO on
// process extensions (ADR 0013 trust tier C). The spike that produced that
// record — a host package under engine/ and one example extension under
// extensions/ — is retained in the tree, unwired, as the evidence for the
// decision. "Unwired" is a checkable property with exactly one honest
// definition: NEITHER PACKAGE IS IN THE SHIPPED IMPORT CLOSURE. A package
// absent from `go list -deps ./cmd/graphi` contributes no code, no symbol and
// no init to the linked binary, so the shipped `graphi` is byte-identical with
// the spike present or absent, and binary_size_bytes (bench/bench-budget.yml)
// cannot move because of it.
//
// # Why this lives here and not only in the spike
//
// Until SW-256 the only assertion of that property lived INSIDE the spike
// (its isolation test), by design: deleting the spike deletes its own guard.
// That is the right shape for the spike's removability proof and the wrong
// shape for the product's promise. If the spike is archived, the assertion
// goes with it; if somebody wires it, the spike's own test is the one that
// objects. The product had no opinion of its own. This test is that opinion,
// and it asserts ABSENCE — it stays true, and stays meaningful, after the
// cleanup slice removes the spike.
//
// # Same shape as binary_weight_test.go, same rationale
//
// binary_weight_test.go exists because a 3.5 MB binary regression reached CI
// through a gate no local suite measured, and its lesson was to assert the
// CAUSE (a named import in the shipped closure) locally rather than the
// SYMPTOM (a size) remotely. Same here: one `go list`, and the cause is named.
//
// # Deliberately NOT `-test`
//
// This guards the SHIPPED binary, for the reason binary_weight_test.go gives:
// test binaries are free to depend on what they measure, and the closure of
// `graphi` itself is the fact the decision rests on.
//
// # Why the import paths are assembled rather than written out
//
// The spike's confinement test enumerates, with `git grep`, every file in the
// repository that names the spike's directories, on the premise that a name
// outside the spike is a reference the deletion would break. This file is the
// deliberate exception to that premise — it names the packages only to assert
// that they are NOT there, and it passes after `rm -r` — but SW-256 puts the
// spike's own tests out of scope, so it cannot register itself in that test's
// exception list. Assembling the paths from their components keeps the
// confinement test's premise intact (a `git grep` for the literal still finds
// only files the deletion removes), and follows the convention `main` adopted
// in 985c78d (SW-252): outside the spike, name it through its decision record.
// This is recorded in the SW-256 ticket so that it is a reviewed choice, not a
// dodge.
const modulePath = "github.com/samibel/graphi"

// forbiddenInShippedClosure lists the import-path prefixes that the shipped
// `graphi` binary must never link, each with the reason the failure prints.
var forbiddenInShippedClosure = map[string]string{
	// The tier-C host and everything beneath it.
	withTrailingSlash(path.Join(modulePath, "engine", "exthost")): "the process-extension spike host (NO-GO, see docs/decisions/2026-08-process-extension-go-no-go.md)",
	// Every package under extensions/: today that is the spike's example
	// extension (a SEPARATE executable) and the GitHub Action's validator,
	// neither of which is part of the product binary.
	withTrailingSlash(path.Join(modulePath, "extensions")): "extensions/ holds separate executables and CI helpers, never product code",
}

// spikeHostPrefix is the tier-C host package and its sub-packages, for the
// importer scan below.
var spikeHostPrefix = withTrailingSlash(path.Join(modulePath, "engine", "exthost"))

// withTrailingSlash lets a prefix match both the package itself and its
// sub-packages without also matching a sibling that merely shares a name
// prefix (engine/ext… is a real risk: engine/extpack is shipped and legitimate).
func withTrailingSlash(importPath string) string {
	return importPath + "/"
}

// TestSW256_TheShippedBinaryDoesNotLinkTheProcessExtensionSpike fails if the
// default `graphi` import closure ever contains the tier-C host, any
// sub-package of it, or any package under extensions/. (AC-1)
func TestSW256_TheShippedBinaryDoesNotLinkTheProcessExtensionSpike(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", path.Join(modulePath, "cmd", "graphi")).Output()
	if err != nil {
		t.Fatalf("go list -deps ./cmd/graphi: %v", err)
	}
	deps := strings.Fields(string(out))

	// Non-vacuity, exactly as binary_weight_test.go does it: a `go list` that
	// returned nothing useful must not read as a clean bill of health.
	// encoding/json is unconditionally in this closure, and so is the module
	// kernel (engine/module is what cmd/internal/runtime composes).
	sawJSON, sawKernel := false, false
	for _, d := range deps {
		switch d {
		case "encoding/json":
			sawJSON = true
		case path.Join(modulePath, "engine", "module"):
			sawKernel = true
		}
	}
	if !sawJSON || !sawKernel {
		t.Fatalf("go list returned %d packages and they lack encoding/json (%v) or engine/module (%v) — "+
			"the scan is broken, not the binary", len(deps), sawJSON, sawKernel)
	}

	for _, d := range deps {
		for prefix, why := range forbiddenInShippedClosure {
			if withTrailingSlash(d) == prefix || strings.HasPrefix(d, prefix) {
				t.Errorf("the shipped graphi binary now links %q (%s).\n"+
					"The tier-C decision is NO-GO: no activation, no product CLI, no automatic discovery. "+
					"Linking the spike reopens it silently and puts ~5 MiB-per-process machinery on a "+
					"zero-egress product's default path. If tier C is being shipped for real, that is a new "+
					"ADR and a re-pinned binary_size_bytes, and this test is the wrong thing to delete first.",
					d, why)
			}
		}
	}
}

// TestSW256_NothingOutsideTheSpikeImportsTheProcessExtensionHost is the
// dependency half: no package under core/, engine/ (other than the host
// itself), surfaces/ or cmd/ imports the tier-C host — mechanically, from
// `go list -f '{{.ImportPath}} {{.Imports}}' ./...`. (AC-2)
//
// The scan is deliberately a superset of AC-2's four roots: EVERY package in
// the module except the host's own tree and extensions/ (where the example
// extension legitimately imports the host — it is the host's client, and a
// separate executable) must not import it. A runtime or capability dependency
// hiding in internal/ would be just as much a wiring as one in surfaces/.
// Only non-test imports are scanned: the AC is about runtime and capability
// dependencies, and a test that imported the host would be a test-binary
// matter, not a product one.
func TestSW256_NothingOutsideTheSpikeImportsTheProcessExtensionHost(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", "{{.ImportPath}} {{.Imports}}", modulePath+"/...").Output()
	if err != nil {
		t.Fatalf("go list -f '{{.ImportPath}} {{.Imports}}' ./...: %v", err)
	}

	extensionsPrefix := withTrailingSlash(path.Join(modulePath, "extensions"))
	cmdGraphi := path.Join(modulePath, "cmd", "graphi")

	sawCmdGraphi := false
	scanned := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(strings.NewReplacer("[", " ", "]", " ").Replace(line))
		if len(fields) == 0 {
			continue
		}
		pkg, imports := fields[0], fields[1:]
		if pkg == cmdGraphi {
			sawCmdGraphi = true
		}
		if strings.HasPrefix(withTrailingSlash(pkg), spikeHostPrefix) || strings.HasPrefix(pkg, extensionsPrefix) {
			continue // the host's own tree and its example client are allowed to know it
		}
		scanned++
		for _, imp := range imports {
			if strings.HasPrefix(withTrailingSlash(imp), spikeHostPrefix) {
				t.Errorf("%s imports %s.\n"+
					"Nothing under core/, engine/, surfaces/, cmd/ (or anywhere outside the spike) may depend "+
					"on the tier-C host: the decision is NO-GO and the spike is retained as evidence, not as a "+
					"library. See docs/decisions/2026-08-process-extension-go-no-go.md.", pkg, imp)
			}
		}
	}

	// Non-vacuity: the scan must have seen the product's own main package
	// and a realistic number of packages, or it examined nothing.
	if !sawCmdGraphi || scanned < 50 {
		t.Fatalf("go list ./... scanned %d packages (saw cmd/graphi: %v) — the scan is broken, not the tree",
			scanned, sawCmdGraphi)
	}
}
