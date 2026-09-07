// Package retrieval is the deep retrieval module that backs graphi's semantic
// search. Its shipped ModeAuto owns candidate composition end to end:
//
//   - natural-language queries jointly rank lexical and semantic candidates,
//     including bounded direct-callee expansion before the result limit;
//   - non-ready semantic states return the delegated lexical rows unchanged,
//     with their exact typed state and repair reason;
//   - exact paths use the lexical list; actual identifier equality takes
//     precedence over semantic similarity, without promoting approximate names;
//   - a typed, byte-stable Explain block per row (LexicalRank, SemanticRank,
//     Base, RRF, Graph, Classification, Final) and a typed Summary stamped with
//     the retrieval version, weights hash, model fingerprint and index
//     fingerprint so the bytes are reproducible across runs and architectures.
//
// The package consumes the two existing engine seams — engine/search.Service
// for lexical (and the same service for SemanticSearch over the active
// GenerationStore generation, AC-7) — and the graphstore the lexical service
// reads from, for path/line/span provenance and evaluator-only graph reranking.
// It introduces NO new port and NO module-kernel contribution: it is
// composed once at cmd/internal/runtime.Composition.Client() and handed to
// both consumers in SW-264. Symmetric RRF and its graph rerank remain behind
// explicit evaluator-only modes and have no production request mapping.
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
	"github.com/samibel/graphi/engine/agenttools/hybridsearch"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// retrievalVersion stamps the retrieval method (per AC-1 Summary). It is the
// audit value that lets a downstream caller tell the SW-263 release from any
// later revision that breaks serialization.
//
// retrieval/3 added exact-name precedence to the semantic-first strategy.
// retrieval/4 (SW-282) replaces the natural-language sub-dispatch's
// immutable semantic prefix with a shared candidate scoring pass over the
// lexical+semantic union (engine/retrieval/flow.go), plus a bounded
// wrapper->callee expansion that admits a top candidate's direct callees
// before the Top-K cut. Exact-identifier and exact-path handling and the
// evaluator-only fusion controls are unchanged.
// retrieval/5 preserves /4's ranking, exposes its complete score arithmetic,
// hashes its active weights and propagates bounded graph-read errors.
// retrieval/6 preserves the evidence score and makes lexical candidate
// admission term-order independent through search_hybrid/1.1.
// retrieval/7 adds a single deterministic tail view for explicit
// initialization-function lifecycle questions.
const retrievalVersion = "retrieval/7"

// Version is the exported form of retrievalVersion. SW-264 stamps it on
// every search_hybrid/2 and task_context/2 summary so a reader of the
// bytes can verify the audit signal against the SW-263 release.
const Version = retrievalVersion

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
	// maxPerFile is the lexical-backfill admission threshold. Counts are
	// seeded from the complete semantic prefix; the prefix itself is never
	// reordered or removed by this threshold.
	maxPerFile = 3
	// limitDefault is the default Result.Limit when Request.Limit <= 0.
	limitDefault = 20
)

// Mode is the retrieval mode (AC-1 Request.Mode). The default (zero value)
// ModeAuto is the shipped ready pipeline. Natural-language requests use shared
// evidence ranking; exact identifiers and paths have dedicated dispatches.
// On any non-ready state it returns lexical-only rows unchanged.
//
// The two fusion modes (ModeFusionNoGraph, ModeFusionGraph) are
// evaluator-only — they implement symmetric RRF over the lexical+semantic
// union, with or without the bounded graph rerank. The reviewer's AC-1
// requires that no production composition, client, CLI, MCP, HTTP,
// `search_hybrid/2`, or `task_context/2` path may select them; the eval
// harness is the only surface that does. Their values are kept stable so
// the diff tool (cmd/differential) and the AC-9 ablation baselines can
// still pin and select them.
//
// ModeLexicalOnly is the no-semantic-considered path used by the
// "chunk-only" ablation and by `modeLexicalOnly` whenever the
// semantic generation is unavailable.
//
// ModeSemanticRequired is the strict semantic-first shape that refuses
// to degrade: it returns the typed unavailability response (no rows, no
// error) when the semantic generation is not ready, and otherwise runs
// the same pipeline as ModeAuto.
type Mode int

const (
	// ModeAuto selects shared evidence ranking for natural language, exact
	// equality precedence for names, and lexical order for paths. Non-ready
	// states preserve delegated lexical rows (the AC-7 fallback contract).
	ModeAuto Mode = iota
	// ModeLexicalOnly: ignore semantic candidates regardless of state.
	// Used by the "chunk-only" ablation in the AC-9 eval harness.
	ModeLexicalOnly
	// ModeSemanticRequired: refuse to answer if the semantic generation
	// is not ready — typed unavailability response with the typed reason,
	// no error. When ready it runs the same pipeline as ModeAuto.
	ModeSemanticRequired
	// ModeFusionNoGraph is the EVALUATOR-ONLY symmetric RRF fusion
	// ablation: lexical+semantic union + integer RRF + classification
	// penalty + diversify, WITHOUT the bounded graph rerank. No
	// production surface may select this mode.
	ModeFusionNoGraph
	// ModeFusionGraph is the EVALUATOR-ONLY symmetric RRF fusion ablation
	// plus the bounded graph rerank the SW-263 rerank stage applies.
	// No production surface may select this mode. The `fusion+graph`
	// eval baseline selects it.
	ModeFusionGraph
)

// Request is one retrieval call (AC-1).
type Request struct {
	// Query is the free-text question.
	Query string
	// Limit caps Result.Rows. <=0 selects limitDefault.
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
	// Base is the quantised semantic score, or the documented candidate-pool
	// floor/inherited caller base on the evidence-ranked natural-language path.
	// Zero on legacy exact/path/fallback and evaluator-only fusion paths.
	Base int `json:",omitempty"`
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
	// On evidence-ranked rows it is name/callee support above Base, including
	// the caller ceiling; it can therefore be negative when that ceiling binds.
	Graph int
	// Classification is the integer penalty applied for vendor / generated
	// paths (negative or zero — never positive, never a "bonus").
	Classification int
	// Final is the final integer score (Base + RRF + Graph + Classification).
	Final int
}

// Row is one ranked retrieval result (AC-1, AC-11).
type Row struct {
	// NodeID is the canonical model node id; unique across the row set.
	NodeID string
	// DocumentID is the embedding-space document id when the row came from
	// semantic (the SW-260 SemanticDocument.DocumentID) or "" when lexical.
	// It is provenance, NOT the cross-channel dedupe key: one canonical
	// node_id may appear only once even if semantic records disagree on
	// document_id.
	DocumentID string
	// Path is the repo-relative source path.
	Path string
	// Span is "start_line-end_line" (e.g. "12-34"). engine-owned
	// serialization, never the literal parsed source.
	Span string
	// Region is the AC-11 audit tag the pipeline stamps on every row to
	// record provenance. Natural-language rows use "evidence_ranked";
	// exact-name equality uses "exact_identifier". The following legacy tags
	// still apply to the other dispatches.
	// record how it entered the result. The shipped semantic-first mode
	// stamps "semantic_prefix" on rows the AC-3 quantised semantic list
	// emitted and "lexical_backfill" on rows the delegated hybrid_v1
	// candidates supplied to fill unfilled positions. Lexical-only and
	// degraded paths stamp "lexical_only". An exact-path query (one
	// matching the documented isExactPath rule) returns the delegated
	// lexical list unchanged with region "lexical_path_override".
	// Evaluator-only fusion modes stamp "fused". An empty string means
	// the region tag did not apply to the strategy that produced the
	// row (a forward-compatible default that lets new modes omit the
	// stamp).
	Region string
	// Explain is the integer scoring breakdown. Deterministic.
	Explain Explain
}

// Summary is the per-Result header (AC-1, AC-11). All fields are engine-owned
// strings; rendering is the engine's responsibility, so a CLI/MCP/HTTP
// surface all see byte-identical bytes for the same retrieval.
type Summary struct {
	// RetrievalVersion stamps the retrieval method. retrieval/1 was
	// symmetric-RRF fusion; retrieval/2 is semantic-first.
	RetrievalVersion string
	// Strategy is the historical pipeline dispatch label. RetrievalVersion
	// and Row.Region identify the concrete ordering revision. One of:
	//   "semantic_first" — ModeAuto / ModeSemanticRequired on a ready
	//     generation. This compatibility label does NOT promise an immutable
	//     semantic prefix in retrieval/4 and later: natural-language rows use
	//     evidence ranking, while exact names and paths have dedicated rules;
	//   "lexical_only" — ModeLexicalOnly, or any shipped mode whose
	//     semantic generation is not ready (the AC-7 byte parity path);
	//   "fusion_no_graph" — ModeFusionNoGraph (evaluator-only);
	//   "fusion_graph" — ModeFusionGraph (evaluator-only);
	//   "unavailable" — ModeSemanticRequired with a non-ready semantic
	//     generation; the result carries zero rows and the typed state.
	// A reader of the bytes can verify the strategy against
	// Summary.RetrievalVersion and Summary.Degradation without consulting
	// the source.
	Strategy string
	// WeightsHash is the short sha256 of the active rerank weight set, so a
	// reader can tell when the audit weights have changed. Stamped on every
	// result for traceability even when the rerank signals did not apply
	// (AC-11 truthfulness: the hash is the audit discipline, not a claim
	// that the weights influenced the order).
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
	// CandidateK, RRFk and RRFScale are the pinned candidate/fusion constants.
	// MaxPerFile is the semantic-first lexical-backfill admission threshold.
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
	// Reason is the exact semantic unavailability reason. It is empty on
	// ready results and explicit lexical-only requests; degraded ModeAuto
	// results preserve the provider's repair text verbatim.
	Reason string
}

// LexicalProvider is the lexical candidate source. It is satisfied by
// engine/search.Service. Limit is the per-source top-k.
type lexicalProvider interface {
	search(ctx context.Context, query string, limit int) ([]lexicalHit, error)
}

// readyLexicalProvider optionally supplies the term-balanced candidate method
// for a ready semantic generation. Non-ready and explicit lexical-only paths
// continue through lexicalProvider.search, preserving search_hybrid/1 bytes.
type readyLexicalProvider interface {
	searchReady(ctx context.Context, query string, limit int) ([]lexicalHit, error)
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
	b, _ := json.Marshal(struct {
		Rerank                                                      rerankWeights
		NameTerm, WrapperWidth, CalleeCap, CalleeDecay, FloorMargin int
		LifecycleFocusWords                                         int
		LexicalMethod                                               string
	}{defaultRerankWeights, nameTermWeight, wrapperExpansionWidth, calleeExpansionCap, calleeBaseDecay, lexicalFloorMargin, lifecycleFocusWords, hybridsearch.FairCandidateMethodVersion})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:8]
}

// WeightsHash is the exported form of weightsHash. SW-264 stamps it on
// every search_hybrid/2 and task_context/2 summary so a reader of the
// bytes can verify the audit weights have not changed under the read.
func WeightsHash() string { return weightsHash() }

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
//
// calleeCandidates is the SW-282 bounded wrapper->callee expansion seam
// (engine/retrieval/flow.go): a second selective graph read per top
// candidate — never a whole-graph scan (docs/adr/0003-selective-read-contract.md).
// A reader with no node-hydration capability returns (nil, nil); callee
// expansion then degrades to a no-op, the same posture inboundDegree
// takes when graph is nil.
type graphReader interface {
	inboundDegree(ctx context.Context, id string, cap int) (int, error)
	calleeCandidates(ctx context.Context, id string, limit int) ([]calleeCandidate, error)
}

// calleeCandidate is the minimal row-shaped metadata calleeCandidates
// returns for one bounded direct "calls" callee.
type calleeCandidate struct {
	NodeID        string
	Kind          string
	QualifiedName string
	Path          string
	Line          int
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

// calleeCandidates implements the SW-282 bounded wrapper->callee expansion
// read: at most `limit` direct "calls" edges out of id, hydrated through the
// same NodesByID batch read taskctx's v2 neighbor hop already uses. Every
// backend the harness ships (SQLiteStore, MemStore) satisfies both
// BoundedGraphLookup (the declared port) and GraphLookup (asserted here,
// exactly as documented on the graphReader/calleeCandidates doc comment);
// a hypothetical future backend that only satisfies the bounded port
// degrades to (nil, nil) rather than erroring.
func (d *degreeAdapter) calleeCandidates(ctx context.Context, id string, limit int) ([]calleeCandidate, error) {
	if d == nil || d.src == nil || limit <= 0 {
		return nil, nil
	}
	edges, _, err := d.src.OutgoingBounded(ctx, model.NodeId(id), limit, query.EdgeKindCalls)
	if err != nil {
		return nil, err
	}
	if len(edges) == 0 {
		return nil, nil
	}
	lookup, ok := d.src.(graphstore.GraphLookup)
	if !ok {
		return nil, nil
	}
	ids := make([]model.NodeId, len(edges))
	for i, e := range edges {
		ids[i] = e.To()
	}
	nodes, err := lookup.NodesByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[model.NodeId]model.Node, len(nodes))
	for _, n := range nodes {
		byID[n.ID()] = n
	}
	out := make([]calleeCandidate, 0, len(edges))
	for _, e := range edges {
		n, ok := byID[e.To()]
		if !ok || n.SourcePath() == "" {
			continue
		}
		out = append(out, calleeCandidate{
			NodeID:        string(n.ID()),
			Kind:          n.Kind(),
			QualifiedName: n.QualifiedName(),
			Path:          n.SourcePath(),
			Line:          n.Line(),
		})
	}
	return out, nil
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
//
// Dispatch (SW-263 owner decision 2026-09-01):
//
//   - ModeLexicalOnly: pure lexical. No semantic candidates consulted.
//   - ModeSemanticRequired: if the semantic generation is not ready,
//     return the typed unavailability response (no rows, no error).
//     Otherwise identical to ModeAuto.
//   - ModeAuto (the SHIPPED mode): shared evidence ranking for natural
//     language, with dedicated exact-name/path rules. When not ready, the
//     result is the delegated lexical list unchanged (the AC-7
//     byte parity path). On the ready path an exact-PATH query
//     (one matching the documented isExactPath rule) returns the
//     delegated lexical list L unchanged under the path-override
//     sub-dispatch, stamped with the path-override region. An actual
//     identifier equality match is promoted ahead of approximate matches.
//   - ModeFusionNoGraph / ModeFusionGraph: EVALUATOR-ONLY symmetric
//     RRF fusion, with or without the bounded graph rerank. No
//     production surface may select these.
func (e *engine) Retrieve(ctx context.Context, req Request) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	req = normaliseRequest(req)
	state, reason, semHits, semFP, idxFP := e.semanticOutcome(ctx, req)
	if state == StateReady && (req.Mode == ModeAuto || req.Mode == ModeSemanticRequired) {
		if focused := lifecycleFocusedQuery(req.Query); focused != "" {
			out, focusErr := e.semantic.search(ctx, focused, candidateK)
			if focusErr != nil {
				mustStderr("retrieval: lifecycle semantic search failed: %v\n", focusErr)
			} else if out.Available {
				semHits = mergeFocusedSemanticHits(semHits, out.Hits)
			}
		}
	}
	var lexHits []lexicalHit
	var err error
	if state == StateReady && req.Mode != ModeLexicalOnly {
		if ready, ok := e.lexical.(readyLexicalProvider); ok {
			lexHits, err = ready.searchReady(ctx, req.Query, candidateK)
		} else {
			lexHits, err = e.lexical.search(ctx, req.Query, candidateK)
		}
	} else {
		lexHits, err = e.lexical.search(ctx, req.Query, candidateK)
	}
	if err != nil {
		return Result{}, err
	}
	rows, strategy, err := e.dispatch(ctx, req, state, lexHits, semHits)
	if err != nil {
		return Result{}, err
	}
	res := Result{
		Rows:        finaliseRows(rows, req.Limit),
		Degradation: state,
		Reason:      reason,
		Summary: Summary{
			RetrievalVersion: retrievalVersion,
			Strategy:         strategy,
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

// dispatch routes the request to the right strategy. It returns the
// ordered rows and the Summary.Strategy string the caller stamps on the
// result. The dispatch table is:
//
//	mode \ state         StateReady                non-Ready
//	ModeAuto             semantic-first            lexical_only (delegated L unchanged)
//	ModeLexicalOnly      lexical_only              lexical_only
//	ModeSemanticRequired semantic-first            unavailable (zero rows, no error)
//	ModeFusionNoGraph    fusion_no_graph           lexical_only (same row projection as the
//	                                                    non-fusion paths; cap bypassed)
//	ModeFusionGraph      fusion_graph              lexical_only
//
// AC-11 truthfulness: the strategy name on every Result records which
// branch ran, and every row's Region records how it entered the
// pipeline (semantic_prefix / lexical_backfill / lexical_only /
// lexical_path_override / exact_identifier / fused).
//
// On the ready path the shipped ModeAuto (and ModeSemanticRequired) takes
// a further sub-dispatch: an exact-PATH query — a query matching the
// documented isExactPath constant — returns the delegated lexical list
// unchanged under the semantic-first strategy. Identifier-shaped queries
// promote actual qualified-name/suffix equality, retaining semantic ordering
// for approximate matches. No dataset-specific name or path enters dispatch.
func (e *engine) dispatch(ctx context.Context, req Request, state State, lexHits []lexicalHit, semHits []semanticHit) ([]row, string, error) {
	switch req.Mode {
	case ModeLexicalOnly:
		return e.lexicalOnlyRows(lexHits), strategyLexicalOnly, nil
	case ModeFusionNoGraph:
		if state != StateReady {
			// AC-7 fallback: an experimental fusion mode on a non-ready
			// semantic state is the same lexical output as ModeLexicalOnly.
			// The cap is bypassed (AC-5 vs AC-7 — the amendment scopes AC-5
			// to the semantic/fused path; the evaluator's degraded-state
			// measurements report unavailable instead).
			return e.lexicalOnlyRows(lexHits), strategyLexicalOnly, nil
		}
		return e.fusionRows(ctx, req, lexHits, semHits, false /* withGraph */), strategyFusionNoGraph, nil
	case ModeFusionGraph:
		if state != StateReady {
			return e.lexicalOnlyRows(lexHits), strategyLexicalOnly, nil
		}
		return e.fusionRows(ctx, req, lexHits, semHits, true /* withGraph */), strategyFusionGraph, nil
	case ModeSemanticRequired:
		if state != StateReady {
			// Refuse: typed unavailability, no rows, no error. The caller
			// is told via Summary.Strategy == "unavailable" that the result
			// is intentionally empty.
			return nil, strategyUnavailable, nil
		}
		return e.readyDispatch(ctx, req, lexHits, semHits)
	default: // ModeAuto
		if state != StateReady {
			// AC-7 fallback: the shipped ModeAuto returns the delegated
			// lexical list unchanged when the semantic generation is not
			// ready. The bytes are identical to search_hybrid's audit
			// output (AC-7) and to ModeLexicalOnly.
			return e.lexicalOnlyRows(lexHits), strategyLexicalOnly, nil
		}
		return e.readyDispatch(ctx, req, lexHits, semHits)
	}
}

// readyDispatch shares the exact-path, exact-name and natural-language rules
// between ModeAuto and ModeSemanticRequired. The historical strategy label is
// retained; the version and per-row regions identify the applied algorithm.
func (e *engine) readyDispatch(ctx context.Context, req Request, lexHits []lexicalHit, semHits []semanticHit) ([]row, string, error) {
	if isExactPath(req.Query) {
		// Paths retain the delegated lexical ranking, with an explicit region.
		return e.lexicalPathOverrideRows(lexHits), strategySemanticFirst, nil
	}
	if isExactIdentifier(req.Query) {
		return e.exactNameFirstRows(req.Query, lexHits, semHits, req.Limit), strategySemanticFirst, nil
	}
	rows, err := e.naturalLanguageRows(ctx, req.Query, lexHits, semHits)
	return rows, strategySemanticFirst, err
}

// Strategy name constants (AC-11). They are package-private string
// literals so the package surface stays minimal; a reader of the
// Result's bytes can still match the value verbatim.
const (
	strategySemanticFirst = "semantic_first"
	strategyLexicalOnly   = "lexical_only"
	strategyFusionNoGraph = "fusion_no_graph"
	strategyFusionGraph   = "fusion_graph"
	strategyUnavailable   = "unavailable"
)

// Region name constants (AC-11). Same reasoning as the strategy
// constants above: engine-owned strings, package-private.
const (
	regionSemanticPrefix      = "semantic_prefix"
	regionLexicalBackfill     = "lexical_backfill"
	regionLexicalOnly         = "lexical_only"
	regionLexicalPathOverride = "lexical_path_override"
	regionFused               = "fused"
)

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
func (e *engine) semanticOutcome(ctx context.Context, req Request) (state State, reason string, hits []semanticHit, modelFP, indexFP string) {
	state = StateLexicalOnly
	if req.Mode == ModeLexicalOnly {
		// Caller pinned lexical-only: state stays lexical; no semantic
		// candidates consulted even when the embedder is configured.
		return
	}
	if e.semantic == nil {
		reason = search.UnavailableReason
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
		reason = out.Reason
		return
	}
	state = StateReady
	hits = out.Hits
	if bp, ok := e.semantic.(interface{ fingerprints() (model, index string) }); ok {
		modelFP, indexFP = bp.fingerprints()
	}
	return
}

// finaliseRows projects at most Limit already-ordered internal rows.
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
