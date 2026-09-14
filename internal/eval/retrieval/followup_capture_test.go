package retrieval

import (
	"strings"
	"testing"

	taskcompact "github.com/samibel/graphi/engine/agenttools/taskctx/compact"
)

func TestValidateCapturedTranscript_AcceptsValidTwoSliceBundle(t *testing.T) {
	counter := equalRecallFixtureCounter()
	repository := followupFixtureRepository()
	first := followupFixtureCutLead(t, counter)
	second, err := CaptureFollowupRead(repository, "dev-followup", first, counter)
	if err != nil || second == nil {
		t.Fatal(err)
	}
	bundle := CapturedCandidateBundle{QueryID: "dev-followup", Payload: first, FollowupRead: second}
	if err := ValidateCapturedTranscript(repository, bundle.QueryID, bundle, counter); err != nil {
		t.Fatalf("valid two-slice transcript: %v", err)
	}
}

func TestValidateCapturedTranscript_AcceptsContractOneBundle(t *testing.T) {
	bundle := CapturedCandidateBundle{QueryID: "dev-followup"}
	if err := ValidateCapturedTranscript(nil, bundle.QueryID, bundle, PayloadCounter{}); err != nil {
		t.Fatalf("one-slice contract-1 bundle: %v", err)
	}
}

func TestValidateCapturedTranscript_RejectsInvalidFollowupRead(t *testing.T) {
	counter := equalRecallFixtureCounter()
	repository := followupFixtureRepository()
	first := followupFixtureCutLead(t, counter)
	valid, err := CaptureFollowupRead(repository, "dev-followup", first, counter)
	if err != nil || valid == nil {
		t.Fatal(err)
	}
	reslice := func(raw string) *PreservedPayload {
		mutated := *valid
		mutated.Bytes = []byte(raw)
		mutated.SHA256 = SHA256Hex(mutated.Bytes)
		mutated.ByteCount = len(mutated.Bytes)
		n, err := counter.Count(mutated.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		mutated.TokenCounts = []PayloadTokenCount{
			{TokenizerID: TokenizerID, Tokens: len(strings.Fields(raw))},
			{TokenizerID: counter.TokenizerID, VocabularySHA256: counter.VocabularySHA256, Tokens: n},
		}
		return &mutated
	}

	wrongOperation := *valid
	wrongOperation.Operation = PayloadOperationRead
	undesignated := followupFixtureFirstSlice(t, counter, []taskcompact.Source{{Path: "answer.go", StartLine: 1, EndLine: 1, Text: "package answer"}}, "")
	cases := map[string]struct {
		first  PreservedPayload
		second *PreservedPayload
		want   string
	}{
		"mismatching span": {
			first:  first,
			second: reslice(`{"path":"answer.go","start_line":4,"end_line":5,"text":"// the answer\n// continues"}` + "\n"),
			want:   "not the designated span",
		},
		"forged text": {
			first:  first,
			second: reslice(`{"path":"answer.go","start_line":1,"end_line":5,"text":"package answer\n// one\n// two\n// forged\n// continues"}` + "\n"),
			want:   "bytes differ",
		},
		"wrong operation": {
			first:  first,
			second: &wrongOperation,
			want:   "operation",
		},
		"undesignated slice 2": {
			first:  undesignated,
			second: valid,
			want:   "designated no follow-up",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			bundle := CapturedCandidateBundle{QueryID: "dev-followup", Payload: tc.first, FollowupRead: tc.second}
			err := ValidateCapturedTranscript(repository, bundle.QueryID, bundle, counter)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}
