package jvmresolve

// Slice 4 (WP-J3 Phase B, Java): declared-type propagation through method and
// constructor bodies — NO inference, ever. The walker types receivers from
// exactly the declared forms ADR 0008 enumerates:
//
//	parameters · explicitly typed locals · fields/properties via the member
//	lookup · `this` · `super` (the intra-repo class supertype) · constructor
//	results (`new Foo(…)`) · qualified statics (`Foo.bar(…)`) · cast
//	assertions (`(Foo) x` — a declared form: the programmer wrote the type) ·
//	declared-return-type chains (`a.b().c()`).
//
// Everything else is skip+counted under a NAMED counter — `var` locals, calls
// on untypable expressions, receivers whose type resolves outside the repo,
// open chains, ambiguous overloads. A skip is a recall gap; a guess would be
// a soundness hole, and there are none by construction.
//
// Scoping is honest: the environment is a stack of block scopes, because Java
// permits same-named locals in DISJOINT blocks — a flat map would leak the
// first block's declared type into the second and could type a receiver
// WRONG. Locals bind at their declaration statement, in source order.
//
// The walker collects sites from EVERY tabled body — nested types and enum
// bodies included. Whether a site's endpoints have graph nodes is not its
// question: Phase C maps endpoints through qn.go's identity rules and the
// committed-node set, where node-less members drop structurally.

import (
	gts "github.com/odvcencio/gotreesitter"
)

// SiteKind distinguishes call sites from value (field/property) accesses.
type SiteKind string

const (
	SiteCall  SiteKind = "call"
	SiteValue SiteKind = "value"
)

// TypedSite is one use site whose receiver Phase B PROVED from declared
// types, bound through the member lookup (hierarchy.go).
type TypedSite struct {
	FromFile string
	// FromType/FromMember locate the enclosing declaration the site occurs
	// in. FromType is never nil for Java.
	FromType   *Type
	FromMember *Member
	// Receiver is the resolved receiver type; Declaring/Member the D6-bound
	// target member and its declaring type.
	Receiver  *Type
	Declaring *Type
	Member    *Member
	// StaticReceiver: the receiver was written as a TYPE name (`Rate.max`).
	// ImplicitReceiver: a bare call (`helper()`) bound through the enclosing
	// type's chain — Java's implicit this.
	StaticReceiver   bool
	ImplicitReceiver bool
	Name             string
	// Arity of the call; -1 for value sites.
	Arity int
	Line  int
	Kind  SiteKind
}

// Skip-counter names — the closed vocabulary of Phase B's named gaps. These
// are observability data (trust evidence), so they are constants, not ad-hoc
// strings.
const (
	SkipVarInferred      = "java_var_inferred"       // `var x = …` local
	SkipReceiverUntyped  = "java_receiver_untyped"   // expression form the walker cannot type
	SkipReceiverExternal = "java_receiver_external"  // receiver type resolves outside the repo
	SkipReceiverAmbig    = "java_receiver_ambiguous" // receiver type name ambiguous
	SkipLookupAmbiguous  = "java_lookup_ambiguous"   // D6 overload-set drop
	SkipLookupOpenChain  = "java_lookup_open_chain"  // external/ambiguous supertype forfeit
	SkipLookupNotFound   = "java_lookup_not_found"   // closed chain provably lacks the member
	SkipSuperExternal    = "java_super_external"     // `super` with no intra-repo class supertype
	SkipBodyUnmatched    = "java_body_unmatched"     // CST body with no tabled type/member pair
)

// SkipCounts tallies named gaps; Phase C folds them into trust evidence.
type SkipCounts map[string]int

func (s SkipCounts) add(name string) { s[name]++ }

// AnalyzeJavaBodies walks every tabled Java file's bodies over src (the same
// path→bytes snapshot BuildTable consumed) and returns the proved sites in
// deterministic (file, source) order plus the named skip counters.
func (ix *Index) AnalyzeJavaBodies(src map[string][]byte) ([]TypedSite, SkipCounts) {
	skips := SkipCounts{}
	var sites []TypedSite
	for fi := range ix.table.Files {
		file := &ix.table.Files[fi]
		if file.Language != LangJava {
			continue
		}
		bytes, ok := src[file.Path]
		if !ok {
			continue
		}
		sites = append(sites, ix.analyzeJavaFile(file, bytes, skips)...)
	}
	return sites, skips
}

// SkipBodyPanic counts a body walk that panicked (grammar edge case): the
// file's sites drop, the pass continues — degradation never aborts.
const SkipBodyPanic = "java_body_panic"

func (ix *Index) analyzeJavaFile(file *File, src []byte, skips SkipCounts) []TypedSite {
	defer func() {
		if r := recover(); r != nil {
			skips.add(SkipBodyPanic)
		}
	}()
	lang := javaLang()
	tree, err := gts.NewParser(lang).Parse(src)
	if err != nil {
		return nil
	}
	w := &javaBodyWalk{ix: ix, file: file, lang: lang, src: src, skips: skips}
	root := tree.RootNode()
	for i := 0; i < root.ChildCount(); i++ {
		if c := root.Child(i); c != nil {
			w.typeBodies(c, nil)
		}
	}
	return w.sites
}

type javaBodyWalk struct {
	ix    *Index
	file  *File
	lang  *gts.Language
	src   []byte
	skips SkipCounts
	sites []TypedSite
}

func (w *javaBodyWalk) text(n *gts.Node) string { return n.Text(w.src) }

func (w *javaBodyWalk) childByType(n *gts.Node, typ string) *gts.Node {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c != nil && c.Type(w.lang) == typ {
			return c
		}
	}
	return nil
}

// typeBodies pairs CST type declarations with their tabled Types (by FQN —
// the same walk order Phase A used) and descends into member bodies.
func (w *javaBodyWalk) typeBodies(n *gts.Node, nesting []string) {
	if _, isType := javaTypeForms[n.Type(w.lang)]; !isType {
		return
	}
	name := n.ChildByFieldName("name", w.lang)
	if name == nil {
		return
	}
	bare := w.text(name)
	ty := w.tabledType(fqnOf(w.file.Package, nesting, bare))
	if ty == nil {
		w.skips.add(SkipBodyUnmatched)
		return
	}
	body := n.ChildByFieldName("body", w.lang)
	if body == nil {
		return
	}
	nested := append(append([]string(nil), nesting...), bare)
	w.memberBodies(body, ty, nested)
	// Enum members live one level down, under enum_body_declarations.
	if decls := w.childByType(body, "enum_body_declarations"); decls != nil {
		w.memberBodies(decls, ty, nested)
	}
}

func (w *javaBodyWalk) memberBodies(body *gts.Node, ty *Type, nesting []string) {
	for i := 0; i < body.ChildCount(); i++ {
		c := body.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "method_declaration", "constructor_declaration":
			w.oneBody(c, ty)
		default:
			w.typeBodies(c, nesting)
		}
	}
}

// oneBody types one method/constructor body: seed the scope stack with the
// declared parameters, then walk the block.
func (w *javaBodyWalk) oneBody(decl *gts.Node, ty *Type) {
	name := decl.ChildByFieldName("name", w.lang)
	if name == nil {
		return
	}
	member := w.tabledMember(ty, w.text(name), nodeLine(name))
	if member == nil {
		w.skips.add(SkipBodyUnmatched)
		return
	}
	block := w.childByType(decl, "block")
	if block == nil {
		block = w.childByType(decl, "constructor_body")
	}
	if block == nil {
		return
	}
	env := &scopeStack{}
	env.push()
	for _, p := range member.Params {
		env.bind(p.Name, w.resolveLocalType(ty, p.Type))
	}
	w.block(block, ty, member, env)
}

// binding is one scoped name: a resolved type, or a named reason it is not
// typable (propagated to the site skip counter on use).
type binding struct {
	t      *Type
	reason string
}

type scopeStack struct{ scopes []map[string]binding }

func (s *scopeStack) push() { s.scopes = append(s.scopes, map[string]binding{}) }
func (s *scopeStack) pop()  { s.scopes = s.scopes[:len(s.scopes)-1] }
func (s *scopeStack) bind(name string, b binding) {
	if name != "" {
		s.scopes[len(s.scopes)-1][name] = b
	}
}
func (s *scopeStack) lookup(name string) (binding, bool) {
	for i := len(s.scopes) - 1; i >= 0; i-- {
		if b, ok := s.scopes[i][name]; ok {
			return b, true
		}
	}
	return binding{}, false
}

// resolveLocalType turns a declared TypeRef into a binding.
func (w *javaBodyWalk) resolveLocalType(enclosing *Type, ref TypeRef) binding {
	if ref.IsZero() {
		return binding{reason: SkipReceiverUntyped}
	}
	if ref.Base == "var" {
		return binding{reason: SkipVarInferred}
	}
	t, res := w.ix.ResolveTypeName(w.file, enclosing, ref.Base)
	switch res {
	case ResolvedType:
		return binding{t: t}
	case AmbiguousType:
		return binding{reason: SkipReceiverAmbig}
	default:
		return binding{reason: SkipReceiverExternal}
	}
}

// block walks statements in source order, pushing a scope per block.
func (w *javaBodyWalk) block(n *gts.Node, ty *Type, member *Member, env *scopeStack) {
	env.push()
	defer env.pop()
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		w.statement(c, ty, member, env)
	}
}

func (w *javaBodyWalk) statement(n *gts.Node, ty *Type, member *Member, env *scopeStack) {
	switch n.Type(w.lang) {
	case "local_variable_declaration":
		var ref TypeRef
		if t := n.ChildByFieldName("type", w.lang); t != nil {
			if r, ok := w.typeRefOf(t); ok {
				ref = r
			} else if w.text(t) == "var" {
				ref = TypeRef{Raw: "var", Base: "var"}
			}
		}
		b := w.resolveLocalType(ty, ref)
		for i := 0; i < n.ChildCount(); i++ {
			d := n.Child(i)
			if d == nil || d.Type(w.lang) != "variable_declarator" {
				continue
			}
			// Walk the initializer BEFORE binding (Java: the name is not in
			// scope in its own initializer for our purposes — sites inside
			// still get collected).
			w.expressions(d, ty, member, env)
			if name := d.ChildByFieldName("name", w.lang); name != nil {
				env.bind(w.text(name), b)
			}
		}
	case "block":
		w.block(n, ty, member, env)
	default:
		// Compound statements (if/for/while/try…) carry nested blocks and
		// expressions; recurse generically, opening a scope per nested block.
		for i := 0; i < n.ChildCount(); i++ {
			c := n.Child(i)
			if c == nil {
				continue
			}
			switch c.Type(w.lang) {
			case "block":
				w.block(c, ty, member, env)
			case "local_variable_declaration":
				w.statement(c, ty, member, env)
			default:
				if isJavaStatement(c.Type(w.lang)) {
					w.statement(c, ty, member, env)
				} else {
					w.expressions(c, ty, member, env)
				}
			}
		}
	}
}

// isJavaStatement lists the compound statement forms the walker recurses
// through as statements (so their nested locals bind scoped).
func isJavaStatement(t string) bool {
	switch t {
	case "if_statement", "for_statement", "enhanced_for_statement",
		"while_statement", "do_statement", "try_statement",
		"try_with_resources_statement", "switch_expression", "switch_block",
		"synchronized_statement", "labeled_statement", "expression_statement",
		"return_statement", "throw_statement":
		return true
	}
	return false
}

// expressions walks an expression tree collecting sites.
func (w *javaBodyWalk) expressions(n *gts.Node, ty *Type, member *Member, env *scopeStack) {
	switch n.Type(w.lang) {
	case "method_invocation":
		w.callSite(n, ty, member, env)
		// Recurse into receiver and arguments for nested sites.
		if obj := n.ChildByFieldName("object", w.lang); obj != nil {
			w.expressions(obj, ty, member, env)
		}
		if args := n.ChildByFieldName("arguments", w.lang); args != nil {
			w.expressions(args, ty, member, env)
		}
		return
	case "field_access":
		w.valueSite(n, ty, member, env)
		if obj := n.ChildByFieldName("object", w.lang); obj != nil {
			w.expressions(obj, ty, member, env)
		}
		return
	}
	for i := 0; i < n.ChildCount(); i++ {
		if c := n.Child(i); c != nil {
			w.expressions(c, ty, member, env)
		}
	}
}

// callSite types one method_invocation's receiver and binds the member.
func (w *javaBodyWalk) callSite(n *gts.Node, ty *Type, member *Member, env *scopeStack) {
	name := n.ChildByFieldName("name", w.lang)
	args := n.ChildByFieldName("arguments", w.lang)
	if name == nil || args == nil {
		w.skips.add(SkipReceiverUntyped)
		return
	}
	arity := countArgs(args, w.lang)

	obj := n.ChildByFieldName("object", w.lang)
	var (
		receiver *Type
		static   bool
		implicit bool
	)
	if obj == nil {
		// Bare call: Java's implicit this — bound through the enclosing type.
		receiver, implicit = ty, true
	} else {
		b := w.typeOfExpr(obj, ty, member, env)
		if b.t == nil {
			w.skips.add(b.reason)
			return
		}
		receiver = b.t
		static = b.static
	}

	res := w.ix.LookupCallable(receiver, w.text(name), arity)
	switch res.Outcome {
	case BoundMember:
		w.sites = append(w.sites, TypedSite{
			FromFile: w.file.Path, FromType: ty, FromMember: member,
			Receiver: receiver, Declaring: res.Declaring, Member: res.Member,
			StaticReceiver: static, ImplicitReceiver: implicit,
			Name: w.text(name), Arity: arity, Line: nodeLine(name), Kind: SiteCall,
		})
	case AmbiguousMember:
		w.skips.add(SkipLookupAmbiguous)
	case OpenChain:
		w.skips.add(SkipLookupOpenChain)
	default:
		w.skips.add(SkipLookupNotFound)
	}
}

// valueSite types one field_access as a value use.
func (w *javaBodyWalk) valueSite(n *gts.Node, ty *Type, member *Member, env *scopeStack) {
	obj := n.ChildByFieldName("object", w.lang)
	fieldName := n.ChildByFieldName("field", w.lang)
	if obj == nil || fieldName == nil {
		return
	}
	b := w.typeOfExpr(obj, ty, member, env)
	if b.t == nil {
		w.skips.add(b.reason)
		return
	}
	res := w.ix.LookupValue(b.t, w.text(fieldName))
	switch res.Outcome {
	case BoundMember:
		w.sites = append(w.sites, TypedSite{
			FromFile: w.file.Path, FromType: ty, FromMember: member,
			Receiver: b.t, Declaring: res.Declaring, Member: res.Member,
			StaticReceiver: b.static,
			Name:           w.text(fieldName), Arity: -1, Line: nodeLine(fieldName), Kind: SiteValue,
		})
	case AmbiguousMember:
		w.skips.add(SkipLookupAmbiguous)
	case OpenChain:
		w.skips.add(SkipLookupOpenChain)
	default:
		w.skips.add(SkipLookupNotFound)
	}
}

// exprBinding is typeOfExpr's outcome: a type (with the static-receiver
// marker) or the named reason there is none.
type exprBinding struct {
	t      *Type
	static bool
	reason string
}

// typeOfExpr types an expression from DECLARED forms only.
func (w *javaBodyWalk) typeOfExpr(n *gts.Node, ty *Type, member *Member, env *scopeStack) exprBinding {
	switch n.Type(w.lang) {
	case "identifier":
		name := w.text(n)
		if b, ok := env.lookup(name); ok {
			if b.t == nil {
				return exprBinding{reason: b.reason}
			}
			return exprBinding{t: b.t}
		}
		// A field of the enclosing type's chain.
		if res := w.ix.LookupValue(ty, name); res.Outcome == BoundMember {
			return w.memberTypeBinding(res)
		}
		// A TYPE name: the qualified-static receiver form.
		if t, r := w.ix.ResolveTypeName(w.file, ty, name); r == ResolvedType {
			return exprBinding{t: t, static: true}
		} else if r == AmbiguousType {
			return exprBinding{reason: SkipReceiverAmbig}
		}
		return exprBinding{reason: SkipReceiverUntyped}
	case "this":
		return exprBinding{t: ty}
	case "super":
		supers, closed := w.ix.DirectSupertypes(ty)
		if !closed {
			return exprBinding{reason: SkipSuperExternal}
		}
		for _, s := range supers {
			if s.Form == FormClass {
				return exprBinding{t: s}
			}
		}
		return exprBinding{reason: SkipSuperExternal}
	case "object_creation_expression":
		if t := n.ChildByFieldName("type", w.lang); t != nil {
			if ref, ok := w.typeRefOf(t); ok {
				return w.refBinding(ty, ref)
			}
		}
		return exprBinding{reason: SkipReceiverUntyped}
	case "parenthesized_expression":
		for i := 0; i < n.ChildCount(); i++ {
			c := n.Child(i)
			if c == nil {
				continue
			}
			if t := c.Type(w.lang); t != "(" && t != ")" {
				return w.typeOfExpr(c, ty, member, env)
			}
		}
		return exprBinding{reason: SkipReceiverUntyped}
	case "cast_expression":
		// A cast is a DECLARED type assertion; javac binds against it.
		if t := n.ChildByFieldName("type", w.lang); t != nil {
			if ref, ok := w.typeRefOf(t); ok {
				return w.refBinding(ty, ref)
			}
		}
		return exprBinding{reason: SkipReceiverUntyped}
	case "method_invocation":
		// Declared-return-type chain: type the inner call's bound member.
		name := n.ChildByFieldName("name", w.lang)
		args := n.ChildByFieldName("arguments", w.lang)
		if name == nil || args == nil {
			return exprBinding{reason: SkipReceiverUntyped}
		}
		var recv exprBinding
		if obj := n.ChildByFieldName("object", w.lang); obj != nil {
			recv = w.typeOfExpr(obj, ty, member, env)
		} else {
			recv = exprBinding{t: ty}
		}
		if recv.t == nil {
			return recv
		}
		res := w.ix.LookupCallable(recv.t, w.text(name), countArgs(args, w.lang))
		if res.Outcome != BoundMember {
			return exprBinding{reason: lookupSkip(res.Outcome)}
		}
		return w.memberTypeBinding(res)
	case "field_access":
		obj := n.ChildByFieldName("object", w.lang)
		fieldName := n.ChildByFieldName("field", w.lang)
		if obj == nil || fieldName == nil {
			return exprBinding{reason: SkipReceiverUntyped}
		}
		recv := w.typeOfExpr(obj, ty, member, env)
		if recv.t == nil {
			return recv
		}
		res := w.ix.LookupValue(recv.t, w.text(fieldName))
		if res.Outcome != BoundMember {
			return exprBinding{reason: lookupSkip(res.Outcome)}
		}
		return w.memberTypeBinding(res)
	}
	return exprBinding{reason: SkipReceiverUntyped}
}

// memberTypeBinding resolves a bound member's declared type in the DECLARING
// type's context (its file, its scope — where the type name was written).
func (w *javaBodyWalk) memberTypeBinding(res LookupResult) exprBinding {
	if res.Member.Type.IsZero() {
		return exprBinding{reason: SkipReceiverUntyped}
	}
	declFile := w.ix.fileOf(res.Declaring)
	if declFile == nil {
		return exprBinding{reason: SkipReceiverUntyped}
	}
	t, r := w.ix.ResolveTypeName(declFile, res.Declaring, res.Member.Type.Base)
	switch r {
	case ResolvedType:
		return exprBinding{t: t}
	case AmbiguousType:
		return exprBinding{reason: SkipReceiverAmbig}
	default:
		return exprBinding{reason: SkipReceiverExternal}
	}
}

// refBinding resolves a written TypeRef in the CURRENT file's context.
func (w *javaBodyWalk) refBinding(enclosing *Type, ref TypeRef) exprBinding {
	t, r := w.ix.ResolveTypeName(w.file, enclosing, ref.Base)
	switch r {
	case ResolvedType:
		return exprBinding{t: t}
	case AmbiguousType:
		return exprBinding{reason: SkipReceiverAmbig}
	default:
		return exprBinding{reason: SkipReceiverExternal}
	}
}

func lookupSkip(o LookupOutcome) string {
	switch o {
	case AmbiguousMember:
		return SkipLookupAmbiguous
	case OpenChain:
		return SkipLookupOpenChain
	default:
		return SkipLookupNotFound
	}
}

// typeRefOf adapts the Phase A type-node erasure for body positions.
func (w *javaBodyWalk) typeRefOf(n *gts.Node) (TypeRef, bool) {
	jw := &javaWalk{lang: w.lang, src: w.src}
	return jw.typeRef(n)
}

// countArgs counts an argument_list's expression children (token children
// excluded).
func countArgs(args *gts.Node, lang *gts.Language) int {
	n := 0
	for i := 0; i < args.ChildCount(); i++ {
		c := args.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(lang) {
		case "(", ")", ",":
		default:
			n++
		}
	}
	return n
}

// tabledType finds the unique tabled type for an FQN; nil when absent or
// duplicated (the strict rule: a duplicate FQN is unresolvable).
func (w *javaBodyWalk) tabledType(fqn string) *Type {
	cands := w.ix.byFQN[fqn]
	if len(cands) != 1 {
		return nil
	}
	return cands[0]
}

// tabledMember pairs a CST member with its Phase A row by (name, line).
func (w *javaBodyWalk) tabledMember(ty *Type, name string, line int) *Member {
	for i := range ty.Members {
		m := &ty.Members[i]
		if m.Name == name && m.Line == line {
			return m
		}
	}
	return nil
}
