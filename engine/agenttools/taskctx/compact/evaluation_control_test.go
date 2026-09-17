package compact

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/shape"
)

func TestEvaluationLexicalControlUsesCompact17WithoutRelabelingState(t *testing.T) {
	tokenizerDir, err := filepath.Abs("../../../../core/tokenizer/testdata/artifact")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GRAPHI_EVAL_TOKENIZER_DIR", tokenizerDir)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "answer.go"), []byte("package answer\n\nfunc ExactAnswer() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ready := compactTestLegacyBundle(t, "ready")
	lexical := compactTestLegacyBundle(t, "lexical_only")
	readyResult, err := Build(context.Background(), "ExactAnswer", ready, os.DirFS(root), 20)
	if err != nil {
		t.Fatalf("ready build: %v", err)
	}
	if _, err := Build(context.Background(), "ExactAnswer", lexical, os.DirFS(root), 20); err != ErrRetrievalNotReady {
		t.Fatalf("product build error = %v, want ErrRetrievalNotReady", err)
	}
	control, err := BuildEvaluationControl(context.Background(), "ExactAnswer", lexical, os.DirFS(root), 20)
	if err != nil {
		t.Fatalf("lexical eval control: %v", err)
	}
	if control.Structured.Version != Version || control.Structured.Provenance.RetrievalState != "lexical_only" {
		t.Fatalf("control identity/state = %q/%q", control.Structured.Version, control.Structured.Provenance.RetrievalState)
	}
	if readyResult.Summary != control.Summary || !equalCompactSources(readyResult.Structured.Sources, control.Structured.Sources) || readyResult.Structured.Truncated != control.Structured.Truncated {
		t.Fatalf("selection/serialization algorithm diverged:\nready=%+v\ncontrol=%+v", readyResult, control)
	}
}

func compactTestLegacyBundle(t *testing.T, state string) []byte {
	t.Helper()
	weights, model, strategy := "", "", "lexical_only"
	if state == "ready" {
		weights, model, strategy = "weights-sha256:test", "fixture-model", "semantic_first"
	}
	result := contract.Result{
		Outcome: contract.OutcomeFound,
		Summary: `task_context/2: 1 seed(s) for "ExactAnswer" — 0 related, 0 callers, 0 callees, 0 tests, 0 configs, 1 files, risk low (task_context/2; retrieval/4; weights ` + weights + `; model ` + model + `; 3/1200 snippet tokens; context-definitions/3; strategy ` + strategy + `; degradation: ` + state + `)`,
		Items:   []contract.Item{{RefID: "item-1", Rank: 1, Reason: "primary", EvidenceRefIDs: []string{"source-1", "snippet-1"}}},
		Evidence: []contract.Evidence{
			{RefID: "source-1", Path: "answer.go", Line: 3, Span: "3-3", Role: "primary", ClaimType: "source_match"},
			{RefID: "snippet-1", Path: "answer.go", Line: 3, Span: "3-3", Role: "snippet", Snippet: "func ExactAnswer() {}", TextHash: shape.TextHash("func ExactAnswer() {}")},
		},
		Confidence: contract.Confidence{Distribution: map[string]float64{"heuristic": 1}, Top: "heuristic", Method: "fixture"},
		Limits:     contract.Limits{CapApplied: 40, TotalAvailable: 1},
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func equalCompactSources(a, b []Source) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
