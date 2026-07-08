package parse

import (
	"fmt"
	"go/ast"
	"go/token"
	"path"
	"strconv"

	"github.com/samibel/graphi/core/model"
)

// Node kinds emitted by the Go extractor. They join the open NodeKind vocabulary
// the rest of the engine already uses ("function", "method", "type", ...).
const (
	goKindFile     = "file"
	goKindFunction = "function"
	goKindMethod   = "method"
	goKindType     = "type"
	goKindVariable = "variable"
	goKindConstant = "constant"
)

// Edge kinds emitted by the Go extractor. They match the canonical vocabulary the
// query layer navigates (engine/query): "defines", "calls", "references".
const (
	goEdgeDefines    = "defines"
	goEdgeCalls      = "calls"
	goEdgeReferences = "references"

	// Hierarchy edge kinds (EP-011 G2). Recorded as PendingRefs by recordEmbeds
	// and resolved by the linker.
	goEdgeImplements = "implements"
	goEdgeInherits   = "inherits"
)

// extractGo walks a parsed Go file and derives the graph elements it can resolve
// WITHIN the file, without any I/O or cross-file knowledge:
//
//   - one "file" node for the source file,
//   - one node per top-level declaration (func/method/type/var/const),
//   - a "defines" edge from the file node to each declared symbol,
//   - a "calls" edge for every call whose callee is a file-local function,
//   - a "references" edge for every non-call use of a file-local symbol.
//
// Cross-file / cross-package resolution (selector calls like pkg.Fn or x.Method,
// imports) is deliberately out of scope here: graphstore.PutEdge requires both
// endpoints to already exist, and the ingest pipeline commits one file at a time,
// so an edge into another file would fail referential validation. Resolving those
// edges needs a separate linker pass over the fully-committed node set; until then
// this extractor emits exactly the edges it can prove from a single file, keeping
// the full-vs-incremental byte-identical invariant intact.
//
// The result is deterministic: declarations are visited in source order and edges
// are de-duplicated by identity, so identical input always yields identical
// nodes/edges.
//
// Alongside the intra-file edges it RECORDS (but never resolves) every reference
// it could not prove from a single file: same-package cross-file bare-ident
// calls/refs and cross-package selector calls/refs (pkg.Fn, recv.Method). These
// are returned as PendingRefs for the engine/link linker pass; the parse leaf
// fabricates no endpoint and emits no cross-file edge, preserving its pure-leaf
// boundary and the byte-identical full-vs-incremental invariant.
func extractGo(filename, pkg string, fset *token.FileSet, file *ast.File) ([]model.Node, []model.Edge, []PendingRef, error) {
	ex := &goExtractor{
		filename:    filename,
		pkg:         pkg,
		fset:        fset,
		funcs:       map[string]model.NodeId{},
		symbols:     map[string]model.NodeId{},
		edgeSeen:    map[model.EdgeId]struct{}{},
		aliasToPath: goAliasToPath(file),
	}

	fileNode, err := model.NewNode(goKindFile, filename, filename, 1, 1)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("extract: file node: %w", err)
	}
	ex.fileID = fileNode.ID()
	ex.nodes = append(ex.nodes, fileNode)

	// Pass 1: declare every top-level symbol so intra-file call/reference targets
	// resolve regardless of declaration order (forward references included).
	for _, decl := range file.Decls {
		if err := ex.declare(decl); err != nil {
			return nil, nil, nil, err
		}
	}

	// Pass 2: resolve call/reference edges inside each function body against the
	// symbol tables built in pass 1.
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		from, ok := ex.funcByDecl[fn]
		if !ok {
			continue
		}
		fromQN := ex.qnByDecl[fn]
		if err := ex.resolveBody(from, fromQN, fn); err != nil {
			return nil, nil, nil, err
		}
	}

	return ex.nodes, ex.edges, ex.pending, nil
}

// goExtractor accumulates the nodes/edges and the per-file symbol tables used to
// resolve intra-file edges.
type goExtractor struct {
	filename string
	pkg      string
	fset     *token.FileSet

	fileID model.NodeId
	nodes  []model.Node
	edges  []model.Edge

	// funcs maps a bare function name to its node ID (call targets).
	funcs map[string]model.NodeId
	// symbols maps a bare type/var/const name to its node ID (reference targets).
	symbols map[string]model.NodeId
	// funcByDecl links a FuncDecl back to its node ID for the body pass.
	funcByDecl map[*ast.FuncDecl]model.NodeId
	// qnByDecl links a FuncDecl back to its qualified name, the owning "from" of
	// any pending reference recorded inside its body.
	qnByDecl map[*ast.FuncDecl]string

	edgeSeen map[model.EdgeId]struct{}

	// aliasToPath maps a file import alias to its import path (dot/blank imports
	// excluded), reused to resolve a receiver variable's declared type expression
	// (`sql` → "database/sql") into an interned external-method qualified name.
	aliasToPath map[string]string
	// recvTypes maps the receiver-var name to its syntactically-resolved
	// "<importPath>.<TypeName>" for the function CURRENTLY being walked by
	// resolveBody (parameters + method receiver). It is rebuilt per function and
	// consulted by addPending to stamp a selector call's ReceiverType. It is nil
	// outside a body walk (e.g. during declare/recordEmbeds), where a nil-map read
	// safely yields "".
	recvTypes map[string]string

	// pending accumulates deferred references the linker resolves later. It is
	// deduplicated by logical reference identity so repeated call sites collapse;
	// the linker performs the final sorted-union evidence merge.
	pending     []PendingRef
	pendingSeen map[string]struct{}
}

// pos returns the 1-based line/column for an AST position.
func (e *goExtractor) pos(p token.Pos) (int, int) {
	pp := e.fset.Position(p)
	return pp.Line, pp.Column
}

// evidence renders a stable "file:line" citation for provenance.
func (e *goExtractor) evidence(line int) string {
	return fmt.Sprintf("%s:%d", e.filename, line)
}

// declare emits the node(s) for a single top-level declaration plus the file's
// "defines" edge to each, and records them in the symbol tables.
func (e *goExtractor) declare(decl ast.Decl) error {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		line, col := e.pos(d.Name.Pos())
		kind := goKindFunction
		qn := e.pkg + "." + d.Name.Name
		if d.Recv != nil {
			kind = goKindMethod
			qn = e.pkg + "." + receiverName(d.Recv) + "." + d.Name.Name
		}
		id, err := e.addNode(kind, qn, line, col)
		if err != nil {
			return err
		}
		if e.funcByDecl == nil {
			e.funcByDecl = map[*ast.FuncDecl]model.NodeId{}
		}
		if e.qnByDecl == nil {
			e.qnByDecl = map[*ast.FuncDecl]string{}
		}
		e.funcByDecl[d] = id
		e.qnByDecl[d] = qn
		// Only bare (non-method) functions are callable by simple name in-file.
		if d.Recv == nil {
			e.funcs[d.Name.Name] = id
		}
	case *ast.GenDecl:
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				line, col := e.pos(s.Name.Pos())
				id, err := e.addNode(goKindType, e.pkg+"."+s.Name.Name, line, col)
				if err != nil {
					return err
				}
				e.symbols[s.Name.Name] = id
				// Class-hierarchy extraction (EP-011 G2): scan the declared type for
				// embedded interfaces (→ implements) and embedded concrete types in a
				// struct (→ inherits). These are intra-AST, deterministic facts; they
				// are RECORDED as PendingRefs and resolved by the linker exactly like
				// references, so cross-file/cross-package embeds resolve to the
				// committed type nodes without the parse leaf fabricating endpoints.
				typeQN := e.pkg + "." + s.Name.Name
				e.recordEmbeds(typeQN, s.Type, line)
			case *ast.ValueSpec:
				kind := goKindVariable
				if d.Tok == token.CONST {
					kind = goKindConstant
				}
				for _, name := range s.Names {
					if name.Name == "_" {
						continue
					}
					line, col := e.pos(name.Pos())
					id, err := e.addNode(kind, e.pkg+"."+name.Name, line, col)
					if err != nil {
						return err
					}
					e.symbols[name.Name] = id
				}
			}
		}
	}
	return nil
}

// addNode constructs and stores a symbol node and its "defines" edge from the
// file node. It returns the new node's ID.
func (e *goExtractor) addNode(kind, qualifiedName string, line, col int) (model.NodeId, error) {
	n, err := model.NewNode(kind, qualifiedName, e.filename, line, col)
	if err != nil {
		return "", fmt.Errorf("extract: node %q: %w", qualifiedName, err)
	}
	e.nodes = append(e.nodes, n)
	if err := e.addEdge(e.fileID, n.ID(), goEdgeDefines, model.TierConfirmed, 1.0,
		"declared as a top-level symbol in the source file", line); err != nil {
		return "", err
	}
	return n.ID(), nil
}

// resolveBody walks one function body, emitting a "calls" edge for every call to
// a file-local function and a "references" edge for every other use of a
// file-local symbol. A name is never both: call targets are excluded from the
// reference scan.
//
// Every name it cannot prove from this file is RECORDED as a PendingRef for the
// linker (it is never resolved here): an unresolved bare-ident call/ref becomes a
// non-selector PendingRef (same-package cross-file candidate), and a selector
// call/ref (alias.Name / recv.Method) becomes a selector PendingRef. fromQN is
// the qualified name of the enclosing symbol that owns these references.
//
// The walk runs under panic-recovery discipline (the parser's two-layer guard
// already recovers at the Parse boundary; this keeps a single malformed body
// from aborting the whole file's extraction with an opaque crash).
func (e *goExtractor) resolveBody(from model.NodeId, fromQN string, fn *ast.FuncDecl) (err error) {
	defer func() {
		if r := recover(); r != nil && err == nil {
			err = fmt.Errorf("extract: recovered from panic resolving %q body: %v", fromQN, r)
		}
	}()
	// Build the receiver-type table for THIS function so selector calls on a
	// typed parameter / method receiver (e.g. `db.Query(...)` with `db *sql.DB`)
	// carry the receiver's fully-qualified type for the linker to mint a precise
	// external method node. Rebuilt per function; consulted by addPending.
	e.recvTypes = e.receiverTypes(fn)
	var outErr error
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if outErr != nil {
			return false
		}
		switch x := n.(type) {
		case *ast.CallExpr:
			switch fun := x.Fun.(type) {
			case *ast.Ident:
				if to, ok := e.funcs[fun.Name]; ok {
					line, _ := e.pos(fun.Pos())
					if err := e.addEdge(from, to, goEdgeCalls, model.TierDerived, 0.9,
						"call to a function declared in the same file, resolved by name", line); err != nil {
						outErr = err
						return false
					}
					// Skip the callee ident so it is not also a reference.
					return false
				}
				// Unresolved bare call: same-package cross-file candidate.
				line, _ := e.pos(fun.Pos())
				e.addPending(fromQN, "", fun.Name, goEdgeCalls, line, false)
				return false
			case *ast.SelectorExpr:
				// Selector call alias.Name / recv.Method: cross-package or
				// cross-file method candidate, resolved by the linker.
				base := selectorBase(fun.X)
				line, _ := e.pos(fun.Sel.Pos())
				e.addPending(fromQN, base, fun.Sel.Name, goEdgeCalls, line, true)
				// Skip the selector's identifiers so they are not double-counted
				// as bare references; still descend into call arguments.
				for _, arg := range x.Args {
					ast.Inspect(arg, e.inspectFn(from, fromQN, &outErr))
				}
				// WP-05a: a chained call (`exec.Command(...).Output()`) has the
				// inner call as the selector's receiver (fun.X). The old walk
				// returned here and dropped it, so a chained sink like exec.Command
				// was never recorded. Descend into the inner call so its own target
				// is recorded as a pending ref (the bare-ident base case above is
				// unaffected: fun.X is only a CallExpr for a chained call).
				if inner, ok := fun.X.(*ast.CallExpr); ok {
					ast.Inspect(inner, e.inspectFn(from, fromQN, &outErr))
				}
				return false
			}
			return true
		case *ast.SelectorExpr:
			// Non-call selector use (alias.Name / recv.Field-or-Method): record a
			// cross-package/cross-file reference candidate; do not descend into
			// the selector identifiers (X is handled by recording the base).
			base := selectorBase(x.X)
			line, _ := e.pos(x.Sel.Pos())
			e.addPending(fromQN, base, x.Sel.Name, goEdgeReferences, line, true)
			return false
		case *ast.Ident:
			if to, ok := e.symbols[x.Name]; ok {
				line, _ := e.pos(x.Pos())
				if err := e.addEdge(from, to, goEdgeReferences, model.TierDerived, 0.8,
					"reference to a symbol declared in the same file, resolved by name", line); err != nil {
					outErr = err
					return false
				}
				return true
			}
			// An unresolved bare identifier MAY be a same-package cross-file
			// symbol. Only record selectable-looking names (exported or known
			// local-package style); locals/params are filtered by the linker
			// (no same-package index hit ⇒ skipped, counted, no edge).
			line, _ := e.pos(x.Pos())
			e.addPending(fromQN, "", x.Name, goEdgeReferences, line, false)
		}
		return true
	})
	if outErr != nil {
		return outErr
	}
	return nil
}

// inspectFn returns an ast.Inspect visitor that records pending refs for a
// nested expression (call arguments of a selector call), reusing the same
// resolution discipline as the top-level walk.
func (e *goExtractor) inspectFn(from model.NodeId, fromQN string, outErr *error) func(ast.Node) bool {
	return func(n ast.Node) bool {
		if *outErr != nil {
			return false
		}
		switch x := n.(type) {
		case *ast.CallExpr:
			switch fun := x.Fun.(type) {
			case *ast.Ident:
				if to, ok := e.funcs[fun.Name]; ok {
					line, _ := e.pos(fun.Pos())
					if err := e.addEdge(from, to, goEdgeCalls, model.TierDerived, 0.9,
						"call to a function declared in the same file, resolved by name", line); err != nil {
						*outErr = err
						return false
					}
					return false
				}
				line, _ := e.pos(fun.Pos())
				e.addPending(fromQN, "", fun.Name, goEdgeCalls, line, false)
				return false
			case *ast.SelectorExpr:
				base := selectorBase(fun.X)
				line, _ := e.pos(fun.Sel.Pos())
				e.addPending(fromQN, base, fun.Sel.Name, goEdgeCalls, line, true)
				for _, arg := range x.Args {
					ast.Inspect(arg, e.inspectFn(from, fromQN, outErr))
				}
				// WP-05a: descend into a chained call's inner receiver call so it is
				// not dropped (mirrors resolveBody).
				if inner, ok := fun.X.(*ast.CallExpr); ok {
					ast.Inspect(inner, e.inspectFn(from, fromQN, outErr))
				}
				return false
			}
			return true
		case *ast.SelectorExpr:
			base := selectorBase(x.X)
			line, _ := e.pos(x.Sel.Pos())
			e.addPending(fromQN, base, x.Sel.Name, goEdgeReferences, line, true)
			return false
		case *ast.Ident:
			if to, ok := e.symbols[x.Name]; ok {
				line, _ := e.pos(x.Pos())
				if err := e.addEdge(from, to, goEdgeReferences, model.TierDerived, 0.8,
					"reference to a symbol declared in the same file, resolved by name", line); err != nil {
					*outErr = err
					return false
				}
				return true
			}
			line, _ := e.pos(x.Pos())
			e.addPending(fromQN, "", x.Name, goEdgeReferences, line, false)
		}
		return true
	}
}

// addPending records a deferred reference, deduplicated by logical identity
// (fromQN, base, name, kind, selector) so repeated call/reference sites collapse
// to a single PendingRef; the linker performs the sorted-union evidence merge.
// The FIRST line wins for the recorded evidence, keeping output order-stable.
func (e *goExtractor) addPending(fromQN, base, name, kind string, line int, selector bool) {
	if fromQN == "" || name == "" {
		return
	}
	if e.pendingSeen == nil {
		e.pendingSeen = map[string]struct{}{}
	}
	key := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%t", fromQN, base, name, kind, selector)
	if _, seen := e.pendingSeen[key]; seen {
		return
	}
	e.pendingSeen[key] = struct{}{}
	// Stamp the syntactically-known receiver type for a selector CALL whose base is
	// a typed receiver variable (parameter / method receiver). Restricted to calls
	// (method sinks); field references and non-selector refs never carry it. The
	// dedup key already includes base, and recvTypes is fixed per function, so the
	// stamped type is consistent across a collapsed ref's call sites (determinism).
	recvType := ""
	if selector && kind == goEdgeCalls && base != "" {
		recvType = e.recvTypes[base]
	}
	e.pending = append(e.pending, PendingRef{
		FromQN:       fromQN,
		Name:         name,
		SelectorBase: base,
		Kind:         kind,
		Line:         line,
		Selector:     selector,
		ReceiverType: recvType,
	})
}

// recordEmbeds scans a declared type's AST for embedded types and records
// hierarchy PendingRefs (EP-011 G2):
//
//   - *ast.InterfaceType: every embedded interface expr (a Field with no Names)
//     → an `implements` pending ref from the declaring type to the embedded
//     interface. Go's implicit interface satisfaction is NOT inferred here
//     (only explicit embedding / satisfaction by embedding).
//   - *ast.StructType: every embedded field (a Field with no Names) → an
//     `inherits` pending ref from the declaring type to the embedded type.
//
// An embedded name that is a bare *ast.Ident is a same-package (directory)
// candidate (selector=false); a *ast.SelectorExpr is a cross-package candidate
// (selector=true). Both are resolved by the linker against committed type nodes,
// exactly like reference pending refs, so no endpoint is ever fabricated at the
// parse leaf (purity boundary preserved).
//
// Determinism: embeds are scanned in source order and recorded via addPending,
// which dedups by logical identity and keeps the FIRST line — byte-identical for
// identical input.
func (e *goExtractor) recordEmbeds(typeQN string, t ast.Expr, declLine int) {
	switch tt := t.(type) {
	case *ast.InterfaceType:
		if tt.Methods == nil {
			return
		}
		for _, f := range tt.Methods.List {
			if len(f.Names) > 0 {
				continue // a named method, not an embedded interface
			}
			line := declLine
			if pos := f.Pos(); pos.IsValid() {
				l, _ := e.pos(pos)
				line = l
			}
			base, name, sel, ok := embedName(f.Type)
			if !ok {
				continue
			}
			e.addPending(typeQN, base, name, goEdgeImplements, line, sel)
		}
	case *ast.StructType:
		if tt.Fields == nil {
			return
		}
		for _, f := range tt.Fields.List {
			if len(f.Names) > 0 {
				continue // a named field, not an embedded type
			}
			line := declLine
			if pos := f.Pos(); pos.IsValid() {
				l, _ := e.pos(pos)
				line = l
			}
			base, name, sel, ok := embedName(f.Type)
			if !ok {
				continue
			}
			e.addPending(typeQN, base, name, goEdgeInherits, line, sel)
		}
	}
}

// embedName extracts the (selectorBase, name, isSelector) triple from an
// embedded-type expression. For a bare ident it returns ("", name, false); for a
// pkg.Name selector it returns (pkg, name, true). Anything more complex returns
// (_, _, _, false) and is skipped (unresolvable at the leaf).
func embedName(t ast.Expr) (base, name string, selector, ok bool) {
	if star, isStar := t.(*ast.StarExpr); isStar {
		t = star.X
	}
	switch x := t.(type) {
	case *ast.Ident:
		return "", x.Name, false, true
	case *ast.SelectorExpr:
		if id, isIdent := x.X.(*ast.Ident); isIdent {
			return id.Name, x.Sel.Name, true, true
		}
	}
	return "", "", false, false
}

// receiverTypes maps a function's receiver-var names (method receiver + explicit
// parameters) to their syntactically-resolved "<importPath>.<TypeName>" when the
// declared type is a plain `[*]alias.TypeName` with alias in the file's imports.
// Anything else (same-package bare-ident types, composite types, unresolvable
// aliases) is skipped — no guessing. This is the SYNTACTIC receiver-type
// inference the linker needs to mint precise external method nodes offline (the
// engine's go/types pass uses empty stub packages and cannot resolve these).
func (e *goExtractor) receiverTypes(fn *ast.FuncDecl) map[string]string {
	m := map[string]string{}
	add := func(names []*ast.Ident, typ ast.Expr) {
		ref, ok := e.typeRef(typ)
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
// plain `[*]alias.TypeName` whose alias binds to a file import (e.g. `*sql.DB`
// with `import "database/sql"` → "database/sql.DB"). It deliberately returns
// ok=false for a bare *ast.Ident (a same-package type — internal, resolved
// elsewhere) and any composite/complex shape (maps, slices, channels, func
// types, chained selectors), keeping receiver-type inference precise.
func (e *goExtractor) typeRef(expr ast.Expr) (string, bool) {
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
	impPath, ok := e.aliasToPath[id.Name]
	if !ok {
		return "", false
	}
	return impPath + "." + sel.Sel.Name, true
}

// goAliasToPath builds the file's import alias → import path map, mirroring the
// linker's own alias resolution: an unaliased import binds to the last segment of
// its path; dot ("."), blank ("_") imports are excluded (they never qualify a
// selector). Used to resolve a receiver variable's declared type package.
func goAliasToPath(file *ast.File) map[string]string {
	out := map[string]string{}
	for _, imp := range file.Imports {
		if imp == nil || imp.Path == nil {
			continue
		}
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if p == "" {
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

// selectorBase returns the leading qualifier of a selector's X expression when
// it is a bare identifier (the common alias.Name / recv.Method shape). For
// anything more complex (chained selectors, index expressions, literals) it
// returns "", which the linker treats as an unresolvable base (skipped, counted).
func selectorBase(x ast.Expr) string {
	if id, ok := x.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// addEdge constructs a fully-provenanced edge and appends it, de-duplicating by
// edge identity (from,to,kind) so repeated calls/references collapse to one edge
// and the output stays deterministic.
func (e *goExtractor) addEdge(from, to model.NodeId, kind string, tier model.ConfidenceTier, confidence float64, reason string, line int) error {
	edge, err := model.NewEdge(from, to, kind, tier, confidence, reason, []string{e.evidence(line)})
	if err != nil {
		return fmt.Errorf("extract: edge %s->%s (%s): %w", from, to, kind, err)
	}
	if _, seen := e.edgeSeen[edge.ID()]; seen {
		return nil
	}
	e.edgeSeen[edge.ID()] = struct{}{}
	e.edges = append(e.edges, edge)
	return nil
}

// receiverName returns the bare receiver type name for a method, stripping any
// pointer star and generic type parameters (e.g. *Foo[T] -> "Foo"). It returns
// "_" only if the receiver type is unexpectedly missing, so the qualified name
// stays non-empty and the node is constructible.
func receiverName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return "_"
	}
	return typeName(recv.List[0].Type)
}

// typeName extracts the bare identifier from a (possibly pointer/generic) type
// expression. Anything it cannot reduce to an identifier yields "_".
func typeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return typeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr: // generic receiver Foo[T]
		return typeName(t.X)
	case *ast.IndexListExpr: // generic receiver Foo[T, U]
		return typeName(t.X)
	default:
		return "_"
	}
}
