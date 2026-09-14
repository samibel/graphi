package retrieval

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAnswerSpanFeasibilityAgainstFrozenCeilingDev pins the development
// split's feasibility bounds. They are what a development measurement must be
// read against: they move only when the dataset, the pinned checkout, the
// tokenizer or the frozen ceiling moves, and each of those invalidates the
// recorded measurement.
//
// It is opt-in because it needs the pinned repository checkout, and it never
// opens the combined dataset holding the spent holdout.
func TestAnswerSpanFeasibilityAgainstFrozenCeilingDev(t *testing.T) {
	root := os.Getenv("GRAPHI_PRODUCT_COMPACT_DEV_COBRA")
	if root == "" {
		t.Skip("set GRAPHI_PRODUCT_COMPACT_DEV_COBRA to the pinned Cobra checkout")
	}
	moduleRoot := taskContextModuleRoot(t)
	loaded, err := LoadDataset(filepath.Join(moduleRoot, "docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/dataset.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range loaded.Dataset.Queries {
		if query.Split != SplitDev {
			t.Fatalf("refusing non-development query %s", query.ID)
		}
	}
	ceiling, details, err := ComputeAnswerSpanCeiling(os.DirFS(root), loaded)
	if err != nil {
		t.Fatal(err)
	}
	var unreachable []string
	for _, detail := range details {
		if !detail.AnyFeasible {
			unreachable = append(unreachable, detail.QueryID+" ["+detail.Stratum+"]")
		}
	}
	all := ceiling.Overall
	t.Logf("answer-span feasibility at %d cl100k tokens: answerable=%d spans=%d spans_over_ceiling=%d any_complete_feasible=%d/%d all_complete_feasible=%d/%d no_complete_span_possible=%v",
		ceiling.CeilingTokens, all.Answerable, all.Spans, all.SpansOverCeiling, all.AnyCompleteFeasible, all.Answerable, all.AllCompleteFeasible, all.Answerable, unreachable)
	if all.Answerable != 40 || all.Spans != 63 {
		t.Fatalf("development answer population changed: %d queries, %d grade-3 spans", all.Answerable, all.Spans)
	}
	if all.AnyCompleteFeasible != 35 || all.AllCompleteFeasible != 33 || all.SpansOverCeiling != 7 {
		t.Fatalf("recorded feasibility ceiling moved: any=%d/40 all=%d/40 over=%d, recorded 35/40, 33/40 and 7", all.AnyCompleteFeasible, all.AllCompleteFeasible, all.SpansOverCeiling)
	}
	if len(unreachable) != 5 || all.NoCompleteSpanPossible != 5 {
		t.Fatalf("recorded unreachable development questions changed: %v", unreachable)
	}
}
