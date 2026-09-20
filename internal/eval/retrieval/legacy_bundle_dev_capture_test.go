package retrieval

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/taskctx"
	enginecontext "github.com/samibel/graphi/engine/context"
	_ "github.com/samibel/graphi/engine/embed/ollama" // opt-in selector; registration performs no I/O
	staticembed "github.com/samibel/graphi/engine/embed/static"
	"github.com/samibel/graphi/engine/query"
	engineretrieval "github.com/samibel/graphi/engine/retrieval"
)

// TestLegacyBundleDevCapture writes the pre-compact task_context/2 bundle of
// every query in a development dataset, so the compact projector can be
// iterated in tenths of a second without rebuilding the index. The file it
// writes is a development iteration input in the shape
// TestProductCompactTaskContextDev and TestOneSpanCompactDev read; it is not
// evidence, carries no candidate binding, and must not be cited by a run.
// It refuses any query outside the development split.
func TestLegacyBundleDevCapture(t *testing.T) {
	out := os.Getenv("GRAPHI_LEGACY_CAPTURE_OUT")
	root := os.Getenv("GRAPHI_PRODUCT_COMPACT_DEV_COBRA")
	datasetPath := os.Getenv("GRAPHI_LEGACY_CAPTURE_DATASET")
	if out == "" || root == "" {
		t.Skip("set GRAPHI_LEGACY_CAPTURE_OUT and GRAPHI_PRODUCT_COMPACT_DEV_COBRA (and optionally GRAPHI_LEGACY_CAPTURE_DATASET)")
	}
	module := taskContextModuleRoot(t)
	if datasetPath == "" {
		datasetPath = "docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/dataset.json"
	}
	if !filepath.IsAbs(datasetPath) {
		datasetPath = filepath.Join(module, datasetPath)
	}
	ds, err := LoadDataset(datasetPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range ds.Dataset.Queries {
		if q.Split != SplitDev {
			t.Fatalf("refusing non-development query %s", q.ID)
		}
	}
	selector := os.Getenv("GRAPHI_RECOVERY_EMBEDDER")
	if selector == "" {
		selector = staticembed.PinnedSelector
	}
	counter := loadHermeticRealPayloadCounterForTest(t)
	idx, err := buildTaskContextIndex(t.Context(), root, t.TempDir(), selector, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.store.Close()
	deps := resolve.Deps{Query: query.New(idx.store), Search: idx.search}
	realEngine := engineretrieval.New(deps, idx.search, idx.store)
	reader := enginecontext.NewRootedReader(root)
	type row struct {
		QueryID string                  `json:"query_id"`
		Capture CapturedCandidateBundle `json:"capture"`
	}
	type content struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	var rows []row
	for _, q := range ds.Dataset.Queries {
		adapter := NewTaskContextRetriever(realEngine)
		queryDeps := deps
		queryDeps.Retrieval = adapter
		bundle, err := taskctx.AssembleV2(t.Context(), taskctx.Params{Task: q.Text, TokenBudget: TaskContextTokenBudget, Deps: queryDeps, Reader: reader})
		if err != nil {
			t.Fatal(err)
		}
		inner, err := contract.Serialize(bundle)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := json.Marshal(struct {
			JSONRPC string `json:"jsonrpc"`
			ID      int    `json:"id"`
			Result  struct {
				Content []content `json:"content"`
				IsError bool      `json:"isError"`
			} `json:"result"`
		}{JSONRPC: "2.0", ID: 1, Result: struct {
			Content []content `json:"content"`
			IsError bool      `json:"isError"`
		}{Content: []content{{Type: "text", Text: string(inner)}}}})
		if err != nil {
			t.Fatal(err)
		}
		wire = append(wire, '\n')
		payload, err := preserveCandidatePayload(q.ID, wire, counter)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row{QueryID: q.ID, Capture: CapturedCandidateBundle{QueryID: q.ID, Payload: payload}})
	}
	report := struct {
		DatasetSHA string   `json:"dataset_sha256"`
		Note       string   `json:"note"`
		Identical  int      `json:"identical_payloads"`
		Runs       [2][]row `json:"independent_builds"`
	}{ds.SHA256, "development iteration input: pre-compact bundles from one index build, not a candidate-bound capture and not evidence", len(rows), [2][]row{rows, rows}}
	raw, err := json.MarshalIndent(report, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("legacy bundle capture: %d rows -> %s", len(rows), out)
}
