package goldenfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The rule under test is the one that makes these artifacts a gate rather than a
// log: nothing is ever recorded implicitly. A golden that writes itself on a
// mismatch pins whatever the code just did — including a bug.

func TestVerify_MatchingBytesPass(t *testing.T) {
	path := filepath.Join(t.TempDir(), "golden.json")
	if err := os.WriteFile(path, []byte("{\"a\":1}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := Verify(path, []byte("{\"a\":1}\n")); err != nil {
		t.Errorf("identical bytes reported drift: %v", err)
	}
}

func TestVerify_MissingGoldenIsAnErrorAndIsNotRecorded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "golden.json")

	err := Verify(path, []byte("new content"))
	if err == nil {
		t.Fatal("a missing golden must be an error — recording on first sight is how a golden ends up pinning a bug")
	}
	if !strings.Contains(err.Error(), UpdateEnvVar) {
		t.Errorf("the failure must name the explicit regeneration opt-in; got: %v", err)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("Verify created the golden file — verification must never write")
	}
}

func TestVerify_DriftIsAnErrorAndLeavesTheArtifactUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "golden.json")
	const committed = "frozen bytes\n"
	if err := os.WriteFile(path, []byte(committed), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	err := Verify(path, []byte("changed bytes\n"))
	if err == nil {
		t.Fatal("drift was not reported")
	}
	message := err.Error()
	for _, want := range []string{"DRIFTED", "not a flake", UpdateEnvVar, "frozen bytes", "changed bytes"} {
		if !strings.Contains(message, want) {
			t.Errorf("drift message is missing %q — a reviewer must see what moved and how to regenerate:\n%s", want, message)
		}
	}

	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read back: %v", readErr)
	}
	if string(raw) != committed {
		t.Errorf("the committed artifact was modified by a verification run: %q", raw)
	}
}

func TestRegenerate_WritesAndCreatesParents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "golden.json")
	if err := Regenerate(path, []byte("recorded\n")); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(raw) != "recorded\n" {
		t.Errorf("regenerated content = %q, want %q", raw, "recorded\n")
	}
	if err := Verify(path, []byte("recorded\n")); err != nil {
		t.Errorf("a freshly regenerated golden does not verify: %v", err)
	}
}

func TestUpdateRequested_OnlyTheExactOptIn(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"1", true},
		{"", false},
		{"0", false},
		{"true", false}, // deliberately NOT accepted: one spelling, no near-misses
		{"yes", false},
		{"TRUE", false},
	} {
		t.Setenv(UpdateEnvVar, tc.value)
		if got := UpdateRequested(); got != tc.want {
			t.Errorf("%s=%q: UpdateRequested() = %v, want %v", UpdateEnvVar, tc.value, got, tc.want)
		}
	}
}

// TestAssert_RegeneratesOnlyUnderTheOptIn exercises the testing.TB path in the
// one direction that can be observed without a fake TB: with the opt-in set, a
// missing golden is written rather than failed.
func TestAssert_RegeneratesOnlyUnderTheOptIn(t *testing.T) {
	t.Setenv(UpdateEnvVar, "1")
	path := filepath.Join(t.TempDir(), "golden.json")
	Assert(t, path, []byte("written by the opt-in\n"))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the opt-in did not write the golden: %v", err)
	}
	if string(raw) != "written by the opt-in\n" {
		t.Errorf("content = %q", raw)
	}
}

// TestPackageHint_NamesTheTestsOwnPackage pins that the regeneration command in
// a failure message is copy-pasteable. The test's working directory IS the
// package directory, so the hint must resolve to this package.
func TestPackageHint_NamesTheTestsOwnPackage(t *testing.T) {
	if got, want := packageHint(filepath.Join("testdata", "x.json")), "./internal/goldenfile"; got != want {
		t.Errorf("packageHint = %q, want %q", got, want)
	}
	// A path outside any module must degrade to a correct-if-broad command
	// rather than to a wrong one.
	if got := packageHint(filepath.Join(t.TempDir(), "x.json")); got != "./..." {
		t.Errorf("packageHint for a path outside the module = %q, want %q", got, "./...")
	}
}
