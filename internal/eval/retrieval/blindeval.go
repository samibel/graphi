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
	QueryID           string              `json:"query_id"`
	FamilyID          string              `json:"family_id"`
	Stratum           string              `json:"stratum"`
	QueryTextSHA256   string              `json:"query_text_sha256"`
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
type DisclosureRecord struct {
	QueryID                   string   `json:"query_id"`
	AdjudicatorResponseSHA256 string   `json:"adjudicator_response_sha256"`
	DisclosedArtifactSHA256   []string `json:"disclosed_artifact_sha256"`
	DisclosedAt               string   `json:"disclosed_at"`
}

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
	for _, r := range a.Responses {
		if r.Role == RaterRolePrimary {
			primaryBySHA[r.SHA256] = r
		}
	}
	for _, g := range a.Grades {
		gradeBySHA[g.SHA256] = g
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
		if adj.Disclosure.AdjudicatorResponseSHA256 != adj.Response.SHA256 {
			return fmt.Errorf("retrieval %s: query %s disclosure names response %q but the frozen adjudicator response is %q; the disclosure does not prove it followed the response",
				QrelBlindSmokeEvaluationName, adj.QueryID, adj.Disclosure.AdjudicatorResponseSHA256, adj.Response.SHA256)
		}
		respondedAt, err := time.Parse(timeLayout, adj.Response.RespondedAt)
		if err != nil {
			return err
		}
		disclosedAt, err := time.Parse(timeLayout, adj.Disclosure.DisclosedAt)
		if err != nil {
			return fmt.Errorf("retrieval %s: query %s disclosed_at %q is not %s", QrelBlindSmokeEvaluationName, adj.QueryID, adj.Disclosure.DisclosedAt, timeLayout)
		}
		if disclosedAt.Before(respondedAt) {
			return fmt.Errorf("retrieval %s: query %s disclosure is stamped %s, which predates the adjudicator response at %s", QrelBlindSmokeEvaluationName, adj.QueryID, adj.Disclosure.DisclosedAt, adj.Response.RespondedAt)
		}
		if len(adj.Disclosure.DisclosedArtifactSHA256) == 0 {
			return fmt.Errorf("retrieval %s: query %s discloses nothing; the disclosure record exists to prove what was shown after the adjudicator answered", QrelBlindSmokeEvaluationName, adj.QueryID)
		}
		for _, sha := range adj.Disclosure.DisclosedArtifactSHA256 {
			_, isResponse := primaryBySHA[sha]
			_, isGrade := gradeBySHA[sha]
			if !isResponse && !isGrade {
				return fmt.Errorf("retrieval %s: query %s discloses artifact %q, which resolves to no primary response or grade", QrelBlindSmokeEvaluationName, adj.QueryID, sha)
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
	Release               string                `json:"release"`
	Reasons               []string              `json:"reasons"`
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

	if out.PassCount < out.K {
		out.Release = ReleaseNo
		out.Reasons = append(out.Reasons, fmt.Sprintf("%d of %d queries passed, below the pre-registered k=%d; there is no override, exception or waiver", out.PassCount, out.N, out.K))
	}
	if !comparison.AllMatch {
		out.Release = ReleaseNo
		out.Reasons = append(out.Reasons, "at least one frozen input's hash differs at the end of the run from its value in the precondition record")
	}
	if out.Release == "" {
		out.Release = ReleaseYes
		out.Reasons = append(out.Reasons, fmt.Sprintf("%d of %d queries passed, at or above the pre-registered k=%d, and every frozen input hash was unchanged at the end of the run", out.PassCount, out.N, out.K))
	}
	return out, nil
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
