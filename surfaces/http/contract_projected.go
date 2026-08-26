package http

// SW-225 (AX-05) — the HTTP capability/operation list PROJECTED from the
// operation catalog.
//
// # What is derived and what deliberately is not
//
// Two facts about an operation used to be re-stated here in string arithmetic:
// WHERE it is reachable ("query/"+op, "analyze/"+name) and WHAT TIER it carries
// (isLabsCapability → mcp.IsStableOperation). Both now come from
// engine/opcatalog, which states them once for every surface.
//
// What does NOT come from the catalog is AVAILABILITY — which capabilities this
// concrete server will actually serve. That is a wiring fact (queryOps from
// engine/query.Operations, the agent-tool seams, and the analyzer list cmd/graphi
// injects through WithDescriptors), and it stays exactly where it was, because
// AC-4 keeps request handling on the legacy path and because /contract must keep
// advertising only what this process can answer. The catalog says what an
// operation IS; the wiring says whether it is here.
//
// The consequence is worth stating plainly: a server built without
// WithDescriptors advertises no analyzer resources, before and after this
// change. A projection that emitted every catalog resource unconditionally would
// have been a widening dressed as a refactor.
//
// # The unclaimed remainder
//
// The HTTP surface exposes eight engine analyzers that have no MCP tool and
// therefore no catalog spec (analyze/metrics, analyze/communities, …). They keep
// the legacy naming and the legacy tier check. That remainder is not hidden: it
// is exactly the allow-list surfaces/opcatalog_shadow_parity_test.go already
// maintains, so a NEW unclaimed resource still forces a decision instead of
// sliding in.

import (
	"fmt"
	"sort"

	"github.com/samibel/graphi/engine/opcatalog"
)

// contractSourceKind names which of the two sources builds the /contract
// resource list.
type contractSourceKind string

const (
	// contractSourceProjected derives the list from engine/opcatalog.
	contractSourceProjected contractSourceKind = "projected"
	// contractSourceLegacy uses the hand-written string arithmetic.
	contractSourceLegacy contractSourceKind = "legacy"
)

// contractSource is the AC-5 rollback switch for the HTTP half. Like the MCP
// one it is deliberately not an environment variable: /contract is a negotiated
// wire document, and two processes on one version must not be able to advertise
// differently.
var contractSource = contractSourceProjected

// contractCatalog is the frozen operation catalog, loaded once.
//
// It panics on failure for the same reason the MCP projection does: shadow.json
// is go:embed'ed, so a catalog that will not load is a defect in the binary, not
// a runtime condition. Falling back to the legacy list would ship a corrupted
// catalog as a green server.
var contractCatalog = mustContractCatalog()

func mustContractCatalog() *opcatalog.Catalog {
	catalog, err := opcatalog.Shadow()
	if err != nil {
		panic(fmt.Sprintf("http: the /contract projection needs the operation catalog: %v", err))
	}
	return catalog
}

// capabilitySource records where one advertised resource's metadata came from.
//
// Two catalog sources exist because the HTTP surface names two different things.
// Query operations, lexical search and the agent tools are addressed by their
// OPERATION ID, so the catalog resolves them directly and supplies both the
// resource path and the tier. The generic analyzers are addressed by their
// ANALYZER name, which is not the operation id (`taint` is served by the
// `analyze_taint` operation), and no catalog field maps one to the other — so
// they are joined on the declared HTTP resource instead, and the catalog
// supplies the tier while the resource path stays the surface's own naming.
// Recording which of the two applied is the point of this field: it is what
// stops "derived from the catalog" from quietly meaning "half of it was".
type capabilitySource string

const (
	// capabilityFromCatalog: resolved by operation id; resource AND tier come
	// from the catalog.
	capabilityFromCatalog capabilitySource = "catalog"
	// capabilityFromCatalogResource: resolved by declared HTTP resource; the
	// tier comes from the catalog.
	capabilityFromCatalogResource capabilitySource = "catalog-by-resource"
	// capabilityFromSurface: no catalog spec exists (an engine analyzer with no
	// MCP tool), so the legacy naming and tier check apply.
	capabilityFromSurface capabilitySource = "surface"
)

// contractCatalogByResource indexes the catalog by declared HTTP resource, the
// join key for capabilities the HTTP surface addresses by analyzer name.
var contractCatalogByResource = func() map[string]opcatalog.OperationSpec {
	out := make(map[string]opcatalog.OperationSpec)
	for _, spec := range contractCatalog.All() {
		if spec.HTTPResource == "" {
			continue
		}
		if owner, dup := out[spec.HTTPResource]; dup {
			panic(fmt.Sprintf("http: operations %q and %q both claim HTTP resource %q",
				owner.ID, spec.ID, spec.HTTPResource))
		}
		out[spec.HTTPResource] = spec
	}
	return out
}()

// capabilityDiagnostic is one row of the projected capability list: the product
// operation, where it is reachable, its tier, whether the shipped default
// profile advertises it, and which of the two sources supplied the metadata.
//
// This is the "diagnostics" half of AC-2. It is the same information
// /contract is built from, kept in a shape a human (and the parity test) can
// read row by row — a bare sorted list of resource strings cannot show that a
// tier came from the catalog rather than from a second opinion in this package.
type capabilityDiagnostic struct {
	Capability string
	Operation  string
	Resource   string
	Labs       bool
	Advertised bool
	Source     capabilitySource
}

// servedCapabilities returns the product-operation names this server can serve,
// in the deterministic order the legacy builder walked them: the structural
// query operations, the lexical search, the agent tools, and the injected
// analyzers. Query operations come out of a map, so they are sorted here — the
// legacy code got away with map order only because it fed a set that was sorted
// at the end, and a diagnostic list has no such second chance.
func (s *Server) servedCapabilities() []string {
	ops := make([]string, 0, len(queryOps))
	for op := range queryOps {
		ops = append(ops, op)
	}
	sort.Strings(ops)

	out := make([]string, 0, len(ops)+1+len(agentToolNames)+len(s.analyzers))
	out = append(out, ops...)
	out = append(out, "search")
	out = append(out, agentToolNames...)
	out = append(out, s.analyzers...)
	return out
}

// legacyResourceFor is the pre-AX-05 naming: query operations live under
// query/, everything else under analyze/, and lexical search is its own
// top-level resource.
func legacyResourceFor(capability string) string {
	if _, isQuery := queryOps[capability]; isQuery {
		return "query/" + capability
	}
	if capability == "search" {
		return "search"
	}
	return "analyze/" + capability
}

// capabilityDiagnostics resolves every served capability to a resource and a
// tier, preferring the catalog and falling back to the surface's own naming for
// the engine analyzers no MCP tool covers.
func (s *Server) capabilityDiagnostics() []capabilityDiagnostic {
	seen := make(map[string]bool)
	out := make([]capabilityDiagnostic, 0, len(queryOps)+1+len(agentToolNames)+len(s.analyzers))
	for _, capability := range s.servedCapabilities() {
		if seen[capability] {
			continue
		}
		seen[capability] = true

		row := capabilityDiagnostic{
			Capability: capability,
			Resource:   legacyResourceFor(capability),
			Labs:       isLabsCapability(capability),
			Source:     capabilityFromSurface,
		}
		if spec, ok := contractCatalog.Lookup(capability); ok {
			row.Source = capabilityFromCatalog
			row.Operation = spec.ID
			row.Labs = spec.Tier != opcatalog.TierStable
			if spec.HTTPResource != "" {
				row.Resource = spec.HTTPResource
			}
			// An empty HTTPResource on a capability this surface serves is a
			// catalog defect, not a reason to drop the resource: the legacy name
			// stands and surfaces/opcatalog_shadow_parity_test.go reports the
			// disagreement. Advertising LESS than the legacy list would be a
			// silent narrowing of a negotiated document.
		} else if spec, ok := contractCatalogByResource[row.Resource]; ok {
			row.Source = capabilityFromCatalogResource
			row.Operation = spec.ID
			row.Labs = spec.Tier != opcatalog.TierStable
		}
		row.Advertised = s.labsEnabled || !row.Labs
		out = append(out, row)
	}
	return out
}

// projectedContractResources is the catalog-derived /contract resource list.
func (s *Server) projectedContractResources() []string {
	set := make(map[string]struct{})
	for _, row := range s.capabilityDiagnostics() {
		if row.Advertised {
			set[row.Resource] = struct{}{}
		}
	}
	resources := make([]string, 0, len(set))
	for resource := range set {
		resources = append(resources, resource)
	}
	sort.Strings(resources)
	return resources
}

// legacyContractResources is the pre-AX-05 list, kept compiled in and switchable
// back (AC-5). It is byte-for-byte the code handleContract used to inline.
func (s *Server) legacyContractResources() []string {
	resourceSet := make(map[string]struct{}, len(queryOps)+1+len(agentToolNames)+len(s.analyzers))
	add := func(operation, resource string) {
		if s.labsEnabled || !isLabsCapability(operation) {
			resourceSet[resource] = struct{}{}
		}
	}
	for op := range queryOps {
		add(op, "query/"+op)
	}
	add("search", "search")
	// EP-020 agent tools are always served (the client seam degrades to the
	// contract "unavailable" outcome when no graph services are wired).
	for _, t := range agentToolNames {
		add(t, "analyze/"+t)
	}
	for _, a := range s.analyzers {
		add(a, "analyze/"+a)
	}
	resources := make([]string, 0, len(resourceSet))
	for resource := range resourceSet {
		resources = append(resources, resource)
	}
	sort.Strings(resources)
	return resources
}

// contractResources returns the /contract resource list from whichever source
// contractSource selects.
func (s *Server) contractResources() []string {
	if contractSource == contractSourceLegacy {
		return s.legacyContractResources()
	}
	return s.projectedContractResources()
}
