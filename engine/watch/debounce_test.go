package watch

import (
	"reflect"
	"sync"
	"testing"
	"time"
)

// TestCoalescer_CoalescesBurstToSingleBatch is AC-4: rapid successive events to
// the same file(s) within the quiet window collapse into ONE batch.
func TestCoalescer_CoalescesBurstToSingleBatch(t *testing.T) {
	var (
		mu      sync.Mutex
		fires   int
		lastSet []string
	)
	// A generous window so the synchronous burst below stays inside it; we then
	// Flush deterministically rather than waiting on the wall clock.
	c := newCoalescer(500*time.Millisecond, func(paths []string) {
		mu.Lock()
		fires++
		lastSet = paths
		mu.Unlock()
	})

	// Save-storm: 100 writes to the same file plus a couple of siblings.
	for i := 0; i < 100; i++ {
		c.Add("a.go")
	}
	c.Add("b.go")
	c.Add("a.go")

	// Nothing should have fired yet (still within the window).
	mu.Lock()
	if fires != 0 {
		mu.Unlock()
		t.Fatalf("fired %d times before window elapsed", fires)
	}
	mu.Unlock()

	c.Flush()

	mu.Lock()
	defer mu.Unlock()
	if fires != 1 {
		t.Fatalf("expected exactly 1 coalesced batch, got %d", fires)
	}
	want := []string{"a.go", "b.go"}
	if !reflect.DeepEqual(lastSet, want) {
		t.Fatalf("coalesced set = %v, want %v", lastSet, want)
	}
}

// TestCoalescer_TimerFires verifies the quiet-window timer fires on its own
// (no explicit Flush) after the configured window with no further events.
func TestCoalescer_TimerFires(t *testing.T) {
	done := make(chan []string, 1)
	c := newCoalescer(20*time.Millisecond, func(paths []string) { done <- paths })
	c.Add("x.go")
	select {
	case got := <-done:
		if len(got) != 1 || got[0] != "x.go" {
			t.Fatalf("fired with %v, want [x.go]", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("coalescer did not fire within timeout")
	}
}

// TestCoalescer_StopPreventsFire ensures Stop cancels a pending batch.
func TestCoalescer_StopPreventsFire(t *testing.T) {
	fired := make(chan struct{}, 1)
	c := newCoalescer(20*time.Millisecond, func([]string) { fired <- struct{}{} })
	c.Add("a.go")
	c.Stop()
	select {
	case <-fired:
		t.Fatal("coalescer fired after Stop")
	case <-time.After(100 * time.Millisecond):
		// expected: no fire
	}
}
