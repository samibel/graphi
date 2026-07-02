package parse

import (
	"context"
	"fmt"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/core/model"
)

// RubyParser is the SW-054 curated tier-1 Ruby parser. It clones the SW-053 recipe
// over the pure-Go gotreesitter Ruby grammar (CGo-free; default tier green under
// CGO_ENABLED=0; grammar blob Go-embedded behind subset tag grammar_subset_ruby).
// RubyParser carries no mutable state and is safe for concurrent use.
type RubyParser struct {
	lang      *gts.Language
	extractor SymbolExtractor
}

// NewRubyParser returns a ready RubyParser wired to the pure-Go Ruby grammar.
func NewRubyParser() *RubyParser {
	lang := grammars.RubyLanguage()
	return &RubyParser{lang: lang, extractor: &rubySymbolExtractor{lang: lang}}
}

// Language implements Parser.
func (*RubyParser) Language() string { return "ruby" }

// Runtime implements Parser: pure-Go gotreesitter tree-sitter runtime (CGo-free).
func (*RubyParser) Runtime() Runtime { return RuntimeGoTreeSitter }

// Extensions implements Parser.
func (*RubyParser) Extensions() []string { return []string{".rb"} }

type rubyAST struct {
	root *gts.Node
	src  []byte
	lang *gts.Language
}

// Parse implements Parser.
func (p *RubyParser) Parse(ctx context.Context, filename string, src []byte) (res *ParseResult, err error) {
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
		return nil, fmt.Errorf("parse: ruby error in %q: %w", filename, perr)
	}
	root := &rubyAST{root: tree.RootNode(), src: src, lang: p.lang}

	extractor := p.extractor
	if extractor == nil {
		extractor = &rubySymbolExtractor{lang: p.lang}
	}
	nodes, edges, pending, xerr := extractor.Extract(filename, root)
	if xerr != nil {
		return nil, fmt.Errorf("parse: ruby extraction in %q: %w", filename, xerr)
	}

	imports := rubyImports(root)
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

// Kind mapping (Ruby collapses onto {file, function, method, type, constant}):
//
//	function ← top-level `method` (def) node
//	method   ← `method` (def) inside a class/module body
//	type     ← class / module declaration
//	constant ← top-level assignment whose target is a constant (CamelCase/UPPER)
//
// Absent by design: variable (Ruby local-var distinction is out of the top-level node
// set this slice). `require`/`require_relative` calls are recorded as ImportSpecs.

type rubySymbolExtractor struct{ lang *gts.Language }

// Language implements SymbolExtractor.
func (*rubySymbolExtractor) Language() string { return "ruby" }

// Extract implements SymbolExtractor for Ruby.
func (e *rubySymbolExtractor) Extract(filename string, root any) ([]model.Node, []model.Edge, []PendingRef, error) {
	t, ok := root.(*rubyAST)
	if !ok || t == nil || t.root == nil {
		return nil, nil, nil, fmt.Errorf("parse: ruby extractor: expected non-nil *rubyAST root for %q, got %T", filename, root)
	}
	w := newCSTWalk(t.lang, t.src, langPackage(filename))
	// SW-055 AC#6: fail-closed parse-depth guard on untrusted input (skips the
	// file with structured, source-free provenance if nesting exceeds the bound).
	if derr := w.guardDepth(t.root, filename, "ruby"); derr != nil {
		return nil, nil, nil, derr
	}
	rubyCollectDefs(w, t.root, false)
	rubyResolveUses(w, t.root, false)
	return w.finishExtract(filename, "ruby")
}

func rubyCollectDefs(w *cstWalk, n *gts.Node, inClass bool) {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "method":
			kind := KindFunction
			if inClass {
				kind = KindMethod
			}
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				w.addDef(name.Text(w.src), kind, nodePoint(name))
			}
		case "class", "module":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				w.addDef(name.Text(w.src), KindType, nodePoint(name))
			}
			if body := childByType(c, "body_statement", w.lang); body != nil {
				rubyCollectDefs(w, body, true)
			}
		case "assignment":
			if !inClass {
				left := c.ChildByFieldName("left", w.lang)
				if left != nil && left.Type(w.lang) == "constant" {
					w.addDef(left.Text(w.src), KindConstant, nodePoint(left))
				}
			}
		}
	}
}

func rubyResolveUses(w *cstWalk, n *gts.Node, inClass bool) {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "method":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				rubyScanBody(w, c, name.Text(w.src))
			}
		case "class", "module":
			if body := childByType(c, "body_statement", w.lang); body != nil {
				rubyResolveUses(w, body, true)
			}
		}
	}
}

func rubyScanBody(w *cstWalk, n *gts.Node, ownerBare string) {
	if n == nil {
		return
	}
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		if c.Type(w.lang) == "call" {
			rubyHandleCall(w, c, ownerBare)
		}
		rubyScanBody(w, c, ownerBare)
	}
}

func rubyHandleCall(w *cstWalk, call *gts.Node, ownerBare string) {
	recv := call.ChildByFieldName("receiver", w.lang)
	m := call.ChildByFieldName("method", w.lang)
	if m == nil {
		return
	}
	if recv == nil {
		w.callBare(ownerBare, m.Text(w.src), nodePoint(m))
		return
	}
	w.callSelector(ownerBare, recv.Text(w.src), m.Text(w.src), nodePoint(m))
}

// rubyImports records `require`/`require_relative "x"` calls as ImportSpecs.
func rubyImports(t *rubyAST) []ImportSpec {
	if t == nil || t.root == nil {
		return nil
	}
	var out []ImportSpec
	var walk func(n *gts.Node)
	walk = func(n *gts.Node) {
		if n == nil {
			return
		}
		if n.Type(t.lang) == "call" {
			m := n.ChildByFieldName("method", t.lang)
			recv := n.ChildByFieldName("receiver", t.lang)
			if recv == nil && m != nil {
				name := m.Text(t.src)
				if name == "require" || name == "require_relative" {
					if args := n.ChildByFieldName("arguments", t.lang); args != nil {
						if s := childByType(args, "string", t.lang); s != nil {
							if path := rubyStringContent(s, t.lang, t.src); path != "" {
								out = append(out, ImportSpec{Path: path})
							}
						}
					}
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

func rubyStringContent(s *gts.Node, lang *gts.Language, src []byte) string {
	if frag := childByType(s, "string_content", lang); frag != nil {
		return frag.Text(src)
	}
	return strings.Trim(s.Text(src), "\"'")
}
