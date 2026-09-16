package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/shape"
	taskcompact "github.com/samibel/graphi/engine/agenttools/taskctx/compact"
)

func TestBuildOracleControlsKeepsNormalCaptureImmutable(t *testing.T) {
	in := oracleFixture(t)
	before := canonicalOracleCandidates(t, in.CurrentCandidates)

	got, err := BuildOracleControls(in)
	if err != nil {
		t.Fatal(err)
	}
	if after := canonicalOracleCandidates(t, in.CurrentCandidates); after != before {
		t.Fatal("oracle mutated normal candidates")
	}
	if !got.CurrentCandidatesOraclePacker.CompleteGrade3Span {
		t.Fatal("oracle packer missed an available grade-3 span")
	}
	if !got.OracleCandidateCurrentSelector.Injected || got.OracleCandidateCurrentSelector.InjectedRows != 1 {
		t.Fatalf("oracle candidate injection = %+v", got.OracleCandidateCurrentSelector)
	}
	if !got.OracleCandidateOraclePacker.CompleteGrade3Span {
		t.Fatal("oracle candidate/oracle packer missed its injected span")
	}
	wantCurrentSHA := SHA256Hex(canonicalOracleCandidateBytes(t, in.CurrentCandidates))
	if got.CurrentCandidatesOraclePacker.CandidateSHA256 != wantCurrentSHA {
		t.Fatalf("current candidate provenance = %q, want frozen bytes %q", got.CurrentCandidatesOraclePacker.CandidateSHA256, wantCurrentSHA)
	}

	seenKinds := map[string]bool{}
	seenNames := map[string]bool{}
	seenProvenance := map[string]bool{}
	for _, bundle := range []OracleBundle{
		got.CurrentCandidatesOraclePacker,
		got.OracleCandidateCurrentSelector,
		got.OracleCandidateOraclePacker,
	} {
		if bundle.QueryID != in.Query.ID || bundle.TokenCount > SavingsCandidateBudget {
			t.Fatalf("invalid oracle bundle identity/budget: %+v", bundle)
		}
		if seenKinds[bundle.ControlKind] || seenNames[bundle.OutputName] || seenProvenance[bundle.CandidateProvenance] || bundle.OutputName == "capture.json" {
			t.Fatalf("oracle control identity is not distinct: %+v", bundle)
		}
		seenKinds[bundle.ControlKind] = true
		seenNames[bundle.OutputName] = true
		seenProvenance[bundle.CandidateProvenance] = true
		if _, err := ValidateCompactCandidateBundleBytes(in.Query.ID, bundle.Payload.Bytes); err != nil {
			t.Fatalf("%s fails normal compact validator: %v", bundle.ControlKind, err)
		}
		if _, err := BuildRaterPrompt(in.Query.ID, in.Query.Text, bundle.Payload); err != nil {
			t.Fatalf("%s fails blind rater payload contract: %v", bundle.ControlKind, err)
		}
	}

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"judgements"`, `"qrels"`, `"grade"`, `"answer_label"`} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("oracle grader artifact leaks %s: %s", forbidden, raw)
		}
	}
}

func TestQualificationOracleArtifactsUseSeparatedBlindPaths(t *testing.T) {
	in := oracleFixture(t)
	controls, err := BuildOracleControls(in)
	if err != nil {
		t.Fatal(err)
	}
	captured := []CapturedCandidateBundle{{QueryID: in.Query.ID, OracleControls: &controls}}
	dir := t.TempDir()
	if err := writeQualificationOracleControls(dir, captured, []Query{in.Query}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{
		OracleControlCurrentCandidatesOraclePacker,
		OracleControlOracleCandidateCurrentSelector,
		OracleControlOracleCandidateOraclePacker,
	} {
		controlDir := filepath.Join(dir, "oracle", in.Query.ID, kind)
		artifact, err := os.ReadFile(filepath.Join(controlDir, "artifact.json"))
		if err != nil {
			t.Fatalf("read %s artifact: %v", kind, err)
		}
		for _, forbidden := range [][]byte{[]byte(`"judgements"`), []byte(`"qrels"`), []byte(`"answer_label"`), []byte(`"complete_grade_3_span"`)} {
			if bytes.Contains(artifact, forbidden) {
				t.Fatalf("%s artifact leaks %s: %s", kind, forbidden, artifact)
			}
		}
		for _, required := range [][]byte{[]byte(`"control_kind": "` + kind + `"`), []byte(`"candidate_provenance"`), []byte(`"candidate_sha256"`), []byte(`"payload_sha256"`), []byte(`"real_token_count"`)} {
			if !bytes.Contains(artifact, required) {
				t.Fatalf("%s artifact missing %s: %s", kind, required, artifact)
			}
		}
		prompt, err := os.ReadFile(filepath.Join(controlDir, "grader-prompt.txt"))
		if err != nil {
			t.Fatalf("read %s grader prompt: %v", kind, err)
		}
		if !bytes.Contains(prompt, []byte("QUESTION:\n"+in.Query.Text)) || bytes.Contains(prompt, []byte("complete_grade_3_span")) {
			t.Fatalf("%s grader prompt is not blind-compatible", kind)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "capture.json")); !os.IsNotExist(err) {
		t.Fatalf("oracle writer used normal capture filename: %v", err)
	}
}

func TestQualificationLateOracleErrorPublishesNothing(t *testing.T) {
	out := filepath.Join(t.TempDir(), "qualification")
	if err := os.Mkdir(out, 0o755); err != nil {
		t.Fatal(err)
	}
	q := oracleFixture(t).Query
	err := publishQualificationAtomically(out, func(stage string) error {
		if err := os.WriteFile(filepath.Join(stage, "normal-capture-complete"), []byte("normal"), 0o644); err != nil {
			return err
		}
		return writeQualificationOracleControls(stage, []CapturedCandidateBundle{{QueryID: q.ID}}, []Query{q})
	})
	if err == nil || !strings.Contains(err.Error(), "oracle") {
		t.Fatalf("late missing oracle controls = %v", err)
	}
	entries, readErr := os.ReadDir(out)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("late oracle error published partial evidence: entries=%v err=%v", entries, readErr)
	}
}

func TestBuildOracleControlsDistinguishesAbsentRelevantCandidate(t *testing.T) {
	in := oracleFixture(t)
	in.CurrentCandidates.Items = in.CurrentCandidates.Items[1:]
	in.CurrentCandidates.Evidence = in.CurrentCandidates.Evidence[1:]

	got, err := BuildOracleControls(in)
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentCandidatesOraclePacker.CompleteGrade3Span {
		t.Fatal("current-candidate packer fabricated an absent relevant candidate")
	}
	if !got.OracleCandidateOraclePacker.CompleteGrade3Span || !got.OracleCandidateOraclePacker.Injected {
		t.Fatal("oracle-candidate packer did not isolate candidate-pool recall")
	}
}

func TestBuildOracleControlsExposesSelectorDrop(t *testing.T) {
	in := oracleFixture(t)
	// The qrel-matching row is present, but it has only a citation and is ranked
	// behind a query-matching snippet large enough to consume the selector's
	// fixed source frontier.
	in.CurrentCandidates.Evidence[0].Snippet = ""
	in.CurrentCandidates.Evidence[0].TextHash = ""
	in.CurrentCandidates.Items[0].Rank = 99
	in.CurrentCandidates.Evidence[1].Snippet = strings.TrimSpace(strings.Repeat("distractor needle ", taskcompact.DefaultSourceBudget/2))
	in.CurrentCandidates.Evidence[1].TextHash = shape.TextHash(in.CurrentCandidates.Evidence[1].Snippet)
	in.Repository.(fstest.MapFS)["distractor.go"] = &fstest.MapFile{Data: []byte(in.CurrentCandidates.Evidence[1].Snippet)}

	selected, err := taskcompact.Build(context.Background(), in.Query.Text, canonicalOracleCandidateBytes(t, in.CurrentCandidates), in.Repository, taskcompact.DefaultSourceBudget)
	if err != nil {
		t.Fatal(err)
	}
	if qualificationCompleteGrade3Span(in.Query, selected.Structured.Sources) {
		t.Fatal("selector-drop fixture unexpectedly retained the judged span")
	}

	got, err := BuildOracleControls(in)
	if err != nil {
		t.Fatal(err)
	}
	if !got.CurrentCandidatesOraclePacker.CompleteGrade3Span {
		t.Fatal("oracle packer did not expose evidence dropped by the current selector")
	}
}

func TestBuildOracleControlsStopsBeforeOverBudgetGrade3Span(t *testing.T) {
	in := oracleFixture(t)
	var huge strings.Builder
	for i := 0; i < 1800; i++ {
		fmt.Fprintf(&huge, "answerIdentifier%d := computeAnswerIdentifier%d\n", i, i)
	}
	in.Repository = fstest.MapFS{
		"answer.go":     {Data: []byte(huge.String())},
		"distractor.go": {Data: []byte("package p\nfunc Needle() {}\n")},
	}
	in.Query.Judgements[0].StartLine = 1
	in.Query.Judgements[0].EndLine = 1800
	in.CurrentCandidates.Evidence[0].Line = 1
	in.CurrentCandidates.Evidence[0].Span = "1-1800"
	in.CurrentCandidates.Evidence[0].Snippet = ""
	in.CurrentCandidates.Evidence[0].TextHash = ""

	got, err := BuildOracleControls(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, bundle := range []OracleBundle{
		got.CurrentCandidatesOraclePacker,
		got.OracleCandidateCurrentSelector,
		got.OracleCandidateOraclePacker,
	} {
		if bundle.TokenCount > SavingsCandidateBudget {
			t.Fatalf("%s emitted %d tokens", bundle.ControlKind, bundle.TokenCount)
		}
		if bundle.CompleteGrade3Span {
			t.Fatalf("%s claimed an over-budget complete span", bundle.ControlKind)
		}
	}
}

func oracleFixture(t *testing.T) OracleInput {
	t.Helper()
	counter, err := LoadPinnedRealPayloadCounter()
	if err != nil {
		t.Fatal(err)
	}
	answer := "package p\nfunc Answer() int {\n\treturn 42\n}\n"
	distractor := "package p\nfunc Needle() {}\n"
	return OracleInput{
		Query: Query{
			ID: "q-oracle", Text: "needle", Split: SplitDev, Stratum: StratumNLBehaviour,
			Judgements: []Judgement{{Path: "answer.go", StartLine: 2, EndLine: 3, Grade: GradeMax}},
		},
		CurrentCandidates: contract.Result{
			Outcome: contract.OutcomeOK,
			Summary: `task_context/2: 2 seed(s) for "needle" — 2 files (task_context/2; retrieval/7; weights abc123; model sha256:0123456789abcdef; 8/1200 snippet tokens; context-definitions/3; strategy semantic_first; degradation: ready)`,
			Items: []contract.Item{
				{RefID: "answer-item", Rank: 2, Reason: "candidate", EvidenceRefIDs: []string{"answer-evidence"}},
				{RefID: "distractor-item", Rank: 1, Reason: "candidate", EvidenceRefIDs: []string{"distractor-evidence"}},
			},
			Evidence: []contract.Evidence{
				{RefID: "answer-evidence", Path: "answer.go", Line: 2, Span: "2-3", Role: "snippet", Snippet: "func Answer() int {\n\treturn 42", TextHash: shape.TextHash("func Answer() int {\n\treturn 42")},
				{RefID: "distractor-evidence", Path: "distractor.go", Line: 2, Span: "2-2", Role: "snippet", Snippet: "func Needle() {}", TextHash: shape.TextHash("func Needle() {}")},
			},
			Confidence: contract.Confidence{Distribution: map[string]float64{"relevant": 1}, Top: "relevant", Method: "fixture"},
			Limits:     contract.Limits{CapApplied: 50, TotalAvailable: 2},
		},
		Repository: fstest.MapFS{
			"answer.go":     {Data: []byte(answer)},
			"distractor.go": {Data: []byte(distractor)},
		},
		RealCounter: counter,
	}
}

func canonicalOracleCandidates(t *testing.T, candidates contract.Result) string {
	t.Helper()
	return string(canonicalOracleCandidateBytes(t, candidates))
}

func canonicalOracleCandidateBytes(t *testing.T, candidates contract.Result) []byte {
	t.Helper()
	raw, err := contract.SerializeStable(&candidates)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
