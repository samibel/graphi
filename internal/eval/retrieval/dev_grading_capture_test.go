package retrieval

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/resolve"
	staticembed "github.com/samibel/graphi/engine/embed/static"
	"github.com/samibel/graphi/engine/query"
	engineretrieval "github.com/samibel/graphi/engine/retrieval"
)

// TestDevGradingCapture is a development instrument, env-gated: it captures
// the production two-slice transcript for every question of a DEVELOPMENT
// split exactly as the sealed holdout capture does (same capture code, same
// follow-up read, same rater prompt builder) and writes bundles/, prompts/
// and questions.json into GRAPHI_DEV_GRADING_OUT, so the holdout's rater and
// grader pipeline can be run over a split whose answer key the author may
// read. It refuses any non-development query. It measures nothing itself.
func TestDevGradingCapture(t *testing.T) {
	datasetPath := os.Getenv("GRAPHI_DEV_GRADING_DATASET")
	root := os.Getenv("GRAPHI_PRODUCT_COMPACT_DEV_COBRA")
	out := os.Getenv("GRAPHI_DEV_GRADING_OUT")
	if datasetPath == "" || root == "" || out == "" {
		t.Skip("set GRAPHI_DEV_GRADING_DATASET, GRAPHI_PRODUCT_COMPACT_DEV_COBRA and GRAPHI_DEV_GRADING_OUT")
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
			t.Fatalf("refusing non-development query %s", q.ID)
		}
	}
	head, err := CheckoutHEAD(context.Background(), root)
	if err != nil || !strings.EqualFold(head, loaded.Dataset.RepoSHA) {
		t.Fatalf("checkout is at %q, dataset cites %q: %v", head, loaded.Dataset.RepoSHA, err)
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
	for _, dir := range []string{"bundles", "prompts"} {
		if err := os.MkdirAll(filepath.Join(out, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	type question struct {
		ID        string      `json:"id"`
		Stratum   string      `json:"stratum"`
		Text      string      `json:"text"`
		FamilyID  string      `json:"family_id"`
		Judgement []Judgement `json:"judgements"`
	}
	var questions []question
	followups := 0
	for _, q := range loaded.Dataset.Queries {
		captured, err := captureOneCandidateBundle(context.Background(), CandidateCaptureOptions{RepoRoot: root, RealCounter: counter}, q, qs, idx, engine)
		if err != nil {
			t.Fatal(err)
		}
		captured.QueryID = q.ID
		followup, err := CaptureFollowupRead(repository, q.ID, captured.Payload, counter)
		if err != nil {
			t.Fatal(err)
		}
		captured.FollowupRead = followup
		if followup != nil {
			followups++
		}
		if err := ValidateCapturedTranscript(repository, q.ID, captured, counter, QrelBlindSmokeContractVersion2); err != nil {
			t.Fatal(err)
		}
		raw, err := json.MarshalIndent(captured, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(out, "bundles", BundleFileName(q.ID)), append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		prompt, err := BuildRaterTranscriptPrompt(q.ID, q.Text, captured)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(out, "prompts", PromptFileName(q.ID)), prompt.Bytes, 0o644); err != nil {
			t.Fatal(err)
		}
		questions = append(questions, question{ID: q.ID, Stratum: q.Stratum, Text: q.Text, FamilyID: q.FamilyID, Judgement: q.Judgements})
	}
	raw, err := json.MarshalIndent(questions, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "questions.json"), append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("dev grading capture: dataset=%s sha=%s queries=%d followup_reads=%d out=%s", loaded.Dataset.ID, loaded.SHA256[:12], len(questions), followups, out)
}
