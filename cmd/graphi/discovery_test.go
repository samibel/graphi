package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/internal/state"
)

// TestResolveSession_DefaultDiscovery verifies the additive default-discovery
// path (SW-068, invariant #6): with NO state DB present for the cwd repo,
// resolveSession returns ("","") so query/search stay byte-identical to today's
// in-memory behavior; once a state DB exists it is discovered. Hermetic/offline:
// XDG_STATE_HOME is redirected to a temp dir and no daemon is started (so the
// socket stays empty via the liveness probe).
func TestResolveSession_DefaultDiscovery(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}

	// No state DB yet → in-memory fallback (both empty).
	if db, socket := resolveSession(repo, "", ""); db != "" || socket != "" {
		t.Fatalf("resolveSession with no state = (%q,%q), want (\"\",\"\")", db, socket)
	}

	// Create the per-repo state dir + an empty db.sqlite for this repo fingerprint.
	p, err := state.Resolve(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Ensure(p); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.DB, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}

	db, socket := resolveSession(repo, "", "")
	if db != p.DB {
		t.Fatalf("resolveSession db = %q, want %q", db, p.DB)
	}
	// No daemon was started, so the (dead) socket must be suppressed.
	if socket != "" {
		t.Fatalf("resolveSession socket = %q, want \"\" (no live daemon)", socket)
	}
}

// TestResolveSession_OverridesWin verifies explicit overrides bypass discovery.
func TestResolveSession_OverridesWin(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := t.TempDir()
	db, socket := resolveSession(repo, "/explicit/db.sqlite", "/explicit.sock")
	if db != "/explicit/db.sqlite" {
		t.Fatalf("db override = %q", db)
	}
	if socket != "/explicit.sock" {
		t.Fatalf("socket override = %q", socket)
	}
}
