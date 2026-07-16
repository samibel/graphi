package surfaces_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/surfaces/client"
)

func TestCAP01_StableClientMethodSetExcludesLabsSelectors(t *testing.T) {
	typeOfStable := reflect.TypeOf((*client.StableClient)(nil)).Elem()
	for _, forbidden := range []string{"Analyze", "SemanticSearch"} {
		if _, ok := typeOfStable.MethodByName(forbidden); ok {
			t.Fatalf("StableClient must not expose Labs selector %s", forbidden)
		}
	}
	for _, required := range []string{"Query", "Impact", "Search", "Brief", "ExplainSymbol", "RelatedFiles", "ChangeRisk"} {
		if _, ok := typeOfStable.MethodByName(required); !ok {
			t.Fatalf("StableClient missing required stable method %s", required)
		}
	}
}

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

	// The consumer-owned Stable view is capability-narrowed by construction;
	// every call below goes through that type, never client.Client.
	var (
		stable client.StableClient = client.AsStable(c)
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

	// StableQueryPort: the five structural ops plus the dedicated impact port.
	for _, op := range []string{"definition", "callers", "callees", "references", "neighborhood"} {
		b, err := stable.Query(ctx, op, helloID, 1)
		check(op, b, err)
	}
	b, err := stable.Impact(ctx, client.ImpactParams{Symbol: helloID})
	check("impact", b, err)

	// StableSearchPort: lexical search only; SemanticSearch is not in scope.
	b, err = stable.Search(ctx, "Hello", 10)
	check("search", b, err)

	// AgentContextPort: the four agent-first ops.
	jb, _, err := stable.Brief(ctx, "Hello")
	check("agent_brief", jb, err)
	b, err = stable.ExplainSymbol(ctx, helloID, 5)
	check("explain_symbol", b, err)
	b, err = stable.RelatedFiles(ctx, helloID, "both", 5)
	check("related_files", b, err)
	b, err = stable.ChangeRisk(ctx, helloID, "", 5)
	check("change_risk", b, err)
}
