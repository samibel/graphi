package embed

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
)

func TestGenerateAndPersistEpochChangeBeforeCommitPreservesPriorActive(t *testing.T) {
	store, prior := readyGenerationFixture(t)
	e := newAttestedEmbedder("epoch-1")
	e.changeEpochAfterEmbed = "epoch-2"

	_, err := GenerateAndPersist(t.Context(), registryWith(t, e), changedFixtureNodes(t), fixtureDocuments(), NewIndex(), store, "graph-2")
	assertGenerationCommitAttestationFailure(t, err)
	assertPriorActive(t, store, prior)
}

func TestGenerateAndPersistCarryForwardStillReattestsBeforeCommit(t *testing.T) {
	store, prior := readyGenerationFixture(t)
	e := newAttestedEmbedder("epoch-1")
	e.changeEpochOnAttestation = 2
	e.changeEpochTo = "epoch-2"

	_, err := GenerateAndPersist(t.Context(), registryWith(t, e), fixtureNodes(t), fixtureDocuments(), NewIndex(), store, "graph-2")
	assertGenerationCommitAttestationFailure(t, err)
	if e.embedCalls != 0 {
		t.Fatalf("Embed calls = %d, want 0 for carry-forward-only build", e.embedCalls)
	}
	assertPriorActive(t, store, prior)
}

func TestGenerateAndPersistEmptyNodesStillAttestsBeforeCommit(t *testing.T) {
	store, prior := readyGenerationFixture(t)
	e := newAttestedEmbedder("epoch-1")
	e.changeEpochOnAttestation = 2
	e.changeEpochTo = "epoch-2"

	_, err := GenerateAndPersist(t.Context(), registryWith(t, e), nil, fixtureDocuments(), NewIndex(), store, "graph-2")
	assertGenerationCommitAttestationFailure(t, err)
	assertPriorActive(t, store, prior)
}

func TestGenerateAndPersistInitialAttestationFailurePreservesPriorActive(t *testing.T) {
	store, prior := readyGenerationFixture(t)
	e := newAttestedEmbedder("epoch-1")
	e.observed.Epoch = "epoch-2"

	_, err := GenerateAndPersist(t.Context(), registryWith(t, e), fixtureNodes(t), fixtureDocuments(), NewIndex(), store, "graph-2")
	var mismatch *RuntimeAttestationError
	if !errors.As(err, &mismatch) || mismatch.Phase != "before generation build" {
		t.Fatalf("error = %T %v, want initial RuntimeAttestationError", err, err)
	}
	assertPriorActive(t, store, prior)
}

func assertGenerationCommitAttestationFailure(t *testing.T, err error) {
	t.Helper()
	var mismatch *RuntimeAttestationError
	if !errors.As(err, &mismatch) || mismatch.Phase != "before generation commit" {
		t.Fatalf("error = %T %v, want pre-commit RuntimeAttestationError", err, err)
	}
}

func assertPriorActive(t *testing.T, store GenerationStore, prior Generation) {
	t.Helper()
	got, state, err := store.Active(t.Context(), prior.Fingerprint, nil)
	if err != nil || state != StateReady || got.ID != prior.ID {
		t.Fatalf("active=%+v state=%s err=%v, want prior ready generation %s", got, state, err, prior.ID)
	}
}

func readyGenerationFixture(t *testing.T) (GenerationStore, Generation) {
	t.Helper()
	store := NewMemGenerationStore()
	seed := NewMockEmbedder(8)
	if _, err := GenerateAndPersist(t.Context(), registryWith(t, seed), fixtureNodes(t), fixtureDocuments(), NewIndex(), store, "graph-2"); err != nil {
		t.Fatalf("seed generation: %v", err)
	}
	prior, state, err := store.Active(t.Context(), FingerprintFor(seed, "graph-2"), nil)
	if err != nil || state != StateReady || prior.ID == "" {
		t.Fatalf("seed active=%+v state=%s err=%v", prior, state, err)
	}
	return store, prior
}

func registryWith(t *testing.T, emb Embedder) *Registry {
	t.Helper()
	reg := NewRegistry()
	reg.Register(emb)
	return reg
}

func fixtureNodes(t *testing.T) []model.Node {
	t.Helper()
	n, err := model.NewNode("function", "example.Work", "example/work.go", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	return []model.Node{n}
}

func changedFixtureNodes(t *testing.T) []model.Node {
	t.Helper()
	n, err := model.NewNode("function", "example.ChangedWork", "example/work.go", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	return []model.Node{n}
}

func fixtureDocuments() DocumentSource { return V1DocumentSource{} }

type attestedEmbedder struct {
	*MockEmbedder
	expected                 RuntimeAttestation
	observed                 RuntimeAttestation
	changeEpochAfterEmbed    string
	changeEpochOnAttestation int
	changeEpochTo            string
	attestationCalls         int
	embedCalls               int
}

func newAttestedEmbedder(epoch string) *attestedEmbedder {
	attestation := RuntimeAttestation{IdentityDigest: strings.Repeat("a", 64), Epoch: epoch}
	return &attestedEmbedder{
		MockEmbedder: NewMockEmbedder(8),
		expected:     attestation,
		observed:     attestation,
	}
}

func (e *attestedEmbedder) ExpectedRuntimeAttestation() RuntimeAttestation { return e.expected }

func (e *attestedEmbedder) RuntimeAttestation(context.Context) (RuntimeAttestation, error) {
	e.attestationCalls++
	if e.changeEpochOnAttestation == e.attestationCalls {
		e.observed.Epoch = e.changeEpochTo
	}
	return e.observed, nil
}

func (e *attestedEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	e.embedCalls += len(texts)
	vectors, err := e.MockEmbedder.Embed(ctx, texts)
	if e.changeEpochAfterEmbed != "" {
		e.observed.Epoch = e.changeEpochAfterEmbed
	}
	return vectors, err
}
