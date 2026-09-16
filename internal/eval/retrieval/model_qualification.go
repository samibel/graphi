package retrieval

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	// QualificationSchemaVersion is the only preregistration schema accepted by
	// the embedded-model qualification gate.
	QualificationSchemaVersion = 1

	QualificationCompactVersion   = "compact/17"
	QualificationTokenBudget      = 1200
	QualificationBootstrapSamples = 100000

	QualificationMinPasses                     = 56
	QualificationMinPairedGain                 = 9
	QualificationMinWeakStrataWithPositiveGain = 2
	QualificationBootstrapConfidence           = 0.95
	QualificationMaxSidecarRSSBytes            = int64(2 << 30)
	QualificationMaxArtifactBytes              = int64(1 << 30)
	QualificationMaxQueryP95Millis             = int64(1000)
	QualificationMinQuerySamples               = 100
	QualificationMaxReindexSeconds             = int64(600)
)

const (
	spentQualificationHoldoutID     = "cobra-compact17-fresh-unseen-v4"
	spentQualificationHoldoutSHA256 = "c4115b6331e46f1a8c0b0daad020e86e4826a51de8e06a68e3211dea0770a3a6"
)

var qualificationStratumCounts = map[string]int{
	StratumAmbiguous:        10,
	StratumArchitectureFlow: 11,
	StratumConfigDocs:       10,
	StratumExactIdentifier:  11,
	StratumExactPath:        11,
	StratumNLBehaviour:      11,
}

// QualificationArm is one immutable comparison arm in the embedded-model
// development qualification.
type QualificationArm string

const (
	ArmLexical    QualificationArm = "M0_lexical"
	ArmPotion512  QualificationArm = "M1_potion_512"
	ArmPotion8192 QualificationArm = "M2_potion_8192"
	ArmCodeRank   QualificationArm = "M3_coderank"
)

// QualificationPreregistration freezes every input that may affect the paired
// development comparison before any result is opened.
type QualificationPreregistration struct {
	SchemaVersion       int                         `json:"schema_version"`
	DatasetSHA256       string                      `json:"dataset_sha256"`
	SourceRepoSHA       string                      `json:"source_repo_sha"`
	CandidateSHA        string                      `json:"candidate_sha"`
	CandidateDiffSHA256 string                      `json:"candidate_diff_sha256"`
	ReaderPromptSHA256  string                      `json:"reader_prompt_sha256"`
	GraderPromptSHA256  string                      `json:"grader_prompt_sha256"`
	Arms                map[QualificationArm]ArmPin `json:"arms"`
	CompactVersion      string                      `json:"compact_version"`
	TokenBudget         int                         `json:"token_budget"`
	BootstrapSamples    int                         `json:"bootstrap_samples"`
	BootstrapSeed       uint64                      `json:"bootstrap_seed"`
	Thresholds          QualificationThresholds     `json:"thresholds"`
	ReferenceMachine    ReferenceMachine            `json:"reference_machine"`
}

// ArmPin binds one arm to its exact semantic identity. The lexical arm carries
// only its label; every semantic arm carries all four embedding pins.
type ArmPin struct {
	Label                string `json:"label"`
	EmbedderID           string `json:"embedder_id,omitempty"`
	FingerprintCanonical string `json:"fingerprint_canonical,omitempty"`
	ManifestSHA256       string `json:"manifest_sha256,omitempty"`
	AdmissionSHA256      string `json:"admission_sha256,omitempty"`
}

// QualificationThresholds is the complete immutable promotion and operating
// budget gate.
type QualificationThresholds struct {
	MinPasses                     int     `json:"min_passes"`
	MinPairedGain                 int     `json:"min_paired_gain"`
	MinWeakStrataWithPositiveGain int     `json:"min_weak_strata_with_positive_gain"`
	BootstrapConfidence           float64 `json:"bootstrap_confidence"`
	MaxSidecarRSSBytes            int64   `json:"max_sidecar_rss_bytes"`
	MaxArtifactBytes              int64   `json:"max_artifact_bytes"`
	MaxQueryP95Millis             int64   `json:"max_query_p95_millis"`
	MinQuerySamples               int     `json:"min_query_samples"`
	MaxReindexSeconds             int64   `json:"max_reindex_seconds"`
}

// ReferenceMachine freezes the CPU-only operating-budget environment.
type ReferenceMachine struct {
	OS             string `json:"os"`
	OSVersion      string `json:"os_version"`
	CPU            string `json:"cpu"`
	PhysicalCores  int    `json:"physical_cores"`
	RuntimeThreads int    `json:"runtime_threads"`
	BackgroundLoad string `json:"background_load"`
}

// ValidateQualificationDataset accepts exactly one fresh, holdout-shaped
// development population. A path is not an identity: spent evidence is
// rejected by its immutable dataset ID or byte digest even after a rename.
func ValidateQualificationDataset(loaded *Loaded) error {
	if loaded == nil || loaded.Dataset == nil {
		return fmt.Errorf("embedded-model qualification dataset is missing")
	}
	if !isLowerHexDigest(loaded.SHA256, 64) {
		return fmt.Errorf("embedded-model qualification dataset sha256 must be 64 lowercase hex characters")
	}
	if strings.TrimSpace(loaded.Dataset.ID) == spentQualificationHoldoutID || loaded.SHA256 == spentQualificationHoldoutSHA256 {
		return fmt.Errorf("embedded-model qualification refuses spent holdout dataset id %q or sha256 %q", loaded.Dataset.ID, loaded.SHA256)
	}
	if err := loaded.Dataset.Validate(); err != nil {
		return fmt.Errorf("embedded-model qualification dataset: %w", err)
	}
	if len(loaded.Dataset.Queries) != 64 {
		return fmt.Errorf("embedded-model qualification requires exactly 64 queries, got %d", len(loaded.Dataset.Queries))
	}

	counts := make(map[string]int, len(qualificationStratumCounts))
	families := make(map[string]string, len(loaded.Dataset.Queries))
	for _, query := range loaded.Dataset.Queries {
		if query.Split != SplitDev {
			return fmt.Errorf("embedded-model qualification query %q has split %q, want %q", query.ID, query.Split, SplitDev)
		}
		if _, known := qualificationStratumCounts[query.Stratum]; !known {
			return fmt.Errorf("embedded-model qualification query %q has forbidden stratum %q", query.ID, query.Stratum)
		}
		counts[query.Stratum]++
		family := strings.TrimSpace(query.FamilyID)
		if family == "" {
			return fmt.Errorf("embedded-model qualification query %q has blank family_id", query.ID)
		}
		if strings.TrimSpace(query.Provenance) == "" {
			return fmt.Errorf("embedded-model qualification query %q has blank provenance", query.ID)
		}
		if prior, exists := families[family]; exists {
			return fmt.Errorf("embedded-model qualification family %q is reused by queries %q and %q", family, prior, query.ID)
		}
		families[family] = query.ID
		hasGrade3 := false
		for _, judgement := range query.Judgements {
			if judgement.Grade == GradeMax {
				hasGrade3 = true
				break
			}
		}
		if !hasGrade3 {
			return fmt.Errorf("embedded-model qualification query %q has no grade-3 answer span", query.ID)
		}
	}
	for stratum, want := range qualificationStratumCounts {
		if got := counts[stratum]; got != want {
			return fmt.Errorf("embedded-model qualification stratum %s has %d queries, want %d", stratum, got, want)
		}
	}
	return nil
}

// ValidateQualificationPreregistration fails closed unless every binding,
// arm, promotion threshold and reference-machine field equals the approved
// experiment contract.
func ValidateQualificationPreregistration(pre QualificationPreregistration) error {
	if pre.SchemaVersion != QualificationSchemaVersion {
		return fmt.Errorf("embedded-model qualification schema_version is %d, want %d", pre.SchemaVersion, QualificationSchemaVersion)
	}
	for _, digest := range []struct {
		name  string
		value string
		size  int
	}{
		{name: "dataset_sha256", value: pre.DatasetSHA256, size: 64},
		{name: "source_repo_sha", value: pre.SourceRepoSHA, size: 40},
		{name: "candidate_sha", value: pre.CandidateSHA, size: 40},
		{name: "candidate_diff_sha256", value: pre.CandidateDiffSHA256, size: 64},
		{name: "reader_prompt_sha256", value: pre.ReaderPromptSHA256, size: 64},
		{name: "grader_prompt_sha256", value: pre.GraderPromptSHA256, size: 64},
	} {
		if !isLowerHexDigest(digest.value, digest.size) {
			return fmt.Errorf("embedded-model qualification %s must be %d lowercase hex characters", digest.name, digest.size)
		}
	}
	if pre.DatasetSHA256 == spentQualificationHoldoutSHA256 {
		return fmt.Errorf("embedded-model qualification preregistration names spent holdout sha256 %s", pre.DatasetSHA256)
	}
	if pre.CompactVersion != QualificationCompactVersion {
		return fmt.Errorf("embedded-model qualification compact_version is %q, want %q", pre.CompactVersion, QualificationCompactVersion)
	}
	if pre.TokenBudget != QualificationTokenBudget {
		return fmt.Errorf("embedded-model qualification token_budget is %d, want %d", pre.TokenBudget, QualificationTokenBudget)
	}
	if pre.BootstrapSamples != QualificationBootstrapSamples {
		return fmt.Errorf("embedded-model qualification bootstrap_samples is %d, want %d", pre.BootstrapSamples, QualificationBootstrapSamples)
	}
	if pre.BootstrapSeed == 0 {
		return fmt.Errorf("embedded-model qualification bootstrap_seed must be non-zero")
	}
	if err := validateQualificationArms(pre.Arms); err != nil {
		return err
	}
	if err := validateQualificationThresholds(pre.Thresholds); err != nil {
		return err
	}
	return validateQualificationReferenceMachine(pre.ReferenceMachine)
}

func validateQualificationArms(arms map[QualificationArm]ArmPin) error {
	required := []QualificationArm{ArmLexical, ArmPotion512, ArmPotion8192, ArmCodeRank}
	if len(arms) != len(required) {
		return fmt.Errorf("embedded-model qualification requires exactly four arms, got %d", len(arms))
	}
	for _, arm := range required {
		pin, exists := arms[arm]
		if !exists {
			return fmt.Errorf("embedded-model qualification arm %s is missing", arm)
		}
		if pin.Label != string(arm) {
			return fmt.Errorf("embedded-model qualification arm %s label is %q", arm, pin.Label)
		}
		if arm == ArmLexical {
			if pin.EmbedderID != "" || pin.FingerprintCanonical != "" || pin.ManifestSHA256 != "" || pin.AdmissionSHA256 != "" {
				return fmt.Errorf("embedded-model qualification lexical arm must not carry embedding pins")
			}
			continue
		}
		if strings.TrimSpace(pin.EmbedderID) == "" {
			return fmt.Errorf("embedded-model qualification arm %s embedder_id is required", arm)
		}
		if err := validateQualificationFingerprint(pin.FingerprintCanonical, pin.EmbedderID); err != nil {
			return fmt.Errorf("embedded-model qualification arm %s fingerprint: %w", arm, err)
		}
		if !isLowerHexDigest(pin.ManifestSHA256, 64) {
			return fmt.Errorf("embedded-model qualification arm %s manifest_sha256 must be 64 lowercase hex characters", arm)
		}
		if !isLowerHexDigest(pin.AdmissionSHA256, 64) {
			return fmt.Errorf("embedded-model qualification arm %s admission_sha256 must be 64 lowercase hex characters", arm)
		}
	}
	return nil
}

func validateQualificationFingerprint(canonical, embedderID string) error {
	fields, ok := decodeQualificationFingerprint(canonical)
	if !ok || len(fields) != 8 {
		return fmt.Errorf("must be a complete eight-field canonical fingerprint")
	}
	if fields[0] != embedderID {
		return fmt.Errorf("model id %q does not match embedder_id %q", fields[0], embedderID)
	}
	if strings.TrimSpace(fields[1]) == "" {
		return fmt.Errorf("revision is required")
	}
	if !isLowerHexDigest(fields[2], 64) || !isLowerHexDigest(fields[3], 64) {
		return fmt.Errorf("model and tokenizer digests must be complete lowercase sha256 values")
	}
	dim, err := strconv.Atoi(fields[4])
	if err != nil || dim <= 0 || strconv.Itoa(dim) != fields[4] {
		return fmt.Errorf("dimension %q must be a canonical positive integer", fields[4])
	}
	for i, name := range []string{"document schema", "admission/chunker profile", "graph generation"} {
		if strings.TrimSpace(fields[i+5]) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	return nil
}

func decodeQualificationFingerprint(canonical string) ([]string, bool) {
	if canonical == "" {
		return nil, false
	}
	var fields []string
	for pos := 0; pos < len(canonical); {
		colon := strings.IndexByte(canonical[pos:], ':')
		if colon <= 0 {
			return nil, false
		}
		colon += pos
		lengthText := canonical[pos:colon]
		for _, digit := range lengthText {
			if digit < '0' || digit > '9' {
				return nil, false
			}
		}
		length, err := strconv.Atoi(lengthText)
		if err != nil || length < 0 || strconv.Itoa(length) != lengthText {
			return nil, false
		}
		start := colon + 1
		end := start + length
		if end < start || end > len(canonical) {
			return nil, false
		}
		fields = append(fields, canonical[start:end])
		if end == len(canonical) {
			return fields, true
		}
		if canonical[end] != '\n' {
			return nil, false
		}
		pos = end + 1
	}
	return nil, false
}

func validateQualificationThresholds(got QualificationThresholds) error {
	want := QualificationThresholds{
		MinPasses:                     QualificationMinPasses,
		MinPairedGain:                 QualificationMinPairedGain,
		MinWeakStrataWithPositiveGain: QualificationMinWeakStrataWithPositiveGain,
		BootstrapConfidence:           QualificationBootstrapConfidence,
		MaxSidecarRSSBytes:            QualificationMaxSidecarRSSBytes,
		MaxArtifactBytes:              QualificationMaxArtifactBytes,
		MaxQueryP95Millis:             QualificationMaxQueryP95Millis,
		MinQuerySamples:               QualificationMinQuerySamples,
		MaxReindexSeconds:             QualificationMaxReindexSeconds,
	}
	if got != want {
		return fmt.Errorf("embedded-model qualification thresholds do not match the frozen promotion and operating-budget gates")
	}
	return nil
}

func validateQualificationReferenceMachine(machine ReferenceMachine) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "os", value: machine.OS},
		{name: "os_version", value: machine.OSVersion},
		{name: "cpu", value: machine.CPU},
		{name: "background_load", value: machine.BackgroundLoad},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("embedded-model qualification reference_machine.%s is required", field.name)
		}
	}
	if machine.PhysicalCores <= 0 {
		return fmt.Errorf("embedded-model qualification reference_machine.physical_cores must be positive")
	}
	if machine.RuntimeThreads <= 0 {
		return fmt.Errorf("embedded-model qualification reference_machine.runtime_threads must be positive")
	}
	return nil
}
