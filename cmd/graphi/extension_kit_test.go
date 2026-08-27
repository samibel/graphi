package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/extpack"
)

// SW-230 (AX-10) — the developer-kit verbs: `init`, `lint`, `conform`.
//
// The exit-code contract is the one SW-229 established and is asserted the same
// way: 0 ok, 1 an actionable no, 2 you invoked it wrong. `lint` and `conform`
// are the two verbs whose whole job is to say no, so their 1 must not be
// confusable with their 2.

// TestExtensionInitScaffoldsAPackThatValidatesAndConforms is AC-1 end to end,
// through the real CLI: scaffold, lint clean, conform clean, validate clean —
// with nothing edited and no repository bound.
func TestExtensionInitScaffoldsAPackThatValidatesAndConforms(t *testing.T) {
	for _, kind := range extpack.ScaffoldKinds() {
		t.Run(string(kind), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "pack")
			code, out, errOut := runExt(t, "init", "--kind", string(kind), dir)
			if code != extExitOK {
				t.Fatalf("init = %d, want %d (stderr: %s)", code, extExitOK, errOut)
			}
			if !strings.Contains(out, extpack.PackManifestName) {
				t.Errorf("init did not report the manifest it wrote:\n%s", out)
			}

			if code, out, errOut := runExt(t, "lint", dir); code != extExitOK {
				t.Fatalf("lint = %d, want %d\nstdout: %s\nstderr: %s", code, extExitOK, out, errOut)
			}
			code, out, errOut = runExt(t, "conform", dir)
			if code != extExitOK {
				t.Fatalf("conform = %d, want %d\nstdout: %s\nstderr: %s", code, extExitOK, out, errOut)
			}
			for _, want := range []string{"manifest", "artifact-schema", "api-version", "merge-determinism", "provenance"} {
				if !strings.Contains(out, want) {
					t.Errorf("the conform report does not run the %q check:\n%s", want, out)
				}
			}
			if code, _, errOut := runExt(t, "validate", filepath.Join(dir, extpack.PackManifestName)); code != extExitOK {
				t.Fatalf("validate = %d, want %d (stderr: %s)", code, extExitOK, errOut)
			}
		})
	}
}

// TestExtensionInitRefusesToOverwrite: `init` is a scaffold, not a reset.
func TestExtensionInitRefusesToOverwrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pack")
	if code, _, errOut := runExt(t, "init", dir); code != extExitOK {
		t.Fatalf("first init = %d (stderr: %s)", code, errOut)
	}
	code, _, errOut := runExt(t, "init", dir)
	if code != extExitProblem {
		t.Fatalf("second init = %d, want %d — overwriting an author's pack is not an option", code, extExitProblem)
	}
	if !strings.Contains(errOut, "never overwrites") {
		t.Errorf("stderr does not explain the refusal: %s", errOut)
	}
}

// TestExtensionInitUsageErrorsAreExitTwo keeps "you invoked it wrong" separate
// from "the answer is no".
func TestExtensionInitUsageErrorsAreExitTwo(t *testing.T) {
	for _, args := range [][]string{
		{"init"},
		{"init", "a", "b"},
		{"lint"},
		{"conform"},
		{"lint", "a", "b"},
	} {
		if code, _, _ := runExt(t, args...); code != extExitUsageErr {
			t.Errorf("%v = %d, want %d", args, code, extExitUsageErr)
		}
	}
}

// TestExtensionInitRejectsAKindItCannotScaffold: a deferred kind gets the
// backlog answer, not a half-written pack.
func TestExtensionInitRejectsAKindItCannotScaffold(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pack")
	code, _, errOut := runExt(t, "init", "--kind", "query-presets", dir)
	if code != extExitProblem {
		t.Fatalf("init --kind query-presets = %d, want %d (stderr: %s)", code, extExitProblem, errOut)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("a refused init left a directory behind")
	}
}

// TestExtensionLintReportsPositionsAndFailsWithOne is AC-2 through the CLI.
func TestExtensionLintReportsPositionsAndFailsWithOne(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pack")
	if code, _, errOut := runExt(t, "init", dir); code != extExitOK {
		t.Fatalf("init = %d (stderr: %s)", code, errOut)
	}
	manifest := filepath.Join(dir, extpack.PackManifestName)
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(data), "determinism: deterministic", "determinism: whenever", 1)
	if err := os.WriteFile(manifest, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}

	code, out, _ := runExt(t, "lint", dir)
	if code != extExitProblem {
		t.Fatalf("lint of a broken pack = %d, want %d\n%s", code, extExitProblem, out)
	}
	if !strings.Contains(out, extpack.PackManifestName+":") {
		t.Errorf("lint output carries no file:line position:\n%s", out)
	}
	if !strings.Contains(out, "determinism") {
		t.Errorf("lint output does not name the field:\n%s", out)
	}

	// --json is the machine form of the same finding.
	code, out, _ = runExt(t, "lint", "--json", dir)
	if code != extExitProblem {
		t.Fatalf("lint --json = %d, want %d", code, extExitProblem)
	}
	var doc struct {
		Diagnostics []extpack.Diagnostic `json:"diagnostics"`
		Problems    int                  `json:"problems"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("decode lint --json: %v\n%s", err, out)
	}
	if doc.Problems == 0 || len(doc.Diagnostics) == 0 {
		t.Fatalf("lint --json reported no diagnostics: %s", out)
	}
	if doc.Diagnostics[0].Line <= 0 || doc.Diagnostics[0].Field == "" {
		t.Errorf("a JSON diagnostic lost its position or field: %+v", doc.Diagnostics[0])
	}
}

// TestExtensionConformFailsClosedOnABrokenPack is the CLI half of the
// fail-closed proof: the verb whose job is to certify must be able to refuse.
func TestExtensionConformFailsClosedOnABrokenPack(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pack")
	if code, _, errOut := runExt(t, "init", dir); code != extExitOK {
		t.Fatalf("init = %d (stderr: %s)", code, errOut)
	}
	// Edit the artifact and do not re-pin it — the mistake every author makes
	// exactly once.
	artifact := filepath.Join(dir, "rules.yaml")
	data, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, append(data, []byte("# an unpinned edit\n")...), 0o600); err != nil {
		t.Fatal(err)
	}

	code, out, _ := runExt(t, "conform", dir)
	if code != extExitProblem {
		t.Fatalf("conform of an unpinned pack = %d, want %d\n%s", code, extExitProblem, out)
	}
	if !strings.Contains(out, "fail") {
		t.Errorf("the report shows no failing check:\n%s", out)
	}

	code, out, _ = runExt(t, "conform", "--json", dir)
	if code != extExitProblem {
		t.Fatalf("conform --json = %d, want %d", code, extExitProblem)
	}
	var doc struct {
		OK      bool `json:"ok"`
		Results []struct {
			Check  string `json:"check"`
			Status string `json:"status"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("decode conform --json: %v\n%s", err, out)
	}
	if doc.OK {
		t.Errorf("conform --json reports ok for an unpinned pack: %s", out)
	}
	if len(doc.Results) == 0 {
		t.Errorf("conform --json reports no checks: %s", out)
	}
}

// TestExtensionKitVerbsNeedNoRepository: a pack author works on files, not on a
// bound repository, and requiring one would make the kit unusable outside a
// graphi-indexed tree. The cwd handed in here is an empty directory.
func TestExtensionKitVerbsNeedNoRepository(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pack")
	for _, args := range [][]string{
		{"init", dir},
		{"lint", dir},
		{"conform", dir},
		{"validate", filepath.Join(dir, extpack.PackManifestName)},
	} {
		code, _, errOut := runExt(t, args...)
		if code != extExitOK {
			t.Fatalf("%v = %d, want %d (stderr: %s)", args, code, extExitOK, errOut)
		}
	}
}
