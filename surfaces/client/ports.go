package client

// This file defines the CAP-01 (SW-117) consumer-owned stable ports (master
// plan §4 "Application Ports"): the three small interfaces the STABLE surfaces
// (CLI verbs, MCP stdio tools) actually consume for the frozen 12 operations.
// Stable consumers depend on the smallest port they need — never on the full
// Client, which remains the LABS FACADE carrying every experimental capability
// (edit/refactor, memory, distill, skillgen, the PR/forge vertical, compound/
// AST/clone queries, savings, diagnostics).
//
// The ports are strict subsets of Client, so every existing client (Direct,
// daemon) satisfies them structurally — no adapter, no stub. The compile-time
// assertions below plus the surfaces-level no-stub gate
// (surfaces/capability_ports_test.go) are the CAP-01 exit evidence: no stable
// method is served by an Unavailable stub.

import "context"

// QueryPort is the structural read port: the five graph query operations of
// the stable set (definition/callers/callees/references/neighborhood via
// Query) and the analyzer dispatch that serves the stable `impact` operation
// (Analyze). The stable contract covers ONLY analyzer "impact"; every other
// analyzer reachable through Analyze is Labs-tier, governed by the capability
// manifest (docs/coverage-matrix.yaml), not by this interface.
type QueryPort interface {
	// Query runs a structural query operation and returns the canonical
	// serialized result bytes.
	Query(ctx context.Context, op, symbol string, depth int) ([]byte, error)
	// Analyze runs a named analyzer and returns the canonical serialized
	// analysis result bytes (stable scope: analyzer "impact").
	Analyze(ctx context.Context, p AnalyzeParams) ([]byte, error)
}

// SearchPort is the lexical search port serving the stable `search` operation.
// SemanticSearch rides the same surface verb but is Labs-tier: it is included
// because the search consumers own it, and it is graceful-skip by contract (a
// typed Unavailable RESPONSE, never an ErrSearchUnavailable stub).
type SearchPort interface {
	// Search runs a lexical/symbol search and returns the canonical serialized
	// result bytes.
	Search(ctx context.Context, q string, limit int) ([]byte, error)
	// SemanticSearch runs the OPTIONAL semantic search (Labs; graceful-skip
	// typed response when no embedder is configured).
	SemanticSearch(ctx context.Context, q string, limit int) ([]byte, error)
}

// AgentContextPort is the agent-context port: the four agent-first stable
// operations (agent_brief, explain_symbol, related_files, change_risk).
type AgentContextPort interface {
	// Brief runs the agent_brief assembler and returns the canonical serialized
	// Result bytes plus a Markdown rendering.
	Brief(ctx context.Context, topic string) ([]byte, []byte, error)
	// ExplainSymbol returns the cited explain_symbol context packet.
	ExplainSymbol(ctx context.Context, symbol string, maxItems int) ([]byte, error)
	// RelatedFiles returns the ranked, cited read-first file list.
	RelatedFiles(ctx context.Context, target, direction string, maxFiles int) ([]byte, error)
	// ChangeRisk returns the cited change-risk assessment.
	ChangeRisk(ctx context.Context, target, diff string, maxItems int) ([]byte, error)
}

// StableClient is the composed view a stable surface holds: exactly the three
// consumer-owned ports, nothing else. The MCP server routes its stable tool
// dispatch through this type so the compiler proves the stable path cannot
// reach a Labs capability.
type StableClient interface {
	QueryPort
	SearchPort
	AgentContextPort
}

// Compile-time proof the ports are strict subsets of the full Client: every
// existing client implementation satisfies them without adapters or stubs.
var (
	_ QueryPort        = (Client)(nil)
	_ SearchPort       = (Client)(nil)
	_ AgentContextPort = (Client)(nil)
	_ StableClient     = (Client)(nil)
)
