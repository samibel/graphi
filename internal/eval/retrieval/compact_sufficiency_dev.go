package retrieval

// A separate development diagnostic. Nothing in this module produces a release
// decision or accepts a mixed/holdout dataset. Capture/selection is upstream of
// this module: only the registration and later grading see the dev qrels.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	evaltokenizer "github.com/samibel/graphi/internal/eval/tokenizer"
)

const (
	CompactDevSufficiencyVersion    = "compact-dev-sufficiency/1"
	CompactDevSufficiencyScope      = "development_diagnostic_not_release"
	CompactDevSufficiencyBudget     = 140
	CompactDevSufficiencyPopulation = 40
	compactDevRegistrationFile      = "pre-registration.json"
)

// Participant identities are frozen before any answer is accepted. Independent
// sessions may use the same model; IDs must identify different sessions.
type CompactDevParticipant struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type CompactDevSufficiencyBinding struct {
	CandidateSHA string `json:"candidate_git_sha"`
	// Binds a preserved manifest of the actual candidate files, including a
	// dirty worktree. A git HEAD by itself does not identify uncommitted code.
	CandidateFilesSHA256 string                    `json:"candidate_files_sha256"`
	CandidateFiles       []CompactDevCandidateFile `json:"candidate_files"`
	// Computed from the ordered query_id/input_sha256 pairs in the captures.
	InputSHA256 string                   `json:"input_sha256"`
	Primary     [2]CompactDevParticipant `json:"primary"`
	Grader      CompactDevParticipant    `json:"grader"`
	Adjudicator CompactDevParticipant    `json:"adjudicator"`
}

type CompactDevCandidateFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type CompactDevSufficiencyQuery struct {
	QueryID         string              `json:"query_id"`
	Query           string              `json:"query"`
	QueryTextSHA256 string              `json:"query_text_sha256"`
	InputSHA256     string              `json:"input_sha256"`
	Captures        [2]PreservedPayload `json:"independent_captures"`
	Prompt          []byte              `json:"prompt"`
	PromptSHA256    string              `json:"prompt_sha256"`
	BundleOffset    int                 `json:"bundle_offset"`
}

type CompactDevSufficiencyRegistration struct {
	Version          string                       `json:"version"`
	Scope            string                       `json:"scope"`
	WireVersion      string                       `json:"wire_version"`
	DatasetSHA256    string                       `json:"dataset_sha256"`
	RepoSHA          string                       `json:"repo_sha"`
	Binding          CompactDevSufficiencyBinding `json:"binding"`
	TokenizerID      string                       `json:"tokenizer_id"`
	VocabularySHA256 string                       `json:"vocabulary_sha256"`
	SourceBudget     int                          `json:"source_budget_whitespace_tokens"`
	N                int                          `json:"n"`
	K                int                          `json:"k"`
	Floor            string                       `json:"floor"`
	ConfidenceMethod string                       `json:"confidence_method"`
	LevelBasisPoints int                          `json:"level_basis_points"`
	Rubric           string                       `json:"rubric"`
	Queries          []CompactDevSufficiencyQuery `json:"queries"`
	SHA256           string                       `json:"sha256"`
}

const compactDevRubric = `Grade only the answer's correctness and sufficiency for the question using the development answer key. Every factual claim required for a correct answer must be supported by the exact preserved bundle bytes; external repository knowledge cannot repair missing bundle evidence. Pass only a concrete, correct, supported answer; otherwise fail. Cite the supporting or missing bundle evidence in the rationale. Empty, missing, refused and INSUFFICIENT responses fail mechanically. Do not disclose the answer key, grades or other answers to any answerer.`

// BuildCompactDevSufficiencyRegistration is called only after both capture sets
// are complete. It does not retrieve, select, repair or score any source text.
// It projects the graded dev dataset to ID/text and makes byte-exact prompts.
func BuildCompactDevSufficiencyRegistration(datasetRaw []byte, binding CompactDevSufficiencyBinding, first, second map[string]PreservedPayload, real PayloadCounter) (CompactDevSufficiencyRegistration, error) {
	if real.Count == nil || real.TokenizerID != evaltokenizer.TokenizerID || real.VocabularySHA256 != evaltokenizer.PinnedVocabularySHA256 {
		return CompactDevSufficiencyRegistration{}, fmt.Errorf("compact dev sufficiency: require executable pinned cl100k_base counter")
	}
	var ds Dataset
	decoder := json.NewDecoder(bytes.NewReader(datasetRaw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ds); err != nil {
		return CompactDevSufficiencyRegistration{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return CompactDevSufficiencyRegistration{}, fmt.Errorf("compact dev sufficiency: trailing dataset JSON")
	}
	if err := ds.Validate(); err != nil {
		return CompactDevSufficiencyRegistration{}, err
	}
	members, err := SelectEqualRecallDevPopulation(&ds)
	if err != nil {
		return CompactDevSufficiencyRegistration{}, err
	}
	if len(members) != CompactDevSufficiencyPopulation || len(first) != len(members) || len(second) != len(members) {
		return CompactDevSufficiencyRegistration{}, fmt.Errorf("compact dev sufficiency: require exactly 40 answerable dev queries and two complete captures")
	}
	if !isLowerHexDigest(binding.CandidateSHA, 40) || len(binding.CandidateFiles) == 0 || !isLowerHexDigest(ds.RepoSHA, 40) {
		return CompactDevSufficiencyRegistration{}, fmt.Errorf("compact dev sufficiency: missing candidate commit or actual-files digest")
	}
	lastPath := ""
	for _, file := range binding.CandidateFiles {
		if !fs.ValidPath(file.Path) || file.Path == "." || file.Path <= lastPath || !isLowerHexDigest(file.SHA256, 64) {
			return CompactDevSufficiencyRegistration{}, fmt.Errorf("compact dev sufficiency: candidate file manifest must be sorted, unique, safe and content addressed")
		}
		lastPath = file.Path
	}
	manifestRaw, err := json.Marshal(binding.CandidateFiles)
	if err != nil {
		return CompactDevSufficiencyRegistration{}, err
	}
	manifestSHA := SHA256Hex(manifestRaw)
	if binding.CandidateFilesSHA256 != "" && binding.CandidateFilesSHA256 != manifestSHA {
		return CompactDevSufficiencyRegistration{}, fmt.Errorf("compact dev sufficiency: candidate files manifest digest drift")
	}
	binding.CandidateFilesSHA256 = manifestSHA
	ids := map[string]bool{}
	for _, p := range []CompactDevParticipant{binding.Primary[0], binding.Primary[1], binding.Grader, binding.Adjudicator} {
		if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.Provider) == "" || strings.TrimSpace(p.Model) == "" || ids[p.ID] {
			return CompactDevSufficiencyRegistration{}, fmt.Errorf("compact dev sufficiency: participant identities must be complete and distinct")
		}
		ids[p.ID] = true
	}
	k, err := MinimumPassCount(len(members))
	if err != nil {
		return CompactDevSufficiencyRegistration{}, err
	}
	reg := CompactDevSufficiencyRegistration{
		Version: CompactDevSufficiencyVersion, Scope: CompactDevSufficiencyScope, WireVersion: CompactTaskContextDevVersion,
		DatasetSHA256: SHA256Hex(datasetRaw), RepoSHA: ds.RepoSHA, Binding: binding,
		TokenizerID: real.TokenizerID, VocabularySHA256: real.VocabularySHA256,
		SourceBudget: CompactDevSufficiencyBudget, N: len(members), K: k,
		Floor: FloorString(), ConfidenceMethod: ClopperPearsonMethod, LevelBasisPoints: QrelBlindSmokeLevelBasisPoints, Rubric: compactDevRubric,
	}
	texts := make(map[string]string)
	for _, q := range ds.Queries {
		texts[q.ID] = q.Text
	}
	type inputAddress struct {
		QueryID string `json:"query_id"`
		SHA256  string `json:"input_sha256"`
	}
	var inputAddresses []inputAddress
	for _, member := range members {
		id := member.QueryID
		captures := [2]PreservedPayload{first[id], second[id]}
		var inputSHA string
		for _, capture := range captures {
			if _, err := validatePreservedPayload(id, "compact-dev", capture, 1, PayloadBoundaryCandidate, map[string]string{TokenizerID: "", real.TokenizerID: real.VocabularySHA256}, map[string]PayloadCounter{real.TokenizerID: real}); err != nil {
				return CompactDevSufficiencyRegistration{}, err
			}
			if capture.Operation != CompactTaskContextDevVersion {
				return CompactDevSufficiencyRegistration{}, fmt.Errorf("compact dev sufficiency: %s has wrong wire operation", id)
			}
			_, body, err := ParseCompactTaskContextDev(capture.Bytes)
			if err != nil {
				return CompactDevSufficiencyRegistration{}, err
			}
			if body.Provenance.Budget != CompactDevSufficiencyBudget {
				return CompactDevSufficiencyRegistration{}, fmt.Errorf("compact dev sufficiency: %s source budget changed", id)
			}
			inputSHA = body.Provenance.InputSHA256
		}
		if !reflect.DeepEqual(captures[0], captures[1]) {
			return CompactDevSufficiencyRegistration{}, fmt.Errorf("compact dev sufficiency: %s independent capture bytes/digests/counts differ", id)
		}
		// Reuse the established instructions but name this wire version honestly.
		prefix := RaterInstructions + "\n\nQUESTION:\n" + texts[id] + "\n\n----- BEGIN " + CompactTaskContextDevVersion + " RESPONSE BYTES -----\n"
		prompt := append([]byte(prefix), captures[0].Bytes...)
		prompt = append(prompt, []byte("----- END "+CompactTaskContextDevVersion+" RESPONSE BYTES -----\n")...)
		reg.Queries = append(reg.Queries, CompactDevSufficiencyQuery{QueryID: id, Query: texts[id], QueryTextSHA256: SHA256Hex([]byte(texts[id])), InputSHA256: inputSHA, Captures: captures, Prompt: prompt, PromptSHA256: SHA256Hex(prompt), BundleOffset: len(prefix)})
		inputAddresses = append(inputAddresses, inputAddress{id, inputSHA})
	}
	inputRaw, err := json.Marshal(inputAddresses)
	if err != nil {
		return CompactDevSufficiencyRegistration{}, err
	}
	inputSHA := SHA256Hex(inputRaw)
	if binding.InputSHA256 != "" && binding.InputSHA256 != inputSHA {
		return CompactDevSufficiencyRegistration{}, fmt.Errorf("compact dev sufficiency: ordered input population digest changed")
	}
	reg.Binding.InputSHA256 = inputSHA
	reg.SHA256, err = ContentAddress(reg, func(r *CompactDevSufficiencyRegistration) { r.SHA256 = "" })
	return reg, err
}

// Validation reconstructs every field from bound dataset bytes and captures.
// This refuses missing, reordered, duplicated or substituted population rows,
// drifted k, altered instructions, token counts and even self-resealed drift.
func ValidateCompactDevSufficiencyRegistration(reg CompactDevSufficiencyRegistration, datasetRaw []byte, real PayloadCounter) error {
	first, second := map[string]PreservedPayload{}, map[string]PreservedPayload{}
	for _, q := range reg.Queries {
		if _, duplicate := first[q.QueryID]; duplicate {
			return fmt.Errorf("compact dev sufficiency: duplicate query %s", q.QueryID)
		}
		first[q.QueryID], second[q.QueryID] = q.Captures[0], q.Captures[1]
	}
	want, err := BuildCompactDevSufficiencyRegistration(datasetRaw, reg.Binding, first, second, real)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(reg, want) {
		return fmt.Errorf("compact dev sufficiency: registration differs from bound inputs or content digest")
	}
	return nil
}

// Records form a hash chain rooted in the registration. Slot 0/1 is a primary;
// slot 2 is an independent blind adjudicator answer, only after disagreement.
// Grade records bind the response hash and the pre-registered grader/rubric.
type CompactDevSufficiencyRecord struct {
	Version            string                `json:"version"`
	RegistrationSHA256 string                `json:"registration_sha256"`
	Sequence           int                   `json:"sequence"`
	PreviousSHA256     string                `json:"previous_sha256"`
	Kind               string                `json:"kind"`
	QueryID            string                `json:"query_id"`
	Slot               int                   `json:"slot"`
	Participant        CompactDevParticipant `json:"participant"`
	PromptSHA256       string                `json:"prompt_sha256,omitempty"`
	// Inputs is the complete invocation input inventory. Rater records must
	// name only answer_instructions, query_text and preserved_bundle.
	Inputs         []string `json:"inputs,omitempty"`
	Status         string   `json:"status,omitempty"`
	Text           string   `json:"text,omitempty"`
	ResponseSHA256 string   `json:"response_sha256,omitempty"`
	Outcome        string   `json:"outcome,omitempty"`
	Rationale      string   `json:"rationale,omitempty"`
	SHA256         string   `json:"sha256"`
}

type CompactDevSufficiencyQueryOutcome struct {
	QueryID  string `json:"query_id"`
	Pass     bool   `json:"pass"`
	Complete bool   `json:"complete"`
	Reason   string `json:"reason"`
}

type CompactDevSufficiencyOutcome struct {
	Version            string                              `json:"version"`
	Scope              string                              `json:"scope"`
	RegistrationSHA256 string                              `json:"registration_sha256"`
	RecordHeadSHA256   string                              `json:"record_head_sha256"`
	N                  int                                 `json:"n"`
	K                  int                                 `json:"k"`
	Passed             int                                 `json:"passed"`
	Complete           bool                                `json:"complete"`
	DiagnosticPass     bool                                `json:"diagnostic_pass"`
	Queries            []CompactDevSufficiencyQueryOutcome `json:"queries"`
	SHA256             string                              `json:"sha256"`
}

type compactDevQueryState struct {
	responses [3]*CompactDevSufficiencyRecord
	grades    [3]*CompactDevSufficiencyRecord
}

func compactDevRecords(reg CompactDevSufficiencyRegistration, records []CompactDevSufficiencyRecord) (map[string]*compactDevQueryState, error) {
	states := make(map[string]*compactDevQueryState)
	queries := make(map[string]CompactDevSufficiencyQuery)
	for _, q := range reg.Queries {
		states[q.QueryID] = &compactDevQueryState{}
		queries[q.QueryID] = q
	}
	previous := reg.SHA256
	for i := range records {
		r := &records[i]
		digest, err := ContentAddress(*r, func(v *CompactDevSufficiencyRecord) { v.SHA256 = "" })
		if err != nil {
			return nil, err
		}
		state := states[r.QueryID]
		if r.Version != CompactDevSufficiencyVersion || r.RegistrationSHA256 != reg.SHA256 || r.Sequence != i+1 || r.PreviousSHA256 != previous || r.SHA256 != digest || state == nil || r.Slot < 0 || r.Slot > 2 {
			return nil, fmt.Errorf("compact dev sufficiency: record %d identity, population or digest chain invalid", i+1)
		}
		if r.Slot == 2 && (state.grades[0] == nil || state.grades[1] == nil || state.grades[0].Outcome == state.grades[1].Outcome || state.responses[0].Status != ResponseStatusAnswered || state.responses[1].Status != ResponseStatusAnswered) {
			return nil, fmt.Errorf("compact dev sufficiency: adjudication requires two differing primary grades")
		}
		switch r.Kind {
		case "response":
			participant := reg.Binding.Adjudicator
			if r.Slot < 2 {
				participant = reg.Binding.Primary[r.Slot]
			}
			if state.responses[r.Slot] != nil {
				return nil, fmt.Errorf("compact dev sufficiency: response retry/overwrite refused")
			}
			if r.Participant != participant || r.PromptSHA256 != queries[r.QueryID].PromptSHA256 || r.ResponseSHA256 != "" || r.Outcome != "" || r.Rationale != "" || !reflect.DeepEqual(r.Inputs, []string{"answer_instructions", "query_text", "preserved_bundle"}) {
				return nil, fmt.Errorf("compact dev sufficiency: response identity or blind input inventory invalid")
			}
			if err := compactDevResponseStatus(*r); err != nil {
				return nil, err
			}
			state.responses[r.Slot] = r
		case "grade":
			response := state.responses[r.Slot]
			if response == nil || state.grades[r.Slot] != nil {
				return nil, fmt.Errorf("compact dev sufficiency: grade without prior response or grade retry/overwrite")
			}
			if r.Participant != reg.Binding.Grader || r.ResponseSHA256 != response.SHA256 || r.PromptSHA256 != "" || len(r.Inputs) != 0 || r.Status != "" || r.Text != "" || strings.TrimSpace(r.Rationale) == "" || (r.Outcome != GradeOutcomePass && r.Outcome != GradeOutcomeFail) {
				return nil, fmt.Errorf("compact dev sufficiency: invalid grade or response binding")
			}
			if response.Status != ResponseStatusAnswered {
				return nil, fmt.Errorf("compact dev sufficiency: empty/missing/refused/INSUFFICIENT response fails mechanically and cannot be graded")
			}
			state.grades[r.Slot] = r
		default:
			return nil, fmt.Errorf("compact dev sufficiency: unknown record kind %q", r.Kind)
		}
		previous = r.SHA256
	}
	return states, nil
}

func compactDevResponseStatus(r CompactDevSufficiencyRecord) error {
	t := strings.TrimSpace(r.Text)
	insufficient := strings.HasPrefix(strings.ToUpper(t), "INSUFFICIENT")
	switch r.Status {
	case ResponseStatusAnswered:
		if t == "" || insufficient {
			return fmt.Errorf("compact dev sufficiency: empty or INSUFFICIENT response must fail mechanically")
		}
	case "insufficient":
		if !insufficient {
			return fmt.Errorf("compact dev sufficiency: insufficient status lacks INSUFFICIENT answer")
		}
	case ResponseStatusMissing, ResponseStatusEmpty:
		if t != "" {
			return fmt.Errorf("compact dev sufficiency: missing/empty response contains text")
		}
	case ResponseStatusRefused:
	default:
		return fmt.Errorf("compact dev sufficiency: invalid response status")
	}
	return nil
}

// WriteCompactDevSufficiencyRun seals the registration and exact plaintext
// prompts. It accepts no responses and writes no answer key. The caller must
// give raters only prompts/<id>.txt, never its private dataset or grading data.
func WriteCompactDevSufficiencyRun(dir string, reg CompactDevSufficiencyRegistration, datasetRaw []byte, real PayloadCounter) error {
	if err := ValidateCompactDevSufficiencyRegistration(reg, datasetRaw, real); err != nil {
		return err
	}
	if err := WriteBlindEvalJSONWriteOnce("compact dev pre-registration", filepath.Join(dir, compactDevRegistrationFile), reg); err != nil {
		return err
	}
	for _, q := range reg.Queries {
		if filepath.Base(q.QueryID) != q.QueryID || strings.ContainsAny(q.QueryID, "/\\") {
			return fmt.Errorf("compact dev sufficiency: unsafe query filename")
		}
		if err := compactDevWriteOnce(filepath.Join(dir, "prompts", q.QueryID+".txt"), q.Prompt); err != nil {
			return err
		}
	}
	return nil
}

func compactDevWriteOnce(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0444)
	if os.IsExist(err) {
		prior, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Equal(prior, raw) {
			return nil
		}
		return fmt.Errorf("compact dev sufficiency: overwrite refused for %s", path)
	}
	if err != nil {
		return err
	}
	_, err = f.Write(raw)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func compactDevReadJSON(path string, out any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("compact dev sufficiency: trailing JSON in %s", path)
	}
	return nil
}

func compactDevLoad(dir string, datasetRaw []byte, real PayloadCounter) (CompactDevSufficiencyRegistration, []CompactDevSufficiencyRecord, error) {
	var reg CompactDevSufficiencyRegistration
	if err := compactDevReadJSON(filepath.Join(dir, compactDevRegistrationFile), &reg); err != nil {
		return reg, nil, err
	}
	if err := ValidateCompactDevSufficiencyRegistration(reg, datasetRaw, real); err != nil {
		return reg, nil, err
	}
	for _, q := range reg.Queries {
		raw, err := os.ReadFile(filepath.Join(dir, "prompts", q.QueryID+".txt"))
		if err != nil || !bytes.Equal(raw, q.Prompt) {
			return reg, nil, fmt.Errorf("compact dev sufficiency: missing or drifted prompt %s", q.QueryID)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "records"))
	if os.IsNotExist(err) {
		return reg, nil, nil
	}
	if err != nil {
		return reg, nil, err
	}
	var records []CompactDevSufficiencyRecord
	for i, entry := range entries {
		if entry.IsDir() || entry.Name() != fmt.Sprintf("%06d.json", i+1) {
			return reg, nil, fmt.Errorf("compact dev sufficiency: noncontiguous or unexpected record %s", entry.Name())
		}
		var r CompactDevSufficiencyRecord
		if err := compactDevReadJSON(filepath.Join(dir, "records", entry.Name()), &r); err != nil {
			return reg, nil, err
		}
		records = append(records, r)
	}
	return reg, records, nil
}

// Append seals one first attempt. Version, sequence and hashes are derived;
// caller supplies the invocation/grade fields. Duplicate slots are refused even
// for identical responses. A process race cannot replace an existing sequence.
func AppendCompactDevSufficiencyRecord(dir string, datasetRaw []byte, real PayloadCounter, record CompactDevSufficiencyRecord) (CompactDevSufficiencyRecord, error) {
	reg, records, err := compactDevLoad(dir, datasetRaw, real)
	if err != nil {
		return record, err
	}
	if _, err := os.Stat(filepath.Join(dir, "outcome.json")); err == nil || !os.IsNotExist(err) {
		return record, fmt.Errorf("compact dev sufficiency: outcome already sealed; run is closed")
	}
	record.Version, record.RegistrationSHA256, record.Sequence = CompactDevSufficiencyVersion, reg.SHA256, len(records)+1
	record.PreviousSHA256 = reg.SHA256
	if len(records) > 0 {
		record.PreviousSHA256 = records[len(records)-1].SHA256
	}
	if record.Kind == "response" && strings.HasPrefix(strings.ToUpper(strings.TrimSpace(record.Text)), "INSUFFICIENT") {
		record.Status = "insufficient"
	}
	record.SHA256, err = ContentAddress(record, func(r *CompactDevSufficiencyRecord) { r.SHA256 = "" })
	if err != nil {
		return record, err
	}
	if _, err := compactDevRecords(reg, append(records, record)); err != nil {
		return record, err
	}
	// Unlike registration sealing, attempt creation is never idempotent: two
	// concurrent first attempts with identical text are still two attempts.
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return record, err
	}
	path := filepath.Join(dir, "records", fmt.Sprintf("%06d.json", record.Sequence))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return record, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0444)
	if err != nil {
		return record, fmt.Errorf("compact dev sufficiency: exclusive attempt creation (no retry): %w", err)
	}
	_, err = f.Write(append(raw, '\n'))
	closeErr := f.Close()
	if err != nil {
		return record, err
	}
	return record, closeErr
}

// Decide includes every registered query. Missing attempts/grades fail and make
// the diagnostic incomplete; an incomplete run cannot produce diagnostic_pass.
// Disagreement uses the independently answered and graded third slot, matching
// the existing blind-evaluation policy without borrowing its release identity.
func DecideCompactDevSufficiency(dir string, datasetRaw []byte, real PayloadCounter) (CompactDevSufficiencyOutcome, error) {
	reg, records, err := compactDevLoad(dir, datasetRaw, real)
	if err != nil {
		return CompactDevSufficiencyOutcome{}, err
	}
	return compactDevDecide(reg, records)
}

func compactDevDecide(reg CompactDevSufficiencyRegistration, records []CompactDevSufficiencyRecord) (CompactDevSufficiencyOutcome, error) {
	states, err := compactDevRecords(reg, records)
	if err != nil {
		return CompactDevSufficiencyOutcome{}, err
	}
	out := CompactDevSufficiencyOutcome{Version: CompactDevSufficiencyVersion, Scope: CompactDevSufficiencyScope, RegistrationSHA256: reg.SHA256, RecordHeadSHA256: reg.SHA256, N: reg.N, K: reg.K, Complete: true}
	if len(records) > 0 {
		out.RecordHeadSHA256 = records[len(records)-1].SHA256
	}
	for _, q := range reg.Queries {
		s := states[q.QueryID]
		row := CompactDevSufficiencyQueryOutcome{QueryID: q.QueryID, Reason: "missing primary response or grade"}
		ready := func(slot int) bool {
			return s.responses[slot] != nil && (s.responses[slot].Status != ResponseStatusAnswered || s.grades[slot] != nil)
		}
		if ready(0) && ready(1) && (s.responses[0].Status != ResponseStatusAnswered || s.responses[1].Status != ResponseStatusAnswered) {
			row.Complete = true
			row.Reason = "non-answered primary response fails mechanically and is not adjudicated"
		} else if s.grades[0] != nil && s.grades[1] != nil {
			row.Complete = true
			if s.grades[0].Outcome == s.grades[1].Outcome {
				row.Pass = s.grades[0].Outcome == GradeOutcomePass
				row.Reason = "primary agreement"
			} else if ready(2) {
				row.Pass = s.responses[2].Status == ResponseStatusAnswered && s.grades[2].Outcome == GradeOutcomePass
				row.Reason = "independent blind adjudication"
			} else {
				row.Complete = false
				row.Reason = "missing disagreement adjudication"
			}
		}
		if row.Pass {
			out.Passed++
		}
		out.Complete = out.Complete && row.Complete
		out.Queries = append(out.Queries, row)
	}
	out.DiagnosticPass = out.Complete && out.Passed >= out.K
	out.SHA256, err = ContentAddress(out, func(o *CompactDevSufficiencyOutcome) { o.SHA256 = "" })
	return out, err
}
