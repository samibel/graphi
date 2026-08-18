package link

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
)

// This file is the LINK-001 / ADR 0011 gate: an `imports` edge targets the
// imported package's SOURCE files, not every file that happens to sit in the
// resolved directory.
//
// WHY THERE WAS NO RED GATE BEFORE IT. Every pre-existing hermetic fixture in
// this package builds single-extension directories — `shop/cart.go` +
// `shop/price.go`, `app/main.py` + `app/util.py`. A directory in which the whole
// contents and the package's source files are the SAME SET cannot distinguish
// "targets the directory" from "targets the package", which is exactly how a
// defect that put an `imports` edge on `README.md` survived to a release. Every
// fixture below therefore puts at least one NON-SOURCE file in the target
// directory; that is the load-bearing part of each of them.

// importsTargets returns the sorted qualified names of the `imports` edge
// targets emitted from fromPath, so an assertion can state the exact target set
// rather than "contains".
func importsTargets(t *testing.T, nodes []model.Node, edges []model.Edge, fromPath string) []string {
	t.Helper()
	byID := map[model.NodeId]model.Node{}
	var from model.NodeId
	for _, n := range nodes {
		byID[n.ID()] = n
		if n.Kind() == fileKind && n.QualifiedName() == fromPath {
			from = n.ID()
		}
	}
	if from == "" {
		t.Fatalf("fixture has no file node %q", fromPath)
	}
	var out []string
	for _, e := range edges {
		if e.From() != from || e.Kind() != edgeImports {
			continue
		}
		target, ok := byID[e.To()]
		if !ok {
			t.Fatalf("imports edge to unknown node %s", e.To())
		}
		out = append(out, target.QualifiedName())
	}
	sort.Strings(out)
	return out
}

func assertTargets(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("imports targets = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("imports targets = %v, want %v", got, want)
		}
	}
}

// mixedGoPackage is the shape LINK-001 is about: an imported Go package
// directory holding one source file, one test file, a README and a lint config.
func mixedGoPackage(t *testing.T) ([]model.Node, []FileRefs) {
	t.Helper()
	nodes := []model.Node{
		mustNode(t, "file", "app/main.go", "app/main.go"),
		mustNode(t, "function", "app.Main", "app/main.go"),

		mustNode(t, "file", "tax/tax.go", "tax/tax.go"),
		mustNode(t, "function", "tax.Rate", "tax/tax.go"),
		mustNode(t, "file", "tax/tax_test.go", "tax/tax_test.go"),
		mustNode(t, "function", "tax.TestRate", "tax/tax_test.go"),
		// Markdown and YAML mint REAL symbol nodes whose clause is the directory
		// base (core/parse/parser_tswalk.go), which is why a clause-based filter
		// would not have caught them. Modelled here so the fixture is honest
		// about that.
		mustNode(t, "file", "tax/README.md", "tax/README.md"),
		mustNode(t, "section", "tax.Overview", "tax/README.md"),
		mustNode(t, "file", "tax/.golangci.yml", "tax/.golangci.yml"),
	}
	files := []FileRefs{{
		SourcePath: "app/main.go",
		Dir:        "app",
		Imports:    []parse.ImportSpec{{Path: "example.com/m/tax"}},
	}}
	return nodes, files
}

// TestImports_Go_TargetsOnlyPackageSourceFiles is AC-1 on the module basis
// (ADR 0009's moduleMap present, the shipped Go shape).
func TestImports_Go_TargetsOnlyPackageSourceFiles(t *testing.T) {
	nodes, files := mixedGoPackage(t)
	b := NewIndexBuilder()
	for _, n := range nodes {
		b.Add(n)
	}
	b.SetModuleMap(NewModuleMap(map[string][]byte{
		"go.mod": []byte("module example.com/m\n\ngo 1.26\n"),
	}))
	idx := b.Build()

	_, edges, _, err := New().Link("go", files, idx)
	if err != nil {
		t.Fatal(err)
	}
	assertTargets(t, importsTargets(t, nodes, edges, "app/main.go"), []string{"tax/tax.go"})
}

// TestImports_Go_ClauseBasis_TargetsOnlyPackageSourceFiles is AC-1 on the
// no-go.mod clause basis, which is a SEPARATE branch of packageFileNodes. Both
// branches have to filter or the fix is half a fix.
func TestImports_Go_ClauseBasis_TargetsOnlyPackageSourceFiles(t *testing.T) {
	nodes, files := mixedGoPackage(t)
	idx := BuildIndex(nodes) // no module map: the historical clause union
	_, edges, _, err := New().Link("go", files, idx)
	if err != nil {
		t.Fatal(err)
	}
	assertTargets(t, importsTargets(t, nodes, edges, "app/main.go"), []string{"tax/tax.go"})
}

// TestImports_Go_BuildTagVariantsAllRemainTargets is AC-6's first control. Four
// build-tag variants of one `package json` directory are four ordinary `.go`
// files; the filter is extension-based, so all four stay targets. A clause- or
// registry-based filter is the thing that could have broken this.
func TestImports_Go_BuildTagVariantsAllRemainTargets(t *testing.T) {
	nodes := []model.Node{
		mustNode(t, "file", "app/main.go", "app/main.go"),
		mustNode(t, "function", "app.Main", "app/main.go"),

		mustNode(t, "file", "json/json_linux.go", "json/json_linux.go"),
		mustNode(t, "function", "json.Marshal", "json/json_linux.go"),
		mustNode(t, "file", "json/json_darwin.go", "json/json_darwin.go"),
		mustNode(t, "file", "json/json_windows.go", "json/json_windows.go"),
		mustNode(t, "file", "json/json_js.go", "json/json_js.go"),
		mustNode(t, "file", "json/README.md", "json/README.md"),
	}
	files := []FileRefs{{
		SourcePath: "app/main.go",
		Dir:        "app",
		Imports:    []parse.ImportSpec{{Path: "example.com/m/json"}},
	}}
	b := NewIndexBuilder()
	for _, n := range nodes {
		b.Add(n)
	}
	b.SetModuleMap(NewModuleMap(map[string][]byte{
		"go.mod": []byte("module example.com/m\n\ngo 1.26\n"),
	}))
	_, edges, _, err := New().Link("go", files, b.Build())
	if err != nil {
		t.Fatal(err)
	}
	assertTargets(t, importsTargets(t, nodes, edges, "app/main.go"), []string{
		"json/json_darwin.go", "json/json_js.go", "json/json_linux.go", "json/json_windows.go",
	})
}

// TestImports_Go_TestFileIsStillAnImporter is AC-6's second control: the
// SOURCE-side direction. `_test.go` is excluded as a TARGET (not importable) but
// remains a first-class `from` — an external test package
// (`tax/tax_ext_test.go` in `package tax_test`) importing the package under test
// must keep its edge onto `tax.go`.
func TestImports_Go_TestFileIsStillAnImporter(t *testing.T) {
	nodes := []model.Node{
		mustNode(t, "file", "tax/tax.go", "tax/tax.go"),
		mustNode(t, "function", "tax.Rate", "tax/tax.go"),
		mustNode(t, "file", "tax/tax_ext_test.go", "tax/tax_ext_test.go"),
		mustNode(t, "function", "tax_test.TestRate", "tax/tax_ext_test.go"),
		mustNode(t, "file", "tax/README.md", "tax/README.md"),
	}
	files := []FileRefs{{
		SourcePath: "tax/tax_ext_test.go",
		Dir:        "tax",
		Imports:    []parse.ImportSpec{{Path: "example.com/m/tax"}},
	}}
	b := NewIndexBuilder()
	for _, n := range nodes {
		b.Add(n)
	}
	b.SetModuleMap(NewModuleMap(map[string][]byte{
		"go.mod": []byte("module example.com/m\n\ngo 1.26\n"),
	}))
	_, edges, _, err := New().Link("go", files, b.Build())
	if err != nil {
		t.Fatal(err)
	}
	assertTargets(t, importsTargets(t, nodes, edges, "tax/tax_ext_test.go"), []string{"tax/tax.go"})
}

// TestImports_Python_TargetsSourceFilesIncludingNotebooks is AC-2 and AC-6's
// third control in one fixture: `.py` and `.ipynb` are both package sources,
// `test_*.py` stays a target because Python test modules ARE importable, and the
// README/requirements.txt are not targets.
func TestImports_Python_TargetsSourceFilesIncludingNotebooks(t *testing.T) {
	nodes := []model.Node{
		mustNode(t, "file", "app/main.py", "app/main.py"),
		mustNode(t, "function", "app.main", "app/main.py"),

		mustNode(t, "file", "shop/cart.py", "shop/cart.py"),
		mustNode(t, "function", "shop.checkout", "shop/cart.py"),
		mustNode(t, "file", "shop/test_cart.py", "shop/test_cart.py"),
		mustNode(t, "function", "shop.test_checkout", "shop/test_cart.py"),
		mustNode(t, "file", "shop/explore.ipynb", "shop/explore.ipynb"),
		mustNode(t, "function", "shop.nb_helper", "shop/explore.ipynb"),
		mustNode(t, "file", "shop/README.md", "shop/README.md"),
		mustNode(t, "file", "shop/requirements.txt", "shop/requirements.txt"),
	}
	files := []FileRefs{{
		SourcePath: "app/main.py",
		Dir:        "app",
		Imports:    []parse.ImportSpec{{Alias: "shop", Path: "shop"}},
	}}
	_, edges, _, err := New().Link("python", files, BuildIndex(nodes))
	if err != nil {
		t.Fatal(err)
	}
	assertTargets(t, importsTargets(t, nodes, edges, "app/main.py"), []string{
		"shop/cart.py", "shop/explore.ipynb", "shop/test_cart.py",
	})
}

// TestImports_CSharp_TargetsOnlySourceFiles is AC-2 for C#.
func TestImports_CSharp_TargetsOnlySourceFiles(t *testing.T) {
	nodes := []model.Node{
		mustNode(t, "file", "App/Program.cs", "App/Program.cs"),
		mustNode(t, "class", "App.Program", "App/Program.cs"),

		mustNode(t, "file", "Shop/Price.cs", "Shop/Price.cs"),
		mustNode(t, "class", "Shop.Price", "Shop/Price.cs"),
		mustNode(t, "file", "Shop/Shop.csproj", "Shop/Shop.csproj"),
		mustNode(t, "file", "Shop/README.md", "Shop/README.md"),
	}
	files := []FileRefs{{
		SourcePath: "App/Program.cs",
		Dir:        "App",
		Imports:    []parse.ImportSpec{{Path: "Shop"}},
	}}
	_, edges, _, err := New().Link("c_sharp", files, BuildIndex(nodes))
	if err != nil {
		t.Fatal(err)
	}
	assertTargets(t, importsTargets(t, nodes, edges, "App/Program.cs"), []string{"Shop/Price.cs"})
}

// TestImports_Rust_TargetsOnlySourceFiles is AC-2 for Rust. `Cargo.toml` sits in
// every crate directory, so it is the exact non-source file this filter has to
// keep out.
func TestImports_Rust_TargetsOnlySourceFiles(t *testing.T) {
	nodes := []model.Node{
		mustNode(t, "file", "src/main.rs", "src/main.rs"),
		mustNode(t, "function", "src.main", "src/main.rs"),

		mustNode(t, "file", "shop/price.rs", "shop/price.rs"),
		mustNode(t, "function", "shop.price", "shop/price.rs"),
		mustNode(t, "file", "shop/Cargo.toml", "shop/Cargo.toml"),
		mustNode(t, "file", "shop/README.md", "shop/README.md"),
	}
	files := []FileRefs{{
		SourcePath: "src/main.rs",
		Dir:        "src",
		Imports:    []parse.ImportSpec{{Path: "crate::shop::price"}},
	}}
	_, edges, _, err := New().Link("rust", files, BuildIndex(nodes))
	if err != nil {
		t.Fatal(err)
	}
	assertTargets(t, importsTargets(t, nodes, edges, "src/main.rs"), []string{"shop/price.rs"})
}

// TestPackageFileFilter_IsAReadParameter_NotAnIndexNarrowing is AC-3 as an
// executable claim rather than a comment. ONE index, built once, answers three
// questions about the SAME directory:
//
//	packageFileNodes  — emission: filtered, the package's source files only;
//	hasPackage        — presence: unfiltered, true even for a directory whose
//	                    only file the emission filter rejects;
//	DirsForImport     — re-link scheduling: unfiltered, so the cascade still
//	                    reaches that directory.
//
// If a future "simplification" moves the filter into SymbolIndex.Add, the first
// assertion keeps passing and the last two go red — which is the whole reason
// the filter is a parameter of reading.
func TestPackageFileFilter_IsAReadParameter_NotAnIndexNarrowing(t *testing.T) {
	// docsonly/ holds NO file the Go source filter admits. It is a real package
	// directory to hasPackage and DirsForImport, and no imports target.
	nodes := []model.Node{
		mustNode(t, "file", "docsonly/README.md", "docsonly/README.md"),
		mustNode(t, "section", "docsonly.Overview", "docsonly/README.md"),
	}
	b := NewIndexBuilder()
	for _, n := range nodes {
		b.Add(n)
	}
	b.SetModuleMap(NewModuleMap(map[string][]byte{
		"go.mod": []byte("module example.com/m\n\ngo 1.26\n"),
	}))
	idx := b.Build()

	if got := idx.packageFileNodes("example.com/m/docsonly", goPackageFile); len(got) != 0 {
		t.Errorf("packageFileNodes admitted %d non-source target(s), want 0", len(got))
	}
	if !idx.hasPackage("example.com/m/docsonly") {
		t.Error("hasPackage read a FILTERED list: a directory whose files the emission filter " +
			"rejects is still present in the repo, and answering \"absent\" mints a phantom external node")
	}
	dirs := idx.DirsForImport("example.com/m/docsonly")
	found := false
	for _, d := range dirs {
		if d == "docsonly" {
			found = true
		}
	}
	if !found {
		t.Errorf("DirsForImport read a FILTERED list: got %v, want it to contain \"docsonly\". "+
			"Under-approximating here freezes an edge permanently (the ADR 0009 defect class)", dirs)
	}
}

// TestPackageFileNodes_NilFilterAdmitsNothing pins the fail-closed direction: a
// caller that forgets its membership filter loses recall rather than silently
// restoring LINK-001.
func TestPackageFileNodes_NilFilterAdmitsNothing(t *testing.T) {
	nodes, _ := mixedGoPackage(t)
	idx := BuildIndex(nodes)
	if got := idx.packageFileNodes("example.com/m/tax", nil); got != nil {
		t.Errorf("packageFileNodes(nil filter) = %v, want nil", got)
	}
	if got := clausePackageFileNodes(idx, "tax", nil); got != nil {
		t.Errorf("clausePackageFileNodes(nil filter) = %v, want nil", got)
	}
}

// TestImports_CaseVariantExtensionsStayTargets pins the case-sensitivity
// contract on both halves of the rule, because they differ on purpose.
//
// parse.Registry.ParserFor LOWERCASES an extension before selecting a parser
// (core/parse/registry.go normalizeExt), so `Main.PY` and `Helper.GO` really are
// indexed Python and Go modules with committed file nodes. A case-sensitive
// membership filter would have dropped their edges — a recall regression this
// change would have introduced rather than found. Meanwhile `x_TEST.go` is NOT a
// test file to go/build, so it stays importable and stays a target.
func TestImports_CaseVariantExtensionsStayTargets(t *testing.T) {
	t.Run("go", func(t *testing.T) {
		nodes := []model.Node{
			mustNode(t, "file", "app/main.go", "app/main.go"),
			mustNode(t, "function", "app.Main", "app/main.go"),
			mustNode(t, "file", "tax/Helper.GO", "tax/Helper.GO"),
			mustNode(t, "function", "tax.Helper", "tax/Helper.GO"),
			mustNode(t, "file", "tax/x_TEST.go", "tax/x_TEST.go"),
			mustNode(t, "function", "tax.X", "tax/x_TEST.go"),
			mustNode(t, "file", "tax/tax_test.go", "tax/tax_test.go"),
			mustNode(t, "file", "tax/README.md", "tax/README.md"),
		}
		files := []FileRefs{{
			SourcePath: "app/main.go",
			Dir:        "app",
			Imports:    []parse.ImportSpec{{Path: "example.com/m/tax"}},
		}}
		b := NewIndexBuilder()
		for _, n := range nodes {
			b.Add(n)
		}
		b.SetModuleMap(NewModuleMap(map[string][]byte{
			"go.mod": []byte("module example.com/m\n\ngo 1.26\n"),
		}))
		_, edges, _, err := New().Link("go", files, b.Build())
		if err != nil {
			t.Fatal(err)
		}
		assertTargets(t, importsTargets(t, nodes, edges, "app/main.go"), []string{
			"tax/Helper.GO", "tax/x_TEST.go",
		})
	})

	t.Run("python", func(t *testing.T) {
		nodes := []model.Node{
			mustNode(t, "file", "app/main.py", "app/main.py"),
			mustNode(t, "function", "app.main", "app/main.py"),
			mustNode(t, "file", "shop/Cart.PY", "shop/Cart.PY"),
			mustNode(t, "function", "shop.checkout", "shop/Cart.PY"),
			mustNode(t, "file", "shop/README.md", "shop/README.md"),
		}
		files := []FileRefs{{
			SourcePath: "app/main.py",
			Dir:        "app",
			Imports:    []parse.ImportSpec{{Alias: "shop", Path: "shop"}},
		}}
		_, edges, _, err := New().Link("python", files, BuildIndex(nodes))
		if err != nil {
			t.Fatal(err)
		}
		assertTargets(t, importsTargets(t, nodes, edges, "app/main.py"), []string{"shop/Cart.PY"})
	})
}

// TestExtensionSets_AreWellFormed pins the shape extSetFilter can actually
// match: it compares a LOWERCASED path.Ext, so an entry without its leading dot
// — or an entry carrying an uppercase letter — silently matches nothing.
func TestExtensionSets_AreWellFormed(t *testing.T) {
	for name, exts := range map[string][]string{
		"pyPkgExts":     pyPkgExts,
		"csharpPkgExts": csharpPkgExts,
		"rustPkgExts":   rustPkgExts,
	} {
		if len(exts) == 0 {
			t.Errorf("%s is empty: extSetFilter would admit nothing and the language would emit "+
				"NO imports edges at all", name)
		}
		for _, ext := range exts {
			if len(ext) < 2 || ext[0] != '.' {
				t.Errorf("%s entry %q is not a leading-dot extension; extSetFilter compares "+
					"against path.Ext and would never match it", name, ext)
			}
			if ext != strings.ToLower(ext) {
				t.Errorf("%s entry %q is not lowercase; extSetFilter lowercases the path's "+
					"extension before comparing, so this entry can never match", name, ext)
			}
		}
	}
}

// TestEveryClauseKeyedResolver_DeclaresPkgTargetExts is the guard that binds a
// FUTURE language, which no fixture above can do: the three behavioural tests
// prove Python, C# and Rust are wired, but a fourth clause-keyed resolver added
// next year would simply emit zero imports edges and no existing test would
// notice.
//
// It is a source scan for the same reason TestLinkPurity is one: the fact being
// asserted is "every file that opts into the package fan-out also declares its
// membership set", which is a property of the FILE SET, not of any one call.
// Building it out of a test-only hook in the product code would have been the
// alternative, and a product byte added purely to observe a test is a worse
// trade than reading the directory `go test` already runs in.
//
// IT WALKS THE AST, NOT THE TEXT. *Strengthened in review, which showed the
// first version had exactly the holes that matter:* a `strings.Contains` scan
// for the literal `"b.pkgImportPaths = append"` is blind to
// `b.pkgImportPaths = []string{…}`, to a `binder{pkgImportPaths: …}` composite
// literal, and to any other spelling — each of which would give a new language
// zero `imports` edges with nothing going red, which is precisely the
// silent-zero scenario the guard exists to prevent. Symmetrically, a text scan
// for `"pkgTargetExts:"` is satisfied by a COMMENT. Walking the AST removes
// both: every field reference is found whatever its syntax, and comments are
// not part of it.
func TestEveryClauseKeyedResolver_DeclaresPkgTargetExts(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	scanned, optedIn := 0, 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Clean(name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		scanned++
		// resolve_common.go DEFINES both fields and is the consumer of
		// pkgTargetExts, so it names them without opting in. Every other file is
		// judged on the pair.
		if name == "resolve_common.go" {
			continue
		}
		usesImports := fileMentionsField(f, "pkgImportPaths")
		usesExts := fileMentionsField(f, "pkgTargetExts")
		if !usesImports {
			if usesExts {
				t.Errorf("%s declares pkgTargetExts but never populates pkgImportPaths: the "+
					"extension set has no fan-out to filter and is dead", name)
			}
			continue
		}
		optedIn++
		if !usesExts {
			t.Errorf("%s populates binder.pkgImportPaths (the package fan-out) but never sets "+
				"pkgTargetExts, so extSetFilter admits nothing and this language emits NO imports "+
				"edges at all (LINK-001 / ADR 0011)", name)
		}
	}
	if scanned == 0 {
		t.Fatal("scanned no source files: the guard is vacuous")
	}
	if optedIn != 3 {
		t.Errorf("%d files opt into the package fan-out, want 3 (python, c_sharp, rust). "+
			"A change to that set is a deliberate scope change and must update ADR 0011 and "+
			"docs/language-support.md with it", optedIn)
	}
}

// fileMentionsField reports whether f references the named struct field in any
// syntactic position — a selector (`b.pkgTargetExts`), a composite-literal key
// (`binder{pkgTargetExts: …}`), or a field declaration. Comments are not part of
// the AST walked here, which is the point: a mention in prose must not satisfy
// the guard.
func fileMentionsField(f *ast.File, field string) bool {
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if found {
			return false
		}
		switch v := n.(type) {
		case *ast.SelectorExpr:
			if v.Sel != nil && v.Sel.Name == field {
				found = true
			}
		case *ast.KeyValueExpr:
			if id, ok := v.Key.(*ast.Ident); ok && id.Name == field {
				found = true
			}
		}
		return !found
	})
	return found
}
