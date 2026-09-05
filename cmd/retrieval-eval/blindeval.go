package main

// The qrel-blind smoke evaluation mode (SW-280, SW-266 slice 6).
//
// Three phases, in the only order they may happen:
//
//	freeze   writes the precondition record — every input named by content hash,
//	         with its freeze commit and timestamp. Nothing else may run until it
//	         exists and validates.
//	capture  derives N and k from the sealed dataset, captures exactly one
//	         complete MCP task_context/2 response per answerable holdout query,
//	         and writes the pre-registration record. Both happen BEFORE any
//	         rater response exists.
//	seal     turns the raters', grader's and adjudicator's raw text into
//	         content-addressed records, deriving each response's status by rule
//	         and each timestamp from the raw file itself. An absent raw file is
//	         a missing response, not an omitted one.
//	decide   reads the responses, grades and adjudications, recompares every
//	         frozen input hash, applies the decision procedure and writes the
//	         outcome and report. RELEASE: NO exits non-zero.
//
// There is deliberately no fourth phase and no option that changes k, the
// population, or a graded outcome.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/samibel/graphi/internal/eval/retrieval"
	evaltokenizer "github.com/samibel/graphi/internal/eval/tokenizer"
)

// Blind-evaluation phases. The flag accepts exactly these three values.
const (
	blindEvalFreeze  = "freeze"
	blindEvalCapture = "capture"
	blindEvalSeal    = "seal"
	blindEvalDecide  = "decide"
)

// BlindEvalPhases is the closed set of accepted phases, exported so the
// no-override test can enumerate it rather than trusting a doc string.
var BlindEvalPhases = []string{blindEvalFreeze, blindEvalCapture, blindEvalSeal, blindEvalDecide}

// blindEvalOptions is everything the mode reads. Every field is a location or
// an identity; none of them is a threshold, a waiver or a retry.
type blindEvalOptions struct {
	phase    string
	dir      string
	root     string
	dataset  string
	repoName string
	checkout string
	embedder string
}

func runBlindEval(o blindEvalOptions, stdout, stderr io.Writer) int {
	switch o.phase {
	case blindEvalFreeze:
		return runBlindEvalFreeze(o, stdout, stderr)
	case blindEvalCapture:
		return runBlindEvalCapture(o, stdout, stderr)
	case blindEvalSeal:
		return runBlindEvalSeal(o, stdout, stderr)
	case blindEvalDecide:
		return runBlindEvalDecide(o, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "retrieval-eval: -blind-eval must be one of %s\n", strings.Join(BlindEvalPhases, ", "))
		return exitUsage
	}
}

// blindEvalFrozenInputs are the files AC-1 requires the precondition record to
// name. The rubric lives inside the run directory and must be committed before
// the freeze; the other three are repository files.
func blindEvalFrozenInputs(runDirRelative string) []struct{ role, path string } {
	return []struct{ role, path string }{
		{"budgets", "docs/eval/retrieval-budgets.json"},
		{"targets", "docs/eval/retrieval-targets.json"},
		{retrieval.PreconditionInputGradingRubric, filepath.ToSlash(filepath.Join(runDirRelative, "grading-rubric.md"))},
		{"methodology", "docs/eval/retrieval/methodology.md"},
	}
}

func runBlindEvalFreeze(o blindEvalOptions, stdout, stderr io.Writer) int {
	if o.dir == "" || o.dataset == "" {
		fmt.Fprintln(stderr, "retrieval-eval: -blind-eval freeze needs -blind-eval-dir and -dataset")
		return exitUsage
	}
	head, err := gitHead(o.root)
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	dataset, err := retrieval.LoadDataset(filepath.Join(o.root, filepath.FromSlash(o.dataset)))
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	runDirRelative, err := runDirectoryInsideRepository(o.root, o.dir)
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	// A candidate sha read off a dirty worktree names a commit whose code is
	// not the code that will run. Freezing it there would make every later
	// binding check compare against a fiction.
	if clean, err := candidateWorktreeClean(o.root); err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	} else if !clean {
		fmt.Fprintf(stderr, "retrieval-eval: the %s refuses to freeze: the candidate worktree at %s has uncommitted changes, so candidate_sha %s would not name the code this evaluation runs\n", retrieval.QrelBlindSmokeEvaluationName, o.root, head)
		return exitError
	}
	record := retrieval.PreconditionRecord{
		ContractVersion:            retrieval.QrelBlindSmokeContractVersion,
		Evaluation:                 retrieval.QrelBlindSmokeEvaluationName,
		FreezeCommit:               head,
		FreezeTimestamp:            time.Now().UTC().Format(time.RFC3339),
		DatasetPath:                filepath.ToSlash(o.dataset),
		DatasetSHA256:              dataset.SHA256,
		CandidateSHA:               head,
		CandidateMethod:            retrieval.SavingsCandidateMethod,
		CandidateTokenBudget:       retrieval.SavingsCandidateBudget,
		ComparatorVersion:          retrieval.BlindEvalComparatorVersion,
		TokenizerID:                tokenizerPinID(),
		TokenizerVocabularySHA256:  tokenizerPinVocabularySHA256(),
		MeasurementContractVersion: retrieval.MeasurementContractVersion,
		ClaimWordingSHA256:         retrieval.SHA256Hex([]byte(retrieval.FrozenClaimWording())),
	}
	read := retrieval.RepoFileSHA256Reader(o.root)
	for _, input := range blindEvalFrozenInputs(filepath.ToSlash(runDirRelative)) {
		sha, err := read(input.path)
		if err != nil {
			fmt.Fprintf(stderr, "retrieval-eval: freeze input %s (%s): %v\n", input.role, input.path, err)
			return exitError
		}
		record.Inputs = append(record.Inputs, retrieval.FrozenInput{Role: input.role, Path: input.path, SHA256: sha})
	}
	sealed, err := retrieval.SealPreconditionRecord(record)
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	if err := retrieval.ValidatePreconditionRecord(sealed); err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	path := filepath.Join(o.dir, retrieval.BlindEvalPreconditionFile)
	if err := retrieval.WriteBlindEvalJSON(path, sealed); err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	fmt.Fprintf(stdout, "retrieval-eval: froze %d inputs for the %s at %s (record sha256 %s)\n",
		len(sealed.Inputs)+1, retrieval.QrelBlindSmokeEvaluationName, path, sealed.SHA256)
	return exitOK
}

func runBlindEvalCapture(o blindEvalOptions, stdout, stderr io.Writer) int {
	if o.dir == "" || o.repoName == "" || o.checkout == "" || o.embedder == "" {
		fmt.Fprintln(stderr, "retrieval-eval: -blind-eval capture needs -blind-eval-dir, -repo, -checkout and -embedder")
		return exitUsage
	}
	// AC-1: refuse to start without a complete precondition record.
	precondition, err := retrieval.LoadPreconditionRecord(filepath.Join(o.dir, retrieval.BlindEvalPreconditionFile))
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: the %s refuses to start: %v\n", retrieval.QrelBlindSmokeEvaluationName, err)
		return exitError
	}
	// And refuse to start over inputs that have already drifted, so the
	// end-of-run comparison is not the first time anyone looks.
	startComparison, err := retrieval.CompareFrozenInputs(precondition, retrieval.RepoFileSHA256Reader(o.root), time.Now().UTC())
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	if !startComparison.AllMatch {
		fmt.Fprintf(stderr, "retrieval-eval: the %s refuses to start: a frozen input already differs from the precondition record\n", retrieval.QrelBlindSmokeEvaluationName)
		for _, c := range startComparison.Comparisons {
			if !c.Matches {
				fmt.Fprintf(stderr, "  %s %s frozen=%s observed=%s %s\n", c.Role, c.Path, c.Frozen, c.Observed, c.Error)
			}
		}
		return exitError
	}

	dataset, err := retrieval.LoadDataset(filepath.Join(o.root, filepath.FromSlash(precondition.DatasetPath)))
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	if dataset.SHA256 != precondition.DatasetSHA256 {
		fmt.Fprintf(stderr, "retrieval-eval: dataset sha256 %s, precondition froze %s\n", dataset.SHA256, precondition.DatasetSHA256)
		return exitError
	}
	population, err := retrieval.AnswerableHoldout(dataset.Dataset)
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	// AC-4: an unsatisfiable N records RELEASE: NO with the reason, and stops.
	// "Records" means an artifact on disk, not a line on stderr: printing the
	// typed error and exiting left automation unable to tell the mandatory
	// refusal apart from a run that was interrupted or never started.
	derivation, err := retrieval.DerivePassCount(len(population), dataset.SHA256,
		"count of answerable holdout queries in the sealed dataset: split=holdout, stratum!=no_hit, at least one grade-3 span")
	if err != nil {
		outcome := retrieval.UnsatisfiableOutcome(precondition, len(population), err)
		if writeErr := retrieval.WriteBlindEvalJSON(filepath.Join(o.dir, retrieval.BlindEvalOutcomeFile), outcome); writeErr != nil {
			fmt.Fprintf(stderr, "retrieval-eval: %v\n", writeErr)
			return exitError
		}
		fmt.Fprintf(stderr, "retrieval-eval: RELEASE: NO — %v\n", err)
		fmt.Fprintf(stdout, "retrieval-eval: recorded RELEASE: NO in %s\n", filepath.Join(o.dir, retrieval.BlindEvalOutcomeFile))
		return exitError
	}
	fmt.Fprintf(stdout, "retrieval-eval: N=%d, k=%d (lower bound %s) derived before any response is opened\n",
		derivation.N, derivation.K, derivation.KInterval.LowerBound)

	// The judgements must still resolve at the pin, or the population is not
	// the answerable population the derivation counted.
	slice := *dataset.Dataset
	slice.Queries = population
	if err := retrieval.CheckSpanCoverage(o.checkout, &slice); err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: holdout span coverage at the pin: %v\n", err)
		return exitError
	}

	counter, err := retrieval.LoadPinnedRealPayloadCounter()
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	repoSHA, err := retrieval.CheckoutHEAD(context.Background(), o.checkout)
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	runDirRelative, err := blindEvalRunDirectory(o.root, o.dir, precondition)
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	captured, provenance, err := retrieval.CaptureCandidateBundles(context.Background(), retrieval.CandidateCaptureOptions{
		RepoRoot:         o.checkout,
		RepoName:         o.repoName,
		RepoSHA:          repoSHA,
		Dataset:          dataset,
		Queries:          population,
		EmbedderSelector: o.embedder,
		RealCounter:      counter,
		Log:              stderr,
		Probe:            retrieval.GitRepoProbe(),
		Binding: retrieval.CandidateBindingOptions{
			CandidateRoot:      o.root,
			FrozenCandidateSHA: precondition.CandidateSHA,
			ExcludePath:        runDirRelative,
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}

	// The field is named precondition_record_commit, so it must name the
	// commit that CONTAINS the precondition record. It used to be copied from
	// the record's own freeze_commit — the candidate commit at freeze time —
	// which is a different commit and, for the first run of this evaluation,
	// a commit in which the record does not exist at all. An auditor following
	// it found nothing, and a record created later could name any earlier
	// commit as its own precedence evidence.
	preconditionCommit, err := gitCommitContaining(o.root, filepath.ToSlash(filepath.Join(runDirRelative, retrieval.BlindEvalPreconditionFile)))
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	pre := retrieval.PreRegistration{
		ContractVersion:    retrieval.QrelBlindSmokeContractVersion,
		Evaluation:         retrieval.QrelBlindSmokeEvaluationName,
		PreconditionSHA256: precondition.SHA256,
		PreconditionCommit: preconditionCommit,
		RecordedAt:         time.Now().UTC().Format(time.RFC3339),
		Derivation:         derivation,
	}
	byID := map[string]retrieval.Query{}
	for _, q := range population {
		byID[q.ID] = q
	}
	for _, bundle := range captured {
		q := byID[bundle.QueryID]
		prompt, err := retrieval.BuildRaterPrompt(q.ID, q.Text, bundle.Payload)
		if err != nil {
			fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
			return exitError
		}
		if err := retrieval.CheckPromptCarriesPreservedBundle(prompt, bundle.Payload); err != nil {
			fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
			return exitError
		}
		if err := retrieval.WriteBlindEvalJSON(filepath.Join(o.dir, retrieval.BlindEvalBundlesDir, retrieval.BundleFileName(q.ID)), bundle); err != nil {
			fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
			return exitError
		}
		if err := os.MkdirAll(filepath.Join(o.dir, retrieval.BlindEvalPromptsDir), 0o755); err != nil {
			fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
			return exitError
		}
		promptPath := filepath.Join(o.dir, retrieval.BlindEvalPromptsDir, retrieval.BundleFileName(q.ID))
		if err := os.WriteFile(strings.TrimSuffix(promptPath, ".json")+".txt", prompt.Bytes, 0o644); err != nil {
			fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
			return exitError
		}
		pre.Queries = append(pre.Queries, retrieval.PreRegisteredQuery{
			QueryID:           q.ID,
			FamilyID:          q.FamilyID,
			Stratum:           q.Stratum,
			QueryTextSHA256:   retrieval.SHA256Hex([]byte(q.Text)),
			PromptSHA256:      retrieval.SHA256Hex(prompt.Bytes),
			BundleSHA256:      bundle.Payload.SHA256,
			BundleByteCount:   bundle.Payload.ByteCount,
			BundleBoundary:    bundle.Payload.Boundary,
			BundleTokenCounts: bundle.Payload.TokenCounts,
		})
	}
	if err := retrieval.WriteBlindEvalJSON(filepath.Join(o.dir, retrieval.BlindEvalProvenanceFile), provenance); err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}

	// The raters, grader and adjudicator are declared here, before any response
	// exists, so an identity cannot be chosen after seeing an answer.
	participants, err := loadBlindEvalParticipants(filepath.Join(o.dir, "participants.json"))
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	pre.PrimaryRaters = participants.PrimaryRaters
	pre.Grader = participants.Grader
	pre.Adjudicator = participants.Adjudicator

	sealed, err := retrieval.SealPreRegistration(pre)
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	if err := retrieval.ValidatePreRegistration(sealed); err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	if err := retrieval.WriteBlindEvalJSON(filepath.Join(o.dir, retrieval.BlindEvalPreRegFile), sealed); err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	fmt.Fprintf(stdout, "retrieval-eval: captured %d task_context/2 bundles and pre-registered N=%d k=%d (record sha256 %s)\n",
		len(captured), sealed.Derivation.N, sealed.Derivation.K, sealed.SHA256)
	return exitOK
}

// blindEvalParticipants is the declared panel, read from the run directory
// before capture completes.
type blindEvalParticipants struct {
	PrimaryRaters []retrieval.Participant `json:"primary_raters"`
	Grader        retrieval.Participant   `json:"grader"`
	Adjudicator   retrieval.Participant   `json:"adjudicator"`
}

func loadBlindEvalParticipants(path string) (blindEvalParticipants, error) {
	var participants blindEvalParticipants
	raw, err := os.ReadFile(path)
	if err != nil {
		return participants, fmt.Errorf("read declared panel %s: %w", path, err)
	}
	if err := jsonUnmarshalStrict(raw, &participants); err != nil {
		return participants, fmt.Errorf("parse declared panel %s: %w", path, err)
	}
	return participants, nil
}

func runBlindEvalDecide(o blindEvalOptions, stdout, stderr io.Writer) int {
	if o.dir == "" {
		fmt.Fprintln(stderr, "retrieval-eval: -blind-eval decide needs -blind-eval-dir")
		return exitUsage
	}
	// M5: containment was checked at freeze time only, so decide would read a
	// run directory anywhere at all — including one assembled outside the
	// repository, where the rubric and every artifact are files git never saw,
	// or one so broad that the candidate exclusion swallows the
	// implementation. It is checked BEFORE anything in the directory is read,
	// because a directory this phase must not operate on is not a directory to
	// start parsing records out of.
	if _, err := runDirectoryInsideRepository(o.root, o.dir); err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	artifacts, err := retrieval.LoadEvaluationArtifacts(o.dir)
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	// And it must be the directory this run was frozen into, which the
	// precondition record names.
	if _, err := blindEvalRunDirectory(o.root, o.dir, artifacts.Precondition); err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	// Resolve the prompts the raters were actually given against the
	// pre-registered inputs, by rebuilding each one from the pre-registered
	// bundle bytes and question. Nothing else opens a prompt file.
	dataset, err := retrieval.LoadDataset(filepath.Join(o.root, filepath.FromSlash(artifacts.Precondition.DatasetPath)))
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	if dataset.SHA256 != artifacts.Precondition.DatasetSHA256 {
		fmt.Fprintf(stderr, "retrieval-eval: dataset sha256 %s, precondition froze %s\n", dataset.SHA256, artifacts.Precondition.DatasetSHA256)
		return exitError
	}
	queryText := map[string]string{}
	for _, q := range dataset.Dataset.Queries {
		queryText[q.ID] = q.Text
	}
	if err := retrieval.CheckPromptBinding(o.dir, artifacts, queryText); err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	comparison, err := retrieval.CompareFrozenInputs(artifacts.Precondition, retrieval.RepoFileSHA256Reader(o.root), time.Now().UTC())
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	outcome, err := retrieval.EvaluateQrelBlindSmoke(artifacts, comparison)
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	if err := retrieval.ValidateEvaluationOutcome(outcome, artifacts); err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	report, err := retrieval.RenderQrelBlindSmokeReport(outcome, artifacts.PreRegistration, artifacts.Precondition, artifacts.CaptureProvenance)
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	if err := retrieval.WriteBlindEvalJSON(filepath.Join(o.dir, retrieval.BlindEvalComparisonFile), comparison); err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	if err := retrieval.WriteBlindEvalJSON(filepath.Join(o.dir, retrieval.BlindEvalOutcomeFile), outcome); err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	if err := os.WriteFile(filepath.Join(o.dir, retrieval.BlindEvalReadmeFile), []byte(report), 0o644); err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	fmt.Fprintf(stdout, "retrieval-eval: %s — %d/%d passed against k=%d; RELEASE: %s\n",
		retrieval.QrelBlindSmokeEvaluationName, outcome.PassCount, outcome.N, outcome.K, outcome.Release)
	for _, reason := range outcome.Reasons {
		fmt.Fprintf(stdout, "  %s\n", reason)
	}
	if outcome.Release != retrieval.ReleaseYes {
		return exitError
	}
	return exitOK
}

// runDirectoryInsideRepository resolves the run directory relative to the
// repository root and REFUSES a path that escapes it.
//
// filepath.Rel happily returns "../../elsewhere" and returns no error doing so,
// so computing it was never a containment check. A run directory outside the
// repository is a mutable rubric and a mutable set of artifacts that git never
// saw: the evaluation would freeze inputs nobody can retrieve and publish
// content addresses over files that were never committed.
func runDirectoryInsideRepository(root, dir string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, absDir)
	if err != nil {
		return "", fmt.Errorf("run directory %s is not under %s", dir, root)
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("run directory %s resolves to %s, which is outside the repository at %s; the evaluation's inputs and artifacts must be files git can be asked about", dir, rel, absRoot)
	}
	// Inside the repository is not enough. The run directory is the one path
	// the candidate binding excludes from its comparison against the frozen
	// candidate, so a run directory at the root — or any directory that
	// contains the candidate's own source — makes that exclusion swallow the
	// implementation, and a later retrieval-improving commit then compares as
	// identical to the frozen candidate.
	if err := retrieval.CheckRunDirectoryRelativePath(rel); err != nil {
		return "", err
	}
	if swallowed, err := runDirectoryHoldsCandidateSource(absDir); err != nil {
		return "", err
	} else if swallowed != "" {
		return "", fmt.Errorf("run directory %s holds candidate source (%s); the candidate binding excludes the run directory from its comparison against the frozen candidate, so a run directory over the implementation excludes the very code the binding exists to pin", rel, swallowed)
	}
	return rel, nil
}

// runDirectoryHoldsCandidateSource names the first candidate source file found
// under the run directory, or "" when there is none.
//
// "Is this exclusion too broad?" has no useful answer in the abstract; it has a
// concrete one here. The exclusion is too broad exactly when it covers code
// whose change the binding is supposed to notice, so this looks for that code.
// A run directory that does not yet exist holds nothing, which is the state the
// freeze phase legitimately starts from.
func runDirectoryHoldsCandidateSource(absDir string) (string, error) {
	found := ""
	err := filepath.WalkDir(absDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".go") || entry.Name() == "go.mod" {
			found = entry.Name()
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("examine run directory %s: %w", absDir, err)
	}
	return found, nil
}

// blindEvalRunDirectory resolves the run directory AND requires it to be the
// directory the precondition record was frozen into.
//
// Containment was only ever checked at freeze time, so `seal` and `decide`
// would happily operate on any directory at all — including one assembled
// outside the repository after the fact. Requiring the frozen run directory as
// well makes the phases agree about which run they are working on: the
// precondition record is content-addressed, and the rubric path inside it is
// the sealed statement of where this run lives.
func blindEvalRunDirectory(root, dir string, precondition retrieval.PreconditionRecord) (string, error) {
	rel, err := runDirectoryInsideRepository(root, dir)
	if err != nil {
		return "", err
	}
	frozen, err := retrieval.RunDirectoryFromPreconditionRecord(precondition)
	if err != nil {
		return "", err
	}
	if rel != frozen {
		return "", fmt.Errorf("run directory %s is not the directory this run was frozen into (%s); the precondition record's grading rubric names the run directory, and a phase run over a different directory is a different run", rel, frozen)
	}
	return rel, nil
}

// candidateWorktreeClean reports whether the candidate repository has no
// uncommitted change.
func candidateWorktreeClean(root string) (bool, error) {
	return retrieval.GitRepoProbe().WorktreeClean(context.Background(), root)
}

// gitCommitContaining names the commit that last wrote path, and refuses when
// git has never seen it. The capture already requires a clean worktree, so the
// committed bytes and the bytes on disk are the same bytes.
func gitCommitContaining(root, path string) (string, error) {
	out, err := exec.Command("git", "-C", root, "log", "-1", "--format=%H", "--", path).Output()
	if err != nil {
		return "", fmt.Errorf("git log for %s in %s: %w", path, root, err)
	}
	commit := strings.TrimSpace(string(out))
	if commit == "" {
		return "", fmt.Errorf("%s is not committed; the pre-registration must name the commit that contains the precondition record, and an uncommitted record has no such commit", path)
	}
	return commit, nil
}

func gitHead(root string) (string, error) {
	cmd := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD in %s: %w", root, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// jsonUnmarshalStrict refuses unknown fields, so a run-directory file carrying
// a field this build does not understand is an error rather than a silent
// partial read.
func jsonUnmarshalStrict(raw []byte, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(into)
}

// tokenizerPinID and tokenizerPinVocabularySHA256 read the SAME pin table the
// offline loader verifies, so the precondition record cannot name a tokenizer
// the measurement did not use.
func tokenizerPinID() string { return evaltokenizer.TokenizerID }

func tokenizerPinVocabularySHA256() string { return evaltokenizer.PinnedVocabularySHA256 }

// repositoryRoot locates the graphi module root by walking up from the working
// directory to the first go.mod. Every frozen input path in the precondition
// record is relative to it, so a run started from a subdirectory hashes the
// same files as one started from the root.
func repositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found walking up from the working directory")
		}
		dir = parent
	}
}
