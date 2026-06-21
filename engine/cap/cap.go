// Package cap is graphi's anti-gaming savings cap (story SW-020, epic EP-003).
// It is a pure, deterministic policy that clamps a per-call USD savings
// contribution so a single operation or session cannot inflate the headline
// savings total beyond a defensible bound. The cap is transparent: callers
// record whether a contribution was clamped so the readout never presents a
// capped figure as if it were the raw uncapped amount.
//
// The cap is anti-GAMING by design: it only ever REDUCES a positive
// contribution (the inflation risk). Negative contributions (honest overruns
// reported by SW-017/SW-018) pass through untouched — clamping an overrun
// downward would itself be dishonest.
//
// Layering: cap is an engine package and a pure leaf policy. It performs no I/O
// and imports nothing from ledger/price/meter (the daemon composes
// meter -> price -> cap -> ledger; the cap is one pure step). It never imports
// surfaces/ or cmd/.
package cap

// MicroUSD is the integer fixed-point USD type the cap operates on, matching
// engine/price.MicroUSD and engine/ledger.MicroUSD (1e6 = $1). It is a type
// alias so values pass between packages without conversion.
type MicroUSD = int64

// Cap is the anti-gaming policy. A zero value (PerOpMicroUSD == 0 &&
// PerSessionMicroUSD == 0) means unlimited — no clamping. A non-zero bound is
// enforced as a ceiling: contributions above it are clamped down to it.
type Cap struct {
	// PerOpMicroUSD is the maximum contribution a single operation may make. 0
	// means unlimited (no per-op clamp).
	PerOpMicroUSD MicroUSD
	// PerSessionMicroUSD is the maximum cumulative contribution a session may
	// reach. 0 means unlimited (no per-session clamp).
	PerSessionMicroUSD MicroUSD
}

// Unlimited is the zero-value Cap that clamps nothing. It documents intent at
// call sites that explicitly disable the cap.
var Unlimited = Cap{}

// Apply clamps a contribution against the cap given the session's current
// running total. It returns the clamped contribution and whether any cap was
// applied (per-op or per-session).
//
// Order: the per-op clamp is applied first (contribution -> min(contribution,
// PerOp)), then the per-session clamp (sessionRunning + clamped ->
// min(..., PerSession)). The tighter of the two bounds wins. A zero bound means
// unlimited for that dimension.
//
// Negative contributions (overruns) are never clamped up or down — the cap only
// prevents inflation, so it leaves honest reductions alone.
func (c Cap) Apply(contribution, sessionRunning MicroUSD) (MicroUSD, bool) {
	clamped := contribution
	capped := false

	// Per-op: only clamps positive contributions above the bound.
	if c.PerOpMicroUSD != 0 && clamped > c.PerOpMicroUSD {
		clamped = c.PerOpMicroUSD
		capped = true
	}

	// Per-session: only clamps positive contributions that would push the session
	// total over the bound. Negative contributions reduce the total and are left
	// alone. room is the remaining headroom on the session ceiling (>= 0).
	if c.PerSessionMicroUSD != 0 && clamped > 0 {
		room := c.PerSessionMicroUSD - sessionRunning
		if room < 0 {
			room = 0
		}
		if clamped > room {
			clamped = room
			capped = true
		}
	}

	return clamped, capped
}
