package retrieval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	releaseEvidenceFixtureCandidate = "0123456789abcdef0123456789abcdef01234567"
	releaseEvidenceFixtureRepo      = "cccccccccccccccccccccccccccccccccccccccc"
	releaseEvidenceFixtureVersion   = "retrieval/test"
	releaseEvidenceFixtureTokenizer = "fixture-real-tokenizer"
)

type releaseEvidenceWorld struct {
	root     string
	manifest ReleaseEvidenceManifest
	counters map[string]PayloadCounter
}

func TestValidateReleaseEvidence_ValidFixtureIsReleaseReady(t *testing.T) {
	w := newReleaseEvidenceWorld(t)
	assessment, err := ValidateReleaseEvidence(w.root, w.manifest, w.counters)
	if err != nil {
		t.Fatalf("ValidateReleaseEvidence: %v", err)
	}
	if !assessment.ReleaseReady {
		t.Fatalf("assessment = %+v, want release-ready", assessment)
	}
	if assessment.CandidateSHA != releaseEvidenceFixtureCandidate || assessment.ManifestSHA256 != w.manifest.SHA256 {
		t.Fatalf("assessment binding = candidate %q manifest %q", assessment.CandidateSHA, assessment.ManifestSHA256)
	}
	if assessment.FilesChecked != 11 {
		t.Fatalf("files_checked = %d, want four artifacts plus seven identities", assessment.FilesChecked)
	}
	wantBindings := []string{"ranking_report", "bundle_coverage", "blind_smoke_outcome", "savings_aggregate"}
	if !releaseEvidenceStringsEqual(assessment.CandidateBindings, wantBindings) {
		t.Fatalf("candidate bindings = %v, want %v", assessment.CandidateBindings, wantBindings)
	}
}

func TestValidateReleaseEvidence_MissingEvidenceFailsClosed(t *testing.T) {
	w := newReleaseEvidenceWorld(t)
	w.manifest.BlindSmokeOutcome.Path = "evidence/absent-outcome.json"
	w.manifest = mustSealReleaseEvidence(t, w.manifest)
	_, err := ValidateReleaseEvidence(w.root, w.manifest, w.counters)
	if err == nil || !strings.Contains(err.Error(), "absent-outcome.json") {
		t.Fatalf("missing evidence error = %v", err)
	}
}

func TestValidateReleaseEvidence_DigestDriftFailsClosed(t *testing.T) {
	w := newReleaseEvidenceWorld(t)
	path := filepath.Join(w.root, filepath.FromSlash(w.manifest.RankingReport.Path))
	if err := os.WriteFile(path, []byte(`{"tampered":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ValidateReleaseEvidence(w.root, w.manifest, w.counters)
	if err == nil || !strings.Contains(err.Error(), "digest drift") {
		t.Fatalf("digest drift error = %v", err)
	}
}

func TestValidateReleaseEvidence_CandidateMismatchFailsAfterDigestValidation(t *testing.T) {
	w := newReleaseEvidenceWorld(t)
	wrong := strings.Repeat("f", 40)
	report := releaseEvidenceRankingReport(wrong, releaseEvidenceIdentityDigest(w.manifest, "development_dataset"))
	w.manifest.RankingReport = writeReleaseEvidenceJSON(t, w.root, "evidence/ranking.json", report)
	w.manifest = mustSealReleaseEvidence(t, w.manifest)
	_, err := ValidateReleaseEvidence(w.root, w.manifest, w.counters)
	if err == nil || !strings.Contains(err.Error(), "ranking_report carries candidate_sha") || !strings.Contains(err.Error(), wrong) {
		t.Fatalf("candidate mismatch error = %v", err)
	}
}

func TestValidateReleaseEvidence_SavingsCandidateMismatchFailsClosed(t *testing.T) {
	w := newReleaseEvidenceWorld(t)
	var savings ReleaseSavingsEvidence
	readReleaseEvidenceJSON(t, w.root, w.manifest.SavingsAggregate.Path, &savings)
	savings.CandidateSHA = strings.Repeat("e", 40)
	w.manifest.SavingsAggregate = writeReleaseEvidenceJSON(t, w.root, "evidence/savings.json", savings)
	w.manifest = mustSealReleaseEvidence(t, w.manifest)
	_, err := ValidateReleaseEvidence(w.root, w.manifest, w.counters)
	if err == nil || !strings.Contains(err.Error(), "savings_aggregate carries candidate_sha") {
		t.Fatalf("savings candidate mismatch error = %v", err)
	}
}

func TestValidateReleaseEvidence_DatasetMismatchFailsClosed(t *testing.T) {
	w := newReleaseEvidenceWorld(t)
	var coverage TaskContextMeasurement
	readReleaseEvidenceJSON(t, w.root, w.manifest.BundleCoverage.Path, &coverage)
	coverage.Dataset.SourceSHA256 = strings.Repeat("e", 64)
	w.manifest.BundleCoverage = writeReleaseEvidenceJSON(t, w.root, "evidence/coverage.json", coverage)
	w.manifest = mustSealReleaseEvidence(t, w.manifest)
	_, err := ValidateReleaseEvidence(w.root, w.manifest, w.counters)
	if err == nil || !strings.Contains(err.Error(), "development dataset") {
		t.Fatalf("dataset mismatch error = %v", err)
	}
}

func TestValidateReleaseEvidence_SavingsPopulationMustDeriveFromReleaseDataset(t *testing.T) {
	w := newReleaseEvidenceWorld(t)
	var savings ReleaseSavingsEvidence
	readReleaseEvidenceJSON(t, w.root, w.manifest.SavingsAggregate.Path, &savings)
	savings.Result.Input.Population[0].FamilyID = "silently-changed-family"
	result, err := ComputeSavingsAggregate(savings.Result.Input, w.counters)
	if err != nil {
		t.Fatal(err)
	}
	savings.Result = result
	w.manifest.SavingsAggregate = writeReleaseEvidenceJSON(t, w.root, "evidence/savings.json", savings)
	w.manifest = mustSealReleaseEvidence(t, w.manifest)
	_, err = ValidateReleaseEvidence(w.root, w.manifest, w.counters)
	if err == nil || !strings.Contains(err.Error(), "differs from the release dataset derivation") {
		t.Fatalf("population derivation error = %v", err)
	}
}

func TestValidateReleaseEvidence_TargetMissFailsClosed(t *testing.T) {
	w := newReleaseEvidenceWorld(t)
	devSHA := releaseEvidenceIdentityDigest(w.manifest, "development_dataset")
	report := releaseEvidenceRankingReport(releaseEvidenceFixtureCandidate, devSHA)
	report.Reproducible.Baselines[0].Queries[0].Metrics.NDCG10 = 0
	w.manifest.RankingReport = writeReleaseEvidenceJSON(t, w.root, "evidence/ranking.json", report)
	w.manifest = mustSealReleaseEvidence(t, w.manifest)
	_, err := ValidateReleaseEvidence(w.root, w.manifest, w.counters)
	if err == nil || !strings.Contains(err.Error(), "target miss") {
		t.Fatalf("target miss error = %v", err)
	}
}

func TestValidateReleaseEvidence_SavingsDriftFailsClosed(t *testing.T) {
	w := newReleaseEvidenceWorld(t)
	var savings ReleaseSavingsEvidence
	readReleaseEvidenceJSON(t, w.root, w.manifest.SavingsAggregate.Path, &savings)
	savings.Result.Median.Numerator = "999"
	w.manifest.SavingsAggregate = writeReleaseEvidenceJSON(t, w.root, "evidence/savings.json", savings)
	w.manifest = mustSealReleaseEvidence(t, w.manifest)
	_, err := ValidateReleaseEvidence(w.root, w.manifest, w.counters)
	if err == nil || !strings.Contains(err.Error(), "does not recompute exactly") {
		t.Fatalf("savings drift error = %v", err)
	}
}

func TestValidateReleaseEvidence_MissingTokenizerCounterFailsClosed(t *testing.T) {
	w := newReleaseEvidenceWorld(t)
	delete(w.counters, releaseEvidenceFixtureTokenizer)
	_, err := ValidateReleaseEvidence(w.root, w.manifest, w.counters)
	if err == nil || !strings.Contains(err.Error(), "counter") {
		t.Fatalf("missing counter error = %v", err)
	}
}

func TestValidateReleaseEvidence_NonPositiveSavingsCannotClaimFewerTokens(t *testing.T) {
	w := newReleaseEvidenceWorld(t)
	result := releaseEvidenceSavingsResult(t, releaseEvidenceIdentityDigest(w.manifest, "release_dataset"), w.counters, false)
	w.manifest.SavingsAggregate = writeReleaseEvidenceJSON(t, w.root, "evidence/savings.json", ReleaseSavingsEvidence{
		Version: ReleaseSavingsEvidenceVersion, CandidateSHA: releaseEvidenceFixtureCandidate, Result: result,
	})
	w.manifest = mustSealReleaseEvidence(t, w.manifest)
	_, err := ValidateReleaseEvidence(w.root, w.manifest, w.counters)
	if err == nil || !strings.Contains(err.Error(), "exact positive") {
		t.Fatalf("non-positive savings error = %v", err)
	}
}

func newReleaseEvidenceWorld(t *testing.T) releaseEvidenceWorld {
	t.Helper()
	root := t.TempDir()
	devRef := writeReleaseEvidenceBytes(t, root, "identities/development-dataset.json", []byte("development dataset\n"))
	releaseRef := writeReleaseEvidenceJSON(t, root, "identities/release-dataset.json", releaseEvidenceDataset())
	vocabRef := writeReleaseEvidenceBytes(t, root, "identities/tokenizer.bin", []byte("tokenizer vocabulary\n"))
	counters := releaseEvidenceCounters(vocabRef.ExpectedSHA256)
	targets := releaseEvidenceTargets(devRef.ExpectedSHA256)
	manifest := ReleaseEvidenceManifest{
		Version:           ReleaseEvidenceManifestVersion,
		CandidateSHA:      releaseEvidenceFixtureCandidate,
		RankingReport:     writeReleaseEvidenceJSON(t, root, "evidence/ranking.json", releaseEvidenceRankingReport(releaseEvidenceFixtureCandidate, devRef.ExpectedSHA256)),
		BundleCoverage:    writeReleaseEvidenceJSON(t, root, "evidence/coverage.json", releaseEvidenceCoverage(devRef.ExpectedSHA256)),
		BlindSmokeOutcome: writeReleaseEvidenceJSON(t, root, "evidence/smoke.json", releaseEvidenceSmoke()),
	}
	result := releaseEvidenceSavingsResult(t, releaseRef.ExpectedSHA256, counters, true)
	manifest.SavingsAggregate = writeReleaseEvidenceJSON(t, root, "evidence/savings.json", ReleaseSavingsEvidence{
		Version: ReleaseSavingsEvidenceVersion, CandidateSHA: releaseEvidenceFixtureCandidate, Result: result,
	})
	identityRefs := map[string]ReleaseEvidenceFile{
		"budgets":              writeReleaseEvidenceBytes(t, root, "identities/budgets.json", []byte("budgets\n")),
		"development_dataset":  devRef,
		"grading_rubric":       writeReleaseEvidenceBytes(t, root, "identities/rubric.md", []byte("rubric\n")),
		"methodology":          writeReleaseEvidenceBytes(t, root, "identities/methodology.md", []byte("methodology\n")),
		"release_dataset":      releaseRef,
		"targets":              writeReleaseEvidenceJSON(t, root, "identities/targets.json", targets),
		"tokenizer_vocabulary": vocabRef,
	}
	for _, name := range requiredReleaseIdentityNames {
		manifest.Identities = append(manifest.Identities, ReleaseEvidenceIdentity{Name: name, ReleaseEvidenceFile: identityRefs[name]})
	}
	return releaseEvidenceWorld{root: root, manifest: mustSealReleaseEvidence(t, manifest), counters: counters}
}

func releaseEvidenceDataset() Dataset {
	judgement := func(path string) Judgement {
		return Judgement{Path: path, StartLine: 1, EndLine: 1, Anchor: "answer", Grade: GradeMax,
			Reason: "fixture answer", Annotator: "fixture annotator", Reviewer: "fixture reviewer"}
	}
	return Dataset{
		SchemaVersion: SchemaVersion, ID: "cobra-release", Repo: "cobra", RepoSHA: releaseEvidenceFixtureRepo,
		Language: "en", EvidenceClass: EvidenceClassAgentHumanReviewed,
		Queries: []Query{
			{ID: "q-dev", Stratum: StratumNLBehaviour, Language: "en", Split: SplitDev, Text: "development question",
				FamilyID: "family-dev", Provenance: "fixture", Judgements: []Judgement{judgement("dev.go")}},
			{ID: "q-holdout", Stratum: StratumArchitectureFlow, Language: "en", Split: SplitHoldout, Text: "holdout question",
				FamilyID: "family-holdout", Provenance: "fixture", Judgements: []Judgement{judgement("holdout.go")}},
		},
	}
}

func releaseEvidenceRankingReport(candidate, datasetSHA string) Report {
	perfect := func(id, stratum string) QueryResult {
		return QueryResult{ID: id, Stratum: stratum, Split: SplitDev, Metrics: QueryMetrics{
			Scored: true, Top1: 1, Recall5: 1, Recall10: 1, MRR10: 1, NDCG10: 1,
			RecallAtTokens: map[string]float64{"1200": 1},
		}}
	}
	return Report{
		FormatVersion: FormatVersion, HarnessVersion: HarnessVersion, ScorerVersion: ScorerVersion,
		Reproducible: Reproducible{
			CandidateSHA: candidate, Repo: RepoRef{Name: "cobra", SHA: releaseEvidenceFixtureRepo},
			Dataset: DatasetRef{ID: "cobra-dev", SHA256: datasetSHA}, TokenBudgets: []int{1200},
			EmbedderSpec: &EmbedderSpec{Fingerprint: "fixture-embedder"},
			Baselines: []BaselineResult{
				{
					Name: GateBaseline, Status: BaselineStatusOK,
					Queries: []QueryResult{
						perfect("q-nl", StratumNLBehaviour),
						perfect("q-arch", StratumArchitectureFlow),
						perfect("q-exact", StratumExactIdentifier),
					},
				},
			},
		},
	}
}

func releaseEvidenceTargets(datasetSHA string) Targets {
	fusion := func() StratumTarget {
		return StratumTarget{DevQueries: 1, Oracle: map[string]float64{TargetMetric: 1}, FusionTarget: &FusionTarget{
			Metric: TargetMetric, BestBaseline: BaselineSemanticNameOnly, BestValue: 0.4, MinDelta: FusionMinDelta, MustReach: 0.5}}
	}
	return Targets{
		SchemaVersion: TargetsSchemaVersion, Metric: TargetMetric, ConceptualStrata: append([]string(nil), ConceptualStrata...),
		FusionMinDelta: FusionMinDelta, Split: SplitDev,
		DerivedFrom: DerivedFrom{SHA256: strings.Repeat("d", 64), Repo: "cobra", RepoSHA: releaseEvidenceFixtureRepo, Dataset: "cobra-dev", DatasetSHA256: datasetSHA},
		Strata: map[string]StratumTarget{
			StratumNLBehaviour:      fusion(),
			StratumArchitectureFlow: fusion(),
			StratumExactIdentifier: {DevQueries: 1, NoRegression: &NoRegression{
				Metric: MetricTop1, Baseline: BaselineSemanticNameOnly, Floor: 0.5,
			}},
		},
		BundleCoverage: &BundleCoverage{Population: BundleCoveragePopulation{Dataset: "cobra-dev", Split: SplitDev, Stratum: StratumNLBehaviour, QueryIDs: []string{"q-coverage"}, N: 1},
			TokenBudget: 1200, Threshold: BundleCoverageThreshold{CoveredQueriesRequired: 1, MaxMisses: 0}},
	}
}

func releaseEvidenceCoverage(datasetSHA string) TaskContextMeasurement {
	return TaskContextMeasurement{
		FormatVersion: TaskContextFormatVersion, HarnessVersion: TaskContextHarnessVersion, ScorerVersion: TaskContextScorerVersion,
		EligibleForThreshold: true, Eligibility: []TaskContextEligibilityCheck{{Name: "fixture", Passed: true, Detail: "fixture"}},
		Candidate: TaskContextCandidate{SHA: releaseEvidenceFixtureCandidate, BaseSHA: releaseEvidenceFixtureCandidate,
			DiffSHA256: strings.Repeat("a", 64), SourceFiles: []TaskContextFileDigest{{File: "fixture.go", SHA256: strings.Repeat("b", 64)}}},
		Dataset: TaskContextDatasetRef{ID: "cobra-dev", SourceSHA256: datasetSHA, QueryIDs: []string{"q-coverage"}},
		Repo:    TaskContextRepoRef{Name: "cobra", SHA: releaseEvidenceFixtureRepo}, Retrieval: TaskContextRetrievalRef{Version: releaseEvidenceFixtureVersion},
		Bundle:    TaskContextBundleRef{MethodVersion: SavingsCandidateMethod, TokenBudget: SavingsCandidateBudget},
		Aggregate: TaskContextAggregate{CoveredQueries: 1, TotalQueries: 1}, Queries: []TaskContextQueryResult{{ID: "q-coverage", Covered: true}},
	}
}

func releaseEvidenceSmoke() EvaluationOutcome {
	return EvaluationOutcome{ContractVersion: QrelBlindSmokeContractVersion2, Release: ReleaseYes,
		CaptureBinding: CaptureBindingAssessment{Bound: true, CaptureVersion: CandidateCaptureVersion,
			CandidateSHA: releaseEvidenceFixtureCandidate, CheckoutSHA: releaseEvidenceFixtureRepo}}
}

func releaseEvidenceSavingsResult(t *testing.T, datasetSHA string, counters map[string]PayloadCounter, positive bool) SavingsAggregateResult {
	t.Helper()
	target := RecallTarget{Grade: SavingsGrade, RequiredSpans: 1, TotalSpans: 1}
	in := SavingsAggregateInput{
		Contract: FrozenMeasurementContract(), DatasetSHA256: datasetSHA, RepoSHA: releaseEvidenceFixtureRepo,
		CandidateVersion: releaseEvidenceFixtureVersion, ComparatorVersion: "grepread/test", TokenizerID: releaseEvidenceFixtureTokenizer,
		TokenizerVocabularySHA256: counters[releaseEvidenceFixtureTokenizer].VocabularySHA256, Confidence: FrozenConfidenceSpec(),
		Population: []SavingsPopulationMember{
			{QueryID: "q-dev", FamilyID: "family-dev", Split: SplitDev, Stratum: StratumNLBehaviour, Target: target},
			{QueryID: "q-holdout", FamilyID: "family-holdout", Split: SplitHoldout, Stratum: StratumArchitectureFlow, Target: target}},
	}
	for _, member := range in.Population {
		candidateRaw := []byte("candidate")
		if !positive {
			candidateRaw = []byte(strings.Repeat("candidate expensive ", 10))
		}
		candidate := releaseEvidencePayload(1, PayloadBoundaryCandidate, PayloadOperationTaskContext, candidateRaw, counters)
		grep := releaseEvidencePayload(1, PayloadBoundaryGrepRead, PayloadOperationGrep, []byte("grep response with more bytes"), counters)
		read := releaseEvidencePayload(2, PayloadBoundaryGrepRead, PayloadOperationRead, []byte("read response with still more answer bytes"), counters)
		candidateTokens := releaseEvidencePayloadTokens([]PreservedPayload{candidate}, releaseEvidenceFixtureTokenizer)
		grepTokens := releaseEvidencePayloadTokens([]PreservedPayload{grep, read}, releaseEvidenceFixtureTokenizer)
		in.Observations = append(in.Observations, SavingsObservation{QueryID: member.QueryID,
			Candidate: SavingsArmOutcome{Target: target, Status: SavingsOutcomeReached, StopReason: SavingsStopOneCallComplete,
				Grade3SpansAtPrefix: 1, ConsumedPayloadSlices: 1, TokensToTarget: releaseEvidenceInt(candidateTokens), Payloads: []PreservedPayload{candidate}},
			GrepRead: SavingsArmOutcome{Target: target, Status: SavingsOutcomeReached, StopReason: SavingsStopExhausted,
				Grade3SpansAtPrefix: 1, ConsumedPayloadSlices: 2, TokensToTarget: releaseEvidenceInt(grepTokens), Payloads: []PreservedPayload{grep, read}}})
	}
	result, err := ComputeSavingsAggregate(in, counters)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func releaseEvidenceCounters(vocabularySHA string) map[string]PayloadCounter {
	return map[string]PayloadCounter{
		TokenizerID: {TokenizerID: TokenizerID, Count: func(raw []byte) (int, error) { return len(strings.Fields(string(raw))), nil }},
		releaseEvidenceFixtureTokenizer: {TokenizerID: releaseEvidenceFixtureTokenizer, VocabularySHA256: vocabularySHA,
			Count: func(raw []byte) (int, error) { return len(raw), nil }},
	}
}

func releaseEvidencePayload(sequence int, boundary PayloadBoundary, operation string, raw []byte, counters map[string]PayloadCounter) PreservedPayload {
	counts := make([]PayloadTokenCount, 0, 2)
	for _, id := range []string{TokenizerID, releaseEvidenceFixtureTokenizer} {
		counter := counters[id]
		tokens, _ := counter.Count(raw)
		counts = append(counts, PayloadTokenCount{TokenizerID: id, VocabularySHA256: counter.VocabularySHA256, Tokens: tokens})
	}
	return PreservedPayload{Sequence: sequence, Boundary: boundary, Operation: operation, Bytes: raw,
		SHA256: SHA256Hex(raw), ByteCount: len(raw), TokenCounts: counts}
}

func releaseEvidencePayloadTokens(payloads []PreservedPayload, tokenizer string) int {
	total := 0
	for _, payload := range payloads {
		for _, count := range payload.TokenCounts {
			if count.TokenizerID == tokenizer {
				total += count.Tokens
			}
		}
	}
	return total
}

func releaseEvidenceInt(v int) *int { return &v }

func releaseEvidenceIdentityDigest(manifest ReleaseEvidenceManifest, name string) string {
	for _, identity := range manifest.Identities {
		if identity.Name == name {
			return identity.ExpectedSHA256
		}
	}
	return ""
}

func readReleaseEvidenceJSON(t *testing.T, root, path string, value any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, value); err != nil {
		t.Fatal(err)
	}
}

func writeReleaseEvidenceJSON(t *testing.T, root, path string, value any) ReleaseEvidenceFile {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return writeReleaseEvidenceBytes(t, root, path, raw)
}

func writeReleaseEvidenceBytes(t *testing.T, root, path string, raw []byte) ReleaseEvidenceFile {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return ReleaseEvidenceFile{Path: path, ExpectedSHA256: SHA256Hex(raw)}
}

func mustSealReleaseEvidence(t *testing.T, manifest ReleaseEvidenceManifest) ReleaseEvidenceManifest {
	t.Helper()
	sealed, err := SealReleaseEvidenceManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}
