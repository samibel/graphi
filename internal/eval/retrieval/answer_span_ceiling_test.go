package retrieval

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func TestAnswerSpanCeiling_AggregatesPerSplitAndNamesNothing(t *testing.T) {
	loaded, err := LoadDataset(fixtureDataset)
	if err != nil {
		t.Fatal(err)
	}
	ceiling, details, err := ComputeAnswerSpanCeiling(os.DirFS(fixtureRepo), loaded)
	if err != nil {
		t.Fatal(err)
	}
	if ceiling.Version != AnswerSpanCeilingVersion || ceiling.CeilingTokens != SavingsCandidateBudget || ceiling.OverheadTokens != AnswerSpanResponseOverhead || ceiling.DatasetSHA256 != loaded.SHA256 {
		t.Fatalf("ceiling identity = %+v", ceiling)
	}
	// Every fixture span is small, so the bound must be saturated: each
	// answerable query is feasible on both measures and no span is over.
	want := 0
	for _, q := range loaded.Dataset.Queries {
		if q.Stratum == StratumNoHit {
			continue
		}
		for _, j := range q.Judgements {
			if j.Grade == SavingsGrade {
				want++
				break
			}
		}
	}
	all := ceiling.Overall
	if all.Answerable != want || all.AnyCompleteFeasible != want || all.AllCompleteFeasible != want || all.SpansOverCeiling != 0 || all.NoCompleteSpanPossible != 0 {
		t.Fatalf("fixture overall = %+v, want every one of %d answerable queries feasible", all, want)
	}
	if len(ceiling.Splits) != 2 || ceiling.Splits[0].Split != SplitDev || ceiling.Splits[1].Split != SplitHoldout {
		t.Fatalf("splits = %+v, want dev then holdout", ceiling.Splits)
	}
	if ceiling.Splits[0].Answerable+ceiling.Splits[1].Answerable != all.Answerable || ceiling.Splits[0].Spans+ceiling.Splits[1].Spans != all.Spans {
		t.Fatalf("split aggregates do not sum to overall: %+v vs %+v", ceiling.Splits, all)
	}
	if len(details) != all.Answerable {
		t.Fatalf("%d details for %d answerable queries", len(details), all.Answerable)
	}
	// The aggregate is what a curator returns from a sealed split. It must
	// carry no query id, no question text, no path and no line number.
	raw, err := json.Marshal(ceiling)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range loaded.Dataset.Queries {
		if strings.Contains(string(raw), q.ID) || strings.Contains(string(raw), q.Text) {
			t.Fatalf("aggregate report leaks query %s", q.ID)
		}
		for _, j := range q.Judgements {
			if strings.Contains(string(raw), j.Path) {
				t.Fatalf("aggregate report leaks path %s", j.Path)
			}
		}
	}
	if strings.Contains(string(raw), "start_line") || strings.Contains(string(raw), "query_id") {
		t.Fatalf("aggregate report carries per-span fields: %s", raw)
	}
}

func TestAnswerSpanCeiling_ClassifiesAgainstTheCeilingExactly(t *testing.T) {
	// One small span, one span that alone exceeds the ceiling, in one query;
	// plus a query whose only span is over. Costs are checked by construction:
	// the big file is far past 1,200 tokens in any tokenizer.
	small := "package p\n\nfunc Small() int { return 1 }\n"
	var big strings.Builder
	big.WriteString("package p\n\nfunc Big() {\n")
	for i := 0; i < 400; i++ {
		big.WriteString("\tstepValueNumber := computeSomethingLong(stepValueNumber, anotherIdentifier)\n")
	}
	big.WriteString("}\n")
	repository := fstest.MapFS{
		"small.go": {Data: []byte(small)},
		"big.go":   {Data: []byte(big.String())},
	}
	bigLines := strings.Count(big.String(), "\n")
	loaded := &Loaded{SHA256: "x", Dataset: &Dataset{ID: "t", Queries: []Query{
		{ID: "q1", Split: SplitDev, Stratum: StratumExactIdentifier, Judgements: []Judgement{
			{Path: "small.go", StartLine: 3, EndLine: 3, Grade: 3},
			{Path: "big.go", StartLine: 1, EndLine: bigLines, Grade: 3},
			{Path: "small.go", StartLine: 1, EndLine: 1, Grade: 2},
		}},
		{ID: "q2", Split: SplitHoldout, Stratum: StratumExactPath, Judgements: []Judgement{
			{Path: "big.go", StartLine: 1, EndLine: bigLines, Grade: 3},
		}},
		{ID: "q3", Split: SplitHoldout, Stratum: StratumNoHit},
	}}}
	ceiling, details, err := ComputeAnswerSpanCeiling(repository, loaded)
	if err != nil {
		t.Fatal(err)
	}
	all := ceiling.Overall
	if all.Answerable != 2 || all.Spans != 3 || all.SpansOverCeiling != 2 || all.AnyCompleteFeasible != 1 || all.AllCompleteFeasible != 0 || all.NoCompleteSpanPossible != 1 {
		t.Fatalf("overall = %+v", all)
	}
	if len(details) != 2 || !details[0].AnyFeasible || details[0].AllFeasible || details[1].AnyFeasible {
		t.Fatalf("details = %+v", details)
	}
	if details[0].CheapestResponse != details[0].Spans[0].Tokens+AnswerSpanResponseOverhead {
		t.Fatalf("cheapest response %d does not add the overhead to the cheapest span %d", details[0].CheapestResponse, details[0].Spans[0].Tokens)
	}
}

func TestAnswerSpanCeiling_RefusesAnUnresolvableSpan(t *testing.T) {
	loaded := &Loaded{SHA256: "x", Dataset: &Dataset{ID: "t", Queries: []Query{
		{ID: "q1", Split: SplitDev, Stratum: StratumExactIdentifier, Judgements: []Judgement{
			{Path: "missing.go", StartLine: 1, EndLine: 1, Grade: 3},
		}},
	}}}
	if _, _, err := ComputeAnswerSpanCeiling(fstest.MapFS{}, loaded); err == nil || !strings.Contains(err.Error(), "q1") {
		t.Fatalf("a missing span must be an error naming the query, got %v", err)
	}
}
