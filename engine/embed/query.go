package embed

import "context"

// QueryEmbedder optionally prepares search queries differently from documents.
// The adapter owns preparation and includes its policy in its stable ID. Embed
// continues to consume admitted document bytes unchanged. Existing symmetric
// embedders need not implement this interface.
type QueryEmbedder interface {
	EmbedQuery(context.Context, string) ([][]float32, error)
}

// EmbedQuery is the shared query seam for production and evaluation search.
func EmbedQuery(ctx context.Context, e Embedder, query string) ([][]float32, error) {
	if err := VerifyRuntime(ctx, e, "before query embed"); err != nil {
		return nil, err
	}
	var vectors [][]float32
	var err error
	if q, ok := e.(QueryEmbedder); ok {
		vectors, err = q.EmbedQuery(ctx, query)
	} else {
		vectors, err = e.Embed(ctx, []string{query})
	}
	if err != nil {
		return nil, err
	}
	if err := VerifyRuntime(ctx, e, "after query embed"); err != nil {
		return nil, err
	}
	return vectors, nil
}
