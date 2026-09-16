package retrieval

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

func qualificationBuildDigestFixture() qualificationBuildInputs {
	payload := PreservedPayload{Bytes: []byte("payload\n"), SHA256: SHA256Hex([]byte("payload\n")), TokenCounts: []PayloadTokenCount{{TokenizerID: TokenizerID, Tokens: 2}}}
	oraclePayload := PreservedPayload{Bytes: []byte("oracle\n"), SHA256: SHA256Hex([]byte("oracle\n")), TokenCounts: []PayloadTokenCount{{TokenizerID: "cl100k_base", VocabularySHA256: strings.Repeat("a", 64), Tokens: 3}}}
	return qualificationBuildInputs{
		Rows:              []embed.Row{{GenerationID: "independent-build-1", NodeID: "n1", DocumentID: "d1", TextHash: "text", Path: "a.go", StartLine: 1, EndLine: 2, SpanMethod: "whole", Vector: []float32{1, -0.5}}},
		QueryVectors:      map[string][]float32{"q-1": {0.25, 0.75}},
		Payloads:          []PreservedPayload{payload},
		AdmittedDocuments: []embed.SemanticDocument{{DocumentID: "d1", NodeID: "n1", Path: "a.go", StartLine: 1, EndLine: 2, TextHash: "text", Text: "admitted bytes", Truncated: true, Bound: "tokens", AdmissionTokenCount: 17, AdmissionLimit: 512, AdmissionAlgorithmID: "first-n-tokens@1"}},
		OracleControls:    map[string]OracleControls{"q-1": {CurrentCandidatesOraclePacker: OracleBundle{ControlKind: OracleControlCurrentCandidatesOraclePacker, QueryID: "q-1", CandidateSHA256: strings.Repeat("b", 64), TokenCount: 3, Payload: oraclePayload}}},
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
