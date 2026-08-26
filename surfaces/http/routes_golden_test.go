package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/internal/goldenfile"
)

// AX-00 (SW-220) AC-2, HTTP half — the operation/capability list freeze.
//
// TestCharacterization_HTTPRoutes_Snapshot pins the ordered ServeMux patterns.
// That catches a route added, removed or reordered, and misses everything the
// extension kernel is actually likely to change: which product operation a route
// resolves to, what tier that operation carries, and what the capability
// negotiation document (/contract) advertises to a client per profile. AX-05
// will DERIVE the capability list from the operation catalog; this golden is what
// proves the derived list is the same list.
//
// Three artifacts, deliberately separate:
//
//   - http-routes.json — the ordered route table with each route's resolved
//     capability and tier. Mixed routes (/query/{op}, /analyze/{analyzer},
//     /events?analyzer=) are probed at BOTH tiers, because one pattern serving a
//     stable and a labs operation is exactly where a tier mistake hides.
//   - http-contract-stable.json / http-contract-labs.json — the live /contract
//     body per profile, captured through the real handler and envelope, so a
//     reshaped envelope or a silently widened default profile is a byte diff.
//
// Regeneration is explicit:
//
//	GRAPHI_UPDATE_GOLDEN=1 go test ./surfaces/http -run TestAX00

// goldenRoute is one row of the frozen HTTP operation list.
type goldenRoute struct {
	Pattern    string `json:"pattern"`
	ProbePath  string `json:"probe_path"`
	Capability string `json:"capability"`
	Tier       string `json:"tier"`
}

// goldenRouteTable is the whole frozen HTTP surface projection.
type goldenRouteTable struct {
	Note       string        `json:"note"`
	RouteCount int           `json:"route_count"`
	Patterns   []string      `json:"patterns"`
	Rows       []goldenRoute `json:"rows"`
}

// routeTier classifies a resolved capability with the SAME membership check the
// runtime capabilityGuard uses (isLabsCapability → mcp.IsStableOperation), so
// the golden cannot hold a second opinion about the tier of a route. An empty
// capability is transport/infrastructure and is exempt from the gate.
func routeTier(capability string) string {
	switch {
	case capability == "":
		return "infrastructure"
	case isLabsCapability(capability):
		return "labs"
	default:
		return "stable"
	}
}

// goldenCanonicalJSON renders v as stable, reviewable bytes (two-space indent,
// trailing newline, no HTML escaping).
func goldenCanonicalJSON(t *testing.T, v any) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		t.Fatalf("encode golden: %v", err)
	}
	return buf.Bytes()
}

// goldenRouteRows walks the production route table and resolves each route's
// capability through the SAME resolver the handler uses at runtime. It reuses
// representativeProbesFor (already the source of truth for the SAFE-01
// matrix gate) so the golden and the gate can never disagree about what a route
// means.
func goldenRouteRows(t *testing.T, srv *Server) []goldenRoute {
	t.Helper()
	var rows []goldenRoute
	for _, registered := range srv.routes() {
		for _, probe := range representativeProbesFor(registered) {
			rows = append(rows, goldenRoute{
				Pattern:    registered.Pattern,
				ProbePath:  probe.path,
				Capability: probe.capability,
				Tier:       routeTier(probe.capability),
			})
		}
	}
	return rows
}

func TestAX00_HTTPRouteTable_Golden(t *testing.T) {
	t.Setenv(LabsEnvVar, "") // pin the shipped default: no Labs opt-in
	srv := New(&stubClient{}, nil)

	rows := goldenRouteRows(t, srv)
	artifact := goldenRouteTable{
		Note:       "AX-00 baseline freeze: the ordered HTTP route table with each route's resolved product capability and its tier. Tier comes from surfaces/mcp.IsStableOperation — the same membership check capabilityGuard applies at runtime. Mixed routes are probed at both tiers.",
		RouteCount: len(srv.RoutePatterns()),
		Patterns:   srv.RoutePatterns(),
		Rows:       rows,
	}
	goldenfile.Assert(t, filepath.Join("testdata", "http-routes.json"), goldenCanonicalJSON(t, artifact))
}

// analyzerNames returns the analyzer set cmd/graphi injects via WithDescriptors,
// taken from the real analysis service rather than a hand-copied list — so the
// frozen /contract reflects the shipped catalog. The engine/analysis import is
// test-only (surfaces → engine is a legal downward edge anyway; the production
// http package stays free of it by design).
func analyzerNames(t *testing.T) []string {
	t.Helper()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	return analysis.NewDefaultService(store).Names()
}

// contractBody serves GET /contract through the production handler chain and
// returns the raw response bytes — envelope included, because the envelope shape
// is part of what a client negotiates against.
func contractBody(t *testing.T, srv *Server) []byte {
	t.Helper()
	req := newLocalRequest(http.MethodGet, "/contract", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /contract: status %d, body %s", rec.Code, rec.Body.String())
	}
	// Re-indent so the golden is reviewable line by line; the wire bytes stay
	// verifiable through the round trip (a reshaped envelope still diffs).
	var doc any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode /contract body %q: %v", rec.Body.String(), err)
	}
	return goldenCanonicalJSON(t, doc)
}

func TestAX00_HTTPContractDocument_Golden(t *testing.T) {
	for _, tc := range []struct {
		name  string
		labs  string
		fname string
	}{
		{name: "stable-default", labs: "", fname: "http-contract-stable.json"},
		{name: "labs-opt-in", labs: "1", fname: "http-contract-labs.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(LabsEnvVar, tc.labs)
			srv := New(&stubClient{}, nil).WithDescriptors(analyzerNames(t))
			goldenfile.Assert(t, filepath.Join("testdata", tc.fname), contractBody(t, srv))
		})
	}
}

// TestAX00_HTTPArtifacts_ReproducibleAcrossRuns is the AC-2 determinism half:
// two consecutive productions on one machine must be byte-identical. /contract
// builds its resource list from a Go map, so this is not a theoretical worry —
// it is exactly the class of bug (map iteration order escaping into a wire
// document) that a single-shot golden would hide behind a lucky regeneration.
func TestAX00_HTTPArtifacts_ReproducibleAcrossRuns(t *testing.T) {
	t.Setenv(LabsEnvVar, "")
	names := analyzerNames(t)

	buildRoutes := func() []byte {
		srv := New(&stubClient{}, nil)
		return goldenCanonicalJSON(t, goldenRouteRows(t, srv))
	}
	if first, second := buildRoutes(), buildRoutes(); !bytes.Equal(first, second) {
		t.Errorf("HTTP route table is not reproducible across two runs:\n first =%s\n second=%s", first, second)
	}

	buildContract := func() []byte {
		return contractBody(t, New(&stubClient{}, nil).WithDescriptors(names))
	}
	if first, second := buildContract(), buildContract(); !bytes.Equal(first, second) {
		t.Errorf("/contract document is not reproducible across two runs (map iteration order leaking into the wire?):\n first =%s\n second=%s", first, second)
	}
}
