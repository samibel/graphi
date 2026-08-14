package jvmresolve

import (
	"context"
	"sort"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
)

// The golden cross-test (WP-J2 / gate G2a): the REAL core/parse extractors run
// over fixture sources, and this package's reconstruction must reproduce their
// symbol-node id sets BYTE-EXACTLY — every reconstructed id minted, every
// minted id reconstructed. It is the jvmresolve analog of engine/typeresolve's
// qn_test.go and the load-bearing artifact of the phase: any drift in kind
// strings, the flat QN convention, the package-key derivation, the
// collection depth rules, or the first-binding-wins dedup fails HERE instead
// of silently dropping confirmed edges later.

// extractSymbolIDs parses src with the registry parser for path and returns
// the minted SYMBOL node ids (the file node and the interned package node are
// outside the symbol table by design) plus their id→node index for diagnosis.
func extractSymbolIDs(t *testing.T, path, src string) (map[model.NodeId]model.Node, []model.NodeId) {
	t.Helper()
	reg := parse.NewDefaultRegistry()
	p, err := reg.ParserFor(path)
	if err != nil {
		t.Fatalf("no parser for %s: %v", path, err)
	}
	res, err := p.Parse(context.Background(), path, []byte(src))
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	byID := map[model.NodeId]model.Node{}
	var ids []model.NodeId
	for _, n := range res.Nodes {
		if n.Kind() == "file" || n.Kind() == "package" {
			continue
		}
		byID[n.ID()] = n
		ids = append(ids, n.ID())
	}
	return byID, ids
}

// assertSameIDSet asserts set equality with readable diagnosis.
func assertSameIDSet(t *testing.T, byID map[model.NodeId]model.Node, extracted, reconstructed []model.NodeId) {
	t.Helper()
	got := map[model.NodeId]struct{}{}
	for _, id := range reconstructed {
		if _, dup := got[id]; dup {
			t.Errorf("reconstruction emitted duplicate id %s", id)
		}
		got[id] = struct{}{}
	}
	want := map[model.NodeId]struct{}{}
	for _, id := range extracted {
		want[id] = struct{}{}
	}
	for id := range want {
		if _, ok := got[id]; !ok {
			n := byID[id]
			t.Errorf("extractor minted a node the reconstruction misses: %s %s (%s)", n.Kind(), n.QualifiedName(), id)
		}
	}
	for id := range got {
		if _, ok := want[id]; !ok {
			t.Errorf("reconstruction fabricated an id the extractor never minted: %s", id)
		}
	}
	if t.Failed() {
		var dump []string
		for _, id := range extracted {
			n := byID[id]
			dump = append(dump, n.Kind()+" "+n.QualifiedName())
		}
		sort.Strings(dump)
		t.Logf("extractor symbol nodes:\n  %v", dump)
	}
}

// TestGolden_JavaIdentity pins the Java rules: flat "<dirBase>.<bare>" QNs,
// top-level-only collection (a nested type and its members mint NOTHING),
// declared-form field kinds, and first-binding-wins across kinds.
func TestGolden_JavaIdentity(t *testing.T) {
	const path = "com/shop/Shop.java"
	const src = `package com.shop;

import java.util.List;

public class Shop {
    public static final int MAX = 10;
    private int total;
    private int a, b;

    public int total() { return total; }
    public void add(int x) { this.total += x; }
    public void add(int x, int y) { this.total += x + y; }

    class Inner {
        void innerM() {}
        int innerField;
    }
}

interface Pricer {
    int RATE = 1;
    int rate(int amount);
}

enum Color {
    RED, GREEN;
    void colorM() {}
}
`
	byID, extracted := extractSymbolIDs(t, path, src)

	// The Phase A table for this file, in extractor discovery order. The
	// entries that must map to ok=false are listed too — the cross-test proves
	// they contribute nothing rather than omitting them silently.
	decls := []Decl{
		{Form: JavaType, File: path, Name: "Shop"},
		{Form: JavaField, File: path, Name: "MAX", TypeDepth: 1, StaticFinal: true},
		// `total` the FIELD is discovered before `total` the METHOD: the field
		// wins the bare name, the method mints no node (first-binding-wins).
		{Form: JavaField, File: path, Name: "total", TypeDepth: 1},
		{Form: JavaField, File: path, Name: "a", TypeDepth: 1},
		{Form: JavaField, File: path, Name: "b", TypeDepth: 1},
		{Form: JavaMethod, File: path, Name: "total", TypeDepth: 1},
		// Overloads share kind and flat QN → one id; the dedup collapses them.
		{Form: JavaMethod, File: path, Name: "add", TypeDepth: 1},
		{Form: JavaMethod, File: path, Name: "add", TypeDepth: 1},
		// Nested type and its members: TypeDepth rules → no nodes at all.
		{Form: JavaType, File: path, Name: "Inner", TypeDepth: 1},
		{Form: JavaMethod, File: path, Name: "innerM", TypeDepth: 2},
		{Form: JavaField, File: path, Name: "innerField", TypeDepth: 2},
		{Form: JavaType, File: path, Name: "Pricer"},
		// Interface field: constant_declaration — constant by declared form.
		{Form: JavaConstantField, File: path, Name: "RATE", TypeDepth: 1},
		{Form: JavaMethod, File: path, Name: "rate", TypeDepth: 1},
		{Form: JavaType, File: path, Name: "Color"},
		// Enum members sit under enum_body_declarations, which the collector
		// never descends into: no node (pinned by EnclosingEnum).
		{Form: JavaMethod, File: path, Name: "colorM", TypeDepth: 1, EnclosingEnum: true},
	}
	assertSameIDSet(t, byID, extracted, FileNodeIDs(decls))

	// Spot-pin the declared-form kind rules through DeclNode directly.
	if k, qn, ok := DeclNode(decls[1]); !ok || k != KindConstant || qn != "shop.MAX" {
		t.Errorf("static final field: got (%s, %s, %v), want (constant, shop.MAX, true)", k, qn, ok)
	}
	if k, _, ok := DeclNode(decls[2]); !ok || k != KindVariable {
		t.Errorf("plain field must be a variable, got (%s, %v)", k, ok)
	}
	if _, _, ok := DeclNode(decls[8]); ok {
		t.Error("a nested Java type must map to NO node")
	}
}

// TestGolden_KotlinIdentity pins the Kotlin rules: recursive collection
// through class bodies (nested types and their members DO mint nodes),
// function vs method by enclosing-type depth, declared-const property kinds.
func TestGolden_KotlinIdentity(t *testing.T) {
	const path = "com/shop/Basket.kt"
	const src = `package com.shop

import java.util.ArrayList

const val LIMIT = 10
val label = "basket"

fun make(): Basket = Basket()

class Basket {
    val items = ArrayList<String>()
    fun add(item: String) { items.add(item) }

    class Nested {
        fun deep() {}
    }

    companion object {
        const val TAG = "basket"
        fun make(): Basket = Basket()
    }
}

object Registry {
    fun lookup(name: String): Basket? = null
}

interface Priced {
    fun price(): Int
}

enum class Status {
    OPEN, CLOSED;
    fun pretty(): String = ""
}
`
	byID, extracted := extractSymbolIDs(t, path, src)

	decls := []Decl{
		{Form: KotlinProperty, File: path, Name: "LIMIT", Const: true},
		{Form: KotlinProperty, File: path, Name: "label"},
		{Form: KotlinFunction, File: path, Name: "make"},
		{Form: KotlinType, File: path, Name: "Basket"},
		{Form: KotlinProperty, File: path, Name: "items", TypeDepth: 1},
		{Form: KotlinFunction, File: path, Name: "add", TypeDepth: 1},
		// Kotlin collection recurses: the nested type and its member exist.
		{Form: KotlinType, File: path, Name: "Nested", TypeDepth: 1},
		{Form: KotlinFunction, File: path, Name: "deep", TypeDepth: 2},
		// A companion and its members mint NOTHING — the collector never
		// matches companion_object and never enters its body.
		{Form: KotlinCompanion, File: path, Name: "Companion", TypeDepth: 1},
		{Form: KotlinProperty, File: path, Name: "TAG", TypeDepth: 2, Const: true, EnclosingCompanion: true},
		{Form: KotlinFunction, File: path, Name: "make", TypeDepth: 2, EnclosingCompanion: true},
		{Form: KotlinType, File: path, Name: "Registry"},
		{Form: KotlinFunction, File: path, Name: "lookup", TypeDepth: 1},
		{Form: KotlinType, File: path, Name: "Priced"},
		{Form: KotlinFunction, File: path, Name: "price", TypeDepth: 1},
		// The enum CLASS mints a type node, but its body is enum_class_body —
		// never entered — so its member function does not exist.
		{Form: KotlinType, File: path, Name: "Status"},
		{Form: KotlinFunction, File: path, Name: "pretty", TypeDepth: 1, EnclosingEnum: true},
	}
	assertSameIDSet(t, byID, extracted, FileNodeIDs(decls))

	if k, qn, ok := DeclNode(decls[0]); !ok || k != KindConstant || qn != "shop.LIMIT" {
		t.Errorf("const val: got (%s, %s, %v), want (constant, shop.LIMIT, true)", k, qn, ok)
	}
	if k, _, ok := DeclNode(decls[2]); !ok || k != KindFunction {
		t.Errorf("top-level fun must be a function, got (%s, %v)", k, ok)
	}
	if k, _, ok := DeclNode(decls[5]); !ok || k != KindMethod {
		t.Errorf("member fun must be a method, got (%s, %v)", k, ok)
	}
}

// TestFilePackage_MirrorsLangPackage pins the package-key derivation against
// core/parse's convention for the shapes the walk produces (repo-relative,
// forward slashes), including the root-file stem fallback.
func TestFilePackage_MirrorsLangPackage(t *testing.T) {
	cases := map[string]string{
		"com/shop/Price.java": "shop",
		"src/main/App.kt":     "main",
		"Price.java":          "Price",
		"Basket.kt":           "Basket",
	}
	for file, want := range cases {
		if got := filePackage(file); got != want {
			t.Errorf("filePackage(%q) = %q, want %q", file, got, want)
		}
	}
}
