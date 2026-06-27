package mcp

import (
	"context"
	"reflect"
	"sort"
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
func (allToolsClient) Diagnose(context.Context, []string) ([]byte, error) {
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
