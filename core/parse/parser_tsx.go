package parse

import (
	"context"
	"fmt"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/core/model"
)

// TSXParser is the SW-054 curated tier-1 TSX parser. It clones the SW-053 TypeScript
// recipe (parser_ts.go); the TSX grammar is the TypeScript grammar plus JSX, so the
// declaration/use walk transfers directly (class names surface as type_identifier and
// parameter types live under required_parameter). It uses the pure-Go gotreesitter
// runtime, so the default tier stays green under CGO_ENABLED=0 and passes
// internal/cgoconformance. The grammar blob is Go-embedded (subset tag
// grammar_subset_tsx). TSXParser carries no mutable state and is safe for concurrent use.
type TSXParser struct {
	lang      *gts.Language
	extractor SymbolExtractor
}

// NewTSXParser returns a ready TSXParser wired to the pure-Go TSX grammar.
func NewTSXParser() *TSXParser {
	lang := grammars.TsxLanguage()
	return &TSXParser{lang: lang, extractor: &tsxSymbolExtractor{lang: lang}}
}

// Language implements Parser.
func (*TSXParser) Language() string { return "tsx" }

// Runtime implements Parser: pure-Go gotreesitter tree-sitter runtime (CGo-free).
func (*TSXParser) Runtime() Runtime { return RuntimeGoTreeSitter }

// Extensions implements Parser.
func (*TSXParser) Extensions() []string { return []string{".tsx"} }

type tsxAST struct {
	root *gts.Node
	src  []byte
	lang *gts.Language
}

// Parse implements Parser.
func (p *TSXParser) Parse(ctx context.Context, filename string, src []byte) (res *ParseResult, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	defer func() {
		if r := recover(); r != nil {
			res = nil
			err = fmt.Errorf("parse: recovered from panic parsing %q: %v", filename, r)
		}
	}()

	parser := gts.NewParser(p.lang)
	tree, perr := parser.Parse(src)
	if perr != nil {
		return nil, fmt.Errorf("parse: tsx error in %q: %w", filename, perr)
	}
	root := &tsxAST{root: tree.RootNode(), src: src, lang: p.lang}

	extractor := p.extractor
	if extractor == nil {
		extractor = &tsxSymbolExtractor{lang: p.lang}
	}
	nodes, edges, pending, xerr := extractor.Extract(filename, root)
	if xerr != nil {
		return nil, fmt.Errorf("parse: tsx extraction in %q: %w", filename, xerr)
	}

	imports := tsxImports(root)
	return &ParseResult{
		Meta: SourceMeta{
			Path: filename, Language: p.Language(),
			ContentHash: contentHash(src), Size: len(src),
		},
		Root:        root,
		Nodes:       nodes,
		Edges:       edges,
		PendingRefs: pending,
		Imports:     imports,
		References:  importsToRefs(imports),
	}, nil
}

// Kind mapping (TSX collapses onto {file, function, method, type, variable, constant}):
//
//	function ← function_declaration
//	method   ← method_definition
//	type     ← interface / type alias / enum / class declarations (collapsed)
//	variable ← let/var bindings
//	constant ← const bindings
//
// Absent by design: namespaces/modules, decorators, ambient declarations, JSX elements.

type tsxSymbolExtractor struct{ lang *gts.Language }

// Language implements SymbolExtractor.
func (*tsxSymbolExtractor) Language() string { return "tsx" }

// Extract implements SymbolExtractor for TSX.
func (e *tsxSymbolExtractor) Extract(filename string, root any) ([]model.Node, []model.Edge, []PendingRef, error) {
	t, ok := root.(*tsxAST)
	if !ok || t == nil || t.root == nil {
		return nil, nil, nil, fmt.Errorf("parse: tsx extractor: expected non-nil *tsxAST root for %q, got %T", filename, root)
	}
	w := newCSTWalk(t.lang, t.src, langPackage(filename))
	// SW-055 AC#6: fail-closed parse-depth guard on untrusted input (skips the
	// file with structured, source-free provenance if nesting exceeds the bound).
	if derr := w.guardDepth(t.root, filename, "tsx"); derr != nil {
		return nil, nil, nil, derr
	}
	tsxCollectDefs(w, t.root, false)
	tsxResolveUses(w, t.root, false)
	return w.finishExtract(filename, "tsx")
}

func tsxCollectDefs(w *cstWalk, n *gts.Node, inClass bool) {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "function_declaration":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				w.addDef(name.Text(w.src), KindFunction, nodePoint(name))
			}
		case "class_declaration":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				w.addDef(name.Text(w.src), KindType, nodePoint(name))
			}
			if body := c.ChildByFieldName("body", w.lang); body != nil {
				tsxCollectDefs(w, body, true)
			}
		case "interface_declaration", "type_alias_declaration", "enum_declaration":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				w.addDef(name.Text(w.src), KindType, nodePoint(name))
			}
		case "method_definition":
			if inClass {
				if name := c.ChildByFieldName("name", w.lang); name != nil {
					w.addDef(name.Text(w.src), KindMethod, nodePoint(name))
				}
			}
		case "lexical_declaration":
			kind := KindVariable
			if tsxLexicalIsConst(w, c) {
				kind = KindConstant
			}
			tsxCollectDeclarators(w, c, kind)
		case "variable_declaration":
			tsxCollectDeclarators(w, c, KindVariable)
		case "export_statement":
			tsxCollectDefs(w, c, inClass)
		}
	}
}

func tsxLexicalIsConst(w *cstWalk, decl *gts.Node) bool {
	for i := 0; i < decl.ChildCount(); i++ {
		c := decl.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "const":
			return true
		case "let", "var":
			return false
		}
	}
	return false
}

func tsxCollectDeclarators(w *cstWalk, decl *gts.Node, kind string) {
	for i := 0; i < decl.ChildCount(); i++ {
		c := decl.Child(i)
		if c == nil || c.Type(w.lang) != "variable_declarator" {
			continue
		}
		name := c.ChildByFieldName("name", w.lang)
		if name == nil || name.Type(w.lang) != "identifier" {
			continue
		}
		w.addDef(name.Text(w.src), kind, nodePoint(name))
	}
}

func tsxResolveUses(w *cstWalk, n *gts.Node, inClass bool) {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "function_declaration":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				tsxScanBody(w, c, name.Text(w.src))
			}
		case "method_definition":
			if inClass {
				if name := c.ChildByFieldName("name", w.lang); name != nil {
					tsxScanBody(w, c, name.Text(w.src))
				}
			}
		case "class_declaration":
			if body := c.ChildByFieldName("body", w.lang); body != nil {
				tsxResolveUses(w, body, true)
			}
		case "export_statement":
			tsxResolveUses(w, c, inClass)
		}
	}
}

func tsxScanBody(w *cstWalk, n *gts.Node, ownerBare string) {
	if n == nil {
		return
	}
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "call_expression":
			tsxHandleCall(w, c, ownerBare)
		case "type_annotation":
			tsxHandleTypeRef(w, c, ownerBare)
		}
		tsxScanBody(w, c, ownerBare)
	}
}

func tsxHandleCall(w *cstWalk, call *gts.Node, ownerBare string) {
	fn := call.ChildByFieldName("function", w.lang)
	if fn == nil {
		return
	}
	switch fn.Type(w.lang) {
	case "identifier":
		w.callBare(ownerBare, fn.Text(w.src), nodePoint(fn))
	case "member_expression":
		obj := fn.ChildByFieldName("object", w.lang)
		prop := fn.ChildByFieldName("property", w.lang)
		if obj == nil || prop == nil {
			return
		}
		w.callSelector(ownerBare, obj.Text(w.src), prop.Text(w.src), nodePoint(prop))
	}
}

func tsxHandleTypeRef(w *cstWalk, ann *gts.Node, ownerBare string) {
	for i := 0; i < ann.ChildCount(); i++ {
		c := ann.Child(i)
		if c == nil || c.Type(w.lang) != "type_identifier" {
			continue
		}
		w.typeRef(ownerBare, c.Text(w.src), nodePoint(c))
	}
}

func tsxImports(t *tsxAST) []ImportSpec {
	if t == nil || t.root == nil {
		return nil
	}
	var out []ImportSpec
	root := t.root
	for i := 0; i < root.ChildCount(); i++ {
		c := root.Child(i)
		if c == nil || c.Type(t.lang) != "import_statement" {
			continue
		}
		path := tsxImportPath(c, t.lang, t.src)
		if path == "" {
			continue
		}
		clause := childByType(c, "import_clause", t.lang)
		if clause == nil {
			out = append(out, ImportSpec{Path: path})
			continue
		}
		if ns := childByType(clause, "namespace_import", t.lang); ns != nil {
			if id := childByType(ns, "identifier", t.lang); id != nil {
				out = append(out, ImportSpec{Alias: id.Text(t.src), Path: path})
			}
			continue
		}
		if named := childByType(clause, "named_imports", t.lang); named != nil {
			for j := 0; j < named.ChildCount(); j++ {
				spec := named.Child(j)
				if spec == nil || spec.Type(t.lang) != "import_specifier" {
					continue
				}
				if name := spec.ChildByFieldName("name", t.lang); name != nil {
					out = append(out, ImportSpec{Alias: name.Text(t.src), Path: path})
				}
			}
			continue
		}
		out = append(out, ImportSpec{Path: path})
	}
	return out
}

func tsxImportPath(imp *gts.Node, lang *gts.Language, src []byte) string {
	s := imp.ChildByFieldName("source", lang)
	if s == nil {
		s = childByType(imp, "string", lang)
	}
	if s == nil {
		return ""
	}
	if frag := childByType(s, "string_fragment", lang); frag != nil {
		return frag.Text(src)
	}
	return strings.Trim(s.Text(src), "\"'`")
}
