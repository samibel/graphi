// Package observe is graphi's minimal, generic event broker. It is the single
// publish/subscribe seam surfaces (HTTP SSE) consume to stream ingestion /
// analysis events to clients. It is deliberately loss-tolerant for slow
// subscribers (documented backpressure) and non-blocking for producers, so a
// slow SSE client can never stall ingestion.
//
// Layering: engine. observe imports nothing from graphi; producers (ingest) and
// consumers (surfaces/http) depend on it — it never depends on them.
package observe

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// SubscriberBuffer is the per-subscriber channel capacity. When a subscriber's
// buffer is full, additional events are DROPPED for that subscriber only (never
// for others, never blocking the producer). This is the documented backpressure
// contract: a read-only freshness stream prefers liveness over completeness.
const SubscriberBuffer = 16

// Event is a single observation published to the broker and streamed to
// subscribers. Payload is arbitrary JSON so producers stay decoupled from the
// wire shape consumed by surfaces.
type Event struct {
	Type    string          `json:"type"`              // e.g. "ingest-completed"
	Ts      time.Time       `json:"ts"`                // publisher wall-clock
	Payload json.RawMessage `json:"payload,omitempty"` // optional, producer-specific
}

// Broker fans Events out to all current subscribers. It is safe for concurrent
// use. The zero value is NOT usable; construct with New.
type Broker struct {
	mu     sync.RWMutex
	subs   map[uint64]chan Event
	nextID uint64
}

// New constructs an empty Broker.
func New() *Broker {
	return &Broker{subs: make(map[uint64]chan Event)}
}

// Subscribe registers a new subscriber and returns its receive channel. When ctx
// is cancelled (e.g. an SSE client disconnect), the subscriber is removed and its
// channel is closed, which unblocks any reader. The channel is buffered
// (SubscriberBuffer); overflow drops events for this subscriber only.
func (b *Broker) Subscribe(ctx context.Context) <-chan Event {
	ch := make(chan Event, SubscriberBuffer)
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.subs[id] = ch
	b.mu.Unlock()
	go func() {
		<-ctx.Done()
		b.mu.Lock()
		if c, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(c)
		}
		b.mu.Unlock()
	}()
	return ch
}

// Publish fans e out to every current subscriber. It never blocks: a full
// subscriber buffer causes that subscriber's copy to be dropped. Publish is safe
// to call from a producer that must not be stalled (e.g. the ingest hot path).
func (b *Broker) Publish(ctx context.Context, e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default:
			// drop on full (loss-tolerant backpressure)
		}
	}
}

// Subscribers returns the current subscriber count. Test/observability helper.
func (b *Broker) Subscribers() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}
