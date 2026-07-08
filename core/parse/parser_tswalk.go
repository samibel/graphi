package parse

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	gts "github.com/odvcencio/gotreesitter"

	"github.com/samibel/graphi/core/model"
)

// This file factors the common machinery shared by every SW-054 pure-Go
// gotreesitter language extractor (JavaScript, TSX, Python, Java, … HCL). It is the
// SW-053 `parser_ts.go` recipe distilled to its language-neutral core so each
// per-language parser_<lang>.go stays a thin, declarative mapping of grammar node
// types onto the frozen Kind*/Edge* vocabulary instead of re-deriving the two-pass
// walk, the intra-file-edge-vs-PendingRef split, the dedup tables, the deterministic
// node/edge ordering, and the MapTreeSitter call 20×.
//
// It carries NO mutable package state. Every helper either operates on an explicitly
// passed *cstWalk (allocated per Extract call) or is a pure function over its
// arguments, so all 20 extractors remain safe for concurrent use exactly like the TS
// reference (per-Extract walk, no shared mutable state).

// cstWalk accumulates the definition tables and the resolved edge/pending split for a
// single source file, language-agnostically. It mirrors tsWalk (parser_ts.go) but is
// reused by every SW-054 extractor. It is created per Extract call and never shared.
type cstWalk struct {
	lang *gts.Language
	src  []byte
	pkg  string

	// maxDepth is the fail-closed recursion/nesting bound applied to the CST of
	// untrusted inputs (SW-055 AC#6). It is seeded from the package-level
	// default (configurable via SetMaxParseDepth) at newCSTWalk time. 0 = unbounded.
	maxDepth int

	defKind  map[string]string         // bareName -> Kind (first binding wins)
	defPos   map[string]TSPoint        // bareName -> definition position
	defMeta  map[string]model.NodeMeta // bareName -> non-identity meta (first binding wins)
	defOrder []string                  // discovery order
	funcs    map[string]struct{}       // bare names that are callable (function/method)
	types    map[string]struct{}       // bare names that are types

	edgeSpecs []TSEdgeSpec
	edgeSeen  map[string]struct{}
	pending   []PendingRef
	pendSeen  map[string]struct{}
}

// newCSTWalk allocates a cstWalk for one Extract pass. It seeds the fail-closed
// parse-depth bound from the package-level default so every gotreesitter extractor
// shares the same untrusted-input nesting guard with no per-parser wiring.
func newCSTWalk(lang *gts.Language, src []byte, pkg string) *cstWalk {
	return &cstWalk{
		lang:     lang,
		src:      src,
		pkg:      pkg,
		maxDepth: maxParseDepth(),
		defKind:  map[string]string{},
		defPos:   map[string]TSPoint{},
		defMeta:  map[string]model.NodeMeta{},
		funcs:    map[string]struct{}{},
		types:    map[string]struct{}{},
		edgeSeen: map[string]struct{}{},
		pendSeen: map[string]struct{}{},
	}
}

// addDef records a top-level definition (first binding wins), tracking whether it is
// callable (for intra-file call resolution) or a type (for intra-file reference
// resolution).
func (w *cstWalk) addDef(bare, kind string, pos TSPoint) {
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
	if kind == KindType {
		w.types[bare] = struct{}{}
	}
}

// setDefMeta attaches NON-identity metadata (annotations/flags) to a
// previously-added definition, keyed by bare name. First non-empty binding wins
// (mirroring addDef's first-binding-wins), so a later same-named declaration
// cannot clobber it. A zero meta or unknown bare name is ignored.
func (w *cstWalk) setDefMeta(bare string, m model.NodeMeta) {
	if bare == "" || m.IsZero() {
		return
	}
	if _, set := w.defMeta[bare]; set {
		return
	}
	w.defMeta[bare] = m
}

// guardDepth fail-closes the extract pass against deeply-nested (billion-laughs /
// stack-overflow) inputs (SW-055 AC#6). It measures the CST nesting depth of root
// ITERATIVELY (an explicit work stack — it never recurses, so the guard itself
// cannot overflow) and returns a SanitizedError wrapping ErrMaxDepthExceeded the
// moment the configured maxDepth is exceeded, short-circuiting before the
// recursive per-language collectors descend. It carries ONLY structured provenance
// (no raw source). A zero maxDepth disables the bound.
func (w *cstWalk) guardDepth(root *gts.Node, filename, language string) error {
	return guardCSTDepth(root, w.lang, w.maxDepth, filename, language)
}

// nodeSpecs builds position-sorted node specs (Row, Column, QualifiedName) for
// deterministic output, mirroring the TS reference.
func (w *cstWalk) nodeSpecs() []TSNodeSpec {
	type entry struct {
		spec TSNodeSpec
	}
	entries := make([]entry, 0, len(w.defOrder))
	for _, bare := range w.defOrder {
		entries = append(entries, entry{
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
	out := make([]TSNodeSpec, 0, len(entries))
	for _, en := range entries {
		out = append(out, en.spec)
	}
	return out
}

// callBare resolves a bare-identifier call site against the in-file definition table:
// a callee in w.funcs becomes an intra-file calls edge; an unmapped bare callee becomes
// an inert PendingRef (no fabricated NodeId), exactly like the Go/TS reference.
func (w *cstWalk) callBare(ownerBare, calleeName string, pos TSPoint) {
	ownerQN := w.pkg + "." + ownerBare
	if _, callable := w.funcs[calleeName]; callable {
		w.addEdge(ownerQN, w.pkg+"."+calleeName, EdgeCalls, pos,
			"call resolved to an in-file definition")
		return
	}
	w.addPending(PendingRef{
		FromQN: ownerQN, Name: calleeName, Kind: EdgeCalls,
		Line: int(pos.Row) + 1,
	})
}

// callSelector records a selector call site (obj.method()) as a selector PendingRef:
// the linker resolves it later; the extractor never fabricates an endpoint.
func (w *cstWalk) callSelector(ownerBare, base, name string, pos TSPoint) {
	w.addPending(PendingRef{
		FromQN:       w.pkg + "." + ownerBare,
		SelectorBase: base,
		Name:         name,
		Kind:         EdgeCalls,
		Line:         int(pos.Row) + 1,
		Selector:     true,
	})
}

// typeRef resolves a bare type reference against the in-file type table: a name in
// w.types becomes an intra-file references edge; an unmapped type becomes a PendingRef.
func (w *cstWalk) typeRef(ownerBare, typeName string, pos TSPoint) {
	ownerQN := w.pkg + "." + ownerBare
	if _, ok := w.types[typeName]; ok {
		w.addEdge(ownerQN, w.pkg+"."+typeName, EdgeReferences, pos,
			"type reference resolved to an in-file definition")
		return
	}
	w.addPending(PendingRef{
		FromQN: ownerQN, Name: typeName, Kind: EdgeReferences,
		Line: int(pos.Row) + 1,
	})
}

func (w *cstWalk) addEdge(fromQN, toQN, kind string, pos TSPoint, reason string) {
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

func (w *cstWalk) addPending(p PendingRef) {
	key := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%v", p.FromQN, p.SelectorBase, p.Name, p.Kind, p.Selector)
	if _, dup := w.pendSeen[key]; dup {
		return
	}
	w.pendSeen[key] = struct{}{}
	w.pending = append(w.pending, p)
}

// nodePoint returns the 0-based start position of n as a TSPoint.
func nodePoint(n *gts.Node) TSPoint {
	sp := n.StartPoint()
	return TSPoint{Row: sp.Row, Column: sp.Column}
}

// childByType returns the first direct child of n whose type is typ (or nil).
func childByType(n *gts.Node, typ string, lang *gts.Language) *gts.Node {
	if n == nil {
		return nil
	}
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c != nil && c.Type(lang) == typ {
			return c
		}
	}
	return nil
}

// langPackage derives the symbol-namespace prefix for a source file: the parent
// directory's base name, falling back to the file stem at the root. Identical to the
// TS reference tsPackage so every language shares the fixture convention (shop.Foo
// for shop/bar.<ext>).
func langPackage(filename string) string {
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

// finishExtract builds the final nodes/edges for an Extract pass via MapTreeSitter and
// returns them with the recorded PendingRefs. Centralizes the closing step every
// extractor shares.
func (w *cstWalk) finishExtract(filename, language string) ([]model.Node, []model.Edge, []PendingRef, error) {
	nodes, edges, err := MapTreeSitter(filename, language, w.nodeSpecs(), w.edgeSpecs, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	return nodes, edges, w.pending, nil
}

// importsToRefs flattens recorded ImportSpec paths into the References slice (for the
// reverse-dependency cascade), mirroring the Go/TS path.
func importsToRefs(imports []ImportSpec) []string {
	refs := make([]string, 0, len(imports))
	for _, imp := range imports {
		refs = append(refs, imp.Path)
	}
	return refs
}
