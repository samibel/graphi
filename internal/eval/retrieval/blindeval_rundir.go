package retrieval

// Run-directory I/O for the qrel-blind smoke evaluation.
//
// Every loader here fails on an unreadable or malformed file rather than
// skipping it. That is deliberate and it is the SW-279 lesson: a check that
// reports PASS over a population it could not evaluate is worse than one that
// does not run, because it looks like evidence.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Run-directory file and subdirectory names.
const (
	BlindEvalPreconditionFile = "precondition-record.json"
	BlindEvalPreRegFile       = "pre-registration.json"
	BlindEvalProvenanceFile   = "capture-provenance.json"
	BlindEvalComparisonFile   = "end-of-run-hash-comparison.json"
	BlindEvalOutcomeFile      = "outcome.json"
	BlindEvalConcernsFile     = "disclosed-grading-concerns.json"
	BlindEvalReadmeFile       = "README.md"
	BlindEvalBundlesDir       = "bundles"
	BlindEvalPromptsDir       = "prompts"
	BlindEvalResponsesDir     = "responses"
	BlindEvalGradesDir        = "grades"
	BlindEvalAdjudicationsDir = "adjudications"
)

// PreconditionInputGradingRubric is the frozen-input role of the grading
// rubric. The rubric lives INSIDE the run directory, so the precondition
// record's own path for it is the sealed, content-addressed statement of where
// this run lives — which is what binds the candidate exclusion below.
const PreconditionInputGradingRubric = "grading_rubric"

// CheckRunDirectoryRelativePath refuses a run-directory path that is not a
// proper repository-relative subdirectory.
//
// The run directory is not just a place to write files. It is the ONE path the
// candidate binding excludes from its comparison against the frozen candidate,
// because the run necessarily writes into it after the freeze. A run directory
// at the repository root therefore turns that exclusion into
// `git diff … -- . ':(exclude).'`, which returns nothing at all: every later
// change to the implementation — including one that improves retrieval —
// compares as identical to the frozen candidate, and the report says the frozen
// candidate produced the rated bytes.
func CheckRunDirectoryRelativePath(rel string) error {
	trimmed := strings.TrimSpace(rel)
	if trimmed == "" {
		return fmt.Errorf("retrieval %s: the run directory has no repository-relative path", QrelBlindSmokeEvaluationName)
	}
	if trimmed != rel {
		return fmt.Errorf("retrieval %s: the run directory %q is padded with whitespace", QrelBlindSmokeEvaluationName, rel)
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return fmt.Errorf("retrieval %s: the run directory %q is absolute; it is named relative to the repository root so git can be asked about it", QrelBlindSmokeEvaluationName, rel)
	}
	if cleaned := path.Clean(rel); cleaned != rel {
		return fmt.Errorf("retrieval %s: the run directory %q is not a clean repository-relative path (it cleans to %q); a path that has to be normalised before it is compared is a path two checks can read differently", QrelBlindSmokeEvaluationName, rel, cleaned)
	}
	if rel == "." {
		return fmt.Errorf("retrieval %s: the run directory is the repository root; the candidate binding excludes the run directory from its comparison against the frozen candidate, so a run directory at the root excludes the entire implementation and a later retrieval-improving commit would compare as identical to the frozen candidate", QrelBlindSmokeEvaluationName)
	}
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return fmt.Errorf("retrieval %s: the run directory %q is outside the repository; the evaluation's inputs and artifacts must be files git can be asked about", QrelBlindSmokeEvaluationName, rel)
	}
	return nil
}

// RunDirectoryFromPreconditionRecord reads the run directory out of the sealed
// precondition record, from the frozen grading rubric's path.
//
// This is deliberately NOT a parameter. The excluded path in a capture binding
// used to be whatever the binding said it was, so a binding could name an
// unrelated — or an arbitrarily broad — directory and still be accepted. The
// precondition record is content-addressed, the pre-registration names that
// address, and all of the responses name the pre-registration, so the rubric's
// path is the one statement of where this run lives that cannot be edited
// alongside the file that quotes it.
func RunDirectoryFromPreconditionRecord(rec PreconditionRecord) (string, error) {
	for _, input := range rec.Inputs {
		if input.Role != PreconditionInputGradingRubric {
			continue
		}
		dir := path.Dir(filepath.ToSlash(input.Path))
		if err := CheckRunDirectoryRelativePath(dir); err != nil {
			return "", err
		}
		return dir, nil
	}
	return "", fmt.Errorf("retrieval %s: the precondition record froze no %s input, so nothing names the directory this run lives in", QrelBlindSmokeEvaluationName, PreconditionInputGradingRubric)
}

// WriteBlindEvalJSON writes v as indented JSON with a trailing newline.
func WriteBlindEvalJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("retrieval %s: create %s: %w", QrelBlindSmokeEvaluationName, filepath.Dir(path), err)
	}
	encoded, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("retrieval %s: encode %s: %w", QrelBlindSmokeEvaluationName, path, err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return fmt.Errorf("retrieval %s: write %s: %w", QrelBlindSmokeEvaluationName, path, err)
	}
	return nil
}

// WriteBlindEvalJSONWriteOnce writes v at path, refusing when a record already
// exists there whose bytes differ.
//
// This is the append-only seal, and it is the difference between "there is no
// override flag" and "there is no way to change a graded outcome". Sealing used
// to overwrite: change a raw FAIL to PASS, re-run seal, re-run decide, and the
// old grade was simply gone and the replacement sealed validly. Repeat as often
// as needed and RELEASE: NO becomes RELEASE: YES, with no flag, no environment
// variable and nothing in the report to see. Re-sealing IDENTICAL material stays
// idempotent, because every field the seal derives comes from the raw file
// rather than from the clock; re-sealing DIFFERENT material for an address that
// already exists is refused and says so.
//
// The creation is EXCLUSIVE (O_CREATE|O_EXCL), not a read followed by an
// ordinary write. Reading first and writing afterwards is a check/write race:
// two seals of DIFFERING material can both observe an absent destination, both
// proceed, and the last writer installs its grade over the first one's — the
// same overwrite this helper exists to refuse, reached by running the command
// twice at once instead of twice in a row. With an exclusive create only one
// writer can create the file; every other writer takes the "already exists"
// branch and is compared against what is actually on disk.
func WriteBlindEvalJSONWriteOnce(kind, path string, v any) error {
	encoded, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("retrieval %s: encode %s: %w", QrelBlindSmokeEvaluationName, path, err)
	}
	encoded = append(encoded, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("retrieval %s: create %s: %w", QrelBlindSmokeEvaluationName, filepath.Dir(path), err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	switch {
	case err == nil:
		if _, err := file.Write(encoded); err != nil {
			file.Close()
			return fmt.Errorf("retrieval %s: write %s: %w", QrelBlindSmokeEvaluationName, path, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("retrieval %s: write %s: %w", QrelBlindSmokeEvaluationName, path, err)
		}
		return nil
	case os.IsExist(err):
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("retrieval %s: read existing %s at %s: %w", QrelBlindSmokeEvaluationName, kind, path, readErr)
		}
		if bytes.Equal(existing, encoded) {
			return nil
		}
		return fmt.Errorf("retrieval %s: %s already exists at %s and the material being sealed differs from it; a sealed %s is append-only and is never re-sealed, re-graded, retried or replaced — delete nothing and change nothing, because a second answer for the same address is the retry loop this evaluation exists to exclude",
			QrelBlindSmokeEvaluationName, kind, path, kind)
	default:
		return fmt.Errorf("retrieval %s: create %s at %s: %w", QrelBlindSmokeEvaluationName, kind, path, err)
	}
}

// WriteSealedAdjudication writes one adjudication, refusing to change the
// adjudicator ANSWER that is already sealed there.
//
// The disclosure half of the record is derived, deterministically, from
// artifacts that are themselves append-only, so recomputing it introduces no
// freedom and lets the record's shape be corrected. The adjudicator's response
// is the answer, and an answer is written once.
func WriteSealedAdjudication(path string, adj Adjudication) error {
	existing, err := os.ReadFile(path)
	switch {
	case err == nil:
		// Read only the answer's digest, tolerantly: the disclosure half may be
		// an older shape, and the point of this check is the answer.
		var sealed struct {
			Response struct {
				SHA256 string `json:"sha256"`
			} `json:"response"`
		}
		if err := json.Unmarshal(existing, &sealed); err != nil {
			return fmt.Errorf("retrieval %s: parse the sealed adjudication at %s: %w", QrelBlindSmokeEvaluationName, path, err)
		}
		if sealed.Response.SHA256 != adj.Response.SHA256 {
			return fmt.Errorf("retrieval %s: query %s is already sealed with adjudicator response %s and the material being sealed addresses to %s; an adjudicator answer is written once and is never re-sealed or replaced",
				QrelBlindSmokeEvaluationName, adj.QueryID, sealed.Response.SHA256, adj.Response.SHA256)
		}
	case os.IsNotExist(err):
	default:
		return fmt.Errorf("retrieval %s: read the sealed adjudication at %s: %w", QrelBlindSmokeEvaluationName, path, err)
	}
	return WriteBlindEvalJSON(path, adj)
}

func readBlindEvalJSON(path string, into any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("retrieval %s: read %s: %w", QrelBlindSmokeEvaluationName, path, err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return fmt.Errorf("retrieval %s: parse %s: %w", QrelBlindSmokeEvaluationName, path, err)
	}
	return nil
}

// LoadPreconditionRecord reads and validates a precondition record. A record
// that does not validate is an error, which is the refusal to start.
func LoadPreconditionRecord(path string) (PreconditionRecord, error) {
	var rec PreconditionRecord
	if err := readBlindEvalJSON(path, &rec); err != nil {
		return PreconditionRecord{}, err
	}
	if err := ValidatePreconditionRecord(rec); err != nil {
		return PreconditionRecord{}, err
	}
	return rec, nil
}

// LoadPreRegistration reads and validates a pre-registration record.
func LoadPreRegistration(path string) (PreRegistration, error) {
	var pre PreRegistration
	if err := readBlindEvalJSON(path, &pre); err != nil {
		return PreRegistration{}, err
	}
	if err := ValidatePreRegistration(pre); err != nil {
		return PreRegistration{}, err
	}
	return pre, nil
}

// jsonFilesIn lists the .json files directly inside dir, sorted. A missing
// directory yields an empty list and no error, because "no responses yet" is a
// legitimate state that the decision procedure will then refuse on its own
// terms (a pre-registered query with no response record).
func jsonFilesIn(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("retrieval %s: read %s: %w", QrelBlindSmokeEvaluationName, dir, err)
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		files = append(files, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(files)
	return files, nil
}

// LoadEvaluationArtifacts reads a complete run directory.
func LoadEvaluationArtifacts(dir string) (EvaluationArtifacts, error) {
	var artifacts EvaluationArtifacts
	precondition, err := LoadPreconditionRecord(filepath.Join(dir, BlindEvalPreconditionFile))
	if err != nil {
		return artifacts, err
	}
	pre, err := LoadPreRegistration(filepath.Join(dir, BlindEvalPreRegFile))
	if err != nil {
		return artifacts, err
	}
	artifacts.Precondition = precondition
	artifacts.PreRegistration = pre

	responseFiles, err := jsonFilesIn(filepath.Join(dir, BlindEvalResponsesDir))
	if err != nil {
		return artifacts, err
	}
	for _, path := range responseFiles {
		var response RaterResponse
		if err := readBlindEvalJSON(path, &response); err != nil {
			return artifacts, err
		}
		artifacts.Responses = append(artifacts.Responses, response)
	}
	gradeFiles, err := jsonFilesIn(filepath.Join(dir, BlindEvalGradesDir))
	if err != nil {
		return artifacts, err
	}
	for _, path := range gradeFiles {
		var grade Grade
		if err := readBlindEvalJSON(path, &grade); err != nil {
			return artifacts, err
		}
		artifacts.Grades = append(artifacts.Grades, grade)
	}
	adjudicationFiles, err := jsonFilesIn(filepath.Join(dir, BlindEvalAdjudicationsDir))
	if err != nil {
		return artifacts, err
	}
	for _, path := range adjudicationFiles {
		var adjudication Adjudication
		if err := readBlindEvalJSON(path, &adjudication); err != nil {
			return artifacts, err
		}
		artifacts.Adjudications = append(artifacts.Adjudications, adjudication)
	}
	// The capture provenance is loaded here, not opportunistically at the call
	// site. It used to be read with `if err == nil`, so every read error —
	// absent file, unreadable file, malformed JSON — was silently the same as
	// "no provenance", and a run with no evidence of transport, checkout,
	// embedder or candidate could still publish RELEASE: YES. An unreadable
	// provenance is now an error, and an absent one is a release refusal that
	// AssessCaptureBinding states in the outcome.
	provenancePath := filepath.Join(dir, BlindEvalProvenanceFile)
	switch _, err := os.Stat(provenancePath); {
	case err == nil:
		if err := readBlindEvalJSON(provenancePath, &artifacts.CaptureProvenance); err != nil {
			return artifacts, err
		}
	case os.IsNotExist(err):
	default:
		return artifacts, fmt.Errorf("retrieval %s: stat %s: %w", QrelBlindSmokeEvaluationName, provenancePath, err)
	}
	concernsPath := filepath.Join(dir, BlindEvalConcernsFile)
	switch _, err := os.Stat(concernsPath); {
	case err == nil:
		if err := readBlindEvalJSON(concernsPath, &artifacts.GradingConcerns); err != nil {
			return artifacts, err
		}
	case os.IsNotExist(err):
	default:
		return artifacts, fmt.Errorf("retrieval %s: stat %s: %w", QrelBlindSmokeEvaluationName, concernsPath, err)
	}
	// Both sidecars above are read with "if the file is there, use it", and
	// both feed the decision: the provenance decides whether the capture is
	// bound, and the concern record subtracts from the corrected count. So an
	// absent one used to be indistinguishable from one that had been deleted,
	// and deleting a concern record raised the corrected count. The manifest
	// is the record that they existed, it is mandatory, and it makes every
	// deletion, substitution and forgery of a sidecar a refusal here rather
	// than a better number later.
	if err := CheckSidecarBinding(dir, artifacts.PreRegistration); err != nil {
		return EvaluationArtifacts{}, err
	}
	return artifacts, nil
}

// PromptFileName is the run-directory file name for one rater prompt.
func PromptFileName(queryID string) string { return safeBlindEvalName(queryID) + ".txt" }

// CheckPromptBinding resolves the prompt every rater was actually given
// against the pre-registered inputs, by RECONSTRUCTING it.
//
// The prompt is the whole of a rater's world, and it used to be the one input
// nothing checked. `PreRegisteredQuery` carried the query and bundle digests
// but no prompt digest; sealing hashed whichever prompt file happened to exist
// at sealing time; validation only checked the resulting hex was well shaped;
// and `decide` never opened a prompt at all. So an expected answer appended to
// a prompt after pre-registration, rated, sealed, and then removed left a
// response whose recorded prompt digest resolved to nothing anybody checked.
//
// Reconstruction closes it at the root rather than at the digest: the prompt is
// a pure function of the pre-registered bundle bytes and the pre-registered
// query text, so this rebuilds it and requires the committed prompt file to be
// byte-identical, requires every response and adjudicator response for that
// query to carry that digest, and — for a run that pre-registered the digest
// too — requires the pre-registration to agree.
func CheckPromptBinding(dir string, a EvaluationArtifacts, queryText map[string]string) error {
	expected := make(map[string]string, len(a.PreRegistration.Queries))
	for _, q := range a.PreRegistration.Queries {
		text, known := queryText[q.QueryID]
		if !known {
			return fmt.Errorf("retrieval %s: query %s is pre-registered but absent from the frozen dataset", QrelBlindSmokeEvaluationName, q.QueryID)
		}
		if SHA256Hex([]byte(text)) != q.QueryTextSHA256 {
			return fmt.Errorf("retrieval %s: query %s reads from the frozen dataset with text hashing to %s, but %s was pre-registered", QrelBlindSmokeEvaluationName, q.QueryID, SHA256Hex([]byte(text)), q.QueryTextSHA256)
		}
		var bundle CapturedCandidateBundle
		bundlePath := filepath.Join(dir, BlindEvalBundlesDir, BundleFileName(q.QueryID))
		if err := readBlindEvalJSON(bundlePath, &bundle); err != nil {
			return err
		}
		if bundle.Payload.SHA256 != q.BundleSHA256 || SHA256Hex(bundle.Payload.Bytes) != q.BundleSHA256 {
			return fmt.Errorf("retrieval %s: the committed bundle for query %s hashes to %s, but %s was pre-registered; the bytes on disk are not the bytes this run pre-registered", QrelBlindSmokeEvaluationName, q.QueryID, SHA256Hex(bundle.Payload.Bytes), q.BundleSHA256)
		}
		rebuilt, err := BuildRaterPrompt(q.QueryID, text, bundle.Payload)
		if err != nil {
			return err
		}
		promptPath := filepath.Join(dir, BlindEvalPromptsDir, PromptFileName(q.QueryID))
		onDisk, err := os.ReadFile(promptPath)
		if err != nil {
			return fmt.Errorf("retrieval %s: read the prompt query %s was answered from (%s): %w", QrelBlindSmokeEvaluationName, q.QueryID, promptPath, err)
		}
		if !bytes.Equal(onDisk, rebuilt.Bytes) {
			return fmt.Errorf("retrieval %s: the committed prompt for query %s is not the prompt its pre-registered query text and bundle bytes rebuild to; a prompt carrying anything else — an expected answer, a hint, a different question — is not the input this evaluation pre-registered", QrelBlindSmokeEvaluationName, q.QueryID)
		}
		digest := SHA256Hex(rebuilt.Bytes)
		if q.PromptSHA256 != "" && q.PromptSHA256 != digest {
			return fmt.Errorf("retrieval %s: query %s pre-registers prompt %s, but its pre-registered query text and bundle rebuild to %s", QrelBlindSmokeEvaluationName, q.QueryID, q.PromptSHA256, digest)
		}
		expected[q.QueryID] = digest
	}
	check := func(r RaterResponse) error {
		want, known := expected[r.QueryID]
		if !known {
			return fmt.Errorf("retrieval %s: a response names query %s, which is not pre-registered", QrelBlindSmokeEvaluationName, r.QueryID)
		}
		if r.PromptSHA256 != want {
			return fmt.Errorf("retrieval %s: query %s was answered by %s from prompt %s, but the pre-registered inputs rebuild to prompt %s", QrelBlindSmokeEvaluationName, r.QueryID, r.RaterID, r.PromptSHA256, want)
		}
		return nil
	}
	for _, r := range a.Responses {
		if err := check(r); err != nil {
			return err
		}
	}
	for _, adj := range a.Adjudications {
		if err := check(adj.Response); err != nil {
			return err
		}
	}
	return nil
}

// RepoFileSHA256Reader reads a repository-relative path and returns its
// SHA-256. It is the production ReadFileSHA256 for the end-of-run comparison.
func RepoFileSHA256Reader(root string) ReadFileSHA256 {
	return func(path string) (string, error) {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return "", err
		}
		return SHA256Hex(raw), nil
	}
}

// BundleFileName is the run-directory file name for one captured bundle.
func BundleFileName(queryID string) string { return safeBlindEvalName(queryID) + ".json" }

// ResponseFileName is the run-directory file name for one rater response.
func ResponseFileName(queryID, raterID string) string {
	return safeBlindEvalName(queryID) + "--" + safeBlindEvalName(raterID) + ".json"
}

// GradeFileName names a grade by the content address of the response it graded,
// so a grade file cannot be silently re-pointed by renaming it.
func GradeFileName(responseSHA256 string) string { return safeBlindEvalName(responseSHA256) + ".json" }

// safeBlindEvalName keeps run-directory file names to a boring, portable
// alphabet. It never shortens, so two distinct ids cannot collide.
func safeBlindEvalName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteString(fmt.Sprintf("_%04x", r))
		}
	}
	return b.String()
}
