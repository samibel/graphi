package jvmgroundtruth

// The drift guard on this package's COPY of engine/jvmresolve's value-class
// recognition rule. See groundtruth.go::valueClassBridgeName for why the rule
// is copied rather than imported (exporting the product symbol moves the
// product binary digest — measured, and the two digests are recorded there).
//
// A copy needs a guard, and a MIRRORED TABLE is not one. Measured on this
// tree, not argued: raise the product rule's suffix minimum from 4 to 6 in
// engine/jvmresolve/hierarchy.go and add NO case anywhere; both table tests —
// engine/jvmresolve's TestValueClassBridgeName and this package's
// TestValueClassBridgeName_MirrorsJVMResolve — stay green, because no case
// either table exercises has a suffix of exactly 4 or 5 base-64 characters
// that the product side is expected to accept. The two rules disagree on
// `foo-abcd` while both tables report ok. A shared fixture would behave the
// same way: a fixture only sees the cases it contains.
//
// What does catch it is SOURCE IDENTITY — comparing the two declarations as
// printed syntax, which is what TestValueClassRule_IdenticalToJVMResolve does.

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"sort"
	"strings"
	"testing"
)

// productRulePath holds the ORIGINAL of the copied rule. It is read as source
// text, not imported: the functions are unexported on both sides.
const productRulePath = "../../engine/jvmresolve/hierarchy.go"

// copyRulePath holds this package's copy.
const copyRulePath = "groundtruth.go"

// copiedRuleFuncs are the declarations that make up the copy. Every one of
// them must be structurally identical on both sides.
//
// The list is hand-written, but it is not hand-TRUSTED:
// TestValueClassRule_CopySetIsClosed asserts that nothing these declarations
// reference at package scope is missing from it, so a fifth helper cannot be
// added on one side and silently escape comparison.
var copiedRuleFuncs = []string{
	"valueClassBridgeName",
	"isBase64Char",
	"isIdentStart",
	"isIdentCont",
}

// TestValueClassRule_IdenticalToJVMResolve is the guard: it parses both files
// and asserts the copied declarations are structurally identical after
// gofmt-printing, with comments and blank lines removed.
//
// Comments are excluded on purpose — the two copies carry deliberately
// different documentation (one explains the rule, the other explains why it is
// a copy), and only the code must agree. Blank lines are excluded because
// nothing semantic can hide in one.
//
// # What this guard does NOT catch — three things, counted on this tree
//
//  1. DIVERGENCE IN THE CALLERS. The two sides use the rule for different jobs
//     (engine/jvmresolve binds a call site whose source text is mangled; this
//     package rewrites a javap-printed declaration name) and nothing here
//     asserts anything about either caller. This is by design and is the
//     largest of the three.
//  2. A SECOND FILE. Each side is named by a fixed path. If either package
//     gained another file declaring one of these functions under a build tag,
//     the guard would keep comparing the two files it names.
//  3. A PREDECLARED IDENTIFIER SHADOWED ON ONE SIDE ONLY. `len`, `byte` and
//     `string` are skipped by the closure check below as predeclared; a
//     package-level declaration shadowing one of them in exactly one of the two
//     packages would change what the identical text means.
//
// Two holes that a source-identity guard has by default are CLOSED here rather
// than listed: the copy set cannot silently miss a helper (test 2 below), and a
// package qualifier cannot resolve to different imports on the two sides (test
// 3 below). A rename or a move of either function is not on the list either —
// it fails loudly, which is the intended direction.
func TestValueClassRule_IdenticalToJVMResolve(t *testing.T) {
	product := ruleDecls(t, productRulePath)
	copied := ruleDecls(t, copyRulePath)

	for _, name := range copiedRuleFuncs {
		p, okP := product[name]
		c, okC := copied[name]
		switch {
		case !okP:
			t.Errorf("%s no longer declares func %s — the copy in %s has nothing to mirror; "+
				"re-point productRulePath or retire the copy", productRulePath, name, copyRulePath)
		case !okC:
			t.Errorf("%s no longer declares func %s — the copy is incomplete", copyRulePath, name)
		case printDecl(t, p) != printDecl(t, c):
			t.Errorf("value-class rule DRIFTED: %s differs between\n  %s (product, authoritative)\n"+
				"  %s (copy)\n--- product ---\n%s\n--- copy ---\n%s",
				name, productRulePath, copyRulePath, printDecl(t, p), printDecl(t, c))
		}
	}
}

// TestValueClassRule_CopySetIsClosed asserts that copiedRuleFuncs is CLOSED
// under package-scope reference: every plain identifier the copied
// declarations reference, on either side, is either a predeclared name or is
// itself in copiedRuleFuncs.
//
// Without this, the guard would be only as good as a hand-maintained list: a
// maintainer who factors a fifth helper out of the product rule and copies it
// here without extending copiedRuleFuncs would leave that helper uncompared,
// and the source-identity test would report green over a rule half of which is
// unguarded. This turns that into a red.
func TestValueClassRule_CopySetIsClosed(t *testing.T) {
	for _, path := range []string{productRulePath, copyRulePath} {
		decls := ruleDecls(t, path)
		imports := importNames(ruleFile(t, path))
		want := map[string]bool{}
		for _, n := range copiedRuleFuncs {
			want[n] = true
		}
		refs := map[string]bool{}
		for _, name := range copiedRuleFuncs {
			fn, ok := decls[name]
			if !ok {
				continue // the identity test above reports the absence
			}
			for _, r := range packageScopeRefs(fn, imports) {
				refs[r] = true
			}
		}
		var missing []string
		for r := range refs {
			if !want[r] {
				missing = append(missing, r)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("%s: the copied rule references %v at package scope, but copiedRuleFuncs does not list them — "+
				"they would be copied without being compared; add them to copiedRuleFuncs (and to the copy)",
				path, missing)
		}
	}
}

// TestValueClassRule_QualifiersResolveAlike asserts that every package
// qualifier the copied declarations use resolves to the SAME import path in
// both files. `strings.Index` printed on both sides is only the same call if
// `strings` is the same package on both sides; an import alias would make the
// identical text mean different things, and source identity alone cannot see
// that.
func TestValueClassRule_QualifiersResolveAlike(t *testing.T) {
	productFile, productDecls := ruleFile(t, productRulePath), ruleDecls(t, productRulePath)
	copyFile, copyDecls := ruleFile(t, copyRulePath), ruleDecls(t, copyRulePath)

	quals := map[string]bool{}
	for _, decls := range []map[string]*ast.FuncDecl{productDecls, copyDecls} {
		for _, name := range copiedRuleFuncs {
			if fn, ok := decls[name]; ok {
				for _, q := range qualifiers(fn) {
					quals[q] = true
				}
			}
		}
	}
	for q := range quals {
		p, okP := importPathFor(productFile, q)
		c, okC := importPathFor(copyFile, q)
		switch {
		case !okP || !okC:
			t.Errorf("qualifier %q resolves to an import in %s=%v, %s=%v — one side does not import it under that name",
				q, productRulePath, okP, copyRulePath, okC)
		case p != c:
			t.Errorf("qualifier %q resolves to DIFFERENT packages: %s imports %s, %s imports %s",
				q, productRulePath, p, copyRulePath, c)
		}
	}
}

// ruleFile parses path with comments discarded.
func ruleFile(t *testing.T, path string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(ruleFset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return f
}

// ruleFset is shared so a printed declaration keeps its original formatting.
var ruleFset = token.NewFileSet()

// ruleDecls returns the copied function declarations found in path, by name.
func ruleDecls(t *testing.T, path string) map[string]*ast.FuncDecl {
	t.Helper()
	wanted := map[string]bool{}
	for _, n := range copiedRuleFuncs {
		wanted[n] = true
	}
	out := map[string]*ast.FuncDecl{}
	for _, d := range ruleFile(t, path).Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !wanted[fn.Name.Name] {
			continue
		}
		out[fn.Name.Name] = fn
	}
	return out
}

// printDecl gofmt-prints fn and drops blank lines.
func printDecl(t *testing.T, fn *ast.FuncDecl) string {
	t.Helper()
	var sb strings.Builder
	if err := printer.Fprint(&sb, ruleFset, fn); err != nil {
		t.Fatalf("print %s: %v", fn.Name.Name, err)
	}
	var keep []string
	for _, line := range strings.Split(sb.String(), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		keep = append(keep, line)
	}
	return strings.Join(keep, "\n")
}

// packageScopeRefs returns the identifiers fn references that are neither
// declared inside fn, nor fn's own name, nor predeclared, nor a package
// qualifier. On this rule the expected answer is the three helper functions.
func packageScopeRefs(fn *ast.FuncDecl, imports map[string]bool) []string {
	local := localNames(fn)
	var out []string
	seen := map[string]bool{}
	walkIdents(fn, func(id *ast.Ident) {
		n := id.Name
		if local[n] || imports[n] || n == fn.Name.Name || predeclared[n] || seen[n] {
			return
		}
		seen[n] = true
		out = append(out, n)
	})
	return out
}

// qualifiers returns the package qualifiers fn uses (the X of a selector whose
// X is an identifier that is not a local).
func qualifiers(fn *ast.FuncDecl) []string {
	local := localNames(fn)
	var out []string
	seen := map[string]bool{}
	ast.Inspect(fn, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && !local[id.Name] && !seen[id.Name] {
			seen[id.Name] = true
			out = append(out, id.Name)
		}
		return true
	})
	return out
}

// walkIdents visits every identifier in fn that is used as a plain reference:
// the selected half of a selector (`Index` in `strings.Index`), field keys,
// declaration names and labels are skipped, because none of them resolves in
// package scope the way a bare identifier does.
func walkIdents(fn *ast.FuncDecl, visit func(*ast.Ident)) {
	var walk func(ast.Node) bool
	walk = func(n ast.Node) bool {
		switch x := n.(type) {
		case nil:
			return false
		case *ast.SelectorExpr:
			ast.Inspect(x.X, walk) // the Sel half never resolves in package scope
			return false
		case *ast.KeyValueExpr:
			ast.Inspect(x.Value, walk) // a struct-literal key is a field name
			return false
		case *ast.FuncDecl:
			ast.Inspect(x.Type, walk)
			if x.Body != nil {
				ast.Inspect(x.Body, walk)
			}
			return false // skip x.Name, the declaration itself
		case *ast.LabeledStmt:
			ast.Inspect(x.Stmt, walk)
			return false
		case *ast.Ident:
			visit(x)
			return false
		}
		return true
	}
	ast.Inspect(fn, walk)
}

// localNames collects every name fn declares itself: parameters, results,
// receiver, short variable declarations, var/const/type declarations in the
// body, and range variables.
func localNames(fn *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	addFields := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, f := range fl.List {
			for _, n := range f.Names {
				out[n.Name] = true
			}
		}
	}
	addFields(fn.Recv)
	if fn.Type != nil {
		addFields(fn.Type.Params)
		addFields(fn.Type.Results)
	}
	ast.Inspect(fn, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			if x.Tok == token.DEFINE {
				for _, lhs := range x.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						out[id.Name] = true
					}
				}
			}
		case *ast.RangeStmt:
			if x.Tok == token.DEFINE {
				for _, e := range []ast.Expr{x.Key, x.Value} {
					if id, ok := e.(*ast.Ident); ok {
						out[id.Name] = true
					}
				}
			}
		case *ast.GenDecl:
			for _, spec := range x.Specs {
				switch s := spec.(type) {
				case *ast.ValueSpec:
					for _, id := range s.Names {
						out[id.Name] = true
					}
				case *ast.TypeSpec:
					out[s.Name.Name] = true
				}
			}
		case *ast.FuncLit:
			addFields(x.Type.Params)
			addFields(x.Type.Results)
		}
		return true
	})
	return out
}

// importNames is the set of names under which file imports packages. An
// identifier in that set is a package qualifier, not a package-scope
// reference: TestValueClassRule_QualifiersResolveAlike is what checks those.
func importNames(file *ast.File) map[string]bool {
	out := map[string]bool{}
	for _, im := range file.Imports {
		path := strings.Trim(im.Path.Value, `"`)
		name := path
		if i := strings.LastIndex(path, "/"); i >= 0 {
			name = path[i+1:]
		}
		if im.Name != nil {
			name = im.Name.Name
		}
		out[name] = true
	}
	return out
}

// importPathFor resolves a qualifier to the import path it names in file.
func importPathFor(file *ast.File, qual string) (string, bool) {
	for _, im := range file.Imports {
		path := strings.Trim(im.Path.Value, `"`)
		name := path
		if i := strings.LastIndex(path, "/"); i >= 0 {
			name = path[i+1:]
		}
		if im.Name != nil {
			name = im.Name.Name
		}
		if name == qual {
			return path, true
		}
	}
	return "", false
}

// predeclared is Go's universe block, the names that resolve without any
// declaration in either package.
var predeclared = map[string]bool{
	"bool": true, "byte": true, "complex64": true, "complex128": true,
	"error": true, "float32": true, "float64": true, "int": true, "int8": true,
	"int16": true, "int32": true, "int64": true, "rune": true, "string": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uintptr": true, "any": true, "comparable": true,
	"true": true, "false": true, "iota": true, "nil": true,
	"append": true, "cap": true, "clear": true, "close": true, "complex": true,
	"copy": true, "delete": true, "imag": true, "len": true, "make": true,
	"max": true, "min": true, "new": true, "panic": true, "print": true,
	"println": true, "real": true, "recover": true, "_": true,
}
