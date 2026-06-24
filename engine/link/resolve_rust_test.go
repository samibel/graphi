package link

import (
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
)

// rustScene exercises every Rust resolution class:
//   - use crate::shop::price → bare price()       → heuristic (clause "shop");
//   - mod util; util::helper() selector            → heuristic (base-as-clause "util");
//   - same-directory sibling call (sweep)          → derived;
//   - missing::thing() (unknown module)            → skipped;
//   - use crate::shop::dup, dup in two "shop" dirs  → ambiguous.
func rustScene(t *testing.T) ([]model.Node, []FileRefs) {
	t.Helper()
	nodes := []model.Node{
		mustNode(t, "file", "app/main.rs", "app/main.rs"),
		mustNode(t, "function", "app.checkout", "app/main.rs"),
		mustNode(t, "function", "app.sweep", "app/helpers.rs"), // same dir as checkout

		mustNode(t, "file", "shop/lib.rs", "shop/lib.rs"),
		mustNode(t, "function", "shop.price", "shop/lib.rs"),

		mustNode(t, "file", "util/lib.rs", "util/lib.rs"),
		mustNode(t, "function", "util.helper", "util/lib.rs"),

		// dup declared under clause "shop" in two directories → ambiguous.
		mustNode(t, "file", "x/shop/a.rs", "x/shop/a.rs"),
		mustNode(t, "function", "shop.dup", "x/shop/a.rs"),
		mustNode(t, "file", "y/shop/b.rs", "y/shop/b.rs"),
		mustNode(t, "function", "shop.dup", "y/shop/b.rs"),
	}
	files := []FileRefs{{
		SourcePath: "app/main.rs",
		Dir:        "app",
		Language:   "rust",
		Imports: []parse.ImportSpec{
			{Alias: "", Path: "crate::shop::price"},
			{Alias: "", Path: "crate::shop::dup"},
		},
		Pending: []parse.PendingRef{
			{FromQN: "app.checkout", Name: "price", Kind: "calls", Line: 4, Selector: false},
			{FromQN: "app.checkout", SelectorBase: "util", Name: "helper", Kind: "calls", Line: 5, Selector: true},
			{FromQN: "app.checkout", Name: "sweep", Kind: "calls", Line: 6, Selector: false},
			{FromQN: "app.checkout", SelectorBase: "missing", Name: "thing", Kind: "calls", Line: 7, Selector: true},
			{FromQN: "app.checkout", Name: "dup", Kind: "calls", Line: 8, Selector: false},
		},
	}}
	return nodes, files
}

func TestRustLink_Resolves(t *testing.T) {
	nodes, files := rustScene(t)
	edges, st, err := New().Link("rust", files, BuildIndex(nodes))
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	assertCall(t, nodes, edges, "app.checkout", "shop.price", model.TierHeuristic)
	assertCall(t, nodes, edges, "app.checkout", "util.helper", model.TierHeuristic)
	assertCall(t, nodes, edges, "app.checkout", "app.sweep", model.TierDerived)
	assertNoPhantomNoConfirmed(t, nodes, edges)
	if st.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1 (missing::thing)", st.Skipped)
	}
	if st.Ambiguous != 1 {
		t.Errorf("Ambiguous = %d, want 1 (shop.dup twin dirs)", st.Ambiguous)
	}
}

func TestRustLink_Deterministic(t *testing.T) {
	assertOrderIndependent(t, "rust", rustScene)
}
