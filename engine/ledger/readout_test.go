package ledger

import "testing"

// AC: capped contribution recorded transparently; readout reports capped amount
// + indication, never raw-as-actual. Durable across restart.
func TestReadout_CapTransparency(t *testing.T) {
	l, path := newLedger(t)
	// A capped contribution: raw would have been 5M, cap clamped to 1M.
	if _, err := l.RecordCapped(Credit{CallID: "c1", Model: "m", MicroUSD: 1_000_000, Priced: true}, true); err != nil {
		t.Fatal(err)
	}
	r := l.Readout()
	if !r.LastCallCapped || !r.SessionCapped {
		t.Errorf("readout must report cap applied: %+v", r)
	}
	// The readout reflects the CAPPED amount (1M), not a raw 5M.
	if r.LastCallMicroUSD != 1_000_000 || r.SessionMicroUSD != 1_000_000 {
		t.Errorf("readout must show capped amount: %+v", r)
	}
	l.Close()

	// Durable across restart: the CapApplied flag survives.
	l2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	r2 := l2.Readout()
	// LastCallCapped reflects the last entry's persisted flag.
	if !r2.LastCallCapped {
		t.Errorf("CapApplied not durable across restart: %+v", r2)
	}
	if r2.CumulativeMicroUSD != 1_000_000 {
		t.Errorf("cumulative after restart: %d", r2.CumulativeMicroUSD)
	}
	// Fresh session: SessionCapped is false until a new capped contribution.
	if r2.SessionCapped {
		t.Errorf("fresh session should have SessionCapped=false: %+v", r2)
	}
}

// AC: uncapped contributions report no cap indication.
func TestReadout_NoCapWhenUncapped(t *testing.T) {
	l, _ := newLedger(t)
	defer l.Close()
	l.Record(Credit{CallID: "c1", Model: "m", MicroUSD: 1_000_000, Priced: true})
	r := l.Readout()
	if r.LastCallCapped || r.SessionCapped {
		t.Errorf("uncapped readout must not report cap: %+v", r)
	}
}

// AC: readout carries per-call (last), per-session, and cumulative.
func TestReadout_Fields(t *testing.T) {
	l, path := newLedger(t)
	l.Record(Credit{CallID: "c1", Model: "m", MicroUSD: 1_000_000, Priced: true})
	l.Record(Credit{CallID: "c2", Model: "m", MicroUSD: 500_000, Priced: true})
	l.Close()
	l2, _ := Open(path)
	defer l2.Close()
	l2.Record(Credit{CallID: "c3", Model: "m", MicroUSD: 250_000, Priced: true})
	r := l2.Readout()
	if r.LastCallMicroUSD != 250_000 {
		t.Errorf("last call: %d", r.LastCallMicroUSD)
	}
	if r.SessionMicroUSD != 250_000 {
		t.Errorf("session (fresh): %d", r.SessionMicroUSD)
	}
	if r.CumulativeMicroUSD != 1_750_000 {
		t.Errorf("cumulative: %d", r.CumulativeMicroUSD)
	}
}

// AC: MarshalReadout is canonical/stable (deterministic key order) — same readout
// -> identical bytes (parity foundation).
func TestMarshalReadout_Deterministic(t *testing.T) {
	r := Readout{SessionMicroUSD: 1, CumulativeMicroUSD: 2, LastCallMicroUSD: 3, SessionCapped: true}
	b1, _ := MarshalReadout(r)
	b2, _ := MarshalReadout(r)
	if string(b1) != string(b2) {
		t.Errorf("MarshalReadout not deterministic")
	}
}
