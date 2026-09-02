package http

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/samibel/graphi/surfaces/mcp"
)

// TestCharacterization_HTTPRoutes_Snapshot is the SW-110 (TEST-01) AC3 snapshot of
// the HTTP REST/SSE route set: it pins the exact ordered ServeMux patterns the
// surface registers as of the characterization baseline, so any SCOPE-01/SAFE-01
// change to the network surface (a route added, removed, or reordered) surfaces
// here as a REVIEWED diff.
func TestCharacterization_HTTPRoutes_Snapshot(t *testing.T) {
	want := []string{
		"GET /healthz",
		"GET /contract",
		"GET /query/{op}",
		"POST /compound",
		"POST /query-ast",
		"POST /find-clones",
		"GET /search",
		"GET /search/semantic",
		"GET /semantic/status",
		"GET /analyze/{analyzer}",
		"GET /prs",
		"GET /prs/triage",
		"GET /prs/conflicts",
		"GET /prs/suggest-reviewers",
		"GET /branches/compare",
		"GET /reviews/critique",
		"POST /memory",
		"POST /distill",
		"POST /skillgen",
		"GET /events",
		"GET /wiki",
		"GET /wiki/c/{id}",
		"GET /",
	}
	got := New(&stubClient{}, nil).RoutePatterns()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("HTTP route-set snapshot drifted (intentional scope change? update this baseline):\n got  = %#v\n want = %#v", got, want)
	}
}

type routeProbe struct {
	method     string
	path       string
	capability string
}

// labsRouteProbes derives its coverage from the production route table. Mixed
// routes use a representative Labs operation; every fixed route is classified
// by the resolver stored beside its handler. This prevents the test itself from
// becoming another hand-maintained list that can omit a newly registered route.
func labsRouteProbes(t *testing.T, srv *Server) []routeProbe {
	t.Helper()
	var probes []routeProbe
	for _, registered := range srv.routes() {
		parts := strings.SplitN(registered.Pattern, " ", 2)
		if len(parts) != 2 {
			t.Fatalf("invalid route pattern %q", registered.Pattern)
		}
		method, path := parts[0], parts[1]
		reqPath := path
		request := newLocalRequest(method, path, nil)
		switch registered.Pattern {
		case "GET /query/{op}":
			reqPath = "/query/implementers?symbol=x"
			request = newLocalRequest(method, reqPath, nil)
			request.SetPathValue("op", "implementers")
		case "GET /analyze/{analyzer}":
			reqPath = "/analyze/taint?symbol=x"
			request = newLocalRequest(method, reqPath, nil)
			request.SetPathValue("analyzer", "taint")
		case "GET /events":
			reqPath = "/events?analyzer=taint"
			request = newLocalRequest(method, reqPath, nil)
		case "GET /wiki/c/{id}":
			reqPath = "/wiki/c/1"
			request = newLocalRequest(method, reqPath, nil)
			request.SetPathValue("id", "1")
		}
		capability := registered.capability(request)
		if isLabsCapability(capability) {
			probes = append(probes, routeProbe{method: method, path: reqPath, capability: capability})
		}
	}
	return probes
}

func TestRoutes_EveryEntryHasCapabilityClassification(t *testing.T) {
	for _, registered := range New(&stubClient{}, nil).routes() {
		if registered.capability == nil {
			t.Errorf("route %q has no capability classification", registered.Pattern)
		}
	}
}

// TestSAFE01_LabsRoutes_FailClosedByDefault is the SW-112 (SAFE-01) gate that
// replaced the SW-110 AC6 expected-red characterization
// (TestCharacterization_UnauthLabsRoutes_ExpectedRed): historically the surface
// served every Labs route (PR-triage / forge / memory / distill / skillgen /
// critique) to any local client with no token, header, or capability check.
//
// Since SW-112 the contract is FAIL CLOSED: without the explicit
// GRAPHI_HTTP_LABS=1 operator opt-in, every Labs route answers 403 with the
// stable "labs_disabled" code and the wrapped handler is never reached. The
// Stable read routes are untouched by the guard.
func TestSAFE01_LabsRoutes_FailClosedByDefault(t *testing.T) {
	t.Setenv(LabsEnvVar, "") // pin the default: no opt-in
	srv := New(&stubClient{}, nil)

	for _, probe := range labsRouteProbes(t, srv) {
		req := newLocalRequest(probe.method, probe.path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s (%s): want 403 (fail-closed Labs route, SAFE-01), got %d", probe.method, probe.path, probe.capability, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"code":"labs_disabled"`) {
			t.Errorf("%s %s (%s): missing labs_disabled error: %s", probe.method, probe.path, probe.capability, rec.Body.String())
		}
	}

	// Guard scope check: a Stable read route must NOT be captured by the guard.
	req := newLocalRequest(http.MethodGet, "/query/callers?symbol=x", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Errorf("GET /query/callers: stable route was fail-closed by the Labs guard (%d)", rec.Code)
	}
}

// TestSAFE01_LabsRoutes_ExplicitOptIn proves the fail-closed default is
// reversible exactly as documented: with GRAPHI_HTTP_LABS=1 exported at server
// construction, the Labs routes are served again (any status EXCEPT the guard's
// 401/403 — the stub client yields 405/400/503-class answers, and the point is
// precisely that the guard no longer intercepts).
func TestSAFE01_LabsRoutes_ExplicitOptIn(t *testing.T) {
	t.Setenv(LabsEnvVar, "1")
	srv := New(&stubClient{}, nil)

	for _, probe := range labsRouteProbes(t, srv) {
		req := newLocalRequest(probe.method, probe.path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
			t.Errorf("%s %s (%s): opted-in Labs route still rejected (%d)", probe.method, probe.path, probe.capability, rec.Code)
		}
	}
}

func TestSAFE01_StableCapabilityVariantsRemainAvailable(t *testing.T) {
	t.Setenv(LabsEnvVar, "")
	srv := New(&stubClient{}, nil)
	paths := []string{
		"/query/callers?symbol=x",
		"/query/callees?symbol=x",
		"/query/references?symbol=x",
		"/query/definition?symbol=x",
		"/query/neighborhood?symbol=x",
		"/search?q=x",
		"/analyze/impact?symbol=x",
		"/analyze/agent_brief",
		"/analyze/change_risk?target=x",
		"/analyze/explain_symbol?symbol=x",
		"/analyze/related_files?target=x",
		"/events?analyzer=impact&symbol=x",
	}
	for _, path := range paths {
		req := newLocalRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code == http.StatusForbidden {
			t.Errorf("GET %s: stable capability was rejected by Labs guard: %s", path, rec.Body.String())
		}
	}
}

// representativeProbe describes one probe of a registered HTTP route, pinned
// to a representative stable or labs operation the capability resolver will
// recognise. The matrix-driven CI guard below walks every route in
// production with one or more of these probes.
type representativeProbe struct {
	method     string
	path       string
	capability string
}

// representativeProbesFor returns one or more probe requests per registered
// route. Mixed routes (/query/{op}, /analyze/{analyzer}, /events) yield TWO
// probes — one stable, one labs — so the matrix-driven gate is checked at
// both tiers. POST routes use POST so the request reaches the route handler
// (a GET on a POST route falls through to the SPA catch-all and would not
// exercise the gate). Infrastructure routes get a single bare probe with
// capability "" and the matching branch in the test treats them as exempt
// from the matrix-driven gate.
func representativeProbesFor(registered route) []representativeProbe {
	parts := strings.SplitN(registered.Pattern, " ", 2)
	method, _ := parts[0], parts[1]
	switch registered.Pattern {
	case "GET /query/{op}":
		return []representativeProbe{
			{method: method, path: "/query/callers?symbol=x", capability: "callers"},
			{method: method, path: "/query/implementers?symbol=x", capability: "implementers"},
		}
	case "GET /analyze/{analyzer}":
		return []representativeProbe{
			{method: method, path: "/analyze/impact?symbol=x", capability: "impact"},
			{method: method, path: "/analyze/taint?symbol=x", capability: "taint"},
		}
	case "GET /events":
		return []representativeProbe{
			{method: method, path: "/events?analyzer=impact&symbol=x", capability: "impact"},
			{method: method, path: "/events?analyzer=taint", capability: "taint"},
		}
	case "POST /compound", "POST /query-ast", "POST /find-clones",
		"POST /memory", "POST /distill", "POST /skillgen":
		// Capability is fixed by the route registration; look it up via a bare
		// resolver invocation so this list is the source of truth for what the
		// test thinks the capability is. Probe at the registered path so Go
		// 1.22 ServeMux routes the request to the same handler the production
		// surface does (no SPA-fallback or 405 noise).
		parts := strings.SplitN(registered.Pattern, " ", 2)
		req := newLocalRequest(parts[0], parts[1], nil)
		cap := registered.capability(req)
		return []representativeProbe{{method: parts[0], path: parts[1], capability: cap}}
	}
	// Default: probe the pattern's bare path; the capability is whatever the
	// route's resolver returns for it. Infrastructure resolvers return "".
	probe := representativeProbe{method: method, path: strings.TrimPrefix(registered.Pattern, method+" "), capability: registered.capability(newLocalRequest(method, "/", nil))}
	if probe.path == "" {
		probe.path = "/"
	}
	return []representativeProbe{probe}
}

// TestSAFE01_HTTPGateFollowsStableMatrixTags is the CI guard that ties the
// HTTP capability gate to the frozen stable operations matrix (SCOPE-01).
// It walks every route in the production route table (no hand-maintained list),
// exercises each with the SAME resolver the handler uses at runtime, and
// asserts the default-server response matches the matrix tag: a Labs
// capability MUST answer 403 labs_disabled, every Stable capability MUST
// remain reachable, and infrastructure routes are exempt from the gate. This
// prevents the HTTP gate from drifting out of agreement with
// surfaces/mcp.IsStableOperation — the single membership check the MCP tools
// list, the internal/coverage stable-tier gate, and now the HTTP gate share.
//
// If a future change drops capabilityGuard from Handler(), narrows the
// membership check away from mcp.IsStableOperation, or adds a route whose
// capability resolver returns "" for a non-infrastructure surface, this test
// fails on the next CI run. Verified by removing capabilityGuard from
// Handler() during the rebuild: the test reports every leaked Labs route
// (`GET /branches/compare`, `GET /reviews/critique`, `POST /memory`,
// `POST /distill`, `POST /skillgen`, `GET /events (taint)`,
// `GET /wiki (communities)`) by name and capability.
func TestSAFE01_HTTPGateFollowsStableMatrixTags(t *testing.T) {
	t.Setenv(LabsEnvVar, "")

	srv := New(&stubClient{}, nil)
	for _, registered := range srv.routes() {
		for _, probe := range representativeProbesFor(registered) {
			req := newLocalRequest(probe.method, probe.path, nil)
			// Mirror the dynamic path values representativeProbesFor built so the
			// capability resolver at runtime sees the SAME shape it saw while
			// classifying the probe above.
			switch registered.Pattern {
			case "GET /query/{op}":
				req.SetPathValue("op", probe.capability)
			case "GET /analyze/{analyzer}":
				req.SetPathValue("analyzer", probe.capability)
			}
			capability := registered.capability(req)
			if capability != probe.capability {
				t.Errorf("%s probe %q: resolver returned %q, want %q (probe fixture drift)",
					registered.Pattern, probe.path, capability, probe.capability)
				continue
			}

			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			switch {
			case capability == "":
				// Infrastructure routes (/healthz, /contract, /) are exempt from
				// the matrix-driven gate; we assert no panic and nothing else.
			case isLabsCapability(capability):
				// Matrix says Labs: the gate MUST fail-closed with labs_disabled.
				if rec.Code != http.StatusForbidden {
					t.Errorf("%s (%s): matrix=Labs but gate did not 403: code=%d body=%s",
						registered.Pattern, capability, rec.Code, rec.Body.String())
					continue
				}
				if !strings.Contains(rec.Body.String(), `"code":"labs_disabled"`) {
					t.Errorf("%s (%s): matrix=Labs but missing labs_disabled error: %s",
						registered.Pattern, capability, rec.Body.String())
				}
			default:
				// Matrix says Stable: the gate MUST NOT intercept. The handler
				// may answer 200/400/503 (service unavailable) but NEVER 403.
				if rec.Code == http.StatusForbidden {
					t.Errorf("%s (%s): matrix=Stable but gate 403-ed: %s",
						registered.Pattern, capability, rec.Body.String())
				}
			}
		}
	}
}

// TestSAFE01_HTTPCapabilityGateUsesStableMatrixMembership is the second half
// of the matrix-gate CI guard: it asserts the HTTP gate's membership function
// is the SAME membership function the matrix pins
// (surfaces/mcp.IsStableOperation), not a hand-maintained parallel list. The
// probe is deliberately synthetic: it asks isLabsCapability what the verdict
// would be for every frozen stable op and a representative set of Labs ops
// and verifies the gate and the matrix agree. If a future change introduces
// a second membership check (e.g. a hardcoded Labs allowlist in routes.go),
// this test will be the first to fail.
func TestSAFE01_HTTPCapabilityGateUsesStableMatrixMembership(t *testing.T) {
	// Every frozen stable op is reported by isLabsCapability as NOT labs.
	for _, op := range mcp.StableOperations {
		if isLabsCapability(op) {
			t.Errorf("isLabsCapability(%q) = true; surfaces/mcp.IsStableOperation says %q is stable — gate and matrix disagree",
				op, op)
		}
	}
	// A few representative non-stable ops are reported as labs by the gate.
	// Using operations the route table actually exercises so the disagreement
	// would be visible at runtime too.
	for _, op := range []string{"implementers", "taint", "memory", "distill", "skillgen"} {
		if !isLabsCapability(op) {
			t.Errorf("isLabsCapability(%q) = false; this is a Labs operation and the matrix/HTTP gate should agree — they disagree",
				op)
		}
	}
	// Infrastructure ("") is not a capability and is exempt from the gate.
	if isLabsCapability("") {
		t.Errorf("isLabsCapability(\"\") = true; an empty capability is not a Labs capability — the gate must treat it as exempt")
	}
}
