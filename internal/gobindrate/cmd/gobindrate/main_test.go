package main

// Tests for the gobindrate CLI wrapper. These exercise the entry point the
// SW-187 doc's §2 reproducibility claim is taken from — `go run
// ./internal/gobindrate/cmd/gobindrate <repo-dir>` — and pin the silent-
// corruption failure mode that review r2 named as MAJOR-1: a zero-denominator
// corpus must surface as a non-zero exit code with a stderr message, never
// as a clean exit with a "0 / 0" report.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCmd_ExitsNonZeroOnZeroDenominator pins MAJOR-1 (review r2): a corpus
// that yields zero denominator must NOT exit 0 with a "0/0" report. The CLI
// must fail loudly with exit 3 and a stderr message naming the cause.
func TestCmd_ExitsNonZeroOnZeroDenominator(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// No .go source files -> denominator will be 0.

	bin := buildCLI(t)
	cmd := exec.Command(bin, dir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatalf("CLI exited 0 on a zero-denominator corpus; stdout=%q stderr=%q",
			stdout.String(), stderr.String())
	}
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}
	if ee.ExitCode() == 0 {
		t.Fatalf("ExitError.ExitCode() == 0 (silent success on zero denominator): %v", err)
	}
	if !strings.Contains(stderr.String(), "denominator is zero") {
		t.Fatalf("stderr must name the cause; got %q", stderr.String())
	}
	// stdout must NOT carry the report_sha256=... token on a failed run —
	// a successful-looking SHA line after a failure is the silent-corruption
	// class the review r2 flagged.
	if strings.Contains(stdout.String(), "report_sha256=") {
		t.Fatalf("stdout must not contain report_sha256= on a failed run; got %q",
			stdout.String())
	}
}

// buildCLI compiles this package into a temp binary and returns the path.
// It is shared by every test in this file so each subprocess invocation
// uses a freshly built CLI that reflects the test tree's source.
func buildCLI(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "gobindrate-test")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build: %v", err)
	}
	return bin
}
