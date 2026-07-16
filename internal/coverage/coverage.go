// Package coverage derives graphi's LIVE capability set directly from the
// registries the product runs on — registered parsers, registered analyzers,
// maximally advertisable MCP tools, and present surfaces — so the checked-in capability
// coverage matrix (docs/coverage-matrix.yaml + docs/coverage-matrix.md) can be
// machine-checked against reality and never silently drift (story SW-060, FU-4).
//
// It mirrors the internal/layerguard pattern: a read-only, deterministic guard
// over real code, surfaced by a cmd/* entrypoint and exercised by a CI workflow.
// Like layerguard, package coverage is UNRANKED tooling (it sits outside the
// cmd→surfaces→engine→core chain), so it may import core/engine/surfaces purely
// to enumerate their registries without violating the layer-direction invariant.
//
// Determinism is a hard requirement: every enumerated list is sorted so the
// drift check is never flaky.
package coverage

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/surfaces/mcp"
)

// Category names for the four code-derived capability categories the drift guard
// enforces. They are the canonical category strings used in the coverage matrix.
const (
	CategoryParser   = "parser"
	CategoryAnalyzer = "analyzer"
	CategoryMCPTool  = "mcp-tool"
	CategorySurface  = "surface"
	CategoryCLI      = "cli-subcommand"
)

// LiveSet is the deterministic, sorted snapshot of every live capability across
// the four code-derived categories. It is derived purely from the running
// registries; nothing here is hand-maintained.
type LiveSet struct {
	Parsers        []string // registered parser languages (parse.NewDefaultRegistry().Languages())
	Analyzers      []string // registered analyzer names (analysis default registry .Names())
	MCPTools       []string // maximal Stable+Labs union (mcp.ToolNames()); tiers define the default profile
	Surfaces       []string // present surfaces (fixed dir→id map, existence-checked)
	CLISubcommands []string // dispatch case labels statically scanned from cmd/graphi/main.go
}

// IDs returns every live capability id paired with its category, sorted by
// (category, id). It is the flat view the drift guard diffs against the matrix.
func (l LiveSet) IDs() []Capability {
	var out []Capability
	add := func(cat string, ids []string) {
		for _, id := range ids {
			out = append(out, Capability{ID: id, Category: cat})
		}
	}
	add(CategoryParser, l.Parsers)
	add(CategoryAnalyzer, l.Analyzers)
	add(CategoryMCPTool, l.MCPTools)
	add(CategorySurface, l.Surfaces)
	add(CategoryCLI, l.CLISubcommands)
	sortCapabilities(out)
	return out
}

// surfaceDirs is the fixed, documented mapping from a repository directory to its
// canonical surface id. The shared surfaces/client transport is intentionally
// NOT a surface (it has no user-facing entrypoint). Keeping the mapping explicit
// (rather than globbing surfaces/*) makes the id set stable and lets the scan
// treat a REMOVED surface as drift (a missing mapped dir is an error).
var surfaceDirs = []struct{ dir, id string }{
	{"surfaces/cli", "cli"},
	{"surfaces/daemon", "daemon"},
	{"surfaces/http", "http"},
	{"surfaces/mcp", "mcp"},
	{"surfaces/tui", "tui"},
	{"web", "web"},
	{"extensions/github-action", "github-action"},
	{"extensions/vscode", "vscode"},
}

// Enumerate builds the live capability set from the registries. It is read-only
// and deterministic. The surface scan resolves the module root via `go env
// GOMOD` (the same mechanism internal/layerguard uses) and verifies each mapped
// surface directory exists.
func Enumerate() (LiveSet, error) {
	parsers := parse.NewDefaultRegistry().Languages() // already sorted; copy defensively
	parsers = sortedCopy(parsers)

	analyzers := analysis.NewDefaultService(emptyReader{}).Names() // sorted; concept included via Searcher
	analyzers = sortedCopy(analyzers)

	tools := mcp.ToolNames() // sorted, deduped
	tools = sortedCopy(tools)

	surfaces, err := enumerateSurfaces()
	if err != nil {
		return LiveSet{}, err
	}

	subcommands, err := enumerateCLISubcommands()
	if err != nil {
		return LiveSet{}, err
	}

	return LiveSet{
		Parsers:        parsers,
		Analyzers:      analyzers,
		MCPTools:       tools,
		Surfaces:       surfaces,
		CLISubcommands: subcommands,
	}, nil
}

// enumerateSurfaces returns the sorted ids of every present surface, erroring if
// a mapped surface directory is missing (a removed surface must be reflected in
// the matrix, so it is surfaced as an error rather than silently dropped).
func enumerateSurfaces() ([]string, error) {
	root, err := moduleRoot()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(surfaceDirs))
	for _, s := range surfaceDirs {
		if !isDir(filepath.Join(root, filepath.FromSlash(s.dir))) {
			return nil, fmt.Errorf("coverage: mapped surface directory %q not found under module root %q (surface removed or moved — update surfaceDirs and the coverage matrix)", s.dir, root)
		}
		out = append(out, s.id)
	}
	sort.Strings(out)
	return out, nil
}

// emptyReader satisfies query.Reader AND analysis.Searcher with empty results, so
// analysis.NewDefaultService registers the search-dependent `concept` analyzer
// (it probes reader.(Searcher)) and the live analyzer set is complete and stable.
type emptyReader struct{}

func (emptyReader) GetNode(context.Context, model.NodeId) (model.Node, error) {
	return model.Node{}, graphstore.ErrNotFound
}
func (emptyReader) GetEdge(context.Context, model.EdgeId) (model.Edge, error) {
	return model.Edge{}, graphstore.ErrNotFound
}
func (emptyReader) Nodes(context.Context, graphstore.Query) ([]model.Node, error) {
	return nil, nil
}
func (emptyReader) Edges(context.Context, graphstore.Query) ([]model.Edge, error) {
	return nil, nil
}
func (emptyReader) SearchNodes(context.Context, string, int) ([]graphstore.RankedNode, error) {
	return nil, nil
}

// compile-time proofs that the stub satisfies both contracts.
var (
	_ query.Reader      = emptyReader{}
	_ analysis.Searcher = emptyReader{}
)

func sortedCopy(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	sort.Strings(out)
	return out
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// ModuleRoot resolves the module root directory (via `go env GOMOD`), exported
// so cmd/coverage can locate the checked-in matrix files from any working
// directory under the module.
func ModuleRoot() (string, error) { return moduleRoot() }

// moduleRoot resolves the module root directory once, via `go env GOMOD`,
// mirroring internal/layerguard.moduleRoot so the surface scan works from any
// working directory under the module.
var moduleRoot = sync.OnceValues(func() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", err
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == "/dev/null" {
		return "", fmt.Errorf("coverage: no go.mod found (GOMOD=%q)", gomod)
	}
	return filepath.Dir(gomod), nil
})
