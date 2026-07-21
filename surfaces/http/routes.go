package http

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/samibel/graphi/surfaces/mcp"
)

// route is one registered HTTP route: the wire-visible method+pattern and the
// handler serving it. The route table is the SINGLE SOURCE OF TRUTH the router
// and the SW-110 route snapshot both read, so the advertised HTTP surface cannot
// drift from what a scope change reviews as an intentional diff (TEST-01 AC3).
type route struct {
	Pattern    string // Go 1.22 ServeMux "METHOD /path" pattern
	capability capabilityResolver
	handler    http.HandlerFunc
}

// capabilityResolver maps one concrete HTTP request to the canonical product
// operation it invokes. An empty result marks transport/infrastructure behavior
// such as health, contract negotiation, the plain event stream, or the SPA.
// Every route supplies a resolver, including mixed routes such as
// /query/{op}, /analyze/{analyzer}, and /events?analyzer=.
type capabilityResolver func(*http.Request) string

func infrastructureCapability(*http.Request) string { return "" }

func fixedCapability(name string) capabilityResolver {
	return func(*http.Request) string { return name }
}

func queryCapability(r *http.Request) string {
	op := r.PathValue("op")
	if _, known := queryOps[op]; !known {
		// Preserve the documented 400 for an unknown query operation. Known
		// hierarchy operations are resolved below and therefore fail closed.
		return ""
	}
	return op
}

func analyzerCapability(r *http.Request) string { return r.PathValue("analyzer") }

func eventCapability(r *http.Request) string { return r.URL.Query().Get("analyzer") }

func isLabsCapability(name string) bool {
	return name != "" && !mcp.IsStableOperation(name)
}

// capabilityGuard fail-closes every request that resolves to an operation
// outside the frozen StableOperations manifest (SW-112 / SAFE-01). Guarding is
// applied centrally while registering the route table, so adding a route cannot
// accidentally bypass protection by forgetting a hand-written wrapper.
func (s *Server) capabilityGuard(resolve capabilityResolver, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		capability := resolve(r)
		if isLabsCapability(capability) && !s.labsEnabled {
			writeErr(w, http.StatusForbidden, "labs_disabled",
				"this Labs route is outside the stable capability set and is disabled by default (SAFE-01); export "+LabsEnvVar+"=1 to opt in")
			return
		}
		next(w, r)
	}
}

// routes returns the ordered route table this surface serves. Handler registers
// exactly these in order; the SPA catch-all is LAST and least specific so every
// explicit API/SSE/wiki route wins (Go 1.22 precedence). Labs routes (PR/forge,
// memory, distill, skillgen, hierarchy/compound/pattern/semantic queries,
// non-impact analyzers, analyzer-over-SSE, and community wiki) are classified
// here and guarded centrally by Handler.
func (s *Server) routes() []route {
	return []route{
		{"GET /healthz", infrastructureCapability, s.handleHealth},
		{"GET /contract", infrastructureCapability, s.handleContract},
		{"GET /query/{op}", queryCapability, s.schemaGuard(s.handleQuery)},
		{"POST /compound", fixedCapability("compound"), s.schemaGuard(s.handleCompound)},
		{"POST /query-ast", fixedCapability("search_ast"), s.schemaGuard(s.handleSearchAST)},
		{"POST /find-clones", fixedCapability("find_clones"), s.schemaGuard(s.handleFindClones)},
		{"GET /search", fixedCapability("search"), s.schemaGuard(s.handleSearch)},
		{"GET /search/semantic", fixedCapability("search_semantic"), s.schemaGuard(s.handleSemanticSearch)},
		{"GET /analyze/{analyzer}", analyzerCapability, s.schemaGuard(s.handleAnalyze)},
		{"GET /prs", fixedCapability("list_prs"), s.schemaGuard(s.handleListPRs)},
		{"GET /prs/triage", fixedCapability("triage_prs"), s.schemaGuard(s.handleTriagePRs)},
		{"GET /prs/conflicts", fixedCapability("conflicts_prs"), s.schemaGuard(s.handleConflictsPRs)},
		{"GET /prs/suggest-reviewers", fixedCapability("suggest_reviewers"), s.schemaGuard(s.handleSuggestReviewers)},
		{"GET /branches/compare", fixedCapability("compare_branches"), s.schemaGuard(s.handleCompareBranches)},
		{"GET /reviews/critique", fixedCapability("critique_review"), s.schemaGuard(s.handleCritiqueReview)},
		{"POST /memory", fixedCapability("memory"), s.schemaGuard(s.handleMemory)},
		{"POST /distill", fixedCapability("distill"), s.schemaGuard(s.handleDistill)},
		{"POST /skillgen", fixedCapability("skillgen"), s.schemaGuard(s.handleSkillGen)},
		{"GET /events", eventCapability, s.schemaGuard(s.handleSSE)},
		{"GET /wiki", fixedCapability("communities"), s.handleWikiIndex},
		{"GET /wiki/c/{id}", fixedCapability("communities"), s.handleWikiPage},
		// SPA catch-all (SW-066). Registered LAST and matched LEAST specifically:
		// Go 1.22 ServeMux routes the explicit patterns above ahead of "GET /", so
		// every existing API/SSE/wiki route is byte-identical and the SPA only sees
		// paths none of them claim. It is served over this same loopback surface.
		{"GET /", infrastructureCapability, s.spaHandler()},
	}
}

// RoutePatterns returns the ordered ServeMux patterns this surface registers. It
// is the descriptive projection the SW-110 route-set snapshot pins; it registers
// nothing and has no side effects.
func (s *Server) RoutePatterns() []string {
	rs := s.routes()
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Pattern
	}
	return out
}

// Handler returns the routed http.Handler. Routing uses Go 1.22+ method-pattern
// matching: non-GET methods on data routes return 405 automatically, and unknown
// paths return 404.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, r := range s.routes() {
		mux.HandleFunc(r.Pattern, s.capabilityGuard(r.capability, r.handler))
	}
	return requestSecurityGuard(limitRequestBody(mux))
}

// schemaGuard rejects requests whose X-Graphi-Schema-Version header advertises an
// unsupported contract version (412 Precondition Failed), mirroring the EP-002
// schema-version drift gate. Absent header = no negotiation (pass through).
func (s *Server) schemaGuard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if v := r.Header.Get("X-Graphi-Schema-Version"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n != SchemaVersion {
				writeErr(w, http.StatusPreconditionFailed, "schema_mismatch",
					fmt.Sprintf("schema version mismatch: want %d", SchemaVersion))
				return
			}
		}
		next(w, r)
	}
}
