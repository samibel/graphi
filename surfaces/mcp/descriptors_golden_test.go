package mcp

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"sort"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/internal/goldenfile"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/daemon"
)

// AX-00 (SW-220) AC-1 — the MCP wire/descriptor freeze.
//
// TestCharacterization_ToolNames_Snapshot already pins the NAME SET the stdio
// surface can advertise. That is necessary and not sufficient for the extension
// kernel strangler refactor: the whole point of AX-03/AX-05 is to DERIVE the
// descriptors from an operation catalog instead of hand-maintaining them, and a
// derivation can reproduce every name while quietly reshaping a description, an
// input schema, a `required` list, or a read-only annotation. A client sees all
// of those.
//
// So this file freezes the FULL advertised metadata — name, tier tag,
// description, inputSchema and annotations — per binding profile, as committed
// bytes. Six profiles are pinned, and the split is deliberate:
//
//   - profile-static (`stable`, `maximal`): what the profile promises before any
//     binding narrows it. This is what tools/list answers from while the first
//     index runs (see descriptors.go: an unbound session serves these), so it is
//     directly observable by a client and must be frozen in its own right.
//   - stdio (`stdio-stable`, `stdio-labs`): a fully wired client.Direct — the
//     shape the shipped `graphi mcp` binding has.
//   - daemon (`daemon-stable`, `daemon-labs`): the DaemonClient allow-list —
//     a genuinely different, narrower catalog (its CapabilityReporter hides every
//     tool without a wired daemon RPC).
//
// A tool added, removed, renamed, re-tiered, re-described or re-schema'd shows
// up here as a reviewed byte diff on a PR. Regeneration is explicit:
//
//	GRAPHI_UPDATE_GOLDEN=1 go test ./surfaces/mcp -run TestAX00
//
// Tier tags come from IsStableMCPTool — the SAME membership check the coverage
// matrix and the HTTP capability gate use — so this golden cannot invent a
// third opinion about what "stable" means.

// goldenDescriptor is the canonical, diff-friendly projection of one advertised
// MCP tool. It is a COPY: the production descriptor maps are handed out by
// toolDescriptors() (and cached on the Server), so the golden must never write
// into them.
type goldenDescriptor struct {
	Name        string         `json:"name"`
	Tier        string         `json:"tier"`
	Description string         `json:"description"`
	InputSchema any            `json:"inputSchema,omitempty"`
	Annotations any            `json:"annotations,omitempty"`
	Extra       map[string]any `json:"extra,omitempty"`
}

// goldenCatalog is the whole frozen artifact for one binding profile.
type goldenCatalog struct {
	Profile   string             `json:"profile"`
	Note      string             `json:"note"`
	ToolCount int                `json:"tool_count"`
	Tools     []goldenDescriptor `json:"tools"`
}

// projectDescriptors converts the live descriptor maps into the golden shape,
// preserving the advertised ORDER (which is itself part of the wire contract a
// client observes) and surfacing any descriptor key this projection does not
// know about under "extra" — so a future field cannot be dropped silently by the
// very test meant to catch drift.
func projectDescriptors(t *testing.T, descriptors []map[string]any) []goldenDescriptor {
	t.Helper()
	out := make([]goldenDescriptor, 0, len(descriptors))
	for _, descriptor := range descriptors {
		name, _ := descriptor["name"].(string)
		if name == "" {
			t.Fatalf("descriptor without a name: %#v", descriptor)
		}
		description, _ := descriptor["description"].(string)
		g := goldenDescriptor{
			Name:        name,
			Tier:        tierTag(name),
			Description: description,
			InputSchema: descriptor["inputSchema"],
			Annotations: descriptor["annotations"],
		}
		for key, value := range descriptor {
			switch key {
			case "name", "description", "inputSchema", "annotations":
				continue
			}
			if g.Extra == nil {
				g.Extra = map[string]any{}
			}
			g.Extra[key] = value
		}
		out = append(out, g)
	}
	return out
}

// tierTag classifies an advertised tool with the SAME membership function the
// coverage matrix and the HTTP gate use. `index` is stable but lifecycle-only
// and never an MCP tool, so IsStableMCPTool is the right check here.
func tierTag(name string) string {
	if IsStableMCPTool(name) {
		return "stable"
	}
	return "labs"
}

// canonicalJSON renders v as stable, human-reviewable bytes: two-space indent, a
// trailing newline, and NO HTML escaping (so a description containing < or & is
// frozen as written rather than as < — the wire carries the raw text).
func canonicalJSON(t *testing.T, v any) []byte {
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

// fullyWiredDirect builds the surface client shape the shipped stdio binding
// has: query + search + analysis over a real (empty) store. An empty graph is
// exactly right here — tools/list is DISCOVERY and must not depend on graph
// content; if a future change made the catalog data-dependent, this golden would
// be the first thing to notice.
func fullyWiredDirect(t *testing.T) *client.Direct {
	t.Helper()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	return client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))
}

func TestAX00_MCPDescriptorCatalogs_Golden(t *testing.T) {
	for _, tc := range []struct {
		profile string
		note    string
		build   func(t *testing.T) []map[string]any
	}{
		{
			profile: "stable",
			note:    "profile-static Stable catalog (stableToolDescriptors): what an UNBOUND stdio session answers tools/list with while the first index runs, before any CapabilityReporter narrowing.",
			build:   func(*testing.T) []map[string]any { return stableToolDescriptors() },
		},
		{
			profile: "maximal",
			note:    "profile-static Stable+Labs catalog (maximalToolDescriptors): the complete registry an unbound `-labs` session advertises. Pure by contract — building it must never dial a daemon, enumerate a forge or execute an analyzer.",
			build:   func(*testing.T) []map[string]any { return maximalToolDescriptors() },
		},
		{
			profile: "stdio-stable",
			note:    "bound stdio default profile: fully wired client.Direct (query+search+analysis), Stable profile.",
			build: func(t *testing.T) []map[string]any {
				return NewServerWithClient(fullyWiredDirect(t)).toolDescriptors()
			},
		},
		{
			profile: "stdio-labs",
			note:    "bound stdio `-labs` profile: fully wired client.Direct, maximal catalog narrowed by Direct.SupportsCapability.",
			build: func(t *testing.T) []map[string]any {
				return NewServerWithClient(fullyWiredDirect(t), WithLabs()).toolDescriptors()
			},
		},
		{
			profile: "daemon-stable",
			note:    "bound daemon profile: DaemonClient's wired-RPC allow-list narrows the Stable catalog. Constructing the client dials nothing.",
			build: func(*testing.T) []map[string]any {
				return NewServerWithClient(daemon.NewClient("/nonexistent.sock", "/nonexistent")).toolDescriptors()
			},
		},
		{
			profile: "daemon-labs",
			note:    "bound daemon `-labs` profile: DaemonClient's allow-list narrows the maximal catalog.",
			build: func(*testing.T) []map[string]any {
				return NewServerWithClient(daemon.NewClient("/nonexistent.sock", "/nonexistent"), WithLabs()).toolDescriptors()
			},
		},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			descriptors := tc.build(t)
			tools := projectDescriptors(t, descriptors)
			artifact := goldenCatalog{
				Profile:   tc.profile,
				Note:      tc.note,
				ToolCount: len(tools),
				Tools:     tools,
			}
			goldenfile.Assert(t, filepath.Join("testdata", "mcp-descriptors-"+tc.profile+".json"), canonicalJSON(t, artifact))
		})
	}
}

// wireNames is the second half of AC-1: the advertised NAME sets and the tier
// tag of each, frozen separately from the descriptor bodies so a rename is
// legible as a rename rather than buried in a 900-line descriptor diff.
type wireNames struct {
	Note              string            `json:"note"`
	StableOperations  []string          `json:"stable_operations"`
	StableMCPTools    []string          `json:"stable_mcp_tools"`
	AllAdvertised     []string          `json:"all_advertised_tool_names"`
	TierByName        map[string]string `json:"tier_by_name"`
	StableOpCount     int               `json:"stable_operation_count"`
	StableToolCount   int               `json:"stable_mcp_tool_count"`
	AdvertisedCount   int               `json:"advertised_tool_count"`
	StrictQueryOpList []string          `json:"strict_query_operations"`
}

func TestAX00_MCPWireNames_Golden(t *testing.T) {
	names := ToolNames()
	tiers := make(map[string]string, len(names))
	for _, name := range names {
		tiers[name] = tierTag(name)
	}
	stableOps := append([]string(nil), StableOperations...)
	sort.Strings(stableOps)

	artifact := wireNames{
		Note:              "AX-00 baseline freeze: every advertised MCP wire name and its tier tag. Tool names are FROZEN wire identifiers (surfaces/mcp/tools.go); the [labs] marker belongs on descriptions, never on names. A diff here is a protocol change.",
		StableOperations:  stableOps,
		StableMCPTools:    StableMCPToolNames(),
		AllAdvertised:     names,
		TierByName:        tiers,
		StableOpCount:     len(stableOps),
		StableToolCount:   len(StableMCPToolNames()),
		AdvertisedCount:   len(names),
		StrictQueryOpList: strictQueryOperations(),
	}
	goldenfile.Assert(t, filepath.Join("testdata", "mcp-wire-names.json"), canonicalJSON(t, artifact))
}

// TestAX00_MCPDescriptorCatalogs_ReproducibleAcrossRuns is the determinism half
// of the freeze: building each catalog twice in one process must yield identical
// bytes. Without it a map-iteration-order or wall-clock leak could make the
// golden pass on the machine that regenerated it and fail everywhere else — the
// classic way a "golden" becomes a flake instead of a gate.
func TestAX00_MCPDescriptorCatalogs_ReproducibleAcrossRuns(t *testing.T) {
	builders := map[string]func(t *testing.T) []map[string]any{
		"stable":  func(*testing.T) []map[string]any { return stableToolDescriptors() },
		"maximal": func(*testing.T) []map[string]any { return maximalToolDescriptors() },
		"stdio-stable": func(t *testing.T) []map[string]any {
			return NewServerWithClient(fullyWiredDirect(t)).toolDescriptors()
		},
		"stdio-labs": func(t *testing.T) []map[string]any {
			return NewServerWithClient(fullyWiredDirect(t), WithLabs()).toolDescriptors()
		},
		"daemon-stable": func(*testing.T) []map[string]any {
			return NewServerWithClient(daemon.NewClient("/nonexistent.sock", "/nonexistent")).toolDescriptors()
		},
	}
	for profile, build := range builders {
		first := canonicalJSON(t, projectDescriptors(t, build(t)))
		second := canonicalJSON(t, projectDescriptors(t, build(t)))
		if !bytes.Equal(first, second) {
			t.Errorf("profile %q descriptor catalog is not reproducible within one process:\n first =%s\n second=%s", profile, first, second)
		}
	}
}
