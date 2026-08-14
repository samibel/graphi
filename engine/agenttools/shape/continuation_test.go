package shape

// TODO-16 continuation pins: FinishLabs fills Limits.Next with the
// deterministic cap-rerun hint EXACTLY when the item cap truncated the list,
// while Finish — the stable operations' funnel — never fills it. The second
// half is the stable-freeze guarantee: the frozen ops' wire bytes carry
// `"next":""` forever, independent of whether any golden happens to exercise
// a truncated stable call.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/contract"
)

func manyItems(n int) []contract.Item {
	items := make([]contract.Item, 0, n)
	for i := 0; i < n; i++ {
		items = append(items, contract.Item{RefID: fmt.Sprintf("i%03d", i), Rank: n - i, Reason: "row"})
	}
	return items
}

func TestCapNext(t *testing.T) {
	cases := []struct {
		name string
		l    contract.Limits
		want string
	}{
		{"not truncated", contract.Limits{Truncated: false, TotalAvailable: 9}, ""},
		{"truncated zero dropped", contract.Limits{Truncated: true, Dropped: 0, TotalAvailable: 9}, ""},
		{"truncated", contract.Limits{Truncated: true, Dropped: 4, TotalAvailable: 9},
			"raise limit (>=9) to fetch all 9 item(s) in one response; order is deterministic"},
	}
	for _, c := range cases {
		if got := CapNext(c.l); got != c.want {
			t.Errorf("%s: CapNext = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFinishLabsFillsNextOnCapTruncation(t *testing.T) {
	out, err := FinishLabs(&contract.Result{Outcome: contract.OutcomeFound, Items: manyItems(5)}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Limits.Truncated || out.Limits.Dropped != 3 {
		t.Fatalf("expected truncation with 3 dropped, got %+v", out.Limits)
	}
	want := "raise limit (>=5) to fetch all 5 item(s) in one response; order is deterministic"
	if out.Limits.Next != want {
		t.Fatalf("Next = %q, want %q", out.Limits.Next, want)
	}

	// Untruncated labs calls keep the empty Next.
	out, err = FinishLabs(&contract.Result{Outcome: contract.OutcomeFound, Items: manyItems(2)}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if out.Limits.Next != "" {
		t.Fatalf("untruncated Next = %q, want empty", out.Limits.Next)
	}
}

// TestFinishNeverFillsNext is the stable-freeze pin: the frozen operations
// call Finish, and Finish must leave Next empty EVEN under truncation. If
// this test goes red, stable wire bytes changed.
func TestFinishNeverFillsNext(t *testing.T) {
	out, err := Finish(&contract.Result{
		Outcome: contract.OutcomeFound,
		Items:   manyItems(5),
		Confidence: contract.Confidence{
			Distribution: map[string]float64{"confirmed": 1},
			Top:          "confirmed",
			Method:       "test",
		},
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Limits.Truncated {
		t.Fatal("expected truncation")
	}
	if out.Limits.Next != "" {
		t.Fatalf("STABLE-FREEZE RED: Finish filled Next = %q; the frozen ops' \"next\":\"\" bytes changed", out.Limits.Next)
	}
	b, err := contract.Serialize(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"next":""`) {
		t.Fatalf("stable envelope must carry \"next\":\"\" on the wire: %s", b)
	}
}
