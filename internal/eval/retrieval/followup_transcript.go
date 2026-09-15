package retrieval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"

	taskcompact "github.com/samibel/graphi/engine/agenttools/taskctx/compact"
	compactv9 "github.com/samibel/graphi/engine/agenttools/taskctx/compact/v9"
)

// Second-response transcript (alternative contract version 2,
// `contract-v2-second-response.md`).
//
// A candidate transcript is one task_context/2 response and, when that
// response designates a follow-up, at most one exact read of exactly that
// span. The read is not a graphi response; whoever reads the file produces
// it, and for measurement it is preserved as one newline-terminated JSON
// source line under its own operation label. Contract version 1 remains the
// default; release inputs select this transcript only by embedding the exact
// version-2 measurement literal.
const (
	// PayloadOperationFollowupRead labels slice 2 of a two-slice transcript.
	PayloadOperationFollowupRead = "task_context/2-followup-read/1"
	// SavingsStopFollowupReadComplete is the stop reason when the target was
	// reached only with the designated read.
	SavingsStopFollowupReadComplete = "followup_read_complete"
)

// CaptureFollowupRead builds slice 2 from slice 1's designation: the exact
// repository bytes at the designated span, serialized as one compact Source
// line. It returns nil when the first response designates nothing. It never
// consults a judgement.
func CaptureFollowupRead(repository fs.FS, queryID string, first PreservedPayload, real PayloadCounter) (*PreservedPayload, error) {
	designation, err := compactFollowupDesignation(queryID, first)
	if err != nil {
		return nil, err
	}
	if designation == nil {
		return nil, nil
	}
	source, err := followupReadSource(repository, queryID, *designation)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return nil, fmt.Errorf("retrieval follow-up read: query %s: %w", queryID, err)
	}
	raw = append(raw, '\n')
	tokens, err := real.Count(append([]byte(nil), raw...))
	if err != nil {
		return nil, fmt.Errorf("retrieval follow-up read: query %s tokenize: %w", queryID, err)
	}
	return &PreservedPayload{
		Sequence: 2, Boundary: PayloadBoundaryCandidate, Operation: PayloadOperationFollowupRead,
		Bytes: raw, SHA256: SHA256Hex(raw), ByteCount: len(raw),
		TokenCounts: []PayloadTokenCount{
			{TokenizerID: TokenizerID, Tokens: len(strings.Fields(string(raw)))},
			{TokenizerID: real.TokenizerID, VocabularySHA256: real.VocabularySHA256, Tokens: tokens},
		},
	}, nil
}

// ScoreTaskContextTranscriptEqualRecallDev scores a one- or two-slice
// transcript under the draft's earliest-prefix rule. A one-slice transcript
// is contract 1 exactly. With two slices: if slice 1 alone reaches the
// target only slice 1 is charged; otherwise both are, whether the read
// reached the target or the query is censored at their sum. Slice 2 must be
// exactly the designated span from the pinned tree.
func ScoreTaskContextTranscriptEqualRecallDev(repository fs.FS, q Query, transcript []PreservedPayload, target RecallTarget, real PayloadCounter) (SavingsArmOutcome, error) {
	switch len(transcript) {
	case 1:
		return ScoreTaskContextEqualRecallDev(repository, q, transcript[0], target, real)
	case 2:
	default:
		return SavingsArmOutcome{}, fmt.Errorf("retrieval follow-up transcript: query %s has %d slices; a transcript is at most two", q.ID, len(transcript))
	}
	first, err := ScoreTaskContextEqualRecallDev(repository, q, transcript[0], target, real)
	if err != nil {
		return SavingsArmOutcome{}, err
	}
	designation, err := compactFollowupDesignation(q.ID, transcript[0])
	if err != nil {
		return SavingsArmOutcome{}, err
	}
	if designation == nil {
		return SavingsArmOutcome{}, fmt.Errorf("retrieval follow-up transcript: query %s designated no follow-up but a second slice was preserved", q.ID)
	}
	read, counts, err := validateFollowupRead(repository, q.ID, *designation, transcript[1], real)
	if err != nil {
		return SavingsArmOutcome{}, err
	}
	first.Payloads = []PreservedPayload{transcript[0], transcript[1]}
	if first.Status == SavingsOutcomeReached {
		return first, nil
	}
	sources, _, err := compactCandidateSources(q.ID, transcript[0].Bytes)
	if err != nil {
		return SavingsArmOutcome{}, err
	}
	grade3 := make([]Judgement, 0, len(q.Judgements))
	for _, judgement := range q.Judgements {
		if judgement.Grade == GradeMax {
			grade3 = append(grade3, judgement)
		}
	}
	complete := 0
	for _, judgement := range grade3 {
		hit := false
		for _, source := range append(sources, read) {
			if source.Path == judgement.Path && source.StartLine <= judgement.EndLine && source.EndLine >= judgement.StartLine {
				hit = true
			}
		}
		if hit {
			complete++
		}
	}
	firstTokens := 0
	for _, count := range transcript[0].TokenCounts {
		if count.TokenizerID == real.TokenizerID {
			firstTokens = count.Tokens
		}
	}
	total := firstTokens + counts[real.TokenizerID]
	outcome := SavingsArmOutcome{
		Target: target, Grade3SpansAtPrefix: complete, ConsumedPayloadSlices: 2,
		Payloads: []PreservedPayload{transcript[0], transcript[1]},
	}
	if complete >= target.RequiredSpans {
		outcome.Status = SavingsOutcomeReached
		outcome.StopReason = SavingsStopFollowupReadComplete
		outcome.TokensToTarget = equalRecallIntPointer(total)
	} else {
		outcome.Status = SavingsOutcomeMissed
		outcome.StopReason = SavingsStopFollowupReadComplete
		outcome.CensorLowerBoundTokens = equalRecallIntPointer(total)
	}
	return outcome, nil
}

// compactFollowupDesignation reads the designation out of a compact first
// slice; a legacy bundle or an undesignated response yields nil.
func compactFollowupDesignation(queryID string, first PreservedPayload) (*taskcompact.Followup, error) {
	var envelope candidateResponseEnvelope
	if err := json.Unmarshal(first.Bytes, &envelope); err != nil || envelope.Result == nil || len(envelope.Result.StructuredContent) == 0 || bytes.Equal(envelope.Result.StructuredContent, []byte("null")) {
		return nil, nil
	}
	var structured taskcompact.Structured
	if err := json.Unmarshal(envelope.Result.StructuredContent, &structured); err != nil {
		return nil, fmt.Errorf("retrieval follow-up transcript: query %s: %w", queryID, err)
	}
	if structured.Followup == nil {
		return nil, nil
	}
	if lines := structured.Followup.EndLine - structured.Followup.StartLine + 1; lines > compactv9.FollowupMaxLines {
		return nil, fmt.Errorf("retrieval follow-up transcript: query %s designation spans %d lines, exceeds the %d-line cap", queryID, lines, compactv9.FollowupMaxLines)
	}
	return structured.Followup, nil
}

// followupReadSource is the exact repository span a designation names.
func followupReadSource(repository fs.FS, queryID string, designation taskcompact.Followup) (taskcompact.Source, error) {
	if repository == nil {
		return taskcompact.Source{}, fmt.Errorf("retrieval follow-up read: query %s requires the pinned repository", queryID)
	}
	text, err := exactSourceSpan(repository, designation.Path, designation.StartLine, designation.EndLine)
	if err != nil {
		return taskcompact.Source{}, fmt.Errorf("retrieval follow-up read: query %s designation %s:%d-%d: %w", queryID, designation.Path, designation.StartLine, designation.EndLine, err)
	}
	return taskcompact.Source{Path: designation.Path, StartLine: designation.StartLine, EndLine: designation.EndLine, Text: text}, nil
}

// validateFollowupRead refuses every slice 2 that is not the designated
// span, verbatim, under the follow-up label with executable token counts.
func validateFollowupRead(repository fs.FS, queryID string, designation taskcompact.Followup, second PreservedPayload, real PayloadCounter) (taskcompact.Source, map[string]int, error) {
	requiredCounters := map[string]string{TokenizerID: "", real.TokenizerID: real.VocabularySHA256}
	counters := map[string]PayloadCounter{real.TokenizerID: real}
	counts, err := validatePreservedPayload(queryID, "candidate follow-up", second, 2, PayloadBoundaryCandidate, requiredCounters, counters)
	if err != nil {
		return taskcompact.Source{}, nil, err
	}
	if second.Operation != PayloadOperationFollowupRead {
		return taskcompact.Source{}, nil, fmt.Errorf("retrieval follow-up transcript: query %s slice 2 operation=%q, want %q", queryID, second.Operation, PayloadOperationFollowupRead)
	}
	if len(second.Bytes) == 0 || second.Bytes[len(second.Bytes)-1] != '\n' || bytes.Count(second.Bytes, []byte{'\n'}) != 1 {
		return taskcompact.Source{}, nil, fmt.Errorf("retrieval follow-up transcript: query %s slice 2 is not one exact line-delimited source", queryID)
	}
	decoder := json.NewDecoder(bytes.NewReader(second.Bytes))
	decoder.DisallowUnknownFields()
	var read taskcompact.Source
	if err := decoder.Decode(&read); err != nil {
		return taskcompact.Source{}, nil, fmt.Errorf("retrieval follow-up transcript: query %s slice 2 is not one follow-up source: %w", queryID, err)
	}
	if read.Path != designation.Path || read.StartLine != designation.StartLine || read.EndLine != designation.EndLine {
		return taskcompact.Source{}, nil, fmt.Errorf("retrieval follow-up transcript: query %s slice 2 reads %s:%d-%d, not the designated span %s:%d-%d", queryID, read.Path, read.StartLine, read.EndLine, designation.Path, designation.StartLine, designation.EndLine)
	}
	want, err := followupReadSource(repository, queryID, designation)
	if err != nil {
		return taskcompact.Source{}, nil, err
	}
	if read.Text != want.Text {
		return taskcompact.Source{}, nil, fmt.Errorf("retrieval follow-up transcript: query %s slice 2 bytes differ from %s:%d-%d in the pinned tree", queryID, designation.Path, designation.StartLine, designation.EndLine)
	}
	return read, counts, nil
}
