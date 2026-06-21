package price

// Savings computes the per-call USD savings for a model and a metered token
// delta. The delta is the token savings reported by engine/meter (SW-017
// MeterRecord.SavingsTokens); it may be negative (an overrun), in which case the
// returned MicroUSD is negative (honest, not clamped here).
//
// The computation is an EXACT integer multiply: MicroUSD = delta × rate. There
// is no rounding step, so no figure can be rounded upward (inflationary). It
// uses the model's input rate as the canonical per-token price (the dominant
// cost component for context-bearing calls); callers needing output-token
// pricing use SavingsWith explicitly.
//
// An unknown model returns an honest UNPRICED result: Priced=false, MicroUSD=0,
// TableVersion stamped, no error, no fabricated number, no implicit default.
// This is the documented honest degradation.
func Savings(table *PriceTable, model string, deltaTokens int) USDResult {
	return SavingsWith(table, model, deltaTokens, false)
}

// SavingsWith computes per-call USD using either the input rate (useOutput=false)
// or the output rate (useOutput=true). Same honesty rules as Savings; exported
// for callers that attribute savings to output tokens.
func SavingsWith(table *PriceTable, model string, deltaTokens int, useOutput bool) USDResult {
	res := USDResult{TableVersion: table.Version, Model: model, Priced: false, MicroUSD: 0}
	rate, ok := table.rateOf(model)
	if !ok {
		return res // honest unpriced: no fabrication, no default, no error
	}
	perToken := rate.InputPerTokenMicroUSD
	if useOutput {
		perToken = rate.OutputPerTokenMicroUSD
	}
	res.Priced = true
	res.MicroUSD = int64(deltaTokens) * perToken // exact integer multiply; may be negative
	return res
}

// Session is a deterministic per-session USD accumulator. It sums the MicroUSD
// of a sequence of per-call USDResults into a per-session total. The total is
// model-attributable (totals are tracked per model) and uses integer addition
// only, so the session total is exactly the sum of its per-call figures.
//
// Cross-restart CUMULATIVE persistence is owned by SW-019 (the ledger); Session
// is the in-memory per-session running sum only.
type Session struct {
	byModel map[string]MicroUSD
}

// NewSession returns an empty per-session accumulator.
func NewSession() *Session {
	return &Session{byModel: make(map[string]MicroUSD)}
}

// Add records one per-call USDResult into the session total. Unpriced results
// contribute zero (and are skipped). It is safe for the caller to call Add once
// per meter record.
func (s *Session) Add(r USDResult) {
	if !r.Priced {
		return
	}
	s.byModel[r.Model] += r.MicroUSD
}

// Total returns the per-session USD total across all models (the sum of every
// priced per-call contribution). It is a pure, deterministic integer sum.
func (s *Session) Total() MicroUSD {
	var total MicroUSD
	for _, v := range s.byModel {
		total += v
	}
	return total
}

// TotalForModel returns the per-session USD total attributed to one model.
func (s *Session) TotalForModel(model string) MicroUSD {
	return s.byModel[model]
}
