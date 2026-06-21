package ledger

import "encoding/json"

// Readout is the honest savings-ledger readout surfaced over MCP and CLI
// (SW-020). It carries the per-call (most recent), per-session, and cumulative
// USD totals as integer micro-USD, plus transparency flags for the anti-gaming
// cap. All figures reflect already-capped contributions; the flags indicate
// whether a cap was applied so a capped figure is never presented as the raw
// uncapped amount.
type Readout struct {
	SessionMicroUSD    MicroUSD `json:"session_micro_usd"`
	CumulativeMicroUSD MicroUSD `json:"cumulative_micro_usd"`
	LastCallMicroUSD   MicroUSD `json:"last_call_micro_usd"`
	// SessionCapped is true if any contribution in the current session was
	// clamped by the cap.
	SessionCapped bool `json:"session_capped"`
	// LastCallCapped is true if the most recent recorded contribution was
	// clamped by the cap.
	LastCallCapped bool `json:"last_call_capped"`
}

// Readout returns the current honest savings readout. It is a pure view over the
// ledger's in-memory totals + the last entry; it performs no I/O and is safe to
// call concurrently with reads (the ledger is single-writer). On a fresh open
// with no new entries, the last-call fields reflect the last recorded entry
// overall (which may be from a prior session), and SessionCapped is false.
func (l *Ledger) Readout() Readout {
	r := Readout{
		SessionMicroUSD:    l.sessionSum,
		CumulativeMicroUSD: l.cumulative,
		SessionCapped:      l.sessionCapped,
	}
	if n := len(l.entries); n > 0 {
		last := l.entries[n-1]
		r.LastCallMicroUSD = last.MicroUSD
		r.LastCallCapped = last.CapApplied
	}
	return r
}

// MarshalReadout serializes a Readout to stable, canonical JSON (deterministic
// key order). It is the single canonical serializer used by every surface so
// MCP and CLI emit byte-identical structured readouts for the same state.
func MarshalReadout(r Readout) ([]byte, error) {
	return json.Marshal(r)
}
