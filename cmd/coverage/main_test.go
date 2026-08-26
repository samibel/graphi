package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// SW-216 AC-5, at the cmd level: readme-claims FAILS CLOSED. An unreadable
// readme.md must exit 2 with a message that says so — never exit 0, never a
// silent skip. The round-1 review proved this path by hand and filed the
// missing test; this pins it, because the wiring between the check and the
// process exit code is exactly what a package-level test cannot see.
//
// It runs against a hermetic root holding only the files -check reads, so the
// exit code is attributable to the missing readme and to nothing else — the
// positive control below runs the same root WITH readme.md and must exit 0.

var checkInputs = []string{
	"docs/coverage-matrix.yaml",
	"docs/coverage-matrix.md",
	"docs/capability-manifest.json",
	"docs/rc/evidence-index.yaml",
}

func buildCoverage(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "coverage")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build cmd/coverage: %v\n%s", err, out)
	}
	return bin
}

// hermeticRoot copies the inputs -check reads into a fresh directory. withReadme
// decides whether readme.md comes along.
func hermeticRoot(t *testing.T, withReadme bool) string {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	dst := t.TempDir()
	files := checkInputs
	if withReadme {
		files = append(append([]string{}, checkInputs...), "readme.md")
	}
	for _, rel := range files {
		b, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		out := filepath.Join(dst, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(out, b, 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return dst
}

func exitCode(t *testing.T, bin, root string) (int, string) {
	t.Helper()
	cmd := exec.Command(bin, "-check", "-root", root)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("run coverage -check: %v\n%s", err, out)
	}
	return ee.ExitCode(), string(out)
}

func TestCheck_UnreadableReadmeExitsTwo(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the coverage binary")
	}
	bin := buildCoverage(t)

	// Positive control: the same hermetic root, readme included, is GREEN.
	if code, out := exitCode(t, bin, hermeticRoot(t, true)); code != 0 {
		t.Fatalf("control run exited %d, want 0:\n%s", code, out)
	}

	code, out := exitCode(t, bin, hermeticRoot(t, false))
	if code != 2 {
		t.Fatalf("missing readme.md exited %d, want 2 (fail closed):\n%s", code, out)
	}
	for _, want := range []string{"read readme.md", "fails closed"} {
		if !strings.Contains(out, want) {
			t.Errorf("exit-2 output does not contain %q:\n%s", want, out)
		}
	}
}
