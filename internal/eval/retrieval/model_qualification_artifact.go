package retrieval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
)

const QualificationCaptureSchemaVersion = 1

// QualificationCaptureArtifact is the lossless, content-addressed handoff for
// one arm/build capture. Oracle evidence exists only on M3 build 1; keeping it
// here avoids relying on CapturedCandidateBundle's intentionally unexported
// qualification-only OracleControls field.
type QualificationCaptureArtifact struct {
	SchemaVersion  int                          `json:"schema_version"`
	Build          int                          `json:"build"`
	Arm            QualificationArm             `json:"arm"`
	Provenance     CandidateCaptureProvenance   `json:"provenance"`
	Digest         QualificationBuildDigest     `json:"digest"`
	Observations   []QualificationObservation   `json:"observations"`
	Bundles        []CapturedCandidateBundle    `json:"bundles"`
	OracleEvidence *QualificationOracleEvidence `json:"oracle_evidence,omitempty"`
	SHA256         string                       `json:"sha256"`
}

// QualificationCaptureRoot commits to the complete set of eight captures.
// A later finalizer can therefore load one sealed root rather than discover
// anonymous files whose membership was never committed.
type QualificationCaptureRoot struct {
	SchemaVersion int                            `json:"schema_version"`
	Captures      []QualificationCaptureArtifact `json:"captures"`
	SHA256        string                         `json:"sha256"`
}

func sealQualificationCaptureArtifact(artifact QualificationCaptureArtifact) (QualificationCaptureArtifact, error) {
	address, err := ContentAddress(artifact, func(value *QualificationCaptureArtifact) { value.SHA256 = "" })
	if err != nil {
		return QualificationCaptureArtifact{}, err
	}
	artifact.SHA256 = address
	return artifact, nil
}

func sealQualificationCaptureRoot(root QualificationCaptureRoot) (QualificationCaptureRoot, error) {
	canonical := append([]QualificationCaptureArtifact(nil), root.Captures...)
	sort.Slice(canonical, func(i, j int) bool {
		if canonical[i].Build != canonical[j].Build {
			return canonical[i].Build < canonical[j].Build
		}
		return canonical[i].Arm < canonical[j].Arm
	})
	root.Captures = canonical
	address, err := ContentAddress(root, func(value *QualificationCaptureRoot) { value.SHA256 = "" })
	if err != nil {
		return QualificationCaptureRoot{}, err
	}
	root.SHA256 = address
	return root, nil
}

func validateQualificationCaptureArtifact(artifact QualificationCaptureArtifact) error {
	if artifact.SchemaVersion != QualificationCaptureSchemaVersion {
		return fmt.Errorf("embedded-model qualification capture artifact: schema version %d, want %d", artifact.SchemaVersion, QualificationCaptureSchemaVersion)
	}
	sealed, err := sealQualificationCaptureArtifact(artifact)
	if err != nil || !isLowerHexDigest(artifact.SHA256, 64) || sealed.SHA256 != artifact.SHA256 {
		return fmt.Errorf("embedded-model qualification capture artifact: content address differs")
	}
	if artifact.Build != artifact.Digest.Build || artifact.Arm != artifact.Digest.Arm ||
		artifact.Build != artifact.Digest.CaptureProvenance.Build || artifact.Arm != artifact.Digest.CaptureProvenance.Arm {
		return fmt.Errorf("embedded-model qualification capture artifact: arm/build labels differ from sealed digest")
	}
	if artifact.Build != 1 && artifact.Build != 2 {
		return fmt.Errorf("embedded-model qualification capture artifact: invalid build ordinal %d", artifact.Build)
	}
	if err := validateQualificationBuildDigestSeal(artifact.Digest); err != nil {
		return fmt.Errorf("embedded-model qualification capture artifact: %w", err)
	}
	recordSeal, err := sealQualificationCaptureProvenanceRecord(artifact.Digest.CaptureProvenance)
	if err != nil || recordSeal.SHA256 != artifact.Digest.CaptureProvenance.SHA256 {
		return fmt.Errorf("embedded-model qualification capture artifact: provenance record content address differs")
	}
	if !reflect.DeepEqual(artifact.Provenance, artifact.Digest.CaptureProvenance.Provenance) {
		return fmt.Errorf("embedded-model qualification capture artifact: provenance differs from sealed build provenance")
	}
	if len(artifact.Observations) != 64 || len(artifact.Bundles) != 64 {
		return fmt.Errorf("embedded-model qualification capture artifact: got %d observations and %d bundles, want 64 each", len(artifact.Observations), len(artifact.Bundles))
	}
	if qualificationObservationsSHA256(artifact.Observations) != artifact.Digest.ObservationsSHA256 {
		return fmt.Errorf("embedded-model qualification capture artifact: observation digest differs")
	}
	observations := make(map[string]QualificationObservation, 64)
	for _, observation := range artifact.Observations {
		if observation.Arm != artifact.Arm || observation.QueryID == "" || observations[observation.QueryID].QueryID != "" {
			return fmt.Errorf("embedded-model qualification capture artifact: invalid or duplicate observation")
		}
		observations[observation.QueryID] = observation
	}
	seenBundles := make(map[string]bool, 64)
	for _, bundle := range artifact.Bundles {
		observation, ok := observations[bundle.QueryID]
		if !ok || seenBundles[bundle.QueryID] || bundle.Qualification == nil || !reflect.DeepEqual(*bundle.Qualification, observation) {
			return fmt.Errorf("embedded-model qualification capture artifact: bundle/observation binding differs for query %q", bundle.QueryID)
		}
		seenBundles[bundle.QueryID] = true
	}
	if artifact.Arm == ArmCodeRank && artifact.Build == 1 {
		if artifact.OracleEvidence == nil {
			return fmt.Errorf("embedded-model qualification capture artifact: M3 build 1 has no oracle evidence")
		}
		if err := validateCaptureOracleEvidence(*artifact.OracleEvidence, artifact.Digest); err != nil {
			return err
		}
	} else if artifact.OracleEvidence != nil {
		return fmt.Errorf("embedded-model qualification capture artifact: oracle evidence belongs only to M3 build 1")
	}
	return nil
}

func validateCaptureOracleEvidence(evidence QualificationOracleEvidence, digest QualificationBuildDigest) error {
	sealed, err := sealQualificationOracleEvidence(evidence)
	if err != nil || !isLowerHexDigest(evidence.SHA256, 64) || sealed.SHA256 != evidence.SHA256 {
		return fmt.Errorf("embedded-model qualification capture artifact: oracle evidence content address differs")
	}
	if evidence.BuildRef.BaseArm != ArmCodeRank || evidence.BuildRef.Build != 1 || evidence.BuildRef.BuildSHA256 != digest.SHA256 ||
		evidence.BuildRef.CaptureProvenanceSHA256 != digest.CaptureProvenance.SHA256 || len(evidence.Controls) != 64 {
		return fmt.Errorf("embedded-model qualification capture artifact: oracle evidence differs from M3 build 1")
	}
	payloads, tokens := qualificationOracleDigests(evidence.Controls)
	if payloads != evidence.OraclePayloadsSHA256 || tokens != evidence.OracleTokenCountsSHA256 ||
		payloads != digest.OraclePayloadsSHA256 || tokens != digest.OracleTokenCountsSHA256 {
		return fmt.Errorf("embedded-model qualification capture artifact: oracle evidence digest differs")
	}
	seen := make(map[string]bool, 64)
	for _, controls := range evidence.Controls {
		id := controls.CurrentCandidatesOraclePacker.QueryID
		if id == "" || seen[id] {
			return fmt.Errorf("embedded-model qualification capture artifact: invalid or duplicate oracle query")
		}
		seen[id] = true
		for _, bundle := range oracleControlBundles(controls) {
			if bundle.QueryID != id || bundle.Payload.SHA256 != SHA256Hex(bundle.Payload.Bytes) || !isLowerHexDigest(bundle.CandidateSHA256, 64) {
				return fmt.Errorf("embedded-model qualification capture artifact: incomplete oracle control for query %s", id)
			}
		}
	}
	return nil
}

func validateQualificationCaptureRoot(root QualificationCaptureRoot) error {
	if root.SchemaVersion != QualificationCaptureSchemaVersion || len(root.Captures) != 8 {
		return fmt.Errorf("embedded-model qualification capture root: schema/capture cardinality differs")
	}
	sealed, err := sealQualificationCaptureRoot(root)
	if err != nil || !isLowerHexDigest(root.SHA256, 64) || sealed.SHA256 != root.SHA256 || !reflect.DeepEqual(root.Captures, sealed.Captures) {
		return fmt.Errorf("embedded-model qualification capture root: content address or canonical ordering differs")
	}
	seen := make(map[string]bool, 8)
	for _, artifact := range root.Captures {
		if err := validateQualificationCaptureArtifact(artifact); err != nil {
			return err
		}
		key := fmt.Sprintf("%s/%d", artifact.Arm, artifact.Build)
		if seen[key] {
			return fmt.Errorf("embedded-model qualification capture root: duplicate %s", key)
		}
		seen[key] = true
	}
	for _, arm := range []QualificationArm{ArmLexical, ArmPotion512, ArmPotion8192, ArmCodeRank} {
		for build := 1; build <= 2; build++ {
			if !seen[fmt.Sprintf("%s/%d", arm, build)] {
				return fmt.Errorf("embedded-model qualification capture root: missing %s build %d", arm, build)
			}
		}
	}
	return nil
}

func WriteQualificationCaptureRoot(path string, root QualificationCaptureRoot) error {
	if err := validateQualificationCaptureRoot(root); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("embedded-model qualification capture root: encode: %w", err)
	}
	raw = append(raw, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("embedded-model qualification capture root: create: %w", err)
	}
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	err = errorsJoin(err, file.Close())
	return err
}

func LoadQualificationCaptureRoot(path string) (QualificationCaptureRoot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return QualificationCaptureRoot{}, fmt.Errorf("embedded-model qualification capture root: read: %w", err)
	}
	var root QualificationCaptureRoot
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&root); err != nil {
		return QualificationCaptureRoot{}, fmt.Errorf("embedded-model qualification capture root: decode: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return QualificationCaptureRoot{}, fmt.Errorf("embedded-model qualification capture root: trailing JSON")
	}
	if err := validateQualificationCaptureRoot(root); err != nil {
		return QualificationCaptureRoot{}, err
	}
	return root, nil
}

func errorsJoin(first, second error) error {
	if first != nil {
		return first
	}
	return second
}
