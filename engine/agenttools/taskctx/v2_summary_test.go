package taskctx

import (
	"testing"

	"github.com/samibel/graphi/engine/agenttools/resolve"
)

// Model and method identify the retrieval implementation. The operational
// index freshness nonce stays in diagnostics, outside actor-visible bytes.
func TestBuildV2Summary_AuditFingerprintBytes(t *testing.T) {
	got := buildV2Summary(
		1,
		0,
		"Where is the auth token validated?",
		2, 1, 0, 1, 0, 3,
		"low",
		"",
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
	want := `task_context/2: 1 seed(s) for "Where is the auth token validated?" — 2 related, 1 callers, 0 callees, 1 tests, 0 configs, 3 files, risk low (task_context/2; retrieval/2; weights weights-sha256:abc; model model-fingerprint; 492/1200 snippet tokens; strategy semantic_first; degradation: ready)`
	if got != want {
		t.Fatalf("task_context/2 summary bytes changed:\n got: %q\nwant: %q", got, want)
	}
}

// SW-282: the widened internal candidate pool is honestly counted in the
// summary when non-zero, immediately after the snippet-token count and
// before the strategy/degradation trailer.
func TestBuildV2Summary_CandidatePoolCounted(t *testing.T) {
	got := buildV2Summary(
		5,
		7,
		"how does X work",
		0, 0, 0, 0, 0, 0,
		"low",
		"",
		"snippets disabled",
		"ready",
		resolve.RetrieverSummary{RetrievalVersion: "retrieval/4", Strategy: "semantic_first"},
	)
	want := `task_context/2: 5 seed(s) for "how does X work" — 0 related, 0 callers, 0 callees, 0 tests, 0 configs, 0 files, risk low (task_context/2; retrieval/4; weights ; model ; snippets disabled; 7 candidate(s) considered; strategy semantic_first; degradation: ready)`
	if got != want {
		t.Fatalf("candidate count not honestly stamped:\n got: %q\nwant: %q", got, want)
	}
}
