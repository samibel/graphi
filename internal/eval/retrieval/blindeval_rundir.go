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
func WriteBlindEvalJSONWriteOnce(kind, path string, v any) error {
	encoded, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("retrieval %s: encode %s: %w", QrelBlindSmokeEvaluationName, path, err)
	}
	encoded = append(encoded, '\n')
	existing, err := os.ReadFile(path)
	switch {
	case err == nil:
		if bytes.Equal(existing, encoded) {
			return nil
		}
		return fmt.Errorf("retrieval %s: %s already exists at %s and the material being sealed differs from it; a sealed %s is append-only and is never re-sealed, re-graded, retried or replaced — delete nothing and change nothing, because a second answer for the same address is the retry loop this evaluation exists to exclude",
			QrelBlindSmokeEvaluationName, kind, path, kind)
	case os.IsNotExist(err):
	default:
		return fmt.Errorf("retrieval %s: read existing %s at %s: %w", QrelBlindSmokeEvaluationName, kind, path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("retrieval %s: create %s: %w", QrelBlindSmokeEvaluationName, filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return fmt.Errorf("retrieval %s: write %s: %w", QrelBlindSmokeEvaluationName, path, err)
	}
	return nil
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
