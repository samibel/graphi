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
// # THE DIVERGENCE DECISION (SW-223's inherited finding)
//
// Ten of the eleven Stable MCP tools are advertised with two different
// descriptors depending on the bound profile: agent_brief, change_risk,
// explain_symbol and related_files differ in description AND input schema
// (explain_symbol's maximal schema has no `limit` at all), while callers,
// callees, references, definition, neighborhood and search differ in
// annotations (present in the Stable profile, absent in the maximal one). Only
// impact agrees, because both builders call one shared function.
//
// SW-225 PRESERVES the divergence, and the reason is not inertia:
//
//  1. Collapsing it would change a Stable-12 REQUEST SCHEMA on the wire. The
//     Extension Platform Kernel spec's whole-slice boundary is that "Stable-12
//     wire names, request schemas and canonical result bytes are byte-untouched
//     throughout"; ADR 0013 I1 says the twelve frozen operations must produce
//     their AX-00 bytes unchanged. A projection story is not the place to spend
//     that budget.
//  2. This story's own acceptance criteria forbid it: AC-1 requires the
//     generated set to be byte-identical to the legacy descriptors "for every
//     binding profile", and AC-6 requires the SW-220 goldens to pass
//     byte-identically with projection serving. A single descriptor per
//     operation cannot satisfy either.
//  3. Rollback would stop meaning anything. AC-5 exists so the projection can be
//     switched off if it misbehaves in the field. If the same commit ALSO
//     changed what ten Stable tools advertise, flipping the switch back would
//     revert both, and a bug report could not be attributed to one of them.
//
// What did change is that the divergence is now ONE fact instead of two
// independently-editable Go literals: it lives in the catalog as
// OperationSpec.StableProfileAdvertisement, it is validated (a divergence record
// that no longer matches the surface, or one invented where the profiles agree,
// fails surfaces/mcp/opcatalog_parity_test.go in both directions), and the
// projection below is the only place that chooses between the two forms.
//
// The recommended way to actually collapse it, in its own ticket, is recorded
// here so the analysis is not redone: make the MAXIMAL profile adopt the
// Stable-profile advertisement for those ten tools. That direction is purely
// additive for a client (`-labs` sessions GAIN the read-only annotations and
// explain_symbol's `limit`), it leaves the shipped default stdio profile
// byte-identical, and it moves only the `maximal`, `stdio-labs` and
// `daemon-labs` goldens. The opposite direction — Stable adopting the maximal
// form — would REMOVE annotations and a documented argument from the default
// profile and should not be taken.
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
// stableProfile selects the Stable-profile advertisement where the catalog
// records one (see the divergence decision in this file's header) and requires
// every projected operation to actually be Stable — a Labs operation reaching
// the default profile would be a silent widening of the frozen surface, so it
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
	advertisement := spec.Advertisement
	if stableProfile {
		if spec.Tier != opcatalog.TierStable {
			return nil, fmt.Errorf("mcp: operation %q is tiered %q but appears in the Stable profile",
				spec.ID, spec.Tier)
		}
		if spec.StableProfileAdvertisement != nil {
			advertisement = *spec.StableProfileAdvertisement
		}
	}
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
