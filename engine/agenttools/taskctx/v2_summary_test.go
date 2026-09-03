package taskctx

import (
	"testing"

	"github.com/samibel/graphi/engine/agenttools/resolve"
)

// TestBuildV2Summary_AuditFingerprintBytes pins SW-278's intentional output
// change. Model and index identity now sit beside the existing weights stamp,
// so a consumer can audit the exact semantic inputs from the returned bytes.
func TestBuildV2Summary_AuditFingerprintBytes(t *testing.T) {
	got := buildV2Summary(
		1,
		"Where is the auth token validated?",
		2, 1, 0, 1, 0, 3,
		"low",
		"492/1200 snippet tokens",
		"ready",
		resolve.RetrieverSummary{
			RetrievalVersion: "retrieval/2",
			Strategy:         "semantic_first",
			WeightsHash:      "weights-sha256:abc",
			ModelFingerprint: "model-fingerprint",
			IndexFingerprint: "index-fingerprint",
		},
	)
	want := `task_context/2: 1 seed(s) for "Where is the auth token validated?" — 2 related, 1 callers, 0 callees, 1 tests, 0 configs, 3 files, risk low (task_context/2; retrieval/2; weights weights-sha256:abc; model model-fingerprint; index index-fingerprint; 492/1200 snippet tokens; strategy semantic_first; degradation: ready)`
	if got != want {
		t.Fatalf("task_context/2 summary bytes changed:\n got: %q\nwant: %q", got, want)
	}
}
