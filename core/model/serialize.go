package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// serialFormatVersion versions the serialized envelope so future format changes
// are explicit and detectable on read.
const serialFormatVersion = 1

// Graph is an order-independent collection of Nodes and Edges used as the unit
// of deterministic serialization. Insertion order does not affect the serialized
// bytes: Marshal sorts nodes by NodeId and edges by EdgeId before encoding.
type Graph struct {
	nodes []Node
	edges []Edge
}

// NewGraph builds a Graph from the given nodes and edges. The inputs are
// defensively copied; insertion order is irrelevant to identity and
// serialization.
func NewGraph(nodes []Node, edges []Edge) Graph {
	ns := make([]Node, len(nodes))
	copy(ns, nodes)
	es := make([]Edge, len(edges))
	copy(es, edges)
	return Graph{nodes: ns, edges: es}
}

// Nodes returns a defensive copy of the graph's nodes (unsorted, as supplied).
func (g Graph) Nodes() []Node {
	out := make([]Node, len(g.nodes))
	copy(out, g.nodes)
	return out
}

// Edges returns a defensive copy of the graph's edges (unsorted, as supplied).
func (g Graph) Edges() []Edge {
	out := make([]Edge, len(g.edges))
	copy(out, g.edges)
	return out
}

// --- wire representations (stable field ordering via struct tag order) ---

type nodeWire struct {
	ID            NodeId `json:"id"`
	Kind          string `json:"kind"`
	QualifiedName string `json:"qualified_name"`
	SourcePath    string `json:"source_path"`
	Line          int    `json:"line"`
	Column        int    `json:"column"`
	// Meta is the non-identity annotation/flag rider. It is a POINTER with
	// omitempty so a node WITHOUT metadata encodes exactly as before (no "meta"
	// key at all) — preserving byte-parity for pre-meta graphs and the golden
	// vector — while a node WITH metadata carries a deterministic (sorted) block.
	Meta *NodeMeta `json:"meta,omitempty"`
}

type edgeWire struct {
	ID         EdgeId         `json:"id"`
	From       NodeId         `json:"from"`
	To         NodeId         `json:"to"`
	Kind       string         `json:"kind"`
	Tier       ConfidenceTier `json:"confidence_tier"`
	Confidence float64        `json:"confidence"`
	Reason     string         `json:"reason"`
	Evidence   []string       `json:"evidence"`
}

type graphWire struct {
	FormatVersion         int        `json:"format_version"`
	IdentitySchemaVersion uint32     `json:"identity_schema_version"`
	Nodes                 []nodeWire `json:"nodes"`
	Edges                 []edgeWire `json:"edges"`
}

func (n Node) toWire() nodeWire {
	w := nodeWire{
		ID:            n.id,
		Kind:          n.kind,
		QualifiedName: n.qualifiedName,
		SourcePath:    n.sourcePath,
		Line:          n.line,
		Column:        n.column,
	}
	// Only emit meta when present so empty-meta nodes stay byte-identical to the
	// pre-meta encoding. Re-normalize defensively so the wire block is sorted.
	if !n.meta.IsZero() {
		m := NewNodeMeta(n.meta.Annotations, n.meta.Flags)
		w.Meta = &m
	}
	return w
}

func (e Edge) toWire() edgeWire {
	ev := make([]string, len(e.evidence))
	copy(ev, e.evidence)
	return edgeWire{
		ID:         e.id,
		From:       e.from,
		To:         e.to,
		Kind:       e.kind,
		Tier:       e.tier,
		Confidence: e.confidence,
		Reason:     e.reason,
		Evidence:   ev,
	}
}

// Marshal serializes the Graph to canonical JSON whose bytes are identical
// regardless of node/edge insertion order or run. Nodes are sorted by NodeId,
// edges by EdgeId, and each edge's Evidence is canonically sorted at
// construction; field ordering is fixed by the wire struct layout. The output is
// indentation-free with sorted object keys (Go's encoder emits struct fields in
// declaration order and map keys sorted), making it byte-for-byte deterministic
// and round-trippable via Unmarshal.
func (g Graph) Marshal() ([]byte, error) {
	nodes := make([]nodeWire, len(g.nodes))
	for i, n := range g.nodes {
		nodes[i] = n.toWire()
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	edges := make([]edgeWire, len(g.edges))
	for i, e := range g.edges {
		edges[i] = e.toWire()
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].ID != edges[j].ID {
			return edges[i].ID < edges[j].ID
		}
		// Stable tie-break (should not occur for distinct edges, but keeps
		// output deterministic if two identity-equal edges are present).
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		return edges[i].Kind < edges[j].Kind
	})

	w := graphWire{
		FormatVersion:         serialFormatVersion,
		IdentitySchemaVersion: IdentitySchemaVersion,
		Nodes:                 nodes,
		Edges:                 edges,
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(w); err != nil {
		return nil, fmt.Errorf("model: marshal graph: %w", err)
	}
	// json.Encoder appends a trailing newline; trim it for byte-stable output.
	out := bytes.TrimRight(buf.Bytes(), "\n")
	return out, nil
}

// Unmarshal parses canonical JSON produced by Marshal back into a Graph,
// re-validating provenance and re-deriving IDs so the round-trip is lossless and
// any tampered/unknown ConfidenceTier is rejected. The reconstructed nodes/edges
// pass through NewNode/NewEdge, guaranteeing every invariant still holds.
func Unmarshal(data []byte) (Graph, error) {
	var w graphWire
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return Graph{}, fmt.Errorf("model: unmarshal graph: %w", err)
	}
	if w.FormatVersion != serialFormatVersion {
		return Graph{}, fmt.Errorf("model: unsupported serialization format_version %d (want %d)", w.FormatVersion, serialFormatVersion)
	}

	nodes := make([]Node, 0, len(w.Nodes))
	for _, nw := range w.Nodes {
		n, err := NewNode(nw.Kind, nw.QualifiedName, nw.SourcePath, nw.Line, nw.Column)
		if err != nil {
			return Graph{}, fmt.Errorf("model: unmarshal node %q: %w", nw.ID, err)
		}
		if n.ID() != nw.ID {
			return Graph{}, fmt.Errorf("model: node id mismatch on read: serialized %q, derived %q", nw.ID, n.ID())
		}
		// Re-attach non-identity meta (re-normalized so a hand-edited/unsorted
		// snapshot still round-trips deterministically). Meta does not affect ID.
		if nw.Meta != nil {
			n = n.WithMeta(NewNodeMeta(nw.Meta.Annotations, nw.Meta.Flags))
		}
		nodes = append(nodes, n)
	}

	edges := make([]Edge, 0, len(w.Edges))
	for _, ew := range w.Edges {
		// reject unknown tier explicitly for a clear error before NewEdge.
		if !ew.Tier.Valid() {
			return Graph{}, fmt.Errorf("model: unmarshal edge %q: %w: unknown confidence_tier %q", ew.ID, ErrInvalidEdge, ew.Tier)
		}
		e, err := NewEdge(ew.From, ew.To, ew.Kind, ew.Tier, ew.Confidence, ew.Reason, ew.Evidence)
		if err != nil {
			return Graph{}, fmt.Errorf("model: unmarshal edge %q: %w", ew.ID, err)
		}
		if e.ID() != ew.ID {
			return Graph{}, fmt.Errorf("model: edge id mismatch on read: serialized %q, derived %q", ew.ID, e.ID())
		}
		edges = append(edges, e)
	}

	return NewGraph(nodes, edges), nil
}

// Validate checks structural integrity of the graph: every edge endpoint
// (From/To) must reference a Node present in the graph, and every node/edge must
// carry a derived ID. It performs no I/O.
func (g Graph) Validate() error {
	ids := make(map[NodeId]struct{}, len(g.nodes))
	for _, n := range g.nodes {
		if strings.TrimSpace(string(n.id)) == "" {
			return fmt.Errorf("%w: node %q has empty id", ErrInvalidNode, n.qualifiedName)
		}
		ids[n.id] = struct{}{}
	}
	for _, e := range g.edges {
		if strings.TrimSpace(string(e.id)) == "" {
			return fmt.Errorf("%w: edge has empty id", ErrInvalidEdge)
		}
		if _, ok := ids[e.from]; !ok {
			return fmt.Errorf("%w: edge %q references unknown from-node %q", ErrInvalidEdge, e.id, e.from)
		}
		if _, ok := ids[e.to]; !ok {
			return fmt.Errorf("%w: edge %q references unknown to-node %q", ErrInvalidEdge, e.id, e.to)
		}
	}
	return nil
}
