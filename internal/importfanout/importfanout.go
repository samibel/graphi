// Package importfanout measures the DIRECT internal import fan-out of a package
// in this module: how many distinct `github.com/samibel/graphi/...` packages it
// imports itself (transitive dependencies are deliberately not counted).
//
// It exists for AX-00 (SW-220) AC-5. The extension-kernel plan's central
// structural claim is that `surfaces/client` has become a hub: one interface
// with ~40 methods whose implementation must know about every service in the
// tree. Fan-out is the cheap, mechanical proxy for that claim, and AX-04/AX-07
// are supposed to move the number. Recording it now, with the baseline checked
// in, is what makes "the executor reduced coupling" a measurement instead of an
// impression.
//
// It is explicitly a METRIC, NOT A RATCHET. Nothing here fails a build when the
// number moves. Turning it into a gate is a separate, deliberate decision (the
// SW-220 story puts that out of scope on purpose): a ratchet adopted before
// anyone knows the healthy range mostly teaches people to route around it.
//
// # Why go/ast and not `go list`
//
// `go list -json` is the authoritative answer and is what internal/layerguard
// uses — but layerguard runs as a `go run ./cmd/layerguard` CI binary, where
// invoking the toolchain is free. This metric is read from an ordinary test, and
// a test that shells out to the toolchain is slower, needs a writable module
// cache, and fails for reasons that have nothing to do with the measurement.
// Parsing imports with go/ast is stdlib-only, hermetic and instant.
//
// The one real difference is build tags: `go list` reports the imports of the
// CURRENT build configuration, while this package counts imports declared in
// every non-test .go file of the directory. That difference is intentional here.
// An import hidden behind `//go:build graphi_broad` is still a package this code
// knows about and still has to be untangled by a refactor, so for a COUPLING
// metric counting it is the more honest answer. Test files are excluded: a test
// importing a helper is not production coupling.
//
// Like layerguard and evidence this is UNRANKED tooling — outside
// cmd→surfaces→engine→core, stdlib-only, and no dependency of the shipped
// binary.
package importfanout

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ModulePath is the module every "internal" import is measured against.
const ModulePath = "github.com/samibel/graphi"

// Result is one package's measured fan-out.
type Result struct {
	// Package is the module-relative package path, e.g. "surfaces/client".
	Package string `json:"package"`
	// Fanout is len(Imports) — the headline number.
	Fanout int `json:"fanout"`
	// Imports is the sorted, deduplicated set of directly imported packages
	// inside this module. Recording the SET, not just the count, is what makes a
	// later diff readable: "41 → 38" says nothing about which three went away.
	Imports []string `json:"imports"`
	// Files is the number of non-test .go files that were parsed, so a reader can
	// tell "the number dropped" from "the parser stopped seeing the package".
	Files int `json:"files"`
}

// Measure parses every non-test .go file in dir and returns the direct internal
// import fan-out. pkgPath is the module-relative path recorded in the result
// (e.g. "surfaces/client"); it is descriptive and is not used for resolution.
//
// A directory with no parsable Go files is an error, not a fan-out of zero: a
// silent zero is exactly how a broken measurement disguises itself as progress.
func Measure(dir, pkgPath string) (Result, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Result{}, fmt.Errorf("importfanout: read %s: %w", dir, err)
	}

	fset := token.NewFileSet()
	seen := map[string]bool{}
	files := 0
	prefix := ModulePath + "/"

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ImportsOnly)
		if err != nil {
			return Result{}, fmt.Errorf("importfanout: parse %s: %w", name, err)
		}
		files++
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return Result{}, fmt.Errorf("importfanout: %s: bad import path %s: %w", name, spec.Path.Value, err)
			}
			if strings.HasPrefix(path, prefix) {
				seen[strings.TrimPrefix(path, prefix)] = true
			}
		}
	}

	if files == 0 {
		return Result{}, fmt.Errorf("importfanout: no non-test Go files under %s — the measurement, not the coupling, is what changed", dir)
	}

	imports := make([]string, 0, len(seen))
	for path := range seen {
		imports = append(imports, path)
	}
	sort.Strings(imports)

	return Result{Package: pkgPath, Fanout: len(imports), Imports: imports, Files: files}, nil
}

// Diff describes how a measured result relates to a recorded baseline. It is
// pure description — no verdict, because this metric has no threshold.
type Diff struct {
	Baseline int
	Current  int
	Added    []string
	Removed  []string
}

// Compare returns the description of current against baseline.
func Compare(baseline, current Result) Diff {
	inBaseline := map[string]bool{}
	for _, path := range baseline.Imports {
		inBaseline[path] = true
	}
	inCurrent := map[string]bool{}
	for _, path := range current.Imports {
		inCurrent[path] = true
	}

	d := Diff{Baseline: baseline.Fanout, Current: current.Fanout}
	for _, path := range current.Imports {
		if !inBaseline[path] {
			d.Added = append(d.Added, path)
		}
	}
	for _, path := range baseline.Imports {
		if !inCurrent[path] {
			d.Removed = append(d.Removed, path)
		}
	}
	return d
}

// Format renders a Diff as the one-paragraph report this metric exists to
// produce. It never says PASS or FAIL — only what the number is and what moved.
func (d Diff) Format(pkg string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "import fan-out metric (NON-BLOCKING, no threshold): %s directly imports %d internal packages", pkg, d.Current)
	switch {
	case d.Current == d.Baseline && len(d.Added) == 0 && len(d.Removed) == 0:
		fmt.Fprintf(&b, " — unchanged from the AX-00 baseline (%d).", d.Baseline)
	default:
		fmt.Fprintf(&b, " — baseline was %d (delta %+d).", d.Baseline, d.Current-d.Baseline)
		if len(d.Added) > 0 {
			fmt.Fprintf(&b, "\n  added:   %s", strings.Join(d.Added, ", "))
		}
		if len(d.Removed) > 0 {
			fmt.Fprintf(&b, "\n  removed: %s", strings.Join(d.Removed, ", "))
		}
		b.WriteString("\n  This is a metric, not a gate: a move is information for the AX-04/AX-07 review, not a build failure.")
	}
	return b.String()
}
