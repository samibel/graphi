package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/opcatalog"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/daemon"
)

// SW-225 (AX-05) — the projection-vs-legacy gate.
//
// The AX-00 goldens already prove that whatever tools/list serves matches the
// committed baseline bytes. They do NOT prove that the two sources agree with
// each other, because only one of them is serving at a time. This file is that
// second proof: for every binding profile, the catalog projection and the
// hand-written legacy literals must produce byte-identical descriptors, and the
// comparison is on canonical JSON so a reordered map key or a changed
// `required` list cannot pass as equal.
//
// It also exercises the AC-5 rollback switch in both positions, which is what
// makes "switchable back" a tested property rather than a claim.

// withDescriptorSource flips the served descriptor source for the duration of a
// test and restores it afterwards.
func withDescriptorSource(t *testing.T, source descriptorSourceKind) {
	t.Helper()
	previous := descriptorSource
	descriptorSource = source
	t.Cleanup(func() { descriptorSource = previous })
}

// descriptorBytes renders a descriptor catalog as canonical, diff-readable JSON.
// It is the same shape the AX-00 golden freezes (name, description, inputSchema,
// annotations, plus any unexpected key) so a failure here reads like the golden
// diff it would otherwise become.
func descriptorBytes(t *testing.T, descriptors []map[string]any) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(descriptors); err != nil {
		t.Fatalf("encode descriptors: %v", err)
	}
	return buf.Bytes()
}

// descriptorLines renders one descriptor per line, so a mismatch can be reported
// as the first differing tool rather than as a 900-line blob.
func descriptorLines(t *testing.T, descriptors []map[string]any) []string {
	t.Helper()
	out := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		raw, err := json.Marshal(descriptor)
		if err != nil {
			t.Fatalf("marshal descriptor %#v: %v", descriptor, err)
		}
		out = append(out, string(raw))
	}
	return out
}

// requireIdenticalCatalogs compares two descriptor catalogs and reports the
// FIRST divergence with the tool name, so a drift names the exact descriptor
// (story test note) instead of dumping both catalogs.
func requireIdenticalCatalogs(t *testing.T, profile string, legacy, projected []map[string]any) {
	t.Helper()
	legacyLines := descriptorLines(t, legacy)
	projectedLines := descriptorLines(t, projected)

	for i := 0; i < len(legacyLines) && i < len(projectedLines); i++ {
		if legacyLines[i] == projectedLines[i] {
			continue
		}
		legacyName, _ := legacy[i]["name"].(string)
		projectedName, _ := projected[i]["name"].(string)
		if legacyName != projectedName {
			t.Fatalf("%s: descriptor %d is %q in the legacy catalog and %q in the projection — "+
				"the advertisement ORDER drifted (surfaces/mcp/tools.go singletonToolNames)",
				profile, i, legacyName, projectedName)
		}
		t.Fatalf("%s: descriptor %d (%q) differs between the legacy literals and the catalog projection\n"+
			"  legacy:    %s\n  projected: %s\n"+
			"Fix engine/opcatalog/shadow.json (the catalog mirrors the surface, never the other way round).",
			profile, i, legacyName, legacyLines[i], projectedLines[i])
	}
	if len(legacyLines) != len(projectedLines) {
		t.Fatalf("%s: the legacy catalog advertises %d tools, the projection advertises %d",
			profile, len(legacyLines), len(projectedLines))
	}
	if !bytes.Equal(descriptorBytes(t, legacy), descriptorBytes(t, projected)) {
		t.Fatalf("%s: canonical bytes differ even though every descriptor compared equal", profile)
	}
}

// AC-1 — the two profile-static catalogs are byte-identical between sources.
func TestAX05_ProjectedDescriptors_MatchTheLegacyProfileCatalogs(t *testing.T) {
	requireIdenticalCatalogs(t, "stable", legacyStableToolDescriptors(), cloneDescriptors(projectedProfiles.stable))
	requireIdenticalCatalogs(t, "maximal", legacyMaximalToolDescriptors(), cloneDescriptors(projectedProfiles.maximal))
}

// projectionProfileBindings are the SIX binding profiles AX-00 froze. AC-1 asks
// for byte identity "for every binding profile", which includes the
// CapabilityReporter narrowing applied on top of a projected catalog — the place
// a projection could plausibly break narrowing without breaking the static
// catalogs.
func projectionProfileBindings(t *testing.T) map[string]func() []map[string]any {
	t.Helper()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	wired := func() *client.Direct {
		return client.NewDirect(query.New(store), search.New(store)).
			WithAnalysis(analysis.NewDefaultService(store))
	}
	return map[string]func() []map[string]any{
		"stable":  stableToolDescriptors,
		"maximal": maximalToolDescriptors,
		"stdio-stable": func() []map[string]any {
			return NewServerWithClient(wired()).toolDescriptors()
		},
		"stdio-labs": func() []map[string]any {
			return NewServerWithClient(wired(), WithLabs()).toolDescriptors()
		},
		"daemon-stable": func() []map[string]any {
			return NewServerWithClient(daemon.NewClient("/nonexistent.sock", "/nonexistent")).toolDescriptors()
		},
		"daemon-labs": func() []map[string]any {
			return NewServerWithClient(daemon.NewClient("/nonexistent.sock", "/nonexistent"), WithLabs()).toolDescriptors()
		},
	}
}

// AC-1 / AC-5 — every binding profile is byte-identical under BOTH switch
// positions, and the per-binding narrowing survives projection.
func TestAX05_EveryBindingProfile_IsIdenticalUnderBothSources(t *testing.T) {
	for profile, build := range projectionProfileBindings(t) {
		t.Run(profile, func(t *testing.T) {
			withDescriptorSource(t, descriptorSourceLegacy)
			legacy := build()
			withDescriptorSource(t, descriptorSourceProjected)
			projected := build()
			requireIdenticalCatalogs(t, profile, legacy, projected)
			if len(projected) == 0 {
				t.Fatalf("%s: advertised no tools at all", profile)
			}
		})
	}
}

// AC-1 — the narrowing counts themselves, stated as numbers rather than implied
// by a byte compare, because "11 stdio-stable / 7 daemon / the full catalog with
// -labs" is the property the story names and a reviewer wants to read.
func TestAX05_PerBindingNarrowing_SurvivesProjection(t *testing.T) {
	bindings := projectionProfileBindings(t)
	for _, tc := range []struct {
		profile string
		want    int
	}{
		{"stable", 11},
		{"maximal", 56},
		{"stdio-stable", 11},
		{"stdio-labs", 44},
		{"daemon-stable", 7},
		{"daemon-labs", 25},
	} {
		build, ok := bindings[tc.profile]
		if !ok {
			t.Fatalf("unknown profile %q", tc.profile)
		}
		if got := len(build()); got != tc.want {
			t.Errorf("profile %q advertises %d tools under the projection, want %d", tc.profile, got, tc.want)
		}
	}
}

// AC-5 — the switch is real: flipping it to legacy makes the projection
// unreachable, and flipping it back restores it. A switch nothing exercises is
// a comment.
func TestAX05_RollbackSwitch_SelectsTheServedSource(t *testing.T) {
	if descriptorSource != descriptorSourceProjected {
		t.Fatalf("the shipped default descriptor source is %q, want %q — AC-1 requires the "+
			"derived form to be the served one", descriptorSource, descriptorSourceProjected)
	}

	withDescriptorSource(t, descriptorSourceLegacy)
	legacy := descriptorBytes(t, maximalToolDescriptors())
	withDescriptorSource(t, descriptorSourceProjected)
	projected := descriptorBytes(t, maximalToolDescriptors())
	if !bytes.Equal(legacy, projected) {
		t.Fatal("the two descriptor sources disagree at switch time; rollback would be a wire change")
	}

	// The switch must actually reach a DIFFERENT code path. Proving that by
	// bytes is impossible (the two agree by construction), so prove it by
	// perturbing the projection: with the switch on legacy, a broken projection
	// must not affect what is served.
	saved := projectedProfiles
	t.Cleanup(func() { projectedProfiles = saved })
	projectedProfiles = profileDescriptors{
		stable:  []map[string]any{{"name": "tampered", "description": "tampered"}},
		maximal: []map[string]any{{"name": "tampered", "description": "tampered"}},
	}
	withDescriptorSource(t, descriptorSourceLegacy)
	if got := descriptorBytes(t, maximalToolDescriptors()); !bytes.Equal(got, legacy) {
		t.Fatal("with the switch on legacy, tampering with the projection changed what is served — " +
			"the switch does not select the source it claims to")
	}
	withDescriptorSource(t, descriptorSourceProjected)
	if got := descriptorBytes(t, maximalToolDescriptors()); bytes.Equal(got, legacy) {
		t.Fatal("with the switch on projected, tampering with the projection changed nothing — " +
			"the projected path is not the one being served")
	}
}

// The divergence decision, asserted as data. SW-223 found that ten of the eleven
// Stable MCP tools are advertised differently by the two profiles; SW-225
// PRESERVED that (see descriptors_projected.go's header for why). This test
// pins the decision so a later story cannot collapse it by accident — the
// failure text is where the argument lives.
func TestAX05_StableProfileDivergence_IsPreservedDeliberately(t *testing.T) {
	catalog, err := opcatalog.Shadow()
	if err != nil {
		t.Fatalf("opcatalog.Shadow(): %v", err)
	}
	wantDivergent := map[string]string{
		"agent_brief":    "description + input schema",
		"callees":        "annotations",
		"callers":        "annotations",
		"change_risk":    "description + input schema",
		"definition":     "annotations",
		"explain_symbol": "description + input schema (the maximal form has no `limit`)",
		"neighborhood":   "annotations",
		"references":     "annotations",
		"related_files":  "description + input schema",
		"search":         "annotations",
	}
	const wantAgreeing = "impact"

	got := map[string]bool{}
	for _, spec := range catalog.All() {
		if spec.StableProfileAdvertisement != nil {
			got[spec.ID] = true
		}
	}
	for id, kind := range wantDivergent {
		if !got[id] {
			t.Errorf("%s no longer records a Stable-profile divergence (%s). If that was deliberate, "+
				"it is a WIRE CHANGE to a frozen Stable operation: update this list, the AX-00 golden "+
				"and the reasoning in descriptors_projected.go together.", id, kind)
		}
		delete(got, id)
	}
	for id := range got {
		t.Errorf("%s newly diverges between the Stable and maximal profiles; SW-225 recorded exactly "+
			"ten such operations and %q as the only agreeing one", id, wantAgreeing)
	}
	if spec, ok := catalog.Lookup(wantAgreeing); !ok || spec.StableProfileAdvertisement != nil {
		t.Errorf("%q was the one Stable tool both profiles agreed on; it no longer is", wantAgreeing)
	}

	// The projection is what CHOOSES between the two forms, so prove it chose:
	// explain_symbol's Stable descriptor must carry `limit` and the maximal one
	// must not.
	stable := stableToolDescriptors()
	maximal := maximalToolDescriptors()
	if !descriptorHasProperty(stable, "explain_symbol", "limit") {
		t.Error("the projected Stable profile lost explain_symbol's `limit` argument")
	}
	if descriptorHasProperty(maximal, "explain_symbol", "limit") {
		t.Error("the projected maximal profile grew explain_symbol's `limit` argument — that is the " +
			"collapse SW-225 deliberately did not perform")
	}
}

func descriptorHasProperty(descriptors []map[string]any, tool, property string) bool {
	for _, descriptor := range descriptors {
		if descriptor["name"] != tool {
			continue
		}
		schema, ok := descriptor["inputSchema"].(map[string]any)
		if !ok {
			return false
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			return false
		}
		_, has := properties[property]
		return has
	}
	return false
}

// AC-4 — the projection is METADATA ONLY. Dispatch may not read the catalog, and
// the tool-call path in this package must keep no second opinion about what is
// advertised: toolAdvertised still resolves through toolDescriptors().
func TestAX05_ProjectionIsMetadataOnly(t *testing.T) {
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	server := NewServerWithClient(client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store)))

	for _, name := range StableMCPToolNames() {
		if !server.toolAdvertised(name) {
			t.Errorf("stable tool %q is not advertised on a fully wired stdio binding", name)
		}
	}
	if server.toolAdvertised("memory") {
		t.Error("a Labs tool is advertised by the default Stable profile")
	}
	if server.toolAdvertised("zz_invented") {
		t.Error("an operation that exists nowhere is advertised")
	}
}

// The projection must fail LOUDLY, never quietly. A catalog missing an
// advertised operation is a build defect, and the honest response is an error
// that names the operation — not a short catalog and not a silent fallback to
// the legacy literals.
func TestAX05_Projection_FailsClosedOnAnIncompleteCatalog(t *testing.T) {
	full, err := opcatalog.Shadow()
	if err != nil {
		t.Fatalf("opcatalog.Shadow(): %v", err)
	}
	partial := opcatalog.New()
	dropped := ""
	for _, spec := range full.All() {
		if spec.ID == ToolSearch {
			dropped = spec.ID
			continue
		}
		if addErr := partial.Add(spec); addErr != nil {
			t.Fatalf("rebuild catalog without %q: %v", spec.ID, addErr)
		}
	}
	if dropped == "" {
		t.Fatalf("the shadow catalog no longer declares %q", ToolSearch)
	}
	frozen, err := partial.Build()
	if err != nil {
		t.Fatalf("build partial catalog: %v", err)
	}
	_, err = projectProfiles(frozen)
	if err == nil {
		t.Fatal("projecting from a catalog missing an advertised operation succeeded")
	}
	if !strings.Contains(err.Error(), dropped) {
		t.Errorf("the failure does not name the missing operation %q: %v", dropped, err)
	}
}

// A Labs operation smuggled into the Stable advertisement order must fail
// construction: the default profile is the frozen product promise, and widening
// it by a data edit is exactly what the 12-op freeze exists to prevent.
func TestAX05_Projection_RefusesALabsOperationInTheStableProfile(t *testing.T) {
	catalog, err := opcatalog.Shadow()
	if err != nil {
		t.Fatalf("opcatalog.Shadow(): %v", err)
	}
	_, err = projectCatalogDescriptors(catalog, []string{ToolMemory}, true)
	if err == nil {
		t.Fatal("a Labs operation was accepted into the Stable profile projection")
	}
	if !strings.Contains(err.Error(), ToolMemory) {
		t.Errorf("the failure does not name the offending operation: %v", err)
	}
}

// The advertisement order the projection reads must cover exactly the advertised
// name set — no more, no less. Without this the order list could silently drop a
// tool while every remaining descriptor still compared equal.
func TestAX05_AdvertisementOrder_CoversExactlyTheAdvertisedNames(t *testing.T) {
	order := maximalAdvertisementOrder()
	names := ToolNames()
	if len(order) != len(names) {
		t.Fatalf("the advertisement order lists %d operations, ToolNames() has %d", len(order), len(names))
	}
	seen := make(map[string]bool, len(order))
	for _, id := range order {
		if seen[id] {
			t.Errorf("the advertisement order lists %q twice", id)
		}
		seen[id] = true
	}
	for _, name := range names {
		if !seen[name] {
			t.Errorf("advertised tool %q has no position in the advertisement order", name)
		}
	}
	stable := stableAdvertisementOrder()
	if len(stable) != len(StableMCPToolNames()) {
		t.Fatalf("the Stable advertisement order lists %d operations, StableMCPToolNames() has %d",
			len(stable), len(StableMCPToolNames()))
	}
	for _, id := range stable {
		if !IsStableMCPTool(id) {
			t.Errorf("the Stable advertisement order lists the Labs operation %q", id)
		}
	}
}

// The projection must be reproducible: two calls in one process return equal
// bytes AND independent memory, so a caller mutating one catalog cannot reach
// the next caller's.
func TestAX05_ProjectedDescriptors_AreFreshAndReproducible(t *testing.T) {
	first := maximalToolDescriptors()
	second := maximalToolDescriptors()
	if !bytes.Equal(descriptorBytes(t, first), descriptorBytes(t, second)) {
		t.Fatal("two projections in one process differ")
	}
	first[0]["description"] = "tampered"
	if schema, ok := first[0]["inputSchema"].(map[string]any); ok {
		schema["tampered"] = true
	}
	third := maximalToolDescriptors()
	if third[0]["description"] == "tampered" {
		t.Fatal("mutating a returned descriptor corrupted the projection for the next caller")
	}
	if schema, ok := third[0]["inputSchema"].(map[string]any); ok {
		if _, tampered := schema["tampered"]; tampered {
			t.Fatal("mutating a returned input schema corrupted the projection for the next caller")
		}
	}
	if !bytes.Equal(descriptorBytes(t, second), descriptorBytes(t, third)) {
		t.Fatal("a projection taken after a caller mutated an earlier one is not identical")
	}
}

// A guard against the comparison being vacuous: if a future change made the
// legacy builders forward to the projection (or vice versa), every test above
// would pass while proving nothing. This asserts the two are genuinely separate
// implementations by tampering with one and requiring the other to notice.
func TestAX05_TheTwoSourcesAreIndependent(t *testing.T) {
	saved := projectedProfiles
	t.Cleanup(func() { projectedProfiles = saved })
	projectedProfiles = profileDescriptors{
		stable:  []map[string]any{{"name": "tampered"}},
		maximal: []map[string]any{{"name": "tampered"}},
	}
	if got := len(legacyMaximalToolDescriptors()); got != len(saved.maximal) {
		t.Fatalf("tampering with the projection changed the legacy builder (%d vs %d) — "+
			"the two sources are not independent and the parity gate certifies nothing",
			got, len(saved.maximal))
	}
}
