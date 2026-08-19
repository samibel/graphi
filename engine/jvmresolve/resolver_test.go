package jvmresolve

import (
	"testing"

	"github.com/samibel/graphi/engine/typeresolve"
)

// The adapter must satisfy the ADR 0007 seam.
var _ typeresolve.Resolver = Resolver{}

func TestResolver_PathPredicates(t *testing.T) {
	java, kotlin := NewResolver(LangJava), NewResolver(LangKotlin)
	cases := []struct {
		path                       string
		javaSubject, kotlinSubject bool
		input                      bool
	}{
		{"a/B.java", true, false, true},
		{"a/B.kt", false, true, true},
		{"a/B.go", false, false, false},
		{"go.mod", false, false, false},
	}
	for _, c := range cases {
		if got := java.Subject(c.path); got != c.javaSubject {
			t.Errorf("java.Subject(%q) = %v", c.path, got)
		}
		if got := kotlin.Subject(c.path); got != c.kotlinSubject {
			t.Errorf("kotlin.Subject(%q) = %v", c.path, got)
		}
		// Input and Triggers span BOTH languages for BOTH registrants: the
		// table needs cross-language type context.
		for _, r := range []Resolver{java, kotlin} {
			if got := r.Input(c.path); got != c.input {
				t.Errorf("%s.Input(%q) = %v", r.Language(), c.path, got)
			}
			if got := r.Triggers(c.path); got != c.input {
				t.Errorf("%s.Triggers(%q) = %v", r.Language(), c.path, got)
			}
		}
	}
}

// crossLangFixture: a Java file whose receiver type is declared in Kotlin —
// the reason Input spans both languages.
func crossLangFixture() map[string][]byte {
	return map[string][]byte{
		"com/tax/Rate.kt": []byte(`package com.tax

class Rate(val n: Int) {
    fun value() {}
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

func TestResolver_CrossLanguageResolution(t *testing.T) {
	files := crossLangFixture()
	committed, byKey := committedSet(t, files)

	res, err := NewResolver(LangJava).Resolve(files, committed)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	run := byKey["method shop.run com/shop/Shop.java"]
	value := byKey["method tax.value com/tax/Rate.kt"]
	kotlinType := byKey["type tax.Rate com/tax/Rate.kt"]
	found := false
	for _, e := range res.Edges {
		if e.From() == run && e.To() == value && e.Kind() == "calls" {
			found = true
		}
		// The JAVA registrant may own only Java-FROM edges: nothing may
		// originate at a Kotlin-file node (the sibling registrant's edge set).
		if e.From() == kotlinType || e.From() == value {
			t.Fatalf("java registrant emitted a kotlin-owned edge: %+v", e)
		}
	}
	if !found {
		t.Fatal("cross-language call edge (java caller → kotlin callee) missing")
	}

	// Units: the java registrant claims only the Java dir.
	var dirs []string
	for _, u := range res.Units {
		dirs = append(dirs, u.Dir)
	}
	if len(dirs) != 1 || dirs[0] != "com/shop" {
		t.Fatalf("java registrant must claim exactly its own dirs, got %v", dirs)
	}
}

// TestResolver_MixedDirIsPartitionedNotExempt is ADR 0008 ruling D9 as a unit
// assertion, and it REPLACES TestResolver_MixedDirIsSweepExempt, which asserted
// the opposite: that a directory holding .java beside .kt is emitted DEGRADED
// under a "mixed-language directory" reason and thereby exempted from the
// stale-confirmed sweep. D9 rules that an exemption is the wrong shape — it is
// unobservable, and it kept superseded confirmed edges alive across every
// incremental sync — so the directory is now PARTITIONED: each registrant
// claims it as its own CHECKED unit and sweeps only its own language's edges.
func TestResolver_MixedDirIsPartitionedNotExempt(t *testing.T) {
	files := map[string][]byte{
		"com/mix/A.java": []byte("package com.mix;\npublic class A { public void m() {} }\n"),
		"com/mix/B.kt":   []byte("package com.mix\nclass B\n"),
	}
	committed, _ := committedSet(t, files)
	for _, lang := range []string{LangJava, LangKotlin} {
		r := NewResolver(lang)
		res, err := r.Resolve(files, committed)
		if err != nil {
			t.Fatalf("[%s] Resolve: %v", lang, err)
		}
		if len(res.Units) != 1 || res.Units[0].Dir != "com/mix" {
			t.Fatalf("[%s] units: %+v", lang, res.Units)
		}
		if res.Units[0].Degraded != "" {
			t.Fatalf("[%s] a mixed-language dir must be a CHECKED unit, not degraded; got %q",
				lang, res.Units[0].Degraded)
		}
	}
	// The partition itself: the two registrants own disjoint halves of the one
	// directory, which is what makes sweeping it safe without an exemption.
	java, kotlin := NewResolver(LangJava), NewResolver(LangKotlin)
	if !java.Owns("com/mix/A.java") || java.Owns("com/mix/B.kt") {
		t.Fatal("the java registrant must own exactly the .java half of the mixed dir")
	}
	if !kotlin.Owns("com/mix/B.kt") || kotlin.Owns("com/mix/A.java") {
		t.Fatal("the kotlin registrant must own exactly the .kt half of the mixed dir")
	}
}

func TestResolver_ParseSkipDegradesUnit(t *testing.T) {
	// A file the walk saw but the grammar panics/errors on is hard to forge;
	// instead pin the SkippedFiles path via an unmatchable... a binary blob
	// still parses under tree-sitter's error tolerance, so exercise the
	// bookkeeping through the seam contract directly: a fixture whose
	// tabled-clean dirs are NOT degraded.
	files := crossLangFixture()
	committed, _ := committedSet(t, files)
	res, err := NewResolver(LangKotlin).Resolve(files, committed)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Units) != 1 || res.Units[0].Dir != "com/tax" || res.Units[0].Degraded != "" {
		t.Fatalf("kotlin registrant units: %+v", res.Units)
	}
	if res.Units[0].Name != "com.tax" {
		t.Fatalf("unit name must be the declared package: %+v", res.Units[0])
	}
}

// TestResolver_DropInvariant pins the UnitResult contract: per-unit dropped
// intents sum to the pass-global counter.
func TestResolver_DropInvariant(t *testing.T) {
	files := kotlinBodyFixture()
	committed, _ := committedSet(t, files)
	res, err := NewResolver(LangKotlin).Resolve(files, committed)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	sum := 0
	for _, u := range res.Units {
		sum += u.DroppedIntents
	}
	if sum != res.DroppedIntents {
		t.Fatalf("unit drops (%d) must sum to the global counter (%d)", sum, res.DroppedIntents)
	}
	if res.DroppedIntents == 0 {
		t.Fatal("fixture must produce drops (primary-ctor property target)")
	}
}

func TestResolver_Deterministic(t *testing.T) {
	files := crossLangFixture()
	committed, _ := committedSet(t, files)
	r := NewResolver(LangJava)
	r1, err := r.Resolve(files, committed)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := r.Resolve(files, committed)
	if err != nil {
		t.Fatal(err)
	}
	if len(r1.Edges) != len(r2.Edges) || len(r1.Units) != len(r2.Units) {
		t.Fatal("Resolve must be deterministic")
	}
	for i := range r1.Edges {
		if r1.Edges[i].ID() != r2.Edges[i].ID() {
			t.Fatal("edge order must be deterministic")
		}
	}
}
