package jvmcorpus_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The two workflows that install a JVM toolchain. Named explicitly rather than
// discovered, so deleting one is a test failure instead of a silently smaller
// scan.
var toolchainWorkflows = []string{
	"../../.github/workflows/jvm-groundtruth.yml",
	"../../.github/workflows/jvm-corpus.yml",
}

// TestWorkflows_KotlincIsPinned is AC-1 made enforceable. The repository
// shipped `releases/latest/download/kotlin-compiler.zip`, which means the same
// commit can compile differently on two days; every figure produced by such a
// run is unreproducible, and unreproducible evidence is not evidence.
//
// It asserts three things, because pinning the URL alone is not pinning: an
// exact version in the URL, a sha256 checked BEFORE the archive is unpacked,
// and no moving reference anywhere in the file.
func TestWorkflows_KotlincIsPinned(t *testing.T) {
	for _, path := range toolchainWorkflows {
		yaml := read(t, path)
		if !strings.Contains(yaml, "kotlin-compiler") {
			continue // this workflow does not install kotlinc
		}
		// Scanned over EXECUTABLE lines only. The rule is about what the
		// workflow does, not about what its prose says — and these files
		// deliberately explain the `releases/latest` mistake they replaced, so a
		// whole-file grep would fail on the documentation of its own fix.
		if line, found := firstExecutableMatch(yaml, `releases/latest`); found {
			t.Errorf("%s installs kotlinc from a MOVING reference: %q; "+
				"the same commit would compile differently on two days (AC-1)",
				filepath.Base(path), line)
		}
		if !regexp.MustCompile(`KOTLIN_VERSION:\s*"\d+\.\d+\.\d+"`).MatchString(yaml) {
			t.Errorf("%s must pin KOTLIN_VERSION to an exact x.y.z (AC-1)", filepath.Base(path))
		}
		if !regexp.MustCompile(`KOTLIN_SHA256:\s*"[0-9a-f]{64}"`).MatchString(yaml) {
			t.Errorf("%s must pin the kotlinc archive by sha256 — a version alone does not "+
				"stop the artifact changing under it (AC-1)", filepath.Base(path))
		}
		if !strings.Contains(yaml, "sha256sum -c -") {
			t.Errorf("%s must VERIFY the kotlinc digest before unpacking; a recorded digest "+
				"that is never checked pins nothing (AC-1)", filepath.Base(path))
		}
	}
}

// TestWorkflows_JavaIsPinnedAndRecorded is AC-2. `java-version: "21"` requests a
// major line, so the exact build is only knowable from the run — which is why
// the workflow must also PRINT it. A version that is requested but never
// recorded cannot be cited beside a figure.
func TestWorkflows_JavaIsPinnedAndRecorded(t *testing.T) {
	for _, path := range toolchainWorkflows {
		yaml := read(t, path)
		if !strings.Contains(yaml, "setup-java") {
			continue
		}
		if !regexp.MustCompile(`java-version:\s*"\d+"`).MatchString(yaml) {
			t.Errorf("%s must pin java-version (AC-2)", filepath.Base(path))
		}
		if !strings.Contains(yaml, "javac -version") {
			t.Errorf("%s must RECORD the javac version it actually installed, beside the "+
				"evidence it produces (AC-2)", filepath.Base(path))
		}
	}
}

// TestJVMCorpusWorkflow_IsDispatchOnly is AC-8's posture and the ticket's
// "hermetic tests and real-corpus evidence are different runs" rule: the run
// that clones must never be reachable from a pull request.
func TestJVMCorpusWorkflow_IsDispatchOnly(t *testing.T) {
	yaml := read(t, "../../.github/workflows/jvm-corpus.yml")
	for _, forbidden := range []string{"pull_request:", "push:"} {
		if regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(forbidden)).MatchString(yaml) {
			t.Errorf("jvm-corpus.yml must not trigger on %s — it clones three repositories "+
				"and fetches a classpath; a hermetic gate that clones is not hermetic", forbidden)
		}
	}
	if !regexp.MustCompile(`(?m)^\s*workflow_dispatch\s*:`).MatchString(yaml) {
		t.Error("jvm-corpus.yml must be dispatchable")
	}
}

// TestWorkflows_ActionsStaySHAPinned keeps the repository-wide supply-chain rule
// holding for the workflow this story adds, at the point it is added.
func TestWorkflows_ActionsStaySHAPinned(t *testing.T) {
	yaml := read(t, "../../.github/workflows/jvm-corpus.yml")
	uses := regexp.MustCompile(`uses:\s*(\S+)`).FindAllStringSubmatch(yaml, -1)
	if len(uses) == 0 {
		t.Fatal("no `uses:` found — this scan is broken")
	}
	for _, m := range uses {
		ref := m[1]
		if !regexp.MustCompile(`@[0-9a-f]{40}$`).MatchString(ref) {
			t.Errorf("action %q is not pinned to a full commit SHA", ref)
		}
	}
}

// firstExecutableMatch finds pattern on a line that is not a YAML comment,
// returning the line for the failure message. Comment lines are skipped so a
// rule can be documented in the very file it governs.
func firstExecutableMatch(yaml, pattern string) (string, bool) {
	re := regexp.MustCompile(pattern)
	for _, line := range strings.Split(yaml, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if re.MatchString(line) {
			return strings.TrimSpace(line), true
		}
	}
	return "", false
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
