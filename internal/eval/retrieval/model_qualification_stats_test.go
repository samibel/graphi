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
		{"required diagnostics", "required_diagnostics_available", func(in *QualificationInput) {
			semanticObservation(in, ArmPotion8192, 0).UnknownTokens = QualificationIntMetric{}
		}},
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
		{"decision cardinality", func(in *QualificationInput) { in.Decisions = in.Decisions[:len(in.Decisions)-1] }},
		{"duplicate decision query", func(in *QualificationInput) { in.Decisions[1] = in.Decisions[0] }},
		{"decision content address", func(in *QualificationInput) { in.Decisions[0].SHA256 = strings.Repeat("9", 64) }},
		{"decision evidence address", func(in *QualificationInput) { in.Decisions[0].Outcome.Reason = "tampered" }},
		{"decision payload binding", func(in *QualificationInput) {
			in.Decisions[0].PayloadSHA256 = strings.Repeat("9", 64)
			in.Decisions[0] = mustSealBlindDecision(t, in.Decisions[0])
		}},
		{"decision prompt binding", func(in *QualificationInput) {
			in.Decisions[0].ReaderPromptSHA256 = strings.Repeat("9", 64)
			in.Decisions[0] = mustSealBlindDecision(t, in.Decisions[0])
		}},
		{"decision subject", func(in *QualificationInput) {
			in.Decisions[0].ControlKind = OracleControlOracleCandidateOraclePacker
			in.Decisions[0] = mustSealBlindDecision(t, in.Decisions[0])
		}},
		{"oracle decision payload binding", func(in *QualificationInput) {
			for i := range in.Decisions {
				if in.Decisions[i].ControlKind == OracleControlOracleCandidateOraclePacker {
					in.Decisions[i].PayloadSHA256 = strings.Repeat("9", 64)
					in.Decisions[i] = mustSealBlindDecision(t, in.Decisions[i])
					break
				}
			}
		}},
		{"non-final query outcome", func(in *QualificationInput) {
			in.Decisions[0].Outcome.Primary = in.Decisions[0].Outcome.Primary[:1]
			in.Decisions[0] = mustSealBlindDecision(t, in.Decisions[0])
		}},
		{"oracle cardinality", func(in *QualificationInput) { in.OracleControls = in.OracleControls[:63] }},
		{"oracle provenance", func(in *QualificationInput) {
			in.OracleControls[0].OracleCandidateOraclePacker.CandidateSHA256 = "bad"
		}},
		{"build cardinality", func(in *QualificationInput) { in.BuildDigests = in.BuildDigests[:7] }},
		{"duplicate build ordinal", func(in *QualificationInput) { in.BuildDigests[1].Build = 1 }},
		{"duplicate build provenance", func(in *QualificationInput) {
			in.BuildDigests[1].CaptureProvenance = in.BuildDigests[0].CaptureProvenance
			in.BuildDigests[1].CaptureProvenance.Arm = in.BuildDigests[1].Arm
			in.BuildDigests[1].CaptureProvenance.Build = in.BuildDigests[1].Build
		}},
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

func TestEvaluateQualificationRequiresValidatedBlindSourceEvidence(t *testing.T) {
	valid := passingQualificationInput(t)
	for _, tc := range []struct {
		name  string
		apply func(*QualificationInput)
	}{
		{"missing subject evidence", func(in *QualificationInput) { in.BlindEvidence = in.BlindEvidence[:6] }},
		{"tampered source address", func(in *QualificationInput) { in.BlindEvidence[0].SHA256 = strings.Repeat("9", 64) }},
		{"tampered response", func(in *QualificationInput) { in.BlindEvidence[0].Responses[0].Text = "tampered" }},
		{"tampered grade", func(in *QualificationInput) { in.BlindEvidence[0].Grades[0].Outcome = GradeOutcomePass }},
		{"wrong frozen rubric", func(in *QualificationInput) {
			for i := range in.BlindEvidence[0].Precondition.Inputs {
				if in.BlindEvidence[0].Precondition.Inputs[i].Role == "grading_rubric" {
					in.BlindEvidence[0].Precondition.Inputs[i].SHA256 = strings.Repeat("9", 64)
				}
			}
		}},
		{"decision differs from derived outcome", func(in *QualificationInput) {
			in.Decisions[0].Outcome.Outcome = GradeOutcomePass
			in.Decisions[0].Outcome.Reason = "both primary grades passed"
			for i := range in.Decisions[0].Outcome.Primary {
				in.Decisions[0].Outcome.Primary[i].Outcome = GradeOutcomePass
			}
			in.Decisions[0] = mustSealBlindDecision(t, in.Decisions[0], in.Decisions[0].EvidenceSHA256)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := cloneQualificationInput(t, valid)
			tc.apply(&in)
			if _, err := EvaluateQualification(in); err == nil {
				t.Fatal("accepted untrusted blind source evidence")
			}
		})
	}
}

func TestEvaluateQualificationRejectsBlindEvidenceReplay(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(*QualificationInput)
	}{
		{"raw dataset tamper", func(in *QualificationInput) { in.Dataset.Raw = append(in.Dataset.Raw, '\n') }},
		{"cross dataset", func(in *QualificationInput) {
			in.BlindEvidence[0].Precondition.DatasetSHA256 = strings.Repeat("9", 64)
			resealBlindEvidenceChain(in, 0)
		}},
		{"cross candidate", func(in *QualificationInput) {
			in.BlindEvidence[0].Precondition.CandidateSHA = strings.Repeat("9", 40)
			in.BlindEvidence[0].Precondition.FreezeCommit = strings.Repeat("9", 40)
			resealBlindEvidenceChain(in, 0)
		}},
		{"cross query text", func(in *QualificationInput) {
			in.BlindEvidence[0].PreRegistration.Queries[0].QueryTextSHA256 = SHA256Hex([]byte("a different query"))
			resealBlindEvidenceChain(in, 0)
		}},
		{"precondition commit", func(in *QualificationInput) {
			in.BlindEvidence[0].PreRegistration.PreconditionCommit = strings.Repeat("9", 40)
			resealBlindEvidenceChain(in, 0)
		}},
		{"cross subject", func(in *QualificationInput) {
			in.BlindEvidence[0].Arm, in.BlindEvidence[3].Arm = in.BlindEvidence[3].Arm, in.BlindEvidence[0].Arm
			resealBlindEvidenceChain(in, 0)
			resealBlindEvidenceChain(in, 3)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := cloneQualificationInput(t, passingQualificationInput(t))
			tc.apply(&in)
			if _, err := EvaluateQualification(in); err == nil {
				t.Fatal("accepted replayed blind evidence")
			}
		})
	}
}

func TestEvaluateQualificationRecomputesClosedBuildProvenance(t *testing.T) {
	valid := passingQualificationInput(t)
	for _, tc := range []struct {
		name  string
		apply func(*QualificationBuildDigest)
	}{
		{"omission", func(d *QualificationBuildDigest) { d.CaptureProvenance = QualificationCaptureProvenanceRecord{} }},
		{"tamper", func(d *QualificationBuildDigest) { d.CaptureProvenance.WorkDir = "/tampered" }},
		{"cycle", func(d *QualificationBuildDigest) {
			copy := *d
			d.CaptureProvenance.Provenance.QualificationBuildDigest = &copy
			d.CaptureProvenance = mustSealQualificationCaptureProvenanceRecord(t, d.CaptureProvenance)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := cloneQualificationInput(t, valid)
			tc.apply(&in.BuildDigests[0])
			if _, err := EvaluateQualification(in); err == nil {
				t.Fatal("accepted non-recomputable build provenance")
			}
		})
	}
}

func TestEvaluateQualificationSemanticallyValidatesBuildProvenance(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(*QualificationInput, *QualificationBuildDigest)
	}{
		{"dataset", func(_ *QualificationInput, d *QualificationBuildDigest) {
			d.CaptureProvenance.Provenance.DatasetSHA256 = strings.Repeat("9", 64)
		}},
		{"source checkout", func(_ *QualificationInput, d *QualificationBuildDigest) {
			d.CaptureProvenance.Provenance.RepoSHA = strings.Repeat("9", 40)
		}},
		{"candidate binding", func(_ *QualificationInput, d *QualificationBuildDigest) {
			d.CaptureProvenance.Provenance.Binding.CandidateSHA = strings.Repeat("9", 40)
		}},
		{"token budget", func(_ *QualificationInput, d *QualificationBuildDigest) { d.CaptureProvenance.Provenance.TokenBudget++ }},
		{"method", func(_ *QualificationInput, d *QualificationBuildDigest) {
			d.CaptureProvenance.Provenance.MethodVersion = "compact/other"
		}},
		{"capture", func(_ *QualificationInput, d *QualificationBuildDigest) {
			d.CaptureProvenance.Provenance.CaptureVersion = "capture/other"
		}},
		{"surface", func(_ *QualificationInput, d *QualificationBuildDigest) {
			d.CaptureProvenance.Provenance.Surface = "other"
		}},
		{"boundary", func(_ *QualificationInput, d *QualificationBuildDigest) {
			d.CaptureProvenance.Provenance.Boundary = "other"
		}},
		{"query count", func(_ *QualificationInput, d *QualificationBuildDigest) {
			d.CaptureProvenance.Provenance.QueryCount = 63
		}},
		{"tokenizer id", func(_ *QualificationInput, d *QualificationBuildDigest) {
			d.CaptureProvenance.Provenance.TokenizerID = "other"
		}},
		{"tokenizer vocabulary", func(_ *QualificationInput, d *QualificationBuildDigest) {
			d.CaptureProvenance.Provenance.TokenizerVocabSHA = strings.Repeat("9", 64)
		}},
		{"candidate excluded path", func(_ *QualificationInput, d *QualificationBuildDigest) {
			d.CaptureProvenance.Provenance.Binding.CandidateExcludedPath = "docs/eval/retrieval/runs/other"
		}},
		{"arm fingerprint", func(_ *QualificationInput, d *QualificationBuildDigest) {
			d.CaptureProvenance.Provenance.ModelFingerprint = "other"
		}},
		{"same workdir", func(in *QualificationInput, d *QualificationBuildDigest) {
			d.CaptureProvenance.WorkDir = in.BuildDigests[0].CaptureProvenance.WorkDir
			d.CaptureProvenance.Provenance.QualificationCaptureRunSHA256 = qualificationCaptureRunSHA(d.Arm, d.CaptureProvenance.WorkDir)
		}},
		{"reordinal copy", func(in *QualificationInput, d *QualificationBuildDigest) {
			d.CaptureProvenance.Provenance = in.BuildDigests[0].CaptureProvenance.Provenance
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := cloneQualificationInput(t, passingQualificationInput(t))
			digest := &in.BuildDigests[1]
			tc.apply(&in, digest)
			digest.CaptureProvenance = mustSealQualificationCaptureProvenanceRecord(t, digest.CaptureProvenance)
			if _, err := EvaluateQualification(in); err == nil {
				t.Fatal("accepted semantically invalid build provenance")
			}
		})
	}
}

func TestSemanticRankDeltaUsesAbsentRank51AndExactPairedRanks(t *testing.T) {
	tests := []struct {
		name   string
		m1, m3 StageHit
		want   int
	}{
		{"absent to present", StageHit{}, StageHit{Present: true, BestRank: 50}, 1},
		{"present to absent", StageHit{Present: true, BestRank: 50}, StageHit{}, -1},
		{"better", StageHit{Present: true, BestRank: 20}, StageHit{Present: true, BestRank: 3}, 1},
		{"equal", StageHit{Present: true, BestRank: 7}, StageHit{Present: true, BestRank: 7}, 0},
		{"worse", StageHit{Present: true, BestRank: 2}, StageHit{Present: true, BestRank: 8}, -1},
		{"both absent", StageHit{}, StageHit{}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pairedSemanticRankDelta(tc.m1, tc.m3); got != tc.want {
				t.Fatalf("delta=%d want %d", got, tc.want)
			}
		})
	}
	in := passingQualificationInput(t)
	got, err := EvaluateQualification(in)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, evidence := range got.Evidence {
		if evidence.Name == "semantic_rank_pairwise_net" {
			found = strings.Contains(evidence.Algorithm, "absent-51")
		}
	}
	if !found {
		t.Fatalf("semantic rank evidence missing algorithm: %+v", got.Evidence)
	}
}

func TestEvaluateQualificationInvalidM2CannotSelectActionBranch(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(*QualificationObservation)
	}{
		{"state", func(o *QualificationObservation) { o.RetrievalState = "stale" }},
		{"fingerprint", func(o *QualificationObservation) { o.IndexFingerprint = "other" }},
		{"degraded", func(o *QualificationObservation) { o.Degraded = true }},
		{"diagnostics", func(o *QualificationObservation) { o.UnknownTokens = QualificationIntMetric{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := passingQualificationInput(t)
			copyDecisions(&in, ArmPotion8192, ArmCodeRank)
			copySpanPattern(&in, ArmPotion8192, ArmCodeRank)
			tc.apply(semanticObservation(&in, ArmPotion8192, 0))
			got, err := EvaluateQualification(in)
			if err != nil {
				t.Fatal(err)
			}
			if got.Branch != "stop_no_new_holdout" || got.Promote {
				t.Fatalf("decision=%+v", got)
			}
		})
	}
}

func TestRepresentationCeilingUsesBlindOracleDecisionsNotSpanMetadata(t *testing.T) {
	in := passingQualificationInput(t)
	copyDecisions(&in, ArmCodeRank, ArmPotion512)
	clearSemanticGain(&in, ArmCodeRank)
	for i := range in.OracleControls {
		in.OracleControls[i].OracleCandidateOraclePacker.CompleteGrade3Span = true
	}
	for i := 0; i < 9; i++ {
		setControlPass(&in, OracleControlOracleCandidateOraclePacker, weakQueryID(i), false)
	}
	got, err := EvaluateQualification(in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Branch != "representation_or_budget_ceiling" {
		t.Fatalf("branch=%q", got.Branch)
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
			for i := 0; i < 9; i++ {
				setControlPass(in, OracleControlOracleCandidateOraclePacker, weakQueryID(i), false)
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
	dataset := &Loaded{Dataset: &Dataset{SchemaVersion: SchemaVersion, ID: "coderank-qualification-fixture-v1", Repo: "cobra",
		RepoSHA: pre.SourceRepoSHA, Language: "en", EvidenceClass: "independent-curator-annotated-and-reviewed", RelevantMinGrade: GradeMax},
		Path: "/qualification/fixture.json"}
	for _, id := range queryIDs {
		dataset.Dataset.Queries = append(dataset.Dataset.Queries, Query{ID: id, Stratum: queryStratum[id], Language: "en", Split: SplitDev,
			Text: fixtureQueryText(id), FamilyID: "family-" + id, Provenance: "independent qualification fixture",
			Judgements: []Judgement{{Path: "answer.go", StartLine: 1, EndLine: 1, Anchor: "answer", Grade: GradeMax, Reason: "exact answer", Annotator: "curator", Reviewer: "reviewer"}}})
	}
	sealQualificationDatasetFixture(dataset)
	pre.DatasetSHA256 = dataset.SHA256
	input := QualificationInput{
		Preregistration: pre, Dataset: dataset,
		Operating: OperatingMeasurements{CPUOnly: true, ArtifactBytes: 512 << 20,
			PeakAdditionalSidecarRSSBytes: 1 << 30, QueryEmbedLatencies: make([]time.Duration, 100), FullReindex: 5 * time.Minute},
	}
	desired := make(map[string]map[string]bool, 7)
	for i := range input.Operating.QueryEmbedLatencies {
		input.Operating.QueryEmbedLatencies[i] = 500 * time.Millisecond
	}
	for _, arm := range []QualificationArm{ArmLexical, ArmPotion512, ArmPotion8192, ArmCodeRank} {
		desired[blindSubjectKey(arm, "")] = make(map[string]bool, 64)
		for q, id := range queryIDs {
			stage := StageHit{}
			if arm == ArmCodeRank {
				stage = StageHit{Present: true, BestRank: 1}
			}
			observation := QualificationObservation{Arm: arm, QueryID: id, Stratum: queryStratum[id],
				SemanticTop50: stage, PostFusion: stage,
				CompleteGrade3Span: arm == ArmCodeRank, BundleSHA256: SHA256Hex([]byte("bundle:" + string(arm) + ":" + id)),
				PayloadSHA256: SHA256Hex([]byte("payload:" + string(arm) + ":" + id)), BundleTokens: 100}
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
			desired[blindSubjectKey(arm, "")][id] = pass
		}
		for build := 0; build < 2; build++ {
			digest := QualificationBuildDigest{Arm: arm, Build: build + 1,
				VectorBytesSHA256:   strings.Repeat(string('a'+rune(arm[1]-'0')), 64),
				PersistedRowsSHA256: strings.Repeat("b", 64), BundlesSHA256: strings.Repeat("c", 64),
				TokenCountsSHA256: strings.Repeat("d", 64), OraclePayloadsSHA256: strings.Repeat("e", 64),
				OracleTokenCountsSHA256: strings.Repeat("f", 64), QueryDiagnosticsSHA256: strings.Repeat("1", 64)}
			digest.CaptureProvenance = mustSealQualificationCaptureProvenanceRecord(t, QualificationCaptureProvenanceRecord{
				Arm: arm, Build: build + 1, WorkDir: "/runs/" + string(arm) + "/build-" + string(rune('1'+build)),
				Provenance: qualificationCaptureProvenanceFixture(pre, dataset, arm, build+1),
			})
			input.BuildDigests = append(input.BuildDigests, digest)
		}
	}
	for _, id := range queryIDs {
		mk := func(kind string) OracleBundle {
			payloadBytes := []byte(kind + ":" + id)
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
		for _, bundle := range []OracleBundle{current, selected, packed} {
			key := blindSubjectKey("", bundle.ControlKind)
			if desired[key] == nil {
				desired[key] = make(map[string]bool, 64)
			}
			desired[key][id] = true
		}
	}
	for _, subject := range []struct {
		arm  QualificationArm
		kind string
	}{
		{arm: ArmLexical}, {arm: ArmPotion512}, {arm: ArmPotion8192}, {arm: ArmCodeRank},
		{kind: OracleControlCurrentCandidatesOraclePacker}, {kind: OracleControlOracleCandidateCurrentSelector}, {kind: OracleControlOracleCandidateOraclePacker},
	} {
		source, decisions := qualificationBlindEvidenceFixture(t, input, subject.arm, subject.kind, desired[blindSubjectKey(subject.arm, subject.kind)])
		input.BlindEvidence = append(input.BlindEvidence, source)
		input.Decisions = append(input.Decisions, decisions...)
	}
	return input
}

func qualificationBlindEvidenceFixture(t *testing.T, in QualificationInput, arm QualificationArm, controlKind string, passes map[string]bool) (BlindEvidenceSet, []BlindDecision) {
	t.Helper()
	precondition := fixturePrecondition(t)
	precondition.DatasetSHA256 = in.Preregistration.DatasetSHA256
	precondition.CandidateSHA = in.Preregistration.CandidateSHA
	precondition.FreezeCommit = in.Preregistration.CandidateSHA
	precondition.CandidateTokenBudget = QualificationTokenBudget
	for i := range precondition.Inputs {
		if precondition.Inputs[i].Role == "grading_rubric" {
			precondition.Inputs[i].SHA256 = in.Preregistration.GraderPromptSHA256
		}
	}
	var err error
	precondition, err = SealPreconditionRecord(precondition)
	if err != nil {
		t.Fatal(err)
	}
	primaries, grader, adjudicator := fixtureParticipants()
	derivation, err := DerivePassCountForContract(QrelBlindSmokeContractVersion, 64, precondition.DatasetSHA256, "qualification fixture population")
	if err != nil {
		t.Fatal(err)
	}
	pre := PreRegistration{ContractVersion: QrelBlindSmokeContractVersion, Evaluation: QrelBlindSmokeEvaluationName,
		PreconditionSHA256: precondition.SHA256, PreconditionCommit: in.Preregistration.CandidateSHA,
		RecordedAt: fixturePreRegAt.Format(time.RFC3339), Derivation: derivation, PrimaryRaters: primaries, Grader: grader, Adjudicator: adjudicator}
	for _, observation := range in.Observations {
		if observation.Arm != ArmLexical {
			continue
		}
		payloadSHA := ""
		if arm != "" {
			for _, candidate := range in.Observations {
				if candidate.Arm == arm && candidate.QueryID == observation.QueryID {
					payloadSHA = candidate.PayloadSHA256
					break
				}
			}
		} else {
			for _, controls := range in.OracleControls {
				for _, bundle := range []OracleBundle{controls.CurrentCandidatesOraclePacker, controls.OracleCandidateCurrentSelector, controls.OracleCandidateOraclePacker} {
					if bundle.QueryID == observation.QueryID && bundle.ControlKind == controlKind {
						payloadSHA = bundle.Payload.SHA256
					}
				}
			}
		}
		pre.Queries = append(pre.Queries, PreRegisteredQuery{QueryID: observation.QueryID, FamilyID: "family-" + observation.QueryID,
			Stratum: observation.Stratum, QueryTextSHA256: SHA256Hex([]byte(fixtureQueryText(observation.QueryID))),
			PromptSHA256: in.Preregistration.ReaderPromptSHA256, BundleSHA256: payloadSHA, BundleByteCount: 2,
			BundleBoundary: PayloadBoundaryCandidate, BundleTokenCounts: []PayloadTokenCount{
				{TokenizerID: TokenizerID, Tokens: 1}, {TokenizerID: "tiktoken:cl100k_base:ordinary", VocabularySHA256: fixtureVocabSHA, Tokens: 1},
			}})
	}
	pre, err = SealPreRegistration(pre)
	if err != nil {
		t.Fatal(err)
	}
	source := BlindEvidenceSet{Arm: arm, ControlKind: controlKind, Precondition: precondition, PreRegistration: pre}
	for _, query := range pre.Queries {
		outcome := GradeOutcomeFail
		if passes[query.QueryID] {
			outcome = GradeOutcomePass
		}
		for _, rater := range primaries {
			response, sealErr := SealRaterResponse(RaterResponse{ContractVersion: pre.ContractVersion, Evaluation: pre.Evaluation,
				Role: RaterRolePrimary, QueryID: query.QueryID, RaterID: rater.ID, Provider: rater.Provider, Model: rater.Model,
				PreRegistrationSHA256: pre.SHA256, QueryTextSHA256: query.QueryTextSHA256, BundleSHA256: query.BundleSHA256,
				PromptSHA256: query.PromptSHA256, Inputs: []string{"query_text", "preserved_bundle", "answer_instructions"},
				Status: ResponseStatusAnswered, Text: "blind answer for " + query.QueryID, RespondedAt: fixtureRespondAt.Format(time.RFC3339)})
			if sealErr != nil {
				t.Fatal(sealErr)
			}
			grade, sealErr := SealGrade(Grade{ContractVersion: pre.ContractVersion, Evaluation: pre.Evaluation, QueryID: query.QueryID,
				ResponseSHA256: response.SHA256, GraderID: grader.ID, Provider: grader.Provider, Model: grader.Model,
				RubricSHA256: in.Preregistration.GraderPromptSHA256, Outcome: outcome, Rationale: "qualification fixture grade", GradedAt: fixtureGradeAt.Format(time.RFC3339)})
			if sealErr != nil {
				t.Fatal(sealErr)
			}
			source.Responses = append(source.Responses, response)
			source.Grades = append(source.Grades, grade)
		}
	}
	source, err = sealBlindEvidenceSet(source)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := EvaluationArtifacts{Precondition: source.Precondition, PreRegistration: source.PreRegistration, Responses: source.Responses, Grades: source.Grades}
	decisions := make([]BlindDecision, 0, 64)
	for _, query := range source.PreRegistration.Queries {
		outcome, decideErr := DecideQuery(artifacts, query)
		if decideErr != nil {
			t.Fatal(decideErr)
		}
		decision := BlindDecision{Arm: arm, ControlKind: controlKind, QueryID: query.QueryID, Stratum: query.Stratum,
			PayloadSHA256: query.BundleSHA256, ReaderPromptSHA256: in.Preregistration.ReaderPromptSHA256,
			GraderPromptSHA256: in.Preregistration.GraderPromptSHA256, Outcome: outcome}
		decisions = append(decisions, mustSealBlindDecision(t, decision, source.SHA256))
	}
	return source, decisions
}

func mustSealBlindDecision(t *testing.T, decision BlindDecision, evidenceSHA ...string) BlindDecision {
	t.Helper()
	address := decision.EvidenceSHA256
	if len(evidenceSHA) != 0 {
		address = evidenceSHA[0]
	}
	decision.SHA256 = ""
	sealed, err := sealBlindDecision(decision, address)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func qualificationCaptureProvenanceFixture(pre QualificationPreregistration, dataset *Loaded, arm QualificationArm, build int) CandidateCaptureProvenance {
	pin := pre.Arms[arm]
	p := CandidateCaptureProvenance{CaptureVersion: CandidateCaptureVersion, Transport: CandidateCaptureTransport, Surface: CandidateCaptureSurface,
		Boundary: string(PayloadBoundaryCandidate), RepoName: dataset.Dataset.Repo, RepoSHA: pre.SourceRepoSHA, DatasetSHA256: pre.DatasetSHA256,
		EmbedderSelector: pin.Label, ModelFingerprint: pin.FingerprintCanonical, IndexFingerprint: pin.FingerprintCanonical,
		GenerationID: "generation-" + string(rune('0'+build)), PersistedVectors: 64, SemanticState: "ready", TokenBudget: QualificationTokenBudget,
		MethodVersion: QualificationCompactVersion, TokenizerID: PinnedRealPayloadTokenizerID, TokenizerVocabSHA: PinnedRealPayloadTokenizerVocabularySHA256, QueryCount: 64,
		Binding: &CandidateBinding{CandidateSHA: pre.CandidateSHA, FrozenCandidateSHA: pre.CandidateSHA, CandidateWorktreeClean: true,
			CandidateMatchesFrozen: true, CandidateExcludedPath: QualificationCandidateExcludedPath, CheckoutSHA: pre.SourceRepoSHA, CheckoutWorktreeClean: true}}
	if arm == ArmLexical {
		p.ModelFingerprint, p.IndexFingerprint, p.GenerationID, p.SemanticState, p.PersistedVectors = "", "", "", "unset", 0
	}
	return p
}

func mustSealQualificationCaptureProvenanceRecord(t *testing.T, record QualificationCaptureProvenanceRecord) QualificationCaptureProvenanceRecord {
	t.Helper()
	if record.Provenance.QualificationCaptureRunSHA256 == "" {
		record.Provenance.QualificationCaptureRunSHA256 = qualificationCaptureRunSHA(record.Arm, record.WorkDir)
	}
	identity, err := qualificationCaptureRecordIdentitySHA(record)
	if err != nil {
		t.Fatal(err)
	}
	record.CaptureIdentitySHA256 = identity
	sealed, err := sealQualificationCaptureProvenanceRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
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
	setBlindEvidencePass(in, arm, "", queryID, pass)
	rebuildBlindSubject(in, arm, "")
}

func setControlPass(in *QualificationInput, controlKind, queryID string, pass bool) {
	setBlindEvidencePass(in, "", controlKind, queryID, pass)
	rebuildBlindSubject(in, "", controlKind)
}

func setBlindEvidencePass(in *QualificationInput, arm QualificationArm, controlKind, queryID string, pass bool) {
	outcome := GradeOutcomeFail
	if pass {
		outcome = GradeOutcomePass
	}
	for i := range in.BlindEvidence {
		if in.BlindEvidence[i].Arm != arm || in.BlindEvidence[i].ControlKind != controlKind {
			continue
		}
		for j := range in.BlindEvidence[i].Grades {
			if in.BlindEvidence[i].Grades[j].QueryID == queryID {
				in.BlindEvidence[i].Grades[j].Outcome = outcome
				sealed, err := SealGrade(in.BlindEvidence[i].Grades[j])
				if err != nil {
					panic(err)
				}
				in.BlindEvidence[i].Grades[j] = sealed
			}
		}
		return
	}
	panic("blind evidence subject not found")
}

func rebuildBlindSubject(in *QualificationInput, arm QualificationArm, controlKind string) {
	var source *BlindEvidenceSet
	for i := range in.BlindEvidence {
		if in.BlindEvidence[i].Arm == arm && in.BlindEvidence[i].ControlKind == controlKind {
			source = &in.BlindEvidence[i]
			break
		}
	}
	if source == nil {
		panic("blind evidence subject not found")
	}
	sealedSource, err := sealBlindEvidenceSet(*source)
	if err != nil {
		panic(err)
	}
	*source = sealedSource
	artifacts := EvaluationArtifacts{Precondition: source.Precondition, PreRegistration: source.PreRegistration, Responses: source.Responses, Grades: source.Grades, Adjudications: source.Adjudications}
	queries := make(map[string]PreRegisteredQuery, 64)
	for _, query := range source.PreRegistration.Queries {
		queries[query.QueryID] = query
	}
	for i := range in.Decisions {
		if in.Decisions[i].Arm != arm || in.Decisions[i].ControlKind != controlKind {
			continue
		}
		outcome, decideErr := DecideQuery(artifacts, queries[in.Decisions[i].QueryID])
		if decideErr != nil {
			panic(decideErr)
		}
		in.Decisions[i].Outcome = outcome
		sealed, sealErr := sealBlindDecision(in.Decisions[i], source.SHA256)
		if sealErr != nil {
			panic(sealErr)
		}
		in.Decisions[i] = sealed
	}
}

func resealBlindEvidenceChain(in *QualificationInput, sourceIndex int) {
	source := &in.BlindEvidence[sourceIndex]
	var err error
	source.Precondition, err = SealPreconditionRecord(source.Precondition)
	if err != nil {
		panic(err)
	}
	source.PreRegistration.PreconditionSHA256 = source.Precondition.SHA256
	source.PreRegistration, err = SealPreRegistration(source.PreRegistration)
	if err != nil {
		panic(err)
	}
	queries := make(map[string]PreRegisteredQuery, 64)
	for _, query := range source.PreRegistration.Queries {
		queries[query.QueryID] = query
	}
	responseAddresses := make(map[string]string, len(source.Responses))
	for i := range source.Responses {
		old := source.Responses[i].SHA256
		query := queries[source.Responses[i].QueryID]
		source.Responses[i].PreRegistrationSHA256 = source.PreRegistration.SHA256
		source.Responses[i].QueryTextSHA256 = query.QueryTextSHA256
		source.Responses[i].BundleSHA256 = query.BundleSHA256
		source.Responses[i].PromptSHA256 = query.PromptSHA256
		source.Responses[i], err = SealRaterResponse(source.Responses[i])
		if err != nil {
			panic(err)
		}
		responseAddresses[old] = source.Responses[i].SHA256
	}
	for i := range source.Grades {
		source.Grades[i].ResponseSHA256 = responseAddresses[source.Grades[i].ResponseSHA256]
		source.Grades[i], err = SealGrade(source.Grades[i])
		if err != nil {
			panic(err)
		}
	}
	*source, err = sealBlindEvidenceSet(*source)
	if err != nil {
		panic(err)
	}
	artifacts := EvaluationArtifacts{Precondition: source.Precondition, PreRegistration: source.PreRegistration, Responses: source.Responses, Grades: source.Grades, Adjudications: source.Adjudications}
	for i := range in.Decisions {
		if in.Decisions[i].Arm != source.Arm || in.Decisions[i].ControlKind != source.ControlKind {
			continue
		}
		outcome, decideErr := DecideQuery(artifacts, queries[in.Decisions[i].QueryID])
		if decideErr != nil {
			panic(decideErr)
		}
		in.Decisions[i].Outcome = outcome
		in.Decisions[i], err = sealBlindDecision(in.Decisions[i], source.SHA256)
		if err != nil {
			panic(err)
		}
	}
}

func copyDecisions(in *QualificationInput, dst, src QualificationArm) {
	passes := map[string]bool{}
	for _, decision := range in.Decisions {
		if decision.Arm == src {
			passes[decision.QueryID] = decision.Outcome.Outcome == GradeOutcomePass
		}
	}
	for queryID, pass := range passes {
		setBlindEvidencePass(in, dst, "", queryID, pass)
	}
	rebuildBlindSubject(in, dst, "")
}

func copyGrades(in *QualificationInput, dst, src QualificationArm) {
	copyDecisions(in, dst, src)
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
	var ids []string
	for _, decision := range in.Decisions {
		if decision.Arm == ArmPotion512 {
			ids = append(ids, decision.QueryID)
		}
	}
	for i, id := range ids {
		setPass(in, ArmPotion512, id, i < 47)
		setPass(in, ArmCodeRank, id, i < 39 || (i >= 47 && i < 64))
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
