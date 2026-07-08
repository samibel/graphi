package intraproctaint

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/samibel/graphi/engine/analysis/taint"
)

// MetadataKey is the graphstore metadata key under which the ingest pipeline
// persists the intra-procedural taint findings for a full index. It is read back
// by the taint dispatch adapter so `graphi analyze taint` surfaces the flows.
// The graphstore Snapshot serializes only nodes/edges (never metadata), so
// persisting here is byte-parity-safe: it never perturbs the graph identity.
const MetadataKey = "analysis.intraproc_taint.v1"

// Encode serializes findings to canonical, byte-stable JSON. It sorts a copy
// first (source ordering is a pure function of file content), disables HTML
// escaping, and trims the trailing newline — the same canonical-ordering
// discipline the interproctaint artifact uses — so identical findings always
// encode to identical bytes regardless of the order they were discovered in.
func Encode(findings []taint.Finding) (string, error) {
	sorted := make([]taint.Finding, len(findings))
	copy(sorted, findings)
	sortFindings(sorted)
	if sorted == nil {
		sorted = []taint.Finding{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(sorted); err != nil {
		return "", fmt.Errorf("intraproctaint: encode findings: %w", err)
	}
	return string(bytes.TrimRight(buf.Bytes(), "\n")), nil
}

// Decode parses findings previously produced by Encode. An empty string decodes
// to no findings (the common "clean index" case).
func Decode(s string) ([]taint.Finding, error) {
	if s == "" {
		return nil, nil
	}
	var findings []taint.Finding
	if err := json.Unmarshal([]byte(s), &findings); err != nil {
		return nil, fmt.Errorf("intraproctaint: decode findings: %w", err)
	}
	return findings, nil
}

// Sort canonically orders findings in place (exported for callers that assemble
// findings from multiple files before persisting/asserting).
func Sort(findings []taint.Finding) { sortFindings(findings) }
