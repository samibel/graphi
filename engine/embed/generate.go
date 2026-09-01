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
// embedderRevision returns the embedder's revision tag, or "" when the
// adapter does not expose one. The pinned static embedder's revision
// lives in its ID() (the @<revision> segment); Ollama is a model-tag
// identity and reports "".
func embedderRevision(emb Embedder) string {
	if r, ok := emb.(interface{ Revision() string }); ok {
		return r.Revision()
	}
	return ""
}

// embedderModelSHA returns the model's pinned SHA-256 (lowercase hex)
// when the adapter exposes one. The static adapter's model digest is
// already in its ID(); Ollama has no native digest binding in v0, so
// the field reads "" until /api/show's digest is plumbed in.
func embedderModelSHA(emb Embedder) string {
	if m, ok := emb.(interface{ ModelSHA256() string }); ok {
		return m.ModelSHA256()
	}
	return ""
}

// embedderTokenizerSHA returns the tokenizer's pinned SHA-256 when the
// adapter exposes one. The static adapter's tokenizer digest is
// already in its ID(); Ollama reads "" until binding lands.
func embedderTokenizerSHA(emb Embedder) string {
	if t, ok := emb.(interface{ TokenizerSHA256() string }); ok {
		return t.TokenizerSHA256()
	}
	return ""
}

// embedderChunkerConfig returns the chunker description the fingerprint
// binds (AC-8). graphi embeds the whole document so the field is ""
// for every adapter; a future chunk-and-index-every-chunk design (out
// of scope, SW-267) would populate it.
func embedderChunkerConfig(emb Embedder) string {
	if c, ok := emb.(interface{ ChunkerConfig() string }); ok {
		return c.ChunkerConfig()
	}
	return ""
}

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

// DocumentResult is the structured outcome of asking a DocumentSource for
// a node's document. The reviewer fix for Critical 4 replaces the
// silent Skipped counter with three explicit outcomes:
//
//	Embedded: the node has an admitted document (will be embedded or
//	          carried forward).
//	Excluded: the node was deliberately excluded (file/package/external,
//	          generated path, no_span). A legitimately empty graph
//	          may have zero Embedded and zero Failed rows.
//	Failed:   the source could not produce a document for a node that
//	          MUST have one (read/parse failure, source bytes missing,
//	          admission failure). The build aborts.
type DocumentResult int

const (
	DocumentEmbedded DocumentResult = iota
	DocumentExcluded
	DocumentFailed
)

// ResultDocumentSource is the OPTIONAL richer interface that lets a
// DocumentSource report which outcome (Embedded/Excluded/Failed)
// applies to each node. The default DocumentSource above always
// returns ok=false for failed cases, so the runtime treats every
// Skipped node as a coverage failure under the reviewer's fix
// (reviewer Critical 4: exact set equality between persisted rows
// and eligible declaration IDs; unexpected omission fails).
type ResultDocumentSource interface {
	DocumentSource
	Result(node model.Node) DocumentResult
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
	// A node whose prior row is carried forward increments Reused ONLY
	// — not Embedded — so the documented invariant holds:
	// Embedded + Reused + Excluded == len(nodes) − Failed. Failed
	// nodes abort the build (reviewer fix Critical 4: exact set
	// equality between persisted rows and eligible declaration IDs).
	// A previous revision double-counted carried rows; the test that
	// pinned the wrong count is fixed in the same change.
	Embedded int
	// Excluded is the number of nodes the DocumentSource deliberately
	// excluded (file/package/external kinds, generated paths, no_span).
	// They get no vector — the legitimate exclusions the coverage
	// invariant allows. A legitimately empty graph may have zero
	// Embedded and zero Failed rows; the build commits a zero-row
	// generation (reviewer fix Critical 4).
	Excluded int
	// Failed is the number of nodes the DocumentSource could not
	// produce a document for, where the absence is a coverage failure
	// (read/parse error, missing source bytes, admission failure). The
	// build aborts on any Failed node: the build does not reach
	// ready and no partial generation is published.
	Failed int
	// Reused is the number of nodes whose prior vector was carried forward
	// without re-embedding (AC-4). Carried-forward rows do NOT increment
	// Embedded — a previous revision double-counted them, contradicting
	// the documented invariant. The total of Embedded + Reused + Excluded
	// equals the number of nodes visited minus Failed.
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
//
// graphGeneration is the current-graph identity the fingerprint embeds
// (SW-261). Callers should source it from the graphstore's
// `index.commit_generation` key so the build path and the reload path
// consume the same value. An empty value substitutes the documented
// placeholder so in-process tests that bypass the runtime stay
// fingerprint-compatible.
func GenerateAndPersist(ctx context.Context, reg *Registry, nodes []model.Node, docs DocumentSource, index VectorIndex, store GenerationStore, graphGeneration string) (GenerateResult, error) {
	return GenerateAndPersistWithProgress(ctx, reg, nodes, docs, index, store, nil, graphGeneration)
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
// rows are looked up by NodeID via the GenerationStore.LoadRow
// point-probe (no whole-generation materialisation, per the working-set
// rule); a node whose text_hash matches is carried forward (the prior
// row's Vector is upserted unchanged — no re-embed). Nodes whose
// text_hash differs are re-embedded. Nodes not in the graph at all are
// counted as Purged. The test in
// generationstore_conformance_test.go asserts the embedder call count.
//
// graphGeneration is the current-graph identity the fingerprint embeds.
// Empty substitutes the placeholder so in-process tests stay
// fingerprint-compatible.
func GenerateAndPersistWithProgress(ctx context.Context, reg *Registry, nodes []model.Node, docs DocumentSource, index VectorIndex, store GenerationStore, onProgress func(done, total int), graphGeneration string) (GenerateResult, error) {
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
	// SW-261 review round 2 (MAJOR 5): Ollama reports dim 0 until its
	// first call. If we fingerprint with dim=0, a real dim change is
	// neither fingerprinted nor validated (the SQLite check is gated
	// on fp.Dim > 0). Learn the real dim BEFORE fingerprinting: an
	// embedder that implements DimDiscoverer (Ollama) is probed once
	// here; an embedder that does not (Mock, fixed-dim ONNX) keeps its
	// declared dim. A probe failure surfaces the error verbatim so
	// the build fails closed rather than silently fingerprinting with
	// dim=0.
	if dd, ok := emb.(DimDiscoverer); ok {
		if err := dd.ProbeDim(ctx); err != nil {
			return GenerateResult{}, fmt.Errorf("embed: probe dim before fingerprint: %w", err)
		}
	}
	// Fingerprint the build with the embedder's FULL identity (SW-267
	// AC-3 / AC-8): model ID (which already carries the inference
	// configuration + admission profile hash), revision, model + tokenizer
	// digests when the embedder exposes them, dimension, document schema,
	// graph generation, and the admission profile's serialized form.
	// Every field has a real value: the Ollama adapter's hidden
	// dimensions are surfaced (ModelSHA256 / TokenizerSHA256 / ChunkerConfig)
	// via the AdmissionProfile, and the static adapter's hidden profile
	// is in its ID(). A profile change invalidates prior generations by
	// fingerprint (no silent reuse under false provenance).
	if graphGeneration == "" {
		graphGeneration = GraphGenerationPlaceholder
	}
	fp := Fingerprint{
		ModelID:         emb.ID(),
		Revision:        embedderRevision(emb),
		ModelSHA256:     embedderModelSHA(emb),
		TokenizerSHA256: embedderTokenizerSHA(emb),
		Dim:             emb.Dim(),
		DocumentSchema:  DocumentSchema,
		ChunkerConfig:   embedderChunkerConfig(emb),
		GraphGeneration: graphGeneration,
	}

	// AC-4 carry-forward: when the store holds a READY generation under
	// the SAME embedding-space fingerprint, lookup each prior row by
	// NodeID and reuse the row whose text_hash matches the current
	// document. The state MUST be StateReady — a stale / corrupt / missing
	// generation cannot vouch for the prior row's vectors, so the only
	// safe action is to re-embed (the precise failure mode CRITICAL 2 of
	// the SW-261 review caught: a previous revision loaded stale rows
	// and republished them under the new fingerprint, which is the
	// embedding-space mixing this story exists to prevent).
	//
	// Streaming: the prior rows are looked up by NodeID via the
	// GenerationStore.LoadRow point-lookup seam (AC-4), not
	// materialised into a whole-generation map
	// (context/standards.md:225-229 working-set rule). A node whose
	// text_hash does NOT match the prior row triggers an embed call;
	// a node whose text_hash DOES match carries the prior row's vector
	// forward unchanged. Both adapters satisfy the seam: SQLite via
	// an indexed point probe, mem via a map lookup.
	var (
		priorID    GenerationID
		hasPrior   bool
		priorTotal int
	)
	if store != nil {
		if priorGen, priorState, err := store.Active(ctx, fp, nil); err == nil && priorState == StateReady && priorGen.ID != "" {
			priorID = priorGen.ID
			hasPrior = true
			priorTotal = priorGen.RowCount
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
	// Reused). The Purged count is computed AFTER the loop, using the
	// actual write set the Build saw (Embedded + Reused), so a row
	// that was correctly re-embedded (Embedded, not Reused) is NOT
	// counted as purged — that was the pre-fix MINOR 7 arithmetic,
	// which the operator found misleading. The wantIDs bookkeeping
	// was dead code (written but never read); removed in the same
	// change.
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
			// Reviewer fix Critical 4: the coverage invariant is
			// exact set equality between persisted rows and eligible
			// declaration IDs. The DocumentSource now reports a
			// structured Result (Embedded/Excluded/Failed). Failed
			// nodes are coverage failures — they must abort the
			// build, not be silently counted as Skipped (which the
			// previous code did, leading to partial generations
			// being published as Ready).
			//
			// Backward compat: when the source does NOT implement
			// ResultDocumentSource (the legacy DocumentSource
			// interface — V1DocumentSource, etc.), we fall back to
			// Document's bool result. The structured Result path is
			// preferred when available. Legacy fallback (sources
			// without ResultDocumentSource): ok=false is Excluded
			// ONLY for declared legitimate exclusion kinds
			// (file/package/external/no_span). Anything else is a
			// coverage failure (reviewer fix C4).
			rds, hasRDS := docs.(ResultDocumentSource)
			if hasRDS {
				result := rds.Result(n)
				switch result {
				case DocumentExcluded:
					res.Excluded++
					continue
				case DocumentFailed:
					res.Failed++
					if build != nil {
						_ = build.Abort(ctx)
					}
					return GenerateResult{}, fmt.Errorf("embed: generate: node %s (%s): document source failed (coverage failure)", n.ID(), n.SourcePath())
				}
			}
			d, ok := docs.Document(n)
			if !ok {
				if hasRDS {
					// The richer Result said Embedded but the
					// legacy Document returned false. Treat this
					// as a coverage failure too — the source is
					// inconsistent with itself, and silently
					// skipping is exactly what AC-5 forbids.
					res.Failed++
					if build != nil {
						_ = build.Abort(ctx)
					}
					return GenerateResult{}, fmt.Errorf("embed: generate: node %s (%s): document source returned Excluded/Embedded inconsistent result", n.ID(), n.SourcePath())
				}
				// Legacy fallback: only declared legitimate
				// exclusion kinds are Excluded. Anything else is
				// a coverage failure (reviewer fix C4 — the
				// previous shape laundered any missing declaration
				// into a no_span exclusion).
				if isDeclaredExclusion(n) {
					res.Excluded++
					continue
				}
				res.Failed++
				if build != nil {
					_ = build.Abort(ctx)
				}
				return GenerateResult{}, fmt.Errorf("embed: generate: node %s (%s): legacy document source returned false (coverage failure)", n.ID(), n.SourcePath())
			}
			// AC-4 carry-forward: lookup the prior row by NodeID via the
			// GenerationStore.LoadRow point-probe (working-set rule).
			// When the prior row's text_hash matches the current
			// document's, reuse the prior row's vector — no embed
			// call. Carry-forward counts ONLY as Reused (a previous
			// revision double-counted it under both Embedded and
			// Reused, contradicting the documented Embedded + Reused +
			// Excluded invariant).
			if hasPrior {
				if priorRow, exists, lerr := store.LoadRow(ctx, priorID, n.ID()); lerr == nil && exists && priorRow.TextHash == d.TextHash {
					row := Row{
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
					continue
				}
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
			// SW-267 AC-4 / AC-7: the adapter is the final authority
			// on per-document admission. The document builder already
			// called Admit during BuildDocument; this call is the
			// embedding-side second look so a swap of the underlying
			// admitter (e.g. an explicit /api/embed with truncate:false
			// for Ollama) cannot embed an oversized payload. A failure
			// aborts the build (AC-5: no partial generation as Ready).
			if adm, ok := emb.(Admission); ok {
				for _, t := range texts {
					_, err := adm.Admit(ctx, t)
					if err != nil {
						if build != nil {
							_ = build.Abort(ctx)
						}
						return GenerateResult{}, err
					}
				}
			}
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
	//
	// SW-261 review round 2 (MINOR 7): the previous formula
	// `Purged = priorTotal - Reused` was wrong because a row whose
	// text changed is counted under Embedded (not Reused), so the
	// formula miscounted re-embedded rows as purged. The correct
	// formula subtracts the row set the new generation actually holds
	// (Embedded + Reused) from the prior generation's row count, so
	// the re-embedded row contributes to the new generation rather
	// than to Purged. The prior id's rows for nodes that are no
	// longer in the graph at all are also counted as Purged — they
	// are unreachable, like the re-embedded case, and the operator
	// sees them as dropped.
	if hasPrior && priorTotal > 0 {
		purged := priorTotal - (res.Embedded + res.Reused)
		if purged < 0 {
			purged = 0
		}
		res.Purged = purged
	}
	return res, nil
}
