package http

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/samibel/graphi/surfaces/client"
)

// envelope wraps every data response so consumers can detect contract drift.
// Payload carries the engine's canonical serialized bytes verbatim (as
// json.RawMessage) so the wire bytes are byte-identical to MCP/CLI output.
type envelope struct {
	SchemaVersion int             `json:"schema_version"`
	Payload       json.RawMessage `json:"payload"`
}

// errBody is the inner structured error of errorEnvelope: a stable machine code
// plus a safe, sanitized human message (never a raw engine error).
type errBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// errorEnvelope is the single error shape shared by every REST error response
// and the SSE event:error frame (AC-5). It is version-stamped like the success
// envelope so clients can detect contract drift on the error path too.
type errorEnvelope struct {
	SchemaVersion int     `json:"schema_version"`
	Error         errBody `json:"error"`
}

// mapError classifies an engine/daemon error into a documented HTTP status, a
// stable machine code, and a SANITIZED client-safe message. Unexpected errors
// collapse to 500 / "internal" / "internal error" — the raw err is NEVER
// returned to the client (it is logged by the caller); this is the AC-5
// no-leak guarantee. Capability-unavailable errors map to 503.
func mapError(err error) (status int, code, message string) {
	switch {
	case errors.Is(err, client.ErrSearchUnavailable),
		errors.Is(err, client.ErrAnalysisUnavailable),
		errors.Is(err, client.ErrMemoryUnavailable),
		errors.Is(err, client.ErrDistillUnavailable),
		errors.Is(err, client.ErrSkillGenUnavailable):
		return http.StatusServiceUnavailable, "unavailable", "capability unavailable"
	default:
		return http.StatusInternalServerError, "internal", "internal error"
	}
}

// writeEnvelope writes a successful data response: the engine bytes embedded
// verbatim inside the versioned envelope. An encode failure is logged (the
// client may already have a partial 200) rather than silently swallowed.
func writeEnvelope(w http.ResponseWriter, raw []byte) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(envelope{SchemaVersion: SchemaVersion, Payload: json.RawMessage(raw)}); err != nil {
		log.Printf("http: writeEnvelope encode: %v", err)
	}
}

// writeJSON writes a bare JSON object (used by /healthz).
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("http: writeJSON encode: %v", err)
	}
}

// writeErr writes the shared errorEnvelope (AC-5) with an explicit status, a
// stable machine code, and an already-safe message. Callers MUST NOT pass a raw
// engine error string here; use writeErrSanitized for engine/daemon errors so
// 5xx detail is mapped and never leaks.
func writeErr(w http.ResponseWriter, code int, errCode, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(errorEnvelope{
		SchemaVersion: SchemaVersion,
		Error:         errBody{Code: errCode, Message: msg},
	}); err != nil {
		log.Printf("http: writeErr encode: %v", err)
	}
}

// writeErrSanitized maps an engine/daemon error to its documented status + code
// + sanitized message (AC-5) and writes the shared errorEnvelope. For 500s the
// real error is logged locally and NEVER written to the client.
func writeErrSanitized(w http.ResponseWriter, err error) {
	status, code, msg := mapError(err)
	if status >= 500 {
		log.Printf("http: %d %s: %v", status, code, err) // detail stays local
	}
	writeErr(w, status, code, msg)
}

// writeMarkdown writes a Markdown body with the correct content type.
func writeMarkdown(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = w.Write([]byte(body))
}
