package parse

import (
	"fmt"
	"sort"

	"github.com/samibel/graphi/core/model"
)

// This file is the tree-sitter→graph mapping helper (SW-052, Slice 2): the one
// genuinely-new code path of STEP-0. Go uses go/ast (not tree-sitter), so this
// helper has no real grammar consumer yet; it is written test-first against fixture
// captures so SW-053's first grammar worker inherits a proven, deterministic
// primitive instead of re-deriving graph plumbing.
//
// It is a PURE, I/O-free transform (passes core/parse purity_test.go): it takes a
// language tag plus the captures a tree-sitter query already produced and returns
// the canonical model.Node/model.Edge values with full provenance + file:line
// evidence. It performs NO native execution, NO CGo, and NO source reading — the
// caller (a future grammar worker) is responsible for running the grammar query and
// handing the resolved capture text/positions in. This keeps the firewall: even a
// CGo grammar's output is mapped through a pure leaf.

// TSPoint is a 0-based (row, column) source position, mirroring tree-sitter's
// TSPoint. Row 0 is the first line; the mapping helper renders 1-based file:line
// evidence from it.
type TSPoint struct {
	Row    uint32
	Column uint32
}

// TSCapture is a single named capture produced by a tree-sitter query: the capture
// name from the query (e.g. "function.name"), the matched text, and the start
// position of the matched node. It is a grammar-agnostic value — the helper never
// touches a live tree-sitter tree, only these already-resolved captures.
type TSCapture struct {
	// Name is the query capture name (the @-tag, without the leading @), e.g.
	// "function.name", "type.name", "call.callee".
	Name string
	// Text is the source text the captured node spans (already extracted by the
	// caller; the helper does no source slicing).
	Text string
	// Start is the 0-based start position of the captured node.
	Start TSPoint
}

// TSNodeSpec describes one symbol-defining match a grammar query produced: the
// canonical node kind (from the Kind* vocabulary), the symbol's qualified name, and
// the defining capture's start position. The mapping helper turns each spec into a
// model.Node plus a "defines" edge from the file node.
type TSNodeSpec struct {
	// Kind is the canonical node kind (KindFunction/KindMethod/KindType/...).
	Kind string
	// QualifiedName is the fully-qualified symbol name (e.g. "pkg.Fn").
	QualifiedName string
	// Pos is the 0-based start position used for the node's line/column and the
	// defines-edge file:line evidence.
	Pos TSPoint
}

// TSEdgeSpec describes one resolved intra-file relationship a grammar query proved:
// a calls/references edge between two already-specified symbols (by qualified name).
// Cross-file/selector uses MUST NOT be emitted as edges here — they belong in
// PendingRefs; the helper only wires edges whose endpoints are both present in the
// node specs (it refuses to fabricate an endpoint).
type TSEdgeSpec struct {
	// FromQN / ToQN are qualified names that MUST each match a TSNodeSpec.
	FromQN string
	ToQN   string
	// Kind is the canonical edge kind (EdgeCalls/EdgeReferences). EdgeDefines is
	// emitted automatically per node and is rejected here.
	Kind string
	// Pos is the 0-based position of the reference site, for file:line evidence.
	Pos TSPoint
	// Tier / Confidence / Reason carry the provenance the grammar worker attaches.
	Tier       model.ConfidenceTier
	Confidence float64
	Reason     string
}

// MapTreeSitter is the canonical tree-sitter→graph mapping helper. Given a filename
// and the symbol/edge specs a grammar query produced, it deterministically builds:
//
//   - one file node for filename,
//   - one node per TSNodeSpec (in input order) with a "defines" edge from the file
//     node, full provenance, and file:line evidence,
//   - one calls/references edge per TSEdgeSpec whose endpoints both resolve to a
//     mapped node (edges are de-duplicated and emitted in a stable, sorted order).
//
// It is deterministic: identical specs yield byte-identical nodes/edges/IDs and
// identical ordering (mirrors TestExtractGo_Deterministic). It performs no I/O and
// fabricates no endpoint: an edge whose from/to is not among the node specs is
// dropped (the grammar worker should have recorded it as a PendingRef instead).
//
// captures is accepted for forward compatibility (a worker may want raw captures
// for richer provenance); the STEP-0 helper does not require them and ignores nil.
func MapTreeSitter(filename, language string, nodeSpecs []TSNodeSpec, edgeSpecs []TSEdgeSpec, captures []TSCapture) ([]model.Node, []model.Edge, error) {
	_ = language
	_ = captures

	fileNode, err := model.NewNode(KindFile, filename, filename, 1, 1)
	if err != nil {
		return nil, nil, fmt.Errorf("mapping: file node for %q: %w", filename, err)
	}

	nodes := make([]model.Node, 0, len(nodeSpecs)+1)
	nodes = append(nodes, fileNode)

	// Build nodes in input order and index them by qualified name so edge specs can
	// resolve endpoints. The defines edge for each node is collected alongside.
	byQN := make(map[string]model.NodeId, len(nodeSpecs))
	edges := make([]model.Edge, 0, len(nodeSpecs)+len(edgeSpecs))
	seen := make(map[model.EdgeId]struct{}, len(nodeSpecs)+len(edgeSpecs))

	addEdge := func(from, to model.NodeId, kind string, tier model.ConfidenceTier, conf float64, reason string, line int) error {
		ev := fmt.Sprintf("%s:%d", filename, line)
		edge, err := model.NewEdge(from, to, kind, tier, conf, reason, []string{ev})
		if err != nil {
			return fmt.Errorf("mapping: edge %s->%s (%s): %w", from, to, kind, err)
		}
		if _, dup := seen[edge.ID()]; dup {
			return nil
		}
		seen[edge.ID()] = struct{}{}
		edges = append(edges, edge)
		return nil
	}

	for _, spec := range nodeSpecs {
		line := int(spec.Pos.Row) + 1
		col := int(spec.Pos.Column) + 1
		n, err := model.NewNode(spec.Kind, spec.QualifiedName, filename, line, col)
		if err != nil {
			return nil, nil, fmt.Errorf("mapping: node %q: %w", spec.QualifiedName, err)
		}
		nodes = append(nodes, n)
		byQN[spec.QualifiedName] = n.ID()
		if err := addEdge(fileNode.ID(), n.ID(), EdgeDefines, model.TierConfirmed, 1.0,
			"declared as a top-level symbol in the source file", line); err != nil {
			return nil, nil, err
		}
	}

	// Resolve relationship edges. Endpoints MUST both be mapped nodes; an edge with
	// an unmapped endpoint is dropped (no fabricated endpoint — that is a PendingRef
	// the grammar worker should emit). EdgeDefines is reserved for the per-node edge.
	for _, spec := range edgeSpecs {
		if spec.Kind == EdgeDefines {
			return nil, nil, fmt.Errorf("mapping: edge spec %s->%s may not use reserved kind %q", spec.FromQN, spec.ToQN, EdgeDefines)
		}
		from, okF := byQN[spec.FromQN]
		to, okT := byQN[spec.ToQN]
		if !okF || !okT {
			// Unprovable endpoint: skip, do not fabricate. (PendingRef territory.)
			continue
		}
		tier := spec.Tier
		if !tier.Valid() {
			tier = model.TierDerived
		}
		if err := addEdge(from, to, spec.Kind, tier, spec.Confidence, spec.Reason, int(spec.Pos.Row)+1); err != nil {
			return nil, nil, err
		}
	}

	// Determinism: nodes already follow input order (file node first). Edges are
	// sorted by their content-derived EdgeId so identical specs in any encounter
	// order yield byte-identical edge ordering.
	sort.SliceStable(edges, func(i, j int) bool { return edges[i].ID() < edges[j].ID() })

	return nodes, edges, nil
}
