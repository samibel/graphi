package retrieval

// The SW-280 qrel-blind smoke evaluation: record types, ordering evidence and
// the fail-closed decision procedure.
//
// The entire value of this evaluation is the ORDER its steps happen in, so the
// types below exist to make that order checkable from the artifacts rather than
// asserted in prose:
//
//   - the precondition record freezes every input by content hash before the
//     evaluation may start, and is compared again at the end of the run;
//   - the pre-registration record derives k from N and content-addresses every
//     query text and every captured bundle BEFORE a rater response exists, and
//     every response names the pre-registration's own hash, which a response
//     produced earlier could not have done;
//   - a grade names the content address of the response it graded, so a grade
//     cannot precede its response or be re-pointed at a different one;
//   - an adjudicator's response is frozen and content-addressed before the
//     disclosure record, which names that frozen hash, exists.
//
// It is a QREL-BLIND SMOKE EVALUATION. The raters see our bundle format and our
// question set. It is not a system-blind evaluation, not a human panel, and not
// an estimate of whether graphi answers Go questions in general. Its pass count
// is a separate gate and never enters the token estimand
// (docs/eval/retrieval/methodology.md line 25).

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	// QrelBlindSmokeContractVersion identifies this evaluation's record shapes
	// and decision procedure. A change to either requires a new version.
	QrelBlindSmokeContractVersion = "sw280-qrel-blind-smoke-evaluation/1"

	// QrelBlindSmokeEvaluationName is the ONLY permitted name for this
	// evaluation. CheckQrelBlindSmokeWording requires it and rejects the
	// forbidden alternatives.
	QrelBlindSmokeEvaluationName = "qrel-blind smoke evaluation"

	// BlindEvalComparatorVersion is the frozen comparator identity recorded in
	// the precondition record. This slice runs no comparator; it records the
	// version so a later comparator change invalidates this evaluation.
	BlindEvalComparatorVersion = SavingsComparatorMethod + "/1"
)

// Rater roles.
const (
	RaterRolePrimary     = "primary"
	RaterRoleAdjudicator = "adjudicator"
)

// Response statuses. Exactly one of these is recorded for every rater slot of
// every pre-registered query; there is no "not attempted" state, because an
// absent attempt is what AC-9 calls a missing response.
const (
	ResponseStatusAnswered = "answered"
	ResponseStatusEmpty    = "empty"
	ResponseStatusRefused  = "refused"
	ResponseStatusMissing  = "missing"
)

// Grade outcomes.
const (
	GradeOutcomePass = "pass"
	GradeOutcomeFail = "fail"
)

// Release results. There is no third value and no override.
const (
	ReleaseYes = "YES"
	ReleaseNo  = "NO"
)

// timeLayout is the one accepted timestamp spelling. Ordering evidence is
// worthless if two artifacts can express the same instant differently.
const timeLayout = time.RFC3339

// ---------------------------------------------------------------------------
// Population and k derivation
// ---------------------------------------------------------------------------

// AnswerableHoldout returns the sealed dataset's answerable holdout queries in
// dataset order.
//
// "Answerable" is SW-266 AC-2's definition: the query has at least one grade-3
// answer span. `no_hit` queries are excluded and counted separately. The two
// readings a reader might use — "not no_hit" and "carries a grade-3 span" — are
// BOTH computed, and a disagreement between them is refused rather than
// resolved silently, because which one was used would then decide N.
func AnswerableHoldout(ds *Dataset) ([]Query, error) {
	if ds == nil {
		return nil, fmt.Errorf("retrieval %s: no dataset", QrelBlindSmokeEvaluationName)
	}
	var answerable []Query
	var discrepancies []string
	noHit := 0
	for _, q := range ds.Queries {
		if q.Split != SplitHoldout {
			continue
		}
		if q.Stratum == StratumNoHit {
			noHit++
		}
		hasGrade3 := false
		for _, j := range q.Judgements {
			if j.Grade == GradeMax {
				hasGrade3 = true
				break
			}
		}
		notNoHit := q.Stratum != StratumNoHit
		if hasGrade3 != notNoHit {
			discrepancies = append(discrepancies, fmt.Sprintf("%s (stratum=%s, grade3_span=%t)", q.ID, q.Stratum, hasGrade3))
			continue
		}
		if hasGrade3 {
			answerable = append(answerable, q)
		}
	}
	if len(discrepancies) > 0 {
		return nil, fmt.Errorf("retrieval %s: %d holdout queries disagree between the two readings of \"answerable\" (not no_hit vs carries a grade-3 span): %s; N would depend on which reading was used, so the population is refused",
			QrelBlindSmokeEvaluationName, len(discrepancies), strings.Join(discrepancies, ", "))
	}
	if len(answerable) == 0 {
		return nil, fmt.Errorf("retrieval %s: the sealed dataset holds no answerable holdout query (%d holdout queries are no_hit and are counted separately)", QrelBlindSmokeEvaluationName, noHit)
	}
	return answerable, nil
}

// PassCountDerivation is the recorded derivation of k from N. It is written
// into the pre-registration BEFORE any response exists, and re-derived from N
// during validation, so a hand-edited k cannot survive.
type PassCountDerivation struct {
	ContractVersion  string `json:"contract_version"`
	Evaluation       string `json:"evaluation"`
	NSource          string `json:"n_source"`
	DatasetSHA256    string `json:"dataset_sha256"`
	N                int    `json:"n"`
	K                int    `json:"k"`
	Floor            string `json:"floor"`
	LevelBasisPoints int    `json:"level_basis_points"`
	Method           string `json:"method"`
	// KInterval is the interval at exactly k passes: the smallest pass count
	// that clears the floor. KMinusOneInterval is the one below it, recorded so
	// the report can show the step that decided k rather than asserting it.
	KInterval         ExactBinomialInterval  `json:"k_interval"`
	KMinusOneInterval *ExactBinomialInterval `json:"k_minus_one_interval,omitempty"`
}

// DerivePassCount derives k from N and records the derivation. The typed
// UnsatisfiableBoundError is returned unchanged for N below the cliff.
func DerivePassCount(n int, datasetSHA256, nSource string) (PassCountDerivation, error) {
	k, err := MinimumPassCount(n)
	if err != nil {
		return PassCountDerivation{}, err
	}
	kInterval, err := ClopperPearsonInterval(k, n)
	if err != nil {
		return PassCountDerivation{}, err
	}
	derivation := PassCountDerivation{
		ContractVersion:  QrelBlindSmokeContractVersion,
		Evaluation:       QrelBlindSmokeEvaluationName,
		NSource:          nSource,
		DatasetSHA256:    datasetSHA256,
		N:                n,
		K:                k,
		Floor:            FloorString(),
		LevelBasisPoints: QrelBlindSmokeLevelBasisPoints,
		Method:           ClopperPearsonMethod,
		KInterval:        kInterval,
	}
	if k > 0 {
		below, err := ClopperPearsonInterval(k-1, n)
		if err != nil {
			return PassCountDerivation{}, err
		}
		derivation.KMinusOneInterval = &below
	}
	return derivation, nil
}

// ValidatePassCountDerivation re-derives k from the recorded N and refuses any
// value that was not produced by MinimumPassCount.
func ValidatePassCountDerivation(d PassCountDerivation) error {
	if d.ContractVersion != QrelBlindSmokeContractVersion {
		return fmt.Errorf("retrieval %s: derivation contract_version=%q, want %q", QrelBlindSmokeEvaluationName, d.ContractVersion, QrelBlindSmokeContractVersion)
	}
	if d.Evaluation != QrelBlindSmokeEvaluationName {
		return fmt.Errorf("retrieval %s: derivation evaluation=%q, want %q", QrelBlindSmokeEvaluationName, d.Evaluation, QrelBlindSmokeEvaluationName)
	}
	if d.Floor != FloorString() || d.LevelBasisPoints != QrelBlindSmokeLevelBasisPoints {
		return fmt.Errorf("retrieval %s: derivation floor=%q level=%d, want %q and %d", QrelBlindSmokeEvaluationName, d.Floor, d.LevelBasisPoints, FloorString(), QrelBlindSmokeLevelBasisPoints)
	}
	want, err := DerivePassCount(d.N, d.DatasetSHA256, d.NSource)
	if err != nil {
		return err
	}
	if d.K != want.K {
		return fmt.Errorf("retrieval %s: recorded k=%d for N=%d, but the derivation yields k=%d; k is derived, never written", QrelBlindSmokeEvaluationName, d.K, d.N, want.K)
	}
	if d.KInterval != want.KInterval {
		return fmt.Errorf("retrieval %s: recorded interval at k=%d does not recompute", QrelBlindSmokeEvaluationName, d.K)
	}
	if (d.KMinusOneInterval == nil) != (want.KMinusOneInterval == nil) {
		return fmt.Errorf("retrieval %s: recorded k-1 interval presence does not recompute", QrelBlindSmokeEvaluationName)
	}
	if d.KMinusOneInterval != nil && *d.KMinusOneInterval != *want.KMinusOneInterval {
		return fmt.Errorf("retrieval %s: recorded interval at k-1=%d does not recompute", QrelBlindSmokeEvaluationName, d.K-1)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Precondition record (AC-1)
// ---------------------------------------------------------------------------

// FrozenInput is one file frozen by content before the evaluation may start.
type FrozenInput struct {
	Role   string `json:"role"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// PreconditionRecord names every input by content hash, with the commit and
// timestamp at which they were frozen. The evaluation refuses to start when any
// field is absent, and fails the run when any hash differs at the end.
type PreconditionRecord struct {
	ContractVersion            string        `json:"contract_version"`
	Evaluation                 string        `json:"evaluation"`
	FreezeCommit               string        `json:"freeze_commit"`
	FreezeTimestamp            string        `json:"freeze_timestamp"`
	DatasetPath                string        `json:"dataset_path"`
	DatasetSHA256              string        `json:"dataset_sha256"`
	CandidateSHA               string        `json:"candidate_sha"`
	CandidateMethod            string        `json:"candidate_method"`
	CandidateTokenBudget       int           `json:"candidate_token_budget"`
	ComparatorVersion          string        `json:"comparator_version"`
	TokenizerID                string        `json:"tokenizer_id"`
	TokenizerVocabularySHA256  string        `json:"tokenizer_vocabulary_sha256"`
	MeasurementContractVersion string        `json:"measurement_contract_version"`
	ClaimWordingSHA256         string        `json:"claim_wording_sha256"`
	Inputs                     []FrozenInput `json:"inputs"`
	SHA256                     string        `json:"sha256"`
}

// preconditionInputRoles are the roles AC-1 requires by name. A record missing
// any of them is incomplete, which is a refusal to start rather than a warning.
var preconditionInputRoles = []string{
	"budgets",
	"targets",
	"grading_rubric",
	"methodology",
}

// FrozenClaimWording is the exact claim wording this evaluation is frozen
// against. It is hashed into the precondition record so a later edit to the
// permitted sentence invalidates the evaluation.
func FrozenClaimWording() string {
	return ClaimTemplateExample + "\n" + RequiredClaimLimitation + "\n" + MeasurementContractVersion
}

// ValidatePreconditionRecord is the REFUSAL TO START. Every named field must be
// present and well-shaped; there is no partial-record mode.
func ValidatePreconditionRecord(rec PreconditionRecord) error {
	if rec.ContractVersion != QrelBlindSmokeContractVersion {
		return fmt.Errorf("retrieval %s: precondition contract_version=%q, want %q", QrelBlindSmokeEvaluationName, rec.ContractVersion, QrelBlindSmokeContractVersion)
	}
	if rec.Evaluation != QrelBlindSmokeEvaluationName {
		return fmt.Errorf("retrieval %s: precondition evaluation=%q, want %q", QrelBlindSmokeEvaluationName, rec.Evaluation, QrelBlindSmokeEvaluationName)
	}
	for _, field := range []struct{ name, value string }{
		{"freeze_commit", rec.FreezeCommit},
		{"freeze_timestamp", rec.FreezeTimestamp},
		{"dataset_path", rec.DatasetPath},
		{"candidate_sha", rec.CandidateSHA},
		{"comparator_version", rec.ComparatorVersion},
		{"tokenizer_id", rec.TokenizerID},
		{"measurement_contract_version", rec.MeasurementContractVersion},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("retrieval %s: precondition record has no %s; the evaluation refuses to start", QrelBlindSmokeEvaluationName, field.name)
		}
	}
	for _, digest := range []struct{ name, value string }{
		{"dataset_sha256", rec.DatasetSHA256},
		{"tokenizer_vocabulary_sha256", rec.TokenizerVocabularySHA256},
		{"claim_wording_sha256", rec.ClaimWordingSHA256},
	} {
		if !isLowerHexDigest(digest.value, 64) {
			return fmt.Errorf("retrieval %s: precondition record %s must be 64 lowercase hex characters; the evaluation refuses to start", QrelBlindSmokeEvaluationName, digest.name)
		}
	}
	if !isLowerHexDigest(rec.CandidateSHA, 40) {
		return fmt.Errorf("retrieval %s: precondition candidate_sha must be a 40-character commit sha; the evaluation refuses to start", QrelBlindSmokeEvaluationName)
	}
	if _, err := time.Parse(timeLayout, rec.FreezeTimestamp); err != nil {
		return fmt.Errorf("retrieval %s: precondition freeze_timestamp %q is not %s", QrelBlindSmokeEvaluationName, rec.FreezeTimestamp, timeLayout)
	}
	if rec.CandidateMethod != SavingsCandidateMethod {
		return fmt.Errorf("retrieval %s: precondition candidate_method=%q, want %q", QrelBlindSmokeEvaluationName, rec.CandidateMethod, SavingsCandidateMethod)
	}
	if rec.CandidateTokenBudget != SavingsCandidateBudget {
		return fmt.Errorf("retrieval %s: precondition candidate_token_budget=%d, want the frozen %d", QrelBlindSmokeEvaluationName, rec.CandidateTokenBudget, SavingsCandidateBudget)
	}
	if rec.ComparatorVersion != BlindEvalComparatorVersion {
		return fmt.Errorf("retrieval %s: precondition comparator_version=%q, want %q", QrelBlindSmokeEvaluationName, rec.ComparatorVersion, BlindEvalComparatorVersion)
	}
	if rec.MeasurementContractVersion != MeasurementContractVersion {
		return fmt.Errorf("retrieval %s: precondition measurement_contract_version=%q, want %q", QrelBlindSmokeEvaluationName, rec.MeasurementContractVersion, MeasurementContractVersion)
	}
	if rec.ClaimWordingSHA256 != SHA256Hex([]byte(FrozenClaimWording())) {
		return fmt.Errorf("retrieval %s: precondition claim_wording_sha256 does not match the frozen claim wording", QrelBlindSmokeEvaluationName)
	}
	seen := map[string]bool{}
	for i, input := range rec.Inputs {
		if strings.TrimSpace(input.Role) == "" || strings.TrimSpace(input.Path) == "" {
			return fmt.Errorf("retrieval %s: precondition input %d has no role or path", QrelBlindSmokeEvaluationName, i)
		}
		if !isLowerHexDigest(input.SHA256, 64) {
			return fmt.Errorf("retrieval %s: precondition input %q (%s) has no valid sha256; the evaluation refuses to start", QrelBlindSmokeEvaluationName, input.Role, input.Path)
		}
		if seen[input.Role] {
			return fmt.Errorf("retrieval %s: precondition input role %q appears twice", QrelBlindSmokeEvaluationName, input.Role)
		}
		seen[input.Role] = true
	}
	for _, role := range preconditionInputRoles {
		if !seen[role] {
			return fmt.Errorf("retrieval %s: precondition record has no %q input; the evaluation refuses to start", QrelBlindSmokeEvaluationName, role)
		}
	}
	address, err := ContentAddress(rec, func(v *PreconditionRecord) { v.SHA256 = "" })
	if err != nil {
		return err
	}
	if rec.SHA256 != address {
		return fmt.Errorf("retrieval %s: precondition record sha256=%q does not match its own content address %q", QrelBlindSmokeEvaluationName, rec.SHA256, address)
	}
	return nil
}

// InputHashComparison is one end-of-run comparison of a frozen input against
// its value observed at the end of the run.
type InputHashComparison struct {
	Role     string `json:"role"`
	Path     string `json:"path"`
	Frozen   string `json:"frozen_sha256"`
	Observed string `json:"observed_sha256"`
	Matches  bool   `json:"matches"`
	// Error records why a frozen input could not be read at all. An unreadable
	// input is a mismatch, never a skipped row.
	Error string `json:"error,omitempty"`
}

// HashComparisonResult is the end-of-run comparison AC-1 requires and AC-13
// makes the enforcement of "nothing changed after the evaluation ran".
type HashComparisonResult struct {
	ComparedAt  string                `json:"compared_at"`
	Comparisons []InputHashComparison `json:"comparisons"`
	AllMatch    bool                  `json:"all_match"`
}

// ReadFileSHA256 is the seam the end-of-run comparison reads files through, so
// a test can drive a drifting input without touching the working tree.
type ReadFileSHA256 func(path string) (string, error)

// CompareFrozenInputs recomputes every hash the precondition record froze. An
// input that cannot be read is reported as a MISMATCH with its error, never
// omitted: a check that cannot evaluate a row must not report that row green.
func CompareFrozenInputs(rec PreconditionRecord, read ReadFileSHA256, at time.Time) (HashComparisonResult, error) {
	if read == nil {
		return HashComparisonResult{}, fmt.Errorf("retrieval %s: end-of-run comparison needs a reader", QrelBlindSmokeEvaluationName)
	}
	result := HashComparisonResult{ComparedAt: at.UTC().Format(timeLayout), AllMatch: true}
	frozen := append([]FrozenInput{
		{Role: "dataset", Path: rec.DatasetPath, SHA256: rec.DatasetSHA256},
	}, rec.Inputs...)
	for _, input := range frozen {
		comparison := InputHashComparison{Role: input.Role, Path: input.Path, Frozen: input.SHA256}
		observed, err := read(input.Path)
		if err != nil {
			comparison.Error = err.Error()
			comparison.Matches = false
		} else {
			comparison.Observed = observed
			comparison.Matches = observed == input.SHA256
		}
		if !comparison.Matches {
			result.AllMatch = false
		}
		result.Comparisons = append(result.Comparisons, comparison)
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Pre-registration (AC-5)
// ---------------------------------------------------------------------------

// PreRegisteredQuery content-addresses one query's text and its captured
// bundle. Recording the bundle hash here — before a response exists — is what
// binds every later response to the exact bytes a rater could have seen.
type PreRegisteredQuery struct {
	QueryID         string `json:"query_id"`
	FamilyID        string `json:"family_id"`
	Stratum         string `json:"stratum"`
	QueryTextSHA256 string `json:"query_text_sha256"`
	// PromptSHA256 is the digest of the EXACT prompt bytes the rater was
	// given. Pre-registering it is what makes a prompt swap detectable: a
	// prompt with an expected answer appended after pre-registration hashes
	// differently, and CheckPromptBinding refuses it.
	//
	// It is omitempty because this contract version's first run pre-dated the
	// field. A run whose pre-registration omits it is still bound, and more
	// tightly: CheckPromptBinding RECONSTRUCTS the prompt from the
	// pre-registered bundle bytes and query text and requires the committed
	// prompt file to be byte-identical, so the digest is a pure function of
	// pre-registered content either way.
	PromptSHA256      string              `json:"prompt_sha256,omitempty"`
	BundleSHA256      string              `json:"bundle_sha256"`
	BundleByteCount   int                 `json:"bundle_byte_count"`
	BundleBoundary    PayloadBoundary     `json:"bundle_boundary"`
	BundleTokenCounts []PayloadTokenCount `json:"bundle_token_counts"`
}

// PreRegistration is written and committed BEFORE the first rater response
// exists. Every response names its SHA256, which is the ordering evidence: a
// response produced earlier could not have named a hash that did not yet exist.
type PreRegistration struct {
	ContractVersion    string               `json:"contract_version"`
	Evaluation         string               `json:"evaluation"`
	PreconditionSHA256 string               `json:"precondition_record_sha256"`
	PreconditionCommit string               `json:"precondition_record_commit"`
	RecordedAt         string               `json:"recorded_at"`
	Derivation         PassCountDerivation  `json:"derivation"`
	PrimaryRaters      []Participant        `json:"primary_raters"`
	Grader             Participant          `json:"grader"`
	Adjudicator        Participant          `json:"adjudicator"`
	Queries            []PreRegisteredQuery `json:"queries"`
	SHA256             string               `json:"sha256"`
}

// Participant is one recorded rater, grader or adjudicator identity. Model and
// provider version are required: "an agent" is not an identity.
type Participant struct {
	ID       string `json:"id"`
	Role     string `json:"role"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// IndependenceBasis states, in the participant's own record, why this
	// participant did or did not take part in this track's implementation or in
	// dataset annotation. AC-6 requires the report to say which one and on what
	// basis; recording it here keeps the claim beside the identity.
	IndependenceBasis string `json:"independence_basis"`
	// ParticipatedInTrack is true when the participant DID take part in
	// implementation or annotation. At least one primary rater must be false.
	ParticipatedInTrack bool `json:"participated_in_track"`
}

func validateParticipant(p Participant, wantRole string) error {
	for _, field := range []struct{ name, value string }{
		{"id", p.ID},
		{"provider", p.Provider},
		{"model", p.Model},
		{"independence_basis", p.IndependenceBasis},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("retrieval %s: participant %q has no %s", QrelBlindSmokeEvaluationName, p.ID, field.name)
		}
	}
	if p.Role != wantRole {
		return fmt.Errorf("retrieval %s: participant %q role=%q, want %q", QrelBlindSmokeEvaluationName, p.ID, p.Role, wantRole)
	}
	return nil
}

// ValidatePreRegistration checks the record's internal consistency, re-derives
// k from N, and refuses a population that does not match the pre-registered
// query list.
func ValidatePreRegistration(pre PreRegistration) error {
	if pre.ContractVersion != QrelBlindSmokeContractVersion {
		return fmt.Errorf("retrieval %s: pre-registration contract_version=%q, want %q", QrelBlindSmokeEvaluationName, pre.ContractVersion, QrelBlindSmokeContractVersion)
	}
	if pre.Evaluation != QrelBlindSmokeEvaluationName {
		return fmt.Errorf("retrieval %s: pre-registration evaluation=%q, want %q", QrelBlindSmokeEvaluationName, pre.Evaluation, QrelBlindSmokeEvaluationName)
	}
	if !isLowerHexDigest(pre.PreconditionSHA256, 64) {
		return fmt.Errorf("retrieval %s: pre-registration names no precondition record", QrelBlindSmokeEvaluationName)
	}
	if !isLowerHexDigest(pre.PreconditionCommit, 40) {
		return fmt.Errorf("retrieval %s: pre-registration names no precondition record commit", QrelBlindSmokeEvaluationName)
	}
	recordedAt, err := time.Parse(timeLayout, pre.RecordedAt)
	if err != nil {
		return fmt.Errorf("retrieval %s: pre-registration recorded_at %q is not %s", QrelBlindSmokeEvaluationName, pre.RecordedAt, timeLayout)
	}
	_ = recordedAt
	if err := ValidatePassCountDerivation(pre.Derivation); err != nil {
		return err
	}
	if len(pre.PrimaryRaters) != 2 {
		return fmt.Errorf("retrieval %s: %d primary raters recorded, want exactly 2", QrelBlindSmokeEvaluationName, len(pre.PrimaryRaters))
	}
	independent := 0
	ids := map[string]bool{}
	for _, rater := range pre.PrimaryRaters {
		if err := validateParticipant(rater, RaterRolePrimary); err != nil {
			return err
		}
		if ids[rater.ID] {
			return fmt.Errorf("retrieval %s: primary rater %q appears twice; the two primaries must be distinct", QrelBlindSmokeEvaluationName, rater.ID)
		}
		ids[rater.ID] = true
		if !rater.ParticipatedInTrack {
			independent++
		}
	}
	if independent == 0 {
		return fmt.Errorf("retrieval %s: both primary raters participated in this track's implementation or dataset annotation; at least one must not have", QrelBlindSmokeEvaluationName)
	}
	if err := validateParticipant(pre.Grader, "grader"); err != nil {
		return err
	}
	if err := validateParticipant(pre.Adjudicator, RaterRoleAdjudicator); err != nil {
		return err
	}
	if ids[pre.Adjudicator.ID] {
		return fmt.Errorf("retrieval %s: the adjudicator %q is also a primary rater; a tie-breaker cannot be one of the tied parties", QrelBlindSmokeEvaluationName, pre.Adjudicator.ID)
	}
	if len(pre.Queries) != pre.Derivation.N {
		return fmt.Errorf("retrieval %s: pre-registration holds %d queries but N=%d; N is the pre-registered population, not a subset", QrelBlindSmokeEvaluationName, len(pre.Queries), pre.Derivation.N)
	}
	seen := map[string]bool{}
	for _, q := range pre.Queries {
		if strings.TrimSpace(q.QueryID) == "" {
			return fmt.Errorf("retrieval %s: a pre-registered query has no id", QrelBlindSmokeEvaluationName)
		}
		if seen[q.QueryID] {
			return fmt.Errorf("retrieval %s: query %q is pre-registered twice", QrelBlindSmokeEvaluationName, q.QueryID)
		}
		seen[q.QueryID] = true
		if !isLowerHexDigest(q.QueryTextSHA256, 64) || !isLowerHexDigest(q.BundleSHA256, 64) {
			return fmt.Errorf("retrieval %s: query %q lacks a content address for its text or its bundle", QrelBlindSmokeEvaluationName, q.QueryID)
		}
		if q.PromptSHA256 != "" && !isLowerHexDigest(q.PromptSHA256, 64) {
			return fmt.Errorf("retrieval %s: query %q pre-registers a malformed prompt_sha256", QrelBlindSmokeEvaluationName, q.QueryID)
		}
		if q.BundleBoundary != PayloadBoundaryCandidate {
			return fmt.Errorf("retrieval %s: query %q bundle boundary=%q, want %q", QrelBlindSmokeEvaluationName, q.QueryID, q.BundleBoundary, PayloadBoundaryCandidate)
		}
		if q.BundleByteCount <= 0 {
			return fmt.Errorf("retrieval %s: query %q has an empty bundle; an empty bundle is not a captured payload", QrelBlindSmokeEvaluationName, q.QueryID)
		}
		if len(q.BundleTokenCounts) != 2 {
			return fmt.Errorf("retrieval %s: query %q records %d token counts, want the whitespace counter and the pinned real tokenizer", QrelBlindSmokeEvaluationName, q.QueryID, len(q.BundleTokenCounts))
		}
	}
	address, err := ContentAddress(pre, func(v *PreRegistration) { v.SHA256 = "" })
	if err != nil {
		return err
	}
	if pre.SHA256 != address {
		return fmt.Errorf("retrieval %s: pre-registration sha256=%q does not match its own content address %q", QrelBlindSmokeEvaluationName, pre.SHA256, address)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Responses, grades, adjudication
// ---------------------------------------------------------------------------

// RaterResponse is one rater's answer to one query. Its field set IS the
// blindness contract: there is no field for the repository, the judgements, the
// expected answer, the other rater's response or a primary grade, so an
// implementation cannot record having supplied one.
type RaterResponse struct {
	ContractVersion       string `json:"contract_version"`
	Evaluation            string `json:"evaluation"`
	Role                  string `json:"role"`
	QueryID               string `json:"query_id"`
	RaterID               string `json:"rater_id"`
	Provider              string `json:"provider"`
	Model                 string `json:"model"`
	PreRegistrationSHA256 string `json:"pre_registration_sha256"`
	QueryTextSHA256       string `json:"query_text_sha256"`
	BundleSHA256          string `json:"bundle_sha256"`
	PromptSHA256          string `json:"prompt_sha256"`
	// Inputs enumerates, by name, everything the rater was given. It is
	// compared against the closed permitted set: anything else is a refusal.
	Inputs      []string `json:"inputs"`
	Status      string   `json:"status"`
	Text        string   `json:"text"`
	RespondedAt string   `json:"responded_at"`
	SHA256      string   `json:"sha256"`
}

// permittedRaterInputs is the closed set. A response naming anything else is
// invalid; this is what "the recorded invocation shall show that the only
// inputs were the query text and the preserved bundle bytes" means executably.
var permittedRaterInputs = map[string]bool{
	"query_text":          true,
	"preserved_bundle":    true,
	"answer_instructions": true,
}

// ValidateRaterResponse checks one response in isolation.
func ValidateRaterResponse(r RaterResponse) error {
	if r.ContractVersion != QrelBlindSmokeContractVersion {
		return fmt.Errorf("retrieval %s: response contract_version=%q, want %q", QrelBlindSmokeEvaluationName, r.ContractVersion, QrelBlindSmokeContractVersion)
	}
	if r.Evaluation != QrelBlindSmokeEvaluationName {
		return fmt.Errorf("retrieval %s: response evaluation=%q, want %q", QrelBlindSmokeEvaluationName, r.Evaluation, QrelBlindSmokeEvaluationName)
	}
	if r.Role != RaterRolePrimary && r.Role != RaterRoleAdjudicator {
		return fmt.Errorf("retrieval %s: response role=%q, want %q or %q", QrelBlindSmokeEvaluationName, r.Role, RaterRolePrimary, RaterRoleAdjudicator)
	}
	switch r.Status {
	case ResponseStatusAnswered, ResponseStatusEmpty, ResponseStatusRefused, ResponseStatusMissing:
	default:
		return fmt.Errorf("retrieval %s: response status=%q is not one of answered/empty/refused/missing", QrelBlindSmokeEvaluationName, r.Status)
	}
	if r.Status == ResponseStatusAnswered && strings.TrimSpace(r.Text) == "" {
		return fmt.Errorf("retrieval %s: query %s rater %s is marked answered but its text is empty; an empty response is status %q", QrelBlindSmokeEvaluationName, r.QueryID, r.RaterID, ResponseStatusEmpty)
	}
	if r.Status == ResponseStatusMissing && strings.TrimSpace(r.Text) != "" {
		return fmt.Errorf("retrieval %s: query %s rater %s is marked missing but carries text", QrelBlindSmokeEvaluationName, r.QueryID, r.RaterID)
	}
	for _, field := range []struct{ name, value string }{
		{"query_id", r.QueryID},
		{"rater_id", r.RaterID},
		{"provider", r.Provider},
		{"model", r.Model},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("retrieval %s: response for query %q has no %s", QrelBlindSmokeEvaluationName, r.QueryID, field.name)
		}
	}
	for _, digest := range []struct{ name, value string }{
		{"pre_registration_sha256", r.PreRegistrationSHA256},
		{"query_text_sha256", r.QueryTextSHA256},
		{"bundle_sha256", r.BundleSHA256},
		{"prompt_sha256", r.PromptSHA256},
	} {
		if !isLowerHexDigest(digest.value, 64) {
			return fmt.Errorf("retrieval %s: response for query %q has no valid %s", QrelBlindSmokeEvaluationName, r.QueryID, digest.name)
		}
	}
	if _, err := time.Parse(timeLayout, r.RespondedAt); err != nil {
		return fmt.Errorf("retrieval %s: response for query %q has responded_at %q, not %s", QrelBlindSmokeEvaluationName, r.QueryID, r.RespondedAt, timeLayout)
	}
	if len(r.Inputs) == 0 {
		return fmt.Errorf("retrieval %s: response for query %q records no inputs", QrelBlindSmokeEvaluationName, r.QueryID)
	}
	for _, input := range r.Inputs {
		if !permittedRaterInputs[input] {
			return fmt.Errorf("retrieval %s: response for query %q records input %q, which is outside the permitted set (query_text, preserved_bundle, answer_instructions); the rater was not qrel-blind", QrelBlindSmokeEvaluationName, r.QueryID, input)
		}
	}
	address, err := ContentAddress(r, func(v *RaterResponse) { v.SHA256 = "" })
	if err != nil {
		return err
	}
	if r.SHA256 != address {
		return fmt.Errorf("retrieval %s: response for query %q sha256=%q does not match its own content address %q", QrelBlindSmokeEvaluationName, r.QueryID, r.SHA256, address)
	}
	return nil
}

// Grade is one graded outcome, bound to the response hash it graded.
type Grade struct {
	ContractVersion string `json:"contract_version"`
	Evaluation      string `json:"evaluation"`
	QueryID         string `json:"query_id"`
	ResponseSHA256  string `json:"response_sha256"`
	GraderID        string `json:"grader_id"`
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	RubricSHA256    string `json:"rubric_sha256"`
	Outcome         string `json:"outcome"`
	Rationale       string `json:"rationale"`
	GradedAt        string `json:"graded_at"`
	SHA256          string `json:"sha256"`
}

// ValidateGrade checks one grade in isolation.
func ValidateGrade(g Grade) error {
	if g.ContractVersion != QrelBlindSmokeContractVersion {
		return fmt.Errorf("retrieval %s: grade contract_version=%q, want %q", QrelBlindSmokeEvaluationName, g.ContractVersion, QrelBlindSmokeContractVersion)
	}
	if g.Evaluation != QrelBlindSmokeEvaluationName {
		return fmt.Errorf("retrieval %s: grade evaluation=%q, want %q", QrelBlindSmokeEvaluationName, g.Evaluation, QrelBlindSmokeEvaluationName)
	}
	if g.Outcome != GradeOutcomePass && g.Outcome != GradeOutcomeFail {
		return fmt.Errorf("retrieval %s: grade outcome=%q, want pass or fail", QrelBlindSmokeEvaluationName, g.Outcome)
	}
	for _, field := range []struct{ name, value string }{
		{"query_id", g.QueryID},
		{"grader_id", g.GraderID},
		{"provider", g.Provider},
		{"model", g.Model},
		{"rationale", g.Rationale},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("retrieval %s: grade for query %q has no %s", QrelBlindSmokeEvaluationName, g.QueryID, field.name)
		}
	}
	for _, digest := range []struct{ name, value string }{
		{"response_sha256", g.ResponseSHA256},
		{"rubric_sha256", g.RubricSHA256},
	} {
		if !isLowerHexDigest(digest.value, 64) {
			return fmt.Errorf("retrieval %s: grade for query %q has no valid %s", QrelBlindSmokeEvaluationName, g.QueryID, digest.name)
		}
	}
	if _, err := time.Parse(timeLayout, g.GradedAt); err != nil {
		return fmt.Errorf("retrieval %s: grade for query %q has graded_at %q, not %s", QrelBlindSmokeEvaluationName, g.QueryID, g.GradedAt, timeLayout)
	}
	address, err := ContentAddress(g, func(v *Grade) { v.SHA256 = "" })
	if err != nil {
		return err
	}
	if g.SHA256 != address {
		return fmt.Errorf("retrieval %s: grade for query %q sha256=%q does not match its own content address %q", QrelBlindSmokeEvaluationName, g.QueryID, g.SHA256, address)
	}
	return nil
}

// DisclosureRecord is written AFTER the adjudicator's response is frozen. It
// names that response's content address, which is the evidence the adjudicator
// answered before either primary response or primary grade was disclosed: a
// disclosure that happened first could not have named a hash that did not yet
// exist.
//
// It carries NO timestamp. The earlier shape recorded a `disclosed_at` derived
// as the adjudicator response's file mtime plus exactly one second, which is a
// generated number wearing the costume of an observation: every disclosure was
// +1s because the code wrote +1s, and a mutable mtime can be set to anything.
// A field that cannot be wrong is not evidence, so it is gone. What remains is
// the digest chain, which a later record genuinely cannot forge backwards, plus
// an explicit statement of what the chain does NOT establish.
type DisclosureRecord struct {
	QueryID                   string   `json:"query_id"`
	AdjudicatorResponseSHA256 string   `json:"adjudicator_response_sha256"`
	DisclosedArtifactSHA256   []string `json:"disclosed_artifact_sha256"`
	// OrderingEvidence and Limitation are fixed strings, compared against the
	// constants below rather than read as prose, so the limitation travels
	// inside every adjudication artifact and cannot be softened by an edit.
	OrderingEvidence string `json:"ordering_evidence"`
	Limitation       string `json:"limitation"`
}

// The two fixed strings a DisclosureRecord must carry.
const (
	// DisclosureOrderingEvidence names what the artifacts actually establish.
	DisclosureOrderingEvidence = "this record names the adjudicator response's content address and the content address of every primary response for the query and of every grade bound to those responses; a record written before those artifacts existed could not name their digests"

	// DisclosureLimitation names what they do NOT establish. It is the honest
	// counterpart of the removed timestamp: no artifact this slice produces
	// can show that no primary material reached the adjudicator out of band.
	DisclosureLimitation = "these artifacts do NOT establish that no primary response or primary grade reached the adjudicator by another route; adjudicator blindness here is instruction-enforced, not artifact-enforced, and no mechanism in this slice evidences it"
)

// Adjudication is one adjudicated query.
type Adjudication struct {
	QueryID    string           `json:"query_id"`
	Response   RaterResponse    `json:"response"`
	Disclosure DisclosureRecord `json:"disclosure"`
}

// ---------------------------------------------------------------------------
// Ordering checks
// ---------------------------------------------------------------------------

// EvaluationArtifacts is everything the decision procedure reads. Keeping it
// one value means every check below sees the same set; there is no partial
// mode where a check runs over a subset and reports green for the rest.
type EvaluationArtifacts struct {
	Precondition    PreconditionRecord
	PreRegistration PreRegistration
	Responses       []RaterResponse
	Grades          []Grade
	Adjudications   []Adjudication
	// CaptureProvenance is the record the capture instrument wrote. It is NOT
	// optional: a run with no evidence of transport, checkout, embedder and
	// candidate binding cannot release, so its absence is a release refusal
	// rather than a missing row in a report.
	CaptureProvenance CandidateCaptureProvenance
	// GradingConcerns are disclosed defects in COUNTED PASSES. They subtract
	// from the reviewed count and never add to it.
	GradingConcerns []GradingConcern
}

// Concern kinds. There is exactly one, and it can only ever subtract.
const (
	// ConcernCountedPassNotSupported discloses that a query counted as a pass
	// is not supported by the bundle-only rule. Disclosing it lowers the
	// corrected count; there is deliberately no concern kind that raises one,
	// because turning a counted failure into a pass is the re-grade loop the
	// append-only seal exists to prevent.
	ConcernCountedPassNotSupported = "counted_pass_not_supported"
)

// GradingConcern is a disclosed defect in a counted pass, recorded beside the
// result instead of being quietly re-graded away.
//
// Silently re-grading is exactly the hole the append-only seal closes, so a
// concern is not a grade: it changes no grade, no response and no query
// outcome. It changes the CORRECTED count, which is published beside the
// reviewed count, and the release decision is taken on whichever is smaller.
type GradingConcern struct {
	QueryID string `json:"query_id"`
	Kind    string `json:"kind"`
	// GradeSHA256 are the grades the concern is raised against. They must
	// resolve to recorded grades for this query, so a concern cannot be raised
	// against material that does not exist.
	GradeSHA256 []string `json:"grade_sha256"`
	Summary     string   `json:"summary"`
	Evidence    []string `json:"evidence"`
	RaisedBy    string   `json:"raised_by"`
	RaisedAt    string   `json:"raised_at"`
}

// checkDeclaredIdentity refuses a response or grade whose recorded identity is
// not the identity that was pre-registered for that slot.
//
// The slot name alone is not an identity: "grader-1" is a label anyone can put
// on a record. Comparing provider and model as well is what makes "a different
// model or actor supplied and re-sealed favourable grades under the declared
// slot" a refusal rather than an accepted record. It cannot prove which model
// actually ran — the participant records say so themselves — but it does pin
// every artifact to one declared, unchanging claim about who ran it.
func checkDeclaredIdentity(what, queryID string, declared Participant, gotID, gotProvider, gotModel string) error {
	if gotID != declared.ID {
		return fmt.Errorf("retrieval %s: %s for query %s names %q, but the pre-registered identity for that slot is %q", QrelBlindSmokeEvaluationName, what, queryID, gotID, declared.ID)
	}
	if gotProvider != declared.Provider {
		return fmt.Errorf("retrieval %s: %s for query %s by %s records provider %q, but %q was pre-registered; the slot name is not the identity", QrelBlindSmokeEvaluationName, what, queryID, declared.ID, gotProvider, declared.Provider)
	}
	if gotModel != declared.Model {
		return fmt.Errorf("retrieval %s: %s for query %s by %s records model %q, but %q was pre-registered; the slot name is not the identity", QrelBlindSmokeEvaluationName, what, queryID, declared.ID, gotModel, declared.Model)
	}
	return nil
}

// checkResponseIdentity resolves a response's declared slot in the
// pre-registration and binds its recorded identity to it.
func checkResponseIdentity(pre PreRegistration, r RaterResponse) error {
	switch r.Role {
	case RaterRolePrimary:
		for _, rater := range pre.PrimaryRaters {
			if rater.ID == r.RaterID {
				return checkDeclaredIdentity("response", r.QueryID, rater, r.RaterID, r.Provider, r.Model)
			}
		}
		return fmt.Errorf("retrieval %s: response for query %s is by %q, who is not a pre-registered primary rater", QrelBlindSmokeEvaluationName, r.QueryID, r.RaterID)
	case RaterRoleAdjudicator:
		return checkDeclaredIdentity("adjudicator response", r.QueryID, pre.Adjudicator, r.RaterID, r.Provider, r.Model)
	default:
		return fmt.Errorf("retrieval %s: response for query %s has role %q", QrelBlindSmokeEvaluationName, r.QueryID, r.Role)
	}
}

// CheckPrecedence proves, from the artifacts alone, that the pre-registration
// preceded every response and that every response was answered from the
// pre-registered inputs. It fails on a response that is missing from the
// pre-registered population, on a pre-registered query with no response from a
// declared rater, and on any response that does not name the pre-registration.
func CheckPrecedence(a EvaluationArtifacts) error {
	pre := a.PreRegistration
	byID := make(map[string]PreRegisteredQuery, len(pre.Queries))
	for _, q := range pre.Queries {
		byID[q.QueryID] = q
	}
	recordedAt, err := time.Parse(timeLayout, pre.RecordedAt)
	if err != nil {
		return fmt.Errorf("retrieval %s: pre-registration recorded_at %q is not %s", QrelBlindSmokeEvaluationName, pre.RecordedAt, timeLayout)
	}
	slots := map[string]bool{}
	for _, r := range a.Responses {
		if err := ValidateRaterResponse(r); err != nil {
			return err
		}
		if r.PreRegistrationSHA256 != pre.SHA256 {
			return fmt.Errorf("retrieval %s: response for query %s by %s names pre-registration %q, but the pre-registration's content address is %q; a response that did not name the pre-registration cannot be shown to postdate it",
				QrelBlindSmokeEvaluationName, r.QueryID, r.RaterID, r.PreRegistrationSHA256, pre.SHA256)
		}
		q, known := byID[r.QueryID]
		if !known {
			return fmt.Errorf("retrieval %s: response for query %q is absent from the pre-registered population", QrelBlindSmokeEvaluationName, r.QueryID)
		}
		if r.QueryTextSHA256 != q.QueryTextSHA256 {
			return fmt.Errorf("retrieval %s: response for query %s was answered from query text %q, not the pre-registered %q", QrelBlindSmokeEvaluationName, r.QueryID, r.QueryTextSHA256, q.QueryTextSHA256)
		}
		if r.BundleSHA256 != q.BundleSHA256 {
			return fmt.Errorf("retrieval %s: response for query %s was answered from bundle %q, not the pre-registered %q", QrelBlindSmokeEvaluationName, r.QueryID, r.BundleSHA256, q.BundleSHA256)
		}
		if q.PromptSHA256 != "" && r.PromptSHA256 != q.PromptSHA256 {
			return fmt.Errorf("retrieval %s: response for query %s was answered from prompt %q, not the pre-registered %q", QrelBlindSmokeEvaluationName, r.QueryID, r.PromptSHA256, q.PromptSHA256)
		}
		if err := checkResponseIdentity(pre, r); err != nil {
			return err
		}
		respondedAt, err := time.Parse(timeLayout, r.RespondedAt)
		if err != nil {
			return err
		}
		if respondedAt.Before(recordedAt) {
			return fmt.Errorf("retrieval %s: response for query %s by %s is stamped %s, which predates the pre-registration at %s", QrelBlindSmokeEvaluationName, r.QueryID, r.RaterID, r.RespondedAt, pre.RecordedAt)
		}
		if r.Role == RaterRolePrimary {
			key := r.QueryID + "\x00" + r.RaterID
			if slots[key] {
				return fmt.Errorf("retrieval %s: query %s has two primary responses from %s", QrelBlindSmokeEvaluationName, r.QueryID, r.RaterID)
			}
			slots[key] = true
		}
	}
	for _, q := range pre.Queries {
		for _, rater := range pre.PrimaryRaters {
			if !slots[q.QueryID+"\x00"+rater.ID] {
				return fmt.Errorf("retrieval %s: query %s has no primary response record from rater %s; an absent attempt must be recorded with status %q, not omitted",
					QrelBlindSmokeEvaluationName, q.QueryID, rater.ID, ResponseStatusMissing)
			}
		}
	}
	return nil
}

// CheckGradeBinding proves every grade resolves to a response that exists, was
// graded after it, and was graded against the frozen rubric. A grade whose
// response hash does not resolve is invalid.
func CheckGradeBinding(a EvaluationArtifacts) error {
	rubric := ""
	for _, input := range a.Precondition.Inputs {
		if input.Role == "grading_rubric" {
			rubric = input.SHA256
		}
	}
	if rubric == "" {
		return fmt.Errorf("retrieval %s: the precondition record froze no grading rubric", QrelBlindSmokeEvaluationName)
	}
	responses := map[string]RaterResponse{}
	for _, r := range a.Responses {
		responses[r.SHA256] = r
	}
	for _, adj := range a.Adjudications {
		responses[adj.Response.SHA256] = adj.Response
	}
	graded := map[string]bool{}
	for _, g := range a.Grades {
		if err := ValidateGrade(g); err != nil {
			return err
		}
		r, ok := responses[g.ResponseSHA256]
		if !ok {
			return fmt.Errorf("retrieval %s: grade for query %s names response %q, which resolves to no recorded response; the grade is invalid", QrelBlindSmokeEvaluationName, g.QueryID, g.ResponseSHA256)
		}
		if r.QueryID != g.QueryID {
			return fmt.Errorf("retrieval %s: grade names query %s but its response is for query %s", QrelBlindSmokeEvaluationName, g.QueryID, r.QueryID)
		}
		if g.RubricSHA256 != rubric {
			return fmt.Errorf("retrieval %s: grade for query %s used rubric %q, not the frozen %q", QrelBlindSmokeEvaluationName, g.QueryID, g.RubricSHA256, rubric)
		}
		if err := checkDeclaredIdentity("grade", g.QueryID, a.PreRegistration.Grader, g.GraderID, g.Provider, g.Model); err != nil {
			return err
		}
		if r.Status != ResponseStatusAnswered {
			return fmt.Errorf("retrieval %s: query %s has a grade for a %s response; a missing, empty or refused response is a failure by rule and is not graded away", QrelBlindSmokeEvaluationName, g.QueryID, r.Status)
		}
		respondedAt, err := time.Parse(timeLayout, r.RespondedAt)
		if err != nil {
			return err
		}
		gradedAt, err := time.Parse(timeLayout, g.GradedAt)
		if err != nil {
			return err
		}
		if gradedAt.Before(respondedAt) {
			return fmt.Errorf("retrieval %s: grade for query %s is stamped %s, which predates the response it grades at %s", QrelBlindSmokeEvaluationName, g.QueryID, g.GradedAt, r.RespondedAt)
		}
		if graded[g.ResponseSHA256] {
			return fmt.Errorf("retrieval %s: response %q is graded twice; a graded response is never re-graded", QrelBlindSmokeEvaluationName, g.ResponseSHA256)
		}
		graded[g.ResponseSHA256] = true
	}
	return nil
}

// CheckAdjudicationOrder proves each adjudicator's response was frozen and
// content-addressed before either primary response or primary grade was
// disclosed to it.
func CheckAdjudicationOrder(a EvaluationArtifacts) error {
	primaryBySHA := map[string]RaterResponse{}
	gradeBySHA := map[string]Grade{}
	primaryByQuery := map[string][]RaterResponse{}
	gradeByResponse := map[string]Grade{}
	preByQuery := map[string]PreRegisteredQuery{}
	for _, q := range a.PreRegistration.Queries {
		preByQuery[q.QueryID] = q
	}
	for _, r := range a.Responses {
		if r.Role == RaterRolePrimary {
			primaryBySHA[r.SHA256] = r
			primaryByQuery[r.QueryID] = append(primaryByQuery[r.QueryID], r)
		}
	}
	for _, g := range a.Grades {
		gradeBySHA[g.SHA256] = g
		gradeByResponse[g.ResponseSHA256] = g
	}
	seen := map[string]bool{}
	for _, adj := range a.Adjudications {
		if seen[adj.QueryID] {
			return fmt.Errorf("retrieval %s: query %s is adjudicated twice", QrelBlindSmokeEvaluationName, adj.QueryID)
		}
		seen[adj.QueryID] = true
		if err := ValidateRaterResponse(adj.Response); err != nil {
			return err
		}
		if adj.Response.Role != RaterRoleAdjudicator {
			return fmt.Errorf("retrieval %s: query %s adjudication carries a %s response", QrelBlindSmokeEvaluationName, adj.QueryID, adj.Response.Role)
		}
		if adj.Response.QueryID != adj.QueryID || adj.Disclosure.QueryID != adj.QueryID {
			return fmt.Errorf("retrieval %s: query %s adjudication mixes query ids", QrelBlindSmokeEvaluationName, adj.QueryID)
		}
		if adj.Response.RaterID != a.PreRegistration.Adjudicator.ID {
			return fmt.Errorf("retrieval %s: query %s was adjudicated by %s, not the pre-registered adjudicator %s", QrelBlindSmokeEvaluationName, adj.QueryID, adj.Response.RaterID, a.PreRegistration.Adjudicator.ID)
		}
		if err := checkDeclaredIdentity("adjudicator response", adj.QueryID, a.PreRegistration.Adjudicator, adj.Response.RaterID, adj.Response.Provider, adj.Response.Model); err != nil {
			return err
		}
		// The adjudicator answers from the SAME pre-registered inputs as the
		// primaries. Without this the adjudicator response bypassed every
		// input comparison CheckPrecedence applies, so a third opinion could
		// have been taken over a different bundle, a different question or a
		// different prompt from the one the panel disagreed about.
		prq, known := preByQuery[adj.QueryID]
		if !known {
			return fmt.Errorf("retrieval %s: query %s is adjudicated but absent from the pre-registered population", QrelBlindSmokeEvaluationName, adj.QueryID)
		}
		if adj.Response.PreRegistrationSHA256 != a.PreRegistration.SHA256 {
			return fmt.Errorf("retrieval %s: query %s adjudicator response names pre-registration %q, but the pre-registration's content address is %q", QrelBlindSmokeEvaluationName, adj.QueryID, adj.Response.PreRegistrationSHA256, a.PreRegistration.SHA256)
		}
		if adj.Response.QueryTextSHA256 != prq.QueryTextSHA256 {
			return fmt.Errorf("retrieval %s: query %s was adjudicated from query text %q, not the pre-registered %q", QrelBlindSmokeEvaluationName, adj.QueryID, adj.Response.QueryTextSHA256, prq.QueryTextSHA256)
		}
		if adj.Response.BundleSHA256 != prq.BundleSHA256 {
			return fmt.Errorf("retrieval %s: query %s was adjudicated from bundle %q, not the pre-registered %q", QrelBlindSmokeEvaluationName, adj.QueryID, adj.Response.BundleSHA256, prq.BundleSHA256)
		}
		if prq.PromptSHA256 != "" && adj.Response.PromptSHA256 != prq.PromptSHA256 {
			return fmt.Errorf("retrieval %s: query %s was adjudicated from prompt %q, not the pre-registered %q", QrelBlindSmokeEvaluationName, adj.QueryID, adj.Response.PromptSHA256, prq.PromptSHA256)
		}
		if adj.Disclosure.AdjudicatorResponseSHA256 != adj.Response.SHA256 {
			return fmt.Errorf("retrieval %s: query %s disclosure names response %q but the frozen adjudicator response is %q; the disclosure does not prove it followed the response",
				QrelBlindSmokeEvaluationName, adj.QueryID, adj.Disclosure.AdjudicatorResponseSHA256, adj.Response.SHA256)
		}
		if adj.Disclosure.OrderingEvidence != DisclosureOrderingEvidence {
			return fmt.Errorf("retrieval %s: query %s disclosure does not carry the fixed ordering-evidence statement; the disclosure record states what the digest chain establishes and must not paraphrase it", QrelBlindSmokeEvaluationName, adj.QueryID)
		}
		if adj.Disclosure.Limitation != DisclosureLimitation {
			return fmt.Errorf("retrieval %s: query %s disclosure does not carry the fixed limitation statement; what these artifacts do not establish travels with them", QrelBlindSmokeEvaluationName, adj.QueryID)
		}
		if len(adj.Disclosure.DisclosedArtifactSHA256) == 0 {
			return fmt.Errorf("retrieval %s: query %s discloses nothing; the disclosure record exists to name what was shown after the adjudicator answered", QrelBlindSmokeEvaluationName, adj.QueryID)
		}
		disclosed := map[string]bool{}
		for _, sha := range adj.Disclosure.DisclosedArtifactSHA256 {
			_, isResponse := primaryBySHA[sha]
			_, isGrade := gradeBySHA[sha]
			if !isResponse && !isGrade {
				return fmt.Errorf("retrieval %s: query %s discloses artifact %q, which resolves to no primary response or grade", QrelBlindSmokeEvaluationName, adj.QueryID, sha)
			}
			if disclosed[sha] {
				return fmt.Errorf("retrieval %s: query %s discloses artifact %q twice", QrelBlindSmokeEvaluationName, adj.QueryID, sha)
			}
			disclosed[sha] = true
		}
		// The disclosure must name the GRADES, not only the responses. An
		// adjudication happens on a graded disagreement, so the grades are
		// what actually entered the decision; a disclosure that lists only the
		// two responses leaves the material that produced the disagreement
		// unaccounted for.
		for _, primary := range primaryByQuery[adj.QueryID] {
			if !disclosed[primary.SHA256] {
				return fmt.Errorf("retrieval %s: query %s discloses the adjudication without naming primary response %q; the disclosure must name every primary response for the query it resolves", QrelBlindSmokeEvaluationName, adj.QueryID, primary.SHA256)
			}
			g, graded := gradeByResponse[primary.SHA256]
			if !graded {
				continue
			}
			if !disclosed[g.SHA256] {
				return fmt.Errorf("retrieval %s: query %s discloses the adjudication without naming grade %q of primary response %q; a disagreement is a disagreement between GRADES, and the disclosure must name them", QrelBlindSmokeEvaluationName, adj.QueryID, g.SHA256, primary.SHA256)
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Decision procedure
// ---------------------------------------------------------------------------

// RaterOutcome is one rater's contribution to a query's decision.
type RaterOutcome struct {
	RaterID        string `json:"rater_id"`
	Role           string `json:"role"`
	ResponseSHA256 string `json:"response_sha256"`
	Status         string `json:"status"`
	GradeSHA256    string `json:"grade_sha256,omitempty"`
	Outcome        string `json:"outcome"`
	// Mechanical is true when the outcome came from the missing/empty/refused
	// rule rather than from a grader.
	Mechanical bool `json:"mechanical"`
}

// QueryOutcome is one query's final decision and how it was reached.
type QueryOutcome struct {
	QueryID      string         `json:"query_id"`
	Stratum      string         `json:"stratum"`
	Primary      []RaterOutcome `json:"primary"`
	Disagreement bool           `json:"primary_disagreement"`
	Adjudicated  bool           `json:"adjudicated"`
	Adjudicator  *RaterOutcome  `json:"adjudicator,omitempty"`
	Outcome      string         `json:"outcome"`
	Reason       string         `json:"reason"`
}

// DecideQuery applies the frozen decision procedure to one query.
//
// The rules, in the order they apply:
//
//  1. A missing, empty or refused PRIMARY response is a failure for that rater
//     AND a failure for the query. It is not adjudicated, re-requested or
//     replaced. This is the strict reading of "shall not be adjudicated away":
//     a query the panel could not answer is not rescued by a third opinion.
//  2. Two primary failures are a query failure, and are not adjudicated.
//  3. Two primary passes are a query pass.
//  4. A pass/fail disagreement REQUIRES adjudication; majority of the three
//     graded outcomes decides. An adjudicator response that is missing, empty
//     or refused fails the query by rule 1.
func DecideQuery(a EvaluationArtifacts, q PreRegisteredQuery) (QueryOutcome, error) {
	out := QueryOutcome{QueryID: q.QueryID, Stratum: q.Stratum}
	gradeByResponse := map[string]Grade{}
	for _, g := range a.Grades {
		gradeByResponse[g.ResponseSHA256] = g
	}
	byRater := map[string]RaterResponse{}
	for _, r := range a.Responses {
		if r.QueryID == q.QueryID && r.Role == RaterRolePrimary {
			byRater[r.RaterID] = r
		}
	}
	nonAnswered := 0
	fails := 0
	passes := 0
	for _, rater := range a.PreRegistration.PrimaryRaters {
		r, ok := byRater[rater.ID]
		if !ok {
			return QueryOutcome{}, fmt.Errorf("retrieval %s: query %s has no primary response record from rater %s", QrelBlindSmokeEvaluationName, q.QueryID, rater.ID)
		}
		outcome := RaterOutcome{RaterID: rater.ID, Role: RaterRolePrimary, ResponseSHA256: r.SHA256, Status: r.Status}
		if r.Status != ResponseStatusAnswered {
			outcome.Outcome = GradeOutcomeFail
			outcome.Mechanical = true
			nonAnswered++
			fails++
		} else {
			g, graded := gradeByResponse[r.SHA256]
			if !graded {
				return QueryOutcome{}, fmt.Errorf("retrieval %s: query %s response by %s is answered but ungraded; an ungraded answered response has no outcome", QrelBlindSmokeEvaluationName, q.QueryID, rater.ID)
			}
			outcome.GradeSHA256 = g.SHA256
			outcome.Outcome = g.Outcome
			if g.Outcome == GradeOutcomePass {
				passes++
			} else {
				fails++
			}
		}
		out.Primary = append(out.Primary, outcome)
	}
	var adjudication *Adjudication
	for i := range a.Adjudications {
		if a.Adjudications[i].QueryID == q.QueryID {
			adjudication = &a.Adjudications[i]
			break
		}
	}
	out.Disagreement = passes == 1 && fails == 1 && nonAnswered == 0

	switch {
	case nonAnswered > 0:
		if adjudication != nil {
			return QueryOutcome{}, fmt.Errorf("retrieval %s: query %s has a %d-response adjudication over a missing, empty or refused primary response; such a response is a failure and is not adjudicated away", QrelBlindSmokeEvaluationName, q.QueryID, nonAnswered)
		}
		out.Outcome = GradeOutcomeFail
		out.Reason = fmt.Sprintf("%d of %d primary responses were missing, empty or refused; a non-answered response is a failure and is not adjudicated, re-requested or replaced", nonAnswered, len(out.Primary))
		return out, nil
	case fails == len(out.Primary):
		if adjudication != nil {
			return QueryOutcome{}, fmt.Errorf("retrieval %s: query %s was adjudicated although both primary grades failed; two primary failures are a query failure", QrelBlindSmokeEvaluationName, q.QueryID)
		}
		out.Outcome = GradeOutcomeFail
		out.Reason = "both primary grades failed; two primary failures are a query failure and are not adjudicated"
		return out, nil
	case passes == len(out.Primary):
		if adjudication != nil {
			return QueryOutcome{}, fmt.Errorf("retrieval %s: query %s was adjudicated although both primary grades passed; adjudication occurs only on a disagreement", QrelBlindSmokeEvaluationName, q.QueryID)
		}
		out.Outcome = GradeOutcomePass
		out.Reason = "both primary grades passed"
		return out, nil
	}

	if adjudication == nil {
		return QueryOutcome{}, fmt.Errorf("retrieval %s: query %s has a primary pass/fail disagreement and no adjudication; a disagreement is not resolved by choosing one side", QrelBlindSmokeEvaluationName, q.QueryID)
	}
	out.Adjudicated = true
	adjOutcome := RaterOutcome{
		RaterID:        adjudication.Response.RaterID,
		Role:           RaterRoleAdjudicator,
		ResponseSHA256: adjudication.Response.SHA256,
		Status:         adjudication.Response.Status,
	}
	if adjudication.Response.Status != ResponseStatusAnswered {
		adjOutcome.Outcome = GradeOutcomeFail
		adjOutcome.Mechanical = true
		out.Adjudicator = &adjOutcome
		out.Outcome = GradeOutcomeFail
		out.Reason = fmt.Sprintf("the adjudicator response was %s, which is a failure by rule", adjudication.Response.Status)
		return out, nil
	}
	g, graded := gradeByResponse[adjudication.Response.SHA256]
	if !graded {
		return QueryOutcome{}, fmt.Errorf("retrieval %s: query %s adjudicator response is ungraded", QrelBlindSmokeEvaluationName, q.QueryID)
	}
	adjOutcome.GradeSHA256 = g.SHA256
	adjOutcome.Outcome = g.Outcome
	out.Adjudicator = &adjOutcome
	if g.Outcome == GradeOutcomePass {
		passes++
	} else {
		fails++
	}
	if passes > fails {
		out.Outcome = GradeOutcomePass
	} else {
		out.Outcome = GradeOutcomeFail
	}
	out.Reason = fmt.Sprintf("primary raters disagreed; majority of the three graded outcomes is %d pass / %d fail", passes, fails)
	return out, nil
}

// StratumCount renders one stratum's authoritative counts. Percentages are
// deliberately absent: SW-274's display rules make counts authoritative, and a
// renderer that wants a percentage must state the 1/n resolution beside it.
type StratumCount struct {
	Stratum    string `json:"stratum"`
	Passed     int    `json:"passed"`
	Total      int    `json:"total"`
	Rendered   string `json:"rendered"`
	Resolution string `json:"resolution"`
}

// EvaluationOutcome is the published result. Every number in it is recomputed
// by ValidateEvaluationOutcome from the artifacts, so a hand-edited field
// cannot survive review.
type EvaluationOutcome struct {
	ContractVersion       string                `json:"contract_version"`
	Evaluation            string                `json:"evaluation"`
	PreconditionSHA256    string                `json:"precondition_record_sha256"`
	PreRegistrationSHA256 string                `json:"pre_registration_sha256"`
	N                     int                   `json:"n"`
	K                     int                   `json:"k"`
	PassCount             int                   `json:"pass_count"`
	Interval              ExactBinomialInterval `json:"observed_interval"`
	DisagreementCount     int                   `json:"primary_disagreement_count"`
	AdjudicationCount     int                   `json:"adjudication_count"`
	NonAnsweredCount      int                   `json:"non_answered_response_count"`
	PerStratum            []StratumCount        `json:"per_stratum"`
	Queries               []QueryOutcome        `json:"queries"`
	Participants          []Participant         `json:"participants"`
	EndOfRunComparison    HashComparisonResult  `json:"end_of_run_hash_comparison"`
	// CaptureBinding says whether the bytes the raters saw are bound to the
	// candidate implementation and the indexed checkout they were recorded
	// against. An unbound capture cannot release.
	CaptureBinding CaptureBindingAssessment `json:"capture_binding"`
	// DisclosedGradingConcerns are counted passes disclosed as unsupported,
	// and CorrectedPassCount is the reviewed count minus those queries. The
	// release is taken on the SMALLER of the two counts.
	DisclosedGradingConcerns []GradingConcern `json:"disclosed_grading_concerns"`
	CorrectedPassCount       int              `json:"corrected_pass_count"`
	Release                  string           `json:"release"`
	Reasons                  []string         `json:"reasons"`
}

// CaptureBindingAssessment records whether the capture provenance binds this
// run's bytes to the candidate and checkout it names.
//
// It exists because provenance used to be optional: a run that read no
// provenance at all, or whose provenance failed to parse, could still publish
// RELEASE: YES. Evidence that is optional on the passing path is not evidence,
// so an unbound or unprovenanced capture is now a release refusal, recorded
// with its reasons rather than left as an empty section of a report.
type CaptureBindingAssessment struct {
	Bound          bool     `json:"bound"`
	CaptureVersion string   `json:"capture_version"`
	CandidateSHA   string   `json:"candidate_sha,omitempty"`
	CheckoutSHA    string   `json:"checkout_sha,omitempty"`
	Reasons        []string `json:"reasons,omitempty"`
}

// AssessCaptureBinding decides whether a capture provenance binds the run.
func AssessCaptureBinding(precondition PreconditionRecord, p CandidateCaptureProvenance) CaptureBindingAssessment {
	assessment := CaptureBindingAssessment{CaptureVersion: p.CaptureVersion}
	if strings.TrimSpace(p.CaptureVersion) == "" {
		assessment.Reasons = append(assessment.Reasons, "no capture provenance was recorded: there is no evidence of the transport, the indexed checkout, the embedder or the candidate the rated bytes came from")
		return assessment
	}
	for _, field := range []struct{ name, value string }{
		{"transport", p.Transport},
		{"boundary", p.Boundary},
		{"repo_sha", p.RepoSHA},
		{"embedder_selector", p.EmbedderSelector},
		{"index_fingerprint", p.IndexFingerprint},
		{"semantic_state", p.SemanticState},
	} {
		if strings.TrimSpace(field.value) == "" {
			assessment.Reasons = append(assessment.Reasons, "the capture provenance records no "+field.name)
		}
	}
	if p.DatasetSHA256 != precondition.DatasetSHA256 {
		assessment.Reasons = append(assessment.Reasons, fmt.Sprintf("the capture ran over dataset %s, but %s was frozen", p.DatasetSHA256, precondition.DatasetSHA256))
	}
	if p.TokenBudget != precondition.CandidateTokenBudget {
		assessment.Reasons = append(assessment.Reasons, fmt.Sprintf("the capture ran at a %d-token budget, but %d was frozen", p.TokenBudget, precondition.CandidateTokenBudget))
	}
	if !isLowerHexDigest(p.RepoSHA, 40) {
		assessment.Reasons = append(assessment.Reasons, fmt.Sprintf("the capture provenance records repo_sha %q, which is not a 40-character commit id", p.RepoSHA))
	}
	binding := p.Binding
	if binding == nil {
		assessment.Reasons = append(assessment.Reasons,
			"the capture recorded no candidate binding: neither the candidate implementation nor the indexed checkout is bound to the commit this run names, so a worktree modified without committing — one that makes retrieval return the expected answers — would leave this report saying the frozen candidate produced these bytes")
		return assessment
	}
	assessment.CandidateSHA = binding.CandidateSHA
	assessment.CheckoutSHA = binding.CheckoutSHA
	// The booleans below are the binding's CONCLUSIONS, and they were trusted
	// on sight. A hand-written binding naming candidate_sha "not-a-sha" and an
	// unrelated excluded path was accepted as bound, which made the whole
	// binding a claim the run makes about itself. So the fields are checked for
	// what they must BE before their booleans are read: a commit id is 40 hex
	// characters, and the excluded path is not free text — it is the one
	// directory this run was frozen into, and the precondition record already
	// names it, because the grading rubric it froze lives there.
	for _, id := range []struct{ name, value string }{
		{"candidate_sha", binding.CandidateSHA},
		{"frozen_candidate_sha", binding.FrozenCandidateSHA},
		{"checkout_sha", binding.CheckoutSHA},
	} {
		if !isLowerHexDigest(id.value, 40) {
			assessment.Reasons = append(assessment.Reasons, fmt.Sprintf("the candidate binding records %s %q, which is not a 40-character commit id; a binding that does not name a commit binds nothing", id.name, id.value))
		}
	}
	frozenRunDir, err := RunDirectoryFromPreconditionRecord(precondition)
	switch {
	case err != nil:
		assessment.Reasons = append(assessment.Reasons, err.Error())
	case binding.CandidateExcludedPath != frozenRunDir:
		assessment.Reasons = append(assessment.Reasons, fmt.Sprintf("the candidate binding excluded %q from the comparison against the frozen candidate, and this run was frozen into %q; the excluded path is the only place the candidate tree is allowed to differ, so an excluded path that is not this run's directory is a hole of the operator's own choosing",
			binding.CandidateExcludedPath, frozenRunDir))
	}
	if !binding.CandidateWorktreeClean {
		assessment.Reasons = append(assessment.Reasons, "the candidate worktree was not clean at capture; uncommitted candidate code is not the candidate this run froze")
	}
	if !binding.CheckoutWorktreeClean {
		assessment.Reasons = append(assessment.Reasons, "the indexed checkout's worktree was not clean at capture; an uncommitted edit to the indexed repository is not the pinned corpus")
	}
	if !binding.CandidateMatchesFrozen {
		assessment.Reasons = append(assessment.Reasons, fmt.Sprintf("the candidate tree at capture (%s) differs outside the run directory from the frozen candidate %s", binding.CandidateSHA, precondition.CandidateSHA))
	}
	if binding.FrozenCandidateSHA != precondition.CandidateSHA {
		assessment.Reasons = append(assessment.Reasons, fmt.Sprintf("the capture compared against candidate %s, but the precondition record froze %s", binding.FrozenCandidateSHA, precondition.CandidateSHA))
	}
	if binding.CheckoutSHA != p.RepoSHA {
		assessment.Reasons = append(assessment.Reasons, fmt.Sprintf("the binding names checkout %s but the provenance names %s", binding.CheckoutSHA, p.RepoSHA))
	}
	assessment.Bound = len(assessment.Reasons) == 0
	return assessment
}

// ValidateGradingConcerns refuses a concern that names material which does not
// exist, or that is raised against anything but a counted pass.
func ValidateGradingConcerns(concerns []GradingConcern, a EvaluationArtifacts, decided map[string]string) error {
	gradeByQuery := map[string]map[string]bool{}
	for _, g := range a.Grades {
		if gradeByQuery[g.QueryID] == nil {
			gradeByQuery[g.QueryID] = map[string]bool{}
		}
		gradeByQuery[g.QueryID][g.SHA256] = true
	}
	seen := map[string]bool{}
	for _, c := range concerns {
		if c.Kind != ConcernCountedPassNotSupported {
			return fmt.Errorf("retrieval %s: disclosed concern for query %s has kind %q; the only permitted kind is %q, because a concern may only ever subtract from the count", QrelBlindSmokeEvaluationName, c.QueryID, c.Kind, ConcernCountedPassNotSupported)
		}
		if seen[c.QueryID] {
			return fmt.Errorf("retrieval %s: query %s is disclosed twice; one query subtracts at most one pass", QrelBlindSmokeEvaluationName, c.QueryID)
		}
		seen[c.QueryID] = true
		if decided[c.QueryID] != GradeOutcomePass {
			return fmt.Errorf("retrieval %s: a concern is disclosed against query %s, which was not counted as a pass; a concern discloses an unsupported PASS and never converts a failure", QrelBlindSmokeEvaluationName, c.QueryID)
		}
		for _, field := range []struct{ name, value string }{
			{"summary", c.Summary},
			{"raised_by", c.RaisedBy},
			{"raised_at", c.RaisedAt},
		} {
			if strings.TrimSpace(field.value) == "" {
				return fmt.Errorf("retrieval %s: disclosed concern for query %s has no %s", QrelBlindSmokeEvaluationName, c.QueryID, field.name)
			}
		}
		if _, err := time.Parse(timeLayout, c.RaisedAt); err != nil {
			return fmt.Errorf("retrieval %s: disclosed concern for query %s has raised_at %q, not %s", QrelBlindSmokeEvaluationName, c.QueryID, c.RaisedAt, timeLayout)
		}
		if len(c.Evidence) == 0 {
			return fmt.Errorf("retrieval %s: disclosed concern for query %s cites no evidence", QrelBlindSmokeEvaluationName, c.QueryID)
		}
		if len(c.GradeSHA256) == 0 {
			return fmt.Errorf("retrieval %s: disclosed concern for query %s names no grade", QrelBlindSmokeEvaluationName, c.QueryID)
		}
		for _, sha := range c.GradeSHA256 {
			if !gradeByQuery[c.QueryID][sha] {
				return fmt.Errorf("retrieval %s: disclosed concern for query %s names grade %q, which resolves to no grade of that query", QrelBlindSmokeEvaluationName, c.QueryID, sha)
			}
		}
	}
	return nil
}

// EvaluateQrelBlindSmoke applies the decision procedure to every pre-registered query and
// produces the outcome. It never partially evaluates: a query it cannot decide
// is an error, not a skipped row.
func EvaluateQrelBlindSmoke(a EvaluationArtifacts, comparison HashComparisonResult) (EvaluationOutcome, error) {
	if err := ValidatePreconditionRecord(a.Precondition); err != nil {
		return EvaluationOutcome{}, err
	}
	if err := ValidatePreRegistration(a.PreRegistration); err != nil {
		return EvaluationOutcome{}, err
	}
	if a.PreRegistration.PreconditionSHA256 != a.Precondition.SHA256 {
		return EvaluationOutcome{}, fmt.Errorf("retrieval %s: the pre-registration names precondition %q but the supplied record addresses to %q", QrelBlindSmokeEvaluationName, a.PreRegistration.PreconditionSHA256, a.Precondition.SHA256)
	}
	if err := CheckPrecedence(a); err != nil {
		return EvaluationOutcome{}, err
	}
	if err := CheckGradeBinding(a); err != nil {
		return EvaluationOutcome{}, err
	}
	if err := CheckAdjudicationOrder(a); err != nil {
		return EvaluationOutcome{}, err
	}

	out := EvaluationOutcome{
		ContractVersion:       QrelBlindSmokeContractVersion,
		Evaluation:            QrelBlindSmokeEvaluationName,
		PreconditionSHA256:    a.Precondition.SHA256,
		PreRegistrationSHA256: a.PreRegistration.SHA256,
		N:                     a.PreRegistration.Derivation.N,
		K:                     a.PreRegistration.Derivation.K,
		EndOfRunComparison:    comparison,
	}
	perStratum := map[string]*StratumCount{}
	var strata []string
	for _, q := range a.PreRegistration.Queries {
		decision, err := DecideQuery(a, q)
		if err != nil {
			return EvaluationOutcome{}, err
		}
		out.Queries = append(out.Queries, decision)
		if decision.Disagreement {
			out.DisagreementCount++
		}
		if decision.Adjudicated {
			out.AdjudicationCount++
		}
		for _, p := range decision.Primary {
			if p.Status != ResponseStatusAnswered {
				out.NonAnsweredCount++
			}
		}
		counts, known := perStratum[q.Stratum]
		if !known {
			counts = &StratumCount{Stratum: q.Stratum}
			perStratum[q.Stratum] = counts
			strata = append(strata, q.Stratum)
		}
		counts.Total++
		if decision.Outcome == GradeOutcomePass {
			counts.Passed++
			out.PassCount++
		}
	}
	sort.Strings(strata)
	for _, name := range strata {
		counts := perStratum[name]
		counts.Rendered = fmt.Sprintf("%d/%d", counts.Passed, counts.Total)
		counts.Resolution = fmt.Sprintf("1/%d", counts.Total)
		out.PerStratum = append(out.PerStratum, *counts)
	}
	interval, err := ClopperPearsonInterval(out.PassCount, out.N)
	if err != nil {
		return EvaluationOutcome{}, err
	}
	out.Interval = interval
	out.Participants = append(out.Participants, a.PreRegistration.PrimaryRaters...)
	out.Participants = append(out.Participants, a.PreRegistration.Grader, a.PreRegistration.Adjudicator)

	decided := make(map[string]string, len(out.Queries))
	for _, q := range out.Queries {
		decided[q.QueryID] = q.Outcome
	}
	if err := ValidateGradingConcerns(a.GradingConcerns, a, decided); err != nil {
		return EvaluationOutcome{}, err
	}
	out.DisclosedGradingConcerns = append(out.DisclosedGradingConcerns, a.GradingConcerns...)
	out.CorrectedPassCount = out.PassCount - len(a.GradingConcerns)
	out.CaptureBinding = AssessCaptureBinding(a.Precondition, a.CaptureProvenance)

	// The release is taken on the SMALLER of the reviewed and corrected
	// counts, so disclosing a concern can only ever move the decision towards
	// NO. There is no arithmetic here that can move it the other way.
	effective := out.PassCount
	if out.CorrectedPassCount < effective {
		effective = out.CorrectedPassCount
	}
	if effective < out.K {
		// Name the count that actually fell short. This used to say "%d of %d
		// queries passed" with the REVIEWED count, which is a false statement
		// of the reason whenever the reviewed count reaches k and only the
		// corrected count does not — exactly the case a disclosed concern
		// creates, and exactly the case a reader most needs to understand.
		decisive := "reviewed"
		if out.CorrectedPassCount < out.PassCount {
			decisive = "corrected"
		}
		out.Release = ReleaseNo
		out.Reasons = append(out.Reasons, fmt.Sprintf("the %s pass count is %d of %d, below the pre-registered k=%d (reviewed %d, corrected %d, and the release is decided on the smaller); there is no override, exception or waiver",
			decisive, effective, out.N, out.K, out.PassCount, out.CorrectedPassCount))
	}
	if len(out.DisclosedGradingConcerns) > 0 {
		out.Reasons = append(out.Reasons, fmt.Sprintf("%d counted pass(es) are disclosed as unsupported by the bundle-only rule; the reviewed count is %d of %d and the corrected count is %d of %d, and the release is decided on the smaller of the two", len(out.DisclosedGradingConcerns), out.PassCount, out.N, out.CorrectedPassCount, out.N))
	}
	if !comparison.AllMatch {
		out.Release = ReleaseNo
		out.Reasons = append(out.Reasons, "at least one frozen input's hash differs at the end of the run from its value in the precondition record")
	}
	if !out.CaptureBinding.Bound {
		out.Release = ReleaseNo
		for _, reason := range out.CaptureBinding.Reasons {
			out.Reasons = append(out.Reasons, "the capture is not bound: "+reason)
		}
	}
	if out.Release == "" {
		out.Release = ReleaseYes
		out.Reasons = append(out.Reasons, fmt.Sprintf("%d of %d queries passed, at or above the pre-registered k=%d, every frozen input hash was unchanged at the end of the run, and the capture is bound to the frozen candidate and the pinned checkout", out.PassCount, out.N, out.K))
	}
	return out, nil
}

// UnsatisfiableOutcome is the outcome record for a population no k can serve.
//
// AC-4 says the evaluation "shall record RELEASE: NO with that reason", and a
// line on stderr is not a record: a caller that finds no outcome.json cannot
// tell the mandatory refusal from a run that crashed, was interrupted, or was
// never started. This writes the refusal down. It carries no queries, no
// participants and no pass count, because none of those exist yet.
func UnsatisfiableOutcome(precondition PreconditionRecord, n int, reason error) EvaluationOutcome {
	return EvaluationOutcome{
		ContractVersion:    QrelBlindSmokeContractVersion,
		Evaluation:         QrelBlindSmokeEvaluationName,
		PreconditionSHA256: precondition.SHA256,
		N:                  n,
		Release:            ReleaseNo,
		Reasons: []string{
			reason.Error(),
			"no minimum passing count exists for this population, so the evaluation did not run; k is never clamped to N and there is no best-available mode",
		},
	}
}

// ValidateEvaluationOutcome recomputes every published number from the
// artifacts. It is the executable form of "there is no override": k is
// re-derived from N, the pass count is recounted, and the release result is
// recomputed from the comparison.
func ValidateEvaluationOutcome(got EvaluationOutcome, a EvaluationArtifacts) error {
	want, err := EvaluateQrelBlindSmoke(a, got.EndOfRunComparison)
	if err != nil {
		return err
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		return err
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		return err
	}
	if string(gotJSON) != string(wantJSON) {
		return fmt.Errorf("retrieval %s: the recorded outcome does not recompute from its own artifacts; k, the pass count and the release result are derived, never written", QrelBlindSmokeEvaluationName)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Content addressing
// ---------------------------------------------------------------------------

// ContentAddress computes the SHA-256 of a record's canonical JSON with its own
// self-referential digest field cleared. clear is the only way to name that
// field, so a record type cannot accidentally hash its own hash.
func ContentAddress[T any](v T, clear func(*T)) (string, error) {
	copied := v
	clear(&copied)
	encoded, err := json.Marshal(copied)
	if err != nil {
		return "", fmt.Errorf("retrieval %s: content address: %w", QrelBlindSmokeEvaluationName, err)
	}
	return SHA256Hex(encoded), nil
}

// SealPreconditionRecord fills in the record's own content address.
func SealPreconditionRecord(rec PreconditionRecord) (PreconditionRecord, error) {
	address, err := ContentAddress(rec, func(v *PreconditionRecord) { v.SHA256 = "" })
	if err != nil {
		return PreconditionRecord{}, err
	}
	rec.SHA256 = address
	return rec, nil
}

// SealPreRegistration fills in the record's own content address.
func SealPreRegistration(pre PreRegistration) (PreRegistration, error) {
	address, err := ContentAddress(pre, func(v *PreRegistration) { v.SHA256 = "" })
	if err != nil {
		return PreRegistration{}, err
	}
	pre.SHA256 = address
	return pre, nil
}

// SealRaterResponse fills in the response's own content address. This is the
// "content-addressed before grading" step: a response is sealed the moment it
// is recorded, and a grade names the sealed address.
func SealRaterResponse(r RaterResponse) (RaterResponse, error) {
	address, err := ContentAddress(r, func(v *RaterResponse) { v.SHA256 = "" })
	if err != nil {
		return RaterResponse{}, err
	}
	r.SHA256 = address
	return r, nil
}

// SealGrade fills in the grade's own content address.
func SealGrade(g Grade) (Grade, error) {
	address, err := ContentAddress(g, func(v *Grade) { v.SHA256 = "" })
	if err != nil {
		return Grade{}, err
	}
	g.SHA256 = address
	return g, nil
}
