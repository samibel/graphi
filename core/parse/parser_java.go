package parse

import (
	"context"
	"fmt"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/core/model"
)

// JavaParser is the SW-054 curated tier-1 Java parser. It clones the SW-053 recipe
// over the pure-Go gotreesitter Java grammar (CGo-free; default tier green under
// CGO_ENABLED=0; grammar blob Go-embedded behind subset tag grammar_subset_java).
// JavaParser carries no mutable state and is safe for concurrent use.
type JavaParser struct {
	lang      *gts.Language
	extractor SymbolExtractor
}

// NewJavaParser returns a ready JavaParser wired to the pure-Go Java grammar.
func NewJavaParser() *JavaParser {
	lang := grammars.JavaLanguage()
	return &JavaParser{lang: lang, extractor: &javaSymbolExtractor{lang: lang}}
}

// Language implements Parser.
func (*JavaParser) Language() string { return "java" }

// Runtime implements Parser: pure-Go gotreesitter tree-sitter runtime (CGo-free).
func (*JavaParser) Runtime() Runtime { return RuntimeGoTreeSitter }

// Extensions implements Parser.
func (*JavaParser) Extensions() []string { return []string{".java"} }

type javaAST struct {
	root *gts.Node
	src  []byte
	lang *gts.Language
}

// Parse implements Parser.
func (p *JavaParser) Parse(ctx context.Context, filename string, src []byte) (res *ParseResult, err error) {
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
		return nil, fmt.Errorf("parse: java error in %q: %w", filename, perr)
	}
	root := &javaAST{root: tree.RootNode(), src: src, lang: p.lang}

	extractor := p.extractor
	if extractor == nil {
		extractor = &javaSymbolExtractor{lang: p.lang}
	}
	nodes, edges, pending, xerr := extractor.Extract(filename, root)
	if xerr != nil {
		return nil, fmt.Errorf("parse: java extraction in %q: %w", filename, xerr)
	}

	imports := javaImports(root)
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

// Kind mapping (Java collapses onto {file, method, type}):
//
//	type   ← class_declaration / interface_declaration / enum_declaration
//	method ← method_declaration (Java has no free functions; all callables are methods)
//
// Absent by design: function (no free functions), variable/constant (field
// declarations are out of the top-level node set this slice).

type javaSymbolExtractor struct{ lang *gts.Language }

// Language implements SymbolExtractor.
func (*javaSymbolExtractor) Language() string { return "java" }

// Extract implements SymbolExtractor for Java.
func (e *javaSymbolExtractor) Extract(filename string, root any) ([]model.Node, []model.Edge, []PendingRef, error) {
	t, ok := root.(*javaAST)
	if !ok || t == nil || t.root == nil {
		return nil, nil, nil, fmt.Errorf("parse: java extractor: expected non-nil *javaAST root for %q, got %T", filename, root)
	}
	w := newCSTWalk(t.lang, t.src, langPackage(filename))
	// SW-055 AC#6: fail-closed parse-depth guard on untrusted input (skips the
	// file with structured, source-free provenance if nesting exceeds the bound).
	if derr := w.guardDepth(t.root, filename, "java"); derr != nil {
		return nil, nil, nil, derr
	}
	javaCollectDefs(w, t.root)
	javaResolveUses(w, t.root)
	nodes, edges, pending, err := w.finishExtract(filename, "java")
	if err != nil {
		return nil, nil, nil, err
	}
	// WP-01: mint an interned package node keyed by the file's real
	// `package com.x.y;` declaration (empty source path ⇒ identical NodeId across
	// every file in the package). The linker attaches ONE file→package `imports`
	// edge to it, collapsing the cross-module file→file import fan-out. A file
	// with no package declaration mints no node.
	if pkg := javaPackagePath(t); pkg != "" {
		pn, perr := model.NewNode(KindPackage, pkg, "", 0, 0)
		if perr != nil {
			return nil, nil, nil, fmt.Errorf("parse: java package node for %q: %w", filename, perr)
		}
		nodes = append(nodes, pn)
	}
	return nodes, edges, pending, nil
}

// javaPackagePath returns the full dotted path of the file's `package_declaration`
// (e.g. "com.example.service"), or "" when the file declares no package. The
// scoped name is a scoped_identifier (multi-segment) or a bare identifier
// (single segment), mirroring javaImports.
func javaPackagePath(t *javaAST) string {
	if t == nil || t.root == nil {
		return ""
	}
	root := t.root
	for i := 0; i < root.ChildCount(); i++ {
		c := root.Child(i)
		if c == nil || c.Type(t.lang) != "package_declaration" {
			continue
		}
		if s := childByType(c, "scoped_identifier", t.lang); s != nil {
			return s.Text(t.src)
		}
		if id := childByType(c, "identifier", t.lang); id != nil {
			return id.Text(t.src)
		}
	}
	return ""
}

func javaCollectDefs(w *cstWalk, program *gts.Node) {
	for i := 0; i < program.ChildCount(); i++ {
		c := program.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "class_declaration", "interface_declaration", "enum_declaration":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				w.addDef(name.Text(w.src), KindType, nodePoint(name))
			}
			if body := c.ChildByFieldName("body", w.lang); body != nil {
				javaCollectMethods(w, body)
			}
		}
	}
}

func javaCollectMethods(w *cstWalk, body *gts.Node) {
	for i := 0; i < body.ChildCount(); i++ {
		c := body.Child(i)
		if c != nil && c.Type(w.lang) == "method_declaration" {
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				w.addDef(name.Text(w.src), KindMethod, nodePoint(name))
			}
		}
	}
}

func javaResolveUses(w *cstWalk, program *gts.Node) {
	for i := 0; i < program.ChildCount(); i++ {
		c := program.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "class_declaration", "interface_declaration", "enum_declaration":
			if body := c.ChildByFieldName("body", w.lang); body != nil {
				for j := 0; j < body.ChildCount(); j++ {
					m := body.Child(j)
					if m != nil && m.Type(w.lang) == "method_declaration" {
						if name := m.ChildByFieldName("name", w.lang); name != nil {
							javaScanBody(w, m, name.Text(w.src))
						}
					}
				}
			}
		}
	}
}

func javaScanBody(w *cstWalk, n *gts.Node, ownerBare string) {
	if n == nil {
		return
	}
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		if c.Type(w.lang) == "method_invocation" {
			javaHandleCall(w, c, ownerBare)
		}
		javaScanBody(w, c, ownerBare)
	}
}

func javaHandleCall(w *cstWalk, call *gts.Node, ownerBare string) {
	// method_invocation has an optional `object` field; without it the call is a bare
	// in-class method invocation (name field), with it a selector (obj.name()).
	obj := call.ChildByFieldName("object", w.lang)
	name := call.ChildByFieldName("name", w.lang)
	if name == nil {
		return
	}
	if obj == nil {
		w.callBare(ownerBare, name.Text(w.src), nodePoint(name))
		return
	}
	w.callSelector(ownerBare, obj.Text(w.src), name.Text(w.src), nodePoint(name))
}

// javaImports records import_declaration scoped identifiers as ImportSpecs. The
// trailing identifier is the bound simple name; the full scoped path is the import
// path.
func javaImports(t *javaAST) []ImportSpec {
	if t == nil || t.root == nil {
		return nil
	}
	var out []ImportSpec
	root := t.root
	for i := 0; i < root.ChildCount(); i++ {
		c := root.Child(i)
		if c == nil || c.Type(t.lang) != "import_declaration" {
			continue
		}
		scoped := childByType(c, "scoped_identifier", t.lang)
		if scoped == nil {
			continue
		}
		path := scoped.Text(t.src)
		alias := path
		// Trailing identifier under the outermost scoped_identifier is the bound name.
		for j := scoped.ChildCount() - 1; j >= 0; j-- {
			d := scoped.Child(j)
			if d != nil && d.Type(t.lang) == "identifier" {
				alias = d.Text(t.src)
				break
			}
		}
		// On-demand import (`import com.a.b.*;`): the `.*` is a sibling token of the
		// scoped_identifier, so the scoped path IS the package. Mark it so the linker
		// links the package directly instead of stripping a (non-existent) type tail.
		wildcard := importDeclIsWildcard(c, t)
		out = append(out, ImportSpec{Alias: alias, Path: path, Wildcard: wildcard})
	}
	return out
}

// importDeclIsWildcard reports whether an import_declaration is an on-demand
// import (`import com.a.b.*;`), detected by an asterisk token among its children.
func importDeclIsWildcard(decl *gts.Node, t *javaAST) bool {
	for j := 0; j < decl.ChildCount(); j++ {
		if d := decl.Child(j); d != nil && d.Text(t.src) == "*" {
			return true
		}
	}
	return false
}
