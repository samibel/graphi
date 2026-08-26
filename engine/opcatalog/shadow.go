package opcatalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

// shadowJSON is the AX-03 shadow population: one OperationSpec for every MCP
// operation surfaces/mcp.ToolNames() advertised at the AX-00 baseline.
//
// It is DATA rather than Go literals on purpose. The catalog's whole point is
// that an operation is described once, in a reviewable form, so a projection
// can be derived from it (AX-05); 56 hand-written Go composite literals holding
// JSON Schema maps would be the maintained-by-hand list this program exists to
// abolish, just relocated. Being an embedded file also keeps the descriptions
// and schemas diffable line by line on a PR, exactly like the AX-00 goldens
// they mirror.
//
// The initial contents were mirrored from those goldens
// (surfaces/mcp/testdata/mcp-descriptors-{maximal,stable}.json) plus a
// hand-audited port table. Bootstrapping a mirror from the thing it mirrors is
// unavoidable and is not the gate: the gate is
// surfaces/mcp/opcatalog_parity_test.go, which re-derives the comparison from
// the LIVE builders on every run, so any later drift in either direction breaks
// the build.
//
//go:embed shadow.json
var shadowJSON []byte

// shadowDocument is the on-disk shape of shadow.json.
type shadowDocument struct {
	SchemaVersion int             `json:"schema_version"`
	Note          string          `json:"note"`
	Operations    []OperationSpec `json:"operations"`
}

// ShadowSchemaVersion is the shape version of the embedded document. It is
// checked on load so a future reshaping cannot be read by an older decoder that
// would silently drop fields.
const ShadowSchemaVersion = 1

var shadow = sync.OnceValues(loadShadow)

// Shadow returns the frozen shadow catalog. The document is decoded, validated
// and frozen exactly once per process; every caller gets the same immutable
// catalog, and every accessor on it hands out copies.
//
// It returns an error rather than panicking, and no production code path calls
// it yet — AX-03 is shadow mode. A malformed embedded document is therefore a
// test failure, which is where it belongs.
func Shadow() (*Catalog, error) { return shadow() }

func loadShadow() (*Catalog, error) {
	var doc shadowDocument
	if err := json.Unmarshal(shadowJSON, &doc); err != nil {
		return nil, fmt.Errorf("opcatalog: decode shadow.json: %w", err)
	}
	if doc.SchemaVersion != ShadowSchemaVersion {
		return nil, fmt.Errorf("opcatalog: shadow.json schema_version %d, want %d",
			doc.SchemaVersion, ShadowSchemaVersion)
	}
	if len(doc.Operations) == 0 {
		return nil, fmt.Errorf("opcatalog: shadow.json declares no operations")
	}
	catalog := New()
	for _, spec := range doc.Operations {
		if err := catalog.Add(spec); err != nil {
			return nil, fmt.Errorf("opcatalog: shadow.json: %w", err)
		}
	}
	return catalog.Build()
}
