// Package mcpregistration manages checkout-bound MCP registrations. Existing
// state/repo.json descriptors remain authoritative for checkout/store identity;
// this package only validates and refers to those stores.
package mcpregistration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/internal/state"
)

// Store is a validated existing Graphi store for one checkout. Root is the
// path preserved by repo.json; CanonicalRoot is used only to detect aliases.
type Store struct {
	CheckoutID    string
	Root          string
	CanonicalRoot string
	Dir           string
	DB            string
	Meta          string
	RepoFile      string
}

// Resolver reads existing descriptors below StateDir. StateDir is injected so
// tests and callers never need to inspect a real user profile.
type Resolver struct {
	StateDir string
}

// NewResolver returns a resolver rooted at an explicit Graphi state directory.
func NewResolver(stateDir string) Resolver { return Resolver{StateDir: stateDir} }

// Resolve validates the existing descriptor, graph database, and ingest
// sidecar for root. It never creates or migrates an index.
func (r Resolver) Resolve(root string) (Store, error) {
	if !filepath.IsAbs(r.StateDir) {
		return Store{}, errors.New("mcp registration: state directory must be absolute")
	}
	absRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return Store{}, fmt.Errorf("mcp registration: resolve checkout path: %w", err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return Store{}, fmt.Errorf("mcp registration: checkout is unavailable: %w", err)
	}
	if !info.IsDir() {
		return Store{}, fmt.Errorf("mcp registration: checkout is not a directory: %s", absRoot)
	}
	detected, ok := state.DetectRepo(absRoot)
	if !ok {
		return Store{}, fmt.Errorf("mcp registration: no repository at %s", absRoot)
	}
	requestedCanonical, err := canonicalPath(absRoot)
	if err != nil {
		return Store{}, fmt.Errorf("mcp registration: canonical checkout path: %w", err)
	}
	detectedCanonical, err := canonicalPath(detected)
	if err != nil || detectedCanonical != requestedCanonical {
		return Store{}, fmt.Errorf("mcp registration: path is not the repository root: %s", absRoot)
	}

	entries, err := os.ReadDir(r.StateDir)
	if err != nil {
		return Store{}, fmt.Errorf("mcp registration: read state directory: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var matches []Store
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(r.StateDir, entry.Name())
		repoFile := filepath.Join(dir, "repo.json")
		descriptor, readErr := state.ReadRepoDescriptor(repoFile)
		if readErr != nil {
			if os.IsNotExist(unwrapPathError(readErr)) {
				continue
			}
			// A malformed descriptor whose root cannot be established is not a
			// candidate. All matching, readable descriptors are validated below.
			continue
		}
		if !filepath.IsAbs(descriptor.AbsRoot) {
			continue
		}
		descriptorCanonical, canonicalErr := canonicalPath(descriptor.AbsRoot)
		if canonicalErr != nil || descriptorCanonical != requestedCanonical {
			continue
		}
		if descriptor.Fingerprint == "" || descriptor.Fingerprint != state.Fingerprint(descriptor.AbsRoot) {
			return Store{}, fmt.Errorf("mcp registration: descriptor fingerprint mismatch in %s", repoFile)
		}
		if entry.Name() != descriptor.Fingerprint {
			return Store{}, fmt.Errorf("mcp registration: descriptor directory mismatch in %s", repoFile)
		}
		matches = append(matches, Store{
			CheckoutID:    descriptor.Fingerprint,
			Root:          filepath.Clean(descriptor.AbsRoot),
			CanonicalRoot: descriptorCanonical,
			Dir:           dir,
			DB:            filepath.Join(dir, "db.sqlite"),
			Meta:          filepath.Join(dir, "meta"),
			RepoFile:      repoFile,
		})
	}

	if len(matches) == 0 {
		return Store{}, fmt.Errorf("mcp registration: no existing store for %s; run graphi sync first", absRoot)
	}
	if len(matches) > 1 {
		ids := make([]string, 0, len(matches))
		for _, match := range matches {
			ids = append(ids, match.CheckoutID)
		}
		return Store{}, fmt.Errorf("mcp registration: multiple stores match checkout alias %s: %s", absRoot, strings.Join(ids, ", "))
	}
	if err := validateStore(matches[0]); err != nil {
		return Store{}, err
	}
	return matches[0], nil
}

func canonicalPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Clean(resolved))
}

func validateStore(store Store) error {
	roStore, err := graphstore.OpenSQLiteReadOnly(store.DB)
	if err != nil {
		return fmt.Errorf("mcp registration: validate graph store: %w", err)
	}
	if _, err := roStore.CountNodes(context.Background()); err != nil {
		_ = roStore.Close()
		return fmt.Errorf("mcp registration: validate graph schema: %w", err)
	}
	roMeta, err := ingest.NewReadOnly(roStore, parse.NewDefaultRegistry(), store.Meta)
	if err != nil {
		_ = roStore.Close()
		return fmt.Errorf("mcp registration: validate ingest metadata: %w", err)
	}
	// CanWarmStart probes the expected metadata schema. A cold/non-semantic
	// store is valid; only the probe error indicates a mismatched sidecar.
	_, _, probeErr := roMeta.CanWarmStart(context.Background(), store.Root)
	metaCloseErr := roMeta.Close()
	storeCloseErr := roStore.Close()
	if probeErr != nil {
		return fmt.Errorf("mcp registration: validate ingest metadata schema: %w", probeErr)
	}
	if metaCloseErr != nil {
		return fmt.Errorf("mcp registration: close ingest metadata: %w", metaCloseErr)
	}
	if storeCloseErr != nil {
		return fmt.Errorf("mcp registration: close graph store: %w", storeCloseErr)
	}
	return nil
}

// unwrapPathError exposes the underlying filesystem error for os.IsNotExist.
func unwrapPathError(err error) error {
	for err != nil {
		var pathErr *os.PathError
		if errors.As(err, &pathErr) {
			return pathErr.Err
		}
		break
	}
	return err
}
