// Command compact-sufficiency-dev prepares and records one explicitly dev-only
// blind sufficiency diagnostic. It has no dataset, budget or threshold override.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/samibel/graphi/internal/eval/retrieval"
)

const (
	devDatasetPath    = "docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/dataset.json"
	devDatasetSHA256  = "2d05e3bb015a1447e0c31a9a855712e6aae6f4281adbf7acd72e86c923a43d6c"
	devCapturePath    = "docs/eval/retrieval/runs/2026-09-07-answer-recovery-dev/bundles-after.json"
	devCaptureSHA256  = "9ab813c03a414b5d5d698581c9f293d78cbbc218f9a2452b96454f5516675b1d"
	devGrepReadPath   = "docs/eval/retrieval/runs/2026-09-07-grepread-v2-dev/grepread-v2.json"
	devGrepReadSHA256 = "ed58d7d0d9e1e6f7f6f790ebfb7a1249ecd6d80aea34de97b8e9c05a473f436b"
)

const usage = `compact-sufficiency-dev PHASE [flags]

  prepare  -run-dir REL -candidate-sha GIT_SHA -candidate-files MANIFEST.json -participants PARTICIPANTS.json -repository COBRA_CHECKOUT
  response -run-dir REL -query ID -slot 0|1|2 -status answered|empty|missing|refused -response-file ANSWER.txt
  grade    -run-dir REL -query ID -slot 0|1|2 -outcome pass|fail -rationale-file REASON.txt
  decide   -run-dir REL [-seal]

Run directories are proper repository-relative subdirectories. Dataset and
two-build input paths are compiled in; source budget is 250 and k is derived.
Only prompts/<query>.txt may be sent to answerers. Slot 2 is a fresh blind
adjudicator and is accepted only after two answered primary grades disagree.
INSUFFICIENT is a mechanical failure. Every response/grade is a first attempt;
no retry/overwrite flag exists. decide emits only status/counts/digests; -seal
writes outcome.json once and closes further appends. All results are dev-only.
`

type options struct {
	phase, runDir, candidateSHA, candidateFiles, participants string
	repository                                                string
	query, status, responseFile, outcome, rationaleFile       string
	slot                                                      int
	seal                                                      bool
}

func main() {
	if err := run(os.Args[1:], os.Stdout, ""); err != nil {
		fmt.Fprintf(os.Stderr, "compact-sufficiency-dev: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer, root string) error {
	rootInjected := root != ""
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		_, err := io.WriteString(out, usage)
		return err
	}
	o := options{phase: args[0]}
	allowed := map[string]string{
		"prepare":  "run-dir candidate-sha candidate-files participants repository",
		"response": "run-dir query slot status response-file",
		"grade":    "run-dir query slot outcome rationale-file",
		"decide":   "run-dir seal",
	}
	if _, ok := allowed[o.phase]; !ok {
		return fmt.Errorf("unknown phase %q", o.phase)
	}
	flags := flag.NewFlagSet(o.phase, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&o.runDir, "run-dir", "", "repository-relative run directory")
	flags.StringVar(&o.candidateSHA, "candidate-sha", "", "exact candidate HEAD")
	flags.StringVar(&o.candidateFiles, "candidate-files", "", "JSON array of sorted path/sha256 records")
	flags.StringVar(&o.participants, "participants", "", "frozen four-participant identity JSON")
	flags.StringVar(&o.repository, "repository", "", "clean pinned source checkout used for declaration hydration")
	flags.StringVar(&o.query, "query", "", "registered query ID")
	flags.IntVar(&o.slot, "slot", -1, "primary 0/1 or adjudicator 2")
	flags.StringVar(&o.status, "status", "", "response status")
	flags.StringVar(&o.responseFile, "response-file", "", "exact raw response text file")
	flags.StringVar(&o.outcome, "outcome", "", "grade pass/fail")
	flags.StringVar(&o.rationaleFile, "rationale-file", "", "raw grading rationale text file")
	flags.BoolVar(&o.seal, "seal", false, "seal outcome and close appends")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err = io.WriteString(out, usage)
			return err
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	var wrongFlag string
	flags.Visit(func(f *flag.Flag) {
		if !strings.Contains(" "+allowed[o.phase]+" ", " "+f.Name+" ") {
			wrongFlag = f.Name
		}
	})
	if wrongFlag != "" {
		return fmt.Errorf("flag -%s is not accepted for %s", wrongFlag, o.phase)
	}
	if err := retrieval.CheckRunDirectoryRelativePath(o.runDir); err != nil {
		return err
	}
	if !strings.HasPrefix(o.runDir, "docs/eval/retrieval/runs/") {
		return fmt.Errorf("run-dir must be a named directory beneath docs/eval/retrieval/runs/")
	}
	if root == "" {
		raw, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
		if err != nil {
			return fmt.Errorf("locate repository root: %w", err)
		}
		root = strings.TrimSpace(string(raw))
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	dir, err := insidePath(root, o.runDir)
	if err != nil {
		return err
	}
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) && path == dir {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink inside run directory refused")
		}
		return nil
	}); err != nil {
		return err
	}
	datasetRaw, err := readInside(root, devDatasetPath)
	if err != nil {
		return err
	}
	if retrieval.SHA256Hex(datasetRaw) != devDatasetSHA256 {
		return fmt.Errorf("fixed development dataset digest changed")
	}
	real, err := retrieval.LoadPinnedRealPayloadCounter()
	if err != nil {
		return err
	}
	if o.phase == "prepare" {
		// The binary always requires a source checkout. Tests inject a temporary
		// module root and may omit it to exercise the append-only protocol with
		// the frozen pre-v6 fixture captures.
		if o.repository == "" && !rootInjected {
			return fmt.Errorf("prepare requires -repository for compact-dev/7")
		}
		return prepare(root, dir, datasetRaw, real, o, out)
	}
	var reg retrieval.CompactDevSufficiencyRegistration
	regRaw, err := readInside(root, filepath.ToSlash(filepath.Join(o.runDir, "pre-registration.json")))
	if err != nil {
		return err
	}
	if err := strictJSON(regRaw, &reg); err != nil {
		return err
	}
	if err := retrieval.ValidateCompactDevSufficiencyRegistration(reg, datasetRaw, real); err != nil {
		return err
	}
	if err := verifyCandidate(root, o.runDir, reg.Binding.CandidateSHA, reg.Binding.CandidateFiles); err != nil {
		return err
	}
	if o.phase == "decide" {
		result, err := retrieval.DecideCompactDevSufficiency(dir, datasetRaw, real)
		if err != nil {
			return err
		}
		if o.seal {
			if err := retrieval.WriteBlindEvalJSONWriteOnce("compact dev outcome", filepath.Join(dir, "outcome.json"), result); err != nil {
				return err
			}
		}
		return emit(out, map[string]any{"scope": result.Scope, "phase": "decide", "n": result.N, "k": result.K, "passed": result.Passed, "complete": result.Complete, "diagnostic_pass": result.DiagnosticPass, "registration_sha256": result.RegistrationSHA256, "outcome_sha256": result.SHA256, "sealed": o.seal})
	}
	if o.slot < 0 || o.slot > 2 {
		return fmt.Errorf("slot must be 0, 1 or 2")
	}
	var query *retrieval.CompactDevSufficiencyQuery
	for i := range reg.Queries {
		if reg.Queries[i].QueryID == o.query {
			query = &reg.Queries[i]
			break
		}
	}
	if query == nil {
		return fmt.Errorf("query is not in the registered development population")
	}
	record := retrieval.CompactDevSufficiencyRecord{Kind: o.phase, QueryID: o.query, Slot: o.slot}
	if o.phase == "response" {
		if o.status != "answered" && o.status != "empty" && o.status != "missing" && o.status != "refused" {
			return fmt.Errorf("status must be answered/empty/missing/refused; INSUFFICIENT is derived from text")
		}
		raw, err := readRegular(o.responseFile)
		if err != nil {
			return err
		}
		record.Participant = reg.Binding.Adjudicator
		if o.slot < 2 {
			record.Participant = reg.Binding.Primary[o.slot]
		}
		record.PromptSHA256 = query.PromptSHA256
		record.Inputs = []string{"answer_instructions", "query_text", "preserved_bundle"}
		record.Status = o.status
		record.Text = string(raw)
	} else {
		if o.outcome != "pass" && o.outcome != "fail" {
			return fmt.Errorf("outcome must be pass or fail")
		}
		raw, err := readRegular(o.rationaleFile)
		if err != nil {
			return err
		}
		record.Participant = reg.Binding.Grader
		record.Outcome = o.outcome
		record.Rationale = string(raw)
		// Resolve the immutable prior response internally; the caller cannot
		// substitute a response hash from another query, slot or run.
		entries, err := os.ReadDir(filepath.Join(dir, "records"))
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				return fmt.Errorf("unexpected directory in records")
			}
			raw, err := readRegular(filepath.Join(dir, "records", entry.Name()))
			if err != nil {
				return err
			}
			var prior retrieval.CompactDevSufficiencyRecord
			if err := strictJSON(raw, &prior); err != nil {
				return err
			}
			if prior.Kind == "response" && prior.QueryID == o.query && prior.Slot == o.slot {
				if record.ResponseSHA256 != "" {
					return fmt.Errorf("duplicate response slot")
				}
				record.ResponseSHA256 = prior.SHA256
			}
		}
		if record.ResponseSHA256 == "" {
			return fmt.Errorf("grade requires an existing response")
		}
	}
	sealed, err := retrieval.AppendCompactDevSufficiencyRecord(dir, datasetRaw, real, record)
	if err != nil {
		return err
	}
	return emit(out, map[string]any{"scope": retrieval.CompactDevSufficiencyScope, "phase": o.phase, "query_id": o.query, "slot": o.slot, "status": sealed.Status, "record_sha256": sealed.SHA256, "registration_sha256": sealed.RegistrationSHA256})
}

func prepare(root, dir string, datasetRaw []byte, real retrieval.PayloadCounter, o options, out io.Writer) error {
	var files []retrieval.CompactDevCandidateFile
	raw, err := readRegular(o.candidateFiles)
	if err != nil {
		return err
	}
	if err := strictJSON(raw, &files); err != nil {
		return err
	}
	if err := verifyCandidate(root, o.runDir, o.candidateSHA, files); err != nil {
		return err
	}
	var participants struct {
		Primary     []retrieval.CompactDevParticipant `json:"primary"`
		Grader      retrieval.CompactDevParticipant   `json:"grader"`
		Adjudicator retrieval.CompactDevParticipant   `json:"adjudicator"`
	}
	raw, err = readRegular(o.participants)
	if err != nil {
		return err
	}
	if err := strictJSON(raw, &participants); err != nil {
		return err
	}
	if len(participants.Primary) != 2 {
		return fmt.Errorf("exactly two primary identities are required")
	}
	artifactRaw, err := readInside(root, devCapturePath)
	if err != nil {
		return err
	}
	if retrieval.SHA256Hex(artifactRaw) != devCaptureSHA256 {
		return fmt.Errorf("fixed two-build capture artifact digest changed")
	}
	grepReadRaw, err := readInside(root, devGrepReadPath)
	if err != nil {
		return err
	}
	if retrieval.SHA256Hex(grepReadRaw) != devGrepReadSHA256 {
		return fmt.Errorf("fixed GrepRead/2 artifact digest changed")
	}
	var repository fs.FS
	if o.repository != "" {
		repository, err = verifiedSourceRepository(o.repository, datasetRaw)
		if err != nil {
			return err
		}
	}
	first, second, err := buildCaptures(datasetRaw, artifactRaw, real, repository, grepReadRaw)
	if err != nil {
		return err
	}
	binding := retrieval.CompactDevSufficiencyBinding{CandidateSHA: o.candidateSHA, CandidateFiles: files, Primary: [2]retrieval.CompactDevParticipant{participants.Primary[0], participants.Primary[1]}, Grader: participants.Grader, Adjudicator: participants.Adjudicator}
	reg, err := retrieval.BuildCompactDevSufficiencyRegistration(datasetRaw, binding, first, second, real)
	if err != nil {
		return err
	}
	// Recheck the actual candidate after building before sealing any packets.
	if err := verifyCandidate(root, o.runDir, o.candidateSHA, files); err != nil {
		return err
	}
	if err := retrieval.WriteCompactDevSufficiencyRun(dir, reg, datasetRaw, real); err != nil {
		return err
	}
	return emit(out, map[string]any{"scope": reg.Scope, "phase": "prepare", "n": reg.N, "k": reg.K, "source_budget": reg.SourceBudget, "registration_sha256": reg.SHA256, "source_capture_artifact_sha256": retrieval.SHA256Hex(artifactRaw), "candidate_files_sha256": reg.Binding.CandidateFilesSHA256})
}

// Diagnostics are explicitly opaque here. Retrieval receives only question
// text and preserved production bytes. Qrels are decoded after BOTH complete
// 44-query builds finish, solely to choose the predeclared 40-question panel.
func buildCaptures(datasetRaw, artifactRaw []byte, real retrieval.PayloadCounter, repository fs.FS, grepReadRaw ...[]byte) (map[string]retrieval.PreservedPayload, map[string]retrieval.PreservedPayload, error) {
	var projection struct {
		Queries []struct {
			ID    string `json:"id"`
			Text  string `json:"query"`
			Split string `json:"split"`
		} `json:"queries"`
	}
	if err := json.Unmarshal(datasetRaw, &projection); err != nil {
		return nil, nil, err
	}
	texts := map[string]string{}
	for _, q := range projection.Queries {
		if q.Split != retrieval.SplitDev || q.ID == "" || q.Text == "" || texts[q.ID] != "" {
			return nil, nil, fmt.Errorf("invalid dev-only question projection")
		}
		texts[q.ID] = q.Text
	}
	if len(texts) != 44 {
		return nil, nil, fmt.Errorf("fixed development capture population must contain 44 queries")
	}
	grepReads := map[string]retrieval.GrepReadV2Transcript{}
	if len(grepReadRaw) > 1 {
		return nil, nil, fmt.Errorf("at most one GrepRead/2 artifact is accepted")
	}
	if len(grepReadRaw) == 1 {
		var artifact struct {
			DatasetSHA256 string `json:"dataset_sha256"`
			Queries       []struct {
				QueryID    string                         `json:"query_id"`
				Transcript retrieval.GrepReadV2Transcript `json:"transcript"`
			} `json:"queries"`
		}
		if err := json.Unmarshal(grepReadRaw[0], &artifact); err != nil {
			return nil, nil, err
		}
		if artifact.DatasetSHA256 != retrieval.SHA256Hex(datasetRaw) || len(artifact.Queries) != len(texts) {
			return nil, nil, fmt.Errorf("GrepRead/2 artifact is not bound to the full development dataset")
		}
		for _, row := range artifact.Queries {
			if texts[row.QueryID] == "" || row.Transcript.Query != texts[row.QueryID] || grepReads[row.QueryID].Query != "" {
				return nil, nil, fmt.Errorf("GrepRead/2 query identity missing, duplicate or outside dev population")
			}
			if err := row.Transcript.Validate(); err != nil {
				return nil, nil, err
			}
			grepReads[row.QueryID] = row.Transcript
		}
	}
	type row struct {
		QueryID    string                            `json:"query_id"`
		Capture    retrieval.CapturedCandidateBundle `json:"capture"`
		Candidates json.RawMessage                   `json:"candidates_50"`
		Cited      json.RawMessage                   `json:"cited_spans"`
		Ranks      json.RawMessage                   `json:"grade3_ranks_in_50_candidates"`
		Grade3     json.RawMessage                   `json:"grade3_spans"`
		Items      json.RawMessage                   `json:"items"`
		Dropped    json.RawMessage                   `json:"items_dropped"`
		Contained  json.RawMessage                   `json:"snippet_contained_spans"`
		Overlap    json.RawMessage                   `json:"snippet_overlap_spans"`
		Whitespace json.RawMessage                   `json:"snippet_whitespace_tokens"`
		Stratum    json.RawMessage                   `json:"stratum"`
	}
	var artifact struct {
		DatasetSHA256 string          `json:"dataset_sha256"`
		Queries       int             `json:"queries"`
		Builds        [][]row         `json:"independent_builds"`
		Embedder      json.RawMessage `json:"embedder_selector"`
		Identical     json.RawMessage `json:"identical_payloads"`
		Documents     json.RawMessage `json:"input_documents"`
		Contract      json.RawMessage `json:"measurement_contract"`
		Note          json.RawMessage `json:"note"`
	}
	if err := strictJSON(artifactRaw, &artifact); err != nil {
		return nil, nil, err
	}
	if artifact.DatasetSHA256 != retrieval.SHA256Hex(datasetRaw) || artifact.Queries != 44 || len(artifact.Builds) != 2 {
		return nil, nil, fmt.Errorf("capture artifact requires two complete builds bound to the development dataset")
	}
	built := [2]map[string]retrieval.PreservedPayload{{}, {}}
	for index, rows := range artifact.Builds {
		if len(rows) != 44 {
			return nil, nil, fmt.Errorf("incomplete development capture build")
		}
		for _, row := range rows {
			if texts[row.QueryID] == "" || row.Capture.QueryID != row.QueryID || built[index][row.QueryID].Bytes != nil {
				return nil, nil, fmt.Errorf("capture query identity missing, duplicate or outside dev population")
			}
			var transcript *retrieval.GrepReadV2Transcript
			if value, ok := grepReads[row.QueryID]; ok {
				transcript = &value
			}
			var payload retrieval.PreservedPayload
			var err error
			if repository != nil {
				payload, err = retrieval.BuildCompactTaskContextDevWithRepository(texts[row.QueryID], row.Capture.Payload, transcript, repository, retrieval.CompactDevSufficiencyBudget, real)
			} else {
				payload, err = retrieval.BuildCompactTaskContextDevWithGrepRead(texts[row.QueryID], row.Capture.Payload, transcript, retrieval.CompactDevSufficiencyBudget, real)
			}
			if err != nil {
				return nil, nil, err
			}
			built[index][row.QueryID] = payload
		}
	}
	for id, payload := range built[0] {
		if !bytes.Equal(payload.Bytes, built[1][id].Bytes) {
			return nil, nil, fmt.Errorf("independent compact builds differ for %s", id)
		}
	}
	var ds retrieval.Dataset
	if err := strictJSON(datasetRaw, &ds); err != nil {
		return nil, nil, err
	}
	members, err := retrieval.SelectEqualRecallDevPopulation(&ds)
	if err != nil {
		return nil, nil, err
	}
	selected := [2]map[string]retrieval.PreservedPayload{{}, {}}
	for _, member := range members {
		for i := range selected {
			selected[i][member.QueryID] = built[i][member.QueryID]
		}
	}
	return selected[0], selected[1], nil
}

func verifiedSourceRepository(path string, datasetRaw []byte) (fs.FS, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, fmt.Errorf("source repository is not a directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("source repository is not a directory")
	}
	var identity struct {
		RepoSHA string `json:"repo_sha"`
	}
	if err := json.Unmarshal(datasetRaw, &identity); err != nil || len(identity.RepoSHA) != 40 {
		return nil, fmt.Errorf("development dataset has no valid repository identity")
	}
	head, err := exec.Command("git", "-C", absolute, "rev-parse", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(head)) != identity.RepoSHA {
		return nil, fmt.Errorf("source repository HEAD does not match development dataset")
	}
	status, err := exec.Command("git", "-C", absolute, "status", "--porcelain=v1", "--untracked-files=all").Output()
	if err != nil || len(status) != 0 {
		return nil, fmt.Errorf("source repository must be clean")
	}
	return os.DirFS(absolute), nil
}

func verifyCandidate(root, runDir, sha string, files []retrieval.CompactDevCandidateFile) error {
	head, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return fmt.Errorf("read candidate HEAD: %w", err)
	}
	if sha == "" || strings.TrimSpace(string(head)) != sha {
		return fmt.Errorf("candidate SHA does not match repository HEAD")
	}
	status, err := exec.Command("git", "-C", root, "status", "--porcelain=v1", "-z", "--untracked-files=all").Output()
	if err != nil {
		return fmt.Errorf("check complete candidate worktree: %w", err)
	}
	entries := strings.Split(string(status), "\x00")
	for i := 0; i < len(entries)-1; i++ {
		entry := entries[i]
		if len(entry) < 4 || entry[2] != ' ' {
			return fmt.Errorf("malformed git status")
		}
		if !strings.HasPrefix(entry[3:], runDir+"/") {
			return fmt.Errorf("candidate is dirty outside the frozen run directory: %s", entry[3:])
		}
		if strings.ContainsAny(entry[:2], "RC") {
			i++
			if i >= len(entries)-1 || !strings.HasPrefix(entries[i], runDir+"/") {
				return fmt.Errorf("candidate rename/copy crosses the run-directory boundary")
			}
		}
	}
	if len(files) == 0 {
		return fmt.Errorf("candidate file manifest is empty")
	}
	prior := ""
	for _, file := range files {
		if file.Path <= prior {
			return fmt.Errorf("candidate manifest must be sorted and unique")
		}
		if strings.HasPrefix(file.Path, "internal/eval/retrieval/testdata/datasets/") {
			return fmt.Errorf("dataset files cannot be candidate-manifest inputs")
		}
		prior = file.Path
		raw, err := readInside(root, file.Path)
		if err != nil {
			return err
		}
		if retrieval.SHA256Hex(raw) != file.SHA256 {
			return fmt.Errorf("candidate file digest changed: %s", file.Path)
		}
	}
	return nil
}

func insidePath(root, relative string) (string, error) {
	if !fs.ValidPath(relative) || relative == "." || strings.Contains(relative, "\\") {
		return "", fmt.Errorf("path must be a clean repository-relative path")
	}
	path := root
	for _, component := range strings.Split(relative, "/") {
		path = filepath.Join(path, component)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink path refused")
		}
	}
	return path, nil
}

func readInside(root, relative string) ([]byte, error) {
	path, err := insidePath(root, relative)
	if err != nil {
		return nil, err
	}
	return readRegular(path)
}
func readRegular(path string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("required input file is missing")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("input is not a regular file")
	}
	return os.ReadFile(path)
}
func strictJSON(raw []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing JSON value")
	}
	return nil
}
func emit(out io.Writer, value any) error { return json.NewEncoder(out).Encode(value) }
