// Package jvmresolve will provide the declared-type CONFIRMED-tier resolution
// pass for Java and Kotlin (ADR 0008; language-GA program WP-J3/WP-J4): a
// class/package/member table over the walked snapshot, declared-type
// propagation with NO inference, and (name, arity)-unique member binding —
// everything unprovable is skip+counted, never guessed.
//
// It ships in reviewable slices mirroring engine/typeresolve's structure.
// Slice 1 (this file, WP-J2 / gate G2a) is the declaration→node identity
// mapping plus the golden cross-test that pins it byte-exactly against the
// real core/parse extractors — the load-bearing artifact of the whole phase: a
// confirmed edge can only ever attach to a node the extractor actually
// created, and any drift between the two naming schemes silently drops edges.
//
// Hard constraints (inherited from engine/typeresolve/doc.go via ADR 0008):
// pure Go, no network, no external toolchain (no javac/kotlinc, no classpath),
// CGo-free, deterministic. Layering: an engine package; imports core/model and
// (in tests) core/parse; never surfaces/ or cmd/.
package jvmresolve

import (
	"strings"

	"github.com/samibel/graphi/core/model"
)

// Node kinds as emitted by the core/parse JVM extractors. Duplicated
// deliberately rather than exported from core/parse — the golden cross-test
// compares full NodeIds produced by the REAL extractors against this package's
// reconstruction, so any drift (kind string, qualified name, path
// normalization) fails a test instead of silently dropping edges. The same
// choice engine/typeresolve/qn.go records.
const (
	KindFunction = "function"
	KindMethod   = "method"
	KindType     = "type"
	KindVariable = "variable"
	KindConstant = "constant"
)

// DeclForm is the DECLARED grammar form of one Java/Kotlin declaration as the
// Phase A table records it — grammar-level facts only, never inferred
// semantics (the ADR 0008 regime).
type DeclForm string

const (
	// JavaType is a class_declaration / interface_declaration /
	// enum_declaration.
	JavaType DeclForm = "java-type"
	// JavaMethod is a method_declaration.
	JavaMethod DeclForm = "java-method"
	// JavaField is ONE variable_declarator of a field_declaration (`int a, b;`
	// is two decls).
	JavaField DeclForm = "java-field"
	// JavaConstantField is one declarator of a constant_declaration — the form
	// the grammar gives interface fields, naming their constancy in the
	// declaration itself.
	JavaConstantField DeclForm = "java-constant-field"
	// KotlinType is a class_declaration (classes, interfaces, enum classes) or
	// object_declaration.
	KotlinType DeclForm = "kotlin-type"
	// KotlinFunction is a function_declaration.
	KotlinFunction DeclForm = "kotlin-function"
	// KotlinProperty is a property_declaration with a single declared name (a
	// destructuring declaration has none and never reaches the table).
	KotlinProperty DeclForm = "kotlin-property"
	// KotlinCompanion is a companion_object declaration. It is its own grammar
	// node — neither class_declaration nor object_declaration — so the
	// collector never matches it: a companion mints NO node, ever (and its
	// members carry EnclosingCompanion; see below).
	KotlinCompanion DeclForm = "kotlin-companion"
)

// Decl is one member-level or top-level declaration as the binder's Phase A
// table will see it. Locals inside function bodies are deliberately outside
// this vocabulary: the extractors mint no nodes for them and the binder
// consumes them only transiently (Phase B receiver typing), never as edge
// endpoints.
type Decl struct {
	Form DeclForm
	// File is the repo-relative source path exactly as the ingest walk hands
	// it to the parser (identity — NodeIds normalize it internally).
	File string
	// Name is the bare declared name.
	Name string
	// TypeDepth counts the ENCLOSING TYPE declarations: 0 = top level, 1 = a
	// direct member of a top-level type, 2 = a member of a nested type, …
	TypeDepth int
	// StaticFinal: a JavaField whose field_declaration declares BOTH `static`
	// and `final` (declared modifiers only — absent reads false).
	StaticFinal bool
	// Const: a KotlinProperty with the declared `const` modifier.
	Const bool
	// EnclosingEnum: the declaration is a member of an enum body — Java's
	// grammar interposes enum_body_declarations between the enum's body and
	// its members, and Kotlin's enum classes carry an enum_class_body the
	// collector (which descends class_body only) never enters. Either way the
	// members mint NO nodes — facts the golden cross-test surfaced, not
	// design choices made here.
	EnclosingEnum bool
	// EnclosingCompanion: the declaration is a member of a Kotlin companion
	// object body. The collector never matches companion_object, so it never
	// descends into one: companion members mint NO nodes (cross-test-pinned,
	// same as above). This is also why ADR 0008 lists companion members among
	// Phase B's named skip counters rather than confirmed-edge targets.
	EnclosingCompanion bool
}

// DeclNode maps a declaration to the (kind, qualifiedName) pair the core/parse
// extractor emits for the SAME declaration. ok=false means the extractor
// creates NO node — and the caller must not fabricate an endpoint (the same
// never-fabricate discipline engine/link and engine/typeresolve follow).
//
// The reconstruction mirrors the extractors byte-exactly:
//
//	qualifiedName is ALWAYS "<filePackage>.<bare>" — the flat cstWalk
//	convention (core/parse/parser_tswalk.go nodeSpecs): the package key is
//	derived from the FILE PATH (langPackage), NOT the package declaration,
//	and the type chain is dropped — a method's QN does not mention its class.
//
//	Java (parser_java.go, top-level-only collection):
//	  type      TypeDepth 0 only — javaCollectDefs walks program children;
//	            a NESTED type mints no node, and neither do its members.
//	  method    TypeDepth 1 only (a direct member of a top-level type), and
//	            NEVER inside an enum body (see Decl.EnclosingEnum).
//	  field     TypeDepth 1 only, never inside an enum body; constant when
//	            the declaration is a constant_declaration OR declares
//	            static+final, else variable. Enum constants themselves are
//	            outside this vocabulary entirely (no node, no Decl form).
//
//	Kotlin (parser_kotlin.go, RECURSIVE collection through class_body — and
//	ONLY class_body: enum_class_body and companion bodies are never entered):
//	  type      any TypeDepth (nested classes/objects mint nodes), except
//	            inside an enum or companion body.
//	  function  TypeDepth 0 → function; inside any type body → method; no
//	            node inside an enum or companion body.
//	  property  any TypeDepth; constant when `const` is declared, else
//	            variable; no node inside an enum or companion body.
//	  companion the companion_object itself mints no node (own form below).
func DeclNode(d Decl) (kind, qualifiedName string, ok bool) {
	if d.Name == "" || d.File == "" {
		return "", "", false
	}
	qn := filePackage(d.File) + "." + d.Name
	switch d.Form {
	case JavaType:
		if d.TypeDepth != 0 {
			return "", "", false
		}
		return KindType, qn, true
	case JavaMethod:
		if d.TypeDepth != 1 || d.EnclosingEnum {
			return "", "", false
		}
		return KindMethod, qn, true
	case JavaField, JavaConstantField:
		if d.TypeDepth != 1 || d.EnclosingEnum {
			return "", "", false
		}
		if d.Form == JavaConstantField || d.StaticFinal {
			return KindConstant, qn, true
		}
		return KindVariable, qn, true
	case KotlinType:
		if d.EnclosingEnum || d.EnclosingCompanion {
			return "", "", false
		}
		return KindType, qn, true
	case KotlinFunction:
		if d.EnclosingEnum || d.EnclosingCompanion {
			return "", "", false
		}
		if d.TypeDepth == 0 {
			return KindFunction, qn, true
		}
		return KindMethod, qn, true
	case KotlinProperty:
		if d.EnclosingEnum || d.EnclosingCompanion {
			return "", "", false
		}
		if d.Const {
			return KindConstant, qn, true
		}
		return KindVariable, qn, true
	case KotlinCompanion:
		return "", "", false
	}
	return "", "", false
}

// NodeIDFor maps a declaration to the model.NodeId of the node the extractor
// emitted for it, or ok=false when no such node exists. NodeId identity is
// (kind, qualifiedName, sourcePath); line and column are carried but
// non-identity (see core/model), so the reconstruction needs no positions.
//
// CAUTION for table code: the walk dedups by BARE NAME with first-binding-wins
// (core/parse cstWalk.addDef), so a same-named later declaration in the same
// file has NO node of its own — its NodeIDFor result is the FIRST binding's id
// when the kinds agree (Java overloads: same kind, same flat QN, same id) and
// a fabrication when they differ. Use FileNodeIDs wherever whole-file honesty
// matters; it applies the dedup.
func NodeIDFor(d Decl) (model.NodeId, bool) {
	kind, qn, ok := DeclNode(d)
	if !ok {
		return "", false
	}
	n, err := model.NewNode(kind, qn, d.File, 1, 1)
	if err != nil {
		return "", false
	}
	return n.ID(), true
}

// FileNodeIDs reproduces, from one file's declaration table in EXTRACTOR
// DISCOVERY ORDER (source order; Kotlin recursion is depth-first), the ids of
// exactly the symbol nodes the extractor mints for that file — applying the
// walk's first-binding-wins bare-name dedup. The file node and the interned
// package node are outside the symbol table by design (the binder never
// targets them). The golden cross-test pins this set byte-exactly against the
// real extractors.
func FileNodeIDs(decls []Decl) []model.NodeId {
	bound := map[string]struct{}{}
	var out []model.NodeId
	for _, d := range decls {
		if _, _, ok := DeclNode(d); !ok {
			continue // never reaches addDef: claims no name, mints no node
		}
		if _, seen := bound[d.Name]; seen {
			continue // first binding won; this declaration has no node
		}
		bound[d.Name] = struct{}{}
		id, ok := NodeIDFor(d)
		if !ok {
			continue
		}
		out = append(out, id)
	}
	return out
}

// filePackage mirrors core/parse's langPackage byte-exactly: the package key
// of a file is the BASE NAME of its directory, falling back to the file stem
// for a root-level file. Duplicated deliberately (same rationale as the kind
// constants above); the cross-test fails on any drift. Paths arrive in walk
// form (forward slashes, repo-relative) — the string handling below matches
// filepath.Dir/Base on those inputs.
func filePackage(file string) string {
	dir := "."
	if i := strings.LastIndexByte(file, '/'); i >= 0 {
		dir = file[:i]
	}
	base := dir
	if i := strings.LastIndexByte(dir, '/'); i >= 0 {
		base = dir[i+1:]
	}
	if base == "." || base == "/" || base == "" {
		stem := file
		if i := strings.LastIndexByte(stem, '/'); i >= 0 {
			stem = stem[i+1:]
		}
		if i := strings.LastIndexByte(stem, '.'); i > 0 {
			stem = stem[:i]
		}
		return stem
	}
	return base
}
