package ledgeraudit

import "fmt"

// BaselineMethodVersion stamps the frozen baseline computation method. The audit
// pins this against a fixture value: if the recompute code changes without a
// matching version bump (and fixture re-pin), AssertBaselineVersion fails the
// build (AC: baseline version-stamp enforcement).
const BaselineMethodVersion = "2026-06-20-v1"

// AssertBaselineVersion fails if the code's pinned method version disagrees with
// the fixture's expected version — i.e. the production method changed without an
// explicit version bump.
func AssertBaselineVersion(expected string) error {
	if expected != BaselineMethodVersion {
		return fmt.Errorf(
			"baseline method version drift: code=%q fixture=%q — bump BaselineMethodVersion and re-pin the fixture to bless an intentional change",
			BaselineMethodVersion, expected,
		)
	}
	return nil
}

// RawSavingsMicroUSD independently derives the UNCLAMPED per-op savings from raw
// token-count inputs + the price table, in integer micro-USD. It does not read
// any ledger total.
func RawSavingsMicroUSD(op Op, prices *PriceTable) (MicroUSD, error) {
	mp, err := prices.Price(op.Model)
	if err != nil {
		return 0, err
	}
	in, out := op.SavingsTokens()
	return MicroUSD(in)*mp.InputPerToken + MicroUSD(out)*mp.OutputPerToken, nil
}

// RecomputeEntry independently derives the per-op CAPPED savings from raw inputs.
// It is the audit's independent recompute path; it never reads a stored total.
func RecomputeEntry(op Op, prices *PriceTable, policy Policy) (MicroUSD, bool, error) {
	raw, err := RawSavingsMicroUSD(op, prices)
	if err != nil {
		return 0, false, err
	}
	capped := CapPerOp(raw, policy)
	return capped, capped < raw, nil
}

// RecomputeSessionTotal independently derives the per-session CAPPED cumulative
// savings from raw ops (per-op cap applied first, then the session cap).
func RecomputeSessionTotal(sessionOps []Op, prices *PriceTable, policy Policy) (MicroUSD, error) {
	var sum MicroUSD
	for _, op := range sessionOps {
		c, _, err := RecomputeEntry(op, prices, policy)
		if err != nil {
			return 0, err
		}
		sum += c
	}
	return CapSession(sum, policy), nil
}

// RecomputeTotal independently derives the grand-total CAPPED savings across all
// ops. It is the sum of per-session capped totals, so a session that hits its
// cap cannot inflate the grand total (anti-gaming).
func RecomputeTotal(ops []Op, prices *PriceTable, policy Policy) (MicroUSD, error) {
	bySession, order := groupBySession(ops)
	var total MicroUSD
	for _, id := range order {
		st, err := RecomputeSessionTotal(bySession[id], prices, policy)
		if err != nil {
			return 0, err
		}
		total += st
	}
	return total, nil
}

// groupBySession returns ops grouped by session plus the first-seen order.
func groupBySession(ops []Op) (map[string][]Op, []string) {
	bySession := map[string][]Op{}
	var order []string
	for _, op := range ops {
		if _, ok := bySession[op.SessionID]; !ok {
			order = append(order, op.SessionID)
		}
		bySession[op.SessionID] = append(bySession[op.SessionID], op)
	}
	return bySession, order
}
