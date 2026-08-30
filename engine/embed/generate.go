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
// Deprecated: v1 schema, kept for SW-261's migration comparison and for tests
// that exercise the generation pass without source bytes. The `--semantic`
// path embeds SemanticDocument v2 text (BuildDocument) instead; V1DocumentSource
// wraps this text in the document shape for that comparison.
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
// Deprecated: v1 schema, kept for tests that exercise the generation pass
// without source bytes. Never used by the `--semantic` path.
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
	// Reused is the number of nodes whose prior vector was carried forward
	// without re-embedding (AC-4). The total of Embedded + Reused + Skipped
	// equals the number of nodes visited.
	Reused int
	// Purged is the number of prior-generation rows dropped because their
	// node_id is no longer in the graph (AC-4 prune). The purge happens at
	// Commit time — the generation's row set is the union of the new rows
	// and the carried-forward rows; the prior generation's rows for nodes
	// not in either set are removed because they live in a generation that
	// is no longer active (the next Begin can re-build them, but the prior
	// active generation's rows for those nodes are unreachable).
	Purged int
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
// Embedder keyed by NodeId, and persists rows into the GenerationStore via
// the Begin/Build/Commit pattern.
//
// AC-4 carry-forward: when an active generation exists with the SAME
// fingerprint, the pass loads its rows and skips re-embedding for any node
// whose text_hash matches the prior row. The persisted bytes for carried
// nodes are byte-identical (the same Row's Vector is upserted). A test
// counts embedder calls to prove it. Nodes whose prior generation has a
// row but whose text changed are re-embedded; nodes whose prior
// generation has a row but whose NodeId is no longer in the graph are
// pruned at Commit time (the prior generation's rows for absent nodes
// remain in the sidecar but are no longer reachable from the active
// pointer).
//
// index is the in-memory ranking index (brute-force or HNSW). It may be
// nil for a persist-only caller; a nil index skips the live Put. The
// store is non-nil on the real `--semantic` path; a nil store skips the
// whole Build/Commit flow (in-memory only).
func GenerateAndPersist(ctx context.Context, reg *Registry, nodes []model.Node, docs DocumentSource, index VectorIndex, store GenerationStore) (GenerateResult, error) {
	return GenerateAndPersistWithProgress(ctx, reg, nodes, docs, index, store, nil)
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
// chunk k leaves the rows of chunks < k already persisted (the whole-set
// call persisted nothing on an embed error). Vector rows are derived state
// keyed by NodeId — a re-run overwrites them idempotently — so partial
// progress is strictly recoverable.
//
// SW-261 carry-forward: the pass inspects Active(ctx, fp, nil) before
// embedding. When the active fingerprint matches the requested one, prior
// rows are loaded and indexed by NodeID; a node whose text_hash matches is
// carried forward (the prior row's Vector is upserted unchanged — no
// re-embed). Nodes whose text_hash differs are re-embedded. Nodes not in
// the graph at all are counted as Purged. The test in
// generationstore_conformance_test.go asserts the embedder call count.
func GenerateAndPersistWithProgress(ctx context.Context, reg *Registry, nodes []model.Node, docs DocumentSource, index VectorIndex, store GenerationStore, onProgress func(done, total int)) (GenerateResult, error) {
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
	// Fingerprint the build with the embedder's identity, dimension,
	// schema and graph generation. In-process callers (tests) leave
	// graph_generation at the documented placeholder; the production
	// runtime fills the real value via `loadSemanticState` before the
	// store's Active sees it. The placeholder keeps an in-process test
	// and a real run fingerprint-compatible when the graphstore is not
	// yet wired.
	fp := Fingerprint{
		ModelID:         emb.ID(),
		Dim:             emb.Dim(),
		DocumentSchema:  DocumentSchema,
		GraphGeneration: GraphGenerationPlaceholder,
	}

	// AC-4 carry-forward: when the store holds a ready generation under
	// the same fingerprint, load its rows and reuse unchanged rows.
	var prior map[model.NodeId]Row
	if store != nil {
		// The fingerprint's graph_generation is filled in by the runtime;
		// for in-process tests that bypass the runtime we still build a
		// stable id. GenerateAndPersist uses only ModelID/Dim/Schema to
		// drive carry-forward here; a different graph_generation would
		// push the Active state to Stale and skip carry-forward. The
		// runtime path always supplies a real graph_generation.
		priorGen, _, err := store.Active(ctx, fp, nil)
		if err == nil && priorGen.ID != "" {
			if rows, lerr := store.Load(ctx, priorGen.ID); lerr == nil {
				prior = make(map[model.NodeId]Row, len(rows))
				for _, r := range rows {
					prior[r.NodeID] = r
				}
			}
		}
	}

	// SW-260 MAJOR: a configured embedder with zero nodes is a successful
	// pass over an empty graph. The store's Begin/Commit publishes a new
	// generation with zero rows; the prior active generation's rows
	// become unreachable (their generation is no longer the active one).
	// The nil-source guard above ensures we do not silently no-op.
	if len(nodes) == 0 {
		if store != nil {
			b, err := store.Begin(ctx, fp)
			if err != nil {
				return GenerateResult{}, err
			}
			if cerr := b.Commit(ctx); cerr != nil {
				return GenerateResult{}, cerr
			}
		}
		return res, nil
	}

	// Track the NodeIds the pass actually wrote — a node whose document
	// was skipped or absent does not enter the new generation, so the
	// Build's Commit-time validate sees a row set equal to (Embedded +
	// Reused). The Purged count is `len(prior) - reused` because every
	// prior row not in the new generation's row set represents a node
	// that no longer exists in the graph.
	wantIDs := make(map[model.NodeId]struct{}, len(nodes))

	chunkNodes := make([]model.Node, 0, embedChunkSize)
	texts := make([]string, 0, embedChunkSize)
	// Used for carry-forward inside a chunk: the nodes whose documents
	// match the prior text_hash. They skip the embed call but still
	// produce a Row whose Vector is the prior row's Vector.
	carry := make([]Row, 0, embedChunkSize)

	var build Build
	if store != nil {
		b, err := store.Begin(ctx, fp)
		if err != nil {
			return GenerateResult{}, err
		}
		build = b
	}

	for start := 0; start < len(nodes); start += embedChunkSize {
		end := start + embedChunkSize
		if end > len(nodes) {
			end = len(nodes)
		}
		chunkNodes, texts, carry = chunkNodes[:0], texts[:0], carry[:0]
		for _, n := range nodes[start:end] {
			d, ok := docs.Document(n)
			if !ok {
				res.Skipped++
				continue
			}
			wantIDs[n.ID()] = struct{}{}
			if priorRow, exists := prior[n.ID()]; exists && priorRow.TextHash == d.TextHash {
				// Carry forward: same node, same text_hash, prior row
				// is already in the embedding space. Upsert the prior
				// row's vector unchanged — NO embed call.
				row := Row{
					// GenerationID left blank so the build assigns its
					// own id (the prior generation has a different id).
					DocumentID: d.DocumentID,
					NodeID:     n.ID(),
					TextHash:   d.TextHash,
					Path:       d.Path,
					StartLine:  d.StartLine,
					EndLine:    d.EndLine,
					SpanMethod: d.SpanMethod,
					Vector:     priorRow.Vector,
				}
				carry = append(carry, row)
				if index != nil {
					index.Put(n.ID(), row.Vector)
				}
				res.Reused++
				res.Embedded++
				continue
			}
			chunkNodes = append(chunkNodes, n)
			texts = append(texts, d.Text)
		}
		// Persist the carried-forward rows first (no embed call).
		if build != nil {
			for _, r := range carry {
				if err := build.Upsert(ctx, r); err != nil {
					_ = build.Abort(ctx)
					return GenerateResult{}, err
				}
			}
		}
		if len(texts) > 0 {
			vecs, err := emb.Embed(ctx, texts)
			if err != nil {
				if build != nil {
					_ = build.Abort(ctx)
				}
				return GenerateResult{}, fmt.Errorf("embed: generate: %w", err)
			}
			if len(vecs) != len(texts) {
				if build != nil {
					_ = build.Abort(ctx)
				}
				return GenerateResult{}, fmt.Errorf("embed: embedder returned %d vectors for %d nodes", len(vecs), len(texts))
			}
			for i, n := range chunkNodes {
				d, _ := docs.Document(n)
				row := Row{
					DocumentID: d.DocumentID,
					NodeID:     n.ID(),
					TextHash:   d.TextHash,
					Path:       d.Path,
					StartLine:  d.StartLine,
					EndLine:    d.EndLine,
					SpanMethod: d.SpanMethod,
					Vector:     vecs[i],
				}
				if index != nil {
					index.Put(n.ID(), vecs[i])
				}
				if build != nil {
					if err := build.Upsert(ctx, row); err != nil {
						_ = build.Abort(ctx)
						return GenerateResult{}, err
					}
				}
				res.Embedded++
			}
		}
		if onProgress != nil {
			onProgress(end, len(nodes))
		}
	}
	if build != nil {
		if err := build.Commit(ctx); err != nil {
			return GenerateResult{}, err
		}
	}
	// Purged: prior rows not in the new generation's row set. We don't
	// physically delete them here — the prior generation's rows are still
	// durably persisted (Load(active_id) returns the new generation, not
	// the prior one); the prior id is simply no longer the active
	// pointer, so its rows are unreachable. The count is reported so the
	// operator can see what the pass dropped.
	if prior != nil {
		for id := range prior {
			if _, kept := wantIDs[id]; !kept {
				res.Purged++
			}
		}
	}
	return res, nil
}
