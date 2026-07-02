package parse

import (
	"context"
	"fmt"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/core/model"
)

// CParser is the SW-054 curated tier-1 C parser. It clones the SW-053 recipe over the
// pure-Go gotreesitter C grammar (CGo-free; default tier green under CGO_ENABLED=0;
// grammar blob Go-embedded behind subset tag grammar_subset_c). CParser carries no
// mutable state and is safe for concurrent use.
type CParser struct {
	lang      *gts.Language
	extractor SymbolExtractor
}

// NewCParser returns a ready CParser wired to the pure-Go C grammar.
func NewCParser() *CParser {
	lang := grammars.CLanguage()
	return &CParser{lang: lang, extractor: &cSymbolExtractor{lang: lang}}
}

// Language implements Parser.
func (*CParser) Language() string { return "c" }

// Runtime implements Parser: pure-Go gotreesitter tree-sitter runtime (CGo-free).
func (*CParser) Runtime() Runtime { return RuntimeGoTreeSitter }

// Extensions implements Parser.
func (*CParser) Extensions() []string { return []string{".c", ".h"} }

type cAST struct {
	root *gts.Node
	src  []byte
	lang *gts.Language
}

// Parse implements Parser.
func (p *CParser) Parse(ctx context.Context, filename string, src []byte) (res *ParseResult, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	defer func() {
		if r := recover(); r != nil {
			res = nil
			err = fmt.Errorf("parse: recovered from panic parsing %q: %v", filename, r)
		}
	}()

	tree, perr := parseTreeSitter(ctx, p.lang, src)
	if perr != nil {
		return nil, fmt.Errorf("parse: c error in %q: %w", filename, perr)
	}
	root := &cAST{root: tree.RootNode(), src: src, lang: p.lang}

	extractor := p.extractor
	if extractor == nil {
		extractor = &cSymbolExtractor{lang: p.lang}
	}
	nodes, edges, pending, xerr := extractor.Extract(filename, root)
	if xerr != nil {
		return nil, fmt.Errorf("parse: c extraction in %q: %w", filename, xerr)
	}

	imports := cIncludes(root)
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

// Kind mapping (C collapses onto {file, function, type, variable}):
//
//	function ← function_definition (declarator -> function_declarator -> identifier)
//	type     ← struct_specifier / union_specifier / enum_specifier (named)
//	variable ← top-level declaration init_declarator identifier
//
// Absent by design: method (no methods), constant (C `const` qualifier is not a
// distinct top-level kind this slice). #include is recorded as an ImportSpec, never an
// EdgeImports.

type cSymbolExtractor struct{ lang *gts.Language }

// Language implements SymbolExtractor.
func (*cSymbolExtractor) Language() string { return "c" }

// Extract implements SymbolExtractor for C.
func (e *cSymbolExtractor) Extract(filename string, root any) ([]model.Node, []model.Edge, []PendingRef, error) {
	t, ok := root.(*cAST)
	if !ok || t == nil || t.root == nil {
		return nil, nil, nil, fmt.Errorf("parse: c extractor: expected non-nil *cAST root for %q, got %T", filename, root)
	}
	w := newCSTWalk(t.lang, t.src, langPackage(filename))
	// SW-055 AC#6: fail-closed parse-depth guard on untrusted input (skips the
	// file with structured, source-free provenance if nesting exceeds the bound).
	if derr := w.guardDepth(t.root, filename, "c"); derr != nil {
		return nil, nil, nil, derr
	}
	cCollectDefs(w, t.root)
	cResolveUses(w, t.root)
	return w.finishExtract(filename, "c")
}

// cDeclaratorName follows the declarator field chain to the leaf identifier name of a
// function_declarator (price(int c) -> price). Shared with C++ via the same grammar
// shape.
func cDeclaratorName(decl *gts.Node, lang *gts.Language) *gts.Node {
	for decl != nil {
		switch decl.Type(lang) {
		case "identifier", "field_identifier":
			return decl
		}
		next := decl.ChildByFieldName("declarator", lang)
		if next == nil {
			return nil
		}
		decl = next
	}
	return nil
}

func cCollectDefs(w *cstWalk, unit *gts.Node) {
	for i := 0; i < unit.ChildCount(); i++ {
		c := unit.Child(i)
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
		case "struct_specifier", "union_specifier", "enum_specifier":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				w.addDef(name.Text(w.src), KindType, nodePoint(name))
			}
		case "declaration":
			cCollectDeclVars(w, c)
		}
	}
}

func cCollectDeclVars(w *cstWalk, decl *gts.Node) {
	for i := 0; i < decl.ChildCount(); i++ {
		c := decl.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "init_declarator":
			if name := cDeclaratorName(c.ChildByFieldName("declarator", w.lang), w.lang); name != nil {
				w.addDef(name.Text(w.src), KindVariable, nodePoint(name))
			}
		case "identifier":
			w.addDef(c.Text(w.src), KindVariable, nodePoint(c))
		}
	}
}

func cResolveUses(w *cstWalk, unit *gts.Node) {
	for i := 0; i < unit.ChildCount(); i++ {
		c := unit.Child(i)
		if c == nil || c.Type(w.lang) != "function_definition" {
			continue
		}
		decl := c.ChildByFieldName("declarator", w.lang)
		if decl == nil {
			continue
		}
		name := cDeclaratorName(decl, w.lang)
		if name == nil {
			continue
		}
		cScanBody(w, c, name.Text(w.src))
	}
}

func cScanBody(w *cstWalk, n *gts.Node, ownerBare string) {
	if n == nil {
		return
	}
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		if c.Type(w.lang) == "call_expression" {
			cHandleCall(w, c, ownerBare)
		}
		cScanBody(w, c, ownerBare)
	}
}

func cHandleCall(w *cstWalk, call *gts.Node, ownerBare string) {
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
		if arg == nil || field == nil {
			return
		}
		w.callSelector(ownerBare, arg.Text(w.src), field.Text(w.src), nodePoint(field))
	}
}

// cIncludes records #include directives as ImportSpecs (the included header path).
func cIncludes(t *cAST) []ImportSpec {
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
