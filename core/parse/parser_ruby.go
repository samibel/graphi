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

// rubyScanBody walks ONE method body in TWO passes. Pass 1 (rubyCollectScope)
// builds the method's binding table; pass 2 (rubyScanUses) records the call
// sites. The split is required, not stylistic: pass 2 has to know whether a bare
// `identifier` is a local read before it can decide whether the identifier is a
// call site, and Ruby permits the read to appear textually before the binding it
// resolves against (a block parameter, a pattern-match binder, a rescue
// variable, a later assignment in the same body).
//
// SW-194b.5 round 2 — SOUNDNESS MODEL. Ruby's CST cannot tell a receiver-less,
// parenless call from a local read; both are a bare `identifier`. The first
// round guessed with a DENYLIST (emit unless the name was seen bound), which
// leaked: every binder shape the enumeration missed — pattern matching,
// destructuring, implicit block parameters — minted a `calls` edge for a call
// that does not exist. An invented edge is strictly worse than a missing one, so
// the emitter now stands on TWO independent gates and an identifier must clear
// BOTH:
//
//	GATE 1 (allowlist, fails safe) — the identifier must sit in a POSITIVELY
//	recognised call position (rubyIsCallPosition). Anything else — a parameter
//	list, an assignment target, any part of a pattern, a grammar node this
//	parser has never heard of — emits nothing. A construct nobody enumerated is
//	therefore silent by default, which is the direction this repo requires.
//
//	GATE 2 (binding table, semantic) — the name must not be bound as data
//	anywhere in the method. This gate cannot be dropped: Ruby leaks pattern and
//	destructuring binders into the enclosing method scope, so the READ of such a
//	name (`status` after `in {status: status}`) genuinely sits in a call
//	position and only the binding table can tell it apart. Gate 2 is therefore
//	built REGION-WISE, not shape-wise: every identifier under an `in`/`=>`/`in`-
//	test pattern binds, whatever the pattern's shape (rubyBindPattern), and
//	every identifier under an assignment target binds however deeply nested
//	(rubyBindTarget). New pattern node types are covered without being named.
//
// KNOWN LIMITS, all of them false NEGATIVES (a missing edge, never an invented
// one), accepted deliberately:
//   - `it` and `_1`…`_9` are never call sites even when a real method has that
//     name (Ruby 3.4 implicit block parameters; RSpec's `it` carries a block and
//     is parsed as a `call`, so it is unaffected).
//   - a pin (`in ^x`) and the shorthand key of `{name:}` bind the name for the
//     whole method, so a later same-named call is suppressed.
//   - a `def`, `class` or `module` nested inside a method body is not walked at
//     all: `rubyCollectDefs` never defines nested symbols, so attributing their
//     bodies to the OUTER method (the round-1 behaviour) reported calls the
//     outer method does not make.
//   - call positions this parser has not enumerated stay silent.
func rubyScanBody(w *cstWalk, n *gts.Node, ownerBare string) {
	if n == nil {
		return
	}
	sc := newRubyScope()
	// The declaration's own name is not a use of it.
	sc.consume(n.ChildByFieldName("name", w.lang))
	for i := 0; i < n.ChildCount(); i++ {
		rubyCollectScope(w, n.Child(i), sc)
	}
	rubyScanUses(w, n, ownerBare, sc)
}

// rubyScope is the per-method binding table pass 2 consults (GATE 2).
//
// `bound` holds NAMES that pass 1 saw bound as data inside this method —
// parameters, assignment and destructuring targets, pattern-match binders, block
// parameters, loop and rescue variables. Ruby scopes all of these to the
// enclosing method, so a read of such a name anywhere in the body is a variable
// read, never a call.
//
// `consumed` holds the POSITIONS of identifier nodes pass 1 already accounted
// for structurally: a `call` node's method and receiver identifiers (handled by
// rubyHandleCall) and the method's own name. Position keying rather than name
// keying is what lets `helper` stay a call site in one place while a *different*
// identifier node is skipped in another.
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

// bindName suppresses a NAME for the rest of the method. Used where the binder
// has no identifier node of its own — Ruby's `in {name:}` shorthand.
func (s *rubyScope) bindName(name string) {
	if name != "" {
		s.bound[name] = struct{}{}
	}
}

// bind records an identifier node as a data binding: the occurrence is consumed
// and the NAME is suppressed for the rest of the method.
func (s *rubyScope) bind(n *gts.Node, w *cstWalk) {
	if n == nil {
		return
	}
	s.consume(n)
	s.bindName(n.Text(w.src))
}

// skip reports whether pass 2 must leave this identifier alone.
func (s *rubyScope) skip(n *gts.Node, w *cstWalk) bool {
	if _, done := s.consumed[nodePoint(n)]; done {
		return true
	}
	name := n.Text(w.src)
	if rubyImplicitBlockParam(name) {
		return true
	}
	_, isBound := s.bound[name]
	return isBound
}

// rubyImplicitBlockParam reports Ruby's implicit block parameters — `_1`…`_9`
// (Ruby 2.7+) and `it` (Ruby 3.4+). They are reads of an argument the block
// never names, and no binder node exists for them anywhere in the CST, so they
// are suppressed by name. A real method called `it` loses its edge; that is the
// false-negative side of the trade and is taken deliberately.
func rubyImplicitBlockParam(name string) bool {
	if name == "it" {
		return true
	}
	return len(name) == 2 && name[0] == '_' && name[1] >= '1' && name[1] <= '9'
}

// rubyCollectScope is pass 1: it walks the method subtree and records every
// binding occurrence and every identifier a call node already owns.
func rubyCollectScope(w *cstWalk, n *gts.Node, sc *rubyScope) {
	if n == nil {
		return
	}
	switch n.Type(w.lang) {
	case "method", "singleton_method", "class", "module":
		// A nested definition is a separate scope and a separate owner; pass 2
		// does not walk it either. Stopping here keeps its bindings out of this
		// method's table.
		return
	case "method_parameters", "block_parameters", "lambda_parameters":
		rubyBindParams(w, n, sc)
		return
	case "exception_variable":
		// `rescue => err`
		rubyBindTarget(w, n, sc)
		return
	case "for":
		// `for v in xs` / `for (i, j) in xs` — only the loop variable binds; the
		// `in <value>` subtree is an ordinary expression and is left to pass 2.
		rubyBindTarget(w, n.ChildByFieldName("pattern", w.lang), sc)
	case "assignment", "operator_assignment":
		rubyBindTarget(w, n.ChildByFieldName("left", w.lang), sc)
	case "in_clause", "match_pattern", "test_pattern":
		// `case … in <pat>`, `expr => <pat>`, `expr in <pat>`. Only the pattern
		// binds; the scrutinee and an `if`/`unless` guard are ordinary
		// expressions.
		rubyBindPattern(w, n.ChildByFieldName("pattern", w.lang), sc)
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

// rubyBindParams binds the NAMES declared by a parameter list. A direct
// identifier child is itself a name; a wrapper node (`b = 1`, `*c`, `k: 2`,
// `&blk`, `(a, b)`) binds its own name field and recurses. A default-value
// expression (`def f(b = helper)`) is deliberately left unbound so the call
// inside the default is still seen — it is a genuine call position.
func rubyBindParams(w *cstWalk, list *gts.Node, sc *rubyScope) {
	for i := 0; i < list.ChildCount(); i++ {
		c := list.Child(i)
		if c == nil {
			continue
		}
		switch typ := c.Type(w.lang); {
		case typ == "identifier":
			sc.bind(c, w)
		case typ == "destructured_parameter" || typ == "left_assignment_list":
			rubyBindParams(w, c, sc)
		case strings.HasSuffix(typ, "_parameter"):
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				rubyBindTarget(w, name, sc)
				continue
			}
			sc.bind(childByType(c, "identifier", w.lang), w)
		}
	}
}

// rubyBindTarget binds every local named by an assignment target, a `for` loop
// variable or a rescue variable, however deeply the target nests:
// `a = …`, `a, b = …`, `first, *rest = …`, `(a, b), c = …`.
//
// It is written as a REGION walk rather than a list of target shapes so a
// wrapper node this parser has never seen (a new splat or destructuring spelling)
// still binds the identifiers underneath it instead of leaking them to pass 2.
// The two shapes that are READS rather than bindings — `obj.attr = v` (a `call`)
// and `a[i] = v` (an `element_reference`) — terminate the walk so their receiver
// and index stay visible to pass 2.
func rubyBindTarget(w *cstWalk, n *gts.Node, sc *rubyScope) {
	if n == nil {
		return
	}
	switch n.Type(w.lang) {
	case "identifier":
		sc.bind(n, w)
		return
	case "call", "element_reference":
		return
	}
	for i := 0; i < n.ChildCount(); i++ {
		rubyBindTarget(w, n.Child(i), sc)
	}
}

// rubyBindPattern binds every local a pattern introduces. Ruby's pattern
// matching leaks its binders into the enclosing method scope, so
// `case x; in {status: status}; status; end` defines a local `status` and the
// later read is NOT a call — round 1 emitted exactly that phantom.
//
// Like rubyBindTarget this is a REGION walk: EVERY identifier under the pattern
// binds, whatever the pattern node is called. That covers array, hash, find,
// alternative and `=>` binder patterns, the bare `in x` form, splats and nesting
// without enumerating any of them, and covers pattern shapes added to the
// grammar later. The single binder with no identifier node of its own — the
// `{name:}` shorthand, which binds a local named after the key — is bound by
// name.
func rubyBindPattern(w *cstWalk, n *gts.Node, sc *rubyScope) {
	if n == nil {
		return
	}
	switch n.Type(w.lang) {
	case "identifier":
		sc.bind(n, w)
	case "keyword_pattern":
		if n.ChildByFieldName("value", w.lang) == nil {
			if key := n.ChildByFieldName("key", w.lang); key != nil {
				sc.bindName(key.Text(w.src))
			}
		}
	}
	for i := 0; i < n.ChildCount(); i++ {
		rubyBindPattern(w, n.Child(i), sc)
	}
}

// rubyCallPositionAnyChild lists the parent node types EVERY named child of
// which Ruby evaluates as an expression or a statement. An identifier sitting
// directly under one of these is in a call position.
var rubyCallPositionAnyChild = map[string]struct{}{
	// statement holders
	"program": {}, "body_statement": {}, "block_body": {}, "then": {}, "else": {},
	"do": {}, "begin": {}, "begin_block": {}, "end_block": {}, "ensure": {},
	"parenthesized_statements": {},
	// argument and literal element positions
	"argument_list": {}, "array": {}, "splat_argument": {}, "hash_splat_argument": {},
	"block_argument": {}, "right_assignment_list": {}, "interpolation": {},
	// operator and modifier positions
	"binary": {}, "unary": {}, "range": {}, "conditional": {},
	"if_modifier": {}, "unless_modifier": {}, "while_modifier": {},
	"until_modifier": {}, "rescue_modifier": {},
	// `case … when <expr>` scrutinee list, `for v in <expr>`, `a[<expr>]`
	"pattern": {}, "in": {}, "element_reference": {},
}

// rubyCallPositionByField lists the parent node types where only SOME fields are
// evaluated expressions — the other fields being binders or patterns.
var rubyCallPositionByField = map[string]map[string]struct{}{
	"assignment":          {"right": {}},
	"operator_assignment": {"right": {}},
	"pair":                {"value": {}},
	"if":                  {"condition": {}},
	"unless":              {"condition": {}},
	"while":               {"condition": {}},
	"until":               {"condition": {}},
	"case":                {"value": {}},
	"case_match":          {"value": {}},
	"match_pattern":       {"value": {}},
	"test_pattern":        {"value": {}},
	"optional_parameter":  {"value": {}},
	"keyword_parameter":   {"value": {}},
	// `def f(x) = <expr>` — an endless method has no body_statement.
	"method":           {"body": {}},
	"singleton_method": {"body": {}},
	// `in <pat> if <expr>` — the guard is an ordinary expression, only the
	// pattern binds.
	"if_guard":     {"condition": {}},
	"unless_guard": {"condition": {}},
}

// rubyIsCallPosition is GATE 1: it reports whether an identifier occupying
// `field` of a `parentType` node is in a position where Ruby evaluates it — the
// only place a receiver-less, parenless call can appear. Unknown parents and
// unknown fields answer false, so an unenumerated construct costs an edge
// instead of inventing one.
func rubyIsCallPosition(parentType, field string) bool {
	if _, ok := rubyCallPositionAnyChild[parentType]; ok {
		return true
	}
	fields, ok := rubyCallPositionByField[parentType]
	if !ok {
		return false
	}
	_, ok = fields[field]
	return ok
}

// rubyScanUses is pass 2: it records the method's call sites.
//
// A `call` node covers only the spellings that carry a receiver or an argument
// list. Ruby's dominant spelling — receiver-less AND parenless, e.g. `helper`
// inside `def checkout` — is a bare `identifier` node, grammatically
// indistinguishable from a local-variable read. Before SW-194b.5 those call
// sites produced no PendingRef at all, so the linker had nothing to resolve
// through the require's ambient directory and the graph could not answer "who
// calls helper?" across files at the heuristic tier.
//
// An identifier is recorded only when it clears BOTH gates described on
// rubyScanBody: it sits in a recognised call position AND the scope table does
// not hold it. The result is the same inert PendingRef the parenthesised
// spelling produces, resolved (or dropped and counted) by the linker, never
// fabricated here.
func rubyScanUses(w *cstWalk, n *gts.Node, ownerBare string, sc *rubyScope) {
	parentType := n.Type(w.lang)
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "method", "singleton_method", "class", "module":
			// Separate scope, separate owner, and no owner exists for it —
			// see the KNOWN LIMITS note on rubyScanBody.
			continue
		case "call":
			rubyHandleCall(w, c, ownerBare)
		case "identifier":
			if rubyIsCallPosition(parentType, n.FieldNameForChild(i, w.lang)) && !sc.skip(c, w) {
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
