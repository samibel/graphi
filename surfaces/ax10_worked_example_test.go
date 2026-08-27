// SW-230 (AX-10) AC-4 — the worked example, as a test rather than as prose.
//
// The claim under test is the target metric the plan set for this phase: an
// existing read-only built-in is expressible as a MODULE + a REGISTRATION +
// HARNESS TESTS, with ZERO manual dispatch or descriptor edits. `dead_code` is
// the operation, and the reasons it is the right one are recorded in
// docs/extension-developer-kit.md and re-stated here so the choice is auditable
// from the test that proves it:
//
//   - It is read-only, deterministic and graph-read-only (catalog: tier labs,
//     determinism "deterministic", ports [graph.query, graph.search],
//     permissions [graph.read]), so it satisfies the harness's own criteria
//     rather than needing an exemption from them.
//   - Its MCP descriptor is already PROJECTED from its catalog spec (SW-225), so
//     "no descriptor edit" is a property this test can assert byte-for-byte
//     rather than a claim about a diff.
//   - Its surface dispatch already reaches the generic executor (SW-226), so "no
//     dispatch edit" is likewise observable: the canary kill switch at `active`
//     runs it entirely through the executor and the bytes do not move.
//
// What this file adds over SW-225 and SW-226 is the MODULE half, which neither
// of them had: the spec is contributed by a module registration through
// engine/module's typed builder, and the descriptor the live MCP server
// advertises is compared against the projection of THAT module-contributed spec.
// If a future change made the advertised descriptor anything other than a
// projection of the registered spec, this fails.
package surfaces_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/extpack"
	"github.com/samibel/graphi/engine/extpack/conformance"
	"github.com/samibel/graphi/engine/module"
	"github.com/samibel/graphi/engine/observe"
	"github.com/samibel/graphi/engine/opcatalog"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/client"
	httpsrv "github.com/samibel/graphi/surfaces/http"
	"github.com/samibel/graphi/surfaces/mcp"
)

// workedExampleOperation is the built-in AC-4 expresses as a module.
const workedExampleOperation = client.CanaryOperation

// workedExampleModuleID is the module id the registration below declares.
const workedExampleModuleID = "example.deadcode"

// composeWorkedExample is the REGISTRATION half: one module, one Register
// function, one typed AddOperation call. Nothing else is written anywhere.
//
// The spec it contributes is read from the shipped catalog rather than retyped,
// because a worked example that restated the operation's metadata would be
// demonstrating a second source of truth — the exact thing the operation catalog
// exists to remove.
func composeWorkedExample(t *testing.T, reader query.Reader) *module.Composition {
	t.Helper()
	shadow, err := opcatalog.Shadow()
	if err != nil {
		t.Fatalf("operation catalog: %v", err)
	}
	spec, ok := shadow.Lookup(workedExampleOperation)
	if !ok {
		t.Fatalf("the catalog does not declare %q", workedExampleOperation)
	}

	set := module.NewSet()
	if err := set.Add(module.Module{
		Manifest: module.Manifest{ID: workedExampleModuleID, Version: "1"},
		Register: func(b *module.Builder) error { return b.AddOperation(spec) },
	}); err != nil {
		t.Fatalf("add module: %v", err)
	}
	composition, err := set.Build(module.Inputs{Reader: reader})
	if err != nil {
		t.Fatalf("build module set: %v", err)
	}
	if !composition.Frozen() {
		t.Fatal("the composition is not frozen; a runtime a surface holds must be immutable")
	}
	return composition
}

// TestAX10_WorkedExample_OneModuleRegistrationCarriesTheWholeOperation is AC-4.
//
// Four things are asserted in one place because the claim is their conjunction:
// the module registration alone produces the catalog entry; the MCP descriptor
// the server advertises IS that entry projected; the HTTP surface advertises it
// at the resource the entry declares; and the dispatch that answers a call goes
// through the generic executor rather than through a hand-written arm.
func TestAX10_WorkedExample_OneModuleRegistrationCarriesTheWholeOperation(t *testing.T) {
	store := graphstore.NewMemStore()
	indexCharFixture(t, store)
	querySvc := query.New(store)
	searchSvc := search.New(store)
	direct := client.NewDirect(querySvc, searchSvc).
		WithAnalysis(analysis.NewDefaultService(store)).
		WithRepoRoot(charFixtureDir(t))

	composition := composeWorkedExample(t, store)

	// 1. THE MODULE DIRECTORY + REGISTRATION produced the catalog entry.
	if ids := composition.ModuleIDs(); len(ids) != 1 || ids[0] != workedExampleModuleID {
		t.Fatalf("composed modules = %v, want exactly [%s]", ids, workedExampleModuleID)
	}
	registered, ok := composition.Operations().Lookup(workedExampleOperation)
	if !ok {
		t.Fatalf("the module registration did not contribute %q", workedExampleOperation)
	}

	// 2. ZERO DESCRIPTOR EDITS: what the live MCP server advertises is exactly
	// the projection of the registered spec.
	projected, err := mcp.ProjectToolDescriptor(registered, false)
	if err != nil {
		t.Fatalf("project the registered spec: %v", err)
	}
	want := canonicalJSON(t, mcp.MarkLabsDescriptor(projected))
	got := canonicalJSON(t, advertisedDescriptor(t, direct, workedExampleOperation))
	if !bytes.Equal(want, got) {
		t.Fatalf("the advertised descriptor is not a projection of the registered spec\n"+
			"  projected: %s\n  advertised: %s", want, got)
	}

	// 3. The HTTP surface advertises it at the resource the spec declares.
	labsResources := contractResourcesOf(t, direct, true)
	if !contains(labsResources, registered.HTTPResource) {
		t.Fatalf("/contract does not advertise %q; it lists %v", registered.HTTPResource, labsResources)
	}
	if defaults := contractResourcesOf(t, direct, false); contains(defaults, registered.HTTPResource) {
		t.Errorf("a Labs operation is advertised by the DEFAULT HTTP profile; the frozen surface widened")
	}

	// 4. ZERO DISPATCH EDITS: with the kill switch at `active` the operation is
	// answered entirely through the generic executor, and the bytes do not move.
	withCanaryMode(t, client.CanaryModeLegacy)
	legacy := ax06MCPToolText(t, direct, mcp.ToolDeadCode, map[string]any{})
	if len(legacy) == 0 {
		t.Fatal("the legacy position produced no bytes; this case proves nothing")
	}
	withCanaryMode(t, client.CanaryModeActive)
	client.ResetCanaryMismatches()
	executed := ax06MCPToolText(t, direct, mcp.ToolDeadCode, map[string]any{})
	if !bytes.Equal(legacy, executed) {
		t.Errorf("dispatching %q through the executor changed its bytes (%d vs %d)",
			workedExampleOperation, len(legacy), len(executed))
	}
	if count, last := client.CanaryMismatches(); count != 0 {
		t.Errorf("the executor path diverged: %s", last)
	}
	client.ResetCanaryMismatches()
}

// TestAX10_WorkedExample_PassesTheConformanceHarness is the HARNESS TESTS half
// of AC-4, and the one place in the tree where the harness runs against the REAL
// surface projections rather than against stubs.
//
// The port handles are the two engine services dead_code's ports name — the
// same values surfaces/client.Direct's agentDeps() resolves — and the handler
// checks their identity before dispatching. Without that check, taking the
// handles would be ceremony: a handler that ignored them would still pass the
// port check, and the harness would be measuring a convention.
func TestAX10_WorkedExample_PassesTheConformanceHarness(t *testing.T) {
	store := graphstore.NewMemStore()
	indexCharFixture(t, store)
	querySvc := query.New(store)
	searchSvc := search.New(store)
	direct := client.NewDirect(querySvc, searchSvc).
		WithAnalysis(analysis.NewDefaultService(store)).
		WithRepoRoot(charFixtureDir(t))

	composition := composeWorkedExample(t, store)
	spec, ok := composition.Operations().Lookup(workedExampleOperation)
	if !ok {
		t.Fatalf("the module registration did not contribute %q", workedExampleOperation)
	}

	// The executor path is the one under test, so the harness runs with the
	// kill switch at `active`.
	withCanaryMode(t, client.CanaryModeActive)
	client.ResetCanaryMismatches()
	t.Cleanup(client.ResetCanaryMismatches)

	report := conformance.VerifyContribution(context.Background(), conformance.Contribution{
		Spec: spec,
		API:  extpack.APIRange{Min: conformance.HostAPIVersion, Max: conformance.HostAPIVersion},
		Ports: conformance.Ports{
			opcatalog.PortGraphQuery:  querySvc,
			opcatalog.PortGraphSearch: searchSvc,
		},
		Invoke: func(ctx context.Context, host conformance.Host, args json.RawMessage) ([]byte, error) {
			gotQuery, err := host.Use(opcatalog.PortGraphQuery)
			if err != nil {
				return nil, err
			}
			gotSearch, err := host.Use(opcatalog.PortGraphSearch)
			if err != nil {
				return nil, err
			}
			if gotQuery != any(querySvc) || gotSearch != any(searchSvc) {
				return nil, fmt.Errorf("the harness handed a different service than the one %q dispatches over",
					workedExampleOperation)
			}
			var decoded struct {
				Limit int `json:"limit"`
			}
			if len(args) > 0 {
				if err := json.Unmarshal(args, &decoded); err != nil {
					return nil, err
				}
			}
			return client.DispatchOperation(ctx, direct, &client.DeadCodeArgs{MaxItems: decoded.Limit})
		},
		Fixtures: []conformance.Fixture{
			{Name: "default"},
			{Name: "capped", Arguments: json.RawMessage(`{"limit":3}`)},
		},
		Projectors: []conformance.Projector{
			{Surface: "mcp", Render: func(s opcatalog.OperationSpec) (map[string]any, error) {
				descriptor, err := mcp.ProjectToolDescriptor(s, false)
				if err != nil {
					return nil, err
				}
				return mcp.MarkLabsDescriptor(descriptor), nil
			}},
			{Surface: "http", Render: func(s opcatalog.OperationSpec) (map[string]any, error) {
				labs := contractResourcesOf(t, direct, true)
				if !contains(labs, s.HTTPResource) {
					return nil, fmt.Errorf("the HTTP surface does not advertise %q", s.HTTPResource)
				}
				return map[string]any{
					"resource": s.HTTPResource,
					"labs":     !contains(contractResourcesOf(t, direct, false), s.HTTPResource),
				}, nil
			}},
		},
	})
	if err := report.Err(); err != nil {
		t.Fatalf("the worked example is not conformant:\n%v\n%s", err, report)
	}
	if count, last := client.CanaryMismatches(); count != 0 {
		t.Errorf("the executor path diverged during the harness run: %s", last)
	}
}

// advertisedDescriptor returns the descriptor the maximal (Stable+Labs) MCP
// registry actually advertises for one tool, read out of a real tools/list.
func advertisedDescriptor(t *testing.T, direct *client.Direct, name string) map[string]any {
	t.Helper()
	srv := mcp.NewServerWithClient(direct, mcp.WithLabs())
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), bytes.NewReader(append(body, '\n')), &out); err != nil {
		t.Fatalf("mcp.Serve: %v", err)
	}
	var resp struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	for _, tool := range resp.Result.Tools {
		if tool["name"] == name {
			return tool
		}
	}
	t.Fatalf("tools/list does not advertise %q", name)
	return nil
}

// contractResourcesOf reads the resource list a real HTTP server advertises.
func contractResourcesOf(t *testing.T, direct *client.Direct, labs bool) []string {
	t.Helper()
	if labs {
		t.Setenv(httpsrv.LabsEnvVar, "1")
	} else {
		t.Setenv(httpsrv.LabsEnvVar, "")
	}
	srv := httpsrv.New(direct, observe.New())
	req := httptest.NewRequest(http.MethodGet, "/contract", nil)
	req.Host = "127.0.0.1"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /contract: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var doc struct {
		Resources []string `json:"resources"`
		Payload   struct {
			Resources []string `json:"resources"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode /contract: %v\n%s", err, rec.Body.String())
	}
	if len(doc.Resources) > 0 {
		return doc.Resources
	}
	return doc.Payload.Resources
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// canonicalJSON renders a decoded JSON value as stable bytes for comparison.
func canonicalJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}
