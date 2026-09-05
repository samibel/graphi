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
			if !strings.Contains(string(report), fmt.Sprintf("| observed pass count | %d |", tc.passes)) {
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
	if !strings.Contains(stderr.String(), "freeze, capture, decide") {
		t.Errorf("stderr %q does not enumerate the accepted phases", stderr.String())
	}
}

// buildBlindEvalRunDir writes a complete, correctly ordered run directory with
// n queries of which passes pass. It uses real repository files as the frozen
// inputs so the end-of-run comparison genuinely reads them.
func buildBlindEvalRunDir(t *testing.T, n, passes int) string {
	t.Helper()
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	read := retrieval.RepoFileSHA256Reader(root)
	inputs := []struct{ role, path string }{
		{"budgets", "docs/eval/retrieval-budgets.json"},
		{"targets", "docs/eval/retrieval-targets.json"},
		{"grading_rubric", "docs/eval/retrieval/README.md"},
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
	type queryFixture struct {
		id     string
		text   string
		bundle []byte
	}
	fixtures := make([]queryFixture, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("hq-%02d", i+1)
		bundle := []byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"` + id + `"}],"isError":false}}` + "\n")
		fixtures = append(fixtures, queryFixture{id: id, text: "question " + id, bundle: bundle})
		pre.Queries = append(pre.Queries, retrieval.PreRegisteredQuery{
			QueryID: id, FamilyID: "f-" + id, Stratum: "nl_behaviour",
			QueryTextSHA256: retrieval.SHA256Hex([]byte("question " + id)),
			BundleSHA256:    retrieval.SHA256Hex(bundle),
			BundleByteCount: len(bundle),
			BundleBoundary:  retrieval.PayloadBoundaryCandidate,
			BundleTokenCounts: []retrieval.PayloadTokenCount{
				{TokenizerID: retrieval.TokenizerID, Tokens: 3},
				{TokenizerID: evaltokenizer.TokenizerID, VocabularySHA256: evaltokenizer.PinnedVocabularySHA256, Tokens: 20},
			},
		})
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
				PromptSHA256:          retrieval.SHA256Hex([]byte("prompt:" + q.QueryID + ":" + rater.ID)),
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
	_ = fixtures
	return dir
}
