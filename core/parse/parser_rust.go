package parse

import (
	"context"
	"fmt"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/core/model"
)

// RustParser is the SW-054 curated tier-1 Rust parser. It clones the SW-053 recipe
// over the pure-Go gotreesitter Rust grammar (CGo-free; default tier green under
// CGO_ENABLED=0; grammar blob Go-embedded behind subset tag grammar_subset_rust).
// RustParser carries no mutable state and is safe for concurrent use.
type RustParser struct {
	lang      *gts.Language
	extractor SymbolExtractor
}

// NewRustParser returns a ready RustParser wired to the pure-Go Rust grammar.
func NewRustParser() *RustParser {
	lang := grammars.RustLanguage()
	return &RustParser{lang: lang, extractor: &rustSymbolExtractor{lang: lang}}
}

// Language implements Parser.
func (*RustParser) Language() string { return "rust" }

// Extensions implements Parser.
func (*RustParser) Extensions() []string { return []string{".rs"} }

type rustAST struct {
	root *gts.Node
	src  []byte
	lang *gts.Language
}

// Parse implements Parser.
func (p *RustParser) Parse(ctx context.Context, filename string, src []byte) (res *ParseResult, err error) {
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
		return nil, fmt.Errorf("parse: rust error in %q: %w", filename, perr)
	}
	root := &rustAST{root: tree.RootNode(), src: src, lang: p.lang}

	extractor := p.extractor
	if extractor == nil {
		extractor = &rustSymbolExtractor{lang: p.lang}
	}
	nodes, edges, pending, xerr := extractor.Extract(filename, root)
	if xerr != nil {
		return nil, fmt.Errorf("parse: rust extraction in %q: %w", filename, xerr)
	}

	imports := rustImports(root)
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

// Kind mapping (Rust collapses onto {file, function, type, constant}):
//
//	function ← function_item
//	type     ← struct_item / enum_item / trait_item / union_item / type_item
//	constant ← const_item / static_item
//
// Absent by design: method (impl methods out of the top-level node set this slice),
// variable (Rust `let` bindings are function-local). `use` declarations are recorded as
// ImportSpecs.

type rustSymbolExtractor struct{ lang *gts.Language }

// Language implements SymbolExtractor.
func (*rustSymbolExtractor) Language() string { return "rust" }

// Extract implements SymbolExtractor for Rust.
func (e *rustSymbolExtractor) Extract(filename string, root any) ([]model.Node, []model.Edge, []PendingRef, error) {
	t, ok := root.(*rustAST)
	if !ok || t == nil || t.root == nil {
		return nil, nil, nil, fmt.Errorf("parse: rust extractor: expected non-nil *rustAST root for %q, got %T", filename, root)
	}
	w := newCSTWalk(t.lang, t.src, langPackage(filename))
	rustCollectDefs(w, t.root)
	rustResolveUses(w, t.root)
	return w.finishExtract(filename, "rust")
}

func rustCollectDefs(w *cstWalk, unit *gts.Node) {
	for i := 0; i < unit.ChildCount(); i++ {
		c := unit.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "function_item":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				w.addDef(name.Text(w.src), KindFunction, nodePoint(name))
			}
		case "struct_item", "enum_item", "trait_item", "union_item", "type_item":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				w.addDef(name.Text(w.src), KindType, nodePoint(name))
			}
		case "const_item", "static_item":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				w.addDef(name.Text(w.src), KindConstant, nodePoint(name))
			}
		}
	}
}

func rustResolveUses(w *cstWalk, unit *gts.Node) {
	for i := 0; i < unit.ChildCount(); i++ {
		c := unit.Child(i)
		if c == nil || c.Type(w.lang) != "function_item" {
			continue
		}
		if name := c.ChildByFieldName("name", w.lang); name != nil {
			rustScanBody(w, c, name.Text(w.src))
		}
	}
}

func rustScanBody(w *cstWalk, n *gts.Node, ownerBare string) {
	if n == nil {
		return
	}
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		if c.Type(w.lang) == "call_expression" {
			rustHandleCall(w, c, ownerBare)
		}
		rustScanBody(w, c, ownerBare)
	}
}

func rustHandleCall(w *cstWalk, call *gts.Node, ownerBare string) {
	fn := call.ChildByFieldName("function", w.lang)
	if fn == nil {
		return
	}
	switch fn.Type(w.lang) {
	case "identifier":
		w.callBare(ownerBare, fn.Text(w.src), nodePoint(fn))
	case "field_expression":
		val := fn.ChildByFieldName("value", w.lang)
		field := fn.ChildByFieldName("field", w.lang)
		if val == nil || field == nil {
			return
		}
		w.callSelector(ownerBare, val.Text(w.src), field.Text(w.src), nodePoint(field))
	case "scoped_identifier":
		path := fn.ChildByFieldName("path", w.lang)
		name := fn.ChildByFieldName("name", w.lang)
		if path == nil || name == nil {
			return
		}
		w.callSelector(ownerBare, path.Text(w.src), name.Text(w.src), nodePoint(name))
	}
}

// rustImports records `use` declarations as ImportSpecs (the full use path).
func rustImports(t *rustAST) []ImportSpec {
	if t == nil || t.root == nil {
		return nil
	}
	var out []ImportSpec
	root := t.root
	for i := 0; i < root.ChildCount(); i++ {
		c := root.Child(i)
		if c == nil || c.Type(t.lang) != "use_declaration" {
			continue
		}
		arg := c.ChildByFieldName("argument", t.lang)
		if arg == nil {
			// fall back to first scoped/identifier child
			for j := 0; j < c.ChildCount(); j++ {
				d := c.Child(j)
				if d != nil {
					switch d.Type(t.lang) {
					case "scoped_identifier", "identifier", "use_wildcard", "scoped_use_list", "use_list":
						arg = d
					}
				}
			}
		}
		if arg == nil {
			continue
		}
		path := arg.Text(t.src)
		out = append(out, ImportSpec{Path: path})
	}
	return out
}
