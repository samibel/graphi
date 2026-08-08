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
		"devin":          "mcpServers",
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

// TestClient_ContendingGraphiServers pins the duplicate-entry detection: only
// zero-config graphi entries count (a -db/-daemon pin attaches to an explicit
// store and never contends; non-graphi commands are ignored), names come back
// sorted, and a missing config yields an empty list.
func TestClient_ContendingGraphiServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	doc := map[string]any{
		"mcpServers": map[string]any{
			"graphi":       map[string]any{"type": "stdio", "command": "/usr/local/bin/graphi", "args": []any{"mcp"}},
			"graphi-mars":  map[string]any{"type": "stdio", "command": "/Users/x/.local/bin/graphi", "args": []any{"mcp"}},
			"graphi-win":   map[string]any{"type": "stdio", "command": `C:\tools\graphi.EXE`, "args": []any{"mcp"}},
			"graphi-db":    map[string]any{"type": "stdio", "command": "/usr/local/bin/graphi", "args": []any{"mcp", "-db", "/x/db.sqlite"}},
			"graphi-db-eq": map[string]any{"type": "stdio", "command": "/usr/local/bin/graphi", "args": []any{"mcp", "--db=/x/db.sqlite"}},
			"graphi-sock":  map[string]any{"type": "stdio", "command": "/usr/local/bin/graphi", "args": []any{"mcp", "-daemon", "/tmp/g.sock"}},
			"other":        map[string]any{"type": "stdio", "command": "/usr/bin/other-tool", "args": []any{"serve"}},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	c := fakeClient("claude", "mcpServers", path)
	names, err := c.ContendingGraphiServers()
	if err != nil {
		t.Fatalf("ContendingGraphiServers: %v", err)
	}
	want := []string{"graphi", "graphi-mars", "graphi-win"}
	if len(names) != len(want) {
		t.Fatalf("contending = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("contending = %v, want %v", names, want)
		}
	}

	missing := fakeClient("claude", "mcpServers", filepath.Join(dir, "absent.json"))
	names, err = missing.ContendingGraphiServers()
	if err != nil || len(names) != 0 {
		t.Fatalf("missing config: names=%v err=%v, want empty, nil", names, err)
	}
}

// entryWithArgs builds a config-shaped entry ([]any args, as JSON decoding
// yields) for the pinned predicate.
func entryWithArgs(command string, args ...string) map[string]any {
	raw := make([]any, len(args))
	for i, a := range args {
		raw[i] = a
	}
	return map[string]any{"type": "stdio", "command": command, "args": raw}
}

// TestGraphiEntryIsPinned pins the predicate `ContendingGraphiServers` filters
// on. `-root <repo>` (SW-163) pins a session exactly as `-db`/`-daemon` do — it
// binds the repository outright, so the process performs no detection and never
// contends on an auto-resolved repo's ingest lock. Every spelling `graphi mcp`
// itself accepts (cmd/graphi/serve.go extractMCPFlags) must be recognised, and a
// bare `-root` — which that parser REJECTS as an unknown argument — must not be.
func TestGraphiEntryIsPinned(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		// -root, all four spellings the flag parser accepts.
		{"-root with a value", []string{"mcp", "-root", "/repos/mars"}, true},
		{"--root with a value", []string{"mcp", "--root", "/repos/mars"}, true},
		{"-root=value", []string{"mcp", "-root=/repos/mars"}, true},
		{"--root=value", []string{"mcp", "--root=/repos/mars"}, true},
		{"-root alongside -labs", []string{"mcp", "-labs", "-root", "/repos/mars"}, true},

		// AC4 negatives: a root flag that names no repository pins nothing.
		{"bare -root, no value", []string{"mcp", "-root"}, false},
		{"bare --root, no value", []string{"mcp", "--root"}, false},
		{"-root with an empty value", []string{"mcp", "-root", ""}, false},
		{"-root= with an empty value", []string{"mcp", "-root="}, false},

		// AC3 regression: db/daemon recognition is untouched.
		{"-db", []string{"mcp", "-db", "/x/db.sqlite"}, true},
		{"--db=value", []string{"mcp", "--db=/x/db.sqlite"}, true},
		{"-daemon", []string{"mcp", "-daemon", "/tmp/g.sock"}, true},
		{"--daemon=value", []string{"mcp", "--daemon=/tmp/g.sock"}, true},
		{"bare -db keeps its pre-SW-163 lenient treatment", []string{"mcp", "-db"}, true},

		// Zero-config: the entries that DO contend.
		{"zero-config mcp", []string{"mcp"}, false},
		{"no args at all", nil, false},
		{"an unrelated flag", []string{"mcp", "-labs"}, false},
		{"a flag that merely starts with root", []string{"mcp", "-rootless", "x"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := graphiEntryIsPinned(entryWithArgs("/usr/local/bin/graphi", tc.args...)); got != tc.want {
				t.Fatalf("graphiEntryIsPinned(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

// TestClient_ContendingGraphiServers_RootPinnedDoesNotContend is the AC5
// round-trip that ties the predicate to the defect it exists to fix: with two
// graphi entries where exactly one carries `-root`, doctor's `mcp` check warns
// only at two or more contending entries (internal/doctor/checks.go), so the
// pinned one must drop out and leave fewer than two. A non-graphi command
// carrying `-root` stays out of the count entirely (AC4).
func TestClient_ContendingGraphiServers_RootPinnedDoesNotContend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	doc := map[string]any{
		"mcpServers": map[string]any{
			"graphi":       entryWithArgs("/usr/local/bin/graphi", "mcp"),
			"graphi-mars":  entryWithArgs("/usr/local/bin/graphi", "mcp", "-root", "/repos/mars"),
			"rooted-other": entryWithArgs("/usr/bin/other-tool", "serve", "-root", "/repos/mars"),
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	names, err := fakeClient("claude", "mcpServers", path).ContendingGraphiServers()
	if err != nil {
		t.Fatalf("ContendingGraphiServers: %v", err)
	}
	if len(names) >= 2 {
		t.Fatalf("contending = %v (%d entries): a -root-pinned entry still counts, so doctor keeps warning", names, len(names))
	}
	if len(names) != 1 || names[0] != "graphi" {
		t.Fatalf("contending = %v, want exactly [graphi]", names)
	}
}

// TestClient_ContendingGraphiServers_SetupProjectEntryIsPinned pins the reader
// to the writer rather than to an assumption about it: it builds the entry the
// way `graphi setup --project` does (cmd/graphi/setup.go — GraphiEntry(bin,
// []string{"mcp", "-root", <abs root>}) applied through Apply), writes it
// through the real write path, and reads it back through the real config
// decode. An entry graphi itself wrote must never be reported as contending.
func TestClient_ContendingGraphiServers_SetupProjectEntryIsPinned(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), ".mcp.json")

	// Exactly cmd/graphi/setup.go's runSetupProject default (non-attach) path.
	entry := GraphiEntry("/usr/local/bin/graphi", []string{"mcp", "-root", root})
	if _, err := Apply(path, "graphi", entry, false); err != nil {
		t.Fatalf("apply setup --project entry: %v", err)
	}

	names, err := fakeClient("claude", "mcpServers", path).ContendingGraphiServers()
	if err != nil {
		t.Fatalf("ContendingGraphiServers: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("contending = %v, want none: `setup --project` writes a -root-pinned entry", names)
	}
}
