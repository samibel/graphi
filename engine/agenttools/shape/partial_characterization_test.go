package shape

// SW-134 characterization baseline: `shape.Finish` is the ONLY production site
// that can set outcome "partial" for explain_symbol / change_risk /
// related_files (`grep -rn OutcomePartial` over the non-test tree yields exactly
// contract.go's constant, shape.go:171, and brief.go:161 — brief.go being
// agent_brief's own producer, which the harness already accepts).
//
// These tests pin the current behavior; they do not endorse or condemn it. The
// downgrade is designed and GA-frozen — corpus/hero/hero-17-explain-symbol-partial.yaml
// asserts it end to end ("truncation is never silent").

import (
	"testing"

	"github.com/samibel/graphi/engine/agenttools/contract"
)

// nItems builds n distinct, deterministically ordered items.
func nItems(n int) []contract.Item {
	out := make([]contract.Item, 0, n)
	for i := range n {
		out = append(out, contract.Item{
			RefID:  string(rune('a'+i%26)) + string(rune('0'+i/26)),
			Rank:   n - i,
			Reason: "fixture item",
		})
	}
	return out
}

// The downgrade is a pure function of (outcome, item count, cap). Nothing about
// timing, machine or repository enters it — which is why the shortfall the P0
// baseline recorded is identical on two CPU families.
func TestFinish_PartialIsProducedExactlyByCapTruncationOfAFoundResult(t *testing.T) {
	cases := []struct {
		name      string
		outcome   contract.Outcome
		items     int
		cap       int
		want      contract.Outcome
		truncated bool
		dropped   int
	}{
		// The eval harness's cap for all three operations is 10
		// (engine/scenario/fixture.go:292,295,297), so 10 is the boundary the
		// P0 baseline actually measured against.
		{"one over the harness cap downgrades", contract.OutcomeFound, 11, 10, contract.OutcomePartial, true, 1},
		{"exactly at the harness cap does not", contract.OutcomeFound, 10, 10, contract.OutcomeFound, false, 0},
		{"under the harness cap does not", contract.OutcomeFound, 3, 10, contract.OutcomeFound, false, 0},

		// A caller that passes no cap — which is every shipped surface: the CLI
		// flag defaults to 0 (surfaces/cli/cli.go:627) and MCP's absent limit
		// derefs to 0 (surfaces/mcp/toolcalls.go:641) — gets DefaultMaxItems.
		{"one over the default cap downgrades", contract.OutcomeFound, DefaultMaxItems + 1, 0, contract.OutcomePartial, true, 1},
		{"exactly at the default cap does not", contract.OutcomeFound, DefaultMaxItems, 0, contract.OutcomeFound, false, 0},
		{"negative cap selects the default too", contract.OutcomeFound, DefaultMaxItems + 5, -3, contract.OutcomePartial, true, 5},

		// Only "found" is downgraded. An ambiguous or empty envelope keeps its
		// own outcome even when the cap bites, so neither can masquerade as the
		// partial the baseline observed.
		{"ambiguous is not downgraded", contract.OutcomeAmbiguous, 11, 10, contract.OutcomeAmbiguous, true, 1},
		{"empty is not downgraded", contract.OutcomeEmpty, 0, 10, contract.OutcomeEmpty, false, 0},
		{"unavailable is not downgraded", contract.OutcomeUnavailable, 0, 10, contract.OutcomeUnavailable, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := &contract.Result{Outcome: tc.outcome, Items: nItems(tc.items)}
			got, err := Finish(in, tc.cap)
			if err != nil {
				t.Fatalf("Finish: %v", err)
			}
			if got.Outcome != tc.want {
				t.Errorf("outcome = %q, want %q", got.Outcome, tc.want)
			}
			if got.Limits.Truncated != tc.truncated {
				t.Errorf("Limits.Truncated = %v, want %v", got.Limits.Truncated, tc.truncated)
			}
			if got.Limits.Dropped != tc.dropped {
				t.Errorf("Limits.Dropped = %d, want %d", got.Limits.Dropped, tc.dropped)
			}
			if got.Limits.TotalAvailable != tc.items {
				t.Errorf("Limits.TotalAvailable = %d, want %d", got.Limits.TotalAvailable, tc.items)
			}
		})
	}
}

// DefaultMaxItems is what every shipped surface resolves to and the eval
// harness never reaches. Pinning the constant makes the 10-vs-20 divergence
// asserted in engine/scenario a two-sided fact rather than one side's claim.
func TestDefaultMaxItems_IsTwenty(t *testing.T) {
	if DefaultMaxItems != 20 {
		t.Fatalf("DefaultMaxItems = %d, want 20", DefaultMaxItems)
	}
}
