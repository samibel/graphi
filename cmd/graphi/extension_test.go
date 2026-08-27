package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/extpack"
)

// SW-229 (AX-09) — the `graphi extension` CLI verbs.
//
// The exit-code contract is asserted rather than described, because it is the
// only part of this surface a script can see: 0 ok, 1 an actionable no, 2 you
// invoked it wrong. A caller that cannot tell 1 from 2 retries the wrong one.

const cliArchArtifact = `version: "1"
rules:
  - id: no-core-to-engine
    from: core
    to: engine
    description: core packages must not depend on the engine layer
`

// writeCLIPack writes a manifest + artifact pair into a fresh directory and
// returns the manifest path and its sha256.
func writeCLIPack(t *testing.T, id, artifact string, provides ...string) (path, sha string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rules.yaml"), []byte(artifact), 0o600); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	manifest := fmt.Sprintf(`schema_version: %s
id: %s
version: 1.0.0
kind: architecture-rules
api:
  min: "1.0"
  max: "1.0"
artifact:
  path: rules.yaml
  sha256: %s
capabilities:
  provides:
%s
permissions:
  - graph:read
determinism: deterministic
limits:
  max_output_bytes: 4096
`, extpack.SchemaVersion, id, extpack.HashBytes([]byte(artifact)), "    - "+strings.Join(provides, "\n    - "))
	path = filepath.Join(dir, "pack.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path, extpack.HashBytes([]byte(manifest))
}

// runExt runs one `graphi extension` invocation and returns exit code, stdout
// and stderr.
func runExt(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runExtensionAt(t.TempDir(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestExtensionValidatePrintsTheHashToInstallWith(t *testing.T) {
	path, sha := writeCLIPack(t, "graphi.layering", cliArchArtifact, "architecture-rule:no-core-to-engine")

	code, out, errOut := runExt(t, "validate", path)
	if code != extExitOK {
		t.Fatalf("validate = %d, want %d (stderr: %s)", code, extExitOK, errOut)
	}
	for _, want := range []string{"graphi.layering", sha, extpack.SchemaVersion, "architecture-rule:no-core-to-engine"} {
		if !strings.Contains(out, want) {
			t.Errorf("validate output omits %q:\n%s", want, out)
		}
	}
	// The printed install line must be the one that actually works.
	if !strings.Contains(out, "graphi extension install --sha256 "+sha) {
		t.Errorf("validate must print a copy-pasteable install command:\n%s", out)
	}
	// And validate is read-only: it accepts a pinned hash too.
	if code, _, errOut := runExt(t, "validate", path, "--sha256", sha); code != extExitOK {
		t.Errorf("validate with a correct --sha256 = %d (%s)", code, errOut)
	}
}

func TestExtensionInstallListDoctorLifecycle(t *testing.T) {
	root := t.TempDir()
	path, sha := writeCLIPack(t, "graphi.layering", cliArchArtifact, "architecture-rule:no-core-to-engine")

	code, out, errOut := runExt(t, "install", "--sha256", sha, path, "-root", root)
	if code != extExitOK {
		t.Fatalf("install = %d, want 0 (stderr: %s)", code, errOut)
	}
	if !strings.Contains(out, "installed graphi.layering@1.0.0") {
		t.Errorf("install output:\n%s", out)
	}

	code, out, _ = runExt(t, "list", "-root", root)
	if code != extExitOK || !strings.Contains(out, "enabled") || !strings.Contains(out, sha) {
		t.Errorf("list = %d:\n%s", code, out)
	}

	code, out, _ = runExt(t, "list", "--json", "-root", root)
	if code != extExitOK {
		t.Fatalf("list --json = %d:\n%s", code, out)
	}
	var doc struct {
		Packs []struct {
			ID             string `json:"id"`
			ManifestSHA256 string `json:"manifest_sha256"`
			Enabled        bool   `json:"enabled"`
		} `json:"packs"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("list --json is not json: %v\n%s", err, out)
	}
	if len(doc.Packs) != 1 || doc.Packs[0].ID != "graphi.layering" || doc.Packs[0].ManifestSHA256 != sha || !doc.Packs[0].Enabled {
		t.Errorf("list --json = %+v", doc.Packs)
	}

	code, out, _ = runExt(t, "doctor", "-root", root)
	if code != extExitOK || !strings.Contains(out, "none needing attention") {
		t.Errorf("doctor on a healthy store = %d:\n%s", code, out)
	}

	// Disable → doctor still healthy, but the row says disabled.
	if code, _, errOut := runExt(t, "disable", "graphi.layering", "-root", root); code != extExitOK {
		t.Fatalf("disable = %d (%s)", code, errOut)
	}
	code, out, _ = runExt(t, "doctor", "-root", root)
	if code != extExitOK || !strings.Contains(out, "disabled") {
		t.Errorf("doctor after disable = %d:\n%s", code, out)
	}
	if code, _, errOut := runExt(t, "enable", "graphi.layering", "-root", root); code != extExitOK {
		t.Fatalf("enable = %d (%s)", code, errOut)
	}

	// Tamper → doctor reports a hash mismatch and exits 1.
	artifact := filepath.Join(extpack.PackDir(root, "graphi.layering"), "artifact")
	if err := os.WriteFile(artifact, []byte("version: \"1\"\n"), 0o600); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	code, out, _ = runExt(t, "doctor", "-root", root)
	if code != extExitProblem {
		t.Errorf("doctor over a tampered pack = %d, want %d:\n%s", code, extExitProblem, out)
	}
	if !strings.Contains(out, string(extpack.DiagnosisHashMismatch)) {
		t.Errorf("doctor must name the hash mismatch:\n%s", out)
	}
	code, out, _ = runExt(t, "doctor", "--json", "-root", root)
	if code != extExitProblem || !strings.Contains(out, `"problems": 1`) {
		t.Errorf("doctor --json = %d:\n%s", code, out)
	}

	// Remove → back to an empty store.
	if code, _, errOut := runExt(t, "remove", "graphi.layering", "-root", root); code != extExitOK {
		t.Fatalf("remove = %d (%s)", code, errOut)
	}
	code, out, _ = runExt(t, "list", "-root", root)
	if code != extExitOK || !strings.Contains(out, "no rule packs installed") {
		t.Errorf("list after remove = %d:\n%s", code, out)
	}
}

func TestExtensionUsageErrorsExitTwo(t *testing.T) {
	root := t.TempDir()
	path, sha := writeCLIPack(t, "graphi.layering", cliArchArtifact, "architecture-rule:no-core-to-engine")

	cases := []struct {
		name string
		args []string
	}{
		{"no subcommand", nil},
		{"unknown subcommand", []string{"frobnicate", "-root", root}},
		{"install without --sha256", []string{"install", path, "-root", root}},
		{"install with no file", []string{"install", "--sha256", sha, "-root", root}},
		{"validate with no file", []string{"validate"}},
		{"validate with two files", []string{"validate", path, path}},
		{"enable with no id", []string{"enable", "-root", root}},
		{"unknown flag", []string{"list", "--recursive", "-root", root}},
	}
	for _, c := range cases {
		code, _, errOut := runExt(t, c.args...)
		if code != extExitUsageErr {
			t.Errorf("%s: exit = %d, want %d (stderr: %s)", c.name, code, extExitUsageErr, errOut)
		}
	}
	// An empty store is not a usage error and not a problem.
	if code, _, _ := runExt(t, "list", "-root", root); code != extExitOK {
		t.Errorf("list over an empty store = %d, want 0", code)
	}
}

func TestExtensionInstallIsOfflineAndLeavesNothingBehindOnFailure(t *testing.T) {
	root := t.TempDir()
	path, _ := writeCLIPack(t, "graphi.layering", cliArchArtifact, "architecture-rule:no-core-to-engine")

	code, _, errOut := runExt(t, "install", "--sha256", strings.Repeat("0", 64), path, "-root", root)
	if code != extExitProblem {
		t.Fatalf("install with a wrong hash = %d, want %d", code, extExitProblem)
	}
	if !strings.Contains(errOut, "sha256 mismatch") {
		t.Errorf("stderr must explain the refusal: %s", errOut)
	}
	if _, err := os.Stat(extpack.LockPath(root)); err == nil {
		t.Error("a refused install wrote a lockfile")
	}
}
