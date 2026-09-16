package embed

import (
	"context"
	"fmt"
)

// QueryEmbedder optionally prepares search queries differently from documents.
// The adapter owns preparation and includes its policy in its stable ID. Embed
// continues to consume admitted document bytes unchanged. Existing symmetric
// embedders need not implement this interface.
type QueryEmbedder interface {
	EmbedQuery(context.Context, string) ([][]float32, error)
}

// QueryEmbedding is one atomically observed query embedding. UnknownTokens is
// nil when the active provider cannot expose the count; a non-nil zero is an
// observed zero. Providers must derive the count from the exact prepared token
// stream that produced Vectors, never from a second tokenizer pass.
type QueryEmbedding struct {
	Vectors       [][]float32
	UnknownTokens *int
}

// DiagnosticQueryEmbedder optionally returns query diagnostics from the same
// provider operation that produced the vector.
type DiagnosticQueryEmbedder interface {
	EmbedQueryWithDiagnostics(context.Context, string) (QueryEmbedding, error)
}

// EmbedQuery is the shared query seam for production and evaluation search.
func EmbedQuery(ctx context.Context, e Embedder, query string) ([][]float32, error) {
	result, err := EmbedQueryWithDiagnostics(ctx, e, query)
	if err != nil {
		return nil, err
	}
	return result.Vectors, nil
}

// EmbedQueryWithDiagnostics is the attest-on-every-query seam for an atomic
// vector plus provider-owned diagnostics. Legacy embedders remain supported;
// their UnknownTokens field is nil rather than an invented zero.
func EmbedQueryWithDiagnostics(ctx context.Context, e Embedder, query string) (QueryEmbedding, error) {
	if err := VerifyRuntime(ctx, e, "before query embed"); err != nil {
		return QueryEmbedding{}, err
	}
	var result QueryEmbedding
	var err error
	if q, ok := e.(DiagnosticQueryEmbedder); ok {
		result, err = q.EmbedQueryWithDiagnostics(ctx, query)
	} else if q, ok := e.(QueryEmbedder); ok {
		result.Vectors, err = q.EmbedQuery(ctx, query)
	} else {
		result.Vectors, err = e.Embed(ctx, []string{query})
	}
	if err != nil {
		return QueryEmbedding{}, err
	}
	if err := VerifyRuntime(ctx, e, "after query embed"); err != nil {
		return QueryEmbedding{}, err
	}
	if result.UnknownTokens != nil {
		if *result.UnknownTokens < 0 {
			return QueryEmbedding{}, fmt.Errorf("embed: query unknown-token count is negative: %d", *result.UnknownTokens)
		}
		count := *result.UnknownTokens
		result.UnknownTokens = &count
	}
	return result, nil
}
