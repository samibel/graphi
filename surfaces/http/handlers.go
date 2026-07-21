package http

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/http/webui"
)

// spaHandler serves the embedded single-page web UI at "/" (SW-066).
//
//   - If the UI was not embedded (default UI-free build, !webui.Enabled()), it
//     returns the static NoticeHTML page explaining how to get a bundled binary.
//   - Otherwise it serves static files from the embedded webui.FS: "/" → index.html,
//     an existing file path → that file (with FileServerFS content types), and any
//     other path → index.html (SPA history-mode fallback). All served responses
//     are 200.
//
// It is static-only (no wall-clock/rand) and makes no outbound connection — it
// reads exclusively from the in-binary embedded FS, preserving the loopback-only,
// zero-egress contract.
func (s *Server) spaHandler() http.HandlerFunc {
	if !webui.Enabled() {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(webui.NoticeHTML))
		}
	}
	fileServer := http.FileServerFS(webui.FS)
	return func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			http.ServeFileFS(w, r, webui.FS, "index.html")
			return
		}
		if _, err := fs.Stat(webui.FS, p); err == nil {
			// Existing asset: let FileServerFS set content types / handle ranges.
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA history-mode fallback: unknown client-side route → index.html (200).
		http.ServeFileFS(w, r, webui.FS, "index.html")
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "schema_version": SchemaVersion})
}

// contractDoc is the capability-negotiation document served by /contract
// (AC-6). It enumerates the data resources (query ops + search + analyzers) and
// the SSE stream event types a client may consume, stamped with the envelope
// SchemaVersion so a TS/web/TUI/VS Code client can negotiate without
// hard-coding routes.
type contractDoc struct {
	SchemaVersion int      `json:"schema_version"`
	Resources     []string `json:"resources"`
	Streams       []string `json:"streams"`
}

// handleContract returns only resources that the current server profile will
// actually serve. The default profile therefore omits Labs query operations and
// analyzers instead of advertising capabilities that capabilityGuard will
// reject. Labs opt-in exposes the complete catalog. It is the runtime mirror of
// the hand-authored contract.schema.json (the Go↔TS single source of truth).
func (s *Server) handleContract(w http.ResponseWriter, r *http.Request) {
	resourceSet := make(map[string]struct{}, len(queryOps)+1+len(agentToolNames)+len(s.analyzers))
	add := func(operation, resource string) {
		if s.labsEnabled || !isLabsCapability(operation) {
			resourceSet[resource] = struct{}{}
		}
	}
	for op := range queryOps {
		add(op, "query/"+op)
	}
	add("search", "search")
	// EP-020 agent tools are always served (the client seam degrades to the
	// contract "unavailable" outcome when no graph services are wired).
	for _, t := range agentToolNames {
		add(t, "analyze/"+t)
	}
	for _, a := range s.analyzers {
		add(a, "analyze/"+a)
	}
	resources := make([]string, 0, len(resourceSet))
	for resource := range resourceSet {
		resources = append(resources, resource)
	}
	sort.Strings(resources)
	doc := contractDoc{
		SchemaVersion: SchemaVersion,
		Resources:     resources,
		Streams:       append([]string(nil), streamDescriptors...),
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	op := r.PathValue("op")
	if _, ok := queryOps[op]; !ok {
		writeErr(w, http.StatusBadRequest, "bad_request", "unknown query op: "+op)
		return
	}
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "symbol required")
		return
	}
	depth := 0
	if d := r.URL.Query().Get("depth"); d != "" {
		v, err := strconv.Atoi(d)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "bad depth")
			return
		}
		depth = v
	}
	raw, err := s.client.Query(r.Context(), op, symbol, depth)
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleCompound runs a compound / Cypher-style graph query (EP-011 G1). The
// query text is the request body; the canonical query.Result payload is returned
// in the same envelope as /query, so compound and fixed-query results are
// byte-identical across CLI/MCP/HTTP/daemon.
func (s *Server) handleCompound(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "cannot read compound body")
		return
	}
	raw, err := s.client.Compound(r.Context(), string(body))
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleSearchAST runs the structural AST pattern query (SW-082 / SW-085). The
// JSON AstPattern is the request body and limit rides the query string; the
// canonical query.Result payload is returned in the SAME envelope as /query and
// /compound, so search_ast results are byte-identical across CLI/MCP/HTTP/daemon.
// A malformed pattern surfaces the engine's typed error through the shared
// sanitized-error path (no new surface error shape).
func (s *Server) handleSearchAST(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "cannot read query-ast body")
		return
	}
	limit := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		v, err := strconv.Atoi(l)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "bad limit")
			return
		}
		limit = v
	}
	raw, err := s.client.SearchAST(r.Context(), string(body), limit)
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleFindClones runs the clone-detection query (SW-083 / SW-085). The JSON
// CloneConfig is the request body (empty ⇒ engine defaults); the canonical
// query.CloneResult payload is returned in the same envelope for byte-identical
// parity across surfaces.
func (s *Server) handleFindClones(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "cannot read find-clones body")
		return
	}
	raw, err := s.client.FindClones(r.Context(), string(body))
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "q required")
		return
	}
	limit := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		v, err := strconv.Atoi(l)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "bad limit")
			return
		}
		limit = v
	}
	raw, err := s.client.Search(r.Context(), q, limit)
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleSemanticSearch serves the OPTIONAL semantic search (SW-059). It embeds
// the engine's canonical SemanticResponse bytes verbatim, so the graceful-skip
// "unavailable" payload is byte-identical to the CLI and MCP surfaces. The
// graceful-skip path returns 200 with Available=false (NOT a 503), because "no
// embedder configured" is a normal, typed result — not a capability error.
func (s *Server) handleSemanticSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "q required")
		return
	}
	limit := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		v, err := strconv.Atoi(l)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "bad limit")
			return
		}
		limit = v
	}
	raw, err := s.client.SemanticSearch(r.Context(), q, limit)
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	analyzer := r.PathValue("analyzer")
	if analyzer == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "analyzer required")
		return
	}
	// EP-020 agent tools ride the same read-only /analyze/{name} route but
	// dispatch through their dedicated client seams (shared engine logic, same
	// canonical bytes as CLI/MCP) instead of the generic analysis service.
	if handled := s.handleAgentTool(w, r, analyzer); handled {
		return
	}
	p := client.AnalyzeParams{
		Name:      analyzer,
		Symbol:    r.URL.Query().Get("symbol"),
		Direction: r.URL.Query().Get("direction"),
	}
	if mn := r.URL.Query().Get("max-nodes"); mn != "" {
		v, err := strconv.Atoi(mn)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "bad max-nodes")
			return
		}
		p.MaxNodes = v
	}
	var raw []byte
	var err error
	if analyzer == "impact" {
		// The only Stable analyzer route uses the selector-free StableClient seam;
		// arbitrary analyzer dispatch remains confined to the Labs branch below.
		raw, err = s.stable.Impact(r.Context(), client.ImpactParams{
			Symbol: p.Symbol, Direction: p.Direction, MaxNodes: p.MaxNodes,
		})
	} else {
		raw, err = s.client.Analyze(r.Context(), p)
	}
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// agentToolNames are the EP-020 agent tools served on the /analyze/{name}
// route and advertised in /contract. They dispatch through the dedicated
// client seams, not the generic analysis service.
var agentToolNames = []string{"agent_brief", "change_risk", "explain_symbol", "related_files"}

// handleAgentTool serves the EP-020 agent tools on the shared /analyze route.
// It returns false when name is not an agent tool so the generic analyzer
// dispatch proceeds. Read-only: every seam it calls is a GET-safe query.
func (s *Server) handleAgentTool(w http.ResponseWriter, r *http.Request, name string) bool {
	q := r.URL.Query()
	maxItems := 0
	if mi := q.Get("max-items"); mi != "" {
		v, err := strconv.Atoi(mi)
		if err != nil || v < 0 {
			writeErr(w, http.StatusBadRequest, "bad_request", "bad max-items")
			return true
		}
		maxItems = v
	}
	var (
		raw []byte
		err error
	)
	switch name {
	case "explain_symbol":
		symbol := q.Get("symbol")
		if symbol == "" {
			writeErr(w, http.StatusBadRequest, "bad_request", "symbol required")
			return true
		}
		raw, err = s.client.ExplainSymbol(r.Context(), symbol, maxItems)
	case "related_files":
		target := q.Get("target")
		if target == "" {
			target = q.Get("symbol") // IDE convenience alias
		}
		if target == "" {
			writeErr(w, http.StatusBadRequest, "bad_request", "target required")
			return true
		}
		raw, err = s.client.RelatedFiles(r.Context(), target, q.Get("direction"), maxItems)
	case "change_risk":
		target := q.Get("target")
		if target == "" {
			target = q.Get("symbol")
		}
		if target == "" {
			writeErr(w, http.StatusBadRequest, "bad_request", "target required")
			return true
		}
		raw, err = s.client.ChangeRisk(r.Context(), target, "", maxItems)
	case "agent_brief":
		topic := q.Get("topic")
		if topic == "" {
			topic = q.Get("symbol")
		}
		raw, _, err = s.client.Brief(r.Context(), topic)
	default:
		return false
	}
	if err != nil {
		writeErrSanitized(w, err)
		return true
	}
	writeEnvelope(w, raw)
	return true
}

// handleListPRs (SW-105) returns the read-only forge PR-enumeration metadata
// envelope. It delegates 100% to the shared client.ListPRs seam and embeds the
// canonical forge.PRList bytes verbatim, so the payload is byte-identical to the
// CLI/MCP surfaces. No graph scoring occurs.
func (s *Server) handleListPRs(w http.ResponseWriter, r *http.Request) {
	raw, err := s.client.ListPRs(r.Context())
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleTriagePRs (SW-105) returns the single-pass graph-derived ranked PR triage.
// It delegates to the shared client.TriagePRs seam (forge enumeration → zero-egress
// engine analyzer → shared encoder), so the ranked payload is byte-identical to the
// CLI/MCP surfaces.
func (s *Server) handleTriagePRs(w http.ResponseWriter, r *http.Request) {
	raw, err := s.client.TriagePRs(r.Context())
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleConflictsPRs (SW-106) returns the inter-PR conflict report. It delegates
// to the shared client.ConflictsPRs seam (forge enumeration → zero-egress engine
// analyzer → shared encoder), so the pairwise payload is byte-identical to the
// CLI/MCP surfaces.
func (s *Server) handleConflictsPRs(w http.ResponseWriter, r *http.Request) {
	raw, err := s.client.ConflictsPRs(r.Context())
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleSuggestReviewers (SW-107) returns the ranked reviewer report. It delegates
// to the shared client.SuggestReviewers seam (touched-set resolution → zero-egress
// engine analyzer → shared encoder), so the payload is byte-identical to the
// CLI/MCP surfaces. The `diff` query parameter is the local-first PR diff / refs.
func (s *Server) handleSuggestReviewers(w http.ResponseWriter, r *http.Request) {
	raw, err := s.client.SuggestReviewers(r.Context(), r.URL.Query().Get("diff"))
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleCompareBranches (SW-107) returns the graph-level branch diff. It delegates
// to the shared client.CompareBranches seam (base/head materialized above the
// surface boundary → zero-egress engine analyzer → shared encoder), so the payload
// is byte-identical to the CLI/MCP surfaces. The `base`/`head` query parameters are
// branch refs.
func (s *Server) handleCompareBranches(w http.ResponseWriter, r *http.Request) {
	raw, err := s.client.CompareBranches(r.Context(), r.URL.Query().Get("base"), r.URL.Query().Get("head"))
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleCritiqueReview (SW-108, the EP-018 capstone) returns the graph-evidence
// critique of an existing PR review. It delegates to the shared client.CritiqueReview
// seam (surface review fetch / inline review → zero-egress engine analyzer → shared
// encoder), so the payload is byte-identical to the CLI/MCP surfaces. The `pr`
// parameter selects the review to fetch; `review` supplies an inline review JSON
// (takes precedence); `diff` is the PR's touched set.
func (s *Server) handleCritiqueReview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	prNumber := 0
	if v := strings.TrimSpace(q.Get("pr")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeErrSanitized(w, fmt.Errorf("http: invalid pr %q", v))
			return
		}
		prNumber = n
	}
	raw, err := s.client.CritiqueReview(r.Context(), prNumber, q.Get("diff"), q.Get("review"))
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleMemory runs an EP-012 memory operation. The request body is a JSON
// MemoryRequest; the response payload is the canonical MemoryResponse bytes
// (byte-identical to CLI/MCP output).
func (s *Server) handleMemory(w http.ResponseWriter, r *http.Request) {
	var req client.MemoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	raw, err := s.client.Memory(r.Context(), req)
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleDistill runs EP-012 session distillation. The request body is a JSON
// DistillRequest; the response payload is the canonical DistillResponse bytes.
func (s *Server) handleDistill(w http.ResponseWriter, r *http.Request) {
	var req client.DistillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	raw, err := s.client.Distill(r.Context(), req)
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleSkillGen runs EP-012 deterministic skill generation. The request body
// is a JSON SkillGenRequest; the response payload is the canonical
// SkillGenResponse bytes.
func (s *Server) handleSkillGen(w http.ResponseWriter, r *http.Request) {
	var req client.SkillGenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	raw, err := s.client.SkillGen(r.Context(), req)
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}
