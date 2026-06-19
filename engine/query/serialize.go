package query

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Marshal is the single canonical serializer for query results, shared by every
// surface (CLI, MCP, …). It is the ONLY place a Result becomes bytes, so all
// surfaces emit byte-identical output for identical inputs (the MCP↔CLI parity
// guarantee is by construction, not by coincidence).
//
// Byte-stability: the Result's Nodes/Edges are already materialized-then-sorted
// by the canonical comparator before serialization; field order is fixed by the
// wire struct tag declaration order; nested evidence preserves the model's
// canonical sort; HTML escaping is disabled; and the encoder's trailing newline
// is trimmed. Two Marshal calls on equal Results therefore produce identical
// bytes across repeated runs and across surfaces.
func Marshal(r Result) ([]byte, error) {
	// Re-sort defensively so the serializer is canonical even if a caller hands
	// in an unsorted Result; this keeps "one canonical encoder" honest.
	nodes := make([]ResultNode, len(r.Nodes))
	copy(nodes, r.Nodes)
	sortNodes(nodes)

	edges := make([]ResultEdge, len(r.Edges))
	copy(edges, r.Edges)
	sortEdges(edges)

	out := Result{
		Operation: r.Operation,
		Symbol:    r.Symbol,
		Outcome:   r.Outcome,
		Depth:     r.Depth,
		Nodes:     nodes,
		Edges:     edges,
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, fmt.Errorf("query: marshal result: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
