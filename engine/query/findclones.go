package query

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
)

// OpFindClones is the canonical name of the clone-detection query. Like
// search_ast and the lexical search, find_clones is a WHOLE-GRAPH query — its
// input is a configuration, not a symbol id — so it is NOT a member of the
// symbol-input Operations list and is NOT routed through Dispatch; the surfaces
// advertise it as a singleton (the format=clones selector lands in SW-085).
const OpFindClones = "find_clones"

// Clone types in the v1 envelope (Type-1/2/3 mapped to graphi's vocabulary):
//
//	exact      — identical structural fingerprint (same outbound targets by name)
//	renamed    — identical fingerprint SHAPE (same target kinds) but differing names
//	structural — fingerprint shape similarity (Jaccard) >= Threshold, partial overlap
const (
	CloneTypeExact      = "exact"
	CloneTypeRenamed    = "renamed"
	CloneTypeStructural = "structural"
)

// CloneConfig is the tunable contract for find_clones. It follows the in-engine
// value-struct pattern used by engine/analysis/pdg.DefaultConfig() (graphi has no
// graphi.yaml today; the surface-level clones.* config wiring lands in SW-085).
type CloneConfig struct {
	// Threshold is the minimum Jaccard similarity (over the shape token set) for a
	// pair to be grouped as a `structural` clone. Range (0,1]. Default 0.8.
	Threshold float64 `json:"threshold"`
	// MaxGroups bounds the number of groups emitted so a pathological repo cannot
	// blow the envelope. When more groups exist, the canonical prefix is returned
	// and Truncated is set. Default 1000. 0 means unbounded.
	MaxGroups int `json:"max_groups"`
	// CloneKinds restricts candidate nodes to these kinds. Default [function, method].
	CloneKinds []string `json:"clone_kinds"`
	// MinEdges is the minimum number of outbound (calls+references) edges a node
	// must have to be a clone candidate — fragments below this are too trivial to
	// be meaningful clones (and would otherwise all collapse together). Default 1.
	MinEdges int `json:"min_edges"`
}

// DefaultCloneConfig returns the documented defaults. Power users override
// individual fields; an empty/zero CloneConfig passed to FindClones is normalized
// to these defaults so callers never have to fully specify it.
func DefaultCloneConfig() CloneConfig {
	return CloneConfig{
		Threshold:  0.8,
		MaxGroups:  1000,
		CloneKinds: []string{"function", "method"},
		MinEdges:   1,
	}
}

func (c CloneConfig) normalized() CloneConfig {
	if c.Threshold <= 0 || c.Threshold > 1 {
		c.Threshold = 0.8
	}
	if c.MaxGroups < 0 {
		c.MaxGroups = 0
	}
	if len(c.CloneKinds) == 0 {
		c.CloneKinds = []string{"function", "method"}
	}
	if c.MinEdges < 0 {
		c.MinEdges = 0
	}
	return c
}

// CloneMember identifies one fragment in a clone group. EndLine equals Line in
// v1: the AST node table stores only a declaration line, not a span — populating
// EndLine with the node line is the honest best-effort until span data is added.
type CloneMember struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	EndLine int    `json:"end_line"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
}

// CloneGroup is one set of structurally similar fragments. RenamedIdentifiers is
// populated only for `renamed` groups: the sorted set of outbound target names
// that VARY across members (the identifiers that were renamed).
type CloneGroup struct {
	ID                 string        `json:"id"`
	Type               string        `json:"type"`
	Members            []CloneMember `json:"members"`
	Size               int           `json:"size"`
	RenamedIdentifiers []string      `json:"renamed_identifiers,omitempty"`
}

// CloneResult is the typed find_clones envelope. Groups is always a non-nil slice
// (typed-empty is `{"groups":[],"truncated":false}`, never null). Truncated is
// set when MaxGroups bounded the output.
type CloneResult struct {
	Operation string       `json:"operation"`
	Groups    []CloneGroup `json:"groups"`
	Truncated bool         `json:"truncated"`
}

// MarshalCloneResult is the single canonical serializer for clone results,
// mirroring query.Marshal: groups/members are already canonically ordered before
// this call, HTML escaping is off, and the trailing newline is trimmed, so two
// calls on equal results produce byte-identical output across runs and surfaces.
func MarshalCloneResult(r CloneResult) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		return nil, fmt.Errorf("query: marshal clone result: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// candidate is the per-node working record built during the single pass.
//
// exactTokens/shapeTokens are MULTISETS (cardinality preserved): "calls two
// functions" and "calls one function" must differ, so the structure fingerprint
// has to count edges, not just note that a kind is present. shapeSet is the
// deduplicated form used only for the Jaccard structural-similarity comparison.
type candidate struct {
	node        model.Node
	exactTokens []string            // edgeKind|targetName, with multiplicity
	shapeTokens []string            // edgeKind|targetKind, with multiplicity
	shapeSet    map[string]struct{} // edgeKind|targetKind, deduped (for Jaccard)
	names       map[string]struct{} // distinct outbound target names (renamed metadata)
	exactFP     string
	shapeFP     string
	grouped     bool
}

// FindClones detects clone groups over the AST node table. It runs a single
// synchronous pass: build each candidate's structural fingerprint from its
// outbound calls/references edges (the node table itself is identity-only), then
// bucket into exact and renamed groups and greedily cluster the remainder into
// structural groups. Output is deterministic (canonical ordering throughout),
// bounded by cfg.MaxGroups with a typed Truncated flag, and reuses no dependency
// beyond what search_ast already pulls. Read-only.
func (s *Service) FindClones(ctx context.Context, cfg CloneConfig) (CloneResult, error) {
	cfg = cfg.normalized()

	cands, err := s.cloneCandidates(ctx, cfg)
	if err != nil {
		return CloneResult{}, err
	}

	var groups []CloneGroup
	// exact: identical target-name fingerprint.
	groups = append(groups, bucketGroups(cands, CloneTypeExact, func(c *candidate) string { return c.exactFP })...)
	// renamed: identical shape fingerprint among the still-ungrouped.
	groups = append(groups, bucketGroups(cands, CloneTypeRenamed, func(c *candidate) string { return c.shapeFP })...)
	// structural: Jaccard >= threshold among the still-ungrouped.
	groups = append(groups, structuralGroups(cands, cfg.Threshold)...)

	sortCloneGroups(groups)

	truncated := false
	if cfg.MaxGroups > 0 && len(groups) > cfg.MaxGroups {
		groups = groups[:cfg.MaxGroups]
		truncated = true
	}
	if groups == nil {
		groups = []CloneGroup{}
	}
	return CloneResult{Operation: OpFindClones, Groups: groups, Truncated: truncated}, nil
}

// cloneCandidates loads the candidate nodes and builds their edge-derived
// fingerprints in one pass over the calls+references edges.
func (s *Service) cloneCandidates(ctx context.Context, cfg CloneConfig) ([]*candidate, error) {
	kindWanted := make(map[string]struct{}, len(cfg.CloneKinds))
	for _, k := range cfg.CloneKinds {
		kindWanted[k] = struct{}{}
	}

	// All nodes, canonical NodeId order (store guarantees it); also a lookup table
	// to resolve edge targets to (name, kind).
	allNodes, err := s.reader.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return nil, err
	}
	byID := make(map[model.NodeId]model.Node, len(allNodes))
	for _, n := range allNodes {
		byID[n.ID()] = n
	}

	candByID := map[model.NodeId]*candidate{}
	var order []model.NodeId
	for _, n := range allNodes {
		if _, ok := kindWanted[n.Kind()]; !ok {
			continue
		}
		candByID[n.ID()] = &candidate{
			node:     n,
			shapeSet: map[string]struct{}{},
			names:    map[string]struct{}{},
		}
		order = append(order, n.ID())
	}

	// One pass over the structural edge kinds; attach each edge to its source
	// candidate's fingerprint sets.
	for _, ek := range []model.EdgeKind{EdgeKindCalls, EdgeKindReferences} {
		edges, err := s.reader.Edges(ctx, graphstore.Query{EdgeKind: ek})
		if err != nil {
			return nil, err
		}
		for _, e := range edges {
			c, ok := candByID[e.From()]
			if !ok {
				continue
			}
			targetName, targetKind := string(e.To()), ""
			if tn, ok := byID[e.To()]; ok {
				targetName, targetKind = tn.QualifiedName(), tn.Kind()
			}
			c.exactTokens = append(c.exactTokens, ek+"|"+targetName)
			c.shapeTokens = append(c.shapeTokens, ek+"|"+targetKind)
			c.shapeSet[ek+"|"+targetKind] = struct{}{}
			c.names[targetName] = struct{}{}
		}
	}

	out := make([]*candidate, 0, len(order))
	for _, id := range order {
		c := candByID[id]
		if len(c.exactTokens) < cfg.MinEdges {
			continue // too trivial to be a meaningful clone
		}
		c.exactFP = fingerprintMultiset(c.exactTokens)
		c.shapeFP = fingerprintMultiset(c.shapeTokens)
		out = append(out, c)
	}
	return out, nil
}

// bucketGroups groups still-ungrouped candidates by the key function and emits a
// group (of the given type) for every bucket with >= 2 members. Iteration is in
// the candidates' canonical order, so bucket membership and ordering are stable.
func bucketGroups(cands []*candidate, cloneType string, key func(*candidate) string) []CloneGroup {
	buckets := map[string][]*candidate{}
	var bucketOrder []string
	for _, c := range cands {
		if c.grouped {
			continue
		}
		k := key(c)
		if _, seen := buckets[k]; !seen {
			bucketOrder = append(bucketOrder, k)
		}
		buckets[k] = append(buckets[k], c)
	}
	var groups []CloneGroup
	for _, k := range bucketOrder {
		members := buckets[k]
		if len(members) < 2 {
			continue
		}
		for _, c := range members {
			c.grouped = true
		}
		groups = append(groups, newGroup(cloneType, members))
	}
	return groups
}

// structuralGroups greedily clusters the remaining (still-ungrouped) candidates
// by Jaccard similarity of their shape token sets >= threshold. Greedy seeding in
// canonical order makes the clustering deterministic.
func structuralGroups(cands []*candidate, threshold float64) []CloneGroup {
	var groups []CloneGroup
	for i, seed := range cands {
		if seed.grouped {
			continue
		}
		members := []*candidate{seed}
		for _, other := range cands[i+1:] {
			if other.grouped {
				continue
			}
			if jaccard(seed.shapeSet, other.shapeSet) >= threshold {
				members = append(members, other)
			}
		}
		if len(members) < 2 {
			continue
		}
		for _, c := range members {
			c.grouped = true
		}
		groups = append(groups, newGroup(CloneTypeStructural, members))
	}
	return groups
}

// renamedIdentifiers returns the sorted set of outbound target names that vary
// across the group's members (present in some members' name set but not all).
func renamedIdentifiers(members []*candidate) []string {
	if len(members) == 0 {
		return nil
	}
	union := map[string]int{}
	for _, c := range members {
		for name := range c.names {
			union[name]++
		}
	}
	var varying []string
	for name, count := range union {
		if count != len(members) { // not shared by every member → it varies
			varying = append(varying, name)
		}
	}
	sort.Strings(varying)
	if len(varying) == 0 {
		return nil
	}
	return varying
}

// newGroup materializes a CloneGroup from its candidate members: members are
// sorted lexicographically by (file, line, kind, name) per the determinism
// contract, and the group id is the content hash of the sorted member node ids.
func newGroup(cloneType string, members []*candidate) CloneGroup {
	cm := make([]CloneMember, 0, len(members))
	ids := make([]string, 0, len(members))
	for _, c := range members {
		n := c.node
		cm = append(cm, CloneMember{
			File:    n.SourcePath(),
			Line:    n.Line(),
			EndLine: n.Line(), // v1: node table has no span; end_line == line
			Kind:    n.Kind(),
			Name:    n.QualifiedName(),
		})
		ids = append(ids, string(n.ID()))
	}
	sort.Slice(cm, func(i, j int) bool { return lessMember(cm[i], cm[j]) })
	sort.Strings(ids)

	g := CloneGroup{
		ID:      "clone-" + fingerprintStrings(ids),
		Type:    cloneType,
		Members: cm,
		Size:    len(cm),
	}
	if cloneType == CloneTypeRenamed {
		g.RenamedIdentifiers = renamedIdentifiers(members)
	}
	return g
}

func lessMember(a, b CloneMember) bool {
	if a.File != b.File {
		return a.File < b.File
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	return a.Name < b.Name
}

func cloneTypeRank(t string) int {
	switch t {
	case CloneTypeExact:
		return 0
	case CloneTypeRenamed:
		return 1
	case CloneTypeStructural:
		return 2
	default:
		return 3
	}
}

// sortCloneGroups applies the single canonical group ordering: by clone-type
// (exact < renamed < structural), then by the group's first (canonically least)
// member, then by the content-addressed group id as a total-order backstop.
func sortCloneGroups(groups []CloneGroup) {
	sort.Slice(groups, func(i, j int) bool {
		a, b := groups[i], groups[j]
		if ra, rb := cloneTypeRank(a.Type), cloneTypeRank(b.Type); ra != rb {
			return ra < rb
		}
		if len(a.Members) > 0 && len(b.Members) > 0 && !sameMember(a.Members[0], b.Members[0]) {
			return lessMember(a.Members[0], b.Members[0])
		}
		return a.ID < b.ID
	})
}

func sameMember(a, b CloneMember) bool {
	return a.File == b.File && a.Line == b.Line && a.Kind == b.Kind && a.Name == b.Name
}

// jaccard is the size of the intersection over the size of the union of two token
// sets. Two empty sets are defined as similarity 0 (they are excluded as
// candidates anyway via MinEdges).
func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if _, ok := b[k]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// fingerprintMultiset is the order-independent content hash of a token MULTISET:
// the tokens (with their repetitions) are sorted then hashed, so the fingerprint
// is stable regardless of edge-iteration order yet still distinguishes "calls
// foo twice" from "calls foo once".
func fingerprintMultiset(tokens []string) string {
	toks := make([]string, len(tokens))
	copy(toks, tokens)
	sort.Strings(toks)
	return fingerprintStrings(toks)
}

func fingerprintStrings(sorted []string) string {
	h := fnv.New64a()
	for _, t := range sorted {
		_, _ = h.Write([]byte(t))
		_, _ = h.Write([]byte{0})
	}
	return fmt.Sprintf("%016x", h.Sum64())
}
