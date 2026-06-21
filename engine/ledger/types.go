package ledger

// MicroUSD is the integer fixed-point USD type carried by ledger entries, matching
// engine/price.MicroUSD (1e6 = $1). It is a type alias so a price.MicroUSD value
// assigns directly without an import.
type MicroUSD = int64

// Entry is one durably-persisted savings record in the journal. It is written
// one-per-line as JSON. Seq is monotonic (assigned at append time) and is the
// stable identity that lets reload count each entry exactly once. SessionID
// groups the entries written during one ledger session (one Open..Close span).
//
// Only Priced entries contribute to totals; an unpriced entry is recorded for
// auditability but contributes 0 (its MicroUSD should be 0).
type Entry struct {
	Seq       int64    `json:"seq"`
	SessionID string   `json:"session_id"`
	CallID    string   `json:"call_id"`
	Model     string   `json:"model"`
	MicroUSD  MicroUSD `json:"micro_usd"`
	Priced    bool     `json:"priced"`
	// CapApplied is true when this contribution was clamped by the anti-gaming
	// cap (SW-020). It makes cap transparency durable: a readout can honestly
	// indicate "a cap was applied" rather than presenting the capped figure as
	// the raw unclosed amount. omitempty keeps old journals (SW-019) clean.
	CapApplied bool `json:"cap_applied,omitempty"`
}

// Credit is the caller-supplied input to Ledger.Record: the priced fields from
// engine/price.USDResult plus a call id. It is local to the ledger so the
// package does not import engine/price.
type Credit struct {
	CallID   string
	Model    string
	MicroUSD MicroUSD
	Priced   bool
}
