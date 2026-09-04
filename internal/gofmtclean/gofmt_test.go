// Package gofmtclean holds one repo-wide hygiene test: `gofmt -l .` over the
// whole module must exit 0, print nothing on stderr and list nothing on stdout.
//
// SW-273. The CI gofmt step in .github/workflows/lint.yml was, for two months,
//
//	drift=$(gofmt -l . | grep -v node_modules || true)
//
// which measures something other than what it claims: $(...) captures stdout
// only while gofmt reports parse errors on stderr, the pipe replaces gofmt's
// exit status with grep's, and `|| true` discards whatever is left. Two .go
// files with no package clause sat under engine/embed/testdata from 37de918
// (2026-09-02) with that step green on every PR, while `gofmt -l .` exited 2 on
// a clean checkout. The step is fixed alongside this test; this test is the
// half that bites under `go test ./...` locally and inside testgate, so the
// next unparseable .go file fails on the developer's machine, not only in a
// workflow nobody reads the transcript of.
//
// The shape is engine/exthost/isolation_test.go's: a fact about the whole
// module, asserted mechanically from inside the suite, with no product import.
// It lives in its own package so that it has no other reason to change.
package gofmtclean_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSW273_GofmtExitsZeroAndListsNothing asserts, separately, all three
// things the CI step used to conflate: gofmt's exit status, its stderr and its
// stdout. Each is reported on its own so a failure says WHICH one it was.
func TestSW273_GofmtExitsZeroAndListsNothing(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go toolchain unavailable: %v", err)
	}
	root := moduleRoot(t, goTool)
	gofmt := gofmtBinary(t, goTool)

	// Exactly the CI invocation, from the module root, with the two streams
	// kept apart and the exit status read from the process, not from a pipe.
	cmd := exec.Command(gofmt, "-l", ".")
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	if runErr != nil {
		t.Errorf("gofmt -l . (in %s) exited non-zero: %v\n"+
			"gofmt exits 2 when a .go file cannot be parsed. Every *.go under the module — "+
			"testdata included — must be a parseable Go file; a fixture that is a bare function "+
			"body belongs under a non-.go extension (see engine/embed/testdata/cobra/*.txt).\n"+
			"stderr:\n%s", root, runErr, stderr.String())
	}
	if s := strings.TrimSpace(stderr.String()); s != "" {
		t.Errorf("gofmt -l . wrote to stderr; parse errors go there, never to stdout, and the "+
			"old CI step could not see them:\n%s", s)
	}
	if drift := driftList(stdout.String()); len(drift) > 0 {
		t.Errorf("gofmt -l . lists %d unformatted file(s) (run gofmt -w):\n  %s",
			len(drift), strings.Join(drift, "\n  "))
	}
}

// driftList is the stdout of `gofmt -l .` as a list of paths, with the same
// node_modules exclusion the workflow step applies (web/node_modules is a local
// artifact of `npm install`, not part of the checkout CI formats).
func driftList(stdout string) []string {
	var out []string
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "node_modules") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// moduleRoot is the directory holding go.mod, from the toolchain rather than
// from a walk: a test's working directory is its own package directory, and
// `go env GOMOD` answers the same in a plain checkout and in a linked worktree.
func moduleRoot(t *testing.T, goTool string) string {
	t.Helper()
	out, err := exec.Command(goTool, "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		t.Fatalf("go env GOMOD = %q; this test must run inside the module", gomod)
	}
	return filepath.Dir(gomod)
}

// gofmtBinary is the gofmt that belongs to the toolchain running the test —
// GOROOT/bin/gofmt — falling back to PATH. CI's setup-go puts the same one on
// PATH; locally a GOTOOLCHAIN-selected Go keeps its gofmt under its own GOROOT,
// which PATH may not contain.
func gofmtBinary(t *testing.T, goTool string) string {
	t.Helper()
	out, err := exec.Command(goTool, "env", "GOROOT").Output()
	if err == nil {
		candidate := filepath.Join(strings.TrimSpace(string(out)), "bin", "gofmt")
		if st, statErr := os.Stat(candidate); statErr == nil && !st.IsDir() {
			return candidate
		}
	}
	gofmt, err := exec.LookPath("gofmt")
	if err != nil {
		t.Skipf("gofmt unavailable (not under GOROOT/bin and not on PATH): %v", err)
	}
	return gofmt
}
