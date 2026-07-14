package memory

// SW-119 (PRIV-01): the memory journal holds whatever an agent chose to
// remember — owner-only by contract: parent dirs created 0700, journal 0600,
// and a pre-existing too-wide journal migrated on open.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivacy_JournalOwnerOnly(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "nested", "state", "memory.jsonl")

	s, err := Open(path, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat journal: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("journal mode = %o, want 0600", got)
	}
	for _, dir := range []string{filepath.Join(base, "nested"), filepath.Join(base, "nested", "state")} {
		di, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if got := di.Mode().Perm(); got != 0o700 {
			t.Fatalf("created parent %s mode = %o, want 0700", dir, got)
		}
	}
}

func TestPrivacy_JournalModeMigratedOnOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("seed journal: %v", err)
	}

	s, err := Open(path, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat journal: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("pre-existing journal not migrated: mode = %o, want 0600", got)
	}
}
