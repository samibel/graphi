package retrieval

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/shape"
)

const equalRecallFixtureVocabSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestSelectEqualRecallDevPopulation_PinsTheFortyAnswerableDevQueries(t *testing.T) {
	datasetPath := filepath.Join(taskContextModuleRoot(t), "docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/dataset.json")
	loaded, err := LoadDataset(datasetPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SHA256 != "2d05e3bb015a1447e0c31a9a855712e6aae6f4281adbf7acd72e86c923a43d6c" {
		t.Fatalf("dev slice sha256 = %s", loaded.SHA256)
	}
	members, err := SelectEqualRecallDevPopulation(loaded.Dataset)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 40 {
		t.Fatalf("answerable dev population = %d, want 40", len(members))
	}
	for _, member := range members {
		if member.Split != SplitDev || member.Stratum == StratumNoHit || member.Target.Grade != GradeMax || member.Target.RequiredSpans != 1 || member.Target.TotalSpans < 1 {
			t.Fatalf("invalid frozen dev member: %+v", member)
		}
	}
}

func TestSelectEqualRecallDevPopulation_RefusesAnyNonDevRow(t *testing.T) {
	dataset := &Dataset{ID: "mixed", Queries: []Query{
		{ID: "dev", FamilyID: "dev-family", Split: SplitDev, Stratum: StratumNLBehaviour, Judgements: []Judgement{{Grade: GradeMax}}},
		{ID: "sealed", FamilyID: "sealed-family", Split: SplitHoldout, Stratum: StratumNLBehaviour, Judgements: []Judgement{{Grade: GradeMax}}},
	}}
	if _, err := SelectEqualRecallDevPopulation(dataset); err == nil || !strings.Contains(err.Error(), "only a dev-only slice is allowed") {
		t.Fatalf("error = %v, want non-dev refusal", err)
	}
}

// TestEqualRecallDevArtifact is an explicit development-only diagnostic. It
// scores the first of the two byte-identical checked-in MCP captures against a
// clean pinned Cobra checkout. It never loads the sealed dataset.
func TestEqualRecallDevArtifact(t *testing.T) {
	repositoryRoot := os.Getenv("GRAPHI_EQUAL_RECALL_DEV_COBRA")
	if repositoryRoot == "" {
		t.Skip("set GRAPHI_EQUAL_RECALL_DEV_COBRA to a clean Cobra checkout at the dev slice's pinned commit")
	}
	moduleRoot := taskContextModuleRoot(t)
	loaded, err := LoadDataset(filepath.Join(moduleRoot, "docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/dataset.json"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SHA256 != "2d05e3bb015a1447e0c31a9a855712e6aae6f4281adbf7acd72e86c923a43d6c" {
		t.Fatal("development slice digest changed")
	}
	head, err := CheckoutHEAD(t.Context(), repositoryRoot)
	if err != nil || head != loaded.Dataset.RepoSHA {
		t.Fatalf("Cobra checkout = %s, want %s: %v", head, loaded.Dataset.RepoSHA, err)
	}
	clean, err := GitRepoProbe().WorktreeClean(t.Context(), repositoryRoot)
	if err != nil || !clean {
		t.Fatalf("Cobra checkout must be clean: %v", err)
	}
	members, err := SelectEqualRecallDevPopulation(loaded.Dataset)
	if err != nil {
		t.Fatal(err)
	}
	counter := loadEmbeddedRealPayloadCounterForTest(t)

	type capturedRow struct {
		QueryID string                  `json:"query_id"`
		Capture CapturedCandidateBundle `json:"capture"`
	}
	var artifact struct {
		DatasetSHA string          `json:"dataset_sha256"`
		Runs       [][]capturedRow `json:"independent_builds"`
	}
	raw, err := os.ReadFile(filepath.Join(moduleRoot, "docs/eval/retrieval/runs/2026-09-07-answer-recovery-dev/bundles-after.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.DatasetSHA != loaded.SHA256 || len(artifact.Runs) != 2 {
		t.Fatal("capture does not bind the development slice or both independent builds")
	}
	queries := make(map[string]Query, len(loaded.Dataset.Queries))
	for _, q := range loaded.Dataset.Queries {
		queries[q.ID] = q
	}
	captures := make(map[string]CapturedCandidateBundle, len(artifact.Runs[0]))
	for _, row := range artifact.Runs[0] {
		captures[row.QueryID] = row.Capture
	}
	reached, totalTokens := 0, 0
	var misses []string
	for _, member := range members {
		capture, ok := captures[member.QueryID]
		if !ok {
			t.Fatalf("missing capture for %s", member.QueryID)
		}
		outcome, err := ScoreTaskContextEqualRecallDev(os.DirFS(repositoryRoot), queries[member.QueryID], capture.Payload, member.Target, counter)
		if err != nil {
			t.Fatalf("%s: %v", member.QueryID, err)
		}
		if outcome.Status == SavingsOutcomeReached {
			reached++
			totalTokens += *outcome.TokensToTarget
		} else {
			misses = append(misses, member.QueryID)
			totalTokens += *outcome.CensorLowerBoundTokens
		}
	}
	t.Logf("equal-recall dev tokens_to_exact_span: reached=%d/%d, missed=%d/%d, complete-response mean real tokens=%.3f", reached, len(members), len(misses), len(members), float64(totalTokens)/float64(len(members)))
	t.Logf("right-censored query IDs: %v", misses)
}

func TestScoreTaskContextEqualRecallDev_CreditsOnlyWholeGrade3Spans(t *testing.T) {
	repository := fstest.MapFS{
		"answer.go": {Data: []byte("package answer\n// first line\n// second line\n// other answer\n")},
	}
	query := Query{
		ID: "dev-answer", Split: SplitDev, Stratum: StratumNLBehaviour,
		Judgements: []Judgement{
			{Path: "answer.go", StartLine: 2, EndLine: 3, Grade: GradeMax},
			{Path: "answer.go", StartLine: 4, EndLine: 4, Grade: GradeMax},
		},
	}
	bundle := equalRecallFixtureBundle(
		contract.Evidence{RefID: "overlap", Path: "answer.go", Line: 2, Span: "2-2", Role: "source", Snippet: "// first line", TextHash: "0123456789abcdef"},
		contract.Evidence{RefID: "point-only", Path: "answer.go", Line: 4, Span: "4-4", Role: "source", ClaimType: "source_match"},
	)
	counter := equalRecallFixtureCounter()
	payload := equalRecallFixturePayload(t, bundle, counter)
	target := RecallTarget{Grade: GradeMax, RequiredSpans: 2, TotalSpans: 2}

	got, err := ScoreTaskContextEqualRecallDev(repository, query, payload, target, counter)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != SavingsOutcomeMissed || got.Grade3SpansAtPrefix != 1 {
		t.Fatalf("outcome = %+v, want a miss with exactly one whole span", got)
	}
	if got.TokensToTarget != nil || got.CensorLowerBoundTokens == nil {
		t.Fatalf("miss accounting = %+v", got)
	}
}

func TestScoreTaskContextEqualRecallDev_ChargesTheCompleteIndivisibleMCPResponse(t *testing.T) {
	repository := fstest.MapFS{"answer.go": {Data: []byte("the answer\n")}}
	query := Query{
		ID: "dev-answer", Split: SplitDev, Stratum: StratumExactIdentifier,
		Judgements: []Judgement{{Path: "answer.go", StartLine: 1, EndLine: 1, Grade: GradeMax}},
	}
	bundle := equalRecallFixtureBundle(
		contract.Evidence{RefID: "answer", Path: "answer.go", Line: 1, Span: "1-1", Role: "source", Snippet: "the answer", TextHash: "0123456789abcdef"},
	)
	counter := equalRecallFixtureCounter()
	payload := equalRecallFixturePayload(t, bundle, counter)
	target := RecallTarget{Grade: GradeMax, RequiredSpans: 1, TotalSpans: 1}

	got, err := ScoreTaskContextEqualRecallDev(repository, query, payload, target, counter)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != SavingsOutcomeReached || got.ConsumedPayloadSlices != 1 || got.Grade3SpansAtPrefix != 1 {
		t.Fatalf("outcome = %+v", got)
	}
	if got.TokensToTarget == nil || *got.TokensToTarget != len(payload.Bytes) {
		t.Fatalf("tokens_to_target = %v, want all %d response bytes under fixture counter", got.TokensToTarget, len(payload.Bytes))
	}
	if got.CensorLowerBoundTokens != nil || len(got.Payloads) != 1 || !bytes.Equal(got.Payloads[0].Bytes, payload.Bytes) {
		t.Fatalf("candidate response was split or reconstructed: %+v", got)
	}
}

func TestScoreTaskContextEqualRecallDev_RejectsByteCountAndSourceSubstitution(t *testing.T) {
	repository := fstest.MapFS{"answer.go": {Data: []byte("the answer\n")}}
	query := Query{
		ID: "dev-answer", Split: SplitDev, Stratum: StratumNLBehaviour,
		Judgements: []Judgement{{Path: "answer.go", StartLine: 1, EndLine: 1, Grade: GradeMax}},
	}
	target := RecallTarget{Grade: GradeMax, RequiredSpans: 1, TotalSpans: 1}
	counter := equalRecallFixtureCounter()
	valid := equalRecallFixturePayload(t, equalRecallFixtureBundle(
		contract.Evidence{RefID: "answer", Path: "answer.go", Line: 1, Span: "1-1", Role: "source", Snippet: "the answer", TextHash: "0123456789abcdef"},
	), counter)

	t.Run("changed response bytes with stale digest", func(t *testing.T) {
		mutated := valid
		mutated.Bytes = append(append([]byte(nil), valid.Bytes...), ' ')
		if _, err := ScoreTaskContextEqualRecallDev(repository, query, mutated, target, counter); err == nil || !strings.Contains(err.Error(), "sha256 does not match") {
			t.Fatalf("error = %v, want exact-byte rejection", err)
		}
	})

	t.Run("changed real-token count", func(t *testing.T) {
		mutated := valid
		mutated.TokenCounts = append([]PayloadTokenCount(nil), valid.TokenCounts...)
		for i := range mutated.TokenCounts {
			if mutated.TokenCounts[i].TokenizerID == counter.TokenizerID {
				mutated.TokenCounts[i].Tokens++
			}
		}
		if _, err := ScoreTaskContextEqualRecallDev(repository, query, mutated, target, counter); err == nil || !strings.Contains(err.Error(), "recomputed") {
			t.Fatalf("error = %v, want executable token recount rejection", err)
		}
	})

	t.Run("self-consistent MCP payload with fabricated source", func(t *testing.T) {
		fabricated := equalRecallFixturePayload(t, equalRecallFixtureBundle(
			contract.Evidence{RefID: "answer", Path: "answer.go", Line: 1, Span: "1-1", Role: "source", Snippet: "not the source", TextHash: "0123456789abcdef"},
		), counter)
		if _, err := ScoreTaskContextEqualRecallDev(repository, query, fabricated, target, counter); err == nil || !strings.Contains(err.Error(), "snippet bytes differ") {
			t.Fatalf("error = %v, want pinned-source rejection", err)
		}
	})
}

func TestScoreTaskContextEqualRecallDev_PropagatesMissToMagnitudeRefusal(t *testing.T) {
	repository := fstest.MapFS{"answer.go": {Data: []byte("the answer\n")}}
	query := Query{
		ID: "q-dev", Split: SplitDev, Stratum: StratumNLBehaviour,
		Judgements: []Judgement{{Path: "answer.go", StartLine: 1, EndLine: 1, Grade: GradeMax}},
	}
	target := RecallTarget{Grade: GradeMax, RequiredSpans: 1, TotalSpans: 1}
	counter := equalRecallFixtureCounter()
	payload := equalRecallFixturePayload(t, equalRecallFixtureBundle(), counter)

	miss, err := ScoreTaskContextEqualRecallDev(repository, query, payload, target, counter)
	if err != nil {
		t.Fatal(err)
	}
	if miss.Status != SavingsOutcomeMissed || miss.TokensToTarget != nil || miss.CensorLowerBoundTokens == nil || *miss.CensorLowerBoundTokens != len(payload.Bytes) {
		t.Fatalf("miss was not propagated as right-censored: %+v", miss)
	}

	in, counters := validSavingsAggregateInput(t)
	in.Observations[0].Candidate = miss
	err = ValidateSavingsAggregateInput(in, counters)
	if err == nil || !strings.Contains(err.Error(), "right-censored") || !strings.Contains(err.Error(), "complete-case aggregation is forbidden") {
		t.Fatalf("aggregate error = %v, want fail-closed miss propagation", err)
	}
}

func equalRecallFixtureBundle(evidence ...contract.Evidence) contract.Result {
	for i := range evidence {
		if evidence[i].Snippet != "" {
			evidence[i].TextHash = shape.TextHash(evidence[i].Snippet)
		}
	}
	return contract.Result{
		Outcome:  contract.OutcomeFound,
		Summary:  readyV2Summary(),
		Items:    []contract.Item{},
		Evidence: evidence,
		Confidence: contract.Confidence{
			Distribution: map[string]float64{"heuristic": 1},
			Top:          "heuristic",
			Method:       "fixture",
		},
	}
}

func equalRecallFixtureCounter() PayloadCounter {
	return PayloadCounter{
		TokenizerID:      "fixture-real-tokenizer",
		VocabularySHA256: equalRecallFixtureVocabSHA,
		Count: func(raw []byte) (int, error) {
			return len(raw), nil
		},
	}
}

func equalRecallFixturePayload(t *testing.T, bundle contract.Result, counter PayloadCounter) PreservedPayload {
	t.Helper()
	inner, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	type response struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  any    `json:"result"`
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(response{
		JSONRPC: "2.0",
		ID:      1,
		Result: map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(inner)}},
			"isError": false,
		},
	}); err != nil {
		t.Fatal(err)
	}
	payload, err := preserveCandidatePayload("dev-answer", out.Bytes(), counter)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
