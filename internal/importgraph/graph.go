// Package importgraph snapshots graphi's module-internal package import graph as
// a deterministic, machine-readable artifact.
//
// ARCH-P0 (architecture Phase 0): the P2 modularization moves use-case logic out
// of the broad surface client into an application layer. Every later phase is
// judged by whether specific import edges disappeared — surface handlers must
// stop importing engine packages, contracts must import nothing inward, and the
// unclassified internal packages must each be assigned an architecture zone. That
// judgement needs a recorded starting point, not a recollection.
//
// This package produces that starting point. It is descriptive, not prescriptive:
// internal/layerguard remains the enforcing gate. A snapshot here answers "what
// did the graph look like at the baseline commit", so a later phase can diff
// against it and show the intended edges actually went away.
//
// Like internal/layerguard, internal/coverage, and internal/evidence, this is
// UNRANKED CI tooling: it is not part of any shipped runtime import graph.
package importgraph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// ModulePath is graphi's Go module path. Package paths in a snapshot are stored
// relative to it, so the artifact stays readable and diffable.
const ModulePath = "github.com/samibel/graphi"

// SchemaVersion versions the artifact envelope, so a change in what we capture is
// distinguishable from a change in the graph itself.
const SchemaVersion = 1

// Zone names classify a package by its top-level directory. They mirror the
// layerguard ranks plus the two categories layerguard leaves unconstrained today
// (internal tooling/runtime, and everything else), because naming those is
// precisely the Phase 10 work this baseline feeds.
const (
	ZoneCmd      = "cmd"
	ZoneSurfaces = "surfaces"
	ZoneEngine   = "engine"
	ZoneCore     = "core"
	ZoneInternal = "internal"
	ZoneOther    = "other"
)

// ModuleRoot resolves the module root once via `go env GOMOD` and caches it,
// mirroring the helper internal/layerguard and internal/bench use.
var ModuleRoot = sync.OnceValues(func() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", err
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == "/dev/null" {
		return "", fmt.Errorf("importgraph: no go.mod found (GOMOD=%q)", gomod)
	}
	return filepath.Dir(gomod), nil
})

// Package is one node of the snapshot: a module-relative package path, its zone,
// and its module-internal production imports (sorted).
type Package struct {
	Path    string   `json:"path"`
	Zone    string   `json:"zone"`
	Imports []string `json:"imports"`
}

// ZoneEdge aggregates how many module-internal import edges cross from one zone
// into another. This is the number later phases must move: "surfaces→engine: N"
// is exactly the debt the application layer has to absorb.
type ZoneEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Count int    `json:"count"`
}

// Snapshot is the full artifact.
type Snapshot struct {
	SchemaVersion int    `json:"schema_version"`
	Module        string `json:"module"`
	// Commit is the baseline revision this graph describes. It is supplied by the
	// caller rather than read from git: the artifact characterizes a specific
	// reviewed state, so re-rendering it later must not silently re-stamp it with
	// whatever HEAD happens to be.
	Commit string `json:"commit"`
	// Note records the capture scope in the artifact itself, so a reader does not
	// have to find this package to learn that test-only imports are excluded.
	Note      string     `json:"note"`
	Packages  []Package  `json:"packages"`
	ZoneEdges []ZoneEdge `json:"zone_edges"`
}

// captureNote documents the deliberate scope choice inside every artifact.
const captureNote = "Module-internal production imports only (go list Imports). " +
	"Test-only imports are excluded: test packages legitimately cross layers (a " +
	"cross-surface parity test drives CLI, MCP, and the engine at once), and the " +
	"architecture rules layerguard enforces govern production edges."

// ZoneOf classifies a module-internal package path (relative to ModulePath).
func ZoneOf(relPath string) string {
	top := relPath
	if i := strings.IndexByte(relPath, '/'); i >= 0 {
		top = relPath[:i]
	}
	switch top {
	case ZoneCmd, ZoneSurfaces, ZoneEngine, ZoneCore, ZoneInternal:
		return top
	}
	return ZoneOther
}

// rel converts a full import path into a module-relative one, reporting whether
// it belongs to this module at all.
func rel(importPath string) (string, bool) {
	if importPath == ModulePath {
		return ".", true
	}
	prefix := ModulePath + "/"
	if !strings.HasPrefix(importPath, prefix) {
		return "", false
	}
	return strings.TrimPrefix(importPath, prefix), true
}

// Build runs `go list -json ./...` in dir and returns the module-internal import
// graph at commit. External and standard-library imports are dropped: they are
// not what the architecture rules constrain.
func Build(ctx context.Context, dir, commit string) (Snapshot, error) {
	if commit == "" {
		return Snapshot{}, fmt.Errorf("importgraph: a baseline commit is required (the artifact records which revision it describes)")
	}
	cmd := exec.CommandContext(ctx, "go", "list", "-json", "./...")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Snapshot{}, err
	}
	if err := cmd.Start(); err != nil {
		return Snapshot{}, fmt.Errorf("importgraph: go list: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}

	dec := json.NewDecoder(stdout)
	var packages []Package
	edgeCount := map[string]int{}
	for {
		var p struct {
			ImportPath string
			Imports    []string
		}
		if err := dec.Decode(&p); err != nil {
			if err == io.EOF {
				break
			}
			_ = cmd.Wait()
			return Snapshot{}, fmt.Errorf("importgraph: decode go list json: %w", err)
		}
		relPath, ok := rel(p.ImportPath)
		if !ok {
			continue
		}
		zone := ZoneOf(relPath)
		imports := make([]string, 0, len(p.Imports))
		for _, imp := range p.Imports {
			relImp, inModule := rel(imp)
			if !inModule {
				continue
			}
			imports = append(imports, relImp)
			edgeCount[zone+"→"+ZoneOf(relImp)]++
		}
		sort.Strings(imports)
		packages = append(packages, Package{Path: relPath, Zone: zone, Imports: imports})
	}
	if err := cmd.Wait(); err != nil {
		return Snapshot{}, fmt.Errorf("importgraph: go list failed: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}

	sort.Slice(packages, func(i, j int) bool { return packages[i].Path < packages[j].Path })

	edges := make([]ZoneEdge, 0, len(edgeCount))
	for key, count := range edgeCount {
		from, to, _ := strings.Cut(key, "→")
		edges = append(edges, ZoneEdge{From: from, To: to, Count: count})
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})

	return Snapshot{
		SchemaVersion: SchemaVersion,
		Module:        ModulePath,
		Commit:        commit,
		Note:          captureNote,
		Packages:      packages,
		ZoneEdges:     edges,
	}, nil
}

// Render encodes the snapshot as canonical, reviewable JSON: HTML escaping off,
// two-space indent, trailing newline. Every list is already sorted, so the bytes
// are a pure function of the graph and the recorded commit.
func Render(s Snapshot) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		return nil, fmt.Errorf("importgraph: encode snapshot: %w", err)
	}
	return buf.Bytes(), nil
}

// Summary renders a short human-readable digest for the tool's stdout.
func Summary(s Snapshot) string {
	byZone := map[string]int{}
	for _, p := range s.Packages {
		byZone[p.Zone]++
	}
	zones := make([]string, 0, len(byZone))
	for z := range byZone {
		zones = append(zones, z)
	}
	sort.Strings(zones)

	var b strings.Builder
	fmt.Fprintf(&b, "import graph @ %s — %d module packages\n", s.Commit, len(s.Packages))
	for _, z := range zones {
		fmt.Fprintf(&b, "  %-9s %3d packages\n", z, byZone[z])
	}
	b.WriteString("zone edges (module-internal production imports):\n")
	for _, e := range s.ZoneEdges {
		fmt.Fprintf(&b, "  %-9s → %-9s %4d\n", e.From, e.To, e.Count)
	}
	return b.String()
}
