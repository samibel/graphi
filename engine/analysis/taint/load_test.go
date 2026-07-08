package taint

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig writes body to <root>/.graphi/taint.json and returns root.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ConfigDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return root
}

// TestLoadConfig_AbsentFileIsDefaultUnchanged is the byte-parity guarantee: a
// repo with no .graphi/taint.json gets EXACTLY DefaultConfig (including its
// empty ContentHash), so its persisted intra-proc findings are identical to the
// pre-WP-09 behaviour and full-vs-incremental parity is unaffected.
func TestLoadConfig_AbsentFileIsDefaultUnchanged(t *testing.T) {
	root := t.TempDir() // no .graphi/taint.json
	got, err := LoadConfig(root)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	want := DefaultConfig()
	if got.Version != want.Version {
		t.Errorf("version = %q, want %q", got.Version, want.Version)
	}
	if got.ContentHash != want.ContentHash {
		t.Errorf("content hash = %q, want %q (default must be returned untouched)", got.ContentHash, want.ContentHash)
	}
	if len(got.Sources) != len(want.Sources) || len(got.Sinks) != len(want.Sinks) || len(got.Sanitizers) != len(want.Sanitizers) {
		t.Errorf("definition counts = (%d,%d,%d), want (%d,%d,%d)",
			len(got.Sources), len(got.Sinks), len(got.Sanitizers),
			len(want.Sources), len(want.Sinks), len(want.Sanitizers))
	}
}

// TestLoadConfig_NewIDAppends verifies a project can add a custom sink/source
// without touching the defaults: every built-in stays, and the new ID appears.
func TestLoadConfig_NewIDAppends(t *testing.T) {
	root := writeConfig(t, `{
		"sinks": [
			{"id": "custom_render", "category": "template_injection", "name_patterns": ["myrender.Render"]}
		]
	}`)
	got, err := LoadConfig(root)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	base := DefaultConfig()
	if len(got.Sinks) != len(base.Sinks)+1 {
		t.Fatalf("sink count = %d, want %d (all defaults + 1 custom)", len(got.Sinks), len(base.Sinks)+1)
	}
	if _, cat := got.MatchSink("call", "myrender.Render"); cat != "template_injection" {
		t.Errorf("custom sink not classified: category = %q", cat)
	}
	// A default sink must still match (defaults preserved).
	if id, _ := got.MatchSink("call", "os/exec.Command"); id != "os_exec" {
		t.Errorf("default sink lost: os/exec.Command classified as %q", id)
	}
	if got.ContentHash == "" || got.ContentHash == base.ContentHash {
		t.Errorf("content hash = %q, want a fresh non-empty hash distinct from default", got.ContentHash)
	}
}

// TestLoadConfig_SameIDOverrides verifies a same-ID definition REPLACES the
// built-in (retune/disable a default). An override with empty name_patterns and
// node_kinds matches nothing — the escape hatch to switch a default off.
func TestLoadConfig_SameIDOverrides(t *testing.T) {
	root := writeConfig(t, `{
		"sinks": [
			{"id": "os_exec", "category": "command_injection"}
		]
	}`)
	got, err := LoadConfig(root)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(got.Sinks) != len(DefaultConfig().Sinks) {
		t.Errorf("sink count = %d, want %d (override, not append)", len(got.Sinks), len(DefaultConfig().Sinks))
	}
	if id, _ := got.MatchSink("call", "os/exec.Command"); id != "" {
		t.Errorf("disabled sink still matched: %q", id)
	}
}

// TestLoadConfig_VersionOverride verifies the overlay's version wins when set.
func TestLoadConfig_VersionOverride(t *testing.T) {
	root := writeConfig(t, `{"version": "proj-2", "sources": [], "sinks": []}`)
	got, err := LoadConfig(root)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got.Version != "proj-2" {
		t.Errorf("version = %q, want %q", got.Version, "proj-2")
	}
}

// TestLoadConfig_HashOrderIndependent verifies computeConfigHash is independent
// of definition order: two configs with the same effective definitions in
// different overlay order hash the same.
func TestLoadConfig_HashOrderIndependent(t *testing.T) {
	rootA := writeConfig(t, `{"sinks": [
		{"id": "aaa", "category": "x", "name_patterns": ["A"]},
		{"id": "zzz", "category": "y", "name_patterns": ["Z"]}
	]}`)
	rootB := writeConfig(t, `{"sinks": [
		{"id": "zzz", "category": "y", "name_patterns": ["Z"]},
		{"id": "aaa", "category": "x", "name_patterns": ["A"]}
	]}`)
	a, err := LoadConfig(rootA)
	if err != nil {
		t.Fatalf("LoadConfig A: %v", err)
	}
	b, err := LoadConfig(rootB)
	if err != nil {
		t.Fatalf("LoadConfig B: %v", err)
	}
	if a.ContentHash != b.ContentHash {
		t.Errorf("hash order-dependent: %q != %q", a.ContentHash, b.ContentHash)
	}
}

// TestLoadConfig_MalformedFailsClosed verifies a syntactically broken or
// schema-violating config is a hard error, NEVER a silent fallback to defaults.
func TestLoadConfig_MalformedFailsClosed(t *testing.T) {
	cases := map[string]string{
		"invalid json":         `{ this is not json`,
		"unknown field":        `{"srcs": []}`,
		"source without id":    `{"sources": [{"label": "x", "name_patterns": ["A"]}]}`,
		"source without label": `{"sources": [{"id": "bad", "name_patterns": ["A"]}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			root := writeConfig(t, body)
			if _, err := LoadConfig(root); err == nil {
				t.Errorf("LoadConfig accepted %s config; want error (fail-closed)", name)
			}
		})
	}
}
