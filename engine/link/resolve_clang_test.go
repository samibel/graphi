package link

import (
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
)

// cScene exercises the #include translation-unit model:
//   - #include "shared/util.h"; helper() → heuristic (ambient include dir);
//   - same-directory sibling()           → derived;
//   - dup() defined in two included dirs  → ambiguous (D2: no overload disambiguation);
//   - missing()                          → skipped;
//   - #include <stdio.h> (no node)        → no phantom import edge.
func cScene(t *testing.T) ([]model.Node, []FileRefs) {
	t.Helper()
	nodes := []model.Node{
		mustNode(t, "file", "app/main.c", "app/main.c"),
		mustNode(t, "function", "app.checkout", "app/main.c"),
		mustNode(t, "function", "app.sibling", "app/other.c"),

		mustNode(t, "file", "app/shared/util.h", "app/shared/util.h"),
		mustNode(t, "file", "app/shared/util.c", "app/shared/util.c"),
		mustNode(t, "function", "shared.helper", "app/shared/util.c"),

		mustNode(t, "file", "app/p/h.h", "app/p/h.h"),
		mustNode(t, "function", "p.dup", "app/p/x.c"),
		mustNode(t, "file", "app/q/h.h", "app/q/h.h"),
		mustNode(t, "function", "q.dup", "app/q/y.c"),
	}
	files := []FileRefs{{
		SourcePath: "app/main.c",
		Dir:        "app",
		Language:   "c",
		Imports: []parse.ImportSpec{
			{Path: "shared/util.h"},
			{Path: "p/h.h"},
			{Path: "q/h.h"},
			{Path: "stdio.h"}, // system header → no committed node
		},
		Pending: []parse.PendingRef{
			{FromQN: "app.checkout", Name: "helper", Kind: "calls", Line: 3, Selector: false},
			{FromQN: "app.checkout", Name: "sibling", Kind: "calls", Line: 4, Selector: false},
			{FromQN: "app.checkout", Name: "dup", Kind: "calls", Line: 5, Selector: false},
			{FromQN: "app.checkout", Name: "missing", Kind: "calls", Line: 6, Selector: false},
		},
	}}
	return nodes, files
}

func TestCLink_Resolves(t *testing.T) {
	nodes, files := cScene(t)
	edges, st, err := New().Link("c", files, BuildIndex(nodes))
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	assertCall(t, nodes, edges, "app.checkout", "shared.helper", model.TierHeuristic)
	assertCall(t, nodes, edges, "app.checkout", "app.sibling", model.TierDerived)
	assertNoPhantomNoConfirmed(t, nodes, edges)
	if st.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1 (missing)", st.Skipped)
	}
	if st.Ambiguous != 1 {
		t.Errorf("Ambiguous = %d, want 1 (dup across two include dirs)", st.Ambiguous)
	}

	// file→file imports edge to the committed local header; none to <stdio.h>.
	mainFile := idOfQN(t, nodes, "app/main.c")
	util := idOfQN(t, nodes, "app/shared/util.h")
	var foundImport bool
	for _, e := range edges {
		if e.From() == mainFile && e.To() == util && e.Kind() == "imports" {
			foundImport = true
		}
	}
	if !foundImport {
		t.Error("missing imports edge main.c -> shared/util.h")
	}
}

func TestCLink_Deterministic(t *testing.T) { assertOrderIndependent(t, "c", cScene) }

// TestCppLink_Resolves reuses the include model for C++ (D2: overloads not
// disambiguated; ambiguity is counted).
func TestCppLink_Resolves(t *testing.T) {
	nodes := []model.Node{
		mustNode(t, "file", "app/main.cpp", "app/main.cpp"),
		mustNode(t, "function", "app.run", "app/main.cpp"),
		mustNode(t, "file", "app/lib/util.hpp", "app/lib/util.hpp"),
		mustNode(t, "function", "lib.helper", "app/lib/util.cpp"),
	}
	files := []FileRefs{{
		SourcePath: "app/main.cpp",
		Dir:        "app",
		Language:   "cpp",
		Imports:    []parse.ImportSpec{{Path: "lib/util.hpp"}},
		Pending: []parse.PendingRef{
			{FromQN: "app.run", Name: "helper", Kind: "calls", Line: 2, Selector: false},
		},
	}}
	edges, _, err := New().Link("cpp", files, BuildIndex(nodes))
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	assertCall(t, nodes, edges, "app.run", "lib.helper", model.TierHeuristic)
	assertNoPhantomNoConfirmed(t, nodes, edges)
}
