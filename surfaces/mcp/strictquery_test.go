package mcp

// Adversarial pins for the P1 strict_query Labs tool (PRD v1.0 §8 Phase 9,
// delta doc §B2).
//
// The CLI/MCP byte parity is structural — ONE shared composition
// (surfaces/client/query_strict.go) feeds both surfaces — but structure can be
// refactored away silently, so this file pins it EXECUTABLY: the MCP tool text
// is compared byte-for-byte against client.ComposeStrictQuery, which is exactly
// what `graphi query-strict` writes minus its trailing newline. Also pinned:
// Labs gating (absent from the Stable catalog AND dispatch-rejected before the
// client), the closed operation set, the error model, and the property this
// whole surface exists for — a result emptied by the filter never reads as a
// proven absence.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/client"
)

// buildStrictQueryStore seeds a MemStore whose callers-of-target result mixes
// tiers, so a minimum-tier filter has something to withhold: one confirmed
// caller and one heuristic caller of the same target.
func buildStrictQueryStore(t *testing.T) (graphstore.Graphstore, model.NodeId) {
	t.Helper()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	mk := func(name string, line int) model.Node {
		n, err := model.NewNode("function", name, "a.go", line, 1)
		if err != nil {
			t.Fatalf("NewNode(%s): %v", name, err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode(%s): %v", name, err)
		}
		return n
	}
	target := mk("target", 1)
	confirmedCaller := mk("confirmedCaller", 10)
	heuristicCaller := mk("heuristicCaller", 20)

	mkEdge := func(from, to model.NodeId, tier model.ConfidenceTier, conf float64) {
		e, err := model.NewEdge(from, to, "calls", tier, conf,
			"seeded by the strict-query fixture", []string{"a.go:1"})
		if err != nil {
			t.Fatalf("NewEdge: %v", err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatalf("PutEdge: %v", err)
		}
	}
	mkEdge(confirmedCaller.ID(), target.ID(), model.TierConfirmed, 1.0)
	mkEdge(heuristicCaller.ID(), target.ID(), model.TierHeuristic, 0.6)

	return store, target.ID()
}

func strictQueryClient(t *testing.T) (client.Client, string) {
	t.Helper()
	store, target := buildStrictQueryStore(t)
	return client.NewDirect(query.New(store), search.New(store)), string(target)
}

// TestStrictQuery_HiddenWithoutLabs is the gating pin, in both halves that
// matter. Advertisement and dispatch share one allow-list, so a tool absent
// from tools/list must also be unreachable through tools/call — otherwise a
// client that learned the name elsewhere could still invoke a Labs capability
// against a Stable-profile server.
func TestStrictQuery_HiddenWithoutLabs(t *testing.T) {
	c, target := strictQueryClient(t)

	stable := NewServerWithClient(c)
	for _, name := range descriptorNames(stable.toolDescriptors()) {
		if name == ToolStrictQuery {
			t.Fatalf("%s is advertised in the Stable catalog", ToolStrictQuery)
		}
	}
	resp := invokeTool(t, stable, ToolStrictQuery, map[string]any{"operation": "callers", "symbol": target})
	if resp.Error == nil {
		t.Fatalf("%s was dispatchable without Labs", ToolStrictQuery)
	}
	if !strings.Contains(resp.Error.Message, "not available") {
		t.Errorf("unexpected gating error: %s", resp.Error.Message)
	}

	labs := NewServerWithClient(c, WithLabs())
	found := false
	for _, name := range descriptorNames(labs.toolDescriptors()) {
		if name == ToolStrictQuery {
			found = true
		}
	}
	if !found {
		t.Fatalf("%s missing from the Labs catalog", ToolStrictQuery)
	}
}

// TestStrictQuery_ParityWithSharedComposition is the byte-parity pin: for the
// same inputs the tool text equals what the shared composition produces for the
// same client. Both halves run over the SAME store, so a divergence can only
// come from the surface, which is the thing under test.
func TestStrictQuery_ParityWithSharedComposition(t *testing.T) {
	c, target := strictQueryClient(t)
	server := NewServerWithClient(c, WithLabs())

	cases := []struct {
		name string
		args map[string]any
		opts client.StrictQueryOptions
	}{
		{"callers default tier", map[string]any{"operation": "callers", "symbol": target},
			client.StrictQueryOptions{Operation: "callers", Symbol: target}},
		{"callers confirmed only", map[string]any{"operation": "callers", "symbol": target, "minimum_tier": "confirmed"},
			client.StrictQueryOptions{Operation: "callers", Symbol: target, MinimumTier: "confirmed"}},
		{"callers derived", map[string]any{"operation": "callers", "symbol": target, "minimum_tier": "derived"},
			client.StrictQueryOptions{Operation: "callers", Symbol: target, MinimumTier: "derived"}},
		{"neighborhood with depth", map[string]any{"operation": "neighborhood", "symbol": target, "depth": 2},
			client.StrictQueryOptions{Operation: "neighborhood", Symbol: target, Depth: 2}},
		{"unknown symbol", map[string]any{"operation": "callers", "symbol": "no_such_symbol_xyz"},
			client.StrictQueryOptions{Operation: "callers", Symbol: "no_such_symbol_xyz"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := toolText(t, invokeTool(t, server, ToolStrictQuery, tc.args))
			want, _, _, err := client.ComposeStrictQuery(context.Background(), c, tc.opts)
			if err != nil {
				t.Fatalf("shared composition: %v", err)
			}
			if got != string(want) {
				t.Errorf("MCP text diverged from the shared composition:\nMCP:    %s\nshared: %s", got, want)
			}
		})
	}
}

// TestStrictQuery_WithholdsAndSaysSo is the red gate, on real mixed-tier data.
// Raising the minimum tier must both remove the weaker caller AND report that
// it did: a filtered result that silently shrank is a false negative, and on a
// trust surface a false negative is worse than no answer.
func TestStrictQuery_WithholdsAndSaysSo(t *testing.T) {
	c, target := strictQueryClient(t)
	server := NewServerWithClient(c, WithLabs())

	decode := func(tier string) client.StrictEnvelope {
		t.Helper()
		args := map[string]any{"operation": "callers", "symbol": target}
		if tier != "" {
			args["minimum_tier"] = tier
		}
		var env client.StrictEnvelope
		text := toolText(t, invokeTool(t, server, ToolStrictQuery, args))
		if err := json.Unmarshal([]byte(text), &env); err != nil {
			t.Fatalf("envelope is not valid JSON: %v\n%s", err, text)
		}
		if env.Limitations == nil {
			t.Errorf("[%s] limitations is null; it must always be an array", tier)
		}
		return env
	}

	// Default admits everything: both callers survive, nothing is withheld.
	all := decode("")
	if all.Filter.ExcludedEdges != 0 {
		t.Errorf("default tier withheld %d edges, want 0", all.Filter.ExcludedEdges)
	}
	if len(all.Result.Edges) != 2 {
		t.Fatalf("default tier returned %d edges, want the 2 seeded callers", len(all.Result.Edges))
	}

	// confirmed-only drops the heuristic caller and MUST say so.
	strict := decode("confirmed")
	if len(strict.Result.Edges) != 1 {
		t.Errorf("confirmed-only returned %d edges, want 1", len(strict.Result.Edges))
	}
	if strict.Filter.ExcludedEdges != 1 {
		t.Errorf("confirmed-only withheld %d edges, want 1", strict.Filter.ExcludedEdges)
	}
	if len(strict.Limitations) == 0 {
		t.Fatal("edges were withheld but no limitation explains it — filtered emptiness must never read as proven emptiness")
	}
	if !strings.Contains(strict.Limitations[0], "not proven") {
		t.Errorf("limitation does not say the emptiness is filtered: %q", strict.Limitations[0])
	}
	// The surviving edge keeps its tier verbatim: filtering never relabels.
	if got := string(strict.Result.Edges[0].Tier); got != "confirmed" {
		t.Errorf("surviving edge tier = %q, want confirmed (filtering must not rewrite provenance)", got)
	}
}

// TestStrictQuery_InputErrors pins the closed operation set and the tier
// vocabulary. Every one of these is -32602 (input), never an empty success: a
// tool that answered "nothing found" to a malformed request would be
// indistinguishable from a genuine empty result.
func TestStrictQuery_InputErrors(t *testing.T) {
	c, target := strictQueryClient(t)
	server := NewServerWithClient(c, WithLabs())

	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"missing operation", map[string]any{"symbol": target}},
		{"missing symbol", map[string]any{"operation": "callers"}},
		{"analyzer is not a structural query", map[string]any{"operation": "analyze_taint", "symbol": target}},
		{"agent tool is not a structural query", map[string]any{"operation": "agent_brief", "symbol": target}},
		{"index is lifecycle, not a query", map[string]any{"operation": "index", "symbol": target}},
		{"invented operation", map[string]any{"operation": "yolo", "symbol": target}},
		{"invalid tier", map[string]any{"operation": "callers", "symbol": target, "minimum_tier": "probably"}},
		{"bare policy token", map[string]any{"operation": "callers", "symbol": target, "policy": "review"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := invokeTool(t, server, ToolStrictQuery, tc.args)
			if resp.Error == nil {
				t.Fatalf("expected an input error, got a result")
			}
			if resp.Error.Code != -32602 {
				t.Errorf("error code = %d, want -32602 (input)", resp.Error.Code)
			}
		})
	}
}

// TestStrictQuery_LabsMarkedInCatalog pins the tier tag. strict_query is not a
// Stable operation, so markLabs must prefix its description — an agent reading
// the catalog must not mistake it for part of the frozen surface.
func TestStrictQuery_LabsMarkedInCatalog(t *testing.T) {
	server := func() *Server { c, _ := strictQueryClient(t); return NewServerWithClient(c, WithLabs()) }()
	for _, d := range server.toolDescriptors() {
		if d["name"] != ToolStrictQuery {
			continue
		}
		desc, _ := d["description"].(string)
		if !strings.HasPrefix(desc, "[labs] ") {
			t.Fatalf("%s description is not marked [labs]: %q", ToolStrictQuery, desc)
		}
		return
	}
	t.Fatalf("%s missing from the Labs catalog", ToolStrictQuery)
}
