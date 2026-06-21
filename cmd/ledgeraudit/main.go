// Command ledgeraudit runs the independent savings-ledger audit, recompute,
// anti-gaming cap, and cross-restart integrity conformance suite (story SW-011).
// It builds the reference ledger fixture from the frozen workload, audits it
// against the independent recompute + cap + integrity + local-only-pricing
// checks using the embedded local price table, emits a machine-readable report,
// and exits non-zero on any violation so CI fails loudly. Hermetic (loopback,
// CGo-free, zero-telemetry, embedded pricing).
//
// Usage:
//
//	go run ./cmd/ledgeraudit
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/samibel/graphi/internal/ledgeraudit"
)

func main() {
	prices, err := ledgeraudit.LoadEmbeddedPriceTable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledgeraudit: load prices: %v\n", err)
		os.Exit(2)
	}
	policy := ledgeraudit.DefaultPolicy()
	ops := ledgeraudit.FrozenOps()

	ledger, err := ledgeraudit.NewReferenceLedger(ops, prices, policy)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledgeraudit: build ledger: %v\n", err)
		os.Exit(2)
	}
	state, err := ledgeraudit.NewLedgerState(ledger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledgeraudit: build state: %v\n", err)
		os.Exit(2)
	}

	rep := ledgeraudit.Audit(ledgeraudit.AuditInput{
		Ledger:              ledger,
		Ops:                 ops,
		Prices:              prices,
		Policy:              policy,
		State:               state,
		PinnedMethodVersion: ledgeraudit.FixtureBaselineVersion,
		Guard:               ledgeraudit.NewLocalityGuard(ledgeraudit.EmbeddedPriceSource),
		PriceSource:         ledgeraudit.EmbeddedPriceSource,
	})

	if err := json.NewEncoder(os.Stdout).Encode(rep); err != nil {
		fmt.Fprintf(os.Stderr, "ledgeraudit: encode report: %v\n", err)
		os.Exit(2)
	}
	if !rep.Pass {
		fmt.Fprint(os.Stderr, rep.FormatViolations())
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "%s PASS (method_version=%s)\n", rep.Name, rep.MethodVersion)
}
