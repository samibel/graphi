package link

import (
	"math/rand"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
)

// tsScene builds a multi-directory TypeScript repo exercising every resolution
// class the TS resolver must model:
//   - cross-file NAMED import binding (greet from ../lib/util) → heuristic;
//   - cross-file NAMESPACE selector (mathx.add from ../lib/calc) → heuristic;
//   - non-relative import (react) → external, skipped (D1);
//   - a name present in BOTH candidate dirs of one relative import (Cfg) → ambiguous.
func tsScene(t *testing.T) ([]model.Node, []FileRefs) {
	t.Helper()
	nodes := []model.Node{
		mustNode(t, "file", "app/main.ts", "app/main.ts"),
		mustNode(t, "function", "app.run", "app/main.ts"),

		mustNode(t, "file", "lib/util.ts", "lib/util.ts"),
		mustNode(t, "function", "lib.greet", "lib/util.ts"),

		mustNode(t, "file", "lib/calc.ts", "lib/calc.ts"),
		mustNode(t, "function", "lib.add", "lib/calc.ts"),

		// Ambiguous twin: Cfg exists both as x/shared.ts (dir "x") and as the
		// directory-module x/shared/index.ts (dir "x/shared"); an import of
		// "../x/shared" offers both dirs as candidates → ambiguous, no edge.
		mustNode(t, "file", "x/shared.ts", "x/shared.ts"),
		mustNode(t, "type", "x.Cfg", "x/shared.ts"),
		mustNode(t, "file", "x/shared/index.ts", "x/shared/index.ts"),
		mustNode(t, "type", "shared.Cfg", "x/shared/index.ts"),
	}

	files := []FileRefs{{
		SourcePath: "app/main.ts",
		Dir:        "app",
		Language:   "typescript",
		Imports: []parse.ImportSpec{
			{Alias: "greet", Path: "../lib/util"}, // named import
			{Alias: "mathx", Path: "../lib/calc"}, // namespace import
			{Alias: "useState", Path: "react"},    // non-relative → external (D1)
			{Alias: "Cfg", Path: "../x/shared"},   // ambiguous twin dirs
		},
		Pending: []parse.PendingRef{
			{FromQN: "app.run", Name: "greet", Kind: "calls", Line: 5, Selector: false},
			{FromQN: "app.run", SelectorBase: "mathx", Name: "add", Kind: "calls", Line: 6, Selector: true},
			{FromQN: "app.run", Name: "useState", Kind: "calls", Line: 7, Selector: false},
			{FromQN: "app.run", Name: "Cfg", Kind: "references", Line: 8, Selector: false},
		},
	}}
	return nodes, files
}

func idOfQN(t *testing.T, nodes []model.Node, qn string) model.NodeId {
	t.Helper()
	for _, n := range nodes {
		if n.QualifiedName() == qn {
			return n.ID()
		}
	}
	t.Fatalf("no node %q", qn)
	return ""
}

func TestTSLink_ResolvesCrossFile(t *testing.T) {
	nodes, files := tsScene(t)
	idx := BuildIndex(nodes)
	extNodes, edges, st, err := New().Link("typescript", files, idx)
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
				if e.Reason() == "" || len(e.Evidence()) == 0 {
					t.Errorf("%s->%s missing reason/evidence", fromQN, toQN)
				}
				return
			}
		}
		t.Errorf("missing calls edge %s -> %s", fromQN, toQN)
	}

	// Named-import bare call and namespace selector both resolve cross-file (heuristic).
	hasCall("app.run", "lib.greet", model.TierHeuristic)
	hasCall("app.run", "lib.add", model.TierHeuristic)

	// WP-14: react.useState (non-relative package import, clause "react" absent from
	// the repo) is now MATERIALIZED as one interned external node "react.useState".
	known := map[model.NodeId]struct{}{}
	for _, n := range nodes {
		known[n.ID()] = struct{}{}
	}
	var extQNs []string
	for _, n := range extNodes {
		if n.Kind() != "external" {
			t.Errorf("minted node %s kind = %q, want external", n.ID(), n.Kind())
		}
		known[n.ID()] = struct{}{}
		extQNs = append(extQNs, n.QualifiedName())
	}
	for _, e := range edges {
		if _, ok := known[e.To()]; !ok {
			t.Errorf("edge to unknown target %s", e.To())
		}
		if e.Tier() == model.TierConfirmed {
			t.Errorf("linker emitted a confirmed edge: %s", e.ID())
		}
	}

	if len(extNodes) != 1 || extNodes[0].QualifiedName() != "react.useState" {
		t.Fatalf("external nodes = %v, want exactly [react.useState]", extQNs)
	}
	assertEdgeTier(t, edges, idOfQN(t, nodes, "app.run"), extNodes[0].ID(), "calls", model.TierHeuristic)
	if st.ResolvedExternal != 1 {
		t.Errorf("ResolvedExternal = %d, want 1 (react.useState)", st.ResolvedExternal)
	}
	// react.useState is materialized (not skipped); Cfg stays ambiguous across the
	// relative twin dirs (a relative-path miss is never fabricated as external).
	if st.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0 (react.useState now external)", st.Skipped)
	}
	if st.Ambiguous != 1 {
		t.Errorf("Ambiguous = %d, want 1 (Cfg twin dirs)", st.Ambiguous)
	}

	// File→file imports edges land for the two committed relative modules.
	mainFile := idOfQN(t, nodes, "app/main.ts")
	wantImports := []string{"lib/util.ts", "lib/calc.ts"}
	for _, tgtPath := range wantImports {
		tgt := idOfQN(t, nodes, tgtPath)
		found := false
		for _, e := range edges {
			if e.From() == mainFile && e.To() == tgt && e.Kind() == "imports" {
				found = true
			}
		}
		if !found {
			t.Errorf("missing imports edge main.ts -> %s", tgtPath)
		}
	}
}

func TestTSLink_OrderIndependentAndIdempotent(t *testing.T) {
	nodes, files := tsScene(t)
	idx := BuildIndex(nodes)
	_, base, _, err := New().Link("typescript", files, idx)
	if err != nil {
		t.Fatal(err)
	}
	// Idempotent.
	_, again, _, err := New().Link("typescript", files, idx)
	if err != nil {
		t.Fatal(err)
	}
	if !edgesDeepEqual(base, again) {
		t.Fatalf("TS Link not idempotent:\n%v\n%v", dump(base), dump(again))
	}
	// Order-independent over shuffled nodes + pending refs.
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 20; iter++ {
		shNodes := append([]model.Node(nil), nodes...)
		rng.Shuffle(len(shNodes), func(i, j int) { shNodes[i], shNodes[j] = shNodes[j], shNodes[i] })
		shFiles := []FileRefs{files[0]}
		p := append([]parse.PendingRef(nil), files[0].Pending...)
		rng.Shuffle(len(p), func(i, j int) { p[i], p[j] = p[j], p[i] })
		shFiles[0].Pending = p
		_, got, _, err := New().Link("typescript", shFiles, BuildIndex(shNodes))
		if err != nil {
			t.Fatal(err)
		}
		if !edgesDeepEqual(base, got) {
			t.Fatalf("TS order-dependence at iter %d:\nbase=%v\ngot =%v", iter, dump(base), dump(got))
		}
	}
}

// TestTSLink_JavascriptAndTsxRegistered asserts the one impl is registered under
// all three language ids the parsers emit.
func TestTSLink_JavascriptAndTsxRegistered(t *testing.T) {
	l := New()
	for _, lang := range []string{"typescript", "tsx", "javascript"} {
		if _, ok := l.resolvers[lang]; !ok {
			t.Errorf("no resolver registered for %q", lang)
		}
	}
}
