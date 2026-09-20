package retrieval

// Development diagnostics, not a release gate or a sufficiency rating. This
// deliberately loads the committed dev-only slice, never the sealed dataset.
// Capture uses the existing real MCP transport instrument. Qrels are consulted
// only afterwards, to diagnose source retention using the existing SpanMatches.
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	taskcompact "github.com/samibel/graphi/engine/agenttools/taskctx/compact"
	enginecontext "github.com/samibel/graphi/engine/context"
	"github.com/samibel/graphi/engine/embed"
	_ "github.com/samibel/graphi/engine/embed/ollama" // opt-in selector; registration performs no I/O
	staticembed "github.com/samibel/graphi/engine/embed/static"
	"github.com/samibel/graphi/engine/query"
	engineretrieval "github.com/samibel/graphi/engine/retrieval"
	evaltokenizer "github.com/samibel/graphi/internal/eval/tokenizer"
)

type recoveryObservation struct {
	QueryID               string                  `json:"query_id"`
	Stratum               string                  `json:"stratum"`
	Grade3Spans           int                     `json:"grade3_spans"`
	CitedSpans            int                     `json:"cited_spans"`
	SnippetOverlapSpans   int                     `json:"snippet_overlap_spans"`
	SnippetContainedSpans int                     `json:"snippet_contained_spans"`
	SnippetTokens         int                     `json:"snippet_whitespace_tokens"`
	Items                 int                     `json:"items"`
	Dropped               int                     `json:"items_dropped"`
	CandidateGrade3Ranks  []int                   `json:"grade3_ranks_in_50_candidates"`
	Candidates            []engineretrieval.Row   `json:"candidates_50,omitempty"`
	Capture               CapturedCandidateBundle `json:"capture"`
}

func TestRecoveryDevCapture(t *testing.T) {
	out := os.Getenv("GRAPHI_RECOVERY_OUT")
	if out == "" {
		t.Skip("set GRAPHI_RECOVERY_OUT and GRAPHI_RECOVERY_COBRA to capture development MCP payloads")
	}
	root := os.Getenv("GRAPHI_RECOVERY_COBRA")
	if root == "" {
		t.Fatal("GRAPHI_RECOVERY_COBRA is required")
	}
	module := taskContextModuleRoot(t)
	ds, err := LoadDataset(filepath.Join(module, "docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/dataset.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.Dataset.Queries) != 44 {
		t.Fatal("development population changed")
	}
	for _, q := range ds.Dataset.Queries {
		if q.Split != SplitDev {
			t.Fatal("refusing non-development query", q.ID)
		}
	}
	head, err := CheckoutHEAD(t.Context(), root)
	if err != nil || head != ds.Dataset.RepoSHA {
		t.Fatalf("checkout: %s %v", head, err)
	}
	clean, err := GitRepoProbe().WorktreeClean(t.Context(), root)
	if err != nil || !clean {
		t.Fatalf("indexed checkout must be clean: %v", err)
	}
	if err := CheckSpanCoverage(root, ds.Dataset); err != nil {
		t.Fatal(err)
	}
	counter := loadHermeticRealPayloadCounterForTest(t)
	candidateSHA := os.Getenv("GRAPHI_RECOVERY_CANDIDATE_SHA")
	if candidateSHA == "" {
		t.Fatal("GRAPHI_RECOVERY_CANDIDATE_SHA is required; development evidence must bind to a frozen candidate")
	}
	outAbs, err := filepath.Abs(out)
	if err != nil {
		t.Fatal(err)
	}
	moduleAbs, err := filepath.Abs(module)
	if err != nil {
		t.Fatal(err)
	}
	excluded, err := filepath.Rel(moduleAbs, filepath.Dir(outAbs))
	if err != nil {
		t.Fatal(err)
	}
	binding, err := ObserveCandidateBinding(t.Context(), GitRepoProbe(), CandidateBindingOptions{
		CandidateRoot: module, FrozenCandidateSHA: candidateSHA, ExcludePath: filepath.ToSlash(excluded),
		CheckoutRoot: root, CheckoutSHA: head,
	})
	if err != nil {
		t.Fatal(err)
	}
	var runs [2][]recoveryObservation
	var generations [2]string
	selector := os.Getenv("GRAPHI_RECOVERY_EMBEDDER")
	if selector == "" {
		selector = staticembed.PinnedSelector
	}
	type inputIdentity struct {
		NodeID   string `json:"node_id"`
		TextHash string `json:"text_hash"`
		Path     string `json:"path"`
		Start    int    `json:"start_line"`
		End      int    `json:"end_line"`
	}
	var inputs [2][]inputIdentity
	for build := range runs {
		t.Logf("independent build %d/2: constructing %s", build+1, selector)
		workDir := t.TempDir()
		idx, err := buildTaskContextIndex(t.Context(), root, workDir, selector, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		inputStore, err := embed.OpenSQLiteGenerationStore(t.Context(), filepath.Join(workDir, "task-context-eval-meta"))
		if err != nil {
			t.Fatal(err)
		}
		rows, readErr := inputStore.Load(t.Context(), idx.generationID)
		closeErr := inputStore.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("input manifest: %v %v", readErr, closeErr)
		}
		for _, row := range rows {
			inputs[build] = append(inputs[build], inputIdentity{string(row.NodeID), row.TextHash, row.Path, row.StartLine, row.EndLine})
		}
		t.Logf("independent build %d/2: %d input documents, capturing all %d dev queries", build+1, len(rows), len(ds.Dataset.Queries))
		generations[build] = idx.fingerprint.GraphGeneration
		qs := query.New(idx.store)
		engine := engineretrieval.New(resolve.Deps{Query: qs, Search: idx.search}, idx.search, idx.store)
		for _, q := range ds.Dataset.Queries {
			captured, err := captureOneCandidateBundle(context.Background(), CandidateCaptureOptions{RepoRoot: root, RealCounter: counter}, q, qs, idx, engine)
			if err != nil {
				t.Fatal(err)
			}
			var envelope candidateResponseEnvelope
			if err := json.Unmarshal(captured.Payload.Bytes, &envelope); err != nil {
				t.Fatal(err)
			}
			type sourceSpan struct {
				path       string
				start, end int
				text       string
			}
			var sources []sourceSpan
			obs := recoveryObservation{QueryID: q.ID, Stratum: q.Stratum, Capture: captured}
			if len(envelope.Result.StructuredContent) > 0 {
				var compact taskcompact.Structured
				if err := json.Unmarshal(envelope.Result.StructuredContent, &compact); err != nil {
					t.Fatal(err)
				}
				obs.Items = len(compact.Sources)
				for _, source := range compact.Sources {
					sources = append(sources, sourceSpan{path: source.Path, start: source.StartLine, end: source.EndLine, text: source.Text})
				}
			} else {
				var bundle contract.Result
				if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &bundle); err != nil {
					t.Fatal(err)
				}
				obs.Items, obs.Dropped = len(bundle.Items), bundle.Limits.Dropped
				for _, evidence := range bundle.Evidence {
					if evidence.Snippet == "" {
						continue
					}
					start, end := recoverySpan(t, evidence.Span)
					sources = append(sources, sourceSpan{path: evidence.Path, start: start, end: end, text: evidence.Snippet})
				}
			}
			reader := enginecontext.RootedReader{Root: root}
			for _, emitted := range sources {
				obs.SnippetTokens += len(strings.Fields(emitted.text))
				source, got, err := reader.ReadSpan(emitted.path, enginecontext.Span{Start: emitted.start, End: emitted.end})
				if err != nil || source != emitted.text || got.Start != emitted.start || got.End != emitted.end {
					t.Fatalf("%s snippet fails source roundtrip: %s:%d-%d %v", q.ID, emitted.path, emitted.start, emitted.end, err)
				}
			}
			if obs.SnippetTokens > SavingsCandidateBudget {
				t.Fatalf("%s exceeds frozen snippet budget", q.ID)
			}
			pool, err := engine.Retrieve(t.Context(), engineretrieval.Request{Query: q.Text, Limit: 50})
			if err != nil {
				t.Fatal(err)
			}
			obs.Candidates = pool.Rows
			for _, j := range q.Judgements {
				if j.Grade != GradeMax {
					continue
				}
				obs.Grade3Spans++
				firstRank := 0
				for rank, row := range pool.Rows {
					line, _ := recoverySpan(t, row.Span)
					if SpanMatches(row.Path, line, j) {
						firstRank = rank + 1
						break
					}
				}
				obs.CandidateGrade3Ranks = append(obs.CandidateGrade3Ranks, firstRank)
				cited, overlap := false, false
				covered := make(map[int]bool)
				for _, emitted := range sources {
					cited = cited || SpanMatches(emitted.path, emitted.start, j)
					for line := emitted.start; line <= emitted.end; line++ {
						if SpanMatches(emitted.path, line, j) {
							overlap = true
							covered[line] = true
						}
					}
				}
				if cited {
					obs.CitedSpans++
				}
				if overlap {
					obs.SnippetOverlapSpans++
				}
				if len(covered) == j.EndLine-j.StartLine+1 {
					obs.SnippetContainedSpans++
				}
			}
			runs[build] = append(runs[build], obs)
		}
		if err := idx.store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	identical := 0
	if generations[0] == generations[1] {
		t.Fatal("independent builds did not produce distinct freshness generations")
	}
	for i, a := range runs[0] {
		b := runs[1][i]
		if bytes.Equal(a.Capture.Payload.Bytes, b.Capture.Payload.Bytes) && a.Capture.Payload.SHA256 == b.Capture.Payload.SHA256 && reflect.DeepEqual(a.Capture.Payload.TokenCounts, b.Capture.Payload.TokenCounts) {
			identical++
		}
	}
	report := struct {
		DatasetSHA string                   `json:"dataset_sha256"`
		Contract   MeasurementContract      `json:"measurement_contract"`
		Binding    CandidateBinding         `json:"candidate_binding"`
		Note       string                   `json:"note"`
		Identical  int                      `json:"identical_payloads"`
		Queries    int                      `json:"queries"`
		Selector   string                   `json:"embedder_selector"`
		Inputs     [2][]inputIdentity       `json:"input_documents"`
		Runs       [2][]recoveryObservation `json:"independent_builds"`
	}{ds.SHA256, FrozenMeasurementContract(), binding, "Candidate-bound development diagnostic; not a release capture or sufficiency rating. Citation/overlap uses SpanMatches at exact grade 3; containment additionally requires every judged line in emitted source. All 44 dev rows are retained; three no_hit rows and cb-31 (no grade-3 judgement) are excluded from the 40-query grade-3 aggregates.", identical, len(runs[0]), selector, inputs, runs}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("independent builds: %d/%d identical MCP bytes, digests and token counts; distinct freshness generations=%t", identical, len(runs[0]), generations[0] != generations[1])
	if os.Getenv("GRAPHI_RECOVERY_REQUIRE_IDENTICAL") == "1" && identical != len(runs[0]) {
		t.Fatal("independent indexes produced different MCP payloads")
	}
}

func recoverySpan(t *testing.T, span string) (int, int) {
	t.Helper()
	var start, end int
	if _, err := fmt.Sscanf(span, "%d-%d", &start, &end); err != nil || start < 1 || end < start {
		t.Fatalf("invalid source span %q", span)
	}
	return start, end
}

// Offline artifact verification uses the checked-in, SHA-verified vocabulary,
// as the existing tokenizer golden tests do. It does not execute a corpus
// query or open the holdout. Re-capture additionally verifies snippets against
// the clean pinned checkout via TestRecoveryDevCapture.
func TestRecoveryDevArtifactPayloads(t *testing.T) {
	ds, err := LoadDataset("../../../docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/dataset.json")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := evaltokenizer.Load("../tokenizer/testdata/artifact")
	if err != nil {
		t.Fatal(err)
	}
	counter, err := NewPinnedRealPayloadCounter(tok)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"2026-09-06-recovery-dev/bundles-before.json",
		"2026-09-06-recovery-dev/bundles-after.json",
		"2026-09-06-architecture-dev/bundles-after.json",
		"2026-09-06-bundle-selection-dev/bundles-inherited.json",
		"2026-09-06-bundle-selection-dev/bundles-dedup.json",
		"2026-09-06-bundle-selection-dev/bundles-value.json",
		"2026-09-06-bundle-selection-dev/bundles-selected-only.json",
		"2026-09-06-bundle-selection-dev/bundles-compact.json",
		"2026-09-06-bundle-selection-dev/bundles-before-short-query-guard.json",
		"2026-09-06-bundle-selection-dev/bundles-after.json",
		"2026-09-06-qwen-dev/bundles-potion.json",
		"2026-09-06-qwen-dev/bundles-qwen-cpu.json",
		"2026-09-06-candidate-admission-dev/bundles-admission-only.json",
		"2026-09-06-candidate-admission-dev/bundles-after.json",
		"2026-09-07-answer-recovery-dev/bundles-after.json",
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile("../../../docs/eval/retrieval/runs/" + name)
			if err != nil {
				t.Fatal(err)
			}
			var report struct {
				DatasetSHA string                  `json:"dataset_sha256"`
				Queries    int                     `json:"queries"`
				Identical  int                     `json:"identical_payloads"`
				Runs       [][]recoveryObservation `json:"independent_builds"`
				Inputs     [][]struct {
					NodeID   string `json:"node_id"`
					TextHash string `json:"text_hash"`
				} `json:"input_documents"`
			}
			if err := json.Unmarshal(raw, &report); err != nil {
				t.Fatal(err)
			}
			if report.DatasetSHA != ds.SHA256 || report.Queries != len(ds.Dataset.Queries) || len(report.Runs) != 2 {
				t.Fatal("wrong development population or build count")
			}
			if strings.HasPrefix(name, "2026-09-06-qwen-dev/") {
				if len(report.Inputs) != 2 || len(report.Inputs[0]) != 768 || !reflect.DeepEqual(report.Inputs[0], report.Inputs[1]) {
					t.Fatal("model trial has incomplete or different independent input manifests")
				}
				seen := map[string]bool{}
				for _, input := range report.Inputs[0] {
					if input.NodeID == "" || input.TextHash == "" || seen[input.NodeID] {
						t.Fatal("model trial input manifest has empty or duplicate identities")
					}
					seen[input.NodeID] = true
				}
			}
			for _, run := range report.Runs {
				if len(run) != report.Queries {
					t.Fatal("incomplete capture population")
				}
				for i, obs := range run {
					q := ds.Dataset.Queries[i]
					if q.Split != SplitDev || obs.QueryID != q.ID || obs.Capture.QueryID != q.ID {
						t.Fatal("wrong or non-development query")
					}
					if _, err := ValidateCandidateBundleBytes(q.ID, obs.Capture.Payload.Bytes); err != nil {
						t.Fatal(err)
					}
					want, err := preserveCandidatePayload(q.ID, obs.Capture.Payload.Bytes, counter)
					if err != nil || !reflect.DeepEqual(want, obs.Capture.Payload) {
						t.Fatalf("%s payload count/digest drift: %v", q.ID, err)
					}
					var env candidateResponseEnvelope
					if err := json.Unmarshal(want.Bytes, &env); err != nil {
						t.Fatal(err)
					}
					var bundle contract.Result
					if err := json.Unmarshal([]byte(env.Result.Content[0].Text), &bundle); err != nil {
						t.Fatal(err)
					}
					grade3, cited, overlap, contained, tokens := 0, 0, 0, 0, 0
					for _, ev := range bundle.Evidence {
						tokens += len(strings.Fields(ev.Snippet))
					}
					for _, j := range q.Judgements {
						if j.Grade != GradeMax {
							continue
						}
						grade3++
						citation := false
						covered := map[int]bool{}
						for _, ev := range bundle.Evidence {
							citation = citation || SpanMatches(ev.Path, ev.Line, j)
							if ev.Snippet == "" {
								continue
							}
							start, end := recoverySpan(t, ev.Span)
							if len(strings.Split(ev.Snippet, "\n")) != end-start+1 {
								t.Fatal("snippet line count differs from citation")
							}
							for line := start; line <= end; line++ {
								if SpanMatches(ev.Path, line, j) {
									covered[line] = true
								}
							}
						}
						if citation {
							cited++
						}
						if len(covered) > 0 {
							overlap++
						}
						if len(covered) == j.EndLine-j.StartLine+1 {
							contained++
						}
					}
					if grade3 != obs.Grade3Spans || cited != obs.CitedSpans || overlap != obs.SnippetOverlapSpans || contained != obs.SnippetContainedSpans || tokens != obs.SnippetTokens || tokens > SavingsCandidateBudget {
						t.Fatalf("%s source-retention metrics drifted", q.ID)
					}
				}
			}
			identical := 0
			for i, a := range report.Runs[0] {
				if reflect.DeepEqual(a.Capture.Payload, report.Runs[1][i].Capture.Payload) {
					identical++
				}
			}
			if identical != report.Identical || name != "2026-09-06-recovery-dev/bundles-before.json" && identical != report.Queries {
				t.Fatal("payload reproducibility drifted")
			}
		})
	}
}
