package ledgeraudit

import (
	"fmt"
	"sort"
)

// ReferenceLedger is a MINIMAL, in-package implementation of Ledger used ONLY as
// the audited fixture subject for this conformance suite. It is deliberately NOT
// the EP-003 production token-savings ledger (which meters real engine calls and
// persists across daemon restarts); it exists so the suite can prove its
// recompute / cap / integrity / locality checks work against a known-clean
// subject. It derives savings via its OWN summation path, independent of
// recompute.go, so the audit's recompute genuinely cross-checks it.
type ReferenceLedger struct {
	entries      []LedgerEntry
	sessions     map[string]MicroUSD
	sessionOrder []string
	total        MicroUSD
}

// NewReferenceLedger builds the audited fixture subject from raw ops, pricing,
// and the cap policy. Entries are ordered by (session, seq) for determinism.
func NewReferenceLedger(ops []Op, prices *PriceTable, policy Policy) (*ReferenceLedger, error) {
	rl := &ReferenceLedger{sessions: map[string]MicroUSD{}}
	sorted := append([]Op(nil), ops...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].SessionID != sorted[j].SessionID {
			return sorted[i].SessionID < sorted[j].SessionID
		}
		return sorted[i].Seq < sorted[j].Seq
	})

	sessionRaw := map[string]MicroUSD{}
	var order []string
	for _, op := range sorted {
		mp, err := prices.Price(op.Model)
		if err != nil {
			return nil, err
		}
		// The ledger's OWN derivation: integer micro-USD from raw token savings.
		in, out := op.SavingsTokens()
		raw := MicroUSD(in)*mp.InputPerToken + MicroUSD(out)*mp.OutputPerToken
		capped := CapPerOp(raw, policy)
		rl.entries = append(rl.entries, LedgerEntry{Op: op, SavingsMicroUSD: capped, CapApplied: capped < raw})
		if _, ok := sessionRaw[op.SessionID]; !ok {
			order = append(order, op.SessionID)
		}
		sessionRaw[op.SessionID] += capped
	}
	var grand MicroUSD
	for _, id := range order {
		capped := CapSession(sessionRaw[id], policy)
		rl.sessions[id] = capped
		rl.sessionOrder = append(rl.sessionOrder, id)
		grand += capped
	}
	rl.total = grand
	return rl, nil
}

// Entries implements Ledger.
func (r *ReferenceLedger) Entries() []LedgerEntry { return append([]LedgerEntry(nil), r.entries...) }

// Total implements Ledger.
func (r *ReferenceLedger) Total() MicroUSD { return r.total }

// SessionTotal implements Ledger.
func (r *ReferenceLedger) SessionTotal(sessionID string) MicroUSD { return r.sessions[sessionID] }

// SessionIDs implements Ledger (first-seen order).
func (r *ReferenceLedger) SessionIDs() []string { return append([]string(nil), r.sessionOrder...) }

// tamperedLedger wraps a Ledger and inflates a single entry's savings and the
// derived totals by delta, simulating a ledger that lies. Used to prove the
// independent recompute catches divergence.
type tamperedLedger struct {
	inner       Ledger
	entryIdx    int
	delta       MicroUSD
	tampered    []LedgerEntry
	newTotal    MicroUSD
	newSessions map[string]MicroUSD
}

func newTamperedLedger(inner Ledger, entryIdx int, delta MicroUSD) (*tamperedLedger, error) {
	entries := inner.Entries()
	if entryIdx < 0 || entryIdx >= len(entries) {
		return nil, fmt.Errorf("tamper: entry index %d out of range (%d entries)", entryIdx, len(entries))
	}
	cp := append([]LedgerEntry(nil), entries...)
	cp[entryIdx].SavingsMicroUSD += delta
	sessions := map[string]MicroUSD{}
	for _, id := range inner.SessionIDs() {
		sessions[id] = inner.SessionTotal(id)
	}
	sessions[cp[entryIdx].Op.SessionID] += delta
	return &tamperedLedger{
		inner: inner, entryIdx: entryIdx, delta: delta,
		tampered: cp, newTotal: inner.Total() + delta, newSessions: sessions,
	}, nil
}

func (t *tamperedLedger) Entries() []LedgerEntry          { return t.tampered }
func (t *tamperedLedger) Total() MicroUSD                 { return t.newTotal }
func (t *tamperedLedger) SessionTotal(id string) MicroUSD { return t.newSessions[id] }
func (t *tamperedLedger) SessionIDs() []string            { return t.inner.SessionIDs() }
