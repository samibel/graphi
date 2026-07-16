package http

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
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
