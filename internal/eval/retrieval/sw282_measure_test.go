package retrieval

// SW-282's two dispatches. Both are deliberate-run only: the regular suite
// skips before touching the cobra checkout or the pinned model, exactly as
// TestSW264_AC9Measurement does. The PR carries their committed output.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	staticembed "github.com/samibel/graphi/engine/embed/static"
)

// sw282DevSliceSource is the frozen release dataset the recalibration measures
// the development half of.
const sw282DevSliceSource = "internal/eval/retrieval/testdata/datasets/cobra-v2.json"

// TestSW282_WriteDevSlice materialises the development-only slice the
// recalibration and coverage runs are measured over.
//
// It exists because DeriveTargets REFUSES a report carrying a holdout row, and
// the only honest way to satisfy that is never to measure the holdout. A
// `-split` flag on cmd/retrieval-eval would have been the alternative and was
// rejected: a flag that can select a split can select the holdout.
func TestSW282_WriteDevSlice(t *testing.T) {
	out := os.Getenv("SW282_DEV_SLICE_OUT")
	if out == "" {
		t.Skip("set SW282_DEV_SLICE_OUT=<path> to write the development-only slice of cobra-v2")
	}
	root := taskContextModuleRoot(t)
	src, err := LoadDataset(filepath.Join(root, filepath.FromSlash(sw282DevSliceSource)))
	if err != nil {
		t.Fatal(err)
	}
	src.Path = sw282DevSliceSource
	slice, err := SelectDevSplit(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, slice.Raw, 0o644); err != nil {
		t.Fatal(err)
	}
	answerable, excluded, err := AnswerableQueries(slice.Dataset, SplitDev)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s: id=%s queries=%d answerable=%d excluded=%d sha256=%s",
		out, slice.Dataset.ID, len(slice.Dataset.Queries), len(answerable), len(excluded), slice.SHA256)
}

// TestSW282_CoverageMeasurement re-measures task_context/2 grade-3 span
// coverage over the SAME six dev nl_behaviour queries SW-264 measured, on the
// frozen release dataset's development slice rather than cobra-v1.
//
// The threshold it is checked against is NOT calibrated from this run (see
// BundleCoverageThresholdSource): the bar is zero misses. If this run misses,
// the miss is recorded.
func TestSW282_CoverageMeasurement(t *testing.T) {
	if os.Getenv("SW282_COVERAGE_MEASURE") != "1" {
		t.Skip("set SW282_COVERAGE_MEASURE=1, SW282_COBRA_ROOT, SW282_DEV_SLICE and GRAPHI_STATIC_MODEL_DIR to reproduce the checked-in run")
	}
	cobraRoot := os.Getenv("SW282_COBRA_ROOT")
	if cobraRoot == "" {
		t.Fatal("SW282_COBRA_ROOT must name the pinned cobra checkout")
	}
	if os.Getenv("GRAPHI_STATIC_MODEL_DIR") == "" {
		t.Fatal("GRAPHI_STATIC_MODEL_DIR must name the pinned static model artifact")
	}
	slicePath := os.Getenv("SW282_DEV_SLICE")
	if slicePath == "" {
		t.Fatal("SW282_DEV_SLICE must name the development-only slice written by TestSW282_WriteDevSlice")
	}
	root := taskContextModuleRoot(t)
	ds, err := LoadDataset(slicePath)
	if err != nil {
		t.Fatal(err)
	}
	ds.Path = "docs/eval/retrieval/runs/2026-09-06-sw282-recalibration-local/dataset.json"
	for _, q := range ds.Dataset.Queries {
		if q.Split != SplitDev {
			t.Fatalf("the measured slice carries a %s query (%s); this measurement is development-only", q.Split, q.ID)
		}
	}
	selected, err := SelectTaskContextDevNLBehaviour(ds.Dataset)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("measuring %d dev nl_behaviour queries from %s", len(selected), ds.Dataset.ID)

	repoSHA, err := CheckoutHEAD(context.Background(), cobraRoot)
	if err != nil {
		t.Fatal(err)
	}
	if repoSHA != ds.Dataset.RepoSHA {
		t.Fatalf("cobra checkout sha = %s, dataset pins %s", repoSHA, ds.Dataset.RepoSHA)
	}
	run, err := RunTaskContextV2(context.Background(), TaskContextOptions{
		RepoRoot: cobraRoot, RepoName: "cobra", RepoSHA: repoSHA,
		Dataset: ds, DatasetSHA: ds.SHA256,
		EmbedderSelector: staticembed.PinnedSelector,
		Candidate:        taskContextCandidateForTest(t, root),
		Story:            "SW-282",
		AC:               "AC-2",
		RecomputeNote: "Set GRAPHI_STATIC_MODEL_DIR to the pinned static model directory, SW282_COBRA_ROOT to the pinned cobra checkout, " +
			"and SW282_DEV_SLICE to the development-only slice written by TestSW282_WriteDevSlice. The command fails before running if any input is missing.",
		RecomputeCommand: `: "${GRAPHI_STATIC_MODEL_DIR:?set to your potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b model dir}"
: "${SW282_COBRA_ROOT:?set to the cobra checkout at a0a6ae020bb3899ff0276067863e50523f897370}"
SW282_DEV_SLICE_OUT=/tmp/cobra-v2-dev.json CGO_ENABLED=0 \
go test ./internal/eval/retrieval -run '^TestSW282_WriteDevSlice$' -count=1 -v
SW282_COVERAGE_MEASURE=1 SW282_DEV_SLICE=/tmp/cobra-v2-dev.json CGO_ENABLED=0 \
go test ./internal/eval/retrieval -run '^TestSW282_CoverageMeasurement$' -count=1 -v`,
		Log: os.Stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(root, "docs/eval/retrieval/runs/2026-09-06-sw282-coverage-local")
	if err := WriteTaskContextRunDir(runDir, run); err != nil {
		t.Fatal(err)
	}
	verifyTaskContextRunDir(t, runDir)
	t.Logf("SW-282 task_context/2 grade-3 span coverage on %s: %d/%d (%.6f)",
		ds.Dataset.ID,
		run.Measurement.Aggregate.CoveredQueries,
		run.Measurement.Aggregate.TotalQueries,
		run.Measurement.Aggregate.Coverage)
}
