package retrieval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFinalizeQualificationReconstructsTypedEvidenceAndPublishesYesOrNo(t *testing.T) {
	for _, promote := range []bool{true, false} {
		t.Run(map[bool]string{true: "yes", false: "no"}[promote], func(t *testing.T) {
			options, in := qualificationFinalizeFixture(t)
			if !promote {
				in.Operating.StartAttestation.ArtifactBytes = QualificationMaxArtifactBytes + 1
				in.Operating.EndAttestation.ArtifactBytes = QualificationMaxArtifactBytes + 1
				in.Operating = mustSealOperatingEvidence(t, in.Operating)
				writeQualificationJSONFile(t, options.OperatingPath, in.Operating)
			}
			decision, err := FinalizeQualification(options)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Promote != promote {
				t.Fatalf("promote=%t want %t", decision.Promote, promote)
			}
			markdown, err := os.ReadFile(filepath.Join(options.OutputDir, "qualification.md"))
			if err != nil {
				t.Fatal(err)
			}
			want := "DEVELOPMENT PROMOTION: NO"
			if promote {
				want = "DEVELOPMENT PROMOTION: YES"
			}
			if !strings.Contains(string(markdown), want) || strings.Contains(string(markdown), "RELEASE:") {
				t.Fatalf("unexpected decision wording: %s", markdown)
			}
		})
	}
}

func TestFinalizeQualificationRejectsCardinalityBeforePublishing(t *testing.T) {
	for _, tc := range []struct {
		name string
		file func(FinalizeQualificationOptions) string
	}{
		{"blind evidence", func(o FinalizeQualificationOptions) string { return o.BlindEvidencePath }},
		{"blind decisions", func(o FinalizeQualificationOptions) string { return o.BlindDecisionsPath }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options, _ := qualificationFinalizeFixture(t)
			path := tc.file(options)
			var values []json.RawMessage
			strictQualificationJSONFile(t, path, &values)
			values = values[:len(values)-1]
			writeQualificationJSONFile(t, path, values)
			if _, err := FinalizeQualification(options); err == nil {
				t.Fatal("accepted incomplete finalizer inputs")
			}
			if _, err := os.Stat(options.OutputDir); !os.IsNotExist(err) {
				t.Fatalf("invalid evidence created output: %v", err)
			}
		})
	}
}

func TestFinalizeQualificationRejectsDuplicateAndMisboundEvidenceBeforePublishing(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, FinalizeQualificationOptions)
	}{
		{"duplicate blind subject", func(t *testing.T, o FinalizeQualificationOptions) {
			var values []BlindEvidenceSet
			strictQualificationJSONFile(t, o.BlindEvidencePath, &values)
			values[len(values)-1] = values[0]
			writeQualificationJSONFile(t, o.BlindEvidencePath, values)
		}},
		{"duplicate decision query", func(t *testing.T, o FinalizeQualificationOptions) {
			var values []BlindDecision
			strictQualificationJSONFile(t, o.BlindDecisionsPath, &values)
			values[len(values)-1] = values[0]
			writeQualificationJSONFile(t, o.BlindDecisionsPath, values)
		}},
		{"wrong payload binding", func(t *testing.T, o FinalizeQualificationOptions) {
			mutateFinalizerDecision(t, o.BlindDecisionsPath, func(v *BlindDecision) { v.PayloadSHA256 = strings.Repeat("9", 64) })
		}},
		{"wrong prompt binding", func(t *testing.T, o FinalizeQualificationOptions) {
			mutateFinalizerDecision(t, o.BlindDecisionsPath, func(v *BlindDecision) { v.ReaderPromptSHA256 = strings.Repeat("9", 64) })
		}},
		{"wrong evidence binding", func(t *testing.T, o FinalizeQualificationOptions) {
			var values []BlindDecision
			strictQualificationJSONFile(t, o.BlindDecisionsPath, &values)
			values[0].EvidenceSHA256 = strings.Repeat("9", 64)
			values[0].SHA256 = ""
			sha, err := ContentAddress(values[0], func(v *BlindDecision) { v.SHA256 = "" })
			if err != nil {
				t.Fatal(err)
			}
			values[0].SHA256 = sha
			writeQualificationJSONFile(t, o.BlindDecisionsPath, values)
		}},
		{"oracle moved from M3 build1", func(t *testing.T, o FinalizeQualificationOptions) {
			root, err := LoadQualificationCaptureRoot(o.CaptureRootPath)
			if err != nil {
				t.Fatal(err)
			}
			var oracle *QualificationOracleEvidence
			for i := range root.Captures {
				if root.Captures[i].Arm == ArmCodeRank && root.Captures[i].Build == 1 {
					oracle = root.Captures[i].OracleEvidence
					root.Captures[i].OracleEvidence = nil
					root.Captures[i] = mustSealQualificationCaptureArtifact(t, root.Captures[i])
				}
			}
			for i := range root.Captures {
				if root.Captures[i].Arm == ArmCodeRank && root.Captures[i].Build == 2 {
					root.Captures[i].OracleEvidence = oracle
					root.Captures[i] = mustSealQualificationCaptureArtifact(t, root.Captures[i])
				}
			}
			root = mustSealQualificationCaptureRoot(t, root)
			writeQualificationJSONFile(t, o.CaptureRootPath, root)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			options, _ := qualificationFinalizeFixture(t)
			tc.mutate(t, options)
			if _, err := FinalizeQualification(options); err == nil {
				t.Fatal("accepted duplicate or misbound finalizer evidence")
			}
			if _, err := os.Stat(options.OutputDir); !os.IsNotExist(err) {
				t.Fatalf("invalid evidence created output: %v", err)
			}
		})
	}
}

func mutateFinalizerDecision(t *testing.T, path string, mutate func(*BlindDecision)) {
	t.Helper()
	var values []BlindDecision
	strictQualificationJSONFile(t, path, &values)
	mutate(&values[0])
	sealed, err := sealBlindDecision(values[0], values[0].EvidenceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	values[0] = sealed
	writeQualificationJSONFile(t, path, values)
}

func qualificationFinalizeFixture(t *testing.T) (FinalizeQualificationOptions, QualificationInput) {
	t.Helper()
	in := passingQualificationInput(t)
	dir := t.TempDir()
	prePath := filepath.Join(dir, "preregistration.json")
	datasetPath := filepath.Join(dir, "dataset.json")
	capturePath := filepath.Join(dir, "captures.json")
	blindEvidencePath := filepath.Join(dir, "blind-evidence.json")
	blindDecisionsPath := filepath.Join(dir, "blind-decisions.json")
	operatingPath := filepath.Join(dir, "operating.json")
	writeQualificationJSONFile(t, prePath, in.Preregistration)
	if err := os.WriteFile(datasetPath, in.Dataset.Raw, 0o644); err != nil {
		t.Fatal(err)
	}
	var captures []QualificationCaptureArtifact
	for _, digest := range in.BuildDigests {
		var observations []QualificationObservation
		var bundles []CapturedCandidateBundle
		for _, observation := range in.Observations {
			if observation.Arm == digest.Arm {
				copy := observation
				observations = append(observations, copy)
				bundles = append(bundles, CapturedCandidateBundle{QueryID: observation.QueryID, Qualification: &copy})
			}
		}
		artifact := QualificationCaptureArtifact{SchemaVersion: QualificationCaptureSchemaVersion, Build: digest.Build, Arm: digest.Arm,
			Provenance: digest.CaptureProvenance.Provenance, Digest: digest, Observations: observations, Bundles: bundles}
		if digest.Arm == ArmCodeRank && digest.Build == 1 {
			oracle := in.OracleEvidence
			artifact.OracleEvidence = &oracle
		}
		captures = append(captures, mustSealQualificationCaptureArtifact(t, artifact))
	}
	root := mustSealQualificationCaptureRoot(t, QualificationCaptureRoot{SchemaVersion: QualificationCaptureSchemaVersion, Captures: captures})
	if err := WriteQualificationCaptureRoot(capturePath, root); err != nil {
		t.Fatal(err)
	}
	writeQualificationJSONFile(t, blindEvidencePath, in.BlindEvidence)
	writeQualificationJSONFile(t, blindDecisionsPath, in.Decisions)
	if err := WriteOperatingEvidence(operatingPath, in.Operating, in.Preregistration, in.Dataset); err != nil {
		t.Fatal(err)
	}
	return FinalizeQualificationOptions{PreregistrationPath: prePath, DatasetPath: datasetPath, CaptureRootPath: capturePath,
		BlindEvidencePath: blindEvidencePath, BlindDecisionsPath: blindDecisionsPath, OperatingPath: operatingPath,
		OutputDir: filepath.Join(dir, "report")}, in
}

func writeQualificationJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func strictQualificationJSONFile(t *testing.T, path string, into any) {
	t.Helper()
	if err := decodeQualificationJSONFile(path, into); err != nil {
		t.Fatal(err)
	}
}
