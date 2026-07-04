package memory

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestStoreWithProvenance(t *testing.T) {
	s, err := NewMemStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	id, err := s.StoreMemoryWithProvenance(context.Background(), ProvenanceInput{
		Scope:      "repo",
		Notebook:   "decisions",
		Payload:    "use sqlite",
		Kind:       "decision",
		Source:     "architect-review",
		Confidence: "confirmed",
		Evidence:   "docs/adr/001.md:12",
	})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	entries, err := s.RecallMemory(context.Background(), Query{Scope: "repo"})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Kind != "decision" || e.Source != "architect-review" || e.Confidence != "confirmed" || e.Evidence != "docs/adr/001.md:12" {
		t.Fatalf("provenance fields not round-tripped: %+v", e)
	}
	if e.CreatedAt == 0 || e.UpdatedAt == 0 {
		t.Fatal("expected created_at and updated_at")
	}
	if e.ID != id {
		t.Fatalf("id mismatch: %s vs %s", e.ID, id)
	}
}

func TestOverwritePreservesCreatedAt(t *testing.T) {
	s, err := NewMemStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	id, err := s.StoreMemoryWithProvenance(context.Background(), ProvenanceInput{
		Scope:   "repo",
		Payload: "original",
	})
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := s.RecallMemory(context.Background(), Query{Scope: "repo"})
	createdAt := entries[0].CreatedAt

	_, err = s.StoreMemoryWithProvenance(context.Background(), ProvenanceInput{
		Scope:       "repo",
		Payload:     "updated",
		OverwriteID: id,
	})
	if err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	entries, _ = s.RecallMemory(context.Background(), Query{Scope: "repo"})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after overwrite, got %d", len(entries))
	}
	if entries[0].Payload != "updated" {
		t.Fatalf("payload not updated: %s", entries[0].Payload)
	}
	if entries[0].CreatedAt != createdAt {
		t.Fatal("created_at changed on overwrite")
	}
}

func TestListAndExport(t *testing.T) {
	s, err := NewMemStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for i := 0; i < 3; i++ {
		_, err := s.StoreMemoryWithProvenance(context.Background(), ProvenanceInput{
			Scope:   "repo",
			Payload: strings.Repeat("x", i+1),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.ListMemory(context.Background(), Query{Scope: "repo"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(list))
	}

	var buf bytes.Buffer
	if err := s.ExportMemory(context.Background(), Query{Scope: "repo"}, &buf); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 exported lines, got %d", len(lines))
	}
}

func TestDetectSecret(t *testing.T) {
	if !detectSecret("AKIAIOSFODNN7EXAMPLE") {
		t.Fatal("expected AWS key to be flagged")
	}
	if !detectSecret("password=supersecret") {
		t.Fatal("expected password to be flagged")
	}
	if detectSecret("hello world") {
		t.Fatal("expected harmless payload not to be flagged")
	}
}

func TestStoreSecretSuspected(t *testing.T) {
	s, err := NewMemStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	id, err := s.StoreMemoryWithProvenance(context.Background(), ProvenanceInput{
		Scope:   "repo",
		Payload: "password=supersecret123",
	})
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := s.RecallMemory(context.Background(), Query{Scope: "repo"})
	if len(entries) != 1 || !entries[0].SecretSuspect {
		t.Fatalf("expected secret_suspected=true for %s", id)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewMemStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStoreRejectsUnknownKind(t *testing.T) {
	s := newTestStore(t)
	_, err := s.StoreMemoryWithProvenance(context.Background(), ProvenanceInput{
		Scope: "repo", Notebook: "n", Payload: "x", Kind: "vibe",
	})
	if !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("expected ErrInvalidKind for unknown kind, got %v", err)
	}
}

func TestStoreAcceptsAllValidKinds(t *testing.T) {
	s := newTestStore(t)
	for _, k := range ValidKinds() {
		if _, err := s.StoreMemoryWithProvenance(context.Background(), ProvenanceInput{
			Scope: "repo", Notebook: "n", Payload: "fact for " + k, Kind: k,
		}); err != nil {
			t.Fatalf("kind %q rejected: %v", k, err)
		}
	}
	// Empty kind stays allowed (untyped fact).
	if _, err := s.StoreMemoryWithProvenance(context.Background(), ProvenanceInput{
		Scope: "repo", Notebook: "n", Payload: "untyped",
	}); err != nil {
		t.Fatalf("empty kind rejected: %v", err)
	}
}
