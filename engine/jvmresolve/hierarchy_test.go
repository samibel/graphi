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

// TestCallableSig_ArrayDim pins the JVMSOUND-004 fix (SW-188): callableSig now
// carries the trailing `[]` groups of each parameter's written type text, so
// `m(T)`, `m(T[])`, `m(T[][])` and `m(T[][][])` produce DISTINCT signature
// keys and an overload set is read as overloads, not as overrides.
//
// THREE shapes are asserted:
//   1. scalar vs one-dim across a BASE/DERIVED chain → AmbiguousMember, NOT
//      the most-derived-wins override pair the old key produced;
//   2. one-dim vs two-dim on the same class → AmbiguousMember, NOT an
//      override pair (the WIDENED blast radius SW-172 round 1 found).
//
// Each is a positive regression test: the fix lands here, the binder is
// correct on these fixtures, and a future change that re-erases array
// dimensionality breaks at least one assertion.
func TestCallableSig_ArrayDim(t *testing.T) {
	files := map[string][]byte{
		// Shape 1: scalar vs one-dim across an override chain.
		"a/Base.java": []byte(`package a;
public class Base {
    public int apply(int x) { return x; }
}
`),
		"a/Derived.java": []byte(`package a;
public class Derived extends Base {
    public int apply(int[] xs) { return xs.length; }
}
`),
		// Shape 2: one-dim vs two-dim, SAME class, same arity.
		"a/Depth.java": []byte(`package a;
public class Depth {
    public int apply(int[] xs)   { return xs.length; }
    public int apply(int[][] xs) { return xs.length; }
}
`),
		"a/App.java": []byte(`package a;
public class App {
    public int run() { return 0; }
}
`),
	}
	tab := BuildTable(files)
	if len(tab.Skipped) != 0 {
		t.Fatalf("fixture must table cleanly: %+v", tab.Skipped)
	}
	ix := NewIndex(tab)

	// SHAPE 1 — the cross-file mis-binding JVMSOUND-004 originally caused.
	// `d.apply(t)` with a scalar `int t` must bind a.Base.apply(int), not
	// a.Derived.apply(int[]). With the fix, the two overloads differ in
	// array dimensionality and the lookup returns AmbiguousMember (since the
	// receiver type's static binding cannot rank by argument type), NOT a
	// most-derived-wins override binding.
	base := typeOf(t, ix, "a.Base")
	if r := ix.LookupCallable(base, "apply", 1); r.Outcome != BoundMember || r.Declaring.FQN != "a.Base" {
		t.Fatalf("Base.apply(int) from Base receiver: %+v", r)
	}
	derived := typeOf(t, ix, "a.Derived")
	if r := ix.LookupCallable(derived, "apply", 1); r.Outcome != AmbiguousMember {
		t.Fatalf("Derived receiver with both apply(int) and apply(int[]) must be AmbiguousMember, got %+v", r)
	}

	// SHAPE 2 — the WIDENED blast radius (one-dim vs two-dim at the same
	// arity): the old key collapsed these onto one signature, the new key
	// keeps them apart.
	depth := typeOf(t, ix, "a.Depth")
	if r := ix.LookupCallable(depth, "apply", 1); r.Outcome != AmbiguousMember {
		t.Fatalf("Depth receiver with apply(int[]) vs apply(int[][]) at the same arity must be AmbiguousMember, got %+v", r)
	}
}

// TestCallableSig_CrossFileBindsBase pins the JVMSOUND-004 cross-file
// closure: when the same-name overload pair lives in DIFFERENT files
// (Base.m(T) and Derived.m(T[])), the binder no longer reads the array
// overload as an override and binds the SCALAR call to Base.m(T) — the
// correct javac answer, in the correct file. The negative cross-file
// pin at internal/jvmgroundtruth/signature_test.go's
// TestJVMSOUND004_CrossFileWrongEdge turned RED-with-instructions when
// the fix landed and is replaced by this positive regression.
func TestCallableSig_CrossFileBindsBase(t *testing.T) {
	files := map[string][]byte{
		"a/Thing.java": []byte(`package a;
public class Thing {}
`),
		"a/Base.java": []byte(`package a;
public class Base {
    public int apply(Thing t) { return 1; }
}
`),
		"a/Derived.java": []byte(`package a;
public class Derived extends Base {
    public int apply(Thing[] ts) { return ts.length; }
}
`),
	}
	tab := BuildTable(files)
	if len(tab.Skipped) != 0 {
		t.Fatalf("fixture must table cleanly: %+v", tab.Skipped)
	}
	ix := NewIndex(tab)
	derived := typeOf(t, ix, "a.Derived")
	// With the fix, the lookup at the Derived receiver (which inherits both
	// overloads across the chain) returns AmbiguousMember rather than the
	// most-derived wrong binding — and the SCALAR lookup, when made on the
	// Base receiver, binds correctly. The two together are what graphi's
	// typed-site emission needs to get the cross-file edge right.
	if r := ix.LookupCallable(derived, "apply", 1); r.Outcome != AmbiguousMember {
		t.Fatalf("Derived with apply(Thing) + apply(Thing[]) at arity 1 must be AmbiguousMember, got %+v", r)
	}
	base := typeOf(t, ix, "a.Base")
	if r := ix.LookupCallable(base, "apply", 1); r.Outcome != BoundMember || r.Declaring.FQN != "a.Base" {
		t.Fatalf("Base.apply(Thing) from Base receiver: %+v", r)
	}
}

// TestValueClassBridgeName pins the JVMHARN-001 fix's recognition rule: a
// method name shaped `name-<hash>` is a value-class bridge; the bridge
// name (`name`, the prefix before the dash) is what graphi's typed-site
// emission records, while the lookup may match either form. The shape
// rule must reject ordinary user-declared names with dashes (legal in
// backticked Kotlin) and accept the compiler-minted base-64-ish hash
// suffix kotlinc emits.
func TestValueClassBridgeName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// Real kotlinx.serialization mangled names (from SW-173's pin
		// capture digest e6d39791…). The first four are the recorded
		// shape; the others stress the boundary.
		{"serialize-2TYgG_w", "serialize"},
		{"toBuilder-GBYM_sE", "toBuilder"},
		{"writeContent-Coi6ktg", "writeContent"},
		{"computeIfAbsent-gIAlu-s", "computeIfAbsent"},
		// No mangling: ordinary name.
		{"apply", ""},
		// Too-short hash suffix (the rule requires ≥4 base-64 chars).
		{"foo-ab", ""},
		// Hash with a non-base-64 character.
		{"foo-abcd!", ""},
		// Prefix starts with a digit: not a valid Kotlin identifier.
		{"1foo-abcd", ""},
		// Just a dash and a hash, no prefix.
		{"-abcd", ""},
		// Just a dash, no suffix.
		{"foo-", ""},
	}
	for _, tc := range cases {
		got := valueClassBridgeName(tc.in)
		if got != tc.want {
			t.Errorf("valueClassBridgeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestLookupCallableValueClassAware pins the JVMHARN-001 lookup rule: when
// the call site names a method in its value-class-mangled form
// (`name-<hash>`), the lookup also tries the bridge name (`name`), so the
// source-declared binding target is what graphi's typed-site emission
// records. The mangled form itself is NEVER written to TypedSite.
func TestLookupCallableValueClassAware(t *testing.T) {
	files := map[string][]byte{
		"a/V.kt": []byte(`package a
@kotlin.jvm.JvmInline
value class V(val v: Int)
class Holder {
    fun foo(x: V): Int = x.v
}
`),
		"a/App.kt": []byte(`package a
class App {
    fun run(h: Holder, v: V): Int = h.foo(v)
}
`),
	}
	tab := BuildTable(files)
	if len(tab.Skipped) != 0 {
		t.Fatalf("fixture must table cleanly: %+v", tab.Skipped)
	}
	ix := NewIndex(tab)
	holder := typeOf(t, ix, "a.Holder")
	// The plain lookup of `foo` from the Holder receiver binds the
	// Holder.foo(V) declaration graphi tables.
	if r := ix.LookupCallable(holder, "foo", 1); r.Outcome != BoundMember {
		t.Fatalf("plain foo: %+v", r)
	}
	// The value-class-aware lookup, given the mangled name, also binds the
	// same Holder.foo(V) declaration — and graphi records the source name
	// (`foo`), not the mangled form, on the emitted typed-site.
	if r := ix.LookupCallableValueClassAware(holder, "foo-2TYgG_w", 1); r.Outcome != BoundMember {
		t.Fatalf("value-class-aware lookup of mangled name: %+v", r)
	}
}
