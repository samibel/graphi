package client

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/samibel/graphi/internal/gatemarker"
	"github.com/samibel/graphi/internal/testgate"
)

// SW-250. These tests are the part of the two migrated gates that can be run on
// any machine: they check that the UNVERIFIED report the gates build is one
// cmd/testgate will accept, without needing a quiet runner to produce a real
// UNKNOWN.
//
// Marker text is only ever printed with %q. These tests run inside the suite
// cmd/testgate reads, so an unquoted marker echoed by a failing assertion would
// arrive at the outer gate as a marker on an unlisted test — a fail-closed
// ERROR for the whole run.

// The drift guard that matters most: an emitter whose gate id is not on
// testgate's permitted list produces ERROR, not UNVERIFIED. If the two lists
// ever part company, the gates go from "reports it could not measure" to
// "breaks the run", and the failure would only show up on a contended runner.
func TestMigratedGateIDsAreOnTestgatesPermittedList(t *testing.T) {
	permitted := map[string]bool{}
	for _, id := range testgate.PermittedUnverifiedGates() {
		permitted[id] = true
	}
	for _, id := range []string{ax06GateID, sw244GateID} {
		if !permitted[id] {
			t.Fatalf("gate id %q is not on testgate's permitted list %v; its UNVERIFIED report "+
				"would be a fail-closed ERROR", id, testgate.PermittedUnverifiedGates())
		}
	}
	if len(testgate.PermittedUnverifiedGates()) != 2 {
		t.Fatalf("the permitted list has grown to %v; SW-250 migrates exactly the two "+
			"measurements that can prove their own blindness", testgate.PermittedUnverifiedGates())
	}
}

func mustFormat(t *testing.T, m gatemarker.Marker) string {
	t.Helper()
	line, err := gatemarker.Format(m)
	if err != nil {
		t.Fatalf("the gate built a marker cmd/testgate will reject: %v", err)
	}
	return line
}

// Every shape the AX-06 gate can reach must produce a valid marker with the
// numbers its reason code refers to.
func TestAX06UnverifiedMarkerCoversEveryUnknownShape(t *testing.T) {
	stat := func(refDelta, noise, ceiling, overhead, budget, baseline time.Duration) canaryLatencyStat {
		return canaryLatencyStat{
			RefDelta: refDelta, NoiseTerm: noise, Ceiling: ceiling,
			Overhead: overhead, Budget: budget, Baseline: baseline,
		}
	}
	cases := []struct {
		name    string
		overall canaryLatencyResult
		want    gatemarker.ReasonCode
		numbers map[string]float64
	}{
		{
			name: "nothing resolved: the median control was above the ceiling",
			overall: canaryLatencyResult{
				Verdict: canaryLatencyUnknown,
				Median:  stat(400*time.Microsecond, 1200*time.Microsecond, 750*time.Microsecond, 0, 0, 3*time.Millisecond),
				Tail:    stat(0, 0, 0, 0, 0, 0),
			},
			want:    gatemarker.ReasonControlAboveCeiling,
			numbers: map[string]float64{"control_delta_us": 400, "noise_term_us": 1200, "ceiling_us": 750, "rounds": 3},
		},
		{
			name: "half the gate ran: the median was clean, the tail was not judgeable",
			overall: canaryLatencyResult{
				Verdict: canaryLatencyUnknown,
				Median:  canaryLatencyStat{Verdict: canaryLatencyPass, Overhead: 40 * time.Microsecond, Budget: 250 * time.Microsecond, Baseline: 3 * time.Millisecond},
				Tail:    canaryLatencyStat{Verdict: canaryLatencyUnknown, RefDelta: 900 * time.Microsecond, NoiseTerm: 2700 * time.Microsecond, Ceiling: 750 * time.Microsecond},
			},
			want: gatemarker.ReasonTailControlAboveCeiling,
			numbers: map[string]float64{
				"tail_control_delta_us": 900, "tail_noise_term_us": 2700, "tail_ceiling_us": 750,
				"median_overhead_us": 40, "median_budget_us": 250, "rounds": 3,
			},
		},
		{
			name:    "no rounds ran at all",
			overall: canaryLatencyResult{Verdict: canaryLatencyUnknown, Reason: "no rounds ran"},
			want:    gatemarker.ReasonInsufficientSamples,
			numbers: map[string]float64{"baseline_us": 0, "rounds": 3},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := ax06UnverifiedMarker(tc.overall, 3)
			if m.GateID != ax06GateID || m.ReasonCode != tc.want {
				t.Fatalf("marker identity = (%q, %q), want (%q, %q)", m.GateID, m.ReasonCode, ax06GateID, tc.want)
			}
			for key, want := range tc.numbers {
				if got, ok := m.Measurements[key]; !ok || got != want {
					t.Fatalf("measurement %q = %v (present=%v), want %v", key, got, ok, want)
				}
			}
			line := mustFormat(t, m)
			parsed, isMarker, err := gatemarker.Parse("        " + line)
			if !isMarker || err != nil {
				t.Fatalf("emitted marker is not readable: isMarker=%v err=%v line=%q", isMarker, err, line)
			}
			if parsed.ReasonCode != tc.want {
				t.Fatalf("reason code did not survive the wire: %q", line)
			}
		})
	}
}

func TestSW244UnverifiedMarkerCoversEveryUnknownShape(t *testing.T) {
	degraded := canaryLatencyAccounting{
		Name: "p50", Baseline: 3 * time.Millisecond,
		RefDelta: 500 * time.Microsecond, NoiseTerm: 1500 * time.Microsecond, Ceiling: 750 * time.Microsecond,
		Verdict: canaryLatencyUnknown,
	}
	m := sw244UnverifiedMarker(degraded, 3)
	if m.GateID != sw244GateID || m.ReasonCode != gatemarker.ReasonControlAboveCeiling {
		t.Fatalf("marker identity = (%q, %q)", m.GateID, m.ReasonCode)
	}
	for key, want := range map[string]float64{
		"control_delta_us": 500, "noise_term_us": 1500, "ceiling_us": 750, "rounds": 3,
	} {
		if got := m.Measurements[key]; got != want {
			t.Fatalf("measurement %q = %v, want %v", key, got, want)
		}
	}
	mustFormat(t, m)

	empty := sw244UnverifiedMarker(canaryLatencyAccounting{Name: "p50", Verdict: canaryLatencyUnknown}, 3)
	if empty.ReasonCode != gatemarker.ReasonInsufficientSamples {
		t.Fatalf("an empty arm must report insufficient samples, got %q", empty.ReasonCode)
	}
	mustFormat(t, empty)
}

// The whole path, end to end, without a contended machine: the marker a gate
// emits, decorated the way go test decorates a t.Skipf continuation line, read
// back by cmd/testgate as a distinct UNVERIFIED verdict.
func TestEmittedMarkerReachesTestgateAsUnverified(t *testing.T) {
	line := mustFormat(t, ax06UnverifiedMarker(canaryLatencyResult{
		Verdict: canaryLatencyUnknown,
		Median: canaryLatencyStat{
			RefDelta: 400 * time.Microsecond, NoiseTerm: 1200 * time.Microsecond,
			Ceiling: 750 * time.Microsecond, Baseline: 3 * time.Millisecond,
		},
	}, 3))
	pkg := "github.com/samibel/graphi/surfaces/client"
	test := "TestAX06_ExecutorSeamLatencyWithinThreshold"
	// Built with the JSON encoder rather than by hand: the marker payload is
	// itself JSON, and hand-concatenating it into an Output field would produce
	// a stream that is invalid for a reason unrelated to what is under test.
	event := func(action, testName, output string) string {
		raw, err := json.Marshal(struct {
			Action  string `json:"Action"`
			Package string `json:"Package"`
			Test    string `json:"Test,omitempty"`
			Output  string `json:"Output,omitempty"`
		}{action, pkg, testName, output})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	stream := strings.Join([]string{
		event("start", "", ""),
		event("run", test, ""),
		event("output", test, "        "+line+"\n"),
		event("output", test, "--- SKIP: "+test+" (12.00s)\n"),
		event("skip", test, ""),
		event("pass", "", ""),
	}, "\n")
	res, err := testgate.EvaluateWithProducer(strings.NewReader(stream), testgate.ProducerStatus{ExitCode: 0})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != testgate.VerdictUnverified {
		t.Fatalf("verdict = %q, want UNVERIFIED\n%s", res.Verdict, testgate.FormatVerdict(res))
	}
	if len(res.Unverified) != 1 || res.Unverified[0].GateID != ax06GateID {
		t.Fatalf("the gate's report did not arrive: %+v", res.Unverified)
	}
	if testgate.ExitCode(res) != 3 {
		t.Fatalf("exit code = %d, want 3", testgate.ExitCode(res))
	}
}
