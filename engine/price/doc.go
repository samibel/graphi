// Package price converts a metered token-savings delta into an honest,
// reproducible USD figure using a checked-in, version-stamped price table
// (story SW-018, epic EP-003). It is the USD layer of graphi's Savings Ledger:
// it turns the raw token signal from engine/meter (SW-017) into the concrete
// "Saved $X" figure the brief's first-run wow depends on.
//
// Honesty & reproducibility posture:
//   - The price table is the SOLE pricing source. It is checked into the repo
//     (engine/price/data/prices.json) and embedded into the binary via
//     //go:embed. There is NEVER an outbound pricing lookup — every figure
//     traces back to an exact, versioned rate. Loading is local-only.
//   - Rates and results are integer micro-USD fixed-point (1e6 micro-USD = $1)
//     to avoid floating-point rounding inflation. The per-call computation is
//     an EXACT integer multiply (delta_tokens × rate); there is no rounding
//     step that could inflate a figure. Micro-USD resolution is fine-grained
//     enough that no rounding is needed.
//   - An unknown model returns a clearly flagged UNPRICED result (Priced=false,
//     MicroUSD=0): no USD is fabricated, no arbitrary default is applied, and it
//     does NOT error. The caller decides how to present an unpriced figure.
//   - A malformed/version-missing/negative-rate/empty table FAILS FAST at load
//     with an explicit error — the package never silently uses stale or partial
//     rates.
//   - Negative deltas (overruns reported by SW-017) produce negative USD
//     honestly. No clamping here (the anti-gaming cap is SW-020).
//
// Layering: price is an engine package and a leaf computation. It does NOT
// import engine/meter (the caller passes the model + delta). It never imports
// surfaces/ or cmd/. It embeds only its own data file.
package price

import _ "embed"

// embeddedPrices is the checked-in, version-stamped price table. It is the sole
// pricing source; it is compiled into the binary so there is never a runtime
// file lookup or network fetch for pricing.
//
//go:embed data/prices.json
var embeddedPrices []byte
