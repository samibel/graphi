package retrieval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/shape"
)

func TestCompactTaskContextDev_IsSingleEncodedDeterministicAndSourceVerifiable(t *testing.T) {
	counter := equalRecallFixtureCounter()
	input := compactTaskContextDevFixtureInput(t, counter)
	first, err := BuildCompactTaskContextDev("where is the answer", input, 7, counter)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildCompactTaskContextDev("where is the answer", input, 7, counter)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || !bytes.Equal(first.Bytes, second.Bytes) {
		t.Fatal("identical input did not produce byte-identical compact response")
	}
	var wire map[string]any
	if err := json.Unmarshal(first.Bytes, &wire); err != nil {
		t.Fatal(err)
	}
	result := wire["result"].(map[string]any)
	if _, ok := result["structuredContent"].(map[string]any); !ok {
		t.Fatal("structuredContent is not a directly encoded object")
	}
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if strings.HasPrefix(strings.TrimSpace(text), "{") {
		t.Fatal("fallback text contains a second JSON encoding")
	}

	query := Query{ID: "q", Split: SplitDev, Stratum: StratumNLBehaviour,
		Judgements: []Judgement{{Path: "answer.go", StartLine: 2, EndLine: 2, Grade: GradeMax}}}
	score, err := ScoreCompactTaskContextDev(
		fstest.MapFS{"answer.go": {Data: []byte("package answer\nthe exact answer\nmore context here\n")}},
		query, first.Bytes, RecallTarget{Grade: GradeMax, RequiredSpans: 1, TotalSpans: 1}, counter)
	if err != nil || !score.Reached || score.OverlapSpans != 1 || score.FullSpanCount != 1 || score.Tokens != len(first.Bytes) {
		t.Fatalf("score = %+v err:%v", score, err)
	}
}

func TestCompactTaskContextDev_FailsClosedOnMalformedDigestAndSourceDrift(t *testing.T) {
	counter := equalRecallFixtureCounter()
	input := compactTaskContextDevFixtureInput(t, counter)

	t.Run("input digest drift", func(t *testing.T) {
		drifted := input
		drifted.SHA256 = strings.Repeat("0", 64)
		if _, err := BuildCompactTaskContextDev("answer", drifted, 7, counter); err == nil || !strings.Contains(err.Error(), "digest") {
			t.Fatalf("error = %v", err)
		}
	})

	payload, err := BuildCompactTaskContextDev("answer", input, 7, counter)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("malformed response", func(t *testing.T) {
		if _, _, err := ParseCompactTaskContextDev([]byte(`{"jsonrpc":`)); err == nil || !strings.Contains(err.Error(), "malformed") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("unknown versioned field", func(t *testing.T) {
		mutated := bytes.Replace(payload.Bytes, []byte(`"isError":false`), []byte(`"unknown":1,"isError":false`), 1)
		if _, _, err := ParseCompactTaskContextDev(mutated); err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("span drift", func(t *testing.T) {
		mutated := bytes.Replace(payload.Bytes, []byte(`"end_line":3`), []byte(`"end_line":1`), 1)
		if _, _, err := ParseCompactTaskContextDev(mutated); err == nil || !strings.Contains(err.Error(), "invalid or duplicate source") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("pinned source drift", func(t *testing.T) {
		query := Query{ID: "q", Split: SplitDev, Stratum: StratumNLBehaviour,
			Judgements: []Judgement{{Path: "answer.go", StartLine: 2, EndLine: 2, Grade: GradeMax}}}
		_, err := ScoreCompactTaskContextDev(
			fstest.MapFS{"answer.go": {Data: []byte("package answer\na different answer\nmore context here\n")}},
			query, payload.Bytes, RecallTarget{Grade: GradeMax, RequiredSpans: 1, TotalSpans: 1}, counter)
		if err == nil || !strings.Contains(err.Error(), "source differs") {
			t.Fatalf("error = %v", err)
		}
	})
}

func compactTaskContextDevFixtureInput(t *testing.T, counter PayloadCounter) PreservedPayload {
	t.Helper()
	bundle := contract.Result{
		Outcome: contract.OutcomePartial,
		Summary: `task_context/2: 1 seed(s) for "answer" — 1 files (task_context/2; retrieval/7; weights abc123; model fixture-model; 7/1200 snippet tokens; context-definitions/3; strategy semantic_first; degradation: ready)`,
		Evidence: []contract.Evidence{{
			RefID: "e1", Path: "answer.go", Line: 2, Span: "2-3", Role: "snippet",
			Snippet: "the exact answer\nmore context here", TextHash: shape.TextHash("the exact answer\nmore context here"),
		}},
		Confidence: contract.Confidence{Distribution: map[string]float64{"confirmed": 1}, Top: "confirmed", Method: "edge_tiers"},
	}
	return equalRecallFixturePayload(t, bundle, counter)
}

func TestCompactTaskContextDevSelect_KeepsCoherentImplementationAheadOfTestNameDecoys(t *testing.T) {
	query := "how are commands added to a parent and their parent pointer set"
	targetText := strings.Join([]string{
		"func (c *Command) AddCommand(cmds ...*Command) {",
		"\tfor _, x := range cmds {",
		"\t\tif x == nil {",
		"\t\t\tcontinue",
		"\t\t}",
		"\t\tc.commands = append(c.commands, x)",
		"\t\tx.parent = c",
		"\t}",
		"}",
	}, "\n")
	evidence := []contract.Evidence{{
		RefID: "target", Path: "command.go", Line: 100, Span: "100-108", Role: "snippet",
		Snippet: targetText, TextHash: shape.TextHash(targetText),
	}}
	items := []contract.Item{{RefID: "target-item", EvidenceRefIDs: []string{"target"}}}
	for i := 0; i < 14; i++ {
		ref := fmt.Sprintf("decoy-%02d", i)
		text := fmt.Sprintf("// command parent pointer set when added to the root command tree\nfunc TestUnrelated%02d(t *testing.T) {}", i)
		evidence = append(evidence, contract.Evidence{
			RefID: ref, Path: "command_test.go", Line: 200 + i*2, Span: fmt.Sprintf("%d-%d", 200+i*2, 201+i*2),
			Role: "snippet", Snippet: text, TextHash: shape.TextHash(text),
		})
		items = append(items, contract.Item{RefID: ref + "-item", EvidenceRefIDs: []string{ref}})
	}

	sources, used, err := compactTaskContextDevSelect(query, evidence, items, 140)
	if err != nil {
		t.Fatal(err)
	}
	if used > 140 {
		t.Fatalf("used %d source fields, want <= 140", used)
	}
	for _, source := range sources {
		if source.Path == "command.go" && strings.Contains(source.Text, "c.commands = append") && strings.Contains(source.Text, "x.parent = c") {
			return
		}
	}
	t.Fatalf("coherent AddCommand implementation was fragmented: %+v", sources)
}

// TestCompactTaskContextDevFrontier is opt-in and DEVELOPMENT-ONLY. Bundle
// construction finishes without judgements; scoring and the paired comparison
// happen only after each response has been serialized and preserved.
func TestCompactTaskContextDevFrontier(t *testing.T) {
	out := os.Getenv("GRAPHI_COMPACT_WIRE_DEV_OUT")
	repositoryRoot := os.Getenv("GRAPHI_COMPACT_WIRE_DEV_COBRA")
	grepReadPath := os.Getenv("GRAPHI_COMPACT_WIRE_DEV_GREPREAD")
	if out == "" || repositoryRoot == "" || grepReadPath == "" {
		t.Skip("set GRAPHI_COMPACT_WIRE_DEV_OUT, GRAPHI_COMPACT_WIRE_DEV_COBRA and GRAPHI_COMPACT_WIRE_DEV_GREPREAD")
	}
	moduleRoot := taskContextModuleRoot(t)
	loaded, err := LoadDataset(filepath.Join(moduleRoot, "docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/dataset.json"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SHA256 != payloadCostDevDatasetSHA256 || len(loaded.Dataset.Queries) != payloadCostDevQueries {
		t.Fatal("development dataset identity changed")
	}
	for _, query := range loaded.Dataset.Queries {
		if query.Split != SplitDev {
			t.Fatal("refusing non-development query", query.ID)
		}
	}
	head, err := CheckoutHEAD(t.Context(), repositoryRoot)
	if err != nil || head != loaded.Dataset.RepoSHA {
		t.Fatalf("checkout = %s, want %s: %v", head, loaded.Dataset.RepoSHA, err)
	}
	clean, err := GitRepoProbe().WorktreeClean(t.Context(), repositoryRoot)
	if err != nil || !clean {
		t.Fatalf("checkout must be clean: %v", err)
	}
	if err := CheckSpanCoverage(repositoryRoot, loaded.Dataset); err != nil {
		t.Fatal(err)
	}
	counter, err := LoadPinnedRealPayloadCounter()
	if err != nil {
		t.Fatal(err)
	}

	bundlePath := filepath.Join(moduleRoot, "docs/eval/retrieval/runs/2026-09-07-answer-recovery-dev/bundles-after.json")
	bundleRaw, err := os.ReadFile(bundlePath)
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
	if err := json.Unmarshal(bundleRaw, &captures); err != nil {
		t.Fatal(err)
	}
	if captures.DatasetSHA != loaded.SHA256 || len(captures.Runs) != 2 || len(captures.Runs[0]) != payloadCostDevQueries || len(captures.Runs[1]) != payloadCostDevQueries {
		t.Fatal("bundle artifact is not two complete development builds")
	}
	inputs := make(map[string]PreservedPayload)
	for _, row := range captures.Runs[0] {
		inputs[row.QueryID] = row.Capture.Payload
	}
	for _, row := range captures.Runs[1] {
		prior, ok := inputs[row.QueryID]
		if !ok || !bytes.Equal(prior.Bytes, row.Capture.Payload.Bytes) {
			t.Fatal("input bundle builds differ", row.QueryID)
		}
	}

	grepReadRaw, err := os.ReadFile(grepReadPath)
	if err != nil {
		t.Fatal(err)
	}
	const grepReadSHA = "ed58d7d0d9e1e6f7f6f790ebfb7a1249ecd6d80aea34de97b8e9c05a473f436b"
	if SHA256Hex(grepReadRaw) != grepReadSHA {
		t.Fatalf("GrepRead/2 sha256=%s, want %s", SHA256Hex(grepReadRaw), grepReadSHA)
	}
	var grepRead struct {
		Status     string   `json:"status"`
		DatasetSHA string   `json:"dataset_sha256"`
		RepoSHA    string   `json:"repo_sha"`
		Population int      `json:"grade3_answerable_dev_queries"`
		Reached    int      `json:"queries_reaching_equal_recall_overlap"`
		Identical  int      `json:"identical_transcripts"`
		Misses     []string `json:"zero_overlap_query_ids"`
		Queries    []struct {
			QueryID string `json:"query_id"`
			Grade3  int    `json:"grade3_spans"`
			Tokens  *int   `json:"tokens_to_first_overlap"`
		} `json:"queries"`
	}
	if err := json.Unmarshal(grepReadRaw, &grepRead); err != nil {
		t.Fatal(err)
	}
	if grepRead.Status != "development_diagnostic_not_release" || grepRead.DatasetSHA != loaded.SHA256 || grepRead.RepoSHA != head || grepRead.Population != 40 || grepRead.Reached != 40 || grepRead.Identical != 44 || len(grepRead.Misses) != 0 || len(grepRead.Queries) != 44 {
		t.Fatal("GrepRead/2 artifact shape or identity is invalid")
	}
	grepTokens := make(map[string]int)
	for _, row := range grepRead.Queries {
		if row.Grade3 == 0 {
			continue
		}
		if row.Tokens == nil || *row.Tokens < 1 || grepTokens[row.QueryID] != 0 {
			t.Fatal("invalid GrepRead/2 earliest target row", row.QueryID)
		}
		grepTokens[row.QueryID] = *row.Tokens
	}
	if len(grepTokens) != 40 {
		t.Fatal("GrepRead/2 does not cover the complete answerable population")
	}

	members, err := SelectEqualRecallDevPopulation(loaded.Dataset)
	if err != nil {
		t.Fatal(err)
	}
	queries := make(map[string]Query)
	for _, query := range loaded.Dataset.Queries {
		queries[query.ID] = query
	}
	type row struct {
		QueryID          string  `json:"query_id"`
		Reached          bool    `json:"reached_equal_recall"`
		FullSpanCount    int     `json:"complete_grade3_spans"`
		FirstOverlapRank int     `json:"first_overlap_source_rank"`
		CandidateTokens  int     `json:"candidate_tokens"`
		GrepReadTokens   int     `json:"grepread_tokens"`
		SavingTokens     int     `json:"saving_tokens"`
		SavingPercent    float64 `json:"saving_percent"`
		SourceSpans      int     `json:"source_spans"`
		SourceBytes      int     `json:"source_bytes"`
		SourceRealTokens int     `json:"source_cl100k_tokens"`
		PayloadSHA256    string  `json:"payload_sha256"`
	}
	type grid struct {
		SourceBudget           int      `json:"source_budget_whitespace_tokens"`
		Reached                int      `json:"queries_reaching_equal_recall"`
		CompleteQueries        int      `json:"queries_with_complete_grade3_span"`
		Misses                 []string `json:"miss_query_ids,omitempty"`
		ByteIdentical          int      `json:"byte_identical_rebuilds"`
		CandidateMeanTokens    float64  `json:"candidate_mean_tokens"`
		CandidateMedianTokens  float64  `json:"candidate_median_tokens"`
		MeanSourceBytes        float64  `json:"mean_source_bytes"`
		MeanSourceRealTokens   float64  `json:"mean_source_cl100k_tokens"`
		MedianSourceSpans      float64  `json:"median_source_spans"`
		MedianFirstOverlapRank float64  `json:"median_first_overlap_source_rank"`
		MaxFirstOverlapRank    int      `json:"max_first_overlap_source_rank"`
		MedianSavingTokens     float64  `json:"paired_median_saving_tokens"`
		MedianSavingPercent    float64  `json:"paired_median_saving_percent"`
		CandidateCheaper       int      `json:"candidate_cheaper_queries"`
		Ties                   int      `json:"equal_cost_queries"`
		CandidateMoreExpensive int      `json:"candidate_more_expensive_queries"`
		Rows                   []row    `json:"queries"`
	}
	report := struct {
		Version                    string         `json:"version"`
		Scope                      string         `json:"scope"`
		Status                     string         `json:"status"`
		DatasetSHA256              string         `json:"dataset_sha256"`
		RepoSHA                    string         `json:"repo_sha"`
		InputBundlesSHA256         string         `json:"input_bundles_sha256"`
		GrepReadSHA256             string         `json:"grepread_v2_sha256"`
		TokenizerID                string         `json:"tokenizer_id"`
		TokenizerVocabularySHA256  string         `json:"tokenizer_vocabulary_sha256"`
		Population                 int            `json:"answerable_dev_queries"`
		IndependentInputBuilds     int            `json:"independent_input_builds"`
		Contract                   map[string]any `json:"wire_contract"`
		OmittedWithInvariant       []string       `json:"omitted_with_invariant"`
		Grid                       []grid         `json:"frontier"`
		MinimumBudgetForFullRecall *int           `json:"minimum_budget_for_40_of_40,omitempty"`
		RecommendedBudget          *int           `json:"recommended_budget,omitempty"`
	}{
		Version: CompactTaskContextDevVersion, Scope: "development_only", Status: "development_diagnostic_not_release",
		DatasetSHA256: loaded.SHA256, RepoSHA: head, InputBundlesSHA256: SHA256Hex(bundleRaw), GrepReadSHA256: grepReadSHA,
		TokenizerID: counter.TokenizerID, TokenizerVocabularySHA256: counter.VocabularySHA256,
		Population: 40, IndependentInputBuilds: 2,
		Contract: map[string]any{
			"encoding":   "MCP result.content carries one concise text fallback; result.structuredContent is one directly encoded object, never JSON text",
			"source":     "ordered exact repository spans with path/start/end/text; response sha256 plus pinned-source verification detect drift",
			"provenance": "full input sha256 plus method/retrieval/weights/model/source-selection identities and explicit source budget unit",
			"budget":     "unchanged 1200 whitespace-fields-v1 maximum; lower rows are deterministic prefix frontiers",
		},
		OmittedWithInvariant: []string{
			"items and claim-only evidence: source order replaces the ref-id join; the preserved input sha256 binds omitted ranking metadata",
			"confidence distribution: not required to verify source; every included span is checked byte-for-byte against pinned Cobra",
			"verbose production summary and limits.next: concise fallback plus explicit provenance/budget/truncated retain their operational meaning",
		},
	}

	budgets := []int{80, 100, 120, 140, 160, 180, 200, 250, 300, 450, 600, 900, 1200}
	for _, budget := range budgets {
		g := grid{SourceBudget: budget}
		var candidateTokens, sourceBytes, sourceRealTokens, sourceSpans, overlapRanks, savingTokens []int
		var savingPercents []float64
		for _, member := range members {
			query := queries[member.QueryID]
			first, err := BuildCompactTaskContextDev(query.Text, inputs[member.QueryID], budget, counter)
			if err != nil {
				t.Fatalf("budget %d query %s: %v", budget, member.QueryID, err)
			}
			second, err := BuildCompactTaskContextDev(query.Text, inputs[member.QueryID], budget, counter)
			if err != nil || !reflect.DeepEqual(first, second) || !bytes.Equal(first.Bytes, second.Bytes) {
				t.Fatalf("budget %d query %s is not byte reproducible: %v", budget, member.QueryID, err)
			}
			g.ByteIdentical++
			score, err := ScoreCompactTaskContextDev(os.DirFS(repositoryRoot), query, first.Bytes, member.Target, counter)
			if err != nil {
				t.Fatalf("budget %d query %s score: %v", budget, member.QueryID, err)
			}
			_, structured, err := ParseCompactTaskContextDev(first.Bytes)
			if err != nil {
				t.Fatal(err)
			}
			r := row{QueryID: member.QueryID, Reached: score.Reached, FullSpanCount: score.FullSpanCount, FirstOverlapRank: score.FirstOverlap, CandidateTokens: score.Tokens, GrepReadTokens: grepTokens[member.QueryID], PayloadSHA256: first.SHA256}
			for _, source := range structured.Sources {
				r.SourceSpans++
				r.SourceBytes += len([]byte(source.Text))
				count, err := counter.Count([]byte(source.Text))
				if err != nil {
					t.Fatal(err)
				}
				r.SourceRealTokens += count
			}
			r.SavingTokens = r.GrepReadTokens - r.CandidateTokens
			r.SavingPercent = float64(r.SavingTokens) * 100 / float64(r.GrepReadTokens)
			if score.Reached {
				g.Reached++
			} else {
				g.Misses = append(g.Misses, member.QueryID)
			}
			if score.FullSpanCount > 0 {
				g.CompleteQueries++
			}
			switch {
			case r.SavingTokens > 0:
				g.CandidateCheaper++
			case r.SavingTokens == 0:
				g.Ties++
			default:
				g.CandidateMoreExpensive++
			}
			candidateTokens = append(candidateTokens, r.CandidateTokens)
			sourceBytes = append(sourceBytes, r.SourceBytes)
			sourceRealTokens = append(sourceRealTokens, r.SourceRealTokens)
			sourceSpans = append(sourceSpans, r.SourceSpans)
			if score.FirstOverlap > 0 {
				overlapRanks = append(overlapRanks, score.FirstOverlap)
				g.MaxFirstOverlapRank = max(g.MaxFirstOverlapRank, score.FirstOverlap)
			}
			savingTokens = append(savingTokens, r.SavingTokens)
			savingPercents = append(savingPercents, r.SavingPercent)
			g.Rows = append(g.Rows, r)
		}
		g.CandidateMeanTokens = compactTaskContextDevMean(candidateTokens)
		g.CandidateMedianTokens = compactTaskContextDevMedian(candidateTokens)
		g.MeanSourceBytes = compactTaskContextDevMean(sourceBytes)
		g.MeanSourceRealTokens = compactTaskContextDevMean(sourceRealTokens)
		g.MedianSourceSpans = compactTaskContextDevMedian(sourceSpans)
		g.MedianFirstOverlapRank = compactTaskContextDevMedian(overlapRanks)
		g.MedianSavingTokens = compactTaskContextDevMedian(savingTokens)
		g.MedianSavingPercent = compactTaskContextDevMedianFloat(savingPercents)
		if g.Reached == len(members) && report.MinimumBudgetForFullRecall == nil {
			v := budget
			report.MinimumBudgetForFullRecall = &v
		}
		if g.Reached == len(members) && g.MedianSavingTokens > 0 && report.RecommendedBudget == nil {
			v := budget
			report.RecommendedBudget = &v
		}
		report.Grid = append(report.Grid, g)
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, g := range report.Grid {
		t.Logf("budget=%d reached=%d/40 complete=%d/40 candidate mean/median=%.1f/%.1f source cl100k mean=%.1f spans median=%.1f answer rank median/max=%.1f/%d saving median=%+.1f (%+.1f%%) cheaper=%d/40 misses=%v",
			g.SourceBudget, g.Reached, g.CompleteQueries, g.CandidateMeanTokens, g.CandidateMedianTokens, g.MeanSourceRealTokens, g.MedianSourceSpans, g.MedianFirstOverlapRank, g.MaxFirstOverlapRank,
			g.MedianSavingTokens, g.MedianSavingPercent, g.CandidateCheaper, g.Misses)
	}
}

func compactTaskContextDevMean(values []int) float64 {
	total := 0
	for _, value := range values {
		total += value
	}
	return float64(total) / float64(len(values))
}

func compactTaskContextDevMedianFloat(values []float64) float64 {
	copyValues := append([]float64(nil), values...)
	sort.Float64s(copyValues)
	mid := len(copyValues) / 2
	if len(copyValues)%2 == 1 {
		return copyValues[mid]
	}
	return (copyValues[mid-1] + copyValues[mid]) / 2
}
