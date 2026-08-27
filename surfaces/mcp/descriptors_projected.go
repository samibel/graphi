package mcp

// SW-225 (AX-05) — MCP tool descriptors PROJECTED from the operation catalog.
//
// # What changed and what did not
//
// Descriptor BODIES (description, input schema, annotations) are no longer
// written twice in Go literals; they are read from engine/opcatalog, which
// SW-223 populated as the single canonical statement of what each operation IS.
// Nothing else moved: dispatch is still legacy (AC-4 — surfaces/mcp/toolcalls.go
// does not import the catalog and must not), the per-binding narrowing is still
// filterSupportedToolDescriptors over the projected list, and the legacy
// literals are still compiled in and switchable back (AC-5, descriptorSource).
//
// # The advertisement ORDER is not in the catalog, on purpose
//
// The catalog is id-sorted by construction (engine/opcatalog.Catalog), so it
// carries no advertisement order. The order of the tools/list array IS
// wire-observable and frozen by the AX-00 goldens, so it has to come from
// somewhere. Rather than add a second ordered name list, the projection reads
// the one that already exists: query.Operations followed by
// mcp.singletonToolNames, with the Stable profile being that same sequence
// filtered by IsStableMCPTool. singletonToolNames is consumed elsewhere only by
// ToolNames(), which sorts, so it was free to become the ordered source of truth
// (two entries moved in SW-225; the resulting order is byte-identical to what
// the legacy builders produced, which the goldens prove).
//
// # THE DIVERGENCE, AND ITS COLLAPSE (SW-223 → SW-225 → SW-241)
//
// SW-223 found that ten of the eleven Stable MCP tools were advertised with two
// different descriptors depending on the bound profile: agent_brief,
// change_risk, explain_symbol and related_files differed in description AND
// input schema (explain_symbol's maximal schema had no `limit` at all), while
// callers, callees, references, definition, neighborhood and search differed in
// annotations (present in the Stable profile, absent in the maximal one). Only
// impact agreed, because both builders call one shared function.
//
// SW-225 PRESERVED that divergence rather than papering over it — collapsing it
// touches a Stable-12 request schema on the wire, this file's own story forbade
// it, and folding a wire change into the projection commit would have made the
// AC-5 rollback switch unattributable. What SW-225 did instead was make the
// divergence ONE validated fact (OperationSpec.StableProfileAdvertisement)
// instead of two independently-editable Go literals, and record the direction a
// later ticket should collapse it in.
//
// SW-241 (AX-12) performed that collapse, in the recorded direction: the
// MAXIMAL profile ADOPTED the Stable-profile advertisement. Consequences, all
// four of them worth stating:
//
//  1. The shipped default profile did not move a byte — the `stable`,
//     `stdio-stable` and `daemon-stable` AX-00 goldens are untouched by that
//     change, which is how the claim is proved rather than asserted.
//  2. `-labs` sessions GAINED the read-only annotations on the six structural
//     query tools and on `search`, and gained explain_symbol's, related_files'
//     and change_risk's `limit` argument.
//  3. `-labs` sessions LOST the longer six-facet prose those four descriptors
//     used to carry. That is the cost of this direction and it is not hidden:
//     the opposite direction would have stripped annotations and a documented
//     argument from the SHIPPED default instead, which is strictly worse.
//  4. OperationSpec.StableProfileAdvertisement was REMOVED, not left behind as
//     an always-equal field. A spec now carries exactly one advertisement, and
//     the projection below has nothing to choose between — its `stableProfile`
//     parameter survives only as the tier guard that keeps a Labs operation out
//     of the default profile.
//
// The invariant that replaced the divergence record is asserted directly:
// TestAX12_ProfileAdvertisements_AgreeAcrossProfiles (descriptors_projected_test.go)
// fails, with the tool and field named, if the two profiles ever advertise
// differently again.
//
// # One thing projection genuinely gives up, stated plainly
//
// Two legacy descriptors were COMPUTED, not written: graph_health's description
// and `policy` enum come from trust.PolicyIDs(), and strict_query's from
// strictQueryOperations(). The legacy comments say why — "a policy version bump
// reaches the agent-facing schema automatically; a hand-copied list here would
// advertise a token the resolver no longer accepts". In the catalog those are
// frozen strings, so that automatic propagation is gone.
//
// It is replaced, deliberately, by a REVIEWED one. The AX-03 parity gate
// (opcatalog_parity_test.go) re-derives the comparison from the legacy builders
// on every run, so a change to trust.PolicyIDs() breaks the build with the exact
// field named and shadow.json has to be updated in the same change. That is
// slower than automatic, and it is the trade graphi's standards already make
// elsewhere: a superseded wire spelling must FAIL rather than be quietly
// accepted, and a schema that silently follows a Go constant is the same class
// of unreviewed wire change in the other direction. The one thing that would be
// unacceptable — the enum drifting away from what the resolver accepts, with
// nothing noticing — is exactly what the gate prevents.

import (
	"fmt"

	"github.com/samibel/graphi/engine/opcatalog"
	"github.com/samibel/graphi/engine/query"
)

// descriptorSourceKind names which of the two descriptor sources serves
// tools/list.
type descriptorSourceKind string

const (
	// descriptorSourceProjected derives descriptors from engine/opcatalog.
	descriptorSourceProjected descriptorSourceKind = "projected"
	// descriptorSourceLegacy serves the hand-written Go literals in
	// descriptors.go.
	descriptorSourceLegacy descriptorSourceKind = "legacy"
)

// descriptorSource is the AC-5 rollback switch. It is deliberately NOT an
// environment variable: tools/list bytes are wire contract, and a knob an
// operator can turn would mean two graphi processes on the same version could
// advertise differently. Rolling back is a one-line source change plus a
// release — visible in a diff, like every other wire change.
//
// Tests flip it through withDescriptorSource (descriptors_projected_test.go),
// which proves both sources produce identical bytes at switch time.
var descriptorSource = descriptorSourceProjected

// stableToolDescriptors returns the profile-static Stable catalog from whichever
// source descriptorSource selects.
func stableToolDescriptors() []map[string]any {
	if descriptorSource == descriptorSourceLegacy {
		return legacyStableToolDescriptors()
	}
	return cloneDescriptors(projectedProfiles.stable)
}

// maximalToolDescriptors returns the profile-static Stable+Labs catalog from
// whichever source descriptorSource selects.
func maximalToolDescriptors() []map[string]any {
	if descriptorSource == descriptorSourceLegacy {
		return legacyMaximalToolDescriptors()
	}
	return cloneDescriptors(projectedProfiles.maximal)
}

// maximalAdvertisementOrder is the wire-observable order of the maximal
// registry: the structural query operations in engine order, then the singleton
// tools in singletonToolNames order.
func maximalAdvertisementOrder() []string {
	out := make([]string, 0, len(query.Operations)+len(singletonToolNames))
	out = append(out, query.Operations...)
	out = append(out, singletonToolNames...)
	return out
}

// stableAdvertisementOrder is the maximal order filtered to the default MCP
// profile. Filtering rather than listing separately is what keeps the two
// profiles from disagreeing about where a tool sits.
func stableAdvertisementOrder() []string {
	full := maximalAdvertisementOrder()
	out := make([]string, 0, len(StableOperations)-1)
	for _, id := range full {
		if IsStableMCPTool(id) {
			out = append(out, id)
		}
	}
	return out
}

// profileDescriptors holds the two profile-static projections.
type profileDescriptors struct {
	stable  []map[string]any
	maximal []map[string]any
}

// projectedProfiles is built ONCE, at package initialisation, and panics on
// failure.
//
// That is the fail-closed choice, not the dramatic one. shadow.json is
// go:embed'ed, so a catalog that cannot be loaded, or that is missing an
// advertised operation, is a defect in the binary rather than a runtime
// condition: it is deterministic, it is identical on every machine, and it is
// caught by any test run before a release. The alternatives are worse — falling
// back to the legacy literals would be exactly the quiet degradation graphi's
// standards forbid (a corrupted catalog would ship as a green server), and
// deferring the failure to the first tools/list would turn a build defect into a
// mid-session protocol error.
var projectedProfiles = mustProjectProfiles()

func mustProjectProfiles() profileDescriptors {
	catalog, err := opcatalog.Shadow()
	if err != nil {
		panic(fmt.Sprintf("mcp: the MCP descriptor projection needs the operation catalog: %v", err))
	}
	projected, err := projectProfiles(catalog)
	if err != nil {
		panic(fmt.Sprintf("mcp: projecting MCP descriptors from the operation catalog: %v", err))
	}
	return projected
}

// projectProfiles builds both profile-static catalogs from one frozen operation
// catalog. It takes the catalog as a parameter so a test can feed it a
// deliberately broken one and prove the failure is loud.
func projectProfiles(catalog *opcatalog.Catalog) (profileDescriptors, error) {
	stable, err := projectCatalogDescriptors(catalog, stableAdvertisementOrder(), true)
	if err != nil {
		return profileDescriptors{}, err
	}
	maximal, err := projectCatalogDescriptors(catalog, maximalAdvertisementOrder(), false)
	if err != nil {
		return profileDescriptors{}, err
	}
	return profileDescriptors{stable: stable, maximal: maximal}, nil
}

// projectDescriptors turns catalog specs into MCP descriptor maps, in the given
// advertisement order.
//
// stableProfile no longer selects between two advertisements — SW-241 collapsed
// them (see this file's header). It survives as the guard that requires every
// operation projected into the default profile to actually be Stable: a Labs
// operation reaching it would be a silent widening of the frozen surface, so it
// fails construction instead.
func projectCatalogDescriptors(catalog *opcatalog.Catalog, order []string, stableProfile bool) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(order))
	seen := make(map[string]bool, len(order))
	for _, id := range order {
		if seen[id] {
			return nil, fmt.Errorf("mcp: advertisement order lists %q twice", id)
		}
		seen[id] = true
		spec, ok := catalog.Lookup(id)
		if !ok {
			return nil, fmt.Errorf("mcp: advertised operation %q has no spec in the operation catalog", id)
		}
		descriptor, err := ProjectToolDescriptor(spec, stableProfile)
		if err != nil {
			return nil, err
		}
		out = append(out, descriptor)
	}
	if !stableProfile {
		// One marker source: the same markLabs the legacy builder used, keyed on
		// the same StableOperations set. The catalog stores clean descriptions
		// precisely so the tier stays a single fact (opcatalog.Advertisement).
		markLabs(out)
	}
	return out, nil
}

// ProjectToolDescriptor renders ONE catalog spec into the MCP descriptor the
// server advertises for it. It does not apply the `[labs]` marker — that is
// markLabs's job, applied once per profile over the whole list, because the
// marker is a function of the profile as well as the tier.
//
// It was extracted from projectCatalogDescriptors's loop body by SW-230 and is
// exported for exactly one reason: the AX-10 conformance harness verifies that a
// contributed spec renders to valid MCP metadata, and it has to verify THIS
// projection rather than a copy of it. A harness pointed at a re-implementation
// certifies the re-implementation.
func ProjectToolDescriptor(spec opcatalog.OperationSpec, stableProfile bool) (map[string]any, error) {
	if stableProfile && spec.Tier != opcatalog.TierStable {
		return nil, fmt.Errorf("mcp: operation %q is tiered %q but appears in the Stable profile",
			spec.ID, spec.Tier)
	}
	advertisement := spec.Advertisement
	if advertisement.Description == "" {
		return nil, fmt.Errorf("mcp: operation %q has an empty description in the operation catalog", spec.ID)
	}
	descriptor := map[string]any{
		"name":        spec.ID,
		"description": advertisement.Description,
	}
	if advertisement.InputSchema != nil {
		descriptor["inputSchema"] = cloneJSONValue(advertisement.InputSchema)
	}
	if advertisement.Annotations != nil {
		descriptor["annotations"] = cloneJSONValue(advertisement.Annotations)
	}
	return descriptor, nil
}

// MarkLabsDescriptor applies the advertisement-time `[labs] ` marker to one
// projected descriptor, through the same markLabs the profile projection uses.
//
// It exists so the conformance harness can compare an ADVERTISED descriptor,
// which is what a client actually receives. Without it a caller would have to
// prepend the prefix itself, which would make the harness's tier check a test of
// the caller's string concatenation.
func MarkLabsDescriptor(descriptor map[string]any) map[string]any {
	list := []map[string]any{descriptor}
	markLabs(list)
	return list[0]
}

// cloneDescriptors returns a deep copy of a projected catalog.
//
// The projections are built once and handed to every caller, and the legacy
// builders returned freshly allocated maps on every call. Handing out the cached
// slice would silently change that contract: markLabs mutates descriptions in
// place, and any future caller that edited a descriptor would corrupt the
// catalog for the rest of the process. Copying keeps the projected path exactly
// as safe as the path it replaces.
func cloneDescriptors(descriptors []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(descriptors))
	for _, descriptor := range descriptors {
		copied := make(map[string]any, len(descriptor))
		for key, value := range descriptor {
			copied[key] = cloneJSONValue(value)
		}
		out = append(out, copied)
	}
	return out
}

// cloneJSONValue deep-copies a decoded JSON value. Scalars are immutable and are
// returned as-is; only the containers need copying.
func cloneJSONValue(v any) any {
	switch value := v.(type) {
	case map[string]any:
		copied := make(map[string]any, len(value))
		for key, inner := range value {
			copied[key] = cloneJSONValue(inner)
		}
		return copied
	case []any:
		copied := make([]any, len(value))
		for i, inner := range value {
			copied[i] = cloneJSONValue(inner)
		}
		return copied
	default:
		return v
	}
}
