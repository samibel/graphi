package link

import (
	"math/rand"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
)

// assertCall asserts a calls edge fromQN->toQN exists with the given tier and full
// provenance. Shared by the FU-5 per-language resolver tests.
func assertCall(t *testing.T, nodes []model.Node, edges []model.Edge, fromQN, toQN string, tier model.ConfidenceTier) {
	t.Helper()
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

// assertEdgeTier asserts an edge from→to of the given kind exists at the given
// tier with non-empty provenance. Unlike assertCall it takes NodeIds directly, so
// it works for edges to linker-minted external nodes (whose QN is not in the
// fixture node set).
func assertEdgeTier(t *testing.T, edges []model.Edge, from, to model.NodeId, kind string, tier model.ConfidenceTier) {
	t.Helper()
	for _, e := range edges {
		if e.From() == from && e.To() == to && e.Kind() == kind {
			if e.Tier() != tier {
				t.Errorf("%s->%s tier=%q want %q", from, to, e.Tier(), tier)
			}
			if e.Reason() == "" || len(e.Evidence()) == 0 {
				t.Errorf("%s->%s missing reason/evidence", from, to)
			}
			return
		}
	}
	t.Errorf("missing %s edge %s -> %s", kind, from, to)
}

// assertNoPhantomNoConfirmed asserts every edge targets a committed node and no
// edge is confirmed tier (the linker is NEVER confirmed).
func assertNoPhantomNoConfirmed(t *testing.T, nodes []model.Node, edges []model.Edge) {
	t.Helper()
	known := map[model.NodeId]struct{}{}
	for _, n := range nodes {
		known[n.ID()] = struct{}{}
	}
	for _, e := range edges {
		if _, ok := known[e.To()]; !ok {
			t.Errorf("edge to unknown target %s (kind %s)", e.To(), e.Kind())
		}
		if e.Tier() == model.TierConfirmed {
			t.Errorf("linker emitted a confirmed edge: %s", e.ID())
		}
	}
}

// assertOrderIndependent asserts the resolver is idempotent and produces a
// byte-identical edge set under shuffled nodes and pending refs.
func assertOrderIndependent(t *testing.T, lang string, scene func(*testing.T) ([]model.Node, []FileRefs)) {
	t.Helper()
	nodes, files := scene(t)
	idx := BuildIndex(nodes)
	_, base, _, err := New().Link(lang, files, idx)
	if err != nil {
		t.Fatal(err)
	}
	_, again, _, err := New().Link(lang, files, idx)
	if err != nil {
		t.Fatal(err)
	}
	if !edgesDeepEqual(base, again) {
		t.Fatalf("%s Link not idempotent:\n%v\n%v", lang, dump(base), dump(again))
	}
	rng := rand.New(rand.NewSource(99))
	for iter := 0; iter < 20; iter++ {
		shNodes := append([]model.Node(nil), nodes...)
		rng.Shuffle(len(shNodes), func(i, j int) { shNodes[i], shNodes[j] = shNodes[j], shNodes[i] })
		shFiles := make([]FileRefs, len(files))
		copy(shFiles, files)
		if len(shFiles) > 0 {
			p := append([]parse.PendingRef(nil), files[0].Pending...)
			rng.Shuffle(len(p), func(i, j int) { p[i], p[j] = p[j], p[i] })
			shFiles[0].Pending = p
		}
		_, got, _, err := New().Link(lang, shFiles, BuildIndex(shNodes))
		if err != nil {
			t.Fatal(err)
		}
		if !edgesDeepEqual(base, got) {
			t.Fatalf("%s order-dependence at iter %d:\nbase=%v\ngot =%v", lang, iter, dump(base), dump(got))
		}
	}
}
