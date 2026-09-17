package retrieval

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
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
	counter := loadHermeticRealPayloadCounterForTest(t)
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
	extents := newDeclarationExtents(repository)

	type tally struct{ total, overlapped, complete, cited, overlapped2, complete2 int }
	byStratum := map[string]*tally{}
	all := &tally{}
	var tokens []int
	var shares []float64
	var incomplete []string
	stageCounts := map[string]int{}
	// Second-response contract: the one read the response designates in its
	// followup field, charged by its own cl100k count. Reported beside the
	// single-response numbers, never folded into them.
	var followupTokens []int
	followupCompleted := 0
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
		score := func(sources []taskcompact.Source) (overlapped, complete, cited bool, lines map[int]bool) {
			lines = map[int]bool{}
			for _, source := range sources {
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
			return overlapped, complete, cited, lines
		}
		overlapped, complete, cited, lines := score(compact.Sources)
		overlapped2, complete2 := overlapped, complete
		if second, err := CaptureFollowupRead(repository, q.ID, captured.Payload, counter); err != nil {
			t.Fatal(err)
		} else if second != nil {
			read, err := followupReadSource(repository, q.ID, *compact.Followup)
			if err != nil {
				t.Fatal(err)
			}
			for _, count := range second.TokenCounts {
				if count.TokenizerID == counter.TokenizerID {
					followupTokens = append(followupTokens, count.Tokens)
				}
			}
			overlapped2, complete2, _, _ = score(append(append([]taskcompact.Source(nil), compact.Sources...), read))
			if complete2 && !complete {
				followupCompleted++
			}
		}
		share := float64(len(lines)) / float64(span.EndLine-span.StartLine+1)
		shares = append(shares, share)
		// Where in the existing 50-row retrieval window the span first
		// appears, so a miss can be attributed to retrieval (absent), to the
		// 15-candidate task-context cap (rank 16..50) or to selection.
		pool, err := engine.Retrieve(context.Background(), engineretrieval.Request{Query: q.Text, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		// A retrieval row carries only its declaration line. The target is
		// often a region inside that declaration, so a row counts when the
		// declaration it names, as parsed from the pinned checkout, overlaps
		// the target.
		rank50 := 0
		for rank, row := range pool.Rows {
			line, _, err := exactEvidenceSpan(row.Span)
			if err != nil || row.Path != span.Path {
				continue
			}
			start, end := extents.extent(row.Path, line)
			if q.Stratum == StratumExactPath {
				// A path query's rows are the file itself; the file contains
				// every span in it.
				start, end = span.StartLine, span.EndLine
			}
			if start <= span.EndLine && end >= span.StartLine {
				rank50 = rank + 1
				break
			}
		}
		switch {
		case complete:
		case rank50 == 0:
			stageCounts["absent_from_retrieval_window"]++
		case rank50 > 15:
			stageCounts["below_candidate_cap"]++
		default:
			stageCounts["lost_in_selection"]++
		}
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
			if overlapped2 {
				tl.overlapped2++
			}
			if complete2 {
				tl.complete2++
			}
		}
		if !complete {
			var cites []string
			for _, source := range compact.Sources {
				cites = append(cites, fmt.Sprintf("%s:%d-%d", source.Path, source.StartLine, source.EndLine))
			}
			followupNote := "none"
			if compact.Followup != nil {
				followupNote = fmt.Sprintf("%s:%d-%d(complete=%t)", compact.Followup.Path, compact.Followup.StartLine, compact.Followup.EndLine, complete2)
			}
			incomplete = append(incomplete, fmt.Sprintf("%s [%s] share=%.2f rank50=%d target=%s:%d-%d sources=%s followup=%s", q.ID, q.Stratum, share, rank50, span.Path, span.StartLine, span.EndLine, strings.Join(cites, ","), followupNote))
		}
	}
	strata := make([]string, 0, len(byStratum))
	for s := range byStratum {
		strata = append(strata, s)
	}
	sort.Strings(strata)
	for _, s := range strata {
		tl := byStratum[s]
		t.Logf("forecast stratum %-18s overlapped=%d/%d complete=%d/%d cited=%d/%d two_call_overlapped=%d/%d two_call_complete=%d/%d", s, tl.overlapped, tl.total, tl.complete, tl.total, tl.cited, tl.total, tl.overlapped2, tl.total, tl.complete2, tl.total)
	}
	for _, line := range incomplete {
		t.Logf("incomplete %s", line)
	}
	t.Logf("forecast misses by stage: absent_from_retrieval_window=%d below_candidate_cap=%d lost_in_selection=%d",
		stageCounts["absent_from_retrieval_window"], stageCounts["below_candidate_cap"], stageCounts["lost_in_selection"])
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
	sort.Ints(followupTokens)
	medianFollowup, maxFollowup := 0, 0
	if len(followupTokens) > 0 {
		medianFollowup, maxFollowup = followupTokens[len(followupTokens)/2], followupTokens[len(followupTokens)-1]
	}
	rate2 := float64(all.overlapped2) / float64(max(1, all.total))
	t.Logf("forecast two-call: overlapped=%d complete=%d followup_reads=%d completed_by_followup=%d median_followup_tokens=%d max_followup_tokens=%d; at the two-call overlap rate %.4f, P(>=56 of 64) = %.3f; at the two-call complete rate %.4f, P = %.3f",
		all.overlapped2, all.complete2, len(followupTokens), followupCompleted, medianFollowup, maxFollowup, rate2, forecastUpperTail(64, 56, rate2),
		float64(all.complete2)/float64(max(1, all.total)), forecastUpperTail(64, 56, float64(all.complete2)/float64(max(1, all.total))))
}

// declarationExtents maps a (path, declaration line) retrieval row to the
// full line range of the declaration it names: a Go top-level declaration
// including its doc comment, or a Markdown section from its heading to the
// next heading. Anything else is the line itself.
type declarationExtents struct {
	repository fs.FS
	goFiles    map[string][][2]int
	mdFiles    map[string][]string
}

func newDeclarationExtents(repository fs.FS) *declarationExtents {
	return &declarationExtents{repository: repository, goFiles: map[string][][2]int{}, mdFiles: map[string][]string{}}
}

func (d *declarationExtents) extent(path string, line int) (int, int) {
	switch {
	case strings.HasSuffix(path, ".go"):
		ranges, ok := d.goFiles[path]
		if !ok {
			raw, err := fs.ReadFile(d.repository, path)
			if err == nil {
				fset := token.NewFileSet()
				if file, err := parser.ParseFile(fset, path, raw, parser.ParseComments); err == nil {
					for _, decl := range file.Decls {
						start := fset.Position(decl.Pos()).Line
						switch x := decl.(type) {
						case *ast.FuncDecl:
							if x.Doc != nil {
								start = fset.Position(x.Doc.Pos()).Line
							}
						case *ast.GenDecl:
							if x.Doc != nil {
								start = fset.Position(x.Doc.Pos()).Line
							}
						}
						ranges = append(ranges, [2]int{start, fset.Position(decl.End()).Line})
					}
				}
			}
			d.goFiles[path] = ranges
		}
		for _, r := range ranges {
			if line >= r[0] && line <= r[1] {
				return r[0], r[1]
			}
		}
	case strings.HasSuffix(path, ".md"):
		lines, ok := d.mdFiles[path]
		if !ok {
			raw, err := fs.ReadFile(d.repository, path)
			if err == nil {
				lines = strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
			}
			d.mdFiles[path] = lines
		}
		if line >= 1 && line <= len(lines) && strings.HasPrefix(lines[line-1], "#") {
			end := len(lines)
			for i := line; i < len(lines); i++ {
				if strings.HasPrefix(lines[i], "#") {
					end = i
					break
				}
			}
			return line, end
		}
	}
	return line, line
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
