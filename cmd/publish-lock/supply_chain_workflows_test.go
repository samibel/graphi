package main

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const minimumSecureGoPatch = "1.26.5"

// TestEveryWorkflowActionIsSHAPinned applies the release-DAG's immutable-action
// policy to the complete repository. A workflow with a write-capable token is
// not special-cased: every remote `uses:` reference must end in one exact
// lowercase 40-hex commit SHA. Local `./...` actions remain valid because their
// bytes are already bound to the checked-out commit.
func TestEveryWorkflowActionIsSHAPinned(t *testing.T) {
	entries, err := os.ReadDir(workflowsDir)
	if err != nil {
		t.Fatal(err)
	}
	fullSHA := regexp.MustCompile(`^[0-9a-f]{40}$`)
	usesCount := 0
	for _, entry := range entries {
		if entry.IsDir() || (filepath.Ext(entry.Name()) != ".yml" && filepath.Ext(entry.Name()) != ".yaml") {
			continue
		}
		path := filepath.Join(workflowsDir, entry.Name())
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(f)
		lineNumber := 0
		for scanner.Scan() {
			lineNumber++
			line := strings.TrimSpace(scanner.Text())
			line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
			if !strings.HasPrefix(line, "uses:") {
				continue
			}
			usesCount++
			ref := strings.TrimSpace(strings.TrimPrefix(line, "uses:"))
			if before, _, found := strings.Cut(ref, "#"); found {
				ref = strings.TrimSpace(before)
			}
			ref = strings.Trim(ref, `"'`)
			if strings.HasPrefix(ref, "./") {
				continue
			}
			at := strings.LastIndexByte(ref, '@')
			if at <= 0 || !fullSHA.MatchString(ref[at+1:]) {
				t.Errorf("%s:%d remote action is not pinned to a full commit SHA: %q", path, lineNumber, ref)
			}
		}
		if err := scanner.Err(); err != nil {
			_ = f.Close()
			t.Fatalf("scan %s: %v", path, err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("close %s: %v", path, err)
		}
	}
	if usesCount == 0 {
		t.Fatal("no workflow uses: lines found; repository-wide pin scan is broken")
	}
}

func TestEveryWorkflowSetupGoUsesCentralVersion(t *testing.T) {
	entries, err := os.ReadDir(workflowsDir)
	if err != nil {
		t.Fatal(err)
	}
	setupCount := 0
	for _, entry := range entries {
		if entry.IsDir() || (filepath.Ext(entry.Name()) != ".yml" && filepath.Ext(entry.Name()) != ".yaml") {
			continue
		}
		path := filepath.Join(workflowsDir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(raw), "\n")
		for i, line := range lines {
			if !strings.Contains(line, "uses: actions/setup-go@") {
				continue
			}
			setupCount++
			end := i + 5
			if end > len(lines) {
				end = len(lines)
			}
			stepPrefix := strings.Join(lines[i:end], "\n")
			if !strings.Contains(stepPrefix, "go-version-file: go.mod") {
				t.Errorf("%s:%d setup-go must consume the central CVE-fixed go.mod version", path, i+1)
			}
		}
	}
	if setupCount == 0 {
		t.Fatal("no setup-go steps found; central-version scan is broken")
	}
}

// TestDependencySecurityWorkflowPinsAllThreeGates prevents the repository-wide
// policy from degrading into a cosmetic action pin. PR dependency diffs must
// fail on new high/critical vulnerabilities, Go source reachability uses an
// explicit scanner version, and both npm production lockfiles are audited.
func TestDependencySecurityWorkflowPinsAllThreeGates(t *testing.T) {
	path := filepath.Join(workflowsDir, "dependency-security.yml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	yaml := string(raw)
	for _, required := range []string{
		"permissions:\n  contents: read",
		"if: github.event_name == 'pull_request'",
		"fail-on-severity: high",
		"go-version-file: go.mod",
		"go run golang.org/x/vuln/cmd/govulncheck@v1.6.0",
		"*/node_modules/*",
		"path: [web, extensions/vscode]",
		"actions/setup-node@49933ea5288caeca8642d1e84afbd3f7d6820020 # v4",
		"npm ci --omit=dev --ignore-scripts",
		"npm audit --omit=dev --audit-level=high",
	} {
		if !strings.Contains(yaml, required) {
			t.Errorf("dependency-security workflow missing %q", required)
		}
	}
	dependencyJobStart := strings.Index(yaml, "  dependency-review:")
	govulncheckJobStart := strings.Index(yaml, "\n  govulncheck:")
	if dependencyJobStart < 0 || govulncheckJobStart <= dependencyJobStart {
		t.Fatal("dependency-security workflow has no isolated dependency-review job before govulncheck")
	}
	dependencyJob := yaml[dependencyJobStart:govulncheckJobStart]
	checkout := strings.Index(dependencyJob, "actions/checkout@34e114876b0b11c390a56381ad16ebd13914f8d5 # v4")
	review := strings.Index(dependencyJob, "actions/dependency-review-action@2031cfc080254a8a887f58cffee85186f0e49e48 # v4.9.0")
	if checkout < 0 || review < 0 || checkout >= review {
		t.Fatal("dependency-review job must check out the PR before its SHA-pinned dependency review action")
	}
	if regexp.MustCompile(`(?m)^\s*(?:go run|go install)\s+golang\.org/x/vuln/cmd/govulncheck@latest(?:\s|$)`).MatchString(yaml) {
		t.Fatal("govulncheck executable must never float on @latest")
	}
	if strings.Contains(yaml, "golang/govulncheck-action@") {
		t.Fatal("govulncheck-action currently installs the scanner at @latest; use the explicitly versioned module invocation")
	}
}

// TestGoDirectiveStaysAboveKnownCVEs locks the minimum patched Go toolchain
// used by every setup-go step through go-version-file. Go 1.26.5 fixes the
// standard-library vulnerabilities GO-2026-5856, GO-2026-5039, GO-2026-5037,
// and GO-2026-4970; the last one is directly relevant to os.Root confinement.
func TestGoDirectiveStaysAboveKnownCVEs(t *testing.T) {
	for _, path := range []string{"../../go.mod", "../../go.work"} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			assertGoDirectiveAtLeast(t, path, minimumSecureGoPatch)
		})
	}
}

func TestShippedActionUsesSecureGoPatch(t *testing.T) {
	raw, err := os.ReadFile("../../extensions/github-action/action.yml")
	if err != nil {
		t.Fatal(err)
	}
	action := string(raw)
	if !strings.Contains(action, `go-version: "`+minimumSecureGoPatch+`"`) {
		t.Fatalf("shipped GitHub Action must pin Go %s exactly", minimumSecureGoPatch)
	}
	if regexp.MustCompile(`(?m)^\s*go-version:\s*["']?1\.26["']?\s*$`).MatchString(action) {
		t.Fatal("shipped GitHub Action must not use a patch-floating Go 1.26 selector")
	}
}

func assertGoDirectiveAtLeast(t *testing.T, path, minimum string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^go\s+(\d+)\.(\d+)(?:\.(\d+))?\s*$`).FindStringSubmatch(string(raw))
	if match == nil {
		t.Fatal("go.mod has no parseable go directive")
	}
	want := strings.Split(minimum, ".")
	for i := 1; i <= 3; i++ {
		gotPart := 0
		if match[i] != "" {
			gotPart, err = strconv.Atoi(match[i])
			if err != nil {
				t.Fatalf("parse go directive component %q: %v", match[i], err)
			}
		}
		wantPart, err := strconv.Atoi(want[i-1])
		if err != nil {
			t.Fatalf("invalid minimum Go version %q: %v", minimum, err)
		}
		if gotPart > wantPart {
			return
		}
		if gotPart < wantPart {
			t.Fatalf("%s go directive %s is below the CVE-fixed floor %s", path, strings.Join(match[1:], "."), minimum)
		}
	}
}
