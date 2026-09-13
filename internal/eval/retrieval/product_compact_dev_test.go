package retrieval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
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
	comparatorRaw, err := os.ReadFile(filepath.Join(moduleRoot, "docs/eval/retrieval/runs/2026-09-07-grepread-v2-dev/grepread-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	var comparator struct {
		DatasetSHA string `json:"dataset_sha256"`
		Queries    []struct {
			QueryID              string `json:"query_id"`
			TokensToFirstOverlap int    `json:"tokens_to_first_overlap"`
		} `json:"queries"`
	}
	if err := json.Unmarshal(comparatorRaw, &comparator); err != nil {
		t.Fatal(err)
	}
	if comparator.DatasetSHA != loaded.SHA256 {
		t.Fatal("GrepRead/2 comparator has unexpected dataset identity")
	}
	comparatorTokens := make(map[string]int, len(comparator.Queries))
	for _, query := range comparator.Queries {
		comparatorTokens[query.QueryID] = query.TokensToFirstOverlap
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
	sourceBudget := taskcompact.DefaultSourceBudget
	if raw := os.Getenv("GRAPHI_PRODUCT_COMPACT_DEV_SOURCE_BUDGET"); raw != "" {
		sourceBudget, err = strconv.Atoi(raw)
		if err != nil || sourceBudget < 1 || sourceBudget > SavingsCandidateBudget {
			t.Fatalf("invalid GRAPHI_PRODUCT_COMPACT_DEV_SOURCE_BUDGET %q", raw)
		}
	}
	reached, allOverlapped, anyComplete, requiredComplete, allComplete, withinBudget, maxTokens := 0, 0, 0, 0, 0, 0, 0
	var tokenCounts []int
	var pairedSavings []int
	var pairedSavingsPercent []float64
	cheaperThanComparator := 0
	var misses []string
	var incomplete []string
	type stratumMeasurement struct {
		total, reached, anyComplete, allComplete int
	}
	byStratum := make(map[string]*stratumMeasurement)
	for _, member := range members {
		query := queries[member.QueryID]
		stratum := string(query.Stratum)
		if byStratum[stratum] == nil {
			byStratum[stratum] = &stratumMeasurement{}
		}
		byStratum[stratum].total++
		bundle, err := taskContextBundleFromCandidateBytes(inputs[member.QueryID].Bytes)
		if err != nil {
			t.Fatalf("%s input: %v", member.QueryID, err)
		}
		legacy, err := contract.Serialize(&bundle)
		if err != nil {
			t.Fatal(err)
		}
		first, err := taskcompact.Build(t.Context(), query.Text, legacy, repository, sourceBudget)
		if err != nil {
			t.Fatalf("%s build: %v", member.QueryID, err)
		}
		second, err := taskcompact.Build(t.Context(), query.Text, legacy, repository, sourceBudget)
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
		if sourceBudget == taskcompact.DefaultSourceBudget {
			if _, err := ValidateCompactCandidateBundleBytes(member.QueryID, wire); err != nil {
				t.Fatalf("%s compact wire validation: %v", member.QueryID, err)
			}
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
		if os.Getenv("GRAPHI_PRODUCT_COMPACT_DEV_TRACE") == "1" && query.Stratum == StratumArchitectureFlow {
			usedFields := 0
			for _, source := range first.Structured.Sources {
				usedFields += len(strings.Fields(source.Text))
			}
			t.Logf("architecture wire: id=%s real_tokens=%d source_fields=%d", member.QueryID, tokens, usedFields)
		}
		tokenCounts = append(tokenCounts, tokens)
		baselineTokens := comparatorTokens[member.QueryID]
		if baselineTokens < 1 {
			t.Fatalf("%s missing GrepRead/2 equal-recall token count", member.QueryID)
		}
		if tokens < baselineTokens {
			cheaperThanComparator++
		}
		pairedSavings = append(pairedSavings, baselineTokens-tokens)
		pairedSavingsPercent = append(pairedSavingsPercent, float64(baselineTokens-tokens)*100/float64(baselineTokens))
		covered := make(map[int]bool)
		complete := make(map[int]bool)
		grade3Spans := 0
		for _, judgement := range query.Judgements {
			if judgement.Grade == SavingsGrade {
				grade3Spans++
			}
		}
		for _, source := range first.Structured.Sources {
			raw, err := exactSourceSpan(repository, source.Path, source.StartLine, source.EndLine)
			if err != nil || raw != source.Text {
				t.Fatalf("%s unverifiable source %s:%d-%d: %v", member.QueryID, source.Path, source.StartLine, source.EndLine, err)
			}
			for i, judgement := range query.Judgements {
				if judgement.Grade == SavingsGrade && source.Path == judgement.Path && source.StartLine <= judgement.EndLine && source.EndLine >= judgement.StartLine {
					covered[i] = true
					if source.StartLine <= judgement.StartLine && source.EndLine >= judgement.EndLine {
						complete[i] = true
					}
				}
			}
		}
		if os.Getenv("GRAPHI_PRODUCT_COMPACT_DEV_TRACE") == "1" && query.Stratum == StratumConfigDocs {
			var targets, citations []string
			for _, judgement := range query.Judgements {
				if judgement.Grade == SavingsGrade {
					targets = append(targets, judgement.Path+":"+strconv.Itoa(judgement.StartLine)+"-"+strconv.Itoa(judgement.EndLine))
				}
			}
			for _, source := range first.Structured.Sources {
				citations = append(citations, source.Path+":"+strconv.Itoa(source.StartLine)+"-"+strconv.Itoa(source.EndLine))
			}
			t.Logf("config trace: id=%s query=%q targets=%s sources=%s", member.QueryID, query.Text, strings.Join(targets, ","), strings.Join(citations, ","))
		}
		if len(complete) > 0 {
			anyComplete++
			byStratum[stratum].anyComplete++
		}
		if len(complete) >= member.Target.RequiredSpans {
			requiredComplete++
		} else {
			var targets, citations []string
			for _, judgement := range query.Judgements {
				if judgement.Grade == SavingsGrade {
					targets = append(targets, judgement.Path+":"+strconv.Itoa(judgement.StartLine)+"-"+strconv.Itoa(judgement.EndLine))
				}
			}
			for _, source := range first.Structured.Sources {
				citations = append(citations, source.Path+":"+strconv.Itoa(source.StartLine)+"-"+strconv.Itoa(source.EndLine))
			}
			incomplete = append(incomplete, member.QueryID+" ["+string(query.Stratum)+"] "+strconv.Quote(query.Text)+" targets="+strings.Join(targets, ",")+" sources="+strings.Join(citations, ","))
		}
		if len(complete) == grade3Spans {
			allComplete++
			byStratum[stratum].allComplete++
		}
		if len(covered) == grade3Spans {
			allOverlapped++
		}
		if len(covered) >= member.Target.RequiredSpans {
			reached++
			byStratum[stratum].reached++
		} else {
			misses = append(misses, member.QueryID)
			var citations []string
			for _, source := range first.Structured.Sources {
				citations = append(citations, source.Path+":"+strconv.Itoa(source.StartLine)+"-"+strconv.Itoa(source.EndLine))
			}
			t.Logf("miss %s (%q): %v", member.QueryID, query.Text, citations)
		}
	}
	sort.Ints(tokenCounts)
	sort.Ints(pairedSavings)
	sort.Float64s(pairedSavingsPercent)
	medianTokens := 0.0
	medianSavingTokens := 0.0
	medianSavingPercent := 0.0
	if len(tokenCounts) > 0 {
		middle := len(tokenCounts) / 2
		medianTokens = float64(tokenCounts[middle])
		medianSavingTokens = float64(pairedSavings[middle])
		medianSavingPercent = pairedSavingsPercent[middle]
		if len(tokenCounts)%2 == 0 {
			medianTokens = float64(tokenCounts[middle-1]+tokenCounts[middle]) / 2
			medianSavingTokens = float64(pairedSavings[middle-1]+pairedSavings[middle]) / 2
			medianSavingPercent = (pairedSavingsPercent[middle-1] + pairedSavingsPercent[middle]) / 2
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
	sort.Strings(incomplete)
	var strata []string
	for stratum := range byStratum {
		strata = append(strata, stratum)
	}
	sort.Strings(strata)
	for _, stratum := range strata {
		measurement := byStratum[stratum]
		t.Logf("production compact dev stratum: name=%s reached=%d/%d any_complete=%d/%d all_complete=%d/%d", stratum, measurement.reached, measurement.total, measurement.anyComplete, measurement.total, measurement.allComplete, measurement.total)
	}
	for _, detail := range incomplete {
		t.Logf("incomplete %s", detail)
	}
	t.Logf("production compact dev: source_budget=%d reached=%d/%d all_overlapped=%d/%d any_complete=%d/%d required_complete=%d/%d all_complete=%d/%d within_1200=%d/%d median_tokens=%.1f max_tokens=%d cheaper_than_grepread=%d/%d median_saving_tokens=%.1f median_saving_percent=%.4f misses=%v", sourceBudget, reached, len(members), allOverlapped, len(members), anyComplete, len(members), requiredComplete, len(members), allComplete, len(members), withinBudget, len(members), medianTokens, maxTokens, cheaperThanComparator, len(members), medianSavingTokens, medianSavingPercent, misses)
	if os.Getenv("GRAPHI_PRODUCT_COMPACT_DEV_REQUIRE") == "1" && (reached != len(members) || withinBudget != len(members)) {
		t.Fatalf("production compact development target missed")
	}
}
