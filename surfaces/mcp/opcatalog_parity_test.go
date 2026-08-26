package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/opcatalog"
)

// SW-223 (AX-03) AC-3 — the shadow catalog vs the LEGACY MCP sources.
//
// engine/opcatalog mirrors what this package already advertises, and nothing
// dispatches from it yet. A mirror nobody checks is a second hand-maintained
// list, which is the thing the extension-kernel program exists to abolish — so
// this file re-derives the comparison from the LIVE builders on every run:
// ToolNames(), stableToolDescriptors(), maximalToolDescriptors(),
// StableOperations and StableMCPToolNames(). A missing, extra, duplicate,
// re-tiered, re-described or re-schema'd operation fails here, on the PR that
// caused it.
//
// The comparisons are pure functions returning a list of problems rather than
// t.Errorf calls sprinkled through the walk. That shape is what lets
// TestAX03_ParityGate_DetectsEveryDriftClass run the SAME code against
// deliberately corrupted inputs and prove the gate is not vacuous — a gate that
// has never been observed to fail is an assumption, not evidence.
//
// It lives INSIDE package mcp for two reasons. The descriptor builders are
// unexported, and exporting them for a test would widen this package's API in a
// story whose whole point is changing nothing; and the layer guard permits
// surfaces → engine, so importing the catalog from surface rank is the legal
// direction. The HTTP and coverage-matrix halves of AC-3 cannot live here —
// surfaces/http and internal/coverage both import this package, so an
// in-package test importing them would be an import cycle. They live in
// surfaces/opcatalog_shadow_parity_test.go instead.

// canonicalValue renders a decoded JSON value as stable bytes. encoding/json
// sorts map keys, so a live descriptor map and the catalog's decoded JSON reach
// the same string when they mean the same thing. A marshalling failure is
// rendered rather than swallowed, so it surfaces as a loud diff.
func canonicalValue(v any) string {
	if v == nil {
		return "null"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<unmarshalable %T: %v>", v, err)
	}
	return string(b)
}

// descriptorIndex indexes a live descriptor catalog by name, reporting a
// duplicate rather than letting it shadow silently.
func descriptorIndex(descriptors []map[string]any) (map[string]map[string]any, []string) {
	out := make(map[string]map[string]any, len(descriptors))
	var problems []string
	for _, descriptor := range descriptors {
		name, _ := descriptor["name"].(string)
		if name == "" {
			problems = append(problems, fmt.Sprintf("live descriptor without a name: %#v", descriptor))
			continue
		}
		if _, dup := out[name]; dup {
			problems = append(problems, fmt.Sprintf("live descriptor catalog advertises %q twice", name))
			continue
		}
		out[name] = descriptor
	}
	return out, problems
}

// diffAdvertisedNames is the missing / extra / duplicate half of AC-3.
func diffAdvertisedNames(catalogIDs, liveNames []string) []string {
	var problems []string
	liveSet := make(map[string]bool, len(liveNames))
	for _, name := range liveNames {
		if liveSet[name] {
			problems = append(problems, fmt.Sprintf("ToolNames() advertises %q twice", name))
		}
		liveSet[name] = true
	}
	specSet := make(map[string]bool, len(catalogIDs))
	for _, id := range catalogIDs {
		if specSet[id] {
			problems = append(problems, fmt.Sprintf("the shadow catalog holds %q twice", id))
		}
		specSet[id] = true
	}
	for _, name := range liveNames {
		if !specSet[name] {
			problems = append(problems, fmt.Sprintf(
				"advertised operation %q has NO catalog spec — add it to engine/opcatalog/shadow.json", name))
		}
	}
	for _, id := range catalogIDs {
		if !liveSet[id] {
			problems = append(problems, fmt.Sprintf(
				"catalog spec %q describes an operation nothing advertises — the catalog mirrors, it may not invent", id))
		}
	}
	if len(specSet) != len(liveSet) {
		problems = append(problems, fmt.Sprintf(
			"catalog holds %d distinct specs, ToolNames() advertises %d distinct names",
			len(specSet), len(liveSet)))
	}
	return problems
}

// diffTiers is the tier-mismatch half of AC-3. isStableMCPTool is injected so
// the drift test can feed it a corrupted taxonomy; production callers pass the
// real IsStableMCPTool, the single source of the stability taxonomy.
func diffTiers(specs []opcatalog.OperationSpec, isStableMCPTool func(string) bool, stableProfile []string) []string {
	var problems []string
	for _, spec := range specs {
		want := opcatalog.TierLabs
		if isStableMCPTool(spec.ID) {
			want = opcatalog.TierStable
		}
		if spec.Tier != want {
			problems = append(problems, fmt.Sprintf(
				"%s: catalog tier %q, mcp.IsStableMCPTool says %q", spec.ID, spec.Tier, want))
		}
	}
	var stableInCatalog []string
	for _, spec := range specs {
		if spec.Tier == opcatalog.TierStable {
			stableInCatalog = append(stableInCatalog, spec.ID)
		}
	}
	sort.Strings(stableInCatalog)
	wantStable := append([]string(nil), stableProfile...)
	sort.Strings(wantStable)
	if strings.Join(stableInCatalog, ",") != strings.Join(wantStable, ",") {
		problems = append(problems, fmt.Sprintf(
			"the catalog's Stable set %v differs from StableMCPToolNames() %v", stableInCatalog, wantStable))
	}
	return problems
}

// diffAdvertisement compares one catalog advertisement against one live
// descriptor. labsMarker is applied to the description when the operation is
// Labs, because the catalog stores the CLEAN description and the marker is a
// projection of the tier (surfaces/mcp.markLabs).
func diffAdvertisement(id, context string, advertisement opcatalog.Advertisement, tier opcatalog.Tier,
	descriptor map[string]any, labsMarker string) []string {
	var problems []string
	wantDescription := advertisement.Description
	if labsMarker != "" && tier == opcatalog.TierLabs {
		wantDescription = labsMarker + advertisement.Description
	}
	if got, _ := descriptor["description"].(string); got != wantDescription {
		problems = append(problems, fmt.Sprintf(
			"%s: %s description drift\n  catalog: %q\n  live:    %q", id, context, wantDescription, got))
	}
	if got, want := canonicalValue(descriptor["inputSchema"]), canonicalValue(advertisement.InputSchema); got != want {
		problems = append(problems, fmt.Sprintf(
			"%s: %s inputSchema drift\n  catalog: %s\n  live:    %s", id, context, want, got))
	}
	if got, want := canonicalValue(descriptor["annotations"]), canonicalValue(advertisement.Annotations); got != want {
		problems = append(problems, fmt.Sprintf(
			"%s: %s annotations drift\n  catalog: %s\n  live:    %s", id, context, want, got))
	}
	for key := range descriptor {
		switch key {
		case "name", "description", "inputSchema", "annotations":
		default:
			problems = append(problems, fmt.Sprintf(
				"%s: the live descriptor carries %q, which no OperationSpec field mirrors — "+
					"the catalog would silently drop it", id, key))
		}
	}
	return problems
}

// diffMaximalRegistry compares every spec against the maximal (Stable+Labs)
// registry, which is the canonical advertisement.
func diffMaximalRegistry(specs []opcatalog.OperationSpec, live map[string]map[string]any, labsMarker string) []string {
	var problems []string
	seen := make(map[string]bool, len(specs))
	for _, spec := range specs {
		seen[spec.ID] = true
		descriptor, ok := live[spec.ID]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: no descriptor in the maximal registry", spec.ID))
			continue
		}
		problems = append(problems,
			diffAdvertisement(spec.ID, "maximal", spec.Advertisement, spec.Tier, descriptor, labsMarker)...)
	}
	names := make([]string, 0, len(live))
	for name := range live {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !seen[name] {
			problems = append(problems, fmt.Sprintf(
				"the maximal registry advertises %q with no catalog spec", name))
		}
	}
	return problems
}

// diffStableProfile compares the SECOND advertisement — today the Stable
// profile disagrees with the maximal registry for ten of the eleven Stable MCP
// tools — and pins the divergence record in both directions, so it cannot rot
// into a lie: a variant that stopped being needed and a divergence that appeared
// without one are both reported.
func diffStableProfile(specs []opcatalog.OperationSpec, stableLive, maximalLive map[string]map[string]any) (problems, withVariant []string) {
	for _, spec := range specs {
		descriptor, advertised := stableLive[spec.ID]
		if !advertised {
			if spec.StableProfileAdvertisement != nil {
				problems = append(problems, fmt.Sprintf(
					"%s: carries a Stable-profile advertisement, but the Stable profile does not advertise it", spec.ID))
			}
			continue
		}
		if spec.Tier != opcatalog.TierStable {
			problems = append(problems, fmt.Sprintf(
				"%s: advertised in the Stable profile but tiered %q", spec.ID, spec.Tier))
		}
		advertisement := spec.Advertisement
		if spec.StableProfileAdvertisement != nil {
			withVariant = append(withVariant, spec.ID)
			advertisement = *spec.StableProfileAdvertisement
		}
		// No labs marker: the Stable profile advertises only Stable tools, and
		// markLabs leaves those untouched.
		problems = append(problems,
			diffAdvertisement(spec.ID, "Stable-profile", advertisement, spec.Tier, descriptor, "")...)

		maximalDescriptor := maximalLive[spec.ID]
		diverges := canonicalValue(descriptor["description"]) != canonicalValue(maximalDescriptor["description"]) ||
			canonicalValue(descriptor["inputSchema"]) != canonicalValue(maximalDescriptor["inputSchema"]) ||
			canonicalValue(descriptor["annotations"]) != canonicalValue(maximalDescriptor["annotations"])
		switch {
		case diverges && spec.StableProfileAdvertisement == nil:
			problems = append(problems, fmt.Sprintf(
				"%s: the Stable and maximal profiles advertise it differently, but the catalog "+
					"records no stable_profile_advertisement", spec.ID))
		case !diverges && spec.StableProfileAdvertisement != nil:
			problems = append(problems, fmt.Sprintf(
				"%s: the catalog records a Stable-profile divergence that no longer exists; "+
					"drop stable_profile_advertisement", spec.ID))
		}
	}
	return problems, withVariant
}

// ---------------------------------------------------------------------------
// The gate itself, against the live builders.
// ---------------------------------------------------------------------------

func shadowCatalog(t *testing.T) *opcatalog.Catalog {
	t.Helper()
	catalog, err := opcatalog.Shadow()
	if err != nil {
		t.Fatalf("opcatalog.Shadow(): %v", err)
	}
	return catalog
}

func report(t *testing.T, problems []string) {
	t.Helper()
	for _, problem := range problems {
		t.Error(problem)
	}
}

// AC-2 / AC-3 — exactly one spec per advertised operation.
func TestAX03_ShadowCatalog_CoversExactlyTheAdvertisedToolNames(t *testing.T) {
	report(t, diffAdvertisedNames(shadowCatalog(t).IDs(), ToolNames()))
}

// AC-3 — tier parity against the SINGLE source of the stability taxonomy.
func TestAX03_ShadowCatalog_TiersMatchTheStabilityTaxonomy(t *testing.T) {
	catalog := shadowCatalog(t)
	report(t, diffTiers(catalog.All(), IsStableMCPTool, StableMCPToolNames()))

	// `index` is one of the twelve frozen operations but is lifecycle-only and
	// never an MCP tool, so it must NOT appear here — a catalog that grew one
	// would be advertising a thirteenth tool by accident.
	if _, ok := catalog.Lookup("index"); ok {
		t.Error("the shadow catalog holds `index`, which is a lifecycle operation and never an advertised MCP tool")
	}
	for _, op := range StableOperations {
		if op == "index" {
			continue
		}
		spec, ok := catalog.Lookup(op)
		if !ok {
			t.Errorf("frozen Stable operation %q has no catalog spec", op)
			continue
		}
		if spec.Tier != opcatalog.TierStable {
			t.Errorf("frozen Stable operation %q is tiered %q in the catalog", op, spec.Tier)
		}
	}
}

// AC-2 — description, input schema and annotations match the maximal registry.
func TestAX03_ShadowCatalog_MatchesTheMaximalDescriptorRegistry(t *testing.T) {
	live, problems := descriptorIndex(maximalToolDescriptors())
	report(t, problems)
	report(t, diffMaximalRegistry(shadowCatalog(t).All(), live, labsPrefix))
}

// AC-2 — the Stable profile's second advertisement, and the divergence record.
func TestAX03_ShadowCatalog_MatchesTheStableProfileDescriptors(t *testing.T) {
	stableLive, problems := descriptorIndex(stableToolDescriptors())
	report(t, problems)
	maximalLive, problems := descriptorIndex(maximalToolDescriptors())
	report(t, problems)

	if len(stableLive) != len(StableMCPToolNames()) {
		t.Fatalf("the Stable profile advertises %d tools, StableMCPToolNames() has %d",
			len(stableLive), len(StableMCPToolNames()))
	}
	problems, withVariant := diffStableProfile(shadowCatalog(t).All(), stableLive, maximalLive)
	report(t, problems)
	t.Logf("legacy divergence between the Stable and maximal profiles, mirrored by the catalog "+
		"for %d of %d Stable MCP tools: %v", len(withVariant), len(stableLive), withVariant)
}

// The catalog stores clean descriptions; a stored `[labs] ` marker would make
// the tier two facts that can disagree.
func TestAX03_ShadowCatalog_StoresNoTierMarkerInDescriptions(t *testing.T) {
	for _, spec := range shadowCatalog(t).All() {
		if strings.HasPrefix(spec.Description, labsPrefix) {
			t.Errorf("%s: description stores the %q marker", spec.ID, labsPrefix)
		}
		if spec.StableProfileAdvertisement != nil &&
			strings.HasPrefix(spec.StableProfileAdvertisement.Description, labsPrefix) {
			t.Errorf("%s: Stable-profile description stores the %q marker", spec.ID, labsPrefix)
		}
	}
}

// ---------------------------------------------------------------------------
// The gate about the gate.
// ---------------------------------------------------------------------------

// TestAX03_ParityGate_DetectsEveryDriftClass feeds the SAME comparison
// functions deliberately corrupted inputs and requires each one to be caught.
// AC-3 names four drift classes — missing, extra, duplicate, tier-mismatched —
// and adds description and schema drift through AC-2; a gate that green-lights
// any of them is worse than no gate, because it certifies the absence of a
// check it does not perform.
func TestAX03_ParityGate_DetectsEveryDriftClass(t *testing.T) {
	catalog := shadowCatalog(t)
	specs := catalog.All()
	if len(specs) < 2 {
		t.Fatalf("need at least two specs to perturb, have %d", len(specs))
	}
	names := ToolNames()
	maximalLive, _ := descriptorIndex(maximalToolDescriptors())
	stableLive, _ := descriptorIndex(stableToolDescriptors())

	var stableID, labsID string
	for _, spec := range specs {
		if spec.Tier == opcatalog.TierStable && stableID == "" {
			stableID = spec.ID
		}
		if spec.Tier == opcatalog.TierLabs && labsID == "" {
			labsID = spec.ID
		}
	}
	if stableID == "" || labsID == "" {
		t.Fatalf("the shadow catalog has no stable (%q) or no labs (%q) operation to perturb", stableID, labsID)
	}

	t.Run("missing entry", func(t *testing.T) {
		if got := diffAdvertisedNames(catalog.IDs()[1:], names); len(got) == 0 {
			t.Fatal("dropping a catalog spec was not reported")
		}
	})
	t.Run("extra entry", func(t *testing.T) {
		if got := diffAdvertisedNames(append(catalog.IDs(), "zz_invented"), names); len(got) == 0 {
			t.Fatal("an invented catalog spec was not reported")
		}
	})
	t.Run("duplicate entry", func(t *testing.T) {
		dup := append(catalog.IDs(), catalog.IDs()[0])
		sort.Strings(dup)
		if got := diffAdvertisedNames(dup, names); len(got) == 0 {
			t.Fatal("a duplicated catalog spec was not reported")
		}
	})
	t.Run("operation removed from the live registry", func(t *testing.T) {
		if got := diffAdvertisedNames(catalog.IDs(), names[1:]); len(got) == 0 {
			t.Fatal("an operation that vanished from ToolNames() was not reported")
		}
	})
	t.Run("tier mismatch", func(t *testing.T) {
		perturbed := append([]opcatalog.OperationSpec(nil), specs...)
		for i := range perturbed {
			if perturbed[i].ID == labsID {
				perturbed[i].Tier = opcatalog.TierStable
			}
		}
		if got := diffTiers(perturbed, IsStableMCPTool, StableMCPToolNames()); len(got) == 0 {
			t.Fatalf("promoting %q to stable in the catalog was not reported", labsID)
		}
	})
	t.Run("stable operation demoted", func(t *testing.T) {
		perturbed := append([]opcatalog.OperationSpec(nil), specs...)
		for i := range perturbed {
			if perturbed[i].ID == stableID {
				perturbed[i].Tier = opcatalog.TierLabs
			}
		}
		if got := diffTiers(perturbed, IsStableMCPTool, StableMCPToolNames()); len(got) == 0 {
			t.Fatalf("demoting %q to labs in the catalog was not reported", stableID)
		}
	})
	t.Run("description drift", func(t *testing.T) {
		perturbed := append([]opcatalog.OperationSpec(nil), specs...)
		perturbed[0].Description += " (tampered)"
		if got := diffMaximalRegistry(perturbed, maximalLive, labsPrefix); len(got) == 0 {
			t.Fatal("a rewritten description was not reported")
		}
	})
	t.Run("input schema drift", func(t *testing.T) {
		perturbed := append([]opcatalog.OperationSpec(nil), specs...)
		perturbed[0].InputSchema = map[string]any{"type": "object", "properties": map[string]any{}}
		if got := diffMaximalRegistry(perturbed, maximalLive, labsPrefix); len(got) == 0 {
			t.Fatal("a rewritten input schema was not reported")
		}
	})
	t.Run("annotation drift", func(t *testing.T) {
		perturbed := append([]opcatalog.OperationSpec(nil), specs...)
		perturbed[0].Annotations = map[string]any{"readOnlyHint": false}
		if got := diffMaximalRegistry(perturbed, maximalLive, labsPrefix); len(got) == 0 {
			t.Fatal("a rewritten annotation set was not reported")
		}
	})
	t.Run("a new descriptor key the catalog cannot carry", func(t *testing.T) {
		widened := make(map[string]map[string]any, len(maximalLive))
		for name, descriptor := range maximalLive {
			copied := make(map[string]any, len(descriptor)+1)
			for k, v := range descriptor {
				copied[k] = v
			}
			widened[name] = copied
		}
		widened[specs[0].ID]["outputSchema"] = map[string]any{"type": "object"}
		if got := diffMaximalRegistry(specs, widened, labsPrefix); len(got) == 0 {
			t.Fatal("a descriptor key no OperationSpec field mirrors was not reported")
		}
	})
	t.Run("a divergence record that no longer matches", func(t *testing.T) {
		perturbed := append([]opcatalog.OperationSpec(nil), specs...)
		for i := range perturbed {
			if perturbed[i].StableProfileAdvertisement != nil {
				perturbed[i].StableProfileAdvertisement = nil
				break
			}
		}
		got, _ := diffStableProfile(perturbed, stableLive, maximalLive)
		if len(got) == 0 {
			t.Fatal("dropping a still-needed stable_profile_advertisement was not reported")
		}
	})
	t.Run("a divergence record invented where the profiles agree", func(t *testing.T) {
		perturbed := append([]opcatalog.OperationSpec(nil), specs...)
		invented := false
		for i := range perturbed {
			if perturbed[i].StableProfileAdvertisement == nil && stableLive[perturbed[i].ID] != nil {
				advertisement := perturbed[i].Advertisement
				perturbed[i].StableProfileAdvertisement = &advertisement
				invented = true
				break
			}
		}
		if !invented {
			t.Skip("every Stable-profile tool already carries a divergence record")
		}
		got, _ := diffStableProfile(perturbed, stableLive, maximalLive)
		if len(got) == 0 {
			t.Fatal("an invented stable_profile_advertisement was not reported")
		}
	})
	t.Run("a duplicate live descriptor", func(t *testing.T) {
		descriptors := maximalToolDescriptors()
		_, problems := descriptorIndex(append(descriptors, descriptors[0]))
		if len(problems) == 0 {
			t.Fatal("a duplicated live descriptor was not reported")
		}
	})
}
