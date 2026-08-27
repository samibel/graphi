package exthost

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ProtocolVersion is the ONLY frame protocol this host speaks.
//
// Exact match, not a range — the same discipline extpack.SchemaVersion applies
// to pack manifests, and for the same reason: accepting an older spelling "for
// compatibility" is how a superseded contract stays silently alive. A version
// bump is a new constant and a rejected old one.
const ProtocolVersion = "graphi-ext/1"

// Framing: one header line, then exactly that many bytes of JSON.
//
//	graphi-ext/1 <decimal byte length>\n<payload>
//
// The length is declared BEFORE the payload on purpose. It is what lets the host
// enforce max-response-size by refusing a frame it has not read yet, instead of
// discovering the size after buffering it — the difference between a limit and a
// post-mortem. A lying length is caught anyway, because the body read is bounded
// to the same limit.
//
// Newline-delimited JSON was the alternative and was rejected for exactly that:
// its size is only knowable after the delimiter arrives.
const (
	// MaxHeaderBytes bounds the header line so a stream of garbage cannot be
	// read forever while the host looks for a newline.
	MaxHeaderBytes = 64

	// MinResponseLimit is the smallest max_response_bytes a descriptor may
	// declare. Below this even a handshake would not fit, and a limit that makes
	// every call fail is a configuration error worth naming at load time.
	MinResponseLimit int64 = 512
)

// MessageType is the closed frame vocabulary. An unknown value is a protocol
// violation, never an ignored frame.
type MessageType string

const (
	// MsgHello — host → extension. Opens the handshake, carrying the host's
	// protocol and API versions, the identity the host pinned, the ports the
	// descriptor declared and the limits the host will enforce. The extension is
	// told its own limits so it can fail early and legibly rather than being
	// killed.
	MsgHello MessageType = "hello"
	// MsgHelloAck — extension → host. Closes the handshake.
	MsgHelloAck MessageType = "hello_ack"
	// MsgCall — host → extension. One operation request.
	MsgCall MessageType = "call"
	// MsgPortCall — extension → host. The ONLY way an extension reaches data.
	MsgPortCall MessageType = "port_call"
	// MsgPortResult — host → extension. The answer, or the refusal.
	MsgPortResult MessageType = "port_result"
	// MsgResult — extension → host. The operation's answer.
	MsgResult MessageType = "result"
	// MsgError — extension → host. The operation failed, legibly.
	MsgError MessageType = "error"
	// MsgShutdown — host → extension. Leave. Best-effort: Close kills the
	// process if it does not.
	MsgShutdown MessageType = "shutdown"
)

// Frame is one protocol message.
//
// It is deliberately ONE flat struct rather than a discriminated union of seven.
// A union would need a two-pass decode (type first, then body) and would put the
// per-type field sets in seven places; with one struct the wire shape is
// readable in a single screen and `json:",omitempty"` keeps the encoded frames
// minimal. The cost — a field can be set on a type that ignores it — is paid by
// the host validating each type's required fields explicitly (see host.go), not
// by trusting the shape.
//
// Every string an EXTENSION controls is length-bounded with extpack.Bound before
// it reaches an error message or a provenance record; see bound* in host.go.
type Frame struct {
	Type MessageType `json:"t"`
	// ID correlates a call/port_call with its answer. Zero on hello/shutdown.
	ID uint64 `json:"id,omitempty"`

	// Handshake fields.
	Protocol         string    `json:"protocol,omitempty"`
	HostAPI          string    `json:"host_api,omitempty"`
	API              *APIRange `json:"api,omitempty"`
	ExtensionID      string    `json:"extension_id,omitempty"`
	ExtensionVersion string    `json:"extension_version,omitempty"`
	ArtifactSHA256   string    `json:"artifact_sha256,omitempty"`
	Ports            []string  `json:"ports,omitempty"`
	Limits           *Limits   `json:"limits,omitempty"`
	Operations       []string  `json:"operations,omitempty"`

	// Call fields.
	Operation string          `json:"operation,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`

	// Port fields.
	Port    string          `json:"port,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	OK      bool            `json:"ok,omitempty"`

	// Result fields.
	Findings json.RawMessage `json:"findings,omitempty"`
	// Confidence is the tier the extension claims for its findings. ADR 0013 D5
	// closes `confirmed` to extensions; the host rejects it rather than
	// downgrading it.
	Confidence string `json:"confidence,omitempty"`

	// Error field, shared by MsgError and a refused MsgPortResult.
	Message string `json:"message,omitempty"`
}

// APIRange is the closed range of host API versions an extension supports.
//
// It has the shape and the semantics of extpack.APIRange — the same two MAJOR.MINOR
// bounds, both inclusive — restated here so the JSON wire form is owned by the
// protocol rather than by the tier-A pack manifest that happens to share it.
// Descriptor.API is an extpack.APIRange and is converted at the handshake; see
// descriptor.go for why the two types exist.
type APIRange struct {
	Min string `json:"min"`
	Max string `json:"max"`
}

// Limits are the host-enforced bounds, sent to the extension in the hello so it
// knows what it is being held to.
type Limits struct {
	MaxResponseBytes int64 `json:"max_response_bytes" yaml:"max_response_bytes"`
	TimeoutMS        int64 `json:"timeout_ms" yaml:"timeout_ms"`
}

// WriteFrame encodes and writes one frame.
//
// Header and payload go out in ONE Write so a frame cannot be interleaved with
// another writer's on the same pipe. Both sides are single-writer today; the
// single Write is what keeps that from becoming a latent assumption.
func WriteFrame(w io.Writer, f Frame) error {
	payload, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("exthost: encode %s frame: %w", f.Type, err)
	}
	buf := make([]byte, 0, len(ProtocolVersion)+len(payload)+MaxHeaderBytes)
	buf = append(buf, ProtocolVersion...)
	buf = append(buf, ' ')
	buf = strconv.AppendInt(buf, int64(len(payload)), 10)
	buf = append(buf, '\n')
	buf = append(buf, payload...)
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("exthost: write %s frame: %w", f.Type, err)
	}
	return nil
}

// ReadFrame reads one frame, refusing anything larger than limit.
//
// Order of checks is the contract:
//
//  1. the header line is bounded, so an endless stream of non-newline bytes is
//     an error rather than an allocation;
//  2. the protocol token must match EXACTLY — a foreign or older protocol is
//     ErrProtocolMismatch here, before any body is read;
//  3. the DECLARED length is checked against limit — ErrResponseTooLarge is
//     raised without reading the body;
//  4. the body read is itself bounded to the declared length, so a lying header
//     cannot over-read.
//
// io.EOF is returned unwrapped, because the caller has to tell "the process
// ended" from "the process misbehaved" and wrapping would blur exactly that.
func ReadFrame(r *bufio.Reader, limit int64) (Frame, error) {
	header, err := readHeaderLine(r)
	if err != nil {
		return Frame{}, err
	}
	proto, lengthText, ok := strings.Cut(header, " ")
	if !ok {
		return Frame{}, fmt.Errorf("%w: malformed frame header %q", ErrProtocolViolation, clip(header))
	}
	if proto != ProtocolVersion {
		return Frame{}, fmt.Errorf("%w: peer speaks %q, this host speaks %q",
			ErrProtocolMismatch, clip(proto), ProtocolVersion)
	}
	declared, err := strconv.ParseInt(lengthText, 10, 64)
	if err != nil || declared < 0 {
		return Frame{}, fmt.Errorf("%w: frame length %q is not a non-negative integer",
			ErrProtocolViolation, clip(lengthText))
	}
	if declared > limit {
		return Frame{}, fmt.Errorf("%w: frame declares %d bytes, the limit is %d bytes",
			ErrResponseTooLarge, declared, limit)
	}
	body := make([]byte, declared)
	if _, err := io.ReadFull(r, body); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return Frame{}, fmt.Errorf("%w: frame declared %d bytes and the stream ended early",
				ErrCrashed, declared)
		}
		return Frame{}, fmt.Errorf("exthost: read frame body: %w", err)
	}
	var f Frame
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return Frame{}, fmt.Errorf("%w: frame body is not a valid message: %v", ErrProtocolViolation, err)
	}
	if f.Type == "" {
		return Frame{}, fmt.Errorf("%w: frame carries no type", ErrProtocolViolation)
	}
	return f, nil
}

// readHeaderLine reads up to the first newline, bounded by MaxHeaderBytes.
func readHeaderLine(r *bufio.Reader) (string, error) {
	var sb strings.Builder
	for i := 0; i <= MaxHeaderBytes; i++ {
		b, err := r.ReadByte()
		if err != nil {
			if err == io.EOF && sb.Len() == 0 {
				return "", io.EOF
			}
			if err == io.EOF {
				return "", fmt.Errorf("%w: stream ended mid-header after %q", ErrCrashed, clip(sb.String()))
			}
			return "", fmt.Errorf("exthost: read frame header: %w", err)
		}
		if b == '\n' {
			return sb.String(), nil
		}
		sb.WriteByte(b)
	}
	return "", fmt.Errorf("%w: frame header exceeded %d bytes without a newline",
		ErrProtocolViolation, MaxHeaderBytes)
}

// clip bounds peer-controlled text that lands in an error message. It is a
// second, tighter bound than extpack.Bound's 240 bytes: a header fragment quoted
// back at a user is a diagnostic hint, not a value.
func clip(s string) string {
	const max = 48
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
