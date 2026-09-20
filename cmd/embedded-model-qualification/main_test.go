package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/eval/retrieval"
)

func TestFinalizeCLIPrintsOnlyDevelopmentPromotionAndNoIsSuccess(t *testing.T) {
	original := finalizeQualification
	t.Cleanup(func() { finalizeQualification = original })
	for _, promote := range []bool{true, false} {
		finalizeQualification = func(retrieval.FinalizeQualificationOptions) (retrieval.QualificationDecision, error) {
			return retrieval.QualificationDecision{Promote: promote}, nil
		}
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), []string{"finalize", "--preregistration", "p", "--dataset", "d", "--captures", "c", "--blind-evidence", "b", "--blind-decisions", "j", "--operating", "o", "--out", "r"}, &stdout, &stderr)
		if code != 0 || stderr.Len() != 0 {
			t.Fatalf("promote=%t code=%d stderr=%q", promote, code, stderr.String())
		}
		want := "DEVELOPMENT PROMOTION: NO\n"
		if promote {
			want = "DEVELOPMENT PROMOTION: YES\n"
		}
		if stdout.String() != want || strings.Contains(stdout.String(), "RELEASE") {
			t.Fatalf("stdout=%q want=%q", stdout.String(), want)
		}
	}
}

func TestCLIRejectsInvalidOrFailedCommands(t *testing.T) {
	original := finalizeQualification
	t.Cleanup(func() { finalizeQualification = original })
	finalizeQualification = func(retrieval.FinalizeQualificationOptions) (retrieval.QualificationDecision, error) {
		return retrieval.QualificationDecision{}, errors.New("invalid evidence")
	}
	for _, args := range [][]string{{}, {"unknown"}, {"finalize"}, {"finalize", "--preregistration", "p", "--dataset", "d", "--captures", "c", "--blind-evidence", "b", "--blind-decisions", "j", "--operating", "o", "--out", "r"}} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), args, &stdout, &stderr); code == 0 || stdout.Len() != 0 {
			t.Fatalf("args=%v code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
}

// capture is the stage whose evidence finalize consumes, and its inputs are
// all pinned by the preregistration - so a missing flag has to be a usage
// refusal rather than a defaulted path, and a positional argument has to be a
// refusal rather than a silently ignored word. The success line names the
// capture root because that is the exact file `finalize --captures` takes.
func TestCaptureCLIMapsEveryFlagAndRefusesAnyIncompleteInvocation(t *testing.T) {
	original := captureQualification
	t.Cleanup(func() { captureQualification = original })

	var got retrieval.CaptureQualificationOptions
	captureQualification = func(_ context.Context, options retrieval.CaptureQualificationOptions) error {
		got = options
		return nil
	}
	args := []string{
		"capture",
		"--preregistration", "pre", "--dataset", "data", "--manifest", "manifest",
		"--source-repo", "source", "--candidate-root", "candidate",
		"--static-model-dir", "model", "--out", "out",
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), args, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	want := retrieval.CaptureQualificationOptions{
		RepoRoot: "source", DatasetPath: "data", PreregistrationPath: "pre", ManifestPath: "manifest",
		OutputPath: "out", StaticModelDir: "model", CandidateRoot: "candidate",
	}
	if got != want {
		t.Fatalf("options=%+v want %+v", got, want)
	}
	if stdout.String() != "CAPTURES: "+filepath.Join("out", "captures.json")+"\n" {
		t.Fatalf("stdout=%q", stdout.String())
	}

	// A refused capture must not print a capture root: the operator would
	// hand a path that was never published to finalize.
	captureQualification = func(context.Context, retrieval.CaptureQualificationOptions) error {
		return errors.New("output directory /absolute/run/captures is not empty")
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), args, &stdout, &stderr); code != 1 || stdout.Len() != 0 {
		t.Fatalf("refused capture code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "is not empty") {
		t.Fatalf("refusal was not reported: %q", stderr.String())
	}

	// Usage failures are exit 2 and never reach the driver.
	captureQualification = func(context.Context, retrieval.CaptureQualificationOptions) error {
		t.Fatal("usage failure reached the capture driver")
		return nil
	}
	for _, invalid := range [][]string{
		{"capture"},
		{"capture", "--preregistration", "pre", "--dataset", "data", "--manifest", "manifest", "--source-repo", "source", "--candidate-root", "candidate", "--static-model-dir", "model"},
		{"capture", "--preregistration", "pre", "--dataset", "data", "--manifest", "manifest", "--source-repo", "source", "--candidate-root", "candidate", "--out", "out"},
		append(append([]string(nil), args...), "extra"),
		{"capture", "--unknown", "x"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := run(context.Background(), invalid, &stdout, &stderr); code != 2 || stdout.Len() != 0 {
			t.Fatalf("args=%v code=%d stdout=%q stderr=%q", invalid, code, stdout.String(), stderr.String())
		}
	}
}

// The capture flags name real paths, and the refusals an operator hits first
// are path refusals. They are the exported entry point's own, so the CLI must
// pass them through unchanged rather than pre-judging them.
func TestCaptureCLIReportsPathRefusalsFromTheEntryPoint(t *testing.T) {
	original := captureQualification
	t.Cleanup(func() { captureQualification = original })
	captureQualification = retrieval.CaptureQualification

	root := t.TempDir()
	out := filepath.Join(root, "out")
	if err := os.Mkdir(out, 0o755); err != nil {
		t.Fatal(err)
	}
	capture := func(outputPath string) (int, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), []string{
			"capture",
			"--preregistration", filepath.Join(root, "preregistration.json"),
			"--dataset", filepath.Join(root, "dataset.json"),
			"--manifest", filepath.Join(root, "coderank.json"),
			"--source-repo", root, "--candidate-root", root,
			"--static-model-dir", filepath.Join(root, "model"),
			"--out", outputPath,
		}, &stdout, &stderr)
		if stdout.Len() != 0 {
			t.Fatalf("a failed capture printed to stdout: %q", stdout.String())
		}
		return code, stderr.String()
	}

	if code, message := capture(filepath.Join(out, "does-not-exist")); code != 1 || !strings.Contains(message, "read output directory") {
		t.Fatalf("unresolvable output directory code=%d stderr=%q", code, message)
	}
	if err := os.WriteFile(filepath.Join(out, "captures.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, message := capture(out); code != 1 || !strings.Contains(message, "is not empty") {
		t.Fatalf("non-empty output directory code=%d stderr=%q", code, message)
	}
}

func TestMeasureCLIMapsEveryFlagAndReportsOnlyEvidenceAddress(t *testing.T) {
	original := measureQualification
	t.Cleanup(func() { measureQualification = original })
	var got retrieval.MeasureQualificationOptions
	measureQualification = func(_ context.Context, options retrieval.MeasureQualificationOptions) (retrieval.OperatingEvidence, error) {
		got = options
		return retrieval.OperatingEvidence{SHA256: strings.Repeat("a", 64)}, nil
	}
	args := []string{"measure", "--preregistration", "pre", "--dataset", "data", "--manifest", "manifest", "--source-repo", "source", "--candidate-root", "candidate", "--work-dir", "work", "--out", "out", "--background-protocol", "protocol", "--background-metadata", "metadata"}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), args, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	want := retrieval.MeasureQualificationOptions{PreregistrationPath: "pre", DatasetPath: "data", ManifestPath: "manifest", RepoRoot: "source", CandidateRoot: "candidate", WorkDir: "work", OutputPath: "out", BackgroundProtocol: "protocol", BackgroundMetadata: "metadata"}
	if got != want || stdout.String() != "OPERATING EVIDENCE: "+strings.Repeat("a", 64)+"\n" {
		t.Fatalf("options=%+v stdout=%q", got, stdout.String())
	}
	measureQualification = func(context.Context, retrieval.MeasureQualificationOptions) (retrieval.OperatingEvidence, error) {
		return retrieval.OperatingEvidence{}, errors.New("measurement failed")
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), args, &stdout, &stderr); code == 0 || stdout.Len() != 0 {
		t.Fatalf("measure error code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

// The exit code is the whole contract of validate-dataset: an operator loops on
// it, so 0 has to mean "the gate itself would accept this population" and never
// "the checker ran". The report goes to stdout even on a refusal, because a
// refusal nobody can read is the problem this subcommand exists to remove.
func TestValidateDatasetCLIPrintsTheWholeReportAndExitsOnTheGateVerdict(t *testing.T) {
	original := checkQualification
	t.Cleanup(func() { checkQualification = original })

	var got retrieval.QualificationDatasetCheckOptions
	for _, tc := range []struct {
		name       string
		conformant bool
		wantCode   int
	}{
		{name: "conformant", conformant: true, wantCode: 0},
		{name: "refused", conformant: false, wantCode: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkQualification = func(options retrieval.QualificationDatasetCheckOptions) (retrieval.QualificationDatasetDiagnosis, error) {
				got = options
				return retrieval.QualificationDatasetDiagnosis{Path: options.DatasetPath, Conformant: tc.conformant}, nil
			}
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), []string{"validate-dataset", "--dataset", "candidate.json", "--dev-only"}, &stdout, &stderr)
			if code != tc.wantCode || stderr.Len() != 0 {
				t.Fatalf("code=%d want %d stderr=%q", code, tc.wantCode, stderr.String())
			}
			want := retrieval.QualificationDatasetCheckOptions{DatasetPath: "candidate.json", DevSplitOnly: true}
			if got != want {
				t.Fatalf("options=%+v want %+v", got, want)
			}
			if !strings.Contains(stdout.String(), "QUALIFICATION DATASET PRE-FLIGHT") || !strings.Contains(stdout.String(), "STRATUM COVERAGE") {
				t.Fatalf("stdout does not carry the report: %q", stdout.String())
			}
		})
	}

	checkQualification = func(retrieval.QualificationDatasetCheckOptions) (retrieval.QualificationDatasetDiagnosis, error) {
		return retrieval.QualificationDatasetDiagnosis{}, errors.New("unreadable dataset")
	}
	for _, args := range [][]string{{"validate-dataset"}, {"validate-dataset", "--dataset", "d", "extra"}, {"validate-dataset", "--dataset", "d"}} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), args, &stdout, &stderr); code != 2 || stdout.Len() != 0 {
			t.Fatalf("args=%v code=%d stdout=%q", args, code, stdout.String())
		}
	}
}

// stdout must stay pasteable and stderr must carry everything else, because the
// documented way to use this subcommand is to redirect stdout into a file. An
// unfreezable candidate is exit 1 rather than a silent success, since the two
// candidate fields in that fragment are wrong rather than merely absent.
func TestDigestsCLISeparatesTheFragmentFromTheNotesAndGatesOnFreezability(t *testing.T) {
	original := computeQualification
	t.Cleanup(func() { computeQualification = original })

	var got retrieval.QualificationPreregistrationDigestInputs
	for _, tc := range []struct {
		name     string
		clean    bool
		wantCode int
	}{
		{name: "freezable", clean: true, wantCode: 0},
		{name: "dirty candidate", clean: false, wantCode: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			computeQualification = func(_ context.Context, options retrieval.QualificationPreregistrationDigestInputs) (retrieval.QualificationPreregistrationDigests, error) {
				got = options
				diff := retrieval.EmptyCandidateDiffSHA256()
				if !tc.clean {
					diff = strings.Repeat("b", 64)
				}
				return retrieval.QualificationPreregistrationDigests{
					Fragment:               []byte("{\n  \"schema_version\": 1\n}\n"),
					CandidateSHA:           strings.Repeat("a", 40),
					CandidateDiffSHA256:    diff,
					CandidateWorktreeClean: tc.clean,
					Unresolved: []retrieval.QualificationUnresolvedField{
						{Field: "bootstrap_seed", Requires: "an operator decision", Recipe: "pick a non-zero uint64"},
					},
				}, nil
			}
			var stdout, stderr bytes.Buffer
			args := []string{"digests", "--dataset", "d.json", "--candidate-root", "root", "--manifest", "m.json", "--grading-rubric", "r.md", "--candidate-sha", "sha"}
			if code := run(context.Background(), args, &stdout, &stderr); code != tc.wantCode {
				t.Fatalf("code=%d want %d stderr=%q", code, tc.wantCode, stderr.String())
			}
			want := retrieval.QualificationPreregistrationDigestInputs{
				DatasetPath: "d.json", CandidateRoot: "root", FrozenCandidateSHA: "sha",
				CodeRankManifestPath: "m.json", GraderPromptPath: "r.md",
			}
			if got != want {
				t.Fatalf("inputs=%+v want %+v", got, want)
			}
			if !json.Valid(stdout.Bytes()) {
				t.Fatalf("stdout is not pasteable JSON: %q", stdout.String())
			}
			if !strings.Contains(stderr.String(), "cannot compute yet") {
				t.Fatalf("stderr does not carry the authoring notes: %q", stderr.String())
			}
			if (strings.Contains(stderr.String(), "NOT READY")) == tc.clean {
				t.Fatalf("clean=%t but stderr says %q", tc.clean, stderr.String())
			}
		})
	}

	computeQualification = func(context.Context, retrieval.QualificationPreregistrationDigestInputs) (retrieval.QualificationPreregistrationDigests, error) {
		return retrieval.QualificationPreregistrationDigests{}, errors.New("dataset is unreadable")
	}
	for _, args := range [][]string{{"digests"}, {"digests", "--dataset", "d"}, {"digests", "--dataset", "d", "--candidate-root", "r", "extra"}, {"digests", "--dataset", "d", "--candidate-root", "r"}} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), args, &stdout, &stderr); code != 2 || stdout.Len() != 0 {
			t.Fatalf("args=%v code=%d stdout=%q", args, code, stdout.String())
		}
	}
}
