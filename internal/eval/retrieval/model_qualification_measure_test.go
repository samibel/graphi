package retrieval

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/embed/coderank"
)

func TestMeasureQualificationOperatingUsesExactScheduleAndSealsRawEvidence(t *testing.T) {
	options, deps, fake := qualificationMeasureFixture(t)
	evidence, err := measureQualificationOperating(t.Context(), options, deps)
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.queries) != 64+128 || len(evidence.Warmups) != 64 || len(evidence.Samples) != 128 {
		t.Fatalf("calls=%d warmups=%d samples=%d", len(fake.queries), len(evidence.Warmups), len(evidence.Samples))
	}
	for pass := 0; pass < 3; pass++ {
		for i, query := range options.dataset.Dataset.Queries {
			if got := fake.queries[pass*64+i]; got != query.Text {
				t.Fatalf("call %d=%q want %q", pass*64+i, got, query.Text)
			}
		}
	}
	if evidence.StartAttestation.Runtime != evidence.EndAttestation.Runtime || evidence.Samples[0].LatencyNS != int64(time.Millisecond) || evidence.Samples[0].UnknownTokens != 1 {
		t.Fatalf("evidence=%+v first=%+v", evidence.StartAttestation, evidence.Samples[0])
	}
	if evidence.SHA256 == "" || evidence.SourceTreeSHA256 != strings.Repeat("6", 64) || evidence.CorpusSHA256 == "" {
		t.Fatalf("unsealed or unbound evidence: %+v", evidence)
	}
	loaded, err := LoadOperatingEvidence(options.outputPath, options.pre, options.dataset)
	if err != nil || !reflect.DeepEqual(loaded, evidence) {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
}

func TestMeasureQualificationOperatingFailsClosedBeforeWriting(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*operatingMeasureOptions, *operatingMeasureDeps, *fakeQualificationOperatingEmbedder)
	}{
		{"767 documents", func(_ *operatingMeasureOptions, d *operatingMeasureDeps, _ *fakeQualificationOperatingEmbedder) {
			d.reindexResult.admittedDocuments = d.reindexResult.admittedDocuments[:767]
		}},
		{"769 documents", func(_ *operatingMeasureOptions, d *operatingMeasureDeps, _ *fakeQualificationOperatingEmbedder) {
			d.reindexResult.admittedDocuments = append(d.reindexResult.admittedDocuments, qualificationMeasureDocument(768))
		}},
		{"carry forward", func(_ *operatingMeasureOptions, d *operatingMeasureDeps, _ *fakeQualificationOperatingEmbedder) {
			d.reindexResult.reused = 1
		}},
		{"wrong fingerprint", func(_ *operatingMeasureOptions, d *operatingMeasureDeps, _ *fakeQualificationOperatingEmbedder) {
			d.reindexResult.fingerprintCanonical = "wrong"
		}},
		{"stale state", func(_ *operatingMeasureOptions, d *operatingMeasureDeps, _ *fakeQualificationOperatingEmbedder) {
			d.reindexResult.state = embed.StateStale
		}},
		{"not flushed", func(_ *operatingMeasureOptions, d *operatingMeasureDeps, _ *fakeQualificationOperatingEmbedder) {
			d.reindexResult.flushed = false
		}},
		{"not closed", func(_ *operatingMeasureOptions, d *operatingMeasureDeps, _ *fakeQualificationOperatingEmbedder) {
			d.reindexResult.closed = false
		}},
		{"not durable", func(_ *operatingMeasureOptions, d *operatingMeasureDeps, _ *fakeQualificationOperatingEmbedder) {
			d.reindexResult.durableReady = false
		}},
		{"machine mismatch", func(_ *operatingMeasureOptions, d *operatingMeasureDeps, _ *fakeQualificationOperatingEmbedder) {
			d.machine.CPU += " changed"
		}},
		{"end epoch drift", func(_ *operatingMeasureOptions, _ *operatingMeasureDeps, e *fakeQualificationOperatingEmbedder) {
			e.end.Runtime.Epoch = "epoch-2"
		}},
		{"query epoch drift", func(_ *operatingMeasureOptions, _ *operatingMeasureDeps, e *fakeQualificationOperatingEmbedder) {
			e.driftAtRuntimeCall = 4
		}},
		{"nonempty workdir", func(o *operatingMeasureOptions, _ *operatingMeasureDeps, _ *fakeQualificationOperatingEmbedder) {
			if err := os.MkdirAll(o.workDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(o.workDir, "old"), []byte("carry-forward"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			options, deps, fake := qualificationMeasureFixture(t)
			tc.mutate(&options, &deps, fake)
			if _, err := measureQualificationOperating(t.Context(), options, deps); err == nil {
				t.Fatal("accepted invalid operating measurement")
			}
			if _, err := os.Stat(options.outputPath); !os.IsNotExist(err) {
				t.Fatalf("invalid measurement wrote output: %v", err)
			}
		})
	}
}

func TestDetachedQualificationCheckoutIsolatesOriginAndRejectsSnapshotDrift(t *testing.T) {
	origin, head := qualificationGitFixture(t)
	called := false
	if err := withDetachedQualificationCheckout(t.Context(), origin, head, func(snapshot string) error {
		called = true
		digest, err := qualificationSourceTreeDigest(t.Context(), snapshot, head)
		if err != nil {
			return err
		}
		if digest == "" || snapshot == origin {
			t.Fatal("snapshot was not independently digested")
		}
		if err := exec.Command("git", "-C", snapshot, "symbolic-ref", "-q", "HEAD").Run(); err == nil {
			t.Fatal("snapshot is not detached")
		}
		return os.WriteFile(filepath.Join(origin, "origin-drift.txt"), []byte("does not alter snapshot"), 0o644)
	}); err != nil || !called {
		t.Fatalf("origin drift affected private snapshot: called=%t err=%v", called, err)
	}
	if err := withDetachedQualificationCheckout(t.Context(), origin, head, func(snapshot string) error {
		return os.WriteFile(filepath.Join(snapshot, "tracked.txt"), []byte("snapshot drift"), 0o644)
	}); err == nil {
		t.Fatal("accepted source snapshot drift during measurement")
	}
}

func TestQualificationMeasurementBindingsRejectCandidateDirtyOrDriftWithoutEvidence(t *testing.T) {
	for _, during := range []bool{false, true} {
		t.Run(map[bool]string{false: "dirty at start", true: "drift during measure"}[during], func(t *testing.T) {
			candidate, candidateHEAD := qualificationGitFixture(t)
			source, sourceHEAD := qualificationGitFixture(t)
			pre := qualificationPreregistrationFixture()
			pre.CandidateSHA, pre.CandidateDiffSHA256, pre.SourceRepoSHA = candidateHEAD, SHA256Hex(nil), sourceHEAD
			output := filepath.Join(t.TempDir(), "operating.json")
			if !during {
				if err := os.WriteFile(filepath.Join(candidate, "tracked.txt"), []byte("dirty"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			_, _, err := withQualificationMeasurementBindings(t.Context(), candidate, source, pre, func(_, _ string) error {
				if during {
					return os.WriteFile(filepath.Join(candidate, "tracked.txt"), []byte("drift"), 0o644)
				}
				return nil
			})
			if err == nil {
				t.Fatal("accepted dirty or drifting candidate")
			}
			if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
				t.Fatalf("invalid binding wrote evidence: %v", statErr)
			}
		})
	}
}

func TestQualificationOperatingReindexDoesNotEmbedReadinessQuery(t *testing.T) {
	emb := &recordingQualificationReindexEmbedder{id: "operating-reindex", dim: 3}
	result, err := qualificationOperatingReindex(t.Context(), fixtureRoot(t), t.TempDir(), emb)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.admittedDocuments) == 0 {
		t.Fatal("operating reindex admitted no documents")
	}
	if emb.queryCalls != 0 {
		t.Fatalf("operating reindex performed %d query embeddings before the explicit qualification schedule", emb.queryCalls)
	}
}

func qualificationGitFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	commands := [][]string{{"init", "-q"}, {"config", "user.email", "eval@example.invalid"}, {"config", "user.name", "Eval"}}
	for _, args := range commands {
		if output, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("pinned\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "tracked.txt"}, {"commit", "-q", "-m", "fixture"}} {
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

type fakeQualificationOperatingEmbedder struct {
	id                 string
	expected           embed.RuntimeAttestation
	start, end         coderank.OperatingAttestation
	queries            []string
	runtimeCalls       int
	driftAtRuntimeCall int
}

type recordingQualificationReindexEmbedder struct {
	id         string
	dim        int
	queryCalls int
}

func (e *recordingQualificationReindexEmbedder) ID() string { return e.id }
func (e *recordingQualificationReindexEmbedder) Dim() int   { return e.dim }
func (e *recordingQualificationReindexEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i := range vectors {
		vectors[i] = make([]float32, e.dim)
		vectors[i][0] = 1
	}
	return vectors, nil
}
func (e *recordingQualificationReindexEmbedder) EmbedQueryWithDiagnostics(_ context.Context, _ string) (embed.QueryEmbedding, error) {
	e.queryCalls++
	unknown := 0
	vector := make([]float32, e.dim)
	vector[0] = 1
	return embed.QueryEmbedding{Vectors: [][]float32{vector}, UnknownTokens: &unknown}, nil
}

func (f *fakeQualificationOperatingEmbedder) ID() string { return f.id }
func (f *fakeQualificationOperatingEmbedder) Dim() int   { return 768 }
func (f *fakeQualificationOperatingEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	return nil, nil
}
func (f *fakeQualificationOperatingEmbedder) EmbedQueryWithDiagnostics(_ context.Context, text string) (embed.QueryEmbedding, error) {
	f.queries = append(f.queries, text)
	unknown := (len(f.queries) - 1) % 3
	vector := make([]float32, 768)
	vector[0], vector[1], vector[2] = 1, 2, 3
	return embed.QueryEmbedding{Vectors: [][]float32{vector}, UnknownTokens: &unknown}, nil
}
func (f *fakeQualificationOperatingEmbedder) ExpectedRuntimeAttestation() embed.RuntimeAttestation {
	return f.expected
}
func (f *fakeQualificationOperatingEmbedder) RuntimeAttestation(context.Context) (embed.RuntimeAttestation, error) {
	f.runtimeCalls++
	if f.runtimeCalls == f.driftAtRuntimeCall {
		return embed.RuntimeAttestation{IdentityDigest: f.expected.IdentityDigest, Epoch: "drift"}, nil
	}
	return f.expected, nil
}
func (f *fakeQualificationOperatingEmbedder) OperatingAttestation(context.Context) (coderank.OperatingAttestation, error) {
	if len(f.queries) == 0 {
		return f.start, nil
	}
	return f.end, nil
}

func qualificationMeasureFixture(t *testing.T) (operatingMeasureOptions, operatingMeasureDeps, *fakeQualificationOperatingEmbedder) {
	t.Helper()
	in := passingQualificationInput(t)
	identity := in.Operating.StartAttestation.Runtime.IdentityDigest
	fake := &fakeQualificationOperatingEmbedder{id: in.Preregistration.Arms[ArmCodeRank].EmbedderID,
		expected: in.Operating.StartAttestation.Runtime, start: in.Operating.StartAttestation, end: in.Operating.EndAttestation}
	if identity == "" {
		t.Fatal("fixture identity is empty")
	}
	root := t.TempDir()
	options := operatingMeasureOptions{pre: in.Preregistration, dataset: in.Dataset, repoRoot: root,
		workDir: filepath.Join(root, "fresh-work"), outputPath: filepath.Join(root, "operating.json"),
		background: OperatingBackgroundLoad{Declaration: in.Preregistration.ReferenceMachine.BackgroundLoad, Protocol: "operator-observed-v1", Metadata: "test observation"}}
	now := time.Unix(0, 0)
	docs := make([]embed.SemanticDocument, 768)
	for i := range docs {
		docs[i] = qualificationMeasureDocument(i)
	}
	deps := operatingMeasureDeps{embedder: fake, machine: in.Preregistration.ReferenceMachine, sourceTreeSHA256: strings.Repeat("6", 64),
		now:            func() time.Time { current := now; now = now.Add(time.Millisecond); return current },
		writeOutput:    true,
		candidateStart: qualificationOperatingBindingFixture(in.Preregistration), candidateEnd: qualificationOperatingBindingFixture(in.Preregistration),
		reindexResult: operatingReindexResult{admittedDocuments: docs, embedded: 768,
			fingerprintCanonical: in.Preregistration.Arms[ArmCodeRank].FingerprintCanonical, state: embed.StateReady,
			flushed: true, closed: true, durableReady: true}}
	return options, deps, fake
}

func qualificationMeasureDocument(i int) embed.SemanticDocument {
	return embed.SemanticDocument{DocumentID: "doc-" + twoDigits(i), NodeID: model.NodeId("node-" + twoDigits(i)), Path: "p.go", Text: "document"}
}
