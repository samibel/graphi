package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpenSession_ExplicitRootWinsOverClientRootsAndCwd pins the top of the
// discovery precedence: an Options.Root pin binds even when transport roots
// and a detectable cwd point at other repositories.
func TestOpenSession_ExplicitRootWinsOverClientRootsAndCwd(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	pinned := repository(t, "pinned", "package fixture\nfunc Hello() string { return \"hello\" }\n")
	clientRepo := repository(t, "client", "package client\nfunc Wrong() {}\n")
	cwdRepo := repository(t, "cwd", "package cwd\nfunc AlsoWrong() {}\n")

	rt, err := OpenSession(context.Background(), Options{Root: pinned, Roots: []string{clientRepo}, Cwd: cwdRepo})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer rt.Close()
	want, _ := filepath.Abs(pinned)
	if rt.Root != want {
		t.Fatalf("bound root = %q, want the pinned root %q", rt.Root, want)
	}
	result, err := rt.Client.Search(context.Background(), "Hello", 10)
	if err != nil {
		t.Fatalf("search pinned repository: %v", err)
	}
	if !contains(result, `"qualified_name":"fixture.Hello"`) {
		t.Fatalf("search did not use the pinned repository: %s", result)
	}
}

// TestOpenSession_ExplicitRootBypassesHomeGuard pins that an explicit root is
// deliberate intent: pinning the home directory binds without
// GRAPHI_ALLOW_HOME_ROOT, while auto-detection of the same directory refuses.
func TestOpenSession_ExplicitRootBypassesHomeGuard(t *testing.T) {
	home, _ := homeWithMarker(t)

	rt, err := OpenSession(context.Background(), Options{Root: home})
	if err != nil {
		t.Fatalf("OpenSession with the home directory pinned: %v", err)
	}
	defer rt.Close()
	if !sameDir(rt.Root, home) {
		t.Fatalf("bound root = %q, want the pinned home %q", rt.Root, home)
	}
}

// TestOpenSession_ExplicitRootPinsExactly_NoDetectionWalk pins the CLI -root
// semantics: the pinned directory is bound as-is, even when an upward walk
// from it would land on an enclosing repository marker.
func TestOpenSession_ExplicitRootPinsExactly_NoDetectionWalk(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := repository(t, "project", "package project\nfunc Outer() {}\n")
	sub := filepath.Join(project, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	rt, err := OpenSession(context.Background(), Options{Root: sub})
	if err != nil {
		t.Fatalf("OpenSession with a marker-less subdirectory pinned: %v", err)
	}
	defer rt.Close()
	want, _ := filepath.Abs(sub)
	if rt.Root != want {
		t.Fatalf("bound root = %q, want the exact pinned subdirectory %q (no upward walk)", rt.Root, want)
	}
}

// TestOpenSession_ExplicitRootMustBeExistingDirectory pins the fail-closed
// validation: a pin that does not exist or is not a directory surfaces
// ErrNoRepository at bind time instead of an opaque failure later.
func TestOpenSession_ExplicitRootMustBeExistingDirectory(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	_, err := OpenSession(context.Background(), Options{Root: filepath.Join(t.TempDir(), "missing")})
	if !errors.Is(err, ErrNoRepository) || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("nonexistent pin returned %v, want ErrNoRepository with does-not-exist detail", err)
	}

	file := filepath.Join(t.TempDir(), "plain.txt")
	if werr := os.WriteFile(file, []byte("x"), 0o644); werr != nil {
		t.Fatal(werr)
	}
	_, err = OpenSession(context.Background(), Options{Root: file})
	if !errors.Is(err, ErrNoRepository) || !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("file pin returned %v, want ErrNoRepository with not-a-directory detail", err)
	}
}
