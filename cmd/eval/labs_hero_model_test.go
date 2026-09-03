//go:build labs_hero

package main

// The production-model Labs hero is opt-in at the Go package boundary so
// ordinary `go test ./...` jobs do not fail merely because a 32 MiB model was
// not installed on that runner. The static-embedder-cross-arch workflow owns
// the authoritative invocation: it downloads the pinned production artifact
// once, verifies all four hashes, and runs this test with -tags labs_hero.

import (
	"context"
	"os"
	"testing"

	"github.com/samibel/graphi/engine/scenario"
)

// TestLabsHeroSuite_TaskContextV2 is the one-scenario, production-model Labs
// gate. An absent artifact remains a failure. Set
// GRAPHI_LABS_HERO_DISABLE_EMBEDDER=1 with an installed artifact to exercise
// the separate AC-6 negative control: lexical fallback also fails, never skips.
func TestLabsHeroSuite_TaskContextV2(t *testing.T) {
	root := repoRoot(t)
	s := loadSingleLabsHeroScenario(t, root)
	p := loadLabsHeroProvenance(t, root)
	q, answerText := verifyLabsHeroProvenance(t, root, s, p)

	disabled := os.Getenv(labsHeroDisableEmbedderEnv) == "1"
	eng, cleanup, err := buildLabsHeroEngine(context.Background(), root, p, disabled)
	if err != nil {
		t.Fatalf("labs hero semantic setup: %v", err)
	}
	defer cleanup()

	run := (&scenario.Runner{Engine: eng}).Run(s)
	if err := assertLabsHeroRun(s, p, q, answerText, run, eng); err != nil {
		t.Fatal(err)
	}
	used, budget, _ := labsHeroTokenCounts(eng.bundle.Summary)
	t.Logf("labs hero PASS: task_context/2 calls=%d retrieval calls=%d semantic_state=%s bundle_tokens=%d/%d answer=%s:%d-%d",
		eng.taskContextV2Calls, eng.retrieval.Called(), eng.retrieval.LastResult().Degradation,
		used, budget, p.AnswerSpan.Path, p.AnswerSpan.StartLine, p.AnswerSpan.EndLine)
}
