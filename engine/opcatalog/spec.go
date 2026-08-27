// Package opcatalog is graphi's canonical operation catalog (SW-223 / AX-03).
//
// # What it is
//
// An OperationSpec states, once, everything graphi's surfaces need to know
// ABOUT an operation without knowing how to run it: its id, contract version,
// stability tier, human/agent-facing description, JSON input schema, the host
// ports it requires, the permissions those ports imply, and its determinism
// class. The Catalog is the immutable, canonically ordered set of those specs.
//
// # Shadow mode — this package changes no behaviour
//
// AX-03 populates the catalog by MIRRORING what the legacy builders already
// advertise. Nothing dispatches from it and no descriptor is projected out of
// it yet: surfaces/mcp/descriptors.go, surfaces/mcp/toolcalls.go, the
// surfaces/http operation list and surfaces/client's capability reporting stay
// the source of truth for every real request. What makes the mirror worth
// having before it is wired is the parity test
// (surfaces/mcp/opcatalog_parity_test.go and
// surfaces/opcatalog_http_parity_test.go): a missing, extra, duplicate or
// differently-tiered entry fails the ordinary test gate. Deleting this package
// and those two test files restores the tree exactly — there is nothing else to
// unwind.
//
// # Metadata, never bytes
//
// The catalog carries what an operation IS. Canonical result serialization
// stays in engine/handlers and the engine services; no byte an MCP or HTTP
// client observes is produced here.
//
// # Layering
//
// This package sits at ENGINE rank and depends only on the standard library and
// core/registry, so every surface may import it under the cmd → surfaces →
// engine → core guard.
//
// # Vocabulary provenance
//
// ADR 0013 bounds WHAT an extension may be trusted with but deliberately
// declines to name the host ports ("The extension host-port surface — which
// ports exist, their signatures, and their cost contracts. SW-230."). The Port
// and Permission vocabularies below are therefore this package's proposal, not
// a quotation, and they are named after the seams that exist today
// (surfaces/client.Direct's SupportsCapability service map and the Direct
// method bodies) rather than invented. SW-230 owns the final surface. Two ADR
// 0013 rules are already binding here: an extension never mints confidence, so
// no spec field expresses a provenance tier (D5/I5), and no spec may claim a
// Stable id it does not already own (I1) — enforced by the parity test against
// mcp.StableOperations, which stays the single source of the taxonomy.
package opcatalog

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Tier is the machine stability tier of an operation. It mirrors the coverage
// matrix's closed vocabulary; `disabled` has no advertised operation and so
// never appears in the catalog.
type Tier string

const (
	// TierStable marks one of the twelve frozen SCOPE-01 operations.
	TierStable Tier = "stable"
	// TierLabs marks everything outside the frozen twelve.
	TierLabs Tier = "labs"
)

// Valid reports whether t is a known tier.
func (t Tier) Valid() bool { return t == TierStable || t == TierLabs }

// Port names one host capability seam an operation needs in order to execute.
// The values are named after the real seams in surfaces/client.Direct, because
// a port list derived from the wiring is evidence and a port list derived from
// a category is a guess.
type Port string

const (
	// PortGraphQuery is engine/query's structural read service.
	PortGraphQuery Port = "graph.query"
	// PortGraphSearch is engine/search's lexical/symbol read service.
	PortGraphSearch Port = "graph.search"
	// PortGraphAnalysis is engine/analysis's analyzer dispatch.
	PortGraphAnalysis Port = "graph.analysis"
	// PortGraphStore is a direct READ-ONLY open of the repository's durable
	// auto-managed store, independent of the wired query service (the trust
	// surface's ADR 0006 observer discipline).
	PortGraphStore Port = "graph.store"
	// PortEmbedVector is the optional embedding/vector store behind semantic
	// search.
	PortEmbedVector Port = "embed.vector"
	// PortBranchState materializes read-only graph states from two branch refs
	// ABOVE the surface boundary.
	PortBranchState Port = "branch.materialize"
	// PortSourceRead reads working-tree source bytes.
	PortSourceRead Port = "source.read"
	// PortSourceWrite writes working-tree source bytes.
	PortSourceWrite Port = "source.write"
	// PortEditJournal is the durable change/undo record behind the edit path.
	PortEditJournal Port = "edit.journal"
	// PortGitHistory reads local git history.
	PortGitHistory Port = "git.history"
	// PortMemoryRead reads the agent-memory store.
	PortMemoryRead Port = "memory.read"
	// PortMemoryWrite writes the agent-memory store.
	PortMemoryWrite Port = "memory.write"
	// PortLedgerRead reads the durable token-savings ledger.
	PortLedgerRead Port = "ledger.read"
	// PortForgeEnumerate lists pull requests through the read-only forge
	// boundary. This is outbound.
	PortForgeEnumerate Port = "forge.enumerate"
	// PortForgeReview fetches an existing review through the forge boundary.
	// This is outbound.
	PortForgeReview Port = "forge.review"
	// PortForgePublish upserts a comment through the forge host boundary. This
	// is outbound, and conditional on an explicit publish request.
	PortForgePublish Port = "forge.publish"
	// PortDistillEngine is engine/distill's deterministic transform.
	PortDistillEngine Port = "distill.engine"
	// PortSkillGenEngine is engine/skillgen's deterministic transform.
	PortSkillGenEngine Port = "skillgen.engine"

	// PortsUnaudited is the honest marker for a spec whose port set has NOT
	// been established from its handler. It is deliberately a Port value rather
	// than an absent field: an unaudited entry has to be visible in the data,
	// not inferable from a gap. It may never be combined with a real port —
	// "half audited" is the state this marker exists to forbid.
	PortsUnaudited Port = "ports_unaudited"
)

// Permission is the coarse, trust-relevant grant an operation needs. It is a
// strict function of the port set (see PermissionsFor), and the spec declares
// it anyway so that a hand edit contradicting the ports fails Validate instead
// of shipping. That checked redundancy is the same discipline the coverage
// matrix applies to the live registries.
type Permission string

const (
	// PermissionGraphRead reads the committed code graph.
	PermissionGraphRead Permission = "graph.read"
	// PermissionSourceRead reads working-tree source bytes.
	PermissionSourceRead Permission = "source.read"
	// PermissionSourceWrite writes working-tree source bytes.
	PermissionSourceWrite Permission = "source.write"
	// PermissionHistoryRead reads local version-control history.
	PermissionHistoryRead Permission = "history.read"
	// PermissionStateRead reads graphi-managed durable state beside the graph.
	PermissionStateRead Permission = "state.read"
	// PermissionStateWrite writes graphi-managed durable state beside the graph.
	PermissionStateWrite Permission = "state.write"
	// PermissionNetworkEgress leaves the machine. ADR 0013 T7 scopes graphi's
	// zero-egress promise to the default binary in the default path; an
	// operation carrying this permission is outside that scope by design.
	PermissionNetworkEgress Permission = "network.egress"

	// PermissionsUnaudited pairs with PortsUnaudited and is never valid beside
	// a real permission.
	PermissionsUnaudited Permission = "permissions_unaudited"
)

// Determinism classifies how far an operation's output is reproducible.
type Determinism string

const (
	// DeterminismDeterministic: the output is a pure function of the committed
	// graph and the request. Same graph, same request, same bytes.
	DeterminismDeterministic Determinism = "deterministic"
	// DeterminismEnvironmentDependent: the output additionally depends on state
	// outside the committed graph that graphi can still read locally — the
	// working tree, git history, or graphi's own durable sidecars (memory
	// store, savings ledger, undo journal, trust snapshot).
	DeterminismEnvironmentDependent Determinism = "environment-dependent"
	// DeterminismExternal: the output additionally depends on a remote host.
	DeterminismExternal Determinism = "external"
	// DeterminismUnaudited is the honest marker for a class not yet
	// established. Like PortsUnaudited it is a value, not an absence.
	DeterminismUnaudited Determinism = "determinism_unaudited"
)

// Valid reports whether d is a known determinism class.
func (d Determinism) Valid() bool {
	switch d {
	case DeterminismDeterministic, DeterminismEnvironmentDependent,
		DeterminismExternal, DeterminismUnaudited:
		return true
	}
	return false
}

// portPermissions is the closed port → permission derivation. A port missing
// from this table is not a valid port, which is what keeps the vocabulary from
// growing by accident.
var portPermissions = map[Port][]Permission{
	PortGraphQuery:     {PermissionGraphRead},
	PortGraphSearch:    {PermissionGraphRead},
	PortGraphAnalysis:  {PermissionGraphRead},
	PortGraphStore:     {PermissionGraphRead},
	PortEmbedVector:    {PermissionGraphRead},
	PortBranchState:    {PermissionGraphRead, PermissionHistoryRead},
	PortSourceRead:     {PermissionSourceRead},
	PortSourceWrite:    {PermissionSourceWrite},
	PortEditJournal:    {PermissionStateRead, PermissionStateWrite},
	PortGitHistory:     {PermissionHistoryRead},
	PortMemoryRead:     {PermissionStateRead},
	PortMemoryWrite:    {PermissionStateWrite},
	PortLedgerRead:     {PermissionStateRead},
	PortForgeEnumerate: {PermissionNetworkEgress},
	PortForgeReview:    {PermissionNetworkEgress},
	PortForgePublish:   {PermissionNetworkEgress},
	PortDistillEngine:  {},
	PortSkillGenEngine: {},
	PortsUnaudited:     {PermissionsUnaudited},
}

// externalPorts leave the machine.
var externalPorts = map[Port]bool{
	PortForgeEnumerate: true,
	PortForgeReview:    true,
	PortForgePublish:   true,
}

// nonDeterministicPorts read state outside the committed graph, so an operation
// requiring one cannot be DeterminismDeterministic.
var nonDeterministicPorts = map[Port]bool{
	PortGraphStore:  true,
	PortBranchState: true,
	PortSourceRead:  true,
	PortSourceWrite: true,
	PortEditJournal: true,
	PortGitHistory:  true,
	PortMemoryRead:  true,
	PortMemoryWrite: true,
	PortLedgerRead:  true,
}

// ValidPort reports whether p is a member of the closed port vocabulary.
func ValidPort(p Port) bool { _, ok := portPermissions[p]; return ok }

// PermissionsFor derives the sorted, de-duplicated permission set implied by a
// port set. It is the single derivation: OperationSpec.Permissions is validated
// against it rather than trusted.
func PermissionsFor(ports []Port) []Permission {
	seen := map[Permission]bool{}
	for _, port := range ports {
		for _, perm := range portPermissions[port] {
			seen[perm] = true
		}
	}
	out := make([]Permission, 0, len(seen))
	for perm := range seen {
		out = append(out, perm)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Advertisement is how one operation is presented to a client: the description
// a human or agent reads and the JSON Schema its arguments are validated
// against, plus the optional MCP tool annotations.
//
// Description carries NO tier marker. The `[labs] ` prefix an MCP client sees
// is a projection of OperationSpec.Tier applied at advertisement time
// (surfaces/mcp.markLabs); storing the projected form here would make the tier
// two facts that can disagree.
//
// InputSchema and Annotations are decoded JSON (map/slice/scalar), not raw
// bytes, so comparison is structural: encoding/json sorts map keys, which makes
// CanonicalJSON a stable byte form regardless of how the value was written.
type Advertisement struct {
	Description string `json:"description"`
	InputSchema any    `json:"input_schema,omitempty"`
	Annotations any    `json:"annotations,omitempty"`
}

// CanonicalJSON renders any decoded JSON value as stable bytes. It is the
// comparison form the parity tests use against the live descriptors.
func CanonicalJSON(v any) ([]byte, error) {
	if v == nil {
		return []byte("null"), nil
	}
	return json.Marshal(v)
}

// OperationSpec is the canonical statement of one advertised operation.
type OperationSpec struct {
	// ID is the wire-visible operation name. It is a frozen identifier;
	// changing one is a protocol change.
	ID string `json:"id"`

	// Version is the operation's contract version. Every operation graphi has
	// ever shipped is at "1": nothing has been versioned yet, and recording the
	// field now is what lets a later revision be expressed without a schema
	// change.
	Version string `json:"version"`

	// Tier is the machine stability tier.
	Tier Tier `json:"tier"`

	// Advertisement is THE presentation, for every binding profile.
	//
	// It used to be only half of one. SW-223 recorded that the Stable MCP
	// profile advertised ten of its eleven tools with a different description,
	// input schema or annotation set than the maximal (Stable+Labs) registry
	// did, and carried the second form alongside this one in a
	// `StableProfileAdvertisement` field. SW-241 (AX-12) collapsed that: the
	// maximal profile adopted the Stable-profile form, the shipped default
	// profile did not move a byte, and the second field was removed rather than
	// left behind as a vestigial always-equal fact. A spec now states what every
	// profile advertises, and surfaces/mcp's projection has nothing to choose
	// between.
	Advertisement

	// Ports are the host seams the operation requires, sorted.
	Ports []Port `json:"ports"`

	// Permissions are the grants the ports imply, sorted. Validate re-derives
	// them and rejects any disagreement.
	Permissions []Permission `json:"permissions"`

	// Determinism is the reproducibility class.
	Determinism Determinism `json:"determinism"`

	// HTTPResource is the surfaces/http `/contract` resource this operation is
	// reachable at, or "" when the HTTP surface does not expose it.
	HTTPResource string `json:"http_resource,omitempty"`

	// PortsEvidence names where the port set was read from. AC-5 requires the
	// ports to be reviewed rather than guessed; an unciteable claim is not a
	// review, so this field is mandatory and is checked non-empty.
	PortsEvidence string `json:"ports_evidence"`
}

// PortsAudited reports whether this spec's ports were actually established.
func (s OperationSpec) PortsAudited() bool {
	for _, p := range s.Ports {
		if p == PortsUnaudited {
			return false
		}
	}
	return true
}

// Validate checks one spec in isolation. Cross-spec obligations (duplicate ids,
// canonical ordering) belong to the Catalog.
func (s OperationSpec) Validate() error {
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf("opcatalog: operation spec with an empty id")
	}
	if strings.TrimSpace(s.Version) == "" {
		return fmt.Errorf("opcatalog: %q: empty version", s.ID)
	}
	if !s.Tier.Valid() {
		return fmt.Errorf("opcatalog: %q: unknown tier %q", s.ID, s.Tier)
	}
	if strings.TrimSpace(s.Description) == "" {
		return fmt.Errorf("opcatalog: %q: empty description", s.ID)
	}
	if strings.HasPrefix(s.Description, "[labs] ") {
		return fmt.Errorf("opcatalog: %q: description carries the [labs] marker; "+
			"the marker is a projection of tier and must not be stored", s.ID)
	}
	if strings.TrimSpace(s.PortsEvidence) == "" {
		return fmt.Errorf("opcatalog: %q: no ports evidence — AC-5 requires the port "+
			"set to be reviewed and citeable, not plausible", s.ID)
	}
	if err := s.validatePorts(); err != nil {
		return err
	}
	if err := s.validatePermissions(); err != nil {
		return err
	}
	if err := s.validateDeterminism(); err != nil {
		return err
	}
	return nil
}

func (s OperationSpec) validatePorts() error {
	if len(s.Ports) == 0 {
		return fmt.Errorf("opcatalog: %q: no ports declared; use %q when the set is "+
			"not established", s.ID, PortsUnaudited)
	}
	for i, port := range s.Ports {
		if !ValidPort(port) {
			return fmt.Errorf("opcatalog: %q: unknown port %q", s.ID, port)
		}
		if i > 0 && s.Ports[i-1] >= port {
			return fmt.Errorf("opcatalog: %q: ports are not sorted and unique at %q", s.ID, port)
		}
		if port == PortsUnaudited && len(s.Ports) != 1 {
			return fmt.Errorf("opcatalog: %q: %q must stand alone — a partially audited "+
				"port set reads as a complete one", s.ID, PortsUnaudited)
		}
	}
	return nil
}

func (s OperationSpec) validatePermissions() error {
	want := PermissionsFor(s.Ports)
	if len(want) != len(s.Permissions) {
		return fmt.Errorf("opcatalog: %q: permissions %v contradict ports %v (derived %v)",
			s.ID, s.Permissions, s.Ports, want)
	}
	for i, perm := range s.Permissions {
		if perm != want[i] {
			return fmt.Errorf("opcatalog: %q: permissions %v contradict ports %v (derived %v)",
				s.ID, s.Permissions, s.Ports, want)
		}
	}
	return nil
}

func (s OperationSpec) validateDeterminism() error {
	if !s.Determinism.Valid() {
		return fmt.Errorf("opcatalog: %q: unknown determinism class %q", s.ID, s.Determinism)
	}
	if !s.PortsAudited() {
		if s.Determinism != DeterminismUnaudited {
			return fmt.Errorf("opcatalog: %q: ports are unaudited, so the determinism class "+
				"cannot be %q", s.ID, s.Determinism)
		}
		return nil
	}
	var external, impure bool
	for _, port := range s.Ports {
		external = external || externalPorts[port]
		impure = impure || nonDeterministicPorts[port]
	}
	switch {
	case external && s.Determinism != DeterminismExternal:
		return fmt.Errorf("opcatalog: %q: declares an outbound port but class %q; "+
			"want %q", s.ID, s.Determinism, DeterminismExternal)
	case !external && s.Determinism == DeterminismExternal:
		return fmt.Errorf("opcatalog: %q: class %q without any outbound port",
			s.ID, DeterminismExternal)
	case !external && impure && s.Determinism == DeterminismDeterministic:
		return fmt.Errorf("opcatalog: %q: reads state outside the committed graph but "+
			"claims %q", s.ID, DeterminismDeterministic)
	case !external && !impure && s.Determinism == DeterminismEnvironmentDependent:
		return fmt.Errorf("opcatalog: %q: claims %q but every port is a committed-graph "+
			"read", s.ID, DeterminismEnvironmentDependent)
	}
	return nil
}
