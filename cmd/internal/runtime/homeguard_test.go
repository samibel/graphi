package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// homeWithMarker stages a fake home directory carrying a repo marker (the
// dotfiles-.git shape) plus a real project repo nested under it, and points
// HOME/XDG_STATE_HOME at isolated temp dirs.
func homeWithMarker(t *testing.T) (home, project string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(home, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	project = filepath.Join(home, "work", "mars")
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, project
}

// TestOpenSession_RefusesHomeDirectoryAsAutoRoot pins the home guard on both
// auto-detection paths: a cwd fallback that walks up to $HOME and a client
// root that resolves to $HOME must fail closed with the actionable refusal
// instead of silently indexing the entire home tree.
func TestOpenSession_RefusesHomeDirectoryAsAutoRoot(t *testing.T) {
	home, _ := homeWithMarker(t)

	// cwd fallback: a marker-less dir under home walks up to home's .git.
	cwd := filepath.Join(home, "Desktop")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := OpenSession(context.Background(), Options{Cwd: cwd})
	if !errors.Is(err, ErrNoRepository) {
		t.Fatalf("cwd-fallback home bind returned %v, want ErrNoRepository", err)
	}
	if !strings.Contains(err.Error(), "refusing to auto-bind your home directory") ||
		!strings.Contains(err.Error(), EnvAllowHomeRoot) {
		t.Fatalf("home refusal message not actionable: %v", err)
	}

	// Client roots: a root resolving to home is refused the same way.
	_, err = OpenSession(context.Background(), Options{Roots: []string{home}})
	if !errors.Is(err, ErrNoRepository) || !strings.Contains(err.Error(), "refusing to auto-bind your home directory") {
		t.Fatalf("client-root home bind returned %v, want home refusal", err)
	}
}

// TestOpenSession_LaterClientRootStillBinds pins that the guard skips a
// home-resolving candidate instead of failing the whole set: a later client
// root that is a real repository must still bind.
func TestOpenSession_LaterClientRootStillBinds(t *testing.T) {
	home, project := homeWithMarker(t)

	rt, err := OpenSession(context.Background(), Options{Roots: []string{home, project}})
	if err != nil {
		t.Fatalf("OpenSession with [home, project] roots: %v", err)
	}
	defer rt.Close()
	if !sameDir(rt.Root, project) {
		t.Fatalf("bound root = %q, want the project %q", rt.Root, project)
	}
}

// TestOpenSession_ProjectUnderHomeBindsNormally pins the non-regression: a
// repository nested under a marker-carrying home binds exactly as before when
// the walk starts inside it.
func TestOpenSession_ProjectUnderHomeBindsNormally(t *testing.T) {
	_, project := homeWithMarker(t)

	rt, err := OpenSession(context.Background(), Options{Cwd: project})
	if err != nil {
		t.Fatalf("OpenSession inside the project: %v", err)
	}
	defer rt.Close()
	if !sameDir(rt.Root, project) {
		t.Fatalf("bound root = %q, want the project %q", rt.Root, project)
	}
}

// TestGuardAutoRoot_OverrideAndFilesystemRoot pins the escape hatch and the
// filesystem-root arm.
func TestGuardAutoRoot_OverrideAndFilesystemRoot(t *testing.T) {
	home, _ := homeWithMarker(t)

	if err := guardAutoRoot(home); err == nil {
		t.Fatal("guardAutoRoot(home) must refuse without the override")
	}
	t.Setenv(EnvAllowHomeRoot, "1")
	if err := guardAutoRoot(home); err != nil {
		t.Fatalf("guardAutoRoot(home) with %s=1 returned %v, want nil", EnvAllowHomeRoot, err)
	}
	t.Setenv(EnvAllowHomeRoot, "")

	fsRoot := "/"
	if v := filepath.VolumeName(home); v != "" {
		fsRoot = v + string(os.PathSeparator)
	}
	err := guardAutoRoot(fsRoot)
	if err == nil || !strings.Contains(err.Error(), "filesystem root") {
		t.Fatalf("guardAutoRoot(%q) = %v, want filesystem-root refusal", fsRoot, err)
	}
}
