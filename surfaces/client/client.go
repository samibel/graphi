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
}

// Client is the thin contract every surface uses to execute structural queries,
// search, and read the savings ledger. Implementations may be in-process or over
// a Unix domain socket.
type Client interface {
	// Query runs a structural query operation and returns the canonical
	// serialized result bytes.
	Query(ctx context.Context, op, symbol string, depth int) ([]byte, error)
	// Search runs a lexical/symbol search and returns the canonical serialized
	// result bytes.
	Search(ctx context.Context, q string, limit int) ([]byte, error)
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
}
