// Package mcp is the MCP stdio surface over the shared surface client.
//
// It speaks a minimal JSON-RPC 2.0 protocol over stdin/stdout using ONLY the Go
// standard library (encoding/json + bufio) — no external MCP SDK, no CGo, and
// zero outbound network activity (local-first contract). It exposes structural
// queries and search as MCP tools and dispatches every call to the SAME shared
// client, then returns the canonical serialized bytes. The serialized result is
// therefore byte-identical to the CLI for identical inputs (MCP↔CLI parity).
//
// Layering: mcp is a surface. It imports surfaces/client only and holds no
// query/traversal/ordering/serialization logic of its own.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/client"
)

// protocolVersion is the MCP protocol version this stdio handler reports.
const protocolVersion = "2024-11-05"

// --- JSON-RPC 2.0 envelopes (stdlib encoding/json only) ---

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // absent for notifications
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Server is the MCP stdio handler bound to a shared surface client.
type Server struct {
	c client.Client
}

// NewServer constructs an MCP server over an in-process query service.
// If searchSvc is non-nil, the search tool is also advertised.
func NewServer(q *query.Service, searchSvc *search.Service) *Server {
	return &Server{c: client.NewDirect(q, searchSvc)}
}

// NewServerWithClient constructs an MCP server over an arbitrary client
// (in-process or daemon).
func NewServerWithClient(c client.Client) *Server {
	return &Server{c: c}
}

// Serve runs the JSON-RPC read/dispatch/write loop until in reaches EOF. Each
// request line is a single JSON object (line-delimited framing); responses are
// written one JSON object per line. Notifications (no id) receive no response.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			// Parse error with no recoverable id.
			if werr := enc.Encode(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error"}}); werr != nil {
				return werr
			}
			continue
		}
		resp, isNotification := s.handle(ctx, req)
		if isNotification {
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *Server) handle(ctx context.Context, req rpcRequest) (rpcResponse, bool) {
	isNotification := len(req.ID) == 0
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}

	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "graphi-query", "version": "1"},
		}
	case "notifications/initialized", "initialized":
		return resp, true // notification, no reply
	case "tools/list":
		resp.Result = map[string]any{"tools": s.toolDescriptors()}
	case "tools/call":
		result, rerr := s.toolsCall(ctx, req.Params)
		if rerr != nil {
			resp.Error = rerr
		} else {
			resp.Result = result
		}
	default:
		resp.Error = &rpcError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)}
	}

	if isNotification {
		return resp, true
	}
	return resp, false
}

// callParams is the tools/call params shape: a tool name plus its arguments.
type callParams struct {
	Name      string `json:"name"`
	Arguments struct {
		Symbol    string `json:"symbol"`
		Target    string `json:"target"`
		Concept   string `json:"concept"`
		Depth     *int   `json:"depth"`
		Analyzer  string `json:"analyzer"`
		Direction string `json:"direction"`
		MaxNodes  *int   `json:"max_nodes"`
		// SW-039 pr-risk scorer arguments (local-first; no remote fetch).
		Diff       string `json:"diff"`
		Provenance string `json:"provenance"`
		// SW-042 sticky PR-comment + merge-gate arguments (local-first).
		PR            string `json:"pr"`
		GateEnabled   bool   `json:"gate_enabled"`
		GateThreshold *int   `json:"gate_threshold"`
		Publish       bool   `json:"publish"`
		// SW-038 edit/refactor + undo arguments.
		Kind            string `json:"kind"`
		TargetSymbol    string `json:"target_symbol"`
		OldName         string `json:"old_name"`
		NewName         string `json:"new_name"`
		DestinationFile string `json:"destination_file"`
		UndoToken       string `json:"undo_token"`
		Actor           string `json:"actor"`
	} `json:"arguments"`
}

// mcpActor is the default actor recorded for edits initiated via the MCP surface
// when the caller supplies none (Scope decision 6: actor is per-surface,
// recorded, excluded from the AC-4 parity comparable subset).
const mcpActor = "mcp"

func (s *Server) toolsCall(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var p callParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params"}
	}

	if p.Name == ToolSearch {
		return s.searchCall(ctx, p)
	}
	if p.Name == ToolSearchSemantic {
		return s.semanticSearchCall(ctx, p)
	}
	if p.Name == ToolSavings {
		return s.savingsCall(ctx)
	}
	if p.Name == ToolAnalyze {
		return s.analysisCall(ctx, p)
	}
	// SW-038 edit/refactor command surface (thin transport over the shared client).
	switch p.Name {
	case ToolRefactorPreview:
		return s.refactorPreviewCall(ctx, p)
	case ToolRefactor:
		return s.refactorCall(ctx, p)
	case ToolUndo:
		return s.undoCall(ctx, p)
	}
	// SW-042 sticky PR-comment writer + optional risk-threshold merge gate.
	if p.Name == ToolPrComment {
		return s.prCommentCall(ctx, p)
	}
	// EP-005 deep-analysis tools (SW-033): each dedicated tool routes through
	// the generic analysis dispatch by injecting its analyzer name.
	if deepAnalyzerName, ok := deepAnalyzerTools[p.Name]; ok {
		p.Arguments.Analyzer = deepAnalyzerName
		return s.analysisCall(ctx, p)
	}

	if p.Arguments.Symbol == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: symbol"}
	}
	depth := 1
	if p.Arguments.Depth != nil {
		depth = *p.Arguments.Depth
	}

	b, err := s.c.Query(ctx, p.Name, p.Arguments.Symbol, depth)
	if err != nil {
		return nil, &rpcError{Code: -32602, Message: err.Error()}
	}

	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}, nil
}

func (s *Server) searchCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Symbol == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: query"}
	}
	limit := search.DefaultResultLimit
	if p.Arguments.Depth != nil && *p.Arguments.Depth > 0 {
		limit = *p.Arguments.Depth
	}
	b, err := s.c.Search(ctx, p.Arguments.Symbol, limit)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}, nil
}

// semanticSearchCall dispatches the OPTIONAL semantic-search tool (SW-059). It
// returns the canonical serialized SemanticResponse from the shared client. When
// no embedder is configured it cleanly reports the typed graceful-skip
// "unavailable" response (Available=false) WITHOUT an error — byte-identical to
// the CLI and HTTP surfaces (parity by construction through the single client
// seam).
func (s *Server) semanticSearchCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Symbol == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: query"}
	}
	limit := search.DefaultResultLimit
	if p.Arguments.Depth != nil && *p.Arguments.Depth > 0 {
		limit = *p.Arguments.Depth
	}
	b, err := s.c.SemanticSearch(ctx, p.Arguments.Symbol, limit)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}, nil
}

// savingsCall dispatches the savings readout tool (SW-020). It returns the
// canonical structured readout (per-call/session/cumulative USD + cap flags) so
// the MCP readout stays byte-identical to the CLI for the same ledger state.
func (s *Server) savingsCall(ctx context.Context) (any, *rpcError) {
	b, err := s.c.Savings(ctx)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}, nil
}

// analysisCall dispatches a named analyzer (SW-022) through the shared client.
// It holds no analysis logic: it builds AnalyzeParams, calls client.Client.Analyze,
// and returns the canonical serialized bytes (byte-identical to the CLI for the
// same inputs, preserving MCP<->CLI parity).
func (s *Server) analysisCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Analyzer == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: analyzer"}
	}
	// The pr-risk scorer (SW-039) is diff-driven, not symbol-driven: it requires
	// a diff argument and accepts no symbol. Every other analyzer requires a symbol.
	if p.Arguments.Analyzer == "pr-risk" || p.Arguments.Analyzer == "pr-signals" || p.Arguments.Analyzer == "pr-questions" {
		if p.Arguments.Diff == "" {
			return nil, &rpcError{Code: -32602, Message: "missing required argument: diff"}
		}
	} else if p.Arguments.Symbol == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: symbol"}
	}
	direction := "forward"
	if p.Arguments.Direction != "" {
		direction = p.Arguments.Direction
	}
	maxNodes := 0
	if p.Arguments.MaxNodes != nil {
		maxNodes = *p.Arguments.MaxNodes
	}
	b, err := s.c.Analyze(ctx, client.AnalyzeParams{
		Name:       p.Arguments.Analyzer,
		Symbol:     p.Arguments.Symbol,
		Target:     p.Arguments.Target,
		Concept:    p.Arguments.Concept,
		Direction:  direction,
		MaxNodes:   maxNodes,
		Diff:       p.Arguments.Diff,
		Provenance: p.Arguments.Provenance,
	})
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}, nil
}

// prCommentCall (SW-042) renders the assembled PR-review findings into one sticky
// Markdown comment and evaluates the optional risk-threshold merge gate through
// the shared client, returning the canonical serialized PublishResult. It holds
// no engine logic: it builds a PrCommentRequest and calls client.Client.PrComment,
// so MCP and CLI emit byte-identical output for the same inputs (parity). The
// diff is diff-driven (required); the default is an offline dry-run.
func (s *Server) prCommentCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Diff == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: diff"}
	}
	threshold := 700
	if p.Arguments.GateThreshold != nil {
		threshold = *p.Arguments.GateThreshold
	}
	provenance := p.Arguments.Provenance
	if provenance == "" {
		provenance = "summary"
	}
	b, err := s.c.PrComment(ctx, client.PrCommentRequest{
		PR:            p.Arguments.PR,
		Diff:          p.Arguments.Diff,
		Provenance:    provenance,
		GateEnabled:   p.Arguments.GateEnabled,
		GateThreshold: threshold,
		Publish:       p.Arguments.Publish,
	})
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}, nil
}

// refactorRequest builds the transport-agnostic request from the MCP arguments.
// TargetSymbol falls back to Symbol so callers can use either field name.
func refactorRequest(p callParams) client.RefactorRequest {
	target := p.Arguments.TargetSymbol
	if target == "" {
		target = p.Arguments.Symbol
	}
	return client.RefactorRequest{
		Kind:            p.Arguments.Kind,
		TargetSymbol:    target,
		OldName:         p.Arguments.OldName,
		NewName:         p.Arguments.NewName,
		DestinationFile: p.Arguments.DestinationFile,
	}
}

// refactorPreviewCall (SW-038) returns the EP-004 impact set BEFORE mutation
// (AC-1) by delegating to the shared client.RefactorPreview. No engine logic.
func (s *Server) refactorPreviewCall(ctx context.Context, p callParams) (any, *rpcError) {
	b, err := s.c.RefactorPreview(ctx, refactorRequest(p))
	if err != nil {
		return nil, &rpcError{Code: -32602, Message: err.Error()}
	}
	return textResult(b), nil
}

// refactorCall (SW-038) commits a refactor through the shared client and returns
// the canonical change record. The actor defaults to "mcp" unless supplied. No
// engine logic — the surface only marshals inputs into a RefactorRequest.
func (s *Server) refactorCall(ctx context.Context, p callParams) (any, *rpcError) {
	actor := p.Arguments.Actor
	if actor == "" {
		actor = mcpActor
	}
	b, err := s.c.Refactor(ctx, refactorRequest(p), actor)
	if err != nil {
		return nil, &rpcError{Code: -32602, Message: err.Error()}
	}
	return textResult(b), nil
}

// undoCall (SW-038) reverses an applied edit by its undo token and returns the
// canonical reversal change record. No engine logic.
func (s *Server) undoCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.UndoToken == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: undo_token"}
	}
	actor := p.Arguments.Actor
	if actor == "" {
		actor = mcpActor
	}
	b, err := s.c.Undo(ctx, p.Arguments.UndoToken, actor)
	if err != nil {
		return nil, &rpcError{Code: -32602, Message: err.Error()}
	}
	return textResult(b), nil
}

// textResult wraps canonical serialized bytes in the MCP tool-result envelope.
func textResult(b []byte) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}
}

// deepAnalyzerTools maps dedicated EP-005 MCP tool names → their analysis
// dispatcher name so each tool name routes through analysisCall after injecting
// the correct analyzer. The map is package-level so both toolsCall routing and
// toolDescriptors advertising can share a single source of truth.
var deepAnalyzerTools = map[string]string{
	ToolAnalyzeTaint:       "taint",
	ToolAnalyzePDG:         "pdg",
	ToolAnalyzeInterproc:   "interproc",
	ToolAnalyzeContracts:   "contracts",
	ToolAnalyzeGitHistory:  "git-history",
	ToolAnalyzePrRisk:      "pr-risk",
	ToolAnalyzePrSignals:   "pr-signals",
	ToolAnalyzePrQuestions: "pr-questions",
}

// deepAnalyzerDescriptors defines the MCP tool schema for each EP-005 deep
// analyzer. Each entry is appended verbatim to the tools/list response when
// the analysis service is available.
var deepAnalyzerDescriptors = []map[string]any{
	{
		"name":        ToolAnalyzeTaint,
		"description": "flow-sensitive taint analysis: finds source-to-sink data-flow paths through the indexed graph",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol":    map[string]any{"type": "string", "description": "symbol (node) id to analyze"},
				"direction": map[string]any{"type": "string", "description": "traversal direction: forward (default) | reverse"},
				"max_nodes": map[string]any{"type": "integer", "description": "output budget on reached nodes (0 = analyzer default)"},
			},
			"required": []string{"symbol"},
		},
	},
	{
		"name":        ToolAnalyzePDG,
		"description": "program dependence graph: computes data-dependence and control-dependence edges via reaching-definitions and post-dominance",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol":    map[string]any{"type": "string", "description": "symbol (node) id to analyze"},
				"max_nodes": map[string]any{"type": "integer", "description": "output budget on reached nodes (0 = analyzer default)"},
			},
			"required": []string{"symbol"},
		},
	},
	{
		"name":        ToolAnalyzeInterproc,
		"description": "interprocedural analysis: Sharir-Pnueli fixpoint solver that computes procedure summaries over the call graph",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol":    map[string]any{"type": "string", "description": "symbol (node) id to analyze"},
				"max_nodes": map[string]any{"type": "integer", "description": "output budget on reached nodes (0 = analyzer default)"},
			},
			"required": []string{"symbol"},
		},
	},
	{
		"name":        ToolAnalyzeContracts,
		"description": "contract drift detection: finds producer/consumer contracts and detects structural drift between linked API surfaces",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol":    map[string]any{"type": "string", "description": "symbol (node) id to analyze"},
				"max_nodes": map[string]any{"type": "integer", "description": "output budget on reached nodes (0 = analyzer default)"},
			},
			"required": []string{"symbol"},
		},
	},
	{
		"name":        ToolAnalyzeGitHistory,
		"description": "git-history signal analysis: computes churn scores, bus-factor risks, and co-change groups from commit history",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol":    map[string]any{"type": "string", "description": "symbol (node) id to analyze"},
				"max_nodes": map[string]any{"type": "integer", "description": "output budget on reached nodes (0 = analyzer default)"},
			},
			"required": []string{"symbol"},
		},
	},
	{
		"name":        ToolAnalyzePrRisk,
		"description": "risk-scored PR diff (SW-039): maps changed nodes onto the graph and combines EP-004 impact with EP-005 taint signals into a deterministic, versioned per-region risk record. Local-first: diff is a unified-diff string or simple ref form; NO remote fetch.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"diff":       map[string]any{"type": "string", "description": "local-first PR diff: a unified-diff string or simple ref form (path:name / path#Lline / bare node id, one per line). No remote fetch."},
				"provenance": map[string]any{"type": "string", "description": "evidence redaction level: full (default) | summary"},
			},
			"required": []string{"diff"},
		},
	},
	{
		"name":        ToolAnalyzePrSignals,
		"description": "hub/bridge/surprise graph signals on PR-changed code (SW-040): annotates each changed node with hub (high fan-in/out over a configurable threshold), bridge (articulation point / cut-vertex between modules), and surprise (rarely-modified or unexpectedly-coupled region) signals. Consumes EP-004 metrics + EP-005 PDG/git-history; never recomputes centrality. Local-first: diff is a unified-diff string or simple ref form; NO remote fetch.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"diff":       map[string]any{"type": "string", "description": "local-first PR diff: a unified-diff string or simple ref form (path:name / path#Lline / bare node id, one per line). No remote fetch."},
				"provenance": map[string]any{"type": "string", "description": "evidence redaction level: full (default) | summary"},
			},
			"required": []string{"diff"},
		},
	},
	{
		"name":        ToolAnalyzePrQuestions,
		"description": "deterministic, no-LLM reviewer questions from graph findings on PR-changed code (SW-041): applies a fixed rule/template set to the consumed SW-039 risk scores and SW-040 hub/bridge/surprise signals to emit targeted reviewer questions. Each question carries a non-empty evidence reference to the triggering node/edge/signal; identical input yields byte-identical output. Consumes the two sibling reports; never recomputes scoring or signals. Local-first: diff is a unified-diff string or simple ref form; NO LLM, NO remote fetch.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"diff":       map[string]any{"type": "string", "description": "local-first PR diff: a unified-diff string or simple ref form (path:name / path#Lline / bare node id, one per line). No remote fetch."},
				"provenance": map[string]any{"type": "string", "description": "evidence redaction level: full (default) | summary"},
			},
			"required": []string{"diff"},
		},
	},
}

// toolDescriptors advertises query tools and, when the client supports it, the
// search tool. The list is derived from the engine's canonical operation list.
func (s *Server) toolDescriptors() []map[string]any {
	tools := make([]map[string]any, 0, len(query.Operations)+2)
	for _, op := range query.Operations {
		props := map[string]any{
			"symbol": map[string]any{"type": "string", "description": "symbol (node) id to query"},
		}
		required := []string{"symbol"}
		if op == query.OpNeighborhood {
			props["depth"] = map[string]any{
				"type":        "integer",
				"description": fmt.Sprintf("hop depth (clamped to MaxNeighborhoodDepth=%d)", query.MaxNeighborhoodDepth),
			}
		}
		tools = append(tools, map[string]any{
			"name":        op,
			"description": "structural query: " + op,
			"inputSchema": map[string]any{"type": "object", "properties": props, "required": required},
		})
	}
	// Probe whether search is available by attempting a dummy search.
	if _, err := s.c.Search(context.Background(), "__probe__", 1); err == nil {
		tools = append(tools, map[string]any{
			"name":        ToolSearch,
			"description": "lexical and symbol search over the indexed graph",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{"type": "string", "description": "search query (symbol token or free-text)"},
					"depth":  map[string]any{"type": "integer", "description": "maximum number of results (default 100)"},
				},
				"required": []string{"symbol"},
			},
		})
	}
	// Optional semantic search (SW-059). Advertised whenever the search tool is —
	// it is always callable through the client and cleanly reports "unavailable"
	// (typed graceful-skip) when no embedder is configured, so there is no
	// capability to probe-hide.
	if _, err := s.c.Search(context.Background(), "__probe__", 1); err == nil {
		tools = append(tools, map[string]any{
			"name":        ToolSearchSemantic,
			"description": "optional semantic (embedding) search over the indexed graph; reports 'unavailable' cleanly when no embedder is configured (OFF by default)",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{"type": "string", "description": "semantic search query (free-text)"},
					"depth":  map[string]any{"type": "integer", "description": "maximum number of results (default 100)"},
				},
				"required": []string{"symbol"},
			},
		})
	}
	// Savings readout (SW-020). Advertised when the client has a ledger attached.
	if _, err := s.c.Savings(context.Background()); err == nil {
		tools = append(tools, map[string]any{
			"name":        ToolSavings,
			"description": "token-savings ledger readout: per-call / per-session / cumulative USD with anti-gaming cap flags",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		})
	}
	// Analyzers (SW-022). Advertised when the client has an analysis service
	// attached (probed: a capability-missing client returns ErrAnalysisUnavailable;
	// any other response — including a not-found result for the probe symbol —
	// means the service is configured).
	if _, err := s.c.Analyze(context.Background(), client.AnalyzeParams{
		Name: "impact", Symbol: "__probe__", Direction: "forward",
	}); err == nil || !isAnalysisUnavailable(err) {
		tools = append(tools, map[string]any{
			"name":        ToolAnalyze,
			"description": "run a named graph analyzer (e.g. impact forward/reverse blast-radius reachability) over the indexed graph",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"analyzer":  map[string]any{"type": "string", "description": "analyzer name (e.g. impact)"},
					"symbol":    map[string]any{"type": "string", "description": "symbol (node) id to analyze"},
					"direction": map[string]any{"type": "string", "description": "traversal direction for directional analyzers: forward (dependents/blast-radius) | reverse (dependencies)"},
					"max_nodes": map[string]any{"type": "integer", "description": "output budget on reached nodes (0 = analyzer default)"},
				},
				"required": []string{"analyzer", "symbol"},
			},
		})
		// EP-005 (SW-033): advertise one dedicated tool per deep analyzer.
		tools = append(tools, deepAnalyzerDescriptors...)
	}
	// SW-038 edit/refactor command surface. Advertised when an edit applier is
	// attached (probed: a capability-missing client returns ErrEditUnavailable; a
	// validation error for the probe request still means the service is wired).
	if _, err := s.c.RefactorPreview(context.Background(), client.RefactorRequest{
		Kind: "rename", TargetSymbol: "__probe__", OldName: "__probe__", NewName: "__probe2__",
	}); err == nil || !isEditUnavailable(err) {
		tools = append(tools, editToolDescriptors...)
	}
	// SW-042 sticky PR-comment + merge-gate surface. Advertised when a review
	// publisher is attached (probed: a capability-missing client returns
	// ErrReviewUnavailable; any other response means the service is wired).
	if _, err := s.c.PrComment(context.Background(), client.PrCommentRequest{Diff: "__probe__"}); err == nil || !isReviewUnavailable(err) {
		tools = append(tools, map[string]any{
			"name":        ToolPrComment,
			"description": "render the assembled PR-review findings (risk + hub/bridge/surprise signals + reviewer questions) into one sticky Markdown comment and evaluate the optional risk-threshold merge gate; offline dry-run by default",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"diff":           map[string]any{"type": "string", "description": "local-first unified-diff or simple ref string (required)"},
					"pr":             map[string]any{"type": "string", "description": "PR reference rendered in the comment header (e.g. owner/repo#42)"},
					"provenance":     map[string]any{"type": "string", "description": "evidence redaction level: summary (default; safe for public comments) | full"},
					"gate_enabled":   map[string]any{"type": "boolean", "description": "enable the optional risk-threshold merge gate"},
					"gate_threshold": map[string]any{"type": "integer", "description": "risk threshold in fixed-point units (1/1000) the worst region must EXCEED to BLOCK (default 700)"},
					"publish":        map[string]any{"type": "boolean", "description": "upsert the sticky comment through the host (default false: offline dry-run, render+gate only)"},
				},
				"required": []string{"diff"},
			},
		})
	}
	return tools
}

// isEditUnavailable reports whether err is the edit-capability-missing sentinel.
func isEditUnavailable(err error) bool {
	return errors.Is(err, client.ErrEditUnavailable)
}

// editToolDescriptors defines the MCP tool schema for the SW-038 edit/refactor
// command surface (refactor-preview, refactor, undo). Each routes through the
// shared client; the surface holds no engine logic.
var editToolDescriptors = []map[string]any{
	{
		"name":        ToolRefactorPreview,
		"description": "preview a graph-aware refactor: resolve the target via the query layer and return the EP-004 impact set (blast radius + planned edits) WITHOUT mutating",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"kind":             map[string]any{"type": "string", "description": "refactor kind: rename|extract|move|signature_change"},
				"target_symbol":    map[string]any{"type": "string", "description": "resolved node id of the symbol to refactor"},
				"old_name":         map[string]any{"type": "string", "description": "current spelling of the symbol"},
				"new_name":         map[string]any{"type": "string", "description": "replacement spelling"},
				"destination_file": map[string]any{"type": "string", "description": "destination file (move only)"},
			},
			"required": []string{"kind", "target_symbol", "old_name", "new_name"},
		},
	},
	{
		"name":        ToolRefactor,
		"description": "apply a graph-aware refactor through the shared atomic edit saga and return an auditable change record (operation, target, before/after, actor, timestamp, undo token)",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"kind":             map[string]any{"type": "string", "description": "refactor kind: rename|extract|move|signature_change"},
				"target_symbol":    map[string]any{"type": "string", "description": "resolved node id of the symbol to refactor"},
				"old_name":         map[string]any{"type": "string", "description": "current spelling of the symbol"},
				"new_name":         map[string]any{"type": "string", "description": "replacement spelling"},
				"destination_file": map[string]any{"type": "string", "description": "destination file (move only)"},
				"actor":            map[string]any{"type": "string", "description": "request identity recorded on the change record (default \"mcp\")"},
			},
			"required": []string{"kind", "target_symbol", "old_name", "new_name"},
		},
	},
	{
		"name":        ToolUndo,
		"description": "reverse a previously applied edit by its undo token, restoring the prior graph + source and recording the reversal as its own auditable change record",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"undo_token": map[string]any{"type": "string", "description": "the undo token returned by a prior refactor"},
				"actor":      map[string]any{"type": "string", "description": "request identity recorded on the reversal record (default \"mcp\")"},
			},
			"required": []string{"undo_token"},
		},
	},
}

// isAnalysisUnavailable reports whether err is the capability-missing sentinel.
// Factored out so the probe logic reads intent rather than string-matching.
func isAnalysisUnavailable(err error) bool {
	return errors.Is(err, client.ErrAnalysisUnavailable)
}

// isReviewUnavailable reports whether err is the SW-042 "PR-review publisher not
// wired" sentinel, so the descriptor probe can hide the pr_comment tool when the
// client has no review service attached (mirrors isAnalysisUnavailable /
// isEditUnavailable).
func isReviewUnavailable(err error) bool {
	return errors.Is(err, client.ErrReviewUnavailable)
}
