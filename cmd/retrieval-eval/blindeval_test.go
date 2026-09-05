package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/samibel/graphi/internal/eval/retrieval"
	evaltokenizer "github.com/samibel/graphi/internal/eval/tokenizer"
)

// AC-10: the command's flag set is ENUMERATED, so a later escape hatch is
// caught by construction rather than by remembering to look for it.
//
// The list is read out of the real command's own usage output — the flag set
// run() builds — so a new flag cannot be added without this test seeing it.
func TestRetrievalEval_FlagSetIsEnumeratedAndCarriesNoOverride(t *testing.T) {
	want := []string{
		"aggregate",
		"baseline",
		"blind-eval",
		"blind-eval-dir",
		"budget-large",
		"budget-medium",
		"budget-small",
		"budgets-out",
		"check-claim",
		"checkout",
		"dataset",
		"date",
		"derive",
		"embedder",
		"export-raw",
		"field-parity",
		"manifest",
		"out",
		"repeats",
		"repo",
		"runner-class",
		"setup-tokenizer",
		"targets-out",
		"targets-report",
		"tokenizer-dir",
		"tokenizer-local",
	}
	got := commandFlagNames(t)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("the retrieval-eval flag set changed.\n got: %v\nwant: %v\n\nEvery flag must be reviewed against SW-280 AC-10 before it is added: no flag may lower k, waive a query, exclude a query from N, retry a graded response or force a pass.", got, want)
	}

	// A second, independent guard: no flag name may read as an override, even
	// if somebody adds it to the list above without thinking.
	forbidden := regexp.MustCompile(`(?i)(^|-)(k|threshold|floor|waive|waiver|exclude|skip|force|override|exception|retry|allow|bypass|relax|min-pass|pass-count)($|-)`)
	for _, name := range got {
		if forbidden.MatchString(name) {
			t.Errorf("flag -%s reads as an override of the qrel-blind smoke evaluation's threshold or population", name)
		}
	}
}

// commandFlagNames reads the real command's flag set out of the usage text
// flag.ContinueOnError prints when parsing fails.
func commandFlagNames(t *testing.T) []string {
	t.Helper()
	var stderr bytes.Buffer
	if code := run([]string{"-this-flag-does-not-exist"}, io.Discard, &stderr); code != exitUsage {
		t.Fatalf("an unknown flag returned %d, want %d", code, exitUsage)
	}
	re := regexp.MustCompile(`(?m)^\s+-([A-Za-z0-9][A-Za-z0-9-]*)`)
	seen := map[string]bool{}
	var names []string
	for _, match := range re.FindAllStringSubmatch(stderr.String(), -1) {
		if seen[match[1]] {
			continue
		}
		seen[match[1]] = true
		names = append(names, match[1])
	}
	if len(names) == 0 {
		t.Fatalf("no flags parsed out of the usage output:\n%s", stderr.String())
	}
	sort.Strings(names)
	return names
}

// AC-10: a pass count below k records RELEASE: NO and exits non-zero.
func TestRetrievalEval_BlindEvalDecideBelowKIsReleaseNoAndNonZero(t *testing.T) {
	for _, tc := range []struct {
		name     string
		passes   int
		wantExit int
		wantSaid string
	}{
		{"below k", 12, exitError, "RELEASE: NO"},
		{"at k", 13, exitOK, "RELEASE: YES"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := buildBlindEvalRunDir(t, 13, tc.passes)
			var stdout, stderr bytes.Buffer
			root, err := repositoryRoot()
			if err != nil {
				t.Fatal(err)
			}
			code := runBlindEval(blindEvalOptions{phase: blindEvalDecide, dir: dir, root: root}, &stdout, &stderr)
			if code != tc.wantExit {
				t.Fatalf("exit=%d, want %d\nstdout: %s\nstderr: %s", code, tc.wantExit, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.wantSaid) {
				t.Errorf("stdout %q does not say %q", stdout.String(), tc.wantSaid)
			}
			// The written report must satisfy the naming discipline, because
			// the renderer refuses to produce one that does not.
			report, err := os.ReadFile(filepath.Join(dir, retrieval.BlindEvalReadmeFile))
			if err != nil {
				t.Fatal(err)
			}
			if err := retrieval.CheckQrelBlindSmokeReport(string(report)); err != nil {
				t.Errorf("the written report fails the naming discipline: %v", err)
			}
			if !strings.Contains(string(report), fmt.Sprintf("| observed pass count (as reviewed) | %d |", tc.passes)) {
				t.Errorf("the report does not publish the integer pass count %d", tc.passes)
			}
		})
	}
}

// Refusal: the capture phase refuses to start without a precondition record.
func TestRetrievalEval_BlindEvalRefusesToStartWithoutAPreconditionRecord(t *testing.T) {
	dir := t.TempDir()
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runBlindEval(blindEvalOptions{
		phase: blindEvalCapture, dir: dir, root: root,
		repoName: "cobra", checkout: dir, embedder: "static:x@y",
	}, &stdout, &stderr)
	if code == exitOK {
		t.Fatal("the capture phase started without a precondition record")
	}
	if !strings.Contains(stderr.String(), "refuses to start") {
		t.Errorf("stderr %q does not say the evaluation refused to start", stderr.String())
	}
}

// Refusal: an unknown phase is a usage error, not a silent default.
func TestRetrievalEval_BlindEvalRejectsAnUnknownPhase(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runBlindEval(blindEvalOptions{phase: "publish"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("an unknown phase returned %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "freeze, capture, seal, decide") {
		t.Errorf("stderr %q does not enumerate the accepted phases", stderr.String())
	}
}

// buildBlindEvalRunDir writes a complete, correctly ordered run directory with
// n queries of which passes pass. It uses real repository files as the frozen
// inputs so the end-of-run comparison genuinely reads them.
//
// The directory is created INSIDE the repository, and the frozen grading rubric
// is a file inside it, because that is the shape every phase now requires: seal
// and decide refuse a run directory that is not the one the precondition record
// was frozen into, and the candidate binding's excluded path must be that same
// directory. A fixture that lived in a temp directory outside the repository
// would only prove those checks do not run.
func buildBlindEvalRunDir(t *testing.T, n, passes int) string {
	t.Helper()
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	dir, relRunDir := blindEvalFixtureRunDir(t, root)
	read := retrieval.RepoFileSHA256Reader(root)
	rubricPath := relRunDir + "/grading-rubric.md"
	if err := os.WriteFile(filepath.Join(dir, "grading-rubric.md"), []byte("# fixture grading rubric\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inputs := []struct{ role, path string }{
		{"budgets", "docs/eval/retrieval-budgets.json"},
		{"targets", "docs/eval/retrieval-targets.json"},
		{retrieval.PreconditionInputGradingRubric, rubricPath},
		{"methodology", "docs/eval/retrieval/methodology.md"},
	}
	precondition := retrieval.PreconditionRecord{
		ContractVersion:            retrieval.QrelBlindSmokeContractVersion,
		Evaluation:                 retrieval.QrelBlindSmokeEvaluationName,
		FreezeCommit:               "abcdefabcdefabcdefabcdefabcdefabcdefabcd",
		FreezeTimestamp:            "2026-09-05T08:00:00Z",
		DatasetPath:                "internal/eval/retrieval/testdata/datasets/cobra-v2.json",
		CandidateSHA:               "0123456789abcdef0123456789abcdef01234567",
		CandidateMethod:            retrieval.SavingsCandidateMethod,
		CandidateTokenBudget:       retrieval.SavingsCandidateBudget,
		ComparatorVersion:          retrieval.BlindEvalComparatorVersion,
		TokenizerID:                evaltokenizer.TokenizerID,
		TokenizerVocabularySHA256:  evaltokenizer.PinnedVocabularySHA256,
		MeasurementContractVersion: retrieval.MeasurementContractVersion,
		ClaimWordingSHA256:         retrieval.SHA256Hex([]byte(retrieval.FrozenClaimWording())),
	}
	datasetSHA, err := read(precondition.DatasetPath)
	if err != nil {
		t.Fatal(err)
	}
	precondition.DatasetSHA256 = datasetSHA
	var rubricSHA string
	for _, input := range inputs {
		sha, err := read(input.path)
		if err != nil {
			t.Fatal(err)
		}
		if input.role == "grading_rubric" {
			rubricSHA = sha
		}
		precondition.Inputs = append(precondition.Inputs, retrieval.FrozenInput{Role: input.role, Path: input.path, SHA256: sha})
	}
	precondition, err = retrieval.SealPreconditionRecord(precondition)
	if err != nil {
		t.Fatal(err)
	}
	if err := retrieval.WriteBlindEvalJSON(filepath.Join(dir, retrieval.BlindEvalPreconditionFile), precondition); err != nil {
		t.Fatal(err)
	}

	derivation, err := retrieval.DerivePassCount(n, precondition.DatasetSHA256, "cmd fixture")
	if err != nil {
		t.Fatal(err)
	}
	raters := []retrieval.Participant{
		{ID: "rater-a", Role: "primary", Provider: "fixture", Model: "m-a", IndependenceBasis: "cmd fixture", ParticipatedInTrack: false},
		{ID: "rater-b", Role: "primary", Provider: "fixture", Model: "m-b", IndependenceBasis: "cmd fixture", ParticipatedInTrack: true},
	}
	grader := retrieval.Participant{ID: "grader", Role: "grader", Provider: "fixture", Model: "m-g", IndependenceBasis: "cmd fixture"}
	adjudicator := retrieval.Participant{ID: "adjudicator", Role: "adjudicator", Provider: "fixture", Model: "m-x", IndependenceBasis: "cmd fixture"}

	pre := retrieval.PreRegistration{
		ContractVersion:    retrieval.QrelBlindSmokeContractVersion,
		Evaluation:         retrieval.QrelBlindSmokeEvaluationName,
		PreconditionSHA256: precondition.SHA256,
		PreconditionCommit: precondition.FreezeCommit,
		RecordedAt:         "2026-09-05T09:00:00Z",
		Derivation:         derivation,
		PrimaryRaters:      raters,
		Grader:             grader,
		Adjudicator:        adjudicator,
	}
	// The fixture uses REAL queries from the frozen dataset and REAL prompts
	// rebuilt from the bundle bytes, because `decide` now resolves both. A
	// fixture whose prompts nobody could rebuild would only prove the check
	// does not run.
	dataset, err := retrieval.LoadDataset(filepath.Join(root, filepath.FromSlash(precondition.DatasetPath)))
	if err != nil {
		t.Fatal(err)
	}
	population, err := retrieval.AnswerableHoldout(dataset.Dataset)
	if err != nil {
		t.Fatal(err)
	}
	if len(population) < n {
		t.Fatalf("the frozen dataset holds %d answerable holdout queries, fixture needs %d", len(population), n)
	}
	prompts := map[string]retrieval.RaterPrompt{}
	for i := 0; i < n; i++ {
		q := population[i]
		body := []byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"` + q.ID + `"}],"isError":false}}` + "\n")
		payload := retrieval.PreservedPayload{
			Sequence:  1,
			Boundary:  retrieval.PayloadBoundaryCandidate,
			Operation: retrieval.PayloadOperationTaskContext,
			Bytes:     body,
			SHA256:    retrieval.SHA256Hex(body),
			ByteCount: len(body),
			TokenCounts: []retrieval.PayloadTokenCount{
				{TokenizerID: retrieval.TokenizerID, Tokens: 3},
				{TokenizerID: evaltokenizer.TokenizerID, VocabularySHA256: evaltokenizer.PinnedVocabularySHA256, Tokens: 20},
			},
		}
		if err := retrieval.WriteBlindEvalJSON(filepath.Join(dir, retrieval.BlindEvalBundlesDir, retrieval.BundleFileName(q.ID)),
			retrieval.CapturedCandidateBundle{QueryID: q.ID, Payload: payload, RetrievalStrategy: "semantic_first", RetrievalState: "ready", BundleSummary: "cmd fixture"}); err != nil {
			t.Fatal(err)
		}
		prompt, err := retrieval.BuildRaterPrompt(q.ID, q.Text, payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, retrieval.BlindEvalPromptsDir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, retrieval.BlindEvalPromptsDir, retrieval.PromptFileName(q.ID)), prompt.Bytes, 0o644); err != nil {
			t.Fatal(err)
		}
		prompts[q.ID] = prompt
		pre.Queries = append(pre.Queries, retrieval.PreRegisteredQuery{
			QueryID: q.ID, FamilyID: q.FamilyID, Stratum: q.Stratum,
			QueryTextSHA256:   retrieval.SHA256Hex([]byte(q.Text)),
			PromptSHA256:      prompt.SHA256,
			BundleSHA256:      payload.SHA256,
			BundleByteCount:   payload.ByteCount,
			BundleBoundary:    retrieval.PayloadBoundaryCandidate,
			BundleTokenCounts: payload.TokenCounts,
		})
	}
	// A complete capture provenance, including the candidate binding. Without
	// it the run cannot release at all, which is the point: a run with no
	// evidence of where its bytes came from is not a passing run.
	if err := retrieval.WriteBlindEvalJSON(filepath.Join(dir, retrieval.BlindEvalProvenanceFile), retrieval.CandidateCaptureProvenance{
		CaptureVersion:    retrieval.CandidateCaptureVersion,
		Transport:         "cmd fixture transport",
		Surface:           "cmd fixture surface",
		Boundary:          string(retrieval.PayloadBoundaryCandidate),
		RepoName:          "cobra",
		RepoSHA:           dataset.Dataset.RepoSHA,
		DatasetSHA256:     precondition.DatasetSHA256,
		EmbedderSelector:  "static:fixture@0",
		ModelFingerprint:  "static:fixture@0:fixture",
		IndexFingerprint:  "fixture-index",
		GenerationID:      "g-fixture",
		PersistedVectors:  1,
		SemanticState:     "ready",
		TokenBudget:       retrieval.SavingsCandidateBudget,
		MethodVersion:     "task_context/2",
		TokenizerID:       evaltokenizer.TokenizerID,
		TokenizerVocabSHA: evaltokenizer.PinnedVocabularySHA256,
		QueryCount:        n,
		Binding: &retrieval.CandidateBinding{
			CandidateSHA:           precondition.CandidateSHA,
			FrozenCandidateSHA:     precondition.CandidateSHA,
			CandidateWorktreeClean: true,
			CandidateMatchesFrozen: true,
			CandidateExcludedPath:  relRunDir,
			CheckoutSHA:            dataset.Dataset.RepoSHA,
			CheckoutWorktreeClean:  true,
		},
	}); err != nil {
		t.Fatal(err)
	}
	pre, err = retrieval.SealPreRegistration(pre)
	if err != nil {
		t.Fatal(err)
	}
	if err := retrieval.WriteBlindEvalJSON(filepath.Join(dir, retrieval.BlindEvalPreRegFile), pre); err != nil {
		t.Fatal(err)
	}

	respondedAt := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
	gradedAt := time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC).Format(time.RFC3339)
	for i, q := range pre.Queries {
		outcome := retrieval.GradeOutcomeFail
		if i < passes {
			outcome = retrieval.GradeOutcomePass
		}
		for _, rater := range raters {
			response := retrieval.RaterResponse{
				ContractVersion:       retrieval.QrelBlindSmokeContractVersion,
				Evaluation:            retrieval.QrelBlindSmokeEvaluationName,
				Role:                  "primary",
				QueryID:               q.QueryID,
				RaterID:               rater.ID,
				Provider:              rater.Provider,
				Model:                 rater.Model,
				PreRegistrationSHA256: pre.SHA256,
				QueryTextSHA256:       q.QueryTextSHA256,
				BundleSHA256:          q.BundleSHA256,
				PromptSHA256:          prompts[q.QueryID].SHA256,
				Inputs:                []string{"answer_instructions", "query_text", "preserved_bundle"},
				Status:                retrieval.ResponseStatusAnswered,
				Text:                  "answer for " + q.QueryID,
				RespondedAt:           respondedAt,
			}
			sealedResponse, err := retrieval.SealRaterResponse(response)
			if err != nil {
				t.Fatal(err)
			}
			if err := retrieval.WriteBlindEvalJSON(filepath.Join(dir, retrieval.BlindEvalResponsesDir,
				retrieval.ResponseFileName(q.QueryID, rater.ID)), sealedResponse); err != nil {
				t.Fatal(err)
			}
			grade := retrieval.Grade{
				ContractVersion: retrieval.QrelBlindSmokeContractVersion,
				Evaluation:      retrieval.QrelBlindSmokeEvaluationName,
				QueryID:         q.QueryID,
				ResponseSHA256:  sealedResponse.SHA256,
				GraderID:        grader.ID,
				Provider:        grader.Provider,
				Model:           grader.Model,
				RubricSHA256:    rubricSHA,
				Outcome:         outcome,
				Rationale:       "cmd fixture",
				GradedAt:        gradedAt,
			}
			sealedGrade, err := retrieval.SealGrade(grade)
			if err != nil {
				t.Fatal(err)
			}
			if err := retrieval.WriteBlindEvalJSON(filepath.Join(dir, retrieval.BlindEvalGradesDir,
				retrieval.GradeFileName(sealedResponse.SHA256)), sealedGrade); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The sidecar manifest, which the decision requires: without it a deleted
	// capture provenance or a deleted concern record is indistinguishable from
	// a run that never had one.
	if _, err := retrieval.SealSidecarManifest(dir, pre); err != nil {
		t.Fatal(err)
	}
	return dir
}

// blindEvalFixtureRunDir makes a throwaway run directory INSIDE the repository
// and returns its absolute and repository-relative paths. It is removed when the
// test ends, so the worktree it lives in stays clean.
func blindEvalFixtureRunDir(t *testing.T, root string) (string, string) {
	t.Helper()
	parent := filepath.Join(root, "cmd", "retrieval-eval", "testdata")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(parent, "blindeval-run-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.RemoveAll(dir)
		// Remove is a no-op when another fixture still lives here, so the
		// repository is left exactly as it was found.
		os.Remove(parent)
	})
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, filepath.ToSlash(rel)
}

// M5: "the run directory must be inside the repository" was computed and never
// checked — filepath.Rel returns "../../elsewhere" without an error, so a run
// could freeze a mutable rubric and content-address artifacts git never saw.
func TestRetrievalEval_BlindEvalRefusesARunDirectoryOutsideTheRepository(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	t.Run("a directory inside the repository resolves", func(t *testing.T) {
		rel, err := runDirectoryInsideRepository(root, filepath.Join(root, "docs", "eval"))
		if err != nil {
			t.Fatal(err)
		}
		if rel != "docs/eval" {
			t.Errorf("rel = %q", rel)
		}
	})
	for _, outside := range []string{
		filepath.Join(root, "..", "elsewhere"),
		filepath.Join(root, "docs", "..", "..", "elsewhere"),
		t.TempDir(),
	} {
		t.Run("refuses "+outside, func(t *testing.T) {
			if rel, err := runDirectoryInsideRepository(root, outside); err == nil {
				t.Fatalf("a run directory outside the repository resolved to %q instead of being refused", rel)
			} else if !strings.Contains(err.Error(), "outside the repository") {
				t.Errorf("refusal %q does not say the directory is outside the repository", err)
			}
		})
	}
	// And the freeze phase actually applies it.
	var stdout, stderr bytes.Buffer
	code := runBlindEval(blindEvalOptions{
		phase: blindEvalFreeze, dir: filepath.Join(root, "..", "elsewhere"), root: root,
		dataset: "internal/eval/retrieval/testdata/datasets/cobra-v2.json",
	}, &stdout, &stderr)
	if code == exitOK {
		t.Fatal("freeze accepted a run directory outside the repository")
	}
	if !strings.Contains(stderr.String(), "outside the repository") {
		t.Errorf("stderr %q", stderr.String())
	}
}

// B3: the rater prompt was never resolved against anything. decide now rebuilds
// each prompt from the pre-registered bundle bytes and question, so a prompt
// carrying an expected answer — appended after pre-registration and removed
// afterwards — is refused rather than accepted on a digest nobody checks.
func TestRetrievalEval_BlindEvalRefusesAPromptThatIsNotTheOneItPreRegistered(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	dir := buildBlindEvalRunDir(t, 13, 13)
	entries, err := os.ReadDir(filepath.Join(dir, retrieval.BlindEvalPromptsDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("the fixture wrote no prompts")
	}
	victim := filepath.Join(dir, retrieval.BlindEvalPromptsDir, entries[0].Name())
	original, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(victim, append(append([]byte(nil), original...),
		[]byte("\n\nHINT: the answer is Command.ExecuteC in command.go.\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runBlindEval(blindEvalOptions{phase: blindEvalDecide, dir: dir, root: root}, &stdout, &stderr); code == exitOK {
		t.Fatal("decide accepted a prompt that is not the one the pre-registered inputs rebuild to")
	}
	if !strings.Contains(stderr.String(), "is not the prompt its pre-registered query text and bundle bytes rebuild to") {
		t.Errorf("stderr %q does not name the prompt substitution", stderr.String())
	}

	// Restoring the prompt but leaving a response naming the substituted digest
	// must also be refused: the response is what records what was answered from.
	if err := os.WriteFile(victim, original, 0o644); err != nil {
		t.Fatal(err)
	}
	responses, err := os.ReadDir(filepath.Join(dir, retrieval.BlindEvalResponsesDir))
	if err != nil {
		t.Fatal(err)
	}
	responsePath := filepath.Join(dir, retrieval.BlindEvalResponsesDir, responses[0].Name())
	raw, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatal(err)
	}
	var response retrieval.RaterResponse
	if err := jsonUnmarshalStrict(raw, &response); err != nil {
		t.Fatal(err)
	}
	response.PromptSHA256 = retrieval.SHA256Hex([]byte("a prompt nobody committed"))
	sealed, err := retrieval.SealRaterResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	if err := retrieval.WriteBlindEvalJSON(responsePath, sealed); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runBlindEval(blindEvalOptions{phase: blindEvalDecide, dir: dir, root: root}, &stdout, &stderr); code == exitOK {
		t.Fatal("decide accepted a response answered from a prompt nobody committed")
	}
	if !strings.Contains(stderr.String(), "the pre-registered inputs rebuild to prompt") {
		t.Errorf("stderr %q", stderr.String())
	}
}

// N3 + N5, end to end: deleting the disclosed-concern record used to raise the
// corrected count, and at the threshold that turns RELEASE: NO into
// RELEASE: YES with nothing in the report to see. The manifest makes the
// deletion a refusal, and the reason string names the count that actually fell
// short.
func TestRetrievalEval_BlindEvalRefusesADeletedDisclosedConcern(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	dir := buildBlindEvalRunDir(t, 13, 13)

	// The threshold itself: 13 of 13 against k=13 releases.
	var stdout, stderr bytes.Buffer
	if code := runBlindEval(blindEvalOptions{phase: blindEvalDecide, dir: dir, root: root}, &stdout, &stderr); code != exitOK {
		t.Fatalf("the at-k control did not release: exit=%d\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "RELEASE: YES") {
		t.Fatalf("the at-k control did not release: %s", stdout.String())
	}

	// Disclose one counted pass as unsupported. The corrected count is now 12
	// of 13 and the release is decided on the smaller of the two.
	artifacts, err := retrieval.LoadEvaluationArtifacts(dir)
	if err != nil {
		t.Fatal(err)
	}
	queryID := artifacts.PreRegistration.Queries[0].QueryID
	var gradeSHAs []string
	for _, grade := range artifacts.Grades {
		if grade.QueryID == queryID {
			gradeSHAs = append(gradeSHAs, grade.SHA256)
		}
	}
	if len(gradeSHAs) == 0 {
		t.Fatalf("query %s has no grade to raise a concern against", queryID)
	}
	concernsPath := filepath.Join(dir, retrieval.BlindEvalConcernsFile)
	if err := retrieval.WriteBlindEvalJSON(concernsPath, []retrieval.GradingConcern{{
		QueryID:     queryID,
		Kind:        retrieval.ConcernCountedPassNotSupported,
		GradeSHA256: gradeSHAs,
		Summary:     "the fixture discloses this counted pass as unsupported by the bundle-only rule",
		Evidence:    []string{"fixture"},
		RaisedBy:    "fixture",
		RaisedAt:    "2026-09-05T12:00:00Z",
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := retrieval.SealSidecarManifest(dir, artifacts.PreRegistration); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runBlindEval(blindEvalOptions{phase: blindEvalDecide, dir: dir, root: root}, &stdout, &stderr); code == exitOK {
		t.Fatalf("a corrected count below k released: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "RELEASE: NO") {
		t.Errorf("stdout %q does not record RELEASE: NO", stdout.String())
	}
	// N5: the reviewed count is AT k, so a reason saying "reviewed count below
	// k" would be false. The corrected count is the one that fell short.
	if !strings.Contains(stdout.String(), "the corrected pass count is 12 of 13, below the pre-registered k=13") {
		t.Errorf("stdout %q does not name the count that actually failed", stdout.String())
	}

	// N3: delete the disclosed concern. Before the manifest, this raised the
	// corrected count back to 13 and released.
	if err := os.Remove(concernsPath); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code := runBlindEval(blindEvalOptions{phase: blindEvalDecide, dir: dir, root: root}, &stdout, &stderr)
	if code == exitOK || strings.Contains(stdout.String(), "RELEASE: YES") {
		t.Fatalf("deleting the disclosed concern produced a better outcome: exit=%d\nstdout: %s", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), retrieval.BlindEvalSidecarManifestFile) {
		t.Errorf("stderr %q does not name the record that says the concern existed", stderr.String())
	}
}

// N1 + N2, end to end: a candidate binding whose candidate_sha is not a commit
// id and whose exclusion covers the whole repository was accepted as bound.
func TestRetrievalEval_BlindEvalRefusesAFabricatedCandidateBinding(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	dir := buildBlindEvalRunDir(t, 13, 13)

	// Fabricate the provenance the way an operator who never had a real
	// binding would: write it, then record the sidecars over it.
	raw, err := os.ReadFile(filepath.Join(dir, retrieval.BlindEvalProvenanceFile))
	if err != nil {
		t.Fatal(err)
	}
	var provenance retrieval.CandidateCaptureProvenance
	if err := jsonUnmarshalStrict(raw, &provenance); err != nil {
		t.Fatal(err)
	}
	provenance.Binding = &retrieval.CandidateBinding{
		CandidateSHA:           "not-a-sha",
		FrozenCandidateSHA:     provenance.Binding.FrozenCandidateSHA,
		CandidateWorktreeClean: true,
		CandidateMatchesFrozen: true,
		CandidateExcludedPath:  ".",
		CheckoutSHA:            provenance.Binding.CheckoutSHA,
		CheckoutWorktreeClean:  true,
	}
	if err := retrieval.WriteBlindEvalJSON(filepath.Join(dir, retrieval.BlindEvalProvenanceFile), provenance); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, retrieval.BlindEvalSidecarManifestFile)); err != nil {
		t.Fatal(err)
	}
	pre, err := retrieval.LoadPreRegistration(filepath.Join(dir, retrieval.BlindEvalPreRegFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := retrieval.SealSidecarManifest(dir, pre); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runBlindEval(blindEvalOptions{phase: blindEvalDecide, dir: dir, root: root}, &stdout, &stderr)
	if code == exitOK || strings.Contains(stdout.String(), "RELEASE: YES") {
		t.Fatalf("a fabricated candidate binding released: exit=%d\nstdout: %s", code, stdout.String())
	}
	for _, said := range []string{
		"not a 40-character commit id",
		"the excluded path is the only place the candidate tree is allowed to differ",
	} {
		if !strings.Contains(stdout.String(), said) {
			t.Errorf("stdout %q does not say %q", stdout.String(), said)
		}
	}
}

// M5: containment was applied at freeze time only. Seal and decide would
// operate on any directory at all, and neither refused a run directory so broad
// that the candidate exclusion swallowed the implementation.
func TestRetrievalEval_SealAndDecideApplyTheContainmentCheck(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	t.Run("the repository root is refused", func(t *testing.T) {
		if rel, err := runDirectoryInsideRepository(root, root); err == nil {
			t.Fatalf("the repository root resolved to %q instead of being refused", rel)
		} else if !strings.Contains(err.Error(), "excludes the entire implementation") {
			t.Errorf("refusal %q does not say the exclusion swallows the implementation", err)
		}
	})
	t.Run("a directory holding candidate source is refused", func(t *testing.T) {
		if rel, err := runDirectoryInsideRepository(root, filepath.Join(root, "internal", "eval", "retrieval")); err == nil {
			t.Fatalf("a run directory over the implementation resolved to %q", rel)
		} else if !strings.Contains(err.Error(), "holds candidate source") {
			t.Errorf("refusal %q does not name the swallowed source", err)
		}
	})

	// And both phases apply it: a complete run directory copied somewhere else
	// is refused by seal and by decide, because the precondition record names
	// the directory this run was frozen into.
	dir := buildBlindEvalRunDir(t, 13, 13)
	elsewhere, _ := blindEvalFixtureRunDir(t, root)
	if err := os.CopyFS(elsewhere, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{blindEvalSeal, blindEvalDecide} {
		t.Run(phase+" refuses a run directory that is not the frozen one", func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := runBlindEval(blindEvalOptions{phase: phase, dir: elsewhere, root: root}, &stdout, &stderr); code == exitOK {
				t.Fatalf("%s accepted a run directory this run was not frozen into", phase)
			}
			if !strings.Contains(stderr.String(), "is not the directory this run was frozen into") {
				t.Errorf("stderr %q", stderr.String())
			}
		})
	}
}
