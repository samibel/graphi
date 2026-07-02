package typeresolve

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path"
	"sort"
	"strconv"

	"github.com/samibel/graphi/core/model"
)

// This file is the type-check + edge-emission slice (roadmap PR 4.3, still
// dark): it runs stdlib types.Config.Check over the package units produced by
// GroupPackages/CheckOrder (pkggraph.go) and derives CONFIRMED-tier
// calls/references/implements edges from the resulting types.Info. No
// golang.org/x/tools, no go/packages, no exec, no network — imports outside
// the repository are served as empty stub packages and every resulting
// type-check error is swallowed per unit, never aborting the pass.
//
// Tier honesty: an edge is emitted here ONLY when go/types bound the
// identifier to a concrete object AND both endpoints reconstruct (via
// NodeIDFor, qn.go) to NodeIds present in the committed node set. go/types
// binding is under-approximating with stub imports — an unresolvable name gets
// NO object rather than a wrong one — so partial type information can only
// drop edges, never mint false ones. Never fabricate: an intent whose
// reconstructed endpoint is not committed is dropped and counted.

// Edge kinds this pass emits, matching the canonical query vocabulary (the
// same strings engine/link uses; duplicated deliberately — see the kind-string
// note in qn.go).
const (
	edgeCalls      = "calls"
	edgeReferences = "references"
	edgeImplements = "implements"
)

// Provenance reasons for the emitted tiers. Kept as constants so the
// collect-then-construct union produces stable reason strings.
const (
	reasonCall       = "call target resolved by the go/types type-checker"
	reasonReference  = "reference resolved by the go/types type-checker"
	reasonImplements = "interface satisfaction proven by the go/types type-checker"
)

// Result is the outcome of one Resolve pass over a repository snapshot.
type Result struct {
	// Edges are the confirmed-tier edges, constructed exactly once per logical
	// (from,to,kind) with sorted-union evidence, sorted by EdgeId.
	Edges []model.Edge
	// Units reports every package unit in check order with its outcome.
	Units []UnitResult
	// SkippedFiles are the sources GroupPackages could not assign to a unit.
	SkippedFiles []SkippedFile
	// DroppedIntents counts resolution intents discarded because an endpoint's
	// reconstructed NodeId was not in the committed set (the never-fabricate
	// counter; one count per use site, before edge merging).
	DroppedIntents int
}

// UnitResult is the per-unit observability record.
type UnitResult struct {
	// Dir and Name identify the unit (see Package).
	Dir, Name string
	// Degraded, when non-empty, names why the unit was not type-checked (its
	// symbols keep their heuristic-tier edges — degradation never deletes
	// knowledge). Grouping- and ordering-time reasons carry through; this pass
	// adds full-parse failures and type-checker panics.
	Degraded string
	// TypeErrors counts the type-check errors swallowed for this unit. Non-zero
	// is EXPECTED for any unit importing stdlib or third-party packages (stub
	// imports cannot resolve); it does not degrade the unit because go/types
	// under-approximates on error (see the tier-honesty note above).
	TypeErrors int
}

// Resolve type-checks the repository snapshot in files (the ingest walk's
// path→bytes map; go.mod is read from files["go.mod"]) and returns the
// confirmed-tier edges whose endpoints exist in committed. It is pure and
// deterministic: identical inputs yield byte-identical results, the property
// the full-vs-incremental parity design leans on. A nil/empty committed set
// yields no edges (everything is dropped and counted, never fabricated).
//
// The error return is construction-plumbing only (model.NewEdge rejecting an
// edge, unreachable by construction here); per-unit type-check failures NEVER
// surface as errors — they degrade the unit and the pass continues.
func Resolve(files map[string][]byte, committed map[model.NodeId]struct{}) (Result, error) {
	modulePath, _ := ParseModulePath(files["go.mod"])
	pkgs, skipped := GroupPackages(files)
	ordered := CheckOrder(modulePath, pkgs)

	res := Result{SkippedFiles: skipped}
	fset := token.NewFileSet()
	imp := newStubImporter()
	sink := newIntentSink(committed)
	var checked []checkedUnit

	for _, u := range ordered {
		ur := UnitResult{Dir: u.Dir, Name: u.Name, Degraded: u.Degraded}
		if ur.Degraded == "" {
			pkgPath := unitImportPath(modulePath, u)
			tpkg, info, asts, degraded, typeErrs := checkUnit(fset, pkgPath, u, files, imp)
			ur.Degraded, ur.TypeErrors = degraded, typeErrs
			if degraded == "" {
				imp.checked[pkgPath] = tpkg
				checked = append(checked, checkedUnit{pkg: tpkg, info: info, files: asts})
			}
		}
		res.Units = append(res.Units, ur)
	}

	for _, cu := range checked {
		for _, f := range cu.files {
			sink.collectFile(f, cu.info, fset)
		}
	}
	sink.collectImplements(checked, fset)

	edges, err := sink.construct()
	if err != nil {
		return Result{}, err
	}
	res.Edges = edges
	res.DroppedIntents = sink.dropped
	return res, nil
}

// checkedUnit pairs a successfully checked package with its info and ASTs for
// the emission passes.
type checkedUnit struct {
	pkg   *types.Package
	info  *types.Info
	files []*ast.File
}

// unitImportPath is the import path a unit is checked (and served) under:
// modulePath for the root, modulePath/dir below it, and the bare directory
// when the repo has no module path (then no import resolves intra-repo anyway
// and the path only needs to be unique per unit).
func unitImportPath(modulePath string, u Package) string {
	switch {
	case modulePath == "":
		return u.Dir
	case u.Dir == ".":
		return modulePath
	default:
		return modulePath + "/" + u.Dir
	}
}

// checkUnit fully parses the unit's files (grouping only parsed clauses and
// imports) and runs types.Config.Check with the tolerant importer. Errors are
// counted, never fatal; a full-parse failure or a checker panic degrades the
// unit. The shared fset is what lets NodeIDFor recover repo-relative source
// paths from object positions.
func checkUnit(fset *token.FileSet, pkgPath string, u Package, files map[string][]byte, imp types.Importer) (tpkg *types.Package, info *types.Info, asts []*ast.File, degraded string, typeErrs int) {
	for _, p := range u.Files {
		f, err := parser.ParseFile(fset, p, files[p], parser.SkipObjectResolution)
		if err != nil {
			return nil, nil, nil, "file does not fully parse: " + p, 0
		}
		asts = append(asts, f)
	}
	info = &types.Info{
		Defs: map[*ast.Ident]types.Object{},
		Uses: map[*ast.Ident]types.Object{},
	}
	conf := types.Config{
		Importer: imp,
		Error:    func(error) { typeErrs++ },
		// Unused imports are a non-issue for edge derivation and would only
		// inflate the swallowed-error count.
		DisableUnusedImportCheck: true,
	}
	defer func() {
		if r := recover(); r != nil {
			tpkg, info, asts = nil, nil, nil
			degraded = fmt.Sprintf("type-check panic: %v", r)
		}
	}()
	tpkg, _ = conf.Check(pkgPath, fset, asts, info) // error already counted via conf.Error
	if tpkg == nil {
		return nil, nil, nil, "type-check produced no package", typeErrs
	}
	return tpkg, info, asts, "", typeErrs
}

// stubImporter is the tolerant types.Importer: intra-repo import paths are
// served the real *types.Package of an already-checked unit (CheckOrder
// guarantees dependencies were checked first; degraded dependencies simply
// miss the map and fall through to a stub), everything else — stdlib,
// third-party, cgo's "C", degraded units — gets an EMPTY complete stub
// package. Selectors into a stub fail to resolve; those errors are swallowed
// by the config, and the unresolved names simply never appear in types.Info.
type stubImporter struct {
	checked map[string]*types.Package
	stubs   map[string]*types.Package
}

func newStubImporter() *stubImporter {
	return &stubImporter{checked: map[string]*types.Package{}, stubs: map[string]*types.Package{}}
}

func (m *stubImporter) Import(p string) (*types.Package, error) {
	if p == "unsafe" {
		return types.Unsafe, nil // the one package go/types requires by identity
	}
	if pkg := m.checked[p]; pkg != nil {
		return pkg, nil
	}
	if pkg := m.stubs[p]; pkg != nil {
		return pkg, nil
	}
	pkg := types.NewPackage(p, stubName(p))
	pkg.MarkComplete()
	m.stubs[p] = pkg
	return pkg, nil
}

// stubName guesses a package name for a stub from its import path (last
// segment, skipping a trailing version segment like /v2). The guess only has
// to be an identifier — an unqualifiable or wrongly-named stub merely leaves
// its selectors unresolved, which is the stub's job anyway.
func stubName(importPath string) string {
	base := path.Base(importPath)
	if isVersionSegment(base) {
		if parent := path.Base(path.Dir(importPath)); parent != "." && parent != "/" {
			base = parent
		}
	}
	out := make([]rune, 0, len(base))
	for _, r := range base {
		if r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') {
			out = append(out, r)
		} else {
			out = append(out, '_')
		}
	}
	if len(out) == 0 || ('0' <= out[0] && out[0] <= '9') {
		return "pkg"
	}
	return string(out)
}

func isVersionSegment(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	for _, r := range s[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// intentSink accumulates edge intents and merges them collect-then-construct
// (mirroring engine/link): every logical (from,to,kind) edge is constructed
// exactly once with sorted-union evidence and reasons, so the output is
// identical regardless of intent order and idempotent across passes.
type intentSink struct {
	committed map[model.NodeId]struct{}
	groups    map[edgeKey]*edgeAgg
	order     []edgeKey
	dropped   int
}

type edgeKey struct {
	from model.NodeId
	to   model.NodeId
	kind string
}

type edgeAgg struct {
	reasons  map[string]struct{}
	evidence map[string]struct{}
}

func newIntentSink(committed map[model.NodeId]struct{}) *intentSink {
	return &intentSink{committed: committed, groups: map[edgeKey]*edgeAgg{}}
}

func (s *intentSink) isCommitted(id model.NodeId) bool {
	_, ok := s.committed[id]
	return ok
}

// add records one intent whose endpoints are already known to be committed.
func (s *intentSink) add(from, to model.NodeId, kind, reason, evidence string) {
	k := edgeKey{from: from, to: to, kind: kind}
	g := s.groups[k]
	if g == nil {
		g = &edgeAgg{reasons: map[string]struct{}{}, evidence: map[string]struct{}{}}
		s.groups[k] = g
		s.order = append(s.order, k)
	}
	g.reasons[reason] = struct{}{}
	g.evidence[evidence] = struct{}{}
}

// collectFile derives calls/references intents from one type-checked file's
// top-level declarations. Attribution mirrors the extractor: every use inside
// a top-level declaration (including nested function literals) belongs to that
// declaration's symbol. Multi-name value specs pair names with values 1:1 when
// the counts match (var a, b = f(), g()) and attribute the whole right-hand
// side to every name otherwise (var a, b = twoValues()).
func (s *intentSink) collectFile(f *ast.File, info *types.Info, fset *token.FileSet) {
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			s.collectDecl(info.Defs[d.Name], fset, info, d)
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch sp := spec.(type) {
				case *ast.TypeSpec:
					s.collectDecl(info.Defs[sp.Name], fset, info, sp)
				case *ast.ValueSpec:
					if len(sp.Names) == len(sp.Values) {
						for i, nm := range sp.Names {
							var roots []ast.Node
							if sp.Type != nil {
								roots = append(roots, sp.Type)
							}
							roots = append(roots, sp.Values[i])
							s.collectDecl(info.Defs[nm], fset, info, roots...)
						}
						continue
					}
					var roots []ast.Node
					if sp.Type != nil {
						roots = append(roots, sp.Type)
					}
					for _, v := range sp.Values {
						roots = append(roots, v)
					}
					for _, nm := range sp.Names {
						s.collectDecl(info.Defs[nm], fset, info, roots...)
					}
				}
			}
		}
	}
}

// collectDecl walks the given subtrees of one top-level declaration and turns
// every use that reconstructs to a committed node into an intent. Uses whose
// object maps to NO extractor node (locals, parameters, fields, package names,
// builtins, stub-package objects) are silently not intents — no node was ever
// supposed to exist. Uses that reconstruct but miss the committed set are the
// never-fabricate drops.
func (s *intentSink) collectDecl(fromObj types.Object, fset *token.FileSet, info *types.Info, roots ...ast.Node) {
	if fromObj == nil {
		return
	}
	fromID, ok := NodeIDFor(fromObj, fset)
	if !ok {
		// The extractor emits no node for this declaration (documented blank
		// asymmetry, see qn.go) — there is no committed source to attach to.
		return
	}
	fromCommitted := s.isCommitted(fromID)
	for _, root := range roots {
		if root == nil {
			continue
		}
		calls := callPositions(root)
		ast.Inspect(root, func(n ast.Node) bool {
			id, isIdent := n.(*ast.Ident)
			if !isIdent {
				return true
			}
			obj := info.Uses[id]
			if obj == nil {
				return true
			}
			toID, ok := NodeIDFor(obj, fset)
			if !ok {
				return true
			}
			kind, reason := edgeReferences, reasonReference
			if calls[id] {
				if _, isFunc := obj.(*types.Func); isFunc {
					kind, reason = edgeCalls, reasonCall
				}
			}
			if kind == edgeReferences && toID == fromID {
				// A symbol mentioning itself is not a meaningful reference
				// (mirrors engine/link); recursive CALLS are kept.
				return true
			}
			if !fromCommitted || !s.isCommitted(toID) {
				s.dropped++
				return true
			}
			pos := fset.Position(id.Pos())
			s.add(fromID, toID, kind, reason, evidenceAt(pos))
			return true
		})
	}
}

// callPositions marks the identifiers that syntactically name a call target:
// the callee ident of f(...), the .Sel of recv.M(...) / pkg.F(...), and the
// same through parentheses and generic instantiations f[T](...). A marked
// ident still only becomes a calls intent if its object is a *types.Func — a
// type in call position is a conversion and stays a reference.
func callPositions(root ast.Node) map[*ast.Ident]bool {
	calls := map[*ast.Ident]bool{}
	mark := func(e ast.Expr) {
		switch fun := ast.Unparen(e).(type) {
		case *ast.Ident:
			calls[fun] = true
		case *ast.SelectorExpr:
			calls[fun.Sel] = true
		}
	}
	ast.Inspect(root, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := ast.Unparen(c.Fun).(type) {
		case *ast.IndexExpr:
			mark(fun.X)
		case *ast.IndexListExpr:
			mark(fun.X)
		default:
			mark(c.Fun)
		}
		return true
	})
	return calls
}

// collectImplements derives implements intents across ALL checked units: every
// package-scope named type is tested against every package-scope non-empty
// interface via types.Implements (value and pointer method sets). Aliases are
// skipped (an alias is not a distinct type; its target already carries the
// edge), as are generic types and interfaces (satisfaction is only defined per
// instantiation). Interfaces whose declared shape references unresolved stub
// types are skipped too: an invalid type is identical to every other invalid
// type, so testing against such an interface could prove satisfaction that the
// real (un-stubbed) interface does not have — the one place stub tolerance
// could over-approximate, closed off here.
func (s *intentSink) collectImplements(checked []checkedUnit, fset *token.FileSet) {
	type namedEntry struct {
		obj   *types.TypeName
		named *types.Named
		id    model.NodeId
	}
	type ifaceEntry struct {
		namedEntry
		iface *types.Interface
	}
	var allNamed []namedEntry
	var allIfaces []ifaceEntry

	// Deterministic collection: units are in check order, scope names sorted.
	for _, cu := range checked {
		scope := cu.pkg.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || tn.IsAlias() {
				continue
			}
			named, ok := tn.Type().(*types.Named)
			if !ok || named.TypeParams().Len() > 0 {
				continue
			}
			id, ok := NodeIDFor(tn, fset)
			if !ok {
				continue
			}
			e := namedEntry{obj: tn, named: named, id: id}
			allNamed = append(allNamed, e)
			if iface, ok := named.Underlying().(*types.Interface); ok && !iface.Empty() && declaredShapeResolved(iface) {
				allIfaces = append(allIfaces, ifaceEntry{namedEntry: e, iface: iface})
			}
		}
	}

	for _, t := range allNamed {
		for _, i := range allIfaces {
			if t.named.Obj() == i.named.Obj() {
				continue
			}
			if !types.Implements(t.named, i.iface) && !types.Implements(types.NewPointer(t.named), i.iface) {
				continue
			}
			if !s.isCommitted(t.id) || !s.isCommitted(i.id) {
				s.dropped++
				continue
			}
			pos := fset.Position(t.obj.Pos())
			s.add(t.id, i.id, edgeImplements, reasonImplements, evidenceAt(pos))
		}
	}
}

// declaredShapeResolved reports whether an interface's method-set-defining
// shape (embedded types and method signatures) is free of the invalid type.
// Named component types count as resolved by their NAME — types.Identical
// compares named types nominally, so an invalid type buried under a valid
// name can never make two signatures spuriously identical.
func declaredShapeResolved(t types.Type) bool {
	switch u := t.(type) {
	case *types.Basic:
		return u.Kind() != types.Invalid
	case *types.Alias:
		return declaredShapeResolved(types.Unalias(u))
	case *types.Named:
		for i := 0; i < u.TypeArgs().Len(); i++ {
			if !declaredShapeResolved(u.TypeArgs().At(i)) {
				return false
			}
		}
		return true // nominal identity: do not descend into the underlying type
	case *types.Pointer:
		return declaredShapeResolved(u.Elem())
	case *types.Slice:
		return declaredShapeResolved(u.Elem())
	case *types.Array:
		return declaredShapeResolved(u.Elem())
	case *types.Chan:
		return declaredShapeResolved(u.Elem())
	case *types.Map:
		return declaredShapeResolved(u.Key()) && declaredShapeResolved(u.Elem())
	case *types.Signature:
		return declaredShapeResolved(u.Params()) && declaredShapeResolved(u.Results())
	case *types.Tuple:
		for i := 0; i < u.Len(); i++ {
			if !declaredShapeResolved(u.At(i).Type()) {
				return false
			}
		}
		return true
	case *types.Struct:
		for i := 0; i < u.NumFields(); i++ { // anonymous struct in a signature
			if !declaredShapeResolved(u.Field(i).Type()) {
				return false
			}
		}
		return true
	case *types.Interface:
		for i := 0; i < u.NumEmbeddeds(); i++ {
			if !declaredShapeResolved(u.EmbeddedType(i)) {
				return false
			}
		}
		for i := 0; i < u.NumMethods(); i++ {
			if !declaredShapeResolved(u.Method(i).Type()) {
				return false
			}
		}
		return true
	}
	return true
}

// construct builds every accumulated logical edge exactly once as
// TierConfirmed with confidence 1.0 — the tier is pinned by construction here
// the same way engine/link pins its tiers via tierFor, so this pass can never
// emit anything weaker or stronger. Output is sorted by EdgeId.
func (s *intentSink) construct() ([]model.Edge, error) {
	edges := make([]model.Edge, 0, len(s.order))
	for _, k := range s.order {
		g := s.groups[k]
		e, err := model.NewEdge(k.from, k.to, k.kind, model.TierConfirmed, 1.0,
			joinSortedSet(g.reasons), sortedSetKeys(g.evidence))
		if err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	sort.Slice(edges, func(a, b int) bool { return edges[a].ID() < edges[b].ID() })
	return edges, nil
}

func evidenceAt(pos token.Position) string {
	return model.NormalizePath(pos.Filename) + ":" + strconv.Itoa(pos.Line)
}

func sortedSetKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func joinSortedSet(m map[string]struct{}) string {
	keys := sortedSetKeys(m)
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += "; "
		}
		out += k
	}
	return out
}
