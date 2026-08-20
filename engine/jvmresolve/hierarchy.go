package jvmresolve

import "strings"

// Slice 3 (WP-J3, the member-lookup half of Phase C's machinery): supertype
// chains and member binding over the Phase A table, under ADR 0008's D6 rule
// and the fail-closed posture.
//
// THE BINDING RULE, in full (this comment is the D6 implementation record):
//
//	A callable binds at (name, arity). The chain is walked receiver-outward
//	in breadth-first levels (the receiver type, then its resolved direct
//	supertypes in declaration order, then theirs, cycle-safe). Matches are
//	collected across ALL levels; then:
//
//	  - ELASTIC-ARITY GUARD (JVMSOUND-001), checked first: if ANY callable of
//	    this name in the closed chain has a VARIADIC or DEFAULTED parameter, the
//	    member can satisfy a call at more arities than its declared count, so
//	    (name, arity) is no longer a reliable key — forfeit outright
//	    (AmbiguousMember). Without this, `f(int,int)` beside `f(String...)`
//	    would bind the fixed overload to `f("a","b")` where javac binds the
//	    varargs one: a WRONG edge, not a missing one.
//	  - every match carries the IDENTICAL RESOLVED parameter signature
//	    → an override chain: bind the FIRST-FOUND (most derived) member —
//	      which is javac's static-binding target for the receiver's type. The
//	      signature keys each parameter on its RESOLVED type identity (intra-repo
//	      FQN via ResolveTypeName), NOT its written text (JVMSOUND-002): two
//	      parameters both spelled `Foo` that resolve to `q.Foo` and `r.Foo` are
//	      DIFFERENT overloads and must not collapse. Primitives and external
//	      types (which do not resolve intra-repo) key on their marked text, so a
//	      genuine `m(int)`/`m(int)` override still collapses correctly.
//	  - signatures differ anywhere in the match set
//	    → same-arity OVERLOADS: javac would pick by argument types, which a
//	      declared-type binder cannot prove — AmbiguousMember, drop+count;
//	  - no match and the chain is CLOSED (every supertype clause resolved
//	    intra-repo) → NotFound, drop+count;
//	  - no binding certainty because the chain is OPEN — some supertype is
//	    external or ambiguous — → OpenChain, drop+count. This is strict on
//	    purpose: even a receiver-level match can be beaten by a more
//	    applicable overload declared in an external supertype, so ANY open
//	    chain forfeits the binding. Costs recall (types extending stdlib
//	    classes), never soundness.
//
//	A field/property binds by NAME (fields cannot overload); same-name
//	declarations at several levels are hiding, and the most derived wins for
//	the receiver's static type — the same first-found rule. The open-chain
//	forfeit applies identically.

// Lookup outcomes — the closed vocabulary Phase C's skip counters key on.
type LookupOutcome int

const (
	// BoundMember: exactly one honest static-binding target.
	BoundMember LookupOutcome = iota + 1
	// AmbiguousMember: same-arity overloads with differing signatures (or a
	// same-FQN duplicate type in the chain) — never ranked.
	AmbiguousMember
	// NotFoundMember: the closed intra-repo chain provably lacks the member.
	NotFoundMember
	// OpenChain: an external or ambiguous supertype makes every binding
	// unprovable — fail closed.
	OpenChain
)

// LookupResult is one member binding.
type LookupResult struct {
	// Declaring is the type whose declaration binds; Member points into it.
	Declaring *Type
	Member    *Member
	Outcome   LookupOutcome
}

// DirectSupertypes resolves a type's supertype clauses. closed=false when any
// clause is external or ambiguous — the OPEN marker the lookup forfeits on.
// Resolution runs in the DECLARING file with the type itself as the enclosing
// scope (its own and its enclosing types' nested names are in scope at the
// clause, per the Phase A walk order).
func (ix *Index) DirectSupertypes(ty *Type) (supers []*Type, closed bool) {
	closed = true
	file := ix.fileOf(ty)
	if file == nil {
		return nil, false
	}
	for _, ref := range ty.Supertypes {
		t, res := ix.ResolveTypeName(file, ty, ref.Base)
		if res != ResolvedType {
			closed = false
			continue
		}
		supers = append(supers, t)
	}
	return supers, closed
}

// chain walks the supertype graph receiver-outward in breadth-first levels,
// cycle-safe, and reports whether the WHOLE reachable chain is closed.
func (ix *Index) chain(receiver *Type) (levels [][]*Type, closed bool) {
	closed = true
	seen := map[*Type]bool{receiver: true}
	level := []*Type{receiver}
	for len(level) > 0 {
		levels = append(levels, level)
		var next []*Type
		for _, t := range level {
			supers, ok := ix.DirectSupertypes(t)
			if !ok {
				closed = false
			}
			for _, s := range supers {
				if seen[s] {
					continue // diamond or cycle: visit once
				}
				seen[s] = true
				next = append(next, s)
			}
		}
		level = next
	}
	return levels, closed
}

// callableForms: the member forms a call site can bind to.
func callableForm(form string) bool {
	return form == MemberMethod || form == MemberFunction
}

// valueForms: the member forms a field/property access can bind to.
func valueForm(form string) bool {
	return form == MemberField || form == MemberProperty || form == MemberEnumConst
}

// callableSig is the parameter signature used for the override-vs-overload
// distinction. Each parameter keys on its RESOLVED type identity: an intra-repo
// type contributes its FQN (so two params both spelled `Foo` that resolve to
// `q.Foo` and `r.Foo` produce DIFFERENT signatures — JVMSOUND-002), while a
// primitive or external type (which does not resolve intra-repo) contributes
// its written text with a leading marker byte, so it can never collide with a
// resolved FQN yet a genuine `m(int)`/`m(int)` override still matches itself.
//
// ARRAY DIMENSIONALITY is carried in the key, by counting the trailing `[]`
// groups of the WRITTEN type text (the same `arrayDims` rule the rendering
// harness already uses). It is what fixes JVMSOUND-004: the previous key used
// only `TypeRef.Base`, which has arrays erased by construction (see table.go),
// so `apply(Thing)` and `apply(Thing[])` produced the IDENTICAL signature, the
// overload set was misread as an override pair, and the most-derived member
// won where `AmbiguousMember` was required. Same shape closes the higher-
// dimensionality instance (`apply(int[])` vs `apply(int[][])`) found in
// SW-172 round 1 — Base erases arrays entirely, so the rule is one-count-of-
// trailing-`[]`-groups, not a present/absent bit.
//
// KNOWN RESIDUAL, stated so it is not overread: two DIFFERENT external types
// that share a simple name (e.g. `a.Foo` and `b.Foo`, neither declared in the
// repo) both key on `?Foo` and would collapse. This is the same simple-name
// collision class the heuristic linker already carries, and it can only affect
// the override/overload distinction of methods whose parameters are both
// external and same-named — much narrower than the intra-repo case this fixes.
func (ix *Index) callableSig(m *Member, declaring *Type) string {
	file := ix.fileOf(declaring)
	var b strings.Builder
	for i, p := range m.Params {
		if i > 0 {
			b.WriteByte(',')
		}
		dims := arrayDims(p.Type.Raw)
		if file != nil {
			if rt, res := ix.ResolveTypeName(file, declaring, p.Type.Base); res == ResolvedType && rt != nil {
				b.WriteString(rt.FQN)
				b.WriteString(dims)
				continue
			}
		}
		b.WriteByte('?')
		b.WriteString(p.Type.Base)
		b.WriteString(dims)
	}
	return b.String()
}

// arrayDims counts the trailing `[]` groups of a written type text and
// returns them as a string (`""`, `"[]"`, `"[][]"`, …). It is the same rule
// the rendering harness uses (binder.go::renderParam); keeping them in
// lockstep is what makes a signature dimension match the descriptor a real
// javac compile produces, so an `m(T[])` and `m(T)` overload pair cannot
// collapse to one signature.
//
// Undercount-only: `int x` (no brackets) is 0; `int[] x` and `int xs[]` both
// count the brackets on the written type text, and the C-style declarator's
// brackets sit on the declarator — so a `m(T)` rendered with the bracket on
// the parameter would undercount, never overcount, and an overload pair can
// only ever see DISTINCT keys, never a false collapse. The loop requires a
// `[]` GROUP (open + close) and walks the written text backward, stripping
// each trailing group in turn.
func arrayDims(raw string) string {
	if raw == "" {
		return ""
	}
	n := 0
	for {
		// trailing group must close before it can open.
		if !strings.HasSuffix(raw, "]") {
			break
		}
		i := strings.LastIndex(raw, "[")
		if i < 0 {
			break
		}
		n++
		raw = raw[:i]
		if raw == "" {
			break
		}
	}
	if n == 0 {
		return ""
	}
	return strings.Repeat("[]", n)
}

// valueClassBridgeName strips kotlinc's VALUE-CLASS name mangling from a
// method name and returns the bridge name, or "" if `name` does not look
// value-class-mangled. The mangling shape is `<plain>-<hash>` where `<hash>`
// is a base-64-ish stable hash kotlinc computes over the inline-class
// parameters' types. The compiler emits a BRIDGE function under the plain
// name (taking the boxed inline-class parameter) AND a REAL function under
// the mangled name (taking the unwrapped underlying type). Source calls
// resolve to the mangled name, so without recognition the binder answers the
// bridge name and the real-body call is unbindable.
//
// kotlinc's hash alphabet includes `-` itself, so the rule splits on the
// FIRST `-` (not the last) — the bridge name is everything before the
// first dash, and the hash is everything after. Plain names like `foo-bar`
// (which the user CAN legally declare in backticks) do not match because
// the part after the first `-` must be at least 4 base-64 characters.
//
// KNOWN RESIDUAL: the hash is computed over the parameter types' mangled
// form, so any two functions that take inline-class parameters of the same
// boxed types at the same arity collide on the SAME mangled suffix and the
// binder must look both up. The lookup below already iterates the closed
// chain in BFS order, so the first-found rule resolves any such collision
// the same way the bytecode would. Frequency is bounded by inline-class
// parameter shapes and is named rather than measured here.
func valueClassBridgeName(name string) string {
	i := strings.Index(name, "-")
	if i <= 0 || i >= len(name)-1 {
		return ""
	}
	// Suffix must be at least 4 base-64 chars; the base-64 alphabet kotlinc
	// uses includes letters, digits, `-`, `+`, `_`.
	suf := name[i+1:]
	if len(suf) < 4 {
		return ""
	}
	for j := 0; j < len(suf); j++ {
		c := suf[j]
		if !isBase64Char(c) {
			return ""
		}
	}
	// Prefix must be a valid Kotlin identifier (a name the user could have
	// declared, OR a name the compiler minted).
	pre := name[:i]
	if pre == "" || !isIdentStart(pre) {
		return ""
	}
	for j := 0; j < len(pre); j++ {
		if !isIdentCont(pre[j]) {
			return ""
		}
	}
	return pre
}

func isBase64Char(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '-' || c == '+' || c == '_'
}

func isIdentStart(s string) bool {
	if s == "" {
		return false
	}
	c := s[0]
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
}

func isIdentCont(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
}

// LookupCallableValueClassAware binds receiver.name(arity), recognising
// kotlinc's value-class name mangling (JVMHARN-001) as a secondary target.
// If the lookup by `name` does not bind, the bridge name (the prefix before
// `-<hash>`) is tried; if THAT binds, the result is reported under the
// ORIGINAL name (the call site's text) so graphi's emitted edge matches
// what the source declares. The bridge name itself is never written to the
// TypedSite.
//
// This is the JVMHARN-001 fix: 22 of kotlinx.serialization's 27 by-name
// counterexamples (and similar shapes on any Kotlin code with inline classes)
// were the oracle accusing correct code — graphi answers what `foo` declares,
// javac compiles the call to `foo-<hash>`. The two are the same binding
// target, and only the body walker, with the call-site text in hand, can
// rejoin them.
func (ix *Index) LookupCallableValueClassAware(receiver *Type, name string, arity int) LookupResult {
	res := ix.LookupCallable(receiver, name, arity)
	if res.Outcome == BoundMember || res.Outcome == AmbiguousMember {
		return res
	}
	if receiver == nil {
		return res
	}
	bridge := valueClassBridgeName(name)
	if bridge == "" {
		return res
	}
	return ix.LookupCallable(receiver, bridge, arity)
}

// elasticMember reports whether any parameter is variadic or defaulted — either
// lets the member satisfy a call at arities other than its declared count, so
// (name, arity) stops being a reliable binding key (JVMSOUND-001).
func elasticMember(m *Member) bool {
	for i := range m.Params {
		if m.Params[i].Variadic || m.Params[i].HasDefault {
			return true
		}
	}
	return false
}

// LookupCallable binds receiver.name(arity) under the rule above.
func (ix *Index) LookupCallable(receiver *Type, name string, arity int) LookupResult {
	return ix.memberLookup(receiver,
		func(m *Member) bool {
			return callableForm(m.Form) && m.Name == name && len(m.Params) == arity
		},
		func(m *Member) bool {
			// A same-name callable with elastic arity forfeits ALL bindings of
			// this name, at every arity — it could be javac's real target.
			return callableForm(m.Form) && m.Name == name && elasticMember(m)
		},
		true)
}

// LookupValue binds receiver.name for field/property access.
func (ix *Index) LookupValue(receiver *Type, name string) LookupResult {
	return ix.memberLookup(receiver, func(m *Member) bool {
		return valueForm(m.Form) && m.Name == name
	}, nil, false)
}

// memberLookup implements the shared walk. checkSig applies the
// override-vs-overload signature rule (callables); fields skip it — same-name
// fields are hiding by construction and the most derived wins. forfeit (may be
// nil) marks any member in the closed chain whose mere presence makes the whole
// (name)-binding unprovable — the elastic-arity guard (JVMSOUND-001).
func (ix *Index) memberLookup(receiver *Type, match func(*Member) bool, forfeit func(*Member) bool, checkSig bool) LookupResult {
	if receiver == nil {
		return LookupResult{Outcome: OpenChain}
	}
	levels, closed := ix.chain(receiver)
	if !closed {
		// Strict forfeit — see the package comment. Checked BEFORE any match:
		// an open chain taints even a receiver-level hit.
		return LookupResult{Outcome: OpenChain}
	}
	var (
		first    LookupResult
		firstSig string
		n        int
		elastic  bool
	)
	for _, level := range levels {
		for _, t := range level {
			for mi := range t.Members {
				m := &t.Members[mi]
				if forfeit != nil && forfeit(m) {
					elastic = true
				}
				if !match(m) {
					continue
				}
				n++
				if n == 1 {
					first = LookupResult{Declaring: t, Member: m, Outcome: BoundMember}
					firstSig = ix.callableSig(m, t)
					continue
				}
				if checkSig && ix.callableSig(m, t) != firstSig {
					// Differing same-arity signatures: an overload set a
					// declared-type binder must not rank.
					return LookupResult{Outcome: AmbiguousMember}
				}
				// Identical signature (override/hiding): the first-found,
				// most-derived binding stands.
			}
		}
	}
	if elastic {
		// A variadic/defaulted same-name callable exists: (name, arity) cannot
		// be trusted to be unique, so forfeit even a lone arity match rather
		// than risk binding a fixed overload where javac binds the elastic one.
		return LookupResult{Outcome: AmbiguousMember}
	}
	if n == 0 {
		return LookupResult{Outcome: NotFoundMember}
	}
	return first
}

// fileOf finds a tabled type's declaring file.
func (ix *Index) fileOf(ty *Type) *File {
	if ix.filesByPath == nil {
		ix.filesByPath = make(map[string]*File, len(ix.table.Files))
		for i := range ix.table.Files {
			ix.filesByPath[ix.table.Files[i].Path] = &ix.table.Files[i]
		}
	}
	return ix.filesByPath[ty.File]
}
