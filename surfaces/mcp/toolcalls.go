package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/client"
)

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
	if !s.toolAdvertised(p.Name) {
		message := fmt.Sprintf("tool not available: %s", p.Name)
		if !s.labs && !IsStableMCPTool(p.Name) {
			message += " (the default MCP profile is Stable; restart with -labs to opt in to Labs tools)"
		}
		return nil, &rpcError{Code: -32602, Message: message}
	}

	if p.Name == ToolSearch {
		return s.searchCall(ctx, p)
	}
	if p.Name == ToolImpact {
		return s.impactCall(ctx, p)
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
	// Catalog filtering has already established that this binding supports them.
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

	b, err := s.stableClient().Query(ctx, p.Name, p.Arguments.Symbol, depth)
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
	b, err := s.client().Compound(ctx, p.Arguments.Query)
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
	b, err := s.client().Memory(ctx, client.MemoryRequest{
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
	b, err := s.client().Distill(ctx, client.DistillRequest{
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
	b, err := s.client().SkillGen(ctx, client.SkillGenRequest{
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
	b, err := s.stableClient().Search(ctx, p.Arguments.Symbol, limit)
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
	b, err := s.client().SemanticSearch(ctx, p.Arguments.Symbol, limit)
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
	b, err := s.client().SearchAST(ctx, p.Arguments.Pattern, limit)
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
	b, err := s.client().FindClones(ctx, p.Arguments.Config)
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
	b, err := s.client().Savings(ctx)
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
	b, err := s.client().Analyze(ctx, client.AnalyzeParams{
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

// impactCall is the dedicated stable analyzer port. The wire input has no
// analyzer selector and any injected analyzer field is ignored: this path can
// dispatch only the frozen "impact" operation through StableClient.Impact.
func (s *Server) impactCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Symbol == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: symbol"}
	}
	maxNodes := 0
	if p.Arguments.MaxNodes != nil {
		maxNodes = *p.Arguments.MaxNodes
	}
	b, err := s.stableClient().Impact(ctx, client.ImpactParams{
		Symbol:    p.Arguments.Symbol,
		Direction: p.Arguments.Direction,
		MaxNodes:  maxNodes,
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
	b, err := s.client().PrComment(ctx, client.PrCommentRequest{
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
	b, err := s.client().RefactorPreview(ctx, refactorRequest(p))
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
	b, err := s.client().Refactor(ctx, refactorRequest(p), actor)
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
	b, err := s.client().Undo(ctx, p.Arguments.UndoToken, actor)
	if err != nil {
		return nil, &rpcError{Code: -32602, Message: err.Error()}
	}
	return textResult(b), nil
}

// listPRsCall (SW-105) enumerates open PRs through the read-only forge boundary
// and returns the canonical serialized forge.PRList. It holds no engine logic and
// performs no scoring — pure metadata enumeration through the shared client.
func (s *Server) listPRsCall(ctx context.Context) (any, *rpcError) {
	b, err := s.client().ListPRs(ctx)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}

// triagePRsCall (SW-105) returns the single-pass graph-derived ranked PR triage
// through the shared client (forge enumeration → zero-egress engine analyzer →
// shared encoder), so the ranked TriageReport is byte-identical across surfaces.
func (s *Server) triagePRsCall(ctx context.Context) (any, *rpcError) {
	b, err := s.client().TriagePRs(ctx)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}

// conflictsPRsCall (SW-106) returns the inter-PR conflict report through the
// shared client (forge enumeration → zero-egress engine analyzer → shared
// encoder), so the pairwise ConflictReport is byte-identical across surfaces.
func (s *Server) conflictsPRsCall(ctx context.Context) (any, *rpcError) {
	b, err := s.client().ConflictsPRs(ctx)
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
	b, err := s.client().SuggestReviewers(ctx, p.Arguments.Diff)
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
	b, err := s.client().CompareBranches(ctx, p.Arguments.Base, p.Arguments.Head)
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
	b, err := s.client().CritiqueReview(ctx, p.Arguments.PRNumber, p.Arguments.Diff, p.Arguments.Review)
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
	b, md, err := s.stableClient().Brief(ctx, p.Arguments.Symbol)
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
	b, err := s.stableClient().ExplainSymbol(ctx, p.Arguments.Symbol, derefInt(p.Arguments.Limit))
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
	b, err := s.stableClient().RelatedFiles(ctx, p.Arguments.Target, p.Arguments.Direction, derefInt(p.Arguments.Limit))
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
	b, err := s.stableClient().ChangeRisk(ctx, p.Arguments.Target, p.Arguments.Diff, derefInt(p.Arguments.Limit))
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}
