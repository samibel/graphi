package observe

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func pub(ctx context.Context, b *Broker, class string, payload string) {
	b.Publish(ctx, Event{Type: class, Ts: time.Unix(0, 0), Payload: json.RawMessage(payload)})
}

// readClass reads one ClassEvent or reports timeout.
func readClass(t *testing.T, ch <-chan ClassEvent) (ClassEvent, bool) {
	t.Helper()
	select {
	case ce, ok := <-ch:
		return ce, ok
	case <-time.After(time.Second):
		return ClassEvent{}, false
	}
}

// drainEmpty asserts no event arrives within a short window.
func drainEmpty(t *testing.T, ch <-chan ClassEvent, label string) {
	t.Helper()
	select {
	case ce := <-ch:
		t.Fatalf("%s: unexpected cross-class delivery: %+v", label, ce)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestSubscribeClass_OnlyOwnClass(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := New()
	ch, err := b.SubscribeClass(ctx, ClassDaemonHealth)
	if err != nil {
		t.Fatalf("SubscribeClass: %v", err)
	}
	// Publish one of every class.
	for _, c := range []string{ClassDiagnostics, ClassDaemonHealth, ClassGraphInvalidated, ClassStaleRefs, ClassWorkspaceReadiness} {
		pub(ctx, b, c, `{"k":1}`)
	}
	ce, ok := readClass(t, ch)
	if !ok || ce.Class != ClassDaemonHealth || ce.Seq != 0 {
		t.Fatalf("got %+v ok=%v, want daemon_health seq 0", ce, ok)
	}
	drainEmpty(t, ch, "daemon_health subscriber")
}

func TestSubscribeClass_NoCrossLeakageMatrix(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := New()
	classes := []string{ClassDaemonHealth, ClassDiagnostics, ClassGraphInvalidated, ClassStaleRefs, ClassWorkspaceReadiness}
	chans := map[string]<-chan ClassEvent{}
	for _, c := range classes {
		ch, err := b.SubscribeClass(ctx, c)
		if err != nil {
			t.Fatalf("SubscribeClass(%s): %v", c, err)
		}
		chans[c] = ch
	}
	// Emit exactly one event of each class.
	for _, c := range classes {
		pub(ctx, b, c, `{"k":1}`)
	}
	// Each subscriber receives exactly its own class's single event, nothing else.
	for _, c := range classes {
		ce, ok := readClass(t, chans[c])
		if !ok || ce.Class != c {
			t.Fatalf("subscriber %s got %+v ok=%v", c, ce, ok)
		}
		drainEmpty(t, chans[c], "subscriber "+c)
	}
}

func TestSubscribeClass_ReplayDeterministic(t *testing.T) {
	record := []string{ClassDaemonHealth, ClassDiagnostics, ClassDaemonHealth, ClassGraphInvalidated, ClassDaemonHealth}
	run := func() [][]byte {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		b := New()
		ch, _ := b.SubscribeClass(ctx, ClassDaemonHealth)
		for i, c := range record {
			pub(ctx, b, c, `{"i":`+itoa(i)+`}`)
		}
		var out [][]byte
		for i := 0; i < 3; i++ { // three daemon_health events in the record
			ce, ok := readClass(t, ch)
			if !ok {
				t.Fatalf("missing daemon_health event %d", i)
			}
			m, _ := MarshalClassEvent(ce)
			out = append(out, m)
		}
		return out
	}
	a, bb := run(), run()
	if len(a) != len(bb) {
		t.Fatalf("length mismatch %d vs %d", len(a), len(bb))
	}
	for i := range a {
		if !bytes.Equal(a[i], bb[i]) {
			t.Fatalf("replay %d not byte-identical:\n a=%s\n b=%s", i, a[i], bb[i])
		}
	}
	// Sequence numbers are 0,1,2 in order.
	for i, m := range a {
		if !bytes.Contains(m, []byte(`"seq":`+itoa(i))) {
			t.Fatalf("event %d missing seq %d: %s", i, i, m)
		}
	}
}

func TestSubscribeClass_UnknownClass(t *testing.T) {
	b := New()
	if _, err := b.SubscribeClass(context.Background(), "not_a_class"); err == nil {
		t.Fatal("expected error for unknown class")
	}
}

func TestMarshalClassEvent_DeterministicNoTimestamp(t *testing.T) {
	ce := ClassEvent{Class: ClassStaleRefs, Seq: 7, Payload: json.RawMessage(`{"node":"abc"}`)}
	a, err := MarshalClassEvent(ce)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	b, _ := MarshalClassEvent(ce)
	if !bytes.Equal(a, b) {
		t.Fatalf("non-deterministic marshal")
	}
	if bytes.Contains(a, []byte("ts")) || bytes.Contains(a, []byte("Ts")) {
		t.Fatalf("envelope must not carry a wall-clock timestamp: %s", a)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
