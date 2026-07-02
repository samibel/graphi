package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/ingest"
)

func TestBrowserOpenCmd(t *testing.T) {
	cases := []struct {
		goos     string
		wantName string
		wantArgs []string
	}{
		{"darwin", "open", nil},
		{"windows", "cmd", []string{"/c", "start", ""}},
		{"linux", "xdg-open", nil},
		{"freebsd", "xdg-open", nil},
	}
	for _, tc := range cases {
		name, args := browserOpenCmd(tc.goos)
		if name != tc.wantName {
			t.Errorf("%s: name = %q, want %q", tc.goos, name, tc.wantName)
		}
		if !reflect.DeepEqual(args, tc.wantArgs) {
			t.Errorf("%s: args = %v, want %v", tc.goos, args, tc.wantArgs)
		}
	}
}

func TestIsHeadless(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	cases := []struct {
		name string
		goos string
		envs map[string]string
		want bool
	}{
		{"darwin always GUI", "darwin", map[string]string{}, false},
		{"windows always GUI", "windows", map[string]string{}, false},
		{"linux no display", "linux", map[string]string{}, true},
		{"linux x11", "linux", map[string]string{"DISPLAY": ":0"}, false},
		{"linux wayland", "linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, false},
		{"linux ssh overrides display", "linux", map[string]string{"DISPLAY": ":0", "SSH_CONNECTION": "1.2.3.4 5 6.7.8.9 22"}, true},
		{"linux ssh no display", "linux", map[string]string{"SSH_CONNECTION": "x"}, true},
	}
	for _, tc := range cases {
		if got := isHeadless(tc.goos, env(tc.envs)); got != tc.want {
			t.Errorf("%s: isHeadless = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestShouldOpenBrowser_NoBrowserFlag(t *testing.T) {
	t.Setenv("GRAPHI_NO_BROWSER", "")
	if shouldOpenBrowser([]string{"--no-browser"}) {
		t.Fatal("--no-browser must suppress browser launch")
	}
}

func TestSetupZeroConfig_RepoServesLoopbackAndIndexes(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "x.go"), []byte("package x\n\nfunc F() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	srv, ln, url, c, store, cleanup, notRepo, err := setupZeroConfig(repo, nil)
	if err != nil {
		t.Fatalf("setupZeroConfig: %v", err)
	}
	defer cleanup()
	if notRepo {
		t.Fatal("notRepo = true for a .git repo, want false")
	}
	if srv == nil || ln == nil || c == nil || store == nil {
		t.Fatal("setupZeroConfig returned nil component(s)")
	}

	// Loopback-only listener.
	addr := ln.Addr().String()
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatalf("listener addr = %q, want 127.0.0.1: prefix", addr)
	}
	if !strings.HasPrefix(url, "http://127.0.0.1:") || !strings.HasSuffix(url, "/") {
		t.Fatalf("url = %q, want http://127.0.0.1:<port>/ form", url)
	}

	// The auto-managed state DB exists on disk.
	dbPath := filepath.Join(os.Getenv("XDG_STATE_HOME"), "graphi")
	found := false
	_ = filepath.Walk(dbPath, func(p string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() && strings.HasSuffix(p, "db.sqlite") {
			found = true
		}
		return nil
	})
	if !found {
		t.Fatalf("expected a db.sqlite under %q", dbPath)
	}

	// Indexing produced nodes.
	nodes, err := store.Nodes(context.Background(), graphstore.Query{})
	if err != nil {
		t.Fatalf("store.Nodes: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected > 0 nodes after IngestAll")
	}

	// Do NOT call srv.Serve — the test only verifies wiring. Close the listener
	// so cleanup leaves nothing bound.
	_ = ln.Close()
}

// TestSetupZeroConfig_WarmStart is the warm-start end-to-end contract over
// three consecutive starts sharing one per-repo state:
//  1. cold — full index (parse phase, no drift scan);
//  2. unchanged repo — drift scan ONLY, no re-ingest at all;
//  3. one edited file — drift scan plus a delta ingest, and the new symbol is
//     actually queryable afterwards (the warm path must update the graph, not
//     just skip work).
func TestSetupZeroConfig_WarmStart(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "x.go"), []byte("package x\n\nfunc F() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	run := func() (phases map[ingest.Phase]bool, store graphstore.Graphstore, done func()) {
		phases = map[ingest.Phase]bool{}
		srv, ln, _, _, st, cleanup, notRepo, err := setupZeroConfig(repo, func(ev ingest.ProgressEvent) {
			phases[ev.Phase] = true
		})
		if err != nil || notRepo || srv == nil {
			t.Fatalf("setupZeroConfig: err=%v notRepo=%v", err, notRepo)
		}
		return phases, st, func() { _ = ln.Close(); cleanup() }
	}

	// 1. Cold start: a full pass.
	phases, _, done := run()
	if !phases[ingest.PhaseParse] || phases[ingest.PhaseDrift] {
		t.Fatalf("cold start phases = %v, want a parse phase and no drift scan", phases)
	}
	done()

	// 2. Unchanged repo: drift scan only — no parse, no link, no resolve.
	phases, _, done = run()
	if !phases[ingest.PhaseDrift] {
		t.Fatalf("warm start phases = %v, want a drift scan", phases)
	}
	for _, p := range []ingest.Phase{ingest.PhaseParse, ingest.PhaseLink, ingest.PhaseResolve, ingest.PhaseWalk} {
		if phases[p] {
			t.Fatalf("unchanged repo re-ingested (saw phase %q): %v", p, phases)
		}
	}
	done()

	// 3. Edit a file: drift scan + delta ingest, and the new symbol exists.
	if err := os.WriteFile(filepath.Join(repo, "x.go"), []byte("package x\n\nfunc F() {}\n\nfunc Fresh() { F() }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	phases, store, done := run()
	defer done()
	if !phases[ingest.PhaseDrift] || !phases[ingest.PhaseParse] || !phases[ingest.PhaseDone] {
		t.Fatalf("edited repo phases = %v, want drift + parse + done", phases)
	}
	nodes, err := store.Nodes(context.Background(), graphstore.Query{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range nodes {
		if n.QualifiedName() == "x.Fresh" {
			found = true
		}
	}
	if !found {
		t.Fatal("warm delta did not index the newly added symbol x.Fresh")
	}
}

func TestSetupZeroConfig_NotARepo(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	bare := t.TempDir() // no .git/go.work/go.mod marker
	srv, ln, url, c, store, cleanup, notRepo, err := setupZeroConfig(bare, nil)
	defer cleanup()
	if err != nil {
		t.Fatalf("setupZeroConfig (not a repo) returned error: %v", err)
	}
	if !notRepo {
		t.Fatal("notRepo = false for a bare dir, want true")
	}
	if srv != nil || ln != nil || url != "" || c != nil || store != nil {
		t.Fatal("not-a-repo path must return nil components and empty url")
	}
}
