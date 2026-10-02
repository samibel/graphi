package mcpconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/testenv"
)

func codexFixture(path string) Client {
	return Client{ID: "codex", Display: "Codex", Format: FormatTOML, pathFn: func() (string, error) { return path, nil }}
}

func TestCodexNamedMCPTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	entry := GraphiEntry("/opt/graphi", []string{"mcp", "-db", "/state/service/db.sqlite", "-meta", "/state/service/meta"})
	result, err := codexFixture(path).ApplyEntry("graphi-service", entry, false)
	if err != nil || result.Action != ActionCreated {
		t.Fatalf("apply = (%q, %v), want created", result.Action, err)
	}
	raw := string(readBytes(t, path))
	if !strings.Contains(raw, "[mcp_servers.graphi-service]") || !strings.Contains(raw, `command = "/opt/graphi"`) {
		t.Fatalf("named Codex table missing:\n%s", raw)
	}
	if err := validateTOML([]byte(raw)); err != nil {
		t.Fatalf("generated TOML is invalid: %v", err)
	}
}

func TestCodexPreservesUnrelatedSettingsAndComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "# keep this comment\nmodel = \"gpt-synthetic\"\n\n[features]\nweb_search = true # inline stays\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := codexFixture(path).ApplyEntry("graphi-service", GraphiEntry("/bin/graphi", nil), false); err != nil {
		t.Fatal(err)
	}
	raw := string(readBytes(t, path))
	for _, preserved := range []string{"# keep this comment", `model = "gpt-synthetic"`, "web_search = true # inline stays"} {
		if !strings.Contains(raw, preserved) {
			t.Fatalf("lost %q:\n%s", preserved, raw)
		}
	}
}

func TestCodexQuotedKeysAndEscapedPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	entry := GraphiEntry(`C:\Program Files\Graphi\graphi.exe`, []string{"mcp", "-db", `C:\State\service\db.sqlite`})
	if _, err := codexFixture(path).ApplyEntry("graphi.service", entry, false); err != nil {
		t.Fatal(err)
	}
	raw := string(readBytes(t, path))
	if !strings.Contains(raw, `[mcp_servers."graphi.service"]`) {
		t.Fatalf("dotted server key was not quoted:\n%s", raw)
	}
	if err := validateTOML([]byte(raw)); err != nil {
		t.Fatalf("escaped path output is invalid TOML: %v\n%s", err, raw)
	}
	doc, err := decodeTOML([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	servers := doc["mcp_servers"].(map[string]any)
	server := servers["graphi.service"].(map[string]any)
	if server["command"] != entry.Command {
		t.Fatalf("decoded command = %q, want %q", server["command"], entry.Command)
	}
}

func TestCodexUnknownNestedFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := `[mcp_servers.graphi-service]
command = "/old/graphi"
args = ["mcp"]
unknown = "keep"

[mcp_servers.graphi-service.tool_policy]
allow = ["query", "search"]
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	entry := GraphiEntry("/new/graphi", []string{"mcp", "-db", "/state/db.sqlite"})
	if _, err := codexFixture(path).ApplyEntry("graphi-service", entry, false); err != nil {
		doc, _ := decodeTOML([]byte(original))
		updated, _ := editCodexTOML([]byte(original), doc, "graphi-service", entry)
		t.Fatalf("%v\n%s", err, updated)
	}
	raw := string(readBytes(t, path))
	for _, preserved := range []string{`unknown = "keep"`, `[mcp_servers.graphi-service.tool_policy]`, `allow = ["query", "search"]`} {
		if !strings.Contains(raw, preserved) {
			t.Fatalf("nested field lost (%q):\n%s", preserved, raw)
		}
	}
}

func TestCodexDuplicateOrInvalidTablesFailClosed(t *testing.T) {
	for _, original := range []string{
		"[mcp_servers.graphi-service]\ncommand=\"one\"\n[mcp_servers.graphi-service]\ncommand=\"two\"\n",
		"[mcp_servers.graphi-service\ncommand=\"broken\"\n",
	} {
		t.Run(strings.Split(original, "\n")[0], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := codexFixture(path).ApplyEntry("graphi-service", GraphiEntry("/bin/graphi", nil), false); err == nil {
				t.Fatal("invalid or duplicate TOML was accepted")
			}
			if got := string(readBytes(t, path)); got != original {
				t.Fatalf("invalid TOML changed:\n%s", got)
			}
		})
	}
}

func TestCodexDisabledEntryPreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "[mcp_servers.graphi-service]\ncommand = \"/old/graphi\"\nargs = [\"mcp\"]\nenabled = false\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := codexFixture(path).ApplyEntry("graphi-service", GraphiEntry("/new/graphi", nil), false); err != nil {
		doc, _ := decodeTOML([]byte(original))
		updated, _ := editCodexTOML([]byte(original), doc, "graphi-service", GraphiEntry("/new/graphi", nil))
		t.Fatalf("%v\n%s", err, updated)
	}
	if raw := string(readBytes(t, path)); !strings.Contains(raw, "enabled = false") {
		t.Fatalf("manual disabled state was lost:\n%s", raw)
	}
}

func TestCodexRemovePreservesUnrelatedTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := `# keep
model = "synthetic"

[mcp_servers.graphi-service]
command = "/bin/graphi"
args = ["mcp"]

[mcp_servers.graphi-service.tool_policy]
allow = ["query"]

[mcp_servers.foreign]
command = "/bin/foreign"
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := codexFixture(path).RemoveEntry("graphi-service", false)
	if err != nil || result.Action != ActionRemoved {
		t.Fatalf("remove = (%q, %v)", result.Action, err)
	}
	raw := string(readBytes(t, path))
	if strings.Contains(raw, "graphi-service") || !strings.Contains(raw, "# keep") || !strings.Contains(raw, "[mcp_servers.foreign]") {
		t.Fatalf("target removal damaged unrelated TOML:\n%s", raw)
	}
	if err := validateTOML([]byte(raw)); err != nil {
		t.Fatalf("removal produced invalid TOML: %v", err)
	}
}

func TestCodexObservedMutationRejectsExternalEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	client := codexFixture(path)
	entry := GraphiEntry("/opt/graphi", []string{"mcp", "-db", "/state/db.sqlite"})
	plan, err := client.PlanEntryState("graphi-service", entry)
	if err != nil {
		t.Fatal(err)
	}
	external := []byte("# external edit\nmodel = \"synthetic\"\n")
	if err := os.WriteFile(path, external, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ApplyEntryObserved("graphi-service", entry, plan.BeforeConfigDigest, false); err == nil {
		t.Fatal("Codex observed writer overwrote an external edit")
	}
	if got := readBytes(t, path); string(got) != string(external) {
		t.Fatalf("external Codex config changed after refusal:\n%s", got)
	}
}

func TestCodexHomeOverride(t *testing.T) {
	paths := testenv.Isolate(t)
	codexHome := filepath.Join(paths.Root, "custom-codex")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	client, ok := ClientByID("codex")
	if !ok {
		t.Fatal("codex client is not registered")
	}
	got, err := client.ConfigPath()
	if err != nil || got != filepath.Join(codexHome, "config.toml") {
		t.Fatalf("Codex config path = %q, %v", got, err)
	}
}
