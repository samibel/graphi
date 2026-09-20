package retrieval

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestBuildRaterTranscriptPromptCarriesBothSlicesByteIdentically(t *testing.T) {
	first := promptTestPayload(1, PayloadOperationTaskContext, []byte("first response\nwith bytes\n"))
	second := promptTestPayload(2, PayloadOperationFollowupRead, []byte("{\"path\":\"answer.go\",\"start_line\":1,\"end_line\":2,\"text\":\"answer\\nbytes\"}\n"))
	bundle := CapturedCandidateBundle{Payload: first, FollowupRead: &second}

	prompt, err := BuildRaterTranscriptPrompt("q-1", "Where is the answer?", bundle)
	if err != nil {
		t.Fatal(err)
	}
	embeddedFirst, err := prompt.EmbeddedBundleBytes()
	if err != nil {
		t.Fatal(err)
	}
	embeddedSecond, err := prompt.EmbeddedFollowupBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(embeddedFirst, first.Bytes) || SHA256Hex(embeddedFirst) != first.SHA256 {
		t.Fatalf("embedded first slice = %q, sha256 %s", embeddedFirst, SHA256Hex(embeddedFirst))
	}
	if !bytes.Equal(embeddedSecond, second.Bytes) || SHA256Hex(embeddedSecond) != second.SHA256 {
		t.Fatalf("embedded second slice = %q, sha256 %s", embeddedSecond, SHA256Hex(embeddedSecond))
	}
	if !bytes.Contains(prompt.Bytes, []byte("----- BEGIN task_context/2 RESPONSE BYTES -----\n")) ||
		!bytes.Contains(prompt.Bytes, []byte("----- BEGIN task_context/2 FOLLOW-UP READ BYTES -----\n")) {
		t.Fatalf("two-slice prompt lacks its response markers:\n%s", prompt.Bytes)
	}
}

func TestBuildRaterTranscriptPromptOneSliceIsByteIdenticalToBuildRaterPrompt(t *testing.T) {
	payload := promptTestPayload(1, PayloadOperationTaskContext, []byte("one response\n"))
	want, err := BuildRaterPrompt("q-1", "Where is the answer?", payload)
	if err != nil {
		t.Fatal(err)
	}
	got, err := BuildRaterTranscriptPrompt("q-1", "Where is the answer?", CapturedCandidateBundle{Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("one-slice transcript prompt changed\n got: %+v\nwant: %+v", got, want)
	}
}

func TestBuildRaterTranscriptPromptRefusesInvalidFollowupIdentity(t *testing.T) {
	first := promptTestPayload(1, PayloadOperationTaskContext, []byte("first\n"))
	valid := promptTestPayload(2, PayloadOperationFollowupRead, []byte("second\n"))
	for _, tc := range []struct {
		name   string
		mutate func(*PreservedPayload)
	}{
		{"sequence", func(p *PreservedPayload) { p.Sequence = 3 }},
		{"digest", func(p *PreservedPayload) { p.SHA256 = SHA256Hex([]byte("different")) }},
		{"byte count", func(p *PreservedPayload) { p.ByteCount++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			followup := valid
			tc.mutate(&followup)
			if _, err := BuildRaterTranscriptPrompt("q-1", "Where?", CapturedCandidateBundle{Payload: first, FollowupRead: &followup}); err == nil {
				t.Fatal("invalid follow-up identity was accepted")
			}
		})
	}
}

func TestRaterInstructionConstantsCarryNoForbiddenContext(t *testing.T) {
	for name, instructions := range map[string]string{
		"one response":  RaterInstructions,
		"two responses": RaterInstructionsTwoResponses,
	} {
		lower := strings.ToLower(instructions)
		for _, forbidden := range []string{"docs/", "internal/", "grade", "rubric", "expected"} {
			if strings.Contains(lower, forbidden) {
				t.Errorf("%s instructions contain %q", name, forbidden)
			}
		}
	}
}

func promptTestPayload(sequence int, operation string, raw []byte) PreservedPayload {
	return PreservedPayload{
		Sequence:  sequence,
		Boundary:  PayloadBoundaryCandidate,
		Operation: operation,
		Bytes:     raw,
		SHA256:    SHA256Hex(raw),
		ByteCount: len(raw),
	}
}
