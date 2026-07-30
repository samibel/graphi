package parity

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// errNoTarget is returned by a class planner when the repository does not
// exhibit the structure the class needs (no interface to change, no generated
// file to replace, …).
//
// It is a FIRST-CLASS control signal, not an error to swallow: repository
// selection walks the corpus in (tier, size) order and takes the first
// repository whose planner succeeds, which is exactly AC-6's "each class runs on
// the smallest repository that exhibits it" — decided against the REAL clone
// rather than assumed from a repository's reputation.
var errNoTarget = errors.New("parity: repository does not exhibit this class's required structure")

// GoFile is one parsed non-test Go source file of the root module.
type GoFile struct {
	// Rel is the repo-relative, slash-separated path — the identity the report
	// and every mutation description uses.
	Rel string
	Abs string
	Src []byte
	AST *ast.File
	// Fset is shared across the whole model so positions are comparable.
	Fset *token.FileSet
	Dir  string // repo-relative dir, "." for the module root
}

// GoPkg is one package directory of the root module.
type GoPkg struct {
	Dir        string // repo-relative, "." for the module root
	Name       string // the package clause
	ImportPath string
	Files      []*GoFile // non-test only, sorted by Rel
}

// RepoModel is the harness's read-only view of a materialized clone.
//
// It is built with the STANDARD LIBRARY parser (go/parser, go/ast, go/token) and
// deliberately not with any graphi package. Its only job is to LOCATE a real
// edit target in real source; it never models the graph, never resolves types
// and never imports engine/ or core/parse. The graph is produced exclusively by
// the graphi binary running as a subprocess.
type RepoModel struct {
	Root   string
	Module string
	Pkgs   []*GoPkg
	byDir  map[string]*GoPkg
}

// discover parses the root module of a clone.
//
// Sub-modules are EXCLUDED by construction: a directory carrying its own go.mod
// is a different module, and grpc-go has eleven of them. Including them would
// mix module boundaries into a class that is supposed to exercise one.
func discover(root string) (*RepoModel, error) {
	modPath, err := modulePath(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	m := &RepoModel{Root: root, Module: modPath, byDir: map[string]*GoPkg{}}

	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			base := d.Name()
			if rel != "." && (base == ".git" || base == "vendor" || base == "testdata" ||
				strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_")) {
				return fs.SkipDir
			}
			// A nested go.mod marks a separate module — skip the whole subtree.
			if rel != "." {
				if _, serr := os.Stat(filepath.Join(p, "go.mod")); serr == nil {
					return fs.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		src, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, p, src, parser.ParseComments)
		if perr != nil {
			// An unparseable file is not fatal: graphi ingests real trees that
			// include them. It is simply not an edit target.
			return nil
		}
		dir := path.Dir(rel)
		gf := &GoFile{Rel: rel, Abs: p, Src: src, AST: file, Fset: fset, Dir: dir}
		pkg := m.byDir[dir]
		if pkg == nil {
			ip := modPath
			if dir != "." {
				ip = modPath + "/" + dir
			}
			pkg = &GoPkg{Dir: dir, Name: file.Name.Name, ImportPath: ip}
			m.byDir[dir] = pkg
			m.Pkgs = append(m.Pkgs, pkg)
		}
		pkg.Files = append(pkg.Files, gf)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("parity: discover %s: %w", root, err)
	}
	sort.Slice(m.Pkgs, func(i, j int) bool { return m.Pkgs[i].Dir < m.Pkgs[j].Dir })
	for _, p := range m.Pkgs {
		sort.Slice(p.Files, func(i, j int) bool { return p.Files[i].Rel < p.Files[j].Rel })
	}
	if len(m.Pkgs) == 0 {
		return nil, fmt.Errorf("parity: %s has no parseable non-test Go package in its root module", root)
	}
	return m, nil
}

// modulePath reads the module path from a go.mod.
func modulePath(gomod string) (string, error) {
	raw, err := os.ReadFile(gomod)
	if err != nil {
		return "", fmt.Errorf("parity: read %s: %w", gomod, err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(rest), nil
		}
	}
	return "", fmt.Errorf("parity: %s declares no module path", gomod)
}

// primaryPkg is the package the "everyday" classes edit: the one with the most
// non-test files, tie-broken by directory so the choice is deterministic across
// runs and machines. Determinism matters more than cleverness here — a harness
// that picks a different target on two dispatches would manufacture the exact
// run-to-run disagreement AC-17 exists to detect.
func (m *RepoModel) primaryPkg() *GoPkg {
	var best *GoPkg
	for _, p := range m.Pkgs {
		if len(p.Files) == 0 {
			continue
		}
		if best == nil || len(p.Files) > len(best.Files) ||
			(len(p.Files) == len(best.Files) && p.Dir < best.Dir) {
			best = p
		}
	}
	return best
}

// offsets converts a node's position range to byte offsets in gf.Src.
func (gf *GoFile) offsets(n ast.Node) (int, int) {
	start := gf.Fset.Position(n.Pos()).Offset
	end := gf.Fset.Position(n.End()).Offset
	if start < 0 {
		start = 0
	}
	if end > len(gf.Src) {
		end = len(gf.Src)
	}
	return start, end
}

// text returns a node's exact source text.
func (gf *GoFile) text(n ast.Node) string {
	s, e := gf.offsets(n)
	return string(gf.Src[s:e])
}

// cut removes a node's source range, together with the whitespace line it sat
// on, and returns the new file bytes.
func (gf *GoFile) cut(n ast.Node) []byte {
	s, e := gf.offsets(n)
	// Extend backwards over the doc comment's blank line and forwards over the
	// trailing newline, so the removal leaves no ragged blank block.
	for s > 0 && (gf.Src[s-1] == ' ' || gf.Src[s-1] == '\t') {
		s--
	}
	for e < len(gf.Src) && gf.Src[e] == '\n' {
		e++
	}
	out := make([]byte, 0, len(gf.Src))
	out = append(out, gf.Src[:s]...)
	out = append(out, gf.Src[e:]...)
	return out
}

// replace substitutes a node's source range with repl.
func (gf *GoFile) replace(n ast.Node, repl string) []byte {
	s, e := gf.offsets(n)
	out := make([]byte, 0, len(gf.Src)+len(repl))
	out = append(out, gf.Src[:s]...)
	out = append(out, repl...)
	out = append(out, gf.Src[e:]...)
	return out
}

// declStart is a FuncDecl's start offset INCLUDING its doc comment, so moving or
// deleting a declaration takes its documentation with it — which is what a real
// refactor does, and what keeps the resulting tree plausible Go.
func declStart(gf *GoFile, d *ast.FuncDecl) int {
	n := ast.Node(d)
	if d.Doc != nil {
		n = d.Doc
	}
	s, _ := gf.offsets(n)
	return s
}

// topFuncs returns the file's package-level functions (no receiver), in source
// order.
func topFuncs(gf *GoFile) []*ast.FuncDecl {
	var out []*ast.FuncDecl
	for _, d := range gf.AST.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil {
			out = append(out, fd)
		}
	}
	return out
}

// methodDecls returns the file's methods (with a receiver), in source order.
func methodDecls(gf *GoFile) []*ast.FuncDecl {
	var out []*ast.FuncDecl
	for _, d := range gf.AST.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv != nil {
			out = append(out, fd)
		}
	}
	return out
}

// declaredNames returns every package-level name a file declares (funcs, types,
// vars, consts), with whether it is exported.
func declaredNames(gf *GoFile) map[string]bool {
	out := map[string]bool{}
	for _, d := range gf.AST.Decls {
		switch n := d.(type) {
		case *ast.FuncDecl:
			if n.Recv == nil {
				out[n.Name.Name] = n.Name.IsExported()
			}
		case *ast.GenDecl:
			for _, sp := range n.Specs {
				switch s := sp.(type) {
				case *ast.TypeSpec:
					out[s.Name.Name] = s.Name.IsExported()
				case *ast.ValueSpec:
					for _, id := range s.Names {
						out[id.Name] = id.IsExported()
					}
				}
			}
		}
	}
	return out
}

// topLevelDeclCount counts a file's package-level declarations — the size proxy
// used to prefer the SMALLEST blast radius when several files would satisfy a
// class.
func topLevelDeclCount(gf *GoFile) int {
	n := 0
	for _, d := range gf.AST.Decls {
		switch g := d.(type) {
		case *ast.FuncDecl:
			n++
		case *ast.GenDecl:
			if g.Tok == token.IMPORT {
				continue
			}
			n += len(g.Specs)
		}
	}
	return n
}

// importAliases maps each import's local qualifier to its path for one file.
func importAliases(gf *GoFile) map[string]string {
	out := map[string]string{}
	for _, im := range gf.AST.Imports {
		p, err := strconv.Unquote(im.Path.Value)
		if err != nil {
			continue
		}
		name := path.Base(p)
		if im.Name != nil {
			name = im.Name.Name
		}
		if name == "_" || name == "." {
			continue
		}
		out[name] = p
	}
	return out
}

// selectorRefs collects the (qualifier, selected-name) pairs a file uses, e.g.
// `cobra.Command` -> ("cobra", "Command").
func selectorRefs(gf *GoFile) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	ast.Inspect(gf.AST, func(n ast.Node) bool {
		se, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := se.X.(*ast.Ident)
		if !ok {
			return true
		}
		if out[id.Name] == nil {
			out[id.Name] = map[string]bool{}
		}
		out[id.Name][se.Sel.Name] = true
		return true
	})
	return out
}

// identOccurrences returns the byte ranges of every *ast.Ident named name that
// is NOT the `Sel` half of a selector — i.e. the occurrences that a rename must
// rewrite. Excluding `Sel` is what stops a package-level rename from corrupting
// an unrelated `x.name` field access.
func identOccurrences(gf *GoFile, name string) [][2]int {
	sel := map[*ast.Ident]bool{}
	ast.Inspect(gf.AST, func(n ast.Node) bool {
		if se, ok := n.(*ast.SelectorExpr); ok {
			sel[se.Sel] = true
		}
		return true
	})
	var out [][2]int
	ast.Inspect(gf.AST, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || id.Name != name || sel[id] {
			return true
		}
		s, e := gf.offsets(id)
		out = append(out, [2]int{s, e})
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// rewriteRanges applies non-overlapping [start,end) replacements to src,
// right-to-left so earlier offsets stay valid.
func rewriteRanges(src []byte, ranges [][2]int, repl string) []byte {
	out := make([]byte, len(src))
	copy(out, src)
	for i := len(ranges) - 1; i >= 0; i-- {
		r := ranges[i]
		buf := make([]byte, 0, len(out)+len(repl))
		buf = append(buf, out[:r[0]]...)
		buf = append(buf, repl...)
		buf = append(buf, out[r[1]:]...)
		out = buf
	}
	return out
}

// interfaceSpecs returns the file's named interface type declarations.
func interfaceSpecs(gf *GoFile) []*ast.TypeSpec {
	var out []*ast.TypeSpec
	for _, d := range gf.AST.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, sp := range gd.Specs {
			ts, ok := sp.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if _, isIface := ts.Type.(*ast.InterfaceType); isIface {
				out = append(out, ts)
			}
		}
	}
	return out
}

// hasGeneratedMarker reports whether a file's head carries the conventional
// "Code generated … DO NOT EDIT." marker. The window is the first 8 lines,
// which is where the convention places it.
func hasGeneratedMarker(src []byte) bool {
	lines := bytes.SplitN(src, []byte("\n"), 9)
	for i, l := range lines {
		if i >= 8 {
			break
		}
		s := string(l)
		if strings.Contains(s, "Code generated") && strings.Contains(s, "DO NOT EDIT") {
			return true
		}
	}
	return false
}

// buildTagLine returns the 0-based line index and text of a file's //go:build
// constraint, or (-1, "").
func buildTagLine(src []byte) (int, string) {
	for i, l := range strings.Split(string(src), "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "//go:build ") {
			return i, l
		}
		// The constraint block must precede the package clause.
		if strings.HasPrefix(t, "package ") {
			return -1, ""
		}
	}
	return -1, ""
}

// replaceLine substitutes one 0-based line of src.
func replaceLine(src []byte, idx int, repl string) []byte {
	lines := strings.Split(string(src), "\n")
	if idx < 0 || idx >= len(lines) {
		return src
	}
	lines[idx] = repl
	return []byte(strings.Join(lines, "\n"))
}
