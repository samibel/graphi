package parse

import (
	"context"
	"fmt"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/core/model"
)

// PHPParser is the SW-054 curated tier-1 PHP parser. It clones the SW-053 recipe over
// the pure-Go gotreesitter PHP grammar (CGo-free; default tier green under
// CGO_ENABLED=0; grammar blob Go-embedded behind subset tag grammar_subset_php).
// PHPParser carries no mutable state and is safe for concurrent use.
type PHPParser struct {
	lang      *gts.Language
	extractor SymbolExtractor
}

// NewPHPParser returns a ready PHPParser wired to the pure-Go PHP grammar.
func NewPHPParser() *PHPParser {
	lang := grammars.PhpLanguage()
	return &PHPParser{lang: lang, extractor: &phpSymbolExtractor{lang: lang}}
}

// Language implements Parser.
func (*PHPParser) Language() string { return "php" }

// Runtime implements Parser: pure-Go gotreesitter tree-sitter runtime (CGo-free).
func (*PHPParser) Runtime() Runtime { return RuntimeGoTreeSitter }

// Extensions implements Parser.
func (*PHPParser) Extensions() []string { return []string{".php"} }

type phpAST struct {
	root *gts.Node
	src  []byte
	lang *gts.Language
}

// Parse implements Parser.
func (p *PHPParser) Parse(ctx context.Context, filename string, src []byte) (res *ParseResult, err error) {
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
		return nil, fmt.Errorf("parse: php error in %q: %w", filename, perr)
	}
	root := &phpAST{root: tree.RootNode(), src: src, lang: p.lang}

	extractor := p.extractor
	if extractor == nil {
		extractor = &phpSymbolExtractor{lang: p.lang}
	}
	nodes, edges, pending, xerr := extractor.Extract(filename, root)
	if xerr != nil {
		return nil, fmt.Errorf("parse: php extraction in %q: %w", filename, xerr)
	}

	imports := phpImports(root)
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

// Kind mapping (PHP collapses onto {file, function, method, type, constant}):
//
//	function ← function_definition (top-level)
//	method   ← method_declaration inside a class body
//	type     ← class_declaration / interface_declaration / trait_declaration
//	constant ← const_declaration's const_element
//
// Absent by design: variable (PHP `$vars` are statement-local, out of the top-level
// node set this slice). `require`/`include` are recorded as ImportSpecs.

type phpSymbolExtractor struct{ lang *gts.Language }

// Language implements SymbolExtractor.
func (*phpSymbolExtractor) Language() string { return "php" }

// Extract implements SymbolExtractor for PHP.
func (e *phpSymbolExtractor) Extract(filename string, root any) ([]model.Node, []model.Edge, []PendingRef, error) {
	t, ok := root.(*phpAST)
	if !ok || t == nil || t.root == nil {
		return nil, nil, nil, fmt.Errorf("parse: php extractor: expected non-nil *phpAST root for %q, got %T", filename, root)
	}
	w := newCSTWalk(t.lang, t.src, langPackage(filename))
	// SW-055 AC#6: fail-closed parse-depth guard on untrusted input (skips the
	// file with structured, source-free provenance if nesting exceeds the bound).
	if derr := w.guardDepth(t.root, filename, "php"); derr != nil {
		return nil, nil, nil, derr
	}
	phpCollectDefs(w, t.root)
	phpResolveUses(w, t.root)
	return w.finishExtract(filename, "php")
}

func phpCollectDefs(w *cstWalk, program *gts.Node) {
	for i := 0; i < program.ChildCount(); i++ {
		c := program.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "function_definition":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				w.addDef(name.Text(w.src), KindFunction, nodePoint(name))
			}
		case "class_declaration", "interface_declaration", "trait_declaration", "enum_declaration":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				w.addDef(name.Text(w.src), KindType, nodePoint(name))
			}
			if body := childByType(c, "declaration_list", w.lang); body != nil {
				phpCollectMethods(w, body)
			}
		case "const_declaration":
			for j := 0; j < c.ChildCount(); j++ {
				if el := c.Child(j); el != nil && el.Type(w.lang) == "const_element" {
					if name := childByType(el, "name", w.lang); name != nil {
						w.addDef(name.Text(w.src), KindConstant, nodePoint(name))
					}
				}
			}
		}
	}
}

func phpCollectMethods(w *cstWalk, body *gts.Node) {
	for i := 0; i < body.ChildCount(); i++ {
		c := body.Child(i)
		if c != nil && c.Type(w.lang) == "method_declaration" {
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				w.addDef(name.Text(w.src), KindMethod, nodePoint(name))
			}
		}
	}
}

func phpResolveUses(w *cstWalk, program *gts.Node) {
	for i := 0; i < program.ChildCount(); i++ {
		c := program.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "function_definition":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				phpScanBody(w, c, name.Text(w.src))
			}
		case "class_declaration", "interface_declaration", "trait_declaration", "enum_declaration":
			if body := childByType(c, "declaration_list", w.lang); body != nil {
				for j := 0; j < body.ChildCount(); j++ {
					m := body.Child(j)
					if m != nil && m.Type(w.lang) == "method_declaration" {
						if name := m.ChildByFieldName("name", w.lang); name != nil {
							phpScanBody(w, m, name.Text(w.src))
						}
					}
				}
			}
		}
	}
}

func phpScanBody(w *cstWalk, n *gts.Node, ownerBare string) {
	if n == nil {
		return
	}
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "function_call_expression":
			if fn := c.ChildByFieldName("function", w.lang); fn != nil && fn.Type(w.lang) == "name" {
				w.callBare(ownerBare, fn.Text(w.src), nodePoint(fn))
			}
		case "member_call_expression":
			obj := c.ChildByFieldName("object", w.lang)
			if obj == nil && c.ChildCount() > 0 {
				obj = c.Child(0) // object is the leading child (e.g. variable_name)
			}
			name := c.ChildByFieldName("name", w.lang)
			if obj != nil && name != nil {
				w.callSelector(ownerBare, obj.Text(w.src), name.Text(w.src), nodePoint(name))
			}
		}
		phpScanBody(w, c, ownerBare)
	}
}

// phpImports records `require`/`require_once`/`include` expressions as ImportSpecs.
func phpImports(t *phpAST) []ImportSpec {
	if t == nil || t.root == nil {
		return nil
	}
	var out []ImportSpec
	var walk func(n *gts.Node)
	walk = func(n *gts.Node) {
		if n == nil {
			return
		}
		switch n.Type(t.lang) {
		case "require_expression", "require_once_expression", "include_expression", "include_once_expression":
			s := childByType(n, "string", t.lang)
			if s == nil {
				s = childByType(n, "encapsed_string", t.lang)
			}
			if s != nil {
				if path := phpStringContent(s, t.lang, t.src); path != "" {
					out = append(out, ImportSpec{Path: path})
				}
			}
		}
		for i := 0; i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(t.root)
	return out
}

func phpStringContent(s *gts.Node, lang *gts.Language, src []byte) string {
	if frag := childByType(s, "string_content", lang); frag != nil {
		return frag.Text(src)
	}
	txt := s.Text(src)
	if len(txt) >= 2 && (txt[0] == '"' || txt[0] == '\'') {
		return txt[1 : len(txt)-1]
	}
	return txt
}
