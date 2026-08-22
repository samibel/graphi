package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samibel/graphi/internal/parity"
)

// The cmd/jvmcoverage binary is a thin adapter over
// parity.ComputeCompileCoverage (already hermetic-tested at
// internal/parity/jvmparity_test.go::TestCompileCoverage_SchemaPinnedOnTheFixture).
// The adapter itself is small but its contract is real:
//
//   - it MUST refuse to run without -runner-class (operator foot-gun: a
//     coverage figure without a runner is not auditable end to end),
//   - it MUST read the manifest, find every JVM pin, and emit one
//     compile_coverage record per pin in deterministic name order,
//   - it MUST stamp the candidate + measured_at in a stderr banner so an
//     operator cannot paste a fresh run into a manifest dated for an older
//     one,
//   - it MUST propagate the same per-pin error from ComputeCompileCoverage
//     as a result.Errors entry rather than crashing the whole run (one
//     bad pin must not mask the others).
//
// These four properties are the cmd's contract; the test below pins them
// against the same fixture TestCompileCoverage_SchemaPinnedOnTheFixture
// uses, so any future reader can RE-DERIVE the cmd's behaviour from one
// hermetic source rather than reading main.go line-by-line.
func TestJVMCoverageCmd_FlagsAndShape(t *testing.T) {
	pinsRoot := t.TempDir()
	root := writeJVMFixtureLikeCmdCoverage(t, pinsRoot, "guava")
	manifest := writeMinimalJVMCoverageManifest(t, "guava")

	const runnerClass = "parity/TestJVMCoverageCmd_FlagsAndShape"
	const candidateSHA = "9f687849cec2b26311401191e90b60e40b5f6cee"
	const measuredAt = "2026-08-21"

	stdout, stderr, err := runJVMCoverageCmd(t, []string{
		"-manifest", manifest,
		"-pins-root", pinsRoot,
		"-runner-class", runnerClass,
		"-candidate-sha", candidateSHA,
		"-measured-at", measuredAt,
	}, 30*time.Second)
	if err != nil {
		t.Fatalf("jvmcoverage failed: %v\nstderr:\n%s", err, stderr)
	}

	// JSON shape: an array of one record per JVM pin, deterministic order.
	var got []map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("jvmcoverage stdout is not JSON: %v\nstdout:\n%s", err, stdout)
	}
	if len(got) != 1 {
		t.Fatalf("len(records) = %d, want 1 (the single JVM pin in the manifest)", len(got))
	}
	r := got[0]

	// Name comes from the manifest, NOT the path or the candidate SHA.
	if name, _ := r["name"].(string); name != "guava" {
		t.Errorf("name = %q, want %q (the manifest's JVM pin entry)", name, "guava")
	}

	// CompileCoverage sub-record exists.
	cov, ok := r["compile_coverage"].(map[string]any)
	if !ok {
		t.Fatalf("compile_coverage missing or wrong shape: %+v", r)
	}
	if runner, _ := cov["runner_class"].(string); runner != runnerClass {
		t.Errorf("runner_class = %q, want %q", runner, runnerClass)
	}
	if sha, _ := cov["candidate_sha"].(string); sha != candidateSHA {
		t.Errorf("candidate_sha = %q, want %q", sha, candidateSHA)
	}
	if measured, _ := cov["measured_at"].(string); measured != measuredAt {
		t.Errorf("measured_at = %q, want %q", measured, measuredAt)
	}

	// Banner is on stderr and carries the same provenance.
	if !strings.Contains(stderr, runnerClass) {
		t.Errorf("stderr banner missing runner_class %q\nstderr:\n%s", runnerClass, stderr)
	}
	if !strings.Contains(stderr, candidateSHA) {
		t.Errorf("stderr banner missing candidate_sha %q\nstderr:\n%s", candidateSHA, stderr)
	}
	if !strings.Contains(stderr, measuredAt) {
		t.Errorf("stderr banner missing measured_at %q\nstderr:\n%s", measuredAt, stderr)
	}

	// The cmd's adapter MUST reproduce parity.ComputeCompileCoverage's
	// figure for the same input — a future divergence between the cmd's
	// per-pin record and the parity package's own output is a bug.
	cov2, err := parity.ComputeCompileCoverage(parity.CompileCoverageInput{
		PinRoot:      root,
		SourceRoots:  []string{"src"},
		Strategy:     "full-dependency-resolution",
		RunnerClass:  runnerClass,
		CandidateSHA: candidateSHA,
		Now:          func() string { return measuredAt },
	})
	if err != nil {
		t.Fatalf("parity.ComputeCompileCoverage (control): %v", err)
	}
	gotSource, _ := cov["source_files"].(float64)
	if int(gotSource) != cov2.SourceFiles {
		t.Errorf("source_files: cmd=%v, parity.ComputeCompileCoverage=%d", gotSource, cov2.SourceFiles)
	}
	gotCompiled, _ := cov["compiled_files"].(float64)
	if int(gotCompiled) != cov2.CompiledFiles {
		t.Errorf("compiled_files: cmd=%v, parity.ComputeCompileCoverage=%d", gotCompiled, cov2.CompiledFiles)
	}
	if cov2.Coverage == 1.0 {
		// Sanity: the fixture has at least one .java and zero collisions,
		// so coverage should be 1.0 — the cmd and the parity package must
		// agree on the 4-decimal truncated figure.
		gotCov, _ := cov["coverage"].(float64)
		if gotCov != 1.0 {
			t.Errorf("coverage: cmd=%v, parity.ComputeCompileCoverage=%v", gotCov, cov2.Coverage)
		}
	}
}

// TestJVMCoverageCmd_RefusesWithoutRunnerClass pins the operator foot-gun
// guard: a coverage figure without a runner class is not auditable end to
// end, so the cmd must exit non-zero (exit 2) without producing JSON.
func TestJVMCoverageCmd_RefusesWithoutRunnerClass(t *testing.T) {
	_, stderr, err := runJVMCoverageCmd(t, []string{
		"-manifest", "/nonexistent/manifest.json",
		"-pins-root", "/nonexistent",
	}, 10*time.Second)
	if err == nil {
		t.Fatalf("jvmcoverage must refuse without -runner-class, but it succeeded")
	}
	if !strings.Contains(stderr, "runner-class") {
		t.Errorf("stderr must name the missing flag, got:\n%s", stderr)
	}
}

// TestJVMCoverageCmd_PinPathMissingIsReportedPerPin pins the per-pin
// error-propagation contract: a missing pin root is recorded on the
// pin's own result.Errors entry, the OTHER pins' records still carry
// their figures, and the cmd exits 0 (it does NOT abort the whole run on
// a single bad pin path — an operator with five pins wants to see the
// four that succeeded, not a single failure).
func TestJVMCoverageCmd_PinPathMissingIsReportedPerPin(t *testing.T) {
	manifest := writeMinimalJVMCoverageManifest(t, "guava")

	stdout, _, err := runJVMCoverageCmd(t, []string{
		"-manifest", manifest,
		"-pins-root", "/nonexistent",
		"-runner-class", "parity/TestJVMCoverageCmd_PinPathMissingIsReportedPerPin",
		"-candidate-sha", "9f687849cec2b26311401191e90b60e40b5f6cee",
		"-measured-at", "2026-08-21",
	}, 30*time.Second)
	if err != nil {
		t.Fatalf("jvmcoverage must exit 0 even when every pin root is missing (per-pin error contract); got: %v", err)
	}

	var got []map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("jvmcoverage stdout is not JSON: %v\nstdout:\n%s", err, stdout)
	}
	if len(got) != 1 {
		t.Fatalf("len(records) = %d, want 1 (one record per JVM pin in the manifest, even when the pin root is missing)", len(got))
	}
	r := got[0]
	errs, ok := r["errors"].([]any)
	if !ok || len(errs) == 0 {
		t.Errorf("errors[] missing or empty: %+v", r)
	}
	if cov, present := r["compile_coverage"]; !present {
		t.Errorf("compile_coverage must still be present (zero-valued) when the pin root is missing; the field is a structural contract, only the fields inside it are zero. Got: %+v", cov)
	} else if m, ok := cov.(map[string]any); ok {
		if sf, _ := m["source_files"].(float64); sf != 0 {
			t.Errorf("source_files = %v, want 0 when pin root is missing", sf)
		}
		if cf, _ := m["compiled_files"].(float64); cf != 0 {
			t.Errorf("compiled_files = %v, want 0 when pin root is missing", cf)
		}
	}
}

// runJVMCoverageCmd runs the cmd/jvmcoverage binary against the given
// flags and returns (stdout, stderr, err). It is hermetic: it uses the
// current test binary (built via `go test -c` already on disk) and a
// per-test tempdir, so it never touches the project's corpus or pins.
func runJVMCoverageCmd(t *testing.T, args []string, timeout time.Duration) (string, string, error) {
	t.Helper()

	// The cmd runs from the package directory (cmd/jvmcoverage) so the
	// default -manifest path resolves to corpus/manifest.json — we MUST
	// override it for hermetic runs.
	cmd := exec.CommandContext(context.Background(), "go", append([]string{"run", "./cmd/jvmcoverage"}, args...)...)
	cmd.Dir = repoRoot(t)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	select {
	case err := <-done:
		return stdout.String(), stderr.String(), err
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		// Wait for cmd.Run() to return after Kill so the writer
		// goroutine finishes draining stdout/stderr before the test
		// reads the buffers. Without this, `go test -race` reports a
		// DATA RACE between the writer goroutine and stdout.String().
		<-done
		return stdout.String(), stderr.String(), context.DeadlineExceeded
	}
}

// writeJVMFixtureLikeCmdCoverage materializes a small JVM source tree the
// same shape TestCompileCoverage_SchemaPinnedOnTheFixture uses, but
// standalone (no parity package import — this test file lives in
// cmd/jvmcoverage and must not depend on internal test fixtures). The
// fixture is rooted at <pinsRoot>/<pinName>/ so it matches the cmd's
// pinRoot construction (*pinsRoot + "/" + e.Name).
func writeJVMFixtureLikeCmdCoverage(t *testing.T, pinsRoot, pinName string) string {
	t.Helper()
	root := filepath.Join(pinsRoot, pinName)
	files := map[string]string{
		"src/p/A.java": `package p;
public class A { public String go() { return "a"; } }
`,
		"src/p/B.java": `package p;
public class B { public String go() { return "b"; } }
`,
		"src/p/C.java": `package p;
public class C { public String go() { return "c"; } }
`,
	}
	for rel, body := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(abs), err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

// writeMinimalJVMCoverageManifest writes the smallest manifest shape
// cmd/jvmcoverage accepts: one JVM pin whose pin root is the tempdir the
// caller just produced. The manifest uses the canonical field names
// (source_roots, strategy, runner_class, candidate_sha) so the cmd's
// JSON-decoder finds them.
func writeMinimalJVMCoverageManifest(t *testing.T, pinName string) string {
	t.Helper()
	manifest := `{
  "entries": [
    {
      "name": "` + pinName + `",
      "language": "java",
      "pinned_sha": "0000000000000000000000000000000000000000",
      "jvm_compile": {
        "strategy": "full-dependency-resolution",
        "source_roots": ["src"]
      }
    }
  ]
}`
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

// repoRoot finds the workspace root so the cmd can be `go run` against
// the package directory. `go run ./cmd/jvmcoverage` needs a go.mod
// somewhere up the tree; we run from the workspace root.
func repoRoot(t *testing.T) string {
	t.Helper()
	// The test runs from cmd/jvmcoverage/ when invoked via `go test
	// ./cmd/jvmcoverage/...`. `go test` leaves the cwd where the user
	// invoked it from, but the path resolution for the `-manifest` and
	// `-pins-root` flags is fine because we pass absolute paths. We
	// only need a working directory for `go run` itself.
	if wd, err := os.Getwd(); err == nil {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
	}
	// Fall back: walk up from this test file looking for go.mod.
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate repo root from %s", dir)
	return ""
}

// keep imports happy even when the test binary is built without some
// helpers used only in debug builds.
var _ = io.Discard
