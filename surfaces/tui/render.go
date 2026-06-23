package tui

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The render structs and renderQuery logic are lifted verbatim from the prior
// stdlib REPL scaffold: they are the proven, parity-preserving rendering of the
// canonical engine/query.Result shape. The TUI never re-derives fields — it
// reads the adapter's envelope-inner payload bytes (byte-identical to
// CLI/MCP/HTTP) and renders them.

// queryResult is the canonical engine/query.Result shape, re-declared here only
// for rendering.
type queryResult struct {
	Operation string     `json:"operation"`
	Symbol    string     `json:"symbol"`
	Outcome   string     `json:"outcome"`
	Depth     *int       `json:"depth,omitempty"`
	Nodes     []nodeView `json:"nodes"`
	Edges     []edgeView `json:"edges"`
}

type nodeView struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	QualifiedName string `json:"qualified_name"`
	SourcePath    string `json:"source_path"`
	Line          int    `json:"line"`
}

type edgeView struct {
	ID         string   `json:"id"`
	From       string   `json:"from"`
	To         string   `json:"to"`
	Kind       string   `json:"kind"`
	Tier       string   `json:"confidence_tier"`
	Confidence float64  `json:"confidence"`
	Reason     string   `json:"reason"`
	Evidence   []string `json:"evidence"`
}

// renderQueryString pretty-prints the canonical Result, surfacing per-edge
// provenance inline (tier/confidence/reason/evidence) — the same rendering the
// REPL used, so the content is parity-identical across surfaces.
func renderQueryString(raw []byte) string {
	var r queryResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return indentJSON(raw) // fall back to raw JSON if the shape is unexpected
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s on %q → %s (%d nodes, %d edges)\n",
		r.Operation, r.Symbol, r.Outcome, len(r.Nodes), len(r.Edges))
	for _, n := range r.Nodes {
		fmt.Fprintf(&b, "  node %s [%s] %s  (%s:%d)\n",
			n.ID, n.Kind, n.QualifiedName, n.SourcePath, n.Line)
	}
	for _, e := range r.Edges {
		fmt.Fprintf(&b, "  edge %s --%s--> %s  [tier=%s conf=%.2f", e.From, e.Kind, e.To, e.Tier, e.Confidence)
		if e.Reason != "" {
			fmt.Fprintf(&b, " reason=%q", e.Reason)
		}
		b.WriteString("]")
		if len(e.Evidence) > 0 {
			fmt.Fprintf(&b, " evidence=%v", e.Evidence)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// parseQuery decodes the canonical Result for the provenance pane; ok=false when
// the payload is not a query result (search/analyze use the raw provenance).
func parseQuery(raw []byte) (queryResult, bool) {
	var r queryResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return queryResult{}, false
	}
	if r.Operation == "" && len(r.Nodes) == 0 && len(r.Edges) == 0 {
		return queryResult{}, false
	}
	return r, true
}

// indentJSON re-emits JSON as 2-space-indented text for readable rendering.
func indentJSON(raw []byte) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(raw)
	}
	return string(out)
}
