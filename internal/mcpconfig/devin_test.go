package mcpconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/internal/testenv"
)

func TestDevinDedicatedConfig(t *testing.T) {
	paths := testenv.Isolate(t)
	dedicated := filepath.Join(paths.Home, ".config", "devin", "mcp_config.json")
	if err := os.MkdirAll(filepath.Dir(dedicated), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dedicated, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := devinConfigPath()
	if err != nil || got != dedicated {
		t.Fatalf("Devin dedicated path = %q, %v", got, err)
	}
}

func TestDevinLegacyConfig(t *testing.T) {
	paths := testenv.Isolate(t)
	legacy := filepath.Join(paths.Home, ".config", "devin", "config.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := devinConfigPath()
	if err != nil || got != legacy {
		t.Fatalf("Devin legacy path = %q, %v", got, err)
	}
}

func TestDevinUnknownVersionAmbiguousPaths(t *testing.T) {
	paths := testenv.Isolate(t)
	dir := filepath.Join(paths.Home, ".config", "devin")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mcp_config.json", "config.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"mcpServers":{}}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := devinConfigPath(); err == nil {
		t.Fatal("ambiguous Devin configs selected a path")
	}
}

func TestDevinWindowsPath(t *testing.T) {
	got, err := resolveDevinConfigPath("windows", `C:\Users\synthetic`, `C:\Users\synthetic\AppData\Roaming`)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(`C:\Users\synthetic\AppData\Roaming`, "devin", "mcp_config.json")
	if got != want {
		t.Fatalf("Windows Devin path = %q, want %q", got, want)
	}
}

func TestConfigOverrideUsesSelectedClientFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "looks-like-json.json")
	client, ok := ClientByID("codex")
	if !ok {
		t.Fatal("codex client is not registered")
	}
	client = client.WithConfigPath(path)
	if _, err := client.ApplyEntry("graphi-service", GraphiEntry("/bin/graphi", nil), false); err != nil {
		t.Fatal(err)
	}
	raw := readBytes(t, path)
	if err := validateTOML(raw); err != nil {
		t.Fatalf("Codex override was not written as TOML: %v\n%s", err, raw)
	}
}
