package parse

import (
	"fmt"
	"go/ast"
	"go/token"

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
func extractGo(filename, pkg string, fset *token.FileSet, file *ast.File) ([]model.Node, []model.Edge, error) {
	ex := &goExtractor{
		filename: filename,
		pkg:      pkg,
		fset:     fset,
		funcs:    map[string]model.NodeId{},
		symbols:  map[string]model.NodeId{},
		edgeSeen: map[model.EdgeId]struct{}{},
	}

	fileNode, err := model.NewNode(goKindFile, filename, filename, 1, 1)
	if err != nil {
		return nil, nil, fmt.Errorf("extract: file node: %w", err)
	}
	ex.fileID = fileNode.ID()
	ex.nodes = append(ex.nodes, fileNode)

	// Pass 1: declare every top-level symbol so intra-file call/reference targets
	// resolve regardless of declaration order (forward references included).
	for _, decl := range file.Decls {
		if err := ex.declare(decl); err != nil {
			return nil, nil, err
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
		if err := ex.resolveBody(from, fn); err != nil {
			return nil, nil, err
		}
	}

	return ex.nodes, ex.edges, nil
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

	edgeSeen map[model.EdgeId]struct{}
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
		e.funcByDecl[d] = id
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
func (e *goExtractor) resolveBody(from model.NodeId, fn *ast.FuncDecl) error {
	var outErr error
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if outErr != nil {
			return false
		}
		switch x := n.(type) {
		case *ast.CallExpr:
			ident, ok := x.Fun.(*ast.Ident)
			if !ok {
				return true // selector / cross-package call: handled by the linker pass
			}
			to, ok := e.funcs[ident.Name]
			if !ok {
				return true
			}
			line, _ := e.pos(ident.Pos())
			if err := e.addEdge(from, to, goEdgeCalls, model.TierDerived, 0.9,
				"call to a function declared in the same file, resolved by name", line); err != nil {
				outErr = err
				return false
			}
			// Skip the callee ident so it is not also counted as a reference.
			return false
		case *ast.Ident:
			to, ok := e.symbols[x.Name]
			if !ok {
				return true
			}
			line, _ := e.pos(x.Pos())
			if err := e.addEdge(from, to, goEdgeReferences, model.TierDerived, 0.8,
				"reference to a symbol declared in the same file, resolved by name", line); err != nil {
				outErr = err
				return false
			}
		}
		return true
	})
	return outErr
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
