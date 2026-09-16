package retrieval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

const QualificationReportSchemaVersion = 1

const (
	QualificationReportCommitSchemaVersion = 1
	qualificationReportJSONName            = "qualification.json"
	qualificationReportMarkdownName        = "qualification.md"
	qualificationReportCommitName          = "qualification.commit.json"
	qualificationReportTransactionName     = ".qualification.transaction.json"
	qualificationPublishAfterJSON          = "after_json"
)

var qualificationReportArms = []QualificationArm{ArmLexical, ArmPotion512, ArmPotion8192, ArmCodeRank}

var qualificationReportStrata = []string{
	StratumAmbiguous,
	StratumArchitectureFlow,
	StratumConfigDocs,
	StratumExactIdentifier,
	StratumExactPath,
	StratumNLBehaviour,
}

var qualificationReportOracleKinds = []string{
	OracleControlCurrentCandidatesOraclePacker,
	OracleControlOracleCandidateCurrentSelector,
	OracleControlOracleCandidateOraclePacker,
}

// QualificationReport is a self-contained, closed record of one development
// qualification. Dataset bytes and blind source evidence are included so an
// independent verifier can reconstruct QualificationInput and rerun the
// decision without trusting an external path.
type QualificationReport struct {
	SchemaVersion   int                          `json:"schema_version"`
	Preregistration QualificationPreregistration `json:"preregistration"`
	Dataset         QualificationDatasetEvidence `json:"dataset"`
	Observations    []QualificationObservation   `json:"observations"`
	BuildDigests    []QualificationBuildDigest   `json:"build_digests"`
	BlindEvidence   []BlindEvidenceSet           `json:"blind_evidence"`
	BlindDecisions  []BlindDecision              `json:"blind_decisions"`
	OracleControls  []OracleControls             `json:"oracle_controls"`
	Operating       OperatingMeasurements        `json:"operating_budget"`
	Derived         QualificationReportDerived   `json:"derived"`
	Decision        QualificationDecision        `json:"decision"`
}

// QualificationDatasetEvidence preserves the exact curator-supplied bytes;
// RawBytes is base64 in JSON and therefore survives report reformatting.
type QualificationDatasetEvidence struct {
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	RawBytes []byte `json:"raw_bytes"`
}

type QualificationReportDerived struct {
	ArmTotals              []QualificationArmTotal              `json:"arm_totals"`
	M3VersusM1             QualificationPairedSummary           `json:"m3_versus_m1"`
	PairedBootstrap95      Interval                             `json:"paired_bootstrap_95"`
	StratumDeltas          []QualificationStratumDelta          `json:"stratum_deltas"`
	StageRetention         []QualificationStageRetention        `json:"stage_retention"`
	Reproducibility        []QualificationReproducibility       `json:"reproducibility"`
	BlindEvidenceManifests []QualificationBlindEvidenceManifest `json:"blind_evidence_manifests"`
	RunValidity            []GateResult                         `json:"run_validity"`
	OracleBlindCeilings    []QualificationOracleBlindCeiling    `json:"oracle_blind_ceilings"`
}

type QualificationArmTotal struct {
	Arm    QualificationArm `json:"arm"`
	Passes int              `json:"passes"`
	Total  int              `json:"total"`
}

type QualificationPairedSummary struct {
	Wins   int `json:"wins"`
	Losses int `json:"losses"`
	Ties   int `json:"ties"`
	Net    int `json:"net"`
}

type QualificationStratumDelta struct {
	Stratum  string `json:"stratum"`
	M1Passes int    `json:"m1_passes"`
	M3Passes int    `json:"m3_passes"`
	Delta    int    `json:"delta"`
}

type QualificationStageRetention struct {
	Arm                      QualificationArm `json:"arm"`
	SemanticTop50Present     int              `json:"semantic_top_50_present"`
	PostFusionPresent        int              `json:"post_fusion_present"`
	CompleteGrade3Span       int              `json:"complete_grade_3_span"`
	SemanticToFusionRetained int              `json:"semantic_to_fusion_retained"`
	FusionToBundleRetained   int              `json:"fusion_to_bundle_retained"`
	SemanticBestRankSum      int              `json:"semantic_best_rank_sum"`
}

type QualificationReproducibility struct {
	Arm            QualificationArm `json:"arm"`
	Build1SHA256   string           `json:"build_1_capture_provenance_sha256"`
	Build2SHA256   string           `json:"build_2_capture_provenance_sha256"`
	ByteIdentical  bool             `json:"byte_identical"`
	ComparedFields string           `json:"compared_fields"`
}

type QualificationBlindEvidenceManifest struct {
	Arm            QualificationArm `json:"arm,omitempty"`
	ControlKind    string           `json:"control_kind,omitempty"`
	SHA256         string           `json:"sha256"`
	Queries        int              `json:"queries"`
	Responses      int              `json:"responses"`
	Grades         int              `json:"grades"`
	Adjudications  int              `json:"adjudications"`
	FinalDecisions int              `json:"final_decisions"`
}

type QualificationOracleBlindCeiling struct {
	ControlKind string `json:"control_kind"`
	Passes      int    `json:"passes"`
	Total       int    `json:"total"`
}

// QualificationReportCommit is the authoritative publication boundary. The
// two human/machine artifacts are not a committed report until this
// content-addressed marker exists and both named digests match.
type QualificationReportCommit struct {
	SchemaVersion               int    `json:"schema_version"`
	ReportSchemaVersion         int    `json:"report_schema_version"`
	QualificationJSONSHA256     string `json:"qualification_json_sha256"`
	QualificationMarkdownSHA256 string `json:"qualification_markdown_sha256"`
	SHA256                      string `json:"sha256"`
}

type qualificationPublishHook func(stage string) error

// WriteQualificationReport validates all source evidence, reruns the frozen
// decision, and publishes the canonical JSON/Markdown pair without replacing
// any existing live evidence.
func WriteQualificationReport(dir string, report QualificationReport) error {
	return writeQualificationReportWithHook(dir, report, nil)
}

func writeQualificationReportWithHook(dir string, report QualificationReport, hook qualificationPublishHook) error {
	if report.SchemaVersion != QualificationReportSchemaVersion {
		return fmt.Errorf("embedded-model qualification report: schema version %d, want %d", report.SchemaVersion, QualificationReportSchemaVersion)
	}
	originalInput, err := qualificationInputFromReport(report)
	if err != nil {
		return err
	}
	originalDecision, err := EvaluateQualification(originalInput)
	if err != nil {
		return fmt.Errorf("embedded-model qualification report: validate evidence: %w", err)
	}
	if !reflect.DeepEqual(report.Decision, originalDecision) {
		return fmt.Errorf("embedded-model qualification report: supplied decision differs from independently evaluated decision")
	}
	report, err = canonicalQualificationReport(report)
	if err != nil {
		return fmt.Errorf("embedded-model qualification report: canonicalize: %w", err)
	}
	input, err := qualificationInputFromReport(report)
	if err != nil {
		return err
	}
	decision, err := EvaluateQualification(input)
	if err != nil {
		return fmt.Errorf("embedded-model qualification report: validate canonical evidence: %w", err)
	}
	if !reflect.DeepEqual(originalDecision, decision) {
		return fmt.Errorf("embedded-model qualification report: canonical evidence changes the evaluated decision")
	}
	report.Decision = decision
	evidence, err := validateQualificationEvidence(input)
	if err != nil {
		return fmt.Errorf("embedded-model qualification report: derive evidence: %w", err)
	}
	report.Derived = deriveQualificationReport(input, evidence)

	jsonBytes, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("embedded-model qualification report: encode JSON: %w", err)
	}
	jsonBytes = append(jsonBytes, '\n')
	markdown := []byte(renderQualificationMarkdown(report))
	if err := publishQualificationReportPair(dir, jsonBytes, markdown, hook); err != nil {
		return err
	}
	return nil
}

func canonicalQualificationReport(report QualificationReport) (QualificationReport, error) {
	raw, err := json.Marshal(report)
	if err != nil {
		return QualificationReport{}, err
	}
	var out QualificationReport
	if err := json.Unmarshal(raw, &out); err != nil {
		return QualificationReport{}, err
	}
	out.Derived = QualificationReportDerived{}
	sort.Slice(out.Observations, func(i, j int) bool {
		if out.Observations[i].Arm != out.Observations[j].Arm {
			return out.Observations[i].Arm < out.Observations[j].Arm
		}
		return out.Observations[i].QueryID < out.Observations[j].QueryID
	})
	sort.Slice(out.BuildDigests, func(i, j int) bool {
		if out.BuildDigests[i].Arm != out.BuildDigests[j].Arm {
			return out.BuildDigests[i].Arm < out.BuildDigests[j].Arm
		}
		return out.BuildDigests[i].Build < out.BuildDigests[j].Build
	})
	for i := range out.OracleControls {
		canonicalizeOracleBundle(&out.OracleControls[i].CurrentCandidatesOraclePacker)
		canonicalizeOracleBundle(&out.OracleControls[i].OracleCandidateCurrentSelector)
		canonicalizeOracleBundle(&out.OracleControls[i].OracleCandidateOraclePacker)
	}
	sort.Slice(out.OracleControls, func(i, j int) bool {
		return out.OracleControls[i].CurrentCandidatesOraclePacker.QueryID < out.OracleControls[j].CurrentCandidatesOraclePacker.QueryID
	})
	sort.Slice(out.Operating.QueryEmbedLatencies, func(i, j int) bool {
		return out.Operating.QueryEmbedLatencies[i] < out.Operating.QueryEmbedLatencies[j]
	})

	// Each BlindEvidenceSet is already content-addressed. Its nested slice
	// order is part of the imported source identity and must remain exact;
	// only the outer collection of independently sealed subjects is set-like.
	sort.Slice(out.BlindEvidence, func(i, j int) bool {
		return blindSubjectKey(out.BlindEvidence[i].Arm, out.BlindEvidence[i].ControlKind) < blindSubjectKey(out.BlindEvidence[j].Arm, out.BlindEvidence[j].ControlKind)
	})
	sort.Slice(out.BlindDecisions, func(i, j int) bool {
		left := blindSubjectKey(out.BlindDecisions[i].Arm, out.BlindDecisions[i].ControlKind)
		right := blindSubjectKey(out.BlindDecisions[j].Arm, out.BlindDecisions[j].ControlKind)
		if left != right {
			return left < right
		}
		return out.BlindDecisions[i].QueryID < out.BlindDecisions[j].QueryID
	})
	return out, nil
}

func canonicalizeOracleBundle(bundle *OracleBundle) {
	sort.Slice(bundle.Payload.TokenCounts, func(i, j int) bool {
		if bundle.Payload.TokenCounts[i].TokenizerID != bundle.Payload.TokenCounts[j].TokenizerID {
			return bundle.Payload.TokenCounts[i].TokenizerID < bundle.Payload.TokenCounts[j].TokenizerID
		}
		return bundle.Payload.TokenCounts[i].VocabularySHA256 < bundle.Payload.TokenCounts[j].VocabularySHA256
	})
}

func qualificationInputFromReport(report QualificationReport) (QualificationInput, error) {
	if strings.TrimSpace(report.Dataset.Path) == "" || !isLowerHexDigest(report.Dataset.SHA256, 64) || len(report.Dataset.RawBytes) == 0 {
		return QualificationInput{}, fmt.Errorf("embedded-model qualification report: dataset evidence is incomplete")
	}
	if got := SHA256Hex(report.Dataset.RawBytes); got != report.Dataset.SHA256 {
		return QualificationInput{}, fmt.Errorf("embedded-model qualification report: dataset content digest differs: got %s want %s", got, report.Dataset.SHA256)
	}
	var dataset Dataset
	decoder := json.NewDecoder(bytes.NewReader(report.Dataset.RawBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&dataset); err != nil {
		return QualificationInput{}, fmt.Errorf("embedded-model qualification report: decode dataset: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return QualificationInput{}, fmt.Errorf("embedded-model qualification report: dataset has trailing JSON value")
		}
		return QualificationInput{}, fmt.Errorf("embedded-model qualification report: dataset has trailing bytes: %w", err)
	}
	loaded := &Loaded{Dataset: &dataset, Path: report.Dataset.Path, Raw: append([]byte(nil), report.Dataset.RawBytes...), SHA256: report.Dataset.SHA256}
	return QualificationInput{
		Preregistration: report.Preregistration,
		Dataset:         loaded,
		Observations:    report.Observations,
		BuildDigests:    report.BuildDigests,
		BlindEvidence:   report.BlindEvidence,
		Decisions:       report.BlindDecisions,
		OracleControls:  report.OracleControls,
		Operating:       report.Operating,
	}, nil
}

func deriveQualificationReport(in QualificationInput, evidence qualificationEvidence) QualificationReportDerived {
	derived := QualificationReportDerived{}
	for _, arm := range qualificationReportArms {
		derived.ArmTotals = append(derived.ArmTotals, QualificationArmTotal{Arm: arm, Passes: passCount(evidence.passes[arm]), Total: len(evidence.queryIDs)})
	}
	outcomes := pairedOutcomes(evidence, ArmCodeRank)
	for _, outcome := range outcomes {
		switch {
		case outcome.M3Pass && !outcome.M1Pass:
			derived.M3VersusM1.Wins++
		case outcome.M1Pass && !outcome.M3Pass:
			derived.M3VersusM1.Losses++
		default:
			derived.M3VersusM1.Ties++
		}
	}
	derived.M3VersusM1.Net = derived.M3VersusM1.Wins - derived.M3VersusM1.Losses
	derived.PairedBootstrap95 = PairedBootstrap95(outcomes, in.Preregistration.BootstrapSeed, in.Preregistration.BootstrapSamples)
	for _, stratum := range qualificationReportStrata {
		row := QualificationStratumDelta{Stratum: stratum}
		for _, queryID := range evidence.queryIDs {
			if evidence.strata[queryID] != stratum {
				continue
			}
			row.M1Passes += boolInt(evidence.passes[ArmPotion512][queryID])
			row.M3Passes += boolInt(evidence.passes[ArmCodeRank][queryID])
		}
		row.Delta = row.M3Passes - row.M1Passes
		derived.StratumDeltas = append(derived.StratumDeltas, row)
	}
	for _, arm := range qualificationReportArms {
		row := QualificationStageRetention{Arm: arm}
		for _, queryID := range evidence.queryIDs {
			observation := evidence.observations[arm][queryID]
			if observation.SemanticTop50.Present {
				row.SemanticTop50Present++
				row.SemanticBestRankSum += observation.SemanticTop50.BestRank
			}
			if observation.PostFusion.Present {
				row.PostFusionPresent++
			}
			if observation.CompleteGrade3Span {
				row.CompleteGrade3Span++
			}
			if observation.SemanticTop50.Present && observation.PostFusion.Present {
				row.SemanticToFusionRetained++
			}
			if observation.PostFusion.Present && observation.CompleteGrade3Span {
				row.FusionToBundleRetained++
			}
		}
		derived.StageRetention = append(derived.StageRetention, row)
	}
	builds := qualificationBuildsByArm(in.BuildDigests)
	for _, arm := range qualificationReportArms {
		first, second := builds[arm][1], builds[arm][2]
		derived.Reproducibility = append(derived.Reproducibility, QualificationReproducibility{
			Arm: arm, Build1SHA256: first.CaptureProvenance.SHA256, Build2SHA256: second.CaptureProvenance.SHA256,
			ByteIdentical:  compareQualificationBuildDigests(first, second) == nil,
			ComparedFields: "vector_bytes,persisted_rows,bundles,token_counts,oracle_payloads,oracle_token_counts,diagnostics",
		})
	}
	decisionCounts := make(map[string]int, 7)
	for _, decision := range in.Decisions {
		decisionCounts[blindSubjectKey(decision.Arm, decision.ControlKind)]++
	}
	for _, source := range in.BlindEvidence {
		derived.BlindEvidenceManifests = append(derived.BlindEvidenceManifests, QualificationBlindEvidenceManifest{
			Arm: source.Arm, ControlKind: source.ControlKind, SHA256: source.SHA256,
			Queries: len(source.PreRegistration.Queries), Responses: len(source.Responses), Grades: len(source.Grades),
			Adjudications: len(source.Adjudications), FinalDecisions: decisionCounts[blindSubjectKey(source.Arm, source.ControlKind)],
		})
	}
	sort.Slice(derived.BlindEvidenceManifests, func(i, j int) bool {
		return blindSubjectKey(derived.BlindEvidenceManifests[i].Arm, derived.BlindEvidenceManifests[i].ControlKind) <
			blindSubjectKey(derived.BlindEvidenceManifests[j].Arm, derived.BlindEvidenceManifests[j].ControlKind)
	})
	derived.RunValidity = qualificationRunValidity(evidence)
	for _, kind := range qualificationReportOracleKinds {
		derived.OracleBlindCeilings = append(derived.OracleBlindCeilings, QualificationOracleBlindCeiling{
			ControlKind: kind, Passes: passCount(evidence.oraclePasses[kind]), Total: len(evidence.queryIDs),
		})
	}
	return derived
}

func qualificationRunValidity(evidence qualificationEvidence) []GateResult {
	return []GateResult{
		gate("dataset_content_identity", true, "validated", "exact bytes, SHA-256, source checkout, and 64-query population agree"),
		gate("preregistration_valid", true, "validated", "all frozen inputs and thresholds valid"),
		gate("observations_complete", true, "256 unique arm/query observations", "4 arms x 64 queries"),
		gate("build_provenance_complete", true, "8 validated independent build records", "2 builds x 4 arms"),
		gate("blind_evidence_complete", true, "7 content-addressed evidence sets and 448 decisions", "4 arms + 3 oracle controls, each with 64 decisions"),
		gate("oracle_controls_complete", true, "64 complete control triplets", "64"),
		gate("state_ready", evidence.stateReady, fmt.Sprintf("%t", evidence.stateReady), "every M1-M3 observation ready"),
		gate("fingerprint_equality", evidence.fingerprintsOK, fmt.Sprintf("%t", evidence.fingerprintsOK), "every M1-M3 model/index fingerprint matches its preregistered pin"),
		gate("no_degradation", evidence.noDegradation, fmt.Sprintf("%t", evidence.noDegradation), "every M1-M3 observation has no sidecar degradation"),
		gate("required_diagnostics_available", evidence.diagnosticsAvailable, fmt.Sprintf("%t", evidence.diagnosticsAvailable), "every M1-M3 observation carries required diagnostics"),
	}
}

func qualificationBuildsByArm(digests []QualificationBuildDigest) map[QualificationArm]map[int]QualificationBuildDigest {
	byArm := make(map[QualificationArm]map[int]QualificationBuildDigest, len(qualificationReportArms))
	for _, arm := range qualificationReportArms {
		byArm[arm] = make(map[int]QualificationBuildDigest, 2)
	}
	for _, digest := range digests {
		byArm[digest.Arm][digest.Build] = digest
	}
	return byArm
}

func renderQualificationMarkdown(report QualificationReport) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# Embedded-model development qualification\n\n")
	fmt.Fprintf(&out, "Dataset content: `%s`  \nCandidate: `%s`  \nSource checkout: `%s`\n\n", report.Dataset.SHA256, report.Preregistration.CandidateSHA, report.Preregistration.SourceRepoSHA)

	out.WriteString("## Arm totals\n\n| Arm | Blind passes |\n|---|---:|\n")
	for _, row := range report.Derived.ArmTotals {
		fmt.Fprintf(&out, "| %s | %d/%d |\n", qualificationMarkdownCell(string(row.Arm)), row.Passes, row.Total)
	}
	pair := report.Derived.M3VersusM1
	fmt.Fprintf(&out, "\nM3 wins over M1: **%d**; M3 losses to M1: **%d**; ties: **%d**; net: **%+d**.  \n", pair.Wins, pair.Losses, pair.Ties, pair.Net)
	ci := report.Derived.PairedBootstrap95
	fmt.Fprintf(&out, "Paired bootstrap 95%%: point `%.6f`, interval `[%.6f, %.6f]`, algorithm `%s`, seed `%d`, samples `%d`.\n\n", ci.Point, ci.Lower, ci.Upper, qualificationBootstrapAlgorithm, report.Preregistration.BootstrapSeed, report.Preregistration.BootstrapSamples)

	out.WriteString("## Stratum deltas\n\n| Stratum | M1 | M3 | M3-M1 |\n|---|---:|---:|---:|\n")
	for _, row := range report.Derived.StratumDeltas {
		fmt.Fprintf(&out, "| %s | %d | %d | %+d |\n", qualificationMarkdownCell(row.Stratum), row.M1Passes, row.M3Passes, row.Delta)
	}

	out.WriteString("\n## Stage rank and retention\n\n| Arm | Semantic top-50 | Post-fusion | Complete span | Semantic→fusion retained | Fusion→bundle retained | Semantic best-rank sum |\n|---|---:|---:|---:|---:|---:|---:|\n")
	for _, row := range report.Derived.StageRetention {
		fmt.Fprintf(&out, "| %s | %d | %d | %d | %d | %d | %d |\n", qualificationMarkdownCell(string(row.Arm)), row.SemanticTop50Present, row.PostFusionPresent, row.CompleteGrade3Span, row.SemanticToFusionRetained, row.FusionToBundleRetained, row.SemanticBestRankSum)
	}
	for _, evidence := range report.Decision.Evidence {
		fmt.Fprintf(&out, "\nRank evidence `%s`: `%s` via `%s`.\n", qualificationMarkdownInline(evidence.Name), qualificationMarkdownInline(evidence.Observed), qualificationMarkdownInline(evidence.Algorithm))
	}

	out.WriteString("\n## Independent builds and provenance\n\n")
	builds := append([]QualificationBuildDigest(nil), report.BuildDigests...)
	sort.Slice(builds, func(i, j int) bool {
		if builds[i].Arm != builds[j].Arm {
			return builds[i].Arm < builds[j].Arm
		}
		return builds[i].Build < builds[j].Build
	})
	for _, build := range builds {
		fmt.Fprintf(&out, "### %s build %d\n\n", qualificationMarkdownInline(string(build.Arm)), build.Build)
		fmt.Fprintf(&out, "- Capture provenance: `%s`\n- Capture identity: `%s`\n", build.CaptureProvenance.SHA256, build.CaptureProvenance.CaptureIdentitySHA256)
		fmt.Fprintf(&out, "- Digests: vectors `%s`; persisted rows `%s`; bundles `%s`; token counts `%s`; oracle payloads `%s`; oracle token counts `%s`\n", build.VectorBytesSHA256, build.PersistedRowsSHA256, build.BundlesSHA256, build.TokenCountsSHA256, build.OraclePayloadsSHA256, build.OracleTokenCountsSHA256)
		fmt.Fprintf(&out, "- Diagnostics: admission truncations `%d`; document zero vectors `%d`\n\n", build.Diagnostics.AdmissionTruncations, build.Diagnostics.DocumentZeroVectors)
	}
	out.WriteString("| Reproducibility | Build 1 provenance | Build 2 provenance | Byte-identical |\n|---|---|---|---|\n")
	for _, row := range report.Derived.Reproducibility {
		fmt.Fprintf(&out, "| %s | `%s` | `%s` | %t |\n", qualificationMarkdownCell(string(row.Arm)), row.Build1SHA256, row.Build2SHA256, row.ByteIdentical)
	}

	out.WriteString("\n## Blind evidence manifests\n\n| Subject | Evidence SHA-256 | Queries | Responses | Grades | Adjudications | Decisions |\n|---|---|---:|---:|---:|---:|---:|\n")
	for _, manifest := range report.Derived.BlindEvidenceManifests {
		fmt.Fprintf(&out, "| %s | `%s` | %d | %d | %d | %d | %d |\n", qualificationMarkdownCell(blindSubjectKey(manifest.Arm, manifest.ControlKind)), manifest.SHA256, manifest.Queries, manifest.Responses, manifest.Grades, manifest.Adjudications, manifest.FinalDecisions)
	}

	p95, _ := operatingP95(report.Operating.QueryEmbedLatencies, report.Preregistration.Thresholds.MinQuerySamples)
	minimum, maximum := durationBounds(report.Operating.QueryEmbedLatencies)
	out.WriteString("\n## Operating measurements\n\n")
	fmt.Fprintf(&out, "- CPU-only: `%t`\n- Installed artifacts: `%d` bytes\n- Peak additional sidecar RSS: `%d` bytes\n", report.Operating.CPUOnly, report.Operating.ArtifactBytes, report.Operating.PeakAdditionalSidecarRSSBytes)
	fmt.Fprintf(&out, "- Query embeddings: `%d` samples, min `%s`, p95 `%s`, max `%s`\n- Full reindex: `%s`\n", len(report.Operating.QueryEmbedLatencies), minimum, p95, maximum, report.Operating.FullReindex)

	out.WriteString("\n## Run validity\n\n| Check | Passed | Observed | Required |\n|---|---|---|---|\n")
	for _, check := range report.Derived.RunValidity {
		fmt.Fprintf(&out, "| %s | %t | %s | %s |\n", qualificationMarkdownCell(check.Name), check.Passed, qualificationMarkdownCell(check.Observed), qualificationMarkdownCell(check.Required))
	}

	out.WriteString("\n## Oracle blind ceilings\n\n| Control | Blind passes |\n|---|---:|\n")
	for _, row := range report.Derived.OracleBlindCeilings {
		fmt.Fprintf(&out, "| %s | %d/%d |\n", qualificationMarkdownCell(row.ControlKind), row.Passes, row.Total)
	}

	out.WriteString("\n## Promotion gates\n\n| Gate | Passed | Observed | Required |\n|---|---|---|---|\n")
	for _, result := range report.Decision.Gates {
		fmt.Fprintf(&out, "| %s | %t | %s | %s |\n", qualificationMarkdownCell(result.Name), result.Passed, qualificationMarkdownCell(result.Observed), qualificationMarkdownCell(result.Required))
	}
	fmt.Fprintf(&out, "\nBranch: `%s`\n\n", qualificationMarkdownInline(report.Decision.Branch))
	if report.Decision.Promote {
		out.WriteString("DEVELOPMENT PROMOTION: YES\n")
	} else {
		out.WriteString("DEVELOPMENT PROMOTION: NO\n")
	}
	return out.String()
}

func qualificationMarkdownCell(value string) string {
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, value)
	value = html.EscapeString(value)
	value = strings.ReplaceAll(value, "`", "&#96;")
	return strings.ReplaceAll(value, "|", "\\|")
}

func qualificationMarkdownInline(value string) string {
	value = qualificationMarkdownCell(value)
	return strings.ReplaceAll(value, "\\|", "|")
}

func durationBounds(values []time.Duration) (time.Duration, time.Duration) {
	if len(values) == 0 {
		return 0, 0
	}
	minimum, maximum := values[0], values[0]
	for _, value := range values[1:] {
		if value < minimum {
			minimum = value
		}
		if value > maximum {
			maximum = value
		}
	}
	return minimum, maximum
}

type qualificationOutputDirectory struct {
	path string
	info os.FileInfo
	file *os.File
}

func openQualificationOutputDirectory(path string) (*qualificationOutputDirectory, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("embedded-model qualification report: inspect output directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("embedded-model qualification report: output directory must not be a symlink")
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("embedded-model qualification report: output path is not a directory")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("embedded-model qualification report: open output directory: %w", err)
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		_ = file.Close()
		return nil, fmt.Errorf("embedded-model qualification report: output directory changed while opening")
	}
	return &qualificationOutputDirectory{path: path, info: info, file: file}, nil
}

func (dir *qualificationOutputDirectory) close() { _ = dir.file.Close() }

func (dir *qualificationOutputDirectory) checkStable() error {
	current, err := os.Lstat(dir.path)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(dir.info, current) {
		return fmt.Errorf("embedded-model qualification report: output directory changed during publication")
	}
	return nil
}

func (dir *qualificationOutputDirectory) sync() error {
	if err := dir.checkStable(); err != nil {
		return err
	}
	if err := dir.file.Sync(); err != nil {
		return fmt.Errorf("embedded-model qualification report: sync output directory: %w", err)
	}
	return nil
}

func sealQualificationReportCommit(marker QualificationReportCommit) (QualificationReportCommit, error) {
	marker.SHA256 = ""
	address, err := ContentAddress(marker, func(v *QualificationReportCommit) { v.SHA256 = "" })
	if err != nil {
		return QualificationReportCommit{}, err
	}
	marker.SHA256 = address
	return marker, nil
}

func qualificationReportCommitBytes(jsonBytes, markdown []byte) ([]byte, QualificationReportCommit, error) {
	marker, err := sealQualificationReportCommit(QualificationReportCommit{
		SchemaVersion: QualificationReportCommitSchemaVersion, ReportSchemaVersion: QualificationReportSchemaVersion,
		QualificationJSONSHA256: SHA256Hex(jsonBytes), QualificationMarkdownSHA256: SHA256Hex(markdown),
	})
	if err != nil {
		return nil, QualificationReportCommit{}, err
	}
	raw, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return nil, QualificationReportCommit{}, err
	}
	return append(raw, '\n'), marker, nil
}

func readQualificationReportCommit(path string) (QualificationReportCommit, error) {
	raw, err := readQualificationRegularNoFollow(path)
	if err != nil {
		return QualificationReportCommit{}, err
	}
	var marker QualificationReportCommit
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&marker); err != nil {
		return QualificationReportCommit{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return QualificationReportCommit{}, fmt.Errorf("commit marker has trailing data")
	}
	sealed, err := sealQualificationReportCommit(marker)
	if err != nil || marker.SchemaVersion != QualificationReportCommitSchemaVersion || marker.ReportSchemaVersion != QualificationReportSchemaVersion ||
		!isLowerHexDigest(marker.QualificationJSONSHA256, 64) || !isLowerHexDigest(marker.QualificationMarkdownSHA256, 64) ||
		!isLowerHexDigest(marker.SHA256, 64) || sealed.SHA256 != marker.SHA256 {
		return QualificationReportCommit{}, fmt.Errorf("commit marker content address or schema differs")
	}
	return marker, nil
}

func readQualificationRegularNoFollow(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular no-follow file", filepath.Base(path))
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("%s changed while opening", filepath.Base(path))
	}
	return io.ReadAll(file)
}

// ValidateQualificationReportPublication accepts only a committed three-file
// publication. The physical JSON/Markdown writes are not a transaction on
// POSIX filesystems; the content-addressed marker is the atomic logical commit.
func ValidateQualificationReportPublication(path string) error {
	dir, err := openQualificationOutputDirectory(path)
	if err != nil {
		return err
	}
	defer dir.close()
	return validateQualificationReportPublication(dir)
}

func validateQualificationReportPublication(dir *qualificationOutputDirectory) error {
	if err := dir.checkStable(); err != nil {
		return err
	}
	marker, err := readQualificationReportCommit(filepath.Join(dir.path, qualificationReportCommitName))
	if err != nil {
		return fmt.Errorf("embedded-model qualification report: no valid authoritative commit marker: %w", err)
	}
	for _, artifact := range []struct{ name, digest string }{
		{qualificationReportJSONName, marker.QualificationJSONSHA256},
		{qualificationReportMarkdownName, marker.QualificationMarkdownSHA256},
	} {
		raw, readErr := readQualificationRegularNoFollow(filepath.Join(dir.path, artifact.name))
		if readErr != nil {
			return fmt.Errorf("embedded-model qualification report: committed %s is missing or unsafe: %w", artifact.name, readErr)
		}
		if got := SHA256Hex(raw); got != artifact.digest {
			return fmt.Errorf("embedded-model qualification report: committed %s digest %s differs from marker %s", artifact.name, got, artifact.digest)
		}
	}
	return dir.checkStable()
}

func publishQualificationReportPair(path string, jsonBytes, markdown []byte, hook qualificationPublishHook) (err error) {
	dir, err := openQualificationOutputDirectory(path)
	if err != nil {
		return err
	}
	defer dir.close()
	if err := prepareQualificationReportPublication(dir); err != nil {
		return err
	}
	markerBytes, _, err := qualificationReportCommitBytes(jsonBytes, markdown)
	if err != nil {
		return fmt.Errorf("embedded-model qualification report: encode commit marker: %w", err)
	}

	jsonTemp, err := writeQualificationReportTemp(path, ".qualification-json-", jsonBytes)
	if err != nil {
		return err
	}
	defer removeQualificationReportTemp(dir, jsonTemp)
	markdownTemp, err := writeQualificationReportTemp(path, ".qualification-markdown-", markdown)
	if err != nil {
		return err
	}
	defer removeQualificationReportTemp(dir, markdownTemp)
	markerTemp, err := writeQualificationReportTemp(path, ".qualification-marker-", markerBytes)
	if err != nil {
		return err
	}
	defer removeQualificationReportTemp(dir, markerTemp)

	created := make([]string, 0, 4)
	committed := false
	defer func() {
		if committed {
			return
		}
		if dir.checkStable() == nil {
			for i := len(created) - 1; i >= 0; i-- {
				_ = os.Remove(created[i])
			}
			_ = dir.sync()
		}
	}()
	publish := func(temp, name string) error {
		if err := dir.checkStable(); err != nil {
			return err
		}
		target := filepath.Join(path, name)
		if err := os.Link(temp, target); err != nil {
			return fmt.Errorf("embedded-model qualification report: publish %s: %w", name, err)
		}
		created = append(created, target)
		return nil
	}
	if err := publish(markerTemp, qualificationReportTransactionName); err != nil {
		return err
	}
	if err := dir.sync(); err != nil {
		return err
	}
	if err := publish(jsonTemp, qualificationReportJSONName); err != nil {
		return err
	}
	if hook != nil {
		if err := hook(qualificationPublishAfterJSON); err != nil {
			return fmt.Errorf("embedded-model qualification report: publication hook: %w", err)
		}
	}
	if err := publish(markdownTemp, qualificationReportMarkdownName); err != nil {
		return err
	}
	if err := dir.sync(); err != nil {
		return err
	}
	if err := publish(markerTemp, qualificationReportCommitName); err != nil {
		return err
	}
	if err := dir.sync(); err != nil {
		return err
	}
	if err := validateQualificationReportPublication(dir); err != nil {
		return err
	}
	if err := dir.checkStable(); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(path, qualificationReportTransactionName)); err != nil {
		return fmt.Errorf("embedded-model qualification report: remove completed transaction marker: %w", err)
	}
	if err := dir.sync(); err != nil {
		return err
	}
	committed = true
	return nil
}

func prepareQualificationReportPublication(dir *qualificationOutputDirectory) error {
	commitPath := filepath.Join(dir.path, qualificationReportCommitName)
	if _, err := os.Lstat(commitPath); err == nil {
		if validationErr := validateQualificationReportPublication(dir); validationErr != nil {
			return fmt.Errorf("embedded-model qualification report: existing committed report is invalid and will not be overwritten: %w", validationErr)
		}
		return fmt.Errorf("embedded-model qualification report: %s already exists", qualificationReportCommitName)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("embedded-model qualification report: inspect commit marker: %w", err)
	}
	transactionPath := filepath.Join(dir.path, qualificationReportTransactionName)
	if _, err := os.Lstat(transactionPath); err == nil {
		marker, readErr := readQualificationReportCommit(transactionPath)
		if readErr != nil {
			return fmt.Errorf("embedded-model qualification report: markerless transaction is not owned: %w", readErr)
		}
		for _, artifact := range []struct{ name, digest string }{
			{qualificationReportJSONName, marker.QualificationJSONSHA256},
			{qualificationReportMarkdownName, marker.QualificationMarkdownSHA256},
		} {
			artifactPath := filepath.Join(dir.path, artifact.name)
			_, statErr := os.Lstat(artifactPath)
			if os.IsNotExist(statErr) {
				continue
			}
			if statErr != nil {
				return fmt.Errorf("embedded-model qualification report: inspect markerless %s: %w", artifact.name, statErr)
			}
			raw, readErr := readQualificationRegularNoFollow(artifactPath)
			if readErr != nil || SHA256Hex(raw) != artifact.digest {
				return fmt.Errorf("embedded-model qualification report: markerless transaction does not own %s", artifact.name)
			}
		}
		for _, name := range []string{qualificationReportJSONName, qualificationReportMarkdownName, qualificationReportTransactionName} {
			if stableErr := dir.checkStable(); stableErr != nil {
				return stableErr
			}
			if removeErr := os.Remove(filepath.Join(dir.path, name)); removeErr != nil && !os.IsNotExist(removeErr) {
				return fmt.Errorf("embedded-model qualification report: clean markerless transaction: %w", removeErr)
			}
		}
		return dir.sync()
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("embedded-model qualification report: inspect transaction marker: %w", err)
	}
	for _, name := range []string{qualificationReportJSONName, qualificationReportMarkdownName} {
		if _, err := os.Lstat(filepath.Join(dir.path, name)); err == nil {
			return fmt.Errorf("embedded-model qualification report: unowned markerless %s already exists", name)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("embedded-model qualification report: inspect %s: %w", name, err)
		}
	}
	return nil
}

func removeQualificationReportTemp(dir *qualificationOutputDirectory, path string) {
	if dir.checkStable() == nil {
		_ = os.Remove(path)
	}
}

func writeQualificationReportTemp(dir, pattern string, content []byte) (path string, err error) {
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", fmt.Errorf("embedded-model qualification report: create staging file: %w", err)
	}
	path = file.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	if err = file.Chmod(0o644); err == nil {
		_, err = file.Write(content)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", fmt.Errorf("embedded-model qualification report: stage file: %w", err)
	}
	return path, nil
}
