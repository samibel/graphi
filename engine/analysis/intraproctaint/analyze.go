// Package intraproctaint implements graphi's intra-procedural taint dataflow: a
// pure, offline, per-function analysis that connects a taint SOURCE to a taint
// SINK INSIDE a single Go function, with enough precision to NOT flag sanitized
// or constant-argument paths.
//
// Layering: this is an ENGINE-level analysis. It may import go/ast, go/token and
// engine/analysis/taint (the shared source/sink/sanitizer Config + the Finding
// type). It must NOT be imported by core/parse (which stays a pure leaf that
// cannot depend on taint). The function is a PURE function of (file, fset, cfg):
// identical input always yields the identical, canonically-sorted findings.
//
// Model (conservative, syntactic):
//   - Taint roots: a parameter whose declared type matches a source pattern
//     (e.g. r *http.Request), plus any call whose resolved qualified name matches
//     a source pattern (e.g. os.Getenv). Taint is syntactic: ANY expression that
//     uses a tainted identifier is tainted, so r.URL.Query().Get("id") is tainted
//     purely because it uses the tainted param r — no offline resolution of the
//     unresolvable .URL.Query chain is needed.
//   - Propagation: assignments carry taint from a tainted RHS to their LHS vars;
//     a sanitizer call (strconv.Atoi, ...) applied to tainted input yields a CLEAN
//     result; a call to a non-sanitizer/non-sink function is taint-preserving (its
//     result is tainted if any argument is tainted), which propagates through
//     local wrapper sources like GetQueryParam.
//   - Sinks: a call whose resolved qualified name matches a sink pattern emits a
//     Finding iff ANY argument uses a tainted identifier. A constant-only argument
//     list (exec.Command("uptime")) is never a finding.
package intraproctaint

import (
	"fmt"
	"go/ast"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis/taint"
)

// Analyze runs the per-function intra-procedural taint dataflow over one parsed
// Go file and returns the source→sink findings it proves, canonically sorted. It
// is pure: no I/O, no globals, deterministic. file/fset are the go/ast handle for
// a single file (as produced by core/parse); cfg supplies the source/sink/
// sanitizer patterns (typically taint.DefaultConfig()).
func Analyze(file *ast.File, fset *token.FileSet, cfg taint.Config) []taint.Finding {
	if file == nil || fset == nil {
		return nil
	}
	aliases := aliasToPath(file)
	pkg := ""
	if file.Name != nil {
		pkg = file.Name.Name
	}
	var findings []taint.Finding
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		fa := &funcAnalyzer{
			cfg:       cfg,
			fset:      fset,
			aliases:   aliases,
			pkg:       pkg,
			funcQN:    funcQualifiedName(pkg, fn),
			recvTypes: receiverTypes(fn, aliases),
			tainted:   map[string]sourceRef{},
			cfgHash:   cfg.ContentHash,
		}
		findings = append(findings, fa.run(fn)...)
	}
	sortFindings(findings)
	return findings
}

// sourceRef is the representative origin of a tainted value: enough provenance to
// name the source in a Finding.
type sourceRef struct {
	name  string
	defID string
	label string
	path  string
	line  int
}

// funcAnalyzer holds the per-function analysis state.
type funcAnalyzer struct {
	cfg       taint.Config
	fset      *token.FileSet
	aliases   map[string]string // import alias -> import path
	pkg       string
	funcQN    string
	recvTypes map[string]string    // param/receiver var -> "<importPath>.<TypeName>"
	tainted   map[string]sourceRef // in-scope tainted variables -> origin
	cfgHash   string
}

// run seeds the taint roots from the function signature and walks the body in
// source order, emitting a Finding at each sink reached by a tainted argument.
func (a *funcAnalyzer) run(fn *ast.FuncDecl) []taint.Finding {
	// Seed parameter-rooted sources: a parameter whose declared type matches a
	// source pattern makes the parameter VARIABLE a taint root.
	if fn.Type != nil && fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			qn, ok := typeRef(field.Type, a.aliases)
			if !ok {
				continue
			}
			label, srcID := a.cfg.MatchSource("parameter", qn)
			if label == "" {
				continue
			}
			for _, nm := range field.Names {
				if nm == nil || nm.Name == "" || nm.Name == "_" {
					continue
				}
				line, _ := a.pos(nm.Pos())
				a.tainted[nm.Name] = sourceRef{
					name:  srcID + ":" + nm.Name,
					defID: srcID,
					label: label,
					path:  a.file(nm.Pos()),
					line:  line,
				}
			}
		}
	}

	var findings []taint.Finding
	// Walk the body in source order. ast.Inspect is pre-order, so an assignment
	// is applied before its RHS children are visited; sink arguments only ever
	// reference variables bound by EARLIER statements, so the straight-line taint
	// state is correct at each sink for the fixtures' shapes.
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			a.applyAssign(node)
		case *ast.CallExpr:
			if f, ok := a.sinkFinding(node); ok {
				findings = append(findings, f)
			}
		}
		return true
	})
	return findings
}

// applyAssign propagates taint from an assignment's RHS to its LHS variables. A
// tainted RHS taints the LHS; a clean (or sanitized) RHS clears it.
func (a *funcAnalyzer) applyAssign(as *ast.AssignStmt) {
	if len(as.Rhs) == len(as.Lhs) {
		for i, lhs := range as.Lhs {
			a.assignOne(lhs, as.Rhs[i])
		}
		return
	}
	if len(as.Rhs) == 1 {
		// Multi-value assignment from a single call (a, b := f()): every LHS var
		// takes the call's taint status.
		src, tainted := a.taintOrigin(as.Rhs[0])
		for _, lhs := range as.Lhs {
			a.setVar(lhs, src, tainted)
		}
	}
}

func (a *funcAnalyzer) assignOne(lhs, rhs ast.Expr) {
	src, tainted := a.taintOrigin(rhs)
	a.setVar(lhs, src, tainted)
}

// setVar taints or clears a single LHS identifier.
func (a *funcAnalyzer) setVar(lhs ast.Expr, src sourceRef, tainted bool) {
	id, ok := lhs.(*ast.Ident)
	if !ok || id.Name == "" || id.Name == "_" {
		return
	}
	if tainted {
		a.tainted[id.Name] = src
	} else {
		delete(a.tainted, id.Name)
	}
}

// taintOrigin reports whether expr is tainted and, if so, a representative source
// origin. It is the conservative use-based taint judgement: a sanitizer call
// breaks taint; a source call introduces it; any other call is taint-preserving
// (result tainted if any argument is tainted), which carries taint through local
// wrapper sources.
func (a *funcAnalyzer) taintOrigin(expr ast.Expr) (sourceRef, bool) {
	switch e := expr.(type) {
	case nil:
		return sourceRef{}, false
	case *ast.Ident:
		if s, ok := a.tainted[e.Name]; ok {
			return s, true
		}
	case *ast.ParenExpr:
		return a.taintOrigin(e.X)
	case *ast.StarExpr:
		return a.taintOrigin(e.X)
	case *ast.UnaryExpr:
		return a.taintOrigin(e.X)
	case *ast.SelectorExpr:
		return a.taintOrigin(e.X)
	case *ast.IndexExpr:
		if s, ok := a.taintOrigin(e.X); ok {
			return s, true
		}
		return a.taintOrigin(e.Index)
	case *ast.SliceExpr:
		for _, sub := range []ast.Expr{e.X, e.Low, e.High, e.Max} {
			if s, ok := a.taintOrigin(sub); ok {
				return s, true
			}
		}
	case *ast.BinaryExpr:
		if s, ok := a.taintOrigin(e.X); ok {
			return s, true
		}
		return a.taintOrigin(e.Y)
	case *ast.TypeAssertExpr:
		return a.taintOrigin(e.X)
	case *ast.CompositeLit:
		for _, el := range e.Elts {
			if s, ok := a.taintOrigin(el); ok {
				return s, true
			}
		}
	case *ast.KeyValueExpr:
		return a.taintOrigin(e.Value)
	case *ast.CallExpr:
		if qn, ok := a.callQN(e); ok {
			// A sanitizer call removes taint: its result is CLEAN regardless of a
			// tainted input (this is what makes safeSanitized produce no finding).
			if _, isSan := a.cfg.MatchSanitizer("call", qn); isSan {
				return sourceRef{}, false
			}
			// A call whose resolved QN matches a source pattern (os.Getenv, ...)
			// introduces taint on its result.
			if label, srcID := a.cfg.MatchSource("call", qn); label != "" {
				line, _ := a.pos(e.Pos())
				return sourceRef{name: srcID + ":" + qn, defID: srcID, label: label, path: a.file(e.Pos()), line: line}, true
			}
		}
		// Taint-preserving: any tainted argument (or a tainted receiver/func expr)
		// makes the call result tainted — carries taint through wrapper functions
		// like GetQueryParam(r, ...).
		if s, ok := a.taintOrigin(e.Fun); ok {
			return s, true
		}
		for _, arg := range e.Args {
			if s, ok := a.taintOrigin(arg); ok {
				return s, true
			}
		}
	}
	return sourceRef{}, false
}

// sinkFinding emits a Finding when call is a sink whose argument list carries
// taint. A sink with only constant/clean arguments (exec.Command("uptime"))
// produces nothing.
func (a *funcAnalyzer) sinkFinding(call *ast.CallExpr) (taint.Finding, bool) {
	qn, ok := a.callQN(call)
	if !ok {
		return taint.Finding{}, false
	}
	sinkID, category := a.cfg.MatchSink("call", qn)
	if category == "" {
		return taint.Finding{}, false
	}
	for _, arg := range call.Args {
		src, tainted := a.taintOrigin(arg)
		if !tainted {
			continue
		}
		sinkLine, _ := a.pos(call.Pos())
		sinkPath := a.file(call.Pos())
		labels := taint.NewLabelSet(src.label)
		srcNode := model.NodeId(fmt.Sprintf("intraproc:%s:%s:%d:src", a.funcQN, src.path, src.line))
		sinkNode := model.NodeId(fmt.Sprintf("intraproc:%s:%s:%d:sink", a.funcQN, sinkPath, sinkLine))
		sourceName := src.name
		sinkName := fmt.Sprintf("%s@%s:%d %s", a.funcQN, sinkPath, sinkLine, qn)
		f := taint.Finding{
			SourceID:     srcNode,
			SourceName:   sourceName,
			SourceDefID:  src.defID,
			SinkID:       sinkNode,
			SinkName:     sinkName,
			SinkDefID:    sinkID,
			SinkCategory: category,
			Labels:       labels,
			ConfigHash:   a.cfgHash,
			Path: []taint.PathStep{
				{
					NodeID:        srcNode,
					Kind:          "source",
					QualifiedName: a.funcQN + " (source " + sourceName + ")",
					SourcePath:    src.path,
					Line:          src.line,
					Labels:        labels,
					Reason:        "intra-procedural taint root",
				},
				{
					NodeID:        sinkNode,
					Kind:          "sink",
					QualifiedName: a.funcQN + " -> " + qn,
					SourcePath:    sinkPath,
					Line:          sinkLine,
					Labels:        labels,
					EdgeKind:      "intraproc_dataflow",
					Reason:        "tainted argument reaches sink",
				},
			},
			PathLength: 2,
		}
		return f, true
	}
	return taint.Finding{}, false
}

// callQN resolves a call expression to a qualified name the taint Config can
// match: a package function (alias.Fn -> "<importPath>.<Fn>") or a receiver
// method on a typed variable (recv.Method -> "<receiverType>.<Method>", e.g.
// db.Query -> "database/sql.DB.Query"). A local (unqualified) function is
// qualified with the file's package name. Anything else (chained-call receivers,
// index expressions) is unresolvable offline and returns ok=false.
func (a *funcAnalyzer) callQN(call *ast.CallExpr) (string, bool) {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if a.pkg == "" {
			return "", false
		}
		return a.pkg + "." + fun.Name, true
	case *ast.SelectorExpr:
		base, ok := fun.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		if impPath, ok := a.aliases[base.Name]; ok {
			return impPath + "." + fun.Sel.Name, true
		}
		if rt, ok := a.recvTypes[base.Name]; ok {
			return rt + "." + fun.Sel.Name, true
		}
	}
	return "", false
}

func (a *funcAnalyzer) pos(p token.Pos) (line, col int) {
	if !p.IsValid() {
		return 0, 0
	}
	pp := a.fset.Position(p)
	return pp.Line, pp.Column
}

func (a *funcAnalyzer) file(p token.Pos) string {
	if !p.IsValid() {
		return ""
	}
	return a.fset.Position(p).Filename
}

// funcQualifiedName builds a stable name for the enclosing function: "<pkg>.Fn"
// for a plain function, "<pkg>.Recv.Method" for a method.
func funcQualifiedName(pkg string, fn *ast.FuncDecl) string {
	name := fn.Name.Name
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		if recv := receiverTypeName(fn.Recv.List[0].Type); recv != "" {
			name = recv + "." + name
		}
	}
	if pkg == "" {
		return name
	}
	return pkg + "." + name
}

// receiverTypeName reduces a method receiver type expression to its bare type
// name (stripping a leading pointer star), e.g. *Foo -> "Foo".
func receiverTypeName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.IndexExpr: // generic receiver Foo[T]
		if id, ok := e.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}

// receiverTypes maps a function's receiver + parameter variable names to their
// syntactically-resolved "<importPath>.<TypeName>" (a plain [*]alias.TypeName
// whose alias binds to a file import). Same rule as the extractor's
// receiver-type inference (WP-05b-1): anything else is skipped, no guessing.
func receiverTypes(fn *ast.FuncDecl, aliases map[string]string) map[string]string {
	m := map[string]string{}
	add := func(names []*ast.Ident, typ ast.Expr) {
		ref, ok := typeRef(typ, aliases)
		if !ok {
			return
		}
		for _, nm := range names {
			if nm == nil || nm.Name == "" || nm.Name == "_" {
				continue
			}
			m[nm.Name] = ref
		}
	}
	if fn.Recv != nil {
		for _, f := range fn.Recv.List {
			add(f.Names, f.Type)
		}
	}
	if fn.Type != nil && fn.Type.Params != nil {
		for _, f := range fn.Type.Params.List {
			add(f.Names, f.Type)
		}
	}
	return m
}

// typeRef reduces a type expression to "<importPath>.<TypeName>" when it is a
// plain [*]alias.TypeName whose alias binds to a file import (e.g. *sql.DB with
// import "database/sql" -> "database/sql.DB"). A bare ident (same-package type)
// or any composite/complex shape returns ok=false.
func typeRef(expr ast.Expr, aliases map[string]string) (string, bool) {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	impPath, ok := aliases[id.Name]
	if !ok {
		return "", false
	}
	return impPath + "." + sel.Sel.Name, true
}

// aliasToPath builds the file's import alias -> import path map, mirroring the
// linker's alias resolution: an unaliased import binds to the last path segment;
// dot and blank imports are excluded (they never qualify a selector).
func aliasToPath(file *ast.File) map[string]string {
	out := map[string]string{}
	for _, imp := range file.Imports {
		if imp == nil || imp.Path == nil {
			continue
		}
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p == "" {
			continue
		}
		alias := ""
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		switch alias {
		case ".", "_":
			continue
		case "":
			alias = path.Base(p)
		}
		out[alias] = p
	}
	return out
}

// sortFindings imposes a canonical, deterministic order: by sink name (which
// embeds sink file:line), then source name, then category. Identical input →
// identical order.
func sortFindings(fs []taint.Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].SinkName != fs[j].SinkName {
			return fs[i].SinkName < fs[j].SinkName
		}
		if fs[i].SourceName != fs[j].SourceName {
			return fs[i].SourceName < fs[j].SourceName
		}
		if fs[i].SinkCategory != fs[j].SinkCategory {
			return fs[i].SinkCategory < fs[j].SinkCategory
		}
		return strings.Join(fs[i].Labels, ",") < strings.Join(fs[j].Labels, ",")
	})
}
