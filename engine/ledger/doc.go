// Package ledger is graphi's durable savings ledger (story SW-019, epic EP-003).
// It persists every per-call USD savings entry and rolls them into per-session
// and cumulative totals that survive daemon restarts with full integrity.
//
// Durability & integrity posture:
//   - The store is an append-only JSONL journal: one Entry per line. Every
//     append is committed with Sync() (fsync), so a crash mid-commit never
//     counts a partial entry.
//   - On Open, a torn final line (truncated / invalid JSON from a mid-commit
//     crash) is truncated to the last fully-committed consistent state — the
//     ledger recovers, never corrupts.
//   - Each Entry carries a monotonic Seq assigned at append time. On reload,
//     entries are read in order; a line whose Seq is not strictly greater than
//     the previous is treated as a torn-tail artifact and the bad tail is
//     truncated. So each entry is counted EXACTLY ONCE — no double-count, no
//     replay/drift.
//   - The cumulative total is recomputed from the journal on every Open (no
//     cached total that could drift): cumulative == sum of all sessions ==
//     sum of all entries' MicroUSD (priced only).
//   - A fresh session after restart starts its per-session total at 0 while the
//     cumulative total continues from the restored prior value.
//
// Layering: ledger is an engine package. It does NOT import engine/price or
// engine/meter — the caller passes already-priced fields via a local Credit
// value, keeping the ledger a pure durable store of USD figures. It never
// imports surfaces/ or cmd/. All operations use deterministic local storage
// only; there is no network I/O.
package ledger
