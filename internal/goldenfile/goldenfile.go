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
	"os"
	"path/filepath"
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
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("golden %s: mkdir: %v", path, err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("golden %s: write: %v", path, err)
		}
		t.Logf("golden %s REGENERATED (%s=1) — review the diff before committing", path, UpdateEnvVar)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s is missing (%v).\nIf this artifact is new or an intended change, regenerate it deliberately:\n  %s=1 go test %s\nand review the resulting diff.", path, err, UpdateEnvVar, packageHint(path))
	}
	if !bytes.Equal(got, want) {
		t.Errorf("golden %s DRIFTED — the frozen baseline no longer matches what the code produces.\nThis is a reviewed change, not a flake: either the change is unintended (fix the code) or it is intended (regenerate with `%s=1 go test %s` and justify the diff in the PR).\n--- want (committed, %d bytes) ---\n%s\n--- got (%d bytes) ---\n%s", path, UpdateEnvVar, packageHint(path), len(want), want, len(got), got)
	}
}

// packageHint turns a golden path into a plausible `go test` target so the
// failure message is copy-pasteable from a CI log.
func packageHint(path string) string {
	dir := filepath.Dir(path)
	for dir != "." && dir != string(filepath.Separator) {
		if filepath.Base(dir) == "testdata" {
			dir = filepath.Dir(dir)
			continue
		}
		break
	}
	if dir == "" || dir == "." {
		return "./..."
	}
	return "./" + filepath.ToSlash(dir)
}
