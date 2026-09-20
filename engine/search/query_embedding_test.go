package search_test

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/search"
)

type asymmetricEmbedder struct {
	t     *testing.T
	query string
}

func (e *asymmetricEmbedder) ID() string { return "asymmetric-test:query-v1" }
func (e *asymmetricEmbedder) Dim() int   { return 2 }
func (e *asymmetricEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	e.t.Fatal("search used the document path")
	return nil, nil
}
func (e *asymmetricEmbedder) EmbedQuery(_ context.Context, q string) ([][]float32, error) {
	e.query = q
	return [][]float32{{1, 0}}, nil
}

func TestSemanticSearchUsesQueryEmbeddingSeam(t *testing.T) {
	st := graphstore.NewMemStore()
	defer st.Close()
	e := &asymmetricEmbedder{t: t}
	reg := embed.NewRegistry()
	if err := reg.Register(e); err != nil {
		t.Fatal(err)
	}
	reg.Freeze()
	svc := search.New(st).WithSemantic(reg, embed.NewIndex(), st)
	if _, err := svc.SemanticSearch(t.Context(), "the original question", 10); err != nil {
		t.Fatal(err)
	}
	if e.query != "the original question" {
		t.Fatalf("wrong query %q", e.query)
	}
}
