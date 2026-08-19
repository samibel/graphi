package jvmgroundtruth

// SW-172 stage 2 — the CI-ONLY binder-level export.
//
// # Why this does NOT read the graph store
//
// A signature-aware comparison cannot be built on the graph store, at all.
// Node identity collapses overloads: engine/jvmresolve/qn.go — see the CAUTION
// on NodeIDFor, "Java overloads: same kind, same flat QN, same id" — so two
// same-named methods declared in one file are ONE node, and core/parse's walk
// dedups by bare name with first-binding-wins so the second declaration mints
// no node of its own. The store therefore physically cannot say WHICH overload
// an edge points at, and no amount of care in reading it recovers the fact.
//
// Changing node identity to distinguish overloads is a graph-bytes change with
// an index migration behind it, and qn.go's collapse is a documented decision.
// This export routes AROUND it rather than reopening it: it reads the binder's
// own TypedSite plus the LookupCallable projection (the bound *Member and its
// declaring *Type), which is the last place in the pipeline where the callee's
// signature still exists.
//
// # Drift
//
// Nothing here re-implements a binding rule. BinderCalls calls the SHIPPED
// BuildTable / NewIndex / AnalyzeJavaBodies, so the binding decisions it
// reports are the product's decisions by construction, not a copy of them; the
// only thing this file computes on its own is the rendering of a declared
// parameter type into its erased JVM descriptor, and that rendering is checked
// against javac's own declared-method table before any verdict rests on it
// (DeclaredMethods.Verify).
//
// # This set is a SUPERSET of the emitted confirmed edges
//
// A typed site becomes an edge only if both endpoints reconstruct to node ids
// that are in the committed set (engine/jvmresolve/emit.go). Sites that fail
// that are counted and dropped there. This export reports the BINDING
// DECISION, which is the thing the oracle measures, so it includes them — a
// site bound to the wrong overload is a defect in the binder whether or not
// the emitter happened to drop it. Statements about emitted edges must use the
// graph-store projection (ConfirmedCalls) instead.

import (
	"strings"

	"github.com/samibel/graphi/engine/jvmresolve"
)

// Additional abstention reasons owned by this file.
const (
	// AbstainBinderNestedCtor: a constructor call whose type is NESTED. javac
	// gives an inner class's constructor a synthetic leading parameter for the
	// enclosing instance (`shop.Cart$Helper(shop.Cart)`), and an enum
	// constructor two more, so the DECLARED parameter count is not the
	// compiled arity. `Type` records no static-ness for a nested type, so
	// every nested constructor declines rather than half of them guessing.
	AbstainBinderNestedCtor = "binder_nested_ctor_synthetic_params"
	// AbstainBinderCallerNotCallable: the site's enclosing declaration is not
	// a method/function/constructor (a field initializer, say), which javac
	// compiles into `<init>`/`<clinit>` under a name this side cannot predict.
	AbstainBinderCallerNotCallable = "binder_caller_not_a_callable"
	// AbstainBinderSignatureUnverified: the rendered descriptor is not among
	// the descriptors javac compiled for that source file, so the rendering is
	// not trustworthy here and no verdict may rest on it. This is the guard
	// that keeps a rendering bug from becoming a fabricated counterexample.
	AbstainBinderSignatureUnverified = "binder_signature_unverified"
)

// javaPrimitives maps a Java primitive keyword to its JVM field descriptor
// (JVMS 4.3.2).
var javaPrimitives = map[string]string{
	"byte": "B", "char": "C", "double": "D", "float": "F",
	"int": "I", "long": "J", "short": "S", "boolean": "Z",
}

// BinderCalls runs the shipped JVM binder over a source snapshot and projects
// every proved CALL site into the Call fact space, carrying the arity and the
// erased parameter signature of the member the binder actually bound.
//
// files is the same path→bytes map engine/ingest hands the resolver. Pure and
// deterministic: identical input yields an identical, sorted slice.
func BinderCalls(files map[string][]byte) []Call {
	tab := jvmresolve.BuildTable(files)
	ix := jvmresolve.NewIndex(tab)

	byPath := make(map[string]*jvmresolve.File, len(tab.Files))
	for i := range tab.Files {
		byPath[tab.Files[i].Path] = &tab.Files[i]
	}

	javaSites, _ := ix.AnalyzeJavaBodies(files)
	kotlinSites, _ := ix.AnalyzeKotlinBodies(files)

	seen := map[callKey]struct{}{}
	var out []Call
	for _, sites := range [][]jvmresolve.TypedSite{javaSites, kotlinSites} {
		for i := range sites {
			c, ok := projectSite(ix, byPath, &sites[i])
			if !ok {
				continue
			}
			k := c.key(BySignature)
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, c)
		}
	}
	sortCalls(out)
	return out
}

// projectSite turns one typed CALL site into a Call fact, deciding arity and
// signature where it can and naming its refusal where it cannot.
func projectSite(ix *jvmresolve.Index, byPath map[string]*jvmresolve.File, s *jvmresolve.TypedSite) (Call, bool) {
	if s.Kind != jvmresolve.SiteCall || s.Member == nil {
		return Call{}, false
	}
	if s.FromMember == nil {
		return Call{}, false
	}

	ctor := s.Member.Form == jvmresolve.MemberConstructor
	calleeFile, callee := "", ""
	switch {
	case ctor:
		if s.Declaring == nil {
			return Call{}, false
		}
		// A constructor call targets the TYPE, and the bytecode side
		// normalizes `<init>` to the constructed type's simple name, so the
		// two sides meet on that name.
		calleeFile, callee = s.Declaring.File, s.Declaring.Name
	case s.Declaring == nil:
		// Kotlin same-file top-level function.
		calleeFile, callee = s.FromFile, s.Member.Name
	default:
		calleeFile, callee = s.Declaring.File, s.Member.Name
	}

	c := Call{
		CallerFile:   s.FromFile,
		CallerMethod: callerMethodName(s.FromMember),
		CalleeFile:   calleeFile,
		Callee:       callee,
		CalleeCtor:   ctor,
		CalleeArity:  ArityUnknown,
		CalleeParams: SigUnknown,
	}

	if reason, ok := binderUndecidable(byPath, s, ctor); ok {
		c.ArityReason, c.ParamsReason = reason, reason
		return c, true
	}

	c.CalleeArity = len(s.Member.Params)
	sig, ok := renderParams(ix, byPath, s.Declaring, s.Member)
	if !ok {
		c.ParamsReason = AbstainBinderParamUnresolved
		return c, true
	}
	c.CalleeParams = sig
	return c, true
}

// callerMethodName is the name javac gives the enclosing declaration: a
// constructor body compiles into `<init>`, which is what parseMethodHeader
// normalizes the bytecode side to.
func callerMethodName(m *jvmresolve.Member) string {
	if m.Form == jvmresolve.MemberConstructor {
		return "<init>"
	}
	return m.Name
}

// binderUndecidable reports the named reason this site's signature cannot be
// compared to bytecode at all, before any rendering is attempted.
func binderUndecidable(byPath map[string]*jvmresolve.File, s *jvmresolve.TypedSite, ctor bool) (string, bool) {
	switch s.FromMember.Form {
	case jvmresolve.MemberMethod, jvmresolve.MemberFunction, jvmresolve.MemberConstructor:
	default:
		return AbstainBinderCallerNotCallable, true
	}
	if isKotlin(s, byPath) {
		// The declared-Kotlin → JVM-descriptor mapping is unproven here; see
		// AbstainKotlinShapeUnproven.
		return AbstainKotlinShapeUnproven, true
	}
	for i := range s.Member.Params {
		p := &s.Member.Params[i]
		if p.Variadic || p.HasDefault {
			return AbstainBinderElastic, true
		}
	}
	if ctor && s.Declaring != nil && (len(s.Declaring.Nesting) > 0 || s.Declaring.Form == jvmresolve.FormEnum) {
		return AbstainBinderNestedCtor, true
	}
	return "", false
}

// isKotlin reports whether either end of the site is Kotlin.
func isKotlin(s *jvmresolve.TypedSite, byPath map[string]*jvmresolve.File) bool {
	if strings.HasSuffix(s.FromFile, ".kt") {
		return true
	}
	if s.Declaring != nil && s.Declaring.Language == jvmresolve.LangKotlin {
		return true
	}
	if f, ok := byPath[s.FromFile]; ok && f.Language == jvmresolve.LangKotlin {
		return true
	}
	return false
}

// renderParams renders a bound member's declared parameter list into the
// shared alphabet: "(" + erased JVM field descriptors + ")". It declines
// unless EVERY parameter is a primitive, an intra-repo type, or an array of
// either — the only forms a declaration erases without guessing. `String` is
// deliberately NOT special-cased: nothing in the declaration proves it means
// `java.lang.String` rather than an imported homonym.
func renderParams(ix *jvmresolve.Index, byPath map[string]*jvmresolve.File, declaring *jvmresolve.Type, m *jvmresolve.Member) (string, bool) {
	if declaring == nil {
		return SigUnknown, false
	}
	file := byPath[declaring.File]
	if file == nil {
		return SigUnknown, false
	}
	var b strings.Builder
	b.WriteByte('(')
	for i := range m.Params {
		d, ok := renderParam(ix, file, declaring, &m.Params[i])
		if !ok {
			return SigUnknown, false
		}
		b.WriteString(d)
	}
	b.WriteByte(')')
	return b.String(), true
}

func renderParam(ix *jvmresolve.Index, file *jvmresolve.File, declaring *jvmresolve.Type, p *jvmresolve.Param) (string, bool) {
	dims, ok := arrayDims(p.Type.Raw)
	if !ok {
		return "", false
	}
	prefix := strings.Repeat("[", dims)
	if prim, isPrim := javaPrimitives[p.Type.Base]; isPrim {
		return prefix + prim, true
	}
	rt, res := ix.ResolveTypeName(file, declaring, p.Type.Base)
	if res != jvmresolve.ResolvedType || rt == nil {
		// External, ambiguous, or a generic type variable (which resolves
		// nowhere and erases to its bound — a fact the declaration does not
		// carry). Decline.
		return "", false
	}
	return prefix + "L" + internalName(rt) + ";", true
}

// arrayDims counts the trailing `[]` groups of a written type. It declines on
// a generic type (`List<String>`), whose erasure the declaration does not
// state.
//
// KNOWN GAP, closed downstream rather than here: the C-style declarator form
// `int xs[]` puts the brackets on the DECLARATOR, not the type, so TypeRef.Raw
// reads "int" and this returns 0 dims — understating the real descriptor. It
// is not detectable from the table, which is exactly why no verdict may rest
// on a rendering until DeclaredMethods.Verify has found it in javac's output.
func arrayDims(raw string) (int, bool) {
	s := strings.TrimSpace(raw)
	if strings.ContainsAny(s, "<>") {
		return 0, false
	}
	n := 0
	for strings.HasSuffix(s, "[]") {
		n++
		s = strings.TrimSpace(strings.TrimSuffix(s, "[]"))
	}
	if s == "" {
		return 0, false
	}
	return n, true
}

// internalName renders a tabled type's JVM internal name: package with
// slashes, nesting chain joined with '$'.
func internalName(t *jvmresolve.Type) string {
	var b strings.Builder
	if t.Package != "" {
		b.WriteString(strings.ReplaceAll(t.Package, ".", "/"))
		b.WriteByte('/')
	}
	for _, n := range t.Nesting {
		b.WriteString(n)
		b.WriteByte('$')
	}
	b.WriteString(t.Name)
	return b.String()
}

// DeclaredMethods indexes, per repo-relative SOURCE PATH, the (name, parameter
// list) pairs javac actually compiled — read from javap -s's `descriptor:`
// lines. It is the independent check on this file's Java→descriptor rendering.
type DeclaredMethods map[string]map[string]struct{}

// ParseDeclaredMethods builds the index from `javap -c -p -s` output. Output
// captured without -s yields an empty index, which Verify treats as "cannot
// confirm anything" — declining, never waving through.
func ParseDeclaredMethods(out []byte) (DeclaredMethods, error) {
	classes, err := parseClasses(out)
	if err != nil {
		return nil, err
	}
	d := DeclaredMethods{}
	for _, ci := range classes {
		for decl := range ci.decls {
			colon := strings.LastIndexByte(decl, ':')
			if colon < 0 {
				continue
			}
			name, desc := decl[:colon], decl[colon+1:]
			params, ok := descriptorParams(desc)
			if !ok {
				continue
			}
			if d[ci.source] == nil {
				d[ci.source] = map[string]struct{}{}
			}
			d[ci.source][name+"("+strings.Join(params, "")+")"] = struct{}{}
		}
	}
	return d, nil
}

// Verify demotes every call whose binder-rendered signature javac did NOT
// compile for that source file to SigUnknown, under
// AbstainBinderSignatureUnverified. Arity is left alone: it is read off
// `Member.Params` directly and does not depend on the rendering.
//
// This is the guard that makes stage 2 safe to act on. Every way the rendering
// can be wrong — the C-style array declarator, a nesting or package error, an
// erasure this file did not model — produces a descriptor javac never emitted,
// so the fact abstains instead of becoming a fabricated JVMSOUND-0xx.
//
// Returns a new slice; the input is not modified.
func (d DeclaredMethods) Verify(calls []Call) []Call {
	out := make([]Call, 0, len(calls))
	for _, c := range calls {
		if c.CalleeParams != SigUnknown {
			name := c.Callee
			if c.CalleeCtor {
				name = "<init>"
			}
			if _, ok := d[c.CalleeFile][name+c.CalleeParams]; !ok {
				c.CalleeParams = SigUnknown
				c.ParamsReason = AbstainBinderSignatureUnverified
			}
		}
		out = append(out, c)
	}
	return out
}
