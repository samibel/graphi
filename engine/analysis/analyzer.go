package analysis

import (
	"context"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// Direction selects the traversal direction for directional analyzers. For the
// impact analyzer it fixes the semantics, following the reverse-dependency
// (rdeps) convention used by build/dependency tooling:
//
//   - Forward — DEPENDENCIES (what this symbol relies on). Traverses OUTGOING
//     edges, i.e. ALONG edge direction (a call edge points caller → callee).
//   - Reverse — DEPENDENTS / blast radius (who is affected if this symbol
//     changes). Traverses INCOMING edges, i.e. AGAINST edge direction — a
//     dependent is a node with an edge into the seed. This is the DEFAULT for
//     impact when no direction is given.
//
// History: before v0.1.3 the two names were swapped relative to this (and to
// half the documentation), so `-direction reverse` returned dependencies. The
// v0.1.3 breaking change fixed the vocabulary once; it is documented here so
// every surface agrees.
type Direction string

const (
	// Forward — dependencies (outgoing-edge traversal, along edge direction).
	Forward Direction = "forward"
	// Reverse — dependents / blast radius (incoming-edge traversal). Default
	// for impact.
	Reverse Direction = "reverse"
)

// Params is the uniform input bag every analyzer reads through the single
// analysis.Dispatch entry point. Each analyzer reads the fields relevant to it
// and ignores the rest; fields are documented per analyzer. Keeping one typed
// struct (rather than per-analyzer parameter types) is what lets the registry
// and dispatch stay uniform and lets surfaces call any analyzer identically.
type Params struct {
	// Symbol is the primary symbol (node id) under analysis. Required by every
	// analyzer that resolves a symbol (impact, call-chain source, metrics).
	Symbol model.NodeId `json:"symbol"`
	// Target is the secondary symbol for analyzers that relate two symbols
	// (call-chain: the callee-side endpoint of the chain).
	Target model.NodeId `json:"target,omitempty"`
	// Concept is a lexical/symbol term for the concept resolver ("where is X
	// handled"). Unused by impact/metrics.
	Concept string `json:"concept,omitempty"`
	// Direction selects traversal direction for directional analyzers (impact).
	// Empty defaults to Reverse (dependents / blast radius).
	Direction Direction `json:"direction,omitempty"`
	// Kinds constrains the edge kinds traversed (impact). Empty = the default
	// dependency kinds {calls, references}. `defines` is containment rather than
	// dependency and must be requested explicitly.
	Kinds []string `json:"kinds,omitempty"`
	// MaxNodes bounds the number of reached/scored nodes returned (impact,
	// metrics). Zero/negative means the analyzer default cap. The bound is an
	// OUTPUT budget: the traversal is cycle-guarded and always terminates; when
	// the reachable set exceeds the cap, the result is truncated to the
	// top-ranked nodes and Analysis.Truncated is set.
	MaxNodes int `json:"max_nodes,omitempty"`
	// MaxPaths bounds the number of paths enumerated (call-chain).
	MaxPaths int `json:"max_paths,omitempty"`
	// MaxTokens bounds the serialized token cost of an aggregated response
	// (batched). Zero/negative means the analyzer default budget. Enforced via
	// the EP-003-consistent tokenizer; over-budget responses are trimmed and
	// Analysis.Truncated is set.
	MaxTokens int `json:"max_tokens,omitempty"`
	// Diff is the local-first PR-diff payload for the pr-risk scorer (SW-039): a
	// unified-diff STRING or the simple line-oriented ref form. It is untrusted
	// input — size-bounded and path-sanitized by the scorer; NO remote fetch /
	// base..head resolution happens here (callers reading a local path do so
	// before dispatch). Unused by all other analyzers.
	Diff string `json:"diff,omitempty"`
	// Provenance selects the redaction level of pr-risk evidence: "full"
	// (default; verbatim taint source/sink/steps) or "summary" (redacted so a
	// downstream publisher can emit a non-sensitive readout). Unused by other
	// analyzers.
	Provenance string `json:"provenance,omitempty"`
	// PRs is the already-enumerated open-PR set the SW-105 triage-prs analyzer
	// ranks. It is populated by the surface-boundary forge client (the ONLY
	// outbound path); the engine never fetches it. Unused by all other analyzers.
	PRs []TriagePRInput `json:"prs,omitempty"`
	// ConflictPRs is the already-enumerated open-PR set the SW-106 conflicts-prs
	// analyzer intersects pairwise. Like PRs it is populated by the surface-boundary
	// forge client (the only outbound path); the engine never fetches it. Each input
	// carries the forge-sourced changed-file list plus an OPTIONAL unified-diff /
	// line-oriented ref string (reused through EP-007 parseDiff) so the analyzer can
	// resolve precise symbols + line ranges when available. Unused by other analyzers.
	ConflictPRs []ConflictPRInput `json:"conflict_prs,omitempty"`
	// CompareBase and CompareHead are the two already-built, read-only graph states
	// the SW-107 compare-branches analyzer diffs. They are materialized ABOVE the
	// surface boundary (the existing indexer / core/graphstore snapshot path) from
	// two branch refs; the engine analyzer performs a pure local node/edge set-diff
	// keyed by canonical NodeId and NEVER resolves a git ref, reads a git tree, or
	// opens a socket. They are runtime query.Reader views (not JSON-serializable),
	// so they carry json:"-". Unused by every other analyzer.
	CompareBase query.Reader `json:"-"`
	CompareHead query.Reader `json:"-"`
	// Review is the already-structured EXISTING PR review (comments with
	// {path,line,symbol} anchors + claim-target refs + overall verdict) the SW-108
	// critique-review analyzer critiques. It is produced ABOVE the surface boundary
	// (fetched from the forge via the net-new surface review-fetch path, or supplied
	// inline) and handed to the engine as Params; the engine NEVER fetches it. The
	// PR's touched-entity set is carried in Diff (reused EP-007 parseDiff/resolveRef).
	// Unused by every other analyzer.
	Review *ReviewInput `json:"review,omitempty"`
}

// ReachedNode is a node reached during a traversal, carrying the provenance of
// the edge that reached it. Provenance fields are passed through VERBATIM from
// the model edge — the analyzer never re-derives or downgrades them. When a node
// is reachable by several edges, the best-tier reaching edge is kept (canonical
// tie-break by edge id), so the most trustworthy reason leads and the choice is
// deterministic.
type ReachedNode struct {
	Node       query.ResultNode `json:"node"`
	ReachedVia query.ResultEdge `json:"reached_via"`
	Depth      int              `json:"depth"`
}

// NodeScore is a node plus a numeric analysis signal (graph metrics: degree,
// betweenness, centrality). Carries the node verbatim, a Kind label so a single
// result can mix metric types, and EdgeCount as bounded provenance for
// degree-derived scores (the number of incident edges the score was derived
// from).
type NodeScore struct {
	Node      query.ResultNode `json:"node"`
	Score     float64          `json:"score"`
	Kind      string           `json:"kind"`
	EdgeCount int              `json:"edge_count,omitempty"`
}

// LocationKind is the closed vocabulary for concept-resolution locations.
const (
	KindDefinition = "definition"
	KindHandler    = "handler"
	KindReference  = "reference"
)

// Location is a concept-resolution result location: a node plus its classified
// kind (definition/handler/reference) and, for references/handlers, the inbound
// graph edge that surfaced it. Definitions are found via lexical search and
// carry no edge (ReachedVia == nil); the node's own file/line IS their
// provenance. References/handlers carry the inbound edge verbatim.
type Location struct {
	Node       query.ResultNode  `json:"node"`
	Kind       string            `json:"kind"`
	ReachedVia *query.ResultEdge `json:"reached_via,omitempty"`
	Depth      int               `json:"depth,omitempty"`
}

// Analysis is the uniform, surface-agnostic result of any analyzer. It carries
// the analyzer name, the queried symbol, the resolution outcome (reusing the
// query.Outcome vocabulary: found/empty/not_found), and the analyzer-specific
// payload slices. Each analyzer populates only its slice; the others stay
// omitted. Nodes/Paths/Metrics are always materialized-then-sorted by the
// canonical comparator before the Analysis is returned.
type Analysis struct {
	Analyzer  string               `json:"analyzer"`
	Outcome   query.Outcome        `json:"outcome"`
	Symbol    model.NodeId         `json:"symbol"`
	Truncated bool                 `json:"truncated,omitempty"`
	Nodes     []ReachedNode        `json:"nodes,omitempty"`
	Paths     [][]query.ResultEdge `json:"paths,omitempty"`
	Metrics   []NodeScore          `json:"metrics,omitempty"`
	Locations []Location           `json:"locations,omitempty"`
	// RiskReport carries the SW-039 pr-risk scorer's versioned per-region risk
	// payload. Only the pr-risk analyzer populates it; it stays omitted (nil) for
	// every other analyzer so the generic envelope is unchanged for them.
	RiskReport *RiskReport `json:"risk_report,omitempty"`
	// SignalReport carries the SW-040 pr-signals detector's versioned per-region
	// hub/bridge/surprise annotations. Only the pr-signals analyzer populates it;
	// it stays omitted (nil) for every other analyzer so the generic envelope is
	// unchanged for them.
	SignalReport *SignalReport `json:"signal_report,omitempty"`
	// QuestionReport carries the SW-041 pr-questions generator's versioned,
	// deterministic reviewer-question set derived from the consumed SW-039 risk and
	// SW-040 signal reports. Only the pr-questions analyzer populates it; it stays
	// omitted (nil) for every other analyzer so the generic envelope is unchanged.
	QuestionReport *QuestionReport `json:"question_report,omitempty"`
	// InterprocTaint carries the SW-102 persisted interprocedural taint fixpoint
	// result: the explicit completeness verdict, the no-recompute observability
	// flags, and the cross-procedure source→sink flows answered from the solved
	// relation. Only the taint analyzer populates it; it stays omitted (nil) for
	// every other analyzer so the generic envelope is unchanged for them.
	InterprocTaint *InterprocTaintReport `json:"interproc_taint,omitempty"`
	// Communities carries the SW-104 `communities` operation payload (SW-103
	// detection surfaced behind the single dispatch table). Only the communities
	// analyzer populates it; nil for every other analyzer.
	Communities *CommunitiesReport `json:"communities,omitempty"`
	// Notebook carries the SW-104 `notebook-ingest` operation payload (SW-100
	// notebook-cell provenance surfaced behind the single dispatch table). Only the
	// notebook analyzer populates it; nil for every other analyzer.
	Notebook *NotebookReport `json:"notebook,omitempty"`
	// WatcherStatus carries the SW-104 `watcher-status` operation payload (SW-101
	// watcher health surfaced behind the single dispatch table). Only the
	// watcher-status analyzer populates it; nil for every other analyzer.
	WatcherStatus *WatcherStatusReport `json:"watcher_status,omitempty"`
	// Triage carries the SW-105 `triage-prs` ranked multi-PR triage payload. Only
	// the triage-prs analyzer populates it; nil for every other analyzer so the
	// generic envelope is unchanged for them.
	Triage *TriageReport `json:"triage,omitempty"`
	// Conflicts carries the SW-106 `conflicts-prs` inter-PR conflict payload: the
	// deterministic pairwise report of conflicting open-PR pairs. Only the
	// conflicts-prs analyzer populates it; nil for every other analyzer so the
	// generic envelope is unchanged for them.
	Conflicts *ConflictReport `json:"conflicts,omitempty"`
	// Reviewers carries the SW-107 `suggest-reviewers` ranked candidate-reviewer
	// payload with a transparent per-signal breakdown (ownership, recency-decayed
	// churn, affected-subgraph proximity). Only the suggest-reviewers analyzer
	// populates it; nil for every other analyzer so the generic envelope is
	// unchanged for them.
	Reviewers *ReviewerReport `json:"reviewers,omitempty"`
	// BranchDiff carries the SW-107 `compare-branches` structured graph-level diff
	// payload keyed by canonical NodeId (added/removed/changed/moved entities + edge
	// added/removed). Only the compare-branches analyzer populates it; nil for every
	// other analyzer so the generic envelope is unchanged for them.
	BranchDiff *BranchDiffReport `json:"branch_diff,omitempty"`
	// Critique carries the SW-108 `critique-review` structured, graph-evidence-grounded
	// critique of an existing PR review (gap / over_flag / unsupported_claim items +
	// the unanchored tallies). Only the critique-review analyzer populates it; nil for
	// every other analyzer so the generic envelope is unchanged for them.
	Critique *CritiqueReport `json:"critique,omitempty"`
}

// InterprocTaintReport is the SW-102 surface payload for the solved, persisted
// interprocedural taint fixpoint. It is byte-stable: the verdict and cap kind are
// deterministic, the flows are pre-sorted, and the loaded/solved flags are the
// no-recompute observability signal (loaded=true means the answer was served from
// the persisted artifact without recomputation).
type InterprocTaintReport struct {
	Verdict string               `json:"verdict"`
	CapKind string               `json:"cap_kind,omitempty"`
	Loaded  bool                 `json:"loaded"`
	Solved  bool                 `json:"solved"`
	Flows   []InterprocTaintFlow `json:"flows"`
}

// InterprocTaintFlow is one cross-procedure source→sink flow with its call path.
type InterprocTaintFlow struct {
	SourceName   string   `json:"source_name"`
	SinkName     string   `json:"sink_name"`
	SinkCategory string   `json:"sink_category,omitempty"`
	Labels       []string `json:"labels"`
	CallPath     []string `json:"call_path"`
}

// Analyzer is the plug-in contract every analysis capability implements. It is
// stateless per call: the read-only Reader and the Params are passed in, so a
// single Analyzer value can serve any graph and any input. Implementations must
// be safe for concurrent use and must never mutate the graph (enforced by the
// read-only Reader type). Analyzers needing lexical search (concept resolution)
// receive a Searcher via construction-time injection rather than this method.
type Analyzer interface {
	// Name is the unique analyzer name used for registry lookup and dispatch
	// (e.g. "impact", "call-chain", "concept"). It is the dispatch key.
	Name() string
	// Analyze runs the analyzer over the read-only graph and returns the
	// canonical, provenance-carrying result. A genuinely missing symbol is an
	// explicit not-found Analysis, NOT an error; an error is reserved for
	// infrastructure failures (cancelled context, closed store, …).
	Analyze(ctx context.Context, r query.Reader, p Params) (Analysis, error)
}

// Searcher is the optional lexical-search capability some analyzers need
// (concept resolution). It is the SearchNodes subset of graphstore.Graphstore;
// query.Reader does NOT include it. graphstore.Graphstore satisfies both
// query.Reader and Searcher. The concept analyzer receives a Searcher via
// construction-time injection (NewDefaultService probes reader.(Searcher)).
type Searcher interface {
	SearchNodes(ctx context.Context, text string, limit int) ([]graphstore.RankedNode, error)
}
