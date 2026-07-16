package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStateDir_XDGAndHomeFallback(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/xdgstate")
	if got, want := StateDir(), filepath.Join("/tmp/xdgstate", "graphi"); got != want {
		t.Fatalf("XDG: got %q want %q", got, want)
	}

	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/tester")
	if got, want := StateDir(), filepath.Join("/home/tester", ".graphi"); got != want {
		t.Fatalf("HOME fallback: got %q want %q", got, want)
	}
}

func TestFingerprint_StableAndDistinct(t *testing.T) {
	a := Fingerprint("/repo/one")
	if a != Fingerprint("/repo/one") {
		t.Fatal("fingerprint not stable across calls")
	}
	if len(a) != 16 {
		t.Fatalf("fingerprint length = %d, want 16", len(a))
	}
	// Path-only and clean-normalized: a non-clean form maps to the same fp.
	if a != Fingerprint("/repo/./one") {
		t.Fatal("fingerprint not path-clean stable")
	}
	if a == Fingerprint("/repo/two") {
		t.Fatal("distinct roots must yield distinct fingerprints")
	}
}

func TestRepoRoot_FindsGitAncestor(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "myrepo")
	deep := filepath.Join(repo, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := RepoRoot(deep)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(repo)
	if got != want {
		t.Fatalf("RepoRoot = %q, want %q", got, want)
	}
}

func TestRepoRoot_GitFileAlsoCounts(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "worktree")
	sub := filepath.Join(repo, "sub")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	// git worktrees use a `.git` FILE, not a dir.
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: /elsewhere"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := RepoRoot(sub)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(repo)
	if got != want {
		t.Fatalf("RepoRoot (.git file) = %q, want %q", got, want)
	}
}

func TestDetectRepo_MarkerVsBareDir(t *testing.T) {
	// A directory with a real marker → ok, root is the marker dir.
	base := t.TempDir()
	repo := filepath.Join(base, "withgit")
	deep := filepath.Join(repo, "x", "y")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, ok := DetectRepo(deep)
	if !ok {
		t.Fatal("DetectRepo with .git marker should be ok")
	}
	want, _ := filepath.Abs(repo)
	if got != want {
		t.Fatalf("DetectRepo root = %q, want %q", got, want)
	}

	// go.mod also counts as a marker.
	modRepo := filepath.Join(base, "withmod")
	if err := os.MkdirAll(modRepo, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modRepo, "go.mod"), []byte("module x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r, ok := DetectRepo(modRepo); !ok || r != mustAbs(t, modRepo) {
		t.Fatalf("DetectRepo go.mod = (%q,%v), want (%q,true)", r, ok, mustAbs(t, modRepo))
	}

	// A bare temp dir with NO marker → !ok (no bare-cwd fallback).
	bare := t.TempDir()
	if r, ok := DetectRepo(bare); ok {
		t.Fatalf("DetectRepo on bare dir should be !ok, got root=%q", r)
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestResolve_LayoutExactness(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/xdgstate")
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := Resolve(repo)
	if err != nil {
		t.Fatal(err)
	}
	wantRoot, _ := filepath.Abs(repo)
	if p.Root != wantRoot {
		t.Fatalf("Root = %q, want %q", p.Root, wantRoot)
	}
	wantDir := filepath.Join("/tmp/xdgstate", "graphi", Fingerprint(wantRoot))
	if p.Dir != wantDir {
		t.Fatalf("Dir = %q, want %q", p.Dir, wantDir)
	}
	checks := map[string]string{
		"DB":       filepath.Join(wantDir, "db.sqlite"),
		"Socket":   filepath.Join(wantDir, "daemon.sock"),
		"Meta":     filepath.Join(wantDir, "meta"),
		"RepoFile": filepath.Join(wantDir, "repo.json"),
	}
	got := map[string]string{"DB": p.DB, "Socket": p.Socket, "Meta": p.Meta, "RepoFile": p.RepoFile}
	for k, want := range checks {
		if got[k] != want {
			t.Fatalf("%s = %q, want %q", k, got[k], want)
		}
	}
	if p.Fingerprint != Fingerprint(wantRoot) {
		t.Fatalf("Fingerprint = %q, want %q", p.Fingerprint, Fingerprint(wantRoot))
	}
}

func TestEnsure_PermsAndRepoJSON(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := Resolve(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := Ensure(p); err != nil {
		t.Fatal(err)
	}

	di, err := os.Stat(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Fatalf("dir perm = %o, want 0700", perm)
	}
	mi, err := os.Stat(p.Meta)
	if err != nil {
		t.Fatal(err)
	}
	if perm := mi.Mode().Perm(); perm != 0o700 {
		t.Fatalf("meta perm = %o, want 0700", perm)
	}
	ri, err := os.Stat(p.RepoFile)
	if err != nil {
		t.Fatal(err)
	}
	if perm := ri.Mode().Perm(); perm != 0o600 {
		t.Fatalf("repo.json perm = %o, want 0600", perm)
	}

	data, err := os.ReadFile(p.RepoFile)
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		AbsRoot     string `json:"abs_root"`
		Fingerprint string `json:"fingerprint"`
		Created     string `json:"created"`
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.AbsRoot != p.Root {
		t.Fatalf("abs_root = %q, want %q", rec.AbsRoot, p.Root)
	}
	if rec.Fingerprint != p.Fingerprint {
		t.Fatalf("fingerprint = %q, want %q", rec.Fingerprint, p.Fingerprint)
	}
	if rec.Created != "-" {
		t.Fatalf("created = %q, want %q (must be a static placeholder, not a timestamp)", rec.Created, "-")
	}

	// Idempotent: a second Ensure leaves content byte-identical.
	if err := Ensure(p); err != nil {
		t.Fatal(err)
	}
	data2, _ := os.ReadFile(p.RepoFile)
	if string(data) != string(data2) {
		t.Fatal("repo.json content changed on second Ensure")
	}
}

func TestEnsure_MigratesExistingPermissiveStateBeforeEarlyReturn(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := Resolve(repo)
	if err != nil {
		t.Fatal(err)
	}

	// Model state written by an older release: MkdirAll would otherwise leave
	// these permissive modes untouched, and the existing repo.json causes Ensure
	// to return early.
	if err := os.MkdirAll(p.Meta, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p.Meta, 0o775); err != nil {
		t.Fatal(err)
	}
	wantContent := []byte(`{"legacy":true}`)
	if err := os.WriteFile(p.RepoFile, wantContent, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p.RepoFile, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Ensure(p); err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]os.FileMode{
		p.Dir:      0o700,
		p.Meta:     0o700,
		p.RepoFile: 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s perm = %o, want %o", path, got, want)
		}
	}
	gotContent, err := os.ReadFile(p.RepoFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotContent) != string(wantContent) {
		t.Fatalf("existing repo.json content changed: got %q want %q", gotContent, wantContent)
	}
}

func TestDiscoverDB_AbsentReturnsEmptyAndOverrideWins(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}

	// No state DB present → "".
	got, err := DiscoverDB(repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("DiscoverDB with no DB = %q, want \"\"", got)
	}

	// Override always wins, regardless of on-disk state.
	if got, _ := DiscoverDB(repo, "/explicit/db.sqlite"); got != "/explicit/db.sqlite" {
		t.Fatalf("override DiscoverDB = %q, want /explicit/db.sqlite", got)
	}

	// Create the DB file → discovered.
	p, _ := Resolve(repo)
	if err := Ensure(p); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.DB, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := DiscoverDB(repo, ""); got != p.DB {
		t.Fatalf("DiscoverDB with DB present = %q, want %q", got, p.DB)
	}
}

func TestDiscoverSocket_OverridePrecedence(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got, _ := DiscoverSocket(repo, "/explicit.sock"); got != "/explicit.sock" {
		t.Fatalf("override DiscoverSocket = %q, want /explicit.sock", got)
	}
	p, _ := Resolve(repo)
	if got, _ := DiscoverSocket(repo, ""); got != p.Socket {
		t.Fatalf("DiscoverSocket = %q, want %q", got, p.Socket)
	}
}
