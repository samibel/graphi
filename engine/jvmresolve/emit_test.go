package jvmresolve

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
)

// committedSet builds the committed node set the way ingest would: by running
// the REAL core/parse extractors over the fixture — so this test proves the
// whole chain: table → walker → identity map → committed check.
func committedSet(t *testing.T, files map[string][]byte) (map[model.NodeId]struct{}, map[string]model.NodeId) {
	t.Helper()
	reg := parse.NewDefaultRegistry()
	committed := map[model.NodeId]struct{}{}
	byKey := map[string]model.NodeId{} // "kind qn path" → id, for assertions
	for path, src := range files {
		p, err := reg.ParserFor(path)
		if err != nil {
			t.Fatalf("no parser for %s: %v", path, err)
		}
		res, err := p.Parse(context.Background(), path, src)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, n := range res.Nodes {
			committed[n.ID()] = struct{}{}
			byKey[fmt.Sprintf("%s %s %s", n.Kind(), n.QualifiedName(), n.SourcePath())] = n.ID()
		}
	}
	return committed, byKey
}

func edgeBetween(edges []model.Edge, from, to model.NodeId, kind string) (model.Edge, bool) {
	for _, e := range edges {
		if e.From() == from && e.To() == to && e.Kind() == kind {
			return e, true
		}
	}
	return model.Edge{}, false
}

func TestEmit_JavaCallsAndReferences(t *testing.T) {
	files := bodyFixture()
	committed, byKey := committedSet(t, files)
	ix := NewIndex(BuildTable(files))
	sites, _ := ix.AnalyzeJavaBodies(files)
	res, err := ix.Emit(sites, committed)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}

	run := byKey["method shop.run com/shop/Shop.java"]
	value := byKey["method tax.value com/tax/Rate.java"]
	total := byKey["variable shop.total com/shop/Shop.java"]
	if run == "" || value == "" || total == "" {
		t.Fatalf("fixture nodes missing from the extractor set: %v", byKey)
	}

	// All six proved value() sites merge into ONE logical edge with a sorted
	// evidence union, at the D1 contract.
	e, ok := edgeBetween(res.Edges, run, value, "calls")
	if !ok {
		t.Fatalf("missing calls edge run→value; edges: %d", len(res.Edges))
	}
	if e.Tier() != model.TierConfirmed || e.Confidence() != 1.0 {
		t.Fatalf("edge must be confirmed@1.0: %v %v", e.Tier(), e.Confidence())
	}
	if !strings.Contains(e.Reason(), "static binding") {
		t.Fatalf("reason must carry the D1 contract: %q", e.Reason())
	}
	if len(e.Evidence()) < 4 {
		t.Fatalf("evidence must union the value() sites: %v", e.Evidence())
	}
	// The this.total read lands as a references edge.
	if e, ok := edgeBetween(res.Edges, run, total, "references"); !ok || !strings.Contains(e.Reason(), "declared-type") {
		t.Fatalf("missing/wrong references edge run→total: %+v ok=%v", e, ok)
	}
}

func TestEmit_JavaNominalImplements(t *testing.T) {
	files := map[string][]byte{
		"com/shop/Base.java": []byte(`package com.shop;
public class Base implements Walk {
    public void step() {}
}
`),
		"com/shop/Walk.java": []byte(`package com.shop;
public interface Walk {
    void step();
}
`),
		"com/shop/Sub.java": []byte(`package com.shop;
public class Sub extends Base {
}
`),
	}
	committed, byKey := committedSet(t, files)
	ix := NewIndex(BuildTable(files))
	res, err := ix.Emit(nil, committed)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	base := byKey["type shop.Base com/shop/Base.java"]
	walk := byKey["type shop.Walk com/shop/Walk.java"]
	sub := byKey["type shop.Sub com/shop/Sub.java"]

	// The declared interface clause is the proof.
	if e, ok := edgeBetween(res.Edges, base, walk, "implements"); !ok || e.Tier() != model.TierConfirmed {
		t.Fatalf("missing implements Base→Walk: ok=%v %+v", ok, e)
	}
	// Class extends class stays OUT of the confirmed set (the ingest sweep
	// excludes inherits; nothing here may claim it).
	for _, e := range res.Edges {
		if e.From() == sub && e.To() == base {
			t.Fatalf("class-extends must not emit a confirmed edge: %+v", e)
		}
	}
}

func TestEmit_KotlinConstructorAndDrops(t *testing.T) {
	files := kotlinBodyFixture()
	committed, byKey := committedSet(t, files)
	ix := NewIndex(BuildTable(files))
	sites, _ := ix.AnalyzeKotlinBodies(files)
	res, err := ix.Emit(sites, committed)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}

	run := byKey["method shop.run com/shop/Basket.kt"]
	rateType := byKey["type tax.Rate com/tax/Rate.kt"]
	value := byKey["method tax.value com/tax/Rate.kt"]
	topMake := byKey["function shop.topMake com/shop/Basket.kt"]

	// Constructor call: calls edge to the TYPE node, constructor reason.
	if e, ok := edgeBetween(res.Edges, run, rateType, "calls"); !ok || !strings.Contains(e.Reason(), "constructor") {
		t.Fatalf("missing constructor calls edge run→Rate: ok=%v %+v", ok, e)
	}
	// Receiver-typed member calls.
	if _, ok := edgeBetween(res.Edges, run, value, "calls"); !ok {
		t.Fatal("missing calls edge run→value")
	}
	// Same-file top-level function target.
	if _, ok := edgeBetween(res.Edges, run, topMake, "calls"); !ok {
		t.Fatal("missing calls edge run→topMake")
	}
	// The this.stored value site binds a PRIMARY-CONSTRUCTOR property — the
	// table records it, the extractor mints no node: the committed check
	// must DROP it (never fabricate), and count it.
	if res.DroppedIntents == 0 {
		t.Fatalf("expected dropped intents (primary-ctor property target): %+v", res)
	}
	for _, e := range res.Edges {
		if e.Kind() == "references" && e.From() == run {
			t.Fatalf("no references edge may survive for the node-less property: %+v", e)
		}
	}
}

func TestEmit_Deterministic(t *testing.T) {
	files := bodyFixture()
	committed, _ := committedSet(t, files)
	ix := NewIndex(BuildTable(files))
	sites, _ := ix.AnalyzeJavaBodies(files)
	r1, err := ix.Emit(sites, committed)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := ix.Emit(sites, committed)
	if err != nil {
		t.Fatal(err)
	}
	dump := func(edges []model.Edge) string {
		var b strings.Builder
		for _, e := range edges {
			fmt.Fprintf(&b, "%s %s->%s %s %s %.2f %q %v\n", e.ID(), e.From(), e.To(), e.Kind(), e.Tier(), e.Confidence(), e.Reason(), e.Evidence())
		}
		return b.String()
	}
	if dump(r1.Edges) != dump(r2.Edges) {
		t.Fatal("Emit must be byte-deterministic")
	}
	if r1.DroppedIntents != r2.DroppedIntents {
		t.Fatal("DroppedIntents must be deterministic")
	}
}
