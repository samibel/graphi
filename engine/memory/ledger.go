package memory

import (
	"context"
	"fmt"

	"github.com/samibel/graphi/engine/ledger"
)

// LedgerHook adapts the engine/ledger package to the memory.Ledger interface.
// It prices a recall as a credit against the USD token-savings ledger.
type LedgerHook struct {
	l      *ledger.Ledger
	model  string
	priced bool
}

// NewLedgerHook creates a hook that writes to the given ledger. The model
// argument records which model's tokens were avoided (empty string is allowed).
// priced=false records an audit-only entry that contributes 0 to totals.
func NewLedgerHook(l *ledger.Ledger, model string, priced bool) *LedgerHook {
	return &LedgerHook{l: l, model: model, priced: priced}
}

// RecordRecall implements the memory.Ledger interface. It records one ledger
// entry per recall operation with a deterministic call id derived from the
// entry count and saved-token estimate.
func (h *LedgerHook) RecordRecall(ctx context.Context, entryCount int, savedTokens int64) error {
	if h.l == nil {
		return nil
	}
	_, err := h.l.Record(ledger.Credit{
		CallID:   fmt.Sprintf("memory:recall:%d:%d", entryCount, savedTokens),
		Model:    h.model,
		MicroUSD: savedTokens, // 1 token = 1 microUSD for this ledger category
		Priced:   h.priced,
	})
	return err
}
