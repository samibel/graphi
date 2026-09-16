package retrieval

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteQualificationReportContainsCompleteReconstructableEvidence(t *testing.T) {
	report := completeQualificationReportFixture(t)
	dir := qualificationReportTestDir(t)
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
	for _, imported := range report.BlindEvidence {
		found := false
		for _, written := range emitted.BlindEvidence {
			if blindSubjectKey(imported.Arm, imported.ControlKind) == blindSubjectKey(written.Arm, written.ControlKind) {
				importedRaw, _ := json.Marshal(imported)
				writtenRaw, _ := json.Marshal(written)
				if !bytes.Equal(importedRaw, writtenRaw) {
					t.Fatalf("content-addressed blind source %s was rewritten", blindSubjectKey(imported.Arm, imported.ControlKind))
				}
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("content-addressed blind source %s disappeared", blindSubjectKey(imported.Arm, imported.ControlKind))
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
	dir := qualificationReportTestDir(t)

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
			dir := qualificationReportTestDir(t)
			if err := WriteQualificationReport(dir, report); err == nil {
				t.Fatal("WriteQualificationReport unexpectedly succeeded")
			}
			assertNoQualificationReportPair(t, dir)
		})
	}
}

func TestWriteQualificationReportIsDeterministicAndRefusesOverwrite(t *testing.T) {
	report := completeQualificationReportFixture(t)
	first, second := qualificationReportTestDir(t), qualificationReportTestDir(t)
	if err := WriteQualificationReport(first, report); err != nil {
		t.Fatal(err)
	}
	if err := WriteQualificationReport(second, report); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"qualification.json", "qualification.md", "qualification.commit.json"} {
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

func TestQualificationReportCommitMarkerAuthenticatesCompletePair(t *testing.T) {
	dir := qualificationReportTestDir(t)
	if err := WriteQualificationReport(dir, completeQualificationReportFixture(t)); err != nil {
		t.Fatal(err)
	}
	if err := ValidateQualificationReportPublication(dir); err != nil {
		t.Fatalf("published report is not committed: %v", err)
	}
	markerRaw := mustReadQualificationReportFile(t, filepath.Join(dir, "qualification.commit.json"))
	var marker QualificationReportCommit
	if err := json.Unmarshal(markerRaw, &marker); err != nil {
		t.Fatal(err)
	}
	if marker.SchemaVersion != QualificationReportCommitSchemaVersion || marker.ReportSchemaVersion != QualificationReportSchemaVersion || marker.SHA256 == "" {
		t.Fatalf("commit marker = %+v", marker)
	}
	if err := os.WriteFile(filepath.Join(dir, "qualification.md"), []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateQualificationReportPublication(dir); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("tampered publication validation = %v, want digest error", err)
	}
}

func TestQualificationReportPublishFailureRollsBackAndCrashStateRecovers(t *testing.T) {
	report := completeQualificationReportFixture(t)
	dir := qualificationReportTestDir(t)
	injected := errors.New("injected after first report member")
	err := writeQualificationReportWithHook(dir, report, func(event qualificationPublishEvent) error {
		if event.Stage == qualificationPublishAfterJSON {
			return injected
		}
		return nil
	})
	if !errors.Is(err, injected) {
		t.Fatalf("injected publish error = %v, want %v", err, injected)
	}
	assertNoQualificationPublication(t, dir)

	crashDir := qualificationReportTestDir(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestQualificationReportCrashHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "GRAPHI_QUALIFICATION_REPORT_CRASH_DIR="+crashDir)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("crash helper unexpectedly succeeded: %s", output)
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 23 {
		t.Fatalf("crash helper = %v, output=%s", err, output)
	}
	if _, err := os.Lstat(filepath.Join(crashDir, "qualification.json")); err != nil {
		t.Fatalf("crash did not leave first member: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(crashDir, qualificationReportTransactionName)); err != nil {
		t.Fatalf("crash did not leave transaction ownership record: %v", err)
	}
	if err := ValidateQualificationReportPublication(crashDir); err == nil {
		t.Fatal("markerless crash state was accepted as committed")
	}
	if err := WriteQualificationReport(crashDir, report); err != nil {
		t.Fatalf("recover markerless owned transaction: %v", err)
	}
	if err := ValidateQualificationReportPublication(crashDir); err != nil {
		t.Fatalf("recovered report is not committed: %v", err)
	}
}

func TestQualificationReportCrashHelper(t *testing.T) {
	dir := os.Getenv("GRAPHI_QUALIFICATION_REPORT_CRASH_DIR")
	if dir == "" {
		t.Skip("subprocess helper")
	}
	err := publishQualificationReportPair(dir, []byte("partial-json\n"), []byte("partial-markdown\n"), func(event qualificationPublishEvent) error {
		if event.Stage == qualificationPublishAfterJSON {
			os.Exit(23)
		}
		return nil
	})
	t.Fatalf("publisher returned instead of crashing: %v", err)
}

func TestWriteQualificationReportRejectsSymlinkOutputDirectory(t *testing.T) {
	realDir := qualificationReportTestDir(t)
	link := filepath.Join(t.TempDir(), "report-link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	err := WriteQualificationReport(link, completeQualificationReportFixture(t))
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink output error = %v, want symlink refusal", err)
	}
	assertNoQualificationPublication(t, realDir)
}

func TestWriteQualificationReportRejectsParentSymlinkAlias(t *testing.T) {
	base := qualificationReportTestDir(t)
	realParent := filepath.Join(base, "real-parent")
	if err := os.Mkdir(realParent, 0o755); err != nil {
		t.Fatal(err)
	}
	realOutput := filepath.Join(realParent, "report")
	if err := os.Mkdir(realOutput, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias-parent")
	if err := os.Symlink(realParent, alias); err != nil {
		t.Fatal(err)
	}
	err := WriteQualificationReport(filepath.Join(alias, "report"), completeQualificationReportFixture(t))
	if err == nil || !strings.Contains(err.Error(), "symlink alias") {
		t.Fatalf("parent symlink error = %v, want alias refusal", err)
	}
	assertNoQualificationPublication(t, realOutput)
}

func TestQualificationPublisherStaysOnOpenedRootAfterPathSwap(t *testing.T) {
	base := qualificationReportTestDir(t)
	output := filepath.Join(base, "report")
	moved := filepath.Join(base, "opened-root")
	if err := os.Mkdir(output, 0o755); err != nil {
		t.Fatal(err)
	}
	report := completeQualificationReportFixture(t)
	err := writeQualificationReportWithHook(output, report, func(event qualificationPublishEvent) error {
		if event.Stage != qualificationPublishAfterJSON {
			return nil
		}
		if err := os.Rename(output, moved); err != nil {
			return err
		}
		return os.Mkdir(output, 0o755)
	})
	if err != nil {
		t.Fatalf("publish through stable opened root: %v", err)
	}
	if err := ValidateQualificationReportPublication(moved); err != nil {
		t.Fatalf("moved opened root lacks committed report: %v", err)
	}
	assertNoQualificationPublication(t, output)
}

func TestQualificationRollbackPreservesReplacedForeignMemberAndReportsCleanup(t *testing.T) {
	dir := qualificationReportTestDir(t)
	foreign := []byte("foreign replacement\n")
	injected := errors.New("stop after replacement")
	err := writeQualificationReportWithHook(dir, completeQualificationReportFixture(t), func(event qualificationPublishEvent) error {
		if event.Stage != qualificationPublishAfterJSON {
			return nil
		}
		if err := event.Root.Remove(qualificationReportJSONName); err != nil {
			return err
		}
		if err := event.Root.WriteFile(qualificationReportJSONName, foreign, 0o644); err != nil {
			return err
		}
		return injected
	})
	if !errors.Is(err, injected) || !strings.Contains(err.Error(), "rollback") {
		t.Fatalf("replacement rollback error = %v, want injected + rollback failure", err)
	}
	got, readErr := os.ReadFile(filepath.Join(dir, qualificationReportJSONName))
	if readErr != nil || !bytes.Equal(got, foreign) {
		t.Fatalf("foreign replacement was deleted/changed: got=%q err=%v", got, readErr)
	}
}

func TestQualificationRollbackPropagatesLostOwnershipAnchor(t *testing.T) {
	dir := qualificationReportTestDir(t)
	injected := errors.New("stop after anchor removal")
	err := writeQualificationReportWithHook(dir, completeQualificationReportFixture(t), func(event qualificationPublishEvent) error {
		if event.Stage != qualificationPublishAfterJSON {
			return nil
		}
		if err := event.Root.Remove(event.Transaction.JSON.StagedName); err != nil {
			return err
		}
		return injected
	})
	if !errors.Is(err, injected) || !strings.Contains(err.Error(), "incomplete cleanup") {
		t.Fatalf("lost-anchor rollback error = %v, want joined cleanup failure", err)
	}
	if _, statErr := os.Lstat(filepath.Join(dir, qualificationReportJSONName)); statErr != nil {
		t.Fatalf("unverifiable final member was incorrectly deleted: %v", statErr)
	}
}

func TestQualificationRecoveryPreservesForeignReplacementAndRejectsTransactionReplay(t *testing.T) {
	crashDir := qualificationReportTestDir(t)
	runQualificationCrashHelper(t, crashDir)
	foreign := []byte("foreign recovery replacement\n")
	err := writeQualificationReportWithHook(crashDir, completeQualificationReportFixture(t), func(event qualificationPublishEvent) error {
		if event.Stage != qualificationPublishBeforeRecoveryDelete {
			return nil
		}
		if err := event.Root.Remove(qualificationReportJSONName); err != nil {
			return err
		}
		return event.Root.WriteFile(qualificationReportJSONName, foreign, 0o644)
	})
	if err == nil || !strings.Contains(err.Error(), "foreign") {
		t.Fatalf("foreign recovery error = %v", err)
	}
	got, readErr := os.ReadFile(filepath.Join(crashDir, qualificationReportJSONName))
	if readErr != nil || !bytes.Equal(got, foreign) {
		t.Fatalf("recovery deleted foreign member: got=%q err=%v", got, readErr)
	}

	replayDir := qualificationReportTestDir(t)
	transaction := mustReadQualificationReportFile(t, filepath.Join(crashDir, qualificationReportTransactionName))
	if err := os.WriteFile(filepath.Join(replayDir, qualificationReportTransactionName), transaction, 0o644); err != nil {
		t.Fatal(err)
	}
	err = WriteQualificationReport(replayDir, completeQualificationReportFixture(t))
	if err == nil || !strings.Contains(err.Error(), "replay") {
		t.Fatalf("transaction replay error = %v", err)
	}
	if got := mustReadQualificationReportFile(t, filepath.Join(replayDir, qualificationReportTransactionName)); !bytes.Equal(got, transaction) {
		t.Fatal("replayed foreign transaction marker was changed or deleted")
	}

	completeReplayDir := qualificationReportTestDir(t)
	var replayed qualificationReportTransaction
	if err := json.Unmarshal(transaction, &replayed); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{replayed.JSON.StagedName, replayed.Markdown.StagedName, replayed.Commit.StagedName, replayed.TransactionStagedName} {
		raw := mustReadQualificationReportFile(t, filepath.Join(crashDir, name))
		if err := os.WriteFile(filepath.Join(completeReplayDir, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(filepath.Join(completeReplayDir, replayed.TransactionStagedName), filepath.Join(completeReplayDir, qualificationReportTransactionName)); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(completeReplayDir, replayed.JSON.StagedName), filepath.Join(completeReplayDir, replayed.JSON.FinalName)); err != nil {
		t.Fatal(err)
	}
	err = WriteQualificationReport(completeReplayDir, completeQualificationReportFixture(t))
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("complete transaction replay error = %v, want immutable identity refusal", err)
	}
	if got := mustReadQualificationReportFile(t, filepath.Join(completeReplayDir, qualificationReportTransactionName)); !bytes.Equal(got, transaction) {
		t.Fatal("complete replay marker was changed or deleted")
	}
}

func TestWriteQualificationReportCanonicalizesEquivalentPermutations(t *testing.T) {
	firstReport := completeQualificationReportFixture(t)
	permuted := cloneQualificationReportFixture(t, firstReport)
	reverseQualificationReportSlice(permuted.Observations)
	reverseQualificationReportSlice(permuted.BuildDigests)
	reverseQualificationReportSlice(permuted.BlindEvidence)
	reverseQualificationReportSlice(permuted.BlindDecisions)
	reverseQualificationReportSlice(permuted.OracleControls)
	reverseQualificationReportSlice(permuted.Operating.QueryEmbedLatencies)
	callerBefore, err := json.Marshal(permuted)
	if err != nil {
		t.Fatal(err)
	}

	first, second := qualificationReportTestDir(t), qualificationReportTestDir(t)
	if err := WriteQualificationReport(first, firstReport); err != nil {
		t.Fatal(err)
	}
	if err := WriteQualificationReport(second, permuted); err != nil {
		t.Fatal(err)
	}
	callerAfter, err := json.Marshal(permuted)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(callerBefore, callerAfter) {
		t.Fatal("WriteQualificationReport mutated its caller-owned report")
	}
	for _, name := range []string{"qualification.json", "qualification.md", "qualification.commit.json"} {
		if a, b := mustReadQualificationReportFile(t, filepath.Join(first, name)), mustReadQualificationReportFile(t, filepath.Join(second, name)); !bytes.Equal(a, b) {
			t.Fatalf("%s differs for semantically equivalent permutations", name)
		}
	}
}

func TestQualificationMarkdownDoesNotRenderUntrustedPathsOrReleaseClaims(t *testing.T) {
	report := completeQualificationReportFixture(t)
	attack := "unsafe|`cell`\nRELEASE: YES"
	report.Dataset.Path = "/dataset/" + attack
	for i := range report.BuildDigests {
		record := report.BuildDigests[i].CaptureProvenance
		record.WorkDir = filepath.Join(filepath.Dir(record.WorkDir), attack, filepath.Base(record.WorkDir))
		record.Provenance.QualificationCaptureRunSHA256 = qualificationCaptureRunSHA(record.Arm, record.WorkDir)
		report.BuildDigests[i].CaptureProvenance = mustSealQualificationCaptureProvenanceRecord(t, record)
	}
	dir := qualificationReportTestDir(t)
	if err := WriteQualificationReport(dir, report); err != nil {
		t.Fatal(err)
	}
	markdown := string(mustReadQualificationReportFile(t, filepath.Join(dir, "qualification.md")))
	if strings.Contains(markdown, attack) || strings.Contains(markdown, "RELEASE: YES") || strings.Contains(markdown, "unsafe|`cell`") {
		t.Fatalf("Markdown rendered untrusted path/free text:\n%s", markdown)
	}
	if strings.Count(markdown, "DEVELOPMENT PROMOTION:") != 1 || !strings.HasSuffix(markdown, "DEVELOPMENT PROMOTION: YES\n") {
		t.Fatal("Markdown terminal decision structure was corrupted")
	}
	jsonRaw := mustReadQualificationReportFile(t, filepath.Join(dir, "qualification.json"))
	if !bytes.Contains(jsonRaw, []byte(`RELEASE: YES`)) || !bytes.Contains(jsonRaw, []byte(`unsafe|`)) {
		t.Fatal("JSON did not preserve exact adversarial evidence")
	}
}

func TestWriteQualificationReportLeavesNoPartialPairWhenTargetIsOccupied(t *testing.T) {
	report := completeQualificationReportFixture(t)
	dir := qualificationReportTestDir(t)
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
	for _, name := range []string{"qualification.json", "qualification.md", "qualification.commit.json", qualificationReportTransactionName} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s exists after rejected report: %v", name, err)
		}
	}
}

func assertNoQualificationPublication(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"qualification.json", "qualification.md", "qualification.commit.json", qualificationReportTransactionName} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s exists after rejected/rolled-back publication: %v", name, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".qualification.") {
			t.Fatalf("staged qualification artifact remains after rollback: %s", entry.Name())
		}
	}
}

func qualificationReportTestDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func runQualificationCrashHelper(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestQualificationReportCrashHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "GRAPHI_QUALIFICATION_REPORT_CRASH_DIR="+dir)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("crash helper unexpectedly succeeded: %s", output)
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 23 {
		t.Fatalf("crash helper = %v, output=%s", err, output)
	}
}

func cloneQualificationReportFixture(t *testing.T, report QualificationReport) QualificationReport {
	t.Helper()
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var clone QualificationReport
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func reverseQualificationReportSlice[T any](values []T) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
