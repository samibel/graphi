package http

import (
	"net/http"
	"net/http/httptest"
	"reflect"
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

// labsRoutes is the set of Labs / non-core routes SCOPE-01 flags as not-Stable
// and SW-112 (SAFE-01) fail-closes behind the GRAPHI_HTTP_LABS opt-in.
var labsRoutes = []struct {
	method string
	path   string
}{
	{http.MethodGet, "/prs"},
	{http.MethodGet, "/prs/triage"},
	{http.MethodGet, "/prs/conflicts"},
	{http.MethodGet, "/prs/suggest-reviewers"},
	{http.MethodGet, "/branches/compare"},
	{http.MethodGet, "/reviews/critique"},
	{http.MethodPost, "/memory"},
	{http.MethodPost, "/distill"},
	{http.MethodPost, "/skillgen"},
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
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	for _, r := range labsRoutes {
		req, err := http.NewRequest(r.method, ts.URL+r.path, http.NoBody)
		if err != nil {
			t.Fatalf("build request %s %s: %v", r.method, r.path, err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", r.method, r.path, err)
		}
		status := resp.StatusCode
		_ = resp.Body.Close()
		if status != http.StatusForbidden {
			t.Errorf("%s %s: want 403 (fail-closed Labs route, SAFE-01), got %d", r.method, r.path, status)
		}
	}

	// Guard scope check: a Stable read route must NOT be captured by the guard.
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		t.Errorf("GET /healthz: stable route was fail-closed by the Labs guard (%d)", resp.StatusCode)
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
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	for _, r := range labsRoutes {
		req, err := http.NewRequest(r.method, ts.URL+r.path, http.NoBody)
		if err != nil {
			t.Fatalf("build request %s %s: %v", r.method, r.path, err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", r.method, r.path, err)
		}
		status := resp.StatusCode
		_ = resp.Body.Close()
		if status == http.StatusForbidden || status == http.StatusUnauthorized {
			t.Errorf("%s %s: opted-in Labs route still rejected (%d)", r.method, r.path, status)
		}
	}
}
