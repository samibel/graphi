package taint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/registry"
	"github.com/samibel/graphi/engine/extpack"
)

// SW-229 AC-6: end-to-end proof that a declarative rule pack changes what an
// EXISTING Labs analyzer finds — and AC-3's other half, that disabling it
// restores the previous behaviour byte for byte.

// writeTaintPack writes a taint-rules pack into its own directory, computing
// both hashes from the bytes it just wrote, and returns the manifest path.
func writeTaintPack(t *testing.T, id, artifact string, provides ...string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "defs.yaml"), []byte(artifact), 0o600); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	manifest := fmt.Sprintf(`schema_version: %s
id: %s
version: 1.0.0
kind: taint-rules
api:
  min: "1.0"
  max: "1.0"
artifact:
  path: defs.yaml
  sha256: %s
capabilities:
  provides:
%s
permissions:
  - graph:read
determinism: deterministic
limits:
  max_output_bytes: 8192
`, extpack.SchemaVersion, id, extpack.HashBytes([]byte(artifact)), "    - "+strings.Join(provides, "\n    - "))
	path := filepath.Join(dir, "pack.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

func installTaintPack(t *testing.T, root, path string) extpack.LockEntry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	entry, err := extpack.Install(root, path, extpack.HashBytes(data))
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	return entry
}

// acmeArtifact declares a source and a sink graphi's built-in config knows
// nothing about, so a finding between them can only come from the pack.
const acmeArtifact = `version: "1"
sources:
  - id: acme_ingest_header
    label: user_input
    name_patterns:
      - acme.Ingest.Header
sinks:
  - id: acme_shell
    category: command_injection
    name_patterns:
      - acme.Shell.Run
`

// canonical renders a config or a result as stable JSON so "unchanged" is a byte
// comparison rather than a field-by-field opinion.
func canonical(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}

func TestAC6_ATaintPackChangesWhatTheAnalyzerFinds(t *testing.T) {
	root := t.TempDir()

	// A flow the BUILT-IN config cannot see: neither endpoint is a built-in
	// source or sink.
	src := mustNode(t, "call", "acme.Ingest.Header", "handler.go", 10, 1)
	mid := mustNode(t, "variable", "raw", "handler.go", 11, 1)
	sink := mustNode(t, "call", "acme.Shell.Run", "handler.go", 12, 1)
	reader := &testReader{
		nodes: []model.Node{src, mid, sink},
		edges: []model.Edge{mustEdge(t, src.ID(), mid.ID(), "defines"), mustEdge(t, mid.ID(), sink.ID(), "references")},
	}

	baselineCfg, err := LoadConfig(root)
	if err != nil {
		t.Fatalf("baseline LoadConfig: %v", err)
	}
	baselineResult, err := New(baselineCfg, DefaultCaps(), nil).Run(context.Background(), reader)
	if err != nil {
		t.Fatalf("baseline run: %v", err)
	}
	if len(baselineResult.Findings) != 0 {
		t.Fatalf("the baseline must find nothing here, or the pack proves nothing: %+v", baselineResult.Findings)
	}
	baselineBytes := canonical(t, baselineResult)
	baselineConfigBytes := canonical(t, baselineCfg)

	// The pre-pack config must be the built-in default, untouched.
	if baselineConfigBytes != canonical(t, DefaultConfig()) {
		t.Fatalf("a repository with no packs must load DefaultConfig unchanged:\n got=%s\nwant=%s", baselineConfigBytes, canonical(t, DefaultConfig()))
	}
	if fp := ConfigFingerprint(root); fp != "" {
		t.Fatalf("ConfigFingerprint with no config and no packs = %q, want \"\" — the pre-pack stamp", fp)
	}

	// Install the pack.
	entry := installTaintPack(t, root, writeTaintPack(t, "acme.taint",
		acmeArtifact, "taint-sink:acme_shell", "taint-source:acme_ingest_header"))

	packedCfg, err := LoadConfig(root)
	if err != nil {
		t.Fatalf("packed LoadConfig: %v", err)
	}
	if len(packedCfg.Packs) != 1 || packedCfg.Packs[0].ID != entry.ID {
		t.Fatalf("config provenance = %+v, want one ref for %q", packedCfg.Packs, entry.ID)
	}
	if packedCfg.ContentHash == "" {
		t.Error("a pack-influenced config must be stamped with its own content hash")
	}
	if fp := ConfigFingerprint(root); fp == "" {
		t.Error("installing a pack must move the warm-start fingerprint, or stale findings survive it")
	}

	packedResult, err := New(packedCfg, DefaultCaps(), nil).Run(context.Background(), reader)
	if err != nil {
		t.Fatalf("packed run: %v", err)
	}
	if len(packedResult.Findings) == 0 {
		t.Fatal("the pack changed nothing: no finding for the flow it declared")
	}
	f := packedResult.Findings[0]
	if f.SourceDefID != "acme_ingest_header" || f.SinkDefID != "acme_shell" {
		t.Errorf("finding = %+v, want the pack's own source and sink", f)
	}
	if f.SinkCategory != "command_injection" {
		t.Errorf("category = %q, want command_injection", f.SinkCategory)
	}

	// AC-3: the finding names the pack's id, version and hash.
	if len(f.Packs) != 1 {
		t.Fatalf("finding provenance = %+v, want exactly one pack ref", f.Packs)
	}
	got := f.Packs[0]
	if got.ID != entry.ID || got.Version != entry.Version || got.SHA256 != entry.ManifestSHA256 {
		t.Errorf("finding provenance = %+v, want %+v", got, entry)
	}
	if len(packedResult.Packs) != 1 {
		t.Errorf("run-level provenance = %+v, want one ref", packedResult.Packs)
	}

	// AC-3 rollback: disabling restores the pre-pack behaviour EXACTLY.
	if _, err := extpack.SetEnabled(root, entry.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	disabledCfg, err := LoadConfig(root)
	if err != nil {
		t.Fatalf("disabled LoadConfig: %v", err)
	}
	if canonical(t, disabledCfg) != baselineConfigBytes {
		t.Errorf("config after disabling is not the pre-pack config:\n got=%s\nwant=%s", canonical(t, disabledCfg), baselineConfigBytes)
	}
	if fp := ConfigFingerprint(root); fp != "" {
		t.Errorf("ConfigFingerprint after disabling = %q, want the pre-pack \"\"", fp)
	}
	disabledResult, err := New(disabledCfg, DefaultCaps(), nil).Run(context.Background(), reader)
	if err != nil {
		t.Fatalf("disabled run: %v", err)
	}
	if canonical(t, disabledResult) != baselineBytes {
		t.Errorf("result after disabling is not byte-identical to the pre-pack result:\n got=%s\nwant=%s",
			canonical(t, disabledResult), baselineBytes)
	}
}

// TestAC4_TaintMergeIsIndependentOfInstallOrder permutes the install order of
// two taint packs and byte-compares the merged config.
func TestAC4_TaintMergeIsIndependentOfInstallOrder(t *testing.T) {
	packA := func(t *testing.T) string {
		return writeTaintPack(t, "alpha.taint", acmeArtifact, "taint-sink:acme_shell", "taint-source:acme_ingest_header")
	}
	packB := func(t *testing.T) string {
		return writeTaintPack(t, "beta.taint", `version: "1"
sources:
  - id: beta_queue_message
    label: user_input
    name_patterns:
      - beta.Queue.Message
`, "taint-source:beta_queue_message")
	}
	render := func(order []func(*testing.T) string) string {
		root := t.TempDir()
		for _, mk := range order {
			installTaintPack(t, root, mk(t))
		}
		cfg, err := LoadConfig(root)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		return canonical(t, cfg)
	}
	first := render([]func(*testing.T) string{packA, packB})
	second := render([]func(*testing.T) string{packB, packA})
	if first != second {
		t.Errorf("merged taint config depends on install order:\n A=%s\n B=%s", first, second)
	}
	if !strings.Contains(first, "beta_queue_message") || !strings.Contains(first, "acme_ingest_header") {
		t.Fatalf("the permutation comparison is vacuous: %s", first)
	}
}

// TestAttack_APackMayNotRedefineABuiltInTaintDefinition is the consumer-side
// half of ADR 0013 threat T5, and the second producer of the sentinel SW-222
// reserved for pack policy.
func TestAttack_APackMayNotRedefineABuiltInTaintDefinition(t *testing.T) {
	root := t.TempDir()
	hostile := `version: "1"
sinks:
  - id: os_exec
    category: harmless
    name_patterns:
      - nothing.matches.this
`
	installTaintPack(t, root, writeTaintPack(t, "hostile.taint", hostile, "taint-sink:os_exec"))
	_, err := LoadConfig(root)
	if !errors.Is(err, registry.ErrUnsupportedOverride) {
		t.Fatalf("a pack redefining the built-in os_exec sink = %v, want registry.ErrUnsupportedOverride", err)
	}
	if !strings.Contains(err.Error(), "os_exec") {
		t.Errorf("the refusal must name the definition the pack tried to take: %v", err)
	}
	// And the fingerprint must not read healthy either: a config that cannot be
	// built must never warm-start.
	if fp := ConfigFingerprint(root); fp != "invalid" {
		t.Errorf("ConfigFingerprint over an unloadable pack set = %q, want \"invalid\"", fp)
	}
}

// TestAttack_AProjectConfigMayNotMintPackProvenance. Provenance a repository can
// write for itself is not provenance (ADR 0013 D5.2).
func TestAttack_AProjectConfigMayNotMintPackProvenance(t *testing.T) {
	for _, forged := range []string{
		`{"version":"1.0.0","packs":[{"id":"trustworthy.pack","version":"9.9.9","sha256":"` + strings.Repeat("0", 64) + `"}]}`,
		`{"version":"1.0.0","sinks":[{"id":"forged_sink","category":"x","name_patterns":["y"],"pack":{"id":"a","version":"1","sha256":"b"}}]}`,
	} {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ConfigDir), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, ConfigDir, ConfigFile), []byte(forged), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		if _, err := LoadConfig(root); err == nil {
			t.Fatalf("a project config minted pack provenance: %s", forged)
		}
	}
}

// TestAPackFailingToLoadFailsTheConfigClosed: a broken pack must not degrade to
// "run without it". An answer produced under rules the caller believes are in
// force and silently were not is the false-green this tree refuses everywhere.
func TestAPackFailingToLoadFailsTheConfigClosed(t *testing.T) {
	root := t.TempDir()
	entry := installTaintPack(t, root, writeTaintPack(t, "acme.taint", acmeArtifact,
		"taint-sink:acme_shell", "taint-source:acme_ingest_header"))
	if err := os.WriteFile(filepath.Join(extpack.PackDir(root, entry.ID), "artifact"), []byte("version: \"1\"\n"), 0o600); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	if _, err := LoadConfig(root); err == nil {
		t.Fatal("a tampered pack was silently skipped")
	}
}
