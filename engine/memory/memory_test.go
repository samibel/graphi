package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type testLedger struct {
	recalls []testRecall
}

type testRecall struct {
	entryCount  int
	savedTokens int64
}

func (tl *testLedger) RecordRecall(ctx context.Context, entryCount int, savedTokens int64) error {
	tl.recalls = append(tl.recalls, testRecall{entryCount: entryCount, savedTokens: savedTokens})
	return nil
}

func TestStoreRecallAndForget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.jsonl")
	tl := &testLedger{}

	s, err := Open(path, tl)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	id, err := s.StoreMemory(ctx, "aria", "notes", []string{"go", "refactor"}, "remember to extract helper")
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	entries, err := s.RecallMemory(ctx, Query{Scope: "aria"})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Payload != "remember to extract helper" {
		t.Fatalf("unexpected payload: %q", entries[0].Payload)
	}
	if len(tl.recalls) != 1 {
		t.Fatalf("expected 1 ledger recall, got %d", len(tl.recalls))
	}

	if err := s.ForgetMemory(ctx, id); err != nil {
		t.Fatalf("forget: %v", err)
	}
	entries, err = s.RecallMemory(ctx, Query{Scope: "aria"})
	if err != nil {
		t.Fatalf("recall after forget: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries after forget, got %d", len(entries))
	}
}

func TestStoreQueryByNotebookAndTag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.jsonl")
	s, err := Open(path, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	if _, err := s.StoreMemory(ctx, "aria", "todo", []string{"urgent"}, "fix bug"); err != nil {
		t.Fatalf("store: %v", err)
	}
	if _, err := s.StoreMemory(ctx, "aria", "notes", []string{"go"}, "extract helper"); err != nil {
		t.Fatalf("store: %v", err)
	}

	entries, err := s.RecallMemory(ctx, Query{Notebook: "todo"})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(entries) != 1 || entries[0].Payload != "fix bug" {
		t.Fatalf("unexpected entries: %+v", entries)
	}

	entries, err = s.RecallMemory(ctx, Query{TagPrefix: "urg"})
	if err != nil {
		t.Fatalf("recall by tag: %v", err)
	}
	if len(entries) != 1 || entries[0].Payload != "fix bug" {
		t.Fatalf("unexpected tag entries: %+v", entries)
	}
}

func TestStoreTimeRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.jsonl")
	s, err := Open(path, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	before := time.Now().UTC().Add(-time.Hour).UnixNano()
	if _, err := s.StoreMemory(ctx, "aria", "notes", nil, "old note"); err != nil {
		t.Fatalf("store old: %v", err)
	}
	after := time.Now().UTC().Add(time.Hour).UnixNano()

	entries, err := s.RecallMemory(ctx, Query{Scope: "aria", CreatedMin: before, CreatedMax: after})
	if err != nil {
		t.Fatalf("recall range: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry in range, got %d", len(entries))
	}

	entries, err = s.RecallMemory(ctx, Query{Scope: "aria", CreatedMin: after})
	if err != nil {
		t.Fatalf("recall future: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 future entries, got %d", len(entries))
	}
}

func TestStoreDeterminism(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.jsonl")

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		s, err := Open(path, nil)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if _, err := s.StoreMemory(ctx, "aria", "notes", []string{"go", "refactor"}, "deterministic note"); err != nil {
			t.Fatalf("store %d: %v", i, err)
		}
		s.Close()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("expected non-empty journal")
	}

	// Journal must contain exactly three valid entries with monotonically
	// increasing IDs (mem-1, mem-2, mem-3).
	lines := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	for i, line := range lines {
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatalf("line %d unmarshal: %v", i, err)
		}
		wantID := ID(fmt.Sprintf("mem-%d", i+1))
		if e.ID != wantID {
			t.Fatalf("line %d expected %s, got %s", i, wantID, e.ID)
		}
	}
}

func TestStorePersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.jsonl")

	ctx := context.Background()
	{
		s, err := Open(path, nil)
		if err != nil {
			t.Fatalf("open first: %v", err)
		}
		if _, err := s.StoreMemory(ctx, "aria", "notes", []string{"go"}, "persist me"); err != nil {
			t.Fatalf("store first: %v", err)
		}
		s.Close()
	}

	{
		s, err := Open(path, nil)
		if err != nil {
			t.Fatalf("open second: %v", err)
		}
		defer s.Close()
		entries, err := s.RecallMemory(ctx, Query{Scope: "aria"})
		if err != nil {
			t.Fatalf("recall second: %v", err)
		}
		if len(entries) != 1 || entries[0].Payload != "persist me" {
			t.Fatalf("unexpected persisted entries: %+v", entries)
		}
	}
}
