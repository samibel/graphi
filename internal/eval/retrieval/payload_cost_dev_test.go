package retrieval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/contract"
	evaltokenizer "github.com/samibel/graphi/internal/eval/tokenizer"
)

func TestAttributeTaskContextPayload_ReconcilesEscapedStringsExactly(t *testing.T) {
	counter := equalRecallFixtureCounter()
	snippet := "if x < 3 {\n\tprintln(\"quoted\\\\value\") // 😀\n}"
	bundle := payloadCostFixtureBundle(snippet)
	payload := equalRecallFixturePayload(t, bundle, counter)

	got, err := AttributeTaskContextPayload("dev-answer", payload, counter)
	if err != nil {
		t.Fatal(err)
	}
	bytesTotal, tokensTotal := 0, 0
	for _, category := range got.Categories {
		bytesTotal += category.WireBytes
		tokensTotal += category.Tokens
	}
	if bytesTotal != len(payload.Bytes) || tokensTotal != payloadToken(payload, counter.TokenizerID) {
		t.Fatalf("marginals = %d bytes/%d tokens, full = %d/%d", bytesTotal, tokensTotal, len(payload.Bytes), payloadToken(payload, counter.TokenizerID))
	}
	source := payloadCostCategoryByName(t, got, payloadCostSource)
	if source.ValueBytes != len([]byte(snippet)) {
		t.Fatalf("decoded source bytes = %d, want %d", source.ValueBytes, len([]byte(snippet)))
	}
	if source.WireExpansionOverValue <= 0 {
		t.Fatalf("escaped source wire expansion = %d, want positive", source.WireExpansionOverValue)
	}
}

func TestBuildDevPayloadCostReport_IsDeterministic(t *testing.T) {
	counter := payloadCostFixtureCounter()
	payload := equalRecallFixturePayload(t, payloadCostFixtureBundle("answer\n"), counter)
	artifact := payloadCostFixtureArtifact(t, payload)

	first, err := BuildDevPayloadCostReport(artifact, counter)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildDevPayloadCostReport(artifact, counter)
	if err != nil {
		t.Fatal(err)
	}
	a, err := MarshalPayloadCostReport(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := MarshalPayloadCostReport(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("identical input produced different attribution bytes")
	}
	if first.Aggregate.Queries != payloadCostDevQueries || first.Aggregate.FullBytes != payloadCostDevQueries*len(payload.Bytes) {
		t.Fatalf("aggregate = %+v", first.Aggregate)
	}
}

func TestBuildDevPayloadCostReport_FailsClosedOnMalformedOrDriftingPayload(t *testing.T) {
	counter := payloadCostFixtureCounter()
	payload := equalRecallFixturePayload(t, payloadCostFixtureBundle("answer"), counter)

	tests := []struct {
		name string
		raw  func() []byte
		want string
	}{
		{name: "malformed artifact", raw: func() []byte { return []byte(`{"dataset_sha256":`) }, want: "malformed capture artifact"},
		{name: "wrong dataset", raw: func() []byte {
			raw := payloadCostFixtureArtifact(t, payload)
			return bytes.Replace(raw, []byte(payloadCostDevDatasetSHA256), []byte(strings.Repeat("f", 64)), 1)
		}, want: "development-only slice"},
		{name: "digest drift", raw: func() []byte {
			drifted := payload
			drifted.SHA256 = strings.Repeat("0", 64)
			return payloadCostFixtureArtifact(t, drifted)
		}, want: "digest or byte count drift"},
		{name: "malformed payload", raw: func() []byte {
			drifted := payload
			drifted.Bytes = []byte("{")
			drifted.SHA256 = SHA256Hex(drifted.Bytes)
			drifted.ByteCount = len(drifted.Bytes)
			drifted.TokenCounts = []PayloadTokenCount{
				{TokenizerID: TokenizerID, Tokens: 1},
				{TokenizerID: counter.TokenizerID, VocabularySHA256: counter.VocabularySHA256, Tokens: 1},
			}
			return payloadCostFixtureArtifact(t, drifted)
		}, want: "does not begin with"},
		{name: "token count drift", raw: func() []byte {
			drifted := payload
			drifted.TokenCounts = append([]PayloadTokenCount(nil), payload.TokenCounts...)
			drifted.TokenCounts[1].Tokens++
			return payloadCostFixtureArtifact(t, drifted)
		}, want: "tokenizer count or vocabulary digest drift"},
		{name: "independent build drift", raw: func() []byte {
			other := equalRecallFixturePayload(t, payloadCostFixtureBundle("different answer"), counter)
			return payloadCostFixtureArtifactPair(t, payload, other)
		}, want: "differs across independent builds"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BuildDevPayloadCostReport(tc.raw(), counter)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCommittedDevPayloadCostReport_UsesPinnedTokenizerAndReconciles(t *testing.T) {
	root := taskContextModuleRoot(t)
	tok, err := evaltokenizer.Load(filepath.Join(root, "internal/eval/tokenizer/testdata/artifact"))
	if err != nil {
		t.Fatal(err)
	}
	counter, err := NewPinnedRealPayloadCounter(tok)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "docs/eval/retrieval/runs/2026-09-07-answer-recovery-dev/bundles-after.json"))
	if err != nil {
		t.Fatal(err)
	}
	report, err := BuildDevPayloadCostReport(raw, counter)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Observations) != 44 || report.Aggregate.Queries != 44 {
		t.Fatalf("development observations = %d/%d, want 44", len(report.Observations), report.Aggregate.Queries)
	}
	for _, obs := range report.Observations {
		if err := validatePayloadCostObservation(obs); err != nil {
			t.Fatalf("%s: %v", obs.QueryID, err)
		}
	}
}

func payloadCostFixtureBundle(snippet string) contract.Result {
	return contract.Result{
		Outcome:    contract.OutcomePartial,
		Summary:    "task_context/2: escaped <summary>; degradation: ready",
		Items:      []contract.Item{{RefID: "node-1", Rank: 7, Reason: "primary: useful", EvidenceRefIDs: []string{"e1"}}},
		Evidence:   []contract.Evidence{{RefID: "e1", Path: "answer.go", Line: 2, Span: "2-4", Role: "snippet", Snippet: snippet, TextHash: "0123456789abcdef"}},
		Confidence: contract.Confidence{Distribution: map[string]float64{"confirmed": 1}, Top: "confirmed", Method: "edge_tiers"},
		Limits:     contract.Limits{CapApplied: 40, TotalAvailable: 41, Dropped: 1, Truncated: true},
	}
}

func payloadCostFixtureArtifact(t *testing.T, payload PreservedPayload) []byte {
	t.Helper()
	return payloadCostFixtureArtifactPair(t, payload, payload)
}

func payloadCostFixtureArtifactPair(t *testing.T, first, second PreservedPayload) []byte {
	t.Helper()
	type row struct {
		QueryID string `json:"query_id"`
		Capture struct {
			QueryID string           `json:"query_id"`
			Payload PreservedPayload `json:"payload"`
		} `json:"capture"`
	}
	makeRow := func(id string, payload PreservedPayload) row {
		var r row
		r.QueryID = id
		r.Capture.QueryID = r.QueryID
		r.Capture.Payload = payload
		return r
	}
	artifact := struct {
		DatasetSHA256     string  `json:"dataset_sha256"`
		Queries           int     `json:"queries"`
		IndependentBuilds [][]row `json:"independent_builds"`
	}{DatasetSHA256: payloadCostDevDatasetSHA256, Queries: payloadCostDevQueries}
	artifact.IndependentBuilds = make([][]row, 2)
	for i := 0; i < payloadCostDevQueries; i++ {
		id := fmt.Sprintf("dev-%02d", i+1)
		artifact.IndependentBuilds[0] = append(artifact.IndependentBuilds[0], makeRow(id, first))
		artifact.IndependentBuilds[1] = append(artifact.IndependentBuilds[1], makeRow(id, second))
	}
	raw, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func payloadCostFixtureCounter() PayloadCounter {
	return PayloadCounter{
		TokenizerID:      evaltokenizer.TokenizerID,
		VocabularySHA256: evaltokenizer.PinnedVocabularySHA256,
		Count:            func(raw []byte) (int, error) { return len(raw), nil },
	}
}

func payloadCostCategoryByName(t *testing.T, obs PayloadCostObservation, name string) PayloadCostCategory {
	t.Helper()
	for _, category := range obs.Categories {
		if category.Name == name {
			return category
		}
	}
	t.Fatalf("missing category %s", name)
	return PayloadCostCategory{}
}
