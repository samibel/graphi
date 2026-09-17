package retrieval

// Answer-span feasibility against the frozen serialized ceiling.
//
// The 1,200-token candidate budget bounds what any task_context/2 projector
// can ever place in front of the actor. A reviewed grade-3 answer span that
// costs more than that budget, alone, in an otherwise empty response, can
// never be delivered complete — by this candidate or by any future one. That
// bound is a property of the dataset, the pinned checkout, the tokenizer and
// the ceiling, not of a candidate, so it is computed here once and read
// against every measurement.
//
// The computation consults judgements. It is therefore an evaluator-only
// instrument: no product path imports it, and its aggregate output carries no
// query identifier, question text, path or line range, so an independent
// curator can run it over a sealed holdout key and return the numbers without
// disclosing the key.

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"

	cltokenizer "github.com/samibel/graphi/internal/eval/tokenizer"
)

// AnswerSpanCeilingVersion identifies the computation so a later change to
// the overhead constant or the counting rule cannot be mistaken for the same
// bound.
const AnswerSpanCeilingVersion = "answer-span-ceiling/1"

// AnswerSpanResponseOverhead is the measured cl100k cost of one compact
// task_context/2 response with its source array removed: the JSON-RPC
// envelope, the one-line text fallback, the structured wrapper and the
// provenance block. It is the floor every response pays before it can quote a
// single line of the repository. It was measured on the production captures
// in docs/eval/retrieval/runs/2026-09-14-named-declaration-dev and is
// deliberately a constant: a bound that moved with the candidate would not be
// a bound.
const AnswerSpanResponseOverhead = 210

// AnswerSpanCeilingSplit is the aggregate for one split. Every field is a
// count; nothing in it identifies a query.
type AnswerSpanCeilingSplit struct {
	Split string `json:"split"`
	// Answerable is the number of queries with at least one grade-3 span.
	// no_hit queries and queries without a grade-3 judgement are excluded,
	// exactly as the answer-recovery aggregates exclude them.
	Answerable int `json:"answerable_queries"`
	Spans      int `json:"grade3_spans"`
	// SpansOverCeiling counts spans whose cost alone exceeds the ceiling.
	SpansOverCeiling int `json:"grade3_spans_over_ceiling"`
	// AnyCompleteFeasible is the number of answerable queries whose cheapest
	// grade-3 span fits: the upper bound on "at least one complete span".
	AnyCompleteFeasible int `json:"any_complete_feasible"`
	// AllCompleteFeasible is the number of answerable queries whose grade-3
	// spans all fit together: the upper bound on "every span complete".
	AllCompleteFeasible int `json:"all_complete_feasible"`
	// NoCompleteSpanPossible is Answerable - AnyCompleteFeasible, stated so a
	// reader does not have to subtract.
	NoCompleteSpanPossible int `json:"no_complete_span_possible"`
}

// AnswerSpanCeiling is the aggregate-only result.
type AnswerSpanCeiling struct {
	Version          string `json:"version"`
	DatasetID        string `json:"dataset_id"`
	DatasetSHA256    string `json:"dataset_sha256"`
	RepoSHA          string `json:"repo_sha"`
	CeilingTokens    int    `json:"ceiling_tokens"`
	OverheadTokens   int    `json:"response_overhead_tokens"`
	Grade            int    `json:"grade"`
	TokenizerID      string `json:"tokenizer_id"`
	VocabularySHA256 string `json:"vocabulary_sha256"`
	// Overall is the aggregate over every split; Splits is the same per
	// split, ordered by split name.
	Overall AnswerSpanCeilingSplit   `json:"overall"`
	Splits  []AnswerSpanCeilingSplit `json:"splits"`
}

// AnswerSpanCeilingDetail is the per-query view. It names queries, paths and
// lines and so must never leave the curator's machine for a sealed split; the
// CLI writes it only to a separately named file on explicit request.
type AnswerSpanCeilingDetail struct {
	QueryID          string                      `json:"query_id"`
	Split            string                      `json:"split"`
	Stratum          string                      `json:"stratum"`
	Spans            []AnswerSpanCeilingSpanCost `json:"spans"`
	CheapestResponse int                         `json:"cheapest_whole_response_tokens"`
	AllResponse      int                         `json:"all_spans_whole_response_tokens"`
	AnyFeasible      bool                        `json:"any_complete_feasible"`
	AllFeasible      bool                        `json:"all_complete_feasible"`
}

// AnswerSpanCeilingSpanCost is one span's exact source-entry cost.
type AnswerSpanCeilingSpanCost struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Lines     int    `json:"lines"`
	Tokens    int    `json:"source_entry_tokens"`
}

// ComputeAnswerSpanCeiling prices every grade-3 span in the dataset against
// the frozen ceiling using the embedded, SHA-verified tokenizer. The
// repository must be the pinned checkout the dataset was judged against; a
// span that does not resolve is an error, never a silent exclusion, because a
// missing span would make the bound look tighter than it is.
func ComputeAnswerSpanCeiling(repository fs.FS, loaded *Loaded) (AnswerSpanCeiling, []AnswerSpanCeilingDetail, error) {
	if loaded == nil || loaded.Dataset == nil {
		return AnswerSpanCeiling{}, nil, fmt.Errorf("answer-span ceiling: nil dataset")
	}
	tokenizer, err := cltokenizer.LoadEmbedded()
	if err != nil {
		return AnswerSpanCeiling{}, nil, fmt.Errorf("answer-span ceiling: load tokenizer: %w", err)
	}
	result := AnswerSpanCeiling{
		Version: AnswerSpanCeilingVersion, DatasetID: loaded.Dataset.ID, DatasetSHA256: loaded.SHA256,
		RepoSHA: loaded.Dataset.RepoSHA, CeilingTokens: SavingsCandidateBudget,
		OverheadTokens: AnswerSpanResponseOverhead, Grade: SavingsGrade,
		TokenizerID: cltokenizer.TokenizerID, VocabularySHA256: cltokenizer.PinnedVocabularySHA256,
		Overall: AnswerSpanCeilingSplit{Split: "all"},
	}
	bySplit := make(map[string]*AnswerSpanCeilingSplit)
	var details []AnswerSpanCeilingDetail
	for _, query := range loaded.Dataset.Queries {
		if query.Stratum == StratumNoHit {
			continue
		}
		detail := AnswerSpanCeilingDetail{QueryID: query.ID, Split: query.Split, Stratum: query.Stratum}
		cheapest, total := 0, 0
		for _, judgement := range query.Judgements {
			if judgement.Grade != SavingsGrade {
				continue
			}
			text, err := exactSourceSpan(repository, judgement.Path, judgement.StartLine, judgement.EndLine)
			if err != nil {
				return AnswerSpanCeiling{}, nil, fmt.Errorf("answer-span ceiling: query %s: %w", query.ID, err)
			}
			entry, err := json.Marshal(struct {
				Path  string `json:"path"`
				Start int    `json:"start_line"`
				End   int    `json:"end_line"`
				Text  string `json:"text"`
			}{judgement.Path, judgement.StartLine, judgement.EndLine, text})
			if err != nil {
				return AnswerSpanCeiling{}, nil, err
			}
			tokens, err := tokenizer.Count(entry)
			if err != nil {
				return AnswerSpanCeiling{}, nil, fmt.Errorf("answer-span ceiling: query %s: %w", query.ID, err)
			}
			detail.Spans = append(detail.Spans, AnswerSpanCeilingSpanCost{
				Path: judgement.Path, StartLine: judgement.StartLine, EndLine: judgement.EndLine,
				Lines: judgement.EndLine - judgement.StartLine + 1, Tokens: tokens,
			})
			total += tokens
			if cheapest == 0 || tokens < cheapest {
				cheapest = tokens
			}
		}
		if len(detail.Spans) == 0 {
			continue
		}
		detail.CheapestResponse = cheapest + AnswerSpanResponseOverhead
		detail.AllResponse = total + AnswerSpanResponseOverhead
		detail.AnyFeasible = detail.CheapestResponse <= SavingsCandidateBudget
		detail.AllFeasible = detail.AllResponse <= SavingsCandidateBudget
		details = append(details, detail)

		split := bySplit[query.Split]
		if split == nil {
			split = &AnswerSpanCeilingSplit{Split: query.Split}
			bySplit[query.Split] = split
		}
		for _, aggregate := range []*AnswerSpanCeilingSplit{split, &result.Overall} {
			aggregate.Answerable++
			aggregate.Spans += len(detail.Spans)
			for _, span := range detail.Spans {
				if span.Tokens+AnswerSpanResponseOverhead > SavingsCandidateBudget {
					aggregate.SpansOverCeiling++
				}
			}
			if detail.AnyFeasible {
				aggregate.AnyCompleteFeasible++
			} else {
				aggregate.NoCompleteSpanPossible++
			}
			if detail.AllFeasible {
				aggregate.AllCompleteFeasible++
			}
		}
	}
	names := make([]string, 0, len(bySplit))
	for name := range bySplit {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		result.Splits = append(result.Splits, *bySplit[name])
	}
	return result, details, nil
}
