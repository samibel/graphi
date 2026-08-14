package jvmresolve

// The Kotlin CST walker of Phase A (slice 2). Node-type names below were
// taken from the REAL embedded grammar (gotreesitter v0.20.2 kotlin); the
// table tests pin them. Declared facts only — an inferred `val x = …` is
// tabled with a ZERO TypeRef, never a guessed one (ADR 0008: inference gaps
// are named skips, and Phase B counts them).

import (
	"fmt"
	"strings"
	"sync"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

var kotlinLang = sync.OnceValue(grammars.KotlinLanguage)

func parseKotlinFile(path string, src []byte) (f File, err error) {
	defer recoverParse(path, &err)
	lang := kotlinLang()
	tree, perr := gts.NewParser(lang).Parse(src)
	if perr != nil {
		return File{}, fmt.Errorf("jvmresolve: kotlin parse %q: %w", path, perr)
	}
	w := &kotlinWalk{lang: lang, src: src}
	f = File{Path: path, Language: LangKotlin}
	root := tree.RootNode()
	for i := 0; i < root.ChildCount(); i++ {
		c := root.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(lang) {
		case "package_header":
			if id := w.childByType(c, "identifier"); id != nil {
				f.Package = w.text(id)
			}
		case "import_list":
			for j := 0; j < c.ChildCount(); j++ {
				if h := c.Child(j); h != nil && h.Type(lang) == "import_header" {
					if imp, ok := w.importClause(h); ok {
						f.Imports = append(f.Imports, imp)
					}
				}
			}
		default:
			w.decl(&f, c, nil, nil, false, false)
		}
	}
	return f, nil
}

type kotlinWalk struct {
	lang *gts.Language
	src  []byte
}

func (w *kotlinWalk) text(n *gts.Node) string { return n.Text(w.src) }

func (w *kotlinWalk) childByType(n *gts.Node, typ string) *gts.Node {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c != nil && c.Type(w.lang) == typ {
			return c
		}
	}
	return nil
}

// importClause reads one import_header: dotted identifier, optional
// `as Alias` (import_alias→type_identifier), optional wildcard_import.
func (w *kotlinWalk) importClause(n *gts.Node) (Import, bool) {
	id := w.childByType(n, "identifier")
	if id == nil {
		return Import{}, false
	}
	imp := Import{Path: w.text(id), Line: nodeLine(n)}
	if w.childByType(n, "wildcard_import") != nil {
		imp.Wildcard = true
	}
	if alias := w.childByType(n, "import_alias"); alias != nil {
		if t := w.childByType(alias, "type_identifier"); t != nil {
			imp.Alias = w.text(t)
		}
	}
	return imp, true
}

// decl tables one declaration node. enclosing is the type currently being
// built (nil at top level); nesting is the enclosing NAME chain. The
// inEnumBody/inCompanion context flags mirror qn.go's Decl fields.
func (w *kotlinWalk) decl(f *File, n *gts.Node, enclosing *Type, nesting []string, inEnumBody, inCompanion bool) {
	switch n.Type(w.lang) {
	case "class_declaration":
		w.classDecl(f, n, nesting, inEnumBody, inCompanion)
	case "object_declaration":
		w.objectDecl(f, n, FormObject, nesting, inEnumBody, inCompanion)
	case "companion_object":
		w.companionDecl(f, n, nesting, inEnumBody)
	case "function_declaration":
		if m, ok := w.function(n, inEnumBody, inCompanion); ok {
			w.addMember(f, enclosing, m)
		}
	case "property_declaration":
		if m, ok := w.property(n, inEnumBody, inCompanion); ok {
			w.addMember(f, enclosing, m)
		}
	}
}

// addMember appends to the enclosing type, or to the file's top level.
func (w *kotlinWalk) addMember(f *File, enclosing *Type, m Member) {
	if enclosing != nil {
		enclosing.Members = append(enclosing.Members, m)
		return
	}
	f.TopLevel = append(f.TopLevel, m)
}

// classDecl tables a class/interface/enum-class declaration: the grammar uses
// class_declaration for all three — an `interface` keyword child marks
// interfaces, an enum_class_body marks enum classes.
func (w *kotlinWalk) classDecl(f *File, n *gts.Node, nesting []string, inEnumBody, inCompanion bool) {
	name := w.childByType(n, "type_identifier")
	if name == nil {
		return
	}
	form := FormClass
	if w.childByType(n, "interface") != nil {
		form = FormInterface
	}
	if w.childByType(n, "enum_class_body") != nil {
		form = FormEnum
	}
	w.buildType(f, n, name, form, nesting, inEnumBody, inCompanion)
}

// objectDecl tables an object declaration (named singleton).
func (w *kotlinWalk) objectDecl(f *File, n *gts.Node, form string, nesting []string, inEnumBody, inCompanion bool) {
	name := w.childByType(n, "type_identifier")
	if name == nil {
		return
	}
	w.buildType(f, n, name, form, nesting, inEnumBody, inCompanion)
}

// companionDecl tables a companion object. An anonymous companion carries the
// language-defined name "Companion" — that IS the declared name Kotlin gives
// it, not an inference. Its members carry InCompanion (qn.go: no nodes).
func (w *kotlinWalk) companionDecl(f *File, n *gts.Node, nesting []string, inEnumBody bool) {
	bare := "Companion"
	if name := w.childByType(n, "type_identifier"); name != nil {
		bare = w.text(name)
	}
	ty := Type{
		Language: LangKotlin, File: f.Path, Package: f.Package,
		Name: bare, FQN: fqnOf(f.Package, nesting, bare), Form: FormCompanion,
		Nesting: append([]string(nil), nesting...), Line: nodeLine(n),
	}
	nested := append(append([]string(nil), nesting...), bare)
	if body := w.childByType(n, "class_body"); body != nil {
		w.body(f, body, &ty, nested, inEnumBody, true)
	}
	f.Types = append(f.Types, ty)
}

// buildType assembles a Type from a class/interface/enum/object declaration
// node: supertype delegation clauses, primary-constructor parameters (val/var
// class parameters are DECLARED properties), and the body members.
func (w *kotlinWalk) buildType(f *File, n *gts.Node, name *gts.Node, form string, nesting []string, inEnumBody, inCompanion bool) {
	bare := w.text(name)
	ty := Type{
		Language: LangKotlin, File: f.Path, Package: f.Package,
		Name: bare, FQN: fqnOf(f.Package, nesting, bare), Form: form,
		Nesting: append([]string(nil), nesting...), Line: nodeLine(name),
	}

	// Supertypes: delegation_specifier children — a constructor_invocation
	// (class supertype `Base()`) or a bare user_type (interface `Priced`).
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil || c.Type(w.lang) != "delegation_specifier" {
			continue
		}
		ut := w.childByType(c, "user_type")
		if ut == nil {
			if ci := w.childByType(c, "constructor_invocation"); ci != nil {
				ut = w.childByType(ci, "user_type")
			}
		}
		if ut != nil {
			ty.Supertypes = append(ty.Supertypes, w.userTypeRef(ut))
		}
	}

	// Primary constructor: class_parameter children; a val/var-bound one
	// declares a property. The constructor itself is a member too.
	if pc := w.childByType(n, "primary_constructor"); pc != nil {
		ctor := Member{Form: MemberConstructor, Name: bare, InEnumBody: inEnumBody, InCompanion: inCompanion, Line: nodeLine(pc)}
		for i := 0; i < pc.ChildCount(); i++ {
			p := pc.Child(i)
			if p == nil || p.Type(w.lang) != "class_parameter" {
				continue
			}
			pname := w.childByType(p, "simple_identifier")
			if pname == nil {
				continue
			}
			param := Param{Name: w.text(pname)}
			if ref, ok := w.declaredType(p); ok {
				param.Type = ref
			}
			ctor.Params = append(ctor.Params, param)
			if w.childByType(p, "binding_pattern_kind") != nil {
				ty.Members = append(ty.Members, Member{
					Form: MemberProperty, Name: param.Name, Type: param.Type,
					InEnumBody: inEnumBody, InCompanion: inCompanion, Line: nodeLine(pname),
				})
			}
		}
		ty.Members = append(ty.Members, ctor)
	}

	nested := append(append([]string(nil), nesting...), bare)
	if body := w.childByType(n, "class_body"); body != nil {
		w.body(f, body, &ty, nested, inEnumBody, inCompanion)
	}
	if body := w.childByType(n, "enum_class_body"); body != nil {
		// enum entries plus ordinary members — ALL flagged InEnumBody: the
		// collector never enters an enum_class_body (qn.go pins no nodes).
		for i := 0; i < body.ChildCount(); i++ {
			c := body.Child(i)
			if c == nil {
				continue
			}
			if c.Type(w.lang) == "enum_entry" {
				if id := w.childByType(c, "simple_identifier"); id != nil {
					ty.Members = append(ty.Members, Member{
						Form: MemberEnumConst, Name: w.text(id),
						Type: TypeRef{Raw: bare, Base: bare}, InEnumBody: true, Line: nodeLine(id),
					})
				}
				continue
			}
			w.decl(f, c, &ty, nested, true, inCompanion)
		}
	}
	f.Types = append(f.Types, ty)
}

// body walks a class_body's direct children.
func (w *kotlinWalk) body(f *File, body *gts.Node, ty *Type, nesting []string, inEnumBody, inCompanion bool) {
	for i := 0; i < body.ChildCount(); i++ {
		c := body.Child(i)
		if c == nil {
			continue
		}
		w.decl(f, c, ty, nesting, inEnumBody, inCompanion)
	}
}

// function tables a function_declaration: name, parameters, declared return
// type (a DIRECT user_type/nullable_type child — parameter types sit nested
// inside function_value_parameters and are not direct children).
func (w *kotlinWalk) function(n *gts.Node, inEnumBody, inCompanion bool) (Member, bool) {
	name := w.childByType(n, "simple_identifier")
	if name == nil {
		return Member{}, false
	}
	m := Member{Form: MemberFunction, Name: w.text(name), InEnumBody: inEnumBody, InCompanion: inCompanion, Line: nodeLine(name)}
	if ref, ok := w.declaredType(n); ok {
		m.Type = ref
	}
	if params := w.childByType(n, "function_value_parameters"); params != nil {
		for i := 0; i < params.ChildCount(); i++ {
			p := params.Child(i)
			if p == nil || p.Type(w.lang) != "parameter" {
				continue
			}
			pname := w.childByType(p, "simple_identifier")
			if pname == nil {
				continue
			}
			param := Param{Name: w.text(pname)}
			if ref, ok := w.declaredType(p); ok {
				param.Type = ref
			}
			m.Params = append(m.Params, param)
		}
	}
	return m, true
}

// property tables a property_declaration: `val`/`var` +
// variable_declaration{name, optional declared type}; the `const` property
// modifier marks constants. A destructuring declaration has no single name
// and is skipped (same fail-closed rule as the extractor).
func (w *kotlinWalk) property(n *gts.Node, inEnumBody, inCompanion bool) (Member, bool) {
	vd := w.childByType(n, "variable_declaration")
	if vd == nil {
		return Member{}, false
	}
	name := w.childByType(vd, "simple_identifier")
	if name == nil {
		return Member{}, false
	}
	m := Member{Form: MemberProperty, Name: w.text(name), InEnumBody: inEnumBody, InCompanion: inCompanion, Line: nodeLine(name)}
	if ref, ok := w.declaredType(vd); ok {
		m.Type = ref
	}
	if mods := w.childByType(n, "modifiers"); mods != nil {
		for i := 0; i < mods.ChildCount(); i++ {
			c := mods.Child(i)
			if c != nil && c.Type(w.lang) == "property_modifier" && w.text(c) == "const" {
				m.Const = true
			}
		}
	}
	return m, true
}

// declaredType returns the declared type of a node carrying a DIRECT
// user_type or nullable_type child, ok=false when none is written (inferred —
// a named gap, never a guess).
func (w *kotlinWalk) declaredType(n *gts.Node) (TypeRef, bool) {
	if ut := w.childByType(n, "user_type"); ut != nil {
		return w.userTypeRef(ut), true
	}
	if nt := w.childByType(n, "nullable_type"); nt != nil {
		if ut := w.childByType(nt, "user_type"); ut != nil {
			ref := w.userTypeRef(ut)
			ref.Raw = w.text(nt) // keep the written `?`
			return ref, true
		}
	}
	return TypeRef{}, false
}

// userTypeRef erases a user_type to its binding base: the dotted chain of its
// type_identifier children with type_arguments stripped ("a.b.C<D>" → base
// "a.b.C"); Raw keeps the source text.
func (w *kotlinWalk) userTypeRef(n *gts.Node) TypeRef {
	var parts []string
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		if c.Type(w.lang) == "type_identifier" {
			parts = append(parts, w.text(c))
		}
	}
	return TypeRef{Raw: w.text(n), Base: strings.Join(parts, ".")}
}
