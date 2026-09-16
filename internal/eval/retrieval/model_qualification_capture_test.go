package retrieval

import (
	"context"
	"encoding/json"
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
	} {
		second := first
		mutate(&second)
		if err := compareQualificationBuildDigests(first, second); err == nil {
			t.Fatal("two-build comparison accepted a changed artifact class")
		}
	}
}

func qualificationBuildDigestFixture() qualificationBuildInputs {
	payload := PreservedPayload{Bytes: []byte("payload\n"), SHA256: SHA256Hex([]byte("payload\n")), TokenCounts: []PayloadTokenCount{{TokenizerID: TokenizerID, Tokens: 2}}}
	return qualificationBuildInputs{
		Rows:              []embed.Row{{GenerationID: "independent-build-1", NodeID: "n1", DocumentID: "d1", TextHash: "text", Path: "a.go", StartLine: 1, EndLine: 2, SpanMethod: "whole", Vector: []float32{1, -0.5}}},
		QueryVectors:      map[string][]float32{"q-1": {0.25, 0.75}},
		Payloads:          []PreservedPayload{payload},
		AdmittedDocuments: []embed.SemanticDocument{{DocumentID: "d1", NodeID: "n1", Path: "a.go", StartLine: 1, EndLine: 2, TextHash: "text", Text: "admitted bytes", Truncated: true, Bound: "tokens", AdmissionTokenCount: 17, AdmissionLimit: 512, AdmissionAlgorithmID: "first-n-tokens@1"}},
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
