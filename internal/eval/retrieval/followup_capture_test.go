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

func TestValidateCapturedTranscript_ContractVersionCoupling(t *testing.T) {
	counter := equalRecallFixtureCounter()
	repository := followupFixtureRepository()
	first := followupFixtureCutLead(t, counter)
	second, err := CaptureFollowupRead(repository, "dev-followup", first, counter)
	if err != nil || second == nil {
		t.Fatal(err)
	}
	bundle := CapturedCandidateBundle{QueryID: "dev-followup", Payload: first, FollowupRead: second}

	err = ValidateCapturedTranscript(repository, bundle.QueryID, bundle, counter, QrelBlindSmokeContractVersion)
	if err == nil || !strings.Contains(err.Error(), QrelBlindSmokeContractVersion) {
		t.Fatalf("error = %v, want followup_read refusal naming contract version 1", err)
	}
	if err := ValidateCapturedTranscript(repository, bundle.QueryID, bundle, counter, QrelBlindSmokeContractVersion2); err != nil {
		t.Fatalf("contract-2 followup_read: %v", err)
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

func TestCheckPreRegisteredBundleBinding_EnforcesFollowupPresenceAndIdentity(t *testing.T) {
	counter := equalRecallFixtureCounter()
	repository := followupFixtureRepository()
	first := followupFixtureCutLead(t, counter)
	second, err := CaptureFollowupRead(repository, "dev-followup", first, counter)
	if err != nil || second == nil {
		t.Fatal(err)
	}
	query := PreRegisteredQuery{
		QueryID:             "dev-followup",
		BundleSHA256:        first.SHA256,
		BundleByteCount:     first.ByteCount,
		BundleBoundary:      first.Boundary,
		BundleTokenCounts:   append([]PayloadTokenCount(nil), first.TokenCounts...),
		FollowupSHA256:      second.SHA256,
		FollowupByteCount:   second.ByteCount,
		FollowupTokenCounts: append([]PayloadTokenCount(nil), second.TokenCounts...),
	}
	bundle := CapturedCandidateBundle{QueryID: query.QueryID, Payload: first, FollowupRead: second}

	if err := CheckPreRegisteredBundleBinding(QrelBlindSmokeContractVersion2, query, bundle); err != nil {
		t.Fatalf("valid contract-2 bundle binding: %v", err)
	}

	t.Run("contract one refuses a follow-up read", func(t *testing.T) {
		err := CheckPreRegisteredBundleBinding(QrelBlindSmokeContractVersion, query, bundle)
		if err == nil || !strings.Contains(err.Error(), QrelBlindSmokeContractVersion) {
			t.Fatalf("error = %v, want v1 refusal naming its version", err)
		}
	})

	t.Run("designation requires the captured read", func(t *testing.T) {
		withoutRead := bundle
		withoutRead.FollowupRead = nil
		err := CheckPreRegisteredBundleBinding(QrelBlindSmokeContractVersion2, query, withoutRead)
		if err == nil || !strings.Contains(err.Error(), "designates") {
			t.Fatalf("error = %v, want missing designated read refusal", err)
		}
	})

	t.Run("undesignated bundle forbids follow-up metadata", func(t *testing.T) {
		undesignated := bundle
		undesignated.Payload = followupFixtureFirstSlice(t, counter, []taskcompact.Source{{Path: "answer.go", StartLine: 1, EndLine: 1, Text: "package answer"}}, "")
		queryWithoutFirst := query
		queryWithoutFirst.BundleSHA256 = undesignated.Payload.SHA256
		queryWithoutFirst.BundleByteCount = undesignated.Payload.ByteCount
		queryWithoutFirst.BundleBoundary = undesignated.Payload.Boundary
		queryWithoutFirst.BundleTokenCounts = append([]PayloadTokenCount(nil), undesignated.Payload.TokenCounts...)
		err := CheckPreRegisteredBundleBinding(QrelBlindSmokeContractVersion2, queryWithoutFirst, undesignated)
		if err == nil || !strings.Contains(err.Error(), "designated no follow-up") {
			t.Fatalf("error = %v, want undesignated read refusal", err)
		}
	})

	for _, tc := range []struct {
		name string
		edit func(*PreRegisteredQuery)
		want string
	}{
		{"follow-up sha", func(q *PreRegisteredQuery) { q.FollowupSHA256 = strings.Repeat("f", 64) }, "followup_sha256"},
		{"follow-up byte count", func(q *PreRegisteredQuery) { q.FollowupByteCount++ }, "followup_byte_count"},
		{"follow-up token counts", func(q *PreRegisteredQuery) { q.FollowupTokenCounts[0].Tokens++ }, "followup_token_counts"},
		{"first-slice sha", func(q *PreRegisteredQuery) { q.BundleSHA256 = strings.Repeat("f", 64) }, "bundle_sha256"},
		{"first-slice byte count", func(q *PreRegisteredQuery) { q.BundleByteCount++ }, "bundle_byte_count"},
		{"first-slice token counts", func(q *PreRegisteredQuery) { q.BundleTokenCounts[0].Tokens++ }, "bundle_token_counts"},
	} {
		t.Run("refuses mismatching "+tc.name, func(t *testing.T) {
			broken := query
			broken.BundleTokenCounts = append([]PayloadTokenCount(nil), query.BundleTokenCounts...)
			broken.FollowupTokenCounts = append([]PayloadTokenCount(nil), query.FollowupTokenCounts...)
			tc.edit(&broken)
			err := CheckPreRegisteredBundleBinding(QrelBlindSmokeContractVersion2, broken, bundle)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q mismatch", err, tc.want)
			}
		})
	}
}
