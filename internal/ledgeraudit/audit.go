package ledgeraudit

import (
	"fmt"
	"strconv"
)

// AuditName is the distinct named CI check emitted by this suite.
const AuditName = "savings-ledger-audit"

// Delta records a recompute-vs-ledger disagreement for one entry.
type Delta struct {
	EntryIndex int     `json:"entry_index"`
	Ledger     MicroUSD `json:"ledger_micro_usd"`
	Recompute  MicroUSD `json:"recompute_micro_usd"`
	Diff       MicroUSD `json:"diff_micro_usd"`
}

// AuditReport is the machine-readable outcome of the savings-ledger audit.
type AuditReport struct {
	Name               string   `json:"name"`
	Pass               bool     `json:"pass"`
	MethodVersion      string   `json:"method_version"`
	RecomputeAgreement bool     `json:"recompute_agreement"`
	CapEnforced        bool     `json:"cap_enforced"`
	RestartIntegrity   bool     `json:"restart_integrity"`
	LocalPricing       bool     `json:"local_pricing"`
	Violations         []string `json:"violations,omitempty"`
	Deltas             []Delta  `json:"deltas,omitempty"`
}

// AuditInput bundles the inputs to Audit.
type AuditInput struct {
	Ledger              Ledger
	Ops                 []Op        // raw inputs the ledger was built from
	Prices              *PriceTable
	Policy              Policy
	State               LedgerState // persisted/canonical state for restart check
	PinnedMethodVersion string
	Guard               *LocalityGuard
	PriceSource         string // local path the prices were loaded from
}

// Audit runs all five check groups against the input and returns a report. Pass
// is true iff every group passes with no violations.
func Audit(in AuditInput) AuditReport {
	rep := AuditReport{Name: AuditName, Pass: true, MethodVersion: BaselineMethodVersion}
	record := func(msg string) {
		rep.Pass = false
		rep.Violations = append(rep.Violations, msg)
	}

	// (1) Baseline version-stamp enforcement.
	if err := AssertBaselineVersion(in.PinnedMethodVersion); err != nil {
		record("baseline version: " + err.Error())
	}

	// (2) Local-only pricing + locality guard.
	rep.LocalPricing = true
	if in.Guard != nil {
		if err := in.Guard.AssertLocal(in.PriceSource); err != nil {
			rep.LocalPricing = false
			record("local pricing: " + err.Error())
		}
	}

	// (3) Recompute agreement: per-entry, grand total, per-session totals. The
	// recompute derives every value from raw ops; it never reads ledger totals,
	// so a tampered ledger is caught here.
	rep.RecomputeAgreement = true
	entries := in.Ledger.Entries()
	expected := map[string]MicroUSD{}
	for _, op := range in.Ops {
		c, _, err := RecomputeEntry(op, in.Prices, in.Policy)
		if err != nil {
			record("recompute entry: " + err.Error())
			rep.RecomputeAgreement = false
			continue
		}
		expected[op.SessionID+"|"+strconv.FormatInt(op.Seq, 10)] = c
	}
	for i, e := range entries {
		key := e.Op.SessionID + "|" + strconv.FormatInt(e.Op.Seq, 10)
		exp := expected[key]
		if e.SavingsMicroUSD != exp {
			rep.RecomputeAgreement = false
			rep.Deltas = append(rep.Deltas, Delta{EntryIndex: i, Ledger: e.SavingsMicroUSD, Recompute: exp, Diff: e.SavingsMicroUSD - exp})
		}
	}
	if rTotal, err := RecomputeTotal(in.Ops, in.Prices, in.Policy); err != nil {
		record("recompute total: " + err.Error())
		rep.RecomputeAgreement = false
	} else if rTotal != in.Ledger.Total() {
		rep.RecomputeAgreement = false
		record(fmt.Sprintf("recompute total mismatch: ledger=%d recompute=%d diff=%d", in.Ledger.Total(), rTotal, in.Ledger.Total()-rTotal))
	}
	bySession, _ := groupBySession(in.Ops)
	for _, id := range in.Ledger.SessionIDs() {
		st, err := RecomputeSessionTotal(bySession[id], in.Prices, in.Policy)
		if err != nil {
			record("recompute session " + id + ": " + err.Error())
			rep.RecomputeAgreement = false
			continue
		}
		if st != in.Ledger.SessionTotal(id) {
			rep.RecomputeAgreement = false
			record(fmt.Sprintf("recompute session %s mismatch: ledger=%d recompute=%d", id, in.Ledger.SessionTotal(id), st))
		}
	}
	if !rep.RecomputeAgreement {
		rep.Pass = false
	}

	// (4) Anti-gaming cap enforcement: no entry/session exceeds its defensible
	// bound.
	rep.CapEnforced = true
	for i, e := range entries {
		if e.SavingsMicroUSD > in.Policy.CapPerOpMicroUSD {
			rep.CapEnforced = false
			record(fmt.Sprintf("cap: entry %d exceeds per-op cap (%d > %d)", i, e.SavingsMicroUSD, in.Policy.CapPerOpMicroUSD))
		}
	}
	for _, id := range in.Ledger.SessionIDs() {
		if st := in.Ledger.SessionTotal(id); st > in.Policy.CapPerSessionMicroUSD {
			rep.CapEnforced = false
			record(fmt.Sprintf("cap: session %s exceeds per-session cap (%d > %d)", id, st, in.Policy.CapPerSessionMicroUSD))
		}
	}
	if !rep.CapEnforced {
		rep.Pass = false
	}

	// (5) Cross-restart integrity: serialize -> deserialize -> verify identical.
	rep.RestartIntegrity = true
	persisted, err := Serialize(in.State)
	if err != nil {
		record("restart integrity serialize: " + err.Error())
		rep.RestartIntegrity = false
	} else {
		after, derr := Deserialize(persisted)
		if derr != nil {
			record("restart integrity deserialize: " + derr.Error())
			rep.RestartIntegrity = false
		} else if verr := VerifyRestart(in.State, after); verr != nil {
			record("restart integrity: " + verr.Error())
			rep.RestartIntegrity = false
		}
	}
	if !rep.RestartIntegrity {
		rep.Pass = false
	}

	return rep
}

// FormatViolations renders a human-readable failure message.
func (r AuditReport) FormatViolations() string {
	if r.Pass {
		return ""
	}
	out := fmt.Sprintf("%s FAILED — violations:\n", r.Name)
	for _, v := range r.Violations {
		out += "  - " + v + "\n"
	}
	return out
}
