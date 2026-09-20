package retrieval

// Preregistration authoring support.
//
// ValidateQualificationPreregistration is deliberately fail-closed and says
// nothing about how to PRODUCE a value it would accept. An operator authoring
// preregistration.json by hand therefore discovers a wrong field only when
// `measure` refuses the run - after the sidecar has been provisioned and, worse,
// after a fresh sealed holdout has been named in a file that is now suspect.
// This file moves the computable half of that authoring problem into code.
//
// Two rules shape everything below.
//
// First: every value is derived with the SAME helper the gate checks it with.
// The dataset digest goes through LoadDataset and SHA256Hex, the candidate diff
// through the same canonical `git diff` argument vector GitRepoProbe uses, the
// Potion identities through qualificationPotionIdentity, the CodeRank identity
// through coderank.Manifest's own IdentityDigest and AdmissionSpec. A second
// hashing path would be a second thing to keep in sync, and a preregistration
// that disagrees with the gate by one byte is indistinguishable from fraud.
//
// Second: a field that cannot be derived yet is REPORTED, never guessed. A
// fabricated digest is worse than a missing one, because it is 64 lowercase hex
// characters and so passes every structural check in
// ValidateQualificationPreregistration; it fails much later, at capture, where
// the failure reads as model drift rather than as an authoring mistake. So the
// helper emits nothing for a field it cannot compute and names the artifact the
// field is waiting on instead.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/embed/coderank"
	"github.com/samibel/graphi/engine/embed/static"
)

// QualificationPreregistrationDigestInputs names the artifacts the helper may
// read. Only the first two are required: the point of the helper is to be
// runnable early and repeatedly, reporting the fields that are still blocked,
// so an operator can see the remaining work rather than discovering it one
// refusal at a time.
type QualificationPreregistrationDigestInputs struct {
	// DatasetPath is the frozen development dataset JSON. Its exact file
	// bytes are dataset_sha256 and its repo_sha is source_repo_sha.
	DatasetPath string
	// CandidateRoot is the GrapHi candidate worktree measure will observe.
	CandidateRoot string
	// FrozenCandidateSHA is the commit being preregistered as candidate_sha.
	// Empty means "the candidate worktree's current HEAD", which is the
	// normal case: the preregistration is authored last, on the commit that
	// is about to be frozen.
	FrozenCandidateSHA string
	// CodeRankManifestPath is the pinned sidecar manifest. Without it the
	// whole M3_coderank arm stays unresolved, because every one of its four
	// pins is a function of the manifest's bytes.
	CodeRankManifestPath string
	// GraderPromptPath is the grading rubric the precondition record will
	// freeze under role "grading_rubric". Its file bytes are
	// grader_prompt_sha256.
	GraderPromptPath string
	// There is deliberately no graph-generation input. The canonical
	// fingerprint's eighth field is minted from crypto/rand by every index
	// build (engine/ingest.mintCommitGeneration), so no trial build can
	// report the value the real run will carry. The helper emits
	// QualificationGraphGenerationPlaceholder there and the gate binds the
	// field at runtime instead — see model_qualification_fingerprint.go.
}

// QualificationUnresolvedField is one preregistration field the helper refused
// to invent, with the artifact it waits on and the exact derivation once that
// artifact exists. Requires and Recipe are separate because they answer
// different questions: "why can't I have it yet" and "what do I run".
type QualificationUnresolvedField struct {
	Field    string
	Requires string
	Recipe   string
}

// QualificationPreregistrationDigests is one authoring observation.
type QualificationPreregistrationDigests struct {
	// Fragment is indented JSON containing ONLY the fields the helper
	// actually computed, shaped exactly like the corresponding subtrees of
	// QualificationPreregistration so it can be pasted in field for field.
	Fragment []byte
	// Unresolved names every remaining field, in a stable order.
	Unresolved []QualificationUnresolvedField
	// CandidateSHA is the commit the diff was taken against, and
	// CandidateDiffSHA256 is the digest of the canonical diff between that
	// commit and the candidate WORKING TREE outside the run directory.
	//
	// On a clean tree that diff is empty, so the digest equals the empty-diff
	// digest - which is also the only value a run can ever succeed with,
	// because ObserveCandidateBinding refuses a candidate that differs from
	// the frozen candidate outside the run directory at all. A digest other
	// than EmptyCandidateDiffSHA256 therefore does not mean "preregister this
	// instead"; it means the tree is not frozen yet.
	CandidateSHA        string
	CandidateDiffSHA256 string
	// CandidateWorktreeClean is git's own answer for the candidate tree
	// outside the run directory, including untracked files. It is recorded
	// separately from the diff because an untracked file makes a tree dirty
	// without changing a commit-to-worktree diff at all.
	CandidateWorktreeClean bool
	// DatasetSHA256 and SourceRepoSHA are surfaced as fields as well as in
	// Fragment because callers gate on them.
	DatasetSHA256 string
	SourceRepoSHA string
}

// EmptyCandidateDiffSHA256 is the digest of a zero-byte diff: the only
// candidate_diff_sha256 a valid qualification run can carry.
//
// This is not a convention, it is forced. ObserveCandidateBinding computes the
// diff between the frozen candidate and the candidate worktree's HEAD outside
// the run directory, and returns an error unless that path set is empty - an
// empty path set means empty diff bytes. Preregistering anything else
// guarantees a refusal at capture.
func EmptyCandidateDiffSHA256() string { return SHA256Hex(nil) }

// PinnedPotionArtifactManifest renders the pinned Potion artifact set as one
// canonical listing, so the two Potion arms have a reproducible
// manifest_sha256.
//
// Unlike CodeRank, the Potion model has no manifest FILE in the repository -
// its artifact identity lives in static.PinnedSHA256. validateQualification
// Arms only requires the two Potion arms to share one 64-hex manifest digest
// that differs from CodeRank's, so this function defines what that digest is
// over: the pinned selector line, then one "<sha256>  <filename>" line per
// pinned file in sorted name order, every line newline-terminated. Any pin
// rotation changes the listing and therefore the digest, which is the property
// the field exists for.
func PinnedPotionArtifactManifest() string {
	names := append([]string(nil), static.PinnedFileNames...)
	sort.Strings(names)
	var out strings.Builder
	out.WriteString(static.PinnedSelector)
	out.WriteByte('\n')
	for _, name := range names {
		out.WriteString(static.PinnedSHA256[name])
		out.WriteString("  ")
		out.WriteString(name)
		out.WriteByte('\n')
	}
	return out.String()
}

// ComputeQualificationPreregistrationDigests derives every preregistration
// field that is a pure function of artifacts already on disk.
//
// It fails closed - returning an error and no partial fragment - when an input
// it was explicitly given cannot be read or does not parse. That distinction
// matters: an input the operator did not supply is an open task the helper
// reports, but an input the operator DID supply and that turns out to be
// missing is an authoring error, and continuing past it would produce a
// fragment whose absent fields look like "not ready yet" rather than "your path
// is wrong".
func ComputeQualificationPreregistrationDigests(ctx context.Context, in QualificationPreregistrationDigestInputs) (QualificationPreregistrationDigests, error) {
	var out QualificationPreregistrationDigests
	datasetPath := strings.TrimSpace(in.DatasetPath)
	candidateRoot := strings.TrimSpace(in.CandidateRoot)
	if datasetPath == "" || candidateRoot == "" {
		return out, fmt.Errorf("embedded-model qualification digests: the frozen dataset path and the candidate worktree root are both required; without them neither dataset_sha256 nor candidate_sha can be derived from anything")
	}

	fragment := map[string]any{"schema_version": QualificationSchemaVersion}
	var unresolved []QualificationUnresolvedField
	note := func(field, requires, recipe string) {
		unresolved = append(unresolved, QualificationUnresolvedField{Field: field, Requires: requires, Recipe: recipe})
	}

	dataset, err := LoadDataset(datasetPath)
	if err != nil {
		return QualificationPreregistrationDigests{}, fmt.Errorf("embedded-model qualification digests: dataset %s could not be read as a valid dataset, so dataset_sha256 would be a digest of bytes nobody validated: %w", datasetPath, err)
	}
	out.DatasetSHA256 = dataset.SHA256
	fragment["dataset_sha256"] = dataset.SHA256

	// source_repo_sha is not independently chosen: validateQualification
	// StatsInputs requires dataset.repo_sha to equal it, so the dataset is the
	// authority and a hand-typed second copy is only an opportunity to differ.
	sourceRepoSHA := strings.TrimSpace(dataset.Dataset.RepoSHA)
	if isLowerHexDigest(sourceRepoSHA, 40) {
		out.SourceRepoSHA = sourceRepoSHA
		fragment["source_repo_sha"] = sourceRepoSHA
	} else {
		note("source_repo_sha",
			fmt.Sprintf("the dataset at %s to carry repo_sha as a 40-character lowercase commit id (it currently carries %q)", datasetPath, sourceRepoSHA),
			"source_repo_sha must EQUAL dataset.repo_sha; the qualification gate compares them, so fix the dataset rather than typing a different value here")
	}

	if err := computeQualificationCandidateBinding(ctx, candidateRoot, strings.TrimSpace(in.FrozenCandidateSHA), &out, fragment); err != nil {
		return QualificationPreregistrationDigests{}, err
	}

	// The reader prompt is a repository constant, not a file: BuildRaterPrompt
	// writes RaterInstructions verbatim at the head of every rater prompt, and
	// validateBlindEvidenceSets requires ONE reader_prompt_sha256 to match all
	// 64 queries of all eight subjects - which only a constant can.
	fragment["reader_prompt_sha256"] = SHA256Hex([]byte(RaterInstructions))

	if err := computeQualificationGraderPromptDigest(strings.TrimSpace(in.GraderPromptPath), fragment, note); err != nil {
		return QualificationPreregistrationDigests{}, err
	}

	arms, err := computeQualificationArmPins(strings.TrimSpace(in.CodeRankManifestPath), note)
	if err != nil {
		return QualificationPreregistrationDigests{}, err
	}
	fragment["arms"] = arms

	// The frozen block is copied from the constants the gate compares against,
	// so an operator never transcribes a threshold. validateQualification
	// Thresholds compares the whole struct, so one wrong digit rejects the run
	// with no indication of which field moved.
	fragment["compact_version"] = QualificationCompactVersion
	fragment["token_budget"] = QualificationTokenBudget
	fragment["bootstrap_samples"] = QualificationBootstrapSamples
	fragment["thresholds"] = QualificationThresholds{
		MinPasses:                     QualificationMinPasses,
		MinPairedGain:                 QualificationMinPairedGain,
		MinWeakStrataWithPositiveGain: QualificationMinWeakStrataWithPositiveGain,
		BootstrapConfidence:           QualificationBootstrapConfidence,
		MaxSidecarRSSBytes:            QualificationMaxSidecarRSSBytes,
		MaxArtifactBytes:              QualificationMaxArtifactBytes,
		MaxQueryP95Millis:             QualificationMaxQueryP95Millis,
		MinQuerySamples:               QualificationMinQuerySamples,
		MaxReindexSeconds:             QualificationMaxReindexSeconds,
	}

	note("bootstrap_seed",
		"an operator decision, not an artifact",
		"choose any non-zero uint64 and write it down BEFORE any result is opened; re-rolling a seed after seeing a confidence interval is the exact manipulation preregistration exists to prevent")
	note("reference_machine",
		"the machine that will run `measure`, observed by the operator",
		"os / os_version / cpu / physical_cores / runtime_threads must equal what measure observes on that machine (`uname -s`, `sw_vers -productVersion` or `uname -r`, `sysctl -n machdep.cpu.brand_string`, `sysctl -n hw.physicalcpu`, the sidecar's own effective thread count); background_load is the literal declaration passed as --background-metadata and must match it byte for byte")

	encoded, err := json.MarshalIndent(fragment, "", "  ")
	if err != nil {
		return QualificationPreregistrationDigests{}, fmt.Errorf("embedded-model qualification digests: encode fragment: %w", err)
	}
	out.Fragment = append(encoded, '\n')
	out.Unresolved = unresolved
	return out, nil
}

// computeQualificationCandidateBinding records the candidate commit and the
// exact digest measure will compare against.
func computeQualificationCandidateBinding(ctx context.Context, candidateRoot, frozen string, out *QualificationPreregistrationDigests, fragment map[string]any) error {
	head, err := CheckoutHEAD(ctx, candidateRoot)
	if err != nil {
		return fmt.Errorf("embedded-model qualification digests: candidate worktree %s has no readable HEAD, so candidate_sha cannot be derived: %w", candidateRoot, err)
	}
	if frozen == "" {
		frozen = head
	}
	if !isLowerHexDigest(frozen, 40) {
		return fmt.Errorf("embedded-model qualification digests: candidate commit %q is not a 40-character lowercase commit id; preregistering a short or abbreviated sha binds the run to nothing", frozen)
	}

	probe := GitRepoProbe()
	clean, err := probe.WorktreeCleanOutside(ctx, candidateRoot, QualificationCandidateExcludedPath)
	if err != nil {
		return fmt.Errorf("embedded-model qualification digests: candidate worktree %s state could not be observed: %w", candidateRoot, err)
	}

	diff, err := qualificationWorkingTreeDiff(ctx, candidateRoot, frozen, QualificationCandidateExcludedPath)
	if err != nil {
		return err
	}

	out.CandidateSHA = frozen
	out.CandidateDiffSHA256 = SHA256Hex(diff)
	out.CandidateWorktreeClean = clean
	fragment["candidate_sha"] = frozen
	fragment["candidate_diff_sha256"] = out.CandidateDiffSHA256
	return nil
}

// qualificationWorkingTreeDiff returns the canonical diff between a commit and
// the current working tree, outside the run directory.
//
// The argument vector is GitRepoProbe.DiffOutside's, minus the second revision:
// with one revision git diffs that commit against the working tree rather than
// against another commit. That is deliberately NOT what measure computes -
// measure compares two commits - and it is the point. An operator authoring the
// preregistration wants to know whether the tree they are about to freeze is
// actually the tree in the commit, and a commit-to-commit diff cannot tell them
// that.
func qualificationWorkingTreeDiff(ctx context.Context, root, rev, exclude string) ([]byte, error) {
	args := []string{"-C", root, "diff", "--binary", "--full-index", "--no-color", "--no-ext-diff", "--no-textconv", "--no-renames", rev, "--", "."}
	if strings.TrimSpace(exclude) != "" {
		args = append(args, ":(exclude)"+exclude)
	}
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("embedded-model qualification digests: canonical diff of %s against %s failed, so candidate_diff_sha256 is unknown rather than empty: %w", root, rev, err)
	}
	return out, nil
}

// computeQualificationGraderPromptDigest digests the rubric file the
// precondition record will freeze.
func computeQualificationGraderPromptDigest(path string, fragment map[string]any, note func(field, requires, recipe string)) error {
	if path == "" {
		note("grader_prompt_sha256",
			"the grading rubric file that the blind-evidence precondition record freezes under role \"grading_rubric\"",
			"author the rubric inside "+QualificationCandidateExcludedPath+", then re-run this helper with --grading-rubric pointing at it; the digest is the SHA-256 of its exact file bytes")
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("embedded-model qualification digests: grading rubric %s could not be read, so grader_prompt_sha256 would be a digest of nothing: %w", path, err)
	}
	if len(raw) == 0 {
		return fmt.Errorf("embedded-model qualification digests: grading rubric %s is empty; an empty rubric still digests to a valid-looking 64-character value, which is precisely the failure this refusal prevents", path)
	}
	fragment["grader_prompt_sha256"] = SHA256Hex(raw)
	return nil
}

// computeQualificationArmPins derives every arm pin that is a pure function of
// repository pins or of the CodeRank manifest bytes.
//
// Since graph_generation became runtime-bound, the two Potion arms are fully
// computable from the repository alone — no index, no trial build, no manifest.
// That is the point of the change: a preregistration field that waited on an
// artifact nobody could produce reproducibly is now no field at all.
func computeQualificationArmPins(manifestPath string, note func(field, requires, recipe string)) (map[string]any, error) {
	arms := map[string]any{
		// The lexical control carries a label and nothing else:
		// validateQualificationArms refuses it outright if it carries any
		// embedding pin, because a lexical arm with an embedder identity is
		// not a negative control.
		string(ArmLexical): map[string]any{"label": string(ArmLexical)},
	}

	potionManifestSHA := SHA256Hex([]byte(PinnedPotionArtifactManifest()))
	for _, arm := range []struct {
		name      QualificationArm
		maxTokens int
	}{
		{name: ArmPotion512, maxTokens: static.DefaultMaxLength},
		{name: ArmPotion8192, maxTokens: 8192},
	} {
		modelID, admissionSHA := qualificationPotionIdentity(arm.maxTokens)
		pin := map[string]any{
			"label":           string(arm.name),
			"embedder_id":     modelID,
			"manifest_sha256": potionManifestSHA,
			// The two Potion arms MUST differ here: they are the same
			// weights under two admission budgets, and an equal admission
			// digest would mean the 512/8192 comparison compares nothing.
			"admission_sha256": admissionSHA,
		}
		pin["fingerprint_canonical"] = embed.Fingerprint{
			ModelID:         modelID,
			Revision:        static.PinnedRevision,
			ModelSHA256:     static.PinnedSHA256[static.FileSafetensors],
			TokenizerSHA256: static.PinnedSHA256[static.FileTokenizer],
			Dim:             qualificationPotionDimension,
			DocumentSchema:  embed.DocumentSchema,
			ChunkerConfig:   "",
			GraphGeneration: QualificationGraphGenerationPlaceholder,
		}.Canonical()
		arms[string(arm.name)] = pin
	}

	if manifestPath == "" {
		note("arms.M3_coderank.*",
			"the finalized CodeRank sidecar manifest file",
			"finalize the manifest (see docs/eval/retrieval/coderank-sidecar-manifest.example.json), then re-run with --manifest <path>; manifest_sha256 is the SHA-256 of its exact file bytes and every other M3 pin is derived from its parsed contents")
		return arms, nil
	}

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("embedded-model qualification digests: CodeRank manifest %s could not be read, so no M3 pin can be derived: %w", manifestPath, err)
	}
	manifest, err := coderank.LoadManifest(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("embedded-model qualification digests: CodeRank manifest %s is not a valid pinned manifest, so its identity digest would describe a profile the sidecar will not serve: %w", manifestPath, err)
	}

	// The durable profile is the manifest with its endpoint removed. The
	// endpoint is the one relocatable field - the same pinned model served on
	// another port is the same embedding space - and validateCodeRank
	// QualificationArm rejects a profile that still carries one.
	durable := manifest
	durable.Endpoint = ""
	profile, err := json.Marshal(durable)
	if err != nil {
		return nil, fmt.Errorf("embedded-model qualification digests: encode CodeRank durable profile: %w", err)
	}

	embedderID := "coderank:" + manifest.Model.ID + "@" + manifest.Model.Revision + ":" + manifest.IdentityDigest()
	pin := map[string]any{
		"label":            string(ArmCodeRank),
		"embedder_id":      embedderID,
		"manifest_sha256":  SHA256Hex(raw),
		"admission_sha256": SHA256Hex([]byte(manifest.AdmissionSpec().String())),
	}
	pin["fingerprint_canonical"] = embed.Fingerprint{
		ModelID:         embedderID,
		Revision:        manifest.Model.Revision,
		ModelSHA256:     manifest.Model.SHA256,
		TokenizerSHA256: manifest.Tokenizer.SHA256,
		Dim:             manifest.Dimension,
		DocumentSchema:  embed.DocumentSchema,
		ChunkerConfig:   string(profile),
		GraphGeneration: QualificationGraphGenerationPlaceholder,
	}.Canonical()
	arms[string(ArmCodeRank)] = pin
	return arms, nil
}

// qualificationPotionDimension is the Potion embedding width the gate requires
// verbatim in the fingerprint's fifth field (see validatePotionQualification
// Arms, which compares it against the literal "256").
const qualificationPotionDimension = 256

// RenderQualificationPreregistrationDigests writes the paste-able fragment to
// fragmentOut and the authoring notes to notesOut.
//
// The two streams are separate on purpose. The fragment must stay valid JSON so
// it can be piped or pasted without editing, and JSON has no comment syntax, so
// anything explanatory written into it would have to be a field - and an extra
// field is fatal: loadQualificationPreregistration decodes with
// DisallowUnknownFields.
func RenderQualificationPreregistrationDigests(digests QualificationPreregistrationDigests, fragmentOut, notesOut io.Writer) error {
	if _, err := fragmentOut.Write(digests.Fragment); err != nil {
		return fmt.Errorf("embedded-model qualification digests: write fragment: %w", err)
	}
	var notes strings.Builder
	notes.WriteString("candidate_sha: " + digests.CandidateSHA + "\n")
	notes.WriteString("candidate_diff_sha256: " + digests.CandidateDiffSHA256 + "\n")
	// Said on every run, not only when something is outstanding: an operator
	// reading a canonical fingerprint that ends in a word rather than a hex
	// generation id must be able to tell at once that it is intentional.
	notes.WriteString("graph_generation: every emitted fingerprint_canonical carries the constant \"" + QualificationGraphGenerationPlaceholder + "\"\n")
	notes.WriteString("  in its eighth field. That field is NOT preregistered. Every index build mints a fresh\n")
	notes.WriteString("  random index.commit_generation (engine/ingest.mintCommitGeneration), so no trial build can\n")
	notes.WriteString("  tell you the value the real run will carry, and a preregistered one could never match.\n")
	notes.WriteString("  The gate compares fields 0-6 against this pin and binds field 7 at runtime, then requires\n")
	notes.WriteString("  every arm and every observation of the run to name that one generation.\n")
	if !digests.CandidateWorktreeClean || digests.CandidateDiffSHA256 != EmptyCandidateDiffSHA256() {
		notes.WriteString("NOT READY - the candidate worktree at HEAD is not clean outside " + QualificationCandidateExcludedPath + ".\n")
		notes.WriteString("  A qualification run can only ever carry candidate_diff_sha256 " + EmptyCandidateDiffSHA256() + ",\n")
		notes.WriteString("  because ObserveCandidateBinding refuses any candidate that differs from the frozen\n")
		notes.WriteString("  candidate outside the run directory. Commit or discard the pending changes, then re-run.\n")
	}
	for _, field := range digests.Unresolved {
		notes.WriteString("cannot compute yet - requires " + field.Requires + "\n")
		notes.WriteString("  field: " + field.Field + "\n")
		notes.WriteString("  how:   " + field.Recipe + "\n")
	}
	if _, err := io.WriteString(notesOut, notes.String()); err != nil {
		return fmt.Errorf("embedded-model qualification digests: write notes: %w", err)
	}
	return nil
}

// CandidateIsFreezable reports whether the candidate worktree is in the only
// state a qualification run can be preregistered from.
//
// It says nothing about the unresolved fields: those are open authoring tasks,
// and an operator legitimately runs this helper many times while they are still
// open. A dirty candidate tree is different in kind - it makes the two candidate
// fields that WERE emitted wrong rather than missing - so it is the one
// condition callers gate their exit code on.
func (d QualificationPreregistrationDigests) CandidateIsFreezable() bool {
	return d.CandidateWorktreeClean && d.CandidateDiffSHA256 == EmptyCandidateDiffSHA256()
}
