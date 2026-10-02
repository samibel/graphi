package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/samibel/graphi/internal/mcpregistration"
	"github.com/samibel/graphi/internal/state"
	"github.com/samibel/graphi/surfaces/mcp"
)

// attachRepoContext resolves provenance only when -db addresses Graphi's
// managed state layout. A legacy database elsewhere remains a valid attach with
// unknown provenance; a path inside managed state must validate completely.
func attachRepoContext(dbPath, metaDir string) (mcp.RepoContext, error) {
	if dbPath == "" {
		return mcp.RepoContext{}, nil
	}
	stateDir, err := state.StrictStateDir()
	if err != nil {
		// Strict state resolution is required for registration writes, but an
		// arbitrary legacy -db attach must remain usable without a profile.
		return mcp.RepoContext{}, nil
	}
	return resolveAttachRepoContext(stateDir, dbPath, metaDir)
}

func resolveAttachRepoContext(stateDir, dbPath, metaDir string) (mcp.RepoContext, error) {
	stateAbs, err := filepath.Abs(filepath.Clean(stateDir))
	if err != nil || !filepath.IsAbs(stateDir) {
		return mcp.RepoContext{}, fmt.Errorf("mcp repository provenance: state directory must be absolute")
	}
	dbAbs, err := filepath.Abs(filepath.Clean(dbPath))
	if err != nil {
		return mcp.RepoContext{}, fmt.Errorf("mcp repository provenance: resolve database path: %w", err)
	}
	rel, err := filepath.Rel(stateAbs, dbAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return mcp.RepoContext{}, nil
	}

	// Anything below Graphi state is managed-looking and therefore must match
	// the exact <checkout-id>/db.sqlite layout. It must never silently degrade
	// to an unmarked external database on descriptor disagreement.
	checkoutDir := filepath.Dir(dbAbs)
	if filepath.Base(dbAbs) != "db.sqlite" || filepath.Dir(checkoutDir) != stateAbs || filepath.Base(checkoutDir) == "." {
		return mcp.RepoContext{}, fmt.Errorf("mcp repository provenance: managed database path has an unexpected layout: %s", dbAbs)
	}
	descriptorPath := filepath.Join(checkoutDir, "repo.json")
	descriptor, err := state.ReadRepoDescriptor(descriptorPath)
	if err != nil {
		return mcp.RepoContext{}, fmt.Errorf("mcp repository provenance: %w", err)
	}
	store, err := mcpregistration.NewResolver(stateAbs).Resolve(descriptor.AbsRoot)
	if err != nil {
		return mcp.RepoContext{}, fmt.Errorf("mcp repository provenance: %w", err)
	}
	metaAbs, err := filepath.Abs(filepath.Clean(metaDir))
	if err != nil {
		return mcp.RepoContext{}, fmt.Errorf("mcp repository provenance: resolve metadata path: %w", err)
	}
	if dbAbs != filepath.Clean(store.DB) || metaDir == "" || metaAbs != filepath.Clean(store.Meta) || descriptorPath != filepath.Clean(store.RepoFile) {
		return mcp.RepoContext{}, fmt.Errorf("mcp repository provenance: attached DB/meta do not match the validated repo descriptor")
	}

	label := filepath.Base(store.Root)
	manifest, err := mcpregistration.LoadManifest(mcpregistration.ManifestPath(stateAbs))
	if err != nil {
		return mcp.RepoContext{}, fmt.Errorf("mcp repository provenance: %w", err)
	}
	seen := false
	for _, registration := range manifest.Registrations {
		if registration.CheckoutID != store.CheckoutID {
			continue
		}
		if seen {
			return mcp.RepoContext{}, fmt.Errorf("mcp repository provenance: multiple registrations for checkout %s", store.CheckoutID)
		}
		seen = true
		if filepath.Clean(registration.RepoFile) != filepath.Clean(store.RepoFile) || registration.Name == "" {
			return mcp.RepoContext{}, fmt.Errorf("mcp repository provenance: registration disagrees with the validated repo descriptor")
		}
		label = registration.Name
	}
	return mcp.RepoContext{
		CheckoutID: store.CheckoutID, Root: store.Root, Label: label,
	}, nil
}
