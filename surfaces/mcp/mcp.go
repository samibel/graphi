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
		// EP-011 G1 compound query text (SEED/HOP/WHERE/MAXDEPTH).
		Query string `json:"query"`
		// SW-085 pattern-query arguments: search_ast JSON pattern + limit, and the
		// find_clones JSON config.
		Pattern string `json:"pattern"`
		Limit   *int   `json:"limit"`
		Config  string `json:"config"`
		// EP-012 memory arguments.
		Op           string   `json:"op"`
		Scope        string   `json:"scope"`
		Notebook     string   `json:"notebook"`
		Tags         []string `json:"tags"`
		Payload      string   `json:"payload"`
		MemID        string   `json:"mem_id"`
		Source       string   `json:"source"`
		Confidence   string   `json:"confidence"`
		Evidence     string   `json:"evidence"`
		ExportToPath string   `json:"export_to_path"`
		// EP-012 distill arguments.
		SessionID      string        `json:"session_id"`
		Turns          []client.Turn `json:"turns"`
		Decisions      []string      `json:"decisions"`
		Risks          []string      `json:"risks"`
		OpenQuestions  []string      `json:"open_questions"`
		FileReferences []string      `json:"file_references"`
		// EP-012 skillgen arguments.
		Name         string             `json:"name"`
		Trigger      string             `json:"trigger"`
		Description  string             `json:"description"`
		SkillInputs  []string           `json:"skill_inputs"`
		SkillOutputs []string           `json:"skill_outputs"`
		SkillSteps   []client.SkillStep `json:"skill_steps"`

		// SW-107 compare_branches base/head branch refs (suggest_reviewers reuses the
		// shared `diff` argument above).
		Base string `json:"base"`
		Head string `json:"head"`

		// SW-108 critique_review: the PR number to fetch the existing review for (when
		// no inline review is supplied) and an inline existing-review JSON string. The
		// touched set reuses the shared `diff` argument above. The review is structured
		// at the surface; the engine never parses a raw blob or fetches.
		PRNumber int    `json:"pr_number"`
		Review   string `json:"review"`
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
	// EP-011 G1 compound query.
	if p.Name == ToolCompound {
		return s.compoundCall(ctx, p)
	}
	// SW-085 pattern-query singletons.
	switch p.Name {
	case ToolSearchAST:
		return s.searchASTCall(ctx, p)
	case ToolFindClones:
		return s.findClonesCall(ctx, p)
	}
	// EP-012 agent memory & skills.
	switch p.Name {
	case ToolMemory:
		return s.memoryCall(ctx, p)
	case ToolDistill:
		return s.distillCall(ctx, p)
	case ToolSkillGen:
		return s.skillGenCall(ctx, p)
	}
	// EP-018 multi-PR triage suite (SW-105): list_prs (read-only forge enumeration)
	// and triage_prs (single-pass graph-derived ranking). Both ride the shared
	// client seam, so the bytes are byte-identical across surfaces.
	switch p.Name {
	case ToolListPRs:
		return s.listPRsCall(ctx)
	case ToolTriagePRs:
		return s.triagePRsCall(ctx)
	case ToolConflictsPRs:
		return s.conflictsPRsCall(ctx)
	}
	// EP-018 SW-107: suggest_reviewers (ranked candidate reviewers from the touched
	// set) and compare_branches (graph-level diff of two branch states). Both ride
	// the shared client seam, so the bytes are byte-identical across surfaces.
	switch p.Name {
	case ToolSuggestReviewers:
		return s.suggestReviewersCall(ctx, p)
	case ToolCompareBranches:
		return s.compareBranchesCall(ctx, p)
	}
	// EP-018 SW-108 (capstone): critique_review (graph-evidence critique of an existing
	// review). Rides the shared client seam, so the bytes are byte-identical across
	// surfaces.
	if p.Name == ToolCritiqueReview {
		return s.critiqueReviewCall(ctx, p)
	}
	// EP-005 deep-analysis tools (SW-033): each dedicated tool routes through
	// the generic analysis dispatch by injecting its analyzer name.
	if deepAnalyzerName, ok := deepAnalyzerTools[p.Name]; ok {
		p.Arguments.Analyzer = deepAnalyzerName
		return s.analysisCall(ctx, p)
	}

	// EP-020 agent-first task tools (SW-115 / SW-116 / SW-117) plus EP-024 (SW-134).
	// These are advertised unconditionally because they require no extra capability probe.
	switch p.Name {
	case ToolExplainSymbol:
		return s.explainSymbolCall(ctx, p)
	case ToolRelatedFiles:
		return s.relatedFilesCall(ctx, p)
	case ToolChangeRisk:
		return s.changeRiskCall(ctx, p)
	case ToolAgentBrief:
		return s.agentBriefCall(ctx, p)
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

// compoundCall runs a compound / Cypher-style graph query (EP-011 G1). The
// query text is the single `query` argument; the result bytes are the canonical
// query.Result, byte-identical to every fixed query across surfaces.
func (s *Server) compoundCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Query == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: query"}
	}
	b, err := s.c.Compound(ctx, p.Arguments.Query)
	if err != nil {
		return nil, &rpcError{Code: -32602, Message: err.Error()}
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}, nil
}

// memoryCall runs an EP-012 memory operation through the shared client and
// returns the canonical serialized MemoryResponse.
func (s *Server) memoryCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Op == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: op"}
	}
	b, err := s.c.Memory(ctx, client.MemoryRequest{
		Op:           p.Arguments.Op,
		Scope:        p.Arguments.Scope,
		Notebook:     p.Arguments.Notebook,
		Tags:         p.Arguments.Tags,
		Payload:      p.Arguments.Payload,
		ID:           p.Arguments.MemID,
		Kind:         p.Arguments.Kind,
		Source:       p.Arguments.Source,
		Confidence:   p.Arguments.Confidence,
		Evidence:     p.Arguments.Evidence,
		Limit:        derefInt(p.Arguments.Limit),
		ExportToPath: p.Arguments.ExportToPath,
	})
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}

// distillCall runs EP-012 session distillation through the shared client and
// returns the canonical serialized DistillResponse.
func (s *Server) distillCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.SessionID == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: session_id"}
	}
	b, err := s.c.Distill(ctx, client.DistillRequest{
		SessionID:      p.Arguments.SessionID,
		Turns:          p.Arguments.Turns,
		Decisions:      p.Arguments.Decisions,
		Risks:          p.Arguments.Risks,
		OpenQuestions:  p.Arguments.OpenQuestions,
		FileReferences: p.Arguments.FileReferences,
	})
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}

// skillGenCall runs EP-012 deterministic skill generation through the shared
// client and returns the canonical serialized SkillGenResponse.
func (s *Server) skillGenCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Name == "" || p.Arguments.Trigger == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required arguments: name and trigger"}
	}
	b, err := s.c.SkillGen(ctx, client.SkillGenRequest{
		Name:        p.Arguments.Name,
		Trigger:     p.Arguments.Trigger,
		Description: p.Arguments.Description,
		Inputs:      p.Arguments.SkillInputs,
		Outputs:     p.Arguments.SkillOutputs,
		Steps:       p.Arguments.SkillSteps,
	})
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
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

// searchASTCall dispatches the structural AST pattern query (SW-082 / SW-085)
// through the shared client. The JSON pattern rides the `pattern` argument and the
// optional `limit` bounds results; the returned bytes are the canonical
// query.Marshal output, byte-identical to the CLI and HTTP surfaces. A malformed
// pattern surfaces the engine's typed error as a JSON-RPC error (no new shape).
func (s *Server) searchASTCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Pattern == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: pattern"}
	}
	limit := 0
	if p.Arguments.Limit != nil {
		limit = *p.Arguments.Limit
	}
	b, err := s.c.SearchAST(ctx, p.Arguments.Pattern, limit)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}, nil
}

// findClonesCall dispatches the clone-detection query (SW-083 / SW-085) through the
// shared client. The optional JSON config rides the `config` argument (empty ⇒
// engine defaults); the returned bytes are the canonical query.MarshalCloneResult
// output for byte-identical parity.
func (s *Server) findClonesCall(ctx context.Context, p callParams) (any, *rpcError) {
	b, err := s.c.FindClones(ctx, p.Arguments.Config)
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
	// a diff argument and accepts no symbol. The SW-104 EP-017 operations
	// (communities, watcher-status, notebook-ingest, taint-query) are whole-graph /
	// status operations needing no symbol (shared client.AnalyzerSymbolOptional rule,
	// identical to the CLI). Every other analyzer requires a symbol.
	switch {
	case p.Arguments.Analyzer == "pr-risk" || p.Arguments.Analyzer == "pr-signals" || p.Arguments.Analyzer == "pr-questions":
		if p.Arguments.Diff == "" {
			return nil, &rpcError{Code: -32602, Message: "missing required argument: diff"}
		}
	case client.AnalyzerSymbolOptional(p.Arguments.Analyzer):
		// no required symbol argument
	case p.Arguments.Symbol == "":
		return nil, &rpcError{Code: -32602, Message: "missing required argument: symbol"}
	}
	// Direction passes through verbatim; the ENGINE owns the default (empty →
	// reverse = dependents/blast radius since the v0.1.3 direction fix). A
	// surface-side fallback here would silently shadow that single source of
	// truth.
	direction := p.Arguments.Direction
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

// listPRsCall (SW-105) enumerates open PRs through the read-only forge boundary
// and returns the canonical serialized forge.PRList. It holds no engine logic and
// performs no scoring — pure metadata enumeration through the shared client.
func (s *Server) listPRsCall(ctx context.Context) (any, *rpcError) {
	b, err := s.c.ListPRs(ctx)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}

// triagePRsCall (SW-105) returns the single-pass graph-derived ranked PR triage
// through the shared client (forge enumeration → zero-egress engine analyzer →
// shared encoder), so the ranked TriageReport is byte-identical across surfaces.
func (s *Server) triagePRsCall(ctx context.Context) (any, *rpcError) {
	b, err := s.c.TriagePRs(ctx)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}

// conflictsPRsCall (SW-106) returns the inter-PR conflict report through the
// shared client (forge enumeration → zero-egress engine analyzer → shared
// encoder), so the pairwise ConflictReport is byte-identical across surfaces.
func (s *Server) conflictsPRsCall(ctx context.Context) (any, *rpcError) {
	b, err := s.c.ConflictsPRs(ctx)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}

// suggestReviewersCall (SW-107) returns the ranked candidate-reviewer report
// through the shared client (touched-set resolution → zero-egress engine analyzer →
// shared encoder), so the ReviewerReport is byte-identical across surfaces. The
// `diff` argument is the local-first PR diff / line-oriented ref string.
func (s *Server) suggestReviewersCall(ctx context.Context, p callParams) (any, *rpcError) {
	b, err := s.c.SuggestReviewers(ctx, p.Arguments.Diff)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}

// compareBranchesCall (SW-107) returns the graph-level branch diff through the
// shared client (base/head materialized above the surface boundary → zero-egress
// engine analyzer → shared encoder), so the BranchDiffReport is byte-identical
// across surfaces. The `base`/`head` arguments are branch refs.
func (s *Server) compareBranchesCall(ctx context.Context, p callParams) (any, *rpcError) {
	b, err := s.c.CompareBranches(ctx, p.Arguments.Base, p.Arguments.Head)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}

// critiqueReviewCall (SW-108) returns the graph-evidence critique of an existing
// review through the shared client (surface review fetch / inline review → zero-egress
// engine analyzer → shared encoder), so the CritiqueReport is byte-identical across
// surfaces. The `pr_number` selects the review to fetch; `review` supplies an inline
// review JSON (takes precedence); `diff` is the PR's touched set.
func (s *Server) critiqueReviewCall(ctx context.Context, p callParams) (any, *rpcError) {
	b, err := s.c.CritiqueReview(ctx, p.Arguments.PRNumber, p.Arguments.Diff, p.Arguments.Review)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
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

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// agentBriefCall (SW-134) returns a bounded, cited task-start context packet
// in the C1 contract shape, plus a Markdown rendering in a fenced JSON block.
// It rides the shared client seam so MCP and CLI emit the same canonical bytes
// (and both see the graph/memory-backed content when those services are wired).
func (s *Server) agentBriefCall(ctx context.Context, p callParams) (any, *rpcError) {
	b, md, err := s.c.Brief(ctx, p.Arguments.Symbol)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	// Dual-format delivery: human-readable Markdown with fenced canonical JSON.
	text := string(md) + "\n\n```json\n" + string(b) + "\n```\n"
	return textResult([]byte(text)), nil
}

// explainSymbolCall (SW-115) returns a compact symbol-identity summary in the C1
// contract shape. It rides the shared client seam (engine/agenttools/explain
// behind it) so MCP and CLI emit the same canonical bytes.
func (s *Server) explainSymbolCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Symbol == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: symbol"}
	}
	b, err := s.c.ExplainSymbol(ctx, p.Arguments.Symbol, derefInt(p.Arguments.Limit))
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}

// relatedFilesCall (SW-116) returns a deterministically ranked read-first file
// list in the C1 contract shape.
func (s *Server) relatedFilesCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Target == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: target"}
	}
	b, err := s.c.RelatedFiles(ctx, p.Arguments.Target, p.Arguments.Direction, derefInt(p.Arguments.Limit))
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}

// changeRiskCall (SW-117) returns a change-risk evaluation in the C1 contract
// shape.
func (s *Server) changeRiskCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Target == "" && p.Arguments.Diff == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: target or diff"}
	}
	b, err := s.c.ChangeRisk(ctx, p.Arguments.Target, p.Arguments.Diff, derefInt(p.Arguments.Limit))
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
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
				"direction": map[string]any{"type": "string", "description": "traversal direction: reverse (dependents/blast radius — the default) | forward (dependencies)"},
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
	// SW-085 pattern-query tools. Advertised whenever search is — they ride the
	// same in-process query.Service and reuse the canonical engine serializers, so
	// there is no separate capability to probe. Per AC4 they carry the explicit
	// annotation set: read-only, idempotent, non-destructive, closed-world.
	if _, err := s.c.Search(context.Background(), "__probe__", 1); err == nil {
		tools = append(tools, map[string]any{
			"name":        ToolSearchAST,
			"description": "structural AST pattern search (SW-082): match nodes by kind/name/parent_kind; returns node identity + parent context only, never a file body",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{"type": "string", "description": "JSON AstPattern, e.g. {\"kind\":\"function\",\"name\":{\"regex\":\"^handle_\"}}"},
					"limit":   map[string]any{"type": "integer", "description": "maximum number of matches (applied after the canonical sort)"},
				},
				"required": []string{"pattern"},
			},
			"annotations": readOnlyToolAnnotations(),
		})
		tools = append(tools, map[string]any{
			"name":        ToolFindClones,
			"description": "clone-group detection (SW-083): reports exact/renamed/structural clone groups derived from the AST edge sets; deterministic and bounded by max_groups",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"config": map[string]any{"type": "string", "description": "optional JSON CloneConfig (threshold, max_groups, clone_kinds, min_edges); empty uses engine defaults"},
				},
			},
			"annotations": readOnlyToolAnnotations(),
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
					"direction": map[string]any{"type": "string", "description": "traversal direction for directional analyzers: reverse (dependents/blast radius — the default) | forward (dependencies)"},
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
	// EP-011 G1 compound query (singleton descriptor; input is query text).
	tools = append(tools, map[string]any{
		"name":        ToolCompound,
		"description": "compound / Cypher-style graph query composing traversals, filters, and projections in one request (SEED/HOP/WHERE/MAXDEPTH text form)",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "compound query text: SEED <id> then HOP <in|out|both> [<kind>] lines, optional WHERE KIND <kind>"},
			},
			"required": []string{"query"},
		},
	})
	// EP-012 agent memory & skills. Advertised when the client has the services
	// wired (probed by attempting the operation; unavailable clients return the
	// capability-missing sentinel).
	if _, err := s.c.Memory(context.Background(), client.MemoryRequest{Op: "recall"}); err == nil || !isMemoryUnavailable(err) {
		tools = append(tools, map[string]any{
			"name":        ToolMemory,
			"description": "scoped agent memory: store, recall, forget, list, or export notes in scopes and notebooks with provenance",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"op":             map[string]any{"type": "string", "description": "operation: store | recall | forget | list | export"},
					"scope":          map[string]any{"type": "string", "description": "memory scope"},
					"notebook":       map[string]any{"type": "string", "description": "memory notebook"},
					"tags":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "tags for store"},
					"payload":        map[string]any{"type": "string", "description": "payload for store"},
					"mem_id":         map[string]any{"type": "string", "description": "entry id for forget or overwrite"},
					"kind":           map[string]any{"type": "string", "description": "entry kind for store: architecture | command | convention | decision | risk | dependency | workflow"},
					"source":         map[string]any{"type": "string", "description": "provenance source for store"},
					"confidence":     map[string]any{"type": "string", "description": "confirmed | derived | heuristic"},
					"evidence":       map[string]any{"type": "string", "description": "optional file:line citation"},
					"limit":          map[string]any{"type": "integer", "description": "max entries for list"},
					"export_to_path": map[string]any{"type": "string", "description": "destination file for export"},
				},
				"required": []string{"op"},
			},
		})
	}
	if _, err := s.c.Distill(context.Background(), client.DistillRequest{SessionID: "__probe__"}); err == nil || !isDistillUnavailable(err) {
		tools = append(tools, map[string]any{
			"name":        ToolDistill,
			"description": "deterministic, non-LLM session distillation: compress a session trace into a reusable artifact",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id":      map[string]any{"type": "string", "description": "session identifier"},
					"decisions":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"risks":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"open_questions":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"file_references": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"required": []string{"session_id"},
			},
		})
	}
	if _, err := s.c.SkillGen(context.Background(), client.SkillGenRequest{Name: "__probe__", Trigger: "__probe__"}); err == nil || !isSkillGenUnavailable(err) {
		tools = append(tools, map[string]any{
			"name":        ToolSkillGen,
			"description": "deterministic, non-LLM skill generation: turn a repeatable procedure into a Markdown skill artifact",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":        map[string]any{"type": "string", "description": "skill name"},
					"trigger":     map[string]any{"type": "string", "description": "skill trigger phrase"},
					"description": map[string]any{"type": "string", "description": "skill description"},
				},
				"required": []string{"name", "trigger"},
			},
		})
	}
	// EP-018 multi-PR triage suite (SW-105). Advertised when a forge
	// PR-enumeration client is wired (probed: a capability-missing client returns
	// ErrForgeUnavailable; any other response means the boundary is configured).
	if _, err := s.c.ListPRs(context.Background()); err == nil || !isForgeUnavailable(err) {
		tools = append(tools, map[string]any{
			"name":        ToolListPRs,
			"description": "list open pull requests of the configured repo with read-only forge metadata (number, title, author, base/head refs, head SHA, changed files, additions/deletions, mergeable). Discovery/metadata ONLY — no graph scoring, no comment posting. The forge enumeration is the suite's only outbound path.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		})
		tools = append(tools, map[string]any{
			"name":        ToolTriagePRs,
			"description": "single-pass graph-derived PR triage: enumerate open PRs, then rank them by blast radius, touched high-centrality nodes, ownership concentration, churn, and test-coverage-of-touched-code, folded into a fixed-integer composite. Deterministic total order (composite DESC, PR number ASC). Scoring is a zero-egress pass over the local graph; the forge is touched only for enumeration.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			"annotations": readOnlyToolAnnotations(),
		})
		tools = append(tools, map[string]any{
			"name":        ToolConflictsPRs,
			"description": "inter-PR conflict detection: enumerate open PRs, then report which PR PAIRS collide over the local graph — textual overlap (overlapping changed line ranges in the same file), shared file/symbol/high-centrality node, and the asymmetric contract-dependency case (one PR mutates a contract that another PR's changed entities depend on via graph edges, flagged even with NO textual overlap). Deterministic pairwise report (pairs by ascending PR number, canonical within-pair entity order). Detection is a zero-egress pass over the local graph; the forge is touched only for enumeration.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			"annotations": readOnlyToolAnnotations(),
		})
	}
	// EP-018 SW-107: suggest_reviewers is advertised when the analysis service is
	// wired (probed: a capability-missing client returns ErrAnalysisUnavailable; an
	// empty-diff probe otherwise completes with an empty report).
	if _, err := s.c.SuggestReviewers(context.Background(), "__probe__"); err == nil || !isAnalysisUnavailable(err) {
		tools = append(tools, map[string]any{
			"name":        ToolSuggestReviewers,
			"description": "suggest reviewers for a change: resolve the touched symbol/file set from a local-first PR diff (or line-oriented refs), then rank candidate reviewers from graph ownership + recency-decayed churn over the touched files plus affected-subgraph proximity (callers/callees/contract neighbors) of the touched symbols. Each candidate carries a transparent per-signal breakdown (ownership/recency-decayed-churn/subgraph-proximity) with honest file-vs-symbol granularity labels. Deterministic total order (composite DESC, reviewer identity ASC). Zero-egress pass over the local graph + git history.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"diff": map[string]any{"type": "string", "description": "unified diff or line-oriented refs (path:name | path#Lline | node id) of the change"},
				},
			},
			"annotations": readOnlyToolAnnotations(),
		})
	}
	// EP-018 SW-107: compare_branches is advertised when a branch-state materializer
	// is wired (probed: a capability-missing client returns ErrCompareUnavailable;
	// otherwise the empty-ref probe materializes empty states and completes).
	if _, err := s.c.CompareBranches(context.Background(), "__base__", "__head__"); err == nil || !isCompareUnavailable(err) {
		tools = append(tools, map[string]any{
			"name":        ToolCompareBranches,
			"description": "compare two branches at the GRAPH level: given two branch refs (states materialized above the surface boundary), report the structured diff of entities/symbols/contracts added/removed/changed plus edges added/removed and entities moved across files — keyed by stable canonical graph identity (NodeId), not line ranges. Detects signature/contract changes (a contract node whose dependency surface changed) and correlates moves by path-independent symbol identity. Deterministic per-group order. Zero-egress pure local set-diff; the engine never resolves a git ref.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"base": map[string]any{"type": "string", "description": "base branch ref"},
					"head": map[string]any{"type": "string", "description": "head branch ref"},
				},
			},
			"annotations": readOnlyToolAnnotations(),
		})
	}
	// EP-018 SW-108 (capstone): critique_review is advertised when the analysis
	// service is wired (probed with an inline empty review so the probe never triggers
	// the surface review-fetch egress; a capability-missing client returns
	// ErrAnalysisUnavailable).
	if _, err := s.c.CritiqueReview(context.Background(), 0, "__probe__", "{}"); err == nil || !isAnalysisUnavailable(err) {
		tools = append(tools, map[string]any{
			"name":        ToolCritiqueReview,
			"description": "critique an EXISTING PR review against the knowledge graph: replay the single-PR risk/blast-radius/centrality/taint signals as a ground-truth oracle over the PR's touched set, then emit a structured, graph-evidence-grounded critique with three item types — gap (a high-risk touched entity the review never mentioned: blast-radius count + centrality + contributing edge kinds + taint provenance), over_flag (a review-flagged entity the graph shows is a low-centrality leaf below the risk threshold), and unsupported_claim (a review comment asserting impact to an anchorable target with NO connecting graph edge). Comment→entity matching is DETERMINISTIC anchoring (file:line/symbol → NodeId); unanchorable comments/claims are counted in an honest unanchored tally, never guessed. NO LLM prose. Deterministic total order (type → entity NodeId → review-anchor). The review is fetched at the surface boundary (or supplied inline); the critique itself is a zero-egress pass over the local graph.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pr_number": map[string]any{"type": "integer", "description": "PR number to fetch the existing review for (when no inline review is supplied)"},
					"diff":      map[string]any{"type": "string", "description": "the PR's touched set: unified diff or line-oriented refs (path:name | path#Lline | node id)"},
					"review":    map[string]any{"type": "string", "description": "inline existing-review JSON ({verdict, comments:[{id,path,line,symbol,claim_targets}]}); takes precedence over the surface fetch"},
				},
			},
			"annotations": readOnlyToolAnnotations(),
		})
	}
	// EP-020 agent-first task tools (SW-115 / SW-116 / SW-117) plus EP-024 (SW-134). Advertised
	// unconditionally: they require only the engine/agenttools packages, not a
	// separate capability probe. Each descriptor uses the hardened six-facet
	// template (purpose, when-to-use, when-not-to-use, input shape, read-only,
	// partial-possible) and carries explicit read-only annotations.
	tools = append(tools, map[string]any{
		"name":        ToolExplainSymbol,
		"description": "explain_symbol: return a compact, cited symbol-identity summary (qualified name, kind, declaring file:line, direct callers/callees). Purpose: answer 'what is this symbol?' in one call. When to use: the agent has a symbol reference and needs identity + immediate neighborhood without reading source. When NOT to use: for broad 'what should I read first?' questions (use related_files) or risk scoring (use change_risk). Input shape: a single symbol reference (qualified id, file:line, or bare name). Read-only: true. Partial results possible: neighbor lists may truncate.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol": map[string]any{"type": "string", "description": "symbol reference: qualified id, file:line anchor, or bare name"},
			},
			"required": []string{"symbol"},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	tools = append(tools, map[string]any{
		"name":        ToolRelatedFiles,
		"description": "related_files: return a deterministically ranked 'read these first' file list for a symbol, file, or diff anchor. Purpose: answer 'what should I read first?' in one call. When to use: the agent needs a scoped, evidence-backed file list before editing or reviewing. When NOT to use: for a single symbol's identity (use explain_symbol) or for risk scoring (use change_risk). Input shape: a single anchor plus optional direction (dependencies | dependents | both). Read-only: true. Partial results possible: ranked file list may truncate.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target":    map[string]any{"type": "string", "description": "anchor: symbol id, file path, or diff line-oriented refs"},
				"direction": map[string]any{"type": "string", "description": "dependencies | dependents | both (default)"},
			},
			"required": []string{"target"},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	tools = append(tools, map[string]any{
		"name":        ToolChangeRisk,
		"description": "change_risk: return an evidence-based low/medium/high/unknown risk assessment for a symbol, file, or diff target. Purpose: answer 'how risky is it to touch this?' in one call. When to use: before proposing or reviewing a change, to gauge blast radius and coverage. When NOT to use: when you only need a file list (use related_files) or a symbol summary (use explain_symbol). Input shape: a target symbol/file or a local-first diff. Read-only: true. Partial results possible: evidence may be truncated, and the tool returns unknown rather than guessing.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target": map[string]any{"type": "string", "description": "symbol id or file path to evaluate"},
				"diff":   map[string]any{"type": "string", "description": "local-first unified diff or line-oriented refs (alternative to target)"},
			},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// EP-024 agent_brief: bounded task-start context packet.
	tools = append(tools, map[string]any{
		"name":        ToolAgentBrief,
		"description": "agent_brief: return a bounded, cited task-start context packet (project identity, start-here files, key symbols, known facts, hotspots, suggested next MCP calls) in Markdown with embedded canonical JSON. Purpose: give an agent a scoped, cited starting context without reading source blindly. When to use: at the beginning of a task or when entering a new subsystem. When NOT to use: when you already have a specific symbol to explain (use explain_symbol) or a file list to read (use related_files). Input shape: optional topic (symbol, path, or subsystem). Read-only: true. Partial results possible: sections may be empty if underlying analyzers are not yet wired.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol": map[string]any{"type": "string", "description": "optional topic: symbol id, file path, or subsystem name"},
			},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// Central stability-tier marking (single source: StableOperations in
	// tools.go) — every advertised tool outside the frozen 12-op stable set is
	// prefixed [labs]; descriptor literals never carry the tag by hand.
	return markLabs(tools)
}

// isForgeUnavailable reports whether err is the SW-105 "forge PR-enumeration
// client not wired" sentinel, so the descriptor probe can hide list_prs/triage_prs
// when no forge boundary is attached (mirrors isAnalysisUnavailable).
func isForgeUnavailable(err error) bool {
	return errors.Is(err, client.ErrForgeUnavailable)
}

// isCompareUnavailable reports whether err is the SW-107 "branch-state materializer
// not wired" sentinel, so the descriptor probe can hide compare_branches when no
// materializer is attached (mirrors isForgeUnavailable).
func isCompareUnavailable(err error) bool {
	return errors.Is(err, client.ErrCompareUnavailable)
}

// isMemoryUnavailable reports whether err is the capability-missing sentinel.
func isMemoryUnavailable(err error) bool {
	return errors.Is(err, client.ErrMemoryUnavailable)
}

// isDistillUnavailable reports whether err is the capability-missing sentinel.
func isDistillUnavailable(err error) bool {
	return errors.Is(err, client.ErrDistillUnavailable)
}

// isSkillGenUnavailable reports whether err is the capability-missing sentinel.
func isSkillGenUnavailable(err error) bool {
	return errors.Is(err, client.ErrSkillGenUnavailable)
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
