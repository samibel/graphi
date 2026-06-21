// Package ledgeraudit is the independent savings-ledger audit, recompute,
// anti-gaming, and cross-restart integrity conformance suite for graphi (story
// SW-011). It validates a token-savings ledger WITHOUT trusting the ledger's own
// accounting: a frozen, version-stamped baseline method is pinned, an independent
// recompute derives savings from raw token-count inputs in integer micro-USD, an
// anti-gaming cap clamps per-operation/per-session contributions, content-
// addressed hashes verify cross-restart integrity, and a locality guard enforces
// local-only pricing. The suite is hermetic (loopback/local, CGo-free,
// zero-telemetry) reusing the SW-008 posture.
//
// IMPORTANT SCOPE NOTE: EP-003 has not yet shipped the production token-savings
// ledger. To make the suite runnable NOW against a known-clean subject, this
// package ships a minimal ReferenceLedger used ONLY as the audited fixture
// subject. It is deliberately NOT the EP-003 production ledger (which meters
// real engine calls and persists across daemon restarts). When EP-003 lands, the
// same Audit/Ledger contract audits the real implementation unchanged.
package ledgeraudit

// MicroUSD is an integer count of micro-dollars (1 USD = 1,000,000 micro-USD).
// All savings math is integer micro-USD to avoid float tolerance ambiguity.
type MicroUSD int64

// TokenCount is an integer count of tokens.
type TokenCount int64

// Op is a raw, pre-ledger operation input: the token counts a call consumed
// versus the whole-file-read baseline. BOTH the independent recompute and the
// (fixture) ledger derive savings from these raw inputs — the recompute never
// reads a stored ledger total, which is what makes the audit independent.
type Op struct {
	Model                string
	InputTokens          TokenCount // tokens actually consumed (input)
	OutputTokens         TokenCount // tokens actually consumed (output)
	BaselineInputTokens  TokenCount // whole-file-read baseline (input)
	BaselineOutputTokens TokenCount // whole-file-read baseline (output)
	SessionID            string
	Seq                  int64 // monotonic within session
}

// SavingsTokens returns the (clamped-non-negative) tokens saved versus baseline.
func (o Op) SavingsTokens() (input, output TokenCount) {
	in := o.BaselineInputTokens - o.InputTokens
	if in < 0 {
		in = 0
	}
	out := o.BaselineOutputTokens - o.OutputTokens
	if out < 0 {
		out = 0
	}
	return in, out
}

// LedgerEntry is what the audited ledger records for one op.
type LedgerEntry struct {
	Op              Op
	SavingsMicroUSD MicroUSD // per-op CAPPED savings (integer)
	CapApplied      bool
}

// Ledger is the contract this suite audits. The in-package ReferenceLedger is
// the fixture subject; EP-003's production ledger will satisfy the same contract.
type Ledger interface {
	Entries() []LedgerEntry
	Total() MicroUSD
	SessionTotal(sessionID string) MicroUSD
	SessionIDs() []string
}
