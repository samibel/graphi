// Package conformance hosts graphi's full-vs-incremental byte-parity conformance
// harness: the SW-104 (EP-017 capstone) envelope gate, generalized by SW-157 into
// the declarative FR-7 change-class matrix.
//
// It proves the central invariant: the P10 parallel watcher parse is an
// implementation detail invisible in serialized output. The harness builds a graph
// two ways over the SAME change — a single full parse (ingest.IngestAll) versus an
// incremental, watcher-driven, bounded-worker-pool parallel parse
// (engine/watch.Service + Pool + ApplyChangedParsed, with a schedule hook that
// perturbs worker COMPLETION order so the canonical-ordered apply has real arrival
// nondeterminism to defeat) — and compares them.
//
// # The declarative change-class table, and what binds it
//
// The mutation sequence used to be a hardcoded []func() literal with no class
// labels. It is now a declared table, changeClassTable() in changeclass_test.go:
// one row per change class, each carrying a frozen class id, its kind, a prose
// description of what it does and does not prove, an `apply` that mutates the
// fixture tree, and a WITNESS predicate over the resulting graph.
//
// The table is bound to docs/rc/parity-classes.yaml, the machine-readable matrix
// SW-156 landed (15 change classes from PRD FR-7 + 2 crash conditions from Delta
// PRD §9, kept as distinct kinds). TestParityMatrix_DriftGuard enforces the
// binding in six directions — MISSING, PHANTOM, KIND, VERDICT, OWNER, VOCABULARY —
// so a class declared in the YAML with no table row FAILS rather than silently
// going unproven, and a row for a class nobody declared fails too. The class list
// deliberately lives in that YAML and NOT in internal/evalreport, whose
// RequiredChangeClasses feeds cmd/eval's changeSequenceCycle and is an instrument
// this harness must not perturb.
//
// # What each row asserts
//
// Per (backend, class): the two graphs' portable snapshots are byte-identical
// (never a spot query — snapshot bytes carry node meta and full edge provenance,
// so byte parity is strictly stronger than FR-7's enumerated field comparison);
// the class's witness holds, so the row cannot be proving parity over a change
// that never reached the graph; and re-applying the identical change set leaves the
// graph unmoved, which is FR-7 idempotency as repeated APPLICATION rather than
// repeated dispatch.
//
// TestChangeClassTable_WitnessesAreNonVacuous mechanizes the red-without-the-change
// check for every row: it builds each row's seed tree with `apply` NOT run and
// requires the witness to FAIL, so a witness cannot rot into one that does not
// observe its own change.
//
// # Both backends
//
// Every row runs on MemStore AND SQLite, following core/graphstore/contract_test.go's
// backend-parameterized shape. Before SW-157 every parity proof in the tree was
// MemStore-only while the shipped store is SQLite. The comparison unit stays the
// portable, store-independent snapshot envelope, so the two backends' results are
// directly comparable.
//
// # Envelopes, in two tiers
//
// TestFullVsIncremental_EnvelopeParity compares operation envelopes, not just the
// graph, and keeps the tiers in SEPARATE subtests: the four EP-017 operations are
// all Labs, while the twelve frozen Stable operations are the product promise. A
// Labs envelope drifting must not be able to fail the Stable gate, and vice versa.
// Each operation goes through its own canonical encoder — the single place a result
// becomes bytes for every surface — so what is compared is the real wire envelope.
//
// # Scope, stated so it is not overread
//
// This is a HERMETIC proof over t.TempDir() fixtures: no clone, no network, no git
// repository, inside the PR gate. It is NOT the PRD §12.3 reliability gate and must
// never be reported as one. That gate needs the real-repository matrix (SW-144) and
// the process-level lifecycle rows (SW-158); until both land, the classes deferred
// to SW-158 are declared placeholders here that SKIP with a pointer, never vacuous
// green rows.
//
// Layering: engine. It depends only on engine + core packages (test-only). It
// contains no production code; the doc declaration exists so the directory is a
// buildable package for its external _test files.
package conformance
