// Package conformance hosts the SW-104 (EP-017 capstone) full-vs-incremental
// byte-parity conformance harness.
//
// It proves the central EP-017 invariant: the P10 parallel watcher parse is an
// implementation detail invisible in serialized output. The harness builds a
// graph two ways over the SAME mutation sequence — a single full parse
// (ingest.IngestAll) versus an incremental, watcher-driven, bounded-worker-pool
// parallel parse (engine/watch.Service + Pool + ApplyChangedParsed) — then asserts
// that (a) the two serialized graphs are byte-identical and (b) every one of the
// four EP-017 operations' canonical envelopes (notebook-ingest, watcher-status,
// taint-query, communities) is byte-identical against each graph.
//
// Layering: engine. It depends only on engine + core packages (test-only). It
// contains no production code; the doc declaration exists so the directory is a
// buildable package for its external _test files.
package conformance
