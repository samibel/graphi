package embed

import (
	"context"
	"fmt"
	"strings"

	"github.com/samibel/graphi/core/model"
)

// NodeText derives the per-node text fed to the embedder. Node carries no
// doc/comment accessor today (core/model is a pure identity leaf), so the embedded
// text is the qualified name enriched with the node Kind for disambiguation
// (story: "qualified name + doc/comment text … fall back to Kind+QualifiedName if
// none"). It is deterministic: identical nodes always yield identical text, hence
// (via a deterministic embedder) identical vectors.
func NodeText(n model.Node) string {
	qn := strings.TrimSpace(n.QualifiedName())
	kind := strings.TrimSpace(n.Kind())
	if kind == "" {
		return qn
	}
	return kind + " " + qn
}

// GenerateResult summarizes an embedding-generation pass.
type GenerateResult struct {
	// Configured reports whether an embedder was active. When false the pass is a
	// graceful skip: nothing was embedded, dialed, or persisted.
	Configured bool
	// EmbedderID is the active embedder's ID (empty on the graceful-skip path).
	EmbedderID string
	// Embedded is the number of node vectors generated and persisted.
	Embedded int
}

// GenerateAndPersist runs the embedding-GENERATION pass for `graphi index
// --semantic`. It is gated STRICTLY on reg.Configured(): with no embedder it
// returns a graceful-skip result (Configured=false) having performed NO embedding,
// NO network, and NO writes — mirroring engine/search's typed Unavailable
// (story AC: "graceful skip preserved").
//
// When an embedder IS configured it enumerates every node, builds NodeText, embeds
// it through the active Embedder keyed by NodeId, Put()s each vector into the live
// in-memory Index, and Upsert()s it into the durable VectorTable. The durable
// rows survive the process so a later reload serves semantic search without
// re-embedding.
//
// nodes is the full node set (e.g. store.Nodes(ctx, Query{})). index and table may
// be nil to skip the respective sink (e.g. persist-only or in-memory-only), but
// the normal index pass supplies both.
func GenerateAndPersist(ctx context.Context, reg *Registry, nodes []model.Node, index VectorIndex, table VectorTable) (GenerateResult, error) {
	return GenerateAndPersistWithProgress(ctx, reg, nodes, index, table, nil)
}

// embedChunkSize bounds how many node texts each Embed call carries. It sets
// the progress-callback granularity AND caps the in-flight vector slice to a
// chunk (the whole-set call held every vector of the repo simultaneously).
// Small enough that a slow per-text embedder (Ollama does one HTTP round-trip
// per text) reports progress every few seconds; large enough that the chunk
// bookkeeping is noise.
const embedChunkSize = 64

// GenerateAndPersistWithProgress is GenerateAndPersist with a progress seam:
// onProgress (nil-safe) is invoked from THIS goroutine after each embedded and
// persisted chunk with the running (done, total) node counts — the final call
// is (total, total). The embedding-generation pass previously produced no
// output between its start and its summary line, which on thousands of nodes
// via a per-text HTTP embedder reads as a hang.
//
// Chunking changes one failure-path detail, deliberately: an Embed error in
// chunk k leaves the vectors of chunks < k already persisted (the whole-set
// call persisted nothing on an embed error). Vector rows are derived state
// keyed by NodeId — a re-run overwrites them idempotently — so partial
// progress is strictly recoverable.
func GenerateAndPersistWithProgress(ctx context.Context, reg *Registry, nodes []model.Node, index VectorIndex, table VectorTable, onProgress func(done, total int)) (GenerateResult, error) {
	if reg == nil || !reg.Configured() {
		return GenerateResult{Configured: false}, nil // graceful skip: no embed, no dial, no write
	}
	emb, ok := reg.Active()
	if !ok {
		return GenerateResult{Configured: false}, nil
	}
	res := GenerateResult{Configured: true, EmbedderID: emb.ID()}
	if len(nodes) == 0 {
		return res, nil
	}

	texts := make([]string, len(nodes))
	for i, n := range nodes {
		texts[i] = NodeText(n)
	}
	for start := 0; start < len(nodes); start += embedChunkSize {
		end := start + embedChunkSize
		if end > len(nodes) {
			end = len(nodes)
		}
		vecs, err := emb.Embed(ctx, texts[start:end])
		if err != nil {
			return GenerateResult{}, fmt.Errorf("embed: generate: %w", err)
		}
		if len(vecs) != end-start {
			return GenerateResult{}, fmt.Errorf("embed: embedder returned %d vectors for %d nodes", len(vecs), end-start)
		}
		for i, n := range nodes[start:end] {
			id := n.ID()
			if index != nil {
				index.Put(id, vecs[i])
			}
			if table != nil {
				if err := table.Upsert(ctx, Vector{NodeID: id, Values: vecs[i]}); err != nil {
					return GenerateResult{}, err
				}
			}
			res.Embedded++
		}
		if onProgress != nil {
			onProgress(end, len(nodes))
		}
	}
	return res, nil
}
