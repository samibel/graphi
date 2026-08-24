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

// ExtractsSymbols implements SymbolCapable: this parser wires a SymbolExtractor
// and emits symbol nodes plus intra-file edges.
func (*RubyParser) ExtractsSymbols() bool { return true }

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

// rubyScanBody walks one method body in TWO passes. Pass 1 (rubyCollectScope)
// builds the method's binding table; pass 2 (rubyScanUses) records the call
// sites. The split is required, not stylistic: pass 2 has to know whether a bare
// `identifier` is a local read before it can decide whether the identifier is a
// call site, and Ruby permits the read to appear textually before the binding it
// resolves against (a block parameter, a rescue variable, a later assignment in
// the same body).
func rubyScanBody(w *cstWalk, n *gts.Node, ownerBare string) {
	if n == nil {
		return
	}
	sc := newRubyScope()
	rubyCollectScope(w, n, sc)
	rubyScanUses(w, n, ownerBare, sc)
}

// rubyScope is the per-method binding table pass 2 consults.
//
// `bound` holds NAMES that pass 1 saw bound as data inside this method —
// parameters, assignment targets, block parameters, loop and rescue variables.
// A read of such a name is a variable read, never a call.
//
// `consumed` holds the POSITIONS of identifier nodes pass 1 already accounted
// for structurally: a `call` node's method and receiver identifiers (handled by
// rubyHandleCall), the method's own name, and every binding occurrence. Position
// keying rather than name keying is what lets `helper` stay a call site in one
// place while a *different* identifier node is skipped in another.
type rubyScope struct {
	bound    map[string]struct{}
	consumed map[TSPoint]struct{}
}

func newRubyScope() *rubyScope {
	return &rubyScope{bound: map[string]struct{}{}, consumed: map[TSPoint]struct{}{}}
}

// consume marks one identifier node as structurally accounted for.
func (s *rubyScope) consume(n *gts.Node) {
	if n != nil {
		s.consumed[nodePoint(n)] = struct{}{}
	}
}

// bind records an identifier node as a data binding: the occurrence is consumed
// and the NAME is suppressed for the rest of the method.
func (s *rubyScope) bind(n *gts.Node, w *cstWalk) {
	if n == nil {
		return
	}
	s.consume(n)
	if name := n.Text(w.src); name != "" {
		s.bound[name] = struct{}{}
	}
}

// skip reports whether pass 2 must leave this identifier alone.
func (s *rubyScope) skip(n *gts.Node, w *cstWalk) bool {
	if _, done := s.consumed[nodePoint(n)]; done {
		return true
	}
	_, isBound := s.bound[n.Text(w.src)]
	return isBound
}

// rubyCollectScope is pass 1: it walks the method subtree and records every
// binding occurrence and every identifier a call node already owns.
func rubyCollectScope(w *cstWalk, n *gts.Node, sc *rubyScope) {
	if n == nil {
		return
	}
	switch n.Type(w.lang) {
	case "method", "singleton_method":
		// The declaration's own name is not a use of it.
		sc.consume(n.ChildByFieldName("name", w.lang))
	case "method_parameters", "block_parameters", "lambda_parameters",
		"exception_variable", "for":
		rubyBindNames(w, n, sc)
	case "assignment", "operator_assignment":
		// Only the two target shapes that name LOCALS bind: `x = …` and
		// `x, y = …`. An `obj.attr = …` target is a `call` node and is left to
		// the "call" case below; an `a[0] = …` target reads `a` and must not
		// suppress it.
		if left := n.ChildByFieldName("left", w.lang); left != nil {
			switch left.Type(w.lang) {
			case "identifier":
				sc.bind(left, w)
			case "left_assignment_list":
				rubyBindNames(w, left, sc)
			}
		}
	case "call":
		// rubyHandleCall already turns these two identifiers into a bare or
		// selector PendingRef; pass 2 must not record them a second time under
		// the wrong shape (a selector's method name is NOT a bare call site).
		sc.consume(n.ChildByFieldName("method", w.lang))
		if recv := n.ChildByFieldName("receiver", w.lang); recv != nil && recv.Type(w.lang) == "identifier" {
			sc.consume(recv)
		}
	}
	for i := 0; i < n.ChildCount(); i++ {
		rubyCollectScope(w, n.Child(i), sc)
	}
}

// rubyBindNames binds the NAMES declared by a parameter list, a multiple-
// assignment target list, a `for` loop head or a rescue clause. A direct
// identifier child is itself a name; a wrapper node (`b = 1`, `*c`, `k: 2`,
// `&blk`) contributes its FIRST identifier child. Deeper identifiers — a
// default-value expression such as `def f(b = helper)` — are deliberately left
// unbound so the call inside the default is still seen.
func rubyBindNames(w *cstWalk, list *gts.Node, sc *rubyScope) {
	for i := 0; i < list.ChildCount(); i++ {
		c := list.Child(i)
		if c == nil {
			continue
		}
		switch typ := c.Type(w.lang); {
		case typ == "identifier":
			sc.bind(c, w)
		case typ == "destructured_parameter" || typ == "left_assignment_list":
			rubyBindNames(w, c, sc)
		case strings.HasSuffix(typ, "_parameter"):
			sc.bind(childByType(c, "identifier", w.lang), w)
		}
	}
}

// rubyScanUses is pass 2: it records the method's call sites.
//
// SW-194b.5: a `call` node covers only the spellings that carry a receiver or an
// argument list. Ruby's dominant spelling — receiver-less AND parenless, e.g.
// `helper` inside `def checkout` — is a bare `identifier` node, grammatically
// indistinguishable from a local-variable read. Before this pass those call
// sites produced no PendingRef at all, so the linker had nothing to resolve
// through the require's ambient directory and the graph could not answer "who
// calls helper?" across files at the heuristic tier. Every identifier the scope
// table did not claim as a binding or as a call node's own is therefore recorded
// as a bare call site — the same inert PendingRef the parenthesised spelling
// produces, resolved (or dropped and counted) by the linker, never fabricated
// here.
func rubyScanUses(w *cstWalk, n *gts.Node, ownerBare string, sc *rubyScope) {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "call":
			rubyHandleCall(w, c, ownerBare)
		case "identifier":
			if !sc.skip(c, w) {
				w.callBare(ownerBare, c.Text(w.src), nodePoint(c))
			}
		}
		rubyScanUses(w, c, ownerBare, sc)
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
