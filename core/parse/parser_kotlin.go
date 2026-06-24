package parse

import (
	"context"
	"fmt"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/core/model"
)

// KotlinParser is the SW-054 curated tier-1 Kotlin parser. It clones the SW-053 recipe
// over the pure-Go gotreesitter Kotlin grammar (CGo-free; default tier green under
// CGO_ENABLED=0; grammar blob Go-embedded behind subset tag grammar_subset_kotlin).
// The Kotlin grammar exposes names/callees as positional children (not fields), so the
// walk reads the leading simple_identifier rather than ChildByFieldName. KotlinParser
// carries no mutable state and is safe for concurrent use.
type KotlinParser struct {
	lang      *gts.Language
	extractor SymbolExtractor
}

// NewKotlinParser returns a ready KotlinParser wired to the pure-Go Kotlin grammar.
func NewKotlinParser() *KotlinParser {
	lang := grammars.KotlinLanguage()
	return &KotlinParser{lang: lang, extractor: &kotlinSymbolExtractor{lang: lang}}
}

// Language implements Parser.
func (*KotlinParser) Language() string { return "kotlin" }

// Runtime implements Parser: pure-Go gotreesitter tree-sitter runtime (CGo-free).
func (*KotlinParser) Runtime() Runtime { return RuntimeGoTreeSitter }

// Extensions implements Parser.
func (*KotlinParser) Extensions() []string { return []string{".kt"} }

type kotlinAST struct {
	root *gts.Node
	src  []byte
	lang *gts.Language
}

// Parse implements Parser.
func (p *KotlinParser) Parse(ctx context.Context, filename string, src []byte) (res *ParseResult, err error) {
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
		return nil, fmt.Errorf("parse: kotlin error in %q: %w", filename, perr)
	}
	root := &kotlinAST{root: tree.RootNode(), src: src, lang: p.lang}

	extractor := p.extractor
	if extractor == nil {
		extractor = &kotlinSymbolExtractor{lang: p.lang}
	}
	nodes, edges, pending, xerr := extractor.Extract(filename, root)
	if xerr != nil {
		return nil, fmt.Errorf("parse: kotlin extraction in %q: %w", filename, xerr)
	}

	imports := kotlinImports(root)
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

// Kind mapping (Kotlin collapses onto {file, function, method, type}):
//
//	function ← top-level function_declaration
//	method   ← function_declaration inside a class_body
//	type     ← class_declaration / object_declaration / interface (collapsed to type)
//
// Absent by design: variable/constant (Kotlin top-level property declarations are out
// of the node set this slice). `import` headers are recorded as ImportSpecs.

type kotlinSymbolExtractor struct{ lang *gts.Language }

// Language implements SymbolExtractor.
func (*kotlinSymbolExtractor) Language() string { return "kotlin" }

// Extract implements SymbolExtractor for Kotlin.
func (e *kotlinSymbolExtractor) Extract(filename string, root any) ([]model.Node, []model.Edge, []PendingRef, error) {
	t, ok := root.(*kotlinAST)
	if !ok || t == nil || t.root == nil {
		return nil, nil, nil, fmt.Errorf("parse: kotlin extractor: expected non-nil *kotlinAST root for %q, got %T", filename, root)
	}
	w := newCSTWalk(t.lang, t.src, langPackage(filename))
	// SW-055 AC#6: fail-closed parse-depth guard on untrusted input (skips the
	// file with structured, source-free provenance if nesting exceeds the bound).
	if derr := w.guardDepth(t.root, filename, "kotlin"); derr != nil {
		return nil, nil, nil, derr
	}
	kotlinCollectDefs(w, t.root, false)
	kotlinResolveUses(w, t.root, false)
	return w.finishExtract(filename, "kotlin")
}

// kotlinName returns the leading simple_identifier / type_identifier child of a
// declaration (its bound name), since the Kotlin grammar exposes it positionally.
func kotlinName(decl *gts.Node, lang *gts.Language) *gts.Node {
	for i := 0; i < decl.ChildCount(); i++ {
		c := decl.Child(i)
		if c == nil {
			continue
		}
		if t := c.Type(lang); t == "simple_identifier" || t == "type_identifier" {
			return c
		}
	}
	return nil
}

func kotlinCollectDefs(w *cstWalk, n *gts.Node, inClass bool) {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "function_declaration":
			kind := KindFunction
			if inClass {
				kind = KindMethod
			}
			if name := kotlinName(c, w.lang); name != nil {
				w.addDef(name.Text(w.src), kind, nodePoint(name))
			}
		case "class_declaration", "object_declaration":
			if name := kotlinName(c, w.lang); name != nil {
				w.addDef(name.Text(w.src), KindType, nodePoint(name))
			}
			if body := childByType(c, "class_body", w.lang); body != nil {
				kotlinCollectDefs(w, body, true)
			}
		}
	}
}

func kotlinResolveUses(w *cstWalk, n *gts.Node, inClass bool) {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "function_declaration":
			if name := kotlinName(c, w.lang); name != nil {
				kotlinScanBody(w, c, name.Text(w.src))
			}
		case "class_declaration", "object_declaration":
			if body := childByType(c, "class_body", w.lang); body != nil {
				kotlinResolveUses(w, body, true)
			}
		}
	}
}

func kotlinScanBody(w *cstWalk, n *gts.Node, ownerBare string) {
	if n == nil {
		return
	}
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		if c.Type(w.lang) == "call_expression" {
			kotlinHandleCall(w, c, ownerBare)
		}
		kotlinScanBody(w, c, ownerBare)
	}
}

func kotlinHandleCall(w *cstWalk, call *gts.Node, ownerBare string) {
	if call.ChildCount() == 0 {
		return
	}
	callee := call.Child(0)
	if callee == nil {
		return
	}
	switch callee.Type(w.lang) {
	case "simple_identifier":
		w.callBare(ownerBare, callee.Text(w.src), nodePoint(callee))
	case "navigation_expression":
		// obj.method() : navigation_expression = <expr> navigation_suffix(. name)
		base := callee.Child(0)
		var name *gts.Node
		if suffix := childByType(callee, "navigation_suffix", w.lang); suffix != nil {
			name = kotlinName(suffix, w.lang)
		}
		if base != nil && name != nil {
			w.callSelector(ownerBare, base.Text(w.src), name.Text(w.src), nodePoint(name))
		}
	}
}

// kotlinImports records import headers as ImportSpecs (the imported FQN).
func kotlinImports(t *kotlinAST) []ImportSpec {
	if t == nil || t.root == nil {
		return nil
	}
	var out []ImportSpec
	var walk func(n *gts.Node)
	walk = func(n *gts.Node) {
		if n == nil {
			return
		}
		if n.Type(t.lang) == "import_header" {
			if id := childByType(n, "identifier", t.lang); id != nil {
				out = append(out, ImportSpec{Path: id.Text(t.src)})
			}
		}
		for i := 0; i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(t.root)
	return out
}
