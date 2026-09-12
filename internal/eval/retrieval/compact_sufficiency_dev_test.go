package retrieval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func compactSufficiencyFixture(t *testing.T) (CompactDevSufficiencyRegistration, []byte, PayloadCounter) {
	t.Helper()
	// Explicit dev-only path; never load the combined release dataset.
	raw, err := os.ReadFile(filepath.Join(taskContextModuleRoot(t), "docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/dataset.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ds Dataset
	if err := json.Unmarshal(raw, &ds); err != nil {
		t.Fatal(err)
	}
	for i := range ds.Queries {
		for j := range ds.Queries[i].Judgements {
			ds.Queries[i].Judgements[j].Reason += " PRIVATE_QREL_SENTINEL_NEVER_SEND_TO_ANSWERER"
		}
	}
	raw, err = json.Marshal(ds)
	if err != nil {
		t.Fatal(err)
	}
	real, err := LoadPinnedRealPayloadCounter()
	if err != nil {
		t.Fatal(err)
	}
	input := compactTaskContextDevFixtureInput(t, real)
	members, err := SelectEqualRecallDevPopulation(&ds)
	if err != nil {
		t.Fatal(err)
	}
	queries := make(map[string]string)
	for _, q := range ds.Queries {
		queries[q.ID] = q.Text
	}
	first, second := make(map[string]PreservedPayload), make(map[string]PreservedPayload)
	for _, member := range members {
		p, err := BuildCompactTaskContextDev(queries[member.QueryID], input, CompactDevSufficiencyBudget, real)
		if err != nil {
			t.Fatal(err)
		}
		first[member.QueryID], second[member.QueryID] = p, p
	}
	binding := CompactDevSufficiencyBinding{
		CandidateSHA: strings.Repeat("a", 40), CandidateFiles: []CompactDevCandidateFile{{Path: "engine/retrieval/candidate.go", SHA256: SHA256Hex([]byte("test candidate"))}},
		Primary: [2]CompactDevParticipant{{ID: "session-a", Provider: "test", Model: "model"}, {ID: "session-b", Provider: "test", Model: "model"}},
		Grader:  CompactDevParticipant{ID: "grader", Provider: "test", Model: "grader-model"}, Adjudicator: CompactDevParticipant{ID: "session-c", Provider: "test", Model: "model"},
	}
	reg, err := BuildCompactDevSufficiencyRegistration(raw, binding, first, second, real)
	if err != nil {
		t.Fatal(err)
	}
	return reg, raw, real
}

func TestCompactDevSufficiencyRegistration_DeterministicBlindAndDerived(t *testing.T) {
	reg, raw, real := compactSufficiencyFixture(t)
	if err := ValidateCompactDevSufficiencyRegistration(reg, raw, real); err != nil {
		t.Fatal(err)
	}
	k, err := MinimumPassCount(40)
	if err != nil {
		t.Fatal(err)
	}
	if reg.K != k || k != 36 || reg.N != 40 || reg.SourceBudget != CompactDevSufficiencyBudget || reg.Scope != CompactDevSufficiencyScope {
		t.Fatalf("invalid registration: N=%d k=%d scope=%s", reg.N, reg.K, reg.Scope)
	}
	for _, q := range reg.Queries {
		if bytes.Contains(q.Prompt, []byte("PRIVATE_QREL_SENTINEL")) || bytes.Contains(q.Prompt, []byte(`"judgements"`)) || bytes.Contains(q.Prompt, []byte("GRADE:")) {
			t.Fatal("qrel leaked into rater prompt")
		}
		if SHA256Hex(q.Prompt) != q.PromptSHA256 || SHA256Hex([]byte(q.Query)) != q.QueryTextSHA256 {
			t.Fatal("prompt/query content address drift")
		}
		if !bytes.Equal(q.Prompt[q.BundleOffset:q.BundleOffset+len(q.Captures[0].Bytes)], q.Captures[0].Bytes) {
			t.Fatal("prompt changed payload bytes")
		}
	}
	first, second := make(map[string]PreservedPayload), make(map[string]PreservedPayload)
	for i := len(reg.Queries) - 1; i >= 0; i-- {
		q := reg.Queries[i]
		first[q.QueryID], second[q.QueryID] = q.Captures[0], q.Captures[1]
	}
	again, err := BuildCompactDevSufficiencyRegistration(raw, reg.Binding, first, second, real)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reg, again) {
		t.Fatal("map insertion order changed registration")
	}
	// Test-only qrels appear nowhere in the public registration either.
	public, _ := json.Marshal(reg)
	if bytes.Contains(public, []byte("PRIVATE_QREL_SENTINEL")) {
		t.Fatal("qrels leaked into registration")
	}
}

func TestCompactDevSufficiencyRegistration_RejectsDriftAndIncompletePopulation(t *testing.T) {
	reg, raw, real := compactSufficiencyFixture(t)
	for name, mutate := range map[string]func(*CompactDevSufficiencyRegistration){
		"missing population":   func(r *CompactDevSufficiencyRegistration) { r.Queries = r.Queries[:39] },
		"duplicate population": func(r *CompactDevSufficiencyRegistration) { r.Queries[1] = r.Queries[0] },
		"reordered population": func(r *CompactDevSufficiencyRegistration) { r.Queries[0], r.Queries[1] = r.Queries[1], r.Queries[0] },
		"k":                    func(r *CompactDevSufficiencyRegistration) { r.K-- },
		"query":                func(r *CompactDevSufficiencyRegistration) { r.Queries[0].Query += " altered" },
		"prompt":               func(r *CompactDevSufficiencyRegistration) { r.Queries[0].Prompt = append(r.Queries[0].Prompt, '!') },
		"payload": func(r *CompactDevSufficiencyRegistration) {
			r.Queries[0].Captures[0].Bytes = append(r.Queries[0].Captures[0].Bytes, '!')
		},
		"token count":      func(r *CompactDevSufficiencyRegistration) { r.Queries[0].Captures[1].TokenCounts[1].Tokens++ },
		"input population": func(r *CompactDevSufficiencyRegistration) { r.Binding.InputSHA256 = strings.Repeat("0", 64) },
		"candidate manifest": func(r *CompactDevSufficiencyRegistration) {
			r.Binding.CandidateFiles[0].SHA256 = strings.Repeat("0", 64)
		},
		"scope": func(r *CompactDevSufficiencyRegistration) { r.Scope = "release" },
	} {
		t.Run(name, func(t *testing.T) {
			encoded, _ := json.Marshal(reg)
			var altered CompactDevSufficiencyRegistration
			if err := json.Unmarshal(encoded, &altered); err != nil {
				t.Fatal(err)
			}
			mutate(&altered)
			// Self-resealing drift does not bypass recomputation from bound inputs.
			altered.SHA256, _ = ContentAddress(altered, func(r *CompactDevSufficiencyRegistration) { r.SHA256 = "" })
			if err := ValidateCompactDevSufficiencyRegistration(altered, raw, real); err == nil {
				t.Fatal("accepted drift")
			}
		})
	}
	var nonDev Dataset
	if err := json.Unmarshal(raw, &nonDev); err != nil {
		t.Fatal(err)
	}
	nonDev.Queries[0].Split = SplitHoldout
	wrong, _ := json.Marshal(nonDev)
	if err := ValidateCompactDevSufficiencyRegistration(reg, wrong, real); err == nil {
		t.Fatal("accepted mixed split")
	}
	missingCounter := real
	missingCounter.Count = nil
	if err := ValidateCompactDevSufficiencyRegistration(reg, raw, missingCounter); err == nil {
		t.Fatal("accepted absent executable counter")
	}
	for name, invalid := range map[string][]byte{
		"trailing value": append(append([]byte(nil), raw...), []byte(" {}")...),
		"unknown field":  bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"unknown_field":true,"schema_version":1`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateCompactDevSufficiencyRegistration(reg, invalid, real); err == nil {
				t.Fatal("accepted malformed dataset contract")
			}
		})
	}
}

func TestCompactDevSufficiencyRegistration_RejectsValidButDifferentCaptures(t *testing.T) {
	reg, raw, real := compactSufficiencyFixture(t)
	first, second := map[string]PreservedPayload{}, map[string]PreservedPayload{}
	for _, q := range reg.Queries {
		first[q.QueryID], second[q.QueryID] = q.Captures[0], q.Captures[1]
	}
	q := reg.Queries[0]
	input := compactTaskContextDevFixtureInput(t, real)
	for _, budget := range []int{CompactDevSufficiencyBudget - 1, CompactDevSufficiencyBudget} {
		p, err := BuildCompactTaskContextDev(q.Query+" altered", input, budget, real)
		if err != nil {
			t.Fatal(err)
		}
		second[q.QueryID] = p
		if _, err := BuildCompactDevSufficiencyRegistration(raw, reg.Binding, first, second, real); err == nil {
			t.Fatalf("accepted independently valid but changed capture at budget %d", budget)
		}
	}
}

func compactTestRecord(t *testing.T, reg CompactDevSufficiencyRegistration, records []CompactDevSufficiencyRecord, id string, slot int, kind, status, text string) []CompactDevSufficiencyRecord {
	t.Helper()
	r := CompactDevSufficiencyRecord{Version: CompactDevSufficiencyVersion, RegistrationSHA256: reg.SHA256, Sequence: len(records) + 1, PreviousSHA256: reg.SHA256, QueryID: id, Slot: slot, Kind: kind}
	if len(records) > 0 {
		r.PreviousSHA256 = records[len(records)-1].SHA256
	}
	if kind == "response" {
		r.Participant = reg.Binding.Adjudicator
		if slot < 2 {
			r.Participant = reg.Binding.Primary[slot]
		}
		for _, q := range reg.Queries {
			if q.QueryID == id {
				r.PromptSHA256 = q.PromptSHA256
			}
		}
		r.Inputs = []string{"answer_instructions", "query_text", "preserved_bundle"}
		r.Status = status
		r.Text = text
	} else {
		r.Participant = reg.Binding.Grader
		r.Outcome = status
		r.Rationale = "Evidence-specific grading rationale for the fixture"
		for _, prior := range records {
			if prior.Kind == "response" && prior.QueryID == id && prior.Slot == slot {
				r.ResponseSHA256 = prior.SHA256
			}
		}
	}
	var err error
	r.SHA256, err = ContentAddress(r, func(v *CompactDevSufficiencyRecord) { v.SHA256 = "" })
	if err != nil {
		t.Fatal(err)
	}
	return append(records, r)
}

func TestCompactDevSufficiencyDecision_FullPopulationAndMechanicalFailures(t *testing.T) {
	reg, _, _ := compactSufficiencyFixture(t)
	var records []CompactDevSufficiencyRecord
	for _, q := range reg.Queries {
		for slot := 0; slot < 2; slot++ {
			records = compactTestRecord(t, reg, records, q.QueryID, slot, "response", ResponseStatusAnswered, "The answer cites answer.go:2.")
			records = compactTestRecord(t, reg, records, q.QueryID, slot, "grade", GradeOutcomePass, "")
		}
	}
	out, err := compactDevDecide(reg, records)
	if err != nil {
		t.Fatal(err)
	}
	if out.Passed != 40 || !out.Complete || !out.DiagnosticPass || out.Scope != CompactDevSufficiencyScope {
		t.Fatalf("unexpected outcome %+v", out)
	}
	partial, err := compactDevDecide(reg, records[:len(records)-1])
	if err != nil {
		t.Fatal(err)
	}
	if partial.DiagnosticPass || partial.Complete || partial.N != 40 || partial.Passed != 39 {
		t.Fatalf("incomplete population passed: %+v", partial)
	}
	for _, status := range []string{ResponseStatusMissing, ResponseStatusEmpty, ResponseStatusRefused, "insufficient"} {
		t.Run(status, func(t *testing.T) {
			id := reg.Queries[0].QueryID
			text := ""
			if status == "insufficient" {
				text = "INSUFFICIENT Missing flow context."
			}
			rows := compactTestRecord(t, reg, nil, id, 0, "response", status, text)
			rows = compactTestRecord(t, reg, rows, id, 1, "response", ResponseStatusAnswered, "Answer")
			rows = compactTestRecord(t, reg, rows, id, 1, "grade", GradeOutcomePass, "")
			out, err := compactDevDecide(reg, rows)
			if err != nil {
				t.Fatal(err)
			}
			if out.Queries[0].Pass || !out.Queries[0].Complete {
				t.Fatalf("nonanswered not mechanical fail %+v", out.Queries[0])
			}
			graded := compactTestRecord(t, reg, append([]CompactDevSufficiencyRecord(nil), rows...), id, 0, "grade", GradeOutcomeFail, "")
			if _, err := compactDevDecide(reg, graded); err == nil {
				t.Fatal("accepted grade for mechanical fail")
			}
			adjudicated := compactTestRecord(t, reg, rows, id, 2, "response", ResponseStatusAnswered, "Answer")
			if _, err := compactDevDecide(reg, adjudicated); err == nil {
				t.Fatal("adjudicated away a mechanical fail")
			}
		})
	}
}

func TestCompactDevSufficiencyDecision_AdjudicationAndOrder(t *testing.T) {
	reg, _, _ := compactSufficiencyFixture(t)
	id := reg.Queries[0].QueryID
	var rows []CompactDevSufficiencyRecord
	for slot := 0; slot < 2; slot++ {
		rows = compactTestRecord(t, reg, rows, id, slot, "response", ResponseStatusAnswered, "Answer")
		grade := GradeOutcomePass
		if slot == 1 {
			grade = GradeOutcomeFail
		}
		rows = compactTestRecord(t, reg, rows, id, slot, "grade", grade, "")
	}
	out, err := compactDevDecide(reg, rows)
	if err != nil {
		t.Fatal(err)
	}
	if out.Queries[0].Complete {
		t.Fatal("disagreement resolved without adjudication")
	}
	answered := compactTestRecord(t, reg, append([]CompactDevSufficiencyRecord(nil), rows...), id, 2, "response", ResponseStatusAnswered, "Independent answer")
	answered = compactTestRecord(t, reg, answered, id, 2, "grade", GradeOutcomePass, "")
	out, err = compactDevDecide(reg, answered)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Queries[0].Pass || !out.Queries[0].Complete {
		t.Fatal("valid independent adjudication failed")
	}
	insufficient := compactTestRecord(t, reg, rows, id, 2, "response", "insufficient", "INSUFFICIENT missing definition")
	out, err = compactDevDecide(reg, insufficient)
	if err != nil {
		t.Fatal(err)
	}
	if out.Queries[0].Pass || !out.Queries[0].Complete {
		t.Fatal("adjudicator insufficiency was not mechanical fail")
	}
	bad := append([]CompactDevSufficiencyRecord(nil), answered...)
	bad[0].Inputs = append(bad[0].Inputs, "qrels")
	if _, err := compactDevDecide(reg, bad); err == nil {
		t.Fatal("accepted input inventory drift")
	}
	bad = append([]CompactDevSufficiencyRecord(nil), answered...)
	bad[1], bad[0] = bad[0], bad[1]
	if _, err := compactDevDecide(reg, bad); err == nil {
		t.Fatal("accepted grade-before-response ordering")
	}
	// A self-resealed record cannot smuggle a larger invocation input set.
	bad = compactTestRecord(t, reg, nil, id, 0, "response", ResponseStatusAnswered, "Answer")
	bad[0].Inputs = append(bad[0].Inputs, "qrels")
	bad[0].SHA256, _ = ContentAddress(bad[0], func(r *CompactDevSufficiencyRecord) { r.SHA256 = "" })
	if _, err := compactDevDecide(reg, bad); err == nil {
		t.Fatal("accepted self-resealed qrel leakage inventory")
	}
}

func TestCompactDevSufficiencyRun_AppendOnlyAndPromptIntegrity(t *testing.T) {
	reg, raw, real := compactSufficiencyFixture(t)
	dir := t.TempDir()
	if err := WriteCompactDevSufficiencyRun(dir, reg, raw, real); err != nil {
		t.Fatal(err)
	}
	if err := WriteCompactDevSufficiencyRun(dir, reg, raw, real); err != nil {
		t.Fatal("identical seal is not idempotent", err)
	}
	q := reg.Queries[0]
	r := CompactDevSufficiencyRecord{Kind: "response", QueryID: q.QueryID, Slot: 0, Participant: reg.Binding.Primary[0], PromptSHA256: q.PromptSHA256, Inputs: []string{"answer_instructions", "query_text", "preserved_bundle"}, Status: ResponseStatusAnswered, Text: "INSUFFICIENT missing definition"}
	sealed, err := AppendCompactDevSufficiencyRecord(dir, raw, real, r)
	if err != nil {
		t.Fatal(err)
	}
	if sealed.Status != "insufficient" {
		t.Fatal("INSUFFICIENT status not mechanically derived")
	}
	if _, err := AppendCompactDevSufficiencyRecord(dir, raw, real, r); err == nil {
		t.Fatal("identical response retry accepted")
	}
	r.Text = "Improved replacement answer"
	if _, err := AppendCompactDevSufficiencyRecord(dir, raw, real, r); err == nil {
		t.Fatal("replacement response accepted")
	}
	out, err := DecideCompactDevSufficiency(dir, raw, real)
	if err != nil {
		t.Fatal(err)
	}
	if out.Passed != 0 || out.Complete || out.DiagnosticPass {
		t.Fatal("unfinished run passed")
	}
	if err := compactDevWriteOnce(filepath.Join(dir, "prompts", q.QueryID+".txt"), []byte("replacement")); err == nil {
		t.Fatal("prompt overwrite accepted")
	}
	path := filepath.Join(dir, "records", fmt.Sprintf("%06d.json", sealed.Sequence))
	var drift CompactDevSufficiencyRecord
	if err := compactDevReadJSON(path, &drift); err != nil {
		t.Fatal(err)
	}
	drift.Text = "changed"
	if err := WriteBlindEvalJSONWriteOnce("test record", path, drift); err == nil {
		t.Fatal("record overwrite accepted")
	}
	if err := WriteBlindEvalJSONWriteOnce("compact outcome", filepath.Join(dir, "outcome.json"), out); err != nil {
		t.Fatal(err)
	}
	r.Slot = 1
	r.Participant = reg.Binding.Primary[1]
	if _, err := AppendCompactDevSufficiencyRecord(dir, raw, real, r); err == nil {
		t.Fatal("accepted response after outcome seal")
	}
}
