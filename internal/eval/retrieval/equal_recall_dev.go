package retrieval

// This file turns one already-captured task_context/2 MCP response into the
// candidate arm of the frozen equal-recall contract. It is development-only:
// callers cannot score a holdout query through this seam, and source credit is
// earned only by bytes present in the response and verified against the pinned
// repository.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/shape"
	taskcompact "github.com/samibel/graphi/engine/agenttools/taskctx/compact"
)

// SelectEqualRecallDevPopulation freezes the development-only answerable
// population at the contract's tokens_to_exact_span target: one independently
// reviewed grade-3 span out of all such spans for the query. The input must be
// a derived dev-only dataset; encountering even one non-dev row is an error,
// not a row to silently filter away.
func SelectEqualRecallDevPopulation(dataset *Dataset) ([]SavingsPopulationMember, error) {
	if dataset == nil {
		return nil, fmt.Errorf("retrieval equal-recall dev: nil dataset")
	}
	members := make([]SavingsPopulationMember, 0, len(dataset.Queries))
	for _, q := range dataset.Queries {
		if q.Split != SplitDev {
			return nil, fmt.Errorf("retrieval equal-recall dev: dataset %s contains query %s from split %q; only a dev-only slice is allowed", dataset.ID, q.ID, q.Split)
		}
		if q.Stratum == StratumNoHit {
			continue
		}
		total := 0
		for _, judgement := range q.Judgements {
			if judgement.Grade == GradeMax {
				total++
			}
		}
		if total == 0 {
			continue
		}
		member := SavingsPopulationMember{
			QueryID:  q.ID,
			FamilyID: q.FamilyID,
			Split:    q.Split,
			Stratum:  q.Stratum,
			Target:   RecallTarget{Grade: SavingsGrade, RequiredSpans: 1, TotalSpans: total},
		}
		if strings.TrimSpace(member.QueryID) == "" || strings.TrimSpace(member.FamilyID) == "" || strings.TrimSpace(member.Stratum) == "" {
			return nil, fmt.Errorf("retrieval equal-recall dev: answerable query requires id, family_id and stratum")
		}
		members = append(members, member)
	}
	if len(members) == 0 {
		return nil, fmt.Errorf("retrieval equal-recall dev: dataset %s has no answerable development queries", dataset.ID)
	}
	return members, nil
}

// ScoreTaskContextEqualRecallDev scores one complete, indivisible MCP response
// against a predeclared rational grade-3 target. Point citations do not carry
// source and therefore earn no span credit. Any source-verified overlap earns
// one indivisible span credit; individual lines never earn fractional credit,
// and duplicate overlaps cannot credit the same judgement twice.
//
// The returned SavingsArmOutcome is ready for the existing frozen contract
// validator. On a miss, TokensToTarget remains undefined and the complete MCP
// response token count is propagated as the right-censor lower bound.
func ScoreTaskContextEqualRecallDev(repository fs.FS, q Query, payload PreservedPayload, target RecallTarget, real PayloadCounter) (SavingsArmOutcome, error) {
	if repository == nil {
		return SavingsArmOutcome{}, fmt.Errorf("retrieval equal-recall dev: query %s requires the pinned repository for source verification", q.ID)
	}
	if q.Split != SplitDev {
		return SavingsArmOutcome{}, fmt.Errorf("retrieval equal-recall dev: refusing query %s from split %q; only %q is allowed", q.ID, q.Split, SplitDev)
	}
	if q.Stratum == "" || q.Stratum == StratumNoHit {
		return SavingsArmOutcome{}, fmt.Errorf("retrieval equal-recall dev: query %s is not in an answerable stratum", q.ID)
	}
	if err := validateRecallTarget(target); err != nil {
		return SavingsArmOutcome{}, fmt.Errorf("retrieval equal-recall dev: query %s: %w", q.ID, err)
	}
	grade3 := make([]Judgement, 0, len(q.Judgements))
	for _, judgement := range q.Judgements {
		if judgement.Grade == GradeMax {
			grade3 = append(grade3, judgement)
		}
	}
	if target.TotalSpans != len(grade3) {
		return SavingsArmOutcome{}, fmt.Errorf("retrieval equal-recall dev: query %s target total_spans=%d, observed %d grade-3 spans", q.ID, target.TotalSpans, len(grade3))
	}

	requiredCounters := map[string]string{
		TokenizerID:      "",
		real.TokenizerID: real.VocabularySHA256,
	}
	counters := map[string]PayloadCounter{real.TokenizerID: real}
	counts, err := validatePreservedPayload(q.ID, "candidate", payload, 1, PayloadBoundaryCandidate, requiredCounters, counters)
	if err != nil {
		return SavingsArmOutcome{}, err
	}
	if payload.Operation != PayloadOperationTaskContext {
		return SavingsArmOutcome{}, fmt.Errorf("retrieval equal-recall dev: query %s payload operation=%q, want %q", q.ID, payload.Operation, PayloadOperationTaskContext)
	}
	if sources, compact, err := compactCandidateSources(q.ID, payload.Bytes); compact {
		if err != nil {
			return SavingsArmOutcome{}, err
		}
		covered := make([]bool, len(grade3))
		for _, emitted := range sources {
			source, err := exactSourceSpan(repository, emitted.Path, emitted.StartLine, emitted.EndLine)
			if err != nil {
				return SavingsArmOutcome{}, fmt.Errorf("retrieval equal-recall dev: query %s compact source: %w", q.ID, err)
			}
			if source != emitted.Text {
				return SavingsArmOutcome{}, fmt.Errorf("retrieval equal-recall dev: query %s compact source differs from %s:%d-%d", q.ID, emitted.Path, emitted.StartLine, emitted.EndLine)
			}
			for i, judgement := range grade3 {
				if emitted.Path == judgement.Path && emitted.StartLine <= judgement.EndLine && emitted.EndLine >= judgement.StartLine {
					covered[i] = true
				}
			}
		}
		complete := 0
		for _, hit := range covered {
			if hit {
				complete++
			}
		}
		return equalRecallCandidateOutcome(payload, target, counts[real.TokenizerID], complete), nil
	}
	if _, err := ValidateCandidateBundleBytes(q.ID, payload.Bytes); err != nil {
		return SavingsArmOutcome{}, err
	}
	bundle, err := taskContextBundleFromCandidateBytes(payload.Bytes)
	if err != nil {
		return SavingsArmOutcome{}, fmt.Errorf("retrieval equal-recall dev: query %s: %w", q.ID, err)
	}
	if err := contract.ValidateResult(&bundle); err != nil {
		return SavingsArmOutcome{}, fmt.Errorf("retrieval equal-recall dev: query %s invalid inner bundle: %w", q.ID, err)
	}

	coveredLines := make([]map[int]bool, len(grade3))
	for i := range coveredLines {
		coveredLines[i] = map[int]bool{}
	}
	for _, evidence := range bundle.Evidence {
		if evidence.Snippet == "" {
			if evidence.TextHash != "" {
				return SavingsArmOutcome{}, fmt.Errorf("retrieval equal-recall dev: query %s evidence %s has text_hash without emitted source bytes", q.ID, evidence.RefID)
			}
			continue
		}
		if evidence.ClaimType != "" {
			return SavingsArmOutcome{}, fmt.Errorf("retrieval equal-recall dev: query %s evidence %s mixes snippet bytes with claim_type", q.ID, evidence.RefID)
		}
		if evidence.TextHash != shape.TextHash(evidence.Snippet) {
			return SavingsArmOutcome{}, fmt.Errorf("retrieval equal-recall dev: query %s evidence %s text_hash does not identify its snippet bytes", q.ID, evidence.RefID)
		}
		start, end, err := exactEvidenceSpan(evidence.Span)
		if err != nil {
			return SavingsArmOutcome{}, fmt.Errorf("retrieval equal-recall dev: query %s evidence %s: %w", q.ID, evidence.RefID, err)
		}
		if evidence.Line != start {
			return SavingsArmOutcome{}, fmt.Errorf("retrieval equal-recall dev: query %s evidence %s line=%d, span starts at %d", q.ID, evidence.RefID, evidence.Line, start)
		}
		if !fs.ValidPath(evidence.Path) {
			return SavingsArmOutcome{}, fmt.Errorf("retrieval equal-recall dev: query %s evidence %s has invalid repository path %q", q.ID, evidence.RefID, evidence.Path)
		}
		source, err := exactSourceSpan(repository, evidence.Path, start, end)
		if err != nil {
			return SavingsArmOutcome{}, fmt.Errorf("retrieval equal-recall dev: query %s evidence %s: %w", q.ID, evidence.RefID, err)
		}
		if source != evidence.Snippet {
			return SavingsArmOutcome{}, fmt.Errorf("retrieval equal-recall dev: query %s evidence %s snippet bytes differ from %s:%d-%d", q.ID, evidence.RefID, evidence.Path, start, end)
		}
		for i, judgement := range grade3 {
			if evidence.Path != judgement.Path {
				continue
			}
			from := max(start, judgement.StartLine)
			to := min(end, judgement.EndLine)
			for line := from; line <= to; line++ {
				coveredLines[i][line] = true
			}
		}
	}

	complete := 0
	for i := range grade3 {
		// A reviewed span is the atomic unit. Any verified emitted source
		// overlap credits that whole span once; individual lines never earn
		// fractional credit.
		if len(coveredLines[i]) > 0 {
			complete++
		}
	}
	return equalRecallCandidateOutcome(payload, target, counts[real.TokenizerID], complete), nil
}

func equalRecallCandidateOutcome(payload PreservedPayload, target RecallTarget, tokens, complete int) SavingsArmOutcome {
	outcome := SavingsArmOutcome{
		Target:                target,
		StopReason:            SavingsStopOneCallComplete,
		Grade3SpansAtPrefix:   complete,
		ConsumedPayloadSlices: 1,
		Payloads:              []PreservedPayload{payload},
	}
	if complete >= target.RequiredSpans {
		outcome.Status = SavingsOutcomeReached
		outcome.TokensToTarget = equalRecallIntPointer(tokens)
	} else {
		outcome.Status = SavingsOutcomeMissed
		outcome.CensorLowerBoundTokens = equalRecallIntPointer(tokens)
	}
	return outcome
}

func compactCandidateSources(queryID string, raw []byte) ([]taskcompact.Source, bool, error) {
	var envelope candidateResponseEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Result == nil || len(envelope.Result.StructuredContent) == 0 || bytes.Equal(envelope.Result.StructuredContent, []byte("null")) {
		return nil, false, nil
	}
	if _, err := ValidateCompactCandidateBundleBytes(queryID, raw); err != nil {
		return nil, true, err
	}
	var structured taskcompact.Structured
	if err := json.Unmarshal(envelope.Result.StructuredContent, &structured); err != nil {
		return nil, true, err
	}
	return structured.Sources, true, nil
}

func taskContextBundleFromCandidateBytes(raw []byte) (contract.Result, error) {
	var envelope candidateResponseEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return contract.Result{}, fmt.Errorf("decode MCP response: %w", err)
	}
	if envelope.Result == nil || len(envelope.Result.Content) != 1 {
		return contract.Result{}, fmt.Errorf("MCP response does not contain exactly one result text block")
	}
	var bundle contract.Result
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &bundle); err != nil {
		return contract.Result{}, fmt.Errorf("decode task_context/2 result: %w", err)
	}
	return bundle, nil
}

func exactEvidenceSpan(span string) (int, int, error) {
	startText, endText, ok := strings.Cut(span, "-")
	if !ok || strings.Contains(endText, "-") {
		return 0, 0, fmt.Errorf("invalid source span %q", span)
	}
	start, startErr := strconv.Atoi(startText)
	end, endErr := strconv.Atoi(endText)
	if startErr != nil || endErr != nil || start < 1 || end < start {
		return 0, 0, fmt.Errorf("invalid source span %q", span)
	}
	return start, end, nil
}

func exactSourceSpan(repository fs.FS, path string, start, end int) (string, error) {
	raw, err := fs.ReadFile(repository, path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	normalized := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if strings.HasSuffix(normalized, "\n") {
		normalized = normalized[:len(normalized)-1]
	}
	lines := strings.Split(normalized, "\n")
	if normalized == "" {
		lines = nil
	}
	if end > len(lines) {
		return "", fmt.Errorf("source %s has %d lines, span ends at %d", path, len(lines), end)
	}
	return strings.Join(lines[start-1:end], "\n"), nil
}

func equalRecallIntPointer(value int) *int { return &value }
