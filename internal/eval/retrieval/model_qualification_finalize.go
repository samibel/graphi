package retrieval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type FinalizeQualificationOptions struct {
	PreregistrationPath string
	DatasetPath         string
	CaptureRootPath     string
	BlindEvidencePath   string
	BlindDecisionsPath  string
	OperatingPath       string
	OutputDir           string
}

func FinalizeQualification(options FinalizeQualificationOptions) (QualificationDecision, error) {
	for name, value := range map[string]string{
		"preregistration": options.PreregistrationPath, "dataset": options.DatasetPath,
		"capture root": options.CaptureRootPath, "blind evidence": options.BlindEvidencePath,
		"blind decisions": options.BlindDecisionsPath, "operating evidence": options.OperatingPath,
		"output directory": options.OutputDir,
	} {
		if strings.TrimSpace(value) == "" {
			return QualificationDecision{}, fmt.Errorf("embedded-model qualification finalizer: %s path is required", name)
		}
	}
	var pre QualificationPreregistration
	if err := decodeQualificationJSONFile(options.PreregistrationPath, &pre); err != nil {
		return QualificationDecision{}, err
	}
	if err := ValidateQualificationPreregistration(pre); err != nil {
		return QualificationDecision{}, fmt.Errorf("embedded-model qualification finalizer: preregistration: %w", err)
	}
	dataset, err := loadStrictQualificationDataset(options.DatasetPath)
	if err != nil {
		return QualificationDecision{}, err
	}
	if dataset.SHA256 != pre.DatasetSHA256 {
		return QualificationDecision{}, fmt.Errorf("embedded-model qualification finalizer: dataset digest differs from preregistration")
	}
	captures, err := LoadQualificationCaptureRoot(options.CaptureRootPath)
	if err != nil {
		return QualificationDecision{}, err
	}
	var blindEvidence []BlindEvidenceSet
	if err := decodeQualificationJSONFile(options.BlindEvidencePath, &blindEvidence); err != nil {
		return QualificationDecision{}, err
	}
	if len(blindEvidence) != 7 {
		return QualificationDecision{}, fmt.Errorf("embedded-model qualification finalizer: got %d blind evidence sets, want 7", len(blindEvidence))
	}
	var decisions []BlindDecision
	if err := decodeQualificationJSONFile(options.BlindDecisionsPath, &decisions); err != nil {
		return QualificationDecision{}, err
	}
	if len(decisions) != 448 {
		return QualificationDecision{}, fmt.Errorf("embedded-model qualification finalizer: got %d blind decisions, want 448", len(decisions))
	}
	operating, err := LoadOperatingEvidence(options.OperatingPath, pre, dataset)
	if err != nil {
		return QualificationDecision{}, err
	}

	input := QualificationInput{Preregistration: pre, Dataset: dataset, BlindEvidence: blindEvidence, Decisions: decisions, Operating: operating}
	for _, capture := range captures.Captures {
		input.BuildDigests = append(input.BuildDigests, capture.Digest)
		if capture.Build == 1 {
			input.Observations = append(input.Observations, capture.Observations...)
		}
		if capture.Arm == ArmCodeRank && capture.Build == 1 {
			if capture.OracleEvidence == nil {
				return QualificationDecision{}, fmt.Errorf("embedded-model qualification finalizer: M3 build 1 has no oracle evidence")
			}
			input.OracleEvidence = *capture.OracleEvidence
		}
	}
	decision, err := EvaluateQualification(input)
	if err != nil {
		return QualificationDecision{}, fmt.Errorf("embedded-model qualification finalizer: %w", err)
	}
	report := QualificationReport{
		SchemaVersion: QualificationReportSchemaVersion, Preregistration: pre,
		Dataset:      QualificationDatasetEvidence{Path: dataset.Path, SHA256: dataset.SHA256, RawBytes: append([]byte(nil), dataset.Raw...)},
		Observations: input.Observations, BuildDigests: input.BuildDigests, BlindEvidence: blindEvidence,
		BlindDecisions: decisions, OracleEvidence: input.OracleEvidence, Operating: operating, Decision: decision,
	}
	outputDir, err := resolveNewQualificationOutputDir(options.OutputDir)
	if err != nil {
		return QualificationDecision{}, err
	}
	if err := os.Mkdir(outputDir, 0o755); err != nil {
		return QualificationDecision{}, fmt.Errorf("embedded-model qualification finalizer: create output directory: %w", err)
	}
	if err := WriteQualificationReport(outputDir, report); err != nil {
		return QualificationDecision{}, err
	}
	return decision, nil
}

func resolveNewQualificationOutputDir(path string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("embedded-model qualification finalizer: absolute output path: %w", err)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", fmt.Errorf("embedded-model qualification finalizer: resolve output parent: %w", err)
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}

func decodeQualificationJSONFile(path string, into any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("embedded-model qualification: read %s: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return fmt.Errorf("embedded-model qualification: decode %s: %w", path, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("embedded-model qualification: %s has trailing JSON", path)
	}
	return nil
}

func loadStrictQualificationDataset(path string) (*Loaded, error) {
	loaded, err := decodeStrictQualificationDataset(path)
	if err != nil {
		return nil, err
	}
	if err := loaded.Dataset.Validate(); err != nil {
		return nil, fmt.Errorf("embedded-model qualification: dataset: %w", err)
	}
	return loaded, nil
}

// decodeStrictQualificationDataset is the byte-level half of the qualification
// loader: read, reject unknown fields, reject trailing JSON, seal the digest. It
// is split out from loadStrictQualificationDataset so the dataset pre-flight
// checker can read a candidate population through the exact same bytes-to-struct
// path the gate uses and still describe a population that does not yet pass
// Dataset.Validate. A parallel reader would let the checker bless a file the
// harness cannot even decode.
func decodeStrictQualificationDataset(path string) (*Loaded, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("embedded-model qualification: read dataset: %w", err)
	}
	var dataset Dataset
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&dataset); err != nil {
		return nil, fmt.Errorf("embedded-model qualification: decode dataset: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("embedded-model qualification: dataset has trailing JSON")
	}
	return &Loaded{Dataset: &dataset, Path: path, Raw: raw, SHA256: SHA256Hex(raw)}, nil
}

// resealQualificationDataset rebuilds Raw and SHA256 after the pre-flight
// checker narrows a population in memory. ValidateQualificationDataset refuses
// any Loaded whose digest does not match its bytes, so a narrowed population
// that kept the file's digest would be rejected for the wrong reason and hide
// the reasons the operator needs to see.
func resealQualificationDataset(loaded *Loaded) error {
	raw, err := json.Marshal(loaded.Dataset)
	if err != nil {
		return fmt.Errorf("embedded-model qualification: reseal filtered dataset: %w", err)
	}
	loaded.Raw = raw
	loaded.SHA256 = SHA256Hex(raw)
	return nil
}
