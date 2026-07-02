package parse

import (
	"context"
	"fmt"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/core/model"
)

// JavaScriptParser is the SW-054 curated tier-1 JavaScript parser. It clones the
// SW-053 TypeScript recipe (parser_ts.go) — the JS node grammar is near-identical to
// TypeScript, so the walk is a mechanical mirror with the TS-only type-annotation
// path removed. It produces its CST through the PURE-GO, CGo-free gotreesitter
// runtime (github.com/odvcencio/gotreesitter), so the default tier stays green under
// CGO_ENABLED=0 and passes internal/cgoconformance. The grammar blob is Go-embedded
// at build time (subset tag grammar_subset_javascript); nothing is fetched at parse
// time. JavaScriptParser carries no mutable state and is safe for concurrent use.
type JavaScriptParser struct {
	lang      *gts.Language
	extractor SymbolExtractor
}

// NewJavaScriptParser returns a ready JavaScriptParser wired to the pure-Go
// JavaScript grammar and its SymbolExtractor.
func NewJavaScriptParser() *JavaScriptParser {
	lang := grammars.JavascriptLanguage()
	return &JavaScriptParser{lang: lang, extractor: &jsSymbolExtractor{lang: lang}}
}

// Language implements Parser.
func (*JavaScriptParser) Language() string { return "javascript" }

// Runtime implements Parser: pure-Go gotreesitter tree-sitter runtime (CGo-free).
func (*JavaScriptParser) Runtime() Runtime { return RuntimeGoTreeSitter }

// Extensions implements Parser.
func (*JavaScriptParser) Extensions() []string { return []string{".js"} }

// jsAST is the typed payload placed in ParseResult.Root for JavaScript sources.
type jsAST struct {
	root *gts.Node
	src  []byte
	lang *gts.Language
}

// Parse implements Parser. It parses src as JavaScript and returns a normalized
// ParseResult whose Root is a *jsAST, honoring ctx cancellation and recovering from
// any runtime panic so a single malformed file can never crash the caller.
func (p *JavaScriptParser) Parse(ctx context.Context, filename string, src []byte) (res *ParseResult, err error) {
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
		return nil, fmt.Errorf("parse: javascript error in %q: %w", filename, perr)
	}
	root := &jsAST{root: tree.RootNode(), src: src, lang: p.lang}

	extractor := p.extractor
	if extractor == nil {
		extractor = &jsSymbolExtractor{lang: p.lang}
	}
	nodes, edges, pending, xerr := extractor.Extract(filename, root)
	if xerr != nil {
		return nil, fmt.Errorf("parse: javascript extraction in %q: %w", filename, xerr)
	}

	imports := jsImports(root)
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

// Kind mapping (JavaScript collapses onto {file, function, method, type, variable,
// constant}):
//
//	function ← function_declaration
//	method   ← method_definition (class methods)
//	type     ← class_declaration (collapsed to type)
//	variable ← let/var bindings (non-callable)
//	constant ← const bindings (non-callable)
//
// Absent by design (JS lacks them at this tier): interfaces, enums, type aliases,
// namespaces, decorators.

// jsSymbolExtractor is the JavaScript SymbolExtractor. It carries no mutable state.
type jsSymbolExtractor struct{ lang *gts.Language }

// Language implements SymbolExtractor.
func (*jsSymbolExtractor) Language() string { return "javascript" }

// Extract implements SymbolExtractor for JavaScript.
func (e *jsSymbolExtractor) Extract(filename string, root any) ([]model.Node, []model.Edge, []PendingRef, error) {
	t, ok := root.(*jsAST)
	if !ok || t == nil || t.root == nil {
		return nil, nil, nil, fmt.Errorf("parse: javascript extractor: expected non-nil *jsAST root for %q, got %T", filename, root)
	}
	w := newCSTWalk(t.lang, t.src, langPackage(filename))
	// SW-055 AC#6: fail-closed parse-depth guard on untrusted input (skips the
	// file with structured, source-free provenance if nesting exceeds the bound).
	if derr := w.guardDepth(t.root, filename, "javascript"); derr != nil {
		return nil, nil, nil, derr
	}
	jsCollectDefs(w, t.root, false)
	jsResolveUses(w, t.root, false)
	return w.finishExtract(filename, "javascript")
}

func jsCollectDefs(w *cstWalk, n *gts.Node, inClass bool) {
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
				jsCollectDefs(w, body, true)
			}
		case "method_definition":
			if inClass {
				if name := c.ChildByFieldName("name", w.lang); name != nil {
					w.addDef(name.Text(w.src), KindMethod, nodePoint(name))
				}
			}
		case "lexical_declaration":
			kind := KindVariable
			if jsLexicalIsConst(w, c) {
				kind = KindConstant
			}
			jsCollectDeclarators(w, c, kind)
		case "variable_declaration":
			jsCollectDeclarators(w, c, KindVariable)
		case "export_statement":
			jsCollectDefs(w, c, inClass)
		}
	}
}

func jsLexicalIsConst(w *cstWalk, decl *gts.Node) bool {
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

func jsCollectDeclarators(w *cstWalk, decl *gts.Node, kind string) {
	for i := 0; i < decl.ChildCount(); i++ {
		c := decl.Child(i)
		if c == nil || c.Type(w.lang) != "variable_declarator" {
			continue
		}
		name := c.ChildByFieldName("name", w.lang)
		if name == nil || name.Type(w.lang) != "identifier" {
			continue // destructuring patterns out of scope this slice
		}
		w.addDef(name.Text(w.src), kind, nodePoint(name))
	}
}

func jsResolveUses(w *cstWalk, n *gts.Node, inClass bool) {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "function_declaration":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				jsScanBody(w, c, name.Text(w.src))
			}
		case "method_definition":
			if inClass {
				if name := c.ChildByFieldName("name", w.lang); name != nil {
					jsScanBody(w, c, name.Text(w.src))
				}
			}
		case "class_declaration":
			if body := c.ChildByFieldName("body", w.lang); body != nil {
				jsResolveUses(w, body, true)
			}
		case "export_statement":
			jsResolveUses(w, c, inClass)
		}
	}
}

func jsScanBody(w *cstWalk, n *gts.Node, ownerBare string) {
	if n == nil {
		return
	}
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		if c.Type(w.lang) == "call_expression" {
			jsHandleCall(w, c, ownerBare)
		}
		jsScanBody(w, c, ownerBare)
	}
}

func jsHandleCall(w *cstWalk, call *gts.Node, ownerBare string) {
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

// jsImports extracts import declarations as ImportSpecs, mirroring tsImports.
func jsImports(t *jsAST) []ImportSpec {
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
		path := jsImportPath(c, t.lang, t.src)
		if path == "" {
			continue
		}
		clause := childByType(c, "import_clause", t.lang)
		if clause == nil {
			out = append(out, ImportSpec{Path: path})
			continue
		}
		// A clause may combine a default import with a namespace or named list
		// (e.g. `import Logger, {warn} from "./log"`), so capture each form
		// additively rather than with early continues.
		hasAlias := false
		// Default import: the clause's direct identifier child is the binding.
		if id := childByType(clause, "identifier", t.lang); id != nil {
			out = append(out, ImportSpec{Alias: id.Text(t.src), Path: path})
			hasAlias = true
		}
		if ns := childByType(clause, "namespace_import", t.lang); ns != nil {
			if id := childByType(ns, "identifier", t.lang); id != nil {
				out = append(out, ImportSpec{Alias: id.Text(t.src), Path: path})
				hasAlias = true
			}
		}
		if named := childByType(clause, "named_imports", t.lang); named != nil {
			for j := 0; j < named.ChildCount(); j++ {
				spec := named.Child(j)
				if spec == nil || spec.Type(t.lang) != "import_specifier" {
					continue
				}
				if name := spec.ChildByFieldName("name", t.lang); name != nil {
					out = append(out, ImportSpec{Alias: name.Text(t.src), Path: path})
					hasAlias = true
				}
			}
		}
		if !hasAlias {
			out = append(out, ImportSpec{Path: path})
		}
	}
	return out
}

func jsImportPath(imp *gts.Node, lang *gts.Language, src []byte) string {
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
