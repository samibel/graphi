package typeresolve

import (
	"go/token"
	"go/types"

	"github.com/samibel/graphi/core/model"
)

// Node kinds as emitted by the core/parse Go extractor. The strings are
// duplicated here deliberately rather than exported from core/parse: the golden
// cross-test (qn_test.go) compares full NodeIds produced by the REAL extractor
// against this package's reconstruction, so any drift — kind string, qualified
// name, or path normalization — fails a test instead of silently dropping
// edges.
const (
	KindFunction = "function"
	KindMethod   = "method"
	KindType     = "type"
	KindVariable = "variable"
	KindConstant = "constant"
)

// ObjectNode maps a type-checked object to the (kind, qualifiedName) pair the
// core/parse Go extractor emits for the SAME top-level declaration. ok=false
// means the extractor creates NO node for this object — locals, parameters,
// struct fields, interface methods, blank vars/consts, imported package names —
// and the caller must not fabricate an endpoint (the same never-fabricate
// discipline engine/link follows).
//
// The reconstruction mirrors core/parse/extract_go.go declare() byte-exactly:
//
//	function:  <pkgName>.<name>                       (top-level FuncDecl, no receiver;
//	                                                   includes init and blank `func _()`)
//	method:    <pkgName>.<receiverBase>.<name>        (pointer star and generic type
//	                                                   parameters stripped from the receiver)
//	type:      <pkgName>.<name>                       (package-scope TypeSpec, incl. aliases)
//	variable:  <pkgName>.<name>                       (package-scope var; blank skipped)
//	constant:  <pkgName>.<name>                       (package-scope const; blank skipped)
//
// pkgName is the package CLAUSE name (types.Package.Name), which is exactly the
// `pkg` the parser hands the extractor.
func ObjectNode(obj types.Object) (kind, qualifiedName string, ok bool) {
	if obj == nil || obj.Pkg() == nil {
		return "", "", false
	}
	pkg := obj.Pkg().Name()

	switch o := obj.(type) {
	case *types.Func:
		sig, isSig := o.Type().(*types.Signature)
		if !isSig {
			return "", "", false
		}
		if recv := sig.Recv(); recv != nil {
			base, isConcrete := receiverBaseName(recv.Type())
			if !isConcrete {
				// Interface methods live inside a type literal; the extractor
				// emits no node for them (they are not top-level FuncDecls).
				return "", "", false
			}
			return KindMethod, pkg + "." + base + "." + o.Name(), true
		}
		// A receiver-less *types.Func in Info.Defs is always a top-level
		// FuncDecl: function literals have no defining Ident, and Go has no
		// local named functions. This includes `init` and blank `func _()`,
		// which are top-level decls but are NOT entered into package scope —
		// the extractor emits nodes for both.
		return KindFunction, pkg + "." + o.Name(), true

	case *types.TypeName:
		// Includes aliases. Deliberate asymmetry: the extractor DOES emit a
		// node for a blank `type _ T`, but this mapping declines it — blank
		// identifiers are unreferencable (never an edge target) and a type has
		// no body (never an edge source), so no edge can ever need its id; and
		// go/types gives blank names a nil scope parent, indistinguishable
		// from a local blank type. The golden cross-test pins this as the
		// ONLY extractor-node the reconstruction skips.
		if !packageScoped(o) {
			return "", "", false
		}
		return KindType, pkg + "." + o.Name(), true

	case *types.Var:
		if o.IsField() || o.Name() == "_" || !packageScoped(o) {
			return "", "", false
		}
		return KindVariable, pkg + "." + o.Name(), true

	case *types.Const:
		if o.Name() == "_" || !packageScoped(o) {
			return "", "", false
		}
		return KindConstant, pkg + "." + o.Name(), true
	}
	return "", "", false
}

// NodeIDFor maps a type-checked object to the model.NodeId of the node the
// extractor emitted for it, or ok=false when no such node exists. It needs the
// FileSet the object was type-checked under to recover the declaring file path
// and position — NodeId identity is (kind, qualifiedName, sourcePath); line and
// column are carried but non-identity (see core/model).
func NodeIDFor(obj types.Object, fset *token.FileSet) (model.NodeId, bool) {
	kind, qn, ok := ObjectNode(obj)
	if !ok {
		return "", false
	}
	pos := fset.Position(obj.Pos())
	if !pos.IsValid() || pos.Filename == "" {
		return "", false
	}
	n, err := model.NewNode(kind, qn, pos.Filename, pos.Line, pos.Column)
	if err != nil {
		return "", false
	}
	return n.ID(), true
}

// receiverBaseName reduces a method receiver type to the bare type name the
// extractor's receiverName/typeName pair produces: the pointer star is
// stripped, generic instantiations reduce to the origin name (Foo[T] -> Foo),
// and interface receivers report isConcrete=false (no node exists for
// interface methods). The extractor's "_" fallback for unreducible receivers
// is unreachable here: source that reaches go/types with a method always has a
// (possibly pointered) named receiver.
func receiverBaseName(t types.Type) (base string, isConcrete bool) {
	if p, isPtr := t.(*types.Pointer); isPtr {
		t = p.Elem()
	}
	named, isNamed := types.Unalias(t).(*types.Named)
	if !isNamed {
		return "", false
	}
	if _, isIface := named.Underlying().(*types.Interface); isIface {
		return "", false
	}
	return named.Obj().Name(), true
}

// packageScoped reports whether obj is declared directly in its package scope —
// the extractor only ever emits nodes for TOP-LEVEL declarations, so locals
// (function-scope vars/consts/types) must map to no node.
func packageScoped(obj types.Object) bool {
	pkg := obj.Pkg()
	return pkg != nil && obj.Parent() == pkg.Scope()
}
