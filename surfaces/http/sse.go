package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/samibel/graphi/surfaces/client"
)

// handleSSE streams broker events to the client as Server-Sent Events with
// stable framing (AC-2): each frame carries event:<type>, id:<monotonic
// per-connection seq>, and data:<json>. The first frame is a version-stamped
// event:ready handshake (R5: the response always stamps the envelope version,
// so a client that omits X-Graphi-Schema-Version still learns the server
// version). A terminal event:bye frame is emitted on clean teardown (broker
// close); an event:error frame (shared error envelope) is emitted on a stream
// marshal error. The connection is kept alive with :keep-alive comments and
// tears down cleanly on client disconnect (the broker drops the subscriber via
// context cancellation — no goroutine or connection leak).
//
// /events is wired behind schemaGuard (see Handler), so a client advertising a
// wrong X-Graphi-Schema-Version is rejected with 412 BEFORE the stream opens.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	// SW-104: when an `analyzer` query param is present, /events serves a one-shot
	// analysis frame whose payload is derived from the SHARED (*Direct).Analyze
	// path (canonical analysis.Marshal bytes), NOT re-serialized here. This routes
	// the four EP-017 operations through the same single dispatch + encoder as
	// every other surface, so the SSE analysis frame is byte-identical to the
	// CLI/MCP/HTTP/daemon envelope. Absent the param, /events is the freshness
	// firehose (unchanged).
	if analyzer := r.URL.Query().Get("analyzer"); analyzer != "" {
		s.handleSSEAnalyze(w, r, analyzer)
		return
	}
	if s.broker == nil {
		writeErr(w, http.StatusServiceUnavailable, "unavailable", "event stream unavailable")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	var seq uint64
	// Version-stamped handshake frame (R5: mandatory response version stamp).
	if !writeFrame(w, flusher, &seq, "ready", []byte(fmt.Sprintf(`{"schema_version":%d}`, SchemaVersion))) {
		return
	}

	ctx := r.Context()
	events := s.broker.Subscribe(ctx)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Client disconnect: the response writer is gone; do not attempt a
			// terminal frame (it would error). The broker removes the subscriber.
			return
		case <-ticker.C:
			refreshWriteDeadline(w)
			if _, err := fmt.Fprintf(w, ":keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case ev, open := <-events:
			if !open {
				// Broker closed the subscription (server shutdown / ctx cancel):
				// emit the terminal completion frame, then return.
				writeFrame(w, flusher, &seq, "bye", []byte("{}"))
				return
			}
			data, err := json.Marshal(ev)
			if err != nil {
				// Surface the stream error as a sanitized event:error frame
				// (shared error envelope) instead of silently dropping it.
				eb, _ := json.Marshal(errorEnvelope{SchemaVersion: SchemaVersion, Error: errBody{Code: "internal", Message: "internal error"}})
				writeFrame(w, flusher, &seq, "error", eb)
				continue
			}
			if !writeFrame(w, flusher, &seq, ev.Type, data) {
				return
			}
		}
	}
}

// handleSSEAnalyze serves a one-shot analysis result over SSE (SW-104). The
// analysis payload is produced by the SHARED client.Analyze path (the same
// (*Direct).Analyze -> Service.Dispatch -> analysis.Marshal seam the REST
// /analyze handler and every other surface use), so the canonical bytes embedded
// in the `analysis` frame are byte-identical to the other surfaces' envelopes.
// The SSE adapter only frames those bytes; it holds NO analysis logic and does
// NOT re-serialize. Frame sequence: ready -> analysis -> bye.
func (s *Server) handleSSEAnalyze(w http.ResponseWriter, r *http.Request, analyzer string) {
	p := client.AnalyzeParams{
		Name:      analyzer,
		Symbol:    r.URL.Query().Get("symbol"),
		Direction: r.URL.Query().Get("direction"),
	}
	if mn := r.URL.Query().Get("max-nodes"); mn != "" {
		v, err := strconv.Atoi(mn)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "bad max-nodes")
			return
		}
		p.MaxNodes = v
	}
	raw, err := s.client.Analyze(r.Context(), p)
	if err != nil {
		// Emit the shared sanitized error envelope BEFORE switching to the SSE
		// content type, so an unavailable/failed analysis is a normal REST error.
		writeErrSanitized(w, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	var seq uint64
	if !writeFrame(w, flusher, &seq, "ready", []byte(fmt.Sprintf(`{"schema_version":%d}`, SchemaVersion))) {
		return
	}
	if !writeFrame(w, flusher, &seq, "analysis", raw) {
		return
	}
	writeFrame(w, flusher, &seq, "bye", []byte("{}"))
}

// writeFrame writes one SSE frame (event:<type>, id:<*seq incremented>,
// data:<payload>) and flushes. It returns false if the write failed (client
// gone), so the caller can stop streaming. The id counter is per-connection and
// monotonic.
func writeFrame(w http.ResponseWriter, flusher http.Flusher, seq *uint64, event string, data []byte) bool {
	refreshWriteDeadline(w)
	*seq++
	if _, err := fmt.Fprintf(w, "event: %s\nid: %d\ndata: %s\n\n", event, *seq, data); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

// refreshWriteDeadline keeps long-lived SSE connections compatible with the
// server-wide WriteTimeout while retaining a bounded deadline for each frame.
// Recorders and wrapper writers may not support deadlines; that is harmless.
func refreshWriteDeadline(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(httpWriteTimeout))
}
