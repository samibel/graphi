package retrieval

// This file is the executable half of the SW-274 measurement contract. It
// deliberately validates inputs without calculating a savings statistic: the
// contract must exist before any result does.

import (
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	// MeasurementContractVersion identifies the frozen SW-266 savings method.
	MeasurementContractVersion = "sw266-measurement-contract/1"

	// The candidate is one complete task_context/2 response at the already
	// frozen product budget. The comparator's implementation version remains a
	// required run input because slice B, not this slice, owns that code.
	SavingsCandidateMethod  = "task_context/2"
	SavingsCandidateBudget  = 1200
	SavingsComparatorMethod = "GrepRead"

	// GrepReadWindowLines retains the existing 40-line window as an operation
	// parameter. It is NOT an accounting shortcut: validators below accept only
	// counts recomputed from the response bytes the operation actually emitted.
	GrepReadWindowLines = 40

	SavingsGrade = 3

	SavingsEstimator                  = "median of paired per-query percent token savings"
	SavingsIntervalType               = "two-sided split-stratified family-cluster percentile bootstrap"
	SavingsConfidenceLevelBasisPoints = 9500
	SavingsBootstrapReplicates        = 10000
	SavingsBootstrapSeedMethod        = "sha256(dataset_sha256 + newline + measurement_contract_version)"
	SavingsConfidencePopulation       = "all answerable English queries in the full frozen release dataset for the pinned Cobra commit; query families are the resampling units"

	// SavingsDisplayDecimalPlaces is a display ceiling, not stored arithmetic.
	// Counts and exact fractions remain authoritative in machine artifacts.
	SavingsDisplayDecimalPlaces = 1
)

// MeasurementContract is the identity a future raw run and report must embed.
// ValidateMeasurementContract compares it exactly with FrozenMeasurementContract
// so a method change cannot retain the old name.
type MeasurementContract struct {
	Version                   string         `json:"version"`
	CandidateMethod           string         `json:"candidate_method"`
	CandidateTokenBudget      int            `json:"candidate_token_budget"`
	ComparatorMethod          string         `json:"comparator_method"`
	ComparatorReadWindowLines int            `json:"comparator_read_window_lines"`
	RelevantGrade             int            `json:"relevant_grade"`
	MissRule                  string         `json:"miss_rule"`
	PayloadRule               string         `json:"payload_rule"`
	EqualRecallRule           string         `json:"equal_recall_rule"`
	Confidence                ConfidenceSpec `json:"confidence"`
	DisplayDecimalPlaces      int            `json:"display_decimal_places"`
}

// ConfidenceSpec closes SW-266's formerly free-form confidence field. Every
// component is method identity and is compared exactly before aggregation.
type ConfidenceSpec struct {
	Estimator        string `json:"estimator"`
	IntervalType     string `json:"interval_type"`
	LevelBasisPoints int    `json:"level_basis_points"`
	Population       string `json:"population"`
	ResamplingUnit   string `json:"resampling_unit"`
	Stratification   string `json:"stratification"`
	Replicates       int    `json:"replicates"`
	SeedMethod       string `json:"seed_method"`
}

// FrozenConfidenceSpec returns a value, rather than an exported variable, so
// callers cannot mutate the package's definition in place.
func FrozenConfidenceSpec() ConfidenceSpec {
	return ConfidenceSpec{
		Estimator:        SavingsEstimator,
		IntervalType:     SavingsIntervalType,
		LevelBasisPoints: SavingsConfidenceLevelBasisPoints,
		Population:       SavingsConfidencePopulation,
		ResamplingUnit:   "family_id (paired candidate and comparator observations stay together)",
		Stratification:   "split (dev and sealed holdout retain their observed family counts)",
		Replicates:       SavingsBootstrapReplicates,
		SeedMethod:       SavingsBootstrapSeedMethod,
	}
}

// FrozenMeasurementContract returns the method identity agreed before a run.
func FrozenMeasurementContract() MeasurementContract {
	return MeasurementContract{
		Version:                   MeasurementContractVersion,
		CandidateMethod:           SavingsCandidateMethod,
		CandidateTokenBudget:      SavingsCandidateBudget,
		ComparatorMethod:          SavingsComparatorMethod,
		ComparatorReadWindowLines: GrepReadWindowLines,
		RelevantGrade:             SavingsGrade,
		MissRule: "a miss is a right-censored observation at the complete preserved transcript token count; " +
			"tokens-to-target and paired percent saving are undefined, the miss count is reported, and any magnitude aggregate fails validation",
		PayloadRule:          "count only complete response byte slices captured below final serialization; preserve each slice and sha256, and recompute every byte and token count from those bytes",
		EqualRecallRule:      "compare the earliest indivisible response prefix reaching the same predeclared rational grade-3 span-recall target; whole spans only, ties contribute zero",
		Confidence:           FrozenConfidenceSpec(),
		DisplayDecimalPlaces: SavingsDisplayDecimalPlaces,
	}
}

// ValidateMeasurementContract refuses drift hidden behind the frozen version.
func ValidateMeasurementContract(got MeasurementContract) error {
	want := FrozenMeasurementContract()
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("retrieval measurement contract: method identity differs from %s", MeasurementContractVersion)
	}
	return nil
}

// ValidateConfidenceSpec rejects an arbitrary confidence label or a partially
// specified method.
func ValidateConfidenceSpec(got ConfidenceSpec) error {
	want := FrozenConfidenceSpec()
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("retrieval measurement contract: confidence must be estimator=%q, interval_type=%q, level=%d basis points, population=%q, resampling_unit=%q, stratification=%q, replicates=%d, seed_method=%q",
			want.Estimator, want.IntervalType, want.LevelBasisPoints, want.Population,
			want.ResamplingUnit, want.Stratification, want.Replicates, want.SeedMethod)
	}
	return nil
}

// RecallTarget is exact rational grade-3 span recall. Integer numerator and
// denominator avoid a float equality decision at the comparison boundary.
type RecallTarget struct {
	Grade         int `json:"grade"`
	RequiredSpans int `json:"required_spans"`
	TotalSpans    int `json:"total_spans"`
}

// SavingsPopulationMember is one and only one unit in an aggregate. Target is
// frozen with the query, before either arm's output is inspected.
type SavingsPopulationMember struct {
	QueryID  string       `json:"query_id"`
	FamilyID string       `json:"family_id"`
	Split    string       `json:"split"`
	Stratum  string       `json:"stratum"`
	Target   RecallTarget `json:"target"`
}

// PayloadBoundary names the two response-byte boundaries. Requests, logs and
// timing metadata are outside both boundaries.
type PayloadBoundary string

const (
	PayloadBoundaryCandidate PayloadBoundary = "mcp_jsonrpc_response_bytes"
	PayloadBoundaryGrepRead  PayloadBoundary = "grepread_response_bytes"
)

// Payload operations. Each preserved slice is one indivisible response; a
// target reached inside a response is charged the whole response.
const (
	PayloadOperationTaskContext = "task_context/2"
	PayloadOperationGrep        = "grep"
	PayloadOperationRead        = "read"
)

// PayloadTokenCount is a claimed count over one preserved byte slice. A real
// tokenizer carries its vocabulary digest; whitespace-fields-v1 has no
// vocabulary artifact and therefore carries an empty digest.
type PayloadTokenCount struct {
	TokenizerID      string `json:"tokenizer_id"`
	VocabularySHA256 string `json:"vocabulary_sha256,omitempty"`
	Tokens           int    `json:"tokens"`
}

// PreservedPayload is the exact byte slice returned to the actor after final
// serialization. []byte's JSON representation is base64, preserving arbitrary
// escaping and envelope changes rather than reconstructing them later.
type PreservedPayload struct {
	Sequence    int                 `json:"sequence"`
	Boundary    PayloadBoundary     `json:"boundary"`
	Operation   string              `json:"operation"`
	Bytes       []byte              `json:"bytes"`
	SHA256      string              `json:"sha256"`
	ByteCount   int                 `json:"byte_count"`
	TokenCounts []PayloadTokenCount `json:"token_counts"`
}

// PayloadCounter is executable tokenizer identity supplied by the tokenizer
// slice. Validation fails when a claimed tokenizer has no counter.
type PayloadCounter struct {
	TokenizerID      string
	VocabularySHA256 string
	Count            func([]byte) (int, error)
}

const (
	SavingsOutcomeReached = "reached"
	SavingsOutcomeMissed  = "missed"

	SavingsStopOneCallComplete = "one_call_complete"
	SavingsStopExhausted       = "exhausted"
	SavingsStopMaxReads        = "max_reads"
)

// SavingsArmOutcome records either an attained prefix or a censored miss.
// The complete transcript is preserved even when an earlier prefix reached
// the target; this keeps GrepRead execution judgement-blind.
type SavingsArmOutcome struct {
	Target                 RecallTarget       `json:"target"`
	Status                 string             `json:"status"`
	StopReason             string             `json:"stop_reason"`
	Grade3SpansAtPrefix    int                `json:"grade3_spans_at_prefix"`
	ConsumedPayloadSlices  int                `json:"consumed_payload_slices"`
	TokensToTarget         *int               `json:"tokens_to_target,omitempty"`
	CensorLowerBoundTokens *int               `json:"censor_lower_bound_tokens,omitempty"`
	Payloads               []PreservedPayload `json:"payloads"`
}

// SavingsObservation is a paired query outcome. Each arm repeats the frozen
// target so accidental unequal-recall comparisons fail validation.
type SavingsObservation struct {
	QueryID   string            `json:"query_id"`
	Candidate SavingsArmOutcome `json:"candidate"`
	GrepRead  SavingsArmOutcome `json:"grepread"`
}

// SavingsAggregateInput is everything a later aggregate needs before it may
// calculate a median. This slice intentionally defines validation only.
type SavingsAggregateInput struct {
	Contract                  MeasurementContract       `json:"contract"`
	DatasetSHA256             string                    `json:"dataset_sha256"`
	RepoSHA                   string                    `json:"repo_sha"`
	CandidateVersion          string                    `json:"candidate_version"`
	ComparatorVersion         string                    `json:"comparator_version"`
	TokenizerID               string                    `json:"tokenizer_id"`
	TokenizerVocabularySHA256 string                    `json:"tokenizer_vocabulary_sha256"`
	Confidence                ConfidenceSpec            `json:"confidence"`
	Population                []SavingsPopulationMember `json:"population"`
	Observations              []SavingsObservation      `json:"observations"`
}

// ValidateSavingsAggregateInput fails closed before a savings median exists.
// In particular, it never offers a complete-case mode: missing observations
// and right-censored misses are errors, not warnings.
func ValidateSavingsAggregateInput(in SavingsAggregateInput, counters map[string]PayloadCounter) error {
	if err := ValidateMeasurementContract(in.Contract); err != nil {
		return err
	}
	if err := ValidateConfidenceSpec(in.Confidence); err != nil {
		return err
	}
	for _, required := range []struct {
		name  string
		value string
	}{
		{"dataset_sha256", in.DatasetSHA256},
		{"repo_sha", in.RepoSHA},
		{"candidate_version", in.CandidateVersion},
		{"comparator_version", in.ComparatorVersion},
		{"tokenizer_id", in.TokenizerID},
	} {
		if strings.TrimSpace(required.value) == "" {
			return fmt.Errorf("retrieval measurement contract: %s is required", required.name)
		}
	}
	if !isLowerHexDigest(in.DatasetSHA256, 64) {
		return fmt.Errorf("retrieval measurement contract: dataset_sha256 must be 64 lowercase hex characters")
	}
	if !isLowerHexDigest(in.RepoSHA, 40) {
		return fmt.Errorf("retrieval measurement contract: repo_sha must be a full 40-character lowercase commit digest")
	}
	if in.TokenizerID == TokenizerID {
		return fmt.Errorf("retrieval measurement contract: tokenizer_id must name the pinned real tokenizer, not %s", TokenizerID)
	}
	if !isLowerHexDigest(in.TokenizerVocabularySHA256, 64) {
		return fmt.Errorf("retrieval measurement contract: tokenizer_vocabulary_sha256 must be 64 lowercase hex characters")
	}
	if len(in.Population) == 0 {
		return fmt.Errorf("retrieval measurement contract: the answerable release population is empty")
	}
	if len(in.Observations) != len(in.Population) {
		return fmt.Errorf("retrieval measurement contract: complete population required: got %d observations for %d answerable queries; complete-case aggregation is forbidden", len(in.Observations), len(in.Population))
	}

	members := make(map[string]SavingsPopulationMember, len(in.Population))
	familiesBySplit := map[string]string{}
	for i, member := range in.Population {
		if strings.TrimSpace(member.QueryID) == "" || strings.TrimSpace(member.FamilyID) == "" || strings.TrimSpace(member.Stratum) == "" {
			return fmt.Errorf("retrieval measurement contract: population member %d requires query_id, family_id and stratum", i)
		}
		if member.Split != SplitDev && member.Split != SplitHoldout {
			return fmt.Errorf("retrieval measurement contract: query %s has split %q, want %q or %q", member.QueryID, member.Split, SplitDev, SplitHoldout)
		}
		if member.Stratum == StratumNoHit {
			return fmt.Errorf("retrieval measurement contract: query %s is no_hit and cannot enter the answerable savings population", member.QueryID)
		}
		if _, exists := members[member.QueryID]; exists {
			return fmt.Errorf("retrieval measurement contract: duplicate population query %s", member.QueryID)
		}
		if split, exists := familiesBySplit[member.FamilyID]; exists && split != member.Split {
			return fmt.Errorf("retrieval measurement contract: family %s crosses splits %s and %s", member.FamilyID, split, member.Split)
		}
		if err := validateRecallTarget(member.Target); err != nil {
			return fmt.Errorf("retrieval measurement contract: query %s: %w", member.QueryID, err)
		}
		familiesBySplit[member.FamilyID] = member.Split
		members[member.QueryID] = member
	}

	seen := make(map[string]bool, len(in.Observations))
	for i, observation := range in.Observations {
		if observation.QueryID != in.Population[i].QueryID {
			return fmt.Errorf("retrieval measurement contract: observation %d is query %q, want frozen population order %q", i, observation.QueryID, in.Population[i].QueryID)
		}
		member, exists := members[observation.QueryID]
		if !exists {
			return fmt.Errorf("retrieval measurement contract: observation %d names query %q outside the frozen population", i, observation.QueryID)
		}
		if seen[observation.QueryID] {
			return fmt.Errorf("retrieval measurement contract: duplicate observation for query %s", observation.QueryID)
		}
		seen[observation.QueryID] = true
		if observation.Candidate.Target != member.Target || observation.GrepRead.Target != member.Target {
			return fmt.Errorf("retrieval measurement contract: query %s arms must use the predeclared equal-recall target %+v", observation.QueryID, member.Target)
		}
		candidateMiss, err := validateSavingsArm(observation.QueryID, "candidate", observation.Candidate, PayloadBoundaryCandidate, in, counters)
		if err != nil {
			return err
		}
		baselineMiss, err := validateSavingsArm(observation.QueryID, "grepread", observation.GrepRead, PayloadBoundaryGrepRead, in, counters)
		if err != nil {
			return err
		}
		if candidateMiss || baselineMiss {
			return fmt.Errorf("retrieval measurement contract: query %s is right-censored (candidate_miss=%t, grepread_miss=%t); report the miss and censor bound, but refuse every magnitude aggregate — complete-case aggregation is forbidden", observation.QueryID, candidateMiss, baselineMiss)
		}
		if observation.GrepRead.TokensToTarget == nil || *observation.GrepRead.TokensToTarget <= 0 {
			return fmt.Errorf("retrieval measurement contract: query %s grepread tokens-to-target must be positive before paired percent saving is defined", observation.QueryID)
		}
	}
	for _, member := range in.Population {
		if !seen[member.QueryID] {
			return fmt.Errorf("retrieval measurement contract: complete population required: query %s has no observation; complete-case aggregation is forbidden", member.QueryID)
		}
	}
	return nil
}

func validateRecallTarget(target RecallTarget) error {
	if target.Grade != SavingsGrade {
		return fmt.Errorf("recall target grade is %d, want exact grade %d", target.Grade, SavingsGrade)
	}
	if target.TotalSpans < 1 || target.RequiredSpans < 1 || target.RequiredSpans > target.TotalSpans {
		return fmt.Errorf("recall target must have 1 <= required_spans <= total_spans, got %d/%d", target.RequiredSpans, target.TotalSpans)
	}
	return nil
}

func validateSavingsArm(queryID, armName string, arm SavingsArmOutcome, boundary PayloadBoundary, in SavingsAggregateInput, counters map[string]PayloadCounter) (bool, error) {
	if err := validateRecallTarget(arm.Target); err != nil {
		return false, fmt.Errorf("retrieval measurement contract: query %s %s: %w", queryID, armName, err)
	}
	if len(arm.Payloads) == 0 {
		return false, fmt.Errorf("retrieval measurement contract: query %s %s preserves no payload byte slices; reconstructed or estimated counts are forbidden", queryID, armName)
	}
	if boundary == PayloadBoundaryCandidate {
		if len(arm.Payloads) != 1 || arm.Payloads[0].Operation != PayloadOperationTaskContext || arm.StopReason != SavingsStopOneCallComplete {
			return false, fmt.Errorf("retrieval measurement contract: query %s candidate must preserve exactly one complete task_context/2 response with stop_reason=%s", queryID, SavingsStopOneCallComplete)
		}
	} else {
		if arm.Payloads[0].Operation != PayloadOperationGrep || (arm.StopReason != SavingsStopExhausted && arm.StopReason != SavingsStopMaxReads) {
			return false, fmt.Errorf("retrieval measurement contract: query %s grepread must preserve the initial grep response and run judgement-blind to exhausted or max_reads", queryID)
		}
		for _, payload := range arm.Payloads[1:] {
			if payload.Operation != PayloadOperationRead {
				return false, fmt.Errorf("retrieval measurement contract: query %s grepread payload %d operation is %q, want read", queryID, payload.Sequence, payload.Operation)
			}
		}
	}

	requiredCounters := map[string]string{
		TokenizerID:    "",
		in.TokenizerID: in.TokenizerVocabularySHA256,
	}
	prefixTokens, totalTokens := 0, 0
	for i, payload := range arm.Payloads {
		counts, err := validatePreservedPayload(queryID, armName, payload, i+1, boundary, requiredCounters, counters)
		if err != nil {
			return false, err
		}
		totalTokens += counts[in.TokenizerID]
		if i < arm.ConsumedPayloadSlices {
			prefixTokens += counts[in.TokenizerID]
		}
	}
	if arm.Grade3SpansAtPrefix < 0 || arm.Grade3SpansAtPrefix > arm.Target.TotalSpans {
		return false, fmt.Errorf("retrieval measurement contract: query %s %s grade3_spans_at_prefix=%d is outside [0,%d]", queryID, armName, arm.Grade3SpansAtPrefix, arm.Target.TotalSpans)
	}

	switch arm.Status {
	case SavingsOutcomeReached:
		if arm.ConsumedPayloadSlices < 1 || arm.ConsumedPayloadSlices > len(arm.Payloads) {
			return false, fmt.Errorf("retrieval measurement contract: query %s %s reached target with invalid consumed_payload_slices=%d", queryID, armName, arm.ConsumedPayloadSlices)
		}
		if arm.Grade3SpansAtPrefix < arm.Target.RequiredSpans {
			return false, fmt.Errorf("retrieval measurement contract: query %s %s says reached with only %d/%d required grade-3 spans", queryID, armName, arm.Grade3SpansAtPrefix, arm.Target.RequiredSpans)
		}
		if arm.TokensToTarget == nil || *arm.TokensToTarget != prefixTokens {
			return false, fmt.Errorf("retrieval measurement contract: query %s %s tokens_to_target must recompute from the earliest complete response prefix: got %v, want %d", queryID, armName, optionalInt(arm.TokensToTarget), prefixTokens)
		}
		if arm.CensorLowerBoundTokens != nil {
			return false, fmt.Errorf("retrieval measurement contract: query %s %s reached target but carries a censor bound", queryID, armName)
		}
		return false, nil
	case SavingsOutcomeMissed:
		if arm.ConsumedPayloadSlices != len(arm.Payloads) || arm.Grade3SpansAtPrefix >= arm.Target.RequiredSpans {
			return false, fmt.Errorf("retrieval measurement contract: query %s %s miss must describe the complete transcript below the target", queryID, armName)
		}
		if arm.TokensToTarget != nil {
			return false, fmt.Errorf("retrieval measurement contract: query %s %s tokens-to-target is undefined on a miss", queryID, armName)
		}
		if arm.CensorLowerBoundTokens == nil || *arm.CensorLowerBoundTokens != totalTokens {
			return false, fmt.Errorf("retrieval measurement contract: query %s %s censor bound must recompute from the complete transcript: got %v, want %d", queryID, armName, optionalInt(arm.CensorLowerBoundTokens), totalTokens)
		}
		return true, nil
	default:
		return false, fmt.Errorf("retrieval measurement contract: query %s %s status %q is not reached or missed", queryID, armName, arm.Status)
	}
}

func validatePreservedPayload(queryID, armName string, payload PreservedPayload, sequence int, boundary PayloadBoundary, requiredCounters map[string]string, counters map[string]PayloadCounter) (map[string]int, error) {
	if payload.Sequence != sequence {
		return nil, fmt.Errorf("retrieval measurement contract: query %s %s payload sequence=%d, want %d", queryID, armName, payload.Sequence, sequence)
	}
	if payload.Boundary != boundary {
		return nil, fmt.Errorf("retrieval measurement contract: query %s %s payload %d boundary=%q, want %q", queryID, armName, sequence, payload.Boundary, boundary)
	}
	if payload.Bytes == nil {
		return nil, fmt.Errorf("retrieval measurement contract: query %s %s payload %d has no preserved bytes; reconstructed or estimated counts are forbidden", queryID, armName, sequence)
	}
	if !utf8.Valid(payload.Bytes) {
		return nil, fmt.Errorf("retrieval measurement contract: query %s %s payload %d is not valid UTF-8", queryID, armName, sequence)
	}
	if payload.SHA256 != SHA256Hex(payload.Bytes) {
		return nil, fmt.Errorf("retrieval measurement contract: query %s %s payload %d sha256 does not match its preserved bytes", queryID, armName, sequence)
	}
	if payload.ByteCount != len(payload.Bytes) {
		return nil, fmt.Errorf("retrieval measurement contract: query %s %s payload %d byte_count=%d, recomputed=%d", queryID, armName, sequence, payload.ByteCount, len(payload.Bytes))
	}
	if len(payload.TokenCounts) != len(requiredCounters) {
		return nil, fmt.Errorf("retrieval measurement contract: query %s %s payload %d has %d token counts, want exactly %d", queryID, armName, sequence, len(payload.TokenCounts), len(requiredCounters))
	}
	got := make(map[string]int, len(payload.TokenCounts))
	for _, claimed := range payload.TokenCounts {
		wantVocabulary, required := requiredCounters[claimed.TokenizerID]
		if !required {
			return nil, fmt.Errorf("retrieval measurement contract: query %s %s payload %d has uncontracted tokenizer %q", queryID, armName, sequence, claimed.TokenizerID)
		}
		if _, duplicate := got[claimed.TokenizerID]; duplicate {
			return nil, fmt.Errorf("retrieval measurement contract: query %s %s payload %d repeats tokenizer %q", queryID, armName, sequence, claimed.TokenizerID)
		}
		if claimed.VocabularySHA256 != wantVocabulary {
			return nil, fmt.Errorf("retrieval measurement contract: query %s %s payload %d tokenizer %q vocabulary identity mismatch", queryID, armName, sequence, claimed.TokenizerID)
		}
		count := 0
		if claimed.TokenizerID == TokenizerID {
			// This counter is already part of the repository; do not let a
			// caller substitute an implementation that happens to agree with
			// a reconstructed claim.
			count = len(strings.Fields(string(payload.Bytes)))
		} else {
			counter, exists := counters[claimed.TokenizerID]
			if !exists || counter.Count == nil {
				return nil, fmt.Errorf("retrieval measurement contract: query %s %s payload %d tokenizer %q has no executable counter; estimated counts are forbidden", queryID, armName, sequence, claimed.TokenizerID)
			}
			if counter.TokenizerID != claimed.TokenizerID || counter.VocabularySHA256 != wantVocabulary {
				return nil, fmt.Errorf("retrieval measurement contract: query %s %s payload %d tokenizer %q vocabulary identity mismatch", queryID, armName, sequence, claimed.TokenizerID)
			}
			var err error
			count, err = counter.Count(payload.Bytes)
			if err != nil {
				return nil, fmt.Errorf("retrieval measurement contract: query %s %s payload %d tokenizer %q: %w", queryID, armName, sequence, claimed.TokenizerID, err)
			}
		}
		if claimed.Tokens != count {
			return nil, fmt.Errorf("retrieval measurement contract: query %s %s payload %d tokenizer %q tokens=%d, recomputed=%d", queryID, armName, sequence, claimed.TokenizerID, claimed.Tokens, count)
		}
		got[claimed.TokenizerID] = count
	}
	for id := range requiredCounters {
		if _, exists := got[id]; !exists {
			return nil, fmt.Errorf("retrieval measurement contract: query %s %s payload %d lacks tokenizer %q", queryID, armName, sequence, id)
		}
	}
	return got, nil
}

func optionalInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

func isLowerHexDigest(s string, size int) bool {
	if len(s) != size {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// RequiredClaimLimitation is the exact negative scope statement the checker
// permits. General-Go wording outside this denial is rejected.
const RequiredClaimLimitation = "This is a descriptive result for that pinned Cobra dataset, not an estimate for Go repositories generally."

// ClaimTemplateExample is the only claim grammar, with placeholders in every
// field a later content-addressed report must fill. The checker accepts this
// example and the same grammar with appropriately shaped concrete values.
const ClaimTemplateExample = "On the N answerable English questions in the frozen Cobra dataset at commit <sha>, graphi task_context/2 at a 1,200-token budget used a median X% fewer <tokenizer_id> tokens than deterministic GrepRead/<version> to reach the same grade-3 span recall (paired 95% <method> interval [L, U]); on the answerable sealed holdout questions the recorded raters answered Y/N correctly from the bundle alone. " + RequiredClaimLimitation

// ClaimViolation is one executable scope rule a candidate sentence broke.
type ClaimViolation struct {
	Rule  string
	Match string
}

var claimPatterns = []struct {
	rule string
	re   *regexp.Regexp
}{
	{"comparator_scope", regexp.MustCompile(`(?i)\bbm[[:space:]-]*25\b`)},
	{"comparator_scope", regexp.MustCompile(`(?i)\bcoir\b`)},
	{"comparator_scope", regexp.MustCompile(`(?i)\blexical(?:[[:space:]-]+(?:baseline|search|retriev[a-z]*|rank[a-z]*|compar[a-z]*))?\b`)},
	{"comparator_scope", regexp.MustCompile(`(?i)\b(?:keyword|sparse)[[:space:]-]+(?:baseline|search|retriev[a-z]*|rank[a-z]*)\b`)},
	// The permitted sentence has no need to name an implementation family.
	// Rejecting these nouns is a fail-closed way to prevent a comparative
	// adjective from laundering semantic superiority by paraphrase.
	{"semantic_superiority", regexp.MustCompile(`(?i)\b(?:semantic|embedding|vector[[:space:]-]+(?:search|retriev[a-z]*|rank[a-z]*))\b`)},
	{"population_scope", regexp.MustCompile(`(?i)\b(?:cross|multi)[[:space:]-]+(?:repository|repo|codebase)`)},
	{"population_scope", regexp.MustCompile(`(?i)\bacross\b[^.;]{0,48}\b(?:repositories|repos|codebases|projects)\b`)},
	{"population_scope", regexp.MustCompile(`(?i)\b(?:all|any|general|generally)[[:space:]-]+(?:go[[:space:]-]+)?(?:repositories|repos|codebases|projects|code)\b`)},
	{"population_scope", regexp.MustCompile(`(?i)\bgo[[:space:]-]+(?:repositories|repos|codebases|projects)\b`)},
	{"population_scope", regexp.MustCompile(`(?i)\bgenerali[sz](?:e|es|ed|ing|ation)\b`)},
}

var allowedClaimShape = regexp.MustCompile(
	`^On the (?:N|[1-9][0-9]*) answerable English questions in the frozen Cobra dataset at commit (?:<sha>|` + "`[0-9a-f]{40}`" +
		`), graphi ` + "`?task_context/2`?" + ` at a 1,200-token budget used a median (?:X|-?[0-9]+(?:\.[0-9])?)% fewer ` +
		`(?:<tokenizer_id>|` + "`?[A-Za-z0-9][A-Za-z0-9:._@/+={}~-]*`?" + `) tokens than deterministic ` +
		`(?:GrepRead/<version>|` + "`?GrepRead/[A-Za-z0-9][A-Za-z0-9:._@/+={}~-]*`?" + `) to reach the same grade-3 span recall ` +
		`\(paired 95% (?:<method>|[a-z][a-z0-9 -]*) interval \[(?:L|-?[0-9]+(?:\.[0-9])?), (?:U|-?[0-9]+(?:\.[0-9])?)\]\); ` +
		`on the answerable sealed holdout questions the recorded raters answered (?:Y/N|[0-9]+/[1-9][0-9]*) correctly from the bundle alone\. ` +
		regexp.QuoteMeta(RequiredClaimLimitation) + `$`,
)

// CheckClaimSentence is the executable claim-scope contract. It validates
// wording only; it neither generates a sentence nor accepts a report.
func CheckClaimSentence(sentence string) error {
	trimmed := strings.TrimSpace(sentence)
	var violations []ClaimViolation
	if trimmed == "" {
		violations = append(violations, ClaimViolation{Rule: "claim_shape", Match: "empty candidate"})
	}
	if !allowedClaimShape.MatchString(trimmed) {
		violations = append(violations, ClaimViolation{Rule: "claim_shape", Match: "not the frozen descriptive template"})
	}
	if !strings.Contains(trimmed, "frozen Cobra dataset") {
		violations = append(violations, ClaimViolation{Rule: "population_scope", Match: "missing frozen Cobra dataset scope"})
	}
	withoutApprovedLimitation := trimmed
	if strings.Contains(trimmed, RequiredClaimLimitation) {
		withoutApprovedLimitation = strings.ReplaceAll(withoutApprovedLimitation, RequiredClaimLimitation, "")
	} else {
		violations = append(violations, ClaimViolation{Rule: "population_scope", Match: "missing required pinned-Cobra limitation"})
	}
	for _, pattern := range claimPatterns {
		for _, match := range pattern.re.FindAllString(withoutApprovedLimitation, -1) {
			violations = append(violations, ClaimViolation{Rule: pattern.rule, Match: match})
		}
	}
	if len(violations) == 0 {
		return nil
	}
	// Pattern order is deliberate, but sorting makes the output stable if a
	// future rule is inserted without considering the detection traversal.
	sort.SliceStable(violations, func(i, j int) bool {
		if violations[i].Rule == violations[j].Rule {
			return strings.ToLower(violations[i].Match) < strings.ToLower(violations[j].Match)
		}
		return violations[i].Rule < violations[j].Rule
	})
	parts := make([]string, 0, len(violations))
	seen := map[string]bool{}
	for _, violation := range violations {
		part := fmt.Sprintf("%s (%q)", violation.Rule, violation.Match)
		key := strings.ToLower(part)
		if !seen[key] {
			parts = append(parts, part)
			seen[key] = true
		}
	}
	return fmt.Errorf("retrieval claim rejected: %s", strings.Join(parts, "; "))
}
