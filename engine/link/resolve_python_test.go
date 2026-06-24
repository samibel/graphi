package link

import (
	"math/rand"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
)

// pyScene builds a multi-package Python repo exercising every resolution class the
// Python resolver models:
//   - import alias selector (rates.compute via `import tax.rates as rates`) → heuristic;
//   - from-import bare binding (build via `from shop.cart import build`)     → heuristic;
//   - same-directory bare call (helper, defined alongside)                   → derived;
//   - stdlib selector (json.loads)                                           → skipped;
//   - a name declared under the same clause in two dirs (dup)                → ambiguous.
func pyScene(t *testing.T) ([]model.Node, []FileRefs) {
	t.Helper()
	nodes := []model.Node{
		mustNode(t, "file", "app/main.py", "app/main.py"),
		mustNode(t, "function", "app.checkout", "app/main.py"),
		mustNode(t, "function", "app.helper", "app/main.py"), // same-dir as checkout

		// package shop/cart (clause "cart"): build.
		mustNode(t, "file", "shop/cart/build.py", "shop/cart/build.py"),
		mustNode(t, "function", "cart.build", "shop/cart/build.py"),

		// package tax/rates (clause "rates"): compute.
		mustNode(t, "file", "tax/rates/calc.py", "tax/rates/calc.py"),
		mustNode(t, "function", "rates.compute", "tax/rates/calc.py"),

		// Two directories both declaring clause "pkg", each with dup → ambiguous.
		mustNode(t, "file", "a/pkg/x.py", "a/pkg/x.py"),
		mustNode(t, "function", "pkg.dup", "a/pkg/x.py"),
		mustNode(t, "file", "b/pkg/y.py", "b/pkg/y.py"),
		mustNode(t, "function", "pkg.dup", "b/pkg/y.py"),
	}

	files := []FileRefs{{
		SourcePath: "app/main.py",
		Dir:        "app",
		Language:   "python",
		Imports: []parse.ImportSpec{
			{Alias: "rates", Path: "tax.rates"},  // import tax.rates as rates
			{Alias: "build", Path: "shop.cart"},  // from shop.cart import build
			{Alias: "json", Path: "json"},        // import json (stdlib)
			{Alias: "dup", Path: "pkg"},          // from pkg import dup (ambiguous)
		},
		Pending: []parse.PendingRef{
			{FromQN: "app.checkout", SelectorBase: "rates", Name: "compute", Kind: "calls", Line: 5, Selector: true},
			{FromQN: "app.checkout", Name: "build", Kind: "calls", Line: 6, Selector: false},
			{FromQN: "app.checkout", Name: "helper", Kind: "calls", Line: 7, Selector: false},
			{FromQN: "app.checkout", SelectorBase: "json", Name: "loads", Kind: "calls", Line: 8, Selector: true},
			{FromQN: "app.checkout", Name: "dup", Kind: "calls", Line: 9, Selector: false},
		},
	}}
	return nodes, files
}

func TestPyLink_ResolvesCrossModule(t *testing.T) {
	nodes, files := pyScene(t)
	idx := BuildIndex(nodes)
	edges, st, err := New().Link("python", files, idx)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}

	hasCall := func(fromQN, toQN string, tier model.ConfidenceTier) {
		from, to := idOfQN(t, nodes, fromQN), idOfQN(t, nodes, toQN)
		for _, e := range edges {
			if e.From() == from && e.To() == to && e.Kind() == "calls" {
				if e.Tier() != tier {
					t.Errorf("%s->%s tier=%q want %q", fromQN, toQN, e.Tier(), tier)
				}
				return
			}
		}
		t.Errorf("missing calls edge %s -> %s", fromQN, toQN)
	}

	// import-alias selector and from-import bare binding both resolve cross-module.
	hasCall("app.checkout", "rates.compute", model.TierHeuristic)
	hasCall("app.checkout", "cart.build", model.TierHeuristic)
	// same-directory call is derived.
	hasCall("app.checkout", "app.helper", model.TierDerived)

	// No phantom, never confirmed.
	known := map[model.NodeId]struct{}{}
	for _, n := range nodes {
		known[n.ID()] = struct{}{}
	}
	for _, e := range edges {
		if _, ok := known[e.To()]; !ok {
			t.Errorf("edge to unknown target %s", e.To())
		}
		if e.Tier() == model.TierConfirmed {
			t.Errorf("linker emitted a confirmed edge: %s", e.ID())
		}
	}

	// json.loads is stdlib → skipped; dup is ambiguous across two "pkg" dirs.
	if st.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1 (json.loads)", st.Skipped)
	}
	if st.Ambiguous != 1 {
		t.Errorf("Ambiguous = %d, want 1 (pkg.dup twin dirs)", st.Ambiguous)
	}
	if st.ResolvedDerived != 1 {
		t.Errorf("ResolvedDerived = %d, want 1 (helper)", st.ResolvedDerived)
	}
}

func TestPyLink_OrderIndependentAndIdempotent(t *testing.T) {
	nodes, files := pyScene(t)
	idx := BuildIndex(nodes)
	base, _, err := New().Link("python", files, idx)
	if err != nil {
		t.Fatal(err)
	}
	again, _, err := New().Link("python", files, idx)
	if err != nil {
		t.Fatal(err)
	}
	if !edgesDeepEqual(base, again) {
		t.Fatalf("Python Link not idempotent:\n%v\n%v", dump(base), dump(again))
	}
	rng := rand.New(rand.NewSource(11))
	for iter := 0; iter < 20; iter++ {
		shNodes := append([]model.Node(nil), nodes...)
		rng.Shuffle(len(shNodes), func(i, j int) { shNodes[i], shNodes[j] = shNodes[j], shNodes[i] })
		shFiles := []FileRefs{files[0]}
		p := append([]parse.PendingRef(nil), files[0].Pending...)
		rng.Shuffle(len(p), func(i, j int) { p[i], p[j] = p[j], p[i] })
		shFiles[0].Pending = p
		got, _, err := New().Link("python", shFiles, BuildIndex(shNodes))
		if err != nil {
			t.Fatal(err)
		}
		if !edgesDeepEqual(base, got) {
			t.Fatalf("Python order-dependence at iter %d:\nbase=%v\ngot =%v", iter, dump(base), dump(got))
		}
	}
}
