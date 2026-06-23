package mcpconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, _ := json.MarshalIndent(v, "", "  ")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestConfigPath_EnvOverride(t *testing.T) {
	t.Setenv(EnvOverride, "/tmp/custom-claude.json")
	got, err := ConfigPath()
	if err != nil || got != "/tmp/custom-claude.json" {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestConfigPath_DefaultHome(t *testing.T) {
	t.Setenv(EnvOverride, "")
	home, _ := os.UserHomeDir()
	got, err := ConfigPath()
	if err != nil || got != filepath.Join(home, DefaultName) {
		t.Fatalf("got %q want %q (err=%v)", got, filepath.Join(home, DefaultName), err)
	}
}

func TestApply_CreateUpdateUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	entry := GraphiEntry("/usr/local/bin/graphi", nil)

	// create
	act, _, err := Apply(path, "graphi", entry, false)
	if err != nil || act != ActionCreated {
		t.Fatalf("create: act=%s err=%v", act, err)
	}
	doc, _ := Load(path)
	if got := doc["mcpServers"].(map[string]any)["graphi"]; !entryMatches(got, entry) {
		t.Fatalf("after create entry=%v", got)
	}

	// unchanged (idempotent)
	act, _, err = Apply(path, "graphi", entry, false)
	if err != nil || act != ActionUnchanged {
		t.Fatalf("unchanged: act=%s err=%v", act, err)
	}

	// update (different binary)
	entry2 := GraphiEntry("/opt/graphi/bin/graphi", nil)
	act, _, err = Apply(path, "graphi", entry2, false)
	if err != nil || act != ActionUpdated {
		t.Fatalf("update: act=%s err=%v", act, err)
	}
	doc, _ = Load(path)
	if got := doc["mcpServers"].(map[string]any)["graphi"]; !entryMatches(got, entry2) {
		t.Fatalf("after update entry=%v", got)
	}
}

func TestApply_PreservesUnrelatedKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	// pre-existing config with unrelated keys + a sibling server
	writeJSON(t, path, map[string]any{
		"userID":   "u-123",
		"theme":    "dark",
		"mcpServers": map[string]any{
			"chrome-devtools": map[string]any{"type": "stdio", "command": "npx"},
		},
	})
	entry := GraphiEntry("/bin/graphi", nil)
	if _, _, err := Apply(path, "graphi", entry, false); err != nil {
		t.Fatal(err)
	}
	doc, _ := Load(path)
	if doc["userID"] != "u-123" || doc["theme"] != "dark" {
		t.Fatalf("unrelated top-level keys lost: %v", doc)
	}
	servers := doc["mcpServers"].(map[string]any)
	if _, ok := servers["chrome-devtools"]; !ok {
		t.Fatalf("sibling mcpServers entry deleted: %v", servers)
	}
	if _, ok := servers["graphi"]; !ok {
		t.Fatalf("graphi entry not added: %v", servers)
	}
}

func TestApply_DryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	entry := GraphiEntry("/bin/graphi", nil)
	act, diff, err := Apply(path, "graphi", entry, true)
	if err != nil || act != ActionCreated {
		t.Fatalf("dry-run: act=%s err=%v", act, err)
	}
	if diff == "" {
		t.Fatal("dry-run diff empty")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote a file: %v", err)
	}
}

func TestApply_CorruptInputLeavesOriginalIntact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	orig := []byte("{not valid json")
	if err := os.WriteFile(path, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := Apply(path, "graphi", GraphiEntry("/bin/graphi", nil), false)
	if err == nil {
		t.Fatal("expected error on corrupt input")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(orig) {
		t.Fatalf("corrupt input modified on error: %s", got)
	}
}

func TestGraphiEntry_DefaultArgs(t *testing.T) {
	e := GraphiEntry("/bin/graphi", nil)
	if len(e.Args) != 1 || e.Args[0] != "mcp" {
		t.Fatalf("default args = %v, want [mcp]", e.Args)
	}
	if e.Type != "stdio" {
		t.Fatalf("type = %s", e.Type)
	}
}

// entryMatches compares a decoded map to a ServerEntry semantically
// (key-order independent), reusing the package's normalizeJSON.
func entryMatches(got any, want ServerEntry) bool {
	return equalJSON(got, want)
}
