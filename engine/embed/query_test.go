package embed

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type attestedQueryFake struct {
	expected   RuntimeAttestation
	observed   RuntimeAttestation
	embedCalls int
}

func (e *attestedQueryFake) ID() string { return "attested-query-fake" }

func (e *attestedQueryFake) Dim() int { return 1 }

func (e *attestedQueryFake) Embed(context.Context, []string) ([][]float32, error) {
	e.embedCalls++
	return [][]float32{{1}}, nil
}

func (e *attestedQueryFake) EmbedQuery(context.Context, string) ([][]float32, error) {
	e.embedCalls++
	return [][]float32{{1}}, nil
}

func (e *attestedQueryFake) ExpectedRuntimeAttestation() RuntimeAttestation { return e.expected }

func (e *attestedQueryFake) RuntimeAttestation(context.Context) (RuntimeAttestation, error) {
	return e.observed, nil
}

func TestEmbedQueryRejectsEpochChangeBeforeEmbedding(t *testing.T) {
	e := &attestedQueryFake{
		expected: RuntimeAttestation{IdentityDigest: strings.Repeat("a", 64), Epoch: "epoch-1"},
		observed: RuntimeAttestation{IdentityDigest: strings.Repeat("a", 64), Epoch: "epoch-2"},
	}

	_, err := EmbedQuery(t.Context(), e, "where is config loaded")
	var mismatch *RuntimeAttestationError
	if !errors.As(err, &mismatch) || e.embedCalls != 0 {
		t.Fatalf("err=%v calls=%d", err, e.embedCalls)
	}
}

func TestEmbedQueryRejectsEpochChangeAfterEmbedding(t *testing.T) {
	e := &attestedQueryFake{
		expected: RuntimeAttestation{IdentityDigest: strings.Repeat("a", 64), Epoch: "epoch-1"},
		observed: RuntimeAttestation{IdentityDigest: strings.Repeat("a", 64), Epoch: "epoch-1"},
	}

	_, err := EmbedQuery(t.Context(), runtimeChangingQueryFake{attestedQueryFake: e}, "where is config loaded")
	var mismatch *RuntimeAttestationError
	if !errors.As(err, &mismatch) || e.embedCalls != 1 {
		t.Fatalf("err=%v calls=%d", err, e.embedCalls)
	}
}

type runtimeChangingQueryFake struct {
	*attestedQueryFake
}

func (e runtimeChangingQueryFake) EmbedQuery(ctx context.Context, query string) ([][]float32, error) {
	vectors, err := e.attestedQueryFake.EmbedQuery(ctx, query)
	e.observed.Epoch = "epoch-2"
	return vectors, err
}

type legacyQueryFake struct {
	embedCalls int
}

func (e *legacyQueryFake) ID() string { return "legacy-query-fake" }

func (e *legacyQueryFake) Dim() int { return 1 }

func (e *legacyQueryFake) Embed(context.Context, []string) ([][]float32, error) {
	e.embedCalls++
	return [][]float32{{1}}, nil
}

func TestEmbedQueryCallsLegacyEmbedderOnce(t *testing.T) {
	e := &legacyQueryFake{}

	vectors, err := EmbedQuery(t.Context(), e, "where is config loaded")
	if err != nil {
		t.Fatal(err)
	}
	if e.embedCalls != 1 {
		t.Fatalf("calls=%d, want 1", e.embedCalls)
	}
	if len(vectors) != 1 || len(vectors[0]) != 1 || vectors[0][0] != 1 {
		t.Fatalf("vectors=%v", vectors)
	}
}
