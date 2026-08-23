// Package ingest_test — pre-flip store test (WP-J11 / SW-179 / C9).
//
// NOTE ON "PRE-FLIP" / "POST-FLIP" IN THIS FILE: both name the SEMANTICS
// STAMP, never the binder state. SW-179 shipped C9 — the
// `ingestSemanticsVersion` bump 11 → 12 — AHEAD of the JVM binder flip, and
// that flip has NOT happened: the binders are still default-off behind
// `GRAPHI_JVM_TYPERESOLVE` (`engine/semantic/semantic.go:45`). So "pre-flip
// store" means a store stamped "11", and the stamp-12 binary is NOT a flipped
// binary.
//
// This file pins the index-migration story's behavioural gate: a store built
// under the pre-flip binary (when `ingestSemanticsVersion` was "11") MUST be
// rejected by `CanWarmStart` once the binary stamps "12". The stamp mismatch
// is the entire protection — content hashes cannot see a binary change, and
// silently serving pre-flip bytes would be exactly the failure mode
// `ingestSemanticsVersion` exists to prevent (see `warmstart.go:14-20`).
//
// CHOICE (documented, not just coded): the pre-flip store is FAIL-CLOSED at
// the warm-start gate, NOT openable for incremental reads. The user-facing
// path is "run `graphi rebuild` once after upgrading" (see `docs/HOWTO.md`,
// "Upgrading across the semantics-version 11 → 12 stamp"). An
// openable-but-stale pre-flip store would be a worse failure mode than a hard
// rejection — it would greet a user with a graph that disagrees with the
// stamp-12 binary without saying so, and that disagreement is exactly the
// shape the W0.b migration race took. The stamp is the only honest gate; the
// test below is the proof that the gate fires.
package ingest_test

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
)

// TestCanWarmStart_PreFlipStoreRejected pins C9 of the WP-J11 flip gate (the
// index-migration story): a store written under the pre-flip semantics
// version ("11") MUST NOT warm-start under the stamp-12 binary, and a fresh
// full pass under the stamp-12 binary MUST re-certify it. The tamper step
// simulates the binary upgrade — it rewrites the sidecar stamp to the
// pre-flip value so the test exercises the exact mismatch a real upgrade
// would produce, without dragging in a second binary.
//
// WHAT THIS PROVES.
//  1. A pre-flip store is REJECTED, not served stale. `CanWarmStart` returns
//     `(files, false, nil)` — the calling code therefore takes the cold
//     re-index path, which is what `graphi rebuild` invokes.
//  2. The cold re-index restores warm-start — a single `IngestAll` under the
//     current binary re-stamps the store and `CanWarmStart` returns true.
//  3. The rejection is the stamp's job, not a side effect: a content-hash
//     mismatch alone would not catch the flip (the file bytes are identical).
//
// WHAT THIS DOES NOT PROVE (out of scope, owner-deferred per C3 / SW-179).
// The graph CONTENT a flipped binary would produce for the same fixture is
// asserted byte-for-byte in `internal/parity` / `cmd/parity` runs against a
// JVM repo (SW-176); a pre-flip binary producing different JVM edges is the
// reason the flip will need the bump that already landed.
func TestCanWarmStart_PreFlipStoreRejected(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, typeresolveFixture())

	// Step 1 — full pass under the stamp-12 binary. The store is now stamped
	// "12" (the current `ingestSemanticsVersion`).
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	if _, ok, err := ing.CanWarmStart(ctx, root); err != nil || !ok {
		t.Fatalf("stamp-12 full pass: CanWarmStart ok = %v (err %v), want true", ok, err)
	}

	// Step 2 — tamper the stamp back to the pre-flip value ("11") to simulate
	// the binary upgrade: an existing store in the wild, written by the
	// pre-flip binary, sitting on disk. The content hashes are unchanged
	// (file bytes are identical); only the binary semantics changed.
	if _, err := ing.MetaDB().ExecContext(ctx,
		"UPDATE ingest_semantics SET value = '11' WHERE key = 'semantics_version'"); err != nil {
		t.Fatalf("rewrite stamp to pre-flip value: %v", err)
	}

	// Step 3 — the gate MUST fire. Pre-flip store is NOT warm-startable.
	files, ok, err := ing.CanWarmStart(ctx, root)
	if err != nil {
		t.Fatalf("CanWarmStart on pre-flip store returned err: %v", err)
	}
	if ok {
		t.Fatalf("pre-flip store warm-started — the stamp gate did NOT fire; a flipped binary would serve "+
			"bytes it never produced. files = %d (a non-zero count means the sidecar still holds the cache)", files)
	}
	if files == 0 {
		t.Fatalf("pre-flip store rejected but files = 0 — the sidecar cache appears empty, so the test " +
			"isn't actually exercising the stamp mismatch it's meant to")
	}

	// Step 4 — `graphi rebuild` in test form: a single full pass re-stamps the
	// store and restores warm-start. The user-facing re-index path is this,
	// with `graphi rebuild` being the documented facade over the cold pass.
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("re-IngestAll under stamp-12 binary: %v", err)
	}
	if _, ok, err := ing.CanWarmStart(ctx, root); err != nil || !ok {
		t.Fatalf("after cold re-index: CanWarmStart ok = %v (err %v), want true", ok, err)
	}
}
