package retrieval

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const qualificationBootstrapAlgorithm = "splitmix64-percentile-paired-v1"

type PairedOutcome struct {
	QueryID string `json:"query_id"`
	M1Pass  bool   `json:"m1_pass"`
	M3Pass  bool   `json:"m3_pass"`
}

type Interval struct {
	Point float64 `json:"point"`
	Lower float64 `json:"lower"`
	Upper float64 `json:"upper"`
}

type QualificationInput struct {
	Preregistration QualificationPreregistration `json:"preregistration"`
	Observations    []QualificationObservation   `json:"observations"`
	BuildDigests    []QualificationBuildDigest   `json:"build_digests"`
	Grades          map[QualificationArm][]Grade `json:"grades"`
	OracleControls  []OracleControls             `json:"oracle_controls"`
	Operating       OperatingMeasurements        `json:"operating_budget"`
}

type GateResult struct {
	Name     string `json:"name"`
	Passed   bool   `json:"passed"`
	Observed string `json:"observed"`
	Required string `json:"required"`
}

type QualificationDecision struct {
	Promote bool         `json:"promote"`
	Gates   []GateResult `json:"gates"`
	Branch  string       `json:"branch"`
}

type OperatingMeasurements struct {
	CPUOnly                       bool            `json:"cpu_only"`
	ArtifactBytes                 int64           `json:"artifact_bytes"`
	PeakAdditionalSidecarRSSBytes int64           `json:"peak_additional_sidecar_rss_bytes"`
	QueryEmbedLatencies           []time.Duration `json:"query_embed_latencies"`
	FullReindex                   time.Duration   `json:"full_reindex"`
}

type splitMix64 struct{ state uint64 }

func (r *splitMix64) next() uint64 {
	r.state += 0x9e3779b97f4a7c15
	z := r.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// PairedBootstrap95 computes the repository-owned deterministic paired
// percentile interval. Qualification evaluation admits only 64 outcomes and
// 100,000 resamples; the general shape keeps this primitive independently
// testable without depending on math/rand's implementation.
func PairedBootstrap95(outcomes []PairedOutcome, seed uint64, samples int) Interval {
	if len(outcomes) == 0 || samples <= 0 {
		return Interval{}
	}
	deltas := make([]int8, len(outcomes))
	sum := 0
	for i, outcome := range outcomes {
		if outcome.M3Pass {
			deltas[i]++
			sum++
		}
		if outcome.M1Pass {
			deltas[i]--
			sum--
		}
	}
	rng := splitMix64{state: seed}
	draws := make([]float64, samples)
	for sample := range draws {
		resampled := 0
		for range outcomes {
			resampled += int(deltas[rng.next()%uint64(len(deltas))])
		}
		draws[sample] = float64(resampled) / float64(len(outcomes))
	}
	sort.Float64s(draws)
	lower := int(math.Floor(0.025 * float64(samples)))
	upper := int(math.Ceil(0.975*float64(samples))) - 1
	return Interval{
		Point: float64(sum) / float64(len(outcomes)),
		Lower: draws[lower],
		Upper: draws[upper],
	}
}

type qualificationEvidence struct {
	queryIDs       []string
	strata         map[string]string
	observations   map[QualificationArm]map[string]QualificationObservation
	passes         map[QualificationArm]map[string]bool
	reproducible   map[QualificationArm]bool
	oracleCeiling  int
	stateReady     bool
	fingerprintsOK bool
	noDegradation  bool
}

// EvaluateQualification fails closed on structurally incomplete evidence and
// otherwise emits every promotion condition as a named, indivisible gate.
// This is a development promotion decision, never a release authorization.
func EvaluateQualification(in QualificationInput) (QualificationDecision, error) {
	if err := ValidateQualificationPreregistration(in.Preregistration); err != nil {
		return QualificationDecision{}, fmt.Errorf("embedded-model qualification decision: preregistration: %w", err)
	}
	evidence, err := validateQualificationEvidence(in)
	if err != nil {
		return QualificationDecision{}, err
	}

	outcomes := pairedOutcomes(evidence, ArmCodeRank)
	interval := PairedBootstrap95(outcomes, in.Preregistration.BootstrapSeed, in.Preregistration.BootstrapSamples)
	m3Passes := passCount(evidence.passes[ArmCodeRank])
	pairedGain := pairedGain(evidence, ArmCodeRank)
	weakPositive := positiveStrata(evidence, ArmCodeRank, []string{StratumAmbiguous, StratumArchitectureFlow, StratumNLBehaviour})
	strongLosses := negativeStrata(evidence, ArmCodeRank, []string{StratumConfigDocs, StratumExactIdentifier, StratumExactPath})
	spanGain := completeSpanGain(evidence, ArmCodeRank)
	p95, latencyPresent := operatingP95(in.Operating.QueryEmbedLatencies, in.Preregistration.Thresholds.MinQuerySamples)

	gates := []GateResult{
		gate("m3_blind_passes", m3Passes >= in.Preregistration.Thresholds.MinPasses, fmt.Sprintf("%d", m3Passes), fmt.Sprintf(">=%d", in.Preregistration.Thresholds.MinPasses)),
		gate("paired_final_pass_gain", pairedGain >= in.Preregistration.Thresholds.MinPairedGain, fmt.Sprintf("%+d", pairedGain), fmt.Sprintf(">=+%d", in.Preregistration.Thresholds.MinPairedGain)),
		gate("paired_bootstrap_positive", interval.Lower > 0, fmt.Sprintf("algorithm=%s seed=%d samples=%d point=%.6f interval=[%.6f,%.6f]", qualificationBootstrapAlgorithm, in.Preregistration.BootstrapSeed, in.Preregistration.BootstrapSamples, interval.Point, interval.Lower, interval.Upper), "95% lower bound > 0"),
		gate("weak_strata_positive_gain", weakPositive >= in.Preregistration.Thresholds.MinWeakStrataWithPositiveGain, fmt.Sprintf("%d", weakPositive), fmt.Sprintf(">=%d", in.Preregistration.Thresholds.MinWeakStrataWithPositiveGain)),
		gate("strong_strata_no_loss", strongLosses == 0, fmt.Sprintf("%d negative strata", strongLosses), "0 negative strata"),
		gate("serialized_span_transfer", spanGain >= 1, fmt.Sprintf("%+d complete-span queries", spanGain), ">=+1 complete-span query"),
		gate("two_build_byte_equality", allReproducible(evidence.reproducible), reproducibilityObserved(evidence.reproducible), "two byte-identical builds for every arm, including oracle payload/token digests"),
		gate("artifact_budget", in.Operating.ArtifactBytes > 0 && in.Operating.ArtifactBytes <= in.Preregistration.Thresholds.MaxArtifactBytes, fmt.Sprintf("%d bytes", in.Operating.ArtifactBytes), fmt.Sprintf("1..%d bytes", in.Preregistration.Thresholds.MaxArtifactBytes)),
		gate("sidecar_rss_budget", in.Operating.PeakAdditionalSidecarRSSBytes > 0 && in.Operating.PeakAdditionalSidecarRSSBytes <= in.Preregistration.Thresholds.MaxSidecarRSSBytes, fmt.Sprintf("%d bytes", in.Operating.PeakAdditionalSidecarRSSBytes), fmt.Sprintf("1..%d bytes", in.Preregistration.Thresholds.MaxSidecarRSSBytes)),
		gate("query_embed_p95_budget", latencyPresent && p95 <= time.Duration(in.Preregistration.Thresholds.MaxQueryP95Millis)*time.Millisecond, fmt.Sprintf("samples=%d p95=%s", len(in.Operating.QueryEmbedLatencies), p95), fmt.Sprintf(">=%d positive samples and p95<=%dms", in.Preregistration.Thresholds.MinQuerySamples, in.Preregistration.Thresholds.MaxQueryP95Millis)),
		gate("reindex_budget", in.Operating.FullReindex > 0 && in.Operating.FullReindex <= time.Duration(in.Preregistration.Thresholds.MaxReindexSeconds)*time.Second, in.Operating.FullReindex.String(), fmt.Sprintf("1s..%ds", in.Preregistration.Thresholds.MaxReindexSeconds)),
		gate("cpu_only", in.Operating.CPUOnly, fmt.Sprintf("%t", in.Operating.CPUOnly), "true"),
		gate("state_ready", evidence.stateReady, fmt.Sprintf("%t", evidence.stateReady), "every M1-M3 observation ready"),
		gate("fingerprint_equality", evidence.fingerprintsOK, fmt.Sprintf("%t", evidence.fingerprintsOK), "every M1-M3 model/index fingerprint equals its full preregistered fingerprint"),
		gate("no_degradation", evidence.noDegradation, fmt.Sprintf("%t", evidence.noDegradation), "every M1-M3 observation has no sidecar degradation"),
	}
	promote := true
	for _, result := range gates {
		promote = promote && result.Passed
	}
	decision := QualificationDecision{Promote: promote, Gates: gates}
	decision.Branch = qualificationBranch(in, evidence, promote, pairedGain, spanGain)
	return decision, nil
}

func validateQualificationEvidence(in QualificationInput) (qualificationEvidence, error) {
	arms := []QualificationArm{ArmLexical, ArmPotion512, ArmPotion8192, ArmCodeRank}
	evidence := qualificationEvidence{
		strata: make(map[string]string, 64), observations: make(map[QualificationArm]map[string]QualificationObservation, 4),
		passes: make(map[QualificationArm]map[string]bool, 4), reproducible: make(map[QualificationArm]bool, 4),
		stateReady: true, fingerprintsOK: true, noDegradation: true,
	}
	if len(in.Observations) != len(arms)*64 {
		return evidence, fmt.Errorf("embedded-model qualification decision: got %d observations, want %d", len(in.Observations), len(arms)*64)
	}
	for _, arm := range arms {
		evidence.observations[arm] = make(map[string]QualificationObservation, 64)
	}
	stratumCounts := make(map[string]int, len(qualificationStratumCounts))
	for _, observation := range in.Observations {
		perArm, known := evidence.observations[observation.Arm]
		if !known {
			return evidence, fmt.Errorf("embedded-model qualification decision: observation has unknown arm %q", observation.Arm)
		}
		queryID := strings.TrimSpace(observation.QueryID)
		if queryID == "" {
			return evidence, fmt.Errorf("embedded-model qualification decision: observation has blank query id")
		}
		if _, duplicate := perArm[queryID]; duplicate {
			return evidence, fmt.Errorf("embedded-model qualification decision: duplicate observation for arm %s query %s", observation.Arm, queryID)
		}
		if _, known := qualificationStratumCounts[observation.Stratum]; !known {
			return evidence, fmt.Errorf("embedded-model qualification decision: query %s has unknown stratum %q", queryID, observation.Stratum)
		}
		if !isLowerHexDigest(observation.BundleSHA256, 64) || !isLowerHexDigest(observation.PayloadSHA256, 64) || observation.BundleTokens < 0 || observation.BundleTokens > in.Preregistration.TokenBudget {
			return evidence, fmt.Errorf("embedded-model qualification decision: arm %s query %s has malformed bundle evidence", observation.Arm, queryID)
		}
		if err := validateStageHit(observation.SemanticTop50); err != nil {
			return evidence, fmt.Errorf("embedded-model qualification decision: arm %s query %s semantic stage: %w", observation.Arm, queryID, err)
		}
		if err := validateStageHit(observation.PostFusion); err != nil {
			return evidence, fmt.Errorf("embedded-model qualification decision: arm %s query %s fusion stage: %w", observation.Arm, queryID, err)
		}
		if observation.Arm == ArmLexical {
			if observation.RetrievalState != "lexical_only" || observation.ModelFingerprint != "" || observation.IndexFingerprint != "" || observation.Degraded {
				return evidence, fmt.Errorf("embedded-model qualification decision: query %s is not a declared lexical-only M0 control", queryID)
			}
			if prior, exists := evidence.strata[queryID]; exists && prior != observation.Stratum {
				return evidence, fmt.Errorf("embedded-model qualification decision: query %s changes stratum across arms", queryID)
			}
			evidence.strata[queryID] = observation.Stratum
			stratumCounts[observation.Stratum]++
			evidence.queryIDs = append(evidence.queryIDs, queryID)
		} else {
			if !observation.UnknownTokens.Available || observation.UnknownTokens.Value == nil || *observation.UnknownTokens.Value < 0 || !observation.QueryVectorAllZero.Available || observation.QueryVectorAllZero.Value == nil {
				return evidence, fmt.Errorf("embedded-model qualification decision: arm %s query %s lacks required diagnostics", observation.Arm, queryID)
			}
			evidence.stateReady = evidence.stateReady && observation.RetrievalState == "ready"
			expected := in.Preregistration.Arms[observation.Arm].FingerprintCanonical
			evidence.fingerprintsOK = evidence.fingerprintsOK && observation.ModelFingerprint == expected && observation.IndexFingerprint == expected
			evidence.noDegradation = evidence.noDegradation && !observation.Degraded
		}
		perArm[queryID] = observation
	}
	if len(evidence.queryIDs) != 64 {
		return evidence, fmt.Errorf("embedded-model qualification decision: lexical control has %d unique queries, want 64", len(evidence.queryIDs))
	}
	sort.Strings(evidence.queryIDs)
	for stratum, want := range qualificationStratumCounts {
		if got := stratumCounts[stratum]; got != want {
			return evidence, fmt.Errorf("embedded-model qualification decision: stratum %s has %d queries, want %d", stratum, got, want)
		}
	}
	for _, arm := range arms {
		if len(evidence.observations[arm]) != 64 {
			return evidence, fmt.Errorf("embedded-model qualification decision: arm %s has %d unique observations, want 64", arm, len(evidence.observations[arm]))
		}
		for _, id := range evidence.queryIDs {
			observation, ok := evidence.observations[arm][id]
			if !ok {
				return evidence, fmt.Errorf("embedded-model qualification decision: arm %s lacks query %s", arm, id)
			}
			if observation.Stratum != evidence.strata[id] {
				return evidence, fmt.Errorf("embedded-model qualification decision: arm %s query %s changes stratum", arm, id)
			}
		}
	}
	if len(in.Grades) != len(arms) {
		return evidence, fmt.Errorf("embedded-model qualification decision: got %d graded arms, want 4", len(in.Grades))
	}
	for _, arm := range arms {
		grades, ok := in.Grades[arm]
		if !ok || len(grades) != 64 {
			return evidence, fmt.Errorf("embedded-model qualification decision: arm %s has %d grades, want 64", arm, len(grades))
		}
		evidence.passes[arm] = make(map[string]bool, 64)
		for _, grade := range grades {
			if err := ValidateGrade(grade); err != nil {
				return evidence, fmt.Errorf("embedded-model qualification decision: arm %s grade: %w", arm, err)
			}
			if _, expected := evidence.strata[grade.QueryID]; !expected {
				return evidence, fmt.Errorf("embedded-model qualification decision: arm %s grade names unknown query %s", arm, grade.QueryID)
			}
			if _, duplicate := evidence.passes[arm][grade.QueryID]; duplicate {
				return evidence, fmt.Errorf("embedded-model qualification decision: arm %s duplicates grade for query %s", arm, grade.QueryID)
			}
			evidence.passes[arm][grade.QueryID] = grade.Outcome == GradeOutcomePass
		}
	}
	if err := validateBuildEvidence(in.BuildDigests, arms, &evidence); err != nil {
		return evidence, err
	}
	if err := validateOracleEvidence(in.OracleControls, evidence.queryIDs, &evidence); err != nil {
		return evidence, err
	}
	for _, latency := range in.Operating.QueryEmbedLatencies {
		if latency < 0 {
			return evidence, fmt.Errorf("embedded-model qualification decision: query embedding latency must not be negative")
		}
	}
	if in.Operating.ArtifactBytes < 0 || in.Operating.PeakAdditionalSidecarRSSBytes < 0 || in.Operating.FullReindex < 0 {
		return evidence, fmt.Errorf("embedded-model qualification decision: operating measurements must not be negative")
	}
	return evidence, nil
}

func validateStageHit(hit StageHit) error {
	if hit.Present && hit.BestRank <= 0 {
		return fmt.Errorf("present hit has non-positive rank")
	}
	if !hit.Present && hit.BestRank != 0 {
		return fmt.Errorf("absent hit has a rank")
	}
	return nil
}

func validateBuildEvidence(digests []QualificationBuildDigest, arms []QualificationArm, evidence *qualificationEvidence) error {
	if len(digests) != len(arms)*2 {
		return fmt.Errorf("embedded-model qualification decision: got %d build digests, want %d", len(digests), len(arms)*2)
	}
	byArm := make(map[QualificationArm][]QualificationBuildDigest, len(arms))
	known := make(map[QualificationArm]bool, len(arms))
	for _, arm := range arms {
		known[arm] = true
	}
	for _, digest := range digests {
		if !known[digest.Arm] {
			return fmt.Errorf("embedded-model qualification decision: build digest has unknown arm %q", digest.Arm)
		}
		for _, value := range []string{digest.VectorBytesSHA256, digest.PersistedRowsSHA256, digest.BundlesSHA256, digest.TokenCountsSHA256, digest.OraclePayloadsSHA256, digest.OracleTokenCountsSHA256} {
			if !isLowerHexDigest(value, 64) {
				return fmt.Errorf("embedded-model qualification decision: arm %s has malformed build digest", digest.Arm)
			}
		}
		if digest.Diagnostics.AdmissionTruncations < 0 || digest.Diagnostics.DocumentZeroVectors < 0 {
			return fmt.Errorf("embedded-model qualification decision: arm %s has negative build diagnostics", digest.Arm)
		}
		byArm[digest.Arm] = append(byArm[digest.Arm], digest)
	}
	for _, arm := range arms {
		if len(byArm[arm]) != 2 {
			return fmt.Errorf("embedded-model qualification decision: arm %s has %d builds, want 2", arm, len(byArm[arm]))
		}
		evidence.reproducible[arm] = compareQualificationBuildDigests(byArm[arm][0], byArm[arm][1]) == nil
	}
	return nil
}

func validateOracleEvidence(controls []OracleControls, queryIDs []string, evidence *qualificationEvidence) error {
	if len(controls) != 64 {
		return fmt.Errorf("embedded-model qualification decision: got %d oracle controls, want 64", len(controls))
	}
	expected := make(map[string]bool, len(queryIDs))
	for _, id := range queryIDs {
		expected[id] = true
	}
	seen := make(map[string]bool, 64)
	for _, control := range controls {
		bundles := []struct {
			kind   string
			bundle OracleBundle
		}{
			{OracleControlCurrentCandidatesOraclePacker, control.CurrentCandidatesOraclePacker},
			{OracleControlOracleCandidateCurrentSelector, control.OracleCandidateCurrentSelector},
			{OracleControlOracleCandidateOraclePacker, control.OracleCandidateOraclePacker},
		}
		queryID := bundles[0].bundle.QueryID
		if !expected[queryID] || seen[queryID] {
			return fmt.Errorf("embedded-model qualification decision: duplicate or unknown oracle query %q", queryID)
		}
		seen[queryID] = true
		for _, item := range bundles {
			if item.bundle.QueryID != queryID || item.bundle.ControlKind != item.kind {
				return fmt.Errorf("embedded-model qualification decision: query %s has malformed oracle control %s", queryID, item.kind)
			}
			if err := validateOracleBundleEvidence(item.bundle); err != nil {
				return fmt.Errorf("embedded-model qualification decision: query %s oracle control %s: %w", queryID, item.kind, err)
			}
		}
		if control.OracleCandidateOraclePacker.CompleteGrade3Span {
			evidence.oracleCeiling++
		}
	}
	return nil
}

func validateOracleBundleEvidence(bundle OracleBundle) error {
	if strings.TrimSpace(bundle.OutputName) == "" || strings.TrimSpace(bundle.CandidateProvenance) == "" || !isLowerHexDigest(bundle.CandidateSHA256, 64) {
		return fmt.Errorf("missing output or candidate provenance")
	}
	if bundle.InjectedRows < 0 || (!bundle.Injected && bundle.InjectedRows != 0) || (bundle.Injected && bundle.InjectedRows == 0) {
		return fmt.Errorf("inconsistent injection provenance")
	}
	if bundle.TokenCount <= 0 || bundle.TokenCount > QualificationTokenBudget || len(bundle.Payload.Bytes) == 0 ||
		bundle.Payload.SHA256 != SHA256Hex(bundle.Payload.Bytes) || bundle.Payload.ByteCount != len(bundle.Payload.Bytes) {
		return fmt.Errorf("malformed or over-budget payload")
	}
	matchedTokenCount := false
	for _, count := range bundle.Payload.TokenCounts {
		if strings.TrimSpace(count.TokenizerID) == "" || count.Tokens < 0 {
			return fmt.Errorf("malformed payload token count")
		}
		if count.Tokens == bundle.TokenCount {
			matchedTokenCount = true
		}
	}
	if !matchedTokenCount {
		return fmt.Errorf("real token count is not bound to payload")
	}
	return nil
}

func gate(name string, passed bool, observed, required string) GateResult {
	return GateResult{Name: name, Passed: passed, Observed: observed, Required: required}
}

func pairedOutcomes(e qualificationEvidence, arm QualificationArm) []PairedOutcome {
	out := make([]PairedOutcome, 0, len(e.queryIDs))
	for _, id := range e.queryIDs {
		out = append(out, PairedOutcome{QueryID: id, M1Pass: e.passes[ArmPotion512][id], M3Pass: e.passes[arm][id]})
	}
	return out
}

func passCount(values map[string]bool) int {
	total := 0
	for _, pass := range values {
		if pass {
			total++
		}
	}
	return total
}
func pairedGain(e qualificationEvidence, arm QualificationArm) int {
	return passCount(e.passes[arm]) - passCount(e.passes[ArmPotion512])
}

func positiveStrata(e qualificationEvidence, arm QualificationArm, strata []string) int {
	wanted := make(map[string]bool, len(strata))
	for _, stratum := range strata {
		wanted[stratum] = true
	}
	deltas := make(map[string]int, len(strata))
	for _, id := range e.queryIDs {
		if wanted[e.strata[id]] {
			deltas[e.strata[id]] += boolInt(e.passes[arm][id]) - boolInt(e.passes[ArmPotion512][id])
		}
	}
	count := 0
	for _, stratum := range strata {
		if deltas[stratum] > 0 {
			count++
		}
	}
	return count
}

func negativeStrata(e qualificationEvidence, arm QualificationArm, strata []string) int {
	wanted := make(map[string]bool, len(strata))
	for _, stratum := range strata {
		wanted[stratum] = true
	}
	deltas := make(map[string]int, len(strata))
	for _, id := range e.queryIDs {
		if wanted[e.strata[id]] {
			deltas[e.strata[id]] += boolInt(e.passes[arm][id]) - boolInt(e.passes[ArmPotion512][id])
		}
	}
	count := 0
	for _, stratum := range strata {
		if deltas[stratum] < 0 {
			count++
		}
	}
	return count
}

func completeSpanGain(e qualificationEvidence, arm QualificationArm) int {
	delta := 0
	for _, id := range e.queryIDs {
		delta += boolInt(e.observations[arm][id].CompleteGrade3Span) - boolInt(e.observations[ArmPotion512][id].CompleteGrade3Span)
	}
	return delta
}

func semanticStageGain(e qualificationEvidence, arm QualificationArm) int {
	delta := 0
	for _, id := range e.queryIDs {
		delta += boolInt(e.observations[arm][id].SemanticTop50.Present) - boolInt(e.observations[ArmPotion512][id].SemanticTop50.Present)
	}
	return delta
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func allReproducible(values map[QualificationArm]bool) bool {
	for _, arm := range []QualificationArm{ArmLexical, ArmPotion512, ArmPotion8192, ArmCodeRank} {
		if !values[arm] {
			return false
		}
	}
	return true
}

func reproducibilityObserved(values map[QualificationArm]bool) string {
	parts := make([]string, 0, 4)
	for _, arm := range []QualificationArm{ArmLexical, ArmPotion512, ArmPotion8192, ArmCodeRank} {
		parts = append(parts, fmt.Sprintf("%s=%t", arm, values[arm]))
	}
	return strings.Join(parts, ",")
}

func operatingP95(values []time.Duration, minimum int) (time.Duration, bool) {
	if len(values) < minimum || minimum <= 0 {
		return 0, false
	}
	copyOfValues := append([]time.Duration(nil), values...)
	for _, value := range copyOfValues {
		if value <= 0 {
			return 0, false
		}
	}
	sort.Slice(copyOfValues, func(i, j int) bool { return copyOfValues[i] < copyOfValues[j] })
	index := int(math.Ceil(0.95*float64(len(copyOfValues)))) - 1
	return copyOfValues[index], true
}

func armMeetsQualityCriteria(in QualificationInput, evidence qualificationEvidence, arm QualificationArm) bool {
	if passCount(evidence.passes[arm]) < in.Preregistration.Thresholds.MinPasses || pairedGain(evidence, arm) < in.Preregistration.Thresholds.MinPairedGain ||
		positiveStrata(evidence, arm, []string{StratumAmbiguous, StratumArchitectureFlow, StratumNLBehaviour}) < in.Preregistration.Thresholds.MinWeakStrataWithPositiveGain ||
		negativeStrata(evidence, arm, []string{StratumConfigDocs, StratumExactIdentifier, StratumExactPath}) != 0 || completeSpanGain(evidence, arm) < 1 || !evidence.reproducible[arm] {
		return false
	}
	return PairedBootstrap95(pairedOutcomes(evidence, arm), in.Preregistration.BootstrapSeed, in.Preregistration.BootstrapSamples).Lower > 0
}

func qualificationBranch(in QualificationInput, evidence qualificationEvidence, promote bool, m3Gain, m3SpanGain int) string {
	m2Quality := armMeetsQualityCriteria(in, evidence, ArmPotion8192)
	m3Quality := armMeetsQualityCriteria(in, evidence, ArmCodeRank)
	if m2Quality && m3Quality {
		if promote && m3Gain > pairedGain(evidence, ArmPotion8192) {
			return "promote_coderank_to_product_integration"
		}
		return "prefer_simpler_potion_candidate"
	}
	if promote {
		return "promote_coderank_to_product_integration"
	}
	if semanticStageGain(evidence, ArmCodeRank) > 0 && (m3SpanGain <= 0 || m3Gain <= 0) {
		return "investigate_projection_or_fusion"
	}
	if m2Quality && !m3Quality {
		return "design_potion_admission_candidate"
	}
	if evidence.oracleCeiling < in.Preregistration.Thresholds.MinPasses {
		return "representation_or_budget_ceiling"
	}
	return "stop_no_new_holdout"
}
