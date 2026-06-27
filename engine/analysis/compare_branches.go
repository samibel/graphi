package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// CompareBranchesAnalyzerName is the dispatch key for the SW-107 graph-level
// branch comparator (EP-018 story 3/4). It receives TWO already-built, read-only
// graph states (base, head) as Params.CompareBase / Params.CompareHead —
// materialized by the existing indexer / core/graphstore snapshot path ABOVE the
// surface boundary — and performs a PURE LOCAL node/edge set-diff keyed by the
// canonical model.NodeId. It NEVER resolves a git ref, reads a git tree, or opens
// a socket (zero engine egress). The diff is structural (entities/symbols/contracts
// added/removed/changed, edges added/removed, entities moved across files), keyed
// by STABLE graph identity rather than line ranges, so it survives full-vs-
// incremental re-indexing byte-identically.
const CompareBranchesAnalyzerName = "compare-branches"

// BranchDiffSchemaVersion versions the BranchDiffReport JSON shape.
const BranchDiffSchemaVersion = 1

// BranchDiffAnalyzerVersion identifies the diff LOGIC version, echoed in the
// report so a stored/audited diff ties to the algorithm that produced it.
const BranchDiffAnalyzerVersion = "compare-branches/1"

// Change kinds for a `changed` node delta — a stable, enumerated, versioned
// vocabulary. A node present (same NodeId) on BOTH sides whose attributes differ
// is classified by which attribute changed. Consumers switch on these rather than
// string-parse.
const (
	// ChangeSignatureContract: a contract-bearing node (exported function/method or
	// a type/class/interface/struct/enum) whose outgoing dependency fingerprint
	// (its calls/references/defines — its behavioral/contract surface) changed.
	// This is the graph-level "signature / contract change" the AC requires.
	ChangeSignatureContract = "signature_contract_change"
	// ChangeDependencies: a non-contract node whose outgoing dependency fingerprint
	// changed (its behavior changed but it is not a contract surface).
	ChangeDependencies = "dependencies_changed"
	// ChangeLocation: only the non-identity location attributes (line/column) of a
	// node moved within its file; identity and dependency fingerprint are unchanged.
	ChangeLocation = "location"
)

// NodeDelta is one added/removed entity, named by its canonical identity. Line is
// the non-identity location attribute, carried for human orientation only (never
// part of identity or ordering).
type NodeDelta struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	QualifiedName string `json:"qualified_name"`
	Path          string `json:"path"`
	Line          int    `json:"line,omitempty"`
}

// ChangedDelta is one entity present (same NodeId) on both sides whose attributes
// differ, classified by ChangeKind. The signature_contract_change classification
// is the AC-mandated detected contract change.
type ChangedDelta struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	QualifiedName string `json:"qualified_name"`
	Path          string `json:"path"`
	ChangeKind    string `json:"change_kind"`
}

// MovedDelta is an entity moved across files: the SAME secondary symbol identity
// (kind + qualified name, path-independent) under a DIFFERENT canonical NodeId in
// base vs head (because the normalized path participates in the NodeId preimage).
// Correlated from an added+removed pair by the deterministic secondary pass.
type MovedDelta struct {
	Kind          string `json:"kind"`
	QualifiedName string `json:"qualified_name"`
	FromID        string `json:"from_id"`
	ToID          string `json:"to_id"`
	FromPath      string `json:"from_path"`
	ToPath        string `json:"to_path"`
}

// EdgeDelta is one added/removed edge, named by its canonical EdgeId plus its
// endpoints and kind (the edge's identity tuple).
type EdgeDelta struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

// BranchDiffReport is the full versioned payload emitted over every surface.
// Deltas are grouped by kind (fixed field order) and each group is ordered by
// canonical identity, so an identical pair of graph states yields byte-identical
// output (determinism / full-vs-incremental parity).
type BranchDiffReport struct {
	SchemaVersion         int            `json:"schema_version"`
	AnalyzerVersion       string         `json:"analyzer_version"`
	IdentitySchemaVersion uint32         `json:"identity_schema_version"`
	Outcome               string         `json:"outcome"` // found | empty
	Added                 []NodeDelta    `json:"added"`
	Removed               []NodeDelta    `json:"removed"`
	Changed               []ChangedDelta `json:"changed"`
	Moved                 []MovedDelta   `json:"moved"`
	EdgesAdded            []EdgeDelta    `json:"edges_added"`
	EdgesRemoved          []EdgeDelta    `json:"edges_removed"`
}

// compareBranchesAnalyzer is the registered graph-level branch comparator. It is
// stateless per call and performs ZERO outbound network activity (pure reads over
// the two read-only graph states handed in as Params).
type compareBranchesAnalyzer struct{}

func newCompareBranchesAnalyzer() compareBranchesAnalyzer { return compareBranchesAnalyzer{} }

func (compareBranchesAnalyzer) Name() string { return CompareBranchesAnalyzerName }

// Analyze diffs the two graph states (Params.CompareBase, Params.CompareHead) and
// returns a versioned BranchDiffReport on the generic Analysis envelope. A nil
// state is treated as an empty graph (degenerate input → well-defined result, no
// error). It never resolves a ref and never egresses.
func (a compareBranchesAnalyzer) Analyze(ctx context.Context, _ query.Reader, p Params) (Analysis, error) {
	report, err := a.diff(ctx, p.CompareBase, p.CompareHead)
	if err != nil {
		return Analysis{}, err
	}
	return Analysis{
		Analyzer:   CompareBranchesAnalyzerName,
		Outcome:    query.Outcome(report.Outcome),
		Symbol:     p.Symbol,
		BranchDiff: &report,
	}, nil
}

// branchState is one side's indexed node/edge sets plus the per-node outgoing
// dependency fingerprint used for signature/contract-change classification.
type branchState struct {
	nodes map[model.NodeId]query.ResultNode
	edges map[model.EdgeId]query.ResultEdge
	fp    map[model.NodeId][]string // node id → sorted outgoing dependency fingerprint
}

// loadState indexes one read-only graph state. A nil reader yields empty sets (a
// degenerate side, e.g. an unknown ref materialized to nothing).
func loadState(ctx context.Context, r query.Reader) (branchState, error) {
	st := branchState{
		nodes: map[model.NodeId]query.ResultNode{},
		edges: map[model.EdgeId]query.ResultEdge{},
		fp:    map[model.NodeId][]string{},
	}
	if r == nil {
		return st, nil
	}
	nodes, err := r.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return branchState{}, err
	}
	for _, n := range nodes {
		st.nodes[n.ID()] = nodeToResult(n)
	}
	edges, err := r.Edges(ctx, graphstore.Query{})
	if err != nil {
		return branchState{}, err
	}
	want := make(map[string]struct{}, len(dependencyKinds))
	for _, k := range dependencyKinds {
		want[k] = struct{}{}
	}
	for _, e := range edges {
		st.edges[e.ID()] = edgeToResult(e)
		if _, ok := want[e.Kind()]; ok {
			st.fp[e.From()] = append(st.fp[e.From()], e.Kind()+"\x1f"+string(e.To()))
		}
	}
	for id := range st.fp {
		sort.Strings(st.fp[id])
	}
	return st, nil
}

// diff is the core, split out for direct unit-testing.
func (a compareBranchesAnalyzer) diff(ctx context.Context, baseR, headR query.Reader) (BranchDiffReport, error) {
	report := BranchDiffReport{
		SchemaVersion:         BranchDiffSchemaVersion,
		AnalyzerVersion:       BranchDiffAnalyzerVersion,
		IdentitySchemaVersion: model.IdentitySchemaVersion,
		Outcome:               string(query.OutcomeEmpty),
		Added:                 []NodeDelta{},
		Removed:               []NodeDelta{},
		Changed:               []ChangedDelta{},
		Moved:                 []MovedDelta{},
		EdgesAdded:            []EdgeDelta{},
		EdgesRemoved:          []EdgeDelta{},
	}

	base, err := loadState(ctx, baseR)
	if err != nil {
		return BranchDiffReport{}, err
	}
	head, err := loadState(ctx, headR)
	if err != nil {
		return BranchDiffReport{}, err
	}

	// (1) Node set-diff keyed by canonical NodeId.
	var added, removed []NodeDelta
	var changed []ChangedDelta
	for id, hn := range head.nodes {
		bn, ok := base.nodes[id]
		if !ok {
			added = append(added, nodeDelta(id, hn))
			continue
		}
		// Same NodeId on both sides → identity fields (kind/qualified-name/path)
		// are identical; classify the change by which non-identity attribute or
		// dependency fingerprint differs.
		if ck, ok := classifyChange(base, head, id, hn); ok {
			changed = append(changed, ChangedDelta{
				ID:            string(id),
				Kind:          hn.Kind,
				QualifiedName: hn.QualifiedName,
				Path:          hn.SourcePath,
				ChangeKind:    ck,
			})
		}
		_ = bn
	}
	for id, bn := range base.nodes {
		if _, ok := head.nodes[id]; !ok {
			removed = append(removed, nodeDelta(id, bn))
		}
	}

	// (2) Move correlation (deterministic secondary pass): an added+removed pair
	// sharing a stable secondary symbol identity (kind + qualified name,
	// path-independent) is a single `moved` delta, because the normalized path
	// participates in the NodeId preimage. Primary key stays NodeId; this only
	// re-labels add/remove pairs that are really moves.
	moved, added, removed := correlateMoves(added, removed)

	// (3) Edge set-diff keyed by canonical EdgeId.
	var edgesAdded, edgesRemoved []EdgeDelta
	for id, he := range head.edges {
		if _, ok := base.edges[id]; !ok {
			edgesAdded = append(edgesAdded, edgeDelta(id, he))
		}
	}
	for id, be := range base.edges {
		if _, ok := head.edges[id]; !ok {
			edgesRemoved = append(edgesRemoved, edgeDelta(id, be))
		}
	}

	report.Added = added
	report.Removed = removed
	report.Changed = changed
	report.Moved = moved
	report.EdgesAdded = edgesAdded
	report.EdgesRemoved = edgesRemoved
	sortBranchDiff(&report)

	if len(report.Added)+len(report.Removed)+len(report.Changed)+
		len(report.Moved)+len(report.EdgesAdded)+len(report.EdgesRemoved) > 0 {
		report.Outcome = string(query.OutcomeFound)
	}
	return report, nil
}

// classifyChange decides whether a node present on both sides changed, and how.
// Priority: a differing outgoing dependency fingerprint is a behavioral/contract
// change (signature_contract_change for a contract node, else dependencies_changed);
// otherwise a differing line/column is a within-file location change. Returns
// (kind, true) when changed.
func classifyChange(base, head branchState, id model.NodeId, hn query.ResultNode) (string, bool) {
	if !equalStrSlice(base.fp[id], head.fp[id]) {
		if isContractNode(hn) {
			return ChangeSignatureContract, true
		}
		return ChangeDependencies, true
	}
	bn := base.nodes[id]
	if bn.Line != hn.Line || bn.Column != hn.Column {
		return ChangeLocation, true
	}
	return "", false
}

// correlateMoves pairs added+removed nodes sharing the path-independent secondary
// symbol identity (kind + qualified name) into `moved` deltas. The pairing is
// deterministic: within a secondary-identity bucket, removed and added nodes are
// matched in canonical NodeId order up to the smaller count; the surplus stays in
// added/removed. Returns the moved deltas and the surviving added/removed sets.
func correlateMoves(added, removed []NodeDelta) ([]MovedDelta, []NodeDelta, []NodeDelta) {
	secKey := func(d NodeDelta) string { return d.Kind + "\x1f" + d.QualifiedName }

	addBy := map[string][]NodeDelta{}
	for _, d := range added {
		addBy[secKey(d)] = append(addBy[secKey(d)], d)
	}
	remBy := map[string][]NodeDelta{}
	for _, d := range removed {
		remBy[secKey(d)] = append(remBy[secKey(d)], d)
	}

	var moved []MovedDelta
	pairedAdd := map[string]struct{}{}
	pairedRem := map[string]struct{}{}

	keys := make([]string, 0, len(remBy))
	for k := range remBy {
		if _, ok := addBy[k]; ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		rem := append([]NodeDelta(nil), remBy[k]...)
		add := append([]NodeDelta(nil), addBy[k]...)
		// A move requires a genuine path change; if any pair shares the same path it
		// would have the same NodeId (not an add/remove), so all pairs here differ.
		sort.Slice(rem, func(i, j int) bool { return rem[i].ID < rem[j].ID })
		sort.Slice(add, func(i, j int) bool { return add[i].ID < add[j].ID })
		n := len(rem)
		if len(add) < n {
			n = len(add)
		}
		for i := 0; i < n; i++ {
			moved = append(moved, MovedDelta{
				Kind:          rem[i].Kind,
				QualifiedName: rem[i].QualifiedName,
				FromID:        rem[i].ID,
				ToID:          add[i].ID,
				FromPath:      rem[i].Path,
				ToPath:        add[i].Path,
			})
			pairedRem[rem[i].ID] = struct{}{}
			pairedAdd[add[i].ID] = struct{}{}
		}
	}

	survAdd := added[:0:0]
	for _, d := range added {
		if _, ok := pairedAdd[d.ID]; !ok {
			survAdd = append(survAdd, d)
		}
	}
	survRem := removed[:0:0]
	for _, d := range removed {
		if _, ok := pairedRem[d.ID]; !ok {
			survRem = append(survRem, d)
		}
	}
	return moved, survAdd, survRem
}

func nodeDelta(id model.NodeId, n query.ResultNode) NodeDelta {
	return NodeDelta{
		ID:            string(id),
		Kind:          n.Kind,
		QualifiedName: n.QualifiedName,
		Path:          n.SourcePath,
		Line:          n.Line,
	}
}

func edgeDelta(id model.EdgeId, e query.ResultEdge) EdgeDelta {
	return EdgeDelta{
		ID:   string(id),
		From: string(e.From),
		To:   string(e.To),
		Kind: e.Kind,
	}
}

func equalStrSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// sortBranchDiff enforces the canonical per-group total order: node groups by
// canonical NodeId; moves by secondary identity (qualified name, kind) then
// from-id; edge groups by canonical EdgeId. This is the ONLY place branch-diff
// ordering is decided; combined with MarshalBranchDiff it makes the report
// byte-identical regardless of map-iteration order.
func sortBranchDiff(r *BranchDiffReport) {
	sort.Slice(r.Added, func(i, j int) bool { return r.Added[i].ID < r.Added[j].ID })
	sort.Slice(r.Removed, func(i, j int) bool { return r.Removed[i].ID < r.Removed[j].ID })
	sort.Slice(r.Changed, func(i, j int) bool { return r.Changed[i].ID < r.Changed[j].ID })
	sort.Slice(r.Moved, func(i, j int) bool {
		a, b := r.Moved[i], r.Moved[j]
		if a.QualifiedName != b.QualifiedName {
			return a.QualifiedName < b.QualifiedName
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.FromID < b.FromID
	})
	sort.Slice(r.EdgesAdded, func(i, j int) bool { return r.EdgesAdded[i].ID < r.EdgesAdded[j].ID })
	sort.Slice(r.EdgesRemoved, func(i, j int) bool { return r.EdgesRemoved[i].ID < r.EdgesRemoved[j].ID })
}

// MarshalBranchDiff is the single canonical serializer for a BranchDiffReport,
// shared by every surface. It re-sorts defensively (per-group total order),
// disables HTML escaping, and trims the trailing newline — byte-for-byte stable
// across runs and surfaces (mirrors MarshalRisk / MarshalTriage / MarshalConflicts).
// Empty slices are materialized (never null) so the shape is stable. No timestamp /
// wall-clock / float / map-iteration leakage.
func MarshalBranchDiff(rep BranchDiffReport) ([]byte, error) {
	out := rep
	out.Added = append([]NodeDelta(nil), rep.Added...)
	out.Removed = append([]NodeDelta(nil), rep.Removed...)
	out.Changed = append([]ChangedDelta(nil), rep.Changed...)
	out.Moved = append([]MovedDelta(nil), rep.Moved...)
	out.EdgesAdded = append([]EdgeDelta(nil), rep.EdgesAdded...)
	out.EdgesRemoved = append([]EdgeDelta(nil), rep.EdgesRemoved...)
	sortBranchDiff(&out)
	if out.Added == nil {
		out.Added = []NodeDelta{}
	}
	if out.Removed == nil {
		out.Removed = []NodeDelta{}
	}
	if out.Changed == nil {
		out.Changed = []ChangedDelta{}
	}
	if out.Moved == nil {
		out.Moved = []MovedDelta{}
	}
	if out.EdgesAdded == nil {
		out.EdgesAdded = []EdgeDelta{}
	}
	if out.EdgesRemoved == nil {
		out.EdgesRemoved = []EdgeDelta{}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, fmt.Errorf("analysis: marshal branch diff report: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
