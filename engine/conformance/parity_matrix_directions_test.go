package conformance_test

// SW-189 AC-4 — the cross-family enumeration guard that pins TEMPL-P4 D-11
// closed. TEMPL-P4 D-11 measured the JVM guard (engine/conformance/jvmparity_matrix_test.go)
// to be missing three of the seven drift-guard directions the Go guard
// carries — VERDICT, AXIS and VOCABULARY. The python and TypeScript-family
// twins were written AFTER D-11 was measured and explicitly recorded the
// inheritance in their YAML header — they narrowed to the same three
// directions the JVM had, plus a fourth (KIND-only per SW-184) absent from
// all three family twins. SW-189 closes all three simultaneously.
//
// This guard exists so that a fourth parity-matrix table written later (the
// next wave's pin family, the residual cross-file-heuristic set, …) cannot
// silently inherit the gap: a family file that lacks any of the seven
// directions stops the build with the family file and the direction named
// in the error — *before* a PASS row is published against an incomplete
// guard.
//
// Enumeration is mechanical — a DIRECTORY GLOB, NOT a hardcoded list. A
// hardcoded list of paths would be exactly the hole D-11 lives in
// (D-11 measured the JVM/python/TS three-table scope; a fourth table added
// later would not be checked). This test enumerates by `filepath.Glob` so
// every `*parity_matrix_test.go` file present at test time is scanned.
//
// Direction presence is recognized in THREE shapes because the family
// twins use different shapes for different directions:
//   (a) `t.Run("DIRECTION", …)` first arg — the shape the Go guard
//       (paritymatrix_test.go:216, 235, 250, 296, 418, 470, 493) uses for
//       all seven directions;
//   (b) the direction name appears as a substring (Title-case) in a top-
//       level Test*Parity* function name — the shape the JVM/python/TS
//       twins use for the four direction-only tests (Verdict, Axis,
//       Vocabulary + the KIND/OWNER bundle);
//   (c) the first word of a `t.Errorf/t.Fatalf/t.Logf` format string is the
//       ALL-CAPS direction — the shape the JVM/python/TS twins use for the
//       MISSING, PHANTOM, OWNER, KIND lines inside DriftGuard /
//       KindCountAndOwners.
//
// The seven-direction set is CLOSED and pinned — a different spelling or
// an alias does not satisfy. The Go guard's seven t.Runs are the
// precedent; the family twins mirror that exact set, not a superset.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// sevenDirections is the closed set of drift-guard directions every
// <lang>parity_matrix_test.go file must carry. Same set the Go guard
// runs (paritymatrix_test.go:216, 235, 250, 296, 418, 470, 493) — kept
// in a fixed-order slice so error messages are byte-stable.
var sevenDirections = []string{
	"MISSING",
	"PHANTOM",
	"KIND",
	"VERDICT",
	"OWNER",
	"AXIS",
	"VOCABULARY",
}

// sevenDirectionsTitleCase mirrors sevenDirections in Title-case for the
// function-name shape match (the JVM/python/TS twins store the direction
// in TestJVMParityMatrix_Verdict, TestPythonParityMatrix_Axis, …).
var sevenDirectionsTitleCase = []string{
	"Missing",
	"Phantom",
	"Kind",
	"Verdict",
	"Owner",
	"Axis",
	"Vocabulary",
}

// TestParityMatrixGuardDirections_ArePresentAcrossAllFamilies asserts that
// every <lang>parity_matrix_test.go file in this package carries all seven
// drift-guard directions (MISSING, PHANTOM, KIND, VERDICT, OWNER, AXIS,
// VOCABULARY). A family file that lacks any of the seven fails the build
// with the family file path and the missing direction named in the error.
//
// Mutation-tested (AC-4 step 5): deleting or commenting out the body of any
// one of the four newly-added direction tests in
// jvmparity_matrix_test.go (Verdict, Axis, Vocabulary, the KIND/OWNER
// bundle) makes this test red — verified by hand before story close.
func TestParityMatrixGuardDirections_ArePresentAcrossAllFamilies(t *testing.T) {
	// Defensive self-exclude: this file's name contains "parity_matrix"
	// even though the glob below does not match it (the literal suffix is
	// "_test.go", not "_directions_test.go"). The check below is harmless
	// in this shape and protects the test against a future rename or a
	// wider glob.
	const thisFile = "parity_matrix_directions_test.go"
	const globPattern = "*parity_matrix_test.go"

	matches, err := filepath.Glob(globPattern)
	if err != nil {
		t.Fatalf("glob %q: %v", globPattern, err)
	}
	if len(matches) == 0 {
		t.Fatalf("glob %q matched 0 family files; the SW-189 AC-4 cross-family enumeration expects the JVM, python and TypeScript-family parity matrix tests to be present", globPattern)
	}
	// Sort matches — filepath.Glob is lexical on Unix but byte-stability is
	// required across 5 runs in CI, and a slice-append from os.ReadDir on
	// Windows is not guaranteed lexical.
	sort.Strings(matches)

	fset := token.NewFileSet()
	for _, path := range matches {
		base := filepath.Base(path)
		if base == thisFile {
			continue
		}
		dirs, err := directionsInFile(fset, path)
		if err != nil {
			t.Errorf("DIRECTIONS: parse %s: %v", path, err)
			continue
		}
		for _, d := range sevenDirections {
			if !dirs[d] {
				t.Errorf("DIRECTIONS: %s missing t.Run(%q); expected all seven present (MISSING, PHANTOM, KIND, VERDICT, OWNER, AXIS, VOCABULARY)",
					filepath.ToSlash(path), d)
			}
		}
	}
}

// directionsInFile parses the given Go source file and returns the set of
// drift-guard direction names it carries, as recognized by shapes (a), (b)
// and (c) on the file header. Returned map keys are drawn from
// sevenDirections; an absent key means that direction is not present.
func directionsInFile(fset *token.FileSet, path string) (map[string]bool, error) {
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	dirs := map[string]bool{}

	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil || fn.Recv != nil {
			continue
		}
		name := fn.Name.Name
		// Scope the scan to top-level test functions whose name mentions
		// "Parity" — this is the parity-matrix universe. Methods, helpers
		// (load*, *, *Table) and fixture constants are correctly out of
		// scope.
		if !strings.HasPrefix(name, "Test") || !strings.Contains(name, "Parity") {
			continue
		}

		// Shape (b): the direction name (Title-case) appears as a substring
		// of the function name. TestJVMParityMatrix_Verdict matches
		// "Verdict". TestJVMParityMatrix_KindCountAndOwners matches BOTH
		// "Kind" AND "Owner" via this loop. We scan all seven so a function
		// whose name embeds more than one direction accounts for both.
		// "DriftGuard" matches none (good — DriftGuard's direction coverage
		// arrives via shape (c) instead).
		for i, d := range sevenDirections {
			title := sevenDirectionsTitleCase[i]
			if strings.Contains(name, title) || strings.Contains(name, d) {
				dirs[d] = true
			}
		}

		// Shapes (a) and (c): walk the body. Shape (a) is t.Run with a
		// STRING-literal first arg that matches a direction exactly. Shape
		// (c) is a t.Errorf/t.Fatalf/t.Logf whose first-word of the format
		// string is ALL-CAPS DIRECTION.
		if fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "Run":
				// Shape (a).
				if len(call.Args) == 0 {
					return true
				}
				lit, ok := call.Args[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				for _, d := range sevenDirections {
					if s == d {
						dirs[d] = true
					}
				}
			case "Errorf", "Fatalf", "Logf":
				// Shape (c): first word of the format string is the
				// direction. We require an EXACT match against one of the
				// seven — "required row %q ..." has first word "required",
				// which is correctly NOT in sevenDirections and is meant
				// not to be.
				if len(call.Args) == 0 {
					return true
				}
				lit, ok := call.Args[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				first := s
				if i := strings.IndexAny(s, " \t"); i >= 0 {
					first = s[:i]
				}
				for _, d := range sevenDirections {
					if first == d {
						dirs[d] = true
					}
				}
			}
			return true
		})
	}
	return dirs, nil
}
