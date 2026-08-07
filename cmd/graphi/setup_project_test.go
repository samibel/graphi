package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// projectRepo stages a minimal marked repository and returns its root.
func projectRepo(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// readProjectEntry decodes .mcp.json at root and returns the graphi entry.
func readProjectEntry(t *testing.T, root string) (command string, args []string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		t.Fatalf("read .mcp.json: %v", err)
	}
	var doc struct {
		MCPServers map[string]struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse .mcp.json: %v", err)
	}
	entry, ok := doc.MCPServers["graphi"]
	if !ok {
		t.Fatalf(".mcp.json has no graphi entry: %s", raw)
	}
	if entry.Type != "stdio" {
		t.Fatalf("entry type = %q, want stdio", entry.Type)
	}
	return entry.Command, entry.Args
}

// TestSetupProject_WritesPinnedProjectEntry pins the --project contract: the
// repo root is detected from the working directory (walking up from a
// subdirectory), .mcp.json lands at the root, and the entry pins the session
// via `mcp -root <abs root>`. A second run is a no-op (idempotent upsert).
func TestSetupProject_WritesPinnedProjectEntry(t *testing.T) {
	root := projectRepo(t)
	sub := filepath.Join(root, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	if rc := runSetupProject(sub, "", "/opt/bin/graphi", false); rc != 0 {
		t.Fatalf("runSetupProject = %d, want 0", rc)
	}
	command, args := readProjectEntry(t, root)
	if command != "/opt/bin/graphi" {
		t.Fatalf("command = %q, want the registered binary", command)
	}
	wantRoot, _ := filepath.Abs(root)
	if len(args) != 3 || args[0] != "mcp" || args[1] != "-root" || args[2] != wantRoot {
		t.Fatalf("args = %v, want [mcp -root %s]", args, wantRoot)
	}

	before, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if rc := runSetupProject(sub, "", "/opt/bin/graphi", false); rc != 0 {
		t.Fatalf("second runSetupProject = %d, want 0 (unchanged)", rc)
	}
	after, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("idempotent re-run rewrote .mcp.json:\nbefore: %s\nafter:  %s", before, after)
	}
}

// TestSetupProject_RootOverrideAndValidation pins --root: an explicit root is
// used as-given (absolutized), and a missing or non-directory root fails
// closed without writing anything.
func TestSetupProject_RootOverrideAndValidation(t *testing.T) {
	root := projectRepo(t)
	elsewhere := t.TempDir()

	if rc := runSetupProject(elsewhere, root, "/opt/bin/graphi", false); rc != 0 {
		t.Fatalf("runSetupProject with --root = %d, want 0", rc)
	}
	_, args := readProjectEntry(t, root)
	wantRoot, _ := filepath.Abs(root)
	if args[2] != wantRoot {
		t.Fatalf("pinned root = %q, want %q", args[2], wantRoot)
	}

	if rc := runSetupProject(elsewhere, filepath.Join(elsewhere, "missing"), "/opt/bin/graphi", false); rc != 1 {
		t.Fatalf("nonexistent --root rc = %d, want 1", rc)
	}
	file := filepath.Join(elsewhere, "plain.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rc := runSetupProject(elsewhere, file, "/opt/bin/graphi", false); rc != 1 {
		t.Fatalf("file --root rc = %d, want 1", rc)
	}
}

// TestSetupProject_NotARepoAndDryRun pins the failure and preview paths: a
// marker-less cwd without --root fails with the not-a-repo hint, and
// --dry-run writes nothing.
func TestSetupProject_NotARepoAndDryRun(t *testing.T) {
	if rc := runSetupProject(t.TempDir(), "", "/opt/bin/graphi", false); rc != 1 {
		t.Fatalf("marker-less cwd rc = %d, want 1", rc)
	}

	root := projectRepo(t)
	if rc := runSetupProject(root, "", "/opt/bin/graphi", true); rc != 0 {
		t.Fatalf("dry-run rc = %d, want 0", rc)
	}
	if _, err := os.Stat(filepath.Join(root, ".mcp.json")); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote .mcp.json (stat err = %v)", err)
	}
}
