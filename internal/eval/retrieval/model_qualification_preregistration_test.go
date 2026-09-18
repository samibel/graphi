package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/embed/coderank"
)

// The helper's whole value is that a value it prints can be pasted into a
// preregistration without further thought. These tests pin the three states
// that decide whether that is true: a frozen candidate, a candidate that only
// looks frozen, and an input the operator named but that is not there.

func TestPreregistrationDigestsOnACleanCandidateEmitTheOnlyDiffDigestARunCanCarry(t *testing.T) {
	root, head := preregistrationDigestFixture(t)
	datasetPath := writePreregistrationDatasetFixture(t, head)

	digests, err := ComputeQualificationPreregistrationDigests(context.Background(), QualificationPreregistrationDigestInputs{
		DatasetPath: datasetPath, CandidateRoot: root,
	})
	if err != nil {
		t.Fatalf("compute digests: %v", err)
	}
	if !digests.CandidateWorktreeClean || !digests.CandidateIsFreezable() {
		t.Fatalf("clean fixture reported clean=%t freezable=%t", digests.CandidateWorktreeClean, digests.CandidateIsFreezable())
	}
	if digests.CandidateDiffSHA256 != EmptyCandidateDiffSHA256() {
		t.Fatalf("candidate diff digest = %s, want the empty-diff digest %s", digests.CandidateDiffSHA256, EmptyCandidateDiffSHA256())
	}
	if digests.CandidateSHA != head {
		t.Fatalf("candidate sha = %s, want HEAD %s", digests.CandidateSHA, head)
	}

	var fragment map[string]any
	if err := json.Unmarshal(digests.Fragment, &fragment); err != nil {
		t.Fatalf("fragment is not valid JSON: %v\n%s", err, digests.Fragment)
	}
	for _, field := range []string{"schema_version", "dataset_sha256", "source_repo_sha", "candidate_sha", "candidate_diff_sha256", "reader_prompt_sha256", "arms", "compact_version", "token_budget", "bootstrap_samples", "thresholds"} {
		if _, present := fragment[field]; !present {
			t.Fatalf("fragment omits %s:\n%s", field, digests.Fragment)
		}
	}
	// The frozen block must be copied from the constants, not retyped: the
	// gate compares the whole struct at once and names no field on refusal.
	var thresholds QualificationThresholds
	raw, err := json.Marshal(fragment["thresholds"])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &thresholds); err != nil {
		t.Fatal(err)
	}
	if err := validateQualificationThresholds(thresholds); err != nil {
		t.Fatalf("emitted thresholds are not the frozen gate: %v", err)
	}
	if fragment["dataset_sha256"] != SHA256Hex(mustReadFile(t, datasetPath)) {
		t.Fatalf("dataset digest does not recompute from the file bytes")
	}
	if fragment["source_repo_sha"] != head {
		t.Fatalf("source_repo_sha = %v, want the dataset's repo_sha %s", fragment["source_repo_sha"], head)
	}
	if fragment["reader_prompt_sha256"] != SHA256Hex([]byte(RaterInstructions)) {
		t.Fatalf("reader prompt digest is not the frozen rater instruction block")
	}

	// Nothing that needs an artifact the fixture does not have may appear.
	arms, ok := fragment["arms"].(map[string]any)
	if !ok {
		t.Fatalf("arms is not an object: %#v", fragment["arms"])
	}
	if _, present := arms[string(ArmCodeRank)]; present {
		t.Fatalf("the CodeRank arm was emitted without a manifest:\n%s", digests.Fragment)
	}
	lexical, ok := arms[string(ArmLexical)].(map[string]any)
	if !ok || len(lexical) != 1 || lexical["label"] != string(ArmLexical) {
		t.Fatalf("lexical control carries more than its label: %#v", arms[string(ArmLexical)])
	}
	potion512, ok := arms[string(ArmPotion512)].(map[string]any)
	if !ok {
		t.Fatalf("Potion/512 arm missing: %#v", arms)
	}
	potion8192 := arms[string(ArmPotion8192)].(map[string]any)
	wantID512, wantAdmission512 := qualificationPotionIdentity(512)
	if potion512["embedder_id"] != wantID512 || potion512["admission_sha256"] != wantAdmission512 {
		t.Fatalf("Potion/512 identity does not match the pinned identity the gate recomputes: %#v", potion512)
	}
	if potion512["manifest_sha256"] != potion8192["manifest_sha256"] {
		t.Fatalf("the Potion arms must share one artifact manifest digest")
	}
	if potion512["admission_sha256"] == potion8192["admission_sha256"] {
		t.Fatalf("the Potion arms must carry distinct admission profiles")
	}
	// The Potion fingerprints no longer wait on anything: graph_generation is
	// bound at runtime, and every other field comes from the pin table. A
	// helper that still reported them as outstanding would be sending the
	// operator after an index build that cannot produce a reproducible value.
	for _, arm := range []map[string]any{potion512, potion8192} {
		canonical, present := arm["fingerprint_canonical"].(string)
		if !present {
			t.Fatalf("a Potion fingerprint is still reported as unavailable:\n%s", digests.Fragment)
		}
		generation, ok := qualificationGraphGenerationOf(canonical)
		if !ok || generation != QualificationGraphGenerationPlaceholder {
			t.Fatalf("emitted fingerprint does not carry the runtime-bound placeholder in field 7: %q", canonical)
		}
	}
	assertUnresolved(t, digests,
		"grader_prompt_sha256",
		"arms.M3_coderank.*",
		"bootstrap_seed",
		"reference_machine")
	for _, field := range digests.Unresolved {
		if strings.Contains(field.Field, "M1_potion_512") || strings.Contains(field.Field, "M2_potion_8192") {
			t.Fatalf("a Potion pin is still reported as outstanding: %+v", field)
		}
	}
}

func TestPreregistrationDigestsOnADirtyCandidateChangeTheDiffDigestAndRefuseToCallItFreezable(t *testing.T) {
	root, head := preregistrationDigestFixture(t)
	datasetPath := writePreregistrationDatasetFixture(t, head)

	clean, err := ComputeQualificationPreregistrationDigests(context.Background(), QualificationPreregistrationDigestInputs{
		DatasetPath: datasetPath, CandidateRoot: root,
	})
	if err != nil {
		t.Fatalf("compute clean digests: %v", err)
	}

	// An edit that is never committed is exactly the state ObserveCandidate
	// Binding exists to refuse, and the state a commit-to-commit diff cannot
	// see. The helper has to surface it before the preregistration is frozen.
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("edited after the freeze\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err := ComputeQualificationPreregistrationDigests(context.Background(), QualificationPreregistrationDigestInputs{
		DatasetPath: datasetPath, CandidateRoot: root,
	})
	if err != nil {
		t.Fatalf("compute dirty digests: %v", err)
	}
	if dirty.CandidateDiffSHA256 == clean.CandidateDiffSHA256 {
		t.Fatalf("an uncommitted edit left candidate_diff_sha256 unchanged at %s", dirty.CandidateDiffSHA256)
	}
	if dirty.CandidateDiffSHA256 == EmptyCandidateDiffSHA256() {
		t.Fatalf("a dirty tree reported the empty-diff digest")
	}
	if dirty.CandidateWorktreeClean || dirty.CandidateIsFreezable() {
		t.Fatalf("dirty fixture reported clean=%t freezable=%t", dirty.CandidateWorktreeClean, dirty.CandidateIsFreezable())
	}
	if dirty.CandidateSHA != head {
		t.Fatalf("candidate sha drifted to %s on a dirty tree", dirty.CandidateSHA)
	}

	var fragmentOut, notesOut bytes.Buffer
	if err := RenderQualificationPreregistrationDigests(dirty, &fragmentOut, &notesOut); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(notesOut.String(), "NOT READY") || !strings.Contains(notesOut.String(), EmptyCandidateDiffSHA256()) {
		t.Fatalf("notes do not say the tree is unfreezable:\n%s", notesOut.String())
	}
	if strings.Contains(fragmentOut.String(), "NOT READY") {
		t.Fatalf("the paste-able fragment carries prose:\n%s", fragmentOut.String())
	}
	if !json.Valid(fragmentOut.Bytes()) {
		t.Fatalf("fragment is not valid JSON:\n%s", fragmentOut.String())
	}
}

func TestPreregistrationDigestsFailClosedOnAMissingNamedInput(t *testing.T) {
	root, head := preregistrationDigestFixture(t)
	datasetPath := writePreregistrationDatasetFixture(t, head)
	absent := filepath.Join(t.TempDir(), "absent.json")

	for _, tc := range []struct {
		name    string
		inputs  QualificationPreregistrationDigestInputs
		mention string
	}{
		{
			name:    "dataset",
			inputs:  QualificationPreregistrationDigestInputs{DatasetPath: absent, CandidateRoot: root},
			mention: "dataset",
		},
		{
			name:    "coderank manifest",
			inputs:  QualificationPreregistrationDigestInputs{DatasetPath: datasetPath, CandidateRoot: root, CodeRankManifestPath: absent},
			mention: "CodeRank manifest",
		},
		{
			name:    "grading rubric",
			inputs:  QualificationPreregistrationDigestInputs{DatasetPath: datasetPath, CandidateRoot: root, GraderPromptPath: absent},
			mention: "grading rubric",
		},
		{
			name:    "candidate root",
			inputs:  QualificationPreregistrationDigestInputs{DatasetPath: datasetPath, CandidateRoot: filepath.Join(t.TempDir(), "not-a-repo")},
			mention: "HEAD",
		},
		{
			name:    "no dataset at all",
			inputs:  QualificationPreregistrationDigestInputs{CandidateRoot: root},
			mention: "required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			digests, err := ComputeQualificationPreregistrationDigests(context.Background(), tc.inputs)
			if err == nil {
				t.Fatalf("a missing %s produced a fragment instead of a refusal:\n%s", tc.name, digests.Fragment)
			}
			if len(digests.Fragment) != 0 || digests.CandidateDiffSHA256 != "" || digests.DatasetSHA256 != "" {
				t.Fatalf("a refusal still returned partial values: %+v", digests)
			}
			if strings.Contains(err.Error(), strings.Repeat("0", 64)) || strings.Contains(err.Error(), SHA256Hex(nil)) {
				t.Fatalf("the refusal leaked a digest-shaped value: %v", err)
			}
			if !strings.Contains(err.Error(), tc.mention) {
				t.Fatalf("refusal does not name what is missing (%q): %v", tc.mention, err)
			}
		})
	}

	// An empty rubric is present but meaningless: it would digest to a
	// perfectly valid 64-character value that pins nothing.
	empty := filepath.Join(t.TempDir(), "grading-rubric.md")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ComputeQualificationPreregistrationDigests(context.Background(), QualificationPreregistrationDigestInputs{
		DatasetPath: datasetPath, CandidateRoot: root, GraderPromptPath: empty,
	}); err == nil {
		t.Fatal("an empty grading rubric was digested instead of refused")
	}
}

func TestPreregistrationDigestsWithAManifestEmitCompleteArmPins(t *testing.T) {
	root, head := preregistrationDigestFixture(t)
	datasetPath := writePreregistrationDatasetFixture(t, head)
	manifestPath := writePreregistrationManifestFixture(t)
	rubricPath := filepath.Join(t.TempDir(), "grading-rubric.md")
	if err := os.WriteFile(rubricPath, []byte("# qualification grading rubric\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	digests, err := ComputeQualificationPreregistrationDigests(context.Background(), QualificationPreregistrationDigestInputs{
		DatasetPath: datasetPath, CandidateRoot: root, CodeRankManifestPath: manifestPath,
		GraderPromptPath: rubricPath,
	})
	if err != nil {
		t.Fatalf("compute digests: %v", err)
	}

	// The emitted arms must survive the gate's own arm validator. That is the
	// only check worth making here: it is the exact function that would
	// refuse the operator's file.
	var fragment struct {
		Arms map[QualificationArm]ArmPin `json:"arms"`
	}
	if err := json.Unmarshal(digests.Fragment, &fragment); err != nil {
		t.Fatalf("fragment is not shaped like a preregistration: %v\n%s", err, digests.Fragment)
	}
	if err := validateQualificationArms(fragment.Arms); err != nil {
		t.Fatalf("emitted arms are refused by the gate: %v\n%s", err, digests.Fragment)
	}
	if got := len(fragment.Arms); got != 4 {
		t.Fatalf("emitted %d arms, want 4", got)
	}
	if fragment.Arms[ArmCodeRank].ManifestSHA256 != SHA256Hex(mustReadFile(t, manifestPath)) {
		t.Fatal("the CodeRank manifest digest is not the digest of the manifest file bytes")
	}
	assertUnresolved(t, digests, "bootstrap_seed", "reference_machine")
}

// The template is only useful if it still matches the schema. The loader
// decodes with DisallowUnknownFields, so a renamed or dropped field turns a
// helpful example into a file that refuses on its first line - and the operator
// who copied it has no way to tell a template problem from their own.
func TestPreregistrationExampleTemplateMatchesTheSchemaAndTheFrozenConstants(t *testing.T) {
	path := filepath.Join(repoRootForTest(t), "docs", "eval", "retrieval", "preregistration.example.json")
	decoder := json.NewDecoder(bytes.NewReader(mustReadFile(t, path)))
	decoder.DisallowUnknownFields()
	var pre QualificationPreregistration
	if err := decoder.Decode(&pre); err != nil {
		t.Fatalf("%s does not decode as a preregistration: %v", path, err)
	}

	if pre.SchemaVersion != QualificationSchemaVersion || pre.CompactVersion != QualificationCompactVersion ||
		pre.TokenBudget != QualificationTokenBudget || pre.BootstrapSamples != QualificationBootstrapSamples {
		t.Fatalf("the template's frozen header drifted from the constants: %+v", pre)
	}
	if err := validateQualificationThresholds(pre.Thresholds); err != nil {
		t.Fatalf("the template's threshold block is not the frozen gate: %v", err)
	}
	if pre.CandidateDiffSHA256 != EmptyCandidateDiffSHA256() {
		t.Fatalf("the template names candidate_diff_sha256 %s; the only value a run can carry is %s", pre.CandidateDiffSHA256, EmptyCandidateDiffSHA256())
	}
	if pre.ReaderPromptSHA256 != SHA256Hex([]byte(RaterInstructions)) {
		t.Fatalf("the template's reader_prompt_sha256 is not the digest of the frozen rater instruction block; RaterInstructions changed")
	}
	if len(pre.Arms) != 4 {
		t.Fatalf("the template declares %d arms, want 4", len(pre.Arms))
	}
	for _, arm := range []struct {
		name      QualificationArm
		maxTokens int
	}{{ArmPotion512, 512}, {ArmPotion8192, 8192}} {
		wantID, wantAdmission := qualificationPotionIdentity(arm.maxTokens)
		pin := pre.Arms[arm.name]
		if pin.EmbedderID != wantID || pin.AdmissionSHA256 != wantAdmission {
			t.Fatalf("the template's %s identity drifted from the pin table; regenerate it with the digests subcommand", arm.name)
		}
		if pin.ManifestSHA256 != SHA256Hex([]byte(PinnedPotionArtifactManifest())) {
			t.Fatalf("the template's %s manifest digest drifted from the pinned artifact listing", arm.name)
		}
	}

	// The template must stay obviously non-live. A reader who mistakes a
	// placeholder for a real pin has been handed a run bound to nothing.
	if !strings.Contains(string(mustReadFile(t, path)), strings.Repeat("0", 64)) {
		t.Fatal("the template no longer marks its derived digests as placeholders")
	}
	if err := ValidateQualificationPreregistration(pre); err == nil {
		t.Fatal("the example template validates as a real preregistration; an example must never be runnable")
	}
}

func assertUnresolved(t *testing.T, digests QualificationPreregistrationDigests, want ...string) {
	t.Helper()
	got := make(map[string]bool, len(digests.Unresolved))
	for _, field := range digests.Unresolved {
		if strings.TrimSpace(field.Requires) == "" || strings.TrimSpace(field.Recipe) == "" {
			t.Fatalf("unresolved field %q has no actionable note: %+v", field.Field, field)
		}
		got[field.Field] = true
	}
	for _, field := range want {
		if !got[field] {
			t.Fatalf("expected %s to be reported as unresolved, got %+v", field, digests.Unresolved)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("unresolved set is %+v, want exactly %v", digests.Unresolved, want)
	}

}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// preregistrationDigestFixture is a one-commit repository standing in for the
// candidate worktree.
func preregistrationDigestFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "eval@example.invalid"},
		{"config", "user.name", "Eval"},
	} {
		if output, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "tracked.txt"}, {"commit", "-q", "-m", "candidate"}} {
		if output, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return root, strings.TrimSpace(string(out))
}

func writePreregistrationDatasetFixture(t *testing.T, repoSHA string) string {
	t.Helper()
	dataset := Dataset{
		SchemaVersion: SchemaVersion, ID: "preregistration-digest-fixture", Repo: "fixture",
		RepoSHA: repoSHA, Language: "en", EvidenceClass: "independent-curator-annotated-and-reviewed",
		RelevantMinGrade: GradeMax,
		Queries: []Query{{
			ID: "q1", Stratum: StratumExactPath, Split: SplitDev, Language: "en",
			Text: "where is the loader", FamilyID: "f1", Provenance: "fixture",
			Judgements: []Judgement{{
				Path: "tracked.txt", StartLine: 1, EndLine: 1, Anchor: "candidate",
				Grade: GradeMax, Reason: "fixture span", Annotator: "fixture", Reviewer: "fixture",
			}},
		}},
	}
	raw, err := json.MarshalIndent(dataset, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "dataset.json")
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// writePreregistrationManifestFixture writes a manifest that satisfies
// coderank.Manifest.Validate and the qualification arm's extra pins, so the
// test exercises the real derivation rather than a stub.
func writePreregistrationManifestFixture(t *testing.T) string {
	t.Helper()
	manifest := coderank.Manifest{
		SchemaVersion: 1, Protocol: coderank.ProtocolVersion, Endpoint: "http://127.0.0.1:8765",
		Model: coderank.ArtifactPin{
			ID: "nomic-ai/CodeRankEmbed", Revision: strings.Repeat("a", 40), SHA256: strings.Repeat("1", 64),
		},
		Tokenizer: coderank.ArtifactPin{
			ID: "nomic-ai/CodeRankEmbed", Revision: strings.Repeat("a", 40), SHA256: strings.Repeat("2", 64),
		},
		Runtime:   coderank.RuntimePin{Name: "sentence-transformers", Version: "3.0.1", SHA256: strings.Repeat("3", 64)},
		Dimension: 768, Precision: "float32", Normalization: "l2", Compute: "cpu",
		Admission: coderank.AdmissionPin{MaxTokens: 8192, Reserve: 0, Algorithm: "first-n-tokens", AlgorithmVersion: "1"},
		Query: coderank.QueryProfilePin{
			ID: "coderank-code-search-query", Version: "1", Instruction: coderank.QueryInstruction,
			InstructionSHA256: SHA256Hex([]byte(coderank.QueryInstruction)),
		},
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "coderank.json")
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
