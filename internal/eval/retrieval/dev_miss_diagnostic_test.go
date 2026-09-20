package retrieval

// Temporary development diagnostic for the two surviving retrieval misses
// (cd-29 findFlag window, cd-87 legacy-bash flow span). Env-gated; not part
// of any gate. Delete after the diagnosis is recorded.

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/resolve"
	staticembed "github.com/samibel/graphi/engine/embed/static"
	"github.com/samibel/graphi/engine/query"
	engineretrieval "github.com/samibel/graphi/engine/retrieval"
)

func parseSpan(t *testing.T, span string) (int, int) {
	t.Helper()
	parts := strings.Split(span, "-")
	if len(parts) != 2 {
		t.Fatalf("span %q", span)
	}
	s, err := strconv.Atoi(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	e, err := strconv.Atoi(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	return s, e
}

func TestDevMissDiagnostic(t *testing.T) {
	if os.Getenv("GRAPHI_MISS_DIAG") == "" {
		t.Skip("set GRAPHI_MISS_DIAG=1")
	}
	root := os.Getenv("GRAPHI_PRODUCT_COMPACT_DEV_COBRA")
	if root == "" {
		t.Skip("set GRAPHI_PRODUCT_COMPACT_DEV_COBRA")
	}
	ctx := context.Background()
	idx, err := buildTaskContextIndex(ctx, root, t.TempDir(), staticembed.PinnedSelector, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.store.Close()
	qs := query.New(idx.store)
	engine := engineretrieval.New(resolve.Deps{Query: qs, Search: idx.search}, idx.search, idx.store)
	cases := []struct {
		id, text, path string
		s, e           int
	}{
		{"cd-29", "how does completion resolve a one-letter flag shorthand to its flag", "completions.go", 841, 858},
		{"cd-87", "how does legacy Bash completion serialize available subcommands and their aliases", "bash_completions.go", 447, 457},
	}
	for _, c := range cases {
		res, err := engine.Retrieve(ctx, engineretrieval.Request{Query: c.text, Limit: 50, Mode: engineretrieval.ModeAuto})
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("== %s strategy=%s rows=%d degradation=%s\n", c.id, res.Summary.Strategy, len(res.Rows), res.Degradation)
		found := false
		for i, r := range res.Rows {
			rs, re := parseSpan(t, r.Span)
			hit := r.Path == c.path && rs <= c.e && re >= c.s
			if hit {
				found = true
			}
			mark := "  "
			if hit {
				mark = "* "
			}
			fmt.Printf("%s%2d %-40s %-10s region=%-20s base=%d lex=%d sem=%d rrf=%d graph=%d\n",
				mark, i+1, r.Path, r.Span, r.Region, r.Explain.Base, r.Explain.LexicalRank, r.Explain.SemanticRank, r.Explain.RRF, r.Explain.Graph)
		}
		fmt.Printf("   reviewed span %s:%d-%d in top-50: %v\n", c.path, c.s, c.e, found)
		sem, err := idx.search.SemanticSearch(ctx, c.text, 50)
		if err != nil {
			t.Fatal(err)
		}
		semFound := false
		for i, h := range sem.Hits {
			near := h.SourcePath == c.path && h.Line >= c.s-20 && h.Line <= c.e+20
			if h.SourcePath == c.path {
				semFound = true
			}
			if near || h.SourcePath == c.path {
				fmt.Printf("   semantic hit %d: %s line=%d kind=%s name=%s score=%.4f near=%v\n",
					i+1, h.SourcePath, h.Line, h.Kind, h.QualifiedName, h.Score, near)
			}
		}
		fmt.Printf("   semantic top-50 lists %s: %v (state=%s available=%v)\n", c.path, semFound, sem.State, sem.Available)
	}
}
