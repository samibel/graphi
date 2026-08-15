package jvmresolve

// Slice 6 (Phase C, first half): confirmed-edge emission. Emit turns Phase
// B's typed sites and the table's nominal interface clauses into
// confirmed-tier edges, under the two structural honesty gates:
//
//	IDENTITY — every endpoint is reconstructed through qn.go's Decl mapping
//	(NodeIDFor). A declaration the extractors mint no node for (nested Java
//	types, enum members, companions, Java constructors, Kotlin
//	primary-constructor properties) yields no id and the intent drops under
//	a named counter.
//
//	COMMITTED — a reconstructed id must exist in the committed node set the
//	ingest pass hands over. An id the store does not hold is DROPPED AND
//	COUNTED (the typeresolve DroppedIntents discipline), never fabricated.
//	This is also the belt for identity drift: qn.go's cross-test is the
//	braces, this check catches anything it ever misses — including the one
//	known table/identity mismatch, Kotlin primary-constructor val/var
//	properties, which the table records as members (they ARE declared
//	properties) but the extractor mints no nodes for.
//
// Edge semantics (ADR 0008 D1): tier confirmed, confidence 1.0, and the
// reason string CARRIES the contract — static binding, runtime dispatch may
// select an override. Kinds are exactly the three the typeresolve pass may
// reconcile (calls / references / implements; engine/ingest typeresolveKind):
//
//	calls       call sites; constructor calls target the TYPE node — the
//	            same shape the heuristic FQN binder gives constructor-style
//	            calls, so confirmed upserts replace the matching heuristic
//	            edge instead of minting a parallel one.
//	references  value sites (field/property reads).
//	implements  nominal: a tabled type's resolved supertype clause whose
//	            target is an INTERFACE. The clause itself is the proof —
//	            the programmer declared it. Class extends class stays the
//	            syntactic extractor's `inherits` (never confirmed here:
//	            engine/ingest's sweep set excludes it).
//
// One edge per logical (from,to,kind), evidence as a sorted union of
// file:line citations, result sorted by EdgeId — byte-determinism matching
// engine/typeresolve's collect-then-construct union.

import (
	"fmt"
	"sort"

	"github.com/samibel/graphi/core/model"
)

// D1 reason strings — constants so the union produces stable bytes.
const (
	reasonCall        = "call target bound by declared-type resolution (static binding; runtime dispatch may select an override)"
	reasonConstructor = "constructor call bound by declared-type resolution"
	reasonReference   = "member reference bound by declared-type resolution"
	reasonImplements  = "interface implementation declared by the supertype clause"
)

// Emission skip counters.
const (
	SkipEmitFromNoNode = "emit_from_no_node" // the site's enclosing member has no node identity
	SkipEmitToNoNode   = "emit_to_no_node"   // the bound target has no node identity
)

// EmitResult is Phase C's outcome over one snapshot.
type EmitResult struct {
	// Edges: confirmed tier, confidence 1.0, sorted by EdgeId.
	Edges []model.Edge
	// DroppedIntents counts intents whose reconstructed endpoint was not in
	// the committed set — never fabricated (one count per use site).
	DroppedIntents int
	// Skips are the named identity gaps (endpoints qn.go maps to no node).
	Skips SkipCounts
}

// Emit builds the confirmed edge set from sites and the table's nominal
// interface clauses, against the committed node set.
func (ix *Index) Emit(sites []TypedSite, committed map[model.NodeId]struct{}) (EmitResult, error) {
	sink := newEdgeSink(committed)
	res := EmitResult{Skips: SkipCounts{}}

	for i := range sites {
		s := &sites[i]
		fromID, ok := ix.siteFromID(s)
		if !ok {
			res.Skips.add(SkipEmitFromNoNode)
			continue
		}
		toID, kind, reason, ok := ix.siteToID(s)
		if !ok {
			res.Skips.add(SkipEmitToNoNode)
			continue
		}
		sink.add(fromID, toID, kind, reason, fmt.Sprintf("%s:%d", s.FromFile, s.Line), &res)
	}

	// Nominal implements: every tabled type's resolved INTERFACE supertypes.
	for fi := range ix.table.Files {
		file := &ix.table.Files[fi]
		for ti := range file.Types {
			ty := &file.Types[ti]
			fromID, ok := typeNodeID(ty)
			if !ok {
				continue // a node-less type (nested Java, companion) claims nothing
			}
			supers, _ := ix.DirectSupertypes(ty)
			for _, s := range supers {
				if s.Form != FormInterface {
					continue
				}
				toID, ok := typeNodeID(s)
				if !ok {
					res.Skips.add(SkipEmitToNoNode)
					continue
				}
				sink.add(fromID, toID, "implements", reasonImplements, fmt.Sprintf("%s:%d", ty.File, ty.Line), &res)
			}
		}
	}

	edges, err := sink.build()
	if err != nil {
		return EmitResult{}, err
	}
	res.Edges = edges
	return res, nil
}

// siteFromID maps a site's enclosing member to its node id. Java
// constructors have no nodes (the extractor collects method_declaration
// only) — their sites drop here under the from counter.
func (ix *Index) siteFromID(s *TypedSite) (model.NodeId, bool) {
	if s.FromType == nil {
		// Kotlin top-level function.
		if s.FromMember == nil || s.FromMember.Form != MemberFunction {
			return "", false
		}
		return NodeIDFor(Decl{
			Form: KotlinFunction, File: s.FromFile, Name: s.FromMember.Name,
			TypeDepth: 0,
		})
	}
	return memberNodeID(s.FromType, s.FromMember)
}

// siteToID maps a site's bound target to (node id, edge kind, reason).
func (ix *Index) siteToID(s *TypedSite) (model.NodeId, string, string, bool) {
	if s.Member != nil && s.Member.Form == MemberConstructor {
		// Constructor call: the TYPE node is the target (see the package
		// comment); Declaring is the constructed type.
		if s.Declaring == nil {
			return "", "", "", false
		}
		id, ok := typeNodeID(s.Declaring)
		return id, "calls", reasonConstructor, ok
	}
	if s.Declaring == nil {
		// Kotlin same-file top-level function target.
		if s.Member == nil || s.Member.Form != MemberFunction {
			return "", "", "", false
		}
		id, ok := NodeIDFor(Decl{
			Form: KotlinFunction, File: s.FromFile, Name: s.Member.Name, TypeDepth: 0,
		})
		return id, "calls", reasonCall, ok
	}
	id, ok := memberNodeID(s.Declaring, s.Member)
	if !ok {
		return "", "", "", false
	}
	if s.Kind == SiteValue {
		return id, "references", reasonReference, true
	}
	return id, "calls", reasonCall, true
}

// memberNodeID reconstructs a member's node id through the qn.go identity
// rules.
func memberNodeID(declaring *Type, m *Member) (model.NodeId, bool) {
	if m == nil {
		return "", false
	}
	depth := len(declaring.Nesting) + 1
	d := Decl{
		File:          declaring.File,
		Name:          m.Name,
		TypeDepth:     depth,
		EnclosingEnum: m.InEnumBody,
	}
	switch declaring.Language {
	case LangJava:
		switch m.Form {
		case MemberMethod:
			d.Form = JavaMethod
		case MemberField:
			if m.ConstantDecl {
				d.Form = JavaConstantField
			} else {
				d.Form = JavaField
				d.StaticFinal = m.Static && m.Final
			}
		default:
			return "", false // constructors, enum constants: no nodes
		}
	case LangKotlin:
		d.EnclosingCompanion = m.InCompanion || declaring.Form == FormCompanion
		switch m.Form {
		case MemberFunction:
			d.Form = KotlinFunction
		case MemberProperty:
			d.Form = KotlinProperty
			d.Const = m.Const
		default:
			return "", false
		}
	default:
		return "", false
	}
	return NodeIDFor(d)
}

// typeNodeID reconstructs a type's node id.
func typeNodeID(ty *Type) (model.NodeId, bool) {
	d := Decl{File: ty.File, Name: ty.Name, TypeDepth: len(ty.Nesting)}
	switch ty.Language {
	case LangJava:
		d.Form = JavaType
	case LangKotlin:
		if ty.Form == FormCompanion {
			return "", false
		}
		d.Form = KotlinType
	default:
		return "", false
	}
	return NodeIDFor(d)
}

// edgeSink is the collect-then-construct union: one logical edge per
// (from,to,kind) with sorted, deduped evidence — the engine/typeresolve
// intentSink shape, including its committed-set gate.
type edgeSink struct {
	committed map[model.NodeId]struct{}
	intents   map[edgeKey]*edgeIntent
}

type edgeKey struct {
	from, to model.NodeId
	kind     string
}

type edgeIntent struct {
	reason   string
	evidence map[string]struct{}
}

func newEdgeSink(committed map[model.NodeId]struct{}) *edgeSink {
	return &edgeSink{committed: committed, intents: map[edgeKey]*edgeIntent{}}
}

// add records one intent; an endpoint outside the committed set drops and
// counts (never fabricate).
func (s *edgeSink) add(from, to model.NodeId, kind, reason, evidence string, res *EmitResult) {
	if _, ok := s.committed[from]; !ok {
		res.DroppedIntents++
		return
	}
	if _, ok := s.committed[to]; !ok {
		res.DroppedIntents++
		return
	}
	key := edgeKey{from: from, to: to, kind: kind}
	in := s.intents[key]
	if in == nil {
		in = &edgeIntent{reason: reason, evidence: map[string]struct{}{}}
		s.intents[key] = in
	}
	in.evidence[evidence] = struct{}{}
}

// build constructs the edges, evidence sorted, result sorted by EdgeId.
func (s *edgeSink) build() ([]model.Edge, error) {
	keys := make([]edgeKey, 0, len(s.intents))
	for k := range s.intents {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		if keys[a].from != keys[b].from {
			return keys[a].from < keys[b].from
		}
		if keys[a].to != keys[b].to {
			return keys[a].to < keys[b].to
		}
		return keys[a].kind < keys[b].kind
	})
	edges := make([]model.Edge, 0, len(keys))
	for _, k := range keys {
		in := s.intents[k]
		ev := make([]string, 0, len(in.evidence))
		for e := range in.evidence {
			ev = append(ev, e)
		}
		sort.Strings(ev)
		e, err := model.NewEdge(k.from, k.to, k.kind, model.TierConfirmed, 1.0, in.reason, ev)
		if err != nil {
			return nil, fmt.Errorf("jvmresolve: emit edge %s->%s (%s): %w", k.from, k.to, k.kind, err)
		}
		edges = append(edges, e)
	}
	sort.Slice(edges, func(a, b int) bool { return edges[a].ID() < edges[b].ID() })
	return edges, nil
}
