package surfaces_test

import (
	"context"
	"errors"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/surfaces/client"
)

// TestCAP01_StableOps_ServedByPorts_NoStubs is the CAP-01 (SW-117) exit gate:
// every client-served stable operation is driven through the SMALLEST
// consumer-owned port — the variables below are TYPED as the ports, so this
// test compiles only if the ports suffice — against the production stable
// wiring (charClient over the pinned fixture), and none of them answers with
// an Unavailable stub. `index` is the twelfth op: it is the fixture ingest
// itself (indexCharFixture), which this test performs to have a graph at all.
func TestCAP01_StableOps_ServedByPorts_NoStubs(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	indexCharFixture(t, store) // stable op 1: index
	c := charClient(store)
	helloID := findFuncID(t, store, "Hello")

	// The consumer-owned views. Assigning Direct to them is the isolation
	// proof; every call below goes through the port type, never client.Client.
	var (
		qp client.QueryPort        = c
		sp client.SearchPort       = c
		ap client.AgentContextPort = c
	)

	// unavailableStub matches every capability-unavailable sentinel the labs
	// facade can raise. A stable op returning ANY of these is a CAP-01 red.
	unavailableStub := func(err error) bool {
		for _, sentinel := range []error{
			client.ErrSearchUnavailable, client.ErrAnalysisUnavailable,
			client.ErrBriefUnavailable, client.ErrAgentToolsUnavailable,
			client.ErrEditUnavailable, client.ErrMemoryUnavailable,
			client.ErrDistillUnavailable, client.ErrSkillGenUnavailable,
			client.ErrReviewUnavailable, client.ErrForgeUnavailable,
			client.ErrCompareUnavailable, client.ErrSavingsUnavailable,
			client.ErrReviewFetchUnavailable,
		} {
			if errors.Is(err, sentinel) {
				return true
			}
		}
		return false
	}
	check := func(op string, b []byte, err error) {
		t.Helper()
		if err != nil {
			if unavailableStub(err) {
				t.Fatalf("CAP-01 RED: stable op %q answered with an Unavailable stub: %v", op, err)
			}
			t.Fatalf("stable op %q failed: %v", op, err)
		}
		if len(b) == 0 {
			t.Fatalf("stable op %q returned an empty payload", op)
		}
	}

	// QueryPort: the five structural ops + impact via the analyzer dispatch.
	for _, op := range []string{"definition", "callers", "callees", "references", "neighborhood"} {
		b, err := qp.Query(ctx, op, helloID, 1)
		check(op, b, err)
	}
	b, err := qp.Analyze(ctx, client.AnalyzeParams{Name: "impact", Symbol: helloID})
	check("impact", b, err)

	// SearchPort: the stable lexical search.
	b, err = sp.Search(ctx, "Hello", 10)
	check("search", b, err)

	// AgentContextPort: the four agent-first ops.
	jb, _, err := ap.Brief(ctx, "Hello")
	check("agent_brief", jb, err)
	b, err = ap.ExplainSymbol(ctx, helloID, 5)
	check("explain_symbol", b, err)
	b, err = ap.RelatedFiles(ctx, helloID, "both", 5)
	check("related_files", b, err)
	b, err = ap.ChangeRisk(ctx, helloID, "", 5)
	check("change_risk", b, err)
}
