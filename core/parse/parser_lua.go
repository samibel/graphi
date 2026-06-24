package parse

import (
	"context"
	"fmt"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/core/model"
)

// LuaParser is the SW-054 curated tier-1 Lua parser. It clones the SW-053 recipe over
// the pure-Go gotreesitter Lua grammar (CGo-free; default tier green under
// CGO_ENABLED=0; grammar blob Go-embedded behind subset tag grammar_subset_lua). Lua
// models objects as tables, so the node set collapses to {file, function, variable}.
// LuaParser carries no mutable state and is safe for concurrent use.
type LuaParser struct {
	lang      *gts.Language
	extractor SymbolExtractor
}

// NewLuaParser returns a ready LuaParser wired to the pure-Go Lua grammar.
func NewLuaParser() *LuaParser {
	lang := grammars.LuaLanguage()
	return &LuaParser{lang: lang, extractor: &luaSymbolExtractor{lang: lang}}
}

// Language implements Parser.
func (*LuaParser) Language() string { return "lua" }

// Runtime implements Parser: pure-Go gotreesitter tree-sitter runtime (CGo-free).
func (*LuaParser) Runtime() Runtime { return RuntimeGoTreeSitter }

// Extensions implements Parser.
func (*LuaParser) Extensions() []string { return []string{".lua"} }

type luaAST struct {
	root *gts.Node
	src  []byte
	lang *gts.Language
}

// Parse implements Parser.
func (p *LuaParser) Parse(ctx context.Context, filename string, src []byte) (res *ParseResult, err error) {
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
		return nil, fmt.Errorf("parse: lua error in %q: %w", filename, perr)
	}
	root := &luaAST{root: tree.RootNode(), src: src, lang: p.lang}

	extractor := p.extractor
	if extractor == nil {
		extractor = &luaSymbolExtractor{lang: p.lang}
	}
	nodes, edges, pending, xerr := extractor.Extract(filename, root)
	if xerr != nil {
		return nil, fmt.Errorf("parse: lua extraction in %q: %w", filename, xerr)
	}

	imports := luaImports(root)
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

// Kind mapping (Lua collapses onto {file, function, variable}):
//
//	function ← function_declaration (name -> identifier; dotted names skipped this slice)
//	variable ← local variable_declaration assignment target (identifier)
//
// Absent by design: method, type, constant (Lua models objects as tables; no language
// const). `require("m")` calls are recorded as ImportSpecs.

type luaSymbolExtractor struct{ lang *gts.Language }

// Language implements SymbolExtractor.
func (*luaSymbolExtractor) Language() string { return "lua" }

// Extract implements SymbolExtractor for Lua.
func (e *luaSymbolExtractor) Extract(filename string, root any) ([]model.Node, []model.Edge, []PendingRef, error) {
	t, ok := root.(*luaAST)
	if !ok || t == nil || t.root == nil {
		return nil, nil, nil, fmt.Errorf("parse: lua extractor: expected non-nil *luaAST root for %q, got %T", filename, root)
	}
	w := newCSTWalk(t.lang, t.src, langPackage(filename))
	// SW-055 AC#6: fail-closed parse-depth guard on untrusted input (skips the
	// file with structured, source-free provenance if nesting exceeds the bound).
	if derr := w.guardDepth(t.root, filename, "lua"); derr != nil {
		return nil, nil, nil, derr
	}
	luaCollectDefs(w, t.root)
	luaResolveUses(w, t.root)
	return w.finishExtract(filename, "lua")
}

func luaCollectDefs(w *cstWalk, chunk *gts.Node) {
	for i := 0; i < chunk.ChildCount(); i++ {
		c := chunk.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "function_declaration":
			if name := c.ChildByFieldName("name", w.lang); name != nil && name.Type(w.lang) == "identifier" {
				w.addDef(name.Text(w.src), KindFunction, nodePoint(name))
			}
		case "variable_declaration":
			luaCollectVarTargets(w, c)
		}
	}
}

func luaCollectVarTargets(w *cstWalk, decl *gts.Node) {
	as := childByType(decl, "assignment_statement", w.lang)
	if as == nil {
		return
	}
	list := childByType(as, "variable_list", w.lang)
	if list == nil {
		return
	}
	for i := 0; i < list.ChildCount(); i++ {
		id := list.Child(i)
		if id != nil && id.Type(w.lang) == "identifier" {
			w.addDef(id.Text(w.src), KindVariable, nodePoint(id))
		}
	}
}

func luaResolveUses(w *cstWalk, chunk *gts.Node) {
	for i := 0; i < chunk.ChildCount(); i++ {
		c := chunk.Child(i)
		if c == nil || c.Type(w.lang) != "function_declaration" {
			continue
		}
		name := c.ChildByFieldName("name", w.lang)
		if name == nil || name.Type(w.lang) != "identifier" {
			continue
		}
		luaScanBody(w, c, name.Text(w.src))
	}
}

func luaScanBody(w *cstWalk, n *gts.Node, ownerBare string) {
	if n == nil {
		return
	}
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		if c.Type(w.lang) == "function_call" {
			luaHandleCall(w, c, ownerBare)
		}
		luaScanBody(w, c, ownerBare)
	}
}

func luaHandleCall(w *cstWalk, call *gts.Node, ownerBare string) {
	if call.ChildCount() == 0 {
		return
	}
	callee := call.Child(0)
	if callee == nil {
		return
	}
	switch callee.Type(w.lang) {
	case "identifier":
		w.callBare(ownerBare, callee.Text(w.src), nodePoint(callee))
	case "dot_index_expression":
		// obj.method() : <table> . <field>
		base := callee.Child(0)
		field := childLastIdentifier(callee, w.lang)
		if base != nil && field != nil {
			w.callSelector(ownerBare, base.Text(w.src), field.Text(w.src), nodePoint(field))
		}
	}
}

func childLastIdentifier(n *gts.Node, lang *gts.Language) *gts.Node {
	var last *gts.Node
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c != nil && c.Type(lang) == "identifier" {
			last = c
		}
	}
	return last
}

// luaImports records `require("m")` calls as ImportSpecs.
func luaImports(t *luaAST) []ImportSpec {
	if t == nil || t.root == nil {
		return nil
	}
	var out []ImportSpec
	var walk func(n *gts.Node)
	walk = func(n *gts.Node) {
		if n == nil {
			return
		}
		if n.Type(t.lang) == "function_call" && n.ChildCount() > 0 {
			callee := n.Child(0)
			if callee != nil && callee.Type(t.lang) == "identifier" && callee.Text(t.src) == "require" {
				if args := childByType(n, "arguments", t.lang); args != nil {
					if s := childByType(args, "string", t.lang); s != nil {
						if path := luaStringContent(s, t.lang, t.src); path != "" {
							out = append(out, ImportSpec{Path: path})
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

func luaStringContent(s *gts.Node, lang *gts.Language, src []byte) string {
	if frag := childByType(s, "string_content", lang); frag != nil {
		return frag.Text(src)
	}
	return strings.Trim(s.Text(src), "\"'")
}
