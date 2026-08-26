// SW-226 (AX-06) canary evidence at the SURFACE level: the dead_code operation
// produces identical bytes and identical failures in all three kill-switch
// positions, across its whole fixture suite, over the real MCP and HTTP dispatch
// paths and the real engine.
//
// This is the file AC-3 is answered from. The unit-level seam tests live in
// surfaces/client/canary_test.go; what those cannot show is that the operation
// the SURFACES actually serve is the one that was compared — which is why the
// active-mode cases below go through mcp.Server.Serve and the HTTP handler
// rather than through client.DispatchCanary.
//
// The fixture suite deliberately covers the failure classes as well as the happy
// path, because a canary proven only on its success case has proven the easy
// half. dead_code has three reachable outcomes, and all three are here:
//
//	populated    the pinned Go corpus fixture, indexed — real candidates, real
//	             exclusions, a non-trivial document
//	empty        an indexed-but-empty graph — the typed `empty` outcome
//	unavailable  a client with no graph dependencies — the typed `unavailable`
//	             outcome, returned with a NIL error
//	error        a client that returns ErrAgentIntelUnavailable — the daemon's
//	             real behaviour for this tool, and the only class that is an
//	             error rather than a document
package surfaces_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/observe"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/client"
	httpsrv "github.com/samibel/graphi/surfaces/http"
	"github.com/samibel/graphi/surfaces/mcp"
)

// withCanaryMode installs a kill-switch position for one test.
func withCanaryMode(t *testing.T, mode client.CanaryMode) {
	t.Helper()
	previous := client.CanaryModeSetting()
	if err := client.SetCanaryMode(mode); err != nil {
		t.Fatalf("SetCanaryMode(%q): %v", mode, err)
	}
	t.Cleanup(func() {
		if err := client.SetCanaryMode(previous); err != nil {
			t.Fatalf("restore canary mode %q: %v", previous, err)
		}
	})
}

// canaryFixture is one dead_code outcome plus the client that produces it.
type canaryFixture struct {
	name    string
	client  client.Client
	wantErr bool
	// wantOutcomes maps the item cap to the contract outcome the document must
	// carry, so a fixture that silently stops exercising its failure class fails
	// instead of quietly comparing two happy paths. The cap matters: on the
	// populated fixture an uncapped run is `found` and a capped one is
	// `partial`, which is how the truncation class gets into the suite.
	wantOutcomes map[int]string
}

// canaryFixtures builds the four outcome fixtures.
func canaryFixtures(t *testing.T) []canaryFixture {
	t.Helper()

	populated := graphstore.NewMemStore()
	indexCharFixture(t, populated)

	empty := graphstore.NewMemStore()

	return []canaryFixture{
		{
			name:         "populated",
			client:       charClient(populated).WithRepoRoot(charFixtureDir(t)),
			wantOutcomes: map[int]string{0: "found", 3: "partial"},
		},
		{
			name:         "empty_graph",
			client:       charClient(empty),
			wantOutcomes: map[int]string{0: "empty", 3: "empty"},
		},
		{
			name: "no_graph_dependencies",
			// No query and no search service: agentDeps() is unavailable, and
			// the tool degrades to its typed `unavailable` document with a NIL
			// error rather than failing.
			client:       client.NewDirect(nil, nil),
			wantOutcomes: map[int]string{0: "unavailable", 3: "unavailable"},
		},
		{
			name:    "agent_intel_unavailable",
			client:  canaryErrorClient{},
			wantErr: true,
		},
	}
}

// canaryErrorClient returns the sentinel the daemon client returns for every
// agent-intelligence tool. It is the only dead_code failure class that is an
// ERROR, and it is here so error parity is proven on a real error rather than a
// constructed one.
type canaryErrorClient struct {
	client.Client
}

func (canaryErrorClient) DeadCode(context.Context, client.DeadCodeParams) ([]byte, error) {
	return nil, client.ErrAgentIntelUnavailable
}

// TestAX06_CanaryByteAndErrorParityAcrossKillSwitchPositions is AC-3: 100 % byte
// and error parity between the legacy and executor paths across the canary's
// fixture suite, including its unavailable/empty/error cases.
//
// The baseline is captured in `legacy` position — the pre-AX-06 behaviour — and
// every other position is compared against THAT, not against itself.
func TestAX06_CanaryByteAndErrorParityAcrossKillSwitchPositions(t *testing.T) {
	ctx := context.Background()
	covered := map[string]bool{}
	for _, fixture := range canaryFixtures(t) {
		t.Run(fixture.name, func(t *testing.T) {
			for _, maxItems := range []int{0, 3} {
				t.Run(fmt.Sprintf("max_items=%d", maxItems), func(t *testing.T) {
					args := func() client.Arguments {
						return &client.DeadCodeArgs{MaxItems: maxItems}
					}

					// Baseline: the legacy position, which by construction is
					// the unwrapped Client.DeadCode call.
					withCanaryMode(t, client.CanaryModeLegacy)
					client.ResetCanaryMismatches()
					wantBytes, wantErr := client.DispatchCanary(ctx, fixture.client, args())
					covered[assertCanaryOutcomeShape(t, fixture, maxItems, wantBytes, wantErr)] = true

					// And an independent baseline: the method call a surface
					// made before this story existed. If DispatchCanary's legacy
					// position ever stops being that call, this catches it.
					directBytes, directErr := fixture.client.DeadCode(ctx, client.DeadCodeParams{MaxItems: maxItems})
					assertCanarySameOutcome(t, "legacy position vs direct method", wantBytes, wantErr, directBytes, directErr)

					for _, mode := range []client.CanaryMode{client.CanaryModeShadow, client.CanaryModeActive} {
						withCanaryMode(t, mode)
						client.ResetCanaryMismatches()
						gotBytes, gotErr := client.DispatchCanary(ctx, fixture.client, args())
						assertCanarySameOutcome(t, string(mode), wantBytes, wantErr, gotBytes, gotErr)
						if count, last := client.CanaryMismatches(); count != 0 {
							t.Fatalf("%q recorded %d mismatch(es): %s", mode, count, last)
						}
					}
					client.ResetCanaryMismatches()
				})
			}
		})
	}

	// The suite is only "the canary's fixture suite including its failure
	// classes" if the failure classes were actually reached. A fixture that
	// stopped producing its class would already have failed above; this catches
	// the other direction — a class quietly dropped from the fixture list.
	for _, class := range []string{"found", "partial", "empty", "unavailable", "error"} {
		if !covered[class] {
			t.Errorf("the canary fixture suite never produced the %q outcome; AC-3 requires "+
				"the unavailable/empty/error classes, not only the happy path", class)
		}
	}
}

// assertCanaryOutcomeShape checks the fixture is still exercising the class it
// claims. Without this, a fixture that quietly started returning the happy-path
// document would still pass every parity comparison below — and would have
// stopped proving anything about failure classes.
func assertCanaryOutcomeShape(t *testing.T, fixture canaryFixture, maxItems int, body []byte, err error) string {
	t.Helper()
	if fixture.wantErr {
		if err == nil {
			t.Fatalf("fixture %q no longer produces an error", fixture.name)
		}
		return "error"
	}
	if err != nil {
		t.Fatalf("fixture %q: %v", fixture.name, err)
	}
	var doc struct {
		Outcome string `json:"outcome"`
	}
	if jsonErr := json.Unmarshal(body, &doc); jsonErr != nil {
		t.Fatalf("fixture %q: decode contract document: %v", fixture.name, jsonErr)
	}
	want, declared := fixture.wantOutcomes[maxItems]
	if !declared {
		t.Fatalf("fixture %q declares no expected outcome for max_items=%d", fixture.name, maxItems)
	}
	if doc.Outcome != want {
		t.Fatalf("fixture %q at max_items=%d produced outcome %q, want %q — the fixture stopped "+
			"exercising the class it was written for", fixture.name, maxItems, doc.Outcome, want)
	}
	return doc.Outcome
}

// assertCanarySameOutcome compares bytes AND error, the same discipline the
// AX-04 parity tests use: comparing only bytes would let a path that fails where
// the other succeeds pass as "both empty".
func assertCanarySameOutcome(t *testing.T, label string, wantBytes []byte, wantErr error, gotBytes []byte, gotErr error) {
	t.Helper()
	switch {
	case wantErr == nil && gotErr != nil:
		t.Fatalf("%s failed where the legacy position succeeded: %v", label, gotErr)
	case wantErr != nil && gotErr == nil:
		t.Fatalf("%s succeeded where the legacy position failed with: %v", label, wantErr)
	case wantErr != nil && gotErr != nil:
		if gotErr.Error() != wantErr.Error() {
			t.Fatalf("%s error = %v, legacy error = %v", label, gotErr, wantErr)
		}
		return
	}
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("%s bytes differ from the legacy position\n  legacy   (%d bytes)\n  %s (%d bytes)",
			label, len(wantBytes), label, len(gotBytes))
	}
	if len(wantBytes) == 0 {
		t.Fatalf("%s: the legacy position produced no bytes and no error — this case proves nothing", label)
	}
}

// TestAX06_CanarySurfaceDispatchIsUnchangedInEveryPosition is AC-4: with the
// switch at `active`, MCP and HTTP dispatch for this ONE tool route through the
// executor and still answer with the same bytes as `legacy` did. It drives the
// real transports, so it also covers the argument mapping each surface applies
// before the seam sees anything.
func TestAX06_CanarySurfaceDispatchIsUnchangedInEveryPosition(t *testing.T) {
	store := graphstore.NewMemStore()
	indexCharFixture(t, store)
	direct := charClient(store).WithRepoRoot(charFixtureDir(t))

	surfaces := map[string]func(*testing.T, *client.Direct, int) []byte{
		"mcp":  mcpDeadCodeOutput,
		"http": httpDeadCodeOutput,
		"cli":  cliDeadCodeOutput,
	}

	for name, run := range surfaces {
		t.Run(name, func(t *testing.T) {
			for _, limit := range []int{0, 3} {
				t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
					withCanaryMode(t, client.CanaryModeLegacy)
					want := run(t, direct, limit)
					if len(want) == 0 {
						t.Fatal("the legacy position produced no bytes; this case proves nothing")
					}
					for _, mode := range []client.CanaryMode{client.CanaryModeShadow, client.CanaryModeActive} {
						withCanaryMode(t, mode)
						client.ResetCanaryMismatches()
						got := run(t, direct, limit)
						if !bytes.Equal(got, want) {
							t.Fatalf("%s dispatch in %q returned different bytes than in %q\n"+
								"  legacy (%d bytes)\n  %s (%d bytes)",
								name, mode, client.CanaryModeLegacy, len(want), mode, len(got))
						}
						if count, last := client.CanaryMismatches(); count != 0 {
							t.Fatalf("%s dispatch in %q recorded %d mismatch(es): %s", name, mode, count, last)
						}
					}
					client.ResetCanaryMismatches()
				})
			}
		})
	}
}

// TestAX06_NonCanaryOperationsAreUntouched is AC-6's dispatch half: the kill
// switch moves ONE operation. Its sibling Labs agent-intelligence tools, which
// share the same MCP arm shape and the same HTTP route, must answer identically
// in every position — if any of them had been wired to the seam by accident,
// `active` would either change their bytes or fail outright.
func TestAX06_NonCanaryOperationsAreUntouched(t *testing.T) {
	store := graphstore.NewMemStore()
	indexCharFixture(t, store)
	direct := charClient(store).WithRepoRoot(charFixtureDir(t))

	neighbours := []string{"repo_overview", "framework_map", "architecture", "architecture_violations", "hotspots"}

	withCanaryMode(t, client.CanaryModeLegacy)
	baseline := map[string][]byte{}
	for _, tool := range neighbours {
		baseline[tool] = mcpAgentToolOutput(t, direct, tool)
	}

	for _, mode := range []client.CanaryMode{client.CanaryModeShadow, client.CanaryModeActive} {
		withCanaryMode(t, mode)
		client.ResetCanaryMismatches()
		for _, tool := range neighbours {
			if got := mcpAgentToolOutput(t, direct, tool); !bytes.Equal(got, baseline[tool]) {
				t.Errorf("%s changed when the canary switch moved to %q — the switch is not "+
					"one operation wide", tool, mode)
			}
		}
		if count, last := client.CanaryMismatches(); count != 0 {
			t.Errorf("a non-canary operation reached the dual-run comparison in %q: %s", mode, last)
		}
	}
	client.ResetCanaryMismatches()
}

// TestAX06_RetryableWireShapeSurvivesTheCanary is AC-6's wire half, checked on
// the ONE tool whose dispatch this story changed. An unbound session must still
// answer a dead_code call with the frozen retryable shape — code -32002, the
// "repository is not bound: " prefix and the "; retry in a moment" suffix —
// in every kill-switch position, because the canary seam sits behind the bind
// gate and must not have moved in front of it.
func TestAX06_RetryableWireShapeSurvivesTheCanary(t *testing.T) {
	for _, mode := range []client.CanaryMode{client.CanaryModeLegacy, client.CanaryModeShadow, client.CanaryModeActive} {
		t.Run(string(mode), func(t *testing.T) {
			withCanaryMode(t, mode)
			// An unbound server whose binder never completes: the session is
			// initialized, the bind is in flight, and a tool call is retryable.
			srv := mcp.NewServerWithBinder(func(context.Context, []string) (mcp.Binding, error) {
				<-make(chan struct{}) // never returns
				return mcp.Binding{}, nil
			}, mcp.WithLabs(), mcp.WithBindGrace(0))

			var in bytes.Buffer
			for _, req := range []map[string]any{
				{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}},
				{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
					"name": mcp.ToolDeadCode, "arguments": map[string]any{},
				}},
			} {
				body, err := json.Marshal(req)
				if err != nil {
					t.Fatal(err)
				}
				in.Write(append(body, '\n'))
			}
			var out bytes.Buffer
			if err := srv.Serve(context.Background(), bytes.NewReader(in.Bytes()), &out); err != nil {
				t.Fatalf("mcp.Serve: %v", err)
			}
			var call struct {
				ID    int `json:"id"`
				Error *struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
				var resp struct {
					ID int `json:"id"`
				}
				if err := json.Unmarshal([]byte(line), &resp); err != nil || resp.ID != 2 {
					continue
				}
				if err := json.Unmarshal([]byte(line), &call); err != nil {
					t.Fatalf("decode tools/call response: %v", err)
				}
			}
			if call.Error == nil {
				t.Fatalf("an unbound dead_code call did not fail closed: %s", out.String())
			}
			if call.Error.Code != -32002 {
				t.Errorf("code = %d, want the frozen retryable -32002", call.Error.Code)
			}
			if !strings.HasPrefix(call.Error.Message, "repository is not bound: ") {
				t.Errorf("message %q lost the frozen prefix", call.Error.Message)
			}
			if !strings.HasSuffix(call.Error.Message, "; retry in a moment") {
				t.Errorf("message %q lost the frozen suffix", call.Error.Message)
			}
		})
	}
}

// mcpDeadCodeOutput drives the real MCP tools/call dispatch for dead_code.
func mcpDeadCodeOutput(t *testing.T, direct *client.Direct, limit int) []byte {
	t.Helper()
	args := map[string]any{}
	if limit > 0 {
		args["limit"] = limit
	}
	return ax06MCPToolText(t, direct, mcp.ToolDeadCode, args)
}

// mcpAgentToolOutput drives the real MCP tools/call dispatch for a neighbouring
// Labs agent tool with default arguments.
func mcpAgentToolOutput(t *testing.T, direct *client.Direct, tool string) []byte {
	t.Helper()
	return ax06MCPToolText(t, direct, tool, map[string]any{})
}

// ax06MCPToolText runs one tools/call against a labs-profile server over the given
// client and returns the text payload.
func ax06MCPToolText(t *testing.T, direct *client.Direct, tool string, args map[string]any) []byte {
	t.Helper()
	srv := mcp.NewServerWithClient(direct, mcp.WithLabs())
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(string(body)+"\n"), &out); err != nil {
		t.Fatalf("mcp.Serve: %v", err)
	}
	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("decode mcp response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("mcp %s: error %d: %s", tool, resp.Error.Code, resp.Error.Message)
	}
	if len(resp.Result.Content) != 1 {
		t.Fatalf("mcp %s: unexpected content: %+v", tool, resp.Result.Content)
	}
	return []byte(resp.Result.Content[0].Text)
}

// httpDeadCodeOutput drives the real HTTP /analyze/dead_code route.
func httpDeadCodeOutput(t *testing.T, direct *client.Direct, limit int) []byte {
	t.Helper()
	t.Setenv(httpsrv.LabsEnvVar, "1")
	srv := httpsrv.New(direct, observe.New())
	target := "/analyze/dead_code"
	if limit > 0 {
		target = fmt.Sprintf("%s?max-items=%d", target, limit)
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = "127.0.0.1"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("http %s: code=%d body=%s", target, rec.Code, rec.Body.String())
	}
	var env struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("http %s: decode envelope: %v", target, err)
	}
	return env.Payload
}

// cliDeadCodeOutput drives the CLI verb, which shares the client seam but not
// the canary dispatch: it calls Client.DeadCode directly. It is here so the
// suite proves the THIRD surface still agrees with the other two in every
// position — a canary that silently changed one transport's bytes would show up
// as a CLI/MCP divergence, which is the parity property this repository already
// guards.
func cliDeadCodeOutput(t *testing.T, direct *client.Direct, limit int) []byte {
	t.Helper()
	var out, errOut bytes.Buffer
	var args []string
	if limit > 0 {
		args = []string{"-max-items", fmt.Sprint(limit)}
	}
	if err := cli.RunDeadCode(context.Background(), direct, args, &out, &errOut); err != nil {
		t.Fatalf("cli.RunDeadCode: %v", err)
	}
	return bytes.TrimRight(out.Bytes(), "\n")
}
