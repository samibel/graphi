package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// This file is the core evidence for SW-224 AC-3 and AC-4: for EVERY adapted
// operation, the executor path and the legacy path return the same bytes and the
// same error, on one shared fixture graph, in both directions.
//
// "Both directions" is meant literally:
//
//	legacy → executor   NewRequest(args) turns typed legacy arguments into an
//	                    addressed Request.
//	executor → legacy   DecodeArguments(req) turns that Request back into the
//	                    typed arguments, and Execute calls the legacy method
//	                    with them.
//
// The round trip is closed at both ends: the decoded arguments re-encode to the
// same bytes as the request they came from, and invoking them directly returns
// the same bytes as Execute.

// executorFixture seeds a small graph and returns a Direct wired with the query,
// search and analysis services — but deliberately WITHOUT a ledger and without a
// memory store, so the capability-unavailable sentinels are live on this fixture
// instead of hypothetical.
func executorFixture(t *testing.T) (*Direct, map[string]model.NodeId) {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	ids := map[string]model.NodeId{}
	nodes := map[string]model.Node{}

	for _, name := range []string{"A", "B", "C", "D"} {
		n, err := model.NewNode("function", "p."+name, "p/"+name+".go", 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatal(err)
		}
		ids[name] = n.ID()
		nodes[name] = n
	}
	edge := func(from, to, kind string, tier model.ConfidenceTier, conf float64, reason string, ev []string) {
		t.Helper()
		e, err := model.NewEdge(nodes[from].ID(), nodes[to].ID(), kind, tier, conf, reason, ev)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	edge("A", "B", query.EdgeKindCalls, model.TierConfirmed, 1, "ab", []string{"e1"})
	edge("B", "C", query.EdgeKindCalls, model.TierDerived, 0.8, "bc", []string{"e2"})
	edge("A", "C", query.EdgeKindCalls, model.TierHeuristic, 0.4, "ac", []string{"e3"})
	edge("D", "B", query.EdgeKindReferences, model.TierDerived, 0.7, "db", []string{"e4"})

	direct := NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))
	return direct, ids
}

// executorParityCase is one adapted operation invoked two INDEPENDENT ways.
//
// legacy is written out by hand as the Client method call a surface makes today.
// It deliberately does NOT go through the adapter: a baseline computed by
// calling the adapter's own invoke would move with the adapter, so a broken
// adapter would agree with itself and the test would prove nothing. (It did,
// briefly, during this story — a mutation that added 1 to every depth passed.)
type executorParityCase struct {
	operation string
	args      Arguments
	legacy    func(ctx context.Context, c Client) ([]byte, error)
}

// executorParityCases returns one invocation per adapted operation. Every entry
// is a REAL call against the fixture; nothing here is a placeholder that would
// make two empty results look like agreement.
func executorParityCases(ids map[string]model.NodeId) []executorParityCase {
	symbol := string(ids["B"])
	cases := []executorParityCase{
		{
			operation: "search",
			args:      &SearchArgs{Query: "p.", Limit: 10},
			legacy:    func(ctx context.Context, c Client) ([]byte, error) { return c.Search(ctx, "p.", 10) },
		},
		{
			operation: "search_semantic",
			args:      &SemanticSearchArgs{Query: "p.", Limit: 10},
			legacy:    func(ctx context.Context, c Client) ([]byte, error) { return c.SemanticSearch(ctx, "p.", 10) },
		},
		{
			operation: "search_ast",
			args:      &SearchASTArgs{Pattern: `{"kind":"function"}`, Limit: 5},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.SearchAST(ctx, `{"kind":"function"}`, 5)
			},
		},
		{
			operation: "find_clones",
			args:      &FindClonesArgs{},
			legacy:    func(ctx context.Context, c Client) ([]byte, error) { return c.FindClones(ctx, "") },
		},
		{
			operation: "compound",
			args:      &CompoundArgs{Query: "SEED p.A\nHOP out calls\n"},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.Compound(ctx, "SEED p.A\nHOP out calls\n")
			},
		},
		{
			operation: "impact",
			args:      &ImpactArgs{Symbol: symbol, Direction: "forward", MaxNodes: 10},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return AsStable(c).Impact(ctx, ImpactParams{Symbol: symbol, Direction: "forward", MaxNodes: 10})
			},
		},
		{
			operation: "analyze",
			args:      &AnalyzeArgs{AnalyzeParams{Name: "impact", Symbol: symbol, Direction: "forward", MaxNodes: 10}},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.Analyze(ctx, AnalyzeParams{Name: "impact", Symbol: symbol, Direction: "forward", MaxNodes: 10})
			},
		},
		{
			operation: "agent_brief",
			args:      &BriefArgs{Topic: "p.B"},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				// Only the CANONICAL bytes; the Markdown rendering is a
				// presentation of them and stays on the surface (BriefArgs docs).
				canonical, _, err := c.Brief(ctx, "p.B")
				return canonical, err
			},
		},
		{
			operation: "explain_symbol",
			args:      &ExplainSymbolArgs{Symbol: symbol, MaxItems: 5},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.ExplainSymbol(ctx, symbol, 5)
			},
		},
		{
			operation: "related_files",
			args:      &RelatedFilesArgs{Target: symbol, Direction: "both", MaxFiles: 5},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.RelatedFiles(ctx, symbol, "both", 5)
			},
		},
		{
			operation: "change_risk",
			args:      &ChangeRiskArgs{Target: symbol, MaxItems: 5},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.ChangeRisk(ctx, symbol, "", 5)
			},
		},
		{
			operation: "savings",
			args:      &SavingsArgs{},
			legacy:    func(ctx context.Context, c Client) ([]byte, error) { return c.Savings(ctx) },
		},
		{
			operation: "memory",
			args:      &MemoryArgs{MemoryRequest{Op: "list", Scope: "project"}},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.Memory(ctx, MemoryRequest{Op: "list", Scope: "project"})
			},
		},
	}
	// The ten structural query operations ride one adapter; exercising each id
	// separately is what proves the shared adapter is addressed per operation.
	for _, op := range query.Operations {
		depth := 0
		if op == query.OpNeighborhood {
			depth = 2
		}
		operation, wantDepth := op, depth
		cases = append(cases, executorParityCase{
			operation: operation,
			args:      &QueryArgs{Op: operation, Symbol: symbol, Depth: wantDepth},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.Query(ctx, operation, symbol, wantDepth)
			},
		})
	}
	return cases
}

// TestExecutor_AdapterByteParity is AC-3 and AC-4: for every adapted operation,
// the adapter path and the legacy path agree byte for byte and error for error,
// in both directions.
func TestExecutor_AdapterByteParity(t *testing.T) {
	direct, ids := executorFixture(t)
	executor, err := NewExecutor(direct)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	ctx := context.Background()

	for _, tc := range executorParityCases(ids) {
		t.Run(tc.operation, func(t *testing.T) {
			// Legacy path: the method call a surface makes today, written out
			// independently of the adapter.
			wantBytes, wantErr := tc.legacy(ctx, direct)

			// legacy → executor.
			req, err := executor.NewRequest(tc.args)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			if req.Operation != tc.operation {
				t.Fatalf("NewRequest addressed %q, want %q", req.Operation, tc.operation)
			}
			if req.Version == "" {
				t.Fatal("NewRequest left the contract version empty")
			}

			// executor → legacy.
			gotBytes, gotErr := executor.Execute(ctx, req)
			assertSameOutcome(t, "Execute", wantBytes, wantErr, gotBytes, gotErr)

			// The reverse adapter alone: decode the request back to typed
			// arguments and invoke the legacy method with THOSE. This is the
			// "and vice versa" half — it proves the decode direction carries
			// every argument, not just that Execute happens to work.
			decoded, err := executor.DecodeArguments(req)
			if err != nil {
				t.Fatalf("DecodeArguments: %v", err)
			}
			if decoded.Operation() != tc.operation {
				t.Fatalf("decoded arguments address %q, want %q", decoded.Operation(), tc.operation)
			}
			viaDecoded, viaDecodedErr := decoded.invoke(ctx, direct)
			assertSameOutcome(t, "decoded legacy call", wantBytes, wantErr, viaDecoded, viaDecodedErr)

			// And the round trip closes: re-encoding the decoded arguments
			// reproduces the request's argument bytes exactly, so nothing was
			// dropped, defaulted or renamed in between.
			reEncoded, err := executor.NewRequest(decoded)
			if err != nil {
				t.Fatalf("NewRequest(decoded): %v", err)
			}
			if !bytes.Equal(reEncoded.Arguments, req.Arguments) {
				t.Fatalf("argument round trip changed the payload:\n  first: %s\n  again: %s",
					req.Arguments, reEncoded.Arguments)
			}
			if reEncoded.Version != req.Version {
				t.Fatalf("version round trip: %q then %q", req.Version, reEncoded.Version)
			}
		})
	}
}

// assertSameOutcome compares bytes AND error identity. Comparing only bytes
// would let a path that fails where the other succeeds pass as "both empty".
func assertSameOutcome(t *testing.T, path string, wantBytes []byte, wantErr error, gotBytes []byte, gotErr error) {
	t.Helper()
	switch {
	case wantErr == nil && gotErr != nil:
		t.Fatalf("%s failed where the legacy path succeeded: %v", path, gotErr)
	case wantErr != nil && gotErr == nil:
		t.Fatalf("%s succeeded where the legacy path failed with: %v", path, wantErr)
	case wantErr != nil && gotErr != nil:
		if !errors.Is(gotErr, wantErr) || gotErr.Error() != wantErr.Error() {
			t.Fatalf("%s error = %v, legacy error = %v", path, gotErr, wantErr)
		}
		return
	}
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("%s bytes differ from the legacy path\n  legacy (%d bytes): %s\n  adapter (%d bytes): %s",
			path, len(wantBytes), truncateForDiff(wantBytes), len(gotBytes), truncateForDiff(gotBytes))
	}
	if len(wantBytes) == 0 {
		t.Fatalf("%s: the legacy path produced no bytes and no error — this case proves nothing", path)
	}
}

func truncateForDiff(b []byte) string {
	const max = 400
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "…"
}

// TestExecutor_EveryAdaptedOperationHasParityEvidence closes the gap a
// representative-set story invites: an adapter added later without a parity case
// would otherwise ship unproven. The build fails instead.
func TestExecutor_EveryAdaptedOperationHasParityEvidence(t *testing.T) {
	direct, ids := executorFixture(t)
	executor, err := NewExecutor(direct)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	covered := map[string]bool{}
	for _, tc := range executorParityCases(ids) {
		if covered[tc.operation] {
			t.Errorf("operation %q has two parity cases", tc.operation)
		}
		covered[tc.operation] = true
	}
	var missing []string
	for _, id := range executor.Adapted() {
		if !covered[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("adapted operations without byte-parity evidence: %v", missing)
	}
	for id := range covered {
		if _, ok := executor.adapters[id]; !ok {
			t.Errorf("parity case %q names an operation with no adapter", id)
		}
	}
}

// TestExecutor_CapabilityUnavailableIsPreservedExactly is the AC-4 half about
// capability-unavailable behaviour: the optional-service checks in Direct keep
// deciding availability, and the adapter path surfaces the SAME typed sentinel
// the surfaces render today. The executor adds no availability logic of its own,
// so it cannot start disagreeing with Direct about what is wired.
func TestExecutor_CapabilityUnavailableIsPreservedExactly(t *testing.T) {
	direct, _ := executorFixture(t) // no ledger, no memory store
	executor, err := NewExecutor(direct)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	ctx := context.Background()

	for _, tc := range []struct {
		name   string
		args   Arguments
		legacy func(ctx context.Context, c Client) ([]byte, error)
		want   error
	}{
		{
			name:   "savings without a ledger",
			args:   &SavingsArgs{},
			legacy: func(ctx context.Context, c Client) ([]byte, error) { return c.Savings(ctx) },
			want:   ErrSavingsUnavailable,
		},
		{
			name: "memory without a store",
			args: &MemoryArgs{MemoryRequest{Op: "list"}},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.Memory(ctx, MemoryRequest{Op: "list"})
			},
			want: ErrMemoryUnavailable,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, legacyErr := tc.legacy(ctx, direct)
			if !errors.Is(legacyErr, tc.want) {
				t.Fatalf("legacy path returned %v, want %v — the fixture no longer exercises this case", legacyErr, tc.want)
			}
			req, err := executor.NewRequest(tc.args)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			_, execErr := executor.Execute(ctx, req)
			if !errors.Is(execErr, tc.want) {
				t.Fatalf("Execute returned %v, want the legacy sentinel %v", execErr, tc.want)
			}
			if execErr.Error() != legacyErr.Error() {
				t.Fatalf("Execute error text %q differs from the legacy %q", execErr, legacyErr)
			}
		})
	}
}

// TestExecutor_GracefulSkipStaysGraceful pins the one optional service whose
// absence is NOT an error: semantic search returns a typed unavailable response
// with a nil error. An adapter that turned that into an error would change a
// documented graceful-skip contract, so it is asserted rather than assumed.
func TestExecutor_GracefulSkipStaysGraceful(t *testing.T) {
	direct, _ := executorFixture(t)
	executor, err := NewExecutor(direct)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	args := &SemanticSearchArgs{Query: "p.", Limit: 5}
	req, err := executor.NewRequest(args)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	got, err := executor.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute returned an error for the graceful-skip path: %v", err)
	}
	var decoded struct {
		Available bool `json:"available"`
	}
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("semantic response is not the typed engine response: %v (%s)", err, got)
	}
	if decoded.Available {
		t.Fatal("no embedder is wired, so the typed response must report available=false")
	}
}
