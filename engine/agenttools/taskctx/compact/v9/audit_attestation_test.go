package v9

import (
	"errors"
	"strings"
	"testing"
)

// attestedSummary is a task_context/2 summary whose audit block names the
// given retrieval state, in the exact field order the assembler emits.
func attestedSummary(state string) string {
	return `task_context/2: 1 seed(s) for "ExactAnswer" — 0 related, 0 callers, 0 callees, 0 tests, 0 configs, 1 files, risk low ` +
		`(task_context/2; retrieval/4; weights weights-sha256:test; model fixture-model; 3/1200 snippet tokens; context-definitions/3; strategy semantic_first; degradation: ` + state + `)`
}

// The two ways an input can fail the state check are different diagnoses and
// must not share one error. "not ready" points at retrieval; "not attested"
// points at whoever produced the summary. Reporting the first for the second
// sends a reader hunting a healthy embedder — which is exactly what happened
// when the empty task_context/2 envelope carried no audit block at all.
func TestAuditAttestationSeparatesNotReadyFromNotAttested(t *testing.T) {
	const digest = "0000000000000000000000000000000000000000000000000000000000000000"

	if _, err := compactTaskContextProvenance(attestedSummary("lexical_only"), digest, 20, "ready"); !errors.Is(err, ErrRetrievalNotReady) {
		t.Fatalf("a summary attesting another state: err = %v, want ErrRetrievalNotReady", err)
	}
	for _, tc := range []struct {
		name    string
		summary string
	}{
		// The shape the empty envelope used to have: prose, no block.
		{name: "no audit block", summary: `task_context: no symbol or file matched "directive"; try ` + "`search`" + ` for discovery`},
		// A block that is there but names no retrieval state says nothing
		// about retrieval either.
		{name: "block without degradation", summary: strings.Replace(attestedSummary("ready"), "; degradation: ready", "", 1)},
		// A trailing group that is not this method's audit block.
		{name: "foreign block", summary: `task_context/1: 1 seed(s) for "ExactAnswer" (task_context; retrieval/4; weights w; model m; 3/1200 snippet tokens; context-definitions/3)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compactTaskContextProvenance(tc.summary, digest, 20, "ready")
			if !errors.Is(err, ErrSummaryNotAttested) {
				t.Fatalf("err = %v, want ErrSummaryNotAttested", err)
			}
			if errors.Is(err, ErrRetrievalNotReady) {
				t.Fatal("a missing attestation must not be reported as a retrieval state")
			}
		})
	}
}

// The projected provenance must repeat what the input said, not what the
// caller asked for: a check against a field the caller itself filled in
// checks nothing.
func TestAuditAttestationProvenanceStateComesFromTheInput(t *testing.T) {
	const digest = "0000000000000000000000000000000000000000000000000000000000000000"
	p, err := compactTaskContextProvenance(attestedSummary("ready"), digest, 20, "ready")
	if err != nil {
		t.Fatalf("provenance: %v", err)
	}
	if p.RetrievalState != "ready" || p.Retrieval != "retrieval/4" || p.SourceSelection != "context-definitions/3" {
		t.Fatalf("provenance did not come from the input block: %+v", p)
	}
	// The state is read out of the block, so a block naming a state the
	// caller did not ask for is rejected rather than relabeled.
	if _, err := compactTaskContextProvenance(attestedSummary("generation_missing"), digest, 20, "lexical_only"); !errors.Is(err, ErrRetrievalNotReady) {
		t.Fatalf("err = %v, want ErrRetrievalNotReady", err)
	}
}

// An audit block must be found as the LAST parenthesised group, so a
// headline may carry prose — the empty answer's next-step hint, or a task
// text with parentheses in it — without hiding the attestation.
func TestAuditAttestationSurvivesProseInTheHeadline(t *testing.T) {
	summary := `task_context/2: 0 seed(s) for "why (really) here" — 0 related, 0 callers, 0 callees, 0 tests, 0 configs, 0 files, risk unknown — no symbol or file matched "why (really) here"; try ` + "`search`" + ` for discovery ` +
		`(task_context/2; retrieval/4; weights weights-sha256:test; model fixture-model; 0/1200 snippet tokens; context-definitions/3; strategy semantic_first; degradation: ready)`
	audit, err := parseTaskContextAudit(summary)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if audit.RetrievalState != "ready" || audit.Method != "task_context/2" {
		t.Fatalf("audit block misread: %+v", audit)
	}
}
