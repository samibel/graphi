package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/extpack"
)

// Adversarial suite for the `graphi extension` verbs (SW-229).
//
// engine/extpack has its own attack suite covering the model. This one covers
// the SURFACE, because the CLI is where a hostile pack actually arrives: the
// four attacks the story names must be refused at the verb, with an exit code a
// script can act on and with nothing left in the store.
//
// The four: a wrong hash, a schema-version downgrade, an artifact bigger than
// the pack's own declared limit, and a traversal in the artifact path.

// hostilePack writes a manifest with one field replaced and returns its path
// plus its real sha256, so a test can pin correctly and still be refused for the
// right reason.
func hostilePack(t *testing.T, replace map[string]string, artifact string) (path, sha string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rules.yaml"), []byte(artifact), 0o600); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	f := map[string]string{
		"schema_version":   extpack.SchemaVersion,
		"artifact_path":    "rules.yaml",
		"artifact_sha256":  extpack.HashBytes([]byte(artifact)),
		"max_output_bytes": "4096",
	}
	for k, v := range replace {
		f[k] = v
	}
	manifest := fmt.Sprintf(`schema_version: %s
id: hostile.pack
version: 1.0.0
kind: architecture-rules
api:
  min: "1.0"
  max: "1.0"
artifact:
  path: %s
  sha256: %s
capabilities:
  provides:
    - architecture-rule:no-core-to-engine
permissions:
  - graph:read
determinism: deterministic
limits:
  max_output_bytes: %s
`, f["schema_version"], f["artifact_path"], f["artifact_sha256"], f["max_output_bytes"])
	path = filepath.Join(dir, "pack.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path, extpack.HashBytes([]byte(manifest))
}

// assertRefusedAndStoreEmpty runs `extension install` and asserts it exits with
// the actionable code and leaves the store untouched.
func assertRefusedAndStoreEmpty(t *testing.T, name, root, path, sha, wantInErr string) {
	t.Helper()
	code, _, errOut := runExt(t, "install", "--sha256", sha, path, "-root", root)
	if code != extExitProblem {
		t.Errorf("%s: exit = %d, want %d (stderr: %s)", name, code, extExitProblem, errOut)
	}
	if wantInErr != "" && !strings.Contains(errOut, wantInErr) {
		t.Errorf("%s: stderr must explain the refusal with %q: %s", name, wantInErr, errOut)
	}
	if _, err := os.Stat(extpack.LockPath(root)); err == nil {
		t.Errorf("%s: a refused install wrote a lockfile", name)
	}
	if entries, err := os.ReadDir(extpack.Dir(root)); err == nil && len(entries) > 0 {
		t.Errorf("%s: a refused install left files in the pack store", name)
	}
}

func TestAttackCLI_WrongHashIsRefused(t *testing.T) {
	root := t.TempDir()
	path, _ := hostilePack(t, nil, cliArchArtifact)
	assertRefusedAndStoreEmpty(t, "wrong hash", root, path, strings.Repeat("0", 64), "sha256 mismatch")
}

func TestAttackCLI_SchemaVersionDowngradeIsRefused(t *testing.T) {
	root := t.TempDir()
	path, sha := hostilePack(t, map[string]string{"schema_version": "graphi.extension/v1"}, cliArchArtifact)
	assertRefusedAndStoreEmpty(t, "schema downgrade", root, path, sha, extpack.SchemaVersion)
}

func TestAttackCLI_OversizedArtifactIsRefused(t *testing.T) {
	root := t.TempDir()
	padded := cliArchArtifact + "# " + strings.Repeat("p", 8192) + "\n"
	path, sha := hostilePack(t, map[string]string{
		"artifact_sha256":  extpack.HashBytes([]byte(padded)),
		"max_output_bytes": "128",
	}, padded)
	assertRefusedAndStoreEmpty(t, "oversized artifact", root, path, sha, "max_output_bytes")
}

func TestAttackCLI_ArtifactPathTraversalIsRefused(t *testing.T) {
	for _, hostile := range []string{"../rules.yaml", "/etc/passwd", "sub/rules.yaml", "https://example.invalid/rules.yaml"} {
		root := t.TempDir()
		path, sha := hostilePack(t, map[string]string{"artifact_path": hostile}, cliArchArtifact)
		assertRefusedAndStoreEmpty(t, "artifact.path "+hostile, root, path, sha, "artifact.path")
	}
}

// TestAttackCLI_NoVerbTouchesTheNetwork is the zero-egress claim restated where
// this story could plausibly have broken it. Installation is a file copy; there
// is no URL field in the schema, no HTTP client in the verb, and no dialer in
// engine/extpack. The egress canary covers the process; this covers the source.
func TestAttackCLI_NoVerbTouchesTheNetwork(t *testing.T) {
	for _, path := range []string{"extension.go", filepath.Join("..", "..", "engine", "extpack")} {
		var files []string
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if info.IsDir() {
			entries, err := os.ReadDir(path)
			if err != nil {
				t.Fatalf("read dir %s: %v", path, err)
			}
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
					files = append(files, filepath.Join(path, e.Name()))
				}
			}
		} else {
			files = []string{path}
		}
		if len(files) == 0 {
			t.Fatalf("the scan found no files under %s; it is not looking where it thinks it is", path)
		}
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			for _, forbidden := range []string{`"net/http"`, `"net"`, `"os/exec"`, `"plugin"`} {
				if strings.Contains(string(data), forbidden) {
					t.Errorf("%s imports %s: the rule-pack path is offline and executes nothing", f, forbidden)
				}
			}
		}
	}
}
