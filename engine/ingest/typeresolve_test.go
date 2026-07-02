package ingest_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
)

// typeresolveFixture is the canonical cross-package repo for the wiring
// invariants: main.main calls util.Answer through an intra-module import, so
// the heuristic linker resolves it at the heuristic tier and the typeresolve
// pass can prove it at the confirmed tier.
func typeresolveFixture() map[string]string {
	return map[string]string{
		"go.mod":       "module example.com/m\n\ngo 1.26\n",
		"util/util.go": "package util\n\nfunc Answer() int { return 42 }\n",
		// NOTE: the call must be the statement's own expression — the extractor
		// records a bare-ident call like println(...) without descending into
		// its arguments, so a selector call wrapped in println would never
		// reach the heuristic linker and the fixture would only exercise the
		// typeresolve layer.
		"main.go": "package main\n\nimport \"example.com/m/util\"\n\nfunc main() { x := util.Answer(); _ = x }\n",
	}
}

// edgeBetween finds the edge (fromQN --kind--> toQN) by qualified names.
func edgeBetween(t *testing.T, store graphstore.Graphstore, fromQN, toQN, kind string) (model.Edge, bool) {
	t.Helper()
	ctx := context.Background()
	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("nodes: %v", err)
	}
	byQN := map[string]model.NodeId{}
	for _, n := range nodes {
		if n.Kind() != "file" {
			byQN[n.QualifiedName()] = n.ID()
		}
	}
	edges, err := store.Edges(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("edges: %v", err)
	}
	for _, e := range edges {
		if e.From() == byQN[fromQN] && e.To() == byQN[toQN] && e.Kind() == kind {
			return e, true
		}
	}
	return model.Edge{}, false
}

// dumpGraph renders the complete graph state deterministically — full node and
// edge identity INCLUDING tier, confidence, reason, and evidence — so two
// stores can be compared byte-for-byte.
func dumpGraph(t *testing.T, store graphstore.Graphstore) string {
	t.Helper()
	ctx := context.Background()
	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("nodes: %v", err)
	}
	edges, err := store.Edges(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("edges: %v", err)
	}
	var lines []string
	for _, n := range nodes {
		lines = append(lines, fmt.Sprintf("node %s %s %s %s:%d:%d",
			n.ID(), n.Kind(), n.QualifiedName(), n.SourcePath(), n.Line(), n.Column()))
	}
	for _, e := range edges {
		lines = append(lines, fmt.Sprintf("edge %s %s->%s %s %s %.2f %q %v",
			e.ID(), e.From(), e.To(), e.Kind(), e.Tier(), e.Confidence(), e.Reason(), e.Evidence()))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func rewrite(t *testing.T, root, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(content), 0o600); err != nil {
		t.Fatalf("rewrite %s: %v", rel, err)
	}
}

// TestTyperesolve_ConfirmedWins is wiring invariant (a): after a full index,
// the type-checkable cross-package call carries the CONFIRMED tier at
// confidence 1.0 — the typeresolve upsert replaced the linker's heuristic
// edge for the same logical (from,to,kind).
func TestTyperesolve_ConfirmedWins(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, typeresolveFixture())

	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	e, ok := edgeBetween(t, store, "main.main", "util.Answer", "calls")
	if !ok {
		t.Fatalf("cross-package call edge missing entirely")
	}
	if e.Tier() != model.TierConfirmed || e.Confidence() != 1.0 {
		t.Errorf("calls main.main -> util.Answer: tier %s/%v, want confirmed/1.0", e.Tier(), e.Confidence())
	}
}

// TestTyperesolve_HeuristicSurvivesDegradation is wiring invariant (b): when
// the type-check DEGRADES (an import cycle here), the previously confirmed
// edge falls back to the linker's heuristic tier — the proof is withdrawn,
// the knowledge is not. Fixing the cycle re-confirms it.
func TestTyperesolve_HeuristicSurvivesDegradation(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, typeresolveFixture())

	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	if e, ok := edgeBetween(t, store, "main.main", "util.Answer", "calls"); !ok || e.Tier() != model.TierConfirmed {
		t.Fatalf("precondition: confirmed edge expected, got ok=%v tier=%v", ok, e.Tier())
	}

	// Introduce an import cycle: util now (blank-)imports the root package.
	// Both units land on one SCC and degrade; the extractor and the heuristic
	// linker are unaffected.
	cycled := "package util\n\nimport _ \"example.com/m\"\n\nfunc Answer() int { return 42 }\n"
	rewrite(t, root, "util/util.go", cycled)
	if err := ing.IngestChanged(ctx, root, []string{"util/util.go"}); err != nil {
		t.Fatalf("IngestChanged (cycle): %v", err)
	}
	e, ok := edgeBetween(t, store, "main.main", "util.Answer", "calls")
	if !ok {
		t.Fatalf("degradation DELETED the call edge — heuristic knowledge must survive")
	}
	if e.Tier() == model.TierConfirmed {
		t.Errorf("edge still confirmed after the unit degraded — the proof must be withdrawn (tier %s)", e.Tier())
	}

	// Fix the cycle: the next incremental pass re-proves the edge.
	rewrite(t, root, "util/util.go", typeresolveFixture()["util/util.go"])
	if err := ing.IngestChanged(ctx, root, []string{"util/util.go"}); err != nil {
		t.Fatalf("IngestChanged (fix): %v", err)
	}
	if e, ok := edgeBetween(t, store, "main.main", "util.Answer", "calls"); !ok || e.Tier() != model.TierConfirmed {
		t.Errorf("edge not re-confirmed after fixing the cycle: ok=%v tier=%v", ok, e.Tier())
	}
}

// TestTyperesolve_FullVsIncrementalByteParity is wiring invariant (c): a full
// index of the final state and an index-then-touch-one-file incremental path
// must produce byte-identical graphs — including tiers, confidence, reasons,
// and evidence. The whole-repo recompute design makes this hold by
// construction; this test keeps it held.
func TestTyperesolve_FullVsIncrementalByteParity(t *testing.T) {
	ctx := context.Background()
	// Final state: main gains a second function that also calls util.Answer
	// and a local helper.
	finalMain := `package main

import "example.com/m/util"

func main() { x := util.Answer(); _ = x }

func twice() int { return util.Answer() + util.Answer() + helper() }

func helper() int { return 1 }
`

	// Path A: index the original state, then touch main.go incrementally.
	storeA := graphstore.NewMemStore()
	t.Cleanup(func() { _ = storeA.Close() })
	ingA := newIngester(t, storeA, parse.NewDefaultRegistry())
	rootA := writeRepo(t, typeresolveFixture())
	if err := ingA.IngestAll(ctx, rootA); err != nil {
		t.Fatalf("IngestAll A: %v", err)
	}
	rewrite(t, rootA, "main.go", finalMain)
	if err := ingA.IngestChanged(ctx, rootA, []string{"main.go"}); err != nil {
		t.Fatalf("IngestChanged A: %v", err)
	}

	// Path B: fresh full index of the final state.
	files := typeresolveFixture()
	files["main.go"] = finalMain
	storeB := graphstore.NewMemStore()
	t.Cleanup(func() { _ = storeB.Close() })
	ingB := newIngester(t, storeB, parse.NewDefaultRegistry())
	rootB := writeRepo(t, files)
	if err := ingB.IngestAll(ctx, rootB); err != nil {
		t.Fatalf("IngestAll B: %v", err)
	}

	a, b := dumpGraph(t, storeA), dumpGraph(t, storeB)
	if a != b {
		t.Errorf("full-vs-incremental graphs diverge:\n--- incremental ---\n%s\n--- full ---\n%s", a, b)
	}
	if !strings.Contains(a, string(model.TierConfirmed)) {
		t.Errorf("parity fixture produced no confirmed edge — the invariant would be vacuous")
	}
}

// TestTyperesolve_GoModChangeParity pins the go.mod cascade expansion: editing
// ONLY go.mod (module rename) withdraws the type-check proof for every
// intra-module import, and the incremental result must still match a fresh
// full index — the confirmed edge degrades to the re-linked heuristic edge
// instead of disappearing.
func TestTyperesolve_GoModChangeParity(t *testing.T) {
	ctx := context.Background()
	renamed := "module example.com/renamed\n\ngo 1.26\n"

	storeA := graphstore.NewMemStore()
	t.Cleanup(func() { _ = storeA.Close() })
	ingA := newIngester(t, storeA, parse.NewDefaultRegistry())
	rootA := writeRepo(t, typeresolveFixture())
	if err := ingA.IngestAll(ctx, rootA); err != nil {
		t.Fatalf("IngestAll A: %v", err)
	}
	rewrite(t, rootA, "go.mod", renamed)
	if err := ingA.IngestChanged(ctx, rootA, []string{"go.mod"}); err != nil {
		t.Fatalf("IngestChanged A: %v", err)
	}

	files := typeresolveFixture()
	files["go.mod"] = renamed
	storeB := graphstore.NewMemStore()
	t.Cleanup(func() { _ = storeB.Close() })
	ingB := newIngester(t, storeB, parse.NewDefaultRegistry())
	rootB := writeRepo(t, files)
	if err := ingB.IngestAll(ctx, rootB); err != nil {
		t.Fatalf("IngestAll B: %v", err)
	}

	if a, b := dumpGraph(t, storeA), dumpGraph(t, storeB); a != b {
		t.Errorf("go.mod-only change diverges from a fresh full index:\n--- incremental ---\n%s\n--- full ---\n%s", a, b)
	}
	// The import no longer resolves intra-module, so the call edge must be
	// back at the heuristic tier — present, not proven.
	e, ok := edgeBetween(t, storeA, "main.main", "util.Answer", "calls")
	if !ok {
		t.Fatalf("module rename deleted the call edge — it must degrade, not disappear")
	}
	if e.Tier() == model.TierConfirmed {
		t.Errorf("edge still confirmed although the module path no longer matches the import (tier %s)", e.Tier())
	}
}

// TestTyperesolve_KillSwitchAndAssetEditSkip pins the operational controls:
// GRAPHI_NO_TYPERESOLVE disables the pass entirely (no confirmed edge
// anywhere), and an incremental change that cannot affect Go resolution (a
// markdown edit) leaves existing confirmed edges untouched.
func TestTyperesolve_KillSwitchAndAssetEditSkip(t *testing.T) {
	ctx := context.Background()

	t.Run("kill switch", func(t *testing.T) {
		t.Setenv(ingest.EnvNoTyperesolve, "1")
		store := graphstore.NewMemStore()
		t.Cleanup(func() { _ = store.Close() })
		ing := newIngester(t, store, parse.NewDefaultRegistry())
		root := writeRepo(t, typeresolveFixture())
		if err := ing.IngestAll(ctx, root); err != nil {
			t.Fatalf("IngestAll: %v", err)
		}
		edges, err := store.Edges(ctx, graphstore.Query{})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range edges {
			// Only the typeresolve kinds: the extractor's own defines edges
			// are legitimately confirmed (authoritative per-file knowledge).
			if k := e.Kind(); k != "calls" && k != "references" && k != "implements" {
				continue
			}
			if e.Tier() == model.TierConfirmed {
				t.Errorf("confirmed %s edge %s emitted with the kill switch set", e.Kind(), e.ID())
			}
		}
		if e, ok := edgeBetween(t, store, "main.main", "util.Answer", "calls"); !ok || e.Tier() == model.TierConfirmed {
			t.Errorf("heuristic layer must still resolve the call: ok=%v tier=%v", ok, e.Tier())
		}
	})

	t.Run("asset edit skips recompute but keeps confirmed edges", func(t *testing.T) {
		store := graphstore.NewMemStore()
		t.Cleanup(func() { _ = store.Close() })
		ing := newIngester(t, store, parse.NewDefaultRegistry())
		files := typeresolveFixture()
		files["README.md"] = "# demo\n"
		root := writeRepo(t, files)
		if err := ing.IngestAll(ctx, root); err != nil {
			t.Fatalf("IngestAll: %v", err)
		}
		rewrite(t, root, "README.md", "# demo v2\n")
		if err := ing.IngestChanged(ctx, root, []string{"README.md"}); err != nil {
			t.Fatalf("IngestChanged: %v", err)
		}
		if e, ok := edgeBetween(t, store, "main.main", "util.Answer", "calls"); !ok || e.Tier() != model.TierConfirmed {
			t.Errorf("confirmed edge lost on a non-Go edit: ok=%v tier=%v", ok, e.Tier())
		}
	})
}
