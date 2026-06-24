package parse

import (
	"context"
	"fmt"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/core/model"
)

// PythonParser is the SW-054 curated tier-1 Python parser. It clones the SW-053
// recipe over the pure-Go gotreesitter Python grammar (CGo-free; default tier stays
// green under CGO_ENABLED=0 and passes internal/cgoconformance; grammar blob
// Go-embedded behind subset tag grammar_subset_python). PythonParser carries no
// mutable state and is safe for concurrent use.
type PythonParser struct {
	lang      *gts.Language
	extractor SymbolExtractor
}

// NewPythonParser returns a ready PythonParser wired to the pure-Go Python grammar.
func NewPythonParser() *PythonParser {
	lang := grammars.PythonLanguage()
	return &PythonParser{lang: lang, extractor: &pySymbolExtractor{lang: lang}}
}

// Language implements Parser.
func (*PythonParser) Language() string { return "python" }

// Runtime implements Parser: pure-Go gotreesitter tree-sitter runtime (CGo-free).
func (*PythonParser) Runtime() Runtime { return RuntimeGoTreeSitter }

// Extensions implements Parser.
func (*PythonParser) Extensions() []string { return []string{".py"} }

type pyAST struct {
	root *gts.Node
	src  []byte
	lang *gts.Language
}

// Parse implements Parser.
func (p *PythonParser) Parse(ctx context.Context, filename string, src []byte) (res *ParseResult, err error) {
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
		return nil, fmt.Errorf("parse: python error in %q: %w", filename, perr)
	}
	root := &pyAST{root: tree.RootNode(), src: src, lang: p.lang}

	extractor := p.extractor
	if extractor == nil {
		extractor = &pySymbolExtractor{lang: p.lang}
	}
	nodes, edges, pending, xerr := extractor.Extract(filename, root)
	if xerr != nil {
		return nil, fmt.Errorf("parse: python extraction in %q: %w", filename, xerr)
	}

	imports := pyImports(root)
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

// Kind mapping (Python collapses onto {file, function, method, type, variable}):
//
//	function ← module-level function_definition
//	method   ← function_definition inside a class body
//	type     ← class_definition
//	variable ← module-level assignment target (Python has no const/var distinction,
//	           so constant is ABSENT BY DESIGN at this tier)
//
// Absent by design: constant (no language-level const), interfaces/enums.

type pySymbolExtractor struct{ lang *gts.Language }

// Language implements SymbolExtractor.
func (*pySymbolExtractor) Language() string { return "python" }

// Extract implements SymbolExtractor for Python.
func (e *pySymbolExtractor) Extract(filename string, root any) ([]model.Node, []model.Edge, []PendingRef, error) {
	t, ok := root.(*pyAST)
	if !ok || t == nil || t.root == nil {
		return nil, nil, nil, fmt.Errorf("parse: python extractor: expected non-nil *pyAST root for %q, got %T", filename, root)
	}
	w := newCSTWalk(t.lang, t.src, langPackage(filename))
	// SW-055 AC#6: fail-closed parse-depth guard on untrusted input (skips the
	// file with structured, source-free provenance if nesting exceeds the bound).
	if derr := w.guardDepth(t.root, filename, "python"); derr != nil {
		return nil, nil, nil, derr
	}
	pyCollectDefs(w, t.root)
	pyResolveUses(w, t.root)
	return w.finishExtract(filename, "python")
}

func pyCollectDefs(w *cstWalk, module *gts.Node) {
	for i := 0; i < module.ChildCount(); i++ {
		c := module.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "function_definition":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				w.addDef(name.Text(w.src), KindFunction, nodePoint(name))
			}
		case "class_definition":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				w.addDef(name.Text(w.src), KindType, nodePoint(name))
			}
			if body := c.ChildByFieldName("body", w.lang); body != nil {
				pyCollectMethods(w, body)
			}
		case "assignment":
			pyCollectAssignTarget(w, c)
		case "expression_statement":
			// Module-level assignments may be wrapped in expression_statement.
			for j := 0; j < c.ChildCount(); j++ {
				if a := c.Child(j); a != nil && a.Type(w.lang) == "assignment" {
					pyCollectAssignTarget(w, a)
				}
			}
		}
	}
}

func pyCollectMethods(w *cstWalk, body *gts.Node) {
	for i := 0; i < body.ChildCount(); i++ {
		c := body.Child(i)
		if c != nil && c.Type(w.lang) == "function_definition" {
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				w.addDef(name.Text(w.src), KindMethod, nodePoint(name))
			}
		}
	}
}

func pyCollectAssignTarget(w *cstWalk, assign *gts.Node) {
	left := assign.ChildByFieldName("left", w.lang)
	if left == nil || left.Type(w.lang) != "identifier" {
		return // tuple/attribute targets out of scope this slice
	}
	w.addDef(left.Text(w.src), KindVariable, nodePoint(left))
}

func pyResolveUses(w *cstWalk, module *gts.Node) {
	for i := 0; i < module.ChildCount(); i++ {
		c := module.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "function_definition":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				pyScanBody(w, c, name.Text(w.src))
			}
		case "class_definition":
			if body := c.ChildByFieldName("body", w.lang); body != nil {
				for j := 0; j < body.ChildCount(); j++ {
					m := body.Child(j)
					if m != nil && m.Type(w.lang) == "function_definition" {
						if name := m.ChildByFieldName("name", w.lang); name != nil {
							pyScanBody(w, m, name.Text(w.src))
						}
					}
				}
			}
		}
	}
}

func pyScanBody(w *cstWalk, n *gts.Node, ownerBare string) {
	if n == nil {
		return
	}
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		if c.Type(w.lang) == "call" {
			pyHandleCall(w, c, ownerBare)
		}
		pyScanBody(w, c, ownerBare)
	}
}

func pyHandleCall(w *cstWalk, call *gts.Node, ownerBare string) {
	fn := call.ChildByFieldName("function", w.lang)
	if fn == nil {
		return
	}
	switch fn.Type(w.lang) {
	case "identifier":
		w.callBare(ownerBare, fn.Text(w.src), nodePoint(fn))
	case "attribute":
		obj := fn.ChildByFieldName("object", w.lang)
		attr := fn.ChildByFieldName("attribute", w.lang)
		if obj == nil || attr == nil {
			return
		}
		w.callSelector(ownerBare, obj.Text(w.src), attr.Text(w.src), nodePoint(attr))
	}
}

// pyImports records `import x` and `from m import n` declarations as ImportSpecs.
func pyImports(t *pyAST) []ImportSpec {
	if t == nil || t.root == nil {
		return nil
	}
	var out []ImportSpec
	root := t.root
	for i := 0; i < root.ChildCount(); i++ {
		c := root.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(t.lang) {
		case "import_statement":
			// import os  / import os, sys
			for j := 0; j < c.ChildCount(); j++ {
				d := c.Child(j)
				if d != nil && d.Type(t.lang) == "dotted_name" {
					out = append(out, ImportSpec{Alias: d.Text(t.src), Path: d.Text(t.src)})
				}
			}
		case "import_from_statement":
			mod := childByType(c, "dotted_name", t.lang)
			if mod == nil {
				continue
			}
			modPath := mod.Text(t.src)
			// Each subsequent dotted_name is an imported name bound under modPath.
			var names []*gts.Node
			for j := 0; j < c.ChildCount(); j++ {
				if d := c.Child(j); d != nil && d.Type(t.lang) == "dotted_name" {
					names = append(names, d)
				}
			}
			if len(names) <= 1 {
				out = append(out, ImportSpec{Alias: modPath, Path: modPath})
				continue
			}
			for _, nm := range names[1:] {
				out = append(out, ImportSpec{Alias: nm.Text(t.src), Path: modPath})
			}
		}
	}
	return out
}
