package retrieval

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
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

type qualificationPublishEvent struct {
	Stage       string
	Root        *os.Root
	Transaction qualificationReportTransaction
}

type qualificationPublishHook func(event qualificationPublishEvent) error

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
			ComparedFields: "vector_bytes,persisted_rows,bundles,token_counts,oracle_payloads,oracle_token_counts,query_diagnostics,diagnostics",
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

const (
	qualificationTransactionSchemaVersion    = 2
	qualificationPublishBeforeRecoveryDelete = "before_recovery_delete"
	qualificationPublishBeforeRollback       = "before_rollback"
)

type qualificationOwnedMember struct {
	FinalName  string `json:"final_name"`
	StagedName string `json:"staged_name"`
	SHA256     string `json:"sha256"`
	Identity   string `json:"identity"`
}

type qualificationReportTransaction struct {
	SchemaVersion         int                      `json:"schema_version"`
	ID                    string                   `json:"id"`
	RootIdentity          string                   `json:"root_identity"`
	TransactionStagedName string                   `json:"transaction_staged_name"`
	JSON                  qualificationOwnedMember `json:"json"`
	Markdown              qualificationOwnedMember `json:"markdown"`
	Commit                qualificationOwnedMember `json:"commit"`
	SHA256                string                   `json:"sha256"`
}

type qualificationOutputDirectory struct {
	root         *os.Root
	durability   qualificationDirectoryDurability
	rootIdentity string
}

type qualificationDirectoryDurability interface {
	identity() string
	sync() error
	close() error
}

type qualificationStagedFile struct {
	info     os.FileInfo
	identity string
}

// The publication policy rejects every symlink alias in the supplied path,
// including parent components. Callers must pass the resolved physical path.
// After OpenRoot succeeds, all access is relative to that stable opened root.
func openQualificationOutputDirectory(path string) (*qualificationOutputDirectory, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("embedded-model qualification report: resolve output directory: %w", err)
	}
	abs = filepath.Clean(abs)
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("embedded-model qualification report: resolve output directory symlinks: %w", err)
	}
	if filepath.Clean(resolved) != abs {
		return nil, fmt.Errorf("embedded-model qualification report: output directory or parent is a symlink alias; pass resolved path %s", resolved)
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("embedded-model qualification report: output path is not a real directory")
	}
	durability, err := openQualificationDirectoryDurability(abs)
	if err != nil {
		return nil, fmt.Errorf("embedded-model qualification report: open durable output directory before mutation: %w", err)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("embedded-model qualification report: open output root: %w", err), durability.close())
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.Join(fmt.Errorf("embedded-model qualification report: output directory changed while opening"), root.Close(), durability.close())
	}
	// This one pre-mutation handle only proves that os.Root and the retained
	// durability handle name the same directory. It is never used for syncing.
	rootFile, err := root.Open(".")
	if err != nil {
		return nil, errors.Join(fmt.Errorf("embedded-model qualification report: open root identity handle: %w", err), root.Close(), durability.close())
	}
	rootFileInfo, statErr := rootFile.Stat()
	rootIdentity, identityErr := qualificationFileIdentity(rootFile)
	rootFileCloseErr := rootFile.Close()
	if statErr != nil || identityErr != nil || rootFileCloseErr != nil || !os.SameFile(opened, rootFileInfo) || rootIdentity != durability.identity() {
		return nil, errors.Join(
			fmt.Errorf("embedded-model qualification report: durable directory handle and output root identities differ"),
			statErr, identityErr, rootFileCloseErr, root.Close(), durability.close(),
		)
	}
	// Capability probe before any mutation. Platforms that cannot durably flush
	// directory entries must fail here rather than pretending publication is durable.
	if err := durability.sync(); err != nil {
		return nil, errors.Join(fmt.Errorf("embedded-model qualification report: durable directory sync unsupported: %w", err), root.Close(), durability.close())
	}
	return &qualificationOutputDirectory{root: root, durability: durability, rootIdentity: rootIdentity}, nil
}

func (dir *qualificationOutputDirectory) close() error {
	return errors.Join(dir.root.Close(), dir.durability.close())
}

func (dir *qualificationOutputDirectory) sync() error {
	if err := dir.durability.sync(); err != nil {
		return fmt.Errorf("embedded-model qualification report: sync output root: %w", err)
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

func readQualificationReportCommit(root *os.Root, name string) (QualificationReportCommit, os.FileInfo, error) {
	raw, info, _, err := readQualificationRegularNoFollow(root, name)
	if err != nil {
		return QualificationReportCommit{}, nil, err
	}
	var marker QualificationReportCommit
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&marker); err != nil {
		return QualificationReportCommit{}, nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return QualificationReportCommit{}, nil, fmt.Errorf("commit marker has trailing data")
	}
	sealed, err := sealQualificationReportCommit(marker)
	if err != nil || marker.SchemaVersion != QualificationReportCommitSchemaVersion || marker.ReportSchemaVersion != QualificationReportSchemaVersion ||
		!isLowerHexDigest(marker.QualificationJSONSHA256, 64) || !isLowerHexDigest(marker.QualificationMarkdownSHA256, 64) ||
		!isLowerHexDigest(marker.SHA256, 64) || sealed.SHA256 != marker.SHA256 {
		return QualificationReportCommit{}, nil, fmt.Errorf("commit marker content address or schema differs")
	}
	return marker, info, nil
}

func readQualificationRegularNoFollow(root *os.Root, name string) ([]byte, os.FileInfo, string, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, nil, "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, nil, "", fmt.Errorf("%s is not a regular no-follow file", name)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, nil, "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, nil, "", fmt.Errorf("%s changed while opening", name)
	}
	identity, err := qualificationFileIdentity(file)
	if err != nil {
		return nil, nil, "", fmt.Errorf("read immutable identity for %s: %w", name, err)
	}
	raw, err := io.ReadAll(file)
	return raw, opened, identity, err
}

// ValidateQualificationReportPublication accepts only a committed three-file
// publication. The physical JSON/Markdown writes are not a transaction on
// POSIX filesystems; the content-addressed marker is the atomic logical commit.
func ValidateQualificationReportPublication(path string) (err error) {
	dir, err := openQualificationOutputDirectory(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dir.close()) }()
	return validateQualificationReportPublication(dir.root)
}

func validateQualificationReportPublication(root *os.Root) error {
	marker, _, err := readQualificationReportCommit(root, qualificationReportCommitName)
	if err != nil {
		return fmt.Errorf("embedded-model qualification report: no valid authoritative commit marker: %w", err)
	}
	for _, artifact := range []struct{ name, digest string }{
		{qualificationReportJSONName, marker.QualificationJSONSHA256},
		{qualificationReportMarkdownName, marker.QualificationMarkdownSHA256},
	} {
		raw, _, _, readErr := readQualificationRegularNoFollow(root, artifact.name)
		if readErr != nil {
			return fmt.Errorf("embedded-model qualification report: committed %s is missing or unsafe: %w", artifact.name, readErr)
		}
		if got := SHA256Hex(raw); got != artifact.digest {
			return fmt.Errorf("embedded-model qualification report: committed %s digest %s differs from marker %s", artifact.name, got, artifact.digest)
		}
	}
	return nil
}

func publishQualificationReportPair(path string, jsonBytes, markdown []byte, hook qualificationPublishHook) (err error) {
	dir, err := openQualificationOutputDirectory(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dir.close()) }()
	if err := prepareQualificationReportPublication(dir, hook); err != nil {
		return err
	}
	markerBytes, _, err := qualificationReportCommitBytes(jsonBytes, markdown)
	if err != nil {
		return fmt.Errorf("embedded-model qualification report: encode commit marker: %w", err)
	}
	nonce, err := qualificationTransactionNonce()
	if err != nil {
		return err
	}
	prefix := ".qualification." + nonce + "."
	tx := qualificationReportTransaction{
		SchemaVersion:         qualificationTransactionSchemaVersion,
		ID:                    nonce,
		RootIdentity:          dir.rootIdentity,
		TransactionStagedName: prefix + "transaction.stage",
	}
	staged := make(map[string]qualificationStagedFile, 4)
	for _, item := range []struct {
		member *qualificationOwnedMember
		name   string
		final  string
		raw    []byte
	}{
		{&tx.JSON, prefix + "json.stage", qualificationReportJSONName, jsonBytes},
		{&tx.Markdown, prefix + "markdown.stage", qualificationReportMarkdownName, markdown},
		{&tx.Commit, prefix + "commit.stage", qualificationReportCommitName, markerBytes},
	} {
		stage, stageErr := writeQualificationReportStage(dir.root, item.name, item.raw)
		if stageErr != nil {
			cleanupErr := cleanupUnpublishedStages(dir, tx, staged)
			return errors.Join(stageErr, cleanupErr)
		}
		staged[item.name] = stage
		*item.member = qualificationOwnedMember{FinalName: item.final, StagedName: item.name, SHA256: SHA256Hex(item.raw), Identity: stage.identity}
	}
	tx, err = sealQualificationReportTransaction(tx)
	if err != nil {
		return errors.Join(err, cleanupUnpublishedStages(dir, tx, staged))
	}
	txBytes, err := json.MarshalIndent(tx, "", "  ")
	if err != nil {
		return errors.Join(err, cleanupUnpublishedStages(dir, tx, staged))
	}
	txBytes = append(txBytes, '\n')
	txStage, err := writeQualificationReportStage(dir.root, tx.TransactionStagedName, txBytes)
	if err != nil {
		return errors.Join(err, cleanupUnpublishedStages(dir, tx, staged))
	}
	staged[tx.TransactionStagedName] = txStage
	if err = dir.root.Link(tx.TransactionStagedName, qualificationReportTransactionName); err != nil {
		cleanupErr := cleanupUnpublishedStages(dir, tx, staged)
		return errors.Join(fmt.Errorf("embedded-model qualification report: publish transaction ownership: %w", err), cleanupErr)
	}
	published := false
	defer func() {
		if published || err == nil {
			return
		}
		if hook != nil {
			err = errors.Join(err, hook(qualificationPublishEvent{Stage: qualificationPublishBeforeRollback, Root: dir.root, Transaction: tx}))
		}
		if rollbackErr := cleanupQualificationTransaction(dir, tx, staged, true); rollbackErr != nil {
			err = errors.Join(err, fmt.Errorf("embedded-model qualification report: rollback incomplete cleanup: %w", rollbackErr))
		}
	}()
	if err := dir.sync(); err != nil {
		return err
	}
	if err = dir.root.Link(tx.JSON.StagedName, tx.JSON.FinalName); err != nil {
		return err
	}
	if hook != nil {
		if err = hook(qualificationPublishEvent{Stage: qualificationPublishAfterJSON, Root: dir.root, Transaction: tx}); err != nil {
			return fmt.Errorf("embedded-model qualification report: publication hook: %w", err)
		}
	}
	if err = dir.root.Link(tx.Markdown.StagedName, tx.Markdown.FinalName); err != nil {
		return err
	}
	if err := dir.sync(); err != nil {
		return err
	}
	if err = dir.root.Link(tx.Commit.StagedName, tx.Commit.FinalName); err != nil {
		return err
	}
	if err := dir.sync(); err != nil {
		return err
	}
	if err = validateQualificationReportPublication(dir.root); err != nil {
		return err
	}
	if err = cleanupQualificationTransaction(dir, tx, staged, false); err != nil {
		return fmt.Errorf("embedded-model qualification report: committed but cleanup incomplete: %w", err)
	}
	published = true
	return nil
}

func prepareQualificationReportPublication(dir *qualificationOutputDirectory, hook qualificationPublishHook) error {
	if _, err := dir.root.Lstat(qualificationReportCommitName); err == nil {
		if validationErr := validateQualificationReportPublication(dir.root); validationErr != nil {
			return fmt.Errorf("embedded-model qualification report: existing committed report is invalid and will not be overwritten: %w", validationErr)
		}
		return fmt.Errorf("embedded-model qualification report: %s already exists", qualificationReportCommitName)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("embedded-model qualification report: inspect commit marker: %w", err)
	}
	if _, err := dir.root.Lstat(qualificationReportTransactionName); err == nil {
		tx, staged, readErr := readQualificationReportTransaction(dir)
		if readErr != nil {
			return fmt.Errorf("embedded-model qualification report: transaction replay or foreign marker: %w", readErr)
		}
		if hook != nil {
			if hookErr := hook(qualificationPublishEvent{Stage: qualificationPublishBeforeRecoveryDelete, Root: dir.root, Transaction: tx}); hookErr != nil {
				return hookErr
			}
		}
		if recoveryErr := cleanupQualificationTransaction(dir, tx, staged, true); recoveryErr != nil {
			return fmt.Errorf("embedded-model qualification report: recovery refused foreign or replaced member: %w", recoveryErr)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("embedded-model qualification report: inspect transaction marker: %w", err)
	}
	for _, name := range []string{qualificationReportJSONName, qualificationReportMarkdownName} {
		if _, err := dir.root.Lstat(name); err == nil {
			return fmt.Errorf("embedded-model qualification report: unowned markerless %s already exists", name)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("embedded-model qualification report: inspect %s: %w", name, err)
		}
	}
	return nil
}

func qualificationTransactionNonce() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("embedded-model qualification report: random transaction id: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

func sealQualificationReportTransaction(tx qualificationReportTransaction) (qualificationReportTransaction, error) {
	tx.SHA256 = ""
	address, err := ContentAddress(tx, func(v *qualificationReportTransaction) { v.SHA256 = "" })
	if err != nil {
		return qualificationReportTransaction{}, err
	}
	tx.SHA256 = address
	return tx, nil
}

func writeQualificationReportStage(root *os.Root, name string, content []byte) (qualificationStagedFile, error) {
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return qualificationStagedFile{}, fmt.Errorf("embedded-model qualification report: create stage %s: %w", name, err)
	}
	identity, identityErr := file.Stat()
	if identityErr != nil {
		closeErr := file.Close()
		return qualificationStagedFile{}, errors.Join(
			fmt.Errorf("embedded-model qualification report: stat new stage %s: %w", name, identityErr),
			closeErr,
			fmt.Errorf("embedded-model qualification report: cleanup incomplete for unverifiable stage %s", name),
		)
	}
	identityAddress, identityErr := qualificationFileIdentity(file)
	if identityErr != nil {
		closeErr := file.Close()
		cleanupErr := removeQualificationOwnedInfo(root, name, identity)
		return qualificationStagedFile{}, errors.Join(
			fmt.Errorf("embedded-model qualification report: identify new stage %s: %w", name, identityErr),
			closeErr,
			cleanupErr,
		)
	}
	stage := qualificationStagedFile{info: identity, identity: identityAddress}
	_, err = file.Write(content)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		cleanupErr := removeQualificationOwnedName(root, name, stage)
		return qualificationStagedFile{}, errors.Join(fmt.Errorf("embedded-model qualification report: write stage %s: %w", name, err), cleanupErr)
	}
	if err := verifyQualificationOwnedFile(root, name, SHA256Hex(content), identityAddress, identity); err != nil {
		cleanupErr := removeQualificationOwnedName(root, name, stage)
		return qualificationStagedFile{}, errors.Join(fmt.Errorf("embedded-model qualification report: inspect stage %s: %w", name, err), cleanupErr)
	}
	return stage, nil
}

func readQualificationReportTransaction(dir *qualificationOutputDirectory) (qualificationReportTransaction, map[string]qualificationStagedFile, error) {
	root := dir.root
	raw, commonInfo, commonIdentity, err := readQualificationRegularNoFollow(root, qualificationReportTransactionName)
	if err != nil {
		return qualificationReportTransaction{}, nil, err
	}
	var tx qualificationReportTransaction
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&tx); err != nil {
		return qualificationReportTransaction{}, nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return qualificationReportTransaction{}, nil, fmt.Errorf("transaction marker has trailing data")
	}
	sealed, err := sealQualificationReportTransaction(tx)
	prefix := ".qualification." + tx.ID + "."
	if err != nil || tx.SchemaVersion != qualificationTransactionSchemaVersion || !isLowerHexDigest(tx.ID, 64) || !isLowerHexDigest(tx.RootIdentity, 64) ||
		tx.TransactionStagedName != prefix+"transaction.stage" || tx.JSON.FinalName != qualificationReportJSONName || tx.JSON.StagedName != prefix+"json.stage" ||
		tx.Markdown.FinalName != qualificationReportMarkdownName || tx.Markdown.StagedName != prefix+"markdown.stage" ||
		tx.Commit.FinalName != qualificationReportCommitName || tx.Commit.StagedName != prefix+"commit.stage" ||
		!isLowerHexDigest(tx.JSON.Identity, 64) || !isLowerHexDigest(tx.Markdown.Identity, 64) || !isLowerHexDigest(tx.Commit.Identity, 64) ||
		sealed.SHA256 != tx.SHA256 {
		return qualificationReportTransaction{}, nil, fmt.Errorf("transaction replay has invalid identity, names, or content address")
	}
	if tx.RootIdentity != dir.rootIdentity {
		return qualificationReportTransaction{}, nil, fmt.Errorf("transaction root identity differs from opened output root")
	}
	staged := make(map[string]qualificationStagedFile, 4)
	for _, member := range []qualificationOwnedMember{tx.JSON, tx.Markdown, tx.Commit} {
		memberRaw, info, identity, readErr := readQualificationRegularNoFollow(root, member.StagedName)
		if readErr != nil || SHA256Hex(memberRaw) != member.SHA256 {
			return qualificationReportTransaction{}, nil, fmt.Errorf("transaction replay missing owned stage %s", member.StagedName)
		}
		if identity != member.Identity {
			return qualificationReportTransaction{}, nil, fmt.Errorf("transaction replay immutable identity differs for %s", member.StagedName)
		}
		staged[member.StagedName] = qualificationStagedFile{info: info, identity: identity}
	}
	txRaw, txInfo, txIdentity, err := readQualificationRegularNoFollow(root, tx.TransactionStagedName)
	if err != nil || SHA256Hex(txRaw) != SHA256Hex(raw) || txIdentity != commonIdentity || !os.SameFile(txInfo, commonInfo) {
		return qualificationReportTransaction{}, nil, fmt.Errorf("transaction replay ownership anchor differs")
	}
	staged[tx.TransactionStagedName] = qualificationStagedFile{info: txInfo, identity: txIdentity}
	return tx, staged, nil
}

func cleanupQualificationTransaction(dir *qualificationOutputDirectory, tx qualificationReportTransaction, staged map[string]qualificationStagedFile, removeFinals bool) error {
	if tx.RootIdentity != dir.rootIdentity {
		return fmt.Errorf("transaction root identity differs from opened output root")
	}
	// Preflight every deletion. If any final or ownership anchor was replaced,
	// no name is removed merely because its bytes happen to match.
	for _, member := range []qualificationOwnedMember{tx.JSON, tx.Markdown, tx.Commit} {
		stage, ok := staged[member.StagedName]
		if !ok {
			return fmt.Errorf("missing immutable stage identity for %s", member.StagedName)
		}
		if err := verifyQualificationOwnedFile(dir.root, member.StagedName, member.SHA256, member.Identity, stage.info); err != nil {
			return err
		}
		finalInfo, err := dir.root.Lstat(member.FinalName)
		if os.IsNotExist(err) {
			if removeFinals {
				continue
			}
			return fmt.Errorf("committed final member %s is missing", member.FinalName)
		}
		if err != nil || !os.SameFile(stage.info, finalInfo) {
			return fmt.Errorf("foreign or replaced final member %s", member.FinalName)
		}
	}
	txStage := staged[tx.TransactionStagedName]
	if err := verifyQualificationOwnedFile(dir.root, tx.TransactionStagedName, "", txStage.identity, txStage.info); err != nil {
		return err
	}
	commonInfo, err := dir.root.Lstat(qualificationReportTransactionName)
	if err != nil || !os.SameFile(txStage.info, commonInfo) {
		return fmt.Errorf("foreign or replayed transaction marker")
	}

	var cleanupErr error
	if removeFinals {
		for _, member := range []qualificationOwnedMember{tx.Commit, tx.Markdown, tx.JSON} {
			if _, err := dir.root.Lstat(member.FinalName); os.IsNotExist(err) {
				continue
			}
			if err := verifyQualificationLinkedIdentity(dir.root, member.FinalName, staged[member.StagedName]); err != nil {
				cleanupErr = errors.Join(cleanupErr, err)
				continue
			}
			cleanupErr = errors.Join(cleanupErr, dir.root.Remove(member.FinalName))
		}
	}
	for _, name := range []string{tx.JSON.StagedName, tx.Markdown.StagedName, tx.Commit.StagedName} {
		if !removeFinals {
			member := tx.JSON
			switch name {
			case tx.Markdown.StagedName:
				member = tx.Markdown
			case tx.Commit.StagedName:
				member = tx.Commit
			}
			if err := verifyQualificationLinkedIdentity(dir.root, member.FinalName, staged[name]); err != nil {
				cleanupErr = errors.Join(cleanupErr, err)
				continue
			}
		}
		if err := verifyQualificationOwnedFile(dir.root, name, "", staged[name].identity, staged[name].info); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
			continue
		}
		cleanupErr = errors.Join(cleanupErr, dir.root.Remove(name))
	}
	if err := verifyQualificationLinkedIdentity(dir.root, qualificationReportTransactionName, txStage); err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	} else {
		cleanupErr = errors.Join(cleanupErr, dir.root.Remove(qualificationReportTransactionName))
	}
	if err := verifyQualificationOwnedFile(dir.root, tx.TransactionStagedName, "", txStage.identity, txStage.info); err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	} else {
		cleanupErr = errors.Join(cleanupErr, dir.root.Remove(tx.TransactionStagedName))
	}
	cleanupErr = errors.Join(cleanupErr, dir.sync())
	return cleanupErr
}

func cleanupUnpublishedStages(dir *qualificationOutputDirectory, tx qualificationReportTransaction, staged map[string]qualificationStagedFile) error {
	if tx.RootIdentity != dir.rootIdentity {
		return fmt.Errorf("transaction root identity differs from opened output root")
	}
	var err error
	for name, stage := range staged {
		if verifyErr := verifyQualificationOwnedFile(dir.root, name, "", stage.identity, stage.info); verifyErr != nil {
			err = errors.Join(err, verifyErr)
			continue
		}
		err = errors.Join(err, dir.root.Remove(name))
	}
	return errors.Join(err, dir.sync())
}

func verifyQualificationOwnedFile(root *os.Root, name, digest, identity string, want os.FileInfo) error {
	raw, got, gotIdentity, err := readQualificationRegularNoFollow(root, name)
	if err != nil || want == nil || !os.SameFile(want, got) || identity == "" || gotIdentity != identity {
		return fmt.Errorf("ownership identity for %s differs", name)
	}
	if digest != "" && SHA256Hex(raw) != digest {
		return fmt.Errorf("ownership content for %s differs", name)
	}
	return nil
}

func verifyQualificationLinkedIdentity(root *os.Root, name string, want qualificationStagedFile) error {
	_, got, identity, err := readQualificationRegularNoFollow(root, name)
	if err != nil || want.info == nil || !os.SameFile(want.info, got) || identity != want.identity {
		return fmt.Errorf("foreign or replaced member %s", name)
	}
	return nil
}

func removeQualificationOwnedName(root *os.Root, name string, stage qualificationStagedFile) error {
	if err := verifyQualificationLinkedIdentity(root, name, stage); err != nil {
		return fmt.Errorf("embedded-model qualification report: cleanup refused %s: %w", name, err)
	}
	if err := root.Remove(name); err != nil {
		return fmt.Errorf("embedded-model qualification report: cleanup %s: %w", name, err)
	}
	return nil
}

func removeQualificationOwnedInfo(root *os.Root, name string, identity os.FileInfo) error {
	current, err := root.Lstat(name)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(identity, current) {
		return fmt.Errorf("embedded-model qualification report: cleanup incomplete for unverifiable stage %s", name)
	}
	if err := root.Remove(name); err != nil {
		return fmt.Errorf("embedded-model qualification report: cleanup %s: %w", name, err)
	}
	return nil
}
