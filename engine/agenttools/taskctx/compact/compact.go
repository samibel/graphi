// Package compact builds the actor-visible task_context/2 MCP document.
//
// The taskctx assembler remains responsible for graph retrieval and its full
// provenance. This package performs the final bounded source recovery and
// transport projection: ranked source spans are embedded directly instead of
// repeating the item/evidence join inside a JSON string.
package compact

import (
	"context"
	"errors"
	"io/fs"

	compactv9 "github.com/samibel/graphi/engine/agenttools/taskctx/compact/v9"
)

const (
	// Version changes whenever source discovery, ordering, or wire semantics
	// change. It is deliberately separate from the retrieval method version.
	Version = "task_context/2-compact/3"
	// DefaultSourceBudget leaves room inside the frozen 1,200-token response
	// budget for JSON, citations, summary and provenance. The development
	// frontier selected 250 source whitespace-fields before productization.
	DefaultSourceBudget = 250
)

// ErrRetrievalNotReady tells a surface to preserve task_context/2's canonical
// lexical fallback instead of turning an expected degraded state into an RPC
// failure. Only ready retrieval results are eligible for compact projection.
var ErrRetrievalNotReady = errors.New("compact task_context: retrieval is not ready")

// Source is both source text and its exact repository-relative citation.
type Source struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Text      string `json:"text"`
}

// Provenance binds the compact projection to the complete retrieval result
// and records the independently auditable source policy.
type Provenance struct {
	InputSHA256     string `json:"input_sha256"`
	Method          string `json:"method"`
	Retrieval       string `json:"retrieval"`
	RetrievalState  string `json:"retrieval_state"`
	Weights         string `json:"weights"`
	Model           string `json:"model"`
	SourceSelection string `json:"source_selection"`
	SourceOrder     string `json:"source_order"`
	SourceBudget    int    `json:"source_budget"`
	BudgetUnit      string `json:"source_budget_unit"`
}

// Structured is emitted as MCP structuredContent.
type Structured struct {
	Version    string     `json:"version"`
	Sources    []Source   `json:"sources"`
	Provenance Provenance `json:"provenance"`
	Truncated  bool       `json:"truncated"`
}

// Result is the complete transport-neutral compact projection.
type Result struct {
	Summary    string
	Structured Structured
}

// Build projects one canonical task_context/2 contract plus a repository into
// compact source evidence using the preregistered V9 selector. Discovery is
// query-only: it has no answer-key or callback seam and completes before
// selection.
func Build(ctx context.Context, query string, legacy []byte, repository fs.FS, sourceBudget int) (Result, error) {
	summary, structured, err := compactv9.Build(ctx, query, legacy, repository, sourceBudget)
	if err != nil {
		if errors.Is(err, compactv9.ErrRetrievalNotReady) {
			return Result{}, ErrRetrievalNotReady
		}
		return Result{}, err
	}
	sources := make([]Source, 0, len(structured.Sources))
	for _, source := range structured.Sources {
		sources = append(sources, Source{Path: source.Path, StartLine: source.Start, EndLine: source.End, Text: source.Text})
	}
	p := structured.Provenance
	return Result{Summary: summary, Structured: Structured{
		Version: Version, Sources: sources, Truncated: structured.Truncated,
		Provenance: Provenance{
			InputSHA256: p.InputSHA256, Method: p.Method, Retrieval: p.Retrieval, RetrievalState: p.RetrievalState,
			Weights: p.Weights, Model: p.Model, SourceSelection: p.SourceSelection,
			SourceOrder: p.SourceOrder, SourceBudget: p.Budget, BudgetUnit: p.BudgetUnit,
		},
	}}, nil
}
