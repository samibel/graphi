package link

import (
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
)

func javaScene(t *testing.T) ([]model.Node, []FileRefs) {
	t.Helper()
	nodes := []model.Node{
		mustNode(t, "file", "com/app/Main.java", "com/app/Main.java"),
		mustNode(t, "type", "app.Main", "com/app/Main.java"),
		mustNode(t, "method", "app.checkout", "com/app/Main.java"),
		mustNode(t, "method", "app.helper", "com/app/Main.java"), // same dir → derived

		mustNode(t, "file", "com/shop/Price.java", "com/shop/Price.java"),
		mustNode(t, "method", "shop.of", "com/shop/Price.java"),

		// dup under clause "x" in two dirs → ambiguous.
		mustNode(t, "file", "a/x/A.java", "a/x/A.java"),
		mustNode(t, "method", "x.dup", "a/x/A.java"),
		mustNode(t, "file", "b/x/B.java", "b/x/B.java"),
		mustNode(t, "method", "x.dup", "b/x/B.java"),
	}
	files := []FileRefs{{
		SourcePath: "com/app/Main.java",
		Dir:        "com/app",
		Language:   "java",
		Imports: []parse.ImportSpec{
			{Alias: "Price", Path: "com.shop.Price"},
			{Alias: "Other", Path: "com.x.Other"},
		},
		Pending: []parse.PendingRef{
			{FromQN: "app.checkout", SelectorBase: "Price", Name: "of", Kind: "calls", Line: 4, Selector: true},
			{FromQN: "app.checkout", Name: "helper", Kind: "calls", Line: 5, Selector: false},
			{FromQN: "app.checkout", SelectorBase: "p", Name: "value", Kind: "calls", Line: 6, Selector: true},
			{FromQN: "app.checkout", SelectorBase: "Other", Name: "dup", Kind: "calls", Line: 7, Selector: true},
		},
	}}
	return nodes, files
}

func TestJavaLink_Resolves(t *testing.T) {
	nodes, files := javaScene(t)
	_, edges, st, err := New().Link("java", files, BuildIndex(nodes))
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	assertCall(t, nodes, edges, "app.checkout", "shop.of", model.TierHeuristic)
	assertCall(t, nodes, edges, "app.checkout", "app.helper", model.TierDerived)
	assertNoPhantomNoConfirmed(t, nodes, edges)
	if st.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1 (instance call p.value)", st.Skipped)
	}
	if st.Ambiguous != 1 {
		t.Errorf("Ambiguous = %d, want 1 (x.dup twin dirs)", st.Ambiguous)
	}
}

func TestJavaLink_Deterministic(t *testing.T) { assertOrderIndependent(t, "java", javaScene) }

func kotlinScene(t *testing.T) ([]model.Node, []FileRefs) {
	t.Helper()
	nodes := []model.Node{
		mustNode(t, "file", "com/app/Main.kt", "com/app/Main.kt"),
		mustNode(t, "function", "app.checkout", "com/app/Main.kt"),
		mustNode(t, "file", "com/shop/api.kt", "com/shop/api.kt"),
		mustNode(t, "function", "shop.price", "com/shop/api.kt"),
	}
	files := []FileRefs{{
		SourcePath: "com/app/Main.kt",
		Dir:        "com/app",
		Language:   "kotlin",
		Imports:    []parse.ImportSpec{{Alias: "", Path: "com.shop.price"}},
		Pending: []parse.PendingRef{
			{FromQN: "app.checkout", Name: "price", Kind: "calls", Line: 3, Selector: false},
		},
	}}
	return nodes, files
}

func TestKotlinLink_Resolves(t *testing.T) {
	nodes, files := kotlinScene(t)
	_, edges, _, err := New().Link("kotlin", files, BuildIndex(nodes))
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	assertCall(t, nodes, edges, "app.checkout", "shop.price", model.TierHeuristic)
	assertNoPhantomNoConfirmed(t, nodes, edges)
}

func csharpScene(t *testing.T) ([]model.Node, []FileRefs) {
	t.Helper()
	nodes := []model.Node{
		mustNode(t, "file", "App/Main.cs", "App/Main.cs"),
		mustNode(t, "type", "App.Main", "App/Main.cs"),
		mustNode(t, "method", "App.Checkout", "App/Main.cs"),
		mustNode(t, "file", "Shop/Price.cs", "Shop/Price.cs"),
		mustNode(t, "method", "Shop.Of", "Shop/Price.cs"),
	}
	files := []FileRefs{{
		SourcePath: "App/Main.cs",
		Dir:        "App",
		Language:   "c_sharp",
		Imports:    []parse.ImportSpec{{Alias: "", Path: "Shop"}},
		Pending: []parse.PendingRef{
			{FromQN: "App.Checkout", SelectorBase: "Price", Name: "Of", Kind: "calls", Line: 3, Selector: true},
		},
	}}
	return nodes, files
}

func TestCSharpLink_Resolves(t *testing.T) {
	nodes, files := csharpScene(t)
	_, edges, _, err := New().Link("c_sharp", files, BuildIndex(nodes))
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	assertCall(t, nodes, edges, "App.Checkout", "Shop.Of", model.TierHeuristic)
	assertNoPhantomNoConfirmed(t, nodes, edges)
}

func TestCSharpLink_Deterministic(t *testing.T) { assertOrderIndependent(t, "c_sharp", csharpScene) }
