package analysis

import (
	"fmt"
	"reflect"
	"testing"
)

func TestImpactKindSelectorMemoryBoundAndCanonicalOrder(t *testing.T) {
	const (
		limit = 16
		total = 100_000
	)
	selector := newImpactKindSelector(limit)
	peakHeap, peakSet := 0, 0
	// Descending input is deliberately hostile to a smallest-N selector: every
	// new value replaces the current maximum. Neither backing structure may grow
	// beyond the one-entry truncation lookahead.
	for i := total - 1; i >= 0; i-- {
		value := fmt.Sprintf("kind-%06d", i)
		selector.add(value)
		selector.add(value) // duplicate must not consume another slot
		if len(selector.values) > peakHeap {
			peakHeap = len(selector.values)
		}
		if len(selector.kept) > peakSet {
			peakSet = len(selector.kept)
		}
	}
	if peakHeap > limit+1 || peakSet > limit+1 {
		t.Fatalf("selector retained heap/set %d/%d entries, want <=%d independent of %d inputs", peakHeap, peakSet, limit+1, total)
	}
	got, truncated := selector.result(limit)
	if !truncated || len(got) != limit {
		t.Fatalf("result len/truncated = %d/%v, want %d/true", len(got), truncated, limit)
	}
	for i, value := range got {
		want := fmt.Sprintf("kind-%06d", i)
		if value != want {
			t.Fatalf("canonical result[%d] = %q, want %q", i, value, want)
		}
	}

	// Reversing input order must produce an identical retained set.
	reverse := newImpactKindSelector(limit)
	for i := 0; i < total; i++ {
		reverse.add(fmt.Sprintf("kind-%06d", i))
	}
	reverseGot, reverseTruncated := reverse.result(limit)
	if !reverseTruncated || !reflect.DeepEqual(reverseGot, got) {
		t.Fatalf("input order changed canonical selection:\n descending=%v\n ascending=%v", got, reverseGot)
	}
}

func TestCanonicalImpactKindsDeduplicatesWithoutFalseTruncation(t *testing.T) {
	got, truncated := canonicalImpactKinds([]string{"b", "a", "b", "a"}, 2)
	if truncated || !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("deduplicated selection = %v, truncated=%v; want [a b]/false", got, truncated)
	}
}
