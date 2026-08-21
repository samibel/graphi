package gobindrate

// This file: per-call-site classification against go/types' resolved-object
// map (info.Uses). The 5 AST-shape buckets that account for every call site
// in the denominator fall out of the Fun switch below. A call that resolved
// to a *types.Func in an internal package is counted separately as
// "bound_internal_func" rather than as a skip reason (it feeds the numerator
// alongside the resolver's edges).

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"sort"
	"strings"
)

// Reason is one named-reason bucket a call expression falls into. The
// vocabulary is closed (see AllReasons); a call that resolved to a
// *types.Func in an internal package returns Reason("") and the *types.Func
// in obj.
type Reason string

const (
	// AST-shape buckets — sum to the entire denominator.
	ReasonNoObjectForSelectorQualifier Reason = "no_object_for_selector_qualifier"
	ReasonNoObjectForBareIdent         Reason = "no_object_for_bare_ident"
	ReasonSelectorWithNonIdentReceiver Reason = "selector_with_non_ident_receiver"
	ReasonCallPositionOther            Reason = "call_position_other"
	ReasonGenericCallSiteSkippedByCST  Reason = "generic_call_site_skipped_by_cst"

	// Resolver-level accounting rows — NOT a portion of the denominator.
	ReasonGoTypesTypeErrors                  Reason = "go_types_type_errors"
	ReasonFileDidNotParse                    Reason = "file_did_not_parse"
	ReasonResolverDroppedIntents             Reason = "resolver_dropped_intents"
	ReasonUnitsDegradedTypeCheckPanic        Reason = "units_degraded:type-check panic"
	ReasonUnitsDegradedTypeCheckNoPackage    Reason = "units_degraded:type-check produced no package"
)

// AllReasons is the closed 10-row vocabulary in render order (resolver-level
// rows first, then AST-shape buckets, both alphabetically within their
// group). The histogram is a CLOSED vocabulary — a reason not in this list
// cannot appear in the rendered report.
func AllReasons() []Reason {
	return []Reason{
		ReasonGoTypesTypeErrors,
		ReasonNoObjectForSelectorQualifier,
		ReasonNoObjectForBareIdent,
		ReasonSelectorWithNonIdentReceiver,
		ReasonCallPositionOther,
		ReasonGenericCallSiteSkippedByCST,
		ReasonFileDidNotParse,
		ReasonResolverDroppedIntents,
		ReasonUnitsDegradedTypeCheckPanic,
		ReasonUnitsDegradedTypeCheckNoPackage,
	}
}

// classifyCall returns the named reason a call expression falls into, given
// go/types' resolved-object map. obj is non-nil iff isInternal is true, in
// which case the call resolved to a *types.Func in an internal package.
//
// Every non-bound call falls into exactly one of the 5 AST-shape buckets
// so the histogram remains exhaustive at the denominator (bound +
// AST-shape == denominator for every well-formed corpus). Local-variable
// calls, builtins, package names in call position, and function literals
// all fold into ReasonCallPositionOther.
func classifyCall(call *ast.CallExpr, info *types.Info, modules map[string]string) (reason Reason, obj *types.Func, isInternal bool) {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		objUse, ok := info.Uses[fun]
		if !ok || objUse == nil {
			return ReasonNoObjectForBareIdent, nil, false
		}
		if f, ok := objUse.(*types.Func); ok {
			// Bare ident that resolved to a *types.Func — internal
			// because it must be in the same package (cross-package
			// bare idents don't exist).
			return Reason("bound_internal_func"), f, true
		}
		// Ident resolved to something other than *types.Func: a local
		// variable, a builtin (len/make/...), a package name in call
		// position. None of these are bound; they all fold into the
		// catch-all bucket so the closed vocabulary stays exhaustive.
		return ReasonCallPositionOther, nil, false
	case *ast.SelectorExpr:
		pkgSel, ok := fun.X.(*ast.Ident)
		if !ok {
			return ReasonSelectorWithNonIdentReceiver, nil, false
		}
		objUse, ok := info.Uses[pkgSel]
		if !ok || objUse == nil {
			return ReasonNoObjectForSelectorQualifier, nil, false
		}
		if _, ok := objUse.(*types.PkgName); !ok {
			// Selector base is not a package name → receiver-method
			// call. Try to resolve the .Sel ident directly.
			objUseSel, okSel := info.Uses[fun.Sel]
			if !okSel || objUseSel == nil {
				return ReasonCallPositionOther, nil, false
			}
			f, okF := objUseSel.(*types.Func)
			if !okF {
				return ReasonCallPositionOther, nil, false
			}
			if isInternalPkg(f.Pkg(), modules) {
				return Reason("bound_internal_func"), f, true
			}
			return ReasonCallPositionOther, nil, false
		}
		// Package-name selector: pkg.Func(...)
		objUseSel, ok := info.Uses[fun.Sel]
		if !ok || objUseSel == nil {
			return ReasonCallPositionOther, nil, false
		}
		f, okF := objUseSel.(*types.Func)
		if !okF {
			return ReasonCallPositionOther, nil, false
		}
		if isInternalPkg(f.Pkg(), modules) {
			return Reason("bound_internal_func"), f, true
		}
		return ReasonCallPositionOther, nil, false
	case *ast.IndexExpr, *ast.IndexListExpr:
		// Generic instantiation: f[T](...) or pkg.F[T](...)
		return ReasonGenericCallSiteSkippedByCST, nil, false
	default:
		// Closures (func(){...}()), type conversions, channel sends, etc.
		return ReasonCallPositionOther, nil, false
	}
}

// isInternalPkg reports whether a package is the repo's own code: it appears
// in the parsed-files set and was NOT served as a stub by the importer.
func isInternalPkg(p *types.Package, modules map[string]string) bool {
	if p == nil {
		return false
	}
	if _, ok := modules[p.Path()]; ok {
		return true
	}
	return false
}

// classifyAll walks every *ast.CallExpr in the ASTs produced by
// runPerUnitTypeInfo and classifies each via classifyCall. Crucially it
// uses the SAME parsed ASTs the type-check saw — types.Info.Uses keys on
// *ast.Ident pointer identity, so a re-parse would never find anything.
//
// Files whose per-unit types.Info is absent (degraded unit) accumulate in
// Reason("unit_degraded_skip_cst_calls") — a separate, NOT-AST-shape
// bucket; the closed 10-row vocabulary never counts it. Caller is
// responsible for merging those into the resolver-level rows.
func classifyAll(parsedByFile map[string]*ast.File, typeInfoByFile map[string]*types.Info, modules map[string]string) map[Reason]int {
	hist := map[Reason]int{}
	// Iterate in deterministic name order so two runs produce the same
	// histogram (the SHA over the rendered report is the reproducibility
	// token, but identical map iteration is its precondition).
	names := make([]string, 0, len(parsedByFile))
	for name := range parsedByFile {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		f := parsedByFile[name]
		if f == nil {
			continue
		}
		info, ok := typeInfoByFile[name]
		if !ok {
			hist[Reason("unit_degraded_skip_cst_calls")] += countCalls(f)
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			reason, _, isInternal := classifyCall(call, info, modules)
			if isInternal {
				hist[Reason("bound_internal_func")]++
				return true
			}
			hist[reason]++
			return true
		})
	}
	return hist
}

// countCalls is a defensive helper: counts *ast.CallExpr nodes in one
// already-parsed AST for the unit-degraded fallback path. Mirrors
// WalkASTCounts' counting.
func countCalls(f *ast.File) int {
	n := 0
	ast.Inspect(f, func(node ast.Node) bool {
		if _, ok := node.(*ast.CallExpr); ok {
			n++
		}
		return true
	})
	return n
}

// runPerUnitTypeInfo re-runs the per-unit type-check the resolver uses, but
// exposes types.Info AND the parsed *ast.File for each checked file so
// classifyAll can walk go/types' resolved objects (info.Uses). It mirrors
// engine/typeresolve/check.go exactly enough for classification; the
// bind-and-commit pass is delegated to engine/typeresolve.Resolve.
//
// Exposing the parsed ASTs is essential: types.Info.Uses is keyed on
// *ast.Ident pointer identity, so classifyAll must use the SAME *ast.File
// the type-check used — a re-parse would walk idents that are not in the
// map.
func runPerUnitTypeInfo(files map[string][]byte) (modules map[string]string, parsedByFile map[string]*ast.File, typeInfoByFile map[string]*types.Info, degradedByReason map[string]int) {
	modules = map[string]string{}
	parsedByFile = map[string]*ast.File{}
	typeInfoByFile = map[string]*types.Info{}
	degradedByReason = map[string]int{}

	// Group files by directory.
	byDir := map[string][]string{}
	for name := range files {
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		d := pathDir(name)
		byDir[d] = append(byDir[d], name)
	}
	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	modulePath := readModulePath(files)
	imp := &miniStubImporter{checked: map[string]*types.Package{}, stubs: map[string]*types.Package{}}
	fset := token.NewFileSet()

	for _, d := range dirs {
		var pkgFiles []*ast.File
		for _, name := range byDir[d] {
			f, err := parser.ParseFile(fset, name, files[name], 0)
			if err != nil {
				continue
			}
			pkgFiles = append(pkgFiles, f)
		}
		if len(pkgFiles) == 0 {
			continue
		}
		pkgPath := unitImportPathLocal(modulePath, d)
		modules[pkgPath] = d
		info := &types.Info{
			Defs: map[*ast.Ident]types.Object{},
			Uses: map[*ast.Ident]types.Object{},
		}
		typeErrs := 0
		conf := types.Config{
			Importer:                 imp,
			Error:                    func(error) { typeErrs++ },
			DisableUnusedImportCheck: true,
		}
		var tpkg *types.Package
		func() {
			defer func() {
				if r := recover(); r != nil {
					degradedByReason[fmt.Sprintf("type-check panic: %v", r)]++
				}
			}()
			tpkg, _ = conf.Check(pkgPath, fset, pkgFiles, info)
		}()
		_ = typeErrs
		if tpkg == nil {
			degradedByReason["type-check produced no package"]++
			continue
		}
		imp.checked[pkgPath] = tpkg
		for _, f := range pkgFiles {
			pos := f.Pos()
			fileName := fset.File(pos).Name()
			parsedByFile[fileName] = f
			typeInfoByFile[fileName] = info
		}
	}
	return modules, parsedByFile, typeInfoByFile, degradedByReason
}

func pathDir(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return "."
}

func readModulePath(files map[string][]byte) string {
	mod, ok := files["go.mod"]
	if !ok {
		return ""
	}
	for _, line := range strings.Split(string(mod), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

func unitImportPathLocal(modulePath, dir string) string {
	switch {
	case modulePath == "":
		return dir
	case dir == ".":
		return modulePath
	default:
		return modulePath + "/" + dir
	}
}

type miniStubImporter struct {
	checked map[string]*types.Package
	stubs   map[string]*types.Package
}

func (m *miniStubImporter) Import(p string) (*types.Package, error) {
	if p == "unsafe" {
		return types.Unsafe, nil
	}
	if pkg := m.checked[p]; pkg != nil {
		return pkg, nil
	}
	if pkg := m.stubs[p]; pkg != nil {
		return pkg, nil
	}
	pkg := types.NewPackage(p, stubNameLocal(p))
	pkg.MarkComplete()
	m.stubs[p] = pkg
	return pkg, nil
}

func stubNameLocal(p string) string {
	base := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		base = p[i+1:]
	}
	out := strings.Builder{}
	for _, r := range base {
		switch {
		case r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9'):
			out.WriteRune(r)
		default:
			out.WriteByte('_')
		}
	}
	if out.Len() == 0 || (out.String()[0] >= '0' && out.String()[0] <= '9') {
		return "pkg"
	}
	return out.String()
}
