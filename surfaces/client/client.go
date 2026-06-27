// Package client defines the transport-agnostic surface client contract.
//
// Both the CLI and MCP surfaces consume a Client so they can run against either
// an in-process service (direct mode) or a hot-index daemon (daemon mode)
// without code changes. The serialized bytes returned by a Client are always
// the canonical output of the shared engine service.
package client

import (
	"context"
	"errors"
)

// ErrSearchUnavailable is returned when a Client has no search service configured.
var ErrSearchUnavailable = errors.New("client: search service unavailable")

// ErrSavingsUnavailable is returned when a Client has no savings ledger
// configured (SW-020). Query/search still work; only the savings readout is
// unavailable.
var ErrSavingsUnavailable = errors.New("client: savings ledger unavailable")

// ErrAnalysisUnavailable is returned when a Client has no analysis service
// configured (SW-022). Query/search still work; only the analyzers are
// unavailable. The in-process Direct client sets it up; the daemon client
// returns it until a daemon analysis RPC is added.
var ErrAnalysisUnavailable = errors.New("client: analysis service unavailable")

// ErrEditUnavailable is returned when a Client has no edit/refactor applier
// configured (SW-038). Query/search/analysis still work; only the
// RefactorPreview/Refactor/Undo command surface is unavailable. The in-process
// Direct client sets it up via WithEditor; the daemon client returns it until a
// daemon edit RPC is added.
var ErrEditUnavailable = errors.New("client: edit/refactor service unavailable")

// ErrReviewUnavailable is returned when a Client has no PR-review publisher
// service configured (SW-042). Query/search/analysis/edit still work; only the
// PrComment publish/gate surface is unavailable. The in-process Direct client
// sets it up via WithReview; the daemon client returns it until a daemon review
// RPC is added (mirrors the analysis/edit "unavailable until wired" precedent).
var ErrReviewUnavailable = errors.New("client: PR-review publisher service unavailable")

// PrCommentRequest is the transport-agnostic input for the SW-042 sticky PR
// comment writer + optional risk-threshold merge gate. It maps onto
// engine/review.PublishOptions so both surfaces drive the publisher identically
// (parity by construction). The diff is local-first, untrusted input (bounded
// and path-sanitized by the consumed analyzers); no remote fetch happens here.
type PrCommentRequest struct {
	PR            string `json:"pr"`             // PR reference rendered in the comment header
	Diff          string `json:"diff"`           // local-first unified-diff / ref string
	Provenance    string `json:"provenance"`     // evidence redaction: "full" | "summary" (default summary for public comments)
	GateEnabled   bool   `json:"gate_enabled"`   // turn the optional merge gate on
	GateThreshold int    `json:"gate_threshold"` // risk threshold in fixed-point units (1/1000) the worst region must exceed to BLOCK
	Publish       bool   `json:"publish"`        // when true, upsert through the host; when false (default) dry-run (render+gate only)
}

// MemoryRequest is the transport-agnostic input for memory operations.
type MemoryRequest struct {
	Op       string   `json:"op"` // store | recall | forget
	Scope    string   `json:"scope"`
	Notebook string   `json:"notebook"`
	Tags     []string `json:"tags"`
	Payload  string   `json:"payload"`
	ID       string   `json:"id"` // for forget
}

// MemoryResponse is the canonical serialized output for memory operations.
type MemoryResponse struct {
	Entries []MemoryEntry `json:"entries"`
	ID      string        `json:"id"`
	Count   int           `json:"count"`
}

// MemoryEntry is one returned memory item.
type MemoryEntry struct {
	ID        string   `json:"id"`
	Scope     string   `json:"scope"`
	Notebook  string   `json:"notebook"`
	Tags      []string `json:"tags"`
	Payload   string   `json:"payload"`
	CreatedAt int64    `json:"created_at"`
}

// DistillRequest is the transport-agnostic input for session distillation.
type DistillRequest struct {
	SessionID      string   `json:"session_id"`
	Turns          []Turn   `json:"turns"`
	Decisions      []string `json:"decisions"`
	Risks          []string `json:"risks"`
	OpenQuestions  []string `json:"open_questions"`
	FileReferences []string `json:"file_references"`
}

// Turn captures one agent turn in a session.
type Turn struct {
	ID       string   `json:"id"`
	Prompt   string   `json:"prompt"`
	FilesIn  []string `json:"files_in"`
	FilesOut []string `json:"files_out"`
}

// DistillResponse is the canonical serialized output for session distillation.
type DistillResponse struct {
	Version        string   `json:"version"`
	SessionID      string   `json:"session_id"`
	Summary        string   `json:"summary"`
	Decisions      []string `json:"decisions"`
	Risks          []string `json:"risks"`
	OpenQuestions  []string `json:"open_questions"`
	FileReferences []string `json:"file_references"`
	TouchedFiles   []string `json:"touched_files"`
}

// SkillGenRequest is the transport-agnostic input for skill generation.
type SkillGenRequest struct {
	Name        string      `json:"name"`
	Trigger     string      `json:"trigger"`
	Description string      `json:"description"`
	Inputs      []string    `json:"inputs"`
	Outputs     []string    `json:"outputs"`
	Steps       []SkillStep `json:"steps"`
}

// SkillStep is one step in a generated skill.
type SkillStep struct {
	Name        string   `json:"name"`
	Action      string   `json:"action"`
	Inputs      []string `json:"inputs"`
	Outputs     []string `json:"outputs"`
	Guard       string   `json:"guard"`
	Description string   `json:"description"`
}

// SkillGenResponse is the canonical serialized output for skill generation.
type SkillGenResponse struct {
	Name        string      `json:"name"`
	Trigger     string      `json:"trigger"`
	Description string      `json:"description"`
	Inputs      []string    `json:"inputs"`
	Outputs     []string    `json:"outputs"`
	Steps       []SkillStep `json:"steps"`
	Markdown    string      `json:"markdown"`
}

// RefactorRequest is the transport-agnostic input for a graph-aware refactor. It
// maps 1:1 onto engine/edit.RefactorOp so BOTH surfaces (MCP tool args, CLI
// flags) construct the SAME operation — the only divergence-risk being input
// decoding, which the trivial 1:1 mapping plus the parity test eliminate. The
// command implementation lives ONCE in Direct (parity by construction).
type RefactorRequest struct {
	Kind            string `json:"kind"`             // rename|extract|move|signature_change
	TargetSymbol    string `json:"target_symbol"`    // resolved NodeId (EP-001)
	OldName         string `json:"old_name"`         // current spelling
	NewName         string `json:"new_name"`         // replacement spelling
	DestinationFile string `json:"destination_file"` // move destination (optional)
}

// InlineRequest is the transport-agnostic input for the SW-092 inline refactor.
// It maps 1:1 onto engine/edit.InlineOp so every surface constructs the same
// operation; the command lives ONCE in Direct (parity by construction).
type InlineRequest struct {
	TargetSymbol string `json:"target_symbol"` // resolved NodeId
	DryRun       bool   `json:"dry_run"`       // preview without mutating
}

// SafeDeleteRequest is the transport-agnostic input for the SW-093 safe-delete
// refactor. It maps 1:1 onto engine/edit.SafeDeleteOp.
type SafeDeleteRequest struct {
	TargetSymbol string `json:"target_symbol"` // resolved NodeId
	DryRun       bool   `json:"dry_run"`       // preview without mutating
}

// ErrDiagnosticUnavailable is returned when a Client has no diagnostic reader
// configured. The in-process Direct client always has one; the daemon/HTTP
// clients return it until a daemon diagnostics RPC is added (mirrors the
// analysis/edit "unavailable until wired" precedent).
var ErrDiagnosticUnavailable = errors.New("client: diagnostic service unavailable")

// AnalyzeParams is the transport-agnostic input for an analyzer call. It maps
// 1:1 onto engine/analysis.Params so both surfaces call every analyzer with the
// same arguments (parity by construction). Each analyzer reads the fields
// relevant to it.
type AnalyzeParams struct {
	Name      string   `json:"name"`      // analyzer name, e.g. "impact"
	Symbol    string   `json:"symbol"`    // primary symbol (node id)
	Target    string   `json:"target"`    // secondary symbol (call-chain endpoint)
	Concept   string   `json:"concept"`   // concept-resolver term
	Direction string   `json:"direction"` // "forward" | "reverse"
	Kinds     []string `json:"kinds"`     // edge kinds to traverse (impact)
	MaxNodes  int      `json:"max_nodes"` // output budget
	MaxPaths  int      `json:"max_paths"` // path enumeration bound
	// Diff is the local-first PR-diff payload for the pr-risk scorer (SW-039): a
	// unified-diff string or the simple ref form. Untrusted input, size-bounded
	// and path-sanitized by the engine; no remote fetch. Unused by other analyzers.
	Diff string `json:"diff,omitempty"`
	// Provenance selects pr-risk evidence redaction: "full" (default) | "summary".
	Provenance string `json:"provenance,omitempty"`
}

// AnalyzerSymbolOptional reports whether an analyzer operation requires NO
// primary symbol argument (SW-104). The four EP-017 canonical operations are
// whole-graph (communities, notebook-ingest, taint-query) or runtime-status
// (watcher-status) operations, so the symbol-required input guard does not apply
// to them. It is shared by the CLI and MCP adapters so both surfaces apply the
// SAME input-validation rule (parity by construction) — neither holds analysis
// logic, only this transport-agnostic argument check.
func AnalyzerSymbolOptional(name string) bool {
	switch name {
	case "communities", "notebook-ingest", "taint-query", "watcher-status":
		return true
	default:
		return false
	}
}

// Client is the thin contract every surface uses to execute structural queries,
// search, and read the savings ledger. Implementations may be in-process or over
// a Unix domain socket.
type Client interface {
	// Query runs a structural query operation and returns the canonical
	// serialized result bytes.
	Query(ctx context.Context, op, symbol string, depth int) ([]byte, error)
	// Compound runs a compound / Cypher-style graph query (EP-011 G1) and returns
	// the canonical serialized query.Result bytes — byte-identical to a fixed
	// Query across every surface. queryText is the SEED/HOP/WHERE/MAXDEPTH text
	// form parsed by engine/query/compound.Parse.
	Compound(ctx context.Context, queryText string) ([]byte, error)
	// Search runs a lexical/symbol search and returns the canonical serialized
	// result bytes.
	Search(ctx context.Context, q string, limit int) ([]byte, error)
	// SemanticSearch runs the OPTIONAL semantic search and returns the canonical
	// serialized engine/search.SemanticResponse bytes (SW-059). It is the single
	// engine-owned typed response, so the unconfigured graceful-skip "unavailable"
	// bytes are byte-identical across CLI/MCP/HTTP. When no embedder is configured
	// (the default path) it returns the typed Unavailable response with NO error
	// and ZERO network — never ErrSearchUnavailable.
	SemanticSearch(ctx context.Context, q string, limit int) ([]byte, error)
	// SearchAST runs the structural AST pattern query (SW-082) and returns the
	// canonical serialized engine/query.Result bytes via query.Marshal — the SAME
	// serializer every symbol query uses, so the bytes are byte-identical across
	// every surface (SW-085 parity). patternJSON is the JSON AstPattern; an invalid
	// pattern surfaces the engine's typed *query.InvalidPattern error unchanged (no
	// new surface error shape). It is a pattern query (no symbol id), so — like
	// search and compound — it is a singleton, NOT a member of query.Operations.
	SearchAST(ctx context.Context, patternJSON string, limit int) ([]byte, error)
	// FindClones runs the clone-group detection query (SW-083) and returns the
	// canonical serialized engine/query.CloneResult bytes via query.MarshalCloneResult.
	// configJSON is the JSON CloneConfig; an empty/omitted config uses the engine
	// defaults (query.DefaultCloneConfig). Like SearchAST it is a singleton pattern
	// query and rides the single engine serializer for byte-identical parity.
	FindClones(ctx context.Context, configJSON string) ([]byte, error)
	// Savings returns the canonical serialized savings-ledger readout (per-call,
	// per-session, cumulative USD + cap flags). It is the single source for the
	// MCP and CLI readouts so both surfaces stay in parity.
	Savings(ctx context.Context) ([]byte, error)
	// Analyze runs a named analyzer and returns the canonical serialized
	// analysis result bytes. It is the single source for MCP and CLI analyzer
	// output (parity). Without a configured analysis service it returns
	// ErrAnalysisUnavailable (SW-022).
	Analyze(ctx context.Context, p AnalyzeParams) ([]byte, error)

	// RefactorPreview resolves the target via the EP-001 query layer and returns
	// the EP-004 impact set (the blast radius + planned ops) WITHOUT mutating —
	// AC-1's "impact set BEFORE mutation". Returns the canonical serialized
	// RefactorResult. Without an edit service it returns ErrEditUnavailable (SW-038).
	RefactorPreview(ctx context.Context, req RefactorRequest) ([]byte, error)

	// Refactor commits a graph-aware refactor through the shared edit applier,
	// then persists an auditable change record (operation, target, before/after,
	// actor, timestamp, undo token) and returns the canonical serialized
	// ChangeRecord. actor is the per-surface request identity (recorded; excluded
	// from the parity comparable subset). Without an edit service it returns
	// ErrEditUnavailable (SW-038).
	Refactor(ctx context.Context, req RefactorRequest, actor string) ([]byte, error)

	// Undo reverses a previously applied edit identified by its undo token,
	// restoring the prior graph + source and recording the reversal as its own
	// auditable change record. Returns the canonical serialized reversal
	// ChangeRecord. Without an edit service it returns ErrEditUnavailable (SW-038).
	Undo(ctx context.Context, undoToken, actor string) ([]byte, error)

	// PrComment renders the assembled PR-review findings (SW-039 risk + SW-040
	// signals + SW-041 questions) into one sticky Markdown comment and evaluates
	// the optional risk-threshold merge gate, returning the canonical serialized
	// engine/review.PublishResult (SW-042). When req.Publish is false (default) it
	// is an offline dry-run (render + gate only; the host is never contacted). The
	// in-process Direct client wires it via WithReview; the daemon client returns
	// ErrReviewUnavailable until a daemon review RPC is added.
	PrComment(ctx context.Context, req PrCommentRequest) ([]byte, error)

	// Memory runs a memory operation (store/recall/forget) and returns the
	// canonical serialized MemoryResponse bytes. The in-process Direct client wires
	// it via WithMemory; other clients return ErrMemoryUnavailable until wired.
	Memory(ctx context.Context, req MemoryRequest) ([]byte, error)

	// Distill runs session distillation and returns the canonical serialized
	// DistillResponse bytes. The in-process Direct client wires it via WithDistill;
	// other clients return ErrDistillUnavailable until wired.
	Distill(ctx context.Context, req DistillRequest) ([]byte, error)

	// SkillGen runs deterministic skill generation and returns the canonical
	// serialized SkillGenResponse bytes. The in-process Direct client wires it via
	// WithSkillGen; other clients return ErrSkillGenUnavailable until wired.
	SkillGen(ctx context.Context, req SkillGenRequest) ([]byte, error)

	// Diagnose runs the engine diagnostics (SW-091) over the graph and returns the
	// canonical serialized diagnostic.Result bytes via diagnostic.Marshal — the
	// single serializer every surface uses (byte-identical parity, SW-094). kinds
	// selects analyzers; an empty slice runs all built-ins. Without a reader it
	// returns ErrDiagnosticUnavailable.
	Diagnose(ctx context.Context, kinds []string) ([]byte, error)

	// Inline applies the SW-092 inline refactor through the shared edit applier and
	// returns the canonical serialized InlineResult via edit.MarshalInlineResult.
	// Without an edit service it returns ErrEditUnavailable.
	Inline(ctx context.Context, req InlineRequest) ([]byte, error)

	// SafeDelete applies the SW-093 safe-delete refactor through the shared edit
	// applier and returns the canonical serialized SafeDeleteResult via
	// edit.MarshalSafeDeleteResult. Without an edit service it returns
	// ErrEditUnavailable.
	SafeDelete(ctx context.Context, req SafeDeleteRequest) ([]byte, error)

	// ListPRs enumerates the open PRs of the configured repo via the read-only
	// forge boundary (SW-105) and returns the canonical serialized forge.PRList
	// bytes — forge-sourced metadata ONLY (number, title, author, base/head, head
	// SHA, changed files, add/del, mergeable). It performs NO graph scoring and
	// posts NO comments. The forge enumeration is the suite's ONLY outbound path;
	// it lives at the surface boundary so the engine stays zero-egress. Without a
	// forge client wired it returns ErrForgeUnavailable.
	ListPRs(ctx context.Context) ([]byte, error)

	// TriagePRs enumerates the open PRs via the read-only forge boundary, then
	// hands the already-enumerated set to the zero-egress engine `triage-prs`
	// analyzer, returning the canonical serialized ranked TriageReport bytes
	// (composite score DESC, PR number ASC). The forge call (enumeration) is the
	// only egress; scoring is in-memory over the local graph. Without a forge
	// client wired it returns ErrForgeUnavailable; without an analysis service it
	// returns ErrAnalysisUnavailable.
	TriagePRs(ctx context.Context) ([]byte, error)
}

// ErrForgeUnavailable is returned when a Client has no read-only forge
// PR-enumeration client configured (SW-105). Query/search/analysis still work;
// only the list_prs / triage_prs PR-triage surface is unavailable. The in-process
// Direct client wires it via WithForge; the daemon/HTTP remote clients return it
// until a remote forge RPC is added (mirrors the analysis/edit/review
// "unavailable until wired" precedent).
var ErrForgeUnavailable = errors.New("client: forge PR-enumeration client unavailable")

// ErrMemoryUnavailable is returned when a Client has no memory service configured.
var ErrMemoryUnavailable = errors.New("client: memory service unavailable")

// ErrDistillUnavailable is returned when a Client has no distillation service configured.
var ErrDistillUnavailable = errors.New("client: distill service unavailable")

// ErrSkillGenUnavailable is returned when a Client has no skill generation service configured.
var ErrSkillGenUnavailable = errors.New("client: skillgen service unavailable")
