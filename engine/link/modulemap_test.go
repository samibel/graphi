package link

import (
	"testing"

	"github.com/samibel/graphi/engine/typeresolve"
)

// TestParseModuleDirective_AgreesWithTyperesolve is the pin the duplication in
// modulemap.go promises. engine/link parses the module directive itself rather
// than importing engine/typeresolve, so the heuristic layer stays independent of
// the type-checked one — the same reasoning check.go:43 records for the kind
// strings. Duplication is only honest if it is held to agree, so this asserts
// the two parsers return identical results over the shapes a real go.mod takes,
// INCLUDING the malformed ones. It is a TEST-only dependency: no production code
// in engine/link imports engine/typeresolve.
func TestParseModuleDirective_AgreesWithTyperesolve(t *testing.T) {
	for _, src := range []string{
		"module example.com/m\n\ngo 1.26\n",
		"module example.com/m",
		"  module   example.com/spaced  \n",
		"\tmodule\texample.com/tabbed\n",
		"// comment first\nmodule example.com/after-comment\n",
		"module example.com/trailing // a trailing comment\n",
		"go 1.26\nrequire example.com/dep v1.0.0\n", // no module directive
		"",
		"modulefoo example.com/nope\n",        // no whitespace after the directive
		"require (\n\tmodule/looking v1\n)\n", // a require block must not read as a module
		"module\n",                            // directive with no path
	} {
		gotPath, gotOK := parseModuleDirective([]byte(src))
		wantPath, wantOK := typeresolve.ParseModulePath([]byte(src))
		if gotOK != wantOK || gotPath != wantPath {
			t.Errorf("parsers disagree on %q:\n  engine/link  = (%q, %v)\n  typeresolve  = (%q, %v)",
				src, gotPath, gotOK, wantPath, wantOK)
		}
	}
}

// TestModuleMap_Dir pins the resolution contract, including the two properties
// that a naive implementation gets wrong: multi-module longest-prefix and
// segment-boundary matching.
func TestModuleMap_Dir(t *testing.T) {
	m := NewModuleMap(map[string][]byte{
		"go.mod":                        []byte("module example.com/m\n\ngo 1.26\n"),
		"sub/go.mod":                    []byte("module example.com/m/sub\n"),
		"staging/src/other/go.mod":      []byte("module other.example/pkg\n"),
		"weird/go.mod":                  []byte("// leading comment\nmodule example.com/mtools // trailing\n"),
		"broken/go.mod":                 []byte("go 1.26\n// no module directive\n"),
		"quoted/nested/deeper/go.mod":   []byte("module \"example.com/m/sub/deep\"\n"),
		"notamodule/go.modish/file.txt": []byte("module nope\n"),
	})

	for _, tc := range []struct {
		importPath string
		wantDir    string
		wantOK     bool
		why        string
	}{
		{"example.com/m", "", true, "the root module itself resolves to the repo root"},
		{"example.com/m/x/json", "x/json", true, "root module: remainder is the directory"},
		{"example.com/m/sub", "sub", true, "a nested module resolves to its own root"},
		{"example.com/m/sub/pkg", "sub/pkg", true,
			"LONGEST prefix wins: example.com/m/sub owns this, not example.com/m"},
		{"example.com/m/sub/deep", "quoted/nested/deeper", true,
			"a deeper nested module wins over both ancestors, and quotes are stripped"},
		{"other.example/pkg/inner", "staging/src/other/inner", true,
			"a module rooted deep in the tree maps relative to ITS root"},
		{"example.com/mtools", "weird", true,
			"mtools is its own module here; it must resolve to weird, never under example.com/m"},
		{"example.com/mtools/x", "weird/x", true, "and its subtree follows it"},
		{"github.com/spf13/cobra", "", false, "third-party: no module in this tree, resolves to nothing"},
		{"fmt", "", false, "stdlib: resolves to nothing"},
		{"", "", false, "empty import path"},
	} {
		got, ok := m.Dir(tc.importPath)
		if ok != tc.wantOK || got != tc.wantDir {
			t.Errorf("Dir(%q) = (%q, %v), want (%q, %v) — %s",
				tc.importPath, got, ok, tc.wantDir, tc.wantOK, tc.why)
		}
	}

	// A broken go.mod contributes nothing rather than a wrong resolution.
	if _, ok := m.Dir("broken"); ok {
		t.Error("a go.mod with no module directive must contribute no resolution")
	}
}

// TestModuleMap_NestedModuleExcludesSubtree pins the Go ownership rule the
// add_nested_gomod conformance class caught this implementation missing: a
// nested module EXCLUDES its directory subtree from the enclosing module, so
// once `sub/` roots its own (differently-named) module, the ROOT module's path
// arithmetic must no longer resolve into it. Fail closed — the import resolves
// nowhere.
func TestModuleMap_NestedModuleExcludesSubtree(t *testing.T) {
	m := NewModuleMap(map[string][]byte{
		"go.mod":     []byte("module example.com/m\n"),
		"sub/go.mod": []byte("module other.example/standalone\n"),
	})
	// The root module's path arithmetic would land in sub/pkg — but sub/ is
	// owned by other.example/standalone now, so this import resolves NOWHERE.
	if dir, ok := m.Dir("example.com/m/sub/pkg"); ok {
		t.Errorf("example.com/m/sub/pkg must not resolve into a foreign nested module's subtree; got %q", dir)
	}
	// The nested module's own path still resolves into its subtree.
	if dir, ok := m.Dir("other.example/standalone/pkg"); !ok || dir != "sub/pkg" {
		t.Errorf("the nested module's own path must resolve: got (%q, %v)", dir, ok)
	}
	// And the root module still owns everything outside sub/.
	if dir, ok := m.Dir("example.com/m/elsewhere"); !ok || dir != "elsewhere" {
		t.Errorf("the root module keeps the rest of the tree: got (%q, %v)", dir, ok)
	}
}

// TestModuleMap_SegmentBoundary is the regression a raw strings.HasPrefix would
// fail: "example.com/m" must not swallow "example.com/mtools".
func TestModuleMap_SegmentBoundary(t *testing.T) {
	m := NewModuleMap(map[string][]byte{
		"go.mod": []byte("module example.com/m\n"),
	})
	if dir, ok := m.Dir("example.com/mtools/x"); ok {
		t.Errorf("example.com/mtools/x must NOT match module example.com/m; got dir %q", dir)
	}
	if dir, ok := m.Dir("example.com/m/x"); !ok || dir != "x" {
		t.Errorf("example.com/m/x must match on the segment boundary; got (%q, %v)", dir, ok)
	}
}

// TestModuleMap_EmptyAndDeterminism pins the no-go.mod case (callers must keep
// their previous behaviour) and byte-identical re-runs.
func TestModuleMap_EmptyAndDeterminism(t *testing.T) {
	if !NewModuleMap(nil).Empty() {
		t.Error("a tree with no go.mod must report Empty")
	}
	if !NewModuleMap(map[string][]byte{"go.mod": []byte("go 1.26\n")}).Empty() {
		t.Error("a go.mod with no module directive leaves the map empty")
	}
	var mp *ModuleMap
	if !mp.Empty() {
		t.Error("a nil ModuleMap must report Empty")
	}
	if _, ok := mp.Dir("anything"); ok {
		t.Error("a nil ModuleMap must resolve nothing")
	}

	// Duplicate module paths at different depths: the shallowest wins, and it
	// wins the same way every time (never map-iteration order).
	in := map[string][]byte{
		"b/go.mod":   []byte("module dup.example/x\n"),
		"a/go.mod":   []byte("module dup.example/x\n"),
		"a/b/go.mod": []byte("module dup.example/x\n"),
	}
	first, _ := NewModuleMap(in).Dir("dup.example/x")
	for i := 0; i < 20; i++ {
		got, _ := NewModuleMap(in).Dir("dup.example/x")
		if got != first {
			t.Fatalf("duplicate module paths must resolve deterministically: %q vs %q", got, first)
		}
	}
	if first != "a" {
		t.Errorf("shallowest-then-lexicographic must win: got %q, want \"a\"", first)
	}
}

// TestGoModPath pins that go.mod is recognised at ANY depth — the predicate the
// cache-invalidation fix keys on, where the old root-only check left nested
// modules able to shift resolution without triggering a re-link.
func TestGoModPath(t *testing.T) {
	for _, p := range []string{"go.mod", "sub/go.mod", "a/b/c/go.mod"} {
		if !GoModPath(p) {
			t.Errorf("GoModPath(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"go.sum", "gomod", "sub/go.mod.bak", "modules/go.mod/x.go"} {
		if GoModPath(p) {
			t.Errorf("GoModPath(%q) = true, want false", p)
		}
	}
}
