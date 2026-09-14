package retrieval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/contract"
	taskcompact "github.com/samibel/graphi/engine/agenttools/taskctx/compact"
)

// TestOneSpanCompactDev rebuilds the compact projection for a one-span
// development dataset from a pre-compact capture written by
// TestLegacyBundleDevCapture and reports, per stratum, how many spans are
// overlapped and delivered complete. It is the fast half of the
// holdout-shaped development loop; TestDraftDevForecast is the slow,
// production-path half. Neither is evidence. It refuses any query outside
// the development split.
func TestOneSpanCompactDev(t *testing.T) {
	root := os.Getenv("GRAPHI_PRODUCT_COMPACT_DEV_COBRA")
	datasetPath := os.Getenv("GRAPHI_ONE_SPAN_DATASET")
	capturePath := os.Getenv("GRAPHI_ONE_SPAN_BUNDLES")
	if root == "" || datasetPath == "" || capturePath == "" {
		t.Skip("set GRAPHI_PRODUCT_COMPACT_DEV_COBRA, GRAPHI_ONE_SPAN_DATASET and GRAPHI_ONE_SPAN_BUNDLES")
	}
	module := taskContextModuleRoot(t)
	if !filepath.IsAbs(datasetPath) {
		datasetPath = filepath.Join(module, datasetPath)
	}
	if !filepath.IsAbs(capturePath) {
		capturePath = filepath.Join(module, capturePath)
	}
	loaded, err := LoadDataset(datasetPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range loaded.Dataset.Queries {
		if q.Split != SplitDev {
			t.Fatalf("refusing non-development query %s", q.ID)
		}
	}
	raw, err := os.ReadFile(capturePath)
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
	if err := json.Unmarshal(raw, &captures); err != nil {
		t.Fatal(err)
	}
	if captures.DatasetSHA != loaded.SHA256 || len(captures.Runs) == 0 {
		t.Fatal("capture does not belong to this dataset")
	}
	inputs := map[string][]byte{}
	for _, row := range captures.Runs[0] {
		inputs[row.QueryID] = row.Capture.Payload.Bytes
	}
	counter, err := LoadPinnedRealPayloadCounter()
	if err != nil {
		t.Fatal(err)
	}
	repository := os.DirFS(root)
	// The source frontier may be swept for a diagnosis; only the default
	// frontier is held to the frozen serialized ceiling.
	sourceBudget := taskcompact.DefaultSourceBudget
	if raw := os.Getenv("GRAPHI_ONE_SPAN_BUDGET"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > SavingsCandidateBudget {
			t.Fatalf("invalid GRAPHI_ONE_SPAN_BUDGET %q", raw)
		}
		sourceBudget = parsed
	}
	type tally struct{ total, overlapped, complete int }
	byStratum := map[string]*tally{}
	all := &tally{}
	var tokens []int
	shareSum := 0.0
	var incomplete []string
	only := os.Getenv("GRAPHI_ONE_SPAN_ONLY")
	for _, q := range loaded.Dataset.Queries {
		if only != "" && q.ID != only {
			continue
		}
		var span *Judgement
		for i := range q.Judgements {
			if q.Judgements[i].Grade == SavingsGrade {
				if span != nil {
					t.Fatalf("%s carries more than one grade-3 span", q.ID)
				}
				span = &q.Judgements[i]
			}
		}
		if span == nil {
			continue
		}
		input, ok := inputs[q.ID]
		if !ok {
			t.Fatalf("capture has no row for %s", q.ID)
		}
		bundle, err := taskContextBundleFromCandidateBytes(input)
		if err != nil {
			t.Fatal(err)
		}
		legacy, err := contract.Serialize(&bundle)
		if err != nil {
			t.Fatal(err)
		}
		res, err := taskcompact.Build(t.Context(), q.Text, legacy, repository, sourceBudget)
		if err != nil {
			t.Fatalf("%s: %v", q.ID, err)
		}
		again, err := taskcompact.Build(t.Context(), q.Text, legacy, repository, sourceBudget)
		if err != nil || again.Summary != res.Summary || len(again.Structured.Sources) != len(res.Structured.Sources) {
			t.Fatalf("%s is not reproducible", q.ID)
		}
		type content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		wire, err := json.Marshal(struct {
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
		}{Content: []content{{Type: "text", Text: res.Summary}}, StructuredContent: res.Structured}})
		if err != nil {
			t.Fatal(err)
		}
		n, err := counter.Count(append(wire, '\n'))
		if err != nil {
			t.Fatal(err)
		}
		if n > SavingsCandidateBudget && sourceBudget == taskcompact.DefaultSourceBudget {
			t.Errorf("%s: %d cl100k tokens exceed the frozen ceiling", q.ID, n)
		}
		tokens = append(tokens, n)
		lines := map[int]bool{}
		overlapped, complete := false, false
		var cites []string
		for _, s := range res.Structured.Sources {
			text, err := exactSourceSpan(repository, s.Path, s.StartLine, s.EndLine)
			if err != nil || text != s.Text {
				t.Fatalf("%s unverifiable source %s:%d-%d", q.ID, s.Path, s.StartLine, s.EndLine)
			}
			cites = append(cites, fmt.Sprintf("%s:%d-%d", s.Path, s.StartLine, s.EndLine))
			if s.Path != span.Path {
				continue
			}
			if s.StartLine <= span.EndLine && s.EndLine >= span.StartLine {
				overlapped = true
				for l := max(s.StartLine, span.StartLine); l <= min(s.EndLine, span.EndLine); l++ {
					lines[l] = true
				}
			}
			if s.StartLine <= span.StartLine && s.EndLine >= span.EndLine {
				complete = true
			}
		}
		share := float64(len(lines)) / float64(span.EndLine-span.StartLine+1)
		shareSum += share
		if byStratum[q.Stratum] == nil {
			byStratum[q.Stratum] = &tally{}
		}
		for _, tl := range []*tally{byStratum[q.Stratum], all} {
			tl.total++
			if overlapped {
				tl.overlapped++
			}
			if complete {
				tl.complete++
			}
		}
		if !complete {
			// What the compact stage was given for this span, before any
			// selection: the bundle's own evidence windows on the span's path.
			// "covers" means the window contains the whole span; "touches"
			// means it overlaps it; nothing means the span never reached the
			// projector at all.
			var given []string
			for _, ev := range bundle.Evidence {
				if ev.Path != span.Path || ev.Snippet == "" {
					continue
				}
				from, to, err := exactEvidenceSpan(ev.Span)
				if err != nil || from > span.EndLine || to < span.StartLine {
					continue
				}
				relation := "touches"
				if from <= span.StartLine && to >= span.EndLine {
					relation = "covers"
				}
				given = append(given, fmt.Sprintf("%s:%d-%d(%s)", ev.RefID, from, to, relation))
			}
			if len(given) == 0 {
				given = []string{"none"}
			}
			incomplete = append(incomplete, fmt.Sprintf("%s [%s] share=%.2f target=%s:%d-%d given=%s sources=%s", q.ID, q.Stratum, share, span.Path, span.StartLine, span.EndLine, strings.Join(given, ","), strings.Join(cites, ",")))
		}
	}
	strata := make([]string, 0, len(byStratum))
	for s := range byStratum {
		strata = append(strata, s)
	}
	sort.Strings(strata)
	for _, s := range strata {
		t.Logf("one-span stratum %-18s overlapped=%d/%d complete=%d/%d", s, byStratum[s].overlapped, byStratum[s].total, byStratum[s].complete, byStratum[s].total)
	}
	if os.Getenv("GRAPHI_ONE_SPAN_TRACE") == "1" {
		for _, line := range incomplete {
			t.Logf("incomplete %s", line)
		}
	}
	if len(tokens) == 0 {
		t.Fatal("no one-span query measured")
	}
	sort.Ints(tokens)
	t.Logf("one-span: dataset=%s source_budget=%d rows=%d overlapped=%d complete=%d mean_share=%.4f median_tokens=%d max_tokens=%d", loaded.Dataset.ID, sourceBudget, all.total, all.overlapped, all.complete, shareSum/float64(all.total), tokens[len(tokens)/2], tokens[len(tokens)-1])
}
