package link

import (
	"math/rand"
	"reflect"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
)

// mustNode builds a symbol node or fails the test.
func mustNode(t *testing.T, kind, qn, src string) model.Node {
	t.Helper()
	n, err := model.NewNode(kind, qn, src, 1, 1)
	if err != nil {
		t.Fatalf("NewNode(%q,%q,%q): %v", kind, qn, src, err)
	}
	return n
}

// scene builds a small multi-file, multi-package committed node set and the
// matching FileRefs, exercising same-package, cross-package, recv.Method, and
// unresolvable (stdlib) references.
func scene(t *testing.T) ([]model.Node, []FileRefs) {
	t.Helper()
	nodes := []model.Node{
		// package shop, dir "shop": checkout (cart.go) calls price (price.go).
		mustNode(t, "file", "shop/cart.go", "shop/cart.go"),
		mustNode(t, "file", "shop/price.go", "shop/price.go"),
		mustNode(t, "function", "shop.checkout", "shop/cart.go"),
		mustNode(t, "function", "shop.price", "shop/price.go"),
		mustNode(t, "method", "shop.Cart.Add", "shop/cart.go"),
		mustNode(t, "type", "shop.Cart", "shop/cart.go"),
		// package tax, dir "tax": Rate, used cross-package as tax.Rate.
		mustNode(t, "file", "tax/tax.go", "tax/tax.go"),
		mustNode(t, "function", "tax.Rate", "tax/tax.go"),
	}

	files := []FileRefs{
		{
			SourcePath: "shop/cart.go",
			Dir:        "shop",
			Imports:    []parse.ImportSpec{{Alias: "", Path: "example.com/tax"}, {Alias: "", Path: "fmt"}},
			Pending: []parse.PendingRef{
				// same-package cross-file call checkout -> price
				{FromQN: "shop.checkout", Name: "price", Kind: "calls", Line: 5, Selector: false},
				// cross-package call checkout -> tax.Rate
				{FromQN: "shop.checkout", SelectorBase: "tax", Name: "Rate", Kind: "calls", Line: 6, Selector: true},
				// stdlib selector fmt.Println -> unresolvable, skipped
				{FromQN: "shop.checkout", SelectorBase: "fmt", Name: "Println", Kind: "calls", Line: 7, Selector: true},
				// receiver-method call checkout -> Cart.Add (recv "c")
				{FromQN: "shop.checkout", SelectorBase: "c", Name: "Add", Kind: "calls", Line: 8, Selector: true},
			},
		},
	}
	return nodes, files
}

func TestLink_Resolves(t *testing.T) {
	nodes, files := scene(t)
	idx := BuildIndex(nodes)
	edges, st, err := New().Link("go", files, idx)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}

	id := func(qn string) model.NodeId {
		for _, n := range nodes {
			if n.QualifiedName() == qn {
				return n.ID()
			}
		}
		t.Fatalf("no node %q", qn)
		return ""
	}
	has := func(from, to model.NodeId, kind string, tier model.ConfidenceTier) model.Edge {
		for _, e := range edges {
			if e.From() == from && e.To() == to && e.Kind() == kind {
				if e.Tier() != tier {
					t.Errorf("edge %s->%s tier=%q want %q", from, to, e.Tier(), tier)
				}
				if e.Reason() == "" || len(e.Evidence()) == 0 {
					t.Errorf("edge %s->%s missing reason/evidence", from, to)
				}
				return e
			}
		}
		t.Errorf("missing edge %s -> %s (%s)", from, to, kind)
		return model.Edge{}
	}

	// same-package derived call
	has(id("shop.checkout"), id("shop.price"), "calls", model.TierDerived)
	// cross-package heuristic call
	has(id("shop.checkout"), id("tax.Rate"), "calls", model.TierHeuristic)
	// recv.Method heuristic call
	has(id("shop.checkout"), id("shop.Cart.Add"), "calls", model.TierHeuristic)

	// No edge may point at an unknown target.
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

	// fmt.Println was the only stdlib selector ⇒ exactly one skip.
	if st.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1 (fmt.Println)", st.Skipped)
	}
	if st.ResolvedDerived != 1 {
		t.Errorf("ResolvedDerived = %d, want 1", st.ResolvedDerived)
	}
}

func TestLink_TierConstantMap(t *testing.T) {
	tier, conf := tierFor(classSamePackage)
	if tier != model.TierDerived || conf != 0.9 {
		t.Errorf("classSamePackage -> (%q,%v), want (derived,0.9)", tier, conf)
	}
	tier, conf = tierFor(classSelector)
	if tier != model.TierHeuristic || conf != 0.6 {
		t.Errorf("classSelector -> (%q,%v), want (heuristic,0.6)", tier, conf)
	}
}

func TestLink_Idempotent(t *testing.T) {
	nodes, files := scene(t)
	idx := BuildIndex(nodes)
	l := New()
	e1, _, err := l.Link("go", files, idx)
	if err != nil {
		t.Fatal(err)
	}
	e2, _, err := l.Link("go", files, idx)
	if err != nil {
		t.Fatal(err)
	}
	if !edgesDeepEqual(e1, e2) {
		t.Fatalf("Link not idempotent:\n%v\n%v", dump(e1), dump(e2))
	}
}

func TestLink_OrderIndependent(t *testing.T) {
	nodes, files := scene(t)
	// Add a multi-call-site case: two pending refs for the same logical edge with
	// different evidence lines must merge into one edge with sorted-union evidence.
	files[0].Pending = append(files[0].Pending,
		parse.PendingRef{FromQN: "shop.checkout", Name: "price", Kind: "calls", Line: 9, Selector: false})

	idx := BuildIndex(nodes)
	base, _, err := New().Link("go", files, idx)
	if err != nil {
		t.Fatal(err)
	}

	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 20; iter++ {
		// Shuffle nodes and pending refs.
		shNodes := append([]model.Node(nil), nodes...)
		rng.Shuffle(len(shNodes), func(i, j int) { shNodes[i], shNodes[j] = shNodes[j], shNodes[i] })
		shFiles := []FileRefs{files[0]}
		p := append([]parse.PendingRef(nil), files[0].Pending...)
		rng.Shuffle(len(p), func(i, j int) { p[i], p[j] = p[j], p[i] })
		shFiles[0].Pending = p

		got, _, err := New().Link("go", shFiles, BuildIndex(shNodes))
		if err != nil {
			t.Fatal(err)
		}
		if !edgesDeepEqual(base, got) {
			t.Fatalf("order-dependence at iter %d:\nbase=%v\ngot =%v", iter, dump(base), dump(got))
		}
	}

	// The merged checkout->price edge must carry BOTH evidence lines (sorted).
	id := func(qn string) model.NodeId {
		for _, n := range nodes {
			if n.QualifiedName() == qn {
				return n.ID()
			}
		}
		return ""
	}
	for _, e := range base {
		if e.From() == id("shop.checkout") && e.To() == id("shop.price") {
			if len(e.Evidence()) != 2 {
				t.Errorf("multi-call-site evidence not unioned: %v", e.Evidence())
			}
		}
	}
}

// TestLink_Honesty mixes resolvable refs with stdlib/3rd-party/unresolvable
// recv.Method refs and asserts the exact resolvable count, zero unknown targets,
// no error, and full provenance on every edge.
func TestLink_Honesty(t *testing.T) {
	nodes := []model.Node{
		mustNode(t, "file", "a/a.go", "a/a.go"),
		mustNode(t, "function", "a.Caller", "a/a.go"),
		mustNode(t, "function", "a.Local", "a/a.go"),
	}
	files := []FileRefs{{
		SourcePath: "a/a.go",
		Dir:        "a",
		Imports:    []parse.ImportSpec{{Path: "fmt"}, {Path: "github.com/x/y"}},
		Pending: []parse.PendingRef{
			{FromQN: "a.Caller", Name: "Local", Kind: "calls", Line: 2},                                        // resolvable same-package
			{FromQN: "a.Caller", SelectorBase: "fmt", Name: "Println", Kind: "calls", Line: 3, Selector: true}, // stdlib
			{FromQN: "a.Caller", SelectorBase: "y", Name: "Do", Kind: "calls", Line: 4, Selector: true},        // 3rd-party unindexed
			{FromQN: "a.Caller", SelectorBase: "obj", Name: "Method", Kind: "calls", Line: 5, Selector: true},  // unresolvable recv
		},
	}}
	edges, st, err := New().Link("go", files, BuildIndex(nodes))
	if err != nil {
		t.Fatalf("Link returned error on unresolvable refs: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("want exactly 1 resolvable edge, got %d: %v", len(edges), dump(edges))
	}
	if st.Skipped != 3 {
		t.Errorf("Skipped = %d, want 3", st.Skipped)
	}
	for _, e := range edges {
		if e.Reason() == "" || len(e.Evidence()) == 0 || !e.Tier().Valid() {
			t.Errorf("edge lacks provenance: %+v", e)
		}
	}
}

// TestLink_MaliciousPathEvidence asserts evidence is repo-relative POSIX with no
// absolute/traversal/host-revealing content even for hostile source paths.
func TestLink_MaliciousPathEvidence(t *testing.T) {
	// model.NewNode normalizes the path on the node, so the index keys on the
	// normalized form; the resolver must use the same normalized path in evidence.
	nodes := []model.Node{
		mustNode(t, "file", "../../../etc/passwd/x.go", "../../../etc/passwd/x.go"),
		mustNode(t, "function", "x.Caller", "../../../etc/passwd/x.go"),
		mustNode(t, "function", "x.Callee", "../../../etc/passwd/x.go"),
	}
	// Determine the normalized dir the node landed in.
	dir := ""
	for _, n := range nodes {
		if n.Kind() == "function" {
			dir = posixDir(n.SourcePath())
			break
		}
	}
	files := []FileRefs{{
		SourcePath: nodes[0].SourcePath(),
		Dir:        dir,
		Pending:    []parse.PendingRef{{FromQN: "x.Caller", Name: "Callee", Kind: "calls", Line: 3}},
	}}
	edges, _, err := New().Link("go", files, BuildIndex(nodes))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range edges {
		for _, ev := range e.Evidence() {
			if len(ev) > 0 && ev[0] == '/' {
				t.Errorf("evidence is absolute: %q", ev)
			}
			if containsTraversal(ev) {
				t.Errorf("evidence contains traversal: %q", ev)
			}
		}
	}
}

func containsTraversal(s string) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '.' && s[i+1] == '.' {
			return true
		}
	}
	return false
}

func TestLink_NoResolverIsNoOp(t *testing.T) {
	edges, _, err := New().Link("python", nil, BuildIndex(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 0 {
		t.Fatalf("unregistered language should yield no edges, got %d", len(edges))
	}
}

// edgesDeepEqual compares two edge slices on the FULL provenance tuple, not just
// EdgeId — the byte-level determinism contract.
func edgesDeepEqual(a, b []model.Edge) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID() != b[i].ID() ||
			a[i].From() != b[i].From() ||
			a[i].To() != b[i].To() ||
			a[i].Kind() != b[i].Kind() ||
			a[i].Tier() != b[i].Tier() ||
			a[i].Confidence() != b[i].Confidence() ||
			a[i].Reason() != b[i].Reason() ||
			!reflect.DeepEqual(a[i].Evidence(), b[i].Evidence()) {
			return false
		}
	}
	return true
}

func dump(edges []model.Edge) []string {
	out := make([]string, 0, len(edges))
	for _, e := range edges {
		out = append(out, string(e.ID())+" "+string(e.From())+"->"+string(e.To())+" "+e.Kind()+" "+string(e.Tier()))
	}
	return out
}
