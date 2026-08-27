package http

import (
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/opcatalog"
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

	// SW-240 (AX-12) — the switch must reach a genuinely DIFFERENT code path.
	// Bytes cannot show that: the two sources agree by construction, so an
	// equality that holds in both positions is equally consistent with one
	// implementation being read twice. Show it the way the MCP half does
	// instead, by corrupting what only the projection reads.
	withTamperedContractCatalog(t)
	withContractSource(t, contractSourceLegacy)
	if got := strings.Join(srv.contractResources(), "\n"); got != legacy {
		t.Fatalf("with the switch on legacy, tampering with the operation catalog changed what "+
			"is served — the switch does not select the source it claims to:\n before = %s\n after  = %s",
			legacy, got)
	}
	withContractSource(t, contractSourceProjected)
	tampered := strings.Join(srv.contractResources(), "\n")
	if tampered == legacy {
		t.Fatal("with the switch on projected, tampering with the operation catalog changed " +
			"nothing — either the projected path is not the one being served, or the tamper " +
			"is inert and proves nothing (SW-240 AC-2)")
	}
	if !strings.Contains(tampered, tamperedResource) {
		t.Fatalf("the served list moved but not in the way the tamper predicts; %q is absent:\n %s",
			tamperedResource, tampered)
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

// SW-240 (AX-12) — the tamper-based independence proof for the HTTP half.
//
// SW-225 left this switch proved only by byte equality and direct calls. Bytes
// cannot distinguish "two independent implementations that agree" from "one
// implementation read twice", and that distinction is exactly what a
// precondition check for the SW-238 migration would have to cite. The MCP half
// already closes the same gap by corrupting what only the projected path reads
// (surfaces/mcp/descriptors_projected_test.go —
// TestAX05_RollbackSwitch_SelectsTheServedSource and
// TestAX05_TheTwoSourcesAreIndependent). This file follows that idiom rather
// than inventing a second one, so the two surfaces read as one argument.

const (
	// tamperedResourceOperation is resolved by operation ID
	// (capabilityFromCatalog) and takes BOTH its resource and its tier from the
	// spec, so renaming its HTTPResource moves a resource in the projected list.
	// It is a Stable operation, which makes the corruption visible in every
	// profile — Labs on or off, analyzers wired or not.
	tamperedResourceOperation = "callers"
	tamperedResource          = "query/callers__SW240_TAMPERED"
	// tamperedTierOperation is resolved by declared HTTP resource
	// (capabilityFromCatalogResource) and takes ONLY its tier from the spec, so
	// the corruption there has to be a tier flip. Labs → Stable makes the
	// projection advertise analyze/taint on a Stable-default server, which the
	// legacy list drops.
	tamperedTierOperation = "analyze_taint"
	tamperedTierResource  = "analyze/taint"
)

// withTamperedContractCatalog corrupts the operation catalog the PROJECTED
// /contract path reads, for the duration of one test, and restores it after.
// The legacy builder reads neither var, so anything it notices is a defect.
//
// The corruption has to CHANGE answers, not remove them. Emptying the catalog
// would prove nothing: capabilityDiagnostics falls back to legacyResourceFor
// plus isLabsCapability, so a catalog with no specs yields precisely the legacy
// list and a "the two agree" assertion would still pass. That is the trap
// SW-240 AC-2 exists to close, so both corruptions here are rewrites — one per
// catalog join capabilityDiagnostics performs.
func withTamperedContractCatalog(t *testing.T) {
	t.Helper()

	tampered := opcatalog.New()
	for _, spec := range contractCatalog.All() {
		switch spec.ID {
		case tamperedResourceOperation:
			spec.HTTPResource = tamperedResource
		case tamperedTierOperation:
			spec.Tier = opcatalog.TierStable
		}
		if err := tampered.Add(spec); err != nil {
			t.Fatalf("build a tampered catalog: add %q: %v", spec.ID, err)
		}
	}
	built, err := tampered.Build()
	if err != nil {
		t.Fatalf("build a tampered catalog: %v", err)
	}
	byResource := make(map[string]opcatalog.OperationSpec)
	for _, spec := range built.All() {
		if spec.HTTPResource == "" {
			continue
		}
		if owner, dup := byResource[spec.HTTPResource]; dup {
			t.Fatalf("the tampered catalog collides on resource %q (%q and %q)",
				spec.HTTPResource, owner.ID, spec.ID)
		}
		byResource[spec.HTTPResource] = spec
	}

	savedCatalog, savedByResource := contractCatalog, contractCatalogByResource
	t.Cleanup(func() {
		contractCatalog, contractCatalogByResource = savedCatalog, savedByResource
	})
	contractCatalog, contractCatalogByResource = built, byResource
}

// AC-1/AC-2 — independence, proved by tampering. If a future change made the
// legacy builder forward to the projection (or vice versa), every byte-equality
// test in this file would pass while certifying nothing. Corrupting the
// catalog and requiring the legacy list NOT to move is what rules that out.
//
// The same run also shows the tamper is not inert: the projected list must move
// in every profile, and it must move in the specific way each corruption
// predicts.
func TestAX05_TheTwoContractSourcesAreIndependent(t *testing.T) {
	for _, tc := range contractProfiles(t) {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(LabsEnvVar, tc.labs)
			srv := New(&stubClient{}, nil).WithDescriptors(tc.analyzers)

			legacyBefore := strings.Join(srv.legacyContractResources(), "\n")
			projectedBefore := strings.Join(srv.projectedContractResources(), "\n")
			untamperedResource := "query/" + tamperedResourceOperation
			if legacyBefore == "" {
				t.Fatal("the legacy resource list is empty; the comparison would be vacuous")
			}

			withTamperedContractCatalog(t)

			if got := strings.Join(srv.legacyContractResources(), "\n"); got != legacyBefore {
				t.Fatalf("tampering with the operation catalog changed the LEGACY list — the two "+
					"/contract sources are not independent and the parity gate certifies "+
					"nothing:\n before = %s\n after  = %s", legacyBefore, got)
			}

			projectedAfter := strings.Join(srv.projectedContractResources(), "\n")
			if projectedAfter == projectedBefore {
				t.Fatal("tampering with the operation catalog changed nothing in the PROJECTED " +
					"list; an inert tamper proves nothing (SW-240 AC-2)")
			}

			// The by-id join: a renamed HTTPResource must be what the projection
			// advertises, and the legacy name must be gone from it.
			if strings.Contains(projectedBefore, tamperedResource) {
				t.Fatalf("%q is advertised without the tamper; pick a sentinel that cannot "+
					"occur naturally", tamperedResource)
			}
			if !strings.Contains(projectedAfter, tamperedResource) {
				t.Fatalf("the projection did not pick up the renamed resource %q — the "+
					"capabilityFromCatalog join is not live:\n %s", tamperedResource, projectedAfter)
			}
			for _, resource := range srv.projectedContractResources() {
				if resource == untamperedResource {
					t.Fatalf("the projection still advertises the pre-tamper resource %q; it is "+
						"not reading the catalog spec it claims to", untamperedResource)
				}
			}

			// The by-resource join supplies the TIER only, and `taint` is an
			// injected analyzer, so the flip is observable exactly where a
			// Stable-default server has analyzers wired.
			tierTamperVisible := tc.labs == "" && len(tc.analyzers) > 0
			hadTaint := strings.Contains(legacyBefore, tamperedTierResource)
			hasTaint := strings.Contains(projectedAfter, tamperedTierResource)
			if tierTamperVisible {
				if hadTaint {
					t.Fatalf("%q is already advertised by the Stable-default legacy list; the "+
						"tier tamper cannot be observed here", tamperedTierResource)
				}
				if !hasTaint {
					t.Fatalf("flipping %q to Stable did not make the projection advertise %q — "+
						"the capabilityFromCatalogResource join is not live",
						tamperedTierOperation, tamperedTierResource)
				}
			}
		})
	}
}
