package typeresolve

import (
	"reflect"
	"testing"
)

func TestParseModulePath(t *testing.T) {
	cases := []struct {
		name, gomod, want string
		ok                bool
	}{
		{"plain", "module github.com/samibel/graphi\n\ngo 1.26\n", "github.com/samibel/graphi", true},
		{"leading comment + blank", "// hi\n\nmodule  example.com/m\t\n", "example.com/m", true},
		{"trailing line comment", "module example.com/m // the module\n", "example.com/m", true},
		{"quoted", `module "example.com/q"` + "\n", "example.com/q", true},
		{"prefix confusion", "modulex example.com/no\nmodule example.com/yes\n", "example.com/yes", true},
		{"missing", "go 1.26\nrequire example.com/dep v1.0.0\n", "", false},
		{"empty", "", "", false},
		{"comment-only module line", "module // nothing\nmodule example.com/m\n", "example.com/m", true},
	}
	for _, c := range cases {
		got, ok := ParseModulePath([]byte(c.gomod))
		if got != c.want || ok != c.ok {
			t.Errorf("%s: ParseModulePath = (%q, %v), want (%q, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestResolveImport(t *testing.T) {
	const mod = "example.com/m"
	cases := []struct {
		imp, dir string
		ok       bool
	}{
		{"example.com/m", ".", true},
		{"example.com/m/sub", "sub", true},
		{"example.com/m/sub/deep", "sub/deep", true},
		{"example.com/module", "", false}, // prefix confusion: m vs module
		{"fmt", "", false},
		{"github.com/other/dep", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		dir, ok := ResolveImport(mod, c.imp)
		if dir != c.dir || ok != c.ok {
			t.Errorf("ResolveImport(%q) = (%q, %v), want (%q, %v)", c.imp, dir, ok, c.dir, c.ok)
		}
	}
	if _, ok := ResolveImport("", "fmt"); ok {
		t.Error("empty module path must resolve nothing")
	}
}

func TestGroupPackages(t *testing.T) {
	files := map[string][]byte{
		"main.go":           []byte("package main\n\nimport (\n\t\"fmt\"\n\t\"example.com/m/util\"\n)\n\nfunc main() { fmt.Println(util.X) }\n"),
		"extra.go":          []byte("package main\n\nimport \"example.com/m/util\"\n\nvar y = util.X\n"),
		"util/util.go":      []byte("package util\n\nvar X = 1\n"),
		"util/util_test.go": []byte("package util\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n"),
		"mixed/a.go":        []byte("package alpha\n"),
		"mixed/b.go":        []byte("package beta\n"),
		"broken/bad.go":     []byte("pkg not-go\n"),
		"docs/readme.md":    []byte("# not go\n"),
	}
	pkgs, skipped := GroupPackages(files)

	byKey := map[string]Package{}
	for _, p := range pkgs {
		byKey[p.Dir+"/"+p.Name] = p
	}

	root := byKey["./main"]
	if !reflect.DeepEqual(root.Files, []string{"extra.go", "main.go"}) {
		t.Errorf("root files = %v", root.Files)
	}
	if !reflect.DeepEqual(root.Imports, []string{"example.com/m/util", "fmt"}) {
		t.Errorf("root imports = %v (must be sorted, de-duplicated union)", root.Imports)
	}
	if root.Degraded != "" {
		t.Errorf("root unexpectedly degraded: %q", root.Degraded)
	}

	util := byKey["util/util"]
	if !reflect.DeepEqual(util.Files, []string{"util/util.go"}) {
		t.Errorf("util files = %v (the _test.go file must be excluded)", util.Files)
	}

	for _, key := range []string{"mixed/alpha", "mixed/beta"} {
		if byKey[key].Degraded != "multiple package clauses in directory" {
			t.Errorf("%s: Degraded = %q, want multiple-clauses reason", key, byKey[key].Degraded)
		}
	}

	wantSkips := map[string]string{
		"util/util_test.go": "test file (heuristic-only in v1)",
		"broken/bad.go":     "package clause unparseable",
	}
	if len(skipped) != len(wantSkips) {
		t.Fatalf("skipped = %v, want exactly %v", skipped, wantSkips)
	}
	for _, s := range skipped {
		if wantSkips[s.Path] != s.Reason {
			t.Errorf("skip %s: reason %q, want %q", s.Path, s.Reason, wantSkips[s.Path])
		}
	}
}

// TestCheckOrder_DependenciesFirst pins the topological property: every
// intra-repo dependency precedes its importers, cycles degrade (never abort),
// and external test packages/foreign imports do not disturb the order.
func TestCheckOrder_DependenciesFirst(t *testing.T) {
	const mod = "example.com/m"
	files := map[string][]byte{
		"main.go":      []byte("package main\n\nimport \"example.com/m/a\"\n\nvar _ = a.X\n"),
		"a/a.go":       []byte("package a\n\nimport \"example.com/m/b\"\n\nvar X = b.Y\n"),
		"b/b.go":       []byte("package b\n\nimport \"fmt\"\n\nvar Y = 1\n\nfunc init() { fmt.Println() }\n"),
		"loop1/x.go":   []byte("package loop1\n\nimport \"example.com/m/loop2\"\n\nvar X = loop2.Y\n"),
		"loop2/y.go":   []byte("package loop2\n\nimport \"example.com/m/loop1\"\n\nvar Y = loop1.X\n"),
		"self/self.go": []byte("package self\n\nimport \"example.com/m/self\"\n"),
		"leaf/leaf.go": []byte("package leaf\n\nvar L = 1\n"),
	}
	pkgs, _ := GroupPackages(files)
	ordered := CheckOrder(mod, pkgs)

	pos := map[string]int{}
	degraded := map[string]string{}
	for i, p := range ordered {
		pos[p.Dir] = i
		degraded[p.Dir] = p.Degraded
	}

	// b before a before main (the dependency chain).
	if !(pos["b"] < pos["a"] && pos["a"] < pos["."]) {
		t.Errorf("dependency order violated: b=%d a=%d main=%d", pos["b"], pos["a"], pos["."])
	}
	// The cycle members are degraded; everything else is not.
	for dir, want := range map[string]string{
		"loop1": "import cycle",
		"loop2": "import cycle",
		"self":  "import cycle",
		"b":     "",
		"a":     "",
		".":     "",
		"leaf":  "",
	} {
		if degraded[dir] != want {
			t.Errorf("%s: Degraded = %q, want %q", dir, degraded[dir], want)
		}
	}
	if len(ordered) != len(pkgs) {
		t.Fatalf("CheckOrder dropped units: %d != %d", len(ordered), len(pkgs))
	}
}

// TestCheckOrder_Deterministic re-runs grouping+ordering many times: identical
// input must yield an identical order (map iteration is the classic leak).
func TestCheckOrder_Deterministic(t *testing.T) {
	const mod = "example.com/m"
	files := map[string][]byte{
		"main.go": []byte("package main\n\nimport (\n\t\"example.com/m/a\"\n\t\"example.com/m/b\"\n\t\"example.com/m/c\"\n)\n\nvar _ = a.X + b.Y + c.Z\n"),
		"a/a.go":  []byte("package a\n\nvar X = 1\n"),
		"b/b.go":  []byte("package b\n\nvar Y = 1\n"),
		"c/c.go":  []byte("package c\n\nvar Z = 1\n"),
		"d/d1.go": []byte("package d\n\nimport \"example.com/m/e\"\n\nvar D = e.E\n"),
		"e/e.go":  []byte("package e\n\nvar E = 1\n"),
		"d/d2.go": []byte("package d\n\nvar D2 = 2\n"),
	}
	var first []string
	for i := 0; i < 50; i++ {
		pkgs, _ := GroupPackages(files)
		ordered := CheckOrder(mod, pkgs)
		var dirs []string
		for _, p := range ordered {
			dirs = append(dirs, p.Dir+"/"+p.Name)
		}
		if first == nil {
			first = dirs
			continue
		}
		if !reflect.DeepEqual(first, dirs) {
			t.Fatalf("iteration %d produced a different order:\n first: %v\n now:   %v", i, first, dirs)
		}
	}
}

// TestCheckOrder_PreexistingDegradationSurvives pins that CheckOrder never
// clears a grouping-time degradation (multi-clause dirs stay degraded even
// when they are also on a cycle).
func TestCheckOrder_PreexistingDegradationSurvives(t *testing.T) {
	const mod = "example.com/m"
	files := map[string][]byte{
		"mixed/a.go": []byte("package alpha\n\nimport \"example.com/m/mixed\"\n"),
		"mixed/b.go": []byte("package beta\n"),
	}
	pkgs, _ := GroupPackages(files)
	ordered := CheckOrder(mod, pkgs)
	for _, p := range ordered {
		if p.Degraded != "multiple package clauses in directory" {
			t.Errorf("%s/%s: Degraded = %q — grouping-time reason must survive ordering", p.Dir, p.Name, p.Degraded)
		}
	}
}
