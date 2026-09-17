package retrieval

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/samibel/graphi/engine/agenttools/taskctx"
	taskcompact "github.com/samibel/graphi/engine/agenttools/taskctx/compact"
	compactv9 "github.com/samibel/graphi/engine/agenttools/taskctx/compact/v9"
)

// followupFixtureRepository is a six-line file whose reviewed answer is lines
// 4-5; a lead window of lines 1-2 is cut and designates lines 1-5.
func followupFixtureRepository() fstest.MapFS {
	return fstest.MapFS{"answer.go": {Data: []byte("package answer\n// one\n// two\n// the answer\n// continues\n// after\n")}}
}

func followupFixtureQuery() Query {
	return Query{
		ID: "dev-followup", Split: SplitDev, Stratum: StratumNLBehaviour,
		Judgements: []Judgement{{Path: "answer.go", StartLine: 4, EndLine: 5, Grade: GradeMax}},
	}
}

// followupFixtureFirstSlice encodes one compact task_context/2 response the
// way the stdio transport does, with the given sources and designation.
func followupFixtureFirstSlice(t *testing.T, counter PayloadCounter, sources []taskcompact.Source, followup string) PreservedPayload {
	t.Helper()
	structured := taskcompact.Structured{
		Version: taskcompact.Version, Sources: sources,
		Provenance: taskcompact.Provenance{
			InputSHA256: strings.Repeat("a", 64), Method: taskctx.MethodVersionV2,
			Retrieval: "retrieval/3", RetrievalState: "ready", Weights: "abcdef12", Model: "sha256:" + strings.Repeat("b", 16),
			SourceSelection: "context-definitions/3", SourceOrder: "ranked_coherent_regions",
			SourceBudget: taskcompact.DefaultSourceBudget, BudgetUnit: TokenizerID,
		},
	}
	if followup != "" {
		path, start, end, err := compactv9.ParseFollowupCitation(followup)
		if err != nil {
			t.Fatal(err)
		}
		structured.Followup = &taskcompact.Followup{Path: path, StartLine: start, EndLine: end}
	}
	type content struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			Content           []content              `json:"content"`
			StructuredContent taskcompact.Structured `json:"structuredContent"`
			IsError           bool                   `json:"isError"`
		} `json:"result"`
	}{JSONRPC: "2.0", ID: 1, Result: struct {
		Content           []content              `json:"content"`
		StructuredContent taskcompact.Structured `json:"structuredContent"`
		IsError           bool                   `json:"isError"`
	}{Content: []content{{Type: "text", Text: "fixture"}}, StructuredContent: structured}}); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	real, err := counter.Count(raw)
	if err != nil {
		t.Fatal(err)
	}
	return PreservedPayload{
		Sequence: 1, Boundary: PayloadBoundaryCandidate, Operation: PayloadOperationTaskContext,
		Bytes: raw, SHA256: SHA256Hex(raw), ByteCount: len(raw),
		TokenCounts: []PayloadTokenCount{
			{TokenizerID: TokenizerID, Tokens: len(strings.Fields(string(raw)))},
			{TokenizerID: counter.TokenizerID, VocabularySHA256: counter.VocabularySHA256, Tokens: real},
		},
	}
}

func followupFixtureCutLead(t *testing.T, counter PayloadCounter) PreservedPayload {
	t.Helper()
	return followupFixtureFirstSlice(t, counter, []taskcompact.Source{{Path: "answer.go", StartLine: 1, EndLine: 2, Text: "package answer\n// one"}}, "answer.go:1-5")
}

// TestCaptureFollowupRead_BuildsSliceTwoFromTheDesignation pins the second
// slice's shape: sequence 2, the follow-up operation, one newline-terminated
// JSON source line whose text is the pinned repository bytes at exactly the
// designated span, with both tokenizer counts.
func TestCaptureFollowupRead_BuildsSliceTwoFromTheDesignation(t *testing.T) {
	counter := equalRecallFixtureCounter()
	first := followupFixtureCutLead(t, counter)
	second, err := CaptureFollowupRead(followupFixtureRepository(), "dev-followup", first, counter)
	if err != nil {
		t.Fatal(err)
	}
	if second == nil || second.Sequence != 2 || second.Boundary != PayloadBoundaryCandidate || second.Operation != PayloadOperationFollowupRead {
		t.Fatalf("slice 2 = %+v", second)
	}
	want := `{"path":"answer.go","start_line":1,"end_line":5,"text":"package answer\n// one\n// two\n// the answer\n// continues"}` + "\n"
	if string(second.Bytes) != want {
		t.Fatalf("slice 2 bytes = %q, want %q", second.Bytes, want)
	}
	if second.SHA256 != SHA256Hex(second.Bytes) || second.ByteCount != len(second.Bytes) || len(second.TokenCounts) != 2 {
		t.Fatalf("slice 2 accounting = %+v", second)
	}
}

func TestCaptureFollowupRead_IsAbsentWithoutADesignation(t *testing.T) {
	counter := equalRecallFixtureCounter()
	first := followupFixtureFirstSlice(t, counter, []taskcompact.Source{{Path: "answer.go", StartLine: 4, EndLine: 5, Text: "// the answer\n// continues"}}, "")
	second, err := CaptureFollowupRead(followupFixtureRepository(), "dev-followup", first, counter)
	if err != nil || second != nil {
		t.Fatalf("slice 2 = %+v, %v; want none", second, err)
	}
}

// TestScoreTranscript_ChargesOnlyTheFirstSliceWhenItReaches: the earliest
// prefix rule. A read that was taken but not needed is preserved, not charged.
func TestScoreTranscript_ChargesOnlyTheFirstSliceWhenItReaches(t *testing.T) {
	counter := equalRecallFixtureCounter()
	repository := followupFixtureRepository()
	first := followupFixtureFirstSlice(t, counter, []taskcompact.Source{{Path: "answer.go", StartLine: 4, EndLine: 4, Text: "// the answer"}}, "answer.go:1-5")
	second, err := CaptureFollowupRead(repository, "dev-followup", first, counter)
	if err != nil || second == nil {
		t.Fatal(err)
	}
	target := RecallTarget{Grade: GradeMax, RequiredSpans: 1, TotalSpans: 1}
	got, err := ScoreTaskContextTranscriptEqualRecallDev(repository, followupFixtureQuery(), []PreservedPayload{first, *second}, target, counter)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != SavingsOutcomeReached || got.ConsumedPayloadSlices != 1 || got.StopReason != SavingsStopOneCallComplete {
		t.Fatalf("outcome = %+v", got)
	}
	if got.TokensToTarget == nil || *got.TokensToTarget != len(first.Bytes) || len(got.Payloads) != 2 {
		t.Fatalf("charged %v over %d payloads, want the first slice only with both preserved", got.TokensToTarget, len(got.Payloads))
	}
}

// TestScoreTranscript_ChargesBothSlicesWhenTheReadCompletes: the first slice
// misses, the designated read covers the span, both slices are charged.
func TestScoreTranscript_ChargesBothSlicesWhenTheReadCompletes(t *testing.T) {
	counter := equalRecallFixtureCounter()
	repository := followupFixtureRepository()
	first := followupFixtureCutLead(t, counter)
	second, err := CaptureFollowupRead(repository, "dev-followup", first, counter)
	if err != nil || second == nil {
		t.Fatal(err)
	}
	target := RecallTarget{Grade: GradeMax, RequiredSpans: 1, TotalSpans: 1}
	got, err := ScoreTaskContextTranscriptEqualRecallDev(repository, followupFixtureQuery(), []PreservedPayload{first, *second}, target, counter)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != SavingsOutcomeReached || got.ConsumedPayloadSlices != 2 || got.StopReason != SavingsStopFollowupReadComplete || got.Grade3SpansAtPrefix != 1 {
		t.Fatalf("outcome = %+v", got)
	}
	if got.TokensToTarget == nil || *got.TokensToTarget != len(first.Bytes)+len(second.Bytes) {
		t.Fatalf("tokens_to_target = %v, want both slices", got.TokensToTarget)
	}
	one, err := ScoreTaskContextTranscriptEqualRecallDev(repository, followupFixtureQuery(), []PreservedPayload{first}, target, counter)
	if err != nil || one.Status != SavingsOutcomeMissed || one.ConsumedPayloadSlices != 1 {
		t.Fatalf("one-slice transcript = %+v, %v; want the contract-1 miss", one, err)
	}
}

// TestScoreTranscript_CensorsAtBothSlicesOnAMiss: a read that does not reach
// the span still counts toward the censor bound.
func TestScoreTranscript_CensorsAtBothSlicesOnAMiss(t *testing.T) {
	counter := equalRecallFixtureCounter()
	repository := followupFixtureRepository()
	first := followupFixtureFirstSlice(t, counter, []taskcompact.Source{{Path: "answer.go", StartLine: 1, EndLine: 1, Text: "package answer"}}, "answer.go:1-3")
	second, err := CaptureFollowupRead(repository, "dev-followup", first, counter)
	if err != nil || second == nil {
		t.Fatal(err)
	}
	target := RecallTarget{Grade: GradeMax, RequiredSpans: 1, TotalSpans: 1}
	got, err := ScoreTaskContextTranscriptEqualRecallDev(repository, followupFixtureQuery(), []PreservedPayload{first, *second}, target, counter)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != SavingsOutcomeMissed || got.ConsumedPayloadSlices != 2 || got.TokensToTarget != nil {
		t.Fatalf("outcome = %+v", got)
	}
	if got.CensorLowerBoundTokens == nil || *got.CensorLowerBoundTokens != len(first.Bytes)+len(second.Bytes) {
		t.Fatalf("censor bound = %v, want both slices", got.CensorLowerBoundTokens)
	}
}

// TestScoreTranscript_RejectsAReadThatIsNotTheDesignation: the second slice
// must be exactly the designated span, verbatim from the pinned tree, under
// the follow-up operation label; the reader is not free to choose.
func TestScoreTranscript_RejectsAReadThatIsNotTheDesignation(t *testing.T) {
	counter := equalRecallFixtureCounter()
	repository := followupFixtureRepository()
	first := followupFixtureCutLead(t, counter)
	valid, err := CaptureFollowupRead(repository, "dev-followup", first, counter)
	if err != nil || valid == nil {
		t.Fatal(err)
	}
	target := RecallTarget{Grade: GradeMax, RequiredSpans: 1, TotalSpans: 1}
	reslice := func(raw string) PreservedPayload {
		mutated := *valid
		mutated.Bytes = []byte(raw)
		mutated.SHA256 = SHA256Hex(mutated.Bytes)
		mutated.ByteCount = len(mutated.Bytes)
		n, _ := counter.Count(mutated.Bytes)
		mutated.TokenCounts = []PayloadTokenCount{
			{TokenizerID: TokenizerID, Tokens: len(strings.Fields(raw))},
			{TokenizerID: counter.TokenizerID, VocabularySHA256: counter.VocabularySHA256, Tokens: n},
		}
		return mutated
	}
	cases := map[string]struct {
		second PreservedPayload
		want   string
	}{
		"a different span": {reslice(`{"path":"answer.go","start_line":4,"end_line":5,"text":"// the answer\n// continues"}` + "\n"), "not the designated span"},
		"fabricated text":  {reslice(`{"path":"answer.go","start_line":1,"end_line":5,"text":"package answer\n// one\n// two\n// forged\n// continues"}` + "\n"), "differ from"},
		"two lines":        {reslice(string(valid.Bytes) + "\n"), "one exact line"},
		"unknown field":    {reslice(`{"path":"answer.go","start_line":1,"end_line":5,"text":"package answer\n// one\n// two\n// the answer\n// continues","extra":1}` + "\n"), "not one follow-up source"},
	}
	wrongOperation := *valid
	wrongOperation.Operation = PayloadOperationRead
	cases["wrong operation"] = struct {
		second PreservedPayload
		want   string
	}{wrongOperation, "operation"}
	wrongSequence := *valid
	wrongSequence.Sequence = 3
	cases["wrong sequence"] = struct {
		second PreservedPayload
		want   string
	}{wrongSequence, "sequence"}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ScoreTaskContextTranscriptEqualRecallDev(repository, followupFixtureQuery(), []PreservedPayload{first, tc.second}, target, counter)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
	t.Run("read without a designation", func(t *testing.T) {
		undesignated := followupFixtureFirstSlice(t, counter, []taskcompact.Source{{Path: "answer.go", StartLine: 1, EndLine: 1, Text: "package answer"}}, "")
		if _, err := ScoreTaskContextTranscriptEqualRecallDev(repository, followupFixtureQuery(), []PreservedPayload{undesignated, *valid}, target, counter); err == nil || !strings.Contains(err.Error(), "designated no follow-up") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("more than two slices", func(t *testing.T) {
		if _, err := ScoreTaskContextTranscriptEqualRecallDev(repository, followupFixtureQuery(), []PreservedPayload{first, *valid, *valid}, target, counter); err == nil || !strings.Contains(err.Error(), "at most two") {
			t.Fatalf("error = %v", err)
		}
	})
}

// TestScoreTranscript_RejectsADesignationOverTheLineCap: a first slice that
// designates more than FollowupMaxLines is not a contract-2 transcript, even
// if the read matches it.
func TestScoreTranscript_RejectsADesignationOverTheLineCap(t *testing.T) {
	counter := equalRecallFixtureCounter()
	long := make([]string, compactv9.FollowupMaxLines+2)
	for i := range long {
		long[i] = "// line"
	}
	repository := fstest.MapFS{"answer.go": {Data: []byte(strings.Join(long, "\n") + "\n")}}
	first := followupFixtureFirstSlice(t, counter, []taskcompact.Source{{Path: "answer.go", StartLine: 1, EndLine: 1, Text: "// line"}}, "answer.go:1-"+strconv.Itoa(compactv9.FollowupMaxLines+1))
	second, err := CaptureFollowupRead(repository, "dev-followup", first, counter)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("capture = %+v, %v; want the line-cap refusal", second, err)
	}
}
