package main

import (
	"bytes"
	"context"
	"errors"
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
