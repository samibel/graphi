package mcpregistration_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/internal/mcpregistration"
)

func TestUnknownManifestVersionFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp-registrations.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":2,"registrations":[]}`), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if _, err := mcpregistration.LoadManifest(path); err == nil {
		t.Fatal("LoadManifest accepted an unknown schema version")
	}
}

func TestManifestDryRunWritesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	path := filepath.Join(dir, "mcp-registrations.json")
	m := mcpregistration.NewManifest()
	m.Registrations = append(m.Registrations, mcpregistration.Registration{
		CheckoutID: "1111111111111111",
		Name:       "graphi-service",
		RepoFile:   "/state/1111111111111111/repo.json",
	})
	if err := mcpregistration.SaveManifest(path, m, true); err != nil {
		t.Fatalf("dry-run save: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("dry-run created state directory: %v", err)
	}
}
