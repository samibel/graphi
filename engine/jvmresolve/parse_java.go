package jvmresolve

// The Java CST walker of Phase A (slice 2). Node-type and field names below
// were taken from the REAL embedded grammar (gotreesitter v0.20.2 java), not
// from upstream docs; the table tests pin them. Declared facts only.

import (
	"fmt"
	"sync"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// javaLang loads the embedded grammar once; gts.Language is immutable and
// shared across parsers (the core/parse extractors do the same).
var javaLang = sync.OnceValue(grammars.JavaLanguage)

func parseJavaFile(path string, src []byte) (f File, err error) {
	defer recoverParse(path, &err)
	lang := javaLang()
	tree, perr := gts.NewParser(lang).Parse(src)
	if perr != nil {
		return File{}, fmt.Errorf("jvmresolve: java parse %q: %w", path, perr)
	}
	w := &javaWalk{lang: lang, src: src}
	f = File{Path: path, Language: LangJava}
	root := tree.RootNode()
	for i := 0; i < root.ChildCount(); i++ {
		c := root.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(lang) {
		case "package_declaration":
			f.Package = w.dottedName(c)
		case "import_declaration":
			if imp, ok := w.importClause(c); ok {
				f.Imports = append(f.Imports, imp)
			}
		default:
			w.typeDecl(&f, c, nil, false)
		}
	}
	return f, nil
}

type javaWalk struct {
	lang *gts.Language
	src  []byte
}

func (w *javaWalk) text(n *gts.Node) string { return n.Text(w.src) }

func (w *javaWalk) childByType(n *gts.Node, typ string) *gts.Node {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c != nil && c.Type(w.lang) == typ {
			return c
		}
	}
	return nil
}

// dottedName extracts the dotted path of a package/import clause: the
// scoped_identifier (multi-segment) or bare identifier (single segment) child.
func (w *javaWalk) dottedName(n *gts.Node) string {
	if s := w.childByType(n, "scoped_identifier"); s != nil {
		return w.text(s)
	}
	if id := w.childByType(n, "identifier"); id != nil {
		return w.text(id)
	}
	return ""
}

// importClause reads one import_declaration: `import a.b.C;`,
// `import a.b.*;` (trailing asterisk child), `import static a.b.C.m;`.
func (w *javaWalk) importClause(n *gts.Node) (Import, bool) {
	path := w.dottedName(n)
	if path == "" {
		return Import{}, false
	}
	return Import{
		Path:     path,
		Wildcard: w.childByType(n, "asterisk") != nil,
		Static:   w.childByType(n, "static") != nil,
		Line:     nodeLine(n),
	}, true
}

// javaTypeForms maps grammar declaration node types onto table forms.
var javaTypeForms = map[string]string{
	"class_declaration":           FormClass,
	"interface_declaration":       FormInterface,
	"enum_declaration":            FormEnum,
	"record_declaration":          FormRecord,
	"annotation_type_declaration": FormAnnotation,
}

// typeDecl tables one type declaration (recursing into nested types).
// inEnumBody marks types declared inside an enum's body declarations.
func (w *javaWalk) typeDecl(f *File, n *gts.Node, nesting []string, inEnumBody bool) {
	form, isType := javaTypeForms[n.Type(w.lang)]
	if !isType {
		return
	}
	name := n.ChildByFieldName("name", w.lang)
	if name == nil {
		return
	}
	bare := w.text(name)
	ty := Type{
		Language: LangJava,
		File:     f.Path,
		Package:  f.Package,
		Name:     bare,
		FQN:      fqnOf(f.Package, nesting, bare),
		Form:     form,
		Nesting:  append([]string(nil), nesting...),
		Line:     nodeLine(name),
	}

	// Supertype clauses as written: `extends X` (superclass), `implements
	// A, B` (super_interfaces→type_list), and an interface's `extends A, B`
	// (extends_interfaces→type_list).
	if sc := w.childByType(n, "superclass"); sc != nil {
		if ref, ok := w.firstTypeRef(sc); ok {
			ty.Supertypes = append(ty.Supertypes, ref)
		}
	}
	for _, clause := range []string{"super_interfaces", "extends_interfaces"} {
		if si := w.childByType(n, clause); si != nil {
			if list := w.childByType(si, "type_list"); list != nil {
				for i := 0; i < list.ChildCount(); i++ {
					c := list.Child(i)
					if c == nil {
						continue
					}
					if ref, ok := w.typeRef(c); ok {
						ty.Supertypes = append(ty.Supertypes, ref)
					}
				}
			}
		}
	}

	// Record components are declared typed members (`record Pair(int a, int
	// b)`): tabled as fields, final by the language's own rule — recorded
	// here as DECLARED because the grammar form itself declares it.
	if form == FormRecord {
		if params := w.childByType(n, "formal_parameters"); params != nil {
			for _, p := range w.params(params) {
				ty.Members = append(ty.Members, Member{
					Form: MemberField, Name: p.Name, Type: p.Type, Final: true, Line: ty.Line,
				})
			}
		}
	}

	body := n.ChildByFieldName("body", w.lang)
	if body == nil {
		f.Types = append(f.Types, ty)
		return
	}
	nested := append(append([]string(nil), nesting...), bare)
	if form == FormEnum {
		// enum_body: enum_constant children plus one enum_body_declarations
		// carrying the ordinary members.
		for i := 0; i < body.ChildCount(); i++ {
			c := body.Child(i)
			if c == nil {
				continue
			}
			switch c.Type(w.lang) {
			case "enum_constant":
				if id := w.childByType(c, "identifier"); id != nil {
					ty.Members = append(ty.Members, Member{
						Form: MemberEnumConst, Name: w.text(id),
						Type: TypeRef{Raw: bare, Base: bare}, InEnumBody: true, Line: nodeLine(id),
					})
				}
			case "enum_body_declarations":
				w.memberDecls(f, c, &ty, nested, true)
			}
		}
	} else {
		w.memberDecls(f, body, &ty, nested, inEnumBody)
	}
	f.Types = append(f.Types, ty)
}

// memberDecls tables the direct member declarations of a type body, recursing
// into nested type declarations.
func (w *javaWalk) memberDecls(f *File, body *gts.Node, ty *Type, nesting []string, inEnumBody bool) {
	for i := 0; i < body.ChildCount(); i++ {
		c := body.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "method_declaration":
			name := c.ChildByFieldName("name", w.lang)
			if name == nil {
				continue
			}
			m := Member{Form: MemberMethod, Name: w.text(name), InEnumBody: inEnumBody, Line: nodeLine(name)}
			m.Static, m.Final = w.modifiers(c)
			if rt := c.ChildByFieldName("type", w.lang); rt != nil {
				if ref, ok := w.typeRef(rt); ok {
					m.Type = ref
				}
			}
			if params := c.ChildByFieldName("parameters", w.lang); params != nil {
				m.Params = w.params(params)
			}
			ty.Members = append(ty.Members, m)
		case "constructor_declaration":
			name := c.ChildByFieldName("name", w.lang)
			if name == nil {
				continue
			}
			m := Member{Form: MemberConstructor, Name: w.text(name), InEnumBody: inEnumBody, Line: nodeLine(name)}
			if params := c.ChildByFieldName("parameters", w.lang); params != nil {
				m.Params = w.params(params)
			}
			ty.Members = append(ty.Members, m)
		case "field_declaration", "constant_declaration":
			w.fieldDecl(c, ty, inEnumBody)
		default:
			w.typeDecl(f, c, nesting, inEnumBody)
		}
	}
}

// fieldDecl tables one declarator-bearing field/constant declaration.
func (w *javaWalk) fieldDecl(n *gts.Node, ty *Type, inEnumBody bool) {
	static, final := w.modifiers(n)
	var ref TypeRef
	if t := n.ChildByFieldName("type", w.lang); t != nil {
		ref, _ = w.typeRef(t)
	}
	constant := n.Type(w.lang) == "constant_declaration"
	for i := 0; i < n.ChildCount(); i++ {
		d := n.Child(i)
		if d == nil || d.Type(w.lang) != "variable_declarator" {
			continue
		}
		name := d.ChildByFieldName("name", w.lang)
		if name == nil {
			name = w.childByType(d, "identifier")
		}
		if name == nil {
			continue
		}
		ty.Members = append(ty.Members, Member{
			Form: MemberField, Name: w.text(name), Type: ref,
			Static: static, Final: final, ConstantDecl: constant,
			InEnumBody: inEnumBody, Line: nodeLine(name),
		})
	}
}

// modifiers reads the declared static/final tokens of a declaration's
// modifiers child; absent reads false (declared only, never implied).
func (w *javaWalk) modifiers(n *gts.Node) (static, final bool) {
	mods := w.childByType(n, "modifiers")
	if mods == nil {
		return false, false
	}
	for i := 0; i < mods.ChildCount(); i++ {
		m := mods.Child(i)
		if m == nil {
			continue
		}
		switch m.Type(w.lang) {
		case "static":
			static = true
		case "final":
			final = true
		}
	}
	return static, final
}

// params tables a formal_parameters list: formal_parameter and
// spread_parameter (varargs) children, each a type node plus its identifier.
func (w *javaWalk) params(n *gts.Node) []Param {
	var out []Param
	for i := 0; i < n.ChildCount(); i++ {
		p := n.Child(i)
		if p == nil {
			continue
		}
		switch p.Type(w.lang) {
		case "formal_parameter", "spread_parameter":
			var param Param
			if ref, ok := w.firstTypeRef(p); ok {
				param.Type = ref
			}
			// The declared name is the LAST identifier child (the type may
			// itself be a bare type_identifier, never an identifier).
			for j := p.ChildCount() - 1; j >= 0; j-- {
				c := p.Child(j)
				if c != nil && c.Type(w.lang) == "identifier" {
					param.Name = w.text(c)
					break
				}
			}
			out = append(out, param)
		}
	}
	return out
}

// javaTypeNodes are the grammar node types that ARE declared-type positions.
var javaTypeNodes = map[string]bool{
	"type_identifier":        true,
	"scoped_type_identifier": true,
	"generic_type":           true,
	"array_type":             true,
	"integral_type":          true,
	"floating_point_type":    true,
	"boolean_type":           true,
	"void_type":              true,
}

// firstTypeRef returns the first type-position child of n as a TypeRef.
func (w *javaWalk) firstTypeRef(n *gts.Node) (TypeRef, bool) {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		if ref, ok := w.typeRef(c); ok {
			return ref, true
		}
	}
	return TypeRef{}, false
}

// typeRef erases a type node to its binding base: generics stripped
// ("List<String>" → "List"), arrays reduced to their element ("int[]" →
// "int"), scoped names kept whole ("a.b.C"). Raw keeps the source text.
func (w *javaWalk) typeRef(n *gts.Node) (TypeRef, bool) {
	if !javaTypeNodes[n.Type(w.lang)] {
		return TypeRef{}, false
	}
	return TypeRef{Raw: w.text(n), Base: w.typeBase(n)}, true
}

func (w *javaWalk) typeBase(n *gts.Node) string {
	switch n.Type(w.lang) {
	case "generic_type":
		for i := 0; i < n.ChildCount(); i++ {
			c := n.Child(i)
			if c == nil {
				continue
			}
			if t := c.Type(w.lang); t == "type_identifier" || t == "scoped_type_identifier" {
				return w.text(c)
			}
		}
		return w.text(n)
	case "array_type":
		if el := n.ChildByFieldName("element", w.lang); el != nil {
			return w.typeBase(el)
		}
		return w.text(n)
	default:
		return w.text(n)
	}
}

// nodeLine is the 1-based source line of a node (grammar rows are 0-based,
// matching the extractors' nodePoint convention).
func nodeLine(n *gts.Node) int {
	return int(n.StartPoint().Row) + 1
}
