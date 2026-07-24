//go:build graphi_broad

// This file is the opt-in graphi-broad CGO parser bundle (SW-056). It is wired
// ONLY into builds carrying `-tags graphi_broad` (CGO_ENABLED=1) and is NEVER part
// of the default, CGo-free graph: every symbol here is behind the `graphi_broad`
// build constraint, so an untagged build (and the SW-055 firewall +
// internal/cgoconformance import-graph scan) never sees go-sitter-forest.
//
// It registers the go-sitter-forest grammar backend (CGO tree-sitter via
// go-tree-sitter-bare) over the SAME SymbolExtractor contract (SW-052) the pure-Go
// default tier uses — `RegisterBroad(r)` is the disjoint opt-in seam; untagged
// `RegisterDefaults` (defaults.go) stays byte-identical.
//
// Runtime/contract notes (DN-2):
//   - go-tree-sitter-bare exposes a VALUE Node with Type() taking no language arg
//     and Parse(ctx, src, lang) — a DIFFERENT runtime than the gotreesitter
//     parser_*.go templates, so the CST walk here is a fresh implementation. The
//     SymbolExtractor `root any` contract and the pure MapTreeSitter helper are
//     reused; the walk is not.
//   - The depth guard reads the SAME maxParseDepth() source as the default tier, so
//     the Go-side walk shares the untrusted-input nesting bound. NOTE (DN-5): this
//     bounds the Go walk only; the native-C parser is NOT bounded by it. The
//     residual native-C crash/OOM/RCE risk is opt-in and HUMAN-ACCEPTED
//     (SW-056-SEC-001); out-of-process isolation is follow-up SW-058.
//
// To wire an additional CGO-only grammar, import its single subpackage
// (github.com/alexaandru/go-sitter-forest/<lang>) and add one registration line
// below — NOT the top-level `forest` meta-module, which statically imports all
// ~257 grammars (hundreds of MB of generated C, DN-1).
package parse

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	sitter "github.com/alexaandru/go-tree-sitter-bare"
	"github.com/samibel/graphi/core/model"

	zig "github.com/alexaandru/go-sitter-forest/zig"
)

// RegisterBroad registers graphi's opt-in CGO go-sitter-forest parsers onto r and
// returns r for chaining. It is the graphi-broad analogue of RegisterDefaults: a
// DISJOINT registration seam reached only under `-tags graphi_broad`. The default
// RegisterDefaults is never touched, so the default tier stays provably pure-Go.
//
// Each line wires one CGO-only grammar subpackage over the shared SymbolExtractor
// contract. The grammars here are CGO-only (absent from the pure-Go default set);
// adding one is a single r.Register(...) line.
func RegisterBroad(r *Registry) *Registry {
	r.Register(NewZigParser()) // zig — CGO-only go-sitter-forest grammar (graphi-broad)
	return r
}

// forestLang pairs a go-sitter-forest grammar pointer with its canonical language
// id and file extensions, so every broad parser is one tiny declaration over the
// shared forestParser machinery.
type forestLang struct {
	language   string
	extensions []string
	getLang    func() *sitter.Language
}

// forestParser is the shared graphi-broad Parser over the go-tree-sitter-bare
// (CGO) runtime. It carries no mutable state and is safe for concurrent use: each
// Parse builds its own tree and walk.
type forestParser struct {
	spec      forestLang
	extractor SymbolExtractor
}

func newForestParser(spec forestLang) *forestParser {
	return &forestParser{spec: spec, extractor: &forestExtractor{spec: spec}}
}

// Language implements Parser.
func (p *forestParser) Language() string { return p.spec.language }

// Extensions implements Parser.
func (p *forestParser) Extensions() []string { return p.spec.extensions }

// Runtime implements Parser: the CGO go-sitter-forest backend. It declares
// RuntimeCGOForest so any accidental registration in the default tier is rejected
// by AssertPureGoDefaults (the runtime is NOT in the pure-Go allowlist).
func (p *forestParser) Runtime() Runtime { return RuntimeCGOForest }

// forestAST is the graphi-broad backend root handle threaded through the
// SymbolExtractor `root any` contract. It carries the bare-runtime root Node
// value, the OWNING *sitter.Tree (the root Node is only a view into it), the
// source bytes, and the resolved *sitter.Language.
type forestAST struct {
	root sitter.Node
	tree *sitter.Tree
	src  []byte
	lang *sitter.Language
}

// Close releases the C tree backing this AST (parse.ReleaseRoot's seam). The
// bare runtime registers NO finalizer on trees — only an explicit Close
// reaches ts_tree_delete — so a dropped forestAST without Close leaks the
// whole C tree (routinely 10-40x the source size) permanently. Idempotent
// (Tree.Close is once-guarded) and nil-safe. The root Node must not be used
// after Close.
func (a *forestAST) Close() {
	if a == nil || a.tree == nil {
		return
	}
	a.tree.Close()
}

// Parse implements Parser. It runs the CGO tree-sitter parse, then maps the bare
// CST onto the shared vocabulary via forestExtractor. It honors ctx and never
// panics out to the caller (recovering internally) — though note (DN-5) a native-C
// crash cannot be recovered, only a Go-side panic.
func (p *forestParser) Parse(ctx context.Context, filename string, src []byte) (res *ParseResult, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	defer func() {
		if r := recover(); r != nil {
			res = nil
			err = fmt.Errorf("parse: recovered from panic parsing %q: %v", filename, r)
		}
	}()

	lang := p.spec.getLang()
	// Parse explicitly (not the sitter.Parse shortcut, which returns only the
	// root Node and DROPS the owning Tree): the Tree handle must survive on
	// the forestAST so Close can reach ts_tree_delete — without it every parse
	// leaks its C tree, the runtime registers no finalizer on trees.
	parser := sitter.NewParser()
	parser.SetLanguage(lang)
	tree, perr := parser.ParseString(ctx, nil, src)
	if perr != nil {
		return nil, fmt.Errorf("parse: %s (graphi-broad) error in %q: %w", p.spec.language, filename, perr)
	}
	ast := &forestAST{root: tree.RootNode(), tree: tree, src: src, lang: lang}

	extractor := p.extractor
	if extractor == nil {
		extractor = &forestExtractor{spec: p.spec}
	}
	nodes, edges, pending, xerr := extractor.Extract(filename, ast)
	if xerr != nil {
		return nil, fmt.Errorf("parse: %s (graphi-broad) extraction in %q: %w", p.spec.language, filename, xerr)
	}

	return &ParseResult{
		Meta: SourceMeta{
			Path: filename, Language: p.spec.language,
			ContentHash: contentHash(src), Size: len(src),
		},
		Root:        ast,
		Nodes:       nodes,
		Edges:       edges,
		PendingRefs: pending,
	}, nil
}

// forestExtractor is the SymbolExtractor for the graphi-broad bare runtime. It
// reuses the shared, pure MapTreeSitter helper but performs a FRESH CST walk over
// the value-typed bare Node (Type() takes no lang arg — a different runtime than
// the gotreesitter templates). It carries no mutable state; the per-Extract walk
// holds all accumulation.
type forestExtractor struct{ spec forestLang }

// Language implements SymbolExtractor.
func (e *forestExtractor) Language() string { return e.spec.language }

// Extract implements SymbolExtractor for the graphi-broad path. It type-asserts the
// *forestAST root, fail-closes on excessive nesting (the SAME maxParseDepth() bound
// the default tier uses — Go-walk only, DN-5), collects top-level definitions, and
// returns the mapped nodes/edges plus inert PendingRefs.
func (e *forestExtractor) Extract(filename string, root any) ([]model.Node, []model.Edge, []PendingRef, error) {
	ast, ok := root.(*forestAST)
	if !ok || ast == nil {
		return nil, nil, nil, fmt.Errorf("parse: %s (graphi-broad) extractor: expected non-nil *forestAST root for %q, got %T", e.spec.language, filename, root)
	}
	w := newForestWalk(ast.src, e.spec.language, forestPackage(filename))
	if derr := w.guardDepth(ast.root, filename); derr != nil {
		return nil, nil, nil, derr
	}
	w.collectDefs(ast.root)
	nodes, edges, err := MapTreeSitter(filename, e.spec.language, w.nodeSpecs(), nil, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	return nodes, edges, w.pending, nil
}

// forestWalk accumulates the definition table for a single graphi-broad Extract
// pass over the bare CST. It is created per Extract and never shared, mirroring the
// default tier's cstWalk discipline but over the value-typed bare Node.
type forestWalk struct {
	src      []byte
	language string
	pkg      string
	maxDepth int

	defKind  map[string]string
	defPos   map[string]TSPoint
	defOrder []string
	pending  []PendingRef
}

func newForestWalk(src []byte, language, pkg string) *forestWalk {
	return &forestWalk{
		src:      src,
		language: language,
		pkg:      pkg,
		maxDepth: maxParseDepth(),
		defKind:  map[string]string{},
		defPos:   map[string]TSPoint{},
	}
}

// guardDepth fail-closes the graphi-broad Go walk against deeply-nested inputs,
// reading the SAME process-wide bound the default tier uses (DN-5: Go walk only;
// the native-C parse is NOT bounded by this). It measures nesting ITERATIVELY (an
// explicit work stack — it never recurses, so the guard itself cannot overflow) and
// returns a SanitizedError wrapping ErrMaxDepthExceeded, carrying ONLY structured
// provenance (no raw source). A zero maxDepth disables the bound.
func (w *forestWalk) guardDepth(root sitter.Node, filename string) error {
	if w.maxDepth <= 0 || root.IsNull() {
		return nil
	}
	type frame struct {
		n     sitter.Node
		depth int
	}
	stack := []frame{{n: root, depth: 1}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.depth > w.maxDepth {
			return SanitizedError(Provenance{
				File:      filename,
				Language:  w.language,
				ByteStart: -1,
				ByteEnd:   -1,
				NodeKind:  f.n.Type(),
			}, ErrMaxDepthExceeded)
		}
		for i := uint32(0); i < f.n.ChildCount(); i++ {
			c := f.n.Child(i)
			if !c.IsNull() {
				stack = append(stack, frame{n: c, depth: f.depth + 1})
			}
		}
	}
	return nil
}

// collectDefs records top-level definitions discovered in the bare CST onto the
// frozen Kind* vocabulary. It walks the whole tree iteratively and maps a small,
// language-neutral set of declaration node types so the smoke grammar(s) produce a
// stable, frozen vocabulary (DN-1/DN-4). It records no fabricated edges — calls and
// cross-symbol references are out of scope for the broad smoke path and would be
// recorded as PendingRefs by a fuller extractor.
func (w *forestWalk) collectDefs(root sitter.Node) {
	if root.IsNull() {
		return
	}
	type frame struct{ n sitter.Node }
	stack := []frame{{n: root}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if kind, name, ok := w.classify(f.n); ok {
			w.addDef(name, kind, nodePointBare(f.n))
		}
		for i := uint32(0); i < f.n.ChildCount(); i++ {
			c := f.n.Child(i)
			if !c.IsNull() {
				stack = append(stack, frame{n: c})
			}
		}
	}
}

// classify maps a bare CST node onto the frozen vocabulary. It recognizes a
// language-neutral set of declaration node types common across tree-sitter
// grammars (function/variable/constant/type), keyed off the node Type() and a
// best-effort name child. Unrecognized nodes return ok=false.
func (w *forestWalk) classify(n sitter.Node) (kind, name string, ok bool) {
	switch n.Type() {
	case "function_declaration", "FnProto", "function_definition", "method_declaration":
		if nm := forestName(n, w.src); nm != "" {
			return KindFunction, nm, true
		}
	case "VarDecl", "variable_declaration", "global_variable_declaration":
		if nm := forestName(n, w.src); nm != "" {
			return KindVariable, nm, true
		}
	case "ContainerDecl", "struct_declaration", "type_declaration", "enum_declaration":
		if nm := forestName(n, w.src); nm != "" {
			return KindType, nm, true
		}
	}
	return "", "", false
}

// addDef records a top-level definition (first binding wins), deterministically.
func (w *forestWalk) addDef(bare, kind string, pos TSPoint) {
	if bare == "" {
		return
	}
	if _, seen := w.defKind[bare]; seen {
		return
	}
	w.defKind[bare] = kind
	w.defPos[bare] = pos
	w.defOrder = append(w.defOrder, bare)
}

// nodeSpecs builds position-sorted node specs for deterministic output, mirroring
// the default tier's cstWalk.nodeSpecs.
func (w *forestWalk) nodeSpecs() []TSNodeSpec {
	specs := make([]TSNodeSpec, 0, len(w.defOrder))
	for _, bare := range w.defOrder {
		specs = append(specs, TSNodeSpec{
			Kind:          w.defKind[bare],
			QualifiedName: w.pkg + "." + bare,
			Pos:           w.defPos[bare],
		})
	}
	// Stable position-then-name ordering (deterministic).
	for i := 1; i < len(specs); i++ {
		for j := i; j > 0; j-- {
			a, b := specs[j], specs[j-1]
			less := a.Pos.Row < b.Pos.Row ||
				(a.Pos.Row == b.Pos.Row && a.Pos.Column < b.Pos.Column) ||
				(a.Pos.Row == b.Pos.Row && a.Pos.Column == b.Pos.Column && a.QualifiedName < b.QualifiedName)
			if !less {
				break
			}
			specs[j], specs[j-1] = specs[j-1], specs[j]
		}
	}
	return specs
}

// forestName returns a best-effort declared name for a bare CST declaration node:
// the "name" field child if present, else the first identifier-like named child.
func forestName(n sitter.Node, src []byte) string {
	if nm := n.ChildByFieldName("name"); !nm.IsNull() {
		return nm.Content(src)
	}
	for i := uint32(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c.IsNull() {
			continue
		}
		if strings.Contains(c.Type(), "identifier") || c.Type() == "IDENTIFIER" {
			return c.Content(src)
		}
	}
	return ""
}

// nodePointBare returns the 0-based start position of a bare Node as a TSPoint.
// The bare runtime's Point uses uint; TSPoint uses uint32 (the mapping helper's
// type) — the narrowing is safe for any real source position.
func nodePointBare(n sitter.Node) TSPoint {
	sp := n.StartPoint()
	return TSPoint{Row: uint32(sp.Row), Column: uint32(sp.Column)}
}

// forestPackage derives the symbol-namespace prefix for a source file, identical to
// the default tier's langPackage so the broad path shares the fixture convention.
func forestPackage(filename string) string {
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

// --- Grammar registrations (one declaration per CGO-only grammar) ---

// NewZigParser returns a ready graphi-broad Zig parser over the CGO go-sitter-forest
// zig grammar. Zig is CGO-only (absent from the pure-Go default set; self-contained
// scanner — DN-1's recommended smoke grammar).
func NewZigParser() *forestParser {
	return newForestParser(forestLang{
		language:   "zig",
		extensions: []string{".zig"},
		getLang: func() *sitter.Language {
			return sitter.NewLanguage(zig.GetLanguage())
		},
	})
}
