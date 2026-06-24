package parse

import (
	"context"
	"fmt"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/core/model"
)

// CppParser is the SW-054 curated tier-1 C++ parser. It clones the SW-053 recipe over
// the pure-Go gotreesitter C++ grammar (CGo-free; default tier green under
// CGO_ENABLED=0; grammar blob Go-embedded behind subset tag grammar_subset_cpp). C++
// adds namespaces/templates over the C grammar; the walk descends into
// namespace_definition bodies and reuses the C declarator-name chain. CppParser carries
// no mutable state and is safe for concurrent use.
type CppParser struct {
	lang      *gts.Language
	extractor SymbolExtractor
}

// NewCppParser returns a ready CppParser wired to the pure-Go C++ grammar.
func NewCppParser() *CppParser {
	lang := grammars.CppLanguage()
	return &CppParser{lang: lang, extractor: &cppSymbolExtractor{lang: lang}}
}

// Language implements Parser.
func (*CppParser) Language() string { return "cpp" }

// Extensions implements Parser.
func (*CppParser) Extensions() []string { return []string{".cpp", ".cc", ".cxx", ".hpp"} }

type cppAST struct {
	root *gts.Node
	src  []byte
	lang *gts.Language
}

// Parse implements Parser.
func (p *CppParser) Parse(ctx context.Context, filename string, src []byte) (res *ParseResult, err error) {
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
		return nil, fmt.Errorf("parse: cpp error in %q: %w", filename, perr)
	}
	root := &cppAST{root: tree.RootNode(), src: src, lang: p.lang}

	extractor := p.extractor
	if extractor == nil {
		extractor = &cppSymbolExtractor{lang: p.lang}
	}
	nodes, edges, pending, xerr := extractor.Extract(filename, root)
	if xerr != nil {
		return nil, fmt.Errorf("parse: cpp extraction in %q: %w", filename, xerr)
	}

	imports := cppIncludes(root)
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

// Kind mapping (C++ collapses onto {file, function, type, variable}):
//
//	function ← function_definition (declarator chain -> identifier)
//	type     ← class_specifier / struct_specifier / union_specifier / enum_specifier
//	variable ← top-level declaration init_declarator identifier
//
// Definitions are discovered inside (possibly nested) namespace_definition bodies.
// Absent by design: method (member functions declared in-class are part of the type;
// out-of-line definitions are scoped names, recorded as selector PendingRefs at use),
// constant. #include is recorded as an ImportSpec.

type cppSymbolExtractor struct{ lang *gts.Language }

// Language implements SymbolExtractor.
func (*cppSymbolExtractor) Language() string { return "cpp" }

// Extract implements SymbolExtractor for C++.
func (e *cppSymbolExtractor) Extract(filename string, root any) ([]model.Node, []model.Edge, []PendingRef, error) {
	t, ok := root.(*cppAST)
	if !ok || t == nil || t.root == nil {
		return nil, nil, nil, fmt.Errorf("parse: cpp extractor: expected non-nil *cppAST root for %q, got %T", filename, root)
	}
	w := newCSTWalk(t.lang, t.src, langPackage(filename))
	cppCollectDefs(w, t.root)
	cppResolveUses(w, t.root)
	return w.finishExtract(filename, "cpp")
}

func cppCollectDefs(w *cstWalk, n *gts.Node) {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "function_definition":
			if decl := c.ChildByFieldName("declarator", w.lang); decl != nil {
				if name := cDeclaratorName(decl, w.lang); name != nil {
					w.addDef(name.Text(w.src), KindFunction, nodePoint(name))
				}
			}
		case "class_specifier", "struct_specifier", "union_specifier", "enum_specifier":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				w.addDef(name.Text(w.src), KindType, nodePoint(name))
			}
		case "declaration":
			cppCollectDeclVars(w, c)
		case "namespace_definition":
			if body := childByType(c, "declaration_list", w.lang); body != nil {
				cppCollectDefs(w, body)
			}
		}
	}
}

func cppCollectDeclVars(w *cstWalk, decl *gts.Node) {
	// Skip function prototypes (declaration whose declarator is a function_declarator).
	for i := 0; i < decl.ChildCount(); i++ {
		c := decl.Child(i)
		if c == nil {
			continue
		}
		if c.Type(w.lang) == "init_declarator" {
			if name := cDeclaratorName(c.ChildByFieldName("declarator", w.lang), w.lang); name != nil {
				w.addDef(name.Text(w.src), KindVariable, nodePoint(name))
			}
		}
	}
}

func cppResolveUses(w *cstWalk, n *gts.Node) {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "function_definition":
			decl := c.ChildByFieldName("declarator", w.lang)
			if decl == nil {
				continue
			}
			if name := cDeclaratorName(decl, w.lang); name != nil {
				cppScanBody(w, c, name.Text(w.src))
			}
		case "namespace_definition":
			if body := childByType(c, "declaration_list", w.lang); body != nil {
				cppResolveUses(w, body)
			}
		}
	}
}

func cppScanBody(w *cstWalk, n *gts.Node, ownerBare string) {
	if n == nil {
		return
	}
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		if c.Type(w.lang) == "call_expression" {
			cppHandleCall(w, c, ownerBare)
		}
		cppScanBody(w, c, ownerBare)
	}
}

func cppHandleCall(w *cstWalk, call *gts.Node, ownerBare string) {
	fn := call.ChildByFieldName("function", w.lang)
	if fn == nil {
		return
	}
	switch fn.Type(w.lang) {
	case "identifier":
		w.callBare(ownerBare, fn.Text(w.src), nodePoint(fn))
	case "field_expression":
		arg := fn.ChildByFieldName("argument", w.lang)
		field := fn.ChildByFieldName("field", w.lang)
		if arg != nil && field != nil {
			w.callSelector(ownerBare, arg.Text(w.src), field.Text(w.src), nodePoint(field))
		}
	case "qualified_identifier":
		scope := fn.ChildByFieldName("scope", w.lang)
		name := fn.ChildByFieldName("name", w.lang)
		if scope != nil && name != nil {
			w.callSelector(ownerBare, scope.Text(w.src), name.Text(w.src), nodePoint(name))
		}
	}
}

// cppIncludes records #include directives as ImportSpecs.
func cppIncludes(t *cppAST) []ImportSpec {
	if t == nil || t.root == nil {
		return nil
	}
	var out []ImportSpec
	root := t.root
	for i := 0; i < root.ChildCount(); i++ {
		c := root.Child(i)
		if c == nil || c.Type(t.lang) != "preproc_include" {
			continue
		}
		path := c.ChildByFieldName("path", t.lang)
		if path == nil {
			path = childByType(c, "system_lib_string", t.lang)
		}
		if path == nil {
			path = childByType(c, "string_literal", t.lang)
		}
		if path == nil {
			continue
		}
		p := strings.Trim(path.Text(t.src), "<>\"")
		if p != "" {
			out = append(out, ImportSpec{Path: p})
		}
	}
	return out
}
