package jvmresolve

import "testing"

// bodyFixture: a two-package repository exercising every declared receiver
// form and every named skip.
func bodyFixture() map[string][]byte {
	return map[string][]byte{
		"com/shop/Shop.java": []byte(`package com.shop;
import com.tax.Rate;
public class Shop {
    Rate stored;
    int total;

    public Rate chain() { return stored; }
    public void helper() {}

    public void run(Rate param) {
        Rate local = new Rate();
        local.value();
        param.value();
        stored.value();
        this.stored.value();
        helper();
        Rate.max();
        chain().value();
        ((Rate) unknown()).value();
        int t = this.total;
        String s = "x";
        s.length();
        var inferred = local;
        inferred.value();
        {
            Other local2 = new Other();
            local2.ping();
        }
    }
}
class Other {
    void ping() {}
}
`),
		"com/tax/Rate.java": []byte(`package com.tax;
public class Rate {
    public int count;
    public void value() {}
    public static void max() {}
}
`),
	}
}

func analyze(t *testing.T) ([]TypedSite, SkipCounts) {
	t.Helper()
	files := bodyFixture()
	tab := BuildTable(files)
	if len(tab.Skipped) != 0 {
		t.Fatalf("fixture must table cleanly: %+v", tab.Skipped)
	}
	return NewIndex(tab).AnalyzeJavaBodies(files)
}

// siteAt finds a call site by (name, line-of-name).
func findSite(sites []TypedSite, kind SiteKind, name string, line int) (TypedSite, bool) {
	for _, s := range sites {
		if s.Kind == kind && s.Name == name && s.Line == line {
			return s, true
		}
	}
	return TypedSite{}, false
}

// TestAnalyzeJavaBodies_VariadicForfeit pins JVMSOUND-001: a variadic overload
// beside a fixed one makes (name, arity) an unreliable key. `f("a","b")` must
// NOT bind f(int,int) — javac binds f(String...) — so the whole binding forfeits
// as ambiguous rather than emitting a wrong confirmed edge.
func TestAnalyzeJavaBodies_VariadicForfeit(t *testing.T) {
	files := map[string][]byte{
		"vf/T.java": []byte(`package vf;
public class T {
    void f(int a, int b) {}
    void f(String... s) {}
    void g() { f("a", "b"); }
}
`),
	}
	tab := BuildTable(files)
	if len(tab.Skipped) != 0 {
		t.Fatalf("fixture must table cleanly: %+v", tab.Skipped)
	}
	sites, skips := NewIndex(tab).AnalyzeJavaBodies(files)
	if s, ok := findSite(sites, SiteCall, "f", 5); ok {
		t.Errorf("variadic overload must forfeit, not bind f(int,int): got %+v (JVMSOUND-001)", s)
	}
	if skips[SkipLookupAmbiguous] == 0 {
		t.Errorf("expected %s > 0 (the variadic forfeit): %+v", SkipLookupAmbiguous, skips)
	}
}

// TestAnalyzeJavaBodies_ResolvedSignature pins JVMSOUND-002: two overloads whose
// parameters are both written `Foo` but resolve to q.Foo / r.Foo are DISTINCT,
// not an override chain. `b.m(f)` must forfeit (differing resolved signatures),
// not collapse onto the most-derived B.m by written-text equality. It also
// controls that a genuine primitive override (m(int)/m(int)) still binds, so the
// resolved-signature rule does not over-forfeit.
func TestAnalyzeJavaBodies_ResolvedSignature(t *testing.T) {
	files := map[string][]byte{
		"q/Foo.java": []byte("package q;\npublic class Foo {}\n"),
		"r/Foo.java": []byte("package r;\npublic class Foo {}\n"),
		"s/A.java":   []byte("package s;\nimport q.Foo;\npublic class A { public void m(Foo x) {} }\n"),
		"s/B.java":   []byte("package s;\nimport r.Foo;\npublic class B extends A { public void m(Foo x) {} }\n"),
		"s/C.java":   []byte("package s;\nimport q.Foo;\npublic class C { void call(B b, Foo f) { b.m(f); } }\n"),
		// control: a real primitive override must still collapse and bind.
		"o/Base.java": []byte("package o;\npublic class Base { public int k(int n) { return n; } }\n"),
		"o/Sub.java":  []byte("package o;\npublic class Sub extends Base { public int k(int n) { return n + 1; } }\n"),
		"o/Use.java":  []byte("package o;\npublic class Use { int go(Sub s) { return s.k(1); } }\n"),
	}
	tab := BuildTable(files)
	if len(tab.Skipped) != 0 {
		t.Fatalf("fixture must table cleanly: %+v", tab.Skipped)
	}
	sites, skips := NewIndex(tab).AnalyzeJavaBodies(files)

	// b.m(f): distinct resolved signatures (q.Foo vs r.Foo) → forfeit, no site.
	for _, s := range sites {
		if s.Name == "m" && s.Declaring.FQN == "s.B" {
			t.Errorf("b.m(f) must forfeit, not bind the most-derived B.m by text equality: %+v (JVMSOUND-002)", s)
		}
	}
	if skips[SkipLookupAmbiguous] == 0 {
		t.Errorf("expected %s > 0 (the resolved-signature forfeit): %+v", SkipLookupAmbiguous, skips)
	}
	// Control: the primitive override still binds most-derived (no over-forfeit).
	boundSubK := false
	for _, s := range sites {
		if s.Name == "k" && s.Declaring.FQN == "o.Sub" {
			boundSubK = true
		}
	}
	if !boundSubK {
		t.Errorf("a genuine primitive override must still bind Sub.k (resolved-sig rule must not over-forfeit): %+v", sites)
	}
}

func TestAnalyzeJavaBodies_DeclaredReceiverForms(t *testing.T) {
	sites, _ := analyze(t)

	// Every provable form binds value() on com.tax.Rate: typed local (12),
	// parameter (13), field (14), this.field (15), declared-return chain
	// (18), cast assertion (19).
	for _, line := range []int{12, 13, 14, 15, 18, 19} {
		s, ok := findSite(sites, SiteCall, "value", line)
		if !ok {
			t.Errorf("line %d: expected a proved value() call site", line)
			continue
		}
		if s.Receiver.FQN != "com.tax.Rate" || s.Declaring.FQN != "com.tax.Rate" || s.Arity != 0 {
			t.Errorf("line %d: bound %s on %s (declaring %s)", line, s.Name, s.Receiver.FQN, s.Declaring.FQN)
		}
		if s.FromType.FQN != "com.shop.Shop" || s.FromMember.Name != "run" {
			t.Errorf("line %d: wrong from-context: %s.%s", line, s.FromType.FQN, s.FromMember.Name)
		}
	}

	// Bare call: implicit this through the enclosing type.
	if s, ok := findSite(sites, SiteCall, "helper", 16); !ok || !s.ImplicitReceiver || s.Receiver.FQN != "com.shop.Shop" {
		t.Errorf("implicit-this helper(): %+v ok=%v", s, ok)
	}
	// Qualified static: the receiver written as a type name.
	if s, ok := findSite(sites, SiteCall, "max", 17); !ok || !s.StaticReceiver || s.Receiver.FQN != "com.tax.Rate" {
		t.Errorf("static Rate.max(): %+v ok=%v", s, ok)
	}
	// Value site through this.total.
	if s, ok := findSite(sites, SiteValue, "total", 20); !ok || s.Receiver.FQN != "com.shop.Shop" || s.Arity != -1 {
		t.Errorf("this.total value site: %+v ok=%v", s, ok)
	}
	// The nested block's local binds in its own scope.
	if s, ok := findSite(sites, SiteCall, "ping", 27); !ok || s.Receiver.FQN != "com.shop.Other" {
		t.Errorf("block-scoped local2.ping(): %+v ok=%v", s, ok)
	}
}

func TestAnalyzeJavaBodies_NamedSkips(t *testing.T) {
	sites, skips := analyze(t)

	// String is external: s.length() must be a named external skip, and no
	// site may claim it.
	if _, ok := findSite(sites, SiteCall, "length", 22); ok {
		t.Error("a call on an external-typed receiver must never produce a site")
	}
	if skips[SkipReceiverExternal] == 0 {
		t.Errorf("expected %s > 0: %+v", SkipReceiverExternal, skips)
	}
	// `var` is inference: the local carries the named reason, and the call on
	// it skips under it.
	if _, ok := findSite(sites, SiteCall, "value", 23); ok {
		t.Error("a call on a var-inferred local must never produce a site")
	}
	if skips[SkipVarInferred] == 0 {
		t.Errorf("expected %s > 0: %+v", SkipVarInferred, skips)
	}
	// ((Rate) unknown()): the CAST types the outer receiver — but unknown()
	// itself is unresolvable and counts.
	if skips[SkipLookupNotFound] == 0 {
		t.Errorf("expected %s > 0 (unknown() has no binding): %+v", SkipLookupNotFound, skips)
	}
}

// TestAnalyzeJavaBodies_Deterministic pins byte-identical re-runs.
func TestAnalyzeJavaBodies_Deterministic(t *testing.T) {
	s1, k1 := analyze(t)
	s2, k2 := analyze(t)
	if len(s1) != len(s2) {
		t.Fatalf("site counts differ: %d vs %d", len(s1), len(s2))
	}
	for i := range s1 {
		if s1[i].Name != s2[i].Name || s1[i].Line != s2[i].Line || s1[i].Receiver.FQN != s2[i].Receiver.FQN {
			t.Fatalf("site %d differs: %+v vs %+v", i, s1[i], s2[i])
		}
	}
	for k, v := range k1 {
		if k2[k] != v {
			t.Fatalf("skip counter %s differs: %d vs %d", k, v, k2[k])
		}
	}
}

// TestAnalyzeJavaBodies_SuperCall pins `super.m()`: the receiver types to the
// enclosing type's direct CLASS supertype (intra-repo, chain closed), and the
// call binds the supertype's method — the invokespecial form javac emits. An
// OPEN supertype chain (external base) forfeits the binding as a named skip.
func TestAnalyzeJavaBodies_SuperCall(t *testing.T) {
	files := map[string][]byte{
		"h/Base.java": []byte(`package h;
public class Base {
    public int seed() { return 3; }
}
`),
		"h/Derived.java": []byte(`package h;
public class Derived extends Base {
    public int reseed() { return super.seed(); }
}
`),
	}
	tab := BuildTable(files)
	if len(tab.Skipped) != 0 {
		t.Fatalf("fixture must table cleanly: %+v", tab.Skipped)
	}
	sites, _ := NewIndex(tab).AnalyzeJavaBodies(files)

	s, ok := findSite(sites, SiteCall, "seed", 3)
	if !ok {
		t.Fatalf("expected a super.seed() call site: %+v", sites)
	}
	if s.Receiver.FQN != "h.Base" || s.Declaring.FQN != "h.Base" || s.Arity != 0 {
		t.Fatalf("super call bound %s on %s (declaring %s), want h.Base", s.Name, s.Receiver.FQN, s.Declaring.FQN)
	}
	if s.FromType.FQN != "h.Derived" || s.FromMember.Name != "reseed" {
		t.Fatalf("wrong from-context: %s.%s", s.FromType.FQN, s.FromMember.Name)
	}
}

// TestAnalyzeJavaBodies_NewChain pins the `new Foo(...).bar()` composition: the
// constructor site AND the chained call site both emit — the receiver of bar()
// types to Foo through object_creation_expression, and the outer new Foo(...)
// itself is a constructor call. Two confirmed edges from one expression, exactly
// as the bytecode emits invokespecial <init> then invokevirtual bar.
func TestAnalyzeJavaBodies_NewChain(t *testing.T) {
	files := map[string][]byte{
		"nc/Widget.java": []byte(`package nc;
public class Widget {
    public Widget(int n) {}
    public int bar() { return 1; }
}
`),
		"nc/Use.java": []byte(`package nc;
public class Use {
    public int run() { return new Widget(1).bar(); }
}
`),
	}
	tab := BuildTable(files)
	if len(tab.Skipped) != 0 {
		t.Fatalf("fixture must table cleanly: %+v", tab.Skipped)
	}
	sites, _ := NewIndex(tab).AnalyzeJavaBodies(files)

	// The chained call bar() on the fresh Widget.
	if s, ok := findSite(sites, SiteCall, "bar", 3); !ok || s.Receiver.FQN != "nc.Widget" || s.Arity != 0 {
		t.Errorf("chained bar() on new Widget: %+v ok=%v", s, ok)
	}
	// The constructor call itself, targeting the type.
	if s, ok := findSite(sites, SiteCall, "Widget", 3); !ok || s.Declaring.FQN != "nc.Widget" || s.Member == nil || s.Member.Form != MemberConstructor {
		t.Errorf("constructor site for new Widget(1): %+v ok=%v", s, ok)
	}
}

// TestAnalyzeJavaBodies_ConstructorSite pins the `new Foo(...)` constructor
// call: the written type resolves intra-repo, an explicit constructor of the
// matching arity exists, and the site targets Foo's TYPE (Declaring == the
// type, a constructor Member). Symmetric with the Kotlin walker.
func TestAnalyzeJavaBodies_ConstructorSite(t *testing.T) {
	files := map[string][]byte{
		"mk/Widget.java": []byte(`package mk;
public class Widget {
    public Widget(int n) {}
}
`),
		"mk/Factory.java": []byte(`package mk;
public class Factory {
    public Widget make() { return new Widget(1); }
    public Widget none() { return new Missing(2); }
}
`),
	}
	tab := BuildTable(files)
	if len(tab.Skipped) != 0 {
		t.Fatalf("fixture must table cleanly: %+v", tab.Skipped)
	}
	sites, skips := NewIndex(tab).AnalyzeJavaBodies(files)

	// make() → the Widget constructor: a call site targeting the type.
	s, ok := findSite(sites, SiteCall, "Widget", 3)
	if !ok {
		t.Fatalf("expected a constructor call site for new Widget(1): %+v", sites)
	}
	if s.Declaring.FQN != "mk.Widget" || s.Member == nil || s.Member.Form != MemberConstructor || !s.StaticReceiver {
		t.Fatalf("constructor site shape: %+v", s)
	}
	// new Missing(2): Missing resolves nowhere intra-repo → external skip, no site.
	if _, ok := findSite(sites, SiteCall, "Missing", 4); ok {
		t.Error("a constructor call on an unresolvable type must not produce a site")
	}
	if skips[SkipReceiverExternal] == 0 {
		t.Errorf("expected %s > 0 for new Missing(): %+v", SkipReceiverExternal, skips)
	}
}
