package observe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// Event classes for the P8 granular subscription surface (SW-097). A subscriber
// selects exactly ONE class and receives ONLY events of that class — zero
// over-delivery.
const (
	ClassDaemonHealth       = "daemon_health"
	ClassDiagnostics        = "diagnostics"
	ClassGraphInvalidated   = "graph_invalidated"
	ClassStaleRefs          = "stale_refs"
	ClassWorkspaceReadiness = "workspace_readiness"
)

// eventClasses is the closed set of subscribable classes.
var eventClasses = map[string]bool{
	ClassDaemonHealth:       true,
	ClassDiagnostics:        true,
	ClassGraphInvalidated:   true,
	ClassStaleRefs:          true,
	ClassWorkspaceReadiness: true,
}

// IsEventClass reports whether c is a known subscribable event class.
func IsEventClass(c string) bool { return eventClasses[c] }

// ClassEvent is the per-class delivery envelope. It carries the class, a per-
// subscription monotonic sequence number (so a replay of the same activity
// yields the same ordered sequence), and the producer payload. It deliberately
// omits the publisher wall-clock so the serialized envelope is deterministic and
// byte-identical across transports.
type ClassEvent struct {
	Class   string          `json:"class"`
	Seq     uint64          `json:"seq"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// SubscribeClass registers a subscriber that receives ONLY events of the given
// class, each tagged with a per-subscription monotonic Seq. It returns an error
// for an unknown class. When ctx is cancelled the stream closes. Delivery is
// loss-tolerant for slow consumers (the broker's documented backpressure), but
// off-class events are NEVER delivered (zero cross-class leakage by construction).
func (b *Broker) SubscribeClass(ctx context.Context, class string) (<-chan ClassEvent, error) {
	if !IsEventClass(class) {
		return nil, fmt.Errorf("observe: unknown event class %q", class)
	}
	src := b.Subscribe(ctx)
	out := make(chan ClassEvent, SubscriberBuffer)
	go func() {
		defer close(out)
		var seq uint64
		for e := range src {
			if e.Type != class {
				continue // zero over-delivery: drop every other class
			}
			ce := ClassEvent{Class: class, Seq: seq, Payload: e.Payload}
			seq++
			select {
			case out <- ce:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// MarshalClassEvent is the single canonical serializer for a ClassEvent, shared
// by every transport (SSE, daemon RPC, streamable-HTTP) so the same event is
// byte-identical on the wire regardless of surface. HTML escaping is disabled and
// the trailing newline trimmed.
func MarshalClassEvent(ce ClassEvent) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(ce); err != nil {
		return nil, fmt.Errorf("observe: marshal class event: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
