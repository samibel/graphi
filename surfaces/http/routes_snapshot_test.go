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

// TestCharacterization_UnauthLabsRoutes_ExpectedRed is the SW-110 AC6
// explicitly-documented EXPECTED-RED characterization of a KNOWN DEFECT: the HTTP
// surface applies NO authentication to any route, so the "Labs" endpoints (the
// non-core PR-triage / memory / distill / skillgen / critique tools that SCOPE-01
// classifies as not-Stable) are reachable by any local client with no token,
// header, or capability check whatsoever.
//
// This test PINS the defective state — it asserts the Labs routes are served
// WITHOUT auth today — and is written to FAIL LOUDLY the moment SW-112 (SAFE-01)
// fail-closes them. It does NOT fix the defect (that is SW-112's job) and it does
// NOT make the suite red: it is "expected-red" in the sense that it captures a
// red/defective behavior as the current, reviewed expectation. When the guard
// lands, the assertion flips and the fix owner updates this baseline.
//
// A "served without auth" response is anything OTHER than 401 Unauthorized / 403
// Forbidden. The concrete status varies (405 for a wrong verb, 503 when the Labs
// capability is unwired, 400 for missing args) — the defect is precisely that
// NONE of them is an auth rejection.
func TestCharacterization_UnauthLabsRoutes_ExpectedRed(t *testing.T) {
	srv := New(&stubClient{}, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// The Labs / non-core routes SCOPE-01 flags as not-Stable.
	labs := []struct {
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
	for _, r := range labs {
		req, err := http.NewRequest(r.method, ts.URL+r.path, http.NoBody)
		if err != nil {
			t.Fatalf("build request %s %s: %v", r.method, r.path, err)
		}
		// Deliberately NO Authorization header / token of any kind.
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", r.method, r.path, err)
		}
		status := resp.StatusCode
		_ = resp.Body.Close()

		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			t.Errorf("KNOWN-DEFECT CHARACTERIZATION FLIPPED: %s %s now rejects unauthenticated access (%d). "+
				"The unauth-Labs-routes defect appears FIXED — update this baseline and close it against SW-112 (SAFE-01).",
				r.method, r.path, status)
			continue
		}
		// Defect present (expected): the Labs route was reached with no auth.
		t.Logf("characterized unauth Labs route %s %s → %d (no auth applied; SW-112 fail-closes this)", r.method, r.path, status)
	}
}
