package hybridsearch

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// fixtureDeps builds an auth-shaped graph: TokenValidator is the on-topic,
// well-connected symbol; TokenBucket is an off-topic namesake; validateInput
// matches only by prefix.
func fixtureDeps(t *testing.T) resolve.Deps {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()

	mk := func(kind, qn, path string, line int) model.Node {
		n, err := model.NewNode(kind, qn, path, line, 1)
		if err != nil {
			t.Fatalf("node %s: %v", qn, err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("put node %s: %v", qn, err)
		}
		return n
	}
	validator := mk("type", "auth.TokenValidator", "auth/token_validator.go", 10)
	filter := mk("type", "auth.AuthFilter", "auth/filter.go", 5)
	bucket := mk("type", "rate.TokenBucket", "rate/bucket.go", 3)
	mk("function", "input.validateInput", "input/validate.go", 7)
	handler := mk("function", "web.LoginHandler", "web/login.go", 20)

	edge := func(from, to model.Node, ev string) {
		e, err := model.NewEdge(from.ID(), to.ID(), "calls", model.TierConfirmed, 0.9, "test fixture", []string{ev})
		if err != nil {
			t.Fatalf("edge: %v", err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatalf("put edge: %v", err)
		}
	}
	// TokenValidator is central: called by the filter and the handler.
	edge(filter, validator, "auth/filter.go:8")
	edge(handler, validator, "web/login.go:22")
	edge(handler, filter, "web/login.go:21")
	_ = bucket

	return resolve.Deps{Query: query.New(store), Search: search.New(store)}
}

func TestSearchRanksOnTopicSymbolsFirst(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Search(context.Background(), Params{Query: "authentication token validation", Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("expected found, got %s (%s)", res.Outcome, res.Summary)
	}
	if !strings.Contains(res.Summary, "top auth.TokenValidator") {
		t.Fatalf("TokenValidator must rank first: %q", res.Summary)
	}
	if !strings.Contains(res.Summary, MethodVersion) || !strings.Contains(res.Summary, "weights "+WeightsHash()) {
		t.Fatalf("summary must stamp method + weights: %q", res.Summary)
	}

	// The off-topic namesake (TokenBucket) must rank strictly below the
	// validator, and every reason must carry the signal breakdown.
	var validatorRank, bucketRank int
	for _, it := range res.Items {
		if !strings.Contains(it.Reason, "[tokens ") {
			t.Fatalf("reason missing breakdown: %q", it.Reason)
		}
		if strings.Contains(it.Reason, "auth.TokenValidator") {
			validatorRank = it.Rank
		}
		if strings.Contains(it.Reason, "rate.TokenBucket") {
			bucketRank = it.Rank
		}
	}
	if validatorRank == 0 {
		t.Fatal("validator missing from results")
	}
	if bucketRank >= validatorRank {
		t.Fatalf("off-topic namesake must rank below: bucket %d vs validator %d", bucketRank, validatorRank)
	}
	if err := contract.ValidateResult(res); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
}

func TestSearchCamelCaseSegments(t *testing.T) {
	got := splitIdentifier("auth.HTTPTokenValidator_v2")
	want := []string{"auth", "http", "token", "validator", "v2"}
	if len(got) != len(want) {
		t.Fatalf("segments = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("segments = %v, want %v", got, want)
		}
	}
}

func TestSearchEmptyAndUnavailable(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Search(context.Background(), Params{Query: "zzz qqq nothing", Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeEmpty {
		t.Fatalf("expected empty, got %s", res.Outcome)
	}

	res, err = Search(context.Background(), Params{Query: "x1", Deps: resolve.Deps{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeUnavailable {
		t.Fatalf("expected unavailable, got %s", res.Outcome)
	}

	if _, err := Search(context.Background(), Params{Query: "   ", Deps: deps}); err == nil {
		t.Fatal("blank query must error")
	}
}

func TestSearchDeterministic(t *testing.T) {
	deps := fixtureDeps(t)
	run := func() []byte {
		res, err := Search(context.Background(), Params{Query: "token validator auth", Deps: deps})
		if err != nil {
			t.Fatal(err)
		}
		b, err := contract.Serialize(res)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	a, b := run(), run()
	if !bytes.Equal(a, b) {
		t.Fatalf("non-deterministic output:\n%s\n%s", a, b)
	}
}
