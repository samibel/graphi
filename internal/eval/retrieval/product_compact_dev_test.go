package retrieval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/contract"
	taskcompact "github.com/samibel/graphi/engine/agenttools/taskctx/compact"
)

// TestProductCompactTaskContextDev measures the production compact projector
// against development judgements only. It is opt-in because it requires a
// clean checkout of the pinned Cobra source; it never opens the combined
// dataset containing the spent holdout.
func TestProductCompactTaskContextDev(t *testing.T) {
	repositoryRoot := os.Getenv("GRAPHI_PRODUCT_COMPACT_DEV_COBRA")
	if repositoryRoot == "" {
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
	members, err := SelectEqualRecallDevPopulation(loaded.Dataset)
	if err != nil {
		t.Fatal(err)
	}
	captureRaw, err := os.ReadFile(filepath.Join(moduleRoot, "docs/eval/retrieval/runs/2026-09-07-answer-recovery-dev/bundles-after.json"))
	if err != nil {
		t.Fatal(err)
	}
	var captures struct {
		DatasetSHA string `json:"dataset_sha256"`
		Runs       [][]struct {
			QueryID string `json:"query_id"`
			Capture struct {
				Payload PreservedPayload `json:"payload"`
			} `json:"capture"`
		} `json:"independent_builds"`
	}
	if err := json.Unmarshal(captureRaw, &captures); err != nil {
		t.Fatal(err)
	}
	if captures.DatasetSHA != loaded.SHA256 || len(captures.Runs) != 2 {
		t.Fatal("development captures have unexpected identity")
	}
	inputs := make(map[string]PreservedPayload)
	for _, row := range captures.Runs[0] {
		inputs[row.QueryID] = row.Capture.Payload
	}
	queries := make(map[string]Query)
	for _, query := range loaded.Dataset.Queries {
		queries[query.ID] = query
	}
	counter, err := LoadPinnedRealPayloadCounter()
	if err != nil {
		t.Fatal(err)
	}
	repository := os.DirFS(repositoryRoot)
	reached, withinBudget, maxTokens := 0, 0, 0
	var misses []string
	for _, member := range members {
		query := queries[member.QueryID]
		bundle, err := taskContextBundleFromCandidateBytes(inputs[member.QueryID].Bytes)
		if err != nil {
			t.Fatalf("%s input: %v", member.QueryID, err)
		}
		legacy, err := contract.Serialize(&bundle)
		if err != nil {
			t.Fatal(err)
		}
		first, err := taskcompact.Build(t.Context(), query.Text, legacy, repository, taskcompact.DefaultSourceBudget)
		if err != nil {
			t.Fatalf("%s build: %v", member.QueryID, err)
		}
		second, err := taskcompact.Build(t.Context(), query.Text, legacy, repository, taskcompact.DefaultSourceBudget)
		if err != nil || !reflect.DeepEqual(first, second) {
			t.Fatalf("%s is not byte-reproducible: %v", member.QueryID, err)
		}
		wire, err := json.Marshal(struct {
			JSONRPC string `json:"jsonrpc"`
			ID      int    `json:"id"`
			Result  struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				StructuredContent taskcompact.Structured `json:"structuredContent"`
				IsError           bool                   `json:"isError"`
			} `json:"result"`
		}{JSONRPC: "2.0", ID: 1, Result: struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			StructuredContent taskcompact.Structured `json:"structuredContent"`
			IsError           bool                   `json:"isError"`
		}{
			Content: []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}{{Type: "text", Text: first.Summary}}, StructuredContent: first.Structured,
		}})
		if err != nil {
			t.Fatal(err)
		}
		wire = append(wire, '\n')
		if _, err := ValidateCompactCandidateBundleBytes(member.QueryID, wire); err != nil {
			t.Fatalf("%s compact wire validation: %v", member.QueryID, err)
		}
		tokens, err := counter.Count(wire)
		if err != nil {
			t.Fatal(err)
		}
		if tokens <= SavingsCandidateBudget {
			withinBudget++
		}
		if tokens > maxTokens {
			maxTokens = tokens
		}
		covered := make(map[int]bool)
		for _, source := range first.Structured.Sources {
			raw, err := exactSourceSpan(repository, source.Path, source.StartLine, source.EndLine)
			if err != nil || raw != source.Text {
				t.Fatalf("%s unverifiable source %s:%d-%d: %v", member.QueryID, source.Path, source.StartLine, source.EndLine, err)
			}
			for i, judgement := range query.Judgements {
				if judgement.Grade == SavingsGrade && source.Path == judgement.Path && source.StartLine <= judgement.EndLine && source.EndLine >= judgement.StartLine {
					covered[i] = true
				}
			}
		}
		if len(covered) >= member.Target.RequiredSpans {
			reached++
		} else {
			misses = append(misses, member.QueryID)
			var citations []string
			for _, source := range first.Structured.Sources {
				citations = append(citations, source.Path+":"+strconv.Itoa(source.StartLine)+"-"+strconv.Itoa(source.EndLine))
			}
			t.Logf("miss %s (%q): %v", member.QueryID, query.Text, citations)
		}
	}
	for _, query := range loaded.Dataset.Queries {
		if query.Stratum != StratumNoHit {
			continue
		}
		bundle, err := taskContextBundleFromCandidateBytes(inputs[query.ID].Bytes)
		if err != nil {
			t.Fatal(err)
		}
		legacy, err := contract.Serialize(&bundle)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := taskcompact.Build(t.Context(), query.Text, legacy, repository, taskcompact.DefaultSourceBudget); err != nil {
			t.Fatalf("no-hit query %s must return a successful empty compact result: %v", query.ID, err)
		}
	}
	sort.Strings(misses)
	t.Logf("production compact dev: reached=%d/%d within_1200=%d/%d max_tokens=%d misses=%v", reached, len(members), withinBudget, len(members), maxTokens, misses)
	if os.Getenv("GRAPHI_PRODUCT_COMPACT_DEV_REQUIRE") == "1" && (reached != len(members) || withinBudget != len(members)) {
		t.Fatalf("production compact development target missed")
	}
}
