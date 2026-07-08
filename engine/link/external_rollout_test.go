package link

import (
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
)

// externalTargets returns the qualified names of the linker-minted external nodes.
func externalTargets(t *testing.T, nodes []model.Node) []string {
	t.Helper()
	var out []string
	for _, n := range nodes {
		if n.Kind() == parse.KindExternal {
			out = append(out, n.QualifiedName())
		}
	}
	return out
}

// TestExternalRollout_TSNonRelativeNeverResolvesInRepo is the WP-14 regression
// for the adversarial finding A: a TS non-relative package import must NEVER
// resolve to an in-repo node via a clause-basename collision (the index is global
// across languages). It must produce ONLY an external node, never a false edge.
func TestExternalRollout_TSNonRelativeNeverResolvesInRepo(t *testing.T) {
	// A Python-side symbol keyed by clause "config" (dir "config"), plus a TS caller
	// importing a name from the non-relative specifier "@app/config" — whose basename
	// is also "config". Before the fix, crossModule("config","parseConfig") matched
	// the Python node and drew a bogus cross-language edge.
	nodes := []model.Node{
		mustNode(t, "file", "web/app.ts", "web/app.ts"),
		mustNode(t, "function", "web.render", "web/app.ts"),
		mustNode(t, "file", "config/settings.py", "config/settings.py"),
		mustNode(t, "function", "config.parseConfig", "config/settings.py"),
	}
	files := []FileRefs{{
		SourcePath: "web/app.ts",
		Dir:        "web",
		Language:   "typescript",
		Imports:    []parse.ImportSpec{{Alias: "parseConfig", Path: "@app/config"}},
		Pending: []parse.PendingRef{
			{FromQN: "web.render", Name: "parseConfig", Kind: "calls", Line: 3, Selector: false},
		},
	}}
	idx := BuildIndex(nodes)
	extNodes, edges, st, err := New().Link("typescript", files, idx)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}

	// No edge may target the Python node — the D1 relative-only rule forbids it.
	pyID := idOfQN(t, nodes, "config.parseConfig")
	for _, e := range edges {
		if e.To() == pyID {
			t.Errorf("TS non-relative import drew a false cross-language edge to %s", pyID)
		}
	}
	// The reference materializes as the package-qualified external node instead.
	if got := externalTargets(t, extNodes); len(got) != 1 || got[0] != "@app/config.parseConfig" {
		t.Errorf("external nodes = %v, want [@app/config.parseConfig]", got)
	}
	if st.ResolvedExternal != 1 {
		t.Errorf("ResolvedExternal = %d, want 1", st.ResolvedExternal)
	}
}

// TestExternalRollout_PythonRelativeImportNeverExternal is the WP-14 regression
// for the adversarial finding B: a Python RELATIVE import (`from . import x`)
// targets an in-repo module, so an unresolved use must be an honest skip — never a
// fabricated external node (and never a nonsense "x.x" FQN).
func TestExternalRollout_PythonRelativeImportNeverExternal(t *testing.T) {
	nodes := []model.Node{
		mustNode(t, "file", "pkg/main.py", "pkg/main.py"),
		mustNode(t, "function", "pkg.run", "pkg/main.py"),
	}
	files := []FileRefs{{
		SourcePath: "pkg/main.py",
		Dir:        "pkg",
		Language:   "python",
		Imports: []parse.ImportSpec{
			{Alias: "sibling", Path: "sibling", Relative: true}, // from . import sibling
			{Alias: "x", Path: "x", Relative: true},             // from . import x
		},
		Pending: []parse.PendingRef{
			{FromQN: "pkg.run", SelectorBase: "sibling", Name: "helper", Kind: "calls", Line: 3, Selector: true},
			{FromQN: "pkg.run", Name: "x", Kind: "calls", Line: 4, Selector: false},
		},
	}}
	idx := BuildIndex(nodes)
	extNodes, _, st, err := New().Link("python", files, idx)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if got := externalTargets(t, extNodes); len(got) != 0 {
		t.Errorf("relative imports minted external nodes %v, want none", got)
	}
	if st.ResolvedExternal != 0 {
		t.Errorf("ResolvedExternal = %d, want 0 (relative imports are in-repo)", st.ResolvedExternal)
	}
	// The two unresolved relative uses are honest skips.
	if st.Skipped != 2 {
		t.Errorf("Skipped = %d, want 2 (both relative uses)", st.Skipped)
	}
}

// TestExternalRollout_PythonNonRelativeStillExternal guards that the fix for B did
// NOT suppress genuine stdlib externals: a non-relative `import subprocess` whose
// member call is unresolved still materializes the external sink node.
func TestExternalRollout_PythonNonRelativeStillExternal(t *testing.T) {
	nodes := []model.Node{
		mustNode(t, "file", "app/run.py", "app/run.py"),
		mustNode(t, "function", "app.go", "app/run.py"),
	}
	files := []FileRefs{{
		SourcePath: "app/run.py",
		Dir:        "app",
		Language:   "python",
		Imports:    []parse.ImportSpec{{Alias: "subprocess", Path: "subprocess"}}, // import subprocess
		Pending: []parse.PendingRef{
			{FromQN: "app.go", SelectorBase: "subprocess", Name: "run", Kind: "calls", Line: 3, Selector: true},
		},
	}}
	idx := BuildIndex(nodes)
	extNodes, _, st, err := New().Link("python", files, idx)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if got := externalTargets(t, extNodes); len(got) != 1 || got[0] != "subprocess.run" {
		t.Errorf("external nodes = %v, want [subprocess.run]", got)
	}
	if st.ResolvedExternal != 1 {
		t.Errorf("ResolvedExternal = %d, want 1", st.ResolvedExternal)
	}
}
