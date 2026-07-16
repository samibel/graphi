package client

// This file defines the CAP-01 (SW-117) consumer-owned ports (master plan §4
// "Application Ports"), including the capability-narrow StableClient held by
// the default MCP surface for the frozen operations.
// Stable consumers depend on the smallest port they need — never on the full
// Client, which remains the LABS FACADE carrying every experimental capability
// (edit/refactor, memory, distill, skillgen, the PR/forge vertical, compound/
// AST/clone queries, savings, diagnostics).
//
// Broad CLI/Labs ports remain structural subsets of Client. StableClient is a
// deliberately narrower adapter: it removes the arbitrary analyzer selector
// and semantic search from the type visible to Stable MCP dispatch.

import "context"

// QueryPort is the broad structural/analyzer port used by CLI and Labs paths.
// Its Query method includes the stable structural operations, but Analyze is an
// intentionally generic Labs-capable selector. Stable dispatch must use
// StableQueryPort instead.
type QueryPort interface {
	// Query runs a structural query operation and returns the canonical
	// serialized result bytes.
	Query(ctx context.Context, op, symbol string, depth int) ([]byte, error)
	// Analyze runs a named analyzer and returns canonical serialized bytes.
	Analyze(ctx context.Context, p AnalyzeParams) ([]byte, error)
}

// ImpactParams is the complete, analyzer-selector-free input accepted by the
// Stable impact port. Adding a Labs analyzer cannot widen this type.
type ImpactParams struct {
	Symbol    string
	Direction string
	MaxNodes  int
}

// StableQueryPort exposes structural queries plus exactly one analyzer:
// impact. Unlike QueryPort, it cannot dispatch an arbitrary analyzer name.
type StableQueryPort interface {
	Query(ctx context.Context, op, symbol string, depth int) ([]byte, error)
	Impact(ctx context.Context, p ImpactParams) ([]byte, error)
}

// SearchPort is the broad lexical + semantic search port used by CLI and Labs
// paths. Stable dispatch must use StableSearchPort instead.
type SearchPort interface {
	// Search runs a lexical/symbol search and returns the canonical serialized
	// result bytes.
	Search(ctx context.Context, q string, limit int) ([]byte, error)
	// SemanticSearch runs the OPTIONAL semantic search (Labs; graceful-skip
	// typed response when no embedder is configured).
	SemanticSearch(ctx context.Context, q string, limit int) ([]byte, error)
}

// StableSearchPort is lexical-only. SemanticSearch remains Labs and is not
// present on the interface held by Stable MCP dispatch.
type StableSearchPort interface {
	Search(ctx context.Context, q string, limit int) ([]byte, error)
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
	StableQueryPort
	StableSearchPort
	AgentContextPort
}

type stableClient struct{ client Client }

// AsStable returns the capability-narrow view used by Stable surfaces. The
// wrapped Labs facade is private and cannot be recovered through StableClient.
func AsStable(c Client) StableClient { return stableClient{client: c} }

func (s stableClient) Query(ctx context.Context, op, symbol string, depth int) ([]byte, error) {
	return s.client.Query(ctx, op, symbol, depth)
}

func (s stableClient) Impact(ctx context.Context, p ImpactParams) ([]byte, error) {
	return s.client.Analyze(ctx, AnalyzeParams{
		Name:      "impact",
		Symbol:    p.Symbol,
		Direction: p.Direction,
		MaxNodes:  p.MaxNodes,
	})
}

func (s stableClient) Search(ctx context.Context, q string, limit int) ([]byte, error) {
	return s.client.Search(ctx, q, limit)
}

func (s stableClient) Brief(ctx context.Context, topic string) ([]byte, []byte, error) {
	return s.client.Brief(ctx, topic)
}

func (s stableClient) ExplainSymbol(ctx context.Context, symbol string, maxItems int) ([]byte, error) {
	return s.client.ExplainSymbol(ctx, symbol, maxItems)
}

func (s stableClient) RelatedFiles(ctx context.Context, target, direction string, maxFiles int) ([]byte, error) {
	return s.client.RelatedFiles(ctx, target, direction, maxFiles)
}

func (s stableClient) ChangeRisk(ctx context.Context, target, diff string, maxItems int) ([]byte, error) {
	return s.client.ChangeRisk(ctx, target, diff, maxItems)
}

// Compile-time proof the ports are strict subsets of the full Client: every
// existing client implementation satisfies them without adapters or stubs.
var (
	_ QueryPort        = (Client)(nil)
	_ SearchPort       = (Client)(nil)
	_ AgentContextPort = (Client)(nil)
	_ StableClient     = stableClient{}
)
