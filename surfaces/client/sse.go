// SSE subscription for the SW-044 freshness firehose (SW-047).
//
// SubscribeEvents reads the read-only /events Server-Sent-Events stream and
// parses each frame into a typed Event. SW-044's /events is a *freshness
// firehose* — it emits lifecycle frames (ready / ingest-completed / bye / error)
// about the Engine/graph state; it is NOT a per-operation progressive answer
// stream. The TUI subscribes to it to keep the navigator live and to drive the
// degraded-state banner without blocking navigation.
package client

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Event is one parsed SSE frame from /events. Type mirrors the server's
// event:<type> line (ready|ingest-completed|bye|error|...); Data is the raw
// JSON from the data: line (verbatim). The TUI maps Type onto navigator-refresh
// or degraded-banner transitions.
type Event struct {
	Type string // SSE event type (ready, ingest-completed, bye, error, ...)
	ID   uint64 // monotonic per-connection sequence (0 if absent)
	Data string // raw JSON payload from the data: line
}

// SubscribeEvents opens the /events SSE stream and returns a channel of typed
// Events. The channel is closed when ctx is cancelled, the stream ends (a "bye"
// frame is delivered first, then the stream closes), or a transport error
// occurs. A dial failure returns ErrUnreachable immediately (no channel) so the
// caller can render the degraded banner. The read loop runs on its own
// goroutine; cancel ctx to tear it down (no goroutine/connection leak).
func (h *HTTP) SubscribeEvents(ctx context.Context) (<-chan Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.base+"/events", nil)
	if err != nil {
		return nil, fmt.Errorf("client: build request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Graphi-Schema-Version", strconv.Itoa(httpSchemaVersion))
	resp, err := h.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, sanitizeDialErr(err))
	}
	if resp.StatusCode != http.StatusOK {
		body := envMessage(readAllSmall(resp))
		_ = resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusPreconditionFailed:
			return nil, fmt.Errorf("%w: %s", ErrSchemaMismatch, body)
		case http.StatusServiceUnavailable:
			return nil, fmt.Errorf("%w: %s", ErrUnavailable, body)
		default:
			return nil, fmt.Errorf("client: event stream error (%d): %s", resp.StatusCode, body)
		}
	}

	out := make(chan Event)
	go func() {
		defer close(out)
		defer func() { _ = resp.Body.Close() }()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		var ev Event
		emit := func() bool {
			if ev.Type == "" && ev.Data == "" {
				return true
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return false
			}
			ev = Event{}
			return true
		}
		for sc.Scan() {
			line := sc.Text()
			if line == "" { // blank line terminates a frame
				if !emit() {
					return
				}
				continue
			}
			if strings.HasPrefix(line, ":") { // comment / keep-alive
				continue
			}
			field, value, found := strings.Cut(line, ":")
			if found && strings.HasPrefix(value, " ") {
				value = value[1:]
			}
			switch field {
			case "event":
				ev.Type = value
			case "data":
				ev.Data = value
			case "id":
				if n, err := strconv.ParseUint(value, 10, 64); err == nil {
					ev.ID = n
				}
			}
		}
		// Stream ended; flush any pending frame (best-effort).
		_ = emit()
	}()
	return out, nil
}

// readAllSmall drains a bounded amount of the body for error-message extraction.
func readAllSmall(resp *http.Response) []byte {
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	return buf[:n]
}
