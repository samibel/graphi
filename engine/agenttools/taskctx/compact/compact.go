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
	Version = compactv9.CompactTaskContextVersion
	// DefaultSourceBudget is the measured source-field frontier inside the
	// frozen 1,200-token serialized response ceiling. The projector still counts
	// the real wire and deterministically backs off when JSON and provenance make
	// a particular response exceed that ceiling.
	DefaultSourceBudget = 325
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

// Followup designates the one exact span a reader should fetch next when the
// lead source is a cut window into a larger declaration or section. It is a
// citation, not bytes; the read is charged by whoever performs it.
type Followup struct {
	Path      string
	StartLine int
	EndLine   int
}

// MarshalJSON writes the citation form `path:start-end`, byte-identical to
// the production selector's wire.
func (f Followup) MarshalJSON() ([]byte, error) {
	return compactv9.CompactTaskContextFollowup{Path: f.Path, Start: f.StartLine, End: f.EndLine}.MarshalJSON()
}

// UnmarshalJSON accepts the citation form only.
func (f *Followup) UnmarshalJSON(raw []byte) error {
	var inner compactv9.CompactTaskContextFollowup
	if err := inner.UnmarshalJSON(raw); err != nil {
		return err
	}
	*f = Followup{Path: inner.Path, StartLine: inner.Start, EndLine: inner.End}
	return nil
}

// Structured is emitted as MCP structuredContent.
type Structured struct {
	Version    string     `json:"version"`
	Sources    []Source   `json:"sources"`
	Provenance Provenance `json:"provenance"`
	Truncated  bool       `json:"truncated"`
	Followup   *Followup  `json:"followup,omitempty"`
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
	var followup *Followup
	if f := structured.Followup; f != nil {
		followup = &Followup{Path: f.Path, StartLine: f.Start, EndLine: f.End}
	}
	return Result{Summary: summary, Structured: Structured{
		Version: Version, Sources: sources, Truncated: structured.Truncated, Followup: followup,
		Provenance: Provenance{
			InputSHA256: p.InputSHA256, Method: p.Method, Retrieval: p.Retrieval, RetrievalState: p.RetrievalState,
			Weights: p.Weights, Model: p.Model, SourceSelection: p.SourceSelection,
			SourceOrder: p.SourceOrder, SourceBudget: p.Budget, BudgetUnit: p.BudgetUnit,
		},
	}}, nil
}
