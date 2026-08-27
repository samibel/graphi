package extpack_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/registry"
	"github.com/samibel/graphi/engine/extpack"
)

// Adversarial suite for the declarative rule packs (SW-229, standards: attack
// fixtures pin the fail-closed claims).
//
// The claim under attack is the one ADR 0013 makes for trust tier A: "the schema
// validator is trusted; the pack is not". Every test here is a hostile pack
// trying to do something the tier says it cannot, and each must FAIL CLOSED —
// refused with an actionable error, nothing written, nothing loaded. A test that
// merely asserted "err != nil" would pass against a validator that rejected
// everything, so each one also proves the honest variant of the same pack is
// accepted.

// attackPack writes a manifest + artifact pair into a fresh directory and
// returns the manifest path. artifact/manifest are written verbatim, so a test
// can ship a deliberately wrong hash or an oversized file.
func attackPack(t *testing.T, manifest, artifactName, artifact string) string {
	t.Helper()
	dir := t.TempDir()
	if artifactName != "" {
		if err := os.WriteFile(filepath.Join(dir, artifactName), []byte(artifact), 0o600); err != nil {
			t.Fatalf("write artifact: %v", err)
		}
	}
	path := filepath.Join(dir, "pack.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

const honestArtifact = `version: "1"
rules:
  - id: no-core-to-engine
    from: core
    to: engine
    description: core must not depend on engine
`

// honestArtifactSHA is the SHA-256 of honestArtifact, computed rather than
// pasted so a change to the fixture cannot silently make a test vacuous.
func honestArtifactSHA() string { return extpack.HashBytes([]byte(honestArtifact)) }

// manifestFor renders a manifest with one field overridden, so each attack
// differs from the honest pack in exactly one place.
func manifestFor(overrides map[string]string) string {
	fields := map[string]string{
		"schema_version":   extpack.SchemaVersion,
		"id":               "attack.pack",
		"version":          "1.0.0",
		"kind":             "architecture-rules",
		"api_min":          "1.0",
		"api_max":          "1.0",
		"artifact_path":    "rules.yaml",
		"artifact_sha256":  honestArtifactSHA(),
		"permissions":      "graph:read",
		"determinism":      "deterministic",
		"max_output_bytes": "4096",
	}
	for k, v := range overrides {
		fields[k] = v
	}
	return fmt.Sprintf(`schema_version: %s
id: %s
version: %s
kind: %s
api:
  min: "%s"
  max: "%s"
artifact:
  path: %s
  sha256: %s
capabilities:
  provides:
    - architecture-rule:no-core-to-engine
permissions:
  - %s
determinism: %s
limits:
  max_output_bytes: %s
`, fields["schema_version"], fields["id"], fields["version"], fields["kind"],
		fields["api_min"], fields["api_max"], fields["artifact_path"], fields["artifact_sha256"],
		fields["permissions"], fields["determinism"], fields["max_output_bytes"])
}

// TestAttack_TheHonestControlPackIsAccepted is the non-vacuity guard for every
// test below: the same generator, unmodified, must produce a pack that installs.
func TestAttack_TheHonestControlPackIsAccepted(t *testing.T) {
	path := attackPack(t, manifestFor(nil), "rules.yaml", honestArtifact)
	root := t.TempDir()
	if _, err := extpack.Install(root, path, sha256File(t, path)); err != nil {
		t.Fatalf("the control pack must install, or every refusal below proves nothing: %v", err)
	}
}

// TestAttack_WrongManifestHashIsRefusedBeforeAnyWrite.
//
// The threat: the file on disk is not the file the user reviewed. Pinning is the
// ONLY supply-chain control tier A has (signing is deferred — ADR 0013 T1), so a
// mismatch must stop the install dead, before the store is touched.
func TestAttack_WrongManifestHashIsRefusedBeforeAnyWrite(t *testing.T) {
	path := attackPack(t, manifestFor(nil), "rules.yaml", honestArtifact)
	root := t.TempDir()
	wrong := strings.Repeat("0", 64)

	_, err := extpack.Install(root, path, wrong)
	if err == nil {
		t.Fatal("a pack whose bytes do not match the pinned hash was installed")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Errorf("error must name the mismatch so the user can tell a typo from a swapped file: %v", err)
	}
	if !strings.Contains(err.Error(), sha256File(t, path)) {
		t.Errorf("error must quote the hash the bytes actually have: %v", err)
	}
	assertStoreUntouched(t, root)
}

// TestAttack_WrongArtifactHashIsRefused: the manifest is authentic, the data
// file it pins is not. Pinning the manifest alone would leave the payload
// unpinned, which is the whole of the pack.
func TestAttack_WrongArtifactHashIsRefused(t *testing.T) {
	path := attackPack(t, manifestFor(map[string]string{
		"artifact_sha256": strings.Repeat("a", 64),
	}), "rules.yaml", honestArtifact)
	root := t.TempDir()

	_, err := extpack.Install(root, path, sha256File(t, path))
	if err == nil {
		t.Fatal("a pack whose artifact does not match its own manifest was installed")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Errorf("unexpected error: %v", err)
	}
	assertStoreUntouched(t, root)
}

// TestAttack_SchemaVersionDowngradeIsRefused.
//
// The threat: a pack declares an older schema version hoping the host will read
// it best-effort under the old, looser rules. graphi's standing rule is that a
// superseded contract spelling FAILS rather than being accepted alongside the
// current one — accepting both is how an old contract stays silently alive.
func TestAttack_SchemaVersionDowngradeIsRefused(t *testing.T) {
	for _, downgrade := range []string{
		"graphi.extension/v1",
		"graphi.extension/v0",
		"graphi.extension/v1alpha0",
		"",
	} {
		path := attackPack(t, manifestFor(map[string]string{"schema_version": downgrade}), "rules.yaml", honestArtifact)
		root := t.TempDir()
		_, err := extpack.Install(root, path, sha256File(t, path))
		if err == nil {
			t.Fatalf("schema_version %q was accepted", downgrade)
		}
		if !strings.Contains(err.Error(), extpack.SchemaVersion) {
			t.Errorf("schema_version %q: the error must name the version this build DOES accept: %v", downgrade, err)
		}
		assertStoreUntouched(t, root)
	}
}

// TestAttack_ArtifactLargerThanItsDeclaredLimitIsRefused.
//
// The threat: a pack declares a small limits.max_output_bytes to look harmless
// and ships a large artifact. The declared limit has to bind its author, or it
// is a comment.
func TestAttack_ArtifactLargerThanItsDeclaredLimitIsRefused(t *testing.T) {
	// A padded artifact: still valid YAML, but far past the declared 128 bytes.
	padded := honestArtifact + "# " + strings.Repeat("p", 4096) + "\n"
	path := attackPack(t, manifestFor(map[string]string{
		"artifact_sha256":  extpack.HashBytes([]byte(padded)),
		"max_output_bytes": "128",
	}), "rules.yaml", padded)
	root := t.TempDir()

	_, err := extpack.Install(root, path, sha256File(t, path))
	if err == nil {
		t.Fatal("an artifact larger than the pack's own declared limit was installed")
	}
	if !strings.Contains(err.Error(), "max_output_bytes") {
		t.Errorf("the error must name the limit the pack broke: %v", err)
	}
	assertStoreUntouched(t, root)

	// And a pack may not simply declare a huge ceiling instead.
	over := extpack.MaxArtifactBytes + 1
	path = attackPack(t, manifestFor(map[string]string{
		"max_output_bytes": fmt.Sprint(over),
	}), "rules.yaml", honestArtifact)
	if _, err := extpack.Install(t.TempDir(), path, sha256File(t, path)); err == nil {
		t.Fatalf("a pack raised its own ceiling to %d bytes", over)
	}
}

// TestAttack_PathTraversalInTheArtifactPathIsRefused.
//
// The threat, and the reason AC-5 exists: `artifact.path` is a string a pack
// author chooses. If graphi resolved it as a path, a pack could name
// ../../../../etc/passwd, an absolute path, or a URL, and graphi would fetch
// whatever that named. The schema makes it a bare file name, and the install
// resolves it through internal/rootfile, which cannot be walked out of.
func TestAttack_PathTraversalInTheArtifactPathIsRefused(t *testing.T) {
	for _, hostile := range []string{
		"../rules.yaml",
		"../../etc/passwd",
		"/etc/passwd",
		"sub/rules.yaml",
		`..\rules.yaml`,
		"file:///etc/passwd",
		"https://example.invalid/rules.yaml",
		"..",
		".",
	} {
		path := attackPack(t, manifestFor(map[string]string{"artifact_path": hostile}), "rules.yaml", honestArtifact)
		root := t.TempDir()
		_, err := extpack.Install(root, path, sha256File(t, path))
		if err == nil {
			t.Fatalf("artifact.path %q was followed", hostile)
		}
		if !strings.Contains(err.Error(), "artifact.path") {
			t.Errorf("artifact.path %q: unhelpful error %v", hostile, err)
		}
		assertStoreUntouched(t, root)
	}
}

// TestAttack_TraversalInThePackIDIsRefused. The id becomes a directory name in
// the store, so it gets the same treatment as the artifact path.
func TestAttack_TraversalInThePackIDIsRefused(t *testing.T) {
	for _, hostile := range []string{
		"../escape",
		"..",
		".",
		"a/b",
		`a\b`,
		"/abs",
		"UPPER",
		"trailing-",
		"-leading",
		"double..dot",
		strings.Repeat("a", 200),
	} {
		path := attackPack(t, manifestFor(map[string]string{"id": hostile}), "rules.yaml", honestArtifact)
		root := t.TempDir()
		if _, err := extpack.Install(root, path, sha256File(t, path)); err == nil {
			t.Fatalf("pack id %q was accepted", hostile)
		}
		assertStoreUntouched(t, root)
	}
}

// TestAttack_ASymlinkedArtifactIsNotFollowed. The schema check is not the only
// defence: even with a legal bare name, the file itself must be a regular file
// inside the manifest's directory.
func TestAttack_ASymlinkedArtifactIsNotFollowed(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.yaml")
	if err := os.WriteFile(outside, []byte(honestArtifact), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "rules.yaml")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	path := filepath.Join(dir, "pack.yaml")
	if err := os.WriteFile(path, []byte(manifestFor(nil)), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	root := t.TempDir()
	if _, err := extpack.Install(root, path, sha256File(t, path)); err == nil {
		t.Fatal("a symlinked artifact was read")
	}
	assertStoreUntouched(t, root)
}

// TestAttack_APackMayNotRequestAPermissionTierACannotHold.
//
// The threat: a pack asks for network or exec access, and something downstream
// has to remember to refuse. Tier A's answer is that the request is not
// expressible — the schema rejects it, so no downstream check can be forgotten.
func TestAttack_APackMayNotRequestAPermissionTierACannotHold(t *testing.T) {
	for _, hostile := range []string{"net:outbound", "fs:write", "exec", "graph:write", "all"} {
		path := attackPack(t, manifestFor(map[string]string{"permissions": hostile}), "rules.yaml", honestArtifact)
		root := t.TempDir()
		_, err := extpack.Install(root, path, sha256File(t, path))
		if err == nil {
			t.Fatalf("permission %q was granted to a declarative pack", hostile)
		}
		if !strings.Contains(err.Error(), string(extpack.PermissionGraphRead)) {
			t.Errorf("permission %q: the error must name the only permission tier A has: %v", hostile, err)
		}
		assertStoreUntouched(t, root)
	}
}

// TestAttack_APackMayNotClaimNonDeterminism.
func TestAttack_APackMayNotClaimNonDeterminism(t *testing.T) {
	path := attackPack(t, manifestFor(map[string]string{"determinism": "best-effort"}), "rules.yaml", honestArtifact)
	if _, err := extpack.Install(t.TempDir(), path, sha256File(t, path)); err == nil {
		t.Fatal("a non-deterministic declarative pack was installed")
	}
}

// TestAttack_APackMayNotOverrideAnotherPacksCapability is the ErrUnsupportedOverride
// producer SW-222 reserved for this story (ADR 0013 threat T5: silent shadowing).
func TestAttack_APackMayNotOverrideAnotherPacksCapability(t *testing.T) {
	root := t.TempDir()
	first := attackPack(t, manifestFor(map[string]string{"id": "first.pack"}), "rules.yaml", honestArtifact)
	if _, err := extpack.Install(root, first, sha256File(t, first)); err != nil {
		t.Fatalf("install first: %v", err)
	}
	second := attackPack(t, manifestFor(map[string]string{"id": "second.pack"}), "rules.yaml", honestArtifact)
	_, err := extpack.Install(root, second, sha256File(t, second))
	if !errors.Is(err, registry.ErrUnsupportedOverride) {
		t.Fatalf("a second pack claiming the same capability key = %v, want registry.ErrUnsupportedOverride", err)
	}
	if !strings.Contains(err.Error(), "first.pack") {
		t.Errorf("the refusal must name the pack that already owns the key: %v", err)
	}
}

// TestAttack_CapabilitiesProvidesMustMatchTheArtifact, in both directions: a
// pack that under-declares smuggles in a rule nobody approved; one that
// over-declares squats on keys it does not implement.
func TestAttack_CapabilitiesProvidesMustMatchTheArtifact(t *testing.T) {
	twoRules := honestArtifact + `  - id: sneaked-in
    from: surfaces
    to: cmd
    description: a rule the manifest never mentioned
`
	path := attackPack(t, manifestFor(map[string]string{
		"artifact_sha256": extpack.HashBytes([]byte(twoRules)),
	}), "rules.yaml", twoRules)
	_, err := extpack.Install(t.TempDir(), path, sha256File(t, path))
	if err == nil {
		t.Fatal("a pack shipping a rule its manifest never declared was installed")
	}
	if !strings.Contains(err.Error(), "sneaked-in") {
		t.Errorf("the error must name the undeclared key: %v", err)
	}

	noRules := `version: "1"
rules:
  - id: something-else
    from: a
    to: b
    description: not the declared rule
`
	path = attackPack(t, manifestFor(map[string]string{
		"artifact_sha256": extpack.HashBytes([]byte(noRules)),
	}), "rules.yaml", noRules)
	if _, err := extpack.Install(t.TempDir(), path, sha256File(t, path)); err == nil {
		t.Fatal("a pack declaring a key its artifact does not define was installed")
	}
}

// TestAttack_UnknownManifestFieldsAreRejected. A field this build does not know
// may be the one carrying the meaning; dropping it silently would let a pack
// mean something the validator never saw.
func TestAttack_UnknownManifestFieldsAreRejected(t *testing.T) {
	hostile := manifestFor(nil) + "artifact_url: https://example.invalid/rules.yaml\n"
	path := attackPack(t, hostile, "rules.yaml", honestArtifact)
	if _, err := extpack.Install(t.TempDir(), path, sha256File(t, path)); err == nil {
		t.Fatal("an unknown manifest field was silently ignored")
	}
}

// TestAttack_APackMayNotShipAUniversalSanitizer: a sanitizer with no
// remove_labels strips EVERY label, which is a one-line way to make a
// repository's taint findings disappear. Suppression is not additive capability.
func TestAttack_APackMayNotShipAUniversalSanitizer(t *testing.T) {
	artifact := `version: "1"
sanitizers:
  - id: silence_everything
    name_patterns:
      - "."
`
	manifest := strings.NewReplacer(
		"kind: architecture-rules", "kind: taint-rules",
		"architecture-rule:no-core-to-engine", "taint-sanitizer:silence_everything",
	).Replace(manifestFor(map[string]string{"artifact_sha256": extpack.HashBytes([]byte(artifact))}))
	path := attackPack(t, manifest, "rules.yaml", artifact)
	_, err := extpack.Install(t.TempDir(), path, sha256File(t, path))
	if err == nil {
		t.Fatal("a pack shipping a universal taint sanitizer was installed")
	}
	if !strings.Contains(err.Error(), "remove_labels") {
		t.Errorf("the error must name the field the pack must fill in: %v", err)
	}
}

// TestAttack_ATamperedInstalledPackFailsClosedOnLoad. Install-time verification
// is not enough: the bytes are re-verified on every load, so editing a pack in
// the store after approval does not take effect either.
func TestAttack_ATamperedInstalledPackFailsClosedOnLoad(t *testing.T) {
	root := t.TempDir()
	installFixture(t, root, layeringPack)
	artifact := filepath.Join(extpack.PackDir(root, "graphi.layering"), "artifact")
	if err := os.WriteFile(artifact, []byte(honestArtifact), 0o600); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	set, err := extpack.Load(root)
	if err == nil {
		t.Fatalf("a tampered pack loaded: %+v", set)
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestAttack_ACorruptLockfileIsAnErrorNotAnEmptySet. Treating a broken lockfile
// as "no packs" would turn a broken install into an invisible behaviour change —
// exactly the false-green the disable-restores-baseline contract exists to make
// impossible to hide.
func TestAttack_ACorruptLockfileIsAnErrorNotAnEmptySet(t *testing.T) {
	for _, corrupt := range []string{
		`{"schema_version":"graphi.extension.lock/v0","packs":[]}`,
		`{"schema_version":"graphi.extension.lock/v1alpha1","packs":[{"id":"a","version":"1","kind":"architecture-rules","manifest_sha256":"zz","artifact_sha256":"zz","enabled":true}]}`,
		`not json at all`,
		`{"schema_version":"graphi.extension.lock/v1alpha1","packs":[],"extra":1}`,
	} {
		root := t.TempDir()
		if err := os.MkdirAll(extpack.Dir(root), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(extpack.LockPath(root), []byte(corrupt), 0o600); err != nil {
			t.Fatalf("write lock: %v", err)
		}
		if _, err := extpack.Load(root); err == nil {
			t.Fatalf("a corrupt lockfile loaded as an empty pack set: %s", corrupt)
		}
	}
}

// TestAttack_DuplicateLockfileEntriesAreErrDuplicate.
func TestAttack_DuplicateLockfileEntriesAreErrDuplicate(t *testing.T) {
	dup := `{"schema_version":"graphi.extension.lock/v1alpha1","packs":[
      {"id":"a.pack","version":"1","kind":"architecture-rules","manifest_sha256":"` + strings.Repeat("0", 64) + `","artifact_sha256":"` + strings.Repeat("0", 64) + `","enabled":true},
      {"id":"a.pack","version":"2","kind":"architecture-rules","manifest_sha256":"` + strings.Repeat("1", 64) + `","artifact_sha256":"` + strings.Repeat("1", 64) + `","enabled":true}]}`
	if _, err := extpack.ParseLock([]byte(dup)); !errors.Is(err, registry.ErrDuplicate) {
		t.Fatalf("duplicate lockfile entries = %v, want registry.ErrDuplicate", err)
	}
}

// assertStoreUntouched proves the "verified BEFORE any write" half of AC-2: a
// refused pack leaves no lockfile and no pack directory behind.
func assertStoreUntouched(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(extpack.LockPath(root)); err == nil {
		t.Error("a refused install wrote a lockfile")
	}
	entries, err := os.ReadDir(extpack.Dir(root))
	if err == nil && len(entries) > 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("a refused install left %v in the pack store", names)
	}
}
