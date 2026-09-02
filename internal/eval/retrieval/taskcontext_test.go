package retrieval

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/contract"
	staticembed "github.com/samibel/graphi/engine/embed/static"
)

func TestSelectTaskContextDevNLBehaviour_FailClosedPopulation(t *testing.T) {
	grade3 := []Judgement{{Path: "a.go", StartLine: 10, EndLine: 20, Anchor: "answer", Grade: 3, Reason: "answer", Annotator: "a", Reviewer: "r"}}
	ds := &Dataset{ID: "selection", Queries: []Query{
		{ID: "dev-nl", Stratum: StratumNLBehaviour, Split: SplitDev, Judgements: grade3},
		{ID: "holdout-nl", Stratum: StratumNLBehaviour, Split: SplitHoldout, Judgements: grade3},
		{ID: "dev-other", Stratum: StratumArchitectureFlow, Split: SplitDev, Judgements: grade3},
	}}
	got, err := SelectTaskContextDevNLBehaviour(ds)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "dev-nl" {
		t.Fatalf("selected %+v, want only dev-nl", got)
	}
	for _, q := range got {
		if q.Split != SplitDev || q.Stratum != StratumNLBehaviour {
			t.Fatalf("selection widened to %s/%s", q.Stratum, q.Split)
		}
	}
}

func TestScoreTaskContextBundle_UsesSpanMatchesIntervalAndAllGrade3Spans(t *testing.T) {
	q := Query{
		ID: "q", Stratum: StratumNLBehaviour, Split: SplitDev,
		Judgements: []Judgement{
			{Path: "answer.go", StartLine: 10, EndLine: 20, Grade: 3},
			{Path: "other.go", StartLine: 30, EndLine: 35, Grade: 3},
			{Path: "answer.go", StartLine: 50, EndLine: 60, Grade: 2},
		},
	}
	bundle := &contract.Result{
		Outcome: contract.OutcomeFound,
		Summary: "task_context/2: fixture (task_context/2; retrieval/2; 4/1200 snippet tokens; strategy semantic_first; degradation: ready)",
		Items:   []contract.Item{{RefID: "n1", Rank: 1, Reason: "test", EvidenceRefIDs: []string{"e1"}}},
		Evidence: []contract.Evidence{
			{RefID: "e1", Path: "answer.go", Line: 15, Role: "primary", Snippet: "one two three"},
			{RefID: "e2", Path: "other.go", Line: 33, Role: "relation"},
			{RefID: "e3", Path: "answer.go", Line: 55, Role: "relation"},
		},
		Confidence: contract.Confidence{Distribution: map[string]float64{"heuristic": 1}, Top: "heuristic", Method: "test"},
		Limits:     contract.Limits{CapApplied: 40, TotalAvailable: 2, Dropped: 1, Truncated: true},
	}
	score, err := ScoreTaskContextBundle(q, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if !score.Covered || len(score.Matches) != 2 {
		t.Fatalf("score = %+v, want two interval matches across both grade-3 spans", score)
	}
	if score.Matches[0].EvidenceLine == score.Matches[0].JudgementStart {
		t.Fatal("fixture accidentally tests exact start-line equality instead of interval containment")
	}
	if score.Grade3Judgements != 2 || score.EmittedSnippetWhitespaceTokens != 3 || score.EngineReportedSnippetTokens != 4 || score.EngineReportedTokenBudget != 1200 || score.EngineMinusEmittedSnippetTokens != 1 || score.SnippetCount != 1 {
		t.Fatalf("score metadata = %+v", score)
	}
	if score.ItemCount != 1 || score.EvidenceCitationCount != 3 || score.ItemCapApplied != 40 || score.ItemsAvailable != 2 || score.ItemsDropped != 1 || !score.Truncated {
		t.Fatalf("score cost metadata = %+v", score)
	}
}

func TestTaskContextAllPassed_CanRejectObservedFailure(t *testing.T) {
	checks := []TaskContextEligibilityCheck{
		{Name: "ready", Passed: true},
		{Name: "method", Passed: false},
	}
	if taskContextAllPassed(checks) {
		t.Fatal("eligibility accepted an observed failed condition")
	}
	checks[1].Passed = true
	if !taskContextAllPassed(checks) {
		t.Fatal("eligibility rejected all observed passing conditions")
	}
	if taskContextAllPassed(nil) {
		t.Fatal("empty eligibility must fail closed")
	}
}

func TestWriteTaskContextRunDir_PreservesIneligibleObservation(t *testing.T) {
	run := &TaskContextRun{
		Measurement: &TaskContextMeasurement{
			Eligibility:          []TaskContextEligibilityCheck{{Name: "ready", Passed: false, Detail: "observed stale"}},
			EligibleForThreshold: false,
			Queries:              []TaskContextQueryResult{{ID: "q"}},
		},
		DatasetSlice: &Dataset{ID: "dev-only"},
		Raw:          []TaskContextRawQuery{{Query: Query{ID: "q"}}},
	}
	dir := t.TempDir()
	if err := WriteTaskContextRunDir(dir, run); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "measurement.json"))
	if err != nil {
		t.Fatal(err)
	}
	var written TaskContextMeasurement
	if err := json.Unmarshal(raw, &written); err != nil {
		t.Fatal(err)
	}
	if written.EligibleForThreshold || taskContextAllPassed(written.Eligibility) {
		t.Fatal("writer turned an observed eligibility failure into threshold eligibility")
	}
}

func TestSW264AC9RunDoesNotPublishHomePaths(t *testing.T) {
	root := taskContextModuleRoot(t)
	runDir := filepath.Join(root, "docs/eval/retrieval/runs/2026-09-02-sw264-task-context-v2-static-local")
	forbidden := [][]byte{[]byte("/Users/"), []byte("/home/")}
	err := filepath.WalkDir(runDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(runDir, path)
		if err != nil {
			return err
		}
		for _, needle := range forbidden {
			if bytes.Contains(contents, needle) {
				t.Errorf("generated run file %s publishes forbidden home path prefix %q", filepath.ToSlash(rel), needle)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestScoreTaskContextBundle_RefusesNonDevOrWrongStratum(t *testing.T) {
	bundle := &contract.Result{
		Outcome:    contract.OutcomeFound,
		Confidence: contract.Confidence{Distribution: map[string]float64{"heuristic": 1}, Top: "heuristic", Method: "test"},
	}
	for _, q := range []Query{
		{ID: "holdout", Stratum: StratumNLBehaviour, Split: SplitHoldout},
		{ID: "other", Stratum: StratumArchitectureFlow, Split: SplitDev},
	} {
		if _, err := ScoreTaskContextBundle(q, bundle); err == nil {
			t.Fatalf("ScoreTaskContextBundle accepted %s/%s", q.Stratum, q.Split)
		}
	}
}

// TestSW264_AC9Measurement is deliberate-run only. The regular suite skips
// before touching the cobra checkout or model; the checked-in recomputation
// command opts in with explicit pinned local resources.
func TestSW264_AC9Measurement(t *testing.T) {
	if os.Getenv("SW264_AC9_MEASURE") != "1" {
		t.Skip("set SW264_AC9_MEASURE=1 and the pinned local resource paths to reproduce the checked-in run")
	}
	const (
		pinnedDatasetSHA = "be604ff7b17db5c35b0c63ddbb5d758633535e81e6771858ff860c724fb50d82"
		pinnedCandidate  = "9895f58618ec446ab4597f5f0c9f1f95a944ba35"
		runDirName       = "2026-09-02-sw264-task-context-v2-static-local"
	)
	cobraRoot := os.Getenv("SW264_AC9_COBRA_ROOT")
	if cobraRoot == "" {
		t.Fatal("SW264_AC9_COBRA_ROOT must name the pinned cobra checkout")
	}
	modelDir := os.Getenv("GRAPHI_STATIC_MODEL_DIR")
	if modelDir == "" {
		t.Fatal("GRAPHI_STATIC_MODEL_DIR must name the pinned static model artifact")
	}
	for name, path := range map[string]string{"cobra checkout": cobraRoot, "static model artifact": modelDir} {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("%s %s is not a directory: %v", name, path, err)
		}
	}

	root := taskContextModuleRoot(t)
	datasetPath := filepath.Join(root, "internal/eval/retrieval/testdata/datasets/cobra-v1.json")
	ds, err := LoadDataset(datasetPath)
	if err != nil {
		t.Fatal(err)
	}
	if ds.SHA256 != pinnedDatasetSHA {
		t.Fatalf("cobra-v1 dataset sha256 = %s, want pinned %s", ds.SHA256, pinnedDatasetSHA)
	}
	ds.Path = "internal/eval/retrieval/testdata/datasets/cobra-v1.json"
	selected, err := SelectTaskContextDevNLBehaviour(ds.Dataset)
	if err != nil {
		t.Fatal(err)
	}
	if selected[0].ID != "cb-11" {
		t.Fatalf("dev nl_behaviour population starts at %s, want cb-11", selected[0].ID)
	}
	for _, q := range selected {
		if q.Split != SplitDev || q.Stratum != StratumNLBehaviour {
			t.Fatalf("query %s widened population to %s/%s", q.ID, q.Stratum, q.Split)
		}
	}
	t.Logf("measuring %d dynamically selected dev nl_behaviour queries", len(selected))

	candidate := taskContextCandidateForTest(t, root)
	if candidate.BaseSHA != pinnedCandidate {
		t.Fatalf("candidate base sha = %s, want branch pin %s", candidate.BaseSHA, pinnedCandidate)
	}
	repoSHA, err := CheckoutHEAD(context.Background(), cobraRoot)
	if err != nil {
		t.Fatal(err)
	}
	if repoSHA != ds.Dataset.RepoSHA {
		t.Fatalf("cobra checkout sha = %s, dataset pins %s", repoSHA, ds.Dataset.RepoSHA)
	}

	run, err := RunTaskContextV2(context.Background(), TaskContextOptions{
		RepoRoot: cobraRoot, RepoName: "cobra", RepoSHA: repoSHA,
		Dataset: ds, DatasetSHA: pinnedDatasetSHA,
		EmbedderSelector: staticembed.PinnedSelector,
		Candidate:        candidate,
		Log:              os.Stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(root, "docs/eval/retrieval/runs", runDirName)
	if err := WriteTaskContextRunDir(runDir, run); err != nil {
		t.Fatal(err)
	}
	verifyTaskContextRunDir(t, runDir)
	t.Logf("AC-9 task_context/2 grade-3 span coverage: %d/%d (%.6f)",
		run.Measurement.Aggregate.CoveredQueries,
		run.Measurement.Aggregate.TotalQueries,
		run.Measurement.Aggregate.Coverage)
}

func taskContextModuleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod walking up from %s", file)
		}
		dir = parent
	}
}

func taskContextCandidateForTest(t *testing.T, root string) TaskContextCandidate {
	t.Helper()
	base := strings.TrimSpace(taskContextCommand(t, root, "git", "rev-parse", "HEAD"))
	status := taskContextCommand(t, root, "git", "status", "--porcelain", "--untracked-files=normal")
	dirty := strings.TrimSpace(status) != ""
	sha := base
	if dirty {
		sha += "+dirty"
	}
	relFiles := []string{
		"internal/eval/retrieval/taskcontext.go",
		"internal/eval/retrieval/taskcontext_test.go",
	}
	files := make([]TaskContextFileDigest, 0, len(relFiles))
	h := sha256.New()
	trackedDiff := taskContextCommand(t, root, "git", "diff", "--binary", "--no-ext-diff")
	_, _ = h.Write([]byte(trackedDiff))
	for _, rel := range relFiles {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, TaskContextFileDigest{File: rel, SHA256: SHA256Hex(raw)})
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(raw)
	}
	return TaskContextCandidate{
		SHA: sha, BaseSHA: base, Dirty: dirty,
		DiffSHA256: hex.EncodeToString(h.Sum(nil)), SourceFiles: files,
	}
}

func taskContextCommand(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func verifyTaskContextRunDir(t *testing.T, dir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "measurement.json"))
	if err != nil {
		t.Fatal(err)
	}
	var measurement TaskContextMeasurement
	if err := json.Unmarshal(raw, &measurement); err != nil {
		t.Fatal(err)
	}
	if !measurement.EligibleForThreshold {
		t.Fatal("written measurement is not eligible_for_threshold")
	}
	if measurement.EligibleForThreshold != taskContextAllPassed(measurement.Eligibility) {
		t.Fatal("written eligible_for_threshold does not equal all(eligibility.passed)")
	}
	covered := 0
	truncated := 0
	for _, q := range measurement.Queries {
		if q.Split != SplitDev || q.Stratum != StratumNLBehaviour {
			t.Fatalf("written query %s has population %s/%s", q.ID, q.Stratum, q.Split)
		}
		rawQueryBytes, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(q.RawFile)))
		if err != nil {
			t.Fatal(err)
		}
		if SHA256Hex(rawQueryBytes) != q.RawSHA256 {
			t.Fatalf("raw query %s digest mismatch", q.ID)
		}
		var rawQuery TaskContextRawQuery
		if err := json.Unmarshal(rawQueryBytes, &rawQuery); err != nil {
			t.Fatal(err)
		}
		recomputed, err := ScoreTaskContextBundle(rawQuery.Query, rawQuery.Bundle)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(recomputed, rawQuery.Score) {
			t.Fatalf("query %s score does not recompute\nwritten: %+v\ncomputed: %+v", q.ID, rawQuery.Score, recomputed)
		}
		if q.ItemCount != recomputed.ItemCount || q.EvidenceCitationCount != recomputed.EvidenceCitationCount || q.SnippetCount != recomputed.SnippetCount ||
			q.EmittedSnippetWhitespaceTokens != recomputed.EmittedSnippetWhitespaceTokens || q.EngineReportedSnippetTokens != recomputed.EngineReportedSnippetTokens ||
			q.EngineReportedTokenBudget != recomputed.EngineReportedTokenBudget || q.EngineMinusEmittedSnippetTokens != recomputed.EngineMinusEmittedSnippetTokens ||
			q.ItemCapApplied != recomputed.ItemCapApplied || q.ItemsAvailable != recomputed.ItemsAvailable || q.ItemsDropped != recomputed.ItemsDropped || q.Truncated != recomputed.Truncated {
			t.Fatalf("query %s aggregate cost fields disagree with raw score\naggregate: %+v\nraw: %+v", q.ID, q, recomputed)
		}
		if recomputed.Covered {
			covered++
		}
		if recomputed.Truncated {
			truncated++
		}
	}
	if covered != measurement.Aggregate.CoveredQueries || len(measurement.Queries) != measurement.Aggregate.TotalQueries {
		t.Fatalf("aggregate does not recompute: %d/%d vs %d/%d", covered, len(measurement.Queries), measurement.Aggregate.CoveredQueries, measurement.Aggregate.TotalQueries)
	}
	wantCoverage := float64(covered) / float64(len(measurement.Queries))
	if measurement.Aggregate.Coverage != wantCoverage {
		t.Fatalf("coverage does not recompute: %g vs %g", measurement.Aggregate.Coverage, wantCoverage)
	}
	wantResolution := 1 / float64(len(measurement.Queries))
	if measurement.Aggregate.CoverageResolution != wantResolution || measurement.Aggregate.CoverageResolutionFraction != "1/"+strconv.Itoa(len(measurement.Queries)) {
		t.Fatalf("coverage resolution = %s (%g), want 1/%d (%g)", measurement.Aggregate.CoverageResolutionFraction, measurement.Aggregate.CoverageResolution, len(measurement.Queries), wantResolution)
	}
	if measurement.Aggregate.TruncatedQueries != truncated || measurement.Aggregate.CoverageCostNote == "" {
		t.Fatalf("aggregate cost disclosure = %+v, recomputed truncated queries %d", measurement.Aggregate, truncated)
	}
	indexBytes, err := os.ReadFile(filepath.Join(dir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var index struct {
		Files []TaskContextFileDigest `json:"files"`
	}
	if err := json.Unmarshal(indexBytes, &index); err != nil {
		t.Fatal(err)
	}
	for _, ref := range index.Files {
		contents, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(ref.File)))
		if err != nil {
			t.Fatal(err)
		}
		if got := SHA256Hex(contents); !bytes.Equal([]byte(got), []byte(ref.SHA256)) {
			t.Fatalf("%s sha256 = %s, want %s", ref.File, got, ref.SHA256)
		}
	}
}
