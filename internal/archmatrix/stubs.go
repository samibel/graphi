package archmatrix

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// Implementation identifiers used in the matrix and in the derived stub scan.
const (
	ImplNameDirect = "direct"
	ImplNameHTTP   = "http"
	ImplNameDaemon = "daemon"
)

// implSource locates one Client implementation in the tree.
type implSource struct {
	name     string
	relPath  string
	receiver string
}

// implSources are the three shipped Client implementations. Their compatibility
// stubs are the debt the PRD forbids growing, so they are read from source rather
// than trusted to a hand-kept column.
var implSources = []implSource{
	{name: ImplNameDirect, relPath: "surfaces/client", receiver: "Direct"},
	{name: ImplNameHTTP, relPath: "surfaces/client", receiver: "HTTP"},
	{name: ImplNameDaemon, relPath: "surfaces/daemon", receiver: "DaemonClient"},
}

// StubScan reports, per implementation, which Client methods are bare sentinel
// stubs: a body that does nothing but refuse with a typed error.
type StubScan map[string]map[string]bool

// ScanStubs derives the stub set for every implementation from the source.
//
// A method counts as a stub when, after discarding parameter-silencing
// assignments (`_, _ = ctx, req`), its body is a single return statement whose
// error operand is an `Err…` sentinel. That is exactly the shape of a
// compatibility stub: it declines without attempting the operation. A method with
// any real control flow — including Direct's "return the sentinel only when the
// optional service is unwired" guard — is not a stub.
func ScanStubs(moduleRoot string) (StubScan, error) {
	methods := make(map[string]bool)
	for _, name := range LiveMethods() {
		methods[name] = true
	}

	out := StubScan{}
	parsed := map[string]map[string]*ast.Package{}
	for _, src := range implSources {
		pkgs, ok := parsed[src.relPath]
		if !ok {
			dir := filepath.Join(moduleRoot, filepath.FromSlash(src.relPath))
			fset := token.NewFileSet()
			p, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
				return !strings.HasSuffix(fi.Name(), "_test.go")
			}, 0)
			if err != nil {
				return nil, fmt.Errorf("archmatrix: parse %s: %w", src.relPath, err)
			}
			parsed[src.relPath] = p
			pkgs = p
		}

		stubs := map[string]bool{}
		found := map[string]bool{}
		for _, pkg := range pkgs {
			for _, file := range pkg.Files {
				for _, decl := range file.Decls {
					fn, ok := decl.(*ast.FuncDecl)
					if !ok || fn.Body == nil || !receiverIs(fn, src.receiver) {
						continue
					}
					if !methods[fn.Name.Name] {
						continue
					}
					found[fn.Name.Name] = true
					if isSentinelStub(fn.Body) {
						stubs[fn.Name.Name] = true
					}
				}
			}
		}
		// Every implementation must define every method — it satisfies the
		// interface — so a gap here means the scan looked in the wrong place and
		// would silently under-report stubs.
		var missing []string
		for name := range methods {
			if !found[name] {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			return nil, fmt.Errorf("archmatrix: stub scan found no %s implementation of %s (scan target %q may be wrong)",
				src.receiver, strings.Join(missing, ", "), src.relPath)
		}
		out[src.name] = stubs
	}
	return out, nil
}

// SentinelRefs maps a Client method to every error sentinel its implementations
// can return, derived from the source.
type SentinelRefs map[string][]string

// ScanSentinelRefs collects, per Client method, the `Err…` sentinels referenced
// anywhere in its three implementations.
//
// This is the fail-closed inventory the PRD asks for, and it is derived rather
// than declared because the interesting question during the migration is not
// "which sentinel did we write down" but "which refusals can this use case still
// produce". Scoping to the function body — instead of a file-level text search —
// keeps a neighbouring method's sentinel from leaking into the answer.
func ScanSentinelRefs(moduleRoot string) (SentinelRefs, error) {
	methods := make(map[string]bool)
	for _, name := range LiveMethods() {
		methods[name] = true
	}

	refs := map[string]map[string]bool{}
	for _, src := range implSources {
		dir := filepath.Join(moduleRoot, filepath.FromSlash(src.relPath))
		fset := token.NewFileSet()
		pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
			return !strings.HasSuffix(fi.Name(), "_test.go")
		}, 0)
		if err != nil {
			return nil, fmt.Errorf("archmatrix: parse %s: %w", src.relPath, err)
		}
		for _, pkg := range pkgs {
			for _, file := range pkg.Files {
				for _, decl := range file.Decls {
					fn, ok := decl.(*ast.FuncDecl)
					if !ok || fn.Body == nil || !receiverIs(fn, src.receiver) || !methods[fn.Name.Name] {
						continue
					}
					ast.Inspect(fn.Body, func(node ast.Node) bool {
						var name string
						switch e := node.(type) {
						case *ast.SelectorExpr:
							name = e.Sel.Name
						case *ast.Ident:
							name = e.Name
						}
						if strings.HasPrefix(name, "Err") && name != "Errorf" {
							if refs[fn.Name.Name] == nil {
								refs[fn.Name.Name] = map[string]bool{}
							}
							refs[fn.Name.Name][name] = true
						}
						return true
					})
				}
			}
		}
	}

	out := SentinelRefs{}
	for method, names := range refs {
		list := make([]string, 0, len(names))
		for name := range names {
			list = append(list, name)
		}
		sort.Strings(list)
		out[method] = list
	}
	return out, nil
}

// For renders the sentinels of one method as inline code, or a dash when the
// method has no refusal path at all.
func (s SentinelRefs) For(method string) string {
	names := s[method]
	if len(names) == 0 {
		return "—"
	}
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, "`"+name+"`")
	}
	return strings.Join(quoted, ", ")
}

// receiverIs reports whether fn is a method on *name (or name).
func receiverIs(fn *ast.FuncDecl, name string) bool {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return false
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name
}

// isSentinelStub reports whether body is nothing but a refusal.
func isSentinelStub(body *ast.BlockStmt) bool {
	var effective []ast.Stmt
	for _, stmt := range body.List {
		if isDiscardAssign(stmt) {
			continue
		}
		effective = append(effective, stmt)
	}
	if len(effective) != 1 {
		return false
	}
	ret, ok := effective[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) == 0 {
		return false
	}
	return isSentinelExpr(ret.Results[len(ret.Results)-1])
}

// isDiscardAssign matches the `_, _ = ctx, req` parameter-silencing idiom.
func isDiscardAssign(stmt ast.Stmt) bool {
	assign, ok := stmt.(*ast.AssignStmt)
	if !ok || assign.Tok != token.ASSIGN {
		return false
	}
	for _, lhs := range assign.Lhs {
		ident, ok := lhs.(*ast.Ident)
		if !ok || ident.Name != "_" {
			return false
		}
	}
	return true
}

// isSentinelExpr reports whether expr names an Err… sentinel, either directly
// (ErrForgeUnavailable) or qualified (client.ErrForgeUnavailable). A wrapped
// sentinel — fmt.Errorf("%w: …", ErrBadInput) — is deliberately NOT a stub: the
// method did work and rejected specific input.
func isSentinelExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return strings.HasPrefix(e.Name, "Err")
	case *ast.SelectorExpr:
		return strings.HasPrefix(e.Sel.Name, "Err")
	}
	return false
}

// CheckStubs compares the matrix's declared implementation statuses against the
// stub set derived from source, in both directions.
//
// It enforces the load-bearing distinction only: does this path refuse with a
// sentinel, or does it do something? The finer `full` vs `typed-skip` annotation
// stays human-declared, because "returns a typed graceful-skip payload" is a
// semantic fact about the payload that no import-level scan can see.
func CheckStubs(m Matrix, scan StubScan) []string {
	var problems []string
	for _, row := range m.Methods {
		declared := map[string]string{
			ImplNameDirect: row.Direct,
			ImplNameHTTP:   row.HTTP,
			ImplNameDaemon: row.Daemon,
		}
		names := make([]string, 0, len(declared))
		for name := range declared {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, impl := range names {
			declaredStub := declared[impl] == ImplUnavailable
			actualStub := scan[impl][row.Name]
			switch {
			case declaredStub && !actualStub:
				problems = append(problems, fmt.Sprintf(
					"%s.%s is recorded as %q but its body does real work — update %s",
					impl, row.Name, ImplUnavailable, MatrixYAMLPath))
			case !declaredStub && actualStub:
				problems = append(problems, fmt.Sprintf(
					"%s.%s is a bare sentinel stub but the matrix records %q — a new compatibility stub must be recorded (and justified) in %s",
					impl, row.Name, declared[impl], MatrixYAMLPath))
			}
		}
	}
	sort.Strings(problems)
	return problems
}
