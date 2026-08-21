// Package gobindrate is the CI-ONLY measurement harness for the SW-187 Go
// confirmed-tier binding rate (docs/rc/go-binding-rate.md). It NEVER ships
// in the product: the product must stay CGo-free, and this package imports
// engine/typeresolve + core/parse purely for the measurement. It lives under
// internal/ and is driven only by tests and the cmd/gobindrate CLI; cmd/graphi
// does not import it, so AC-7 (cmd/graphi byte-identical before and after)
// holds.
//
// The contract it enforces is SW-187 AC-1..AC-4: a published binding rate
// (bound call sites ÷ CST call sites), with the denominator counted from the
// parse tree independently of the binder, a closed 11-row skip histogram,
// and a two-run byte-identical rendered report. The method mirrors SW-175's
// JVM bind-rate measurement, re-targeted to Go's grammar.
//
// This file: the independent CST denominator (walkASTCounts). NO binder, NO
// types, NO scopes — it counts every *ast.CallExpr, excluding _test.go files,
// because the resolver's source-set deliberately excludes them
// (engine/typeresolve/pkggraph.go:103). A construct that would be added by a
// heuristic binder is NOT here; a call inside a test file is NOT here.
package gobindrate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// Denominator is the CST call-site count produced by WalkASTCounts: every
// *ast.CallExpr in the source set the resolver would see (non-test .go files
// at the top of the repo), recursively walked, nested calls counted
// separately (so l.get(0).length() is two call sites, matching the binder).
type Denominator struct {
	// CallSites is the recursive count of *ast.CallExpr nodes.
	CallSites int
	// Files is the number of .go files the walker visited.
	Files int
	// ParseFailures is the number of files go/parser rejected. The published
	// Go corpus has zero parse failures; a non-zero count is reported as a
	// finding, never silently rounded away.
	ParseFailures int
}

// WalkASTCounts counts every *ast.CallExpr in the files map (path → bytes),
// excluding _test.go files. files is the same map engine/typeresolve.Resolve
// takes, so the denominator matches the resolver's source set by construction.
func WalkASTCounts(files map[string][]byte) Denominator {
	var d Denominator
	for name, src := range files {
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		d.Files++
		fset := token.NewFileSet()
		if _, err := parser.ParseFile(fset, name, src, 0); err != nil {
			d.ParseFailures++
			continue
		}
		f, _ := parser.ParseFile(fset, name, src, 0)
		ast.Inspect(f, func(n ast.Node) bool {
			if _, ok := n.(*ast.CallExpr); ok {
				d.CallSites++
			}
			return true
		})
	}
	return d
}
