// Package gatemarker carries one measurement gate's own report that it could
// not measure, from inside a `go test` run out to the gate that reads the
// `go test -json` stream.
//
// # Why this exists (SW-250)
//
// Two measurements in this repository already make a reasoned UNKNOWN decision:
// AX-06's executor-seam latency gate and SW-244's shadow-cost accounting. Both
// were built to report "the runner was too degraded for any comparison to be
// meaningful" rather than to fail on an absolute delta or to pass silently.
// Both emitted that decision through t.Skipf, and cmd/testgate read a skip as a
// pass — so the decision was made correctly and then lost in transport. This
// package is the transport.
//
// # What it is not
//
// It invents no new UNKNOWN. It introduces no rule that "a skip means unknown":
// ordinary skips (-short, platform guards) stay ordinary skips, and the gate
// ids permitted to speak on this channel are a hardcoded list held by the
// reader, internal/testgate. A marker whose gate id is not on that list is a
// fail-closed ERROR for the whole run, because a channel that ignores unknown
// senders is a channel someone will use.
//
// # The contract
//
// A marker is one line, recognised structurally and never by matching prose:
//
//	GRAPHI-GATE-UNVERIFIED/v1 {"gate_id":...,"reason_code":...,"measurements":{...}}
//
// The reason code comes from a closed set, and each member of that set names
// the raw measurements it refers to. A marker that carries only free text does
// not validate: "the runner was busy" is a claim, and this channel transports
// evidence.
package gatemarker

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// LinePrefix opens every marker line. It is deliberately unlike anything go
// test itself writes, and recognition is on this token plus a parseable
// payload — never on a prose match of a skip message, which SW-249 showed to
// be fragile in exactly this position.
const LinePrefix = "GRAPHI-GATE-UNVERIFIED"

// Version is the marker wire version. It is carried in the line prefix rather
// than in the payload so that a reader can reject an unknown version before it
// tries to interpret the fields.
const Version = 1

// ReasonCode names WHY a gate could not measure. It is a closed set: a gate
// may only report an inability this package already knows how to describe, and
// each code binds the measurements that substantiate it.
type ReasonCode string

const (
	// ReasonControlAboveCeiling: the run's own same-run A/A control — two
	// byte-identical paths measured against each other — was wider than the
	// gate's ceiling. Nothing can be concluded about the thing under test
	// because the instrument could not tell two identical things apart.
	ReasonControlAboveCeiling ReasonCode = "control_above_ceiling"

	// ReasonTailControlAboveCeiling: the median was judged and clean, but the
	// tail's own control was above its ceiling. Half the gate ran. This is a
	// separate code from ReasonControlAboveCeiling because it is a different
	// report: it carries positive evidence about the median and none about a
	// regression confined to a minority of calls.
	ReasonTailControlAboveCeiling ReasonCode = "tail_control_above_ceiling"

	// ReasonInsufficientSamples: an arm produced no usable samples, or the
	// baseline came out at or below zero, so the arithmetic the verdict rests
	// on never had inputs.
	ReasonInsufficientSamples ReasonCode = "insufficient_samples"
)

// requiredMeasurements binds each reason code to the raw numbers it refers to.
// This is what makes AC-2 enforceable rather than aspirational: free text alone
// cannot satisfy a reason code, because every code requires values.
//
// Units are in the key names on purpose. A bare "ceiling" that might be
// nanoseconds on one gate and microseconds on another is not a measurement, it
// is a number.
var requiredMeasurements = map[ReasonCode][]string{
	ReasonControlAboveCeiling: {
		"rounds",           // how many measurement rounds were attempted
		"control_delta_us", // |A - A| on two byte-identical paths
		"noise_term_us",    // the gate's noise term derived from that control
		"ceiling_us",       // the ceiling the noise term exceeded
	},
	ReasonTailControlAboveCeiling: {
		"rounds",
		"tail_control_delta_us",
		"tail_noise_term_us",
		"tail_ceiling_us",
		"median_overhead_us", // what the half that DID run measured
		"median_budget_us",   // and the budget it was judged against
	},
	ReasonInsufficientSamples: {
		"rounds",
		"baseline_us", // zero or negative is the observation itself
	},
}

// ReasonCodes returns the closed set, sorted, for diagnostics.
func ReasonCodes() []ReasonCode {
	out := make([]ReasonCode, 0, len(requiredMeasurements))
	for code := range requiredMeasurements {
		out = append(out, code)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// RequiredMeasurements returns the measurement keys a reason code must carry,
// or nil when the code is not in the closed set.
func RequiredMeasurements(code ReasonCode) []string {
	req, ok := requiredMeasurements[code]
	if !ok {
		return nil
	}
	out := make([]string, len(req))
	copy(out, req)
	sort.Strings(out)
	return out
}

// Marker is one gate's structured report that it could not measure.
//
// Detail is free text for a human reading CI output. It is never sufficient on
// its own and is never interpreted by a machine.
type Marker struct {
	GateID       string             `json:"gate_id"`
	ReasonCode   ReasonCode         `json:"reason_code"`
	Measurements map[string]float64 `json:"measurements"`
	Detail       string             `json:"detail,omitempty"`
}

// Validate applies the whole contract. Every rejection here becomes a
// fail-closed ERROR at the reader, so the rules are stated once and hold on
// both sides of the channel.
func Validate(m Marker) error {
	if m.GateID == "" {
		return fmt.Errorf("gatemarker: marker has no gate_id")
	}
	for _, r := range m.GateID {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		return fmt.Errorf("gatemarker: gate_id %q must be lower-case, digits and underscores only", m.GateID)
	}
	req, ok := requiredMeasurements[m.ReasonCode]
	if !ok {
		return fmt.Errorf("gatemarker: reason_code %q is not in the closed set %v", m.ReasonCode, ReasonCodes())
	}
	for _, key := range req {
		if _, ok := m.Measurements[key]; !ok {
			return fmt.Errorf(
				"gatemarker: reason_code %q requires measurement %q; a reason without the numbers it refers to is free text, not evidence",
				m.ReasonCode, key)
		}
	}
	for key, value := range m.Measurements {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("gatemarker: measurement key is empty")
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("gatemarker: measurement %q is not a finite number", key)
		}
	}
	return nil
}

// Format renders a valid marker as its single wire line. An invalid marker is
// an error, never a best-effort line: the emitter fails closed too.
func Format(m Marker) (string, error) {
	if err := Validate(m); err != nil {
		return "", err
	}
	// encoding/json sorts map keys, so the same marker always renders to the
	// same bytes and a verdict is reproducible.
	payload, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("gatemarker: encode marker: %w", err)
	}
	return fmt.Sprintf("%s/v%d %s", LinePrefix, Version, payload), nil
}

// Parse decodes one output line.
//
// The three answers are deliberate:
//
//   - (_, false, nil) — the line is not a marker. Ordinary output, ordinary
//     skip text, anything at all: not this channel's business.
//   - (m, true, nil)  — a valid marker.
//   - (_, true, err)  — the line announced itself as a marker and is not one.
//     This is never silently dropped; the reader turns it into ERROR.
func Parse(line string) (Marker, bool, error) {
	// go test indents continuation lines of a t.Skipf message by eight spaces;
	// trimming is what makes recognition survive that decoration.
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, LinePrefix) {
		return Marker{}, false, nil
	}
	rest := trimmed[len(LinePrefix):]
	if !strings.HasPrefix(rest, "/v") {
		return Marker{}, true, fmt.Errorf("gatemarker: malformed marker: %q is not followed by /v<version>", LinePrefix)
	}
	rest = rest[len("/v"):]
	digits := 0
	for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	if digits == 0 {
		return Marker{}, true, fmt.Errorf("gatemarker: malformed marker: version is not a number")
	}
	version, err := strconv.Atoi(rest[:digits])
	if err != nil {
		return Marker{}, true, fmt.Errorf("gatemarker: malformed marker version: %w", err)
	}
	if version != Version {
		return Marker{}, true, fmt.Errorf("gatemarker: unsupported marker version %d (this reader understands v%d)", version, Version)
	}
	payload := strings.TrimSpace(rest[digits:])
	if payload == "" {
		return Marker{}, true, fmt.Errorf("gatemarker: malformed marker: no payload")
	}
	dec := json.NewDecoder(strings.NewReader(payload))
	// An unknown field is a marker written against a contract this reader does
	// not implement. Accepting it would mean interpreting a message whose
	// meaning is not fully understood.
	dec.DisallowUnknownFields()
	var m Marker
	if err := dec.Decode(&m); err != nil {
		return Marker{}, true, fmt.Errorf("gatemarker: malformed marker payload: %w", err)
	}
	if dec.More() {
		return Marker{}, true, fmt.Errorf("gatemarker: malformed marker: trailing content after the payload")
	}
	if err := Validate(m); err != nil {
		return Marker{}, true, err
	}
	return m, true, nil
}

// TB is the subset of testing.TB an emitting gate needs. It is declared here
// rather than importing testing so that this package stays linkable from
// non-test code (the reader in internal/testgate imports it) without dragging
// the testing flag set along.
type TB interface {
	Helper()
	Skipf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// SkipUnverified ends the calling test as skipped, with a human sentence first
// and the structured marker alone on the final line.
//
// The marker goes last and on its own line because go test decorates only the
// FIRST line of a t.Skipf message with "file.go:NN: "; continuation lines are
// merely indented, which Parse trims.
//
// An invalid marker fails the test. A gate that cannot describe its own
// inability to measure must not quietly become an ordinary, invisible skip —
// that is the exact failure mode this whole channel exists to end.
func SkipUnverified(t TB, m Marker, humanFormat string, args ...any) {
	t.Helper()
	line, err := Format(m)
	if err != nil {
		t.Fatalf("gatemarker: refusing to report UNVERIFIED with an invalid marker: %v", err)
		return
	}
	t.Skipf("%s\n%s", fmt.Sprintf(humanFormat, args...), line)
}
