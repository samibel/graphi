// Package link is graphi's pure, store-free cross-file / cross-package linker
// pass (SW-050, FU-1). It turns the deferred references the parse leaf RECORDED
// (parse.PendingRef / parse.ImportSpec) into fully-provenanced model.Edge values,
// resolving them against a committed node set via a SymbolIndex.
//
// Layering & purity: link is an engine package that depends only on core/model
// and core/parse. It performs NO I/O — no os, net, os/exec, no graphstore, no
// new module dependency — so ingest owns every PutEdge / transaction concern.
// This keeps the determinism guarantees exhaustively unit-testable here, left of
// the high-blast-radius ingest wiring.
//
// Determinism contract (mirrors model.NewEdge): every logical (from,to,kind)
// edge is constructed exactly ONCE via collect-then-construct — evidence for a
// multi-call-site edge is merged as a sorted union, so the output is identical
// regardless of PendingRef / node ordering and idempotent across repeated Link
// calls. Output is sorted by EdgeId.
//
// Tier honesty (security): the confidence tier is DERIVED from the resolution
// class via tierFor, so an over-confident tier is unrepresentable — same-package
// name-resolved calls are `derived`; every cross-package / selector / recv.Method
// edge is `heuristic`; a linker edge is NEVER `confirmed`. The linker resolves
// only against committed NodeIds and never fabricates a target: unresolved /
// ambiguous references are dropped deterministically and counted.
package link

import (
	"sort"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
)

// Edge kinds the linker emits, matching the canonical query vocabulary.
const (
	edgeCalls      = "calls"
	edgeReferences = "references"
	edgeImports    = "imports"

	// Hierarchy edge kinds (epic EP-011 G2). The Go resolver emits these from
	// embedded interface/struct type PendingRefs; they resolve through the same
	// bare-name / selector paths as references.
	edgeImplements = "implements"
	edgeInherits   = "inherits"
	edgeOverrides  = "overrides"
)

// resolutionClass is the closed set of ways the linker can resolve a reference.
// It is the SOLE input to tierFor, making an over-confident tier unrepresentable.
type resolutionClass int

const (
	// classSamePackage — a bare name resolved within the caller's own directory.
	classSamePackage resolutionClass = iota
	// classSelector — a cross-package / receiver-method selector resolution.
	classSelector
)

// tierFor maps a resolution class to its confidence tier and pinned confidence.
// The constants are the frozen tier→confidence map (derived=0.9, heuristic=0.6);
// they are asserted by a unit test. The linker NEVER returns TierConfirmed.
func tierFor(c resolutionClass) (model.ConfidenceTier, float64) {
	switch c {
	case classSamePackage:
		return model.TierDerived, 0.9
	default:
		return model.TierHeuristic, 0.6
	}
}

// Stats are the linker's observability counters for one Link pass.
type Stats struct {
	// ResolvedDerived counts same-package name-resolved edges.
	ResolvedDerived int
	// ResolvedHeuristic counts cross-package/selector/recv.Method edges.
	ResolvedHeuristic int
	// Skipped counts references that resolved to nothing (stdlib/3rd-party/local).
	Skipped int
	// Ambiguous counts references skipped because resolution was ambiguous.
	Ambiguous int
	// ResolvedExternal counts references materialized against an interned
	// `external` node (WP-03): a cross-package call/reference whose target lives
	// outside the repo (stdlib / 3rd-party). These are heuristic-tier edges to a
	// second-class, terminal node — distinct from ResolvedHeuristic so external
	// materialization is observable in the ingest summary.
	ResolvedExternal int
}

// Resolver is the language-neutral registry seam (Open/Closed): FU-2 adds a new
// language by registering another Resolver, never by editing existing code. A
// Resolver turns one file's pending refs + imports into resolved edge intents
// against the shared SymbolIndex. Go is the first (and, for FU-1, only)
// registration.
type Resolver interface {
	// Language is the canonical language identifier this resolver handles.
	Language() string
	// Resolve turns the pending refs/imports of one file (whose owning symbols
	// live at fromDir) into resolved intents against idx. It is pure and
	// store-free; it never constructs an Edge (the Linker does that once).
	Resolve(in FileRefs, idx *SymbolIndex, st *Stats) []intent
}

// FileRefs is one file's deferred data plus the directory its symbols live in
// (the same-package resolution scope, Open Q1 directory-keyed).
type FileRefs struct {
	// SourcePath is the file's normalized repo-relative POSIX path.
	SourcePath string
	// Dir is the file's directory (same-package key); "" for the repo root.
	Dir string
	// Language is the file's canonical language identifier (e.g. "go", "python",
	// "typescript"). FU-5: ingest groups FileRefs by Language and dispatches Link
	// once per language so each registered Resolver sees only its own files. The
	// zero value "" is the FU-1 Go-only behaviour (callers passing the language
	// explicitly to Link are unaffected — Link selects the resolver by its argument,
	// not by this field).
	Language string
	// Pending are the file's deferred references.
	Pending []parse.PendingRef
	// Imports are the file's import declarations (alias → path).
	Imports []parse.ImportSpec
}

// intent is a resolved-but-not-yet-constructed edge: a logical (from,to,kind)
// plus its provenance class and one evidence string. The Linker collects all
// intents, merges evidence per logical edge as a sorted union, and constructs
// each edge exactly once.
//
// INVARIANT (batch-safety): a resolver MUST set `from` to a symbol OWNED by the
// file currently being resolved (`idx.sameDir(in.Dir, lastSeg(p.FromQN))`, where
// FromQN is the ref's own enclosing declaration). Ingest may split a language's
// files into sub-batches for link progress (WP-02) and merge each batch's edges
// independently; byte-parity with a single pass holds ONLY because every logical
// edge's intents share one `from` and therefore live in one batch. A resolver that
// synthesized `from` from a cross-file name could split an edge's evidence union
// across batches and diverge — do not.
type intent struct {
	from     model.NodeId
	to       model.NodeId
	kind     string
	class    resolutionClass
	reason   string
	evidence string
	// toExternalQN, when non-empty, means the resolver wants the edge's target to
	// be an INTERNED external node (WP-03) keyed by this qualified name rather than
	// a committed NodeId. `to` is left zero; construct() interns the node
	// (model.NewNode(KindExternal, toExternalQN, "", 0, 0)), dedups by NodeId so one
	// node per unique QN, and uses its id as the edge target. The class is always
	// classSelector (heuristic tier) for these — an external edge is never derived
	// or confirmed.
	toExternalQN string
}

// Linker resolves pending references into provenanced edges. It holds the
// registered resolvers; it is safe for concurrent construction but Link itself
// is single-threaded and deterministic.
type Linker struct {
	resolvers map[string]Resolver
}

// New constructs a Linker with the default resolvers registered. FU-1 ships Go;
// FU-5 adds one per-language resolver per tier-1 grammar over the same registry
// (Open/Closed) — a new language is a new Register call here, never an edit to an
// existing resolver.
func New() *Linker {
	l := &Linker{resolvers: map[string]Resolver{}}
	l.Register(goResolver{})
	// FU-5 Slice 1 — TypeScript family (one impl, three language ids).
	l.Register(tsResolver{"typescript"})
	l.Register(tsResolver{"tsx"})
	l.Register(tsResolver{"javascript"})
	// FU-5 Slice 2 — Python.
	l.Register(pyResolver{})
	// FU-5 Slice 3 — Rust.
	l.Register(rustResolver{})
	// FU-5 Slice 4 — JVM/CLR FQN family.
	l.Register(javaResolver{})
	l.Register(kotlinResolver{})
	l.Register(csharpResolver{})
	// FU-5 Slice 5 — C / C++ (#include translation units).
	l.Register(cResolver{})
	l.Register(cppResolver{})
	// FU-5 Slice 6 — require/include script family.
	l.Register(rubyResolver{})
	l.Register(phpResolver{})
	l.Register(luaResolver{})
	// FU-5 Slice 7 — Bash/Shell + SQL.
	l.Register(bashResolver{})
	l.Register(sqlResolver{})
	return l
}

// Register adds a resolver under its language. Later registrations override an
// earlier one for the same language (open/closed extension point).
func (l *Linker) Register(r Resolver) { l.resolvers[r.Language()] = r }

// Link resolves the pending refs of the supplied files against idx and returns
// the resulting edges, sorted by EdgeId, plus observability stats. It is pure,
// deterministic, idempotent, and order-independent: shuffling files or their
// pending refs yields a byte-identical edge set (incl. sorted-union evidence).
//
// language selects the resolver; for FU-1 it is always "go". A reference that
// resolves to nothing (stdlib/3rd-party/local var) is dropped and counted, never
// turned into a phantom edge.
func (l *Linker) Link(language string, files []FileRefs, idx *SymbolIndex) ([]model.Node, []model.Edge, Stats, error) {
	var st Stats
	r, ok := l.resolvers[language]
	if !ok {
		// No resolver ⇒ nothing to link (not an error); FU-2 registers more.
		return nil, nil, st, nil
	}
	if idx == nil {
		return nil, nil, st, nil
	}

	// Collect intents from every file. Order does not matter: we group + sort.
	var intents []intent
	// Process files in a deterministic order for stable Skipped/Ambiguous counts.
	ordered := append([]FileRefs(nil), files...)
	sort.Slice(ordered, func(a, b int) bool { return ordered[a].SourcePath < ordered[b].SourcePath })
	for _, f := range ordered {
		intents = append(intents, r.Resolve(f, idx, &st)...)
	}

	nodes, edges, err := construct(intents)
	if err != nil {
		return nil, nil, st, err
	}
	return nodes, edges, st, nil
}

// edgeKey is the logical identity of an edge for collect-then-construct merging.
type edgeKey struct {
	from model.NodeId
	to   model.NodeId
	kind string
}

// construct merges intents by logical (from,to,kind), unions their evidence
// (sorted, deduped) and reasons, constructs each edge exactly once via
// model.NewEdge, and returns the minted external nodes plus the edges, both
// sorted by content id. Constructing once with a fixed tier→confidence map is
// what makes the output byte-identical regardless of input order and idempotent
// across calls.
//
// External-target intents (in.toExternalQN != "") are resolved FIRST: each unique
// QN is interned into a single external node (model.NewNode(KindExternal, qn, "",
// 0, 0)) via extByQN, and the intent's edge target becomes that node's id. The
// interning is deduped by NodeId so one node is minted per unique QN no matter how
// many callsites/files name it, and the returned node set is sorted by NodeId — so
// identical input yields a byte-identical node+edge set, order-independent and
// idempotent (a second Link over the same committed graph re-mints the same nodes
// with the same ids, which upsert cleanly).
func construct(intents []intent) ([]model.Node, []model.Edge, error) {
	// Intern external target nodes and rewrite those intents' `to` to the node id.
	extByQN := map[string]model.Node{}
	resolved := make([]intent, len(intents))
	copy(resolved, intents)
	for i := range resolved {
		qn := resolved[i].toExternalQN
		if qn == "" {
			continue
		}
		n, ok := extByQN[qn]
		if !ok {
			var err error
			n, err = model.NewNode(parse.KindExternal, qn, "", 0, 0)
			if err != nil {
				return nil, nil, err
			}
			extByQN[qn] = n
		}
		resolved[i].to = n.ID()
	}

	type agg struct {
		class    resolutionClass
		reasons  map[string]struct{}
		evidence map[string]struct{}
	}
	groups := map[edgeKey]*agg{}
	var order []edgeKey
	for _, in := range resolved {
		k := edgeKey{from: in.from, to: in.to, kind: in.kind}
		g := groups[k]
		if g == nil {
			g = &agg{class: in.class, reasons: map[string]struct{}{}, evidence: map[string]struct{}{}}
			groups[k] = g
			order = append(order, k)
		}
		// A same-package (derived) resolution is the stronger claim; keep it if
		// any intent for this logical edge resolved same-package.
		if in.class == classSamePackage {
			g.class = classSamePackage
		}
		if in.reason != "" {
			g.reasons[in.reason] = struct{}{}
		}
		if in.evidence != "" {
			g.evidence[in.evidence] = struct{}{}
		}
	}

	edges := make([]model.Edge, 0, len(order))
	for _, k := range order {
		g := groups[k]
		tier, conf := tierFor(g.class)
		evidence := sortedKeys(g.evidence)
		reason := joinSorted(g.reasons)
		e, err := model.NewEdge(k.from, k.to, k.kind, tier, conf, reason, evidence)
		if err != nil {
			return nil, nil, err
		}
		edges = append(edges, e)
	}
	sort.Slice(edges, func(a, b int) bool { return edges[a].ID() < edges[b].ID() })

	nodes := make([]model.Node, 0, len(extByQN))
	for _, n := range extByQN {
		nodes = append(nodes, n)
	}
	sort.Slice(nodes, func(a, b int) bool { return nodes[a].ID() < nodes[b].ID() })
	return nodes, edges, nil
}

// sortedKeys returns the map keys as a sorted, deduped slice (the sorted-union
// evidence merge).
func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// joinSorted joins the reason set deterministically; a single reason is returned
// verbatim so the common case reads naturally.
func joinSorted(m map[string]struct{}) string {
	keys := sortedKeys(m)
	if len(keys) == 0 {
		return "cross-file reference resolved by the linker"
	}
	out := keys[0]
	for _, k := range keys[1:] {
		out += "; " + k
	}
	return out
}
