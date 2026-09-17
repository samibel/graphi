package main

// `-answer-span-ceiling` — price a dataset's reviewed answer spans against the
// frozen 1,200-token candidate budget and report, as counts only, how many
// questions can ever carry a complete answer span.
//
// The bound is a property of the dataset, the pinned checkout, the tokenizer
// and the ceiling, not of a candidate. It exists so that a pre-registered pass
// count can be read against what the ceiling permits at all, and so that an
// independent curator can compute it over a sealed holdout key and hand back
// numbers without handing back the key: the report written to -out names no
// query, no path and no line. Per-query detail is written only when
// -answer-span-detail names a separate file, and that file is for the
// curator's eyes on a sealed split.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/samibel/graphi/internal/eval/retrieval"
)

type answerSpanOptions struct {
	dataset  string
	checkout string
	out      string
	detail   string
}

func runAnswerSpanCeiling(o answerSpanOptions, stdout, stderr io.Writer) int {
	if o.dataset == "" || o.checkout == "" {
		fmt.Fprintln(stderr, "retrieval-eval: -answer-span-ceiling needs -dataset and -checkout")
		return exitUsage
	}
	loaded, err := retrieval.LoadDataset(o.dataset)
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitUsage
	}
	// A dataset that cites a sha is priced only against a checkout at that
	// sha; the in-tree fixture cites none and is pinned by the tree itself.
	if loaded.Dataset.RepoSHA != "" {
		head, err := retrieval.CheckoutHEAD(context.Background(), o.checkout)
		if err != nil {
			fmt.Fprintf(stderr, "retrieval-eval: answer-span ceiling: %v\n", err)
			return exitUsage
		}
		if !strings.EqualFold(head, loaded.Dataset.RepoSHA) {
			fmt.Fprintf(stderr, "retrieval-eval: answer-span ceiling: dataset %s cites sha %s but the checkout is at %s\n", o.dataset, loaded.Dataset.RepoSHA, head)
			return exitUsage
		}
	}
	if err := retrieval.CheckSpanCoverage(o.checkout, loaded.Dataset); err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: answer-span ceiling: %v\n", err)
		return exitUsage
	}
	ceiling, details, err := retrieval.ComputeAnswerSpanCeiling(os.DirFS(o.checkout), loaded)
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	raw, err := json.MarshalIndent(ceiling, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	raw = append(raw, '\n')
	if o.out != "" {
		if err := os.WriteFile(o.out, raw, 0o644); err != nil {
			fmt.Fprintf(stderr, "retrieval-eval: write %s: %v\n", o.out, err)
			return exitError
		}
	} else {
		fmt.Fprint(stdout, string(raw))
	}
	if o.detail != "" {
		detailRaw, err := json.MarshalIndent(details, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
			return exitError
		}
		if err := os.WriteFile(o.detail, append(detailRaw, '\n'), 0o600); err != nil {
			fmt.Fprintf(stderr, "retrieval-eval: write %s: %v\n", o.detail, err)
			return exitError
		}
		fmt.Fprintf(stderr, "retrieval-eval: per-query detail written to %s; it names queries, paths and lines and must not leave a sealed split's custody\n", o.detail)
	}
	for _, split := range append([]retrieval.AnswerSpanCeilingSplit{ceiling.Overall}, ceiling.Splits...) {
		fmt.Fprintf(stderr, "retrieval-eval: answer-span ceiling %-8s answerable=%d spans=%d over_ceiling=%d any_complete_feasible=%d/%d all_complete_feasible=%d/%d\n",
			split.Split, split.Answerable, split.Spans, split.SpansOverCeiling, split.AnyCompleteFeasible, split.Answerable, split.AllCompleteFeasible, split.Answerable)
	}
	return exitOK
}
