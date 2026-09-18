package retrieval

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
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
	Dataset         *Loaded                      `json:"dataset"`
	Observations    []QualificationObservation   `json:"observations"`
	BuildDigests    []QualificationBuildDigest   `json:"build_digests"`
	BlindEvidence   []BlindEvidenceSet           `json:"blind_evidence"`
	Decisions       []BlindDecision              `json:"decisions"`
	OracleEvidence  QualificationOracleEvidence  `json:"oracle_evidence"`
	Operating       OperatingEvidence            `json:"operating_evidence"`
}

// BlindEvidenceSet is the closed, content-addressed source record from which
// the 64 final decisions for exactly one arm or oracle control are derived.
type BlindEvidenceSet struct {
	Arm             QualificationArm   `json:"arm,omitempty"`
	ControlKind     string             `json:"control_kind,omitempty"`
	Precondition    PreconditionRecord `json:"precondition"`
	PreRegistration PreRegistration    `json:"pre_registration"`
	Responses       []RaterResponse    `json:"responses"`
	Grades          []Grade            `json:"grades"`
	Adjudications   []Adjudication     `json:"adjudications"`
	SHA256          string             `json:"sha256"`
}

// BlindDecision binds one final two-rater/adjudication outcome to exactly one
// arm or oracle control and to the exact payload and prompt identities.
type BlindDecision struct {
	Arm                QualificationArm `json:"arm,omitempty"`
	ControlKind        string           `json:"control_kind,omitempty"`
	QueryID            string           `json:"query_id"`
	Stratum            string           `json:"stratum"`
	PayloadSHA256      string           `json:"payload_sha256"`
	ReaderPromptSHA256 string           `json:"reader_prompt_sha256"`
	GraderPromptSHA256 string           `json:"grader_prompt_sha256"`
	Outcome            QueryOutcome     `json:"outcome"`
	EvidenceSHA256     string           `json:"evidence_sha256"`
	SHA256             string           `json:"sha256"`
}

// sealBlindDecision is deliberately internal: a decision becomes trusted only
// after validation against the source evidence whose address it names.
func sealBlindDecision(decision BlindDecision, evidenceSHA string) (BlindDecision, error) {
	decision.EvidenceSHA256 = evidenceSHA
	decision.SHA256 = ""
	decisionSHA, err := ContentAddress(decision, func(v *BlindDecision) { v.SHA256 = "" })
	if err != nil {
		return BlindDecision{}, err
	}
	decision.SHA256 = decisionSHA
	return decision, nil
}

func sealBlindEvidenceSet(source BlindEvidenceSet) (BlindEvidenceSet, error) {
	address, err := ContentAddress(source, func(v *BlindEvidenceSet) { v.SHA256 = "" })
	if err != nil {
		return BlindEvidenceSet{}, err
	}
	source.SHA256 = address
	return source, nil
}

type GateResult struct {
	Name     string `json:"name"`
	Passed   bool   `json:"passed"`
	Observed string `json:"observed"`
	Required string `json:"required"`
}

type QualificationDecision struct {
	Promote  bool             `json:"promote"`
	Gates    []GateResult     `json:"gates"`
	Branch   string           `json:"branch"`
	Evidence []EvidenceRecord `json:"evidence"`
}

type EvidenceRecord struct {
	Name      string `json:"name"`
	Algorithm string `json:"algorithm"`
	Observed  string `json:"observed"`
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
	queryIDs             []string
	strata               map[string]string
	observations         map[QualificationArm]map[string]QualificationObservation
	passes               map[QualificationArm]map[string]bool
	reproducible         map[QualificationArm]bool
	oraclePasses         map[string]map[string]bool
	oraclePayloads       map[string]map[string]string
	armValid             map[QualificationArm]bool
	diagnosticsAvailable bool
	stateReady           bool
	fingerprintsOK       bool
	// fingerprintMismatch names the FIRST observation whose identity differed
	// and how, so the fingerprint_equality gate reports which value was
	// expected and which was observed instead of a bare false. A qualification
	// run costs half an hour of compute; a refusal that names neither side is
	// paid for twice.
	fingerprintMismatch string
	noDegradation       bool
	// graphGenerationConsistent records whether every semantic arm and every
	// observation of THIS run named one and the same graph generation.
	//
	// It is a separate gate from fingerprintsOK because it proves a different
	// thing. fingerprintsOK proves each arm measured the embedding space that
	// was preregistered (fields 0-6). graphGenerationConsistent proves all of
	// them measured the SAME GRAPH — the property that makes the paired
	// arm-vs-arm comparison meaningful, and the only property the random
	// eighth field can attest at all. Nothing checked it before; the
	// byte-for-byte pin comparison it replaces could not, because it demanded
	// a value no run can reproduce.
	graphGenerationConsistent bool
}

// qualificationFingerprintGateObserved renders the fingerprint_equality gate's
// observed value. A passing gate reports "true"; a failing one names the first
// identity that differed and both sides of it, because "false" alone costs the
// operator another half-hour run to learn what moved.
func qualificationFingerprintGateObserved(evidence qualificationEvidence) string {
	if evidence.fingerprintsOK {
		return "true"
	}
	if evidence.fingerprintMismatch == "" {
		return "false"
	}
	return "false (" + evidence.fingerprintMismatch + ")"
}

// qualificationRunGraphGeneration is the run's bound graph generation plus the
// observation that bound it, so a later divergence can name BOTH sides.
type qualificationRunGraphGeneration struct {
	value  string
	bound  bool
	source string
}

// bindQualificationGraphGeneration binds the run's graph generation to the
// first semantic observation that reports a decodable one, and refuses any
// later observation that names a different one.
//
// Only IndexFingerprint is read. ModelFingerprint is a MODEL ID, not a
// canonical fingerprint (see QualificationObservation), so it carries no eighth
// field at all; decoding it as a canonical always failed, which silently held
// graphGenerationConsistent false for every real run and made this gate
// unpassable rather than strict.
//
// A fingerprint that does not decode is NOT a hard error here: that is exactly
// what the fingerprint_equality gate already reports, and turning a malformed
// fingerprint into an abort would replace a named failing gate with an opaque
// refusal. It does clear graphGenerationConsistent, because a run whose
// fingerprints cannot be read has not shown that its arms share a graph.
func bindQualificationGraphGeneration(bound *qualificationRunGraphGeneration, observation QualificationObservation, queryID string, evidence *qualificationEvidence) error {
	generation, ok := qualificationGraphGenerationOf(observation.IndexFingerprint)
	if !ok {
		evidence.graphGenerationConsistent = false
		return nil
	}
	source := fmt.Sprintf("arm %s query %s index fingerprint", observation.Arm, queryID)
	if !bound.bound {
		*bound = qualificationRunGraphGeneration{value: generation, bound: true, source: source}
		return nil
	}
	if generation != bound.value {
		evidence.graphGenerationConsistent = false
		return fmt.Errorf("embedded-model qualification decision: %s names graph generation %q but %s names %q; every arm and every observation of one run must be measured against exactly one graph generation",
			source, generation, bound.source, bound.value)
	}
	return nil
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
	operating, err := operatingMeasurementsFromEvidence(in.Operating, in.Preregistration, in.Dataset)
	if err != nil {
		return QualificationDecision{}, fmt.Errorf("embedded-model qualification decision: operating evidence: %w", err)
	}

	outcomes := pairedOutcomes(evidence, ArmCodeRank)
	interval := PairedBootstrap95(outcomes, in.Preregistration.BootstrapSeed, in.Preregistration.BootstrapSamples)
	m3Passes := passCount(evidence.passes[ArmCodeRank])
	pairedGain := pairedGain(evidence, ArmCodeRank)
	weakPositive := positiveStrata(evidence, ArmCodeRank, []string{StratumAmbiguous, StratumArchitectureFlow, StratumNLBehaviour})
	strongLosses := negativeStrata(evidence, ArmCodeRank, []string{StratumConfigDocs, StratumExactIdentifier, StratumExactPath})
	spanGain := completeSpanGain(evidence, ArmCodeRank)
	semanticRankNet := semanticRankGain(evidence, ArmCodeRank)
	p95 := operating.QueryEmbedP95

	gates := []GateResult{
		gate("m3_blind_passes", m3Passes >= in.Preregistration.Thresholds.MinPasses, fmt.Sprintf("%d", m3Passes), fmt.Sprintf(">=%d", in.Preregistration.Thresholds.MinPasses)),
		gate("paired_final_pass_gain", pairedGain >= in.Preregistration.Thresholds.MinPairedGain, fmt.Sprintf("%+d", pairedGain), fmt.Sprintf(">=+%d", in.Preregistration.Thresholds.MinPairedGain)),
		gate("paired_bootstrap_positive", interval.Lower > 0, fmt.Sprintf("algorithm=%s seed=%d samples=%d point=%.6f interval=[%.6f,%.6f]", qualificationBootstrapAlgorithm, in.Preregistration.BootstrapSeed, in.Preregistration.BootstrapSamples, interval.Point, interval.Lower, interval.Upper), "95% lower bound > 0"),
		gate("weak_strata_positive_gain", weakPositive >= in.Preregistration.Thresholds.MinWeakStrataWithPositiveGain, fmt.Sprintf("%d", weakPositive), fmt.Sprintf(">=%d", in.Preregistration.Thresholds.MinWeakStrataWithPositiveGain)),
		gate("strong_strata_no_loss", strongLosses == 0, fmt.Sprintf("%d negative strata", strongLosses), "0 negative strata"),
		gate("serialized_span_transfer", spanGain >= 1, fmt.Sprintf("%+d complete-span queries", spanGain), ">=+1 complete-span query"),
		gate("two_build_byte_equality", allReproducible(evidence.reproducible), reproducibilityObserved(evidence.reproducible), "two byte-identical builds for every arm, including oracle payload/token digests"),
		gate("artifact_budget", operating.ArtifactBytes <= in.Preregistration.Thresholds.MaxArtifactBytes, fmt.Sprintf("%d bytes", operating.ArtifactBytes), fmt.Sprintf("1..%d bytes", in.Preregistration.Thresholds.MaxArtifactBytes)),
		gate("sidecar_rss_budget", operating.PeakSidecarRSSBytes <= in.Preregistration.Thresholds.MaxSidecarRSSBytes, fmt.Sprintf("%d bytes", operating.PeakSidecarRSSBytes), fmt.Sprintf("1..%d bytes", in.Preregistration.Thresholds.MaxSidecarRSSBytes)),
		gate("query_embed_p95_budget", p95 <= time.Duration(in.Preregistration.Thresholds.MaxQueryP95Millis)*time.Millisecond, fmt.Sprintf("samples=%d p95=%s", len(operating.QueryEmbedLatencies), p95), fmt.Sprintf(">=%d positive samples and p95<=%dms", in.Preregistration.Thresholds.MinQuerySamples, in.Preregistration.Thresholds.MaxQueryP95Millis)),
		gate("reindex_budget", operating.FullReindex <= time.Duration(in.Preregistration.Thresholds.MaxReindexSeconds)*time.Second, operating.FullReindex.String(), fmt.Sprintf("1s..%ds", in.Preregistration.Thresholds.MaxReindexSeconds)),
		gate("cpu_only", true, "true (pinned M3 manifest)", "true"),
		gate("state_ready", evidence.stateReady, fmt.Sprintf("%t", evidence.stateReady), "every M1-M3 observation ready"),
		gate("fingerprint_equality", evidence.fingerprintsOK, qualificationFingerprintGateObserved(evidence), "every M1-M3 index fingerprint equals its preregistered fingerprint in every field except the runtime-bound graph generation, and every M1-M3 model id equals its preregistered model id"),
		gate("graph_generation_consistency", evidence.graphGenerationConsistent, fmt.Sprintf("%t", evidence.graphGenerationConsistent), "every M1-M3 index fingerprint names one and the same runtime-bound graph generation"),
		gate("no_degradation", evidence.noDegradation, fmt.Sprintf("%t", evidence.noDegradation), "every M1-M3 observation has no sidecar degradation"),
		gate("required_diagnostics_available", evidence.diagnosticsAvailable, fmt.Sprintf("%t", evidence.diagnosticsAvailable), "every M1-M3 observation has required diagnostics"),
	}
	promote := true
	for _, result := range gates {
		promote = promote && result.Passed
	}
	decision := QualificationDecision{Promote: promote, Gates: gates, Evidence: []EvidenceRecord{{
		Name: "semantic_rank_pairwise_net", Algorithm: "paired-rank-absent-51-v1", Observed: fmt.Sprintf("%+d", semanticRankNet),
	}}}
	decision.Branch = qualificationBranch(in, evidence, promote, pairedGain, spanGain)
	return decision, nil
}

func validateQualificationEvidence(in QualificationInput) (qualificationEvidence, error) {
	arms := []QualificationArm{ArmLexical, ArmPotion512, ArmPotion8192, ArmCodeRank}
	evidence := qualificationEvidence{
		strata: make(map[string]string, 64), observations: make(map[QualificationArm]map[string]QualificationObservation, 4),
		passes: make(map[QualificationArm]map[string]bool, 4), reproducible: make(map[QualificationArm]bool, 4),
		oraclePasses: make(map[string]map[string]bool, 3), oraclePayloads: make(map[string]map[string]string, 3),
		armValid:   map[QualificationArm]bool{ArmPotion512: true, ArmPotion8192: true, ArmCodeRank: true},
		stateReady: true, fingerprintsOK: true, noDegradation: true, diagnosticsAvailable: true,
		graphGenerationConsistent: true,
	}
	// The one graph generation this run is allowed to carry. It is not known
	// in advance — it is whatever the first semantic observation reports —
	// and every later observation of every arm must agree with it.
	var graphGeneration qualificationRunGraphGeneration
	if err := validateQualificationInputDataset(in); err != nil {
		return evidence, err
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
		if err := validateSemanticStageHit(observation.SemanticTop50); err != nil {
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
			diagnosticsReady := observation.UnknownTokens.Available && observation.UnknownTokens.Value != nil && observation.QueryVectorAllZero.Available && observation.QueryVectorAllZero.Value != nil
			if observation.UnknownTokens.Value != nil && *observation.UnknownTokens.Value < 0 {
				return evidence, fmt.Errorf("embedded-model qualification decision: arm %s query %s has negative unknown-token diagnostic", observation.Arm, queryID)
			}
			ready := observation.RetrievalState == "ready"
			expected := in.Preregistration.Arms[observation.Arm].FingerprintCanonical
			// Fields 0-6 must equal the preregistered pin exactly. Field 7,
			// graph_generation, is minted from crypto/rand by every index
			// build and therefore cannot be preregistered at all — see
			// model_qualification_fingerprint.go. It is bound at runtime and
			// checked for internal consistency by run, just below.
			//
			// The two observed fields are different KINDS of value and are
			// checked as such: IndexFingerprint is a canonical fingerprint and
			// is compared field by field against the pin; ModelFingerprint is
			// a model id (see QualificationObservation) and is compared
			// against the pin's model id — field 0 of that same canonical, so
			// there is still exactly one source of truth. Running the model id
			// through the canonical decoder, as this did before, made the gate
			// unpassable for every real run rather than strict.
			fingerprintsOK := true
			if err := qualificationFingerprintsAgree(observation.IndexFingerprint, expected); err != nil {
				fingerprintsOK = false
				if evidence.fingerprintMismatch == "" {
					evidence.fingerprintMismatch = fmt.Sprintf("arm %s query %s index fingerprint: %v", observation.Arm, queryID, err)
				}
			}
			expectedModelID, expectedModelIDOK := qualificationModelIDOf(expected)
			if !expectedModelIDOK || observation.ModelFingerprint != expectedModelID {
				fingerprintsOK = false
				if evidence.fingerprintMismatch == "" {
					evidence.fingerprintMismatch = fmt.Sprintf("arm %s query %s model id: observed %s, preregistered %s",
						observation.Arm, queryID,
						qualificationFingerprintValue(observation.ModelFingerprint),
						qualificationFingerprintValue(expectedModelID))
				}
			}
			if err := bindQualificationGraphGeneration(&graphGeneration, observation, queryID, &evidence); err != nil {
				return evidence, err
			}
			noDegradation := !observation.Degraded
			evidence.armValid[observation.Arm] = evidence.armValid[observation.Arm] && diagnosticsReady && ready && fingerprintsOK && noDegradation
			evidence.diagnosticsAvailable = evidence.diagnosticsAvailable && diagnosticsReady
			evidence.stateReady = evidence.stateReady && ready
			evidence.fingerprintsOK = evidence.fingerprintsOK && fingerprintsOK
			evidence.noDegradation = evidence.noDegradation && noDegradation
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
	if err := validateBuildEvidence(in, arms, &evidence); err != nil {
		return evidence, err
	}
	if err := validateOracleEvidence(in.OracleEvidence, in.BuildDigests, evidence.queryIDs, &evidence); err != nil {
		return evidence, err
	}
	if err := validateBlindDecisions(in, arms, &evidence); err != nil {
		return evidence, err
	}
	return evidence, nil
}

func validateQualificationInputDataset(in QualificationInput) error {
	if err := ValidateQualificationDataset(in.Dataset); err != nil {
		return fmt.Errorf("embedded-model qualification decision: dataset: %w", err)
	}
	if in.Dataset.SHA256 != in.Preregistration.DatasetSHA256 {
		return fmt.Errorf("embedded-model qualification decision: dataset digest differs from preregistration")
	}
	if in.Dataset.Dataset.RepoSHA != in.Preregistration.SourceRepoSHA {
		return fmt.Errorf("embedded-model qualification decision: dataset source checkout differs from preregistration")
	}
	var decoded Dataset
	if err := json.Unmarshal(in.Dataset.Raw, &decoded); err != nil || !reflect.DeepEqual(&decoded, in.Dataset.Dataset) {
		return fmt.Errorf("embedded-model qualification decision: dataset object differs from its exact raw bytes")
	}
	return nil
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

func validateSemanticStageHit(hit StageHit) error {
	if err := validateStageHit(hit); err != nil {
		return err
	}
	if hit.Present && hit.BestRank > 50 {
		return fmt.Errorf("present semantic hit rank exceeds top 50")
	}
	return nil
}

func validateBuildEvidence(in QualificationInput, arms []QualificationArm, evidence *qualificationEvidence) error {
	digests := in.BuildDigests
	if len(digests) != len(arms)*2 {
		return fmt.Errorf("embedded-model qualification decision: got %d build digests, want %d", len(digests), len(arms)*2)
	}
	byArm := make(map[QualificationArm]map[int]QualificationBuildDigest, len(arms))
	known := make(map[QualificationArm]bool, len(arms))
	for _, arm := range arms {
		known[arm] = true
		byArm[arm] = make(map[int]QualificationBuildDigest, 2)
	}
	for _, digest := range digests {
		if !known[digest.Arm] {
			return fmt.Errorf("embedded-model qualification decision: build digest has unknown arm %q", digest.Arm)
		}
		if digest.Build != 1 && digest.Build != 2 {
			return fmt.Errorf("embedded-model qualification decision: arm %s has invalid build ordinal %d", digest.Arm, digest.Build)
		}
		if _, duplicate := byArm[digest.Arm][digest.Build]; duplicate {
			return fmt.Errorf("embedded-model qualification decision: arm %s duplicates build ordinal %d", digest.Arm, digest.Build)
		}
		if err := validateQualificationCaptureProvenance(digest, in); err != nil {
			return err
		}
		if err := validateQualificationBuildDigestSeal(digest); err != nil {
			return fmt.Errorf("embedded-model qualification decision: arm %s build %d: %w", digest.Arm, digest.Build, err)
		}
		for _, value := range []string{digest.VectorBytesSHA256, digest.PersistedRowsSHA256, digest.BundlesSHA256, digest.TokenCountsSHA256, digest.OraclePayloadsSHA256, digest.OracleTokenCountsSHA256, digest.QueryDiagnosticsSHA256, digest.ObservationsSHA256} {
			if !isLowerHexDigest(value, 64) {
				return fmt.Errorf("embedded-model qualification decision: arm %s has malformed build digest", digest.Arm)
			}
		}
		if digest.Diagnostics.AdmissionTruncations < 0 || digest.Diagnostics.DocumentZeroVectors < 0 {
			return fmt.Errorf("embedded-model qualification decision: arm %s has negative build diagnostics", digest.Arm)
		}
		byArm[digest.Arm][digest.Build] = digest
	}
	for _, arm := range arms {
		if len(byArm[arm]) != 2 {
			return fmt.Errorf("embedded-model qualification decision: arm %s has %d builds, want 2", arm, len(byArm[arm]))
		}
		first, second := byArm[arm][1], byArm[arm][2]
		observations := make([]QualificationObservation, 0, len(evidence.observations[arm]))
		for _, observation := range evidence.observations[arm] {
			observations = append(observations, observation)
		}
		observationsSHA := qualificationObservationsSHA256(observations)
		if first.ObservationsSHA256 != observationsSHA || second.ObservationsSHA256 != observationsSHA {
			return fmt.Errorf("embedded-model qualification decision: arm %s observation digest differs from both captured builds", arm)
		}
		if first.CaptureProvenance.SHA256 == second.CaptureProvenance.SHA256 {
			return fmt.Errorf("embedded-model qualification decision: arm %s builds do not have distinct capture provenance", arm)
		}
		if first.CaptureProvenance.WorkDir == second.CaptureProvenance.WorkDir {
			return fmt.Errorf("embedded-model qualification decision: arm %s builds reuse the same capture workdir", arm)
		}
		if first.CaptureProvenance.CaptureIdentitySHA256 == second.CaptureProvenance.CaptureIdentitySHA256 {
			return fmt.Errorf("embedded-model qualification decision: arm %s builds reuse the same capture identity", arm)
		}
		evidence.reproducible[arm] = compareQualificationBuildDigests(first, second) == nil
	}
	return nil
}

func validateQualificationCaptureProvenance(digest QualificationBuildDigest, in QualificationInput) error {
	record := digest.CaptureProvenance
	if record.Arm != digest.Arm || record.Build != digest.Build || !filepath.IsAbs(record.WorkDir) || filepath.Clean(record.WorkDir) != record.WorkDir || record.Provenance.QualificationBuildDigest != nil {
		return fmt.Errorf("embedded-model qualification decision: arm %s build %d has malformed closed capture provenance", digest.Arm, digest.Build)
	}
	sealed, err := sealQualificationCaptureProvenanceRecord(record)
	if err != nil || !isLowerHexDigest(record.SHA256, 64) || sealed.SHA256 != record.SHA256 {
		return fmt.Errorf("embedded-model qualification decision: arm %s build %d capture provenance content address differs", digest.Arm, digest.Build)
	}
	identity, err := qualificationCaptureRecordIdentitySHA(record)
	if err != nil || !isLowerHexDigest(record.CaptureIdentitySHA256, 64) || identity != record.CaptureIdentitySHA256 {
		return fmt.Errorf("embedded-model qualification decision: arm %s build %d capture identity differs", digest.Arm, digest.Build)
	}
	p := record.Provenance
	binding := p.Binding
	endBinding := p.BindingEnd
	if p.QualificationCaptureRunSHA256 != qualificationCaptureRunSHA(digest.Arm, record.WorkDir) {
		return fmt.Errorf("embedded-model qualification decision: arm %s build %d capture run identity differs from workdir", digest.Arm, digest.Build)
	}
	if p.CaptureVersion != CandidateCaptureVersion || p.Transport != CandidateCaptureTransport || p.Surface != CandidateCaptureSurface ||
		p.Boundary != string(PayloadBoundaryCandidate) || p.DatasetSHA256 != in.Preregistration.DatasetSHA256 ||
		p.RepoName != in.Dataset.Dataset.Repo || p.RepoSHA != in.Preregistration.SourceRepoSHA ||
		p.TokenBudget != QualificationTokenBudget || p.MethodVersion != QualificationCompactVersion || p.QueryCount != 64 ||
		p.TokenizerID != PinnedRealPayloadTokenizerID || p.TokenizerVocabSHA != PinnedRealPayloadTokenizerVocabularySHA256 || binding == nil || endBinding == nil {
		return fmt.Errorf("embedded-model qualification decision: arm %s build %d capture provenance differs from qualification pins", digest.Arm, digest.Build)
	}
	if err := CheckRunDirectoryRelativePath(QualificationCandidateExcludedPath); err != nil {
		return fmt.Errorf("embedded-model qualification decision: candidate excluded path contract: %w", err)
	}
	if binding.CandidateSHA != in.Preregistration.CandidateSHA || binding.FrozenCandidateSHA != in.Preregistration.CandidateSHA ||
		binding.CheckoutSHA != in.Preregistration.SourceRepoSHA || !binding.CandidateWorktreeClean || !binding.CheckoutWorktreeClean ||
		!binding.CandidateMatchesFrozen || len(binding.DifferingPaths) != 0 || binding.CandidateExcludedPath != QualificationCandidateExcludedPath ||
		binding.CandidateDiffSHA256 != in.Preregistration.CandidateDiffSHA256 {
		return fmt.Errorf("embedded-model qualification decision: arm %s build %d candidate binding differs from qualification pins", digest.Arm, digest.Build)
	}
	if !reflect.DeepEqual(binding, endBinding) {
		return fmt.Errorf("embedded-model qualification decision: arm %s build %d start/end binding differs", digest.Arm, digest.Build)
	}
	pin := in.Preregistration.Arms[digest.Arm]
	if p.EmbedderSelector != pin.Label {
		return fmt.Errorf("embedded-model qualification decision: arm %s build %d embedder selector differs from arm pin", digest.Arm, digest.Build)
	}
	if digest.Arm == ArmLexical {
		if p.ModelFingerprint != "" || p.IndexFingerprint != "" || p.GenerationID != "" || p.PersistedVectors != 0 || p.SemanticState != "unset" {
			return fmt.Errorf("embedded-model qualification decision: lexical arm carries semantic identity")
		}
	} else {
		if strings.TrimSpace(p.GenerationID) == "" || p.SemanticState != "ready" || p.PersistedVectors <= 0 {
			return fmt.Errorf("embedded-model qualification decision: arm %s build %d semantic identity differs from arm pin", digest.Arm, digest.Build)
		}
		// Compared field by field against the pin, graph_generation
		// excluded: the pin cannot name it (it is minted from crypto/rand on
		// every index build — see model_qualification_fingerprint.go). The
		// capture's OWN two fingerprints must still be byte-identical to each
		// other, which is what pins the generation within this build.
		for _, observed := range []struct{ name, canonical string }{
			{name: "model fingerprint", canonical: p.ModelFingerprint},
			{name: "index fingerprint", canonical: p.IndexFingerprint},
		} {
			if err := qualificationFingerprintsAgree(observed.canonical, pin.FingerprintCanonical); err != nil {
				return fmt.Errorf("embedded-model qualification decision: arm %s build %d %s differs from arm pin: %w", digest.Arm, digest.Build, observed.name, err)
			}
		}
		if p.ModelFingerprint != p.IndexFingerprint {
			return fmt.Errorf("embedded-model qualification decision: arm %s build %d model and index fingerprints name different graph generations", digest.Arm, digest.Build)
		}
	}
	return nil
}

func validateOracleEvidence(source QualificationOracleEvidence, builds []QualificationBuildDigest, queryIDs []string, evidence *qualificationEvidence) error {
	sealed, err := sealQualificationOracleEvidence(source)
	if err != nil || !isLowerHexDigest(source.SHA256, 64) || sealed.SHA256 != source.SHA256 {
		return fmt.Errorf("embedded-model qualification decision: oracle evidence content address differs")
	}
	if source.BuildRef.BaseArm != ArmCodeRank || source.BuildRef.Build != 1 {
		return fmt.Errorf("embedded-model qualification decision: oracle evidence must use M3_coderank build 1")
	}
	var selected *QualificationBuildDigest
	for i := range builds {
		if builds[i].Arm == ArmCodeRank && builds[i].Build == 1 {
			selected = &builds[i]
			break
		}
	}
	if selected == nil || source.BuildRef.BuildSHA256 != selected.SHA256 || source.BuildRef.CaptureProvenanceSHA256 != selected.CaptureProvenance.SHA256 {
		return fmt.Errorf("embedded-model qualification decision: oracle evidence build reference differs from M3 build 1")
	}
	payloadSHA, tokenSHA := qualificationOracleDigests(source.Controls)
	if payloadSHA != source.OraclePayloadsSHA256 || tokenSHA != source.OracleTokenCountsSHA256 ||
		payloadSHA != selected.OraclePayloadsSHA256 || tokenSHA != selected.OracleTokenCountsSHA256 {
		return fmt.Errorf("embedded-model qualification decision: oracle evidence digests differ from M3 build 1")
	}
	controls := source.Controls
	if len(controls) != 64 {
		return fmt.Errorf("embedded-model qualification decision: got %d oracle controls, want 64", len(controls))
	}
	expected := make(map[string]bool, len(queryIDs))
	for _, id := range queryIDs {
		expected[id] = true
	}
	seen := make(map[string]bool, 64)
	for _, kind := range []string{OracleControlCurrentCandidatesOraclePacker, OracleControlOracleCandidateCurrentSelector, OracleControlOracleCandidateOraclePacker} {
		evidence.oraclePayloads[kind] = make(map[string]string, 64)
	}
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
			evidence.oraclePayloads[item.kind][queryID] = item.bundle.Payload.SHA256
		}
	}
	return nil
}

func validateBlindDecisions(in QualificationInput, arms []QualificationArm, evidence *qualificationEvidence) error {
	controlKinds := []string{OracleControlCurrentCandidatesOraclePacker, OracleControlOracleCandidateCurrentSelector, OracleControlOracleCandidateOraclePacker}
	want := (len(arms) + len(controlKinds)) * 64
	derived, sourceSHAs, err := validateBlindEvidenceSets(in, arms, controlKinds, evidence)
	if err != nil {
		return err
	}
	if len(in.Decisions) != want {
		return fmt.Errorf("embedded-model qualification decision: got %d blind decisions, want %d", len(in.Decisions), want)
	}
	armKnown := make(map[QualificationArm]bool, len(arms))
	for _, arm := range arms {
		armKnown[arm] = true
		evidence.passes[arm] = make(map[string]bool, 64)
	}
	controlKnown := make(map[string]bool, len(controlKinds))
	for _, kind := range controlKinds {
		controlKnown[kind] = true
		evidence.oraclePasses[kind] = make(map[string]bool, 64)
	}
	for _, decision := range in.Decisions {
		subject := blindSubjectKey(decision.Arm, decision.ControlKind)
		derivedOutcome, ok := derived[subject+"\x00"+decision.QueryID]
		if !ok {
			return fmt.Errorf("embedded-model qualification decision: query %s has no validated source evidence", decision.QueryID)
		}
		if err := validateBlindDecision(decision, sourceSHAs[subject], derivedOutcome); err != nil {
			return fmt.Errorf("embedded-model qualification decision: query %s: %w", decision.QueryID, err)
		}
		if _, ok := evidence.strata[decision.QueryID]; !ok || decision.Stratum != evidence.strata[decision.QueryID] || decision.Outcome.Stratum != decision.Stratum {
			return fmt.Errorf("embedded-model qualification decision: decision query %q has wrong or unknown stratum", decision.QueryID)
		}
		if decision.ReaderPromptSHA256 != in.Preregistration.ReaderPromptSHA256 || decision.GraderPromptSHA256 != in.Preregistration.GraderPromptSHA256 {
			return fmt.Errorf("embedded-model qualification decision: query %s prompt pins differ from preregistration", decision.QueryID)
		}
		if decision.Arm != "" {
			if decision.ControlKind != "" || !armKnown[decision.Arm] {
				return fmt.Errorf("embedded-model qualification decision: query %s does not name exactly one known subject", decision.QueryID)
			}
			if decision.PayloadSHA256 != evidence.observations[decision.Arm][decision.QueryID].PayloadSHA256 {
				return fmt.Errorf("embedded-model qualification decision: arm %s query %s payload does not match observation", decision.Arm, decision.QueryID)
			}
			if _, duplicate := evidence.passes[decision.Arm][decision.QueryID]; duplicate {
				return fmt.Errorf("embedded-model qualification decision: arm %s duplicates query %s", decision.Arm, decision.QueryID)
			}
			evidence.passes[decision.Arm][decision.QueryID] = decision.Outcome.Outcome == GradeOutcomePass
			continue
		}
		if !controlKnown[decision.ControlKind] {
			return fmt.Errorf("embedded-model qualification decision: query %s does not name exactly one known subject", decision.QueryID)
		}
		if decision.PayloadSHA256 != evidence.oraclePayloads[decision.ControlKind][decision.QueryID] {
			return fmt.Errorf("embedded-model qualification decision: oracle %s query %s payload does not match bundle", decision.ControlKind, decision.QueryID)
		}
		if _, duplicate := evidence.oraclePasses[decision.ControlKind][decision.QueryID]; duplicate {
			return fmt.Errorf("embedded-model qualification decision: oracle %s duplicates query %s", decision.ControlKind, decision.QueryID)
		}
		evidence.oraclePasses[decision.ControlKind][decision.QueryID] = decision.Outcome.Outcome == GradeOutcomePass
	}
	for _, arm := range arms {
		if len(evidence.passes[arm]) != 64 {
			return fmt.Errorf("embedded-model qualification decision: arm %s has %d unique decisions, want 64", arm, len(evidence.passes[arm]))
		}
	}
	for _, kind := range controlKinds {
		if len(evidence.oraclePasses[kind]) != 64 {
			return fmt.Errorf("embedded-model qualification decision: oracle %s has %d unique decisions, want 64", kind, len(evidence.oraclePasses[kind]))
		}
	}
	return nil
}

func validateBlindEvidenceSets(in QualificationInput, arms []QualificationArm, controlKinds []string, evidence *qualificationEvidence) (map[string]QueryOutcome, map[string]string, error) {
	wantSubjects := make(map[string]bool, len(arms)+len(controlKinds))
	for _, arm := range arms {
		wantSubjects[blindSubjectKey(arm, "")] = true
	}
	for _, kind := range controlKinds {
		wantSubjects[blindSubjectKey("", kind)] = true
	}
	if len(in.BlindEvidence) != len(wantSubjects) {
		return nil, nil, fmt.Errorf("embedded-model qualification decision: got %d blind evidence sets, want %d", len(in.BlindEvidence), len(wantSubjects))
	}
	derived := make(map[string]QueryOutcome, len(wantSubjects)*64)
	sourceSHAs := make(map[string]string, len(wantSubjects))
	datasetQueries := make(map[string]Query, len(in.Dataset.Dataset.Queries))
	for _, query := range in.Dataset.Dataset.Queries {
		datasetQueries[query.ID] = query
	}
	for _, source := range in.BlindEvidence {
		subject := blindSubjectKey(source.Arm, source.ControlKind)
		if !wantSubjects[subject] || (source.Arm != "" && source.ControlKind != "") {
			return nil, nil, fmt.Errorf("embedded-model qualification decision: blind evidence does not name exactly one known subject")
		}
		if _, duplicate := sourceSHAs[subject]; duplicate {
			return nil, nil, fmt.Errorf("embedded-model qualification decision: duplicate blind evidence for %s", subject)
		}
		if err := ValidatePreconditionRecord(source.Precondition); err != nil {
			return nil, nil, fmt.Errorf("embedded-model qualification decision: blind evidence %s precondition: %w", subject, err)
		}
		if source.Precondition.DatasetSHA256 != in.Preregistration.DatasetSHA256 ||
			source.Precondition.CandidateSHA != in.Preregistration.CandidateSHA || source.Precondition.FreezeCommit != in.Preregistration.CandidateSHA ||
			source.Precondition.CandidateTokenBudget != QualificationTokenBudget || source.Precondition.CandidateMethod != SavingsCandidateMethod ||
			source.Precondition.ComparatorVersion != BlindEvalComparatorVersion {
			return nil, nil, fmt.Errorf("embedded-model qualification decision: blind evidence %s precondition differs from qualification pins", subject)
		}
		if err := ValidatePreRegistration(source.PreRegistration, source.Precondition); err != nil {
			return nil, nil, fmt.Errorf("embedded-model qualification decision: blind evidence %s preregistration: %w", subject, err)
		}
		if source.PreRegistration.PreconditionCommit != in.Preregistration.CandidateSHA {
			return nil, nil, fmt.Errorf("embedded-model qualification decision: blind evidence %s precondition commit differs from qualification candidate", subject)
		}
		sealed, err := sealBlindEvidenceSet(source)
		if err != nil || sealed.SHA256 != source.SHA256 {
			return nil, nil, fmt.Errorf("embedded-model qualification decision: blind evidence %s content address differs", subject)
		}
		artifacts := EvaluationArtifacts{Precondition: source.Precondition, PreRegistration: source.PreRegistration, Responses: source.Responses, Grades: source.Grades, Adjudications: source.Adjudications}
		if err := CheckPrecedence(artifacts); err != nil {
			return nil, nil, fmt.Errorf("embedded-model qualification decision: blind evidence %s precedence: %w", subject, err)
		}
		if err := CheckGradeBinding(artifacts); err != nil {
			return nil, nil, fmt.Errorf("embedded-model qualification decision: blind evidence %s grade binding: %w", subject, err)
		}
		if err := CheckAdjudicationOrder(artifacts); err != nil {
			return nil, nil, fmt.Errorf("embedded-model qualification decision: blind evidence %s adjudication: %w", subject, err)
		}
		if len(source.PreRegistration.Queries) != 64 {
			return nil, nil, fmt.Errorf("embedded-model qualification decision: blind evidence %s has %d queries, want 64", subject, len(source.PreRegistration.Queries))
		}
		rubricOK := false
		for _, input := range source.Precondition.Inputs {
			if input.Role == "grading_rubric" && input.SHA256 == in.Preregistration.GraderPromptSHA256 {
				rubricOK = true
			}
		}
		if !rubricOK {
			return nil, nil, fmt.Errorf("embedded-model qualification decision: blind evidence %s does not freeze the preregistered grader prompt", subject)
		}
		for _, query := range source.PreRegistration.Queries {
			datasetQuery, known := datasetQueries[query.QueryID]
			if !known || query.Stratum != datasetQuery.Stratum || query.Stratum != evidence.strata[query.QueryID] ||
				query.QueryTextSHA256 != SHA256Hex([]byte(datasetQuery.Text)) || query.PromptSHA256 != in.Preregistration.ReaderPromptSHA256 {
				return nil, nil, fmt.Errorf("embedded-model qualification decision: blind evidence %s query %s has wrong stratum or reader prompt", subject, query.QueryID)
			}
			wantPayload := ""
			if source.Arm != "" {
				wantPayload = evidence.observations[source.Arm][query.QueryID].PayloadSHA256
			} else {
				wantPayload = evidence.oraclePayloads[source.ControlKind][query.QueryID]
			}
			if query.BundleSHA256 != wantPayload {
				return nil, nil, fmt.Errorf("embedded-model qualification decision: blind evidence %s query %s payload differs", subject, query.QueryID)
			}
			outcome, err := DecideQuery(artifacts, query)
			if err != nil {
				return nil, nil, fmt.Errorf("embedded-model qualification decision: blind evidence %s query %s decision: %w", subject, query.QueryID, err)
			}
			derived[subject+"\x00"+query.QueryID] = outcome
		}
		sourceSHAs[subject] = source.SHA256
	}
	return derived, sourceSHAs, nil
}

func blindSubjectKey(arm QualificationArm, controlKind string) string {
	if arm != "" {
		return "arm:" + string(arm)
	}
	return "control:" + controlKind
}

func validateBlindDecision(decision BlindDecision, sourceSHA string, derived QueryOutcome) error {
	if strings.TrimSpace(decision.QueryID) == "" || decision.Outcome.QueryID != decision.QueryID || !isLowerHexDigest(decision.PayloadSHA256, 64) ||
		!isLowerHexDigest(decision.ReaderPromptSHA256, 64) || !isLowerHexDigest(decision.GraderPromptSHA256, 64) || strings.TrimSpace(decision.Outcome.Reason) == "" {
		return fmt.Errorf("blind decision has malformed identity or outcome")
	}
	if err := validateFinalQueryOutcome(decision.Outcome); err != nil {
		return err
	}
	if decision.EvidenceSHA256 != sourceSHA || !reflect.DeepEqual(decision.Outcome, derived) {
		return fmt.Errorf("blind decision differs from validated source evidence")
	}
	decisionSHA, err := ContentAddress(decision, func(v *BlindDecision) { v.SHA256 = "" })
	if err != nil || decision.SHA256 != decisionSHA {
		return fmt.Errorf("blind decision content address differs")
	}
	return nil
}

func validateFinalQueryOutcome(outcome QueryOutcome) error {
	if outcome.Outcome != GradeOutcomePass && outcome.Outcome != GradeOutcomeFail {
		return fmt.Errorf("final query outcome is neither pass nor fail")
	}
	if len(outcome.Primary) != 2 {
		return fmt.Errorf("final query outcome has %d primary raters, want 2", len(outcome.Primary))
	}
	seen := map[string]bool{}
	passes, fails, nonAnswered := 0, 0, 0
	for _, rater := range outcome.Primary {
		if strings.TrimSpace(rater.RaterID) == "" || seen[rater.RaterID] || rater.Role != RaterRolePrimary || !isLowerHexDigest(rater.ResponseSHA256, 64) {
			return fmt.Errorf("final query outcome has malformed primary evidence")
		}
		seen[rater.RaterID] = true
		if rater.Status == ResponseStatusAnswered {
			if rater.Mechanical || !isLowerHexDigest(rater.GradeSHA256, 64) || (rater.Outcome != GradeOutcomePass && rater.Outcome != GradeOutcomeFail) {
				return fmt.Errorf("answered primary evidence is malformed")
			}
		} else {
			if (rater.Status != ResponseStatusMissing && rater.Status != ResponseStatusEmpty && rater.Status != ResponseStatusRefused) || !rater.Mechanical || rater.GradeSHA256 != "" || rater.Outcome != GradeOutcomeFail {
				return fmt.Errorf("non-answered primary evidence is malformed")
			}
			nonAnswered++
		}
		if rater.Outcome == GradeOutcomePass {
			passes++
		} else {
			fails++
		}
	}
	disagreement := passes == 1 && fails == 1 && nonAnswered == 0
	if outcome.Disagreement != disagreement {
		return fmt.Errorf("final query disagreement flag is inconsistent")
	}
	want := GradeOutcomeFail
	if nonAnswered == 0 && passes == 2 {
		want = GradeOutcomePass
	}
	if disagreement {
		if !outcome.Adjudicated || outcome.Adjudicator == nil {
			return fmt.Errorf("primary disagreement lacks adjudication")
		}
		adj := outcome.Adjudicator
		if strings.TrimSpace(adj.RaterID) == "" || seen[adj.RaterID] || adj.Role != RaterRoleAdjudicator || !isLowerHexDigest(adj.ResponseSHA256, 64) {
			return fmt.Errorf("adjudicator evidence is malformed")
		}
		if adj.Status == ResponseStatusAnswered {
			if adj.Mechanical || !isLowerHexDigest(adj.GradeSHA256, 64) || (adj.Outcome != GradeOutcomePass && adj.Outcome != GradeOutcomeFail) {
				return fmt.Errorf("answered adjudicator evidence is malformed")
			}
		} else if (adj.Status != ResponseStatusMissing && adj.Status != ResponseStatusEmpty && adj.Status != ResponseStatusRefused) || !adj.Mechanical || adj.GradeSHA256 != "" || adj.Outcome != GradeOutcomeFail {
			return fmt.Errorf("non-answered adjudicator evidence is malformed")
		}
		want = adj.Outcome
	} else if outcome.Adjudicated || outcome.Adjudicator != nil {
		return fmt.Errorf("unnecessary adjudication is present")
	}
	if outcome.Outcome != want {
		return fmt.Errorf("final query outcome %s does not follow evidence result %s", outcome.Outcome, want)
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

func semanticRankGain(e qualificationEvidence, arm QualificationArm) int {
	delta := 0
	for _, id := range e.queryIDs {
		delta += pairedSemanticRankDelta(e.observations[ArmPotion512][id].SemanticTop50, e.observations[arm][id].SemanticTop50)
	}
	return delta
}

func pairedSemanticRankDelta(m1, m3 StageHit) int {
	rank := func(hit StageHit) int {
		if hit.Present {
			return hit.BestRank
		}
		return 51
	}
	m1Rank, m3Rank := rank(m1), rank(m3)
	if m3Rank < m1Rank {
		return 1
	}
	if m3Rank > m1Rank {
		return -1
	}
	return 0
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
	if !evidence.armValid[ArmPotion512] || !evidence.armValid[arm] || passCount(evidence.passes[arm]) < in.Preregistration.Thresholds.MinPasses || pairedGain(evidence, arm) < in.Preregistration.Thresholds.MinPairedGain ||
		positiveStrata(evidence, arm, []string{StratumAmbiguous, StratumArchitectureFlow, StratumNLBehaviour}) < in.Preregistration.Thresholds.MinWeakStrataWithPositiveGain ||
		negativeStrata(evidence, arm, []string{StratumConfigDocs, StratumExactIdentifier, StratumExactPath}) != 0 || completeSpanGain(evidence, arm) < 1 || !evidence.reproducible[arm] {
		return false
	}
	return PairedBootstrap95(pairedOutcomes(evidence, arm), in.Preregistration.BootstrapSeed, in.Preregistration.BootstrapSamples).Lower > 0
}

func qualificationBranch(in QualificationInput, evidence qualificationEvidence, promote bool, m3Gain, m3SpanGain int) string {
	if !evidence.armValid[ArmPotion512] || !evidence.armValid[ArmPotion8192] || !evidence.armValid[ArmCodeRank] {
		return "stop_no_new_holdout"
	}
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
	if semanticRankGain(evidence, ArmCodeRank) > 0 && (m3SpanGain <= 0 || m3Gain <= 0) {
		return "investigate_projection_or_fusion"
	}
	if m2Quality && !m3Quality {
		return "design_potion_admission_candidate"
	}
	if passCount(evidence.oraclePasses[OracleControlOracleCandidateOraclePacker]) < in.Preregistration.Thresholds.MinPasses {
		return "representation_or_budget_ceiling"
	}
	return "stop_no_new_holdout"
}
