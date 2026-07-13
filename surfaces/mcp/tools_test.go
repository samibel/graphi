package mcp

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/surfaces/client"
)

// allToolsClient is a fake client.Client whose every capability probe succeeds,
// so the MCP server advertises the MAXIMAL tool set. It lets the no-drift test
// assert that what toolDescriptors() actually advertises equals the canonical
// ToolNames() registry — the structural guarantee that the FU-4 coverage matrix
// source cannot silently diverge from the served tools.
type allToolsClient struct{}

func (allToolsClient) Query(context.Context, string, string, int) ([]byte, error) {
	return []byte("{}"), nil
}
func (allToolsClient) Compound(context.Context, string) ([]byte, error) {
	return []byte("{}"), nil
}
func (allToolsClient) Search(context.Context, string, int) ([]byte, error) {
	return []byte("{}"), nil
}
func (allToolsClient) SemanticSearch(context.Context, string, int) ([]byte, error) {
	return []byte("{}"), nil
}
func (allToolsClient) Savings(context.Context) ([]byte, error) { return []byte("{}"), nil }
func (allToolsClient) Analyze(context.Context, client.AnalyzeParams) ([]byte, error) {
	return []byte("{}"), nil
}
func (allToolsClient) RefactorPreview(context.Context, client.RefactorRequest) ([]byte, error) {
	return []byte("{}"), nil
}
func (allToolsClient) Refactor(context.Context, client.RefactorRequest, string) ([]byte, error) {
	return []byte("{}"), nil
}
func (allToolsClient) Undo(context.Context, string, string) ([]byte, error) {
	return []byte("{}"), nil
}
func (allToolsClient) PrComment(context.Context, client.PrCommentRequest) ([]byte, error) {
	return []byte("{}"), nil
}
func (allToolsClient) Memory(context.Context, client.MemoryRequest) ([]byte, error) {
	return []byte("{}"), nil
}
func (allToolsClient) Distill(context.Context, client.DistillRequest) ([]byte, error) {
	return []byte("{}"), nil
}
func (allToolsClient) SkillGen(context.Context, client.SkillGenRequest) ([]byte, error) {
	return []byte("{}"), nil
}
func (allToolsClient) Brief(context.Context, string) ([]byte, []byte, error) {
	return []byte("{}"), []byte("# Agent Brief\n"), nil
}
func (allToolsClient) ExplainSymbol(context.Context, string, int) ([]byte, error) {
	return []byte("{}"), nil
}
func (allToolsClient) RelatedFiles(context.Context, string, string, int) ([]byte, error) {
	return []byte("{}"), nil
}
func (allToolsClient) ChangeRisk(context.Context, string, string, int) ([]byte, error) {
	return []byte("{}"), nil
}
func (allToolsClient) Diagnose(context.Context, []string, client.DiagnoseOptions) ([]byte, error) {
	return []byte("{}"), nil
}
func (allToolsClient) Inline(context.Context, client.InlineRequest) ([]byte, error) {
	return []byte("{}"), nil
}
func (allToolsClient) SafeDelete(context.Context, client.SafeDeleteRequest) ([]byte, error) {
	return []byte("{}"), nil
}
func (allToolsClient) SearchAST(context.Context, string, int) ([]byte, error) {
	return []byte("{}"), nil
}
func (allToolsClient) FindClones(context.Context, string) ([]byte, error) {
	return []byte("{}"), nil
}
func (allToolsClient) ListPRs(context.Context) ([]byte, error)      { return []byte("{}"), nil }
func (allToolsClient) TriagePRs(context.Context) ([]byte, error)    { return []byte("{}"), nil }
func (allToolsClient) ConflictsPRs(context.Context) ([]byte, error) { return []byte("{}"), nil }
func (allToolsClient) SuggestReviewers(context.Context, string) ([]byte, error) {
	return []byte("{}"), nil
}
func (allToolsClient) CompareBranches(context.Context, string, string) ([]byte, error) {
	return []byte("{}"), nil
}
func (allToolsClient) CritiqueReview(context.Context, int, string, string) ([]byte, error) {
	return []byte("{}"), nil
}

// TestToolNames_MatchesAdvertisedMaximalSet is the in-package drift guard for the
// MCP surface: ToolNames() (the single source the FU-4 coverage matrix reads)
// MUST equal the set of names a fully-capable server actually advertises via
// tools/list. If a tool is added to toolDescriptors() but not to the ToolNames()
// source (or vice-versa), this fails — keeping the registry honest.
func TestToolNames_MatchesAdvertisedMaximalSet(t *testing.T) {
	s := NewServerWithClient(allToolsClient{})
	descriptors := s.toolDescriptors()

	advertised := make([]string, 0, len(descriptors))
	for _, d := range descriptors {
		name, ok := d["name"].(string)
		if !ok || name == "" {
			t.Fatalf("descriptor without a string name: %#v", d)
		}
		advertised = append(advertised, name)
	}
	sort.Strings(advertised)

	got := ToolNames()
	if !reflect.DeepEqual(advertised, got) {
		t.Errorf("advertised tools (maximal) != ToolNames()\n advertised: %v\n ToolNames:  %v", advertised, got)
	}
}

// TestLabsMarking asserts the Stable/Labs stability tier (SCOPE-01) is reflected
// in the advertised MCP descriptors: every advertised tool that is NOT one of the
// 12 frozen stable operations carries the [labs] prefix, and every stable tool's
// description does not. This is the MCP half of "the taxonomy is visible in
// user-facing output".
func TestLabsMarking(t *testing.T) {
	s := NewServerWithClient(allToolsClient{})
	descriptors := s.toolDescriptors()

	sawStable := false
	sawLabs := false
	for _, d := range descriptors {
		name, _ := d["name"].(string)
		desc, _ := d["description"].(string)
		hasPrefix := strings.HasPrefix(desc, labsPrefix)
		switch {
		case IsStableOperation(name):
			sawStable = true
			if hasPrefix {
				t.Errorf("stable operation %q advertised WITH the %q labs prefix", name, labsPrefix)
			}
		default:
			sawLabs = true
			if !hasPrefix {
				t.Errorf("labs tool %q advertised without the %q description prefix", name, labsPrefix)
			}
		}
	}
	if !sawStable || !sawLabs {
		t.Fatalf("expected both stable and labs tools in the maximal descriptor set (stable=%v labs=%v)", sawStable, sawLabs)
	}
}

// TestStableOperations_FrozenTwelve pins the SCOPE-01 stable set: exactly the 12
// named operations, sorted, and every one that is an MCP tool name is actually
// advertised by the maximal server (so the marker set cannot silently drift from
// the served surface).
func TestStableOperations_FrozenTwelve(t *testing.T) {
	want := []string{
		"agent_brief", "callees", "callers", "change_risk", "definition",
		"explain_symbol", "impact", "index", "neighborhood", "references",
		"related_files", "search",
	}
	if !reflect.DeepEqual(StableOperations, want) {
		t.Fatalf("StableOperations drifted (SCOPE-01 freezes exactly 12):\n got  = %#v\n want = %#v", StableOperations, want)
	}
	if !sort.StringsAreSorted(StableOperations) {
		t.Errorf("StableOperations must stay sorted: %v", StableOperations)
	}

	toolSet := map[string]bool{}
	for _, n := range ToolNames() {
		toolSet[n] = true
	}
	for _, op := range StableOperations {
		// index and impact are not MCP tool names (ingest verb / analyzer); the
		// other ten must be advertised tools.
		if op == "index" || op == "impact" {
			continue
		}
		if !toolSet[op] {
			t.Errorf("stable operation %q is expected to be an advertised MCP tool but is not in ToolNames()", op)
		}
	}
}

// TestToolNames_SortedAndDeduped asserts the canonical list is deterministic.
func TestToolNames_SortedAndDeduped(t *testing.T) {
	names := ToolNames()
	if !sort.StringsAreSorted(names) {
		t.Errorf("ToolNames() not sorted: %v", names)
	}
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			t.Errorf("ToolNames() has duplicate %q", n)
		}
		seen[n] = true
	}
	if len(names) == 0 {
		t.Fatal("ToolNames() returned empty set")
	}
}
