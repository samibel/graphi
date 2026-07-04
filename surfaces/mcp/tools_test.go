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

// TestExperimentalMarking asserts the central experimental set is reflected in
// the advertised descriptors — every experimental tool's description carries
// the prefix, and no core tool's does — and that every set member is a real
// canonical tool name (no stale entries).
func TestExperimentalMarking(t *testing.T) {
	s := NewServerWithClient(allToolsClient{})
	descriptors := s.toolDescriptors()

	known := map[string]bool{}
	for _, n := range ToolNames() {
		known[n] = true
	}
	for name := range experimentalTools {
		if !known[name] {
			t.Errorf("experimentalTools entry %q is not a canonical tool name (stale entry)", name)
		}
	}

	seen := map[string]bool{}
	for _, d := range descriptors {
		name, _ := d["name"].(string)
		desc, _ := d["description"].(string)
		seen[name] = true
		hasPrefix := len(desc) >= len(experimentalPrefix) && desc[:len(experimentalPrefix)] == experimentalPrefix
		switch {
		case experimentalTools[name] && !hasPrefix:
			t.Errorf("experimental tool %q advertised without the %q description prefix", name, experimentalPrefix)
		case !experimentalTools[name] && hasPrefix:
			t.Errorf("core tool %q advertised WITH the experimental prefix", name)
		}
	}
	for name := range experimentalTools {
		if !seen[name] {
			t.Errorf("experimental tool %q not advertised by the maximal server (probe wiring changed?)", name)
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
