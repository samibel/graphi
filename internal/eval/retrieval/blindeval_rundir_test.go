package retrieval

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckPromptBindingRequiresFollowupReadInTwoSlicePrompt(t *testing.T) {
	dir := t.TempDir()
	queryID := "q-1"
	queryText := "Where is the answer?"
	first := promptTestPayload(1, PayloadOperationTaskContext, []byte("first response\n"))
	second := promptTestPayload(2, PayloadOperationFollowupRead, []byte("second response\n"))
	bundle := CapturedCandidateBundle{QueryID: queryID, Payload: first, FollowupRead: &second}
	if err := WriteBlindEvalJSON(filepath.Join(dir, BlindEvalBundlesDir, BundleFileName(queryID)), bundle); err != nil {
		t.Fatal(err)
	}
	transcript, err := BuildRaterTranscriptPrompt(queryID, queryText, bundle)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := EvaluationArtifacts{PreRegistration: PreRegistration{Queries: []PreRegisteredQuery{{
		QueryID:         queryID,
		QueryTextSHA256: SHA256Hex([]byte(queryText)),
		PromptSHA256:    transcript.SHA256,
		BundleSHA256:    first.SHA256,
	}}}}
	promptDir := filepath.Join(dir, BlindEvalPromptsDir)
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oneSlice, err := BuildRaterPrompt(queryID, queryText, first)
	if err != nil {
		t.Fatal(err)
	}
	promptPath := filepath.Join(promptDir, PromptFileName(queryID))
	if err := os.WriteFile(promptPath, oneSlice.Bytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckPromptBinding(dir, artifacts, map[string]string{queryID: queryText}); err == nil {
		t.Fatal("one-slice prompt was accepted for a two-slice bundle")
	}

	if err := os.WriteFile(promptPath, transcript.Bytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckPromptBinding(dir, artifacts, map[string]string{queryID: queryText}); err != nil {
		t.Fatalf("rebuilt two-slice prompt was refused: %v", err)
	}
}
