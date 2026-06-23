package observe

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

// collect drains ch into a slice until ctx is done or max events arrive.
func collect(ctx context.Context, ch <-chan Event, max int) []Event {
	var out []Event
	for len(out) < max {
		select {
		case <-ctx.Done():
			return out
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		}
	}
	return out
}

func TestSubscribe_Publish_Ordered(t *testing.T) {
	b := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := b.Subscribe(ctx)

	want := []string{"a", "b", "c"}
	for _, tp := range want {
		b.Publish(ctx, Event{Type: tp, Ts: time.Now()})
	}

	got := collect(ctx, ch, len(want))
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Type != want[i] {
			t.Fatalf("event %d: got %q, want %q (ordering broken)", i, got[i].Type, want[i])
		}
	}
}

func TestPublish_NeverBlocksOnFullSubscriber(t *testing.T) {
	b := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// subscriber that never drains
	_ = b.Subscribe(ctx)

	done := make(chan struct{})
	go func() {
		// publish far more than the buffer; must return promptly
		for i := 0; i < SubscriberBuffer*10; i++ {
			b.Publish(ctx, Event{Type: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
		// pass
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a full subscriber (backpressure contract violated)")
	}
}

func TestUnsubscribe_OnCancel_NoGoroutineLeak(t *testing.T) {
	b := New()
	base := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	_ = b.Subscribe(ctx)
	// allow the subscriber-cleanup watcher goroutine to register
	time.Sleep(20 * time.Millisecond)
	if got := b.Subscribers(); got != 1 {
		t.Fatalf("before cancel: subscribers=%d, want 1", got)
	}

	cancel()
	// subscriber removed + channel closed + cleanup goroutine exits
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b.Subscribers() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := b.Subscribers(); got != 0 {
		t.Fatalf("after cancel: subscribers=%d, want 0", got)
	}
	// allow cleanup goroutine to exit, then assert no net leak
	time.Sleep(50 * time.Millisecond)
	// tolerance: at most +1 transient goroutine (GC, runtime), not a per-sub leak
	if delta := runtime.NumGoroutine() - base; delta > 1 {
		t.Fatalf("goroutine leak: baseline=%d now=%d (delta %d)", base, base+delta, delta)
	}
}

func TestConcurrent_PublishSubscribe_Race(t *testing.T) {
	b := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = b.Subscribe(ctx)
		}()
	}
	for p := 0; p < 8; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				b.Publish(ctx, Event{Type: "e"})
			}
		}()
	}
	wg.Wait()
}
