package watch_test

import (
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
)

const watchPkg = "github.com/samibel/graphi/engine/watch"

type pkgInfo struct {
	ImportPath string
	Imports    []string
	Deps       []string
}

func goList(t *testing.T, args ...string) []pkgInfo {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "go", append([]string{"list", "-json"}, args...)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("go list start: %v", err)
	}
	dec := json.NewDecoder(stdout)
	var pkgs []pkgInfo
	for {
		var p pkgInfo
		if err := dec.Decode(&p); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode go list: %v", err)
		}
		pkgs = append(pkgs, p)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("go list: %v", err)
	}
	return pkgs
}

// TestLayer_CoreDoesNotImportWatch is AC-7: no core package may depend on the
// watcher (the layering direction cmd→surfaces→engine→core forbids a core→engine
// edge, and specifically core must gain no edge to engine/watch).
func TestLayer_CoreDoesNotImportWatch(t *testing.T) {
	for _, p := range goList(t, "../../core/...") {
		for _, dep := range p.Deps {
			if dep == watchPkg {
				t.Fatalf("core package %s imports %s (forbidden core→watch edge)", p.ImportPath, watchPkg)
			}
		}
	}
}

// TestZeroEgress_WatcherDepIsLocalOnly is part of AC-6: the watcher dependency
// this story introduces (fsnotify) opens no network socket — its full transitive
// dependency set contains no outbound-network package. (The watch package itself
// transitively reaches "net" ONLY through the pre-existing engine/ingest →
// modernc.org/sqlite meta-DB driver, which is the cache sidecar, not the
// watch+parse path; the authoritative runtime egress proof is cmd/canary.)
func TestZeroEgress_WatcherDepIsLocalOnly(t *testing.T) {
	forbidden := map[string]bool{
		"net":        true,
		"net/http":   true,
		"net/rpc":    true,
		"net/smtp":   true,
		"crypto/tls": true,
	}
	for _, p := range goList(t, "github.com/fsnotify/fsnotify") {
		for _, dep := range p.Deps {
			if forbidden[dep] {
				t.Fatalf("fsnotify transitively imports network package %q (egress risk)", dep)
			}
		}
	}
	// The watch package itself must not DIRECTLY import a network package.
	pkgs := goList(t, watchPkg)
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 package, got %d", len(pkgs))
	}
	for _, imp := range pkgs[0].Imports {
		if forbidden[imp] {
			t.Fatalf("watch package directly imports network package %q", imp)
		}
	}
	found := false
	for _, imp := range pkgs[0].Imports {
		if strings.Contains(imp, "fsnotify") {
			found = true
		}
	}
	if !found {
		t.Fatalf("watch package does not import fsnotify; imports = %v", pkgs[0].Imports)
	}
}
