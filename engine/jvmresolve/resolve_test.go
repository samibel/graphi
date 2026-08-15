package jvmresolve

import "testing"

// resolveFixture builds a three-package repository exercising every step of
// the scope walk.
func resolveFixture(t *testing.T) *Index {
	t.Helper()
	files := map[string][]byte{
		"com/shop/Shop.java": []byte(`package com.shop;
import com.tax.Rate;
import com.util.*;
import com.legacy.*;
public class Shop {
    class Inner {}
    Rate rate;
    Helper helper;
}
class Sibling {}
`),
		"com/tax/Rate.java": []byte(`package com.tax;
public class Rate {}
`),
		"com/util/Helper.java": []byte(`package com.util;
public class Helper {}
class Dup {}
`),
		"com/legacy/Legacy.java": []byte(`package com.legacy;
public class Legacy {}
class Dup {}
`),
		"com/shop/Local.kt": []byte(`package com.shop
import com.tax.Rate as Levy
class Basket {
    class Nested
}
`),
	}
	tab := BuildTable(files)
	if len(tab.Skipped) != 0 {
		t.Fatalf("fixture must table cleanly: %+v", tab.Skipped)
	}
	return NewIndex(tab)
}

// fileOf returns the tabled file by path.
func fileOf(t *testing.T, ix *Index, path string) *File {
	t.Helper()
	tab := ix.Table()
	for i := range tab.Files {
		if tab.Files[i].Path == path {
			return &tab.Files[i]
		}
	}
	t.Fatalf("no tabled file %s", path)
	return nil
}

func typeOf(t *testing.T, ix *Index, fqn string) *Type {
	t.Helper()
	cands := ix.byFQN[fqn]
	if len(cands) != 1 {
		t.Fatalf("fixture type %s: %d candidates", fqn, len(cands))
	}
	return cands[0]
}

func TestResolveTypeName_ScopeWalk(t *testing.T) {
	ix := resolveFixture(t)
	shopFile := fileOf(t, ix, "com/shop/Shop.java")
	shop := typeOf(t, ix, "com.shop.Shop")

	// Enclosing scope: a type nested directly in the enclosing type.
	if got, res := ix.ResolveTypeName(shopFile, shop, "Inner"); res != ResolvedType || got.FQN != "com.shop.Shop.Inner" {
		t.Fatalf("Inner: %v %v", got, res)
	}
	// The enclosing type's own name binds itself.
	if got, res := ix.ResolveTypeName(shopFile, shop, "Shop"); res != ResolvedType || got.FQN != "com.shop.Shop" {
		t.Fatalf("Shop self: %v %v", got, res)
	}
	// Same file's top-level types.
	if got, res := ix.ResolveTypeName(shopFile, shop, "Sibling"); res != ResolvedType || got.FQN != "com.shop.Sibling" {
		t.Fatalf("Sibling: %v %v", got, res)
	}
	// Single-type import → the imported repo type.
	if got, res := ix.ResolveTypeName(shopFile, shop, "Rate"); res != ResolvedType || got.FQN != "com.tax.Rate" {
		t.Fatalf("Rate: %v %v", got, res)
	}
	// Same package across files.
	if got, res := ix.ResolveTypeName(shopFile, shop, "Basket"); res != ResolvedType || got.FQN != "com.shop.Basket" {
		t.Fatalf("Basket: %v %v", got, res)
	}
	// Exactly one wildcard-imported package provides Helper.
	if got, res := ix.ResolveTypeName(shopFile, shop, "Helper"); res != ResolvedType || got.FQN != "com.util.Helper" {
		t.Fatalf("Helper: %v %v", got, res)
	}
	// BOTH wildcard packages provide Dup → the JLS ambiguous-import case:
	// dropped, never ranked.
	if _, res := ix.ResolveTypeName(shopFile, shop, "Dup"); res != AmbiguousType {
		t.Fatalf("Dup must be ambiguous, got %v", res)
	}
	// Unknown everywhere → external (java.lang included by construction).
	if _, res := ix.ResolveTypeName(shopFile, shop, "String"); res != ExternalType {
		t.Fatalf("String must be external, got %v", res)
	}
}

func TestResolveTypeName_QualifiedAndAlias(t *testing.T) {
	ix := resolveFixture(t)
	shopFile := fileOf(t, ix, "com/shop/Shop.java")
	localFile := fileOf(t, ix, "com/shop/Local.kt")

	// Exact FQN as written.
	if got, res := ix.ResolveTypeName(shopFile, nil, "com.tax.Rate"); res != ResolvedType || got.FQN != "com.tax.Rate" {
		t.Fatalf("FQN: %v %v", got, res)
	}
	// Package-relative qualified name from the same package.
	if got, res := ix.ResolveTypeName(shopFile, nil, "Shop.Inner"); res != ResolvedType || got.FQN != "com.shop.Shop.Inner" {
		t.Fatalf("Shop.Inner: %v %v", got, res)
	}
	// Anchored chain: first segment resolves (same package), rest appended.
	if got, res := ix.ResolveTypeName(localFile, nil, "Basket.Nested"); res != ResolvedType || got.FQN != "com.shop.Basket.Nested" {
		t.Fatalf("Basket.Nested: %v %v", got, res)
	}
	// A Kotlin import alias binds the ALIAS, not the original name.
	if got, res := ix.ResolveTypeName(localFile, nil, "Levy"); res != ResolvedType || got.FQN != "com.tax.Rate" {
		t.Fatalf("Levy alias: %v %v", got, res)
	}
	// An unknown qualified name stays external.
	if _, res := ix.ResolveTypeName(shopFile, nil, "org.springframework.web.Client"); res != ExternalType {
		t.Fatalf("external FQN, got %v", res)
	}
}

// TestResolveTypeName_ImportShadowsPackage pins the JLS §6.4.1 rule the walk
// encodes: a single-type import wins over a same-package type of the same
// name — and an import of an EXTERNAL type therefore makes the name external,
// with no fall-through to the package.
func TestResolveTypeName_ImportShadowsPackage(t *testing.T) {
	files := map[string][]byte{
		"a/User.java":  []byte("package a;\nimport b.Model;\nclass User { Model m; }\n"),
		"a/Model.java": []byte("package a;\nclass Model {}\n"),
		"b/Model.java": []byte("package b;\npublic class Model {}\n"),
		"c/Ext.java":   []byte("package c;\nimport java.util.List;\nclass Ext { List l; }\n"),
		"c/List.java":  []byte("package c;\nclass List {}\n"),
	}
	ix := NewIndex(BuildTable(files))

	user := fileOf(t, ix, "a/User.java")
	if got, res := ix.ResolveTypeName(user, nil, "Model"); res != ResolvedType || got.FQN != "b.Model" {
		t.Fatalf("import must shadow the package: %v %v", got, res)
	}
	ext := fileOf(t, ix, "c/Ext.java")
	if _, res := ix.ResolveTypeName(ext, nil, "List"); res != ExternalType {
		t.Fatalf("an external single-type import must stay external (no package fall-through), got %v", res)
	}
}
