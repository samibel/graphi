package parse

import (
	"context"
	"fmt"
	"strings"

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

// ExtractsSymbols implements SymbolCapable: this parser wires a SymbolExtractor
// and emits symbol nodes plus intra-file edges.
func (*JavaParser) ExtractsSymbols() bool { return true }

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

// Kind mapping (Java collapses onto {file, method, type, variable, constant}):
//
//	type     ← class_declaration / interface_declaration / enum_declaration
//	method   ← method_declaration (Java has no free functions; all callables are methods)
//	constant ← constant_declaration declarator (interface fields — the grammar
//	           names their constancy), or a field_declaration declarator with
//	           DECLARED `static final` (WP-J2)
//	variable ← every other field_declaration declarator (WP-J2)
//
// Absent by design: function (no free functions). Field kinds follow the
// declared form only — nothing is inferred beyond what the CST names. Enum
// constants are not extracted at this tier (they live in enum_body, not as
// field_declaration).

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
	javaCollectDefs(w, t.root, filename)
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

func javaCollectDefs(w *cstWalk, program *gts.Node, filename string) {
	for i := 0; i < program.ChildCount(); i++ {
		c := program.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "class_declaration", "interface_declaration", "enum_declaration":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				bare := name.Text(w.src)
				w.addDef(bare, KindType, nodePoint(name))
				// WP-10: attach the declaration's annotations/flags (e.g. a
				// @Configuration or @RestController class) as non-identity meta.
				w.setDefMeta(bare, javaDeclMeta(w, c, bare, filename, false))
			}
			if body := c.ChildByFieldName("body", w.lang); body != nil {
				javaCollectMethods(w, body, filename)
			}
		}
	}
}

func javaCollectMethods(w *cstWalk, body *gts.Node, filename string) {
	for i := 0; i < body.ChildCount(); i++ {
		c := body.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "method_declaration":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				bare := name.Text(w.src)
				w.addDef(bare, KindMethod, nodePoint(name))
				// WP-10: attach method annotations (@Test/@Bean/@Override/…), the
				// static flag, and the main-signature flag as non-identity meta.
				w.setDefMeta(bare, javaDeclMeta(w, c, bare, filename, true))
			}
		case "field_declaration", "constant_declaration":
			// WP-J2 (ADR 0008 D3): field nodes, so field references have honest
			// committed targets when the JVM binder lands. One declaration can
			// carry several declarators (`int a, b;`) sharing its modifiers.
			// Kind follows the DECLARED form only: an interface field parses as
			// constant_declaration (the grammar names its constancy — reading
			// that is not inference), and a class field is a constant exactly
			// when it declares `static final`.
			javaCollectFields(w, c, filename)
		}
	}
}

// javaCollectFields mints one node per variable_declarator of a
// field_declaration, with the declaration's annotations and static/final
// declared modifiers as non-identity meta.
func javaCollectFields(w *cstWalk, decl *gts.Node, filename string) {
	static, final := javaDeclaredModifiers(w, decl)
	kind := KindVariable
	if (static && final) || decl.Type(w.lang) == "constant_declaration" {
		kind = KindConstant
	}
	for i := 0; i < decl.ChildCount(); i++ {
		d := decl.Child(i)
		if d == nil || d.Type(w.lang) != "variable_declarator" {
			continue
		}
		name := d.ChildByFieldName("name", w.lang)
		if name == nil {
			name = childByType(d, "identifier", w.lang)
		}
		if name == nil {
			continue
		}
		bare := name.Text(w.src)
		w.addDef(bare, kind, nodePoint(name))
		w.setDefMeta(bare, javaFieldMeta(w, decl, filename, static, final))
	}
}

// javaDeclaredModifiers reports the explicit `static` / `final` tokens of a
// declaration's `modifiers` child. Absent modifiers read false — declared
// modifiers only, never language-implied ones.
func javaDeclaredModifiers(w *cstWalk, decl *gts.Node) (static, final bool) {
	mods := childByType(decl, "modifiers", w.lang)
	if mods == nil {
		return false, false
	}
	for i := 0; i < mods.ChildCount(); i++ {
		m := mods.Child(i)
		if m == nil {
			continue
		}
		switch m.Text(w.src) {
		case "static":
			static = true
		case "final":
			final = true
		}
	}
	return static, final
}

// javaFieldMeta derives the NON-identity NodeMeta for a field declarator:
// annotation names plus the declared static/final flags and the test-path
// flag — the same deterministic NewNodeMeta discipline as javaDeclMeta, kept
// separate because fields carry `final` where classes/methods deliberately do
// not (changing javaDeclMeta would move existing class/method meta bytes).
func javaFieldMeta(w *cstWalk, decl *gts.Node, filename string, static, final bool) model.NodeMeta {
	var annotations, flags []string
	if mods := childByType(decl, "modifiers", w.lang); mods != nil {
		for i := 0; i < mods.ChildCount(); i++ {
			m := mods.Child(i)
			if m == nil {
				continue
			}
			switch m.Type(w.lang) {
			case "marker_annotation", "annotation":
				if name := javaAnnotationName(w, m); name != "" {
					annotations = append(annotations, name)
				}
			}
		}
	}
	if static {
		flags = append(flags, "static")
	}
	if final {
		flags = append(flags, "final")
	}
	if javaIsTestPath(filename) {
		flags = append(flags, "test_path")
	}
	return model.NewNodeMeta(annotations, flags)
}

// javaDeclMeta derives the NON-identity NodeMeta for a class/method declaration:
// its annotation NAMES and the `static` flag from the `modifiers` child, plus a
// `main` flag for a static `main` method and a `test_path` flag when the file
// sits on a Java test path. NewNodeMeta sorts+dedups so the result is
// deterministic and a pure function of the source.
func javaDeclMeta(w *cstWalk, decl *gts.Node, bareName, filename string, isMethod bool) model.NodeMeta {
	var annotations, flags []string
	static := false
	if mods := childByType(decl, "modifiers", w.lang); mods != nil {
		for i := 0; i < mods.ChildCount(); i++ {
			m := mods.Child(i)
			if m == nil {
				continue
			}
			switch m.Type(w.lang) {
			case "marker_annotation", "annotation":
				if name := javaAnnotationName(w, m); name != "" {
					annotations = append(annotations, name)
				}
			default:
				if m.Text(w.src) == "static" {
					static = true
				}
			}
		}
	}
	if static {
		flags = append(flags, "static")
	}
	// `public static void main(String[])`: name main + static is a program entry.
	if isMethod && bareName == "main" && static {
		flags = append(flags, "main")
	}
	if javaIsTestPath(filename) {
		flags = append(flags, "test_path")
	}
	return model.NewNodeMeta(annotations, flags)
}

// javaAnnotationName extracts the bare annotation identifier (the token after
// '@') from a marker_annotation / annotation node — the trailing segment of a
// scoped name (`org.junit.Test` → "Test"). Returns "" when no name resolves.
func javaAnnotationName(w *cstWalk, ann *gts.Node) string {
	nameNode := ann.ChildByFieldName("name", w.lang)
	if nameNode == nil {
		for i := 0; i < ann.ChildCount(); i++ {
			c := ann.Child(i)
			if c == nil {
				continue
			}
			if t := c.Type(w.lang); t == "identifier" || t == "scoped_identifier" {
				nameNode = c
				break
			}
		}
	}
	if nameNode == nil {
		return ""
	}
	text := nameNode.Text(w.src)
	if idx := strings.LastIndex(text, "."); idx >= 0 {
		text = text[idx+1:]
	}
	return strings.TrimSpace(text)
}

// javaIsTestPath reports whether a Java source path follows a test convention:
// a `src/test/` directory, or a `*Test.java` / `*Tests.java` file name.
func javaIsTestPath(p string) bool {
	if strings.Contains(p, "src/test/") {
		return true
	}
	base := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		base = p[i+1:]
	}
	return strings.HasSuffix(base, "Test.java") || strings.HasSuffix(base, "Tests.java")
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
