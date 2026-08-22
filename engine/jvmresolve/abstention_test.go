package jvmresolve

// W0.g — legible abstention at the binder seam: the named skip counters the
// walkers and the emitter tally must LEAVE this package through
// typeresolve.Result.NamedSkips. Until SW-171 they were dropped on the floor
// at the seam adapter (`sites, _ = ix.AnalyzeJavaBodies(files)`), so the most
// disciplined abstainer in the system abstained invisibly.
//
// The gate these tests have to clear is non-vacuity: an assertion that a
// counter map "has no unexpected entries" is satisfied by a map that is always
// empty, and would go on passing if the plumbing were deleted again. So the
// abstention fixture is built to FORCE named skips and the assertions name the
// exact reasons and counts, with a control fixture proving the same assertion
// machinery reports nothing when there is nothing to report.

import (
	"reflect"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// abstentionFixture forces four skips under three distinct named reasons, one
// per mechanism the vocabulary distinguishes, alongside one call that DOES
// bind (so the fixture is not merely broken code):
//
//	param.value()   binds — Rate is intra-repo and declared as a parameter
//	inferred.value() java_var_inferred      — `var` local, no declared type
//	param.missing() java_lookup_not_found   — closed chain provably lacks it
//	ext.size()      java_receiver_external  — java.util.List is outside the repo
//	u.thing()       java_receiver_external  — Unknown resolves to no repo type
func abstentionFixture() map[string][]byte {
	return map[string][]byte{
		"com/tax/Rate.java": []byte(`package com.tax;
public class Rate {
    public void value() {}
}
`),
		"com/shop/Shop.java": []byte(`package com.shop;
import com.tax.Rate;
public class Shop {
    public void run(Rate param) {
        param.value();
        var inferred = param;
        inferred.value();
        param.missing();
        java.util.List<String> ext = null;
        ext.size();
        Unknown u = null;
        u.thing();
    }
}
`),
	}
}

// cleanFixture is the CONTROL: every call binds, so a correct implementation
// reports no named skip at all. It is what makes the abstention assertions
// above meaningful — the same code path, exercised over input that must
// produce nothing.
func cleanFixture() map[string][]byte {
	return map[string][]byte{
		"com/tax/Rate.java": []byte(`package com.tax;
public class Rate {
    public void value() {}
}
`),
		"com/shop/Shop.java": []byte(`package com.shop;
import com.tax.Rate;
public class Shop {
    public void run(Rate param) {
        param.value();
    }
}
`),
	}
}

// TestResolve_CarriesNamedSkips is the plumbing pin: Resolve must publish the
// walker's named gaps, with the exact reasons and counts the fixture forces.
// Exact equality is deliberate — a "contains at least" assertion would pass on
// a map that silently gained or lost entries, and the whole point of a NAMED
// skip is that the name and the number are the message.
func TestResolve_CarriesNamedSkips(t *testing.T) {
	res, err := NewResolver(LangJava).Resolve(abstentionFixture(), map[model.NodeId]struct{}{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := map[string]int{
		SkipVarInferred:      1,
		SkipLookupNotFound:   1,
		SkipReceiverExternal: 2,
	}
	if !reflect.DeepEqual(res.NamedSkips, want) {
		t.Errorf("NamedSkips = %#v, want %#v", res.NamedSkips, want)
	}
	// Non-vacuity, stated as an assertion rather than trusted: the fixture
	// really did produce skips, so a green run above cannot be an empty map
	// agreeing with an empty expectation.
	total := 0
	for _, n := range res.NamedSkips {
		total += n
	}
	if total == 0 {
		t.Fatal("the abstention fixture forced no skip at all — the test would be vacuous")
	}
}

// TestResolve_CleanFixtureAbstainsFromNothing is the control half: the same
// path over binding-only input publishes NO named skip. nil and empty are the
// same statement here ("nothing recorded"), and both are accepted; a non-empty
// map would mean the counters are firing on sound bindings, which would make
// every abstention notice downstream noise.
func TestResolve_CleanFixtureAbstainsFromNothing(t *testing.T) {
	res, err := NewResolver(LangJava).Resolve(cleanFixture(), map[model.NodeId]struct{}{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.NamedSkips) != 0 {
		t.Errorf("clean fixture produced named skips %#v, want none", res.NamedSkips)
	}
}

// TestMergeSkips_DropsZerosAndNilsOut pins the two properties downstream
// depends on: a zero count is never published as an observed abstention (an
// explicit 0 would read as a measured absence rather than a non-observation),
// and "nothing recorded" is nil rather than an empty map, so the ingest fold
// can skip the language entirely.
func TestMergeSkips_DropsZerosAndNilsOut(t *testing.T) {
	if got := mergeSkips(SkipCounts{"a": 0}, SkipCounts{}); got != nil {
		t.Errorf("mergeSkips of zeros = %#v, want nil", got)
	}
	got := mergeSkips(SkipCounts{"a": 2, "z": 0}, SkipCounts{"a": 1, "b": 3})
	want := map[string]int{"a": 3, "b": 3}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mergeSkips = %#v, want %#v", got, want)
	}
}
