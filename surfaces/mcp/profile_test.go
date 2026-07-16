package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/client"
)

type profileClient struct {
	allToolsClient
	analyzeCalls      []client.AnalyzeParams
	prCommentCalls    int
	reviewUnavailable bool
}

func (c *profileClient) Analyze(_ context.Context, params client.AnalyzeParams) ([]byte, error) {
	c.analyzeCalls = append(c.analyzeCalls, params)
	return []byte(`{"analyzer":"` + params.Name + `"}`), nil
}

func (c *profileClient) PrComment(context.Context, client.PrCommentRequest) ([]byte, error) {
	c.prCommentCalls++
	if c.reviewUnavailable {
		return nil, client.ErrReviewUnavailable
	}
	return []byte("{}"), nil
}

func (c *profileClient) SupportsCapability(name string) bool {
	return !(name == ToolPrComment && c.reviewUnavailable)
}

type catalogCallSpy struct {
	allToolsClient
	calls []string
}

func (c *catalogCallSpy) called(name string) ([]byte, error) {
	c.calls = append(c.calls, name)
	return []byte("{}"), nil
}

func (c *catalogCallSpy) Search(context.Context, string, int) ([]byte, error) {
	return c.called("Search")
}

func (c *catalogCallSpy) Savings(context.Context) ([]byte, error) {
	return c.called("Savings")
}

func (c *catalogCallSpy) Analyze(context.Context, client.AnalyzeParams) ([]byte, error) {
	return c.called("Analyze")
}

func (c *catalogCallSpy) RefactorPreview(context.Context, client.RefactorRequest) ([]byte, error) {
	return c.called("RefactorPreview")
}

func (c *catalogCallSpy) PrComment(context.Context, client.PrCommentRequest) ([]byte, error) {
	return c.called("PrComment")
}

func (c *catalogCallSpy) Memory(context.Context, client.MemoryRequest) ([]byte, error) {
	return c.called("Memory")
}

func (c *catalogCallSpy) Distill(context.Context, client.DistillRequest) ([]byte, error) {
	return c.called("Distill")
}

func (c *catalogCallSpy) SkillGen(context.Context, client.SkillGenRequest) ([]byte, error) {
	return c.called("SkillGen")
}

func (c *catalogCallSpy) ListPRs(context.Context) ([]byte, error) {
	return c.called("ListPRs")
}

func (c *catalogCallSpy) SuggestReviewers(context.Context, string) ([]byte, error) {
	return c.called("SuggestReviewers")
}

func (c *catalogCallSpy) CompareBranches(context.Context, string, string) ([]byte, error) {
	return c.called("CompareBranches")
}

func (c *catalogCallSpy) CritiqueReview(context.Context, int, string, string) ([]byte, error) {
	return c.called("CritiqueReview")
}

type capabilityProfileClient struct {
	allToolsClient
	unsupported     map[string]bool
	agentBriefCalls int
}

func (c *capabilityProfileClient) SupportsCapability(name string) bool {
	return !c.unsupported[name]
}

func (c *capabilityProfileClient) Brief(context.Context, string) ([]byte, []byte, error) {
	c.agentBriefCalls++
	return []byte("{}"), []byte("# Agent Brief\n"), nil
}

func TestDefaultProfile_AdvertisesExactlyStableMCPTools(t *testing.T) {
	server := NewServerWithClient(allToolsClient{})
	got := descriptorNames(server.toolDescriptors())
	want := StableMCPToolNames()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("default MCP catalog != StableOperations minus index:\n got: %v\nwant: %v", got, want)
	}
	if containsTool(got, ToolAnalyze) || containsTool(got, ToolSearchSemantic) {
		t.Fatalf("default profile leaked Labs tools: %v", got)
	}
}

func TestDirectProfile_HidesUnwiredSearchAndImpact(t *testing.T) {
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })

	for _, tc := range []struct {
		name string
		opts []ServerOption
	}{
		{name: "stable"},
		{name: "labs", opts: []ServerOption{WithLabs()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := NewServer(query.New(store), nil, tc.opts...)
			got := descriptorNames(server.toolDescriptors())
			for _, unavailable := range []string{ToolSearch, ToolImpact} {
				if containsTool(got, unavailable) {
					t.Errorf("Direct without optional service advertised %q: %v", unavailable, got)
				}
			}
		})
	}

	server := NewServer(query.New(store), nil)
	got := descriptorNames(server.toolDescriptors())
	want := []string{
		ToolAgentBrief, query.OpCallees, query.OpCallers, ToolChangeRisk,
		query.OpDefinition, ToolExplainSymbol, query.OpNeighborhood,
		query.OpReferences, ToolRelatedFiles,
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("partially wired Direct Stable catalog mismatch:\n got: %v\nwant: %v", got, want)
	}
}

func TestDirectProfile_FullyWiredStableCatalogRemainsElevenTools(t *testing.T) {
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	direct := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))
	server := NewServerWithClient(direct)
	if got, want := descriptorNames(server.toolDescriptors()), StableMCPToolNames(); !reflect.DeepEqual(got, want) {
		t.Fatalf("fully wired Direct lost Stable tools:\n got: %v\nwant: %v", got, want)
	}
}

func TestDefaultProfile_FiltersCapabilitiesUnavailableOnBoundClient(t *testing.T) {
	c := &capabilityProfileClient{unsupported: map[string]bool{
		ToolAgentBrief:    true,
		ToolExplainSymbol: true,
		ToolRelatedFiles:  true,
		ToolChangeRisk:    true,
	}}
	server := NewServerWithClient(c)

	got := descriptorNames(server.toolDescriptors())
	want := []string{"callees", "callers", "definition", "impact", "neighborhood", "references", "search"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("capability-aware default catalog mismatch:\n got: %v\nwant: %v", got, want)
	}

	response := invokeTool(t, server, ToolAgentBrief, map[string]any{"symbol": "x"})
	if response.Error == nil || !strings.Contains(response.Error.Message, "not available") {
		t.Fatalf("capability-hidden Stable invocation must fail closed: %+v", response)
	}
	if c.agentBriefCalls != 0 {
		t.Fatalf("capability-hidden Stable tool reached client: %d calls", c.agentBriefCalls)
	}
}

func TestLabsProfile_AlsoFiltersBoundClientCapabilities(t *testing.T) {
	c := &capabilityProfileClient{unsupported: map[string]bool{
		ToolAgentBrief:    true,
		ToolExplainSymbol: true,
		ToolRelatedFiles:  true,
		ToolChangeRisk:    true,
	}}
	server := NewServerWithClient(c, WithLabs())
	got := descriptorNames(server.toolDescriptors())
	for name := range c.unsupported {
		if containsTool(got, name) {
			t.Errorf("Labs catalog advertised unsupported bound capability %q", name)
		}
	}
	for _, want := range []string{ToolImpact, ToolAnalyze, ToolSearchSemantic, ToolCompound} {
		if !containsTool(got, want) {
			t.Errorf("Labs catalog lost supported capability %q", want)
		}
	}
}

func TestCapabilityCatalog_RecomputedWhenBinderRebinds(t *testing.T) {
	limited := &capabilityProfileClient{unsupported: map[string]bool{ToolAgentBrief: true}}
	full := &capabilityProfileClient{unsupported: map[string]bool{}}
	bindCalls := 0
	server := NewServerWithBinder(func(context.Context, []string) (Binding, error) {
		bindCalls++
		if bindCalls == 1 {
			return Binding{Client: limited}, nil
		}
		return Binding{Client: full}, nil
	})
	defer server.Close()

	if err := server.bind(context.Background(), []string{"first"}); err != nil {
		t.Fatal(err)
	}
	if containsTool(descriptorNames(server.toolDescriptors()), ToolAgentBrief) {
		t.Fatal("first binding advertised its unsupported agent_brief capability")
	}
	if err := server.bind(context.Background(), []string{"second"}); err != nil {
		t.Fatal(err)
	}
	if !containsTool(descriptorNames(server.toolDescriptors()), ToolAgentBrief) {
		t.Fatal("catalog cache was not recomputed for fully capable replacement binding")
	}
}

func TestLabsProfile_AdvertisesSeparateMaximalCatalog(t *testing.T) {
	server := NewServerWithClient(allToolsClient{}, WithLabs())
	got := descriptorNames(server.toolDescriptors())
	if want := ToolNames(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Labs catalog != maximal ToolNames registry:\n got: %v\nwant: %v", got, want)
	}
	for _, want := range []string{ToolImpact, ToolAnalyze, ToolSearchSemantic, ToolCompound} {
		if !containsTool(got, want) {
			t.Errorf("Labs catalog missing %q", want)
		}
	}
}

func TestLabsProfile_ToolsListConstructionNeverCallsClient(t *testing.T) {
	spy := &catalogCallSpy{}
	server := NewServerWithClient(spy, WithLabs())
	if got, want := descriptorNames(server.toolDescriptors()), ToolNames(); !reflect.DeepEqual(got, want) {
		t.Fatalf("third-party full-Client Labs catalog mismatch:\n got: %v\nwant: %v", got, want)
	}
	if len(spy.calls) != 0 {
		t.Fatalf("Labs descriptor discovery executed client methods: %v", spy.calls)
	}
	// Exercise the JSON-RPC route too; the cached tools/list response must remain
	// pure, not merely direct calls to toolDescriptors in tests.
	request, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := server.Serve(context.Background(), bytes.NewReader(append(request, '\n')), &out); err != nil {
		t.Fatal(err)
	}
	if len(spy.calls) != 0 {
		t.Fatalf("Labs tools/list executed client methods: %v", spy.calls)
	}
}

func TestDefaultProfile_RejectsLabsInvocationBeforeDispatch(t *testing.T) {
	c := &profileClient{}
	server := NewServerWithClient(c)
	response := invokeTool(t, server, ToolAnalyze, map[string]any{
		"analyzer": "impact", "symbol": "x",
	})
	if response.Error == nil || !strings.Contains(response.Error.Message, "Stable") {
		t.Fatalf("default Labs invocation must fail closed: %+v", response)
	}
	if len(c.analyzeCalls) != 0 {
		t.Fatalf("unadvertised analyze reached client: %+v", c.analyzeCalls)
	}
}

func TestImpact_DedicatedStableDispatchForcesImpactAnalyzer(t *testing.T) {
	c := &profileClient{}
	server := NewServerWithClient(c)
	response := invokeTool(t, server, ToolImpact, map[string]any{
		"analyzer":  "taint", // must be ignored; not part of the public schema
		"symbol":    "node-1",
		"direction": "forward",
		"max_nodes": 7,
	})
	if response.Error != nil {
		t.Fatalf("impact call failed: %+v", response.Error)
	}
	if len(c.analyzeCalls) != 1 {
		t.Fatalf("Analyze calls = %d, want 1", len(c.analyzeCalls))
	}
	got := c.analyzeCalls[0]
	if got.Name != "impact" || got.Symbol != "node-1" || got.Direction != "forward" || got.MaxNodes != 7 {
		t.Fatalf("impact dispatch params = %+v", got)
	}
}

func TestLabsProfile_RejectsCapabilityHiddenInvocation(t *testing.T) {
	c := &profileClient{reviewUnavailable: true}
	server := NewServerWithClient(c, WithLabs())
	if containsTool(descriptorNames(server.toolDescriptors()), ToolPrComment) {
		t.Fatal("pr_comment unexpectedly advertised without review service")
	}
	// Pure capability filtering must not execute the unavailable operation, and
	// the actual request must be rejected by the same catalog boundary.
	probeCalls := c.prCommentCalls
	if probeCalls != 0 {
		t.Fatalf("descriptor construction probed pr_comment: %d calls", probeCalls)
	}
	response := invokeTool(t, server, ToolPrComment, map[string]any{"diff": "x"})
	if response.Error == nil || !strings.Contains(response.Error.Message, "not available") {
		t.Fatalf("hidden Labs invocation must fail closed: %+v", response)
	}
	if c.prCommentCalls != probeCalls {
		t.Fatalf("hidden tool reached client: calls %d -> %d", probeCalls, c.prCommentCalls)
	}
}

func descriptorNames(descriptors []map[string]any) []string {
	names := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if name, ok := descriptor["name"].(string); ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func containsTool(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

type toolInvocationResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func invokeTool(t *testing.T, server *Server, name string, arguments map[string]any) toolInvocationResponse {
	t.Helper()
	request, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": arguments},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := server.Serve(context.Background(), bytes.NewReader(append(request, '\n')), &out); err != nil {
		t.Fatal(err)
	}
	var response toolInvocationResponse
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &response); err != nil {
		t.Fatalf("decode response: %v (%s)", err, out.Bytes())
	}
	return response
}
