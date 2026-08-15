package jvmresolve

// Slice 2 (WP-J3 Phase A, first half): the declaration table. BuildTable
// re-parses the walked snapshot's .java/.kt sources with the same pure-Go
// gotreesitter grammars core/parse embeds (mirroring how engine/typeresolve
// re-parses with go/parser) and records DECLARED facts only — packages,
// imports, types with their supertype clauses as written, members with their
// declared parameter/return/field types. Nothing here resolves or infers:
// resolution is the second half of Phase A, and every recorded string is a
// verbatim (or erased-verbatim) copy of source text.
//
// Degradation contract (typeresolve's, verbatim): a file that fails to parse
// is recorded in Skipped with a reason and the pass continues — degradation
// never aborts and never deletes knowledge. Determinism: files are tabled in
// sorted-path order and every walk is source-order, so identical input yields
// an identical Table.

import (
	"fmt"
	"sort"
	"strings"
)

// Language ids, matching core/parse's canonical vocabulary.
const (
	LangJava   = "java"
	LangKotlin = "kotlin"
)

// Import is one declared import clause.
type Import struct {
	// Path is the dotted name as written: a type FQN ("java.util.List"), a
	// package for an on-demand import ("java.util"), or a MEMBER FQN for a
	// Java static import ("java.lang.Math.max").
	Path string
	// Wildcard: `import p.*` (Java) / `import p.*` (Kotlin).
	Wildcard bool
	// Static: Java `import static`.
	Static bool
	// Alias: Kotlin `import a.b.C as D` ("" = none).
	Alias string
	Line  int
}

// TypeRef is a declared type name as written. Base is the binding name with
// generics/nullability/arrays erased ("List" from "List<String>", "a.b.C"
// from "a.b.C<D>", "String" from "String?"); Raw keeps the source text.
type TypeRef struct {
	Raw  string
	Base string
}

// IsZero reports an absent type (void return, inferred Kotlin property).
func (r TypeRef) IsZero() bool { return r.Raw == "" && r.Base == "" }

// Param is one declared parameter.
type Param struct {
	Name string
	Type TypeRef
}

// Member forms — the declared grammar shape, never a judgement.
const (
	MemberMethod      = "method"
	MemberConstructor = "constructor"
	MemberField       = "field"    // Java field_declaration / constant_declaration declarator; record component
	MemberProperty    = "property" // Kotlin property (incl. primary-constructor val/var parameters)
	MemberFunction    = "function" // Kotlin function_declaration
	MemberEnumConst   = "enum-const"
)

// Member is one declared member of a type, or a Kotlin top-level declaration.
type Member struct {
	Form string
	Name string
	// Params: methods/constructors/functions.
	Params []Param
	// Type is the declared return/field/property type; zero when absent
	// (void, constructor, inferred Kotlin val/var — inference is a NAMED GAP,
	// never guessed; ADR 0008 Phase B).
	Type TypeRef
	// Declared modifiers only; absent reads false.
	Static bool // Java `static`
	Final  bool // Java `final`
	Const  bool // Kotlin `const`
	// ConstantDecl: the Java declarator came from a constant_declaration (an
	// interface field — the grammar names its constancy).
	ConstantDecl bool
	// InEnumBody / InCompanion: the member lives where the core/parse
	// collectors never descend (qn.go pins that no node exists for it). The
	// table still records it — the binder may TYPE through such members; it
	// may never EMIT an edge endpoint at them (the committed-node check
	// enforces that structurally).
	InEnumBody  bool
	InCompanion bool
	Line        int
}

// Type forms.
const (
	FormClass      = "class"
	FormInterface  = "interface"
	FormEnum       = "enum"
	FormRecord     = "record"
	FormAnnotation = "annotation"
	FormObject     = "object"    // Kotlin object declaration
	FormCompanion  = "companion" // Kotlin companion object
)

// Type is one declared Java/Kotlin type.
type Type struct {
	Language string
	File     string
	// Package is the DECLARED package ("" = default package). Note the
	// deliberate difference from qn.go's filePackage: node identity keys on
	// the file path, resolution keys on the declaration — both facts are
	// needed and they are not the same fact.
	Package string
	Name    string
	// FQN is Package + Nesting + Name, dotted (no leading dot when the
	// package is empty).
	FQN  string
	Form string
	// Nesting is the enclosing type chain, outer→inner; empty = top level.
	Nesting []string
	// Supertypes are the extends/implements/delegation clauses AS WRITTEN
	// (erased to TypeRef); resolution decides later what they bind to.
	Supertypes []TypeRef
	Members    []Member
	Line       int
}

// File is one source file's declarations.
type File struct {
	Path     string
	Language string
	Package  string
	Imports  []Import
	// Types lists ALL types of the file — nested included — flat, in source
	// order; Nesting distinguishes depth.
	Types []Type
	// TopLevel holds Kotlin top-level functions/properties (Java has none).
	TopLevel []Member
}

// Skip is one file Phase A could not table.
type Skip struct {
	Path   string
	Reason string
}

// Table is the Phase A output over one repository snapshot.
type Table struct {
	Files   []File
	Skipped []Skip
}

// BuildTable tables every .java/.kt file in files (the ingest walk's
// path→bytes map shape). Non-JVM paths are ignored, so callers may hand the
// whole snapshot map over.
func BuildTable(files map[string][]byte) Table {
	paths := make([]string, 0, len(files))
	for p := range files {
		if strings.HasSuffix(p, ".java") || strings.HasSuffix(p, ".kt") {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)

	var t Table
	for _, p := range paths {
		var (
			f   File
			err error
		)
		if strings.HasSuffix(p, ".java") {
			f, err = parseJavaFile(p, files[p])
		} else {
			f, err = parseKotlinFile(p, files[p])
		}
		if err != nil {
			t.Skipped = append(t.Skipped, Skip{Path: p, Reason: err.Error()})
			continue
		}
		t.Files = append(t.Files, f)
	}
	return t
}

// TypesByFQN indexes every tabled type by its FQN. Two types sharing an FQN
// (duplicate declarations across files) BOTH stay recorded — the strict
// ambiguity rule makes such a name unresolvable (drop+count), never
// first-wins.
func (t Table) TypesByFQN() map[string][]*Type {
	idx := map[string][]*Type{}
	for fi := range t.Files {
		for ti := range t.Files[fi].Types {
			ty := &t.Files[fi].Types[ti]
			idx[ty.FQN] = append(idx[ty.FQN], ty)
		}
	}
	return idx
}

// fqnOf joins package, nesting chain and name into the declared FQN.
func fqnOf(pkg string, nesting []string, name string) string {
	parts := make([]string, 0, len(nesting)+2)
	if pkg != "" {
		parts = append(parts, pkg)
	}
	parts = append(parts, nesting...)
	parts = append(parts, name)
	return strings.Join(parts, ".")
}

// recoverParse converts a grammar panic into a Skip reason — the same
// fail-closed posture core/parse's Parse wrappers take.
func recoverParse(path string, err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("jvmresolve: recovered from panic parsing %q: %v", path, r)
	}
}
