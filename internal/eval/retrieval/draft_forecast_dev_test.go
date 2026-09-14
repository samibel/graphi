package retrieval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/resolve"
	taskcompact "github.com/samibel/graphi/engine/agenttools/taskctx/compact"
	_ "github.com/samibel/graphi/engine/embed/ollama" // opt-in selector; registration performs no I/O
	staticembed "github.com/samibel/graphi/engine/embed/static"
	"github.com/samibel/graphi/engine/query"
	engineretrieval "github.com/samibel/graphi/engine/retrieval"
)

// TestDraftDevForecast runs the production compact MCP path over a
// development-split dataset that has the sealed holdouts' shape — one grade-3
// span per question — and reports, per stratum, how many questions receive
// that span overlapped and complete, plus the binomial chance that the
// observed complete rate would clear a 56-of-64 bar.
//
// It is a development forecast, not evidence: the dataset it is pointed at
// may be an unreviewed draft, and the number it prints is only as good as
// that draft. It refuses any query outside the development split, so it can
// never be pointed at a sealed key. It writes no artifact unless asked.
func TestDraftDevForecast(t *testing.T) {
	datasetPath := os.Getenv("GRAPHI_DRAFT_DATASET")
	root := os.Getenv("GRAPHI_PRODUCT_COMPACT_DEV_COBRA")
	if datasetPath == "" || root == "" {
		t.Skip("set GRAPHI_DRAFT_DATASET and GRAPHI_PRODUCT_COMPACT_DEV_COBRA")
	}
	moduleRoot := taskContextModuleRoot(t)
	if !filepath.IsAbs(datasetPath) {
		datasetPath = filepath.Join(moduleRoot, datasetPath)
	}
	loaded, err := LoadDataset(datasetPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range loaded.Dataset.Queries {
		if q.Split != SplitDev {
			t.Fatalf("refusing non-development query %s: this forecast never runs over a sealed split", q.ID)
		}
	}
	head, err := CheckoutHEAD(context.Background(), root)
	if err != nil || !strings.EqualFold(head, loaded.Dataset.RepoSHA) {
		t.Fatalf("checkout is at %q, dataset cites %q: %v", head, loaded.Dataset.RepoSHA, err)
	}
	if err := CheckSpanCoverage(root, loaded.Dataset); err != nil {
		t.Fatal(err)
	}
	counter, err := LoadPinnedRealPayloadCounter()
	if err != nil {
		t.Fatal(err)
	}
	selector := os.Getenv("GRAPHI_RECOVERY_EMBEDDER")
	if selector == "" {
		selector = staticembed.PinnedSelector
	}
	idx, err := buildTaskContextIndex(context.Background(), root, t.TempDir(), selector, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.store.Close()
	qs := query.New(idx.store)
	engine := engineretrieval.New(resolve.Deps{Query: qs, Search: idx.search}, idx.search, idx.store)
	repository := os.DirFS(root)

	type tally struct{ total, overlapped, complete, cited int }
	byStratum := map[string]*tally{}
	all := &tally{}
	var tokens []int
	var shares []float64
	var incomplete []string
	for _, q := range loaded.Dataset.Queries {
		var span *Judgement
		for i := range q.Judgements {
			if q.Judgements[i].Grade == SavingsGrade {
				if span != nil {
					t.Fatalf("%s carries more than one grade-3 span; this forecast is for holdout-shaped keys", q.ID)
				}
				span = &q.Judgements[i]
			}
		}
		if span == nil {
			continue
		}
		captured, err := captureOneCandidateBundle(context.Background(), CandidateCaptureOptions{RepoRoot: root, RealCounter: counter}, q, qs, idx, engine)
		if err != nil {
			t.Fatal(err)
		}
		var envelope candidateResponseEnvelope
		if err := json.Unmarshal(captured.Payload.Bytes, &envelope); err != nil {
			t.Fatal(err)
		}
		var compact taskcompact.Structured
		if err := json.Unmarshal(envelope.Result.StructuredContent, &compact); err != nil {
			t.Fatal(err)
		}
		realTokens := 0
		for _, count := range captured.Payload.TokenCounts {
			if count.TokenizerID == counter.TokenizerID {
				realTokens = count.Tokens
			}
		}
		tokens = append(tokens, realTokens)
		lines := map[int]bool{}
		overlapped, complete, cited := false, false, false
		for _, source := range compact.Sources {
			raw, err := exactSourceSpan(repository, source.Path, source.StartLine, source.EndLine)
			if err != nil || raw != source.Text {
				t.Fatalf("%s unverifiable source %s:%d-%d", q.ID, source.Path, source.StartLine, source.EndLine)
			}
			if source.Path != span.Path {
				continue
			}
			if SpanMatches(source.Path, source.StartLine, *span) {
				cited = true
			}
			if source.StartLine <= span.EndLine && source.EndLine >= span.StartLine {
				overlapped = true
				for line := max(source.StartLine, span.StartLine); line <= min(source.EndLine, span.EndLine); line++ {
					lines[line] = true
				}
			}
			if source.StartLine <= span.StartLine && source.EndLine >= span.EndLine {
				complete = true
			}
		}
		share := float64(len(lines)) / float64(span.EndLine-span.StartLine+1)
		shares = append(shares, share)
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
			if cited {
				tl.cited++
			}
		}
		if !complete {
			var cites []string
			for _, source := range compact.Sources {
				cites = append(cites, fmt.Sprintf("%s:%d-%d", source.Path, source.StartLine, source.EndLine))
			}
			incomplete = append(incomplete, fmt.Sprintf("%s [%s] share=%.2f target=%s:%d-%d sources=%s", q.ID, q.Stratum, share, span.Path, span.StartLine, span.EndLine, strings.Join(cites, ",")))
		}
	}
	strata := make([]string, 0, len(byStratum))
	for s := range byStratum {
		strata = append(strata, s)
	}
	sort.Strings(strata)
	for _, s := range strata {
		tl := byStratum[s]
		t.Logf("forecast stratum %-18s overlapped=%d/%d complete=%d/%d cited=%d/%d", s, tl.overlapped, tl.total, tl.complete, tl.total, tl.cited, tl.total)
	}
	for _, line := range incomplete {
		t.Logf("incomplete %s", line)
	}
	sort.Ints(tokens)
	meanShare := 0.0
	for _, s := range shares {
		meanShare += s
	}
	meanShare /= float64(max(1, len(shares)))
	rate := float64(all.complete) / float64(max(1, all.total))
	t.Logf("forecast dataset=%s sha=%s candidate_rows=%d overlapped=%d complete=%d cited=%d complete_rate=%.4f mean_span_share=%.4f median_tokens=%d max_tokens=%d",
		loaded.Dataset.ID, loaded.SHA256[:12], all.total, all.overlapped, all.complete, all.cited, rate, meanShare, tokens[len(tokens)/2], tokens[len(tokens)-1])
	t.Logf("forecast: if the true per-question complete rate were %.4f, P(>=56 of 64) = %.3f; at the overlap rate %.4f, P = %.3f",
		rate, forecastUpperTail(64, 56, rate), float64(all.overlapped)/float64(max(1, all.total)), forecastUpperTail(64, 56, float64(all.overlapped)/float64(max(1, all.total))))
}

func forecastUpperTail(n, k int, p float64) float64 {
	total := 0.0
	for i := k; i <= n; i++ {
		total += math.Exp(forecastLnChoose(n, i) + float64(i)*math.Log(p) + float64(n-i)*math.Log(1-p))
	}
	return total
}

func forecastLnChoose(n, k int) float64 {
	a, _ := math.Lgamma(float64(n + 1))
	b, _ := math.Lgamma(float64(k + 1))
	c, _ := math.Lgamma(float64(n - k + 1))
	return a - b - c
}
