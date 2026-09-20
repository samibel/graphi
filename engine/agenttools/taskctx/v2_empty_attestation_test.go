// An empty task_context/2 answer must testify like any other /2 answer.
//
// The defect these tests pin: the zero-seed branch used to return the shared
// cross-tool empty envelope, whose summary names neither the method nor the
// retrieval state. A consumer reading such an answer cannot tell "retrieval
// was ready and this repository holds nothing for the query" from "retrieval
// was never ready, so this emptiness says nothing about the repository" —
// two situations that demand opposite follow-ups. Every consumer that decides
// on the strength of the answer needs that distinction, and the compact
// projector (the qualification's own consumer) refuses an answer that cannot
// supply it.
package taskctx_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/taskctx"
	taskcompact "github.com/samibel/graphi/engine/agenttools/taskctx/compact"
)

// auditBlock returns the fields of the trailing parenthesised audit block of
// a /2 summary, split the way every machine reader of these bytes splits it.
// It fails the test when the summary carries no such block — which is the
// regression this file exists to catch.
func auditBlock(t *testing.T, summary string) []string {
	t.Helper()
	open, close := strings.LastIndex(summary, " ("), strings.LastIndex(summary, ")")
	if open < 0 || close <= open+2 {
		t.Fatalf("summary carries no audit block, so it testifies to nothing: %q", summary)
	}
	return strings.Split(summary[open+2:close], "; ")
}

func auditField(fields []string, prefix string) (string, bool) {
	for _, field := range fields {
		if strings.HasPrefix(field, prefix) {
			return strings.TrimPrefix(field, prefix), true
		}
	}
	return "", false
}

// unresolvableRows are retrieval rows whose NodeID hydrates to nothing in the
// fixture graph. resolveSeedsV2 drops such rows (v2.go's hydration loop), so
// this is the shape of a retrieval result that is genuinely ready and still
// leaves the assembler with zero seeds.
func unresolvableRows() []resolve.RetrieverRow {
	return []resolve.RetrieverRow{
		{NodeID: "ffffffffffffffff", DocumentID: "doc-absent", Path: "docs/notes.md", Span: "1-1", Final: 900},
	}
}

// TestTaskContextV2_EmptyAnswerAttestsMethodAndState is the product
// invariant: whatever the retrieval state, an empty /2 answer names itself
// and names that state.
func TestTaskContextV2_EmptyAnswerAttestsMethodAndState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state string
		rows  []resolve.RetrieverRow
	}{
		// Ready retrieval, nothing that hydrates: the emptiness is a real
		// finding about the repository and must be readable as such.
		{name: "ready", state: "ready", rows: unresolvableRows()},
		// A degraded arm falls back to lexical seeding, which finds no
		// symbol for this query. The reader must learn that the answer was
		// produced without the embedder.
		{name: "generation_missing", state: "generation_missing"},
		{name: "lexical_only", state: "lexical_only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps := v2Deps(t, &stubRetriever{state: tc.state, strategy: tc.state, rows: tc.rows})
			// A snippet source is wired exactly as every surface wires one,
			// so the audit block carries the source-selection stamp the
			// compact projector parses. The empty branch reports the same
			// snippet accounting as the found branch; it simply has nothing
			// to spend the budget on.
			res, err := taskctx.AssembleV2(context.Background(), taskctx.Params{
				Task:   "nothing in this graph matches zzz-absent-token",
				Reader: sources(),
				Deps:   deps,
			})
			if err != nil {
				t.Fatalf("AssembleV2: %v", err)
			}
			if res.Outcome != contract.OutcomeEmpty {
				t.Fatalf("Outcome = %s, want empty", res.Outcome)
			}
			if len(res.Items) != 0 || len(res.Evidence) != 0 {
				t.Fatalf("an empty answer must carry no items or evidence, got %d/%d", len(res.Items), len(res.Evidence))
			}
			if !strings.HasPrefix(res.Summary, taskctx.MethodVersionV2+":") {
				t.Fatalf("summary does not name the method that produced it: %q", res.Summary)
			}
			fields := auditBlock(t, res.Summary)
			if fields[0] != taskctx.MethodVersionV2 {
				t.Fatalf("audit block opens with %q, want %q", fields[0], taskctx.MethodVersionV2)
			}
			if !strings.HasPrefix(fields[1], "retrieval/") {
				t.Fatalf("audit block names no retrieval version: %q", res.Summary)
			}
			state, ok := auditField(fields, "degradation: ")
			if !ok {
				t.Fatalf("audit block carries no degradation stamp: %q", res.Summary)
			}
			if state != tc.state {
				t.Fatalf("degradation stamp = %q, want %q", state, tc.state)
			}
			if _, ok := auditField(fields, "context-definitions/"); !ok {
				t.Fatalf("audit block names no source-selection version: %q", res.Summary)
			}
			// The hint is what a person at the terminal actually acts on;
			// gaining the audit block must not cost it.
			if !strings.Contains(res.Summary, "no symbol or file matched") || !strings.Contains(res.Summary, "try `search` for discovery") {
				t.Fatalf("empty answer lost its next-step hint: %q", res.Summary)
			}
			// The hint is prose in the headline, ahead of the block, so the
			// block stays the last parenthesised group.
			if !strings.HasSuffix(res.Summary, ")") {
				t.Fatalf("audit block is not the last group of the summary: %q", res.Summary)
			}
		})
	}
}

// TestTaskContextV2_EmptyReadyAnswerIsProjectable is the consumer end of the
// same invariant, and the one the embedded-model qualification stumbled on: a
// ready-but-empty answer must be accepted by the compact projector instead of
// being mistaken for a non-ready retrieval. Queries that legitimately match
// nothing (a substring of an identifier, a non-Go file the Go indexer holds
// no node for) otherwise take a whole evaluation down with a diagnosis that
// blames the embedder.
func TestTaskContextV2_EmptyReadyAnswerIsProjectable(t *testing.T) {
	tokenizerDir, err := filepath.Abs("../../../core/tokenizer/testdata/artifact")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GRAPHI_EVAL_TOKENIZER_DIR", tokenizerDir)
	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "notes.go"), []byte("package notes\n\n// zzzAbsentToken is only named here.\nfunc zzzAbsentToken() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	deps := v2Deps(t, &stubRetriever{state: "ready", strategy: "semantic_first", model: "fixture-model", rows: unresolvableRows()})
	res, err := taskctx.AssembleV2(context.Background(), taskctx.Params{Task: "zzzAbsentToken", Reader: sources(), Deps: deps})
	if err != nil {
		t.Fatalf("AssembleV2: %v", err)
	}
	if res.Outcome != contract.OutcomeEmpty {
		t.Fatalf("Outcome = %s, want empty", res.Outcome)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := taskcompact.Build(context.Background(), "zzzAbsentToken", raw, os.DirFS(repository), taskcompact.DefaultSourceBudget)
	if err != nil {
		t.Fatalf("compact projection of a ready-but-empty answer: %v", err)
	}
	if projected.Structured.Provenance.RetrievalState != "ready" {
		t.Fatalf("projected retrieval state = %q, want ready", projected.Structured.Provenance.RetrievalState)
	}
	if projected.Structured.Provenance.Method != taskctx.MethodVersionV2 {
		t.Fatalf("projected method = %q, want %q", projected.Structured.Provenance.Method, taskctx.MethodVersionV2)
	}
}
