package jvmresolve

import "testing"

// kotlinBodyFixture exercises every declared Kotlin receiver form and every
// named Kotlin gap. Line numbers are load-bearing in the assertions below.
func kotlinBodyFixture() map[string][]byte {
	return map[string][]byte{
		"com/tax/Rate.kt": []byte(`package com.tax

class Rate(val n: Int) {
    fun value() {}
    fun chain(): Rate = this
}

object Registry {
    fun lookup(): Rate = Rate(1)
}
`),
		"com/shop/Basket.kt": []byte(`package com.shop

import com.tax.Rate
import com.tax.Registry

fun topMake(): Rate = Rate(1)

class Basket(val stored: Rate) {
    fun run(param: Rate) {
        val local: Rate = Rate(1)
        local.value()
        param.value()
        stored.value()
        this.stored.value()
        helper()
        Registry.lookup().value()
        local.chain().value()
        val inferred = local
        inferred.value()
        topMake().value()
        local.apply { value() }
    }
    fun helper() {}
}
`),
	}
}

func analyzeKotlin(t *testing.T) ([]TypedSite, SkipCounts) {
	t.Helper()
	files := kotlinBodyFixture()
	tab := BuildTable(files)
	if len(tab.Skipped) != 0 {
		t.Fatalf("fixture must table cleanly: %+v", tab.Skipped)
	}
	return NewIndex(tab).AnalyzeKotlinBodies(files)
}

func TestAnalyzeKotlinBodies_DeclaredReceiverForms(t *testing.T) {
	sites, _ := analyzeKotlin(t)

	// value() binds on com.tax.Rate through: typed local (11), parameter
	// (12), property (13), this.property (14), object-return chain (16),
	// declared-return chain (17), top-level-function return chain (20).
	for _, line := range []int{11, 12, 13, 14, 16, 17, 20} {
		s, ok := findSite(sites, SiteCall, "value", line)
		if !ok {
			t.Errorf("line %d: expected a proved value() call site", line)
			continue
		}
		if s.Receiver.FQN != "com.tax.Rate" || s.Declaring.FQN != "com.tax.Rate" || s.Arity != 0 {
			t.Errorf("line %d: bound %s on %s (declaring %s)", line, s.Name, s.Receiver.FQN, s.Declaring.FQN)
		}
	}
	// Constructor call: Rate(1) binds the tabled primary constructor.
	if s, ok := findSite(sites, SiteCall, "Rate", 10); !ok || !s.StaticReceiver || s.Member.Form != MemberConstructor || s.Arity != 1 {
		t.Errorf("Rate(1) constructor site: %+v ok=%v", s, ok)
	}
	// Bare call: implicit this through the enclosing type.
	if s, ok := findSite(sites, SiteCall, "helper", 15); !ok || !s.ImplicitReceiver || s.Receiver.FQN != "com.shop.Basket" {
		t.Errorf("implicit-this helper(): %+v ok=%v", s, ok)
	}
	// Object member through the type-name receiver (the static analog).
	if s, ok := findSite(sites, SiteCall, "lookup", 16); !ok || !s.StaticReceiver || s.Receiver.FQN != "com.tax.Registry" {
		t.Errorf("Registry.lookup(): %+v ok=%v", s, ok)
	}
	// Bare call to a same-file top-level function: Declaring nil, Member set.
	if s, ok := findSite(sites, SiteCall, "topMake", 20); !ok || s.Declaring != nil || s.Member == nil || s.Member.Form != MemberFunction {
		t.Errorf("top-level topMake(): %+v ok=%v", s, ok)
	}
	// The receiver navigation of line 14 reports the property read too.
	if s, ok := findSite(sites, SiteValue, "stored", 14); !ok || s.Receiver.FQN != "com.shop.Basket" {
		t.Errorf("this.stored value site: %+v ok=%v", s, ok)
	}
}

func TestAnalyzeKotlinBodies_NamedGaps(t *testing.T) {
	sites, skips := analyzeKotlin(t)

	// The inferred val forfeits its call site under the D2 counter.
	if _, ok := findSite(sites, SiteCall, "value", 19); ok {
		t.Error("a call on an inferred val must never produce a site")
	}
	if skips[SkipKtValInferred] == 0 {
		t.Errorf("expected %s > 0: %+v", SkipKtValInferred, skips)
	}
	// The trailing-lambda call forfeits (arity uncountable)…
	if _, ok := findSite(sites, SiteCall, "apply", 21); ok {
		t.Error("a trailing-lambda call must never produce a site")
	}
	if skips[SkipKtTrailingLambda] == 0 {
		t.Errorf("expected %s > 0: %+v", SkipKtTrailingLambda, skips)
	}
	// …and the bare call INSIDE its lambda is rebound-forfeited, not bound to
	// the enclosing type (apply rebinds the receiver).
	if _, ok := findSite(sites, SiteCall, "value", 21); ok {
		t.Error("a bare call inside a lambda must never bind the enclosing type")
	}
	if skips[SkipKtLambdaRebound] == 0 {
		t.Errorf("expected %s > 0: %+v", SkipKtLambdaRebound, skips)
	}
}

// TestAnalyzeKotlinBodies_ElasticForfeit pins JVMSOUND-001 for Kotlin: both
// `vararg` and defaulted parameters make (name, arity) unreliable, so a call
// that would otherwise bind a fixed sibling must forfeit. Covers the tabling
// path (parse_kotlin.go records Variadic/HasDefault) end to end.
func TestAnalyzeKotlinBodies_ElasticForfeit(t *testing.T) {
	// vararg: f(String) beside f(vararg Int) — a call f(1) has arity 1 and would
	// wrongly bind f(String) at that arity if the vararg did not forfeit.
	varargFiles := map[string][]byte{
		"ef/T.kt": []byte(`package ef
class T {
    fun f(s: String) {}
    fun f(vararg n: Int) {}
    fun g() { f(1) }
}
`),
	}
	tab := BuildTable(varargFiles)
	if len(tab.Skipped) != 0 {
		t.Fatalf("vararg fixture must table cleanly: %+v", tab.Skipped)
	}
	_, skips := NewIndex(tab).AnalyzeKotlinBodies(varargFiles)
	if skips[SkipKtLookupAmbiguous] == 0 {
		t.Errorf("a vararg overload must forfeit (name,arity): %+v (JVMSOUND-001)", skips)
	}

	// default value: h(a, b = 0) beside h(s: String) — the defaulted member can
	// satisfy h("x") at arity 1, so a same-arity sibling must forfeit.
	defaultFiles := map[string][]byte{
		"ef/D.kt": []byte(`package ef
class D {
    fun h(a: Int, b: Int = 0) {}
    fun h(s: String) {}
    fun g() { h("x") }
}
`),
	}
	tab2 := BuildTable(defaultFiles)
	if len(tab2.Skipped) != 0 {
		t.Fatalf("default fixture must table cleanly: %+v", tab2.Skipped)
	}
	_, skips2 := NewIndex(tab2).AnalyzeKotlinBodies(defaultFiles)
	if skips2[SkipKtLookupAmbiguous] == 0 {
		t.Errorf("a defaulted-parameter overload must forfeit (name,arity): %+v (JVMSOUND-001)", skips2)
	}
}

func TestAnalyzeKotlinBodies_Deterministic(t *testing.T) {
	s1, k1 := analyzeKotlin(t)
	s2, k2 := analyzeKotlin(t)
	if len(s1) != len(s2) {
		t.Fatalf("site counts differ: %d vs %d", len(s1), len(s2))
	}
	for i := range s1 {
		if s1[i].Name != s2[i].Name || s1[i].Line != s2[i].Line || s1[i].Kind != s2[i].Kind {
			t.Fatalf("site %d differs: %+v vs %+v", i, s1[i], s2[i])
		}
	}
	for k, v := range k1 {
		if k2[k] != v {
			t.Fatalf("skip counter %s differs: %d vs %d", k, v, k2[k])
		}
	}
}
