package retrieval

// Release-evidence manifest validation is intentionally separate from the
// current target checker. It is a prototype of the eventual release seam, not
// a new pointer to evidence and not a new release decision.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// ReleaseEvidenceManifestVersion pins the closed manifest shape and the
	// exact identity-role universe understood by this validator.
	ReleaseEvidenceManifestVersion = "retrieval-release-evidence/1"

	// ReleaseSavingsEvidenceVersion binds the independently content-addressed
	// SavingsAggregateResult to the candidate commit that produced its
	// task_context/2 payloads. SavingsAggregateInput intentionally identifies
	// the candidate method version, not a git commit, so this outer binding is
	// required rather than inferred.
	ReleaseSavingsEvidenceVersion = "retrieval-release-savings-evidence/1"
)

// ReleaseEvidenceFile keeps the artifact location distinct from the digest
// expected at that location. A path is never treated as an identity.
type ReleaseEvidenceFile struct {
	Path           string `json:"path"`
	ExpectedSHA256 string `json:"expected_sha256"`
}

// ReleaseEvidenceIdentity is one frozen input identity. Name belongs to a
// closed set for this manifest version; Path and ExpectedSHA256 bind its exact
// bytes.
type ReleaseEvidenceIdentity struct {
	Name string `json:"name"`
	ReleaseEvidenceFile
}

// ReleaseEvidenceManifest binds one clean candidate commit to every artifact
// and frozen input a later release decision must use. SHA256 is the manifest's
// content address with this field cleared.
type ReleaseEvidenceManifest struct {
	Version           string                    `json:"version"`
	CandidateSHA      string                    `json:"candidate_sha"`
	RankingReport     ReleaseEvidenceFile       `json:"ranking_report"`
	BundleCoverage    ReleaseEvidenceFile       `json:"bundle_coverage"`
	BlindSmokeOutcome ReleaseEvidenceFile       `json:"blind_smoke_outcome"`
	SavingsAggregate  ReleaseEvidenceFile       `json:"savings_aggregate"`
	Identities        []ReleaseEvidenceIdentity `json:"identities"`
	SHA256            string                    `json:"sha256"`
}

// ReleaseEvidenceAssessment is returned only after every named byte stream,
// artifact shape, target and cross-artifact binding validates. ReleaseReady is
// the result of that closed conjunction; callers do not reconstruct it.
type ReleaseEvidenceAssessment struct {
	ManifestSHA256    string   `json:"manifest_sha256"`
	CandidateSHA      string   `json:"candidate_sha"`
	FilesChecked      int      `json:"files_checked"`
	CandidateBindings []string `json:"candidate_bindings"`
	ReleaseReady      bool     `json:"release_ready"`
}

// ReleaseSavingsEvidence supplies the git identity intentionally absent from
// SavingsAggregateInput. Result remains self-contained and is fully
// recomputed by ValidateSavingsAggregateResult.
type ReleaseSavingsEvidence struct {
	Version      string                 `json:"version"`
	CandidateSHA string                 `json:"candidate_sha"`
	Result       SavingsAggregateResult `json:"result"`
}

var requiredReleaseIdentityNames = []string{
	"budgets",
	"development_dataset",
	"grading_rubric",
	"methodology",
	"release_dataset",
	"targets",
	"tokenizer_vocabulary",
}

// SealReleaseEvidenceManifest returns a copy carrying its content address.
func SealReleaseEvidenceManifest(manifest ReleaseEvidenceManifest) (ReleaseEvidenceManifest, error) {
	digest, err := ContentAddress(manifest, func(v *ReleaseEvidenceManifest) { v.SHA256 = "" })
	if err != nil {
		return ReleaseEvidenceManifest{}, fmt.Errorf("retrieval release evidence: address manifest: %w", err)
	}
	manifest.SHA256 = digest
	return manifest, nil
}

// ValidateReleaseEvidence validates one manifest against files under root.
// It fails on missing evidence, digest drift, unknown shapes and inconsistent
// candidate commits. counters are executable tokenizer identities; claimed
// counts without the matching executables are rejected by the savings module.
func ValidateReleaseEvidence(root string, manifest ReleaseEvidenceManifest, counters map[string]PayloadCounter) (ReleaseEvidenceAssessment, error) {
	assessment := ReleaseEvidenceAssessment{CandidateSHA: manifest.CandidateSHA}
	if manifest.Version != ReleaseEvidenceManifestVersion {
		return assessment, fmt.Errorf("retrieval release evidence: manifest version %q is not %q", manifest.Version, ReleaseEvidenceManifestVersion)
	}
	if !isLowerHexDigest(manifest.CandidateSHA, 40) {
		return assessment, fmt.Errorf("retrieval release evidence: candidate_sha must be one clean 40-character lowercase commit digest")
	}
	wantAddress, err := ContentAddress(manifest, func(v *ReleaseEvidenceManifest) { v.SHA256 = "" })
	if err != nil {
		return assessment, fmt.Errorf("retrieval release evidence: address manifest: %w", err)
	}
	if manifest.SHA256 != wantAddress {
		return assessment, fmt.Errorf("retrieval release evidence: manifest sha256 %q does not match its content address %q", manifest.SHA256, wantAddress)
	}
	assessment.ManifestSHA256 = wantAddress

	refs := []struct {
		role string
		ref  ReleaseEvidenceFile
	}{
		{"ranking_report", manifest.RankingReport},
		{"bundle_coverage", manifest.BundleCoverage},
		{"blind_smoke_outcome", manifest.BlindSmokeOutcome},
		{"savings_aggregate", manifest.SavingsAggregate},
	}
	seenPaths := make(map[string]string, len(refs)+len(manifest.Identities))
	artifactBytes := make(map[string][]byte, len(refs))
	for _, item := range refs {
		raw, cleanPath, err := readReleaseEvidenceFile(root, item.role, item.ref, seenPaths)
		if err != nil {
			return assessment, err
		}
		assessment.FilesChecked++
		artifactBytes[item.role] = raw
		seenPaths[cleanPath] = item.role
	}

	identityBytes, err := validateReleaseIdentities(root, manifest.Identities, seenPaths, &assessment)
	if err != nil {
		return assessment, err
	}
	bindings, err := validateReleaseArtifacts(root, manifest, artifactBytes, identityBytes, counters)
	if err != nil {
		return assessment, err
	}
	assessment.CandidateBindings = bindings
	assessment.ReleaseReady = true
	return assessment, nil
}

func validateReleaseIdentities(root string, identities []ReleaseEvidenceIdentity, seenPaths map[string]string, assessment *ReleaseEvidenceAssessment) (map[string][]byte, error) {
	gotNames := make([]string, 0, len(identities))
	seenNames := make(map[string]bool, len(identities))
	identityBytes := make(map[string][]byte, len(identities))
	for _, identity := range identities {
		if seenNames[identity.Name] {
			return nil, fmt.Errorf("retrieval release evidence: duplicate identity role %q", identity.Name)
		}
		seenNames[identity.Name] = true
		gotNames = append(gotNames, identity.Name)
		raw, cleanPath, err := readReleaseEvidenceFile(root, "identity "+identity.Name, identity.ReleaseEvidenceFile, seenPaths)
		if err != nil {
			return nil, err
		}
		identityBytes[identity.Name] = raw
		assessment.FilesChecked++
		seenPaths[cleanPath] = "identity " + identity.Name
	}
	sort.Strings(gotNames)
	if !releaseEvidenceStringsEqual(gotNames, requiredReleaseIdentityNames) {
		return nil, fmt.Errorf("retrieval release evidence: identity roles are %v, want the exact %s universe %v", gotNames, ReleaseEvidenceManifestVersion, requiredReleaseIdentityNames)
	}
	return identityBytes, nil
}

func readReleaseEvidenceFile(root, role string, ref ReleaseEvidenceFile, seenPaths map[string]string) ([]byte, string, error) {
	if strings.TrimSpace(ref.Path) == "" {
		return nil, "", fmt.Errorf("retrieval release evidence: %s path is required", role)
	}
	if !isLowerHexDigest(ref.ExpectedSHA256, 64) {
		return nil, "", fmt.Errorf("retrieval release evidence: %s expected_sha256 must be 64 lowercase hex characters", role)
	}
	if filepath.IsAbs(ref.Path) {
		return nil, "", fmt.Errorf("retrieval release evidence: %s path %q must be repository-relative", role, ref.Path)
	}
	clean := filepath.Clean(filepath.FromSlash(ref.Path))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, "", fmt.Errorf("retrieval release evidence: %s path %q escapes the repository root", role, ref.Path)
	}
	if previous, duplicate := seenPaths[clean]; duplicate {
		return nil, "", fmt.Errorf("retrieval release evidence: %s and %s both name %q; each evidence role must bind its own artifact", previous, role, ref.Path)
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, "", fmt.Errorf("retrieval release evidence: resolve repository root: %w", err)
	}
	target := filepath.Join(rootReal, clean)
	targetReal, err := filepath.EvalSymlinks(target)
	if err != nil {
		return nil, "", fmt.Errorf("retrieval release evidence: read %s artifact %s: %w", role, ref.Path, err)
	}
	rel, err := filepath.Rel(rootReal, targetReal)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, "", fmt.Errorf("retrieval release evidence: %s path %q resolves outside the repository root", role, ref.Path)
	}
	raw, err := os.ReadFile(targetReal)
	if err != nil {
		return nil, "", fmt.Errorf("retrieval release evidence: read %s artifact %s: %w", role, ref.Path, err)
	}
	observed := SHA256Hex(raw)
	if observed != ref.ExpectedSHA256 {
		return nil, "", fmt.Errorf("retrieval release evidence: %s artifact %s digest drift: observed %s, expected %s", role, ref.Path, observed, ref.ExpectedSHA256)
	}
	return raw, clean, nil
}

func validateReleaseArtifacts(root string, manifest ReleaseEvidenceManifest, artifacts, identities map[string][]byte, counters map[string]PayloadCounter) ([]string, error) {
	candidateSHA := manifest.CandidateSHA
	var ranking Report
	if err := decodeReleaseEvidenceJSON("ranking_report", artifacts["ranking_report"], &ranking); err != nil {
		return nil, err
	}
	if err := CheckReportVersion(&ranking); err != nil {
		return nil, fmt.Errorf("retrieval release evidence: ranking_report: %w", err)
	}
	if err := CheckEmbedderSpec(&ranking); err != nil {
		return nil, fmt.Errorf("retrieval release evidence: ranking_report: %w", err)
	}
	if ranking.Reproducible.CandidateSHA != candidateSHA {
		return nil, candidateMismatch("ranking_report", ranking.Reproducible.CandidateSHA, candidateSHA)
	}

	var coverage TaskContextMeasurement
	if err := decodeReleaseEvidenceJSON("bundle_coverage", artifacts["bundle_coverage"], &coverage); err != nil {
		return nil, err
	}
	if coverage.FormatVersion != TaskContextFormatVersion || coverage.HarnessVersion != TaskContextHarnessVersion || coverage.ScorerVersion != TaskContextScorerVersion {
		return nil, fmt.Errorf("retrieval release evidence: bundle_coverage uses unsupported identity format=%d harness=%q scorer=%q", coverage.FormatVersion, coverage.HarnessVersion, coverage.ScorerVersion)
	}
	if coverage.Candidate.SHA != candidateSHA {
		return nil, candidateMismatch("bundle_coverage", coverage.Candidate.SHA, candidateSHA)
	}
	if coverage.Candidate.Dirty {
		return nil, fmt.Errorf("retrieval release evidence: bundle_coverage was captured from a dirty candidate")
	}
	if !coverage.EligibleForThreshold || !taskContextAllPassed(coverage.Eligibility) {
		return nil, fmt.Errorf("retrieval release evidence: bundle_coverage did not pass its own eligibility checks")
	}

	var smoke EvaluationOutcome
	if err := decodeReleaseEvidenceJSON("blind_smoke_outcome", artifacts["blind_smoke_outcome"], &smoke); err != nil {
		return nil, err
	}
	if smoke.ContractVersion != QrelBlindSmokeContractVersion {
		return nil, fmt.Errorf("retrieval release evidence: blind_smoke_outcome contract %q is not %q", smoke.ContractVersion, QrelBlindSmokeContractVersion)
	}
	if smoke.Release != ReleaseYes || !smoke.CaptureBinding.Bound {
		return nil, fmt.Errorf("retrieval release evidence: blind_smoke_outcome is not a bound RELEASE: YES")
	}
	if smoke.CaptureBinding.CaptureVersion != CandidateCaptureVersion {
		return nil, fmt.Errorf("retrieval release evidence: blind_smoke_outcome capture version %q is not %q", smoke.CaptureBinding.CaptureVersion, CandidateCaptureVersion)
	}
	if smoke.CaptureBinding.CandidateSHA != candidateSHA {
		return nil, candidateMismatch("blind_smoke_outcome", smoke.CaptureBinding.CandidateSHA, candidateSHA)
	}

	var savings ReleaseSavingsEvidence
	if err := decodeReleaseEvidenceJSON("savings_aggregate", artifacts["savings_aggregate"], &savings); err != nil {
		return nil, err
	}
	if savings.Version != ReleaseSavingsEvidenceVersion {
		return nil, fmt.Errorf("retrieval release evidence: savings_aggregate envelope version %q is not %q", savings.Version, ReleaseSavingsEvidenceVersion)
	}
	if savings.CandidateSHA != candidateSHA {
		return nil, candidateMismatch("savings_aggregate", savings.CandidateSHA, candidateSHA)
	}
	if err := ValidateSavingsAggregateResult(savings.Result, counters); err != nil {
		return nil, fmt.Errorf("retrieval release evidence: savings_aggregate: %w", err)
	}
	median, err := parseExactRational(savings.Result.Median)
	if err != nil {
		return nil, fmt.Errorf("retrieval release evidence: savings median: %w", err)
	}
	if median.Sign() <= 0 {
		return nil, fmt.Errorf("retrieval release evidence: savings median is %s/%s; the fewer-tokens claim requires an exact positive value, and zero or negative evidence cannot release", savings.Result.Median.Numerator, savings.Result.Median.Denominator)
	}

	var targets Targets
	if err := decodeReleaseEvidenceJSON("identity targets", identities["targets"], &targets); err != nil {
		return nil, err
	}
	if err := validateManifestTargetChecks(root, manifest, targets, ranking, artifacts["ranking_report"]); err != nil {
		return nil, err
	}

	developmentDatasetSHA := SHA256Hex(identities["development_dataset"])
	releaseDatasetSHA := SHA256Hex(identities["release_dataset"])
	tokenizerVocabularySHA := SHA256Hex(identities["tokenizer_vocabulary"])
	input := savings.Result.Input
	if err := validateSavingsPopulationDataset(identities["release_dataset"], input.Population); err != nil {
		return nil, err
	}
	for _, mismatch := range []struct {
		name     string
		observed string
		expected string
	}{
		{"ranking development dataset", ranking.Reproducible.Dataset.SHA256, developmentDatasetSHA},
		{"coverage development dataset", coverage.Dataset.SourceSHA256, developmentDatasetSHA},
		{"targets development dataset", targets.DerivedFrom.DatasetSHA256, developmentDatasetSHA},
		{"savings release dataset", input.DatasetSHA256, releaseDatasetSHA},
		{"ranking repository", ranking.Reproducible.Repo.SHA, input.RepoSHA},
		{"coverage repository", coverage.Repo.SHA, input.RepoSHA},
		{"blind-smoke repository", smoke.CaptureBinding.CheckoutSHA, input.RepoSHA},
		{"targets repository", targets.DerivedFrom.RepoSHA, input.RepoSHA},
		{"savings candidate version", input.CandidateVersion, coverage.Retrieval.Version},
		{"candidate method", input.Contract.CandidateMethod, coverage.Bundle.MethodVersion},
		{"tokenizer vocabulary", input.TokenizerVocabularySHA256, tokenizerVocabularySHA},
	} {
		if mismatch.observed == "" || mismatch.expected == "" || mismatch.observed != mismatch.expected {
			return nil, fmt.Errorf("retrieval release evidence: %s identity mismatch: observed %q, expected %q", mismatch.name, mismatch.observed, mismatch.expected)
		}
	}
	if input.Contract.CandidateTokenBudget != coverage.Bundle.TokenBudget {
		return nil, fmt.Errorf("retrieval release evidence: candidate budget mismatch: savings uses %d, coverage uses %d", input.Contract.CandidateTokenBudget, coverage.Bundle.TokenBudget)
	}

	return []string{"ranking_report", "bundle_coverage", "blind_smoke_outcome", "savings_aggregate"}, nil
}

// validateSavingsPopulationDataset closes the gap between a dataset digest and
// the population claimed inside a self-consistent savings result. Without this
// derivation check, an aggregate could name the right dataset hash while
// silently omitting an expensive query or changing its family/target.
func validateSavingsPopulationDataset(raw []byte, population []SavingsPopulationMember) error {
	var dataset Dataset
	if err := decodeReleaseEvidenceJSON("identity release_dataset", raw, &dataset); err != nil {
		return err
	}
	if err := dataset.Validate(); err != nil {
		return fmt.Errorf("retrieval release evidence: release dataset: %w", err)
	}

	expected := make(map[string]SavingsPopulationMember)
	for _, query := range dataset.Queries {
		if query.Stratum == StratumNoHit {
			continue
		}
		grade3 := 0
		for _, judgement := range query.Judgements {
			if judgement.Grade == GradeMax {
				grade3++
			}
		}
		if grade3 == 0 {
			continue
		}
		expected[query.ID] = SavingsPopulationMember{
			QueryID:  query.ID,
			FamilyID: query.FamilyID,
			Split:    query.Split,
			Stratum:  query.Stratum,
			Target: RecallTarget{
				Grade:         SavingsGrade,
				RequiredSpans: 1,
				TotalSpans:    grade3,
			},
		}
	}
	if len(population) != len(expected) {
		return fmt.Errorf("retrieval release evidence: savings population has %d members, release dataset derives %d answerable members", len(population), len(expected))
	}
	seen := make(map[string]bool, len(population))
	for _, member := range population {
		want, ok := expected[member.QueryID]
		if !ok {
			return fmt.Errorf("retrieval release evidence: savings population query %q is not answerable in the release dataset", member.QueryID)
		}
		if seen[member.QueryID] {
			return fmt.Errorf("retrieval release evidence: savings population duplicates query %q", member.QueryID)
		}
		seen[member.QueryID] = true
		if member != want {
			return fmt.Errorf("retrieval release evidence: savings population member %q differs from the release dataset derivation: got %+v, want %+v", member.QueryID, member, want)
		}
	}
	return nil
}

func validateManifestTargetChecks(root string, manifest ReleaseEvidenceManifest, targets Targets, ranking Report, rankingRaw []byte) error {
	if targets.SchemaVersion != TargetsSchemaVersion || targets.FusionMinDelta != FusionMinDelta || targets.Metric != TargetMetric || targets.Split != SplitDev {
		return fmt.Errorf("retrieval release evidence: manifest-bound targets use an unsupported schema or method identity")
	}
	conceptual := append([]string(nil), targets.ConceptualStrata...)
	sort.Strings(conceptual)
	wantConceptual := append([]string(nil), ConceptualStrata...)
	sort.Strings(wantConceptual)
	if !releaseEvidenceStringsEqual(conceptual, wantConceptual) {
		return fmt.Errorf("retrieval release evidence: manifest-bound conceptual strata are %v, want %v", conceptual, wantConceptual)
	}
	if targets.DerivedFrom.SHA256 != "" && targets.DerivedFrom.SHA256 == SHA256Hex(rankingRaw) {
		return fmt.Errorf("retrieval release evidence: ranking report is the report the manifest-bound targets were derived from")
	}
	if targets.DerivedFrom.Dataset != "" && ranking.Reproducible.Dataset.ID != targets.DerivedFrom.Dataset {
		return fmt.Errorf("retrieval release evidence: ranking dataset %q differs from manifest-bound target dataset %q", ranking.Reproducible.Dataset.ID, targets.DerivedFrom.Dataset)
	}

	strata, err := gateBaselineDevStrata(&ranking)
	if err != nil {
		return fmt.Errorf("retrieval release evidence: ranking targets: %w", err)
	}
	for _, name := range ConceptualStrata {
		target := targets.Strata[name]
		if target.FusionTarget == nil || target.FusionTarget.Metric != TargetMetric || target.FusionTarget.MinDelta != FusionMinDelta {
			return fmt.Errorf("retrieval release evidence: %s has no valid fusion target", name)
		}
		ft := target.FusionTarget
		ceiling, hasCeiling := target.Oracle[TargetMetric]
		derived := ft.BestValue + ft.MinDelta
		if hasCeiling {
			derived = math.Min(derived, ceiling)
		}
		if ft.MustReach != derived {
			return fmt.Errorf("retrieval release evidence: %s target must_reach %.17g does not derive from best %.17g + delta %.17g and oracle ceiling", name, ft.MustReach, ft.BestValue, ft.MinDelta)
		}
		observed, ok := strata[name].Metrics[ft.Metric]
		if !ok || observed < ft.MustReach {
			return fmt.Errorf("retrieval release evidence: target miss for %s: observed %.17g, required %.17g", name, observed, ft.MustReach)
		}
	}
	exact := targets.Strata[StratumExactIdentifier].NoRegression
	if exact == nil || exact.Metric != MetricTop1 {
		return fmt.Errorf("retrieval release evidence: exact_identifier has no Top-1 no-regression target")
	}
	observed, ok := strata[StratumExactIdentifier].Metrics[exact.Metric]
	if !ok || observed < exact.Floor {
		return fmt.Errorf("retrieval release evidence: target miss for exact_identifier: observed %.17g, required %.17g", observed, exact.Floor)
	}

	coverageCheck := checkBundleCoverage(&targets, TargetCheckInputs{RepoRoot: root, CoveragePath: manifest.BundleCoverage.Path})
	if !coverageCheck.Met {
		return fmt.Errorf("retrieval release evidence: target miss for bundle_coverage: %s", coverageCheck.Detail)
	}
	smokeCheck := checkQrelBlindSmokeGate(TargetCheckInputs{RepoRoot: root, SmokeOutcomePath: manifest.BlindSmokeOutcome.Path})
	if !smokeCheck.Met {
		return fmt.Errorf("retrieval release evidence: target miss for qrel_blind_smoke: %s", smokeCheck.Detail)
	}
	return nil
}

func decodeReleaseEvidenceJSON(role string, raw []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("retrieval release evidence: parse %s: %w", role, err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("retrieval release evidence: parse %s: trailing JSON value", role)
		}
		return fmt.Errorf("retrieval release evidence: parse %s trailing bytes: %w", role, err)
	}
	return nil
}

func candidateMismatch(role, observed, expected string) error {
	return fmt.Errorf("retrieval release evidence: %s carries candidate_sha %q, manifest binds %q; every artifact must describe the same candidate commit", role, observed, expected)
}

func releaseEvidenceStringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
