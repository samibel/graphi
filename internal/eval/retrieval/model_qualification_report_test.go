package retrieval

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteQualificationReportContainsCompleteReconstructableEvidence(t *testing.T) {
	report := completeQualificationReportFixture(t)
	dir := t.TempDir()
	if err := WriteQualificationReport(dir, report); err != nil {
		t.Fatal(err)
	}

	raw := mustReadQualificationReportFile(t, filepath.Join(dir, "qualification.json"))
	for _, required := range []string{
		`"candidate_sha"`, `"dataset_sha256"`, `"raw_bytes"`, `"M3_coderank"`,
		`"blind_evidence"`, `"blind_decisions"`, `"paired_bootstrap_95"`,
		`"stratum_deltas"`, `"stage_retention"`, `"reproducibility"`,
		`"operating_budget"`, `"run_validity"`, `"oracle_blind_ceilings"`,
		`"capture_provenance"`, `"promote"`,
	} {
		if !bytes.Contains(raw, []byte(required)) {
			t.Fatalf("qualification.json missing %s", required)
		}
	}
	if len(report.BlindEvidence) != 7 || len(report.BlindDecisions) != 7*64 {
		t.Fatalf("fixture evidence shape = %d sets/%d decisions, want 7/448", len(report.BlindEvidence), len(report.BlindDecisions))
	}
	if !bytes.HasSuffix(raw, []byte("\n")) || bytes.HasSuffix(raw, []byte("\n\n")) {
		t.Fatal("qualification.json must have exactly one trailing newline")
	}
	if bytes.Contains(raw, []byte("\t")) {
		t.Fatal("qualification.json must use spaces, not tabs")
	}
	var emitted QualificationReport
	if err := json.Unmarshal(raw, &emitted); err != nil {
		t.Fatal(err)
	}
	if got, want := emitted.Dataset.SHA256, report.Dataset.SHA256; got != want || !bytes.Equal(emitted.Dataset.RawBytes, report.Dataset.RawBytes) {
		t.Fatalf("emitted dataset identity differs: sha=%q want %q bytes_equal=%t", got, want, bytes.Equal(emitted.Dataset.RawBytes, report.Dataset.RawBytes))
	}
	if got, want := emitted.Derived.M3VersusM1, (QualificationPairedSummary{Wins: 12, Losses: 2, Ties: 50, Net: 10}); got != want {
		t.Fatalf("M3/M1 paired summary = %+v, want %+v", got, want)
	}
	if len(emitted.Derived.ArmTotals) != 4 || emitted.Derived.ArmTotals[1].Passes != 46 || emitted.Derived.ArmTotals[3].Passes != 56 {
		t.Fatalf("arm totals = %+v, want four arms with M1=46 and M3=56", emitted.Derived.ArmTotals)
	}
	if len(emitted.Derived.StratumDeltas) != 6 || len(emitted.Derived.BlindEvidenceManifests) != 7 || len(emitted.Derived.RunValidity) != 10 || len(emitted.Derived.OracleBlindCeilings) != 3 {
		t.Fatalf("derived evidence shape = strata %d manifests %d validity %d oracle %d", len(emitted.Derived.StratumDeltas), len(emitted.Derived.BlindEvidenceManifests), len(emitted.Derived.RunValidity), len(emitted.Derived.OracleBlindCeilings))
	}
	for _, manifest := range emitted.Derived.BlindEvidenceManifests {
		if manifest.Queries != 64 || manifest.FinalDecisions != 64 {
			t.Fatalf("blind manifest %+v is incomplete", manifest)
		}
	}

	markdown := string(mustReadQualificationReportFile(t, filepath.Join(dir, "qualification.md")))
	for _, required := range []string{
		"## Arm totals", "M3 wins over M1", "M3 losses to M1", "Paired bootstrap 95%",
		StratumAmbiguous, StratumArchitectureFlow, StratumConfigDocs, StratumExactIdentifier, StratumExactPath, StratumNLBehaviour,
		"## Stage rank and retention", "## Independent builds and provenance", "build 1", "build 2",
		"## Operating measurements", "## Run validity", "## Oracle blind ceilings", "## Promotion gates",
		"Branch: `promote_coderank_to_product_integration`",
	} {
		if !strings.Contains(markdown, required) {
			t.Fatalf("qualification.md missing %q", required)
		}
	}
	if strings.Contains(markdown, "RELEASE:") {
		t.Fatal("development report must never claim release")
	}
	if !strings.HasSuffix(markdown, "DEVELOPMENT PROMOTION: YES\n") {
		t.Fatalf("qualification.md has wrong terminal decision:\n%s", markdown[len(markdown)-minInt(120, len(markdown)):])
	}
}

func TestWriteQualificationReportReevaluatesSuppliedDecision(t *testing.T) {
	report := completeQualificationReportFixture(t)
	report.Decision.Promote = false
	dir := t.TempDir()

	err := WriteQualificationReport(dir, report)
	if err == nil || !strings.Contains(err.Error(), "supplied decision differs") {
		t.Fatalf("WriteQualificationReport error = %v, want supplied-decision mismatch", err)
	}
	assertNoQualificationReportPair(t, dir)
}

func TestWriteQualificationReportRejectsIncompleteOrDuplicateEvidenceBeforeWriting(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*QualificationReport)
	}{
		{name: "missing blind evidence", mutate: func(r *QualificationReport) { r.BlindEvidence = r.BlindEvidence[:6] }},
		{name: "duplicate blind decision", mutate: func(r *QualificationReport) { r.BlindDecisions[1] = r.BlindDecisions[0] }},
		{name: "missing build", mutate: func(r *QualificationReport) { r.BuildDigests = r.BuildDigests[:7] }},
		{name: "tampered dataset bytes", mutate: func(r *QualificationReport) { r.Dataset.RawBytes = append(r.Dataset.RawBytes, '\n') }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := completeQualificationReportFixture(t)
			tc.mutate(&report)
			dir := t.TempDir()
			if err := WriteQualificationReport(dir, report); err == nil {
				t.Fatal("WriteQualificationReport unexpectedly succeeded")
			}
			assertNoQualificationReportPair(t, dir)
		})
	}
}

func TestWriteQualificationReportIsDeterministicAndRefusesOverwrite(t *testing.T) {
	report := completeQualificationReportFixture(t)
	first, second := t.TempDir(), t.TempDir()
	if err := WriteQualificationReport(first, report); err != nil {
		t.Fatal(err)
	}
	if err := WriteQualificationReport(second, report); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"qualification.json", "qualification.md"} {
		firstRaw := mustReadQualificationReportFile(t, filepath.Join(first, name))
		secondRaw := mustReadQualificationReportFile(t, filepath.Join(second, name))
		if !bytes.Equal(firstRaw, secondRaw) {
			t.Fatalf("%s is not deterministic", name)
		}
	}

	jsonBefore := mustReadQualificationReportFile(t, filepath.Join(first, "qualification.json"))
	mdBefore := mustReadQualificationReportFile(t, filepath.Join(first, "qualification.md"))
	if err := WriteQualificationReport(first, report); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("overwrite error = %v, want already exists", err)
	}
	if got := mustReadQualificationReportFile(t, filepath.Join(first, "qualification.json")); !bytes.Equal(got, jsonBefore) {
		t.Fatal("refused overwrite changed qualification.json")
	}
	if got := mustReadQualificationReportFile(t, filepath.Join(first, "qualification.md")); !bytes.Equal(got, mdBefore) {
		t.Fatal("refused overwrite changed qualification.md")
	}
}

func TestWriteQualificationReportLeavesNoPartialPairWhenTargetIsOccupied(t *testing.T) {
	report := completeQualificationReportFixture(t)
	dir := t.TempDir()
	occupied := filepath.Join(dir, "qualification.md")
	if err := os.Mkdir(occupied, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteQualificationReport(dir, report); err == nil {
		t.Fatal("WriteQualificationReport unexpectedly succeeded")
	}
	if _, err := os.Stat(filepath.Join(dir, "qualification.json")); !os.IsNotExist(err) {
		t.Fatalf("qualification.json exists after pair failure: %v", err)
	}
}

func completeQualificationReportFixture(t *testing.T) QualificationReport {
	t.Helper()
	in := passingQualificationInput(t)
	decision, err := EvaluateQualification(in)
	if err != nil {
		t.Fatal(err)
	}
	return QualificationReport{
		SchemaVersion:   QualificationReportSchemaVersion,
		Preregistration: in.Preregistration,
		Dataset: QualificationDatasetEvidence{
			Path: in.Dataset.Path, SHA256: in.Dataset.SHA256, RawBytes: append([]byte(nil), in.Dataset.Raw...),
		},
		Observations:   append([]QualificationObservation(nil), in.Observations...),
		BuildDigests:   append([]QualificationBuildDigest(nil), in.BuildDigests...),
		BlindEvidence:  append([]BlindEvidenceSet(nil), in.BlindEvidence...),
		BlindDecisions: append([]BlindDecision(nil), in.Decisions...),
		OracleControls: append([]OracleControls(nil), in.OracleControls...),
		Operating:      in.Operating,
		Decision:       decision,
	}
}

func mustReadQualificationReportFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertNoQualificationReportPair(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"qualification.json", "qualification.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s exists after rejected report: %v", name, err)
		}
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
