// Package tui is graphi's interactive terminal surface over the shared Engine.
//
// SW-047 re-platforms the original stdlib REPL onto a keyboard-driven, multi-pane
// Bubble Tea shell that consumes the Engine over the SW-044 HTTP/SSE surface (no
// in-process daemon embedding). It holds NO query/search/analysis logic: it
// drives the same surfaces/client.Client seam (here over HTTP) used by the
// CLI/MCP/HTTP surfaces, so answers and provenance are byte-identical across
// surfaces (parity by construction — the adapter feeds the envelope-inner
// payload bytes verbatim into the proven render structs).
//
// Layout (AC-1/AC-4): a left navigator (capabilities from /contract + recent
// queries), a center main-content pane (query/search/analyze answers), and a
// persistent provenance/detail pane bound to the current selection. A header
// shows engine/schema version + connection status; a footer shows read-only key
// hints.
//
// Async (AC-3): every query/search/analyze runs as a tea.Cmd off the UI thread
// (spinner + completion marker, navigation never blocks); the freshness SSE
// (/events) is a long-lived tea.Cmd loop that keeps the navigator live and
// drives the degraded banner — SW-044's /events is a freshness firehose, NOT a
// per-operation answer stream, so this is the correct, in-scope realisation.
//
// Read-only + local-first (AC-6): no binding maps to a mutating op; the adapter
// issues only GET and is loopback-only. Errors render as calm status banners
// (AC-5); the model NEVER panics — typed errors only.
package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/samibel/graphi/surfaces/client"
)

// pane identifies the focused pane for keyboard navigation.
type pane int

const (
	paneNav pane = iota
	paneMain
	paneProv
	paneCount
)

// capItem is one /contract-derived capability in the navigator list.
type capItem struct{ name string }

func (c capItem) Title() string       { return c.name }
func (c capItem) Description() string { return "" }
func (c capItem) FilterValue() string { return c.name }

// Model is the Elm-style TUI state (tea.Model). All data comes from the Engine;
// the model holds presentation/connection/in-flight/error state only.
type Model struct {
	eng Engine
	ctx context.Context

	keys   keyMap
	focus  pane
	width  int
	height int
	ready  bool // first WindowSizeMsg received (panes sized)

	nav  list.Model
	main viewport.Model
	prov viewport.Model
	sp   spinner.Model

	// connection / contract state
	schemaVersion int
	connected     bool
	resources     []string

	// in-flight async state (AC-3)
	inFlight     bool
	lastComplete string

	// content state
	focusSymbol string // current symbol for structural queries
	mainContent string
	provContent string

	// degraded / error banner (AC-5)
	banner string

	// eventCh holds the live SSE channel between waitEventCmd hops.
	eventCh <-chan client.Event

	quitting bool
}

// New constructs a TUI model over the given Engine (the HTTP/SSE adapter).
func New(eng Engine) *Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	nav := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	nav.Title = "Capabilities"
	nav.SetShowStatusBar(false)
	nav.SetFilteringEnabled(false)
	nav.SetShowHelp(false)

	return &Model{
		eng:           eng,
		ctx:           context.Background(),
		keys:          defaultKeyMap(),
		focus:         paneNav,
		nav:           nav,
		main:          viewport.New(0, 0),
		prov:          viewport.New(0, 0),
		sp:            sp,
		schemaVersion: eng.SchemaVersion(),
		provContent:   "(no answer selected)",
		mainContent:   "Connecting to engine…",
	}
}

// WithContext binds a cancellable context to the model's commands (used by Run
// and by tests for hermetic teardown).
func (m *Model) WithContext(ctx context.Context) *Model { m.ctx = ctx; return m }

// Init kicks off the startup commands: spinner tick, /contract fetch (navigator
// population), and the freshness SSE subscription.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		m.sp.Tick,
		fetchContractCmd(m.ctx, m.eng),
		subscribeEventsCmd(m.ctx, m.eng),
	)
}

// Update is the central event loop. It is exhaustive and panic-free: every
// branch returns a model + cmd; typed errors become banners, never crashes.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		m.ready = true

	case tea.KeyMsg:
		if cmd := m.handleKey(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if m.quitting {
			return m, tea.Quit
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.sp, cmd = m.sp.Update(msg)
		cmds = append(cmds, cmd)

	case contractMsg:
		m.handleContract(msg)

	case answerMsg:
		m.handleAnswer(msg)

	case eventStreamMsg:
		// Live SSE channel established: ingest the first frame and schedule the
		// next read so the stream flows without re-subscribing (AC-3b).
		m.applyEvent(msg.first)
		cmds = append(cmds, waitEventCmd(msg.ch))
		m.eventCh = msg.ch

	case eventMsg:
		m.applyEvent(msg)
		if !msg.closed && msg.err == nil && m.eventCh != nil {
			cmds = append(cmds, waitEventCmd(m.eventCh))
		}
	}

	// Forward to the focused pane so list/viewport keys (scroll/select) work.
	cmds = append(cmds, m.updateFocusedPane(msg))
	return m, tea.Batch(cmds...)
}

// handleKey processes navigation + read-only action keys. It returns a tea.Cmd
// for async work (answers), or nil. No key triggers a mutating op (AC-6).
func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch {
	case key.Matches(msg, m.keys.Quit):
		m.quitting = true
		return nil
	case key.Matches(msg, m.keys.NextPane):
		m.focus = (m.focus + 1) % paneCount
		return nil
	case key.Matches(msg, m.keys.PrevPane):
		m.focus = (m.focus + paneCount - 1) % paneCount
		return nil
	case key.Matches(msg, m.keys.Retry):
		// Re-attempt connection + navigator population; clears the banner on success.
		m.banner = ""
		return tea.Batch(fetchContractCmd(m.ctx, m.eng), subscribeEventsCmd(m.ctx, m.eng))
	case key.Matches(msg, m.keys.Select):
		return m.runSelected()
	}
	return nil
}

// runSelected runs the read-only operation for the currently selected navigator
// capability against the focused symbol. Always async (AC-3a).
func (m *Model) runSelected() tea.Cmd {
	it, ok := m.nav.SelectedItem().(capItem)
	if !ok {
		return nil
	}
	name := it.name
	sym := m.focusSymbol
	if sym == "" {
		sym = "pkg.Example" // placeholder symbol until the user selects one
	}
	m.inFlight = true
	switch {
	case strings.HasPrefix(name, "query/"):
		op := strings.TrimPrefix(name, "query/")
		return runQueryCmd(m.ctx, m.eng, op, sym, 1)
	case name == "search":
		return runSearchCmd(m.ctx, m.eng, sym)
	case strings.HasPrefix(name, "analyze/"):
		an := strings.TrimPrefix(name, "analyze/")
		return runAnalyzeCmd(m.ctx, m.eng, an, sym, "reverse")
	}
	m.inFlight = false
	return nil
}

// handleContract populates the navigator from the /contract document (AC-1) or
// renders the degraded banner on failure (AC-5).
func (m *Model) handleContract(msg contractMsg) {
	if msg.err != nil {
		m.connected = false
		m.banner = bannerFor(msg.err)
		m.mainContent = "Engine unreachable. Press r to retry, q to quit."
		m.main.SetContent(m.mainContent)
		return
	}
	m.connected = true
	m.banner = ""
	var doc struct {
		SchemaVersion int      `json:"schema_version"`
		Resources     []string `json:"resources"`
		Streams       []string `json:"streams"`
	}
	_ = json.Unmarshal(msg.raw, &doc)
	if doc.SchemaVersion != 0 {
		m.schemaVersion = doc.SchemaVersion
	}
	m.resources = doc.Resources
	items := make([]list.Item, 0, len(doc.Resources))
	for _, r := range doc.Resources {
		items = append(items, capItem{name: r})
	}
	m.nav.SetItems(items)
	m.mainContent = "Connected. Select a capability (Enter) to run a read-only query."
	m.main.SetContent(m.mainContent)
}

// handleAnswer renders an async unary result + provenance, clears the in-flight
// indicator, and sets the completion marker (AC-3a / AC-4). Typed errors map to
// distinct banners (AC-5 / R5).
func (m *Model) handleAnswer(msg answerMsg) {
	m.inFlight = false
	if msg.err != nil {
		m.banner = bannerFor(msg.err)
		m.lastComplete = "✗ " + msg.label + " — " + msg.err.Error()
		return
	}
	m.banner = ""
	m.lastComplete = "✓ " + msg.label
	switch msg.kind {
	case "query":
		m.mainContent = renderQueryString(msg.raw)
	default:
		m.mainContent = indentJSON(msg.raw)
	}
	m.main.SetContent(m.mainContent)
	m.setProvenance(msg)
}

// setProvenance binds the persistent provenance pane to the current answer
// (AC-4): engine/schema version from the adapter + per-edge tier/confidence/
// reason/evidence from the payload. The pane is never empty while an answer is
// displayed.
func (m *Model) setProvenance(msg answerMsg) {
	var b strings.Builder
	fmt.Fprintf(&b, "engine schema_version: %d\n", m.schemaVersion)
	fmt.Fprintf(&b, "operation: %s\n", msg.label)
	if r, ok := parseQuery(msg.raw); ok {
		fmt.Fprintf(&b, "outcome: %s (%d nodes, %d edges)\n\n", r.Outcome, len(r.Nodes), len(r.Edges))
		for _, e := range r.Edges {
			fmt.Fprintf(&b, "edge %s --%s--> %s\n", e.From, e.Kind, e.To)
			fmt.Fprintf(&b, "  tier=%s confidence=%.2f\n", e.Tier, e.Confidence)
			if e.Reason != "" {
				fmt.Fprintf(&b, "  reason: %s\n", e.Reason)
			}
			if len(e.Evidence) > 0 {
				fmt.Fprintf(&b, "  evidence: %v\n", e.Evidence)
			}
		}
	} else {
		b.WriteString("\npayload provenance:\n")
		b.WriteString(indentJSON(msg.raw))
	}
	m.provContent = b.String()
	m.prov.SetContent(m.provContent)
}

// applyEvent folds a freshness SSE frame into the navigator/banner (AC-3b).
func (m *Model) applyEvent(msg eventMsg) {
	if msg.err != nil {
		m.connected = false
		m.banner = bannerFor(msg.err)
		return
	}
	if msg.closed {
		m.connected = false
		m.banner = "⚠ Engine stream ended — r to retry, q to quit"
		return
	}
	switch msg.ev.Type {
	case "ready":
		m.connected = true
	case "ingest-completed":
		m.connected = true
		m.banner = "" // graph updated; keep navigator live
		m.lastComplete = "↻ graph updated"
	case "bye":
		m.connected = false
		m.banner = "⚠ Engine stream ended — r to retry, q to quit"
	case "error":
		m.banner = "⚠ Engine stream error — r to retry"
	}
}

// updateFocusedPane forwards a msg to the currently focused bubbles component so
// scrolling/selection work; non-focused panes are inert.
func (m *Model) updateFocusedPane(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch m.focus {
	case paneNav:
		m.nav, cmd = m.nav.Update(msg)
	case paneMain:
		m.main, cmd = m.main.Update(msg)
	case paneProv:
		m.prov, cmd = m.prov.Update(msg)
	}
	return cmd
}

// bannerFor maps a typed adapter error to a calm, distinct status banner (AC-5 /
// R5). Schema mismatch gets its own banner so the user is not shown
// possibly-misinterpreted bytes.
func bannerFor(err error) string {
	switch {
	case errors.Is(err, client.ErrSchemaMismatch):
		return "⚠ contract version mismatch — r to retry, q to quit"
	case errors.Is(err, client.ErrUnreachable):
		return "⚠ Engine unreachable — r to retry, q to quit"
	case errors.Is(err, client.ErrUnavailable):
		return "⚠ capability unavailable — r to retry"
	case errors.Is(err, client.ErrBadInput):
		return "⚠ bad request"
	default:
		return "⚠ engine error — r to retry"
	}
}
