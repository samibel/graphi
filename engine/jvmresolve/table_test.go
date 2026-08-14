package jvmresolve

import (
	"reflect"
	"testing"
)

// TestBuildTable_Java pins the Phase A Java walk: package/import clauses,
// supertype clauses as written, declared member types with erasure, nested
// types, records, and the enum-body flagging that lines the table up with
// qn.go's no-node facts.
func TestBuildTable_Java(t *testing.T) {
	src := `package com.shop;

import java.util.List;
import java.util.*;
import static java.lang.Math.max;

public class Shop extends Base implements Pricer, AutoCloseable {
    private List<String> names;
    static final int MAX = 3;
    int a, b;

    public Shop(int size) {}
    public int rate(String s, int[] weights) { return 0; }

    class Inner {
        void innerM() {}
    }

    record Pair(int left, int right) {}
}

interface Pricer extends Comparable {
    int RATE = 1;
    int rate(String s, int[] weights);
}

enum Color {
    RED, GREEN;
    void colorM() {}
}
`
	tab := BuildTable(map[string][]byte{"com/shop/Shop.java": []byte(src), "README.md": []byte("x")})
	if len(tab.Skipped) != 0 {
		t.Fatalf("unexpected skips: %+v", tab.Skipped)
	}
	if len(tab.Files) != 1 {
		t.Fatalf("want 1 tabled file, got %d", len(tab.Files))
	}
	f := tab.Files[0]
	if f.Package != "com.shop" || f.Language != LangJava {
		t.Fatalf("package/language: %+v", f)
	}

	wantImports := []Import{
		{Path: "java.util.List", Line: 3},
		{Path: "java.util", Wildcard: true, Line: 4},
		{Path: "java.lang.Math.max", Static: true, Line: 5},
	}
	if !reflect.DeepEqual(f.Imports, wantImports) {
		t.Fatalf("imports:\n got %+v\nwant %+v", f.Imports, wantImports)
	}

	byFQN := tab.TypesByFQN()
	shop := byFQN["com.shop.Shop"][0]
	if shop.Form != FormClass || len(shop.Nesting) != 0 {
		t.Fatalf("Shop: %+v", shop)
	}
	wantSupers := []TypeRef{
		{Raw: "Base", Base: "Base"},
		{Raw: "Pricer", Base: "Pricer"},
		{Raw: "AutoCloseable", Base: "AutoCloseable"},
	}
	if !reflect.DeepEqual(shop.Supertypes, wantSupers) {
		t.Fatalf("Shop supertypes:\n got %+v\nwant %+v", shop.Supertypes, wantSupers)
	}

	members := map[string]Member{}
	for _, m := range shop.Members {
		members[m.Form+"/"+m.Name] = m
	}
	if m := members["field/names"]; m.Type.Base != "List" || m.Type.Raw != "List<String>" || m.Static || m.Final {
		t.Fatalf("names field: %+v", m)
	}
	if m := members["field/MAX"]; !m.Static || !m.Final || m.Type.Base != "int" {
		t.Fatalf("MAX field: %+v", m)
	}
	if _, ok := members["field/a"]; !ok {
		t.Fatal("multi-declarator field a missing")
	}
	if _, ok := members["field/b"]; !ok {
		t.Fatal("multi-declarator field b missing")
	}
	if m := members["constructor/Shop"]; len(m.Params) != 1 || m.Params[0].Type.Base != "int" {
		t.Fatalf("constructor: %+v", m)
	}
	rate := members["method/rate"]
	if rate.Type.Base != "int" || len(rate.Params) != 2 {
		t.Fatalf("rate: %+v", rate)
	}
	// Array parameter erases to its element base; Raw keeps the brackets.
	if rate.Params[1].Type.Base != "int" || rate.Params[1].Type.Raw != "int[]" {
		t.Fatalf("rate weights param: %+v", rate.Params[1])
	}

	inner := byFQN["com.shop.Shop.Inner"][0]
	if !reflect.DeepEqual(inner.Nesting, []string{"Shop"}) || inner.Form != FormClass {
		t.Fatalf("Inner: %+v", inner)
	}
	if len(inner.Members) != 1 || inner.Members[0].Name != "innerM" {
		t.Fatalf("Inner members: %+v", inner.Members)
	}

	pair := byFQN["com.shop.Shop.Pair"][0]
	if pair.Form != FormRecord || len(pair.Members) != 2 || !pair.Members[0].Final {
		t.Fatalf("record components: %+v", pair)
	}

	pricer := byFQN["com.shop.Pricer"][0]
	if pricer.Form != FormInterface || len(pricer.Supertypes) != 1 || pricer.Supertypes[0].Base != "Comparable" {
		t.Fatalf("Pricer: %+v", pricer)
	}
	var rateConst Member
	for _, m := range pricer.Members {
		if m.Name == "RATE" {
			rateConst = m
		}
	}
	if !rateConst.ConstantDecl {
		t.Fatalf("interface field must carry ConstantDecl: %+v", rateConst)
	}

	color := byFQN["com.shop.Color"][0]
	if color.Form != FormEnum {
		t.Fatalf("Color: %+v", color)
	}
	forms := map[string]bool{}
	for _, m := range color.Members {
		forms[m.Form+"/"+m.Name] = m.InEnumBody
		if !m.InEnumBody {
			t.Fatalf("every enum member must be flagged InEnumBody: %+v", m)
		}
	}
	if !forms["enum-const/RED"] || !forms["enum-const/GREEN"] || !forms["method/colorM"] {
		t.Fatalf("enum members: %+v", color.Members)
	}
}

// TestBuildTable_Kotlin pins the Phase A Kotlin walk: aliased and wildcard
// imports, delegation supertypes, primary-constructor properties, inferred
// (zero) vs declared property types, companions and enum-class flagging.
func TestBuildTable_Kotlin(t *testing.T) {
	src := `package com.shop

import java.util.ArrayList
import java.util.concurrent.atomic.AtomicInteger as Counter
import java.util.*

const val LIMIT = 10
fun top(x: Int): String = ""

class Basket(val owner: String, capacity: Int) : Base(), Priced {
    val items: ArrayList<String> = ArrayList()
    var count = 0
    fun add(item: String, n: Int): Boolean { return true }

    companion object {
        fun make(): Basket = Basket("", 0)
    }
}

enum class Status {
    OPEN, CLOSED;
    fun pretty(): String = ""
}
`
	tab := BuildTable(map[string][]byte{"com/shop/Basket.kt": []byte(src)})
	if len(tab.Skipped) != 0 {
		t.Fatalf("unexpected skips: %+v", tab.Skipped)
	}
	f := tab.Files[0]
	if f.Package != "com.shop" {
		t.Fatalf("package: %q", f.Package)
	}
	wantImports := []Import{
		{Path: "java.util.ArrayList", Line: 3},
		{Path: "java.util.concurrent.atomic.AtomicInteger", Alias: "Counter", Line: 4},
		{Path: "java.util", Wildcard: true, Line: 5},
	}
	if !reflect.DeepEqual(f.Imports, wantImports) {
		t.Fatalf("imports:\n got %+v\nwant %+v", f.Imports, wantImports)
	}

	if len(f.TopLevel) != 2 {
		t.Fatalf("top-level members: %+v", f.TopLevel)
	}
	if m := f.TopLevel[0]; m.Form != MemberProperty || m.Name != "LIMIT" || !m.Const || !m.Type.IsZero() {
		t.Fatalf("LIMIT: %+v", m)
	}
	if m := f.TopLevel[1]; m.Form != MemberFunction || m.Name != "top" || m.Type.Base != "String" || len(m.Params) != 1 || m.Params[0].Type.Base != "Int" {
		t.Fatalf("top: %+v", m)
	}

	byFQN := tab.TypesByFQN()
	basket := byFQN["com.shop.Basket"][0]
	wantSupers := []TypeRef{{Raw: "Base", Base: "Base"}, {Raw: "Priced", Base: "Priced"}}
	if !reflect.DeepEqual(basket.Supertypes, wantSupers) {
		t.Fatalf("Basket supertypes: %+v", basket.Supertypes)
	}
	members := map[string]Member{}
	for _, m := range basket.Members {
		members[m.Form+"/"+m.Name] = m
	}
	// `val owner` in the primary constructor declares a property; plain
	// `capacity` does not.
	if m, ok := members["property/owner"]; !ok || m.Type.Base != "String" {
		t.Fatalf("owner property: %+v (ok=%v)", m, ok)
	}
	if _, ok := members["property/capacity"]; ok {
		t.Fatal("a non-val/var class parameter must not become a property")
	}
	if m := members["constructor/Basket"]; len(m.Params) != 2 || m.Params[1].Name != "capacity" {
		t.Fatalf("primary constructor: %+v", m)
	}
	if m := members["property/items"]; m.Type.Base != "ArrayList" || m.Type.Raw != "ArrayList<String>" {
		t.Fatalf("items: %+v", m)
	}
	// Inferred `var count = 0` stays a ZERO TypeRef — a named gap, no guess.
	if m := members["property/count"]; !m.Type.IsZero() {
		t.Fatalf("inferred property must have zero type: %+v", m)
	}
	if m := members["function/add"]; m.Type.Base != "Boolean" || len(m.Params) != 2 {
		t.Fatalf("add: %+v", m)
	}

	comp := byFQN["com.shop.Basket.Companion"][0]
	if comp.Form != FormCompanion || !reflect.DeepEqual(comp.Nesting, []string{"Basket"}) {
		t.Fatalf("companion: %+v", comp)
	}
	if len(comp.Members) != 1 || !comp.Members[0].InCompanion || comp.Members[0].Name != "make" {
		t.Fatalf("companion members: %+v", comp.Members)
	}

	status := byFQN["com.shop.Status"][0]
	if status.Form != FormEnum {
		t.Fatalf("Status: %+v", status)
	}
	for _, m := range status.Members {
		if !m.InEnumBody {
			t.Fatalf("enum-class member must be flagged InEnumBody: %+v", m)
		}
	}
}

// TestBuildTable_Determinism pins sorted-path table order and byte-identical
// re-runs over the same input.
func TestBuildTable_Determinism(t *testing.T) {
	files := map[string][]byte{
		"b/B.java": []byte("package b; class B {}"),
		"a/A.kt":   []byte("package a\nclass A"),
	}
	t1, t2 := BuildTable(files), BuildTable(files)
	if !reflect.DeepEqual(t1, t2) {
		t.Fatal("BuildTable must be deterministic")
	}
	if len(t1.Files) != 2 || t1.Files[0].Path != "a/A.kt" || t1.Files[1].Path != "b/B.java" {
		t.Fatalf("files must table in sorted-path order: %+v", t1.Files)
	}
}
