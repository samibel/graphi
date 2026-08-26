// Package goldenfile is the AX-00 (SW-220) baseline-freeze helper: one shared,
// stdlib-only way for the golden tests that pin graphi's advertised surface to
// compare produced bytes against a COMMITTED artifact.
//
// Two properties matter, and both are deliberate:
//
//   - Regeneration is NEVER automatic. A golden that rewrites itself on
//     mismatch protects nothing — it records whatever the code just did. These
//     goldens are only rewritten when the maintainer asks for it, explicitly, by
//     exporting GRAPHI_UPDATE_GOLDEN=1 for the run. Without it, a mismatch is a
//     test failure that names the file and the command, and the failure text is
//     the review prompt: "is this diff intended?".
//
//   - Comparison is on raw bytes, not on a decoded structure. The whole point of
//     the AX-00 freeze is that the strangler refactor may not reshape what the
//     surfaces advertise or emit; a structural comparison would silently forgive
//     a key-order or formatting change that a client can observe.
//
// The regeneration command is documented in docs/rc/ax00-baseline.md and echoed
// in every failure message, so it is discoverable from a red CI log alone.
//
// Like internal/layerguard and internal/evidence this is UNRANKED CI/test
// tooling: it sits outside cmd→surfaces→engine→core, imports only the standard
// library, and is referenced exclusively from _test.go files, so it adds nothing
// to the shipped binary's import graph.
package goldenfile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// UpdateEnvVar is the single, explicit opt-in that allows a golden to be
// rewritten. Its absence is what makes these files a gate rather than a log.
const UpdateEnvVar = "GRAPHI_UPDATE_GOLDEN"

// UpdateRequested reports whether the maintainer asked for regeneration.
func UpdateRequested() bool { return os.Getenv(UpdateEnvVar) == "1" }

// Assert compares got against the committed golden artifact at path.
//
// With GRAPHI_UPDATE_GOLDEN=1 it writes got to path (creating parent
// directories) and marks the test as regenerating, so a regeneration run can
// never be mistaken for a passing verification run. Otherwise a missing or
// differing golden fails the test with the exact regeneration command.
func Assert(t testing.TB, path string, got []byte) {
	t.Helper()

	if UpdateRequested() {
		if err := Regenerate(path, got); err != nil {
			t.Fatalf("golden %s: %v", path, err)
		}
		t.Logf("golden %s REGENERATED (%s=1) — review the diff before committing", path, UpdateEnvVar)
		return
	}

	if err := Verify(path, got); err != nil {
		t.Error(err)
	}
}

// Regenerate writes got to path, creating parent directories. It is called ONLY
// under the explicit opt-in; nothing else in this package writes.
func Regenerate(path string, got []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if err := os.WriteFile(path, got, 0o644); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// Verify is the pure comparison half, separated from the testing.TB plumbing so
// the never-automatic rule and the message content can themselves be tested.
// A nil error means got matches the committed artifact exactly.
//
// A MISSING golden is an error, never an implicit "record it now": recording on
// first sight is how a golden ends up pinning a bug.
func Verify(path string, got []byte) error {
	want, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("golden %s is missing (%v).\nIf this artifact is new or an intended change, regenerate it deliberately:\n  %s=1 go test %s\nand review the resulting diff.", path, err, UpdateEnvVar, packageHint(path))
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("golden %s DRIFTED — the frozen baseline no longer matches what the code produces.\nThis is a reviewed change, not a flake: either the change is unintended (fix the code) or it is intended (regenerate with `%s=1 go test %s` and justify the diff in the PR).\n--- want (committed, %d bytes) ---\n%s\n--- got (%d bytes) ---\n%s", path, UpdateEnvVar, packageHint(path), len(want), want, len(got), got)
	}
	return nil
}

// packageHint turns a golden path into the `go test` target that produces it,
// so the regeneration command in a failure message is copy-pasteable straight
// out of a CI log rather than something the reader has to reconstruct.
//
// Golden paths are normally relative to the package directory ("testdata/x.json"),
// which on its own says nothing about WHICH package. The package directory is the
// test's working directory, so the module-relative form of that is the answer;
// an absolute golden path is used directly. If neither resolves, the hint falls
// back to ./... — a correct if slower command, never a wrong one.
func packageHint(path string) string {
	dir := filepath.Dir(path)
	if !filepath.IsAbs(dir) {
		wd, err := os.Getwd()
		if err != nil {
			return "./..."
		}
		dir = filepath.Join(wd, dir)
	}
	for {
		if base := filepath.Base(dir); base == "testdata" {
			dir = filepath.Dir(dir)
			continue
		}
		break
	}

	root := dir
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			return "./..."
		}
		root = parent
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "./..."
	}
	return "./" + filepath.ToSlash(rel)
}
