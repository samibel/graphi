package jvmresolve

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
//	  - every match carries the IDENTICAL erased parameter signature
//	    → an override chain: bind the FIRST-FOUND (most derived) member —
//	      which is javac's static-binding target for the receiver's type;
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

// sigOf is the erased parameter signature used for the override-vs-overload
// distinction.
func sigOf(m *Member) string {
	sig := ""
	for i, p := range m.Params {
		if i > 0 {
			sig += ","
		}
		sig += p.Type.Base
	}
	return sig
}

// LookupCallable binds receiver.name(arity) under the rule above.
func (ix *Index) LookupCallable(receiver *Type, name string, arity int) LookupResult {
	return ix.memberLookup(receiver, func(m *Member) bool {
		return callableForm(m.Form) && m.Name == name && len(m.Params) == arity
	}, true)
}

// LookupValue binds receiver.name for field/property access.
func (ix *Index) LookupValue(receiver *Type, name string) LookupResult {
	return ix.memberLookup(receiver, func(m *Member) bool {
		return valueForm(m.Form) && m.Name == name
	}, false)
}

// memberLookup implements the shared walk. checkSig applies the
// override-vs-overload signature rule (callables); fields skip it — same-name
// fields are hiding by construction and the most derived wins.
func (ix *Index) memberLookup(receiver *Type, match func(*Member) bool, checkSig bool) LookupResult {
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
	)
	for _, level := range levels {
		for _, t := range level {
			for mi := range t.Members {
				m := &t.Members[mi]
				if !match(m) {
					continue
				}
				n++
				if n == 1 {
					first = LookupResult{Declaring: t, Member: m, Outcome: BoundMember}
					firstSig = sigOf(m)
					continue
				}
				if checkSig && sigOf(m) != firstSig {
					// Differing same-arity signatures: an overload set a
					// declared-type binder must not rank.
					return LookupResult{Outcome: AmbiguousMember}
				}
				// Identical signature (override/hiding): the first-found,
				// most-derived binding stands.
			}
		}
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
