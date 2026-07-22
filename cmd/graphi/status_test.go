package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hashStateDir maps every database file under dir to its content hash,
// excluding the transient -wal/-shm coordination sidecars mode=ro reads may
// create (see graphstore.OpenSQLiteReadOnly).
func hashStateDir(t *testing.T, dir string) map[string][32]byte {
	t.Helper()
	out := make(map[string][32]byte)
	err := filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || strings.HasSuffix(p, "-wal") || strings.HasSuffix(p, "-shm") {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		out[p] = sha256.Sum256(b)
		return nil
	})
	if err != nil {
		t.Fatalf("hash %s: %v", dir, err)
	}
	return out
}

func TestRunStatus_NotARepo(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var out bytes.Buffer
	if code := runStatusAt(t.TempDir(), nil, &out); code != 2 {
		t.Fatalf("status outside a repo exit = %d, want 2", code)
	}
}

func TestRunStatus_NeverIndexed(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	repo := writeGoRepo(t)
	gitRepo(t, repo, "main")

	var out bytes.Buffer
	if code := runStatusAt(repo, nil, &out); code != 1 {
		t.Fatalf("status of an unindexed repo exit = %d, want 1 (output: %s)", code, out.String())
	}
	if !strings.Contains(out.String(), "no graph for this repository yet") ||
		!strings.Contains(out.String(), "graphi sync") {
		t.Fatalf("unindexed status output missing hint, got: %s", out.String())
	}
	// A pure observer must not create the per-repo state dir.
	if matches, _ := filepath.Glob(filepath.Join(stateHome, "graphi", "*")); len(matches) != 0 {
		t.Fatalf("status created state entries: %v", matches)
	}
}

func TestRunStatus_CurrentThenStale(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("GRAPHI_EMBEDDER", "")
	repo := writeGoRepo(t)
	gitRepo(t, repo, "main")

	if code := runSyncAt(repo, nil, new(bytes.Buffer)); code != 0 {
		t.Fatal("seed sync failed")
	}
	before := hashStateDir(t, stateHome)

	// Current: exit 0, human output shows branch + synced lines.
	var out bytes.Buffer
	if code := runStatusAt(repo, nil, &out); code != 0 {
		t.Fatalf("status of a fresh sync exit = %d, want 0 (output: %s)", code, out.String())
	}
	for _, want := range []string{"repo:    " + repo, "branch:  main @ 1a2b3c4", "status:  current", "synced:"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("current status output missing %q, got: %s", want, out.String())
		}
	}

	// One edit + a branch switch: exit 1 with drift counts and the branch note.
	if err := os.WriteFile(filepath.Join(repo, "cart.go"), []byte("package shop\nfunc checkout() int { return 1 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/feature/login\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := runStatusAt(repo, nil, &out); code != 1 {
		t.Fatalf("stale status exit = %d, want 1 (output: %s)", code, out.String())
	}
	for _, want := range []string{
		"status:  stale — 0 added, 1 changed, 0 removed since last sync",
		"note:    branch changed since last sync: main → feature/login",
		"hint:    run 'graphi sync' to update the graph",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("stale status output missing %q, got: %s", want, out.String())
		}
	}

	// Read-only proof: two status passes changed no stored byte.
	if after := hashStateDir(t, stateHome); len(after) != len(before) {
		t.Fatalf("status changed the state dir file set: %d -> %d files", len(before), len(after))
	} else {
		for p, h := range before {
			if after[p] != h {
				t.Fatalf("status modified %s", p)
			}
		}
	}
}

func TestRunStatus_JSON(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("GRAPHI_EMBEDDER", "")
	repo := writeGoRepo(t)
	gitRepo(t, repo, "main")
	if code := runSyncAt(repo, nil, new(bytes.Buffer)); code != 0 {
		t.Fatal("seed sync failed")
	}

	var out bytes.Buffer
	if code := runStatusAt(repo, []string{"--json"}, &out); code != 0 {
		t.Fatalf("status --json exit = %d, want 0 (output: %s)", code, out.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("status --json is not valid JSON: %v\n%s", err, out.String())
	}
	if v, ok := doc["schema_version"].(float64); !ok || int(v) != statusJSONSchemaVersion {
		t.Fatalf("schema_version = %v, want %d", doc["schema_version"], statusJSONSchemaVersion)
	}
	// Every top-level key is always present.
	for _, key := range []string{"repo", "git", "db_path", "node_count", "profile", "last_sync", "index", "drift", "current", "recommendation"} {
		if _, ok := doc[key]; !ok {
			t.Fatalf("status --json missing key %q: %s", key, out.String())
		}
	}
	if cur, ok := doc["current"].(bool); !ok || !cur {
		t.Fatalf("current = %v, want true", doc["current"])
	}
	git := doc["git"].(map[string]any)
	if git["branch"] != "main" || git["commit"] != syncTestCommit {
		t.Fatalf("git block = %v, want main@%s", git, syncTestCommit)
	}
}
