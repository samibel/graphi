package mcpregistration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ManifestSchemaVersion is the only schema this binary may read or write.
const ManifestSchemaVersion = 1

// Registration binds a stable server name to an existing repo descriptor.
// Root, DB, and Meta deliberately remain absent: repo.json and the state layout
// are the single source of truth for those values.
type Registration struct {
	CheckoutID string `json:"checkout_id"`
	Name       string `json:"name"`
	RepoFile   string `json:"repo_file"`
}

// Manifest records only local registration metadata. Later schema-v1 tasks add
// receipts, pending changes, and policies without duplicating store identity.
type Manifest struct {
	SchemaVersion int            `json:"schema_version"`
	Registrations []Registration `json:"registrations,omitempty"`
}

// NewManifest returns an empty manifest using the current schema.
func NewManifest() Manifest { return Manifest{SchemaVersion: ManifestSchemaVersion} }

// ManifestPath returns the registration manifest location below stateDir.
func ManifestPath(stateDir string) string {
	return filepath.Join(stateDir, "mcp-registrations.json")
}

// LoadManifest reads a manifest without changing the filesystem. A missing
// file represents an empty current-version manifest; unknown versions fail
// closed.
func LoadManifest(path string) (Manifest, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewManifest(), nil
	}
	if err != nil {
		return Manifest{}, fmt.Errorf("mcp registration: read manifest: %w", err)
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(b))
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("mcp registration: decode manifest: %w", err)
	}
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return Manifest{}, fmt.Errorf("mcp registration: unsupported manifest schema version %d", manifest.SchemaVersion)
	}
	return manifest, nil
}

// SaveManifest atomically writes a private manifest. In dry-run mode it does no
// filesystem I/O at all, including directory and temporary-file creation.
func SaveManifest(path string, manifest Manifest, dryRun bool) error {
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("mcp registration: cannot write manifest schema version %d", manifest.SchemaVersion)
	}
	if dryRun {
		return nil
	}
	b, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("mcp registration: encode manifest: %w", err)
	}
	b = append(b, '\n')
	if _, err := decodeManifestBytes(b); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mcp registration: create manifest directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("mcp registration: protect manifest directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".mcp-registrations-*")
	if err != nil {
		return fmt.Errorf("mcp registration: create temporary manifest: %w", err)
	}
	tmpPath := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("mcp registration: protect temporary manifest: %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("mcp registration: write temporary manifest: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("mcp registration: sync temporary manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("mcp registration: close temporary manifest: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("mcp registration: replace manifest: %w", err)
	}
	removeTemp = false
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("mcp registration: protect manifest: %w", err)
	}
	return nil
}

func decodeManifestBytes(b []byte) (Manifest, error) {
	var manifest Manifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("mcp registration: validate manifest bytes: %w", err)
	}
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return Manifest{}, fmt.Errorf("mcp registration: unsupported manifest schema version %d", manifest.SchemaVersion)
	}
	return manifest, nil
}
