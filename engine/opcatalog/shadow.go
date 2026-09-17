package opcatalog

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
// The checked-in shadow.json remains the review surface. The production binary
// embeds its deterministic gzip representation so the catalog does not spend
// tens of kilobytes on JSON indentation and repeated schema keys.
//
//go:embed shadow.json.gz
var compressedShadowJSON []byte

const (
	shadowJSONSHA256   = "b8b07c393721597f896456e8f9a9d945db9c7a51ab330cbb520ed876afe6ff77"
	shadowGzipSHA256   = "6455dcb5b5f7eb6eb7b45bcded21e2a5daea2b039f57a10059a707edef5e8cdc"
	maxShadowJSONBytes = 1 << 20
)

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
	raw, err := decompressShadowJSON()
	if err != nil {
		return nil, err
	}
	var doc shadowDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
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

func decompressShadowJSON() ([]byte, error) {
	compressedDigest := sha256.Sum256(compressedShadowJSON)
	if got := hex.EncodeToString(compressedDigest[:]); got != shadowGzipSHA256 {
		return nil, fmt.Errorf("opcatalog: compressed shadow.json SHA-256 mismatch: got %s, want %s", got, shadowGzipSHA256)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressedShadowJSON))
	if err != nil {
		return nil, fmt.Errorf("opcatalog: open compressed shadow.json: %w", err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(reader, maxShadowJSONBytes+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, fmt.Errorf("opcatalog: decompress shadow.json: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("opcatalog: close compressed shadow.json: %w", closeErr)
	}
	if len(raw) > maxShadowJSONBytes {
		return nil, fmt.Errorf("opcatalog: shadow.json exceeds %d bytes", maxShadowJSONBytes)
	}
	rawDigest := sha256.Sum256(raw)
	if got := hex.EncodeToString(rawDigest[:]); got != shadowJSONSHA256 {
		return nil, fmt.Errorf("opcatalog: shadow.json SHA-256 mismatch: got %s, want %s", got, shadowJSONSHA256)
	}
	return raw, nil
}
