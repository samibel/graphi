package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/samibel/graphi/surfaces/client"
)

// Engine is the read-only capability surface the TUI consumes. It is the
// client.Client contract (so the render path is transport-agnostic and parity is
// preserved) extended with the SW-044 /contract and /events affordances the
// navigator and freshness stream need. The HTTP/SSE adapter (*client.HTTP)
// satisfies it; tests provide a fake.
type Engine interface {
	client.Client
	// Contract returns the envelope-inner /contract capability bytes.
	Contract(ctx context.Context) ([]byte, error)
	// SubscribeEvents opens the freshness SSE stream.
	SubscribeEvents(ctx context.Context) (<-chan client.Event, error)
	// SchemaVersion is the envelope contract version the adapter expects (R5).
	SchemaVersion() int
}

// --- tea.Msg types --------------------------------------------------------

// contractMsg carries the startup /contract result (or error) for the navigator.
type contractMsg struct {
	raw []byte
	err error
}

// answerMsg carries the result of an async unary query/search/analyze. kind
// labels the operation; provenance is the raw payload retained for the pane.
type answerMsg struct {
	kind  string // "query" | "search" | "analyze"
	label string // human label shown as the completion marker
	raw   []byte
	err   error
}

// eventMsg carries one freshness SSE frame. closed=true signals the stream
// ended (the navigator drops to a degraded/stale state).
type eventMsg struct {
	ev     client.Event
	closed bool
	err    error
}

// --- tea.Cmd factories ----------------------------------------------------

// fetchContractCmd loads /contract off the UI thread (AC-1 navigator population).
func fetchContractCmd(ctx context.Context, eng Engine) tea.Cmd {
	return func() tea.Msg {
		raw, err := eng.Contract(ctx)
		return contractMsg{raw: raw, err: err}
	}
}

// runQueryCmd performs a structural query off the UI thread (AC-3a: navigation
// never blocks). The result is delivered as an answerMsg.
func runQueryCmd(ctx context.Context, eng Engine, op, symbol string, depth int) tea.Cmd {
	return func() tea.Msg {
		raw, err := eng.Query(ctx, op, symbol, depth)
		return answerMsg{kind: "query", label: op + " " + symbol, raw: raw, err: err}
	}
}

// runSearchCmd performs a lexical search off the UI thread.
func runSearchCmd(ctx context.Context, eng Engine, q string) tea.Cmd {
	return func() tea.Msg {
		raw, err := eng.Search(ctx, q, 20)
		return answerMsg{kind: "search", label: "search " + q, raw: raw, err: err}
	}
}

// runAnalyzeCmd performs an analyzer call off the UI thread.
func runAnalyzeCmd(ctx context.Context, eng Engine, name, symbol, direction string) tea.Cmd {
	return func() tea.Msg {
		raw, err := eng.Analyze(ctx, client.AnalyzeParams{Name: name, Symbol: symbol, Direction: direction})
		return answerMsg{kind: "analyze", label: name + " " + symbol, raw: raw, err: err}
	}
}

// subscribeEventsCmd opens the freshness SSE stream and returns the first frame
// as an eventMsg. waitEventCmd continues pulling from the same channel, so the
// stream drives a continuous, non-blocking flow of tea.Msgs (AC-3b).
func subscribeEventsCmd(ctx context.Context, eng Engine) tea.Cmd {
	return func() tea.Msg {
		ch, err := eng.SubscribeEvents(ctx)
		if err != nil {
			return eventMsg{err: err}
		}
		// Stash the channel by reading the first frame; the Update loop schedules
		// the next read via waitEventCmd bound to the same channel.
		ev, ok := <-ch
		if !ok {
			return eventMsg{closed: true}
		}
		return eventStreamMsg{ch: ch, first: eventMsg{ev: ev}}
	}
}

// eventStreamMsg hands the live event channel to the model so it can keep
// pulling frames without re-subscribing.
type eventStreamMsg struct {
	ch    <-chan client.Event
	first eventMsg
}

// waitEventCmd pulls the next frame from an already-open event channel.
func waitEventCmd(ch <-chan client.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return eventMsg{closed: true}
		}
		return eventMsg{ev: ev}
	}
}
