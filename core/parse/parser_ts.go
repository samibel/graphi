package parse

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/core/model"
)

// TSParser is the first curated tier-1 tree-sitter language (EP-009, SW-053):
// TypeScript. It mirrors GoParser (Language()/Extensions()/ctx-check + panic-
// recover/returns a normalized ParseResult) but produces its CST through a
// PURE-GO, CGo-free tree-sitter runtime (github.com/odvcencio/gotreesitter) so the
// default tier stays green under CGO_ENABLED=0 and passes the internal/cgoconformance
// import-graph scan.
//
// "Pure-Go" here is literal: the gotreesitter runtime re-implements the tree-sitter
// parser/lexer/query engine in Go (no `import "C"`, no parser.c), and the TypeScript
// parse table ships as a Go-embedded blob. Nothing is fetched at parse time; the
// grammar is module-pinned and embedded at build time, preserving the zero-outbound-
// network-at-runtime invariant.
//
// Symbol extraction runs through the language-neutral SymbolExtractor seam (SW-052):
// TSParser produces the *tsAST handle, then delegates graph derivation to
// tsSymbolExtractor, which walks the CST and feeds resolved specs through the
// MapTreeSitter helper. The two concerns — parsing (text→CST) and extraction
// (CST→graph) — are separated exactly as for Go.
//
// TSParser carries no mutable state (the grammar Language and the extractor are set
// once at construction) and is safe for concurrent use.
type TSParser struct {
	lang      *gts.Language
	extractor SymbolExtractor
}

// NewTSParser returns a ready TSParser wired to the pure-Go TypeScript grammar and
// the TypeScript SymbolExtractor. It is stateless after construction and safe for
// concurrent use.
func NewTSParser() *TSParser {
	lang := grammars.TypescriptLanguage()
	return &TSParser{lang: lang, extractor: &tsSymbolExtractor{lang: lang}}
}

// Language implements Parser.
func (*TSParser) Language() string { return "typescript" }

// Runtime implements Parser: pure-Go gotreesitter tree-sitter runtime (CGo-free).
func (*TSParser) Runtime() Runtime { return RuntimeGoTreeSitter }

// Extensions implements Parser.
func (*TSParser) Extensions() []string { return []string{".ts"} }

// tsAST is the typed payload placed in ParseResult.Root for TypeScript sources. It
// carries the parsed CST root, the original source bytes (needed to slice capture
// text — all source slicing stays inside Extract so the mapping helper remains a
// pure leaf), and the grammar Language handle (needed for Node.Type).
type tsAST struct {
	root *gts.Node
	src  []byte
	lang *gts.Language
}

// Parse implements Parser. It parses src as TypeScript and returns a normalized
// ParseResult whose Root is a *tsAST. It honors ctx cancellation and recovers from
// any unexpected panic in the runtime so a single malformed file can never crash the
// caller (two-layer guard: this recover plus the engine-side timeout/size guard).
func (p *TSParser) Parse(ctx context.Context, filename string, src []byte) (res *ParseResult, err error) {
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
		return nil, fmt.Errorf("parse: typescript error in %q: %w", filename, perr)
	}

	root := &tsAST{root: tree.RootNode(), src: src, lang: p.lang}

	extractor := p.extractor
	if extractor == nil {
		extractor = &tsSymbolExtractor{lang: p.lang}
	}
	nodes, edges, pending, xerr := extractor.Extract(filename, root)
	if xerr != nil {
		return nil, fmt.Errorf("parse: typescript extraction in %q: %w", filename, xerr)
	}

	// Imports are recorded as ImportSpecs (alias + path) and surfaced in References
	// for the reverse-dependency cascade, mirroring the Go path. No EdgeImports graph
	// edge is emitted this slice (the import target is a cross-file/external module).
	imports := tsImports(root)
	refs := make([]string, 0, len(imports))
	for _, imp := range imports {
		refs = append(refs, imp.Path)
	}

	return &ParseResult{
		Meta: SourceMeta{
			Path:        filename,
			Language:    p.Language(),
			ContentHash: contentHash(src),
			Size:        len(src),
		},
		Root:        root,
		Nodes:       nodes,
		Edges:       edges,
		PendingRefs: pending,
		Imports:     imports,
		References:  refs,
	}, nil
}

// Node kinds/edge kinds reuse the canonical Kind*/Edge* vocabulary (extractor.go).
// TypeScript has MORE kinds than the frozen vocabulary; the mapping below collapses
// them. See the documented table in parser_ts_test.go.
//
//	function ← function declarations and named const/let/var-bound arrow functions
//	          / function expressions (a named callable binding stays a function).
//	method   ← class methods (method_definition).
//	type     ← interface / type alias / enum / class declarations (collapsed).
//	variable ← let/var bindings (non-callable).
//	constant ← const bindings (non-callable).
//
// Absent by design (collapsed/omitted): namespaces/modules, decorators, ambient
// declarations.

// tsSymbolExtractor is the TypeScript SymbolExtractor. It walks the CST once,
// collecting top-level definitions and the uses inside each definition body, then
// feeds position-sorted node specs + an intra-file/deferred edge split through
// MapTreeSitter (SW-052). It carries no mutable state and is safe for concurrent use.
type tsSymbolExtractor struct {
	lang *gts.Language
}

// Language implements SymbolExtractor.
func (*tsSymbolExtractor) Language() string { return "typescript" }

// Extract implements SymbolExtractor for the TypeScript path. It expects root to be
// the *tsAST produced by TSParser.Parse and is a pure transform: all source slicing
// happens here (so MapTreeSitter stays a pure leaf), no I/O, and no fabricated
// endpoints — any use it cannot prove from a single file is recorded as a PendingRef.
func (e *tsSymbolExtractor) Extract(filename string, root any) ([]model.Node, []model.Edge, []PendingRef, error) {
	t, ok := root.(*tsAST)
	if !ok || t == nil || t.root == nil {
		return nil, nil, nil, fmt.Errorf("parse: typescript extractor: expected non-nil *tsAST root for %q, got %T", filename, root)
	}

	w := &tsWalk{
		lang:     t.lang,
		src:      t.src,
		pkg:      tsPackage(filename),
		defKind:  map[string]string{},
		defPos:   map[string]TSPoint{},
		defMeta:  map[string]model.NodeMeta{},
		funcs:    map[string]struct{}{},
		edgeSeen: map[string]struct{}{},
		pendSeen: map[string]struct{}{},
	}

	// SW-055 AC#6: fail-closed parse-depth guard on untrusted input before the
	// recursive collectors descend (skips the file with structured, source-free
	// provenance if nesting exceeds the bound).
	if derr := guardCSTDepth(t.root, t.lang, maxParseDepth(), filename, "typescript"); derr != nil {
		return nil, nil, nil, derr
	}

	// Pass 1: discover every top-level definition (so forward references resolve).
	w.collectDefs(t.root)

	// Build position-sorted node specs (Row, Column, QualifiedName) for determinism.
	type entry struct {
		bare string
		spec TSNodeSpec
	}
	entries := make([]entry, 0, len(w.defOrder))
	for _, bare := range w.defOrder {
		entries = append(entries, entry{
			bare: bare,
			spec: TSNodeSpec{Kind: w.defKind[bare], QualifiedName: w.pkg + "." + bare, Pos: w.defPos[bare], Meta: w.defMeta[bare]},
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i].spec, entries[j].spec
		if a.Pos.Row != b.Pos.Row {
			return a.Pos.Row < b.Pos.Row
		}
		if a.Pos.Column != b.Pos.Column {
			return a.Pos.Column < b.Pos.Column
		}
		return a.QualifiedName < b.QualifiedName
	})
	nodeSpecs := make([]TSNodeSpec, 0, len(entries))
	for _, en := range entries {
		nodeSpecs = append(nodeSpecs, en.spec)
	}

	// Pass 2: resolve uses inside each definition body into intra-file edges vs
	// deferred PendingRefs.
	w.resolveUses(t.root)

	nodes, edges, err := MapTreeSitter(filename, "typescript", nodeSpecs, w.edgeSpecs, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	return nodes, edges, w.pending, nil
}

// tsWalk accumulates the definition tables and the resolved edge/pending split for a
// single TypeScript file. It is created per Extract call and never shared, so the
// extractor stays concurrency-safe.
type tsWalk struct {
	lang *gts.Language
	src  []byte
	pkg  string

	defKind  map[string]string         // bareName -> Kind (first binding wins)
	defPos   map[string]TSPoint        // bareName -> definition position
	defMeta  map[string]model.NodeMeta // bareName -> non-identity meta (first binding wins)
	defOrder []string                  // discovery order
	funcs    map[string]struct{}       // bare names that are callable (function/method)

	edgeSpecs []TSEdgeSpec
	edgeSeen  map[string]struct{}
	pending   []PendingRef
	pendSeen  map[string]struct{}
}

func (w *tsWalk) addDef(bare, kind string, pos TSPoint) {
	if bare == "" {
		return
	}
	if _, seen := w.defKind[bare]; seen {
		return
	}
	w.defKind[bare] = kind
	w.defPos[bare] = pos
	w.defOrder = append(w.defOrder, bare)
	if kind == KindFunction || kind == KindMethod {
		w.funcs[bare] = struct{}{}
	}
}

// setDefMeta attaches NON-identity metadata (flags) to a previously-added
// definition (first binding wins, mirroring addDef), so a later same-named
// declaration never clobbers it. A zero meta is skipped so it does not shadow a
// real one recorded for the same name.
func (w *tsWalk) setDefMeta(bare string, m model.NodeMeta) {
	if bare == "" || len(m.Annotations) == 0 && len(m.Flags) == 0 {
		return
	}
	if _, set := w.defMeta[bare]; set {
		return
	}
	w.defMeta[bare] = m
}

// point returns the 0-based start position of n as a TSPoint.
func point(n *gts.Node) TSPoint {
	sp := n.StartPoint()
	return TSPoint{Row: sp.Row, Column: sp.Column}
}

// collectDefs walks the top-level statements of the program collecting definitions.
// It recurses into class bodies (for methods) but NOT into function bodies (nested
// declarations are out of scope for the closed top-level node set, mirroring the Go
// reference which only declares top-level decls).
func (w *tsWalk) collectDefs(n *gts.Node) {
	w.walkDefs(n, false)
}

func (w *tsWalk) walkDefs(n *gts.Node, inClass bool) {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "function_declaration":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				w.addDef(name.Text(w.src), KindFunction, point(name))
			}
		case "class_declaration":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				w.addDef(name.Text(w.src), KindType, point(name))
			}
			// Recurse into the class body to pick up methods.
			if body := c.ChildByFieldName("body", w.lang); body != nil {
				w.walkDefs(body, true)
			}
		case "interface_declaration", "type_alias_declaration", "enum_declaration":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				w.addDef(name.Text(w.src), KindType, point(name))
			}
		case "method_definition":
			if inClass {
				if name := c.ChildByFieldName("name", w.lang); name != nil {
					bare := name.Text(w.src)
					w.addDef(bare, KindMethod, point(name))
					// WP-14 follow-up: a TS `override` member is invoked through its
					// supertype, so flag it "override" to exempt it from dead_symbol.
					if childByType(c, "override_modifier", w.lang) != nil {
						w.setDefMeta(bare, model.NewNodeMeta(nil, []string{"override"}))
					}
				}
			}
		case "lexical_declaration":
			// const / let
			kind := KindVariable
			if w.lexicalIsConst(c) {
				kind = KindConstant
			}
			w.collectDeclarators(c, kind)
		case "variable_declaration":
			// var
			w.collectDeclarators(c, KindVariable)
		default:
			// Recurse through export wrappers and the program node so exported
			// declarations are still discovered.
			if c.Type(w.lang) == "export_statement" {
				w.walkDefs(c, inClass)
			} else if !inClass {
				// Top-level only: do not descend arbitrary nodes (keeps the node set
				// to genuine top-level declarations).
			}
		}
	}
}

// lexicalIsConst reports whether a lexical_declaration is a `const` (vs `let`).
func (w *tsWalk) lexicalIsConst(decl *gts.Node) bool {
	for i := 0; i < decl.ChildCount(); i++ {
		c := decl.Child(i)
		if c == nil {
			continue
		}
		if t := c.Type(w.lang); t == "const" {
			return true
		} else if t == "let" || t == "var" {
			return false
		}
	}
	return false
}

// collectDeclarators records each variable_declarator name in a declaration.
func (w *tsWalk) collectDeclarators(decl *gts.Node, kind string) {
	for i := 0; i < decl.ChildCount(); i++ {
		c := decl.Child(i)
		if c == nil || c.Type(w.lang) != "variable_declarator" {
			continue
		}
		name := c.ChildByFieldName("name", w.lang)
		if name == nil || name.Type(w.lang) != "identifier" {
			continue // destructuring patterns are out of scope this slice
		}
		w.addDef(name.Text(w.src), kind, point(name))
	}
}

// resolveUses walks every top-level definition's body, attributing each call /
// type-reference / selector use to that definition. Intra-file uses (target is a
// mapped def) become TSEdgeSpecs; cross-file/selector/unmapped uses become inert
// PendingRefs (no fabricated endpoint).
func (w *tsWalk) resolveUses(n *gts.Node) {
	w.walkUses(n, "", false)
}

func (w *tsWalk) walkUses(n *gts.Node, ownerBare string, inClass bool) {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "function_declaration":
			name := c.ChildByFieldName("name", w.lang)
			if name != nil {
				// Scan the whole declaration (formal parameters + return type
				// annotation + body) so signature type references are attributed
				// to this owner, not only the statement body.
				w.scanBody(c, name.Text(w.src))
			}
		case "method_definition":
			if inClass {
				name := c.ChildByFieldName("name", w.lang)
				if name != nil {
					w.scanBody(c, name.Text(w.src))
				}
			}
		case "class_declaration":
			if body := c.ChildByFieldName("body", w.lang); body != nil {
				w.walkUses(body, ownerBare, true)
			}
		case "export_statement":
			w.walkUses(c, ownerBare, inClass)
		}
	}
}

// scanBody walks a definition body recursively, recording every call/type-reference
// against the enclosing owner (a mapped definition). It descends through nested
// blocks/statements but attributes everything to the top-level owner.
func (w *tsWalk) scanBody(n *gts.Node, ownerBare string) {
	if n == nil {
		return
	}
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "call_expression":
			w.handleCall(c, ownerBare)
		case "type_annotation":
			w.handleTypeRef(c, ownerBare)
		}
		// Always recurse: calls/refs can be nested arbitrarily deep within the body.
		w.scanBody(c, ownerBare)
	}
}

// handleCall classifies a call expression: a bare-identifier callee resolving to an
// in-file function/method becomes an intra-file calls edge; a selector callee
// (obj.method()) or an unmapped bare callee becomes a PendingRef.
func (w *tsWalk) handleCall(call *gts.Node, ownerBare string) {
	fn := call.ChildByFieldName("function", w.lang)
	if fn == nil {
		return
	}
	ownerQN := w.pkg + "." + ownerBare
	switch fn.Type(w.lang) {
	case "identifier":
		name := fn.Text(w.src)
		if _, callable := w.funcs[name]; callable {
			w.addEdge(ownerQN, w.pkg+"."+name, EdgeCalls, point(fn),
				"call resolved to an in-file definition")
			return
		}
		// Unmapped bare call: same-package cross-file or undeclared — defer.
		w.addPending(PendingRef{
			FromQN: ownerQN, Name: name, Kind: EdgeCalls,
			Line: int(point(fn).Row) + 1,
		})
	case "member_expression":
		obj := fn.ChildByFieldName("object", w.lang)
		prop := fn.ChildByFieldName("property", w.lang)
		if obj == nil || prop == nil {
			return
		}
		w.addPending(PendingRef{
			FromQN:       ownerQN,
			SelectorBase: obj.Text(w.src),
			Name:         prop.Text(w.src),
			Kind:         EdgeCalls,
			Line:         int(point(prop).Row) + 1,
			Selector:     true,
		})
	}
}

// handleTypeRef records a type reference in an annotation: a type_identifier
// resolving to an in-file type becomes an intra-file references edge; an unmapped
// type becomes a PendingRef.
func (w *tsWalk) handleTypeRef(ann *gts.Node, ownerBare string) {
	ownerQN := w.pkg + "." + ownerBare
	for i := 0; i < ann.ChildCount(); i++ {
		c := ann.Child(i)
		if c == nil || c.Type(w.lang) != "type_identifier" {
			continue
		}
		name := c.Text(w.src)
		if kind, ok := w.defKind[name]; ok && kind == KindType {
			w.addEdge(ownerQN, w.pkg+"."+name, EdgeReferences, point(c),
				"type reference resolved to an in-file definition")
			continue
		}
		w.addPending(PendingRef{
			FromQN: ownerQN, Name: name, Kind: EdgeReferences,
			Line: int(point(c).Row) + 1,
		})
	}
}

func (w *tsWalk) addEdge(fromQN, toQN, kind string, pos TSPoint, reason string) {
	key := fromQN + "\x00" + toQN + "\x00" + kind
	if _, dup := w.edgeSeen[key]; dup {
		return
	}
	w.edgeSeen[key] = struct{}{}
	conf := 0.9
	if kind == EdgeReferences {
		conf = 0.8
	}
	w.edgeSpecs = append(w.edgeSpecs, TSEdgeSpec{
		FromQN: fromQN, ToQN: toQN, Kind: kind, Pos: pos,
		Tier: model.TierDerived, Confidence: conf, Reason: reason,
	})
}

func (w *tsWalk) addPending(p PendingRef) {
	key := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%v", p.FromQN, p.SelectorBase, p.Name, p.Kind, p.Selector)
	if _, dup := w.pendSeen[key]; dup {
		return
	}
	w.pendSeen[key] = struct{}{}
	w.pending = append(w.pending, p)
}

// tsPackage derives the symbol-namespace prefix for a TypeScript file: the parent
// directory's base name (e.g. "shop" for "shop/cart.ts"), falling back to the file
// stem when the file is at the root. This mirrors the fixture convention in
// mapping_test.go (shop.Cart for shop/cart.ts).
func tsPackage(filename string) string {
	dir := filepath.Dir(filename)
	base := filepath.Base(dir)
	if base == "." || base == "/" || base == "" {
		stem := filepath.Base(filename)
		if i := strings.LastIndexByte(stem, '.'); i > 0 {
			stem = stem[:i]
		}
		return stem
	}
	return base
}

// tsImports extracts the import declarations of a TypeScript file as ImportSpecs by
// walking the top-level CST. Named imports record each imported binding as the alias;
// namespace imports record the local alias; the path is the module specifier
// (without surrounding quotes). Order follows source order.
func tsImports(t *tsAST) []ImportSpec {
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
		path := tsImportPath(c, t.lang, t.src)
		if path == "" {
			continue
		}
		clause := tsChildByType(c, "import_clause", t.lang)
		if clause == nil {
			out = append(out, ImportSpec{Path: path})
			continue
		}
		// Namespace import: import * as ns from "m"
		if ns := tsChildByType(clause, "namespace_import", t.lang); ns != nil {
			if id := tsChildByType(ns, "identifier", t.lang); id != nil {
				out = append(out, ImportSpec{Alias: id.Text(t.src), Path: path})
			}
			continue
		}
		// Named imports: import { X, Y } from "m"
		if named := tsChildByType(clause, "named_imports", t.lang); named != nil {
			for j := 0; j < named.ChildCount(); j++ {
				spec := named.Child(j)
				if spec == nil || spec.Type(t.lang) != "import_specifier" {
					continue
				}
				name := spec.ChildByFieldName("name", t.lang)
				if name != nil {
					out = append(out, ImportSpec{Alias: name.Text(t.src), Path: path})
				}
			}
			continue
		}
		out = append(out, ImportSpec{Path: path})
	}
	return out
}

// tsImportPath returns the module specifier string of an import_statement, unquoted.
func tsImportPath(imp *gts.Node, lang *gts.Language, src []byte) string {
	src2 := tsChildByField(imp, "source", lang)
	if src2 == nil {
		src2 = tsChildByType(imp, "string", lang)
	}
	if src2 == nil {
		return ""
	}
	if frag := tsChildByType(src2, "string_fragment", lang); frag != nil {
		return frag.Text(src)
	}
	// Fallback: strip surrounding quotes from the raw string node.
	return strings.Trim(src2.Text(src), "\"'`")
}

func tsChildByType(n *gts.Node, typ string, lang *gts.Language) *gts.Node {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c != nil && c.Type(lang) == typ {
			return c
		}
	}
	return nil
}

func tsChildByField(n *gts.Node, field string, lang *gts.Language) *gts.Node {
	return n.ChildByFieldName(field, lang)
}
