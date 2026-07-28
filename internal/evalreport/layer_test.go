package evalreport

// SW-128 (P0-C5) AC-6 / NFR-7: measurement code is not product code.
//
// The raw format, the environment record and the aggregator live under
// `internal/evalreport` and `cmd/eval`, and they must not reach into `engine/`
// or `core/`. The reason is not tidiness: a scorer that imported the engine
// could measure the engine's internals rather than its shipped behaviour, and a
// change made to help a measurement would become a change to the product.
//
// `internal/layerguard` does not cover this — it ranks core/engine/surfaces/cmd
// and treats `internal/*` as unranked tooling, which is right for the layer
// DIRECTION rule and says nothing about this one. So the constraint is asserted
// here, over the source itself.
//
// SCOPE, stated rather than quietly chosen. The guard covers the files this
// story owns, not the whole package. `report.go` — the pre-existing EP-019
// scorecard reporting — imports `engine/scorecard` for the scorecard value
// type, and has since long before P0. That dependency is real and is NOT
// defended here; untangling it is a refactor of another story's artifact
// format, outside SW-128's touched areas. What this test guarantees is that the
// P0 measurement path stays clean, and that the existing exception cannot
// silently spread into it.

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// p0MeasurementFiles are the files that make up the P0 raw-export and
// aggregation path. A new file in this path belongs on the list.
var p0MeasurementFiles = []string{
	"aggregate.go",
	"coldrun.go",
	"environment.go",
	"freshness.go",
	"querylatency.go",
	"rawexport.go",
	"stalls.go",
}

func TestP0MeasurementPath_DoesNotImportProductCode(t *testing.T) {
	const module = "github.com/samibel/graphi/"
	forbidden := []string{"engine/", "core/", "surfaces/"}

	fset := token.NewFileSet()
	for _, name := range p0MeasurementFiles {
		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v (if the file was renamed, update p0MeasurementFiles — do not delete the entry)", name, err)
		}
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s: unquote import %s: %v", name, spec.Path.Value, err)
			}
			rest, isLocal := strings.CutPrefix(path, module)
			if !isLocal {
				continue
			}
			for _, prefix := range forbidden {
				if strings.HasPrefix(rest, prefix) {
					t.Errorf("%s imports %s: the P0 measurement path must not depend on product code (NFR-7, SW-128 AC-6)", name, path)
				}
			}
		}
	}
}

// The aggregator must not have a second home. AC-6 is about WHERE the code
// lives as much as what it imports: a copy of the recomputation under engine/
// or core/ would be product code that measures itself, and the two would drift.
func TestP0MeasurementPath_HasNoCopyUnderProductCode(t *testing.T) {
	for _, dir := range []string{"../../engine", "../../core", "../../surfaces"} {
		matches, err := filepath.Glob(filepath.Join(dir, "*", "rawexport.go"))
		if err != nil {
			t.Fatalf("glob %s: %v", dir, err)
		}
		aggregates, err := filepath.Glob(filepath.Join(dir, "*", "evalreport*.go"))
		if err != nil {
			t.Fatalf("glob %s: %v", dir, err)
		}
		if found := append(matches, aggregates...); len(found) > 0 {
			t.Errorf("measurement code found under product code: %v (NFR-7, SW-128 AC-6)", found)
		}
	}
}
