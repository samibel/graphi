// Package retrieval is the deep, explainable candidate-union module that
// backs graphi's semantic search (SW-263, story of the semantic-search Labs
// track). It owns the ranking pipeline end to end:
//
//   - candidate union of the top-k lexical and top-k semantic candidates
//     (k pinned at candidateK = 50), deduped over document_id then node_id,
//     with a semantic-only hit reachable in the result;
//   - integer Reciprocal Rank Fusion (rrfK = 60, scale = 1_000_000,
//     score = scale/(rrfK+rank)); only integer arithmetic in the ranking
//     path, no floats in ordering;
//   - bounded rerank on the audited integer signals of
//     engine/agenttools/hybridsearch (SegmentExact, SegmentPrefix,
//     PathSegment, DegreePoint) plus a definition bonus and the
//     vendor/generated classification penalty;
//   - diversification with one row per node_id and maxPerFile = 3 rows per
//     path in the top Limit — rows beyond the cap are demoted, never
//     dropped, so a larger Limit still reaches them;
//   - a typed, byte-stable Explain block per row (LexicalRank, SemanticRank,
//     RRF, Graph, Classification, Final) and a typed Summary stamped with
//     the retrieval version, weights hash, model fingerprint and index
//     fingerprint so the bytes are reproducible across runs and architectures.
//
// The package consumes the two existing engine seams — engine/search.Service
// for lexical (and the same service for SemanticSearch over the active
// GenerationStore generation, AC-7) — and the graphstore the lexical service
// reads from, for path/line/span provenance and bounded graph expansion.
// It introduces NO new port and NO module-kernel contribution: it is
// composed once at cmd/internal/runtime.Composition.Client() and handed to
// both consumers in SW-264. The exact-identifier / exact-path rule (AC-6)
// is a documented regex, never learned.
//
// Layering: retrieval is an engine leaf. It depends on core/* and
// engine/{search,agenttools/hybridsearch,embed,model} and must NOT import
// surfaces/* or cmd/*. The cmd→surfaces→engine→core direction is enforced
// by `go run ./cmd/layerguard`.
package retrieval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/search"
)

// retrievalVersion stamps the retrieval method (per AC-1 Summary). It is the
// audit value that lets a downstream caller tell the SW-263 release from any
// later revision that breaks serialization.
const retrievalVersion = "retrieval/1"

// Pinned arithmetic constants from the spec ("Arithmetic that is fixed and
// must not be 'improved'"). They are named so reviewers and tests see them
// as contracts, not magic numbers.
const (
	// candidateK is the per-source top-k the union stage fetches (lexical
	// and semantic independently).
	candidateK = 50
	// rrfK is the standard RRF damping constant (Cormack et al., 2009).
	rrfK = 60
	// rrfScale multiplies every RRF contribution so integer division
	// preserves sub-unit rank differences.
	rrfScale = 1_000_000
	// maxPerFile caps how many rows in the top Limit share one source path.
	// Rows beyond the cap are demoted below the Limit, never dropped.
	maxPerFile = 3
	// limitDefault is the default Result.Limit when Request.Limit <= 0.
	limitDefault = 20
)

// Mode is the retrieval mode (AC-1 Request.Mode). The default (zero value)
// ModeAuto prefers semantic when one is configured and lexical otherwise.
// The other two modes pin the choice and let a caller or test force a path
// without toggling the embedder selector.
type Mode int

const (
	// ModeAuto: lexical when no embedder is configured, otherwise lexical +
	// semantic union, then the full RRF + rerank + diversify pipeline.
	// The default build hits this branch.
	ModeAuto Mode = iota
	// ModeLexicalOnly: ignore semantic candidates regardless of state. The
	// configured path still decides the Degradation state. Used by the
	// "chunk-only" ablation in the AC-9 eval harness: pure lexical ranking
	// with no semantic signal and no RRF fusion contribution.
	ModeLexicalOnly
	// ModeSemanticRequired: refuse to answer if no semantic candidates are
	// available — typed unavailable response with a typed reason, no error.
	ModeSemanticRequired
	// ModeFusionNoGraph: lexical+semantic union, integer RRF fusion, but
	// NO graph rerank. Used by the "fusion" ablation in the AC-9 eval
	// harness: isolates the contribution of the union + RRF step from
	// the bounded graph rerank signals that ModeAuto also applies. The
	// sort is by rrfScore desc, node_id asc (determinism). Diversification
	// still applies.
	ModeFusionNoGraph
)

// Request is one retrieval call (AC-1).
type Request struct {
	// Query is the free-text question.
	Query string
	// Limit caps the rows in the top Result.Rows; rows beyond the cap are
	// demoted below it (a larger Limit still reaches them). <=0 selects
	// limitDefault.
	Limit int
	// BudgetHint is the soft token budget for the row set (advisory; the
	// caller is the contract surface that enforces a real budget).
	BudgetHint int
	// Mode selects the retrieval mode (ModeAuto by default).
	Mode Mode
}

// State is the typed state the retrieval module reports on Result.Degradation.
// It mirrors the SW-261 GenerationStore vocabulary plus the explicit "ready"
// state for the configured-and-served path, so the consumer never has to
// reason about two orthogonal states.
type State string

const (
	// StateReady: semantic candidates available and served.
	StateReady State = "ready"
	// StateLexicalOnly: no embedder configured; lexical-only rows.
	StateLexicalOnly State = "lexical_only"
	// StateGenerationMissing: embedder configured but no active generation.
	StateGenerationMissing State = "generation_missing"
	// StateGenerationStale: active generation exists but does not match the
	// requested fingerprint (model, schema, chunker or graph generation
	// drifted). Lexical-only rows.
	StateGenerationStale State = "generation_stale"
	// StateGenerationCorrupt: active generation exists, fingerprint matches,
	// but validation failed. Lexical-only rows.
	StateGenerationCorrupt State = "generation_corrupt"
)

// Explain is the per-row scoring breakdown (AC-1). All fields are integers;
// the scoring path is integer-only by AC-2 / AC-3. A field that does not
// apply to the row (e.g. SemanticRank when there were no semantic
// candidates) is zero, not nil — zero is the typed "did not contribute"
// value and is distinct from a real zero contribution.
type Explain struct {
	// LexicalRank is 1-based over the lexical candidate list, 0 when the row
	// is semantic-only.
	LexicalRank int
	// SemanticRank is 1-based over the semantic candidate list, 0 when the
	// row is lexical-only.
	SemanticRank int
	// RRF is the integer Reciprocal Rank Fusion contribution:
	// scale / (rrfK + rank) per source, summed over the sources that
	// contributed to the row (lexical, semantic, or both).
	RRF int
	// Graph is the bounded rerank contribution (integer): the segment /
	// prefix / path / full-coverage / definition-bonus / classification
	// penalty / degree-point score from the audited integer signals.
	Graph int
	// Classification is the integer penalty applied for vendor / generated
	// paths (negative or zero — never positive, never a "bonus").
	Classification int
	// Final is the final integer score (RRF + Graph + Classification).
	Final int
}

// Row is one ranked retrieval result (AC-1).
type Row struct {
	// NodeID is the canonical model node id; unique across the row set.
	NodeID string
	// DocumentID is the embedding-space document id when the row came from
	// semantic (the SW-260 SemanticDocument.DocumentID) or "" when lexical.
	DocumentID string
	// Path is the repo-relative source path.
	Path string
	// Span is "start_line-end_line" (e.g. "12-34"). engine-owned
	// serialization, never the literal parsed source.
	Span string
	// Explain is the integer scoring breakdown. Deterministic.
	Explain Explain
}

// Summary is the per-Result header (AC-1). All fields are engine-owned
// strings; rendering is the engine's responsibility, so a CLI/MCP/HTTP
// surface all see byte-identical bytes for the same retrieval.
type Summary struct {
	// RetrievalVersion stamps the retrieval method.
	RetrievalVersion string
	// WeightsHash is the short sha256 of the active rerank weight set, so a
	// reader can tell when the audit weights have changed.
	WeightsHash string
	// ModelFingerprint is the embedder fingerprint the semantic list was
	// built against (Fingerprint.Canonical), or "" on the lexical-only path.
	ModelFingerprint string
	// IndexFingerprint is the GenerationStore fingerprint the semantic list
	// was loaded from (the active generation's Fingerprint.Canonical), or
	// "" on the lexical-only path.
	IndexFingerprint string
	// Query is the request query, echoed back for byte parity with the
	// engine/search.SemanticResponse shape.
	Query string
	// Limit is the applied Limit.
	Limit int
	// CandidateK, RRFk, RRFScale, MaxPerFile are the pinned arithmetic
	// constants echoed into the summary so a reader of the bytes can
	// verify them without consulting the source.
	CandidateK int
	RRFk       int
	RRFScale   int
	MaxPerFile int
}

// Result is one retrieval's outcome (AC-1).
type Result struct {
	Rows        []Row
	Summary     Summary
	Degradation State
}

// LexicalProvider is the lexical candidate source. It is satisfied by
// engine/search.Service. Limit is the per-source top-k.
type lexicalProvider interface {
	search(ctx context.Context, query string, limit int) ([]lexicalHit, error)
}

// SemanticProvider is the semantic candidate source. It is satisfied by
// engine/search.Service via its typed SemanticSearch method. nil means "no
// semantic path" (the default build); a non-nil provider whose Configured()
// reports false is treated the same way.
type semanticProvider interface {
	// Search returns the typed semantic outcome: hits (when ready) or the
	// typed unavailable reason. It never returns an error on the typed
	// unavailable path — only an infrastructure error can produce one.
	search(ctx context.Context, query string, limit int) (semanticOutcome, error)
}

// lexicalHit is the minimal lexical candidate the union stage needs:
// node identity + provenance. Score is the pre-computed rerank score the
// lexical provider (HybridSearchBridge) returns from search_hybrid —
// it carries over so the retrieval's rerank stage can reuse it without
// recomputation when the semantic path is empty (the AC-7 byte-parity
// path). A non-delegating provider (tests) may leave Score at 0; the
// rerank stage then computes it from the row's kind/qualified_name/path
// and the graph's degree signal.
type lexicalHit struct {
	NodeID        string
	Kind          string
	QualifiedName string
	Path          string
	Line          int
	Column        int
	// Score is the lexical provider's rerank score (the audit score
	// search_hybrid produces). The retrieval's rerank stage adds RRF
	// and any semantic contribution on top of it; the lexical-only
	// byte-parity path keeps Final == Score.
	Score int
}

// semanticHit is the minimal semantic candidate the union stage needs.
// CosineScore is the float64 the embedder returned; the retrieval module
// quantises it to an int before ordering (AC-3).
type semanticHit struct {
	NodeID        string
	Kind          string
	QualifiedName string
	Path          string
	Line          int
	Column        int
	DocumentID    string
	CosineScore   float64
}

// semanticOutcome is the typed result SemanticProvider.Search returns.
type semanticOutcome struct {
	// Available is true when the embedder is configured and the active
	// generation is ready. When false, Reason names the typed state and
	// Hits is empty.
	Available bool
	// Reason is the user-visible typed reason (the SW-261 vocabulary).
	Reason string
	// Hits is the ranked semantic candidate list (cosine score desc, then
	// node_id asc) — never nil when Available.
	Hits []semanticHit
	// State is the typed retrieval State this outcome implies.
	State State
}

// rerankWeights is the integer weight set the rerank stage applies. The
// field names are the wire names the summary's weightsHash digests.
//
// AC-4 (SW-263 review / item 3): the set the rerank APPLIES and the set
// the audit hash stamps MUST be the same object — a stamp that does not
// describe the arithmetic it stamps is worse than no stamp. The previous
// implementation read hybridsearch.DefaultWeights at the call site
// (which includes NameSubstring) but hashed a smaller struct that
// omitted it, so a tuned NameSubstring would move rankings without
// moving the audit hash. We carry every integer weight the rerank uses
// in this struct, including NameSubstring, so the hash is a complete
// fingerprint of the active arithmetic.
type rerankWeights struct {
	SegmentExact     int `json:"segment_exact"`
	SegmentPrefix    int `json:"segment_prefix"`
	NameSubstring    int `json:"name_substring"`
	PathSegment      int `json:"path_segment"`
	FullCoverage     int `json:"full_coverage"`
	DefinitionBonus  int `json:"definition_bonus"`
	VendorPenalty    int `json:"vendor_penalty"`
	GeneratedPenalty int `json:"generated_penalty"`
	DegreePoint      int `json:"degree_point"`
}

// defaultRerankWeights reuses the audited hybridsearch integer signals
// (SegmentExact, SegmentPrefix, NameSubstring, PathSegment, FullCoverage,
// DegreePoint) at their published values; adds a definition bonus and a
// vendor/generated penalty. The hybridsearch constants are imported
// verbatim so the two paths share the same audited scoring discipline.
// Every field here is read by the rerank at apply-time AND hashed into
// Summary.weightsHash, so a tuning pass that moves one number moves
// rankings AND the audit hash together.
var defaultRerankWeights = rerankWeights{
	SegmentExact:     100,
	SegmentPrefix:    40,
	NameSubstring:    15,
	PathSegment:      30,
	FullCoverage:     50,
	DefinitionBonus:  20,
	VendorPenalty:    -25,
	GeneratedPenalty: -25,
	DegreePoint:      2,
}

// weightsHash is the auditable stamp of the active weight model. It is the
// short sha256 (first 8 hex bytes, 16 chars) of the JSON encoding of the
// weight struct — the same audit discipline hybridsearch.weightsHash uses
// (so a caller comparing the two hashes sees the same shape).
func weightsHash() string {
	b, _ := json.Marshal(defaultRerankWeights)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:8]
}

// weightsSortedCopy returns the rerank weights sorted by JSON field name
// (so the SHA in weightsHash is independent of struct field order).
func weightsSortedCopy(w rerankWeights) []byte {
	// Marshal with sorted keys via an intermediate map. The map MUST
	// carry every field the rerank actually applies (including
	// NameSubstring) — see the AC-4 audit-discipline comment on
	// rerankWeights for why this list and the apply-time struct must
	// be the same.
	m := map[string]int{
		"definition_bonus":  w.DefinitionBonus,
		"degree_point":      w.DegreePoint,
		"full_coverage":     w.FullCoverage,
		"generated_penalty": w.GeneratedPenalty,
		"name_substring":    w.NameSubstring,
		"path_segment":      w.PathSegment,
		"segment_exact":     w.SegmentExact,
		"segment_prefix":    w.SegmentPrefix,
		"vendor_penalty":    w.VendorPenalty,
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf []byte
	buf = append(buf, '{')
	for i, k := range keys {
		if i > 0 {
			buf = append(buf, ',')
		}
		kb, _ := json.Marshal(k)
		buf = append(buf, kb...)
		buf = append(buf, ':')
		vb, _ := json.Marshal(m[k])
		buf = append(buf, vb...)
	}
	buf = append(buf, '}')
	return buf
}

// weightsHashOf computes weightsHash for an arbitrary weight struct. It is
// the same algorithm as weightsHash() but over an arbitrary value, so a
// test can assert the hash is stable under field reordering.
func weightsHashOf(w rerankWeights) string {
	b := weightsSortedCopy(w)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:8]
}

// New builds the deep retrieval module over the existing engine seams. The
// lexical argument is the same dependency set search_hybrid consumes, semantic
// is the existing search.Service whose SemanticSearch method owns generation
// state, and graph is the existing bounded graphstore read seam. All adapters,
// candidate types and ranking stages remain implementation details (AC-1).
//
// New panics when the always-available lexical seam is incomplete. semantic
// and graph may be nil: that is the default lexical-only composition.
func New(lexical resolve.Deps, semantic *search.Service, graph graphstore.BoundedGraphLookup) *engine {
	if !lexical.Available() || lexical.Search == nil {
		panic("retrieval: New: lexical dependencies are unavailable")
	}
	var sem semanticProvider
	if semantic != nil {
		sem = &searchServiceBridge{service: semantic}
	}
	return newEngine(&hybridSearchBridge{deps: lexical}, sem, newGraphReader(graph))
}

// newEngine is the private construction seam used by stage tests. Production
// callers cross New and cannot supply implementation-shaped candidates or
// adapters, which keeps the module's external interface deep.
func newEngine(lexical lexicalProvider, semantic semanticProvider, graph graphReader) *engine {
	if lexical == nil {
		panic("retrieval: New: lexical provider is nil")
	}
	return &engine{
		lexical:  lexical,
		semantic: semantic,
		graph:    graph,
	}
}

// GraphReader is the optional narrow read dependency a non-delegating
// lexical pipeline (tests) uses for the rerank's degree signal. nil
// disables the degree signal — the delegating HybridSearchBridge does
// not need this for lexical rows because search_hybrid computes degree
// internally (its score is carried as the row's lexicalScore, which
// the rerank stage adopts unaltered). For semantic-only rows the
// bridge never runs, so without a non-nil GraphReader semantic-only
// rows in ModeAuto would receive a zero degree contribution — the
// production composition must therefore wire a non-nil GraphReader
// when the semantic path is active, so semantic-only candidates
// receive the same bounded degree boost lexical-only rows already get
// via the delegating bridge (SW-263 / decision-ac9 defect 3).
type graphReader interface {
	inboundDegree(ctx context.Context, id string, cap int) (int, error)
}

// BoundedDegreeReader is the minimal port the production composition
// passes through NewGraphReader. graphstore.BoundedGraphLookup
// satisfies it (SQLiteStore, MemStore), so the production
// composition can hand the existing graphstore.Graphstore to
// NewGraphReader without introducing a parallel read port. The
// retrieval module's interface surface stays dependency-light; the
// adapter is the only place that imports graphstore for the degree
// read.
// newGraphReader adapts graphstore.BoundedGraphLookup into the graphReader
// the retrieval module consumes. The adapter is the single seam
// between the retrieval module and the graphstore port; tests can
// pass a hand-rolled fake, production code passes the store via
// WithGraphReader at composition time.
func newGraphReader(src graphstore.BoundedGraphLookup) graphReader {
	if src == nil {
		return nil
	}
	return &degreeAdapter{src: src}
}

// degreeAdapter implements GraphReader by counting the edges the
// bounded incident-edge read returns. The cap is the same cap the
// rerank uses (32, matching search_hybrid's degreeReadCap).
type degreeAdapter struct {
	src graphstore.BoundedGraphLookup
}

func (d *degreeAdapter) inboundDegree(ctx context.Context, id string, cap int) (int, error) {
	if d == nil || d.src == nil {
		return 0, nil
	}
	edges, _, err := d.src.IncomingBounded(ctx, model.NodeId(id), cap)
	if err != nil {
		return 0, err
	}
	return len(edges), nil
}

// compile-time guard: every backend the harness ships satisfies
// BoundedDegreeReader through BoundedGraphLookup, so a future
// backend that forgets to will fail the build at this line.
var (
	_ graphstore.BoundedGraphLookup = (*graphstore.SQLiteStore)(nil)
)

// engine is the retrieval module's concrete implementation. New returns it so
// callers can invoke its sole exported method without making the type itself
// part of the package interface.
type engine struct {
	lexical  lexicalProvider
	semantic semanticProvider
	graph    graphReader
}

// Retrieve is the single public entry point (AC-1).
func (e *engine) Retrieve(ctx context.Context, req Request) (Result, error) {
	req = normaliseRequest(req)
	state, semHits, semFP, idxFP := e.semanticOutcome(ctx, req)
	lexHits, err := e.lexical.search(ctx, req.Query, candidateK)
	if err != nil {
		return Result{}, err
	}
	rows := e.union(req.Query, lexHits, semHits)
	// semanticActive drives the RRF stage: when the semantic list is
	// globally absent (no embedder, configured-but-not-ready
	// generation, or the caller pinned ModeLexicalOnly), RRF scores
	// zero across the whole row set so the rerank's lexical score
	// carries every row's Final unaltered — the AC-7 byte-parity
	// invariant. When the semantic list is active, RRF pays out per
	// contributing source so a lexical-only or semantic-only row still
	// receives its single-source contribution (AC-2's union, not an
	// intersection filter).
	//
	// isExactQuery is AC-6's rule, applied here because this is the one
	// stage where the two sources are weighed against each other: on an
	// exact identifier / path query the semantic term collapses to a
	// bounded tie-break so lexical rank dominates.
	rows = e.rrf(rows, state == StateReady, isExactQuery(req.Query))
	semanticActive := state == StateReady
	rows = e.applyRerankAndDiversify(ctx, req, rows, semanticActive)
	res := Result{
		Rows:        finaliseRows(rows, req.Limit),
		Degradation: state,
		Summary: Summary{
			RetrievalVersion: retrievalVersion,
			WeightsHash:      weightsHash(),
			ModelFingerprint: semFP,
			IndexFingerprint: idxFP,
			Query:            req.Query,
			Limit:            req.Limit,
			CandidateK:       candidateK,
			RRFk:             rrfK,
			RRFScale:         rrfScale,
			MaxPerFile:       maxPerFile,
		},
	}
	return res, nil
}

// normaliseRequest fills in defaults. The zero Request is ModeAuto with the
// default limit.
func normaliseRequest(req Request) Request {
	if req.Limit <= 0 {
		req.Limit = limitDefault
	}
	return req
}

// applyRerankAndDiversify is the post-RRF stage dispatcher. ModeAuto and
// ModeLexicalOnly and ModeSemanticRequired all run the full RRF + rerank
// + diversify pipeline (AC-1..AC-5). ModeFusionNoGraph skips the
// bounded rerank so the row ordering reflects the union+RRF contribution
// alone — the "fusion" ablation in the AC-9 eval harness. In that mode
// graphScore and classScore are zero on every row, finalScore equals
// rrfScore, and the sort is rrfScore desc, node_id asc (deterministic).
// Diversification still applies because AC-5 is independent of the
// rerank stage.
func (e *engine) applyRerankAndDiversify(ctx context.Context, req Request, rows []row, semanticActive bool) []row {
	if req.Mode == ModeFusionNoGraph {
		for i := range rows {
			rows[i].graphScore = 0
			rows[i].classScore = 0
			rows[i].finalScore = rows[i].rrfScore
		}
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].finalScore != rows[j].finalScore {
				return rows[i].finalScore > rows[j].finalScore
			}
			return rows[i].nodeID < rows[j].nodeID
		})
		return e.diversify(rows, req.Limit)
	}
	rows = e.rerank(ctx, req.Query, rows, semanticActive)
	return e.diversify(rows, req.Limit)
}

// semanticOutcome runs the semantic path (if any) and returns the typed
// state, the ranked semantic candidates, and the model / index
// fingerprints the summary needs. A nil semantic provider yields a
// lexical-only state with no fingerprint. A non-nil provider that reports
// not Available yields the typed degraded state.
//
// The Search call is made even when the provider's Available() returns
// false: the typed unavailable envelope (missing / stale / corrupt) lives
// in the Search result, and the retrieval's Degradation must carry the
// precise state to the consumer — calling only when Available() would
// collapse the four SW-261 states into one undifferentiated lexical-only.
func (e *engine) semanticOutcome(ctx context.Context, req Request) (state State, hits []semanticHit, modelFP, indexFP string) {
	state = StateLexicalOnly
	if e.semantic == nil {
		return
	}
	if req.Mode == ModeLexicalOnly {
		// Caller pinned lexical-only: state stays lexical; no semantic
		// candidates consulted even when the embedder is configured.
		return
	}
	out, err := e.semantic.search(ctx, req.Query, candidateK)
	if err != nil {
		// An infrastructure error on the semantic path is treated as a
		// lexical-only retrieval with no error surfaced (AC-7 fail-soft).
		// The error is logged to stderr; the retrieval still answers over
		// lexical candidates. This is the conservative posture: a broken
		// embedder must not break the lexical path that the default build
		// has always answered.
		mustStderr("retrieval: semantic search failed: %v\n", err)
		return
	}
	if !out.Available {
		state = out.State
		return
	}
	state = StateReady
	hits = out.Hits
	if bp, ok := e.semantic.(interface{ fingerprints() (model, index string) }); ok {
		modelFP, indexFP = bp.fingerprints()
	}
	return
}

// finaliseRows demotes rows beyond Limit. Diversify already ordered rows
// with capped-by-path rows LAST (after the in-cap rows); we trim to Limit.
func finaliseRows(rows []row, limit int) []Row {
	if len(rows) <= limit {
		out := make([]Row, len(rows))
		for i, r := range rows {
			out[i] = r.toRow()
		}
		return out
	}
	out := make([]Row, limit)
	for i := 0; i < limit; i++ {
		out[i] = rows[i].toRow()
	}
	return out
}
