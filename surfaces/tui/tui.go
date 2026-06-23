// Package tui is graphi's interactive terminal surface over the shared engine.
// It is stdlib-only (no terminal framework dependency, preserving the CGo-free,
// static, minimal-dependency build) and holds NO query/search/analysis logic: it
// drives the same surfaces/client.Client seam as the CLI/MCP/HTTP surfaces, so
// answers and provenance are byte-identical across surfaces (parity by
// construction).
//
// The interaction model is a read/eval/print loop: the user issues structured
// commands (select/neighbors/blast/search/provenance/help/quit); the TUI calls
// client.Client and renders structured node/edge views including per-edge
// provenance (confidence_tier/confidence/reason/evidence). Engine errors are
// caught and rendered without ever crashing the loop. Zero outbound network: it
// calls an in-process client only.
package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/samibel/graphi/surfaces/client"
)

// Model holds the TUI session state. It is deliberately small: the client owns
// all data; the model only tracks the currently focused symbol.
type Model struct {
	client client.Client
	focus  string // currently selected symbol (node id); empty = none
}

// New constructs a TUI model over the given client.
func New(c client.Client) *Model { return &Model{client: c} }

// queryResult is the canonical engine/query.Result shape, re-declared here only
// for rendering (the TUI never re-derives fields — it reads the client's bytes).
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

// Run executes the interactive loop until EOF, "quit", or ctx cancellation.
// Engine errors are rendered to out but never terminate the loop.
func (m *Model) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	fmt.Fprintln(out, "graphi tui — type 'help' for commands, 'quit' to exit.")
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if m.focus == "" {
			fmt.Fprintf(out, "\n(no symbol selected) > ")
		} else {
			fmt.Fprintf(out, "\n[%s] > ", m.focus)
		}
		if !sc.Scan() {
			if err := sc.Err(); err != nil {
				return err
			}
			fmt.Fprintln(out) // EOF — clean exit
			return nil
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		done, err := m.dispatch(ctx, line, out)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

// dispatch executes one command. It returns done=true only for "quit". Any
// Engine error is rendered to out and the loop continues.
func (m *Model) dispatch(ctx context.Context, line string, out io.Writer) (bool, error) {
	parts := splitCmd(line)
	if len(parts) == 0 {
		return false, nil
	}
	cmd, args := parts[0], parts[1:]
	switch cmd {
	case "quit", "q", "exit":
		fmt.Fprintln(out, "bye.")
		return true, nil
	case "help", "h", "?":
		m.renderHelp(out)
	case "select":
		if len(args) < 1 {
			fmt.Fprintln(out, "usage: select <symbol-id>")
			return false, nil
		}
		m.focus = args[0]
		fmt.Fprintf(out, "selected: %s\n", m.focus)
	case "neighbors", "neigh":
		depth := 1
		if len(args) >= 1 {
			fmt.Sscanf(args[0], "%d", &depth)
		}
		m.runQuery(ctx, "neighborhood", depth, out)
	case "callers":
		m.runQuery(ctx, "callers", 0, out)
	case "callees":
		m.runQuery(ctx, "callees", 0, out)
	case "references":
		m.runQuery(ctx, "references", 0, out)
	case "definition":
		m.runQuery(ctx, "definition", 0, out)
	case "blast", "impact":
		m.runAnalyze(ctx, out)
	case "search":
		if len(args) < 1 {
			fmt.Fprintln(out, "usage: search <query>")
			return false, nil
		}
		m.runSearch(ctx, strings.Join(args, " "), out)
	case "provenance", "prov":
		m.renderProvenanceHelp(out)
	default:
		fmt.Fprintf(out, "unknown command %q (try 'help')\n", cmd)
	}
	return false, nil
}

// runQuery runs a structural query on the focused symbol and renders the result.
func (m *Model) runQuery(ctx context.Context, op string, depth int, out io.Writer) {
	if m.focus == "" {
		fmt.Fprintln(out, "select a symbol first ('select <id>').")
		return
	}
	raw, err := m.client.Query(ctx, op, m.focus, depth)
	if err != nil {
		fmt.Fprintf(out, "engine error: %v\n", err)
		return
	}
	m.renderQuery(raw, out)
}

// runAnalyze runs the impact (blast-radius) analyzer on the focused symbol.
func (m *Model) runAnalyze(ctx context.Context, out io.Writer) {
	if m.focus == "" {
		fmt.Fprintln(out, "select a symbol first ('select <id>').")
		return
	}
	raw, err := m.client.Analyze(ctx, client.AnalyzeParams{Name: "impact", Symbol: m.focus, Direction: "reverse"})
	if err != nil {
		fmt.Fprintf(out, "engine error: %v\n", err)
		return
	}
	fmt.Fprintln(out, "blast-radius (impact, reverse):")
	fmt.Fprintln(out, indentJSON(raw))
}

// runSearch runs a lexical search and renders matches with citations.
func (m *Model) runSearch(ctx context.Context, q string, out io.Writer) {
	raw, err := m.client.Search(ctx, q, 20)
	if err != nil {
		fmt.Fprintf(out, "engine error: %v\n", err)
		return
	}
	fmt.Fprintln(out, "search results:")
	fmt.Fprintln(out, indentJSON(raw))
}

// renderQuery pretty-prints the canonical Result, surfacing provenance on edges.
func (m *Model) renderQuery(raw []byte, out io.Writer) {
	var r queryResult
	if err := json.Unmarshal(raw, &r); err != nil {
		// fall back to raw JSON if the shape is unexpected
		fmt.Fprintln(out, indentJSON(raw))
		return
	}
	fmt.Fprintf(out, "%s on %q → %s (%d nodes, %d edges)\n",
		r.Operation, r.Symbol, r.Outcome, len(r.Nodes), len(r.Edges))
	for _, n := range r.Nodes {
		fmt.Fprintf(out, "  node %s [%s] %s  (%s:%d)\n",
			n.ID, n.Kind, n.QualifiedName, n.SourcePath, n.Line)
	}
	for _, e := range r.Edges {
		fmt.Fprintf(out, "  edge %s --%s--> %s  [tier=%s conf=%.2f", e.From, e.Kind, e.To, e.Tier, e.Confidence)
		if e.Reason != "" {
			fmt.Fprintf(out, " reason=%q", e.Reason)
		}
		fmt.Fprintf(out, "]")
		if len(e.Evidence) > 0 {
			fmt.Fprintf(out, " evidence=%v", e.Evidence)
		}
		fmt.Fprintln(out)
	}
}

func (m *Model) renderHelp(out io.Writer) {
	fmt.Fprintln(out, `commands:
  select <id>          focus a symbol
  neighbors [N]        neighborhood (N hops, default 1) of the focused symbol
  callers|callees|references|definition   directed lookups
  blast                blast-radius / impact (reverse) of the focused symbol
  search <query>       lexical/symbol search
  provenance           explain the provenance fields shown on edges
  help                 this help
  quit                 exit`)
}

func (m *Model) renderProvenanceHelp(out io.Writer) {
	fmt.Fprintln(out, `provenance (per edge):
  confidence_tier   symbolic confidence tier (e.g. confirmed|inferred|heuristic)
  confidence        numeric confidence in [0,1]
  reason            short human-readable derivation note
  evidence          supporting evidence refs (file:line / ids)
The TUI shows exactly what the engine emits for CLI/MCP/HTTP (byte-identical).`)
}

// splitCmd splits a command line on whitespace (no shell semantics — read-only,
// no eval). Quotes are not interpreted; the surface is command-parsed only.
func splitCmd(line string) []string {
	return strings.Fields(line)
}

// indentJSON re-emits JSON as 2-space-indented text for readable rendering.
func indentJSON(raw []byte) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(raw)
	}
	return string(b)
}
