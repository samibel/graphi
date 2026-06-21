package ledgeraudit

import (
	"path/filepath"
	"testing"
)

func mustPrices(t *testing.T) *PriceTable {
	t.Helper()
	pt, err := LoadPriceTable(filepath.Join(".", "pricetable.json"))
	if err != nil {
		t.Fatalf("LoadPriceTable: %v", err)
	}
	return pt
}

func TestAudit_CleanFixturePasses(t *testing.T) {
	prices := mustPrices(t)
	policy := DefaultPolicy()
	ops := FrozenOps()
	ledger, err := NewReferenceLedger(ops, prices, policy)
	if err != nil {
		t.Fatalf("NewReferenceLedger: %v", err)
	}
	state, err := NewLedgerState(ledger)
	if err != nil {
		t.Fatalf("NewLedgerState: %v", err)
	}
	guard := NewLocalityGuard(filepath.Join(".", "pricetable.json"))
	rep := Audit(AuditInput{
		Ledger: ledger, Ops: ops, Prices: prices, Policy: policy, State: state,
		PinnedMethodVersion: FixtureBaselineVersion, Guard: guard,
		PriceSource: filepath.Join(".", "pricetable.json"),
	})
	if !rep.Pass {
		t.Fatalf("expected PASS, got violations: %v deltas: %v", rep.Violations, rep.Deltas)
	}
	if !rep.RecomputeAgreement || !rep.CapEnforced || !rep.RestartIntegrity || !rep.LocalPricing {
		t.Errorf("all groups should pass: %+v", rep)
	}
}

func TestAudit_TamperedLedgerCaught(t *testing.T) {
	prices := mustPrices(t)
	policy := DefaultPolicy()
	ops := FrozenOps()
	ledger, _ := NewReferenceLedger(ops, prices, policy)
	tampered, err := newTamperedLedger(ledger, 0, 999)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := NewLedgerState(tampered)
	rep := Audit(AuditInput{
		Ledger: tampered, Ops: ops, Prices: prices, Policy: policy, State: state,
		PinnedMethodVersion: FixtureBaselineVersion,
	})
	if rep.RecomputeAgreement {
		t.Fatal("expected recompute to catch tampered ledger")
	}
	if rep.Pass {
		t.Error("expected overall FAIL on tamper")
	}
	foundDelta := false
	for _, d := range rep.Deltas {
		if d.EntryIndex == 0 && d.Diff == 999 {
			foundDelta = true
		}
	}
	if !foundDelta {
		t.Errorf("expected delta for entry 0 = 999, got %+v", rep.Deltas)
	}
}

func TestBaselineVersion_StampEnforced(t *testing.T) {
	if err := AssertBaselineVersion(FixtureBaselineVersion); err != nil {
		t.Errorf("matching version should pass: %v", err)
	}
	if err := AssertBaselineVersion("stale-v0"); err == nil {
		t.Error("expected drift error for stale pinned version")
	}
}

func TestCap_PerOpClampUnderInflation(t *testing.T) {
	prices := mustPrices(t)
	policy := DefaultPolicy()
	// A single op claiming an enormous baseline (huge raw savings).
	inflate := Op{Model: "default", SessionID: "sx", Seq: 1, InputTokens: 1, OutputTokens: 1, BaselineInputTokens: 10_000_000, BaselineOutputTokens: 10_000_000}
	ledger, err := NewReferenceLedger([]Op{inflate}, prices, policy)
	if err != nil {
		t.Fatal(err)
	}
	e := ledger.Entries()[0]
	if e.SavingsMicroUSD != policy.CapPerOpMicroUSD {
		t.Errorf("oversized op not clamped: got %d want %d (cap_applied=%v)", e.SavingsMicroUSD, policy.CapPerOpMicroUSD, e.CapApplied)
	}
}

func TestCap_PerSessionClamp(t *testing.T) {
	prices := mustPrices(t)
	policy := DefaultPolicy()
	// Many ops in one session; each clamped per-op, but the session sum still
	// exceeds the session cap and must be clamped.
	var ops []Op
	for i := 0; i < 200; i++ {
		ops = append(ops, Op{Model: "default", SessionID: "big", Seq: int64(i), InputTokens: 1, OutputTokens: 1, BaselineInputTokens: 100_000, BaselineOutputTokens: 100_000})
	}
	ledger, err := NewReferenceLedger(ops, prices, policy)
	if err != nil {
		t.Fatal(err)
	}
	if st := ledger.SessionTotal("big"); st != policy.CapPerSessionMicroUSD {
		t.Errorf("session total not clamped: got %d want %d", st, policy.CapPerSessionMicroUSD)
	}
	// Audit confirms no session exceeds bound.
	state, _ := NewLedgerState(ledger)
	rep := Audit(AuditInput{Ledger: ledger, Ops: ops, Prices: prices, Policy: policy, State: state, PinnedMethodVersion: FixtureBaselineVersion})
	if !rep.CapEnforced {
		t.Errorf("cap not enforced under inflation: %+v", rep.Violations)
	}
}

func TestCap_UncappedEntryFailsAudit(t *testing.T) {
	prices := mustPrices(t)
	policy := DefaultPolicy()
	ops := FrozenOps()
	ledger, _ := NewReferenceLedger(ops, prices, policy)
	// Inflate an entry PAST the per-op cap to simulate an uncapped path.
	over := policy.CapPerOpMicroUSD + 5000
	tampered, _ := newTamperedLedger(ledger, 0, over-ledger.Entries()[0].SavingsMicroUSD)
	state, _ := NewLedgerState(tampered)
	rep := Audit(AuditInput{Ledger: tampered, Ops: ops, Prices: prices, Policy: policy, State: state, PinnedMethodVersion: FixtureBaselineVersion})
	if rep.CapEnforced {
		t.Error("expected cap enforcement to flag the over-cap entry")
	}
}

func TestIntegrity_RestartIdentical(t *testing.T) {
	prices := mustPrices(t)
	ledger, _ := NewReferenceLedger(FrozenOps(), prices, DefaultPolicy())
	state, _ := NewLedgerState(ledger)
	persisted, err := Serialize(state)
	if err != nil {
		t.Fatal(err)
	}
	after, err := Deserialize(persisted)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRestart(state, after); err != nil {
		t.Errorf("identical restart should verify: %v", err)
	}
}

func TestIntegrity_ReorderCaught(t *testing.T) {
	prices := mustPrices(t)
	ledger, _ := NewReferenceLedger(FrozenOps(), prices, DefaultPolicy())
	state, _ := NewLedgerState(ledger)
	// Reverse entry order in the "after" state.
	reordered := state
	rev := make([]LedgerEntry, len(state.Entries))
	for i := range state.Entries {
		rev[i] = state.Entries[len(state.Entries)-1-i]
	}
	reordered.Entries = rev
	if err := VerifyRestart(state, reordered); err == nil {
		t.Error("expected reorder to be caught")
	}
}

func TestIntegrity_ReplayCaught(t *testing.T) {
	prices := mustPrices(t)
	ledger, _ := NewReferenceLedger(FrozenOps(), prices, DefaultPolicy())
	state, _ := NewLedgerState(ledger)
	// Duplicate the first entry (double-count / replay).
	replayed := state
	replayed.Entries = append([]LedgerEntry{state.Entries[0]}, state.Entries...)
	if err := VerifyRestart(state, replayed); err == nil {
		t.Error("expected replay/double-count to be caught")
	}
}

func TestPricing_LocalOnlyRejectsRemote(t *testing.T) {
	if _, err := LoadPriceTable("https://example.com/prices.json"); err == nil {
		t.Error("expected remote price source to be rejected")
	}
	g := NewLocalityGuard("local.json")
	if err := g.AssertLocal("https://example.com/prices.json"); err == nil {
		t.Error("expected AssertLocal to reject remote source")
	}
	if err := g.AssertLocal("local.json"); err != nil {
		t.Errorf("local source rejected: %v", err)
	}
}

func TestLocality_NonLoopbackDialRejected(t *testing.T) {
	g := NewLocalityGuard("")
	cases := []struct {
		addr string
		ok   bool
	}{
		{"127.0.0.1:8080", true},
		{"[::1]:9000", true},
		{"8.8.8.8:443", false},
		{"example.com:443", false},
	}
	for _, c := range cases {
		err := g.CheckDial("tcp", c.addr)
		if (err == nil) != c.ok {
			t.Errorf("CheckDial(%q) ok=%v want=%v err=%v", c.addr, err == nil, c.ok, err)
		}
	}
}

func TestDeterministic_AuditReport(t *testing.T) {
	prices := mustPrices(t)
	policy := DefaultPolicy()
	ops := FrozenOps()
	ledger, _ := NewReferenceLedger(ops, prices, policy)
	state, _ := NewLedgerState(ledger)
	run := func() AuditReport {
		return Audit(AuditInput{Ledger: ledger, Ops: ops, Prices: prices, Policy: policy, State: state, PinnedMethodVersion: FixtureBaselineVersion})
	}
	r1, r2 := run(), run()
	if r1.Pass != r2.Pass || len(r1.Violations) != len(r2.Violations) || len(r1.Deltas) != len(r2.Deltas) {
		t.Errorf("non-deterministic audit: r1=%+v r2=%+v", r1, r2)
	}
	// Serialize determinism: two serializations are byte-identical.
	b1, _ := Serialize(state)
	b2, _ := Serialize(state)
	if string(b1) != string(b2) {
		t.Error("Serialize not deterministic")
	}
}
