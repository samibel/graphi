package skillgen

import (
	"context"
	"fmt"

	"github.com/samibel/graphi/engine/ledger"
)

// LedgerHook adapts engine/ledger to the skillgen.Ledger interface.
type LedgerHook struct {
	l      *ledger.Ledger
	model  string
	priced bool
}

// NewLedgerHook creates a hook that writes to the given ledger.
func NewLedgerHook(l *ledger.Ledger, model string, priced bool) *LedgerHook {
	return &LedgerHook{l: l, model: model, priced: priced}
}

// RecordSkillGen implements the skillgen.Ledger interface.
func (h *LedgerHook) RecordSkillGen(ctx context.Context, savedTokens int64) error {
	if h.l == nil {
		return nil
	}
	_, err := h.l.Record(ledger.Credit{
		CallID:   fmt.Sprintf("skillgen:%d", savedTokens),
		Model:    h.model,
		MicroUSD: savedTokens,
		Priced:   h.priced,
	})
	return err
}
