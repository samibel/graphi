package retrieval

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/embed/coderank"
)

const QualificationOperatingEvidenceSchemaVersion = 2

// OperatingEvidence is the sealed raw authority for the qualification's
// operating gates. Summaries are always derived from these samples.
type OperatingEvidence struct {
	SchemaVersion         int                           `json:"schema_version"`
	PreregistrationSHA256 string                        `json:"preregistration_sha256"`
	DatasetSHA256         string                        `json:"dataset_sha256"`
	SourceRepoSHA         string                        `json:"source_repo_sha"`
	CandidateSHA          string                        `json:"candidate_sha"`
	CandidateDiffSHA256   string                        `json:"candidate_diff_sha256"`
	CandidateBindingStart CandidateBinding              `json:"candidate_binding_start"`
	CandidateBindingEnd   CandidateBinding              `json:"candidate_binding_end"`
	ManifestSHA256        string                        `json:"manifest_sha256"`
	FingerprintCanonical  string                        `json:"fingerprint_canonical"`
	StartAttestation      coderank.OperatingAttestation `json:"start_attestation"`
	EndAttestation        coderank.OperatingAttestation `json:"end_attestation"`
	Machine               ReferenceMachine              `json:"machine"`
	BackgroundLoad        OperatingBackgroundLoad       `json:"background_load"`
	SourceTreeSHA256      string                        `json:"source_tree_sha256"`
	CorpusSHA256          string                        `json:"corpus_sha256"`
	Reindex               OperatingReindexEvidence      `json:"reindex"`
	Warmups               []OperatingWarmup             `json:"warmups"`
	Samples               []OperatingQuerySample        `json:"samples"`
	SHA256                string                        `json:"sha256"`
}

type OperatingBackgroundLoad struct {
	Declaration string `json:"declaration"`
	Protocol    string `json:"protocol"`
	Metadata    string `json:"metadata"`
}

type OperatingReindexEvidence struct {
	FreshEmptyWorkDir    bool   `json:"fresh_empty_work_dir"`
	AdmittedDocuments    int    `json:"admitted_documents"`
	FingerprintCanonical string `json:"fingerprint_canonical"`
	State                string `json:"state"`
	Flushed              bool   `json:"flushed"`
	Closed               bool   `json:"closed"`
	DurableReady         bool   `json:"durable_ready"`
	ElapsedNS            int64  `json:"elapsed_ns"`
}

type OperatingWarmup struct {
	Ordinal         int    `json:"ordinal"`
	QueryID         string `json:"query_id"`
	QueryTextSHA256 string `json:"query_text_sha256"`
}

type OperatingQuerySample struct {
	Ordinal         int    `json:"ordinal"`
	QueryID         string `json:"query_id"`
	QueryTextSHA256 string `json:"query_text_sha256"`
	LatencyNS       int64  `json:"latency_ns"`
	VectorSHA256    string `json:"vector_sha256"`
	UnknownTokens   int    `json:"unknown_tokens"`
}

// operatingMeasurements is deliberately not accepted as input evidence.
// It is a transient summary derived after raw evidence validation.
type operatingMeasurements struct {
	ArtifactBytes       int64
	PeakSidecarRSSBytes int64
	RuntimeThreads      int
	QueryEmbedLatencies []time.Duration
	QueryEmbedP95       time.Duration
	FullReindex         time.Duration
}

func qualificationPreregistrationDigest(pre QualificationPreregistration) (string, error) {
	return ContentAddress(pre, func(*QualificationPreregistration) {})
}

func SealOperatingEvidence(evidence OperatingEvidence) (OperatingEvidence, error) {
	address, err := ContentAddress(evidence, func(value *OperatingEvidence) { value.SHA256 = "" })
	if err != nil {
		return OperatingEvidence{}, err
	}
	evidence.SHA256 = address
	return evidence, nil
}

func ValidateOperatingEvidence(evidence OperatingEvidence, pre QualificationPreregistration, dataset *Loaded) error {
	if evidence.SchemaVersion != QualificationOperatingEvidenceSchemaVersion {
		return fmt.Errorf("embedded-model operating evidence: schema version %d, want %d", evidence.SchemaVersion, QualificationOperatingEvidenceSchemaVersion)
	}
	sealed, err := SealOperatingEvidence(evidence)
	if err != nil || !isLowerHexDigest(evidence.SHA256, 64) || sealed.SHA256 != evidence.SHA256 {
		return fmt.Errorf("embedded-model operating evidence: content address differs")
	}
	if err := ValidateQualificationPreregistration(pre); err != nil {
		return fmt.Errorf("embedded-model operating evidence: preregistration: %w", err)
	}
	if err := ValidateQualificationDataset(dataset); err != nil {
		return fmt.Errorf("embedded-model operating evidence: dataset: %w", err)
	}
	preSHA, err := qualificationPreregistrationDigest(pre)
	if err != nil {
		return err
	}
	m3 := pre.Arms[ArmCodeRank]
	if evidence.PreregistrationSHA256 != preSHA || evidence.DatasetSHA256 != dataset.SHA256 || evidence.DatasetSHA256 != pre.DatasetSHA256 ||
		evidence.SourceRepoSHA != pre.SourceRepoSHA || evidence.SourceRepoSHA != dataset.Dataset.RepoSHA ||
		evidence.CandidateSHA != pre.CandidateSHA || evidence.CandidateDiffSHA256 != pre.CandidateDiffSHA256 ||
		evidence.ManifestSHA256 != m3.ManifestSHA256 {
		return fmt.Errorf("embedded-model operating evidence: frozen input binding differs")
	}
	// The fingerprint is the one frozen input that cannot be compared
	// byte-for-byte: its eighth field, graph_generation, is minted from
	// crypto/rand by every index build, so the preregistered value and the
	// measured value differ there by construction (see
	// model_qualification_fingerprint.go). Fields 0-6 — model, tokenizer,
	// dimension, schema, durable profile — are still compared exactly, and a
	// mismatch is named field by field rather than folded into the binding
	// message above, because "the manifest digest moved" and "the sidecar
	// serves a different tokenizer" are different operator problems.
	if err := qualificationFingerprintsAgree(evidence.FingerprintCanonical, m3.FingerprintCanonical); err != nil {
		return fmt.Errorf("embedded-model operating evidence: measured M3 fingerprint differs from the preregistered pin: %w", err)
	}
	if !reflect.DeepEqual(evidence.CandidateBindingStart, evidence.CandidateBindingEnd) {
		return fmt.Errorf("embedded-model operating evidence: candidate binding changed during measurement")
	}
	binding := evidence.CandidateBindingStart
	if binding.CandidateSHA != pre.CandidateSHA || binding.FrozenCandidateSHA != pre.CandidateSHA ||
		!binding.CandidateWorktreeClean || !binding.CandidateMatchesFrozen || binding.CandidateExcludedPath != QualificationCandidateExcludedPath ||
		binding.CandidateDiffSHA256 != pre.CandidateDiffSHA256 || binding.CheckoutSHA != pre.SourceRepoSHA || !binding.CheckoutWorktreeClean || len(binding.DifferingPaths) != 0 {
		return fmt.Errorf("embedded-model operating evidence: observed candidate binding differs from frozen inputs")
	}
	if !reflect.DeepEqual(evidence.Machine, pre.ReferenceMachine) {
		return fmt.Errorf("embedded-model operating evidence: observed reference machine differs from preregistration")
	}
	if evidence.Machine.OS != "linux" && evidence.Machine.OS != "darwin" {
		return fmt.Errorf("embedded-model operating evidence: unsupported reference machine OS %q", evidence.Machine.OS)
	}
	if evidence.BackgroundLoad.Declaration != pre.ReferenceMachine.BackgroundLoad || strings.TrimSpace(evidence.BackgroundLoad.Protocol) == "" || strings.TrimSpace(evidence.BackgroundLoad.Metadata) == "" {
		return fmt.Errorf("embedded-model operating evidence: background load declaration is incomplete or differs")
	}
	if !isLowerHexDigest(evidence.SourceTreeSHA256, 64) || !isLowerHexDigest(evidence.CorpusSHA256, 64) {
		return fmt.Errorf("embedded-model operating evidence: source and corpus digests are required")
	}
	if err := embed.ValidateRuntimeAttestation(evidence.StartAttestation.Runtime); err != nil {
		return fmt.Errorf("embedded-model operating evidence: start attestation: %w", err)
	}
	if err := embed.ValidateRuntimeAttestation(evidence.EndAttestation.Runtime); err != nil {
		return fmt.Errorf("embedded-model operating evidence: end attestation: %w", err)
	}
	if evidence.StartAttestation.Runtime != evidence.EndAttestation.Runtime || !strings.HasSuffix(m3.EmbedderID, ":"+evidence.StartAttestation.Runtime.IdentityDigest) {
		return fmt.Errorf("embedded-model operating evidence: sidecar identity or epoch changed")
	}
	if evidence.StartAttestation.PeakRSSBytes <= 0 || evidence.EndAttestation.PeakRSSBytes < evidence.StartAttestation.PeakRSSBytes ||
		evidence.StartAttestation.ArtifactBytes <= 0 || evidence.StartAttestation.ArtifactBytes != evidence.EndAttestation.ArtifactBytes ||
		evidence.StartAttestation.RuntimeThreads <= 0 || evidence.StartAttestation.RuntimeThreads != evidence.EndAttestation.RuntimeThreads ||
		evidence.EndAttestation.RuntimeThreads != pre.ReferenceMachine.RuntimeThreads {
		return fmt.Errorf("embedded-model operating evidence: invalid attested operating metrics")
	}
	// The reindex is checked against the evidence's OWN fingerprint, not
	// against the pin: the pin comparison above already covered fields 0-6,
	// and what remains to prove is that the reindex the run performed and the
	// embedder the run measured named the SAME graph generation. That is the
	// property the byte-for-byte pin comparison used to give us by accident,
	// and it is the only thing graph_generation can actually attest.
	if err := validateOperatingReindex(evidence.Reindex, evidence.FingerprintCanonical); err != nil {
		return err
	}
	if len(dataset.Dataset.Queries) != 64 {
		return fmt.Errorf("embedded-model operating evidence: dataset has %d queries, want 64", len(dataset.Dataset.Queries))
	}
	if len(evidence.Warmups) != 64 || len(evidence.Samples) != 128 {
		return fmt.Errorf("embedded-model operating evidence: got %d warmups and %d measured samples, want 64 and 128", len(evidence.Warmups), len(evidence.Samples))
	}
	for i, query := range dataset.Dataset.Queries {
		warmup := evidence.Warmups[i]
		if warmup.Ordinal != i || warmup.QueryID != query.ID || warmup.QueryTextSHA256 != SHA256Hex([]byte(query.Text)) {
			return fmt.Errorf("embedded-model operating evidence: warmup %d differs from frozen query order", i)
		}
	}
	for i, sample := range evidence.Samples {
		query := dataset.Dataset.Queries[i%64]
		if sample.Ordinal != i || sample.QueryID != query.ID || sample.QueryTextSHA256 != SHA256Hex([]byte(query.Text)) {
			return fmt.Errorf("embedded-model operating evidence: sample %d differs from frozen two-pass query order", i)
		}
		if sample.LatencyNS <= 0 || !isLowerHexDigest(sample.VectorSHA256, 64) || sample.UnknownTokens < 0 {
			return fmt.Errorf("embedded-model operating evidence: sample %d is incomplete", i)
		}
	}
	return nil
}

// validateOperatingReindex checks the reindex record against the measured M3
// fingerprint. expectedFingerprint is the fingerprint the SAME evidence file
// reports for the embedder under measurement, so here — unlike against the
// preregistered pin — graph_generation must match too: one run carries one
// generation, and a reindex naming a different one means the latency samples
// were taken against a graph the reindex did not produce.
func validateOperatingReindex(reindex OperatingReindexEvidence, expectedFingerprint string) error {
	if !reindex.FreshEmptyWorkDir || reindex.AdmittedDocuments != 768 ||
		reindex.State != embed.StateReady.String() || !reindex.Flushed || !reindex.Closed || !reindex.DurableReady || reindex.ElapsedNS <= 0 {
		return fmt.Errorf("embedded-model operating evidence: reindex is not a fresh, exact 768-document, durable ready M3 build")
	}
	if err := qualificationFingerprintsAgree(reindex.FingerprintCanonical, expectedFingerprint); err != nil {
		return fmt.Errorf("embedded-model operating evidence: reindex fingerprint differs from the measured M3 fingerprint: %w", err)
	}
	reindexGeneration, reindexOK := qualificationGraphGenerationOf(reindex.FingerprintCanonical)
	measuredGeneration, measuredOK := qualificationGraphGenerationOf(expectedFingerprint)
	if !reindexOK || !measuredOK || reindexGeneration != measuredGeneration {
		return fmt.Errorf("embedded-model operating evidence: reindex graph generation %q differs from the measured M3 graph generation %q; one run must carry exactly one graph generation",
			reindexGeneration, measuredGeneration)
	}
	return nil
}

func operatingMeasurementsFromEvidence(evidence OperatingEvidence, pre QualificationPreregistration, dataset *Loaded) (operatingMeasurements, error) {
	if err := ValidateOperatingEvidence(evidence, pre, dataset); err != nil {
		return operatingMeasurements{}, err
	}
	latencies := make([]time.Duration, len(evidence.Samples))
	for i, sample := range evidence.Samples {
		latencies[i] = time.Duration(sample.LatencyNS)
	}
	p95, ok := operatingP95(latencies, pre.Thresholds.MinQuerySamples)
	if !ok {
		return operatingMeasurements{}, fmt.Errorf("embedded-model operating evidence: cannot derive query p95")
	}
	return operatingMeasurements{
		ArtifactBytes: evidence.EndAttestation.ArtifactBytes, PeakSidecarRSSBytes: evidence.EndAttestation.PeakRSSBytes, RuntimeThreads: evidence.EndAttestation.RuntimeThreads,
		QueryEmbedLatencies: latencies, QueryEmbedP95: p95, FullReindex: time.Duration(evidence.Reindex.ElapsedNS),
	}, nil
}

func WriteOperatingEvidence(path string, evidence OperatingEvidence, pre QualificationPreregistration, dataset *Loaded) error {
	if err := ValidateOperatingEvidence(evidence, pre, dataset); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return fmt.Errorf("embedded-model operating evidence: encode: %w", err)
	}
	raw = append(raw, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("embedded-model operating evidence: create: %w", err)
	}
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	return err
}

func LoadOperatingEvidence(path string, pre QualificationPreregistration, dataset *Loaded) (OperatingEvidence, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return OperatingEvidence{}, fmt.Errorf("embedded-model operating evidence: read: %w", err)
	}
	var evidence OperatingEvidence
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&evidence); err != nil {
		return OperatingEvidence{}, fmt.Errorf("embedded-model operating evidence: decode: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return OperatingEvidence{}, fmt.Errorf("embedded-model operating evidence: trailing JSON")
	}
	if err := ValidateOperatingEvidence(evidence, pre, dataset); err != nil {
		return OperatingEvidence{}, err
	}
	return evidence, nil
}

func operatingLatencyBounds(samples []OperatingQuerySample) (time.Duration, time.Duration) {
	if len(samples) == 0 {
		return 0, 0
	}
	values := make([]time.Duration, len(samples))
	for i, sample := range samples {
		values[i] = time.Duration(sample.LatencyNS)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values[0], values[len(values)-1]
}
