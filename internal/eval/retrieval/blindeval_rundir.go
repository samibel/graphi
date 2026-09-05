package retrieval

// Run-directory I/O for the qrel-blind smoke evaluation.
//
// Every loader here fails on an unreadable or malformed file rather than
// skipping it. That is deliberate and it is the SW-279 lesson: a check that
// reports PASS over a population it could not evaluate is worse than one that
// does not run, because it looks like evidence.

import (
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
	return artifacts, nil
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
