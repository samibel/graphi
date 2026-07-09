package parse

import (
	"context"
	"fmt"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/core/model"
)

// CSharpParser is the SW-054 curated tier-1 C# parser. It clones the SW-053 recipe
// over the pure-Go gotreesitter C# grammar (CGo-free; default tier green under
// CGO_ENABLED=0; grammar blob Go-embedded behind subset tag grammar_subset_c_sharp).
// CSharpParser carries no mutable state and is safe for concurrent use.
type CSharpParser struct {
	lang      *gts.Language
	extractor SymbolExtractor
}

// NewCSharpParser returns a ready CSharpParser wired to the pure-Go C# grammar.
func NewCSharpParser() *CSharpParser {
	lang := grammars.CSharpLanguage()
	return &CSharpParser{lang: lang, extractor: &csSymbolExtractor{lang: lang}}
}

// Language implements Parser.
func (*CSharpParser) Language() string { return "c_sharp" }

// Runtime implements Parser: pure-Go gotreesitter tree-sitter runtime (CGo-free).
func (*CSharpParser) Runtime() Runtime { return RuntimeGoTreeSitter }

// Extensions implements Parser.
func (*CSharpParser) Extensions() []string { return []string{".cs"} }

type csAST struct {
	root *gts.Node
	src  []byte
	lang *gts.Language
}

// Parse implements Parser.
func (p *CSharpParser) Parse(ctx context.Context, filename string, src []byte) (res *ParseResult, err error) {
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
		return nil, fmt.Errorf("parse: c_sharp error in %q: %w", filename, perr)
	}
	root := &csAST{root: tree.RootNode(), src: src, lang: p.lang}

	extractor := p.extractor
	if extractor == nil {
		extractor = &csSymbolExtractor{lang: p.lang}
	}
	nodes, edges, pending, xerr := extractor.Extract(filename, root)
	if xerr != nil {
		return nil, fmt.Errorf("parse: c_sharp extraction in %q: %w", filename, xerr)
	}

	imports := csImports(root)
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

// Kind mapping (C# collapses onto {file, method, type}):
//
//	type   ← class_declaration / interface_declaration / struct_declaration / enum_declaration
//	method ← method_declaration (C# has no free functions; all callables are methods)
//
// Definitions are discovered inside (possibly nested) namespace_declaration bodies.
// Absent by design: function (no free functions), variable/constant (fields out of the
// top-level node set this slice).

type csSymbolExtractor struct{ lang *gts.Language }

// Language implements SymbolExtractor.
func (*csSymbolExtractor) Language() string { return "c_sharp" }

// Extract implements SymbolExtractor for C#.
func (e *csSymbolExtractor) Extract(filename string, root any) ([]model.Node, []model.Edge, []PendingRef, error) {
	t, ok := root.(*csAST)
	if !ok || t == nil || t.root == nil {
		return nil, nil, nil, fmt.Errorf("parse: c_sharp extractor: expected non-nil *csAST root for %q, got %T", filename, root)
	}
	w := newCSTWalk(t.lang, t.src, langPackage(filename))
	// SW-055 AC#6: fail-closed parse-depth guard on untrusted input (skips the
	// file with structured, source-free provenance if nesting exceeds the bound).
	if derr := w.guardDepth(t.root, filename, "c_sharp"); derr != nil {
		return nil, nil, nil, derr
	}
	csCollectDefs(w, t.root)
	csResolveUses(w, t.root)
	return w.finishExtract(filename, "c_sharp")
}

// csTypeContainers walks a node's children (and into namespace declaration_lists) to
// visit each type declaration, calling fn on it.
func csVisitTypes(w *cstWalk, n *gts.Node, fn func(typeDecl *gts.Node)) {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "class_declaration", "interface_declaration", "struct_declaration", "enum_declaration", "record_declaration":
			fn(c)
		case "namespace_declaration", "file_scoped_namespace_declaration":
			if body := childByType(c, "declaration_list", w.lang); body != nil {
				csVisitTypes(w, body, fn)
			} else {
				csVisitTypes(w, c, fn)
			}
		case "declaration_list":
			csVisitTypes(w, c, fn)
		}
	}
}

func csCollectDefs(w *cstWalk, unit *gts.Node) {
	csVisitTypes(w, unit, func(td *gts.Node) {
		if name := td.ChildByFieldName("name", w.lang); name != nil {
			bare := name.Text(w.src)
			w.addDef(bare, KindType, nodePoint(name))
			w.setDefMeta(bare, csDeclMeta(w, td))
		}
		if body := childByType(td, "declaration_list", w.lang); body != nil {
			for i := 0; i < body.ChildCount(); i++ {
				m := body.Child(i)
				if m != nil && m.Type(w.lang) == "method_declaration" {
					if name := m.ChildByFieldName("name", w.lang); name != nil {
						bare := name.Text(w.src)
						w.addDef(bare, KindMethod, nodePoint(name))
						// WP-14 follow-up: the `override` modifier becomes the "override"
						// entry-point flag (an override is invoked polymorphically through
						// its base type), plus any attribute names.
						w.setDefMeta(bare, csDeclMeta(w, m))
					}
				}
			}
		}
	})
}

// csDeclMeta derives the NON-identity NodeMeta for a C# type/method declaration:
// the "override" flag from an `override` modifier and any attribute NAMES from
// the declaration's `attribute_list` children. NewNodeMeta sorts+dedups, so the
// result is a deterministic, pure function of the source.
func csDeclMeta(w *cstWalk, decl *gts.Node) model.NodeMeta {
	var annotations, flags []string
	for i := 0; i < decl.ChildCount(); i++ {
		c := decl.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "modifier":
			if c.Text(w.src) == "override" {
				flags = append(flags, "override")
			}
		case "attribute_list":
			for j := 0; j < c.ChildCount(); j++ {
				a := c.Child(j)
				if a != nil && a.Type(w.lang) == "attribute" {
					if name := csAttributeName(w, a); name != "" {
						annotations = append(annotations, name)
					}
				}
			}
		}
	}
	return model.NewNodeMeta(annotations, flags)
}

// csAttributeName extracts the bare attribute identifier — the trailing segment
// of a scoped name (`System.ObsoleteAttribute` → "ObsoleteAttribute") — from a C#
// `attribute` node. Returns "" when no name resolves.
func csAttributeName(w *cstWalk, attr *gts.Node) string {
	name := attr.ChildByFieldName("name", w.lang)
	if name == nil {
		for i := 0; i < attr.ChildCount(); i++ {
			c := attr.Child(i)
			if c != nil {
				if t := c.Type(w.lang); t == "identifier" || t == "qualified_name" {
					name = c
					break
				}
			}
		}
	}
	if name == nil {
		return ""
	}
	return trailingDotSegment(name.Text(w.src))
}

// trailingDotSegment returns the segment after the final '.' ("a.b.C" → "C"), or
// the whole string when there is no dot.
func trailingDotSegment(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return s[i+1:]
		}
	}
	return s
}

func csResolveUses(w *cstWalk, unit *gts.Node) {
	csVisitTypes(w, unit, func(td *gts.Node) {
		if body := childByType(td, "declaration_list", w.lang); body != nil {
			for i := 0; i < body.ChildCount(); i++ {
				m := body.Child(i)
				if m != nil && m.Type(w.lang) == "method_declaration" {
					if name := m.ChildByFieldName("name", w.lang); name != nil {
						csScanBody(w, m, name.Text(w.src))
					}
				}
			}
		}
	})
}

func csScanBody(w *cstWalk, n *gts.Node, ownerBare string) {
	if n == nil {
		return
	}
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		if c.Type(w.lang) == "invocation_expression" {
			csHandleCall(w, c, ownerBare)
		}
		csScanBody(w, c, ownerBare)
	}
}

func csHandleCall(w *cstWalk, call *gts.Node, ownerBare string) {
	fn := call.ChildByFieldName("function", w.lang)
	if fn == nil {
		return
	}
	switch fn.Type(w.lang) {
	case "identifier":
		w.callBare(ownerBare, fn.Text(w.src), nodePoint(fn))
	case "member_access_expression":
		expr := fn.ChildByFieldName("expression", w.lang)
		name := fn.ChildByFieldName("name", w.lang)
		if expr == nil || name == nil {
			return
		}
		w.callSelector(ownerBare, expr.Text(w.src), name.Text(w.src), nodePoint(name))
	}
}

// csImports records using directives as ImportSpecs (the imported namespace).
func csImports(t *csAST) []ImportSpec {
	if t == nil || t.root == nil {
		return nil
	}
	var out []ImportSpec
	root := t.root
	for i := 0; i < root.ChildCount(); i++ {
		c := root.Child(i)
		if c == nil || c.Type(t.lang) != "using_directive" {
			continue
		}
		var ns *gts.Node
		for j := 0; j < c.ChildCount(); j++ {
			d := c.Child(j)
			if d != nil {
				switch d.Type(t.lang) {
				case "identifier", "qualified_name":
					ns = d
				}
			}
		}
		if ns != nil {
			out = append(out, ImportSpec{Path: ns.Text(t.src)})
		}
	}
	return out
}
