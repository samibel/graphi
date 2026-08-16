package ingest

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/samibel/graphi/engine/link"
)

// buildModuleMap reads the named go.mod files FROM DISK and builds the Go
// module-resolution map (PARITY-002 fix, ADR 0009).
//
// Disk, deliberately, for both passes: fileUnit carries only a content HASH
// (the walk's memory discipline — sources are re-read at parse time), so there
// is no in-memory go.mod content to prefer, and the tree is the one truth the
// full and the incremental pass share. Over a stable tree the two therefore
// build identical maps, which is exactly the property the parity gates assert.
//
// Failure posture: a go.mod that cannot be read (deleted mid-pass, oversize,
// symlinked out of root) contributes NO resolution — never a stale or guessed
// one. Only failure to open the root itself is an error.
func (i *Ingester) buildModuleMap(root string, relPaths []string) (*link.ModuleMap, error) {
	gomods := map[string][]byte{}
	var rootHandle *os.Root
	for _, p := range relPaths {
		if !link.GoModPath(p) {
			continue
		}
		if _, dup := gomods[p]; dup {
			continue
		}
		if rootHandle == nil {
			h, err := os.OpenRoot(root)
			if err != nil {
				return nil, fmt.Errorf("ingest: open root for module map: %w", err)
			}
			rootHandle = h
			defer rootHandle.Close()
		}
		read := readRootedRegularFile(rootHandle, p, i.bounds.MaxFileSize)
		if read.reason != "" {
			continue // unreadable/deleted: contributes nothing, never a guess
		}
		gomods[p] = read.src
	}
	return link.NewModuleMap(gomods), nil
}

// moduleMapFromUnits builds the map for a FULL pass: the walk enumerated every
// file in the tree, so the units' paths are the complete go.mod census.
func (i *Ingester) moduleMapFromUnits(root string, units []fileUnit) (*link.ModuleMap, error) {
	paths := make([]string, 0, 4)
	for _, u := range units {
		if link.GoModPath(u.relPath) {
			paths = append(paths, u.relPath)
		}
	}
	return i.buildModuleMap(root, paths)
}

// moduleMapIncremental builds the map for an INCREMENTAL pass: this pass's
// units cover added/changed go.mod files, and the cache (read within tx so this
// pass's own upserts and purges are visible) covers every go.mod the tree
// already held. A go.mod deleted this pass fails its disk read and is thereby
// excluded — the same answer the full pass gives.
func (i *Ingester) moduleMapIncremental(ctx context.Context, tx *sql.Tx, root string, units []fileUnit) (*link.ModuleMap, error) {
	paths := make([]string, 0, 4)
	for _, u := range units {
		if link.GoModPath(u.relPath) {
			paths = append(paths, u.relPath)
		}
	}
	cached, err := i.cachedPathsTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, p := range cached {
		if link.GoModPath(p) {
			paths = append(paths, p)
		}
	}
	return i.buildModuleMap(root, paths)
}
