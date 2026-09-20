package retrieval

import (
	"bytes"
	"encoding/json"
	"math/big"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type aggregateValueFixture struct {
	queryID  string
	familyID string
	split    string
	c, g     int
}

func TestComputeSavingsAggregate_ExactOddAndEvenMedians(t *testing.T) {
	tests := []struct {
		name   string
		values []aggregateValueFixture
		want   SavingsExactRational
	}{
		{
			name: "odd observed order statistic",
			values: []aggregateValueFixture{
				{queryID: "d1", familyID: "df1", split: SplitDev, c: 90, g: 100},
				{queryID: "d2", familyID: "df2", split: SplitDev, c: 70, g: 100},
				{queryID: "h1", familyID: "hf1", split: SplitHoldout, c: 80, g: 100},
			},
			want: SavingsExactRational{Numerator: "20", Denominator: "1"},
		},
		{
			name: "even arithmetic mean of central values",
			values: []aggregateValueFixture{
				{queryID: "d1", familyID: "df1", split: SplitDev, c: 90, g: 100},
				{queryID: "d2", familyID: "df2", split: SplitDev, c: 80, g: 100},
				{queryID: "h1", familyID: "hf1", split: SplitHoldout, c: 70, g: 100},
				{queryID: "h2", familyID: "hf2", split: SplitHoldout, c: 60, g: 100},
			},
			want: SavingsExactRational{Numerator: "25", Denominator: "1"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in, counters := savingsAggregateFixture(t, tc.values)
			got, err := ComputeSavingsAggregate(in, counters)
			if err != nil {
				t.Fatalf("ComputeSavingsAggregate: %v", err)
			}
			if got.Median != tc.want {
				t.Fatalf("median = %+v, want %+v", got.Median, tc.want)
			}
			if got.N != len(tc.values) || got.FamilyCount != len(tc.values) {
				t.Fatalf("counts = N %d families %d, want %d/%d", got.N, got.FamilyCount, len(tc.values), len(tc.values))
			}
		})
	}
}

func TestComputeSavingsAggregate_RetainsZeroTies(t *testing.T) {
	in, counters := savingsAggregateFixture(t, []aggregateValueFixture{
		{queryID: "d-neg", familyID: "df1", split: SplitDev, c: 110, g: 100},
		{queryID: "d-zero", familyID: "df2", split: SplitDev, c: 100, g: 100},
		{queryID: "h-zero", familyID: "hf1", split: SplitHoldout, c: 100, g: 100},
		{queryID: "h-pos", familyID: "hf2", split: SplitHoldout, c: 90, g: 100},
	})
	got, err := ComputeSavingsAggregate(in, counters)
	if err != nil {
		t.Fatal(err)
	}
	if got.Median != (SavingsExactRational{Numerator: "0", Denominator: "1"}) {
		t.Fatalf("median = %+v, want exact zero", got.Median)
	}
	zeroes := 0
	for _, value := range got.QueryValues {
		if value.PercentSavings == (SavingsExactRational{Numerator: "0", Denominator: "1"}) {
			zeroes++
		}
	}
	if zeroes != 2 || len(got.QueryValues) != 4 {
		t.Fatalf("retained zero ties = %d in %d values, want 2 in 4", zeroes, len(got.QueryValues))
	}
	display, err := RenderSavingsAggregate(got, counters)
	if err != nil {
		t.Fatal(err)
	}
	if display.MedianPercent != "0.0%" || display.N != 4 || display.FamilyCount != 4 {
		t.Fatalf("display = %+v, want an explicitly retained 0.0%% tie over N=4/families=4", display)
	}
}

func TestComputeSavingsAggregate_ResamplesWholeFamilies(t *testing.T) {
	in, counters := savingsAggregateFixture(t, []aggregateValueFixture{
		{queryID: "d-a1", familyID: "dev-a", split: SplitDev, c: 200, g: 100},
		{queryID: "d-a2", familyID: "dev-a", split: SplitDev, c: 200, g: 100},
		{queryID: "d-b", familyID: "dev-b", split: SplitDev, c: 0, g: 100},
		{queryID: "h-fixed", familyID: "holdout-a", split: SplitHoldout, c: 100, g: 100},
	})
	got, err := ComputeSavingsAggregate(in, counters)
	if err != nil {
		t.Fatal(err)
	}
	wantUniverse := map[SavingsExactRational]bool{
		{Numerator: "-100", Denominator: "1"}: true,
		{Numerator: "-50", Denominator: "1"}:  true,
		{Numerator: "100", Denominator: "1"}:  true,
	}
	seen := make(map[SavingsExactRational]bool)
	for _, median := range got.Bootstrap.ReplicateMedians {
		if !wantUniverse[median] {
			t.Fatalf("replicate median %+v cannot result from whole-family draws", median)
		}
		seen[median] = true
	}
	if !reflect.DeepEqual(seen, wantUniverse) {
		t.Fatalf("replicate median universe = %v, want %v", seen, wantUniverse)
	}
	if got.Bootstrap.DrawCount != SavingsBootstrapReplicates*3 {
		t.Fatalf("draw count = %d, want %d", got.Bootstrap.DrawCount, SavingsBootstrapReplicates*3)
	}
}

func TestComputeSavingsAggregate_StratifiesDevAndHoldout(t *testing.T) {
	in, counters := savingsAggregateFixture(t, []aggregateValueFixture{
		{queryID: "d-fixed", familyID: "dev-only", split: SplitDev, c: 200, g: 100},
		{queryID: "h-a", familyID: "holdout-a", split: SplitHoldout, c: 100, g: 100},
		{queryID: "h-b", familyID: "holdout-b", split: SplitHoldout, c: 0, g: 100},
	})
	got, err := ComputeSavingsAggregate(in, counters)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Splits) != 2 || got.Splits[0].Split != SplitDev || got.Splits[1].Split != SplitHoldout {
		t.Fatalf("split frames = %+v, want fixed dev then holdout order", got.Splits)
	}
	if got.Splits[0].N != 1 || got.Splits[0].FamilyCount != 1 || got.Splits[1].N != 2 || got.Splits[1].FamilyCount != 2 {
		t.Fatalf("split counts = %+v", got.Splits)
	}
	wantUniverse := map[SavingsExactRational]bool{
		{Numerator: "0", Denominator: "1"}:   true,
		{Numerator: "100", Denominator: "1"}: true,
	}
	seen := map[SavingsExactRational]bool{}
	for _, median := range got.Bootstrap.ReplicateMedians {
		if !wantUniverse[median] {
			t.Fatalf("median %+v proves split family counts were not retained", median)
		}
		seen[median] = true
	}
	if !reflect.DeepEqual(seen, wantUniverse) {
		t.Fatalf("stratified median universe = %v, want %v", seen, wantUniverse)
	}
}

func TestComputeSavingsAggregate_PinsReplicatesRanksAndDeterministicBytes(t *testing.T) {
	in, counters := savingsAggregateFixture(t, []aggregateValueFixture{
		{queryID: "d-z", familyID: "z-family", split: SplitDev, c: 93, g: 100},
		{queryID: "d-a", familyID: "a-family", split: SplitDev, c: 81, g: 100},
		{queryID: "h-b", familyID: "b-family", split: SplitHoldout, c: 74, g: 100},
	})
	first, err := ComputeSavingsAggregate(in, counters)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ComputeSavingsAggregate(in, counters)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("identical inputs produced different aggregate bytes")
	}
	if first.SHA256 == "" || first.SHA256 != second.SHA256 {
		t.Fatalf("content addresses = %q and %q", first.SHA256, second.SHA256)
	}
	if first.SHA256 != "2f927f0ff53cb7951bb9ade0ecf1573ecdca60c43cb91e300edab7a2a5234715" {
		t.Fatalf("aggregate content address = %s; deterministic result bytes drifted", first.SHA256)
	}
	if first.Bootstrap.DrawsSHA256 != "bf803801e3e5798236a9a280f31f3ed11bd9c115a40c5c157603299a248a9ff5" {
		t.Fatalf("draw digest = %s; pinned sampler stream drifted", first.Bootstrap.DrawsSHA256)
	}
	if first.Bootstrap.ReplicateMediansSHA256 != "a56eb4b68ab9f77263105795e03a7813a221e4fe5abce9db989c5b0aa5ea4faa" {
		t.Fatalf("replicate digest = %s; exact bootstrap arithmetic drifted", first.Bootstrap.ReplicateMediansSHA256)
	}
	stableBytes := append([]byte(nil), firstJSON...)
	in.Population[0].FamilyID = "caller-mutated"
	in.Observations[0].Candidate.Payloads[0].Bytes[0] = 'z'
	afterCallerMutation, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stableBytes, afterCallerMutation) {
		t.Fatal("aggregate retained aliases into caller-owned input")
	}
	if first.Bootstrap.Replicates != 10000 || len(first.Bootstrap.ReplicateMedians) != 10000 {
		t.Fatalf("replicates = %d/%d, want 10000", first.Bootstrap.Replicates, len(first.Bootstrap.ReplicateMedians))
	}
	if first.Bootstrap.LowerNearestRank != 250 || first.Bootstrap.UpperNearestRank != 9750 {
		t.Fatalf("nearest ranks = %d/%d, want 250/9750", first.Bootstrap.LowerNearestRank, first.Bootstrap.UpperNearestRank)
	}
	if got := first.Splits[0].FamilyIDs; !reflect.DeepEqual(got, []string{"a-family", "z-family"}) {
		t.Fatalf("dev sampler family order = %v, want bytewise-sorted stable indices", got)
	}
	ordered := append([]SavingsExactRational(nil), first.Bootstrap.ReplicateMedians...)
	sort.Slice(ordered, func(i, j int) bool {
		left, _ := parseExactRational(ordered[i])
		right, _ := parseExactRational(ordered[j])
		return left.Cmp(right) < 0
	})
	if first.Bootstrap.Lower != ordered[249] || first.Bootstrap.Upper != ordered[9749] {
		t.Fatalf("interval = %+v/%+v, want nearest-rank values %+v/%+v", first.Bootstrap.Lower, first.Bootstrap.Upper, ordered[249], ordered[9749])
	}
	seed := SHA256Hex([]byte(in.DatasetSHA256 + "\n" + MeasurementContractVersion))
	if first.Bootstrap.SeedSHA256 != seed {
		t.Fatalf("seed = %s, want %s", first.Bootstrap.SeedSHA256, seed)
	}
	if err := ValidateSavingsAggregateResult(first, counters); err != nil {
		t.Fatalf("ValidateSavingsAggregateResult: %v", err)
	}
	corrupt := first
	corrupt.Bootstrap.Upper.Numerator = "999"
	address, err := ContentAddress(corrupt, func(v *SavingsAggregateResult) { v.SHA256 = "" })
	if err != nil {
		t.Fatal(err)
	}
	corrupt.SHA256 = address
	if err := ValidateSavingsAggregateResult(corrupt, counters); err == nil || !strings.Contains(err.Error(), "does not recompute") {
		t.Fatalf("self-consistent derived corruption error = %v", err)
	}
}

func TestComputeSavingsAggregate_RefusesMissAndIncompletePopulationBeforeMagnitude(t *testing.T) {
	in, counters := savingsAggregateFixture(t, []aggregateValueFixture{
		{queryID: "d", familyID: "dev-family", split: SplitDev, c: 90, g: 100},
		{queryID: "h", familyID: "holdout-family", split: SplitHoldout, c: 80, g: 100},
	})
	miss := &in.Observations[0].Candidate
	miss.Status = SavingsOutcomeMissed
	miss.Grade3SpansAtPrefix = 0
	miss.TokensToTarget = nil
	miss.CensorLowerBoundTokens = intPointer(payloadTokens(miss.Payloads, in.TokenizerID))
	if _, err := ComputeSavingsAggregate(in, counters); err == nil || !strings.Contains(err.Error(), "right-censored") {
		t.Fatalf("miss error = %v, want right-censored refusal", err)
	}

	in, counters = savingsAggregateFixture(t, []aggregateValueFixture{
		{queryID: "d", familyID: "dev-family", split: SplitDev, c: 90, g: 100},
		{queryID: "h", familyID: "holdout-family", split: SplitHoldout, c: 80, g: 100},
	})
	in.Observations = in.Observations[:1]
	if _, err := ComputeSavingsAggregate(in, counters); err == nil || !strings.Contains(err.Error(), "complete population required") {
		t.Fatalf("incomplete population error = %v, want complete-population refusal", err)
	}
}

func TestRenderSavingsAggregate_RoundsOnlyDisplayHalfAwayFromZero(t *testing.T) {
	for _, tc := range []struct {
		value SavingsExactRational
		want  string
	}{
		{SavingsExactRational{Numerator: "1", Denominator: "20"}, "0.1%"},
		{SavingsExactRational{Numerator: "-1", Denominator: "20"}, "-0.1%"},
		{SavingsExactRational{Numerator: "2469", Denominator: "200"}, "12.3%"},
	} {
		got, err := renderSavingsPercent(tc.value, 1)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("render %v = %q, want %q", tc.value, got, tc.want)
		}
	}
}

func savingsAggregateFixture(t *testing.T, values []aggregateValueFixture) (SavingsAggregateInput, map[string]PayloadCounter) {
	t.Helper()
	if len(values) == 0 {
		t.Fatal("fixture values must not be empty")
	}
	target := RecallTarget{Grade: SavingsGrade, RequiredSpans: 1, TotalSpans: 1}
	const fixtureTokenizer = "fixture-real-tokenizer"
	in := SavingsAggregateInput{
		Contract:                  FrozenMeasurementContract(),
		DatasetSHA256:             strings.Repeat("b", 64),
		RepoSHA:                   strings.Repeat("c", 40),
		CandidateVersion:          "candidate/test",
		ComparatorVersion:         "grepread/test",
		TokenizerID:               fixtureTokenizer,
		TokenizerVocabularySHA256: testVocabularySHA,
		Confidence:                FrozenConfidenceSpec(),
	}
	for _, value := range values {
		if value.c < 0 || value.g <= 0 {
			t.Fatalf("invalid fixture tokens C=%d G=%d", value.c, value.g)
		}
		member := SavingsPopulationMember{
			QueryID: value.queryID, FamilyID: value.familyID, Split: value.split,
			Stratum: StratumNLBehaviour, Target: target,
		}
		candidatePayload := aggregatePayload(1, PayloadBoundaryCandidate, PayloadOperationTaskContext, value.c, fixtureTokenizer)
		grepreadPayload := aggregatePayload(1, PayloadBoundaryGrepRead, PayloadOperationGrep, value.g, fixtureTokenizer)
		candidateTokens := value.c
		grepreadTokens := value.g
		in.Population = append(in.Population, member)
		in.Observations = append(in.Observations, SavingsObservation{
			QueryID: value.queryID,
			Candidate: SavingsArmOutcome{
				Target: target, Status: SavingsOutcomeReached, StopReason: SavingsStopOneCallComplete,
				Grade3SpansAtPrefix: 1, ConsumedPayloadSlices: 1, TokensToTarget: &candidateTokens,
				Payloads: []PreservedPayload{candidatePayload},
			},
			GrepRead: SavingsArmOutcome{
				Target: target, Status: SavingsOutcomeReached, StopReason: SavingsStopExhausted,
				Grade3SpansAtPrefix: 1, ConsumedPayloadSlices: 1, TokensToTarget: &grepreadTokens,
				Payloads: []PreservedPayload{grepreadPayload},
			},
		})
	}
	counters := map[string]PayloadCounter{
		fixtureTokenizer: {
			TokenizerID: fixtureTokenizer, VocabularySHA256: testVocabularySHA,
			Count: func(raw []byte) (int, error) { return len(raw), nil },
		},
	}
	return in, counters
}

func aggregatePayload(sequence int, boundary PayloadBoundary, operation string, tokenCount int, tokenizerID string) PreservedPayload {
	raw := bytes.Repeat([]byte{'x'}, tokenCount)
	return PreservedPayload{
		Sequence: sequence, Boundary: boundary, Operation: operation,
		Bytes: raw, SHA256: SHA256Hex(raw), ByteCount: len(raw),
		TokenCounts: []PayloadTokenCount{
			{TokenizerID: TokenizerID, Tokens: len(strings.Fields(string(raw)))},
			{TokenizerID: tokenizerID, VocabularySHA256: testVocabularySHA, Tokens: tokenCount},
		},
	}
}

func TestPercentSavingsUsesExactRationalArithmetic(t *testing.T) {
	got := percentSavings(2, 3)
	want := new(big.Rat).SetFrac64(100, 3)
	if got.Cmp(want) != 0 || exactRational(got) != (SavingsExactRational{Numerator: "100", Denominator: "3"}) {
		t.Fatalf("percentSavings(2,3) = %s, want %s", got, want)
	}
}
