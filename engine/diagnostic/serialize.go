package diagnostic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// Marshal is the single canonical serializer for diagnostics results, shared by
// every surface (CLI, MCP, HTTP, daemon via SW-094). It is the ONLY place a
// Result becomes bytes, so all surfaces emit byte-identical output for identical
// inputs by construction.
//
// Byte-stability: Diagnostics and Unavailable are re-sorted defensively by the
// canonical comparators; field order is fixed by struct tag declaration order;
// HTML escaping is disabled; and the encoder's trailing newline is trimmed. Two
// Marshal calls on equal Results therefore produce identical bytes across
// repeated runs, across surfaces, and across full-vs-incremental indexes.
func Marshal(r Result) ([]byte, error) {
	diags := make([]Diagnostic, len(r.Diagnostics))
	copy(diags, r.Diagnostics)
	sortDiagnostics(diags)

	unavailable := make([]string, len(r.Unavailable))
	copy(unavailable, r.Unavailable)
	sortStrings(unavailable)

	out := Result{
		Outcome:     r.Outcome,
		Diagnostics: diags,
		Unavailable: unavailable,
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, fmt.Errorf("diagnostic: marshal result: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// sortDiagnostics applies the single canonical diagnostic comparator: a fully
// stable lexicographic cascade over file, line, column, severity rank, code,
// symbol id, and finally message. The content-addressed symbol id plus message
// make the order a total order, so output is byte-stable regardless of
// map-iteration or discovery order.
func sortDiagnostics(diags []Diagnostic) {
	sort.Slice(diags, func(i, j int) bool {
		a, b := diags[i], diags[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		if ra, rb := severityRank(a.Severity), severityRank(b.Severity); ra != rb {
			return ra < rb
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		if a.Symbol != b.Symbol {
			return a.Symbol < b.Symbol
		}
		return a.Message < b.Message
	})
}

func sortStrings(s []string) { sort.Strings(s) }
