package retrieval

import (
	"reflect"
	"strings"
	"testing"

	compactv9 "github.com/samibel/graphi/engine/agenttools/taskctx/compact/v9"
)

const testVocabularySHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestMeasurementContract_IsFrozen(t *testing.T) {
	contract := FrozenMeasurementContract()
	if contract.Version != MeasurementContractVersion || contract.CandidateMethod != "task_context/2" || contract.CandidateTokenBudget != 1200 {
		t.Fatalf("candidate contract = %+v", contract)
	}
	if contract.ComparatorMethod != "GrepRead" || contract.ComparatorReadWindowLines != 40 {
		t.Fatalf("comparator contract = %+v", contract)
	}
	if contract.RelevantGrade != 3 || contract.DisplayDecimalPlaces != 1 {
		t.Fatalf("grade/resolution contract = %+v", contract)
	}
	if err := ValidateMeasurementContract(contract); err != nil {
		t.Fatalf("frozen contract rejected: %v", err)
	}

	drifted := contract
	drifted.ComparatorReadWindowLines++
	if err := ValidateMeasurementContract(drifted); err == nil {
		t.Fatal("method drift under the frozen version was accepted")
	}
}

func TestSecondResponseMeasurementContract_IsFrozen(t *testing.T) {
	want := MeasurementContract{
		Version:                   MeasurementContractVersion2,
		CandidateMethod:           SavingsCandidateMethod,
		CandidateTokenBudget:      SavingsCandidateBudget,
		ComparatorMethod:          SavingsComparatorMethod,
		ComparatorReadWindowLines: GrepReadWindowLines,
		RelevantGrade:             SavingsGrade,
		MissRule: "a miss is a right-censored observation at the complete preserved transcript token count; " +
			"tokens-to-target and paired percent saving are undefined, the miss count is reported, and any magnitude aggregate fails validation",
		PayloadRule:          "count only complete response byte slices captured below final serialization; the candidate transcript is one complete task_context/2 response plus at most one designated follow-up read, preserved as one newline-terminated JSON source line under operation task_context/2-followup-read/1; preserve each slice and sha256, and recompute every byte and token count from those bytes",
		EqualRecallRule:      "compare the earliest indivisible response prefix reaching the same predeclared rational grade-3 span-recall target; charge slice 1 alone when it reaches, otherwise charge slices 1 and 2 together; censor a miss at both slices, or at slice 1 when no follow-up is designated; whole spans only, ties contribute zero",
		FollowupMaxLines:     compactv9.FollowupMaxLines,
		FollowupOperation:    PayloadOperationFollowupRead,
		Confidence:           FrozenConfidenceSpec(),
		DisplayDecimalPlaces: SavingsDisplayDecimalPlaces,
	}
	got := SecondResponseMeasurementContract()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("second-response contract drifted\ngot:  %+v\nwant: %+v", got, want)
	}
	if err := ValidateMeasurementContract(got); err != nil {
		t.Fatalf("second-response contract rejected: %v", err)
	}
}

func TestValidateMeasurementContract_RefusesPartialVersionCombinations(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  MeasurementContract
	}{
		{"version one with the line cap", func() MeasurementContract {
			got := FrozenMeasurementContract()
			got.FollowupMaxLines = compactv9.FollowupMaxLines
			return got
		}()},
		{"version one with the follow-up operation", func() MeasurementContract {
			got := FrozenMeasurementContract()
			got.FollowupOperation = PayloadOperationFollowupRead
			return got
		}()},
		{"version two name on the version one literal", func() MeasurementContract {
			got := FrozenMeasurementContract()
			got.Version = MeasurementContractVersion2
			return got
		}()},
		{"version two without the line cap", func() MeasurementContract {
			got := SecondResponseMeasurementContract()
			got.FollowupMaxLines = 0
			return got
		}()},
		{"version two with the wrong operation", func() MeasurementContract {
			got := SecondResponseMeasurementContract()
			got.FollowupOperation = PayloadOperationRead
			return got
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateMeasurementContract(tc.got)
			if err == nil || !strings.Contains(err.Error(), tc.got.Version) {
				t.Fatalf("error = %v, want refusal naming version %q", err, tc.got.Version)
			}
		})
	}
}

func TestValidateSavingsAggregateInput_RequiresExactConfidenceMethod(t *testing.T) {
	in, counters := validSavingsAggregateInput(t)
	if err := ValidateConfidenceSpec(in.Confidence); err != nil {
		t.Fatalf("frozen confidence method rejected: %v", err)
	}

	mutations := []struct {
		name string
		edit func(*ConfidenceSpec)
	}{
		{"estimator", func(c *ConfidenceSpec) { c.Estimator = "median" }},
		{"interval type", func(c *ConfidenceSpec) { c.IntervalType = "confidence" }},
		{"level", func(c *ConfidenceSpec) { c.LevelBasisPoints = 9000 }},
		{"population", func(c *ConfidenceSpec) { c.Population = "Go repositories" }},
		{"resampling unit", func(c *ConfidenceSpec) { c.ResamplingUnit = "query" }},
		{"stratification", func(c *ConfidenceSpec) { c.Stratification = "none" }},
		{"replicates", func(c *ConfidenceSpec) { c.Replicates-- }},
		{"seed", func(c *ConfidenceSpec) { c.SeedMethod = "time" }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			got := in
			got.Confidence = in.Confidence
			mutation.edit(&got.Confidence)
			if err := ValidateSavingsAggregateInput(got, counters); err == nil || !strings.Contains(err.Error(), "confidence must be estimator") {
				t.Fatalf("error = %v, want exact confidence-method rejection", err)
			}
		})
	}
}

func TestValidateSavingsAggregateInput_RejectsCompleteCaseSubsetAndMisses(t *testing.T) {
	in, counters := validSavingsAggregateInput(t)
	if err := ValidateSavingsAggregateInput(in, counters); err != nil {
		t.Fatalf("valid complete population rejected: %v", err)
	}

	t.Run("dropping a miss as a complete-case subset fails", func(t *testing.T) {
		got := in
		got.Observations = append([]SavingsObservation(nil), in.Observations[:1]...)
		err := ValidateSavingsAggregateInput(got, counters)
		if err == nil || !strings.Contains(err.Error(), "complete-case aggregation is forbidden") {
			t.Fatalf("error = %v, want complete-case rejection", err)
		}
	})

	t.Run("candidate miss is right-censored and fails magnitude aggregation", func(t *testing.T) {
		got := in
		got.Observations = cloneSavingsObservations(in.Observations)
		arm := &got.Observations[1].Candidate
		arm.Status = SavingsOutcomeMissed
		arm.Grade3SpansAtPrefix = 0
		arm.ConsumedPayloadSlices = len(arm.Payloads)
		arm.TokensToTarget = nil
		bound := payloadTokens(arm.Payloads, in.TokenizerID)
		arm.CensorLowerBoundTokens = &bound
		err := ValidateSavingsAggregateInput(got, counters)
		if err == nil || !strings.Contains(err.Error(), "right-censored") || !strings.Contains(err.Error(), "complete-case aggregation is forbidden") {
			t.Fatalf("error = %v, want censored-population rejection", err)
		}
	})

	t.Run("a miss cannot invent tokens-to-target", func(t *testing.T) {
		got := in
		got.Observations = cloneSavingsObservations(in.Observations)
		arm := &got.Observations[0].GrepRead
		arm.Status = SavingsOutcomeMissed
		arm.Grade3SpansAtPrefix = 0
		arm.ConsumedPayloadSlices = len(arm.Payloads)
		bound := payloadTokens(arm.Payloads, in.TokenizerID)
		arm.CensorLowerBoundTokens = &bound
		err := ValidateSavingsAggregateInput(got, counters)
		if err == nil || !strings.Contains(err.Error(), "tokens-to-target is undefined on a miss") {
			t.Fatalf("error = %v, want undefined-on-miss rejection", err)
		}
	})
}

func TestValidateSavingsAggregateInput_RejectsReconstructedPayloadsAndCountDrift(t *testing.T) {
	in, counters := validSavingsAggregateInput(t)

	t.Run("preserved bytes are mandatory", func(t *testing.T) {
		got := in
		got.Observations = cloneSavingsObservations(in.Observations)
		got.Observations[0].Candidate.Payloads[0].Bytes = nil
		err := ValidateSavingsAggregateInput(got, counters)
		if err == nil || !strings.Contains(err.Error(), "reconstructed or estimated counts are forbidden") {
			t.Fatalf("error = %v, want reconstructed-count rejection", err)
		}
	})

	t.Run("JSON envelope or escaping mutation moves digest and count", func(t *testing.T) {
		before := in.Observations[0].Candidate.Payloads[0]
		afterBytes := []byte(`{"jsonrpc":"2.0","id":1,"result":{"text":"answer\\nline"}}` + "\n")
		after := preservedPayload(1, PayloadBoundaryCandidate, PayloadOperationTaskContext, afterBytes, in.TokenizerID)
		if before.SHA256 == after.SHA256 || before.ByteCount == after.ByteCount || payloadToken(after, in.TokenizerID) == payloadToken(before, in.TokenizerID) {
			t.Fatalf("serialized-byte mutation did not move every derived identity/count\nbefore=%+v\nafter=%+v", before, after)
		}
	})

	t.Run("digest is recomputed from bytes", func(t *testing.T) {
		got := in
		got.Observations = cloneSavingsObservations(in.Observations)
		got.Observations[0].Candidate.Payloads[0].Bytes[0] = '['
		err := ValidateSavingsAggregateInput(got, counters)
		if err == nil || !strings.Contains(err.Error(), "sha256 does not match") {
			t.Fatalf("error = %v, want digest mismatch", err)
		}
	})

	t.Run("byte count is recomputed", func(t *testing.T) {
		got := in
		got.Observations = cloneSavingsObservations(in.Observations)
		got.Observations[0].Candidate.Payloads[0].ByteCount++
		err := ValidateSavingsAggregateInput(got, counters)
		if err == nil || !strings.Contains(err.Error(), "byte_count") {
			t.Fatalf("error = %v, want byte-count mismatch", err)
		}
	})

	t.Run("token count is recomputed", func(t *testing.T) {
		got := in
		got.Observations = cloneSavingsObservations(in.Observations)
		got.Observations[0].Candidate.Payloads[0].TokenCounts[1].Tokens++
		err := ValidateSavingsAggregateInput(got, counters)
		if err == nil || !strings.Contains(err.Error(), "recomputed") {
			t.Fatalf("error = %v, want token-count mismatch", err)
		}
	})

	t.Run("a missing executable tokenizer is not an estimate path", func(t *testing.T) {
		gotCounters := map[string]PayloadCounter{TokenizerID: counters[TokenizerID]}
		err := ValidateSavingsAggregateInput(in, gotCounters)
		if err == nil || !strings.Contains(err.Error(), "no executable counter") || !strings.Contains(err.Error(), "estimated counts are forbidden") {
			t.Fatalf("error = %v, want missing-counter rejection", err)
		}
	})
}

func TestValidateSavingsAggregateInput_EnforcesEqualRecallAndPopulation(t *testing.T) {
	in, counters := validSavingsAggregateInput(t)

	t.Run("arms cannot use different recall targets", func(t *testing.T) {
		got := in
		got.Observations = cloneSavingsObservations(in.Observations)
		got.Observations[0].Candidate.Target = RecallTarget{Grade: 3, RequiredSpans: 1, TotalSpans: 2}
		err := ValidateSavingsAggregateInput(got, counters)
		if err == nil || !strings.Contains(err.Error(), "predeclared equal-recall target") {
			t.Fatalf("error = %v, want unequal-recall rejection", err)
		}
	})

	t.Run("fractional credit cannot replace a whole grade-3 span", func(t *testing.T) {
		got := in
		got.Population = append([]SavingsPopulationMember(nil), in.Population...)
		got.Observations = cloneSavingsObservations(in.Observations)
		got.Population[0].Target = RecallTarget{Grade: 3, RequiredSpans: 2, TotalSpans: 2}
		got.Observations[0].Candidate.Target = got.Population[0].Target
		got.Observations[0].GrepRead.Target = got.Population[0].Target
		err := ValidateSavingsAggregateInput(got, counters)
		if err == nil || !strings.Contains(err.Error(), "only 1/2 required grade-3 spans") {
			t.Fatalf("error = %v, want whole-span rejection", err)
		}
	})

	t.Run("no_hit queries stay outside the savings population", func(t *testing.T) {
		got := in
		got.Population = append([]SavingsPopulationMember(nil), in.Population...)
		got.Population[0].Stratum = StratumNoHit
		err := ValidateSavingsAggregateInput(got, counters)
		if err == nil || !strings.Contains(err.Error(), "cannot enter the answerable savings population") {
			t.Fatalf("error = %v, want no_hit rejection", err)
		}
	})

	t.Run("query families cannot cross dev and holdout", func(t *testing.T) {
		got := in
		got.Population = append([]SavingsPopulationMember(nil), in.Population...)
		got.Population[1].FamilyID = got.Population[0].FamilyID
		err := ValidateSavingsAggregateInput(got, counters)
		if err == nil || !strings.Contains(err.Error(), "crosses splits") {
			t.Fatalf("error = %v, want family split rejection", err)
		}
	})

	t.Run("observation order is part of the frozen population", func(t *testing.T) {
		got := in
		got.Observations = cloneSavingsObservations(in.Observations)
		got.Observations[0], got.Observations[1] = got.Observations[1], got.Observations[0]
		err := ValidateSavingsAggregateInput(got, counters)
		if err == nil || !strings.Contains(err.Error(), "frozen population order") {
			t.Fatalf("error = %v, want order rejection", err)
		}
	})
}

func TestValidateSavingsAggregateInput_SecondResponseCandidateShapes(t *testing.T) {
	base, counters := validSavingsAggregateInput(t)
	followup := preservedPayload(2, PayloadBoundaryCandidate, PayloadOperationFollowupRead,
		[]byte(`{"path":"answer.go","start_line":1,"end_line":2,"text":"the answer"}`+"\n"), base.TokenizerID)

	withTwoSlices := func(contract MeasurementContract) SavingsAggregateInput {
		got := base
		got.Contract = contract
		got.Observations = cloneSavingsObservations(base.Observations)
		arm := &got.Observations[0].Candidate
		arm.Payloads = append(arm.Payloads, followup)
		arm.StopReason = SavingsStopFollowupReadComplete
		arm.ConsumedPayloadSlices = 2
		tokens := payloadTokens(arm.Payloads, got.TokenizerID)
		arm.TokensToTarget = &tokens
		return got
	}

	t.Run("contract one refuses a two-payload candidate", func(t *testing.T) {
		err := ValidateSavingsAggregateInput(withTwoSlices(FrozenMeasurementContract()), counters)
		if err == nil || !strings.Contains(err.Error(), MeasurementContractVersion) {
			t.Fatalf("error = %v, want a refusal naming contract version 1", err)
		}
	})

	t.Run("contract two accepts one payload", func(t *testing.T) {
		got := base
		got.Contract = SecondResponseMeasurementContract()
		if err := ValidateSavingsAggregateInput(got, counters); err != nil {
			t.Fatalf("one-payload contract-2 candidate: %v", err)
		}
	})

	t.Run("contract two accepts a completed follow-up read", func(t *testing.T) {
		if err := ValidateSavingsAggregateInput(withTwoSlices(SecondResponseMeasurementContract()), counters); err != nil {
			t.Fatalf("two-payload contract-2 candidate: %v", err)
		}
	})

	t.Run("contract two charges the earliest reaching prefix", func(t *testing.T) {
		got := withTwoSlices(SecondResponseMeasurementContract())
		arm := &got.Observations[0].Candidate
		arm.StopReason = SavingsStopOneCallComplete
		arm.ConsumedPayloadSlices = 1
		tokens := payloadToken(arm.Payloads[0], got.TokenizerID)
		arm.TokensToTarget = &tokens
		if err := ValidateSavingsAggregateInput(got, counters); err != nil {
			t.Fatalf("earliest-prefix contract-2 candidate: %v", err)
		}
	})

	t.Run("contract two censors a miss at both slices", func(t *testing.T) {
		got := withTwoSlices(SecondResponseMeasurementContract())
		arm := &got.Observations[0].Candidate
		arm.Status = SavingsOutcomeMissed
		arm.Grade3SpansAtPrefix = 0
		arm.TokensToTarget = nil
		bound := payloadTokens(arm.Payloads, got.TokenizerID)
		arm.CensorLowerBoundTokens = &bound
		err := ValidateSavingsAggregateInput(got, counters)
		if err == nil || !strings.Contains(err.Error(), "right-censored") {
			t.Fatalf("error = %v, want the aggregate's post-validation censor refusal", err)
		}
	})

	t.Run("contract two refuses the wrong follow-up operation", func(t *testing.T) {
		got := withTwoSlices(SecondResponseMeasurementContract())
		got.Observations[0].Candidate.Payloads[1].Operation = PayloadOperationRead
		err := ValidateSavingsAggregateInput(got, counters)
		if err == nil || !strings.Contains(err.Error(), PayloadOperationFollowupRead) {
			t.Fatalf("error = %v, want follow-up operation refusal", err)
		}
	})

	t.Run("contract two refuses the wrong follow-up sequence", func(t *testing.T) {
		got := withTwoSlices(SecondResponseMeasurementContract())
		got.Observations[0].Candidate.Payloads[1].Sequence = 3
		err := ValidateSavingsAggregateInput(got, counters)
		if err == nil || !strings.Contains(err.Error(), "sequence=3, want 2") {
			t.Fatalf("error = %v, want follow-up sequence refusal", err)
		}
	})

	t.Run("contract two refuses a follow-up stop with one payload", func(t *testing.T) {
		got := base
		got.Contract = SecondResponseMeasurementContract()
		got.Observations = cloneSavingsObservations(base.Observations)
		got.Observations[0].Candidate.StopReason = SavingsStopFollowupReadComplete
		err := ValidateSavingsAggregateInput(got, counters)
		if err == nil || !strings.Contains(err.Error(), "one payload") {
			t.Fatalf("error = %v, want incomplete follow-up stop refusal", err)
		}
	})
}

func TestCheckClaimSentence_RejectsForbiddenScope(t *testing.T) {
	if err := CheckClaimSentence(ClaimTemplateExample); err != nil {
		t.Fatalf("narrow descriptive candidate rejected: %v", err)
	}

	cases := []struct {
		name string
		text string
		rule string
	}{
		{"BM25", "BM25 used more tokens on the frozen Cobra dataset. " + RequiredClaimLimitation, "comparator_scope"},
		{"CoIR", "The frozen Cobra dataset reproduces CoIR. " + RequiredClaimLimitation, "comparator_scope"},
		{"lexical baseline", "The lexical baseline lost on the frozen Cobra dataset. " + RequiredClaimLimitation, "comparator_scope"},
		{"keyword paraphrase", "Keyword retrieval lost on the frozen Cobra dataset. " + RequiredClaimLimitation, "comparator_scope"},
		{"semantic superiority", "Semantic search wins on the frozen Cobra dataset. " + RequiredClaimLimitation, "semantic_superiority"},
		{"embedding paraphrase", "Embedding retrieval is better on the frozen Cobra dataset. " + RequiredClaimLimitation, "semantic_superiority"},
		{"cross repository", "The frozen Cobra dataset proves performance across repositories. " + RequiredClaimLimitation, "population_scope"},
		{"general Go", "This works for all Go projects on the frozen Cobra dataset. " + RequiredClaimLimitation, "population_scope"},
		{"generalizes", "The frozen Cobra dataset result generalizes. " + RequiredClaimLimitation, "population_scope"},
		{"implied comparison outside template", "The frozen Cobra dataset result dominates traditional code search. " + RequiredClaimLimitation, "claim_shape"},
		{"missing limitation", "On the frozen Cobra dataset, task_context/2 used fewer tokens than GrepRead.", "population_scope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckClaimSentence(tc.text)
			if err == nil || !strings.Contains(err.Error(), tc.rule) {
				t.Fatalf("error = %v, want %s rejection", err, tc.rule)
			}
		})
	}
}

func validSavingsAggregateInput(t *testing.T) (SavingsAggregateInput, map[string]PayloadCounter) {
	t.Helper()
	target := RecallTarget{Grade: 3, RequiredSpans: 1, TotalSpans: 1}
	in := SavingsAggregateInput{
		Contract:                  FrozenMeasurementContract(),
		DatasetSHA256:             "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		RepoSHA:                   "cccccccccccccccccccccccccccccccccccccccc",
		CandidateVersion:          "candidate/test",
		ComparatorVersion:         "grepread/test",
		TokenizerID:               "fixture-real-tokenizer",
		TokenizerVocabularySHA256: testVocabularySHA,
		Confidence:                FrozenConfidenceSpec(),
		Population: []SavingsPopulationMember{
			{QueryID: "q-dev", FamilyID: "family-dev", Split: SplitDev, Stratum: StratumNLBehaviour, Target: target},
			{QueryID: "q-holdout", FamilyID: "family-holdout", Split: SplitHoldout, Stratum: StratumArchitectureFlow, Target: target},
		},
	}
	for _, member := range in.Population {
		candidatePayload := preservedPayload(1, PayloadBoundaryCandidate, PayloadOperationTaskContext,
			[]byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"text\":\"answer\"}}\n"), in.TokenizerID)
		grepPayload := preservedPayload(1, PayloadBoundaryGrepRead, PayloadOperationGrep,
			[]byte("auth/token.go:40: ValidateToken\n"), in.TokenizerID)
		readPayload := preservedPayload(2, PayloadBoundaryGrepRead, PayloadOperationRead,
			[]byte("func ValidateToken(raw string) error { return nil }\n"), in.TokenizerID)
		candidateTokens := payloadToken(candidatePayload, in.TokenizerID)
		baselineTokens := payloadToken(grepPayload, in.TokenizerID) + payloadToken(readPayload, in.TokenizerID)
		in.Observations = append(in.Observations, SavingsObservation{
			QueryID: member.QueryID,
			Candidate: SavingsArmOutcome{
				Target: member.Target, Status: SavingsOutcomeReached, StopReason: SavingsStopOneCallComplete,
				Grade3SpansAtPrefix: 1, ConsumedPayloadSlices: 1, TokensToTarget: intPointer(candidateTokens),
				Payloads: []PreservedPayload{candidatePayload},
			},
			GrepRead: SavingsArmOutcome{
				Target: member.Target, Status: SavingsOutcomeReached, StopReason: SavingsStopExhausted,
				Grade3SpansAtPrefix: 1, ConsumedPayloadSlices: 2, TokensToTarget: intPointer(baselineTokens),
				Payloads: []PreservedPayload{grepPayload, readPayload},
			},
		})
	}
	counters := map[string]PayloadCounter{
		TokenizerID: {
			TokenizerID: TokenizerID,
			Count:       func(raw []byte) (int, error) { return len(strings.Fields(string(raw))), nil },
		},
		in.TokenizerID: {
			TokenizerID: in.TokenizerID, VocabularySHA256: in.TokenizerVocabularySHA256,
			Count: func(raw []byte) (int, error) { return len(raw), nil },
		},
	}
	return in, counters
}

func preservedPayload(sequence int, boundary PayloadBoundary, operation string, raw []byte, realTokenizer string) PreservedPayload {
	return PreservedPayload{
		Sequence: sequence, Boundary: boundary, Operation: operation,
		Bytes: append([]byte(nil), raw...), SHA256: SHA256Hex(raw), ByteCount: len(raw),
		TokenCounts: []PayloadTokenCount{
			{TokenizerID: TokenizerID, Tokens: len(strings.Fields(string(raw)))},
			{TokenizerID: realTokenizer, VocabularySHA256: testVocabularySHA, Tokens: len(raw)},
		},
	}
}

func payloadToken(payload PreservedPayload, tokenizerID string) int {
	for _, count := range payload.TokenCounts {
		if count.TokenizerID == tokenizerID {
			return count.Tokens
		}
	}
	return 0
}

func payloadTokens(payloads []PreservedPayload, tokenizerID string) int {
	total := 0
	for _, payload := range payloads {
		total += payloadToken(payload, tokenizerID)
	}
	return total
}

func intPointer(v int) *int { return &v }

func cloneSavingsObservations(in []SavingsObservation) []SavingsObservation {
	out := append([]SavingsObservation(nil), in...)
	for i := range out {
		out[i].Candidate.Payloads = clonePayloads(in[i].Candidate.Payloads)
		out[i].GrepRead.Payloads = clonePayloads(in[i].GrepRead.Payloads)
		if in[i].Candidate.TokensToTarget != nil {
			out[i].Candidate.TokensToTarget = intPointer(*in[i].Candidate.TokensToTarget)
		}
		if in[i].Candidate.CensorLowerBoundTokens != nil {
			out[i].Candidate.CensorLowerBoundTokens = intPointer(*in[i].Candidate.CensorLowerBoundTokens)
		}
		if in[i].GrepRead.TokensToTarget != nil {
			out[i].GrepRead.TokensToTarget = intPointer(*in[i].GrepRead.TokensToTarget)
		}
		if in[i].GrepRead.CensorLowerBoundTokens != nil {
			out[i].GrepRead.CensorLowerBoundTokens = intPointer(*in[i].GrepRead.CensorLowerBoundTokens)
		}
	}
	return out
}

func clonePayloads(in []PreservedPayload) []PreservedPayload {
	out := append([]PreservedPayload(nil), in...)
	for i := range out {
		out[i].Bytes = append([]byte(nil), in[i].Bytes...)
		out[i].TokenCounts = append([]PayloadTokenCount(nil), in[i].TokenCounts...)
	}
	return out
}
