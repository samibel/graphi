package jvmresolve

import "testing"

// hierarchyFixture: an intra-repo class chain with an interface diamond, an
// override chain, cross-level and same-level overloads, field hiding, and one
// type whose superclass is external.
func hierarchyFixture(t *testing.T) *Index {
	t.Helper()
	files := map[string][]byte{
		"com/shop/Base.java": []byte(`package com.shop;
public class Base implements Walk {
    protected int total;
    public void add(int x) {}
    public String label(String s) { return s; }
    public void ship() {}
}
`),
		"com/shop/Derived.java": []byte(`package com.shop;
public class Derived extends Base implements Walk {
    protected int total;
    public void add(int x) {}
    public void add(int x, int y) {}
    public int label(int n) { return n; }
}
`),
		"com/shop/Walk.java": []byte(`package com.shop;
public interface Walk {
    void step();
}
`),
		"com/shop/Leaky.java": []byte(`package com.shop;
import java.util.ArrayList;
public class Leaky extends ArrayList {
    public void own() {}
}
`),
	}
	tab := BuildTable(files)
	if len(tab.Skipped) != 0 {
		t.Fatalf("fixture must table cleanly: %+v", tab.Skipped)
	}
	return NewIndex(tab)
}

func TestLookupCallable_BindingRule(t *testing.T) {
	ix := hierarchyFixture(t)
	derived := typeOf(t, ix, "com.shop.Derived")
	base := typeOf(t, ix, "com.shop.Base")

	// Override chain (identical erased signature at two levels): the most
	// derived declaration binds — javac's static target for this receiver.
	if r := ix.LookupCallable(derived, "add", 1); r.Outcome != BoundMember || r.Declaring.FQN != "com.shop.Derived" {
		t.Fatalf("override must bind most-derived: %+v", r)
	}
	// The same name at a different arity is disambiguated by arity alone.
	if r := ix.LookupCallable(derived, "add", 2); r.Outcome != BoundMember || r.Declaring.FQN != "com.shop.Derived" {
		t.Fatalf("arity-2 add: %+v", r)
	}
	// Cross-level same-arity overloads with DIFFERING signatures
	// (label(int) vs label(String)): javac ranks by argument types, a
	// declared-type binder must not — ambiguous, dropped.
	if r := ix.LookupCallable(derived, "label", 1); r.Outcome != AmbiguousMember {
		t.Fatalf("differing-signature overload set must be ambiguous: %+v", r)
	}
	// From the BASE receiver only one label exists — binds cleanly.
	if r := ix.LookupCallable(base, "label", 1); r.Outcome != BoundMember || r.Declaring.FQN != "com.shop.Base" {
		t.Fatalf("base label: %+v", r)
	}
	// Inherited method: found one level up the closed chain.
	if r := ix.LookupCallable(derived, "ship", 0); r.Outcome != BoundMember || r.Declaring.FQN != "com.shop.Base" {
		t.Fatalf("inherited ship: %+v", r)
	}
	// Interface member through the diamond (Walk reached via Derived AND
	// Base — the cycle guard visits it once).
	if r := ix.LookupCallable(derived, "step", 0); r.Outcome != BoundMember || r.Declaring.FQN != "com.shop.Walk" {
		t.Fatalf("diamond interface step: %+v", r)
	}
	// A closed chain that provably lacks the member.
	if r := ix.LookupCallable(derived, "missing", 0); r.Outcome != NotFoundMember {
		t.Fatalf("closed-chain miss: %+v", r)
	}
}

func TestLookupValue_FieldHiding(t *testing.T) {
	ix := hierarchyFixture(t)
	derived := typeOf(t, ix, "com.shop.Derived")
	// `total` is declared at both levels: hiding — the most derived wins for
	// this receiver's static type.
	if r := ix.LookupValue(derived, "total"); r.Outcome != BoundMember || r.Declaring.FQN != "com.shop.Derived" {
		t.Fatalf("field hiding must bind most-derived: %+v", r)
	}
}

// TestLookup_OpenChainForfeitsEverything pins the strict fail-closed rule: a
// type whose superclass is external forfeits EVERY binding — even a member
// the receiver itself declares — because an external overload could be the
// more applicable javac target.
func TestLookup_OpenChainForfeitsEverything(t *testing.T) {
	ix := hierarchyFixture(t)
	leaky := typeOf(t, ix, "com.shop.Leaky")
	if r := ix.LookupCallable(leaky, "own", 0); r.Outcome != OpenChain {
		t.Fatalf("open chain must forfeit even receiver-declared members: %+v", r)
	}
	if r := ix.LookupValue(leaky, "anything"); r.Outcome != OpenChain {
		t.Fatalf("open chain must forfeit value lookups too: %+v", r)
	}
}
