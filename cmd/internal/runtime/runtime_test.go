package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenSession_ClientRootsOverrideProcessCwd(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cwdRepo := repository(t, "cwd", "package cwd\nfunc Wrong() {}\n")
	clientRepo := repository(t, "client", "package fixture\nfunc Hello() string { return \"hello\" }\n")

	rt, err := OpenSession(context.Background(), Options{Cwd: cwdRepo, Roots: []string{clientRepo}})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer rt.Close()
	want, _ := filepath.Abs(clientRepo)
	if rt.Root != want {
		t.Fatalf("bound root = %q, want client root %q", rt.Root, want)
	}
	result, err := rt.Client.Search(context.Background(), "Hello", 10)
	if err != nil {
		t.Fatalf("search bound repository: %v", err)
	}
	if !contains(result, `"qualified_name":"fixture.Hello"`) {
		t.Fatalf("search did not use client repository: %s", result)
	}
}

func TestOpenSession_AuthoritativeEmptyRootsRejectCwd(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cwdRepo := repository(t, "cwd", "package cwd\nfunc Wrong() {}\n")
	_, err := OpenSession(context.Background(), Options{Cwd: cwdRepo, Roots: []string{}})
	if !errors.Is(err, ErrNoRepository) {
		t.Fatalf("OpenSession error = %v, want ErrNoRepository", err)
	}
}

func TestOpenSession_NoRepositoryFailsClosed(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_, err := OpenSession(context.Background(), Options{Cwd: t.TempDir()})
	if !errors.Is(err, ErrNoRepository) {
		t.Fatalf("OpenSession error = %v, want ErrNoRepository", err)
	}
}

func repository(t *testing.T, name, source string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func contains(haystack []byte, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}
