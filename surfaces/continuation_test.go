// TODO-16 continuation, wire-level: a truncated STABLE call keeps `"next":""`
// (the frozen bytes), while a truncated LABS call carries the deterministic
// cap-rerun hint in limits.next. Both run through the shared Direct client —
// the same path CLI, MCP, and HTTP serialize.
package surfaces_test

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/client"
)

func TestContinuation_StableStaysEmpty_LabsCarriesHint(t *testing.T) {
	store, _ := seed(t)
	c := client.NewDirect(query.New(store), search.New(store))
	ctx := context.Background()

	// Stable: explain_symbol forced into truncation (the hero-17 shape).
	stable, err := c.ExplainSymbol(ctx, "p.B", 1)
	if err != nil {
		t.Fatalf("explain_symbol: %v", err)
	}
	if !strings.Contains(string(stable), `"truncated":true`) {
		t.Fatalf("expected a truncated stable call (max_items=1): %s", stable)
	}
	if !strings.Contains(string(stable), `"next":""`) {
		t.Fatalf("STABLE-FREEZE RED: truncated stable call no longer carries \"next\":\"\": %s", stable)
	}

	// Labs: repo_overview forced into truncation carries the cap hint.
	labs, err := c.RepoOverview(ctx, client.RepoOverviewParams{MaxItems: 1})
	if err != nil {
		t.Fatalf("repo_overview: %v", err)
	}
	if !strings.Contains(string(labs), `"truncated":true`) {
		t.Fatalf("expected a truncated labs call (max_items=1): %s", labs)
	}
	if !strings.Contains(string(labs), `"next":"raise limit (>=`) {
		t.Fatalf("truncated labs call must carry the cap-rerun hint in limits.next: %s", labs)
	}
}
