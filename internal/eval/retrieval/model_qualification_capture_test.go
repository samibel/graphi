package retrieval

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/resolve"
	taskcompact "github.com/samibel/graphi/engine/agenttools/taskctx/compact"
	"github.com/samibel/graphi/engine/embed"
	engineretrieval "github.com/samibel/graphi/engine/retrieval"
	"github.com/samibel/graphi/engine/search"
)

type qualificationCaptureFixture struct {
	arm                 QualificationArm
	query               Query
	semanticState       embed.State
	expectedFingerprint embed.Fingerprint
	indexFingerprint    embed.Fingerprint
	searchFingerprint   embed.Fingerprint
	modelFingerprint    string
	retrieval           engineretrieval.Result
	semantic            []search.SemanticHit
	payload             PreservedPayload
	structured          taskcompact.Structured
}

type atomicQueryDiagnosticEmbedder struct{ calls int }

func (e *atomicQueryDiagnosticEmbedder) ID() string { return "atomic-query-diagnostic" }
func (e *atomicQueryDiagnosticEmbedder) Dim() int   { return 2 }
func (e *atomicQueryDiagnosticEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	panic("qualification diagnostics must use the atomic query path")
}
func (e *atomicQueryDiagnosticEmbedder) EmbedQueryWithDiagnostics(context.Context, string) (embed.QueryEmbedding, error) {
	e.calls++
	unknown := 3
	return embed.QueryEmbedding{Vectors: [][]float32{{0.25, 0.75}}, UnknownTokens: &unknown}, nil
}

func TestQualificationQueryEmbeddingCapturesVectorAndUnknownCountAtomically(t *testing.T) {
	e := &atomicQueryDiagnosticEmbedder{}
	vector, unknown, err := captureQualificationQueryEmbedding(t.Context(), e, "find parser", 2)
	if err != nil {
		t.Fatal(err)
	}
	if e.calls != 1 || !reflect.DeepEqual(vector, []float32{0.25, 0.75}) || !unknown.Available || unknown.Value == nil || *unknown.Value != 3 {
		t.Fatalf("vector=%v unknown=%+v calls=%d", vector, unknown, e.calls)
	}
}

func validQualificationCaptureFixture(t *testing.T) qualificationCaptureFixture {
	t.Helper()
	fp := embed.Fingerprint{
		ModelID: "fixture", Revision: "r1", ModelSHA256: strings.Repeat("a", 64),
		TokenizerSHA256: strings.Repeat("b", 64), Dim: 3, DocumentSchema: embed.DocumentSchema,
		GraphGeneration: "graph-1",
	}
	q := Query{ID: "q-1", Stratum: StratumAmbiguous, Split: SplitDev, Judgements: []Judgement{
		{Path: "answer.go", StartLine: 10, EndLine: 12, Grade: GradeMax},
	}}
	structured := taskcompact.Structured{
		Version: taskcompact.Version,
		Sources: []taskcompact.Source{{Path: "answer.go", StartLine: 10, EndLine: 12, Text: "a\nb\nc"}},
	}
	raw, err := json.Marshal(structured)
	if err != nil {
		t.Fatal(err)
	}
	payloadBytes := append([]byte(`{"jsonrpc":"2.0","id":1,"result":`), raw...)
	payloadBytes = append(payloadBytes, []byte("}\n")...)
	return qualificationCaptureFixture{
		arm: ArmCodeRank, query: q, semanticState: embed.StateReady,
		expectedFingerprint: fp, indexFingerprint: fp, searchFingerprint: fp,
		modelFingerprint: fp.Canonical(),
		retrieval: engineretrieval.Result{
			Rows:        []engineretrieval.Row{{Path: "answer.go", Span: "10-12", Explain: engineretrieval.Explain{SemanticRank: 2, LexicalRank: 5}}},
			Summary:     engineretrieval.Summary{RetrievalVersion: engineretrieval.Version, Strategy: "semantic_first", ModelFingerprint: fp.Canonical(), IndexFingerprint: fp.Canonical(), Limit: 50},
			Degradation: engineretrieval.StateReady,
		},
		semantic:   []search.SemanticHit{{SourcePath: "other.go", Line: 1}, {SourcePath: "answer.go", Line: 11}},
		payload:    PreservedPayload{Bytes: payloadBytes, SHA256: SHA256Hex(payloadBytes), ByteCount: len(payloadBytes), TokenCounts: []PayloadTokenCount{{TokenizerID: TokenizerID, Tokens: 117}}},
		structured: structured,
	}
}

func (f qualificationCaptureFixture) capture() (QualificationObservation, error) {
	return captureQualificationObservation(qualificationCaptureFacts{
		Arm: f.arm, Query: f.query, SemanticState: f.semanticState,
		ExpectedFingerprint: f.expectedFingerprint, IndexFingerprint: f.indexFingerprint,
		SearchFingerprint: f.searchFingerprint, ModelFingerprint: f.modelFingerprint,
		Retrieval: f.retrieval, SemanticHits: f.semantic, Payload: f.payload, Structured: f.structured,
	})
}

func TestQualificationCaptureRejectsFingerprintOrDegradation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*qualificationCaptureFixture)
	}{
		{name: "semantic state", mutate: func(f *qualificationCaptureFixture) { f.semanticState = embed.StateStale }},
		{name: "loaded index fingerprint", mutate: func(f *qualificationCaptureFixture) { f.indexFingerprint.ModelID = "wrong" }},
		{name: "search requested fingerprint", mutate: func(f *qualificationCaptureFixture) { f.searchFingerprint.ModelID = "wrong" }},
		{name: "retrieval model fingerprint", mutate: func(f *qualificationCaptureFixture) { f.retrieval.Summary.ModelFingerprint = "wrong" }},
		{name: "retrieval index fingerprint", mutate: func(f *qualificationCaptureFixture) { f.retrieval.Summary.IndexFingerprint = "wrong" }},
		{name: "degradation", mutate: func(f *qualificationCaptureFixture) { f.retrieval.Degradation = engineretrieval.StateLexicalOnly }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := validQualificationCaptureFixture(t)
			tc.mutate(&f)
			if _, err := f.capture(); err == nil {
				t.Fatal("invalid run was captured")
			}
		})
	}
}

func TestQualificationPayloadRetrieverSummaryMustMatchExpectedFingerprint(t *testing.T) {
	f := validQualificationCaptureFixture(t)
	want := f.expectedFingerprint.Canonical()
	summary := resolve.RetrieverResult{
		Degradation: string(engineretrieval.StateReady),
		Summary: resolve.RetrieverSummary{
			RetrievalVersion: engineretrieval.Version, Strategy: "semantic_first",
			ModelFingerprint: want, IndexFingerprint: want,
		},
	}
	if err := validateQualificationRetrieverSummary(ArmCodeRank, f.expectedFingerprint, summary); err != nil {
		t.Fatal(err)
	}
	summary.Summary.IndexFingerprint = "wrong"
	if err := validateQualificationRetrieverSummary(ArmCodeRank, f.expectedFingerprint, summary); err == nil {
		t.Fatal("payload-producing retrieval accepted a different index fingerprint")
	}
}

func TestQualificationCaptureAllowsLexicalBackfillInsideReadyRetrieval(t *testing.T) {
	f := validQualificationCaptureFixture(t)
	f.retrieval.Rows = append(f.retrieval.Rows, engineretrieval.Row{
		Path: "lexical.go", Span: "30-31", Explain: engineretrieval.Explain{LexicalRank: 1}, Region: "lexical_backfill",
	})
	got, err := f.capture()
	if err != nil {
		t.Fatal(err)
	}
	if got.Degraded || got.RetrievalState != string(engineretrieval.StateReady) {
		t.Fatalf("ready lexical backfill was labeled degradation: %+v", got)
	}
	if got.SemanticTop50 != (StageHit{Present: true, BestRank: 2}) || got.PostFusion != (StageHit{Present: true, BestRank: 1}) {
		t.Fatalf("stage observations = semantic %+v post-fusion %+v", got.SemanticTop50, got.PostFusion)
	}
	if !got.CompleteGrade3Span || got.BundleTokens != 117 {
		t.Fatalf("bundle observation = complete:%t tokens:%d", got.CompleteGrade3Span, got.BundleTokens)
	}
}

func TestQualificationLexicalControlHasNoSemanticIdentity(t *testing.T) {
	f := validQualificationCaptureFixture(t)
	f.arm = ArmLexical
	f.retrieval.Degradation = engineretrieval.StateLexicalOnly
	f.retrieval.Summary.Strategy = "lexical_only"
	f.retrieval.Summary.ModelFingerprint = ""
	f.retrieval.Summary.IndexFingerprint = ""
	if _, err := f.capture(); err == nil {
		t.Fatal("lexical control accepted loaded/search semantic identity")
	}
	f.expectedFingerprint = embed.Fingerprint{}
	f.indexFingerprint = embed.Fingerprint{}
	f.searchFingerprint = embed.Fingerprint{}
	f.modelFingerprint = ""
	f.semantic = nil
	got, err := f.capture()
	if err != nil {
		t.Fatal(err)
	}
	if got.Degraded || got.RetrievalState != string(engineretrieval.StateLexicalOnly) {
		t.Fatalf("lexical control was mislabeled: %+v", got)
	}
}

func TestQualificationCaptureBindsCodeRankManifestBytesBeforePayload(t *testing.T) {
	pre := qualificationPreregistrationFixture()
	manifest := []byte("pinned manifest bytes")
	pin := pre.Arms[ArmCodeRank]
	pin.ManifestSHA256 = SHA256Hex(manifest)
	pre.Arms[ArmCodeRank] = pin
	fp := mustQualificationFingerprint(t, pin.FingerprintCanonical)

	if err := validateQualificationCaptureBinding(ArmCodeRank, pre, fp, manifest); err != nil {
		t.Fatalf("valid binding: %v", err)
	}
	if err := validateQualificationCaptureBinding(ArmCodeRank, pre, fp, []byte("different")); err == nil {
		t.Fatal("capture accepted CodeRank manifest bytes outside the preregistered digest")
	}
	fp.ModelID = "wrong"
	if err := validateQualificationCaptureBinding(ArmCodeRank, pre, fp, manifest); err == nil {
		t.Fatal("capture accepted a fingerprint different from the preregistered canonical fingerprint")
	}
}

func TestStableQualificationManifestRejectsChangeDuringConstruction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coderank.json")
	original := []byte("original pinned manifest")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readStableQualificationManifest(path, SHA256Hex(original), func() error {
		return os.WriteFile(path, []byte("changed during constructor"), 0o644)
	}); err == nil {
		t.Fatal("manifest changed during constructor without invalidating capture")
	}
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readStableQualificationManifest(path, SHA256Hex(original), func() error { return nil })
	if err != nil || string(got) != string(original) {
		t.Fatalf("stable manifest = %q, %v", got, err)
	}
}

func TestQualificationPotionArmsConstructFromRawPinnedModelRevision(t *testing.T) {
	t.Setenv("GRAPHI_STATIC_MODEL_DIR", filepath.Join(t.TempDir(), "not-installed"))
	pre := qualificationPreregistrationFixture()
	for arm, wantMaxTokens := range map[QualificationArm]int{ArmPotion512: 512, ArmPotion8192: 8192} {
		t.Run(string(arm), func(t *testing.T) {
			emb, _, _, err := qualificationArmEmbedder(t.Context(), arm, pre, "")
			if err != nil {
				t.Fatalf("construct %s: %v", arm, err)
			}
			if emb == nil {
				t.Fatal("constructor returned nil embedder")
			}
			profile, ok := emb.(embed.AdmissionProfile)
			if !ok || profile.Profile().MaxTokens != wantMaxTokens {
				t.Fatalf("admission profile = %+v, want max_tokens=%d", profile, wantMaxTokens)
			}
		})
	}
}

func TestQualificationDiagnosticsDistinguishUnavailableFromObservedZero(t *testing.T) {
	f := validQualificationCaptureFixture(t)
	facts := qualificationCaptureFacts{
		Arm: f.arm, Query: f.query, SemanticState: f.semanticState,
		ExpectedFingerprint: f.expectedFingerprint, IndexFingerprint: f.indexFingerprint,
		SearchFingerprint: f.searchFingerprint, ModelFingerprint: f.modelFingerprint,
		Retrieval: f.retrieval, SemanticHits: f.semantic, Payload: f.payload, Structured: f.structured,
		QueryVector: []float32{0, 0, 0}, UnknownTokens: QualificationIntMetric{Available: false},
	}
	got, err := captureQualificationObservation(facts)
	if err != nil {
		t.Fatal(err)
	}
	if !got.QueryVectorAllZero.Available || got.QueryVectorAllZero.Value == nil || !*got.QueryVectorAllZero.Value {
		t.Fatalf("all-zero query vector was not measured: %+v", got.QueryVectorAllZero)
	}
	if got.UnknownTokens.Available {
		t.Fatalf("unknown-token count was fabricated as observed: %+v", got.UnknownTokens)
	}
	if err := validateQualificationRunDiagnostics(ArmCodeRank, []QualificationObservation{got}); err == nil {
		t.Fatal("run accepted an unavailable required query diagnostic")
	}
	zero := 0
	got.UnknownTokens = QualificationIntMetric{Available: true, Value: &zero}
	if err := validateQualificationRunDiagnostics(ArmCodeRank, []QualificationObservation{got}); err != nil {
		t.Fatalf("observed zero must be valid: %v", err)
	}
}

func TestQualificationDiagnosticsSerializeMeasuredZeroAndFalse(t *testing.T) {
	zero := 0
	no := false
	observation := QualificationObservation{
		UnknownTokens:      QualificationIntMetric{Available: true, Value: &zero},
		QueryVectorAllZero: QualificationBoolMetric{Available: true, Value: &no},
	}
	raw, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`"unknown_tokens":{"available":true,"value":0}`,
		`"query_vector_all_zero":{"available":true,"value":false}`,
	} {
		if !strings.Contains(string(raw), required) {
			t.Fatalf("measured zero/false omitted from qualification JSON: %s", raw)
		}
	}
	unavailable, err := json.Marshal(struct {
		Unknown QualificationIntMetric  `json:"unknown"`
		AllZero QualificationBoolMetric `json:"all_zero"`
	}{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(unavailable), `"value"`) {
		t.Fatalf("unavailable diagnostic encoded a zero/false value: %s", unavailable)
	}
}

func TestQualificationBuildDiagnosticsAreNotRepeatedPerQuery(t *testing.T) {
	d := qualificationBuildDiagnostics([]embed.Row{{Vector: []float32{0, 0}}, {Vector: []float32{1, 0}}}, 3)
	if d.AdmissionTruncations != 3 || d.DocumentZeroVectors != 1 {
		t.Fatalf("build diagnostics = %+v", d)
	}
	f := validQualificationCaptureFixture(t)
	got, err := f.capture()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"admission_truncations", "zero_vectors"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("per-query observation repeats build diagnostic %q: %s", forbidden, raw)
		}
	}
}

func TestQualificationCaptureProvenanceBindsIndependentRunDirectory(t *testing.T) {
	provenance := CandidateCaptureProvenance{CaptureVersion: "capture/1", GenerationID: "generation-1"}
	first, err := sealQualificationCaptureProvenanceRecord(QualificationCaptureProvenanceRecord{Arm: ArmCodeRank, Build: 1, WorkDir: "/runs/build-1", Provenance: provenance})
	if err != nil {
		t.Fatal(err)
	}
	second, err := sealQualificationCaptureProvenanceRecord(QualificationCaptureProvenanceRecord{Arm: ArmCodeRank, Build: 2, WorkDir: "/runs/build-2", Provenance: provenance})
	if err != nil {
		t.Fatal(err)
	}
	again, err := sealQualificationCaptureProvenanceRecord(QualificationCaptureProvenanceRecord{Arm: ArmCodeRank, Build: 1, WorkDir: "/runs/build-1", Provenance: provenance})
	if err != nil {
		t.Fatal(err)
	}
	if first.SHA256 == second.SHA256 || first.SHA256 != again.SHA256 || !isLowerHexDigest(first.SHA256, 64) {
		t.Fatalf("first=%q second=%q again=%q", first.SHA256, second.SHA256, again.SHA256)
	}
}

func TestQualificationCaptureProvenancePersistsResolvedWorkDir(t *testing.T) {
	provenance := CandidateCaptureProvenance{CaptureVersion: CandidateCaptureVersion}
	record, err := newQualificationCaptureProvenanceRecord(ArmCodeRank, 1, "/resolved/generated-workdir", provenance)
	if err != nil {
		t.Fatal(err)
	}
	if record.WorkDir != "/resolved/generated-workdir" {
		t.Fatalf("workdir=%q", record.WorkDir)
	}
}

func TestQualificationCaptureWorkDirCanonicalizesRelativeAndSymlinkPaths(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	realRelative := filepath.Join(root, "relative-target")
	realSymlink := filepath.Join(root, "symlink-target")
	for _, dir := range []string{realRelative, realSymlink} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	relative, err := filepath.Rel(cwd, realRelative)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "work-link")
	if err := os.Symlink(realSymlink, link); err != nil {
		t.Fatal(err)
	}
	resolvedRelative, cleanupRelative, err := resolveQualificationCaptureWorkDir(relative)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupRelative()
	resolvedSymlink, cleanupSymlink, err := resolveQualificationCaptureWorkDir(link)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupSymlink()
	wantRelative, _ := filepath.EvalSymlinks(realRelative)
	wantSymlink, _ := filepath.EvalSymlinks(realSymlink)
	if resolvedRelative != wantRelative || resolvedSymlink != wantSymlink || !filepath.IsAbs(resolvedRelative) || !filepath.IsAbs(resolvedSymlink) {
		t.Fatalf("relative=%q want=%q symlink=%q want=%q", resolvedRelative, wantRelative, resolvedSymlink, wantSymlink)
	}
	if _, _, err := resolveQualificationCaptureWorkDir(filepath.Join(root, "missing")); err == nil {
		t.Fatal("accepted unresolvable workdir")
	}

	in := passingQualificationInput(t)
	for i, resolved := range []string{resolvedRelative, resolvedSymlink} {
		digest := &in.BuildDigests[i]
		digest.CaptureProvenance.WorkDir = resolved
		digest.CaptureProvenance.Provenance.QualificationCaptureRunSHA256 = qualificationCaptureRunSHA(digest.Arm, resolved)
		digest.CaptureProvenance = mustSealQualificationCaptureProvenanceRecord(t, digest.CaptureProvenance)
	}
	resealQualificationBuildEvidence(t, &in)
	if _, err := EvaluateQualification(in); err != nil {
		t.Fatalf("evaluator rejected canonicalized workdirs: %v", err)
	}
}

func TestQualificationAtomicPublishLeavesNoPartialEvidenceAndCanRetry(t *testing.T) {
	parent := t.TempDir()
	out := filepath.Join(parent, "qualification")
	if err := os.Mkdir(out, 0o755); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("late reproducibility failure")
	err := publishQualificationAtomically(out, func(stage string) error {
		if err := os.MkdirAll(filepath.Join(stage, "build-2", "M3_coderank"), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(stage, "build-2", "M3_coderank", "capture.json"), []byte("partial"), 0o644); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("late failure = %v", err)
	}
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed capture published evidence: entries=%v err=%v", entries, err)
	}
	if err := publishQualificationAtomically(out, func(stage string) error {
		return os.WriteFile(filepath.Join(stage, "complete.json"), []byte("complete"), 0o644)
	}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(out, "complete.json")); err != nil || string(got) != "complete" {
		t.Fatalf("published retry = %q, %v", got, err)
	}
}

func TestQualificationGlobalPostBindingFailureIsNotPublished(t *testing.T) {
	out := filepath.Join(t.TempDir(), "qualification")
	if err := os.Mkdir(out, 0o755); err != nil {
		t.Fatal(err)
	}
	drift := errors.New("original candidate binding changed over the complete capture")
	err := publishQualificationAtomically(out, func(stage string) error {
		if err := os.WriteFile(filepath.Join(stage, "captures.json"), []byte("sealed but invalidated later"), 0o644); err != nil {
			return err
		}
		return drift
	})
	if !errors.Is(err, drift) {
		t.Fatalf("post-binding failure = %v", err)
	}
	entries, readErr := os.ReadDir(out)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("post-binding failure published evidence: entries=%v err=%v", entries, readErr)
	}
}

func TestQualificationPublishCleanupFailureAfterCommitStillSucceeds(t *testing.T) {
	out := filepath.Join(t.TempDir(), "qualification")
	if err := os.Mkdir(out, 0o755); err != nil {
		t.Fatal(err)
	}
	ops := defaultQualificationPublishFSOps()
	realRemove := ops.Remove
	removeCalls := 0
	ops.Remove = func(path string) error {
		removeCalls++
		if removeCalls == 2 {
			return errors.New("injected empty wrapper cleanup failure")
		}
		return realRemove(path)
	}
	err := publishQualificationAtomicallyWithFS(out, func(stage string) error {
		return os.WriteFile(filepath.Join(stage, "captures.json"), []byte("sealed"), 0o644)
	}, ops)
	if err != nil {
		t.Fatalf("cleanup after committed rename changed success into failure: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(out, "captures.json")); err != nil || string(got) != "sealed" {
		t.Fatalf("committed evidence = %q, %v", got, err)
	}
}

func TestQualificationPublishFinalRenameAndRollbackFailureRestoresRetryableOutput(t *testing.T) {
	out := filepath.Join(t.TempDir(), "qualification")
	if err := os.Mkdir(out, 0o755); err != nil {
		t.Fatal(err)
	}
	ops := defaultQualificationPublishFSOps()
	realRename := ops.Rename
	renameCalls := 0
	ops.Rename = func(oldPath, newPath string) error {
		renameCalls++
		switch renameCalls {
		case 2:
			return errors.New("injected final rename failure")
		case 3:
			return errors.New("injected rollback failure")
		default:
			return realRename(oldPath, newPath)
		}
	}
	err := publishQualificationAtomicallyWithFS(out, func(stage string) error {
		return os.WriteFile(filepath.Join(stage, "captures.json"), []byte("must not publish"), 0o644)
	}, ops)
	if err == nil || !strings.Contains(err.Error(), "rollback") {
		t.Fatalf("rename/rollback failure = %v", err)
	}
	entries, readErr := os.ReadDir(out)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("failed rollback left published evidence or non-retryable output: entries=%v err=%v", entries, readErr)
	}
	if err := publishQualificationAtomically(out, func(stage string) error {
		return os.WriteFile(filepath.Join(stage, "captures.json"), []byte("retry"), 0o644)
	}); err != nil {
		t.Fatalf("retry after failed rollback: %v", err)
	}
}

func TestQualificationCaptureRootRoundTripsAllEightSealedCaptures(t *testing.T) {
	in := passingQualificationInput(t)
	captures := make([]QualificationCaptureArtifact, 0, len(in.BuildDigests))
	for _, digest := range in.BuildDigests {
		observations := make([]QualificationObservation, 0, 64)
		bundles := make([]CapturedCandidateBundle, 0, 64)
		for _, observation := range in.Observations {
			if observation.Arm == digest.Arm {
				copy := observation
				observations = append(observations, copy)
				bundles = append(bundles, CapturedCandidateBundle{QueryID: observation.QueryID, Qualification: &copy})
			}
		}
		artifact := QualificationCaptureArtifact{SchemaVersion: QualificationCaptureSchemaVersion, Build: digest.Build, Arm: digest.Arm,
			Provenance: digest.CaptureProvenance.Provenance, Digest: digest, Observations: observations, Bundles: bundles}
		if digest.Arm == ArmCodeRank && digest.Build == 1 {
			oracle := in.OracleEvidence
			artifact.OracleEvidence = &oracle
		}
		captures = append(captures, mustSealQualificationCaptureArtifact(t, artifact))
	}
	root := mustSealQualificationCaptureRoot(t, QualificationCaptureRoot{SchemaVersion: QualificationCaptureSchemaVersion, Captures: captures})
	path := filepath.Join(t.TempDir(), "captures.json")
	if err := WriteQualificationCaptureRoot(path, root); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadQualificationCaptureRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	var loadedOracle *QualificationOracleEvidence
	for i := range loaded.Captures {
		if loaded.Captures[i].Arm == ArmCodeRank && loaded.Captures[i].Build == 1 {
			loadedOracle = loaded.Captures[i].OracleEvidence
		}
	}
	if !reflect.DeepEqual(loaded, root) || loadedOracle == nil || len(loadedOracle.Controls) != 64 {
		t.Fatalf("capture root did not round-trip complete sealed evidence")
	}
	tampered := root
	tampered.Captures = append([]QualificationCaptureArtifact(nil), root.Captures...)
	tampered.Captures[0].Observations = append([]QualificationObservation(nil), root.Captures[0].Observations...)
	tampered.Captures[0].Observations[0].BundleTokens++
	raw, err := json.Marshal(tampered)
	if err != nil {
		t.Fatal(err)
	}
	tamperedPath := filepath.Join(t.TempDir(), "tampered-captures.json")
	if err := os.WriteFile(tamperedPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadQualificationCaptureRoot(tamperedPath); err == nil {
		t.Fatal("strict capture root loader accepted mutated sealed evidence")
	}
}

func TestQualificationCaptureRootRejectsTamperAndWrongOracleOwner(t *testing.T) {
	in := passingQualificationInput(t)
	digest := in.BuildDigests[6]
	observations := make([]QualificationObservation, 0, 64)
	bundles := make([]CapturedCandidateBundle, 0, 64)
	for _, observation := range in.Observations {
		if observation.Arm == digest.Arm {
			copy := observation
			observations = append(observations, copy)
			bundles = append(bundles, CapturedCandidateBundle{QueryID: observation.QueryID, Qualification: &copy})
		}
	}
	oracle := in.OracleEvidence
	artifact := mustSealQualificationCaptureArtifact(t, QualificationCaptureArtifact{SchemaVersion: QualificationCaptureSchemaVersion, Build: 1, Arm: ArmCodeRank,
		Provenance: digest.CaptureProvenance.Provenance, Digest: digest, Observations: observations, Bundles: bundles, OracleEvidence: &oracle})
	t.Run("artifact mutation", func(t *testing.T) {
		mutated := artifact
		mutated.Observations = append([]QualificationObservation(nil), artifact.Observations...)
		mutated.Observations[0].CompleteGrade3Span = !mutated.Observations[0].CompleteGrade3Span
		if err := validateQualificationCaptureArtifact(mutated); err == nil {
			t.Fatal("accepted a mutated sealed artifact")
		}
	})
	t.Run("oracle relabel", func(t *testing.T) {
		mutated := artifact
		mutated.Arm = ArmPotion8192
		mutated = mustSealQualificationCaptureArtifact(t, mutated)
		if err := validateQualificationCaptureArtifact(mutated); err == nil {
			t.Fatalf("oracle relabel error = %v", err)
		}
	})
}

func mustSealQualificationCaptureArtifact(t *testing.T, artifact QualificationCaptureArtifact) QualificationCaptureArtifact {
	t.Helper()
	artifact.SHA256 = ""
	sealed, err := sealQualificationCaptureArtifact(artifact)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func mustSealQualificationCaptureRoot(t *testing.T, root QualificationCaptureRoot) QualificationCaptureRoot {
	t.Helper()
	root.SHA256 = ""
	sealed, err := sealQualificationCaptureRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func TestQualificationCapturePreregistrationRejectsTrailingJSON(t *testing.T) {
	raw, err := json.Marshal(qualificationPreregistrationFixture())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeQualificationCapturePreregistration(append(raw, []byte(`{}`)...)); err == nil {
		t.Fatal("capture accepted a second JSON value after preregistration")
	}
	if _, err := decodeQualificationCapturePreregistration(raw); err != nil {
		t.Fatalf("valid preregistration: %v", err)
	}
}

func TestQualificationBuildDigestExcludesOnlyGenerationID(t *testing.T) {
	base := qualificationBuildDigestFixture()
	first := buildQualificationDigest(ArmCodeRank, base)
	base.Rows[0].GenerationID = "independent-build-2"
	second := buildQualificationDigest(ArmCodeRank, base)
	if first != second {
		t.Fatalf("staging generation id changed reproducibility digest:\nfirst=%+v\nsecond=%+v", first, second)
	}
	base.Rows[0].Vector[0] = 2
	third := buildQualificationDigest(ArmCodeRank, base)
	if first.VectorBytesSHA256 == third.VectorBytesSHA256 || first.PersistedRowsSHA256 == third.PersistedRowsSHA256 {
		t.Fatal("raw vector mutation did not change vector and persisted-row digests")
	}
	base = qualificationBuildDigestFixture()
	base.Payloads[0].Bytes = append([]byte(nil), base.Payloads[0].Bytes...)
	base.Payloads[0].Bytes[0] ^= 1
	if first.BundlesSHA256 == buildQualificationDigest(ArmCodeRank, base).BundlesSHA256 {
		t.Fatal("payload-byte mutation did not change bundle digest")
	}
	base = qualificationBuildDigestFixture()
	base.AdmittedDocuments[0].Text = "different admitted bytes"
	changed := buildQualificationDigest(ArmCodeRank, base)
	if first.PersistedRowsSHA256 == changed.PersistedRowsSHA256 {
		t.Fatal("admitted document byte mutation did not change document-record digest")
	}
	base = qualificationBuildDigestFixture()
	base.AdmittedDocuments[0].AdmissionTokenCount++
	if first.TokenCountsSHA256 == buildQualificationDigest(ArmCodeRank, base).TokenCountsSHA256 {
		t.Fatal("admission token-count mutation did not change token-count digest")
	}
}

func TestQualificationBuildComparisonFailsAnyArtifactDifference(t *testing.T) {
	first := buildQualificationDigest(ArmCodeRank, qualificationBuildDigestFixture())
	if err := compareQualificationBuildDigests(first, first); err != nil {
		t.Fatalf("identical builds: %v", err)
	}
	for _, mutate := range []func(*QualificationBuildDigest){
		func(v *QualificationBuildDigest) { v.VectorBytesSHA256 = strings.Repeat("0", 64) },
		func(v *QualificationBuildDigest) { v.PersistedRowsSHA256 = strings.Repeat("1", 64) },
		func(v *QualificationBuildDigest) { v.BundlesSHA256 = strings.Repeat("2", 64) },
		func(v *QualificationBuildDigest) { v.TokenCountsSHA256 = strings.Repeat("3", 64) },
		func(v *QualificationBuildDigest) { v.OraclePayloadsSHA256 = strings.Repeat("4", 64) },
		func(v *QualificationBuildDigest) { v.OracleTokenCountsSHA256 = strings.Repeat("5", 64) },
		func(v *QualificationBuildDigest) { v.Diagnostics.AdmissionTruncations++ },
	} {
		second := first
		mutate(&second)
		if err := compareQualificationBuildDigests(first, second); err == nil {
			t.Fatal("two-build comparison accepted a changed artifact class")
		}
	}
}

func TestQualificationBuildComparisonRejectsOraclePayloadOrCountMismatch(t *testing.T) {
	base := qualificationBuildDigestFixture()
	first := buildQualificationDigest(ArmCodeRank, base)
	changed := base.OracleControls["q-1"]
	changed.CurrentCandidatesOraclePacker.Payload.Bytes = append([]byte(nil), changed.CurrentCandidatesOraclePacker.Payload.Bytes...)
	changed.CurrentCandidatesOraclePacker.Payload.Bytes[0] ^= 1
	base.OracleControls["q-1"] = changed
	second := buildQualificationDigest(ArmCodeRank, base)
	if first.OraclePayloadsSHA256 == second.OraclePayloadsSHA256 || compareQualificationBuildDigests(first, second) == nil {
		t.Fatal("two-build comparison accepted changed oracle payload bytes")
	}

	base = qualificationBuildDigestFixture()
	changed = base.OracleControls["q-1"]
	changed.CurrentCandidatesOraclePacker.TokenCount++
	base.OracleControls["q-1"] = changed
	third := buildQualificationDigest(ArmCodeRank, base)
	if first.OracleTokenCountsSHA256 == third.OracleTokenCountsSHA256 || compareQualificationBuildDigests(first, third) == nil {
		t.Fatal("two-build comparison accepted changed oracle real token count")
	}
}

func TestQualificationBuildComparisonRejectsQueryDiagnosticMismatch(t *testing.T) {
	base := qualificationBuildDigestFixture()
	first := buildQualificationDigest(ArmCodeRank, base)
	four := 4
	base.QueryDiagnostics["q-1"] = QualificationIntMetric{Available: true, Value: &four}
	second := buildQualificationDigest(ArmCodeRank, base)
	if first.QueryDiagnosticsSHA256 == second.QueryDiagnosticsSHA256 || compareQualificationBuildDigests(first, second) == nil {
		t.Fatal("two-build comparison accepted changed query diagnostics")
	}
}

func TestQualificationBuildDigestBindsCanonicalObservationsAndSealsItself(t *testing.T) {
	base := qualificationBuildDigestFixture()
	base.Observations = []QualificationObservation{
		{Arm: ArmCodeRank, QueryID: "q-2", Stratum: StratumNLBehaviour, BundleTokens: 2},
		{Arm: ArmCodeRank, QueryID: "q-1", Stratum: StratumExactPath, BundleTokens: 1},
	}
	first, err := sealQualificationBuildDigest(buildQualificationDigest(ArmCodeRank, base))
	if err != nil {
		t.Fatal(err)
	}
	base.Observations[0], base.Observations[1] = base.Observations[1], base.Observations[0]
	permuted, err := sealQualificationBuildDigest(buildQualificationDigest(ArmCodeRank, base))
	if err != nil {
		t.Fatal(err)
	}
	if first.ObservationsSHA256 != permuted.ObservationsSHA256 || first.SHA256 != permuted.SHA256 {
		t.Fatalf("observation order changed canonical digest: first=%+v permuted=%+v", first, permuted)
	}
	base.Observations[0].CompleteGrade3Span = true
	changed, err := sealQualificationBuildDigest(buildQualificationDigest(ArmCodeRank, base))
	if err != nil {
		t.Fatal(err)
	}
	if first.ObservationsSHA256 == changed.ObservationsSHA256 || compareQualificationBuildDigests(first, changed) == nil {
		t.Fatal("decision-relevant observation substitution did not invalidate the build digest")
	}
	tampered := first
	tampered.BundlesSHA256 = strings.Repeat("9", 64)
	if validateQualificationBuildDigestSeal(tampered) == nil {
		t.Fatal("reseal-free build digest tamper was accepted")
	}
}

func TestDetachedQualificationCheckoutIsPinnedAndDetectsSnapshotDrift(t *testing.T) {
	repo := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init")
	if err := os.WriteFile(filepath.Join(repo, "source.txt"), []byte("frozen\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "source.txt")
	runGit("commit", "-m", "frozen")
	frozen := runGit("rev-parse", "HEAD")
	if err := withDetachedQualificationCheckout(t.Context(), repo, frozen, func(snapshot string) error {
		if err := os.WriteFile(filepath.Join(repo, "source.txt"), []byte("origin changed\n"), 0o644); err != nil {
			return err
		}
		got, err := os.ReadFile(filepath.Join(snapshot, "source.txt"))
		if err != nil {
			return err
		}
		if string(got) != "frozen\n" {
			return errors.New("detached snapshot followed mutable origin")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	err := withDetachedQualificationCheckout(t.Context(), repo, frozen, func(snapshot string) error {
		_, observeErr := ObserveCandidateBinding(t.Context(), GitRepoProbe(), CandidateBindingOptions{
			CandidateRoot: repo, FrozenCandidateSHA: frozen, ExcludePath: QualificationCandidateExcludedPath,
			ExpectedCandidateDiffSHA256: SHA256Hex(nil), CheckoutRoot: snapshot, CheckoutSHA: frozen,
		})
		return observeErr
	})
	if err == nil || !strings.Contains(err.Error(), "candidate worktree") {
		t.Fatalf("dirty original candidate hidden by clean detached source snapshot: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "source.txt"), []byte("frozen\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = withDetachedQualificationCheckout(t.Context(), repo, frozen, func(snapshot string) error {
		return os.WriteFile(filepath.Join(snapshot, "source.txt"), []byte("snapshot drift\n"), 0o644)
	})
	if err == nil || !strings.Contains(err.Error(), "detached checkout changed during capture") {
		t.Fatalf("snapshot drift error = %v", err)
	}
}

func TestQualificationBindingPairRejectsCandidateDriftAfterCapture(t *testing.T) {
	const frozen = "0123456789abcdef0123456789abcdef01234567"
	const changed = "89abcdef0123456789abcdef0123456789abcdef"
	headCalls := 0
	probe := RepoProbe{
		HeadSHA: func(context.Context, string) (string, error) {
			headCalls++
			if headCalls == 1 {
				return frozen, nil
			}
			return changed, nil
		},
		WorktreeClean:         func(context.Context, string) (bool, error) { return true, nil },
		WorktreeCleanOutside:  func(context.Context, string, string) (bool, error) { return true, nil },
		PathsDifferingOutside: func(context.Context, string, string, string, string) ([]string, error) { return nil, nil },
		DiffOutside:           func(context.Context, string, string, string, string) ([]byte, error) { return nil, nil },
	}
	options := CandidateBindingOptions{CandidateRoot: "/candidate", FrozenCandidateSHA: frozen,
		ExcludePath: QualificationCandidateExcludedPath, ExpectedCandidateDiffSHA256: SHA256Hex(nil),
		CheckoutRoot: "/snapshot", CheckoutSHA: strings.Repeat("a", 40)}
	_, _, err := observeQualificationCaptureBindingPair(t.Context(), probe, options, func(CandidateBinding) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "binding changed during capture") {
		t.Fatalf("candidate post-capture drift error = %v", err)
	}
}

func qualificationBuildDigestFixture() qualificationBuildInputs {
	payload := PreservedPayload{Bytes: []byte("payload\n"), SHA256: SHA256Hex([]byte("payload\n")), TokenCounts: []PayloadTokenCount{{TokenizerID: TokenizerID, Tokens: 2}}}
	oraclePayload := PreservedPayload{Bytes: []byte("oracle\n"), SHA256: SHA256Hex([]byte("oracle\n")), TokenCounts: []PayloadTokenCount{{TokenizerID: "cl100k_base", VocabularySHA256: strings.Repeat("a", 64), Tokens: 3}}}
	return qualificationBuildInputs{
		Rows:              []embed.Row{{GenerationID: "independent-build-1", NodeID: "n1", DocumentID: "d1", TextHash: "text", Path: "a.go", StartLine: 1, EndLine: 2, SpanMethod: "whole", Vector: []float32{1, -0.5}}},
		QueryVectors:      map[string][]float32{"q-1": {0.25, 0.75}},
		QueryDiagnostics:  map[string]QualificationIntMetric{"q-1": {Available: true, Value: intPointer(3)}},
		Payloads:          []PreservedPayload{payload},
		AdmittedDocuments: []embed.SemanticDocument{{DocumentID: "d1", NodeID: "n1", Path: "a.go", StartLine: 1, EndLine: 2, TextHash: "text", Text: "admitted bytes", Truncated: true, Bound: "tokens", AdmissionTokenCount: 17, AdmissionLimit: 512, AdmissionAlgorithmID: "first-n-tokens@1"}},
		OracleControls:    map[string]OracleControls{"q-1": {CurrentCandidatesOraclePacker: OracleBundle{ControlKind: OracleControlCurrentCandidatesOraclePacker, QueryID: "q-1", CandidateSHA256: strings.Repeat("b", 64), TokenCount: 3, Payload: oraclePayload}}},
		Observations:      []QualificationObservation{{Arm: ArmCodeRank, QueryID: "q-1", Stratum: StratumExactPath, BundleTokens: 2}},
	}
}

func TestEmbeddedModelQualificationCapture(t *testing.T) {
	required := []string{
		"GRAPHI_QUALIFICATION_REPO", "GRAPHI_QUALIFICATION_DATASET",
		"GRAPHI_QUALIFICATION_PREREGISTRATION", "GRAPHI_CODERANK_MANIFEST",
		"GRAPHI_QUALIFICATION_OUT", "GRAPHI_STATIC_MODEL_DIR",
	}
	for _, name := range required {
		if strings.TrimSpace(os.Getenv(name)) == "" {
			t.Skipf("live qualification requires every input; %s is unset", name)
		}
	}
	if err := runEmbeddedModelQualificationCapture(context.Background(), qualificationEnvironmentFromOS()); err != nil {
		t.Fatal(err)
	}
}
