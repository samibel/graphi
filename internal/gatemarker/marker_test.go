package gatemarker

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// NOTE for every test in this package and in internal/testgate: a failure
// message must never print a raw marker line at the start of a line. These
// tests run inside the very suite cmd/testgate reads, so an unquoted marker in
// a failure message would be a marker emitted by an unlisted gate id — which
// is, correctly, a fail-closed ERROR for the whole run. Always print marker
// text with %q.

func validMarker() Marker {
	return Marker{
		GateID:     "ax06_executor_seam_latency",
		ReasonCode: ReasonControlAboveCeiling,
		Measurements: map[string]float64{
			"rounds":           3,
			"control_delta_us": 412.5,
			"noise_term_us":    1237.5,
			"ceiling_us":       750,
		},
		Detail: "two byte-identical legacy paths could not be told apart",
	}
}

// AC-1: the marker is structured and versioned, and a round trip preserves it.
func TestFormatParseRoundTrip(t *testing.T) {
	line, err := Format(validMarker())
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.HasPrefix(line, LinePrefix+"/v1 ") {
		t.Fatalf("marker line is not prefixed/versioned: %q", line)
	}
	if strings.Contains(line, "\n") {
		t.Fatalf("a marker must occupy exactly one line: %q", line)
	}
	// go test indents continuation lines; recognition must survive that.
	got, ok, err := Parse("        " + line)
	if err != nil || !ok {
		t.Fatalf("Parse(indented) = ok:%v err:%v for %q", ok, err, line)
	}
	if got.GateID != "ax06_executor_seam_latency" || got.ReasonCode != ReasonControlAboveCeiling {
		t.Fatalf("round trip lost identity: %+v", got)
	}
	if got.Measurements["ceiling_us"] != 750 || got.Detail == "" {
		t.Fatalf("round trip lost payload: %+v", got)
	}
}

// Recognition is on the marker, not on prose. A skip message that merely talks
// about being unverified is not a marker.
func TestParseIgnoresProse(t *testing.T) {
	for _, line := range []string{
		"",
		"    canary_latency_test.go:972: AX-06-LATENCY-VERDICT: UNKNOWN after 3 round(s)",
		"--- SKIP: TestSomething (0.00s)",
		"this run is UNVERIFIED and could not measure anything",
	} {
		if _, ok, err := Parse(line); ok || err != nil {
			t.Fatalf("prose recognised as a marker: line=%q ok=%v err=%v", line, ok, err)
		}
	}
}

// AC-5, at the parser: a line that announces itself as a marker and is not one
// is an error. There is no third answer and no silent drop.
func TestParseMalformedIsAnError(t *testing.T) {
	cases := map[string]string{
		"no version":        LinePrefix + ` {"gate_id":"ax06_executor_seam_latency","reason_code":"control_above_ceiling","measurements":{}}`,
		"unknown version":   LinePrefix + `/v9 {"gate_id":"ax06_executor_seam_latency","reason_code":"control_above_ceiling","measurements":{}}`,
		"no payload":        LinePrefix + `/v1`,
		"truncated json":    LinePrefix + `/v1 {"gate_id":"ax06_executor_seam_latency","reason_`,
		"unknown field":     LinePrefix + `/v1 {"gate_id":"ax06_executor_seam_latency","reason_code":"control_above_ceiling","measurements":{"rounds":1,"control_delta_us":1,"noise_term_us":1,"ceiling_us":1},"severity":"low"}`,
		"trailing content":  LinePrefix + `/v1 {"gate_id":"ax06_executor_seam_latency","reason_code":"control_above_ceiling","measurements":{"rounds":1,"control_delta_us":1,"noise_term_us":1,"ceiling_us":1}} and then some`,
		"empty gate id":     LinePrefix + `/v1 {"gate_id":"","reason_code":"control_above_ceiling","measurements":{"rounds":1,"control_delta_us":1,"noise_term_us":1,"ceiling_us":1}}`,
		"open reason code":  LinePrefix + `/v1 {"gate_id":"ax06_executor_seam_latency","reason_code":"the runner was busy","measurements":{"rounds":1}}`,
		"free text only":    LinePrefix + `/v1 {"gate_id":"ax06_executor_seam_latency","reason_code":"control_above_ceiling","detail":"the runner was too degraded to measure"}`,
		"missing one value": LinePrefix + `/v1 {"gate_id":"ax06_executor_seam_latency","reason_code":"control_above_ceiling","measurements":{"rounds":1,"control_delta_us":1,"noise_term_us":1}}`,
	}
	for name, line := range cases {
		m, ok, err := Parse(line)
		if !ok {
			t.Fatalf("%s: a line announcing itself as a marker must be claimed, not ignored: %q", name, line)
		}
		if err == nil {
			t.Fatalf("%s: malformed marker accepted as %+v from %q", name, m, line)
		}
	}
}

// AC-2: the reason code is a closed set and each member names the raw
// measurements it refers to. Free text can never stand in for them.
func TestReasonCodesAreAClosedSetWithRequiredMeasurements(t *testing.T) {
	codes := ReasonCodes()
	if len(codes) == 0 {
		t.Fatal("the closed set is empty")
	}
	for _, code := range codes {
		req := RequiredMeasurements(code)
		if len(req) == 0 {
			t.Fatalf("reason code %q requires no measurement, so free text would satisfy it", code)
		}
		m := Marker{GateID: "ax06_executor_seam_latency", ReasonCode: code, Measurements: map[string]float64{}}
		for _, key := range req {
			if err := Validate(m); err == nil {
				t.Fatalf("reason code %q validated without %q", code, key)
			}
			m.Measurements[key] = 1
		}
		if err := Validate(m); err != nil {
			t.Fatalf("reason code %q rejected its own required measurements: %v", code, err)
		}
	}
	if RequiredMeasurements(ReasonCode("invented_on_the_spot")) != nil {
		t.Fatal("an unlisted reason code must not acquire requirements")
	}
}

func TestFormatRefusesAnInvalidMarker(t *testing.T) {
	if _, err := Format(Marker{GateID: "x", ReasonCode: "not_a_code"}); err == nil {
		t.Fatal("Format accepted a marker with a reason code outside the closed set")
	}
}

// The emitter fails closed too: a gate that cannot build a valid marker fails
// its test rather than degrading to an ordinary, invisible skip.
type recordingTB struct {
	skipped string
	fatal   string
}

func (r *recordingTB) Helper()                           {}
func (r *recordingTB) Skipf(format string, args ...any)  { r.skipped = fmt.Sprintf(format, args...) }
func (r *recordingTB) Fatalf(format string, args ...any) { r.fatal = fmt.Sprintf(format, args...) }

func TestSkipUnverifiedEmitsTheMarkerOnItsOwnLine(t *testing.T) {
	rec := &recordingTB{}
	SkipUnverified(rec, validMarker(), "AX-06: UNKNOWN after %d round(s)", 3)
	if rec.fatal != "" {
		t.Fatalf("valid marker rejected: %q", rec.fatal)
	}
	lines := strings.Split(rec.skipped, "\n")
	last := lines[len(lines)-1]
	parsed, ok, err := Parse(last)
	if !ok || err != nil {
		t.Fatalf("emitted skip does not end in a parseable marker line: ok=%v err=%v message=%q", ok, err, rec.skipped)
	}
	if parsed.GateID != "ax06_executor_seam_latency" {
		t.Fatalf("emitted marker lost its gate id: %+v", parsed)
	}
	if !strings.Contains(lines[0], "UNKNOWN after 3 round(s)") {
		t.Fatalf("human line lost: %q", rec.skipped)
	}
}

func TestSkipUnverifiedFailsRatherThanEmitAnInvalidMarker(t *testing.T) {
	rec := &recordingTB{}
	SkipUnverified(rec, Marker{GateID: "ax06_executor_seam_latency", ReasonCode: "made_up"}, "whatever")
	if rec.skipped != "" {
		t.Fatalf("an invalid marker degraded into an ordinary skip: %q", rec.skipped)
	}
	if rec.fatal == "" {
		t.Fatal("an invalid marker was neither emitted nor reported")
	}
}

// Determinism: the same marker always serialises to the same bytes, so a
// verdict is reproducible.
func TestFormatIsDeterministic(t *testing.T) {
	first, err := Format(validMarker())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, err := Format(validMarker())
		if err != nil {
			t.Fatal(err)
		}
		if again != first {
			t.Fatalf("marker serialisation is not deterministic: %q vs %q", first, again)
		}
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(first, LinePrefix+"/v1 ")), &probe); err != nil {
		t.Fatalf("marker payload is not JSON: %v", err)
	}
}
