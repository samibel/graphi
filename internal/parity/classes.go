package parity

import (
	"fmt"
	"go/ast"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ClassesPath is the machine-readable class table SW-156 landed and SW-157
// bound the hermetic harness to. THIS harness binds to the same file, so the
// real-repo matrix and the hermetic matrix can never disagree about which
// classes exist.
//
// Note the boundary this respects, and why it is not negotiable:
// internal/evalreport.RequiredChangeClasses feeds cmd/eval/changeseq.go:36-39
// changeSequenceCycle, so declaring a class THERE would reshape the freshness
// and update instrument cmd/eval measures with. This harness keeps its own
// binding, to this YAML, and never touches internal/evalreport.
const ClassesPath = "docs/rc/parity-classes.yaml"

// WHY gopkg.in/yaml.v3 IS USED HERE, recorded so nobody "corrects" it back to a
// hand-rolled parser. internal/coverage/matrix.go:63-64 declines a general YAML
// dependency for docs/coverage-matrix.yaml, and that rationale is about THE
// SHIPPED BINARY staying lean and CGo-free. It does not reach this package:
// internal/parity is linked by neither cmd/graphi nor cmd/eval — the same
// standing this package needs for the no-product-byte boundary anyway — so the
// import cannot fatten the shipped artifact. yaml.v3 is already a real,
// first-party dependency used for exactly this job (engine/scenario/scenario.go
// parses the checked-in hero scenarios with it).

// ClassRow mirrors one row of docs/rc/parity-classes.yaml. Field names track the
// YAML keys exactly; that file's header documents each one.
type ClassRow struct {
	ID          string `yaml:"id"`
	Kind        string `yaml:"kind"`
	Label       string `yaml:"label"`
	Verdict     string `yaml:"verdict"`
	TestFile    string `yaml:"test_file"`
	TestName    string `yaml:"test_name"`
	HarnessRow  string `yaml:"harness_row"`
	KnownDefect string `yaml:"known_defect"`
	DeferredTo  string `yaml:"deferred_to"`
	Owner       string `yaml:"owner"`
}

const (
	kindChangeClass    = "change_class"
	kindCrashCondition = "crash_condition"
	harnessDeferred    = "deferred"
)

// LoadClasses parses the declared class table.
func LoadClasses(p string) ([]ClassRow, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("parity: read %s: %w", p, err)
	}
	var doc struct {
		ParityClasses []ClassRow `yaml:"parity_classes"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parity: parse %s: %w", p, err)
	}
	if len(doc.ParityClasses) == 0 {
		return nil, fmt.Errorf("parity: %s declared no parity_classes rows", p)
	}
	return doc.ParityClasses, nil
}

// CountChangeClasses returns the number of kind: "change_class" rows. The
// report's completeness check is derived from THIS, never from len(rows) —
// counting crash conditions among the change classes is the exact conflation
// that turned FR-7's 15 into backlog.md:55's "16".
func CountChangeClasses(rows []ClassRow) int { return countKind(rows, kindChangeClass) }

// CountCrashConditions returns the number of kind: "crash_condition" rows. It is
// deliberately a SECOND counter rather than a flag on the first: the report's
// completeness check is over each kind separately, so that a crash condition can
// never be absorbed into FR-7's fifteen and a change class can never be absorbed
// into the crash conditions.
func CountCrashConditions(rows []ClassRow) int { return countKind(rows, kindCrashCondition) }

func countKind(rows []ClassRow, kind string) int {
	n := 0
	for _, r := range rows {
		if r.Kind == kind {
			n++
		}
	}
	return n
}

// FileOp kinds.
const (
	opWrite     = "write"
	opDelete    = "delete"
	opRenameDir = "renamedir"
)

// FileOp is one real filesystem change applied to a real clone.
type FileOp struct {
	Kind    string
	Path    string // repo-relative, slash-separated
	Data    []byte
	NewPath string // opRenameDir only
}

// Mutation is one change class expressed as real edits to real source, plus the
// human description that goes into the report so the edit can be re-applied by
// hand from the record alone.
type Mutation struct {
	Desc string
	Ops  []FileOp
}

// Planner locates a real edit target in a real clone and returns the mutation.
// It returns errNoTarget when the repository does not exhibit the structure the
// class needs — the signal repository selection walks on.
type Planner func(m *RepoModel) (*Mutation, error)

// ClassSpec binds a declared class id to the real-source edit that exercises it.
type ClassSpec struct {
	ID string
	// ManifestProperty, when set, PINS the repository to the one the corpus
	// manifest's stratification block attributes the property to. Those two
	// classes are not free to roam: the manifest is the authority on which
	// repository carries build tags and which carries generated code, and a
	// silent fallback to another repository would make the row a claim about a
	// property that repository was never selected for.
	ManifestProperty string
	Plan             Planner
	// Note is carried into the report row, stating what the row does and does
	// not prove.
	Note string
}

// specs is the real-repo class table. Every id here must appear in
// docs/rc/parity-classes.yaml (the PHANTOM direction) and every declared,
// non-deferred change class must appear here (the MISSING direction); both are
// asserted by TestClassTable_BindsToDeclaredMatrix.
func specs() []ClassSpec {
	return []ClassSpec{
		{ID: "add_file", Plan: planAddFile},
		{ID: "modify_file", Plan: planModifyFile},
		{ID: "delete_file", Plan: planDeleteFile,
			Note: "Deletes a file declaring a symbol another package calls through an " +
				"intra-module import — the PARITY-001 precondition. A FAIL here is the " +
				"EXPECTED, tracked result, not a harness fault."},
		{ID: "rename_symbol", Plan: planRenameSymbol},
		{ID: "move_symbol", Plan: planMoveSymbol},
		{ID: "rename_package", Plan: planRenamePackage},
		{ID: "add_call", Plan: planAddCall},
		{ID: "remove_call", Plan: planRemoveCall,
			Note: "The hard form: the caller keeps its identity and only drops the call, so " +
				"no node deletion can cascade the edge away and only the stale-edge sweep " +
				"can converge it."},
		{ID: "change_interface", Plan: planChangeInterface},
		{ID: "add_implementation", Plan: planAddImplementation},
		{ID: "remove_implementation", Plan: planRemoveImplementation},
		{ID: "change_build_tag", ManifestProperty: "build tags", Plan: planChangeBuildTag,
			Note: "DEGENERATE BY CONSTRUCTION, and this row says so rather than implying " +
				"more. No build-constraint evaluation exists anywhere in graphi " +
				"(engine/typeresolve/pkggraph.go:85 groups by directory and package clause " +
				"and never inspects //go:build; doc.go:24 and check.go:20 forbid the " +
				"go/packages machinery that would provide it). To graphi a build-tag edit " +
				"is a comment-line content change, so this row proves PARITY OVER THE " +
				"CHANGE and NOTHING about build-tag semantics."},
		{ID: "replace_generated_file", ManifestProperty: "generated code", Plan: planReplaceGeneratedFile,
			Note: "Runs on the manifest's declared generated-code repository, which is also " +
				"its declared multiple-go.mod repository, so the row additionally exercises " +
				"a multi-module tree (sub-modules are excluded from the edit model by " +
				"construction — only the root module is touched)."},
		{ID: "change_external_import", Plan: planChangeExternalImport},
		{ID: "change_colliding_package_dir", Plan: planCollidingPackageDir,
			Note: "Adds a NEW directory whose package clause collides with an existing " +
				"package's, and that nothing imports — the PARITY-002 precondition. " +
				"`imports` edges fan out over every directory sharing the clause " +
				"(engine/link/index.go packageFileNodes keys on path.Base), while the " +
				"incremental cascade is directory-local, so importers of the ORIGINAL " +
				"directory are never re-linked. A FAIL here is the EXPECTED, tracked " +
				"result, not a harness fault."},
	}
}

// SpecByID indexes the table.
func SpecByID() map[string]ClassSpec {
	out := map[string]ClassSpec{}
	for _, s := range specs() {
		out[s.ID] = s
	}
	return out
}

// ---------------------------------------------------------------------------
// The planners. Every one is DETERMINISTIC: targets are chosen by sorted path
// and source order, never by map iteration. A harness that picked a different
// target on two dispatches would manufacture exactly the run-to-run
// disagreement AC-17 exists to detect.
// ---------------------------------------------------------------------------

const marker = "graphiParity"

// planCollidingPackageDir adds a NEW directory whose package clause collides
// with the primary package's, and which nothing imports — the PARITY-002
// precondition.
//
// Why this is a legitimate real-repo edit and not a manufactured defect: two
// directories declaring the same package clause is an ordinary Go shape (gin
// alone ships four `package json` directories behind build tags), and the edit
// ADDS a self-contained, syntactically valid file. It creates no dangling
// reference — the near-miss the record warns about, where an incomplete
// `rename_package` left an importer pointing at a package that no longer
// existed and the harness then measured its own breakage.
//
// The divergence it exposes belongs to the product: `imports` edges fan out
// over EVERY directory sharing the clause (engine/link/index.go
// packageFileNodes keys on path.Base(importPath)), so adding the colliding
// directory changes the correct fan-out of every importer of the ORIGINAL
// directory — while dependentsOf, being directory-local, re-links none of them.
func planCollidingPackageDir(m *RepoModel) (*Mutation, error) {
	pkg := m.primaryPkg()
	if pkg == nil {
		return nil, errNoTarget
	}
	// A fixed, deterministic location that cannot already exist in a pinned
	// clone, and that no file imports.
	dir := path.Join("graphi_parity_collide", pkg.Name)
	rel := strings.TrimPrefix(path.Join(dir, "collide.go"), "./")
	if fileExistsIn(pkg, rel) {
		return nil, errNoTarget
	}
	body := fmt.Sprintf("package %s\n\n// %sCollide is added by the real-repo parity matrix to create a\n"+
		"// package-clause collision with %s. Nothing imports this directory.\nfunc %sCollide() string {\n\treturn %q\n}\n",
		pkg.Name, marker, pkg.Dir, marker, "parity-002")
	return &Mutation{
		Desc: fmt.Sprintf("add directory %s declaring package %s — colliding with %s, imported by nobody (the PARITY-002 precondition)",
			dir, pkg.Name, pkg.Dir),
		Ops: []FileOp{{Kind: opWrite, Path: rel, Data: []byte(body)}},
	}, nil
}

// planAddFile adds a new file in a new-to-the-graph position of an existing
// package.
func planAddFile(m *RepoModel) (*Mutation, error) {
	pkg := m.primaryPkg()
	if pkg == nil {
		return nil, errNoTarget
	}
	rel := path.Join(pkg.Dir, "graphi_parity_add_file.go")
	rel = strings.TrimPrefix(rel, "./")
	if fileExistsIn(pkg, rel) {
		return nil, errNoTarget
	}
	body := fmt.Sprintf("package %s\n\n// %sAdded is added by the SW-144 real-repo parity matrix.\nfunc %sAdded() string {\n\treturn %q\n}\n",
		pkg.Name, marker, marker, "sw-144")
	return &Mutation{
		Desc: fmt.Sprintf("add file %s declaring func %sAdded in package %s", rel, marker, pkg.Name),
		Ops:  []FileOp{{Kind: opWrite, Path: rel, Data: []byte(body)}},
	}, nil
}

// planModifyFile rewrites an already-indexed file so its node set grows while
// its existing nodes keep identity.
func planModifyFile(m *RepoModel) (*Mutation, error) {
	pkg := m.primaryPkg()
	if pkg == nil {
		return nil, errNoTarget
	}
	for _, gf := range pkg.Files {
		if len(topFuncs(gf)) == 0 {
			continue
		}
		add := fmt.Sprintf("\n// %sModified is appended by the SW-144 real-repo parity matrix.\nfunc %sModified() int {\n\treturn 1\n}\n", marker, marker)
		return &Mutation{
			Desc: fmt.Sprintf("modify %s: append func %sModified, leaving every existing declaration's identity intact", gf.Rel, marker),
			Ops:  []FileOp{{Kind: opWrite, Path: gf.Rel, Data: append(append([]byte{}, gf.Src...), add...)}},
		}, nil
	}
	return nil, errNoTarget
}

// planDeleteFile deletes the SMALLEST file that declares a symbol another
// in-module package references through an import.
//
// That precondition is chosen deliberately: it is the PARITY-001 shape, and
// PARITY-001 is a tracked, filed, scheduled product defect (v0.7.2 batch item
// 3), NOT something this story fixes. Preferring the smallest such file keeps
// the blast radius readable when the FAIL is published.
func planDeleteFile(m *RepoModel) (*Mutation, error) {
	referenced := crossPackageRefs(m)
	var best *GoFile
	bestPkg := ""
	bestScore := 0
	for _, pkg := range m.Pkgs {
		refs := referenced[pkg.ImportPath]
		if len(refs) == 0 {
			continue
		}
		for _, gf := range pkg.Files {
			hit := ""
			for name, exported := range declaredNames(gf) {
				if exported && refs[name] {
					if hit == "" || name < hit {
						hit = name
					}
				}
			}
			if hit == "" {
				continue
			}
			// Never delete the only file of a package AND never delete a file
			// with no siblings if that would empty the module.
			if len(pkg.Files) < 2 && len(m.Pkgs) < 2 {
				continue
			}
			score := topLevelDeclCount(gf)
			if best == nil || score < bestScore || (score == bestScore && gf.Rel < best.Rel) {
				best, bestScore, bestPkg = gf, score, hit
			}
		}
	}
	if best == nil {
		return nil, errNoTarget
	}
	return &Mutation{
		Desc: fmt.Sprintf("delete %s (%d top-level declarations), which declares %q — a symbol another in-module package calls through an intra-module import (the PARITY-001 precondition)",
			best.Rel, bestScore, bestPkg),
		Ops: []FileOp{{Kind: opDelete, Path: best.Rel}},
	}, nil
}

// crossPackageRefs maps an import path to the set of its exported names that
// another package of the same module actually references.
func crossPackageRefs(m *RepoModel) map[string]map[string]bool {
	byImportPath := map[string]*GoPkg{}
	for _, p := range m.Pkgs {
		byImportPath[p.ImportPath] = p
	}
	out := map[string]map[string]bool{}
	for _, pkg := range m.Pkgs {
		for _, gf := range pkg.Files {
			aliases := importAliases(gf)
			sels := selectorRefs(gf)
			for qual, names := range sels {
				ip, ok := aliases[qual]
				if !ok {
					continue
				}
				target, ok := byImportPath[ip]
				if !ok || target.Dir == pkg.Dir {
					continue
				}
				if out[ip] == nil {
					out[ip] = map[string]bool{}
				}
				for n := range names {
					out[ip][n] = true
				}
			}
		}
	}
	return out
}

// planRenameSymbol renames an unexported package-level function and every one
// of its references, in one change set.
//
// Unexported is deliberate: it bounds the rewrite to a single package, so the
// mutation stays reviewable, while still exercising the identity change and the
// edge re-pointing the class is about.
func planRenameSymbol(m *RepoModel) (*Mutation, error) {
	for _, pkg := range m.Pkgs {
		for _, gf := range pkg.Files {
			for _, fd := range topFuncs(gf) {
				name := fd.Name.Name
				if fd.Name.IsExported() || name == "init" || name == "main" || strings.HasPrefix(name, marker) {
					continue
				}
				total := 0
				var ops []FileOp
				for _, sib := range pkg.Files {
					occ := identOccurrences(sib, name)
					if len(occ) == 0 {
						continue
					}
					total += len(occ)
					ops = append(ops, FileOp{Kind: opWrite, Path: sib.Rel,
						Data: rewriteRanges(sib.Src, occ, name+"Renamed")})
				}
				// Require at least one reference beyond the declaration itself,
				// so the row exercises edge re-pointing and not only a node
				// identity change.
				if total < 2 {
					continue
				}
				return &Mutation{
					Desc: fmt.Sprintf("rename package-level func %s -> %sRenamed in package %s, rewriting all %d occurrences across %d file(s) in one change set",
						name, name, pkg.Name, total, len(ops)),
					Ops: ops,
				}, nil
			}
		}
	}
	return nil, errNoTarget
}

// planMoveSymbol relocates a declaration between files of the SAME package.
//
// This is the sub-shape with the interesting hazard: a Go node's qualified name
// is package-qualified, so a same-package move PRESERVES the NodeId while
// changing source_path and line — both of which the node wire carries. Two files
// then claim one NodeId inside a single change set, and the source file's
// per-file stale-node purge runs against a node the destination file now owns.
//
// Only functions whose bodies reference no imported qualifier are eligible, so
// the move never has to carry imports with it and the mutation stays a pure
// relocation.
func planMoveSymbol(m *RepoModel) (*Mutation, error) {
	for _, pkg := range m.Pkgs {
		if len(pkg.Files) < 1 {
			continue
		}
		for _, gf := range pkg.Files {
			aliases := importAliases(gf)
			for _, fd := range topFuncs(gf) {
				if fd.Body == nil || strings.HasPrefix(fd.Name.Name, marker) {
					continue
				}
				if usesImports(gf, fd, aliases) {
					continue
				}
				text := gf.text(fd)
				start := declStart(gf, fd)
				_, end := gf.offsets(fd)
				trimmed := append(append([]byte{}, gf.Src[:start]...), gf.Src[end:]...)
				dest := path.Join(pkg.Dir, "graphi_parity_moved.go")
				dest = strings.TrimPrefix(dest, "./")
				body := fmt.Sprintf("package %s\n\n%s\n", pkg.Name, text)
				return &Mutation{
					Desc: fmt.Sprintf("move package-level func %s from %s to the new file %s in the SAME package %s — the NodeId is preserved while source_path and line change, so two files claim one NodeId inside one change set",
						fd.Name.Name, gf.Rel, dest, pkg.Name),
					Ops: []FileOp{
						{Kind: opWrite, Path: gf.Rel, Data: trimmed},
						{Kind: opWrite, Path: dest, Data: []byte(body)},
					},
				}, nil
			}
		}
	}
	return nil, errNoTarget
}

// usesImports reports whether a declaration references any imported qualifier.
func usesImports(gf *GoFile, fd *ast.FuncDecl, aliases map[string]string) bool {
	used := false
	ast.Inspect(fd, func(n ast.Node) bool {
		se, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := se.X.(*ast.Ident); ok {
			if _, isImport := aliases[id.Name]; isImport {
				used = true
			}
		}
		return true
	})
	return used
}

// planRenamePackage changes a package's DIRECTORY and its package CLAUSE
// together, and rewrites every in-module importer's import path and qualifier —
// all in one change set.
//
// The package with the fewest in-module importers is chosen so the rewrite stays
// small and reviewable; the class is exercised either way, because renaming the
// directory changes the import path and therefore every qualified name the
// package declares.
func planRenamePackage(m *RepoModel) (*Mutation, error) {
	importers := map[string][]*GoFile{}
	for _, pkg := range m.Pkgs {
		for _, gf := range pkg.Files {
			for _, ip := range importAliases(gf) {
				importers[ip] = append(importers[ip], gf)
			}
		}
	}
	var target *GoPkg
	for _, pkg := range m.Pkgs {
		if pkg.Dir == "." || len(pkg.Files) == 0 {
			continue // never rename the module root
		}
		if target == nil || len(importers[pkg.ImportPath]) < len(importers[target.ImportPath]) {
			target = pkg
		}
	}
	if target == nil {
		return nil, errNoTarget
	}
	oldBase := path.Base(target.Dir)
	newBase := oldBase + "renamed"
	newDir := path.Join(path.Dir(target.Dir), newBase)
	oldName, newName := target.Name, target.Name+"renamed"
	newImportPath := m.Module + "/" + newDir

	ops := []FileOp{{Kind: opRenameDir, Path: target.Dir, NewPath: newDir}}

	// Rewrite the package clause of every file in the renamed directory —
	// INCLUDING ITS _test.go FILES.
	//
	// The test files matter and are easy to forget. graphi indexes _test.go
	// sources, and the discovery model deliberately does not (it locates edit
	// targets, and a test file is not one). Leaving them behind would produce a
	// tree whose tests still declare the OLD package and import the OLD path —
	// an INCOMPLETE rename. Parity over an incomplete rename is still a
	// legitimate question, but it is a DIFFERENT question, and a divergence
	// found there could not be attributed to the rename rather than to the
	// dangling references the harness itself created. So the rename is made
	// complete, in one change set, and the class is exercised on a tree a human
	// could plausibly have produced.
	renamed, err := rewriteDirPackageClause(m.Root, target.Dir, newDir, oldName, newName, target.ImportPath, newImportPath)
	if err != nil {
		return nil, err
	}
	ops = append(ops, renamed...)

	// Rewrite each in-module importer's path string and qualifier.
	n := 0
	seen := map[string]bool{}
	for _, gf := range importers[target.ImportPath] {
		if strings.HasPrefix(gf.Rel, target.Dir+"/") || path.Dir(gf.Rel) == target.Dir || seen[gf.Rel] {
			continue
		}
		seen[gf.Rel] = true
		src := gf.Src
		for _, im := range gf.AST.Imports {
			if im.Path.Value != `"`+target.ImportPath+`"` {
				continue
			}
			s, e := gf.offsets(im.Path)
			src = append(append(append([]byte{}, src[:s]...), []byte(`"`+newImportPath+`"`)...), src[e:]...)
		}
		// Qualifier rewrite is only needed for UNALIASED imports.
		if occ := qualifierOccurrences(gf, oldName); len(occ) > 0 {
			src = rewriteRanges(src, occ, newName)
		}
		ops = append(ops, FileOp{Kind: opWrite, Path: gf.Rel, Data: src})
		n++
	}
	return &Mutation{
		Desc: fmt.Sprintf("rename package %s: directory %s -> %s AND package clause %s -> %s across %d file(s) including _test.go files, rewriting %d in-module importer file(s) in one change set (new import path %s)",
			oldName, target.Dir, newDir, oldName, newName, len(renamed), n, newImportPath),
		Ops: ops,
	}, nil
}

// rewriteDirPackageClause rewrites every .go file of a renamed directory — test
// files included — so the rename is COMPLETE: the package clause moves (both the
// package itself and its `_test` external-test sibling), and any self-import of
// the old path is re-pointed.
func rewriteDirPackageClause(root, oldDir, newDir, oldName, newName, oldPath, newPath string) ([]FileOp, error) {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(oldDir)))
	if err != nil {
		return nil, fmt.Errorf("parity: read package dir %s: %w", oldDir, err)
	}
	var ops []FileOp
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join(root, filepath.FromSlash(oldDir), e.Name()))
		if rerr != nil {
			return nil, rerr
		}
		s := string(raw)
		// The external-test package first, so "package doc" does not match the
		// "package doc_test" prefix and leave "_test" stranded.
		s = strings.Replace(s, "package "+oldName+"_test\n", "package "+newName+"_test\n", 1)
		s = strings.Replace(s, "package "+oldName+"\n", "package "+newName+"\n", 1)
		if strings.Contains(s, `"`+oldPath+`"`) {
			s = strings.ReplaceAll(s, `"`+oldPath+`"`, `"`+newPath+`"`)
			s = replaceQualifier(s, oldName, newName)
		}
		ops = append(ops, FileOp{Kind: opWrite, Path: path.Join(newDir, e.Name()), Data: []byte(s)})
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Path < ops[j].Path })
	return ops, nil
}

// replaceQualifier rewrites `old.` to `new.` at identifier boundaries. It is a
// TEXTUAL pass, used only on files the AST model does not carry (test files),
// and only on files that were confirmed to import the renamed path — so the
// qualifier is known to be a package name there.
func replaceQualifier(s, oldName, newName string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], oldName+".") && !isIdentByte(prevByte(s, i)) {
			b.WriteString(newName + ".")
			i += len(oldName) + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func prevByte(s string, i int) byte {
	if i == 0 {
		return 0
	}
	return s[i-1]
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '.' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// qualifierOccurrences returns the ranges of `name` used as the X half of a
// selector — i.e. as a package qualifier.
func qualifierOccurrences(gf *GoFile, name string) [][2]int {
	var out [][2]int
	ast.Inspect(gf.AST, func(n ast.Node) bool {
		se, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := se.X.(*ast.Ident); ok && id.Name == name {
			s, e := gf.offsets(id)
			out = append(out, [2]int{s, e})
		}
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// planAddCall introduces a new call site between two symbols of a package. The
// callee must be callable with no arguments (an empty or purely variadic
// parameter list) so the synthesized call is valid Go without inventing values.
func planAddCall(m *RepoModel) (*Mutation, error) {
	for _, pkg := range m.Pkgs {
		var callee string
		for _, gf := range pkg.Files {
			for _, fd := range topFuncs(gf) {
				if fd.Name.Name == "init" || fd.Name.Name == "main" || strings.HasPrefix(fd.Name.Name, marker) {
					continue
				}
				if callableWithNoArgs(fd) {
					callee = fd.Name.Name
					break
				}
			}
			if callee != "" {
				break
			}
		}
		if callee == "" {
			continue
		}
		host := pkg.Files[0]
		add := fmt.Sprintf("\n// %sCaller is appended by the SW-144 real-repo parity matrix; it introduces\n// a new call edge to the already-indexed %s.\nfunc %sCaller() {\n\t%s()\n}\n",
			marker, callee, marker, callee)
		return &Mutation{
			Desc: fmt.Sprintf("add a new call edge in %s: new func %sCaller calls the already-indexed package-level func %s", host.Rel, marker, callee),
			Ops:  []FileOp{{Kind: opWrite, Path: host.Rel, Data: append(append([]byte{}, host.Src...), add...)}},
		}, nil
	}
	return nil, errNoTarget
}

// callableWithNoArgs reports whether fd can be called as `fd()`.
func callableWithNoArgs(fd *ast.FuncDecl) bool {
	if fd.Type.TypeParams != nil && len(fd.Type.TypeParams.List) > 0 {
		return false // generic: inference from no arguments is not guaranteed
	}
	p := fd.Type.Params
	if p == nil || len(p.List) == 0 {
		return true
	}
	if len(p.List) == 1 && len(p.List[0].Names) <= 1 {
		_, variadic := p.List[0].Type.(*ast.Ellipsis)
		return variadic
	}
	return false
}

// planRemoveCall removes an existing intra-package call statement while the
// CALLER KEEPS ITS IDENTITY.
//
// That is the hard form on purpose: because the caller node is never deleted,
// no node-deletion cascade can take the edge with it, and only the stale-edge
// sweep can converge the graph. It doubles as the FR-7 "no stale linker edges"
// proof at row level.
func planRemoveCall(m *RepoModel) (*Mutation, error) {
	for _, pkg := range m.Pkgs {
		local := map[string]bool{}
		for _, gf := range pkg.Files {
			for _, fd := range topFuncs(gf) {
				local[fd.Name.Name] = true
			}
		}
		for _, gf := range pkg.Files {
			var found ast.Node
			var calleeName string
			ast.Inspect(gf.AST, func(n ast.Node) bool {
				if found != nil {
					return false
				}
				blk, ok := n.(*ast.BlockStmt)
				if !ok || len(blk.List) < 2 {
					return true
				}
				for _, st := range blk.List {
					es, ok := st.(*ast.ExprStmt)
					if !ok {
						continue
					}
					call, ok := es.X.(*ast.CallExpr)
					if !ok {
						continue
					}
					id, ok := call.Fun.(*ast.Ident)
					if !ok || !local[id.Name] {
						continue
					}
					found, calleeName = es, id.Name
					return false
				}
				return true
			})
			if found == nil {
				continue
			}
			return &Mutation{
				Desc: fmt.Sprintf("remove the call to %s in %s while the enclosing function keeps its identity — no node is deleted, so only the stale-edge sweep can converge the graph",
					calleeName, gf.Rel),
				Ops: []FileOp{{Kind: opWrite, Path: gf.Rel, Data: gf.cut(found)}},
			}, nil
		}
	}
	return nil, errNoTarget
}

// planChangeInterface adds a method to an existing named interface, so the
// implements/inherits edge set must converge.
func planChangeInterface(m *RepoModel) (*Mutation, error) {
	for _, pkg := range m.Pkgs {
		for _, gf := range pkg.Files {
			for _, ts := range interfaceSpecs(gf) {
				it := ts.Type.(*ast.InterfaceType)
				if it.Methods == nil || len(it.Methods.List) == 0 {
					continue
				}
				// Insert immediately before the interface's closing brace.
				closing := gf.Fset.Position(it.End()).Offset - 1
				if closing <= 0 || closing > len(gf.Src) {
					continue
				}
				ins := fmt.Sprintf("\t%sAdded() string\n", marker)
				out := append([]byte{}, gf.Src[:closing]...)
				out = append(out, ins...)
				out = append(out, gf.Src[closing:]...)
				return &Mutation{
					Desc: fmt.Sprintf("change interface %s.%s in %s: add method %sAdded() string, so every implements edge to it must be re-derived",
						pkg.Name, ts.Name.Name, gf.Rel, marker),
					Ops: []FileOp{{Kind: opWrite, Path: gf.Rel, Data: out}},
				}, nil
			}
		}
	}
	return nil, errNoTarget
}

// planAddImplementation adds a concrete type satisfying an existing single-method
// interface by METHOD SET — no embedding involved, so the row cannot be
// satisfied by the syntactic embed path alone.
func planAddImplementation(m *RepoModel) (*Mutation, error) {
	for _, pkg := range m.Pkgs {
		for _, gf := range pkg.Files {
			for _, ts := range interfaceSpecs(gf) {
				it := ts.Type.(*ast.InterfaceType)
				if it.Methods == nil || len(it.Methods.List) != 1 {
					continue
				}
				fld := it.Methods.List[0]
				if len(fld.Names) != 1 {
					continue // an embedded interface, not a method
				}
				ft, ok := fld.Type.(*ast.FuncType)
				if !ok {
					continue
				}
				sig := gf.text(ft) // "(a int) error" — includes the parameter list
				sig = strings.TrimPrefix(sig, "func")
				dest := path.Join(pkg.Dir, "graphi_parity_impl.go")
				dest = strings.TrimPrefix(dest, "./")
				body := fmt.Sprintf(
					"package %s\n\n// %sImpl satisfies %s by METHOD SET, with no embedding, so this row\n// exercises method-set satisfaction rather than the syntactic embed path.\ntype %sImpl struct{}\n\nfunc (%sImpl) %s%s {\n\tpanic(%q)\n}\n",
					pkg.Name, marker, ts.Name.Name, marker, marker, fld.Names[0].Name, sig, "sw-144")
				return &Mutation{
					Desc: fmt.Sprintf("add implementation: new type %sImpl in %s satisfies interface %s.%s by method set (method %s), with no embedding",
						marker, dest, pkg.Name, ts.Name.Name, fld.Names[0].Name),
					Ops: []FileOp{{Kind: opWrite, Path: dest, Data: []byte(body)}},
				}, nil
			}
		}
	}
	return nil, errNoTarget
}

// planRemoveImplementation deletes a method that makes a concrete type satisfy
// an interface declared in the same package, so the implements edge must vanish.
// Removal is the direction that can leave a stale edge behind.
func planRemoveImplementation(m *RepoModel) (*Mutation, error) {
	for _, pkg := range m.Pkgs {
		ifaceMethods := map[string]string{} // method name -> interface name
		for _, gf := range pkg.Files {
			for _, ts := range interfaceSpecs(gf) {
				it := ts.Type.(*ast.InterfaceType)
				if it.Methods == nil {
					continue
				}
				for _, fld := range it.Methods.List {
					for _, n := range fld.Names {
						ifaceMethods[n.Name] = ts.Name.Name
					}
				}
			}
		}
		if len(ifaceMethods) == 0 {
			continue
		}
		for _, gf := range pkg.Files {
			ms := methodDecls(gf)
			// Count methods per receiver so the type is not emptied wholesale:
			// the row must remove an IMPLEMENTATION, not the type.
			perRecv := map[string]int{}
			for _, md := range ms {
				perRecv[receiverName(gf, md)]++
			}
			for _, md := range ms {
				iface, ok := ifaceMethods[md.Name.Name]
				if !ok {
					continue
				}
				recv := receiverName(gf, md)
				if perRecv[recv] < 2 {
					continue // removing it would leave the type with no methods
				}
				start := declStart(gf, md)
				_, end := gf.offsets(md)
				out := append(append([]byte{}, gf.Src[:start]...), gf.Src[end:]...)
				return &Mutation{
					Desc: fmt.Sprintf("remove implementation: delete method %s.%s in %s, which is the method interface %s.%s requires — the type survives with its other %d method(s), so the implements edge must vanish without the node being deleted",
						recv, md.Name.Name, gf.Rel, pkg.Name, iface, perRecv[recv]-1),
					Ops: []FileOp{{Kind: opWrite, Path: gf.Rel, Data: out}},
				}, nil
			}
		}
	}
	return nil, errNoTarget
}

// receiverName renders a method's receiver type name.
func receiverName(gf *GoFile, md *ast.FuncDecl) string {
	if md.Recv == nil || len(md.Recv.List) == 0 {
		return ""
	}
	t := md.Recv.List[0].Type
	if st, ok := t.(*ast.StarExpr); ok {
		t = st.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return gf.text(t)
}

// planChangeBuildTag edits a //go:build constraint.
//
// The row is DEGENERATE and its ClassSpec note says so: graphi evaluates no
// build constraints, so the only thing this edit can move is the content of a
// comment line and the LINE NUMBERS of the declarations below it. That is
// exactly what makes it a non-vacuous parity target and a vacuous semantic one.
func planChangeBuildTag(m *RepoModel) (*Mutation, error) {
	for _, pkg := range m.Pkgs {
		for _, gf := range pkg.Files {
			idx, line := buildTagLine(gf.Src)
			if idx < 0 {
				continue
			}
			if strings.Contains(line, "graphiparity") {
				continue
			}
			newLine := line + " && !graphiparity"
			return &Mutation{
				Desc: fmt.Sprintf("change the build constraint in %s line %d: %q -> %q. graphi evaluates no build constraints, so this proves parity over the change and NOTHING about build-tag semantics",
					gf.Rel, idx+1, strings.TrimSpace(line), strings.TrimSpace(newLine)),
				Ops: []FileOp{{Kind: opWrite, Path: gf.Rel, Data: replaceLine(gf.Src, idx, newLine)}},
			}, nil
		}
	}
	return nil, errNoTarget
}

// planReplaceGeneratedFile replaces a file carrying the "Code generated … DO NOT
// EDIT." marker wholesale with a regenerated body: one declaration removed, two
// added. Within the ingest/graph path this class has no special-casing at all,
// which makes it a large-diff, high-symbol-count stress on the ordinary modify
// path rather than a degenerate row.
func planReplaceGeneratedFile(m *RepoModel) (*Mutation, error) {
	for _, pkg := range m.Pkgs {
		for _, gf := range pkg.Files {
			if !hasGeneratedMarker(gf.Src) {
				continue
			}
			fns := topFuncs(gf)
			if len(fns) == 0 {
				continue
			}
			last := fns[len(fns)-1]
			start := declStart(gf, last)
			_, end := gf.offsets(last)
			out := append([]byte{}, gf.Src[:start]...)
			out = append(out, fmt.Sprintf(
				"// %sRegenA is emitted by the regenerated stub.\nfunc %sRegenA() string { return \"a\" }\n\n// %sRegenB is emitted by the regenerated stub.\nfunc %sRegenB() string { return \"b\" }\n\n// %sRegenType is emitted by the regenerated stub.\ntype %sRegenType struct{ Field string }\n",
				marker, marker, marker, marker, marker, marker)...)
			out = append(out, gf.Src[end:]...)
			return &Mutation{
				Desc: fmt.Sprintf("replace the generated file %s wholesale with a regenerated body: func %s removed, two funcs and one type added, the surviving declarations keeping identity",
					gf.Rel, last.Name.Name),
				Ops: []FileOp{{Kind: opWrite, Path: gf.Rel, Data: out}},
			}, nil
		}
	}
	return nil, errNoTarget
}

// planChangeExternalImport SWAPS one external import for another AND ADDS a
// second, in one change set — so one interned external node is orphaned and must
// be swept while new ones are minted. The add-or-swap direction is the one a
// delete-only proof never reaches.
func planChangeExternalImport(m *RepoModel) (*Mutation, error) {
	for _, pkg := range m.Pkgs {
		for _, gf := range pkg.Files {
			aliases := importAliases(gf)
			if _, taken := aliases["strconv"]; taken {
				continue
			}
			// Find an import used exactly once, in an expression statement, so
			// the usage can be rewritten and the import dropped.
			counts := map[string]int{}
			ast.Inspect(gf.AST, func(n ast.Node) bool {
				if se, ok := n.(*ast.SelectorExpr); ok {
					if id, ok := se.X.(*ast.Ident); ok {
						if _, isImp := aliases[id.Name]; isImp {
							counts[id.Name]++
						}
					}
				}
				return true
			})
			var qual string
			for _, name := range sortedKeys(counts) {
				if counts[name] == 1 && aliases[name] != "" {
					qual = name
					break
				}
			}
			if qual == "" {
				continue
			}
			// Rewrite the single usage to a strconv call, drop the old import,
			// add strconv.
			var usage *ast.SelectorExpr
			ast.Inspect(gf.AST, func(n ast.Node) bool {
				if usage != nil {
					return false
				}
				if se, ok := n.(*ast.SelectorExpr); ok {
					if id, ok := se.X.(*ast.Ident); ok && id.Name == qual {
						usage = se
					}
				}
				return true
			})
			if usage == nil {
				continue
			}
			src := gf.replace(usage, "strconv.Itoa")
			// Re-point the import spec itself rather than deleting it, so the
			// file keeps a valid import block without a formatting pass.
			var spec *ast.ImportSpec
			for _, im := range gf.AST.Imports {
				name := path.Base(strings.Trim(im.Path.Value, `"`))
				if im.Name != nil {
					name = im.Name.Name
				}
				if name == qual {
					spec = im
					break
				}
			}
			if spec == nil {
				continue
			}
			s, e := gf.offsets(spec.Path)
			// The usage rewrite shifted offsets after its own position; apply
			// the import rewrite only when it precedes the usage, which it
			// always does (imports come first in a Go file).
			us, _ := gf.offsets(usage)
			if e > us {
				continue
			}
			src2 := append([]byte{}, src[:s]...)
			src2 = append(src2, `"strconv"`...)
			src2 = append(src2, src[e:]...)
			add := fmt.Sprintf("\n// %sExternal is appended by the SW-144 real-repo parity matrix; it mints a\n// second external reference in the same change set.\nfunc %sExternal() string {\n\treturn strconv.FormatBool(true)\n}\n", marker, marker)
			src2 = append(src2, add...)
			return &Mutation{
				Desc: fmt.Sprintf("change external imports in %s: swap %q for \"strconv\" (its single use %s.%s becomes strconv.Itoa) AND add a second strconv reference — one interned external node is orphaned and must be swept while others are minted",
					gf.Rel, aliases[qual], qual, usage.Sel.Name),
				Ops: []FileOp{{Kind: opWrite, Path: gf.Rel, Data: src2}},
			}, nil
		}
	}
	return nil, errNoTarget
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func fileExistsIn(pkg *GoPkg, rel string) bool {
	for _, gf := range pkg.Files {
		if gf.Rel == rel {
			return true
		}
	}
	return false
}
