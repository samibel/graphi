// Package meter is graphi's per-call token metering engine (story SW-017, epic
// EP-003). It wraps a token-efficient engine call and records the tokens that
// call actually consumed against a frozen, version-stamped baseline representing
// what the equivalent whole-file-read would have cost, emitting an honest,
// structured per-call token-savings record.
//
// Honesty posture:
//   - The meter does NOT invent "actual tokens". graphi is local-first and does
//     not call an LLM itself; the tokens graphi contributed to a call are the
//     assembled context bundle's tokens (engine/context.Bundle.Tokens, SW-016),
//     supplied by the caller. The meter records what the caller reports.
//   - The baseline is a pure function of (artifacts, file bytes, version) —
//     byte-identical for identical inputs across processes. The version is a
//     frozen constant captured at emit time; prior records stay attributable to
//     the method under which they were produced (no silent recomputation).
//   - When a baseline cannot be honestly determined (empty artifacts) the record
//     marks BaselineAvailable=false and reports zero savings rather than
//     fabricating a favorable number. Genuine read errors fail-closed (error).
//   - Savings are reported RAW (may be negative); they are NOT clamped here. The
//     anti-gaming cap is a ledger concern (SW-020), not a meter concern.
//
// Attribution: the meter is stateless across calls. Each Record() emits exactly
// one record for exactly one call; the caller owns a unique CallID per engine
// call. The meter adds no aggregation and no dedupe — rollup is the ledger's job
// (SW-019). No wall-clock and no network: hermetic and local-first.
//
// Layering: meter is an engine package. It does NOT import engine/context (the
// caller passes the already-computed actualTokens), keeping the two packages
// decoupled. It never imports surfaces/ or cmd/.
package meter

// BaselineMethodVersion is the version stamp of the frozen baseline method. The
// baseline value is a pure function of (artifacts, file bytes) AND this version;
// if the whole-file-read baseline method changes in a way that alters the value
// for identical inputs, bump this constant. Records capture the version at emit
// time (BaselineVersion field) so prior records stay attributable to the method
// that produced them.
const BaselineMethodVersion = "whole-file-read-v1"
