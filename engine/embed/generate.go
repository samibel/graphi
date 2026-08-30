package embed

import (
	"context"
	"fmt"
	"strings"

	"github.com/cespare/xxhash/v2"

	"github.com/samibel/graphi/core/model"
)

// NodeText derives the v1 per-node text: the qualified name enriched with the
// node Kind for disambiguation. It is deterministic: identical nodes always
// yield identical text, hence (via a deterministic embedder) identical vectors.
//
// Deprecated: v1 schema, kept for SW-261's migration comparison. The
// `--semantic` path embeds SemanticDocument v2 text (BuildDocument) instead;
// V1DocumentSource wraps this text in the document shape for that comparison.
func NodeText(n model.Node) string {
	qn := strings.TrimSpace(n.QualifiedName())
	kind := strings.TrimSpace(n.Kind())
	if kind == "" {
		return qn
	}
	return kind + " " + qn
}

// DocumentSource supplies the SemanticDocument the generation pass embeds for
// each node (SW-260 AC-8). ok=false means the node has no document (excluded
// by the builder, or nothing to cut it from); the pass skips and counts it
// rather than embedding a name-only stand-in.
type DocumentSource interface {
	Document(node model.Node) (SemanticDocument, bool)
}

// V1DocumentSource yields the v1 name-only text (NodeText) in the document
// shape, tagged document_schema "v1".
//
// Deprecated: v1 schema, kept for SW-261's migration comparison and for tests
// that exercise the generation pass without source bytes. Never used by the
// `--semantic` path.
type V1DocumentSource struct{}

// Document implements DocumentSource with the v1 text.
func (V1DocumentSource) Document(n model.Node) (SemanticDocument, bool) {
	text := NodeText(n)
	hash := model.FormatID(xxhash.Sum64String(text))
	return SemanticDocument{
		DocumentID:     documentID(n.ID(), hash, "v1"),
		NodeID:         n.ID(),
		Kind:           n.Kind(),
		QualifiedName:  n.QualifiedName(),
		Path:           n.SourcePath(),
		TextHash:       hash,
		DocumentSchema: "v1",
		Text:           text,
	}, true
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
	// Skipped is the number of nodes the DocumentSource had no document for
	// (excluded artefacts, generated paths, unreadable sources). They get no
	// vector rather than a name-only stand-in.
	Skipped int
}

// GenerateAndPersist runs the embedding-GENERATION pass for `graphi index
// --semantic`. It is gated STRICTLY on reg.Configured(): with no embedder it
// returns a graceful-skip result (Configured=false) having performed NO embedding,
// NO network, and NO writes — mirroring engine/search's typed Unavailable
// (story AC: "graceful skip preserved").
//
// When an embedder IS configured it enumerates every node, obtains its
// SemanticDocument from docs (SW-260: the v2 text — body, doc comment, path —
// replaces the v1 NodeText), embeds the document text through the active
// Embedder keyed by NodeId, Put()s each vector into the live in-memory Index,
// and Upsert()s it into the durable VectorTable. The durable rows survive the
// process so a later reload serves semantic search without re-embedding.
//
// On a SUCCESSFUL pass the table is asked to enforce the SW-260 replace-set
// contract: every row the active embedder still holds whose NodeId was NOT
// embedded this run — including vectors written by a pre-SW-260 v1 pass for
// now-excluded nodes — is dropped, so the persisted set equals the documents
// just embedded and an excluded node cannot serve a stale vector.
//
// nodes is the full node set (e.g. store.Nodes(ctx, Query{})). docs must be
// non-nil once an embedder is configured (the nil-source guard runs BEFORE
// the empty-node return so a configured embedder with zero nodes and a nil
// source errors, never silently no-ops). index and table may be nil to skip
// the respective sink (e.g. persist-only or in-memory-only), but the normal
// index pass supplies both. A nil table skips the replace-set delete
// (in-memory-only caller); a nil index skips the live Put.
func GenerateAndPersist(ctx context.Context, reg *Registry, nodes []model.Node, docs DocumentSource, index VectorIndex, table VectorTable) (GenerateResult, error) {
	return GenerateAndPersistWithProgress(ctx, reg, nodes, docs, index, table, nil)
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
// Documents are obtained chunk by chunk, so a DocumentSource that cuts them
// from source files needs to hold only the chunk's worth (and its current
// file) in memory rather than every document of the repo.
//
// Chunking changes one failure-path detail, deliberately: an Embed error in
// chunk k leaves the vectors of chunks < k already persisted (the whole-set
// call persisted nothing on an embed error). Vector rows are derived state
// keyed by NodeId — a re-run overwrites them idempotently — so partial
// progress is strictly recoverable.
//
// SW-260 replace-set: on a SUCCESSFUL pass (every chunk embedded, every
// Upsert committed), the pass invokes table.DeleteExcept with the NodeIds it
// actually embedded. Any row the table still holds for this embedder that is
// NOT in that set — every vector written for an excluded node by a pre-SW-260
// v1 pass, or any drift the generator skips this run — is removed in a single
// bulk delete. After a successful pass the persisted set for the embedder
// equals the set of documents just embedded; an excluded node cannot serve a
// stale vector. A failed chunk skips the delete entirely (so partial
// progress is still recoverable on a re-run); the error short-circuits with
// whatever earlier chunks already persisted.
func GenerateAndPersistWithProgress(ctx context.Context, reg *Registry, nodes []model.Node, docs DocumentSource, index VectorIndex, table VectorTable, onProgress func(done, total int)) (GenerateResult, error) {
	if reg == nil || !reg.Configured() {
		return GenerateResult{Configured: false}, nil // graceful skip: no embed, no dial, no write
	}
	emb, ok := reg.Active()
	if !ok {
		return GenerateResult{Configured: false}, nil
	}
	res := GenerateResult{Configured: true, EmbedderID: emb.ID()}
	// SW-260 minor (review round 1): the nil-source guard precedes the
	// empty-node return so a configured embedder with zero nodes and a nil
	// DocumentSource errors rather than silently returning a zero result; the
	// unconfigured-registry graceful skip still comes first.
	if docs == nil {
		return GenerateResult{}, fmt.Errorf("embed: generate: no document source for %d nodes", len(nodes))
	}
	// SW-260 MAJOR (review round 2): a configured embedder with zero nodes
	// is NOT a graceful skip — it is a successful pass over an EMPTY graph.
	// The replace-set contract demands that "the persisted set equals the
	// documents just embedded"; with zero documents just embedded, the
	// persisted set must be EMPTY for this embedder. So we still call
	// table.DeleteExcept(ctx, nil) below (the empty-scope reset path) so a
	// prior v1 vector for any node — or any drift the generator previously
	// embedded for this embedder — does not survive a re-index over an
	// emptied graph. The nil-source guard above ensures this case cannot
	// silently no-op behind the caller's back; the unconfigured-registry
	// graceful skip above remains first.
	if len(nodes) == 0 {
		if table != nil {
			if err := table.DeleteExcept(ctx, nil); err != nil {
				return GenerateResult{}, err
			}
		}
		return res, nil
	}

	chunkNodes := make([]model.Node, 0, embedChunkSize)
	texts := make([]string, 0, embedChunkSize)
	// embeddedIDs collects every NodeId this pass actually wrote into the
	// durable table, in iteration order, so the post-pass DeleteExcept call
	// can scope a single bulk delete to "what we just embedded". The IDs are
	// collected deterministically (the same node order as the input) so a
	// repeat pass over the same input calls DeleteExcept with the same
	// argument and the persisted bytes stay byte-identical.
	var embeddedIDs []model.NodeId
	for start := 0; start < len(nodes); start += embedChunkSize {
		end := start + embedChunkSize
		if end > len(nodes) {
			end = len(nodes)
		}
		chunkNodes, texts = chunkNodes[:0], texts[:0]
		for _, n := range nodes[start:end] {
			d, ok := docs.Document(n)
			if !ok {
				res.Skipped++
				continue
			}
			chunkNodes = append(chunkNodes, n)
			texts = append(texts, d.Text)
		}
		if len(texts) > 0 {
			vecs, err := emb.Embed(ctx, texts)
			if err != nil {
				return GenerateResult{}, fmt.Errorf("embed: generate: %w", err)
			}
			if len(vecs) != len(texts) {
				return GenerateResult{}, fmt.Errorf("embed: embedder returned %d vectors for %d nodes", len(vecs), len(texts))
			}
			for i, n := range chunkNodes {
				id := n.ID()
				if index != nil {
					index.Put(id, vecs[i])
				}
				if table != nil {
					if err := table.Upsert(ctx, Vector{NodeID: id, Values: vecs[i]}); err != nil {
						return GenerateResult{}, err
					}
				}
				embeddedIDs = append(embeddedIDs, id)
				res.Embedded++
			}
		}
		if onProgress != nil {
			onProgress(end, len(nodes))
		}
	}
	// Replace-set: drop every row the active embedder holds whose node_id is
	// not in embeddedIDs, so the persisted set equals the documents we just
	// embedded. A nil table (index-only caller) skips the delete; a partial
	// pass (len(embeddedIDs) != len(nodes)) still calls it — a v1 row from a
	// prior pass for a now-skipped node is exactly the case this removes.
	if table != nil {
		if err := table.DeleteExcept(ctx, embeddedIDs); err != nil {
			return GenerateResult{}, err
		}
	}
	return res, nil
}
