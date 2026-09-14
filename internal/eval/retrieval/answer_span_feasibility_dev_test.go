package retrieval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cltokenizer "github.com/samibel/graphi/internal/eval/tokenizer"
)

// answerSpanResponseOverhead is the measured cl100k cost of one compact
// task_context/2 response with its source array removed: the JSON-RPC
// envelope, the one-line text fallback, the structured wrapper and the
// provenance block. It is the floor every response pays before it can quote a
// single line of the repository.
const answerSpanResponseOverhead = 210

// TestAnswerSpanFeasibilityAgainstFrozenCeilingDev prices every development
// grade-3 answer span against the frozen 1,200-token serialized ceiling and
// reports how many development questions could, at best, ever carry a complete
// answer span.
//
// This is a property of the dataset and the ceiling, not of any candidate: it
// bounds from above what every present and future projector can reach, so a
// development measurement below that bound can be read as a real gap and a
// measurement at that bound can be read as saturation. It consults judgements
// and is therefore a development diagnostic, never a product input; the
// projector has no access to anything computed here.
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
	tokenizer, err := cltokenizer.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	repository := os.DirFS(root)
	spanTokens := func(path string, start, end int) int {
		text, err := exactSourceSpan(repository, path, start, end)
		if err != nil {
			t.Fatalf("%s:%d-%d: %v", path, start, end, err)
		}
		encoded, err := json.Marshal(struct {
			Path  string `json:"path"`
			Start int    `json:"start_line"`
			End   int    `json:"end_line"`
			Text  string `json:"text"`
		}{path, start, end, text})
		if err != nil {
			t.Fatal(err)
		}
		tokens, err := tokenizer.Count(encoded)
		if err != nil {
			t.Fatal(err)
		}
		return tokens
	}
	answerable, anyFeasible, allFeasible, spans, spansOverCeiling := 0, 0, 0, 0, 0
	var unreachable []string
	for _, query := range loaded.Dataset.Queries {
		if query.Stratum == StratumNoHit {
			continue
		}
		cheapest, total, judged := 0, 0, 0
		for _, judgement := range query.Judgements {
			if judgement.Grade != SavingsGrade {
				continue
			}
			tokens := spanTokens(judgement.Path, judgement.StartLine, judgement.EndLine)
			judged++
			spans++
			total += tokens
			if cheapest == 0 || tokens < cheapest {
				cheapest = tokens
			}
			if tokens+answerSpanResponseOverhead > SavingsCandidateBudget {
				spansOverCeiling++
			}
		}
		if judged == 0 {
			continue
		}
		answerable++
		if cheapest+answerSpanResponseOverhead <= SavingsCandidateBudget {
			anyFeasible++
		} else {
			unreachable = append(unreachable, query.ID+" ["+string(query.Stratum)+"]")
		}
		if total+answerSpanResponseOverhead <= SavingsCandidateBudget {
			allFeasible++
		}
	}
	t.Logf("answer-span feasibility at %d cl100k tokens: answerable=%d spans=%d spans_over_ceiling=%d any_complete_feasible=%d/%d all_complete_feasible=%d/%d no_complete_span_possible=%v",
		SavingsCandidateBudget, answerable, spans, spansOverCeiling, anyFeasible, answerable, allFeasible, answerable, unreachable)
	if answerable != 40 || spans != 63 {
		t.Fatalf("development answer population changed: %d queries, %d grade-3 spans", answerable, spans)
	}
	// These two bounds are what a development measurement must be read against.
	// They move only when the dataset, the pinned checkout or the frozen
	// ceiling moves, and each of those invalidates the recorded measurement.
	if anyFeasible != 35 || allFeasible != 33 {
		t.Fatalf("recorded feasibility ceiling moved: any=%d/40 all=%d/40, recorded 35/40 and 33/40", anyFeasible, allFeasible)
	}
	if strings.Count(strings.Join(unreachable, " "), "[") != 5 {
		t.Fatalf("recorded unreachable development questions changed: %v", unreachable)
	}
}
