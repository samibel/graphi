package jvmresolve

// Slice 5 (WP-J4 Phase B, Kotlin): declared-type receiver propagation through
// Kotlin function bodies — the Kotlin twin of body_java.go, sharing its
// scope-stack, binding and SkipCounts machinery, with the Kotlin-specific
// honesty rules ADR 0008 names:
//
//	PROVABLE: parameters and explicitly typed locals (`val x: T`), properties
//	via the member lookup, `this` OUTSIDE lambdas, constructor results
//	(`Foo(...)` — the written type name IS the declared result type),
//	qualified statics/objects (`Registry.lookup()`), declared-return chains,
//	same-file top-level functions for bare calls.
//
//	NAMED GAPS, never guessed:
//	  - inferred `val`/`var` (no declared type) — the idiomatic-Kotlin recall
//	    cost ADR 0008 D2 measures;
//	  - `this` and BARE calls inside ANY lambda: scope functions (`apply`,
//	    `with`, `run`, `let`) rebind the receiver, and a declared-type walker
//	    cannot know which lambda rebinds — every lambda forfeits implicit
//	    binding (explicitly-typed receivers inside lambdas stay provable:
//	    closure capture preserves declared types);
//	  - calls carrying a TRAILING LAMBDA argument: the suffix hides an
//	    argument from the arity count, so (name, arity) binding would bind
//	    the WRONG overload — forfeited;
//	  - extension functions/receivers are invisible here by construction:
//	    they resolve like external members and drop.
//
// Like the Java walker, sites are collected from every tabled body (companion
// and enum bodies included) — whether endpoints have nodes is Phase C's
// question. Class-level property INITIALIZERS are not walked in this slice
// (matching the Java walker, which walks method/constructor bodies only); the
// gap is a recall gap and is recorded here rather than implied.

import (
	gts "github.com/odvcencio/gotreesitter"
)

// Kotlin skip-counter names (the Java set lives in body_java.go).
const (
	SkipKtValInferred      = "kotlin_val_inferred"       // inferred local/property receiver
	SkipKtReceiverUntyped  = "kotlin_receiver_untyped"   // expression form the walker cannot type
	SkipKtReceiverExternal = "kotlin_receiver_external"  // receiver type resolves outside the repo
	SkipKtReceiverAmbig    = "kotlin_receiver_ambiguous" // receiver type name ambiguous
	SkipKtLookupAmbiguous  = "kotlin_lookup_ambiguous"   // D6 overload-set drop
	SkipKtLookupOpenChain  = "kotlin_lookup_open_chain"  // external/ambiguous supertype forfeit
	SkipKtLookupNotFound   = "kotlin_lookup_not_found"   // closed chain provably lacks the member
	SkipKtLambdaRebound    = "kotlin_lambda_rebound"     // `this`/bare call inside a lambda
	SkipKtTrailingLambda   = "kotlin_trailing_lambda"    // trailing-lambda call: arity uncountable
	SkipKtBodyUnmatched    = "kotlin_body_unmatched"     // CST body with no tabled type/member pair
	SkipKtBodyPanic        = "kotlin_body_panic"         // body walk panicked; file's sites dropped
)

// AnalyzeKotlinBodies walks every tabled Kotlin file's function bodies over
// src and returns the proved sites plus the named skip counters.
func (ix *Index) AnalyzeKotlinBodies(src map[string][]byte) ([]TypedSite, SkipCounts) {
	skips := SkipCounts{}
	var sites []TypedSite
	for fi := range ix.table.Files {
		file := &ix.table.Files[fi]
		if file.Language != LangKotlin {
			continue
		}
		bytes, ok := src[file.Path]
		if !ok {
			continue
		}
		sites = append(sites, ix.analyzeKotlinFile(file, bytes, skips)...)
	}
	return sites, skips
}

func (ix *Index) analyzeKotlinFile(file *File, src []byte, skips SkipCounts) []TypedSite {
	defer func() {
		if r := recover(); r != nil {
			skips.add(SkipKtBodyPanic)
		}
	}()
	lang := kotlinLang()
	tree, err := gts.NewParser(lang).Parse(src)
	if err != nil {
		return nil
	}
	w := &kotlinBodyWalk{ix: ix, file: file, lang: lang, src: src, skips: skips}
	root := tree.RootNode()
	for i := 0; i < root.ChildCount(); i++ {
		if c := root.Child(i); c != nil {
			w.decl(c, nil, nil)
		}
	}
	return w.sites
}

type kotlinBodyWalk struct {
	ix    *Index
	file  *File
	lang  *gts.Language
	src   []byte
	skips SkipCounts
	sites []TypedSite
	// lambdaDepth > 0 while inside any lambda literal: implicit binding
	// (`this`, bare calls) is forfeited there.
	lambdaDepth int
}

func (w *kotlinBodyWalk) text(n *gts.Node) string { return n.Text(w.src) }

func (w *kotlinBodyWalk) childByType(n *gts.Node, typ string) *gts.Node {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c != nil && c.Type(w.lang) == typ {
			return c
		}
	}
	return nil
}

// decl descends type declarations (pairing them with tabled Types by FQN) and
// walks function bodies. enclosing is nil for top-level declarations.
func (w *kotlinBodyWalk) decl(n *gts.Node, enclosing *Type, nesting []string) {
	switch n.Type(w.lang) {
	case "class_declaration", "object_declaration":
		name := w.childByType(n, "type_identifier")
		if name == nil {
			return
		}
		bare := w.text(name)
		ty := w.tabledType(fqnOf(w.file.Package, nesting, bare))
		if ty == nil {
			w.skips.add(SkipKtBodyUnmatched)
			return
		}
		nested := append(append([]string(nil), nesting...), bare)
		for _, bodyType := range []string{"class_body", "enum_class_body"} {
			if body := w.childByType(n, bodyType); body != nil {
				for i := 0; i < body.ChildCount(); i++ {
					if c := body.Child(i); c != nil {
						w.decl(c, ty, nested)
					}
				}
			}
		}
	case "companion_object":
		bare := "Companion"
		if name := w.childByType(n, "type_identifier"); name != nil {
			bare = w.text(name)
		}
		ty := w.tabledType(fqnOf(w.file.Package, nesting, bare))
		if ty == nil {
			w.skips.add(SkipKtBodyUnmatched)
			return
		}
		nested := append(append([]string(nil), nesting...), bare)
		if body := w.childByType(n, "class_body"); body != nil {
			for i := 0; i < body.ChildCount(); i++ {
				if c := body.Child(i); c != nil {
					w.decl(c, ty, nested)
				}
			}
		}
	case "function_declaration":
		w.oneBody(n, enclosing)
	}
}

// oneBody walks one function body (block or `= expr` form).
func (w *kotlinBodyWalk) oneBody(decl *gts.Node, enclosing *Type) {
	name := w.childByType(decl, "simple_identifier")
	if name == nil {
		return
	}
	member := w.tabledFunc(enclosing, w.text(name), nodeLine(name))
	if member == nil {
		w.skips.add(SkipKtBodyUnmatched)
		return
	}
	body := w.childByType(decl, "function_body")
	if body == nil {
		return
	}
	env := &scopeStack{}
	env.push()
	for _, p := range member.Params {
		env.bind(p.Name, w.resolveLocalType(enclosing, p.Type))
	}
	w.walk(body, enclosing, member, env)
}

// resolveLocalType turns a declared TypeRef into a binding (Kotlin reasons).
func (w *kotlinBodyWalk) resolveLocalType(enclosing *Type, ref TypeRef) binding {
	if ref.IsZero() {
		return binding{reason: SkipKtValInferred}
	}
	t, res := w.ix.ResolveTypeName(w.file, enclosing, ref.Base)
	switch res {
	case ResolvedType:
		return binding{t: t}
	case AmbiguousType:
		return binding{reason: SkipKtReceiverAmbig}
	default:
		return binding{reason: SkipKtReceiverExternal}
	}
}

// walk recurses statements and expressions. A `statements` node is a block
// boundary (scope push); a lambda_literal additionally forfeits implicit
// binding for everything beneath it.
func (w *kotlinBodyWalk) walk(n *gts.Node, ty *Type, member *Member, env *scopeStack) {
	switch n.Type(w.lang) {
	case "statements":
		env.push()
		defer env.pop()
		for i := 0; i < n.ChildCount(); i++ {
			if c := n.Child(i); c != nil {
				w.walk(c, ty, member, env)
			}
		}
		return
	case "lambda_literal":
		w.lambdaDepth++
		defer func() { w.lambdaDepth-- }()
		for i := 0; i < n.ChildCount(); i++ {
			if c := n.Child(i); c != nil {
				w.walk(c, ty, member, env)
			}
		}
		return
	case "property_declaration":
		// A local: walk the initializer first, then bind the name.
		for i := 0; i < n.ChildCount(); i++ {
			c := n.Child(i)
			if c == nil || c.Type(w.lang) == "variable_declaration" {
				continue
			}
			w.walk(c, ty, member, env)
		}
		if vd := w.childByType(n, "variable_declaration"); vd != nil {
			if name := w.childByType(vd, "simple_identifier"); name != nil {
				var ref TypeRef
				if r, ok := w.declaredTypeOf(vd); ok {
					ref = r
				}
				env.bind(w.text(name), w.resolveLocalType(ty, ref))
			}
		}
		return
	case "call_expression":
		w.callSite(n, ty, member, env)
		return
	case "navigation_expression":
		w.valueSite(n, ty, member, env)
		return
	}
	for i := 0; i < n.ChildCount(); i++ {
		if c := n.Child(i); c != nil {
			w.walk(c, ty, member, env)
		}
	}
}

// callSite handles one call_expression: callee is a bare simple_identifier or
// a navigation_expression whose suffix names the member.
func (w *kotlinBodyWalk) callSite(n *gts.Node, ty *Type, member *Member, env *scopeStack) {
	suffix := w.childByType(n, "call_suffix")
	if suffix == nil {
		return
	}
	// Trailing lambda: the suffix hides an argument from the arity count —
	// (name, arity) binding would bind the wrong overload. Forfeit the site,
	// still walk inside for nested sites.
	if w.childByType(suffix, "annotated_lambda") != nil {
		w.skips.add(SkipKtTrailingLambda)
		w.walk(suffix, ty, member, env)
		if nav := w.childByType(n, "navigation_expression"); nav != nil {
			w.walkReceiverOnly(nav, ty, member, env)
		}
		return
	}
	args := w.childByType(suffix, "value_arguments")
	if args == nil {
		return
	}
	arity := countArgs(args, w.lang)

	if callee := w.childByType(n, "simple_identifier"); callee != nil {
		w.bareOrConstructorCall(callee, arity, ty, member, env)
		w.walk(args, ty, member, env)
		return
	}
	nav := w.childByType(n, "navigation_expression")
	if nav == nil {
		w.skips.add(SkipKtReceiverUntyped)
		w.walk(args, ty, member, env)
		return
	}
	nameNode := w.navName(nav)
	recvNode := w.navReceiver(nav)
	if nameNode == nil || recvNode == nil {
		w.skips.add(SkipKtReceiverUntyped)
		w.walk(args, ty, member, env)
		return
	}
	b := w.typeOfExpr(recvNode, ty, member, env)
	if b.t == nil {
		w.skips.add(b.reason)
	} else {
		w.emit(ty, member, b, w.text(nameNode), arity, nodeLine(nameNode), SiteCall)
	}
	w.walkReceiverOnly(recvNode, ty, member, env)
	w.walk(args, ty, member, env)
}

// bareOrConstructorCall handles `helper()` / `Rate()`: inside a lambda the
// implicit form is forfeited; a TYPE name is a constructor call; otherwise
// the enclosing type's chain, then same-file top-level functions.
func (w *kotlinBodyWalk) bareOrConstructorCall(callee *gts.Node, arity int, ty *Type, member *Member, env *scopeStack) {
	name := w.text(callee)
	line := nodeLine(callee)
	// A local/param bound to a name is not callable knowledge we track.
	if _, isLocal := env.lookup(name); isLocal {
		w.skips.add(SkipKtReceiverUntyped)
		return
	}
	// Constructor call: the written name resolves to a TYPE.
	if target, res := w.ix.ResolveTypeName(w.file, ty, name); res == ResolvedType {
		w.constructorSite(target, arity, ty, member, name, line)
		return
	} else if res == AmbiguousType {
		w.skips.add(SkipKtReceiverAmbig)
		return
	}
	if w.lambdaDepth > 0 {
		// A scope-function lambda may have rebound the implicit receiver.
		w.skips.add(SkipKtLambdaRebound)
		return
	}
	if ty != nil {
		res := w.ix.LookupCallableValueClassAware(ty, name, arity)
		switch res.Outcome {
		case BoundMember:
			w.sites = append(w.sites, TypedSite{
				FromFile: w.file.Path, FromType: ty, FromMember: member,
				Receiver: ty, Declaring: res.Declaring, Member: res.Member,
				ImplicitReceiver: true, Name: name, Arity: arity, Line: line, Kind: SiteCall,
			})
			return
		case AmbiguousMember:
			w.skips.add(SkipKtLookupAmbiguous)
			return
		case OpenChain:
			w.skips.add(SkipKtLookupOpenChain)
			return
		}
		// NotFound falls through to the file's top level.
	}
	if m := w.topLevelFunc(name, arity); m != nil {
		w.sites = append(w.sites, TypedSite{
			FromFile: w.file.Path, FromType: ty, FromMember: member,
			Declaring: nil, Member: m,
			ImplicitReceiver: true, Name: name, Arity: arity, Line: line, Kind: SiteCall,
		})
		return
	}
	w.skips.add(SkipKtLookupNotFound)
}

// constructorSite binds `Foo(...)` to Foo's tabled constructor of that arity.
func (w *kotlinBodyWalk) constructorSite(target *Type, arity int, ty *Type, member *Member, name string, line int) {
	for mi := range target.Members {
		m := &target.Members[mi]
		if m.Form == MemberConstructor && len(m.Params) == arity {
			w.sites = append(w.sites, TypedSite{
				FromFile: w.file.Path, FromType: ty, FromMember: member,
				Receiver: target, Declaring: target, Member: m,
				StaticReceiver: true, Name: name, Arity: arity, Line: line, Kind: SiteCall,
			})
			return
		}
	}
	// No tabled constructor of this arity (implicit zero-arg, or secondary
	// constructors this slice does not table): an honest recall gap.
	w.skips.add(SkipKtLookupNotFound)
}

// valueSite handles a navigation_expression in value position (`s.length`).
func (w *kotlinBodyWalk) valueSite(n *gts.Node, ty *Type, member *Member, env *scopeStack) {
	nameNode := w.navName(n)
	recvNode := w.navReceiver(n)
	if nameNode == nil || recvNode == nil {
		return
	}
	b := w.typeOfExpr(recvNode, ty, member, env)
	if b.t == nil {
		w.skips.add(b.reason)
	} else {
		res := w.ix.LookupValue(b.t, w.text(nameNode))
		switch res.Outcome {
		case BoundMember:
			w.sites = append(w.sites, TypedSite{
				FromFile: w.file.Path, FromType: ty, FromMember: member,
				Receiver: b.t, Declaring: res.Declaring, Member: res.Member,
				StaticReceiver: b.static, Name: w.text(nameNode), Arity: -1,
				Line: nodeLine(nameNode), Kind: SiteValue,
			})
		case AmbiguousMember:
			w.skips.add(SkipKtLookupAmbiguous)
		case OpenChain:
			w.skips.add(SkipKtLookupOpenChain)
		default:
			w.skips.add(SkipKtLookupNotFound)
		}
	}
	w.walkReceiverOnly(recvNode, ty, member, env)
}

// walkReceiverOnly recurses a receiver expression for NESTED sites: an inner
// call is a call site, an inner navigation a value site (matching the Java
// walker, where `this.total` inside a larger expression reports the field
// read).
func (w *kotlinBodyWalk) walkReceiverOnly(n *gts.Node, ty *Type, member *Member, env *scopeStack) {
	switch n.Type(w.lang) {
	case "call_expression":
		w.callSite(n, ty, member, env)
	case "navigation_expression":
		w.valueSite(n, ty, member, env)
	default:
		w.walk(n, ty, member, env)
	}
}

// typeOfExpr types a Kotlin expression from declared forms only.
func (w *kotlinBodyWalk) typeOfExpr(n *gts.Node, ty *Type, member *Member, env *scopeStack) exprBinding {
	switch n.Type(w.lang) {
	case "simple_identifier":
		name := w.text(n)
		if b, ok := env.lookup(name); ok {
			if b.t == nil {
				return exprBinding{reason: b.reason}
			}
			return exprBinding{t: b.t}
		}
		if ty != nil {
			if res := w.ix.LookupValue(ty, name); res.Outcome == BoundMember {
				return w.memberTypeBindingKt(res)
			}
		}
		if t, r := w.ix.ResolveTypeName(w.file, ty, name); r == ResolvedType {
			return exprBinding{t: t, static: true}
		} else if r == AmbiguousType {
			return exprBinding{reason: SkipKtReceiverAmbig}
		}
		return exprBinding{reason: SkipKtReceiverUntyped}
	case "this_expression":
		if w.lambdaDepth > 0 {
			return exprBinding{reason: SkipKtLambdaRebound}
		}
		if ty == nil {
			return exprBinding{reason: SkipKtReceiverUntyped}
		}
		return exprBinding{t: ty}
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
		return exprBinding{reason: SkipKtReceiverUntyped}
	case "call_expression":
		// Constructor result or declared-return chain.
		suffix := w.childByType(n, "call_suffix")
		if suffix == nil || w.childByType(suffix, "annotated_lambda") != nil {
			return exprBinding{reason: SkipKtTrailingLambda}
		}
		args := w.childByType(suffix, "value_arguments")
		if args == nil {
			return exprBinding{reason: SkipKtReceiverUntyped}
		}
		arity := countArgs(args, w.lang)
		if callee := w.childByType(n, "simple_identifier"); callee != nil {
			name := w.text(callee)
			if _, isLocal := env.lookup(name); !isLocal {
				if t, r := w.ix.ResolveTypeName(w.file, ty, name); r == ResolvedType {
					return exprBinding{t: t} // constructor result: the written type
				}
			}
			// A bare call's return type: enclosing chain outside lambdas.
			if w.lambdaDepth > 0 {
				return exprBinding{reason: SkipKtLambdaRebound}
			}
			if ty != nil {
				if res := w.ix.LookupCallableValueClassAware(ty, name, arity); res.Outcome == BoundMember {
					return w.memberTypeBindingKt(res)
				} else if res.Outcome != NotFoundMember {
					return exprBinding{reason: lookupSkipKt(res.Outcome)}
				}
			}
			if m := w.topLevelFunc(name, arity); m != nil {
				return w.topLevelTypeBinding(m)
			}
			return exprBinding{reason: SkipKtLookupNotFound}
		}
		nav := w.childByType(n, "navigation_expression")
		if nav == nil {
			return exprBinding{reason: SkipKtReceiverUntyped}
		}
		nameNode := w.navName(nav)
		recvNode := w.navReceiver(nav)
		if nameNode == nil || recvNode == nil {
			return exprBinding{reason: SkipKtReceiverUntyped}
		}
		recv := w.typeOfExpr(recvNode, ty, member, env)
		if recv.t == nil {
			return recv
		}
		res := w.ix.LookupCallableValueClassAware(recv.t, w.text(nameNode), arity)
		if res.Outcome != BoundMember {
			return exprBinding{reason: lookupSkipKt(res.Outcome)}
		}
		return w.memberTypeBindingKt(res)
	case "navigation_expression":
		nameNode := w.navName(n)
		recvNode := w.navReceiver(n)
		if nameNode == nil || recvNode == nil {
			return exprBinding{reason: SkipKtReceiverUntyped}
		}
		recv := w.typeOfExpr(recvNode, ty, member, env)
		if recv.t == nil {
			return recv
		}
		res := w.ix.LookupValue(recv.t, w.text(nameNode))
		if res.Outcome != BoundMember {
			return exprBinding{reason: lookupSkipKt(res.Outcome)}
		}
		return w.memberTypeBindingKt(res)
	}
	return exprBinding{reason: SkipKtReceiverUntyped}
}

// memberTypeBindingKt resolves a bound member's declared type in its
// declaring context; a zero type is Kotlin inference — the D2 gap.
func (w *kotlinBodyWalk) memberTypeBindingKt(res LookupResult) exprBinding {
	if res.Member.Type.IsZero() {
		return exprBinding{reason: SkipKtValInferred}
	}
	declFile := w.ix.fileOf(res.Declaring)
	if declFile == nil {
		return exprBinding{reason: SkipKtReceiverUntyped}
	}
	t, r := w.ix.ResolveTypeName(declFile, res.Declaring, res.Member.Type.Base)
	switch r {
	case ResolvedType:
		return exprBinding{t: t}
	case AmbiguousType:
		return exprBinding{reason: SkipKtReceiverAmbig}
	default:
		return exprBinding{reason: SkipKtReceiverExternal}
	}
}

// topLevelTypeBinding resolves a top-level function's declared return type in
// this file's own context.
func (w *kotlinBodyWalk) topLevelTypeBinding(m *Member) exprBinding {
	if m.Type.IsZero() {
		return exprBinding{reason: SkipKtValInferred}
	}
	t, r := w.ix.ResolveTypeName(w.file, nil, m.Type.Base)
	switch r {
	case ResolvedType:
		return exprBinding{t: t}
	case AmbiguousType:
		return exprBinding{reason: SkipKtReceiverAmbig}
	default:
		return exprBinding{reason: SkipKtReceiverExternal}
	}
}

func lookupSkipKt(o LookupOutcome) string {
	switch o {
	case AmbiguousMember:
		return SkipKtLookupAmbiguous
	case OpenChain:
		return SkipKtLookupOpenChain
	default:
		return SkipKtLookupNotFound
	}
}

// emit records a call site bound through the D6 lookup on a typed receiver.
func (w *kotlinBodyWalk) emit(ty *Type, member *Member, recv exprBinding, name string, arity, line int, kind SiteKind) {
	res := w.ix.LookupCallableValueClassAware(recv.t, name, arity)
	switch res.Outcome {
	case BoundMember:
		w.sites = append(w.sites, TypedSite{
			FromFile: w.file.Path, FromType: ty, FromMember: member,
			Receiver: recv.t, Declaring: res.Declaring, Member: res.Member,
			StaticReceiver: recv.static, Name: name, Arity: arity, Line: line, Kind: kind,
		})
	case AmbiguousMember:
		w.skips.add(SkipKtLookupAmbiguous)
	case OpenChain:
		w.skips.add(SkipKtLookupOpenChain)
	default:
		w.skips.add(SkipKtLookupNotFound)
	}
}

// navName returns a navigation_expression's member name (the suffix's
// simple_identifier); navReceiver its receiver expression (the first
// non-suffix child).
func (w *kotlinBodyWalk) navName(nav *gts.Node) *gts.Node {
	if s := w.childByType(nav, "navigation_suffix"); s != nil {
		return w.childByType(s, "simple_identifier")
	}
	return nil
}

func (w *kotlinBodyWalk) navReceiver(nav *gts.Node) *gts.Node {
	for i := 0; i < nav.ChildCount(); i++ {
		c := nav.Child(i)
		if c == nil || c.Type(w.lang) == "navigation_suffix" {
			continue
		}
		return c
	}
	return nil
}

// declaredTypeOf reads a variable_declaration's declared type.
func (w *kotlinBodyWalk) declaredTypeOf(vd *gts.Node) (TypeRef, bool) {
	kw := &kotlinWalk{lang: w.lang, src: w.src}
	return kw.declaredType(vd)
}

// tabledType finds the unique tabled type for an FQN (nil when absent or
// duplicated — the strict rule).
func (w *kotlinBodyWalk) tabledType(fqn string) *Type {
	cands := w.ix.byFQN[fqn]
	if len(cands) != 1 {
		return nil
	}
	return cands[0]
}

// tabledFunc pairs a CST function with its Phase A row: a member of the
// enclosing type, or a top-level function of this file.
func (w *kotlinBodyWalk) tabledFunc(enclosing *Type, name string, line int) *Member {
	if enclosing != nil {
		for i := range enclosing.Members {
			m := &enclosing.Members[i]
			if m.Form == MemberFunction && m.Name == name && m.Line == line {
				return m
			}
		}
		return nil
	}
	for i := range w.file.TopLevel {
		m := &w.file.TopLevel[i]
		if m.Form == MemberFunction && m.Name == name && m.Line == line {
			return m
		}
	}
	return nil
}

// topLevelFunc finds a same-file top-level function by (name, arity).
// Cross-file package-level binding is a later refinement; missing it costs
// recall, never soundness.
func (w *kotlinBodyWalk) topLevelFunc(name string, arity int) *Member {
	for i := range w.file.TopLevel {
		m := &w.file.TopLevel[i]
		if m.Form == MemberFunction && m.Name == name && len(m.Params) == arity {
			return m
		}
	}
	return nil
}
