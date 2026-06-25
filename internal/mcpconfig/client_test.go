package mcpconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fakeClient builds a Client whose config lives at a fixed path, for testing the
// generic adapter machinery without touching real client locations.
func fakeClient(id, key, path string) Client {
	return Client{ID: id, Display: id, ServersKey: key, pathFn: func() (string, error) { return path, nil }}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return doc
}

func TestClient_Apply_KeyAndMergePreservesOthers(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{"mcpServers key (Claude/Cursor/Windsurf)", "mcpServers"},
		{"servers key (VS Code)", "servers"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cfg.json")
			// Pre-existing config with an UNRELATED server under the same key.
			writeJSON(t, path, map[string]any{
				tc.key:      map[string]any{"other": map[string]any{"type": "stdio", "command": "/bin/other"}},
				"unrelated": "keep-me",
			})
			c := fakeClient("x", tc.key, path)

			res, err := c.Apply("/usr/local/bin/graphi", nil, false)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			if res.Action != ActionCreated {
				t.Fatalf("action = %q, want created", res.Action)
			}
			if res.BackupPath == "" {
				t.Errorf("expected a backup of the pre-existing file")
			}

			doc := readJSON(t, path)
			if doc["unrelated"] != "keep-me" {
				t.Errorf("unrelated top-level key was not preserved: %v", doc["unrelated"])
			}
			servers, _ := doc[tc.key].(map[string]any)
			if servers == nil {
				t.Fatalf("servers key %q missing after apply", tc.key)
			}
			if _, ok := servers["other"]; !ok {
				t.Errorf("unrelated server 'other' was dropped")
			}
			g, ok := servers["graphi"].(map[string]any)
			if !ok {
				t.Fatalf("graphi entry missing under %q", tc.key)
			}
			if g["command"] != "/usr/local/bin/graphi" || g["type"] != "stdio" {
				t.Errorf("graphi entry wrong: %v", g)
			}
		})
	}
}

func TestClient_Apply_IdempotentAndDryRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.json")
	c := fakeClient("x", "mcpServers", path)

	// Dry-run on a virgin path writes nothing.
	res, err := c.Apply("/bin/graphi", nil, true)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if res.Action != ActionCreated {
		t.Fatalf("dry-run action = %q, want created", res.Action)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote a file (or stat err %v)", err)
	}

	// Real apply creates it.
	if _, err := c.Apply("/bin/graphi", nil, false); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Re-apply is unchanged (idempotent) and makes no backup.
	res, err = c.Apply("/bin/graphi", nil, false)
	if err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if res.Action != ActionUnchanged {
		t.Errorf("re-apply action = %q, want unchanged", res.Action)
	}
	if res.BackupPath != "" {
		t.Errorf("unchanged re-apply should not back up, got %q", res.BackupPath)
	}
}

func TestClient_Configurable(t *testing.T) {
	dir := t.TempDir()
	// File present → configurable.
	present := filepath.Join(dir, "present.json")
	writeJSON(t, present, map[string]any{})
	if !fakeClient("a", "mcpServers", present).Configurable() {
		t.Errorf("file present: want configurable")
	}
	// File absent but parent dir present → configurable (install dir exists).
	absentInDir := filepath.Join(dir, "absent.json")
	if !fakeClient("b", "mcpServers", absentInDir).Configurable() {
		t.Errorf("parent dir present: want configurable")
	}
	// Neither file nor parent dir → not configurable.
	deep := filepath.Join(dir, "no", "such", "dir", "cfg.json")
	if fakeClient("c", "mcpServers", deep).Configurable() {
		t.Errorf("missing parent dir: want not configurable")
	}
}

// TestClient_ClaudeParity proves the generalized "claude" client writes a file
// byte-identical to the legacy mcpconfig.Apply path (no regression).
func TestClient_ClaudeParity(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), "legacy.json")
	viaClient := filepath.Join(t.TempDir(), "client.json")

	bin := "/opt/graphi/graphi"
	if _, err := Apply(legacy, "graphi", GraphiEntry(bin, nil), false); err != nil {
		t.Fatalf("legacy apply: %v", err)
	}

	c, ok := ClientByID("claude")
	if !ok {
		t.Fatal("claude client not registered")
	}
	// Point the claude client at our temp path via the env override it honors.
	t.Setenv(EnvOverride, viaClient)
	if _, err := c.Apply(bin, nil, false); err != nil {
		t.Fatalf("client apply: %v", err)
	}

	a, _ := os.ReadFile(legacy)
	b, _ := os.ReadFile(viaClient)
	if string(a) != string(b) {
		t.Errorf("claude client not byte-identical to legacy Apply\nlegacy:\n%s\nclient:\n%s", a, b)
	}
}

func TestRegistry_KnownClientsAndKeys(t *testing.T) {
	want := map[string]string{
		"claude":         "mcpServers",
		"copilot":        "servers",
		"cursor":         "mcpServers",
		"windsurf":       "mcpServers",
		"claude-desktop": "mcpServers",
	}
	got := map[string]bool{}
	for _, c := range Clients() {
		if want[c.ID] != c.ServersKey {
			t.Errorf("client %q servers key = %q, want %q", c.ID, c.ServersKey, want[c.ID])
		}
		got[c.ID] = true
	}
	for id := range want {
		if !got[id] {
			t.Errorf("client %q not registered", id)
		}
	}
	if _, ok := ClientByID("nope"); ok {
		t.Errorf("ClientByID(nope) should be false")
	}
}
