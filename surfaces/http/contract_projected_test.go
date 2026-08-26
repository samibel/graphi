package http

import (
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/analysis"
)

// SW-225 (AX-05) AC-2 — the projected /contract capability list vs the legacy
// one.
//
// The AX-00 golden (routes_golden_test.go) pins whatever /contract currently
// serves. It cannot tell whether the two sources agree, because only one serves
// at a time. This file is that second proof, run across the profile matrix that
// actually varies the answer: Labs on/off × analyzers wired/unwired.

// withContractSource flips the served /contract source for one test.
func withContractSource(t *testing.T, source contractSourceKind) {
	t.Helper()
	previous := contractSource
	contractSource = source
	t.Cleanup(func() { contractSource = previous })
}

// wiredAnalyzerNames is the analyzer set cmd/graphi injects through
// WithDescriptors, taken from the real analysis service rather than a copy.
func wiredAnalyzerNames(t *testing.T) []string {
	t.Helper()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	return analysis.NewDefaultService(store).Names()
}

// contractProfiles are the four server shapes whose /contract answers differ.
// "unwired" is not a curiosity: a Server built without WithDescriptors is what
// every test double and every embedder that does not own an analysis.Service
// gets, and a projection that emitted the whole catalog regardless would widen
// exactly that case.
func contractProfiles(t *testing.T) []struct {
	name      string
	labs      string
	analyzers []string
} {
	t.Helper()
	wired := wiredAnalyzerNames(t)
	return []struct {
		name      string
		labs      string
		analyzers []string
	}{
		{"stable-default/analyzers-wired", "", wired},
		{"labs-opt-in/analyzers-wired", "1", wired},
		{"stable-default/analyzers-unwired", "", nil},
		{"labs-opt-in/analyzers-unwired", "1", nil},
	}
}

// AC-2 — structure-identical resource lists, across every profile.
func TestAX05_ProjectedContractResources_MatchTheLegacyList(t *testing.T) {
	for _, tc := range contractProfiles(t) {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(LabsEnvVar, tc.labs)
			srv := New(&stubClient{}, nil).WithDescriptors(tc.analyzers)

			legacy := srv.legacyContractResources()
			projected := srv.projectedContractResources()
			if len(legacy) == 0 {
				t.Fatal("the legacy resource list is empty; the comparison would be vacuous")
			}
			if strings.Join(legacy, "\n") == strings.Join(projected, "\n") {
				return
			}

			legacySet := map[string]bool{}
			for _, resource := range legacy {
				legacySet[resource] = true
			}
			projectedSet := map[string]bool{}
			for _, resource := range projected {
				projectedSet[resource] = true
			}
			for _, resource := range legacy {
				if !projectedSet[resource] {
					t.Errorf("the projection DROPPED %q, which the legacy /contract advertises", resource)
				}
			}
			for _, resource := range projected {
				if !legacySet[resource] {
					t.Errorf("the projection ADDED %q, which the legacy /contract does not advertise", resource)
				}
			}
			t.Errorf("legacy   = %v\nprojected = %v", legacy, projected)
		})
	}
}

// AC-5 — the served source is the projection, the switch reaches the other one,
// and both answer identically at switch time.
func TestAX05_ContractRollbackSwitch_SelectsTheServedSource(t *testing.T) {
	if contractSource != contractSourceProjected {
		t.Fatalf("the shipped /contract source is %q, want %q — AC-2 requires the derived form "+
			"to be the served one", contractSource, contractSourceProjected)
	}
	t.Setenv(LabsEnvVar, "1")
	srv := New(&stubClient{}, nil).WithDescriptors(wiredAnalyzerNames(t))

	withContractSource(t, contractSourceLegacy)
	legacy := strings.Join(srv.contractResources(), "\n")
	withContractSource(t, contractSourceProjected)
	projected := strings.Join(srv.contractResources(), "\n")
	if legacy != projected {
		t.Fatalf("the two /contract sources disagree at switch time:\n legacy    = %s\n projected = %s",
			legacy, projected)
	}
	if legacy != strings.Join(srv.legacyContractResources(), "\n") {
		t.Fatal("with the switch on legacy, contractResources() did not return the legacy list")
	}
}

// AC-2, the diagnostics half — every served capability resolves to exactly one
// resource, and the tier the catalog declares agrees with the tier the runtime
// capability guard applies. A disagreement here would mean /contract advertises
// one profile while capabilityGuard enforces another, which is the 403-on-an-
// advertised-route failure mode the guard exists to prevent.
func TestAX05_CapabilityDiagnostics_AgreeWithTheRuntimeGuard(t *testing.T) {
	t.Setenv(LabsEnvVar, "1")
	srv := New(&stubClient{}, nil).WithDescriptors(wiredAnalyzerNames(t))

	rows := srv.capabilityDiagnostics()
	if len(rows) == 0 {
		t.Fatal("no served capabilities were resolved")
	}
	byResource := map[string]string{}
	fromCatalog := 0
	for _, row := range rows {
		if row.Resource == "" {
			t.Errorf("capability %q resolved to an empty resource", row.Capability)
		}
		if owner, dup := byResource[row.Resource]; dup {
			t.Errorf("resource %q is claimed by both %q and %q", row.Resource, owner, row.Capability)
		}
		byResource[row.Resource] = row.Capability
		if row.Labs != isLabsCapability(row.Capability) {
			t.Errorf("%s: the projection tiers it labs=%v, the runtime capability guard says labs=%v",
				row.Capability, row.Labs, isLabsCapability(row.Capability))
		}
		if row.Source == capabilityFromCatalog || row.Source == capabilityFromCatalogResource {
			if row.Operation == "" {
				t.Errorf("capability %q resolved through the catalog but names no operation", row.Capability)
			}
			fromCatalog++
		}
	}
	// The projection has to be doing real work: if nothing resolved through the
	// catalog, every row fell back to the legacy naming and this file would be
	// testing the legacy code against itself.
	if fromCatalog < len(rows)/2 {
		t.Errorf("only %d of %d capabilities resolved through the operation catalog; the "+
			"projection is mostly falling back to the legacy naming", fromCatalog, len(rows))
	}
}

// The surface-sourced remainder must stay the eight engine analyzers that have
// no MCP tool. A new one appearing here means a capability was added to the HTTP
// surface without a catalog spec, and the decision belongs on that PR.
func TestAX05_CapabilityDiagnostics_SurfaceRemainderIsTheKnownEight(t *testing.T) {
	t.Setenv(LabsEnvVar, "1")
	srv := New(&stubClient{}, nil).WithDescriptors(wiredAnalyzerNames(t))

	want := []string{
		"batched", "call-chain", "communities", "concept",
		"metrics", "notebook-ingest", "taint-query", "watcher-status",
	}
	var got []string
	for _, row := range srv.capabilityDiagnostics() {
		if row.Source == capabilityFromSurface {
			got = append(got, row.Capability)
		}
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("engine analyzers with no catalog spec:\n got  %v\n want %v\n"+
			"An addition means a new HTTP capability has no OperationSpec; a removal means one "+
			"gained a spec. Either way, update this list in the same change.", got, want)
	}
}

// Determinism: /contract is built from maps on both paths, and map iteration
// order escaping into a negotiated wire document is exactly the class of bug the
// AX-00 reproducibility test was written for.
func TestAX05_ProjectedContractResources_AreReproducible(t *testing.T) {
	t.Setenv(LabsEnvVar, "1")
	srv := New(&stubClient{}, nil).WithDescriptors(wiredAnalyzerNames(t))
	first := strings.Join(srv.projectedContractResources(), "\n")
	for i := 0; i < 8; i++ {
		if got := strings.Join(srv.projectedContractResources(), "\n"); got != first {
			t.Fatalf("the projected /contract list is not reproducible:\n first = %s\n got   = %s", first, got)
		}
	}
	var capabilities []string
	for _, row := range srv.capabilityDiagnostics() {
		capabilities = append(capabilities, row.Capability)
	}
	for i := 0; i < 8; i++ {
		var again []string
		for _, row := range srv.capabilityDiagnostics() {
			again = append(again, row.Capability)
		}
		if strings.Join(again, ",") != strings.Join(capabilities, ",") {
			t.Fatalf("capability diagnostics are not in a stable order:\n first = %v\n got   = %v",
				capabilities, again)
		}
	}
}
