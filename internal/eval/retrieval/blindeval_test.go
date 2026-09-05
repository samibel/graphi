package retrieval

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Fixture
// ---------------------------------------------------------------------------

var (
	fixtureFreezeAt   = time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC)
	fixturePreRegAt   = time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	fixtureRespondAt  = time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	fixtureGradeAt    = time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC)
	fixtureAdjudicate = time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	fixtureDiscloseAt = time.Date(2026, 9, 5, 13, 0, 0, 0, time.UTC)
)

const (
	fixtureRubricSHA  = "1111111111111111111111111111111111111111111111111111111111111111"
	fixtureBudgetsSHA = "2222222222222222222222222222222222222222222222222222222222222222"
	fixtureTargetsSHA = "3333333333333333333333333333333333333333333333333333333333333333"
	fixtureMethodSHA  = "4444444444444444444444444444444444444444444444444444444444444444"
	fixtureDatasetSHA = "5555555555555555555555555555555555555555555555555555555555555555"
	fixtureCandidate  = "0123456789abcdef0123456789abcdef01234567"
	fixtureVocabSHA   = "223921b76ee99bde995b7ff738513eef100fb51d18c93597a113bcffe865b2a7"
)

// blindEvalSpec describes one query's rater slots for the fixture. AdjStatus
// empty means no adjudication record is produced for that query.
type blindEvalSpec struct {
	queryID   string
	stratum   string
	status    [2]string
	grades    [2]string
	adjStatus string
	adjGrade  string
}

func passPass(id string) blindEvalSpec {
	return blindEvalSpec{queryID: id, stratum: StratumNLBehaviour,
		status: [2]string{ResponseStatusAnswered, ResponseStatusAnswered},
		grades: [2]string{GradeOutcomePass, GradeOutcomePass}}
}

func failFail(id string) blindEvalSpec {
	return blindEvalSpec{queryID: id, stratum: StratumNLBehaviour,
		status: [2]string{ResponseStatusAnswered, ResponseStatusAnswered},
		grades: [2]string{GradeOutcomeFail, GradeOutcomeFail}}
}

func fixturePrecondition(t *testing.T) PreconditionRecord {
	t.Helper()
	rec := PreconditionRecord{
		ContractVersion:            QrelBlindSmokeContractVersion,
		Evaluation:                 QrelBlindSmokeEvaluationName,
		FreezeCommit:               "abcdefabcdefabcdefabcdefabcdefabcdefabcd",
		FreezeTimestamp:            fixtureFreezeAt.Format(time.RFC3339),
		DatasetPath:                "internal/eval/retrieval/testdata/datasets/cobra-v2.json",
		DatasetSHA256:              fixtureDatasetSHA,
		CandidateSHA:               fixtureCandidate,
		CandidateMethod:            SavingsCandidateMethod,
		CandidateTokenBudget:       SavingsCandidateBudget,
		ComparatorVersion:          BlindEvalComparatorVersion,
		TokenizerID:                "tiktoken:cl100k_base:ordinary",
		TokenizerVocabularySHA256:  fixtureVocabSHA,
		MeasurementContractVersion: MeasurementContractVersion,
		ClaimWordingSHA256:         SHA256Hex([]byte(FrozenClaimWording())),
		Inputs: []FrozenInput{
			{Role: "budgets", Path: "docs/eval/retrieval-budgets.json", SHA256: fixtureBudgetsSHA},
			{Role: "targets", Path: "docs/eval/retrieval-targets.json", SHA256: fixtureTargetsSHA},
			{Role: "grading_rubric", Path: "docs/eval/retrieval/runs/x/rubric.md", SHA256: fixtureRubricSHA},
			{Role: "methodology", Path: "docs/eval/retrieval/methodology.md", SHA256: fixtureMethodSHA},
		},
	}
	sealed, err := SealPreconditionRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func fixtureParticipants() ([]Participant, Participant, Participant) {
	primaries := []Participant{
		{ID: "rater-a", Role: RaterRolePrimary, Provider: "fixture", Model: "fixture-model-a",
			IndependenceBasis: "fixture rater with no part in this track", ParticipatedInTrack: false},
		{ID: "rater-b", Role: RaterRolePrimary, Provider: "fixture", Model: "fixture-model-b",
			IndependenceBasis: "fixture rater", ParticipatedInTrack: true},
	}
	grader := Participant{ID: "grader-a", Role: "grader", Provider: "fixture", Model: "fixture-model-g",
		IndependenceBasis: "fixture grader", ParticipatedInTrack: false}
	adjudicator := Participant{ID: "adjudicator-a", Role: RaterRoleAdjudicator, Provider: "fixture", Model: "fixture-model-x",
		IndependenceBasis: "fixture adjudicator", ParticipatedInTrack: false}
	return primaries, grader, adjudicator
}

func fixtureQueryText(id string) string { return "what does " + id + " do?" }
func fixtureBundleBytes(id string) []byte {
	return []byte(candidateJSONRPCPrefix + `1,"result":{"content":[{"type":"text","text":"bundle for ` + id + `"}],"isError":false}}` + "\n")
}

func buildBlindEvalArtifacts(t *testing.T, specs []blindEvalSpec) EvaluationArtifacts {
	t.Helper()
	precondition := fixturePrecondition(t)
	primaries, grader, adjudicator := fixtureParticipants()

	derivation, err := DerivePassCount(len(specs), precondition.DatasetSHA256, "fixture population")
	if err != nil {
		t.Fatalf("DerivePassCount(%d): %v", len(specs), err)
	}
	pre := PreRegistration{
		ContractVersion:    QrelBlindSmokeContractVersion,
		Evaluation:         QrelBlindSmokeEvaluationName,
		PreconditionSHA256: precondition.SHA256,
		PreconditionCommit: "fedcbafedcbafedcbafedcbafedcbafedcbafedc",
		RecordedAt:         fixturePreRegAt.Format(time.RFC3339),
		Derivation:         derivation,
		PrimaryRaters:      primaries,
		Grader:             grader,
		Adjudicator:        adjudicator,
	}
	for _, spec := range specs {
		body := fixtureBundleBytes(spec.queryID)
		pre.Queries = append(pre.Queries, PreRegisteredQuery{
			QueryID:         spec.queryID,
			FamilyID:        "family-" + spec.queryID,
			Stratum:         spec.stratum,
			QueryTextSHA256: SHA256Hex([]byte(fixtureQueryText(spec.queryID))),
			BundleSHA256:    SHA256Hex(body),
			BundleByteCount: len(body),
			BundleBoundary:  PayloadBoundaryCandidate,
			BundleTokenCounts: []PayloadTokenCount{
				{TokenizerID: TokenizerID, Tokens: len(strings.Fields(string(body)))},
				{TokenizerID: "tiktoken:cl100k_base:ordinary", VocabularySHA256: fixtureVocabSHA, Tokens: 42},
			},
		})
	}
	pre, err = SealPreRegistration(pre)
	if err != nil {
		t.Fatal(err)
	}

	artifacts := EvaluationArtifacts{Precondition: precondition, PreRegistration: pre}
	for i, spec := range specs {
		prq := pre.Queries[i]
		for slot, participant := range primaries {
			response := newFixtureResponse(t, pre, prq, participant, RaterRolePrimary, spec.status[slot], fixtureRespondAt)
			artifacts.Responses = append(artifacts.Responses, response)
			if spec.status[slot] == ResponseStatusAnswered {
				artifacts.Grades = append(artifacts.Grades, newFixtureGrade(t, grader, response, spec.grades[slot], fixtureGradeAt))
			}
		}
		if spec.adjStatus == "" {
			continue
		}
		adjResponse := newFixtureResponse(t, pre, prq, adjudicator, RaterRoleAdjudicator, spec.adjStatus, fixtureAdjudicate)
		disclosed := []string{}
		for _, r := range artifacts.Responses {
			if r.QueryID == spec.queryID {
				disclosed = append(disclosed, r.SHA256)
			}
		}
		artifacts.Adjudications = append(artifacts.Adjudications, Adjudication{
			QueryID:  spec.queryID,
			Response: adjResponse,
			Disclosure: DisclosureRecord{
				QueryID:                   spec.queryID,
				AdjudicatorResponseSHA256: adjResponse.SHA256,
				DisclosedArtifactSHA256:   disclosed,
				DisclosedAt:               fixtureDiscloseAt.Format(time.RFC3339),
			},
		})
		if spec.adjStatus == ResponseStatusAnswered {
			artifacts.Grades = append(artifacts.Grades, newFixtureGrade(t, grader, adjResponse, spec.adjGrade, fixtureDiscloseAt))
		}
	}
	return artifacts
}

func newFixtureResponse(t *testing.T, pre PreRegistration, q PreRegisteredQuery, who Participant, role, status string, at time.Time) RaterResponse {
	t.Helper()
	text := ""
	switch status {
	case ResponseStatusAnswered:
		text = "the answer is in " + q.QueryID
	case ResponseStatusRefused:
		text = "I will not answer this."
	case ResponseStatusEmpty:
		text = ""
	}
	response := RaterResponse{
		ContractVersion:       QrelBlindSmokeContractVersion,
		Evaluation:            QrelBlindSmokeEvaluationName,
		Role:                  role,
		QueryID:               q.QueryID,
		RaterID:               who.ID,
		Provider:              who.Provider,
		Model:                 who.Model,
		PreRegistrationSHA256: pre.SHA256,
		QueryTextSHA256:       q.QueryTextSHA256,
		BundleSHA256:          q.BundleSHA256,
		PromptSHA256:          SHA256Hex([]byte("prompt:" + q.QueryID + ":" + who.ID)),
		Inputs:                []string{"answer_instructions", "query_text", "preserved_bundle"},
		Status:                status,
		Text:                  text,
		RespondedAt:           at.Format(time.RFC3339),
	}
	sealed, err := SealRaterResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func newFixtureGrade(t *testing.T, grader Participant, response RaterResponse, outcome string, at time.Time) Grade {
	t.Helper()
	grade := Grade{
		ContractVersion: QrelBlindSmokeContractVersion,
		Evaluation:      QrelBlindSmokeEvaluationName,
		QueryID:         response.QueryID,
		ResponseSHA256:  response.SHA256,
		GraderID:        grader.ID,
		Provider:        grader.Provider,
		Model:           grader.Model,
		RubricSHA256:    fixtureRubricSHA,
		Outcome:         outcome,
		Rationale:       "fixture rationale",
		GradedAt:        at.Format(time.RFC3339),
	}
	sealed, err := SealGrade(grade)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

// matchingComparison is the end-of-run comparison for an unchanged tree.
func matchingComparison(t *testing.T, rec PreconditionRecord) HashComparisonResult {
	t.Helper()
	known := map[string]string{rec.DatasetPath: rec.DatasetSHA256}
	for _, input := range rec.Inputs {
		known[input.Path] = input.SHA256
	}
	comparison, err := CompareFrozenInputs(rec, func(path string) (string, error) {
		sha, ok := known[path]
		if !ok {
			return "", fmt.Errorf("no such fixture input %q", path)
		}
		return sha, nil
	}, fixtureDiscloseAt)
	if err != nil {
		t.Fatal(err)
	}
	return comparison
}

// specsFor builds n queries of which the first passCount pass.
func specsFor(n, passCount int) []blindEvalSpec {
	specs := make([]blindEvalSpec, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("hq-%02d", i+1)
		if i < passCount {
			specs = append(specs, passPass(id))
		} else {
			specs = append(specs, failFail(id))
		}
	}
	return specs
}

// ---------------------------------------------------------------------------
// Positive controls
// ---------------------------------------------------------------------------

// Positive control: a complete, correctly ordered N=20 evaluation with 19
// passes clears the pre-registered k=19 and records RELEASE: YES.
func TestQrelBlindSmoke_CompleteEvaluationAtKReleasesYes(t *testing.T) {
	artifacts := buildBlindEvalArtifacts(t, specsFor(20, 19))
	outcome, err := EvaluateQrelBlindSmoke(artifacts, matchingComparison(t, artifacts.Precondition))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.N != 20 || outcome.K != 19 || outcome.PassCount != 19 {
		t.Fatalf("N=%d k=%d passes=%d, want 20/19/19", outcome.N, outcome.K, outcome.PassCount)
	}
	if outcome.Release != ReleaseYes {
		t.Errorf("release = %s (%v)", outcome.Release, outcome.Reasons)
	}
	if outcome.Interval.LowerBound != "0.751267237227" || !outcome.Interval.MeetsFloor {
		t.Errorf("observed interval = %+v", outcome.Interval)
	}
	if outcome.AdjudicationCount != 0 || outcome.DisagreementCount != 0 {
		t.Errorf("adjudications=%d disagreements=%d, want 0/0", outcome.AdjudicationCount, outcome.DisagreementCount)
	}
	if len(outcome.PerStratum) != 1 || outcome.PerStratum[0].Rendered != "19/20" || outcome.PerStratum[0].Resolution != "1/20" {
		t.Errorf("per-stratum = %+v", outcome.PerStratum)
	}
	if err := ValidateEvaluationOutcome(outcome, artifacts); err != nil {
		t.Errorf("a freshly computed outcome must revalidate: %v", err)
	}
}

// Positive control: a disagreement is adjudicated and the majority of the three
// graded outcomes decides the query.
func TestQrelBlindSmoke_AdjudicatedMajorityDecidesTheQuery(t *testing.T) {
	for _, tc := range []struct {
		name        string
		adjGrade    string
		wantOutcome string
	}{
		{"adjudicator passes", GradeOutcomePass, GradeOutcomePass},
		{"adjudicator fails", GradeOutcomeFail, GradeOutcomeFail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			specs := specsFor(20, 19)
			specs[19] = blindEvalSpec{queryID: specs[19].queryID, stratum: StratumNLBehaviour,
				status:    [2]string{ResponseStatusAnswered, ResponseStatusAnswered},
				grades:    [2]string{GradeOutcomePass, GradeOutcomeFail},
				adjStatus: ResponseStatusAnswered, adjGrade: tc.adjGrade}
			artifacts := buildBlindEvalArtifacts(t, specs)
			outcome, err := EvaluateQrelBlindSmoke(artifacts, matchingComparison(t, artifacts.Precondition))
			if err != nil {
				t.Fatal(err)
			}
			if outcome.DisagreementCount != 1 || outcome.AdjudicationCount != 1 {
				t.Errorf("disagreements=%d adjudications=%d, want 1/1", outcome.DisagreementCount, outcome.AdjudicationCount)
			}
			last := outcome.Queries[19]
			if last.Outcome != tc.wantOutcome {
				t.Errorf("query outcome = %s, want %s (%s)", last.Outcome, tc.wantOutcome, last.Reason)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// AC-1 refusals
// ---------------------------------------------------------------------------

// Refusal: the evaluation refuses to start when ANY named precondition field is
// absent. Each row below removes one and asserts the refusal.
func TestQrelBlindSmoke_PreconditionRefusesToStartOnAnyAbsentField(t *testing.T) {
	base := fixturePrecondition(t)
	mutations := map[string]func(*PreconditionRecord){
		"freeze_commit":                func(r *PreconditionRecord) { r.FreezeCommit = "" },
		"freeze_timestamp":             func(r *PreconditionRecord) { r.FreezeTimestamp = "" },
		"dataset_path":                 func(r *PreconditionRecord) { r.DatasetPath = "" },
		"dataset_sha256":               func(r *PreconditionRecord) { r.DatasetSHA256 = "" },
		"candidate_sha":                func(r *PreconditionRecord) { r.CandidateSHA = "" },
		"candidate_budget":             func(r *PreconditionRecord) { r.CandidateTokenBudget = 900 },
		"comparator_version":           func(r *PreconditionRecord) { r.ComparatorVersion = "" },
		"tokenizer_id":                 func(r *PreconditionRecord) { r.TokenizerID = "" },
		"tokenizer_vocabulary_sha256":  func(r *PreconditionRecord) { r.TokenizerVocabularySHA256 = "" },
		"measurement_contract_version": func(r *PreconditionRecord) { r.MeasurementContractVersion = "" },
		"claim_wording_sha256":         func(r *PreconditionRecord) { r.ClaimWordingSHA256 = "" },
		"budgets input":                func(r *PreconditionRecord) { r.Inputs = dropInput(r.Inputs, "budgets") },
		"targets input":                func(r *PreconditionRecord) { r.Inputs = dropInput(r.Inputs, "targets") },
		"grading_rubric input":         func(r *PreconditionRecord) { r.Inputs = dropInput(r.Inputs, "grading_rubric") },
		"methodology input":            func(r *PreconditionRecord) { r.Inputs = dropInput(r.Inputs, "methodology") },
		"rubric hash":                  func(r *PreconditionRecord) { r.Inputs[2].SHA256 = "" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			broken := base
			broken.Inputs = append([]FrozenInput(nil), base.Inputs...)
			mutate(&broken)
			// Re-seal so the record is internally consistent: the refusal must
			// come from the ABSENT FIELD, not from a stale self-digest.
			sealed, err := SealPreconditionRecord(broken)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidatePreconditionRecord(sealed); err == nil {
				t.Fatalf("a precondition record without %s was accepted", name)
			}
		})
	}
	// Positive control: the untouched record is accepted, so the rows above
	// fail for the reason stated and not because the fixture is broken.
	if err := ValidatePreconditionRecord(base); err != nil {
		t.Fatalf("the complete fixture precondition record must be accepted: %v", err)
	}
}

func dropInput(inputs []FrozenInput, role string) []FrozenInput {
	out := make([]FrozenInput, 0, len(inputs))
	for _, input := range inputs {
		if input.Role != role {
			out = append(out, input)
		}
	}
	return out
}

// Refusal: a hand-edited precondition record no longer matches its own content
// address.
func TestQrelBlindSmoke_TamperedPreconditionRecordFailsItsOwnAddress(t *testing.T) {
	rec := fixturePrecondition(t)
	rec.CandidateSHA = "ffffffffffffffffffffffffffffffffffffffff"
	if err := ValidatePreconditionRecord(rec); err == nil {
		t.Fatal("a precondition record edited after sealing was accepted")
	}
}

// Refusal: AC-1's end-of-run comparison. Mutating one frozen input's bytes
// fails the run rather than lowering a number.
func TestQrelBlindSmoke_EndOfRunHashDriftFailsTheRun(t *testing.T) {
	artifacts := buildBlindEvalArtifacts(t, specsFor(20, 19))
	rec := artifacts.Precondition
	known := map[string]string{rec.DatasetPath: rec.DatasetSHA256}
	for _, input := range rec.Inputs {
		known[input.Path] = input.SHA256
	}
	// The targets file drifts after the evaluation ran.
	known["docs/eval/retrieval-targets.json"] = "9999999999999999999999999999999999999999999999999999999999999999"
	comparison, err := CompareFrozenInputs(rec, func(path string) (string, error) { return known[path], nil }, fixtureDiscloseAt)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.AllMatch {
		t.Fatal("the comparison reported all_match over a mutated input")
	}
	outcome, err := EvaluateQrelBlindSmoke(artifacts, comparison)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Release != ReleaseNo {
		t.Fatalf("release = %s over a drifted input, want NO", outcome.Release)
	}
	if outcome.PassCount < outcome.K {
		t.Fatal("this case must fail for the drift reason alone; the pass count must still clear k")
	}
}

// Refusal: an input that cannot be read is a MISMATCH, not a skipped row. This
// is the SW-279 lesson made executable — a check must never report green over a
// population it could not evaluate.
func TestQrelBlindSmoke_UnreadableFrozenInputIsAMismatchNotASkip(t *testing.T) {
	rec := fixturePrecondition(t)
	comparison, err := CompareFrozenInputs(rec, func(path string) (string, error) {
		if strings.HasSuffix(path, "methodology.md") {
			return "", fmt.Errorf("permission denied")
		}
		if path == rec.DatasetPath {
			return rec.DatasetSHA256, nil
		}
		for _, input := range rec.Inputs {
			if input.Path == path {
				return input.SHA256, nil
			}
		}
		return "", fmt.Errorf("unknown")
	}, fixtureDiscloseAt)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.AllMatch {
		t.Fatal("an unreadable input was reported as matching")
	}
	if len(comparison.Comparisons) != len(rec.Inputs)+1 {
		t.Fatalf("%d comparisons for %d frozen inputs; an unevaluable row must still appear", len(comparison.Comparisons), len(rec.Inputs)+1)
	}
	found := false
	for _, c := range comparison.Comparisons {
		if c.Role == "methodology" {
			found = true
			if c.Matches || c.Error == "" {
				t.Errorf("methodology comparison = %+v, want a mismatch carrying its error", c)
			}
		}
	}
	if !found {
		t.Error("the unreadable input is absent from the comparison")
	}
}

// ---------------------------------------------------------------------------
// AC-3 / AC-4 refusals on the recorded derivation
// ---------------------------------------------------------------------------

// Refusal: k is derived, never written. A hand-edited k does not survive.
func TestQrelBlindSmoke_HandEditedKIsRefused(t *testing.T) {
	derivation, err := DerivePassCount(64, fixtureDatasetSHA, "sealed holdout")
	if err != nil {
		t.Fatal(err)
	}
	if derivation.K != 56 {
		t.Fatalf("k = %d for N=64, want 56", derivation.K)
	}
	if err := ValidatePassCountDerivation(derivation); err != nil {
		t.Fatalf("the derived record must validate: %v", err)
	}
	for _, k := range []int{55, 50, 64, 0} {
		broken := derivation
		broken.K = k
		if err := ValidatePassCountDerivation(broken); err == nil {
			t.Errorf("a derivation with a hand-written k=%d for N=64 was accepted", k)
		}
	}
	// And an interval swapped for a friendlier one is refused too.
	broken := derivation
	friendlier, err := ClopperPearsonInterval(64, 64)
	if err != nil {
		t.Fatal(err)
	}
	broken.KInterval = friendlier
	if err := ValidatePassCountDerivation(broken); err == nil {
		t.Error("a derivation carrying an interval that does not recompute was accepted")
	}
}

// Refusal: the two readings of "answerable" must agree on the holdout, because
// N would otherwise depend on which one was used.
func TestQrelBlindSmoke_AmbiguousAnswerabilityIsRefused(t *testing.T) {
	ds := &Dataset{
		SchemaVersion: SchemaVersion, ID: "fixture", Repo: "fixture", Language: "go",
		EvidenceClass: EvidenceClassAgentHumanReviewed,
		Queries: []Query{
			{ID: "h1", Stratum: StratumNLBehaviour, Language: "en", Split: SplitHoldout,
				Judgements: []Judgement{{Grade: GradeMax}}},
			// Not no_hit, but no grade-3 span: the two readings disagree.
			{ID: "h2", Stratum: StratumAmbiguous, Language: "en", Split: SplitHoldout,
				Judgements: []Judgement{{Grade: 2}}},
		},
	}
	_, err := AnswerableHoldout(ds)
	if err == nil {
		t.Fatal("a holdout with an ambiguous answerability reading was accepted")
	}
	if !strings.Contains(err.Error(), "h2") {
		t.Errorf("the refusal must name the offending query: %v", err)
	}
	// Positive control: remove the ambiguity and the population resolves.
	ds.Queries = ds.Queries[:1]
	population, err := AnswerableHoldout(ds)
	if err != nil || len(population) != 1 {
		t.Fatalf("AnswerableHoldout = %d, %v", len(population), err)
	}
}

// ---------------------------------------------------------------------------
// AC-5 precedence refusals
// ---------------------------------------------------------------------------

func TestQrelBlindSmoke_PrecedenceRefusals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*EvaluationArtifacts)
		wantSub string
	}{
		{
			name: "a response that does not name the pre-registration",
			mutate: func(a *EvaluationArtifacts) {
				a.Responses[0].PreRegistrationSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
				resealResponse(a, 0)
			},
			wantSub: "names pre-registration",
		},
		{
			name: "a response stamped before the pre-registration",
			mutate: func(a *EvaluationArtifacts) {
				a.Responses[0].RespondedAt = fixturePreRegAt.Add(-time.Hour).Format(time.RFC3339)
				resealResponse(a, 0)
			},
			wantSub: "predates the pre-registration",
		},
		{
			name: "a response missing from the record entirely",
			mutate: func(a *EvaluationArtifacts) {
				a.Responses = append(a.Responses[:0], a.Responses[1:]...)
			},
			wantSub: "has no primary response record",
		},
		{
			name: "a response answered from a different bundle",
			mutate: func(a *EvaluationArtifacts) {
				a.Responses[0].BundleSHA256 = SHA256Hex([]byte("some other bundle"))
				resealResponse(a, 0)
			},
			wantSub: "not the pre-registered",
		},
		{
			name: "a response answered from a different query text",
			mutate: func(a *EvaluationArtifacts) {
				a.Responses[0].QueryTextSHA256 = SHA256Hex([]byte("a different question"))
				resealResponse(a, 0)
			},
			wantSub: "not the pre-registered",
		},
		{
			name: "a response for a query outside the pre-registered population",
			mutate: func(a *EvaluationArtifacts) {
				a.Responses[0].QueryID = "hq-99"
				resealResponse(a, 0)
			},
			wantSub: "absent from the pre-registered population",
		},
		{
			name: "a response edited after it was content-addressed",
			mutate: func(a *EvaluationArtifacts) {
				a.Responses[0].Text = "a better answer, written later"
			},
			wantSub: "does not match its own content address",
		},
		{
			name: "a rater that was shown the repository",
			mutate: func(a *EvaluationArtifacts) {
				a.Responses[0].Inputs = append(a.Responses[0].Inputs, "repository_checkout")
				resealResponse(a, 0)
			},
			wantSub: "outside the permitted set",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			artifacts := buildBlindEvalArtifacts(t, specsFor(20, 19))
			tc.mutate(&artifacts)
			err := CheckPrecedence(artifacts)
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("refusal %q does not mention %q", err.Error(), tc.wantSub)
			}
		})
	}
	// Positive control.
	artifacts := buildBlindEvalArtifacts(t, specsFor(20, 19))
	if err := CheckPrecedence(artifacts); err != nil {
		t.Fatalf("the untouched fixture must pass the precedence check: %v", err)
	}
}

func resealResponse(a *EvaluationArtifacts, i int) {
	sealed, err := SealRaterResponse(a.Responses[i])
	if err != nil {
		panic(err)
	}
	a.Responses[i] = sealed
	// The grade that referenced the old address is re-pointed, so the test
	// isolates the precedence failure rather than tripping the grade binding.
	for j := range a.Grades {
		if a.Grades[j].QueryID == sealed.QueryID && a.Grades[j].GraderID != "" {
			// leave grades alone; CheckPrecedence does not read them
			_ = j
		}
	}
}

// ---------------------------------------------------------------------------
// AC-7 grade-binding refusals
// ---------------------------------------------------------------------------

func TestQrelBlindSmoke_GradeBindingRefusals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*EvaluationArtifacts)
		wantSub string
	}{
		{
			name: "a grade whose response hash resolves to nothing",
			mutate: func(a *EvaluationArtifacts) {
				a.Grades[0].ResponseSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
				resealGrade(a, 0)
			},
			wantSub: "resolves to no recorded response",
		},
		{
			name: "a grade stamped before the response it graded",
			mutate: func(a *EvaluationArtifacts) {
				a.Grades[0].GradedAt = fixtureRespondAt.Add(-time.Hour).Format(time.RFC3339)
				resealGrade(a, 0)
			},
			wantSub: "predates the response it grades",
		},
		{
			name: "a grade against a rubric that is not the frozen one",
			mutate: func(a *EvaluationArtifacts) {
				a.Grades[0].RubricSHA256 = "8888888888888888888888888888888888888888888888888888888888888888"
				resealGrade(a, 0)
			},
			wantSub: "not the frozen",
		},
		{
			name: "a grade edited after it was content-addressed",
			mutate: func(a *EvaluationArtifacts) {
				a.Grades[0].Outcome = GradeOutcomeFail
				a.Grades[0].Rationale = "reconsidered afterwards"
			},
			wantSub: "does not match its own content address",
		},
		{
			name: "the same response graded twice",
			mutate: func(a *EvaluationArtifacts) {
				second := a.Grades[0]
				second.Rationale = "a second opinion"
				second.Outcome = GradeOutcomePass
				sealed, err := SealGrade(second)
				if err != nil {
					panic(err)
				}
				a.Grades = append(a.Grades, sealed)
			},
			wantSub: "graded twice",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			artifacts := buildBlindEvalArtifacts(t, specsFor(20, 19))
			tc.mutate(&artifacts)
			err := CheckGradeBinding(artifacts)
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("refusal %q does not mention %q", err.Error(), tc.wantSub)
			}
		})
	}
	artifacts := buildBlindEvalArtifacts(t, specsFor(20, 19))
	if err := CheckGradeBinding(artifacts); err != nil {
		t.Fatalf("the untouched fixture must pass the grade-binding check: %v", err)
	}
}

func resealGrade(a *EvaluationArtifacts, i int) {
	sealed, err := SealGrade(a.Grades[i])
	if err != nil {
		panic(err)
	}
	a.Grades[i] = sealed
}

// Refusal: a missing, empty or refused response cannot be graded away. Grading
// it at all is an error, because its outcome is mechanical.
func TestQrelBlindSmoke_ANonAnsweredResponseCannotBeGraded(t *testing.T) {
	specs := specsFor(20, 19)
	specs[19] = blindEvalSpec{queryID: specs[19].queryID, stratum: StratumNLBehaviour,
		status: [2]string{ResponseStatusRefused, ResponseStatusAnswered},
		grades: [2]string{"", GradeOutcomeFail}}
	artifacts := buildBlindEvalArtifacts(t, specs)
	var refused RaterResponse
	for _, r := range artifacts.Responses {
		if r.Status == ResponseStatusRefused {
			refused = r
		}
	}
	grader := artifacts.PreRegistration.Grader
	artifacts.Grades = append(artifacts.Grades, newFixtureGrade(t, grader, refused, GradeOutcomePass, fixtureGradeAt))
	err := CheckGradeBinding(artifacts)
	if err == nil {
		t.Fatal("a refused response was graded")
	}
	if !strings.Contains(err.Error(), "is not graded away") {
		t.Errorf("refusal %q does not explain the rule", err.Error())
	}
}

// ---------------------------------------------------------------------------
// AC-8 adjudication refusals
// ---------------------------------------------------------------------------

func TestQrelBlindSmoke_AdjudicationOrderRefusals(t *testing.T) {
	build := func(t *testing.T) EvaluationArtifacts {
		specs := specsFor(20, 19)
		specs[19] = blindEvalSpec{queryID: specs[19].queryID, stratum: StratumNLBehaviour,
			status:    [2]string{ResponseStatusAnswered, ResponseStatusAnswered},
			grades:    [2]string{GradeOutcomePass, GradeOutcomeFail},
			adjStatus: ResponseStatusAnswered, adjGrade: GradeOutcomePass}
		return buildBlindEvalArtifacts(t, specs)
	}
	for _, tc := range []struct {
		name    string
		mutate  func(*EvaluationArtifacts)
		wantSub string
	}{
		{
			name: "a disclosure that does not name the frozen adjudicator response",
			mutate: func(a *EvaluationArtifacts) {
				a.Adjudications[0].Disclosure.AdjudicatorResponseSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
			},
			wantSub: "does not prove it followed the response",
		},
		{
			name: "a disclosure stamped before the adjudicator answered",
			mutate: func(a *EvaluationArtifacts) {
				a.Adjudications[0].Disclosure.DisclosedAt = fixtureAdjudicate.Add(-time.Hour).Format(time.RFC3339)
			},
			wantSub: "predates the adjudicator response",
		},
		{
			name: "a disclosure that records nothing",
			mutate: func(a *EvaluationArtifacts) {
				a.Adjudications[0].Disclosure.DisclosedArtifactSHA256 = nil
			},
			wantSub: "discloses nothing",
		},
		{
			name: "a disclosure naming an artifact that does not resolve",
			mutate: func(a *EvaluationArtifacts) {
				a.Adjudications[0].Disclosure.DisclosedArtifactSHA256 = []string{"7777777777777777777777777777777777777777777777777777777777777777"}
			},
			wantSub: "resolves to no primary response or grade",
		},
		{
			name: "an adjudication by somebody other than the pre-registered adjudicator",
			mutate: func(a *EvaluationArtifacts) {
				a.Adjudications[0].Response.RaterID = "rater-a"
				sealed, err := SealRaterResponse(a.Adjudications[0].Response)
				if err != nil {
					panic(err)
				}
				a.Adjudications[0].Response = sealed
				a.Adjudications[0].Disclosure.AdjudicatorResponseSHA256 = sealed.SHA256
			},
			wantSub: "not the pre-registered adjudicator",
		},
		{
			name: "the same query adjudicated twice",
			mutate: func(a *EvaluationArtifacts) {
				a.Adjudications = append(a.Adjudications, a.Adjudications[0])
			},
			wantSub: "adjudicated twice",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			artifacts := build(t)
			tc.mutate(&artifacts)
			err := CheckAdjudicationOrder(artifacts)
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("refusal %q does not mention %q", err.Error(), tc.wantSub)
			}
		})
	}
	if err := CheckAdjudicationOrder(build(t)); err != nil {
		t.Fatalf("the untouched adjudicated fixture must pass: %v", err)
	}
}

// Refusal: adjudication happens ONLY on a disagreement.
func TestQrelBlindSmoke_AdjudicationOnlyOnDisagreement(t *testing.T) {
	for _, tc := range []struct {
		name    string
		grades  [2]string
		wantSub string
	}{
		{"both primaries passed", [2]string{GradeOutcomePass, GradeOutcomePass}, "adjudication occurs only on a disagreement"},
		{"both primaries failed", [2]string{GradeOutcomeFail, GradeOutcomeFail}, "two primary failures are a query failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			specs := specsFor(20, 19)
			specs[19] = blindEvalSpec{queryID: specs[19].queryID, stratum: StratumNLBehaviour,
				status:    [2]string{ResponseStatusAnswered, ResponseStatusAnswered},
				grades:    tc.grades,
				adjStatus: ResponseStatusAnswered, adjGrade: GradeOutcomePass}
			artifacts := buildBlindEvalArtifacts(t, specs)
			_, err := EvaluateQrelBlindSmoke(artifacts, matchingComparison(t, artifacts.Precondition))
			if err == nil {
				t.Fatalf("an adjudication over %s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("refusal %q does not mention %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// Refusal: an unadjudicated disagreement is not resolved by picking a side.
func TestQrelBlindSmoke_UnadjudicatedDisagreementIsRefused(t *testing.T) {
	specs := specsFor(20, 19)
	specs[19] = blindEvalSpec{queryID: specs[19].queryID, stratum: StratumNLBehaviour,
		status: [2]string{ResponseStatusAnswered, ResponseStatusAnswered},
		grades: [2]string{GradeOutcomePass, GradeOutcomeFail}}
	artifacts := buildBlindEvalArtifacts(t, specs)
	_, err := EvaluateQrelBlindSmoke(artifacts, matchingComparison(t, artifacts.Precondition))
	if err == nil {
		t.Fatal("a disagreement with no adjudication was decided anyway")
	}
	if !strings.Contains(err.Error(), "not resolved by choosing one side") {
		t.Errorf("refusal %q does not explain the rule", err.Error())
	}
}

// ---------------------------------------------------------------------------
// AC-9
// ---------------------------------------------------------------------------

// AC-9: each of the three non-answered cases is a failure for that rater AND a
// failure for the query, and none of them is adjudicated away.
func TestQrelBlindSmoke_MissingEmptyRefusedResponsesFailTheQuery(t *testing.T) {
	for _, status := range []string{ResponseStatusMissing, ResponseStatusEmpty, ResponseStatusRefused} {
		t.Run(status, func(t *testing.T) {
			specs := specsFor(20, 20)
			specs[19] = blindEvalSpec{queryID: specs[19].queryID, stratum: StratumNLBehaviour,
				status: [2]string{status, ResponseStatusAnswered},
				grades: [2]string{"", GradeOutcomePass}}
			artifacts := buildBlindEvalArtifacts(t, specs)
			outcome, err := EvaluateQrelBlindSmoke(artifacts, matchingComparison(t, artifacts.Precondition))
			if err != nil {
				t.Fatal(err)
			}
			last := outcome.Queries[19]
			if last.Outcome != GradeOutcomeFail {
				t.Errorf("query outcome = %s, want fail", last.Outcome)
			}
			if last.Adjudicated {
				t.Error("a query with a non-answered primary response was adjudicated")
			}
			if last.Primary[0].Outcome != GradeOutcomeFail || !last.Primary[0].Mechanical {
				t.Errorf("the %s rater outcome = %+v, want a mechanical fail", status, last.Primary[0])
			}
			// The other primary's PASS does not rescue the query.
			if last.Primary[1].Outcome != GradeOutcomePass {
				t.Errorf("the second rater outcome = %+v", last.Primary[1])
			}
			if outcome.PassCount != 19 || outcome.NonAnsweredCount != 1 {
				t.Errorf("passes=%d non_answered=%d, want 19/1", outcome.PassCount, outcome.NonAnsweredCount)
			}
			if outcome.Release != ReleaseYes {
				// 19/20 still clears k=19; the point of this row is that the
				// query itself failed, not that the run failed.
				t.Errorf("release = %s (%v)", outcome.Release, outcome.Reasons)
			}

			// And adjudicating it is refused outright.
			specs[19].adjStatus = ResponseStatusAnswered
			specs[19].adjGrade = GradeOutcomePass
			adjudicated := buildBlindEvalArtifacts(t, specs)
			if _, err := EvaluateQrelBlindSmoke(adjudicated, matchingComparison(t, adjudicated.Precondition)); err == nil {
				t.Fatalf("a %s response was adjudicated away", status)
			}
		})
	}
}

// A response marked answered but carrying no text is a contradiction: an empty
// response has its own status.
func TestQrelBlindSmoke_AnsweredWithNoTextIsRefused(t *testing.T) {
	artifacts := buildBlindEvalArtifacts(t, specsFor(20, 19))
	artifacts.Responses[0].Text = "   "
	sealed, err := SealRaterResponse(artifacts.Responses[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRaterResponse(sealed); err == nil {
		t.Fatal("an answered response with no text was accepted")
	}
}

// ---------------------------------------------------------------------------
// AC-10
// ---------------------------------------------------------------------------

// AC-10: a pass count below k yields RELEASE: NO, and there is no field,
// argument or mode that changes that.
func TestQrelBlindSmoke_PassCountBelowKIsReleaseNo(t *testing.T) {
	artifacts := buildBlindEvalArtifacts(t, specsFor(20, 18))
	outcome, err := EvaluateQrelBlindSmoke(artifacts, matchingComparison(t, artifacts.Precondition))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.PassCount != 18 || outcome.K != 19 {
		t.Fatalf("passes=%d k=%d", outcome.PassCount, outcome.K)
	}
	if outcome.Release != ReleaseNo {
		t.Fatalf("release = %s, want NO", outcome.Release)
	}
	if outcome.Interval.MeetsFloor {
		t.Error("18/20 must not clear the floor")
	}
	if len(outcome.Reasons) == 0 || !strings.Contains(outcome.Reasons[0], "no override") {
		t.Errorf("reasons = %v", outcome.Reasons)
	}
}

// AC-10: every published number recomputes. A hand-edited outcome is refused.
func TestQrelBlindSmoke_OutcomeMustRecomputeFromItsArtifacts(t *testing.T) {
	artifacts := buildBlindEvalArtifacts(t, specsFor(20, 18))
	comparison := matchingComparison(t, artifacts.Precondition)
	outcome, err := EvaluateQrelBlindSmoke(artifacts, comparison)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateEvaluationOutcome(outcome, artifacts); err != nil {
		t.Fatalf("the computed outcome must revalidate: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*EvaluationOutcome)
	}{
		{"a lowered k", func(o *EvaluationOutcome) { o.K = 18 }},
		{"an inflated pass count", func(o *EvaluationOutcome) { o.PassCount = 19 }},
		{"a forced release", func(o *EvaluationOutcome) { o.Release = ReleaseYes }},
		{"a shrunken N", func(o *EvaluationOutcome) { o.N = 19 }},
		{"a flipped query outcome", func(o *EvaluationOutcome) { o.Queries[19].Outcome = GradeOutcomePass }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			broken := outcome
			broken.Queries = append([]QueryOutcome(nil), outcome.Queries...)
			tc.mutate(&broken)
			if err := ValidateEvaluationOutcome(broken, artifacts); err == nil {
				t.Fatalf("%s survived validation", tc.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// AC-6 participant rules
// ---------------------------------------------------------------------------

func TestQrelBlindSmoke_ParticipantRefusals(t *testing.T) {
	base := buildBlindEvalArtifacts(t, specsFor(20, 19)).PreRegistration
	for _, tc := range []struct {
		name    string
		mutate  func(*PreRegistration)
		wantSub string
	}{
		{
			name:    "both primaries participated in the track",
			mutate:  func(p *PreRegistration) { p.PrimaryRaters[0].ParticipatedInTrack = true },
			wantSub: "at least one must not have",
		},
		{
			name:    "a rater with no model recorded",
			mutate:  func(p *PreRegistration) { p.PrimaryRaters[0].Model = "" },
			wantSub: "has no model",
		},
		{
			name:    "a rater with no independence basis",
			mutate:  func(p *PreRegistration) { p.PrimaryRaters[1].IndependenceBasis = "" },
			wantSub: "has no independence_basis",
		},
		{
			name:    "one rater used twice",
			mutate:  func(p *PreRegistration) { p.PrimaryRaters[1].ID = p.PrimaryRaters[0].ID },
			wantSub: "appears twice",
		},
		{
			name:    "the adjudicator is also a primary rater",
			mutate:  func(p *PreRegistration) { p.Adjudicator.ID = p.PrimaryRaters[0].ID },
			wantSub: "cannot be one of the tied parties",
		},
		{
			name:    "only one primary rater",
			mutate:  func(p *PreRegistration) { p.PrimaryRaters = p.PrimaryRaters[:1] },
			wantSub: "want exactly 2",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			broken := base
			broken.PrimaryRaters = append([]Participant(nil), base.PrimaryRaters...)
			tc.mutate(&broken)
			sealed, err := SealPreRegistration(broken)
			if err != nil {
				t.Fatal(err)
			}
			err = ValidatePreRegistration(sealed)
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("refusal %q does not mention %q", err.Error(), tc.wantSub)
			}
		})
	}
	if err := ValidatePreRegistration(base); err != nil {
		t.Fatalf("the untouched pre-registration must be accepted: %v", err)
	}
}

// A pre-registration whose query list is shorter than N is a subset, and a
// subset is exactly what "record N before any response exists" forbids.
func TestQrelBlindSmoke_PreRegisteredPopulationMustEqualN(t *testing.T) {
	base := buildBlindEvalArtifacts(t, specsFor(20, 19)).PreRegistration
	base.Queries = base.Queries[:19]
	sealed, err := SealPreRegistration(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePreRegistration(sealed); err == nil {
		t.Fatal("a pre-registration holding fewer queries than N was accepted")
	}
}
