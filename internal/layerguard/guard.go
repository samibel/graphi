// Package layerguard mechanically enforces graphi's dependency direction
// `cmd → surfaces → engine → core` (story SW-013). It parses the import graph of
// the ranked packages via `go list -json`, classifies each into its layer, and
// fails on any upward/sideways edge (a lower layer importing a higher layer). On
// success it reports the verified allowed-edge set. The rule is declared once
// here as the single authoritative source.
//
// Layers and their ranks (higher may import lower):
//
//	cmd       4   (top; wires everything)
//	surfaces  3
//	engine    2
//	core      1   (deepest; pure leaves)
//
// Stdlib, external, and non-ranked internal packages (e.g. internal/*, bench/*)
// return rank 0 and are NOT constrained by this rule — they are tooling outside
// the cmd→surfaces→engine→core chain.
package layerguard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// ModulePath is the single module path the layers are rooted under.
const ModulePath = "github.com/samibel/graphi"

// Layer ranks.
const (
	LayerCore     = 1
	LayerEngine   = 2
	LayerSurfaces = 3
	LayerCmd      = 4
)

// LayerOf returns the layer rank for a package path and whether it is a ranked
// (guarded) package.
func LayerOf(pkg string) (int, bool) {
	switch {
	case strings.HasPrefix(pkg, ModulePath+"/cmd/"):
		return LayerCmd, true
	case strings.HasPrefix(pkg, ModulePath+"/surfaces/"):
		return LayerSurfaces, true
	case strings.HasPrefix(pkg, ModulePath+"/engine/"):
		return LayerEngine, true
	case strings.HasPrefix(pkg, ModulePath+"/core/"):
		return LayerCore, true
	}
	return 0, false
}

// LayerName returns the human-readable layer name for a rank.
func LayerName(rank int) string {
	switch rank {
	case LayerCmd:
		return "cmd"
	case LayerSurfaces:
		return "surfaces"
	case LayerEngine:
		return "engine"
	case LayerCore:
		return "core"
	}
	return "unranked"
}

// Violation is one upward/sideways import edge.
type Violation struct {
	Importer       string
	Imported       string
	ImporterLayer  int
	ImportedLayer  int
}

// Error makes a Violation descriptive.
func (v Violation) String() string {
	return fmt.Sprintf("%s (%s) imports %s (%s) — upward edge violates cmd→surfaces→engine→core",
		v.Importer, LayerName(v.ImporterLayer), v.Imported, LayerName(v.ImportedLayer))
}

// Report is the layer-guard outcome.
type Report struct {
	Pass        bool
	Violations  []Violation
	AllowedEdges []string // verified allowed edges observed, e.g. "cmd→surfaces"
}

// Check scans the ranked packages and returns violations plus the verified
// allowed-edge set. A non-empty Violations list means CI must fail.
func Check(ctx context.Context) (Report, error) {
	dir, err := moduleRoot()
	if err != nil {
		return Report{}, err
	}
	cmd := exec.CommandContext(ctx, "go", "list", "-json",
		"./cmd/...", "./surfaces/...", "./engine/...", "./core/...")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Report{}, err
	}
	if err := cmd.Start(); err != nil {
		return Report{}, fmt.Errorf("go list: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	dec := json.NewDecoder(stdout)
	var violations []Violation
	allowed := map[string]bool{}
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
			return Report{}, fmt.Errorf("decode go list json: %w", err)
		}
		importerRank, ok := LayerOf(p.ImportPath)
		if !ok {
			continue // not a ranked package
		}
		for _, imp := range p.Imports {
			importedRank, ranked := LayerOf(imp)
			if !ranked {
				continue // unranked import is unconstrained
			}
			edge := fmt.Sprintf("%s→%s", LayerName(importerRank), LayerName(importedRank))
			if importedRank > importerRank {
				violations = append(violations, Violation{
					Importer: p.ImportPath, Imported: imp,
					ImporterLayer: importerRank, ImportedLayer: importedRank,
				})
				continue
			}
			allowed[edge] = true
		}
	}
	if err := cmd.Wait(); err != nil {
		return Report{}, fmt.Errorf("go list failed: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	edges := make([]string, 0, len(allowed))
	for e := range allowed {
		edges = append(edges, e)
	}
	sortStrings(edges)
	return Report{Pass: len(violations) == 0, Violations: violations, AllowedEdges: edges}, nil
}

// FormatReport renders a human-readable report.
func (r Report) Format() string {
	if !r.Pass {
		var b strings.Builder
		fmt.Fprintf(&b, "layer-direction check FAILED — %d violation(s):\n", len(r.Violations))
		for _, v := range r.Violations {
			fmt.Fprintf(&b, "  - %s\n", v)
		}
		return b.String()
	}
	return fmt.Sprintf("layer-direction check PASS — verified allowed edges: [%s]\n", strings.Join(r.AllowedEdges, ", "))
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

var moduleRoot = sync.OnceValues(func() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", err
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == "/dev/null" {
		return "", fmt.Errorf("no go.mod found (GOMOD=%q)", gomod)
	}
	return filepath.Dir(gomod), nil
})
