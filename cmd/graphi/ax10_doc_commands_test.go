package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SW-230 (AX-10) — the worked-example doc is checked for drift the cheap way:
// its commands RUN.
//
// docs/extension-developer-kit.md is the page an extension author follows first.
// A quick-start whose commands stopped working would be worse than no page,
// because it would cost the reader the time it takes to discover that. So the
// blocks that promise to work are marked ```console-verified and this test
// executes every `$ graphi extension …` line in them through the real verb
// dispatcher, in order, against a temporary directory.
//
// It is deliberately NOT a full doc linter. Prose drift is a review problem;
// what a test can hold is the executable claim, and that is what this holds.

const docPath = "../../docs/extension-developer-kit.md"

// docPackPlaceholder is the path the doc uses. The test rewrites it to a temp
// directory so the commands run somewhere disposable while the page stays
// readable.
const docPackPlaceholder = "./my-pack"

func TestAX10_WorkedExampleDocCommandsRun(t *testing.T) {
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	blocks := verifiedBlocks(string(data))
	if len(blocks) == 0 {
		t.Fatalf("%s has no ```console-verified block; this test is not checking anything", docPath)
	}

	commands := 0
	for i, block := range blocks {
		dir := filepath.Join(t.TempDir(), "my-pack")
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "$ graphi extension ") {
				continue
			}
			args := strings.Fields(strings.TrimPrefix(line, "$ graphi extension "))
			for j, a := range args {
				if strings.HasPrefix(a, docPackPlaceholder) {
					args[j] = filepath.Join(dir, strings.TrimPrefix(a, docPackPlaceholder))
				}
			}
			code, out, errOut := runExt(t, args...)
			if code != extExitOK {
				t.Fatalf("%s block %d: `%s` exited %d\nstdout: %s\nstderr: %s",
					docPath, i+1, line, code, out, errOut)
			}
			commands++
		}
	}
	if commands == 0 {
		t.Fatalf("%s: the verified blocks contain no `$ graphi extension` command", docPath)
	}
}

// verifiedBlocks returns the contents of every ```console-verified fence.
func verifiedBlocks(page string) []string {
	const open = "```console-verified"
	var out []string
	lines := strings.Split(page, "\n")
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != open {
			continue
		}
		var body []string
		for i++; i < len(lines) && strings.TrimSpace(lines[i]) != "```"; i++ {
			body = append(body, lines[i])
		}
		out = append(out, strings.Join(body, "\n"))
	}
	return out
}

// TestAX10_WorkedExampleDocNamesTheArtefactsItClaims keeps the page's structural
// claims — the operation it uses and the files that prove it — from pointing at
// things that no longer exist. A renamed test file is the commonest way a worked
// example quietly stops being worked.
func TestAX10_WorkedExampleDocNamesTheArtefactsItClaims(t *testing.T) {
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	page := string(data)
	for _, cited := range []string{
		"surfaces/ax10_worked_example_test.go",
		"engine/extpack/conformance/conformance_selftest_test.go",
		"adr/0013-extension-trust-tiers.md",
	} {
		if !strings.Contains(page, cited) {
			t.Errorf("%s no longer cites %q", docPath, cited)
			continue
		}
		path := filepath.Join("..", "..", strings.TrimPrefix(cited, "adr/"))
		if strings.HasPrefix(cited, "adr/") {
			path = filepath.Join("..", "..", "docs", cited)
		}
		if _, statErr := os.Stat(path); statErr != nil {
			t.Errorf("%s cites %q, which is not at %s: %v", docPath, cited, path, statErr)
		}
	}
}
