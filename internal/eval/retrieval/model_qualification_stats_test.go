package retrieval

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPairedBootstrap95IsDeterministic(t *testing.T) {
	outcomes := pairedFixture(64, 12, 2)
	a := PairedBootstrap95(outcomes, 0x475241504849, 100000)
	b := PairedBootstrap95(outcomes, 0x475241504849, 100000)
	if a != b || a.Point != 10.0/64.0 || a.Lower <= 0 {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
}

func TestEvaluateQualificationRequiresEveryGate(t *testing.T) {
	valid := passingQualificationInput(t)
	got, err := EvaluateQualification(valid)
	if err != nil || !got.Promote {
		t.Fatalf("decision=%+v err=%v", got, err)
	}

	mutations := []struct {
		name  string
		gate  string
		apply func(*QualificationInput)
	}{
		{"56 passes", "m3_blind_passes", func(in *QualificationInput) { setPass(in, ArmCodeRank, "weak-11", false) }},
		{"paired gain", "paired_final_pass_gain", func(in *QualificationInput) {
			for i := 9; i < 12; i++ {
				setPass(in, ArmCodeRank, weakQueryID(i), false)
			}
		}},
		{"positive confidence interval", "paired_bootstrap_positive", makeUncertainNetNine},
		{"two weak strata", "weak_strata_positive_gain", keepGainInOneWeakStratum},
		{"no strong loss", "strong_strata_no_loss", func(in *QualificationInput) {
			setPass(in, ArmCodeRank, "strong-00", false)
		}},
		{"serialized span transfer", "serialized_span_transfer", func(in *QualificationInput) {
			for i := range in.Observations {
				if in.Observations[i].Arm == ArmCodeRank {
					in.Observations[i].CompleteGrade3Span = false
				}
			}
		}},
		{"two build equality", "two_build_byte_equality", func(in *QualificationInput) {
			for i := range in.BuildDigests {
				if in.BuildDigests[i].Arm == ArmCodeRank && i%2 == 1 {
					in.BuildDigests[i].OracleTokenCountsSHA256 = strings.Repeat("9", 64)
					return
				}
			}
		}},
		{"artifact budget", "artifact_budget", func(in *QualificationInput) { in.Operating.ArtifactBytes = 1<<30 + 1 }},
		{"rss budget", "sidecar_rss_budget", func(in *QualificationInput) { in.Operating.PeakAdditionalSidecarRSSBytes = 2<<30 + 1 }},
		{"latency budget", "query_embed_p95_budget", func(in *QualificationInput) {
			for i := 94; i < len(in.Operating.QueryEmbedLatencies); i++ {
				in.Operating.QueryEmbedLatencies[i] = time.Second + 1
			}
		}},
		{"reindex budget", "reindex_budget", func(in *QualificationInput) { in.Operating.FullReindex = 10*time.Minute + 1 }},
		{"cpu only", "cpu_only", func(in *QualificationInput) { in.Operating.CPUOnly = false }},
		{"state ready", "state_ready", func(in *QualificationInput) { semanticObservation(in, ArmCodeRank, 0).RetrievalState = "stale" }},
		{"fingerprint equality", "fingerprint_equality", func(in *QualificationInput) { semanticObservation(in, ArmCodeRank, 0).IndexFingerprint = "other" }},
		{"no degradation", "no_degradation", func(in *QualificationInput) { semanticObservation(in, ArmCodeRank, 0).Degraded = true }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			in := cloneQualificationInput(t, valid)
			mutation.apply(&in)
			got, err := EvaluateQualification(in)
			if err != nil {
				t.Fatal(err)
			}
			if got.Promote {
				t.Fatalf("gate %s was bypassed", mutation.gate)
			}
			if gatePassed(t, got, mutation.gate) {
				t.Fatalf("gate %s remained passed: %+v", mutation.gate, got.Gates)
			}
		})
	}
}

func TestEvaluateQualificationRejectsStructurallyIncompleteEvidence(t *testing.T) {
	valid := passingQualificationInput(t)
	for _, tc := range []struct {
		name  string
		apply func(*QualificationInput)
	}{
		{"observation cardinality", func(in *QualificationInput) { in.Observations = in.Observations[:len(in.Observations)-1] }},
		{"duplicate observation query", func(in *QualificationInput) { in.Observations[1].QueryID = in.Observations[0].QueryID }},
		{"grade cardinality", func(in *QualificationInput) { in.Grades[ArmCodeRank] = in.Grades[ArmCodeRank][:63] }},
		{"grade arm missing", func(in *QualificationInput) { delete(in.Grades, ArmPotion8192) }},
		{"grade malformed", func(in *QualificationInput) { in.Grades[ArmCodeRank][0].Outcome = "maybe" }},
		{"diagnostic unavailable", func(in *QualificationInput) {
			semanticObservation(in, ArmCodeRank, 0).UnknownTokens = QualificationIntMetric{}
		}},
		{"oracle cardinality", func(in *QualificationInput) { in.OracleControls = in.OracleControls[:63] }},
		{"oracle provenance", func(in *QualificationInput) {
			in.OracleControls[0].OracleCandidateOraclePacker.CandidateSHA256 = "bad"
		}},
		{"build cardinality", func(in *QualificationInput) { in.BuildDigests = in.BuildDigests[:7] }},
		{"negative build diagnostic", func(in *QualificationInput) {
			in.BuildDigests[0].Diagnostics.AdmissionTruncations = -1
			in.BuildDigests[1].Diagnostics.AdmissionTruncations = -1
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := cloneQualificationInput(t, valid)
			tc.apply(&in)
			if _, err := EvaluateQualification(in); err == nil {
				t.Fatal("accepted incomplete evidence")
			}
		})
	}
}

func TestEvaluateQualificationOperatingMeasurementsMustBePresent(t *testing.T) {
	valid := passingQualificationInput(t)
	for _, tc := range []struct {
		name  string
		gate  string
		apply func(*OperatingMeasurements)
	}{
		{"no artifacts", "artifact_budget", func(v *OperatingMeasurements) { v.ArtifactBytes = 0 }},
		{"no rss", "sidecar_rss_budget", func(v *OperatingMeasurements) { v.PeakAdditionalSidecarRSSBytes = 0 }},
		{"too few latencies", "query_embed_p95_budget", func(v *OperatingMeasurements) { v.QueryEmbedLatencies = v.QueryEmbedLatencies[:99] }},
		{"zero latency", "query_embed_p95_budget", func(v *OperatingMeasurements) { v.QueryEmbedLatencies[0] = 0 }},
		{"no reindex", "reindex_budget", func(v *OperatingMeasurements) { v.FullReindex = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := cloneQualificationInput(t, valid)
			tc.apply(&in.Operating)
			got, err := EvaluateQualification(in)
			if err != nil {
				t.Fatal(err)
			}
			if gatePassed(t, got, tc.gate) || got.Promote {
				t.Fatalf("missing measurement passed: %+v", got)
			}
		})
	}
}

func TestEvaluateQualificationDecisionBranchesFollowSpecOrder(t *testing.T) {
	tests := []struct {
		name  string
		want  string
		apply func(*QualificationInput)
	}{
		{"promotion", "promote_coderank_to_product_integration", func(*QualificationInput) {}},
		{"ranking only", "investigate_projection_or_fusion", func(in *QualificationInput) {
			for i := range in.Observations {
				if in.Observations[i].Arm == ArmCodeRank {
					in.Observations[i].CompleteGrade3Span = false
				}
			}
		}},
		{"potion admission", "design_potion_admission_candidate", func(in *QualificationInput) {
			copyGrades(in, ArmPotion8192, ArmCodeRank)
			copySpanPattern(in, ArmPotion8192, ArmCodeRank)
			copyGrades(in, ArmCodeRank, ArmPotion512)
			clearSemanticGain(in, ArmCodeRank)
		}},
		{"prefer simpler potion", "prefer_simpler_potion_candidate", func(in *QualificationInput) {
			copyGrades(in, ArmPotion8192, ArmCodeRank)
			copySpanPattern(in, ArmPotion8192, ArmCodeRank)
		}},
		{"representation ceiling", "representation_or_budget_ceiling", func(in *QualificationInput) {
			copyGrades(in, ArmCodeRank, ArmPotion512)
			clearSemanticGain(in, ArmCodeRank)
			for i := range in.OracleControls {
				in.OracleControls[i].OracleCandidateOraclePacker.CompleteGrade3Span = i < 55
			}
		}},
		{"stop", "stop_no_new_holdout", func(in *QualificationInput) {
			copyGrades(in, ArmCodeRank, ArmPotion512)
			clearSemanticGain(in, ArmCodeRank)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := cloneQualificationInput(t, passingQualificationInput(t))
			tc.apply(&in)
			got, err := EvaluateQualification(in)
			if err != nil {
				t.Fatal(err)
			}
			if got.Branch != tc.want {
				t.Fatalf("branch=%q want %q; gates=%+v", got.Branch, tc.want, got.Gates)
			}
		})
	}
}

func pairedFixture(n, m3Wins, m1Wins int) []PairedOutcome {
	out := make([]PairedOutcome, n)
	for i := range out {
		out[i].QueryID = weakQueryID(i)
	}
	for i := 0; i < m3Wins; i++ {
		out[i].M3Pass = true
	}
	for i := m3Wins; i < m3Wins+m1Wins; i++ {
		out[i].M1Pass = true
	}
	return out
}

func passingQualificationInput(t *testing.T) QualificationInput {
	t.Helper()
	pre := qualificationPreregistrationFixture()
	strata := []struct {
		name   string
		count  int
		strong bool
	}{
		{StratumAmbiguous, 10, false}, {StratumArchitectureFlow, 11, false},
		{StratumNLBehaviour, 11, false}, {StratumConfigDocs, 10, true},
		{StratumExactIdentifier, 11, true}, {StratumExactPath, 11, true},
	}
	var queryIDs []string
	queryStratum := map[string]string{}
	for _, group := range strata {
		for i := 0; i < group.count; i++ {
			id := "weak-" + twoDigits(len(queryIDs))
			if group.strong {
				id = "strong-" + twoDigits(len(queryIDs)-32)
			}
			queryIDs = append(queryIDs, id)
			queryStratum[id] = group.name
		}
	}
	input := QualificationInput{
		Preregistration: pre,
		Grades:          map[QualificationArm][]Grade{},
		Operating: OperatingMeasurements{CPUOnly: true, ArtifactBytes: 512 << 20,
			PeakAdditionalSidecarRSSBytes: 1 << 30, QueryEmbedLatencies: make([]time.Duration, 100), FullReindex: 5 * time.Minute},
	}
	for i := range input.Operating.QueryEmbedLatencies {
		input.Operating.QueryEmbedLatencies[i] = 500 * time.Millisecond
	}
	for _, arm := range []QualificationArm{ArmLexical, ArmPotion512, ArmPotion8192, ArmCodeRank} {
		for q, id := range queryIDs {
			stage := StageHit{}
			if arm == ArmCodeRank {
				stage = StageHit{Present: true, BestRank: 1}
			}
			observation := QualificationObservation{Arm: arm, QueryID: id, Stratum: queryStratum[id],
				SemanticTop50: stage, PostFusion: stage,
				CompleteGrade3Span: arm == ArmCodeRank, BundleSHA256: strings.Repeat("a", 64), PayloadSHA256: strings.Repeat("b", 64), BundleTokens: 100}
			if arm == ArmLexical {
				observation.RetrievalState = "lexical_only"
			} else {
				zero, unknown := false, 0
				observation.RetrievalState = "ready"
				observation.ModelFingerprint = pre.Arms[arm].FingerprintCanonical
				observation.IndexFingerprint = pre.Arms[arm].FingerprintCanonical
				observation.UnknownTokens = QualificationIntMetric{Available: true, Value: &unknown}
				observation.QueryVectorAllZero = QualificationBoolMetric{Available: true, Value: &zero}
			}
			input.Observations = append(input.Observations, observation)

			pass := false
			switch arm {
			case ArmPotion512:
				pass = q < 14 || q >= 32 // all strong plus 14 weak = 46
			case ArmCodeRank:
				pass = q < 12 || (q >= 14 && q < 26) || q >= 32 // 56, +12/-2 vs M1
			}
			input.Grades[arm] = append(input.Grades[arm], qualificationGrade(t, id, pass))
		}
		for build := 0; build < 2; build++ {
			input.BuildDigests = append(input.BuildDigests, QualificationBuildDigest{Arm: arm,
				VectorBytesSHA256:   strings.Repeat(string('a'+rune(arm[1]-'0')), 64),
				PersistedRowsSHA256: strings.Repeat("b", 64), BundlesSHA256: strings.Repeat("c", 64),
				TokenCountsSHA256: strings.Repeat("d", 64), OraclePayloadsSHA256: strings.Repeat("e", 64),
				OracleTokenCountsSHA256: strings.Repeat("f", 64)})
		}
	}
	for _, id := range queryIDs {
		mk := func(kind string) OracleBundle {
			payloadBytes := []byte("{}")
			return OracleBundle{ControlKind: kind, QueryID: id, OutputName: kind + ".json",
				CandidateProvenance: kind, CandidateSHA256: strings.Repeat("7", 64),
				CompleteGrade3Span: true, TokenCount: 1,
				Payload: PreservedPayload{Bytes: payloadBytes, SHA256: SHA256Hex(payloadBytes), ByteCount: len(payloadBytes),
					TokenCounts: []PayloadTokenCount{{TokenizerID: "cl100k_base", Tokens: 1}}}}
		}
		current := mk(OracleControlCurrentCandidatesOraclePacker)
		selected := mk(OracleControlOracleCandidateCurrentSelector)
		selected.Injected, selected.InjectedRows = true, 1
		packed := mk(OracleControlOracleCandidateOraclePacker)
		packed.Injected, packed.InjectedRows = true, 1
		input.OracleControls = append(input.OracleControls, OracleControls{CurrentCandidatesOraclePacker: current,
			OracleCandidateCurrentSelector: selected, OracleCandidateOraclePacker: packed})
	}
	return input
}

func qualificationGrade(t *testing.T, queryID string, pass bool) Grade {
	t.Helper()
	outcome := GradeOutcomeFail
	if pass {
		outcome = GradeOutcomePass
	}
	g, err := SealGrade(Grade{ContractVersion: QrelBlindSmokeContractVersion, Evaluation: QrelBlindSmokeEvaluationName,
		QueryID: queryID, ResponseSHA256: strings.Repeat("1", 64), GraderID: "grader", Provider: "provider",
		Model: "model", RubricSHA256: strings.Repeat("2", 64), Outcome: outcome, Rationale: "blind grade",
		GradedAt: "2026-09-16T12:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func cloneQualificationInput(t *testing.T, in QualificationInput) QualificationInput {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out QualificationInput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func setPass(in *QualificationInput, arm QualificationArm, queryID string, pass bool) {
	for i := range in.Grades[arm] {
		if in.Grades[arm][i].QueryID == queryID {
			in.Grades[arm][i].Outcome = GradeOutcomeFail
			if pass {
				in.Grades[arm][i].Outcome = GradeOutcomePass
			}
			in.Grades[arm][i].SHA256 = ""
			in.Grades[arm][i], _ = SealGrade(in.Grades[arm][i])
			return
		}
	}
}

func copyGrades(in *QualificationInput, dst, src QualificationArm) {
	for _, grade := range in.Grades[src] {
		setPass(in, dst, grade.QueryID, grade.Outcome == GradeOutcomePass)
	}
}

func copySpanPattern(in *QualificationInput, dst, src QualificationArm) {
	byID := map[string]bool{}
	for _, observation := range in.Observations {
		if observation.Arm == src {
			byID[observation.QueryID] = observation.CompleteGrade3Span
		}
	}
	for i := range in.Observations {
		if in.Observations[i].Arm == dst {
			in.Observations[i].CompleteGrade3Span = byID[in.Observations[i].QueryID]
		}
	}
}

func clearSemanticGain(in *QualificationInput, arm QualificationArm) {
	for i := range in.Observations {
		if in.Observations[i].Arm == arm {
			in.Observations[i].SemanticTop50 = StageHit{}
		}
	}
}

func semanticObservation(in *QualificationInput, arm QualificationArm, ordinal int) *QualificationObservation {
	for i := range in.Observations {
		if in.Observations[i].Arm == arm {
			if ordinal == 0 {
				return &in.Observations[i]
			}
			ordinal--
		}
	}
	panic("semantic observation not found")
}

func gatePassed(t *testing.T, got QualificationDecision, name string) bool {
	t.Helper()
	for _, gate := range got.Gates {
		if gate.Name == name {
			return gate.Passed
		}
	}
	t.Fatalf("gate %q missing from %+v", name, got.Gates)
	return false
}

func makeUncertainNetNine(in *QualificationInput) {
	// 39 shared passes, 17 M3-only, 8 M1-only: pass counts 47/56,
	// paired net +9 but the percentile interval includes zero.
	for i, grade := range in.Grades[ArmPotion512] {
		setPass(in, ArmPotion512, grade.QueryID, i < 47)
		setPass(in, ArmCodeRank, grade.QueryID, i < 39 || (i >= 47 && i < 64))
	}
}

func keepGainInOneWeakStratum(in *QualificationInput) {
	// Only ambiguous gains; architecture_flow and nl_behaviour remain flat.
	for i := 0; i < 32; i++ {
		id := weakQueryID(i)
		setPass(in, ArmPotion512, id, false)
		setPass(in, ArmCodeRank, id, i < 9)
	}
}

func weakQueryID(i int) string { return "weak-" + twoDigits(i) }
func twoDigits(i int) string   { return string([]byte{'0' + byte(i/10), '0' + byte(i%10)}) }
