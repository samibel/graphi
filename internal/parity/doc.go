// Package parity is the REAL-REPOSITORY full-vs-incremental parity matrix — the
// PRD §12.3 / FR-7 reliability gate, measured on real Go rather than on a
// t.TempDir() fixture.
//
// It mirrors internal/corpus structurally, for the same reasons that package
// documents at corpus.go:1-22: a thin cmd/ entrypoint, the logic and its
// HERMETIC tests here, and a dedicated workflow (.github/workflows/parity.yml)
// that is deliberately separate because it shallow-clones public repositories,
// needs the network, and must never be folded into the zero-egress canary
// posture.
//
// # What it asserts
//
// For each change class declared in docs/rc/parity-classes.yaml, it applies the
// class as a REAL EDIT TO REAL SOURCE in a pinned clone, then compares:
//
//	incremental — `graphi rebuild` at the pinned tree, the edit, `graphi sync`
//	full        — `graphi rebuild` over the SAME final tree into a fresh store
//
// Three rows are LIFECYCLE EVENTS rather than content edits and are driven by
// lifecycle.go instead (SW-158): branch_switch indexes at one git ref, checks out
// another and syncs; interrupted_full_pass and restart_and_recovery SIGKILL a
// real graphi subprocess mid-pass and require the next process to converge. Each
// cites the docs/adr/0004-ingest-recovery-disposition.md kill point it lands on,
// and each publishes EVERY repetition of its journey rather than one execution —
// this matrix has already observed a product path whose output varies between
// otherwise-identical runs, so a single green is not evidence of convergence.
//
// The assertion is BYTE EQUALITY of the two portable snapshot envelopes. Byte
// parity over the envelope is strictly stronger than FR-7 :832's enumerated
// field comparison, because model.Graph.Marshal emits ids, kinds, qualified
// names, source anchors, meta, confidence tiers, confidence, reasons and
// evidence canonically sorted — so the field-by-field walk is deliberately not
// re-implemented.
//
// `graphi compare` / BranchDiffReport EXPLAINS a mismatch and never DECIDES one.
// It is a Labs surface, and a §12.3 gate must not depend on a Labs analyzer's
// BranchDiffSchemaVersion. A BranchDiffReport showing no deltas while snapshot
// bytes differ is itself a finding and is recorded as one.
//
// # The instrument boundary
//
// Every graph is produced by the BUILT graphi BINARY as a SUBPROCESS. This
// package never calls ingest in-process: no ingest.Ingester, no IngestAll or
// IngestChanged, no engine/watch, no linker invocation, and nothing from
// cmd/eval. That is the safety property — the harness cannot perturb what it
// measures — and TestNoIngestInProcess asserts it mechanically over BOTH the
// normal and the -test dependency sets.
//
// core/graphstore IS imported, for exactly two calls: open a store file
// read-only, and write its snapshot envelope. That is in-process COMPARISON, not
// in-process INGEST, and it is what makes the real FR-7 assertion available at
// all: no graphi verb emits the envelope, and the store FILE can never be
// byte-compared because kv_meta.index.full_ingest_generation is a fresh random
// id on every full pass. See the AC-1/AC-2 amendment record in the SW-144
// ticket.
//
// # What it is NOT
//
//   - Not a performance measurement. It publishes no latency, no percentile and
//     no RSS figure; parity is a reliability property (PRD :802-805), and §12.2
//     belongs to SW-143.
//   - Not the whole of checklist row 13 in either half alone. Row 13 is
//     satisfied only by SW-144 AND SW-158 TOGETHER — SW-144 built this package
//     and its fifteen change-class rows, SW-158 added the three lifecycle rows in
//     lifecycle.go — and NEITHER STORY ALONE WAS, OR MAY BE REPORTED AS,
//     "SW-144 done" (adopted decision 4).
//   - Not WP6. The evidence index's WP6 gate has a "recovery/crash-fault suite
//     100% green" conjunct (docs/rc/evidence-index.yaml:125-135); the lifecycle
//     rows are an INPUT to it, and its 90-day clock has not started. That row
//     does not move because these rows ran.
//   - Not a place to fix a defect it finds. A real inc≠full mismatch is a
//     product bug; fixing it is a product-byte change that would move the
//     candidate. In slice: find it, publish the FAIL, file it.
package parity
