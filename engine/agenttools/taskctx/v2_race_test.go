package taskctx_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/taskctx"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// TestTaskContextV2_ConcurrentCallsKeepTheirOwnSpans proves that two
// simultaneous AssembleV2 calls do not read each other's retrieval rows.
// The MCP and HTTP surfaces both serve concurrent requests, so this is the
// shape of a real request pair, not a synthetic one.
func TestTaskContextV2_ConcurrentCallsKeepTheirOwnSpans(t *testing.T) {
	store := fixtureGraph(t)
	t.Cleanup(func() { _ = store.Close() })
	validatorID := string(mustLookupQualified(t, store, "auth.TokenValidator").ID())

	// Two callers, same seed node, deliberately different spans.
	mk := func(span string) resolve.Deps {
		return resolve.Deps{
			Query:  query.New(store),
			Search: search.New(store),
			Retrieval: &stubRetriever{
				state: "ready", strategy: "semantic_first",
				rows: []resolve.RetrieverRow{{
					NodeID: validatorID, DocumentID: "doc-1",
					Path: "auth/token_validator.go", Span: span,
					Region: "semantic_prefix", Final: 2000,
					Explain: resolve.RetrieverExplain{Final: 2000, SemanticRank: 1, RRF: 16666},
				}},
			},
		}
	}

	const iterations = 200
	var wg sync.WaitGroup
	bad := make(chan string, 2*iterations)

	run := func(span string) {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			res, err := taskctx.AssembleV2(context.Background(), taskctx.Params{
				Task: "anything", TokenBudget: 0, Deps: mk(span),
			})
			if err != nil {
				bad <- "error: " + err.Error()
				return
			}
			for _, ev := range res.Evidence {
				// Any span present must be this caller's own.
				if ev.Span != "" && ev.Span != span {
					bad <- "caller wanted span " + span + " but read " + ev.Span
					return
				}
			}
		}
	}

	wg.Add(2)
	go run("10-10")
	go run("77-77")
	wg.Wait()
	close(bad)

	var seen []string
	for m := range bad {
		seen = append(seen, m)
	}
	if len(seen) > 0 {
		t.Fatalf("concurrent AssembleV2 calls crossed evidence (%d observations); first: %s",
			len(seen), strings.Join(seen[:1], ""))
	}
}
