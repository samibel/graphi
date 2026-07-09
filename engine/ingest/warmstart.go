package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ingestSemanticsVersion identifies the SEMANTICS of the graph a full pass
// produces — extractor naming, linker resolution, the typeresolve confirmed
// tier, edge provenance shapes. It is stored in the meta sidecar by a
// successful full pass and checked by CanWarmStart: content hashes alone
// cannot detect that the BINARY changed, so without this stamp an upgraded
// graphi would greet an old store with "up to date" and serve a graph the
// current code would never produce.
//
// Bump whenever identical source bytes would ingest into a different graph:
//
//	1 : v0.2.x — go/types confirmed tier (engine/typeresolve) live.
//	2 : WP-01 — Java/Kotlin mint interned `package` nodes and the import
//	    fan-out collapses to a single file→package edge. The committed
//	    node-kind content changes, so an older store must re-index (not
//	    warm-start) against the new schema.
//	3 : WP-03 — the Go linker materializes unresolved stdlib / 3rd-party
//	    selector call/reference targets as interned `external` nodes with
//	    heuristic calls/references edges (previously dropped). The committed
//	    node/edge set changes for every Go repo, so an older store must
//	    re-index rather than warm-start.
//	4 : WP-05b-1 — the Go linker mints PRECISE external METHOD nodes for
//	    selector calls on a syntactically-typed receiver (`db.Query` with
//	    `db *sql.DB` → "database/sql.DB.Query"), where WP-03 honestly skipped
//	    them. New committed external node/edge content for Go repos with typed
//	    receivers, so an older store must re-index rather than warm-start.
//	5 : WP-06 — the graphstore edges schema changes physically: edge `reason`
//	    and `evidence` are interned into a `reasons` dictionary (reason_id /
//	    evidence_id) and edges are no longer FTS-indexed. The graph CONTENT is
//	    byte-identical, but a store written by an older binary carries the old
//	    inline `edges` columns, so an upgraded binary must re-index (a full pass
//	    re-creates the edges table) rather than warm-start against it.
//	6 : WP-07 default-on build-output denylist — node_modules/target/build/
//	    .gradle/dist are pruned by default (opt out with GRAPHI_INDEX_ALL), which
//	    changes the DEFAULT set of indexed files, so a store indexed by an older
//	    (index-everything) binary must re-index under the new default scope.
//	7 : WP-10 node meta — the Java extractor attaches NON-identity NodeMeta
//	    (annotations/flags) to declaration nodes, persisted in the new nodes.meta
//	    column. Node CONTENT changes for annotated Java symbols, so a store built
//	    by an older binary must re-index to populate real metadata.
//	8 : WP-14 external-node rollout — the Java/Kotlin/Python/TypeScript linkers now
//	    materialize interned `external` nodes (with heuristic calls/references
//	    edges) for import-path-keyed references to stdlib / 3rd-party symbols whose
//	    package clause is absent from the repo (previously dropped+counted). The
//	    committed node/edge set changes for every non-Go repo with external
//	    references, so an older store must re-index rather than warm-start.
//	9 : WP-14 follow-up — the Kotlin extractor now attaches NON-identity NodeMeta
//	    (annotation names + the `override` flag) to declarations, so an older store
//	    (Kotlin nodes with empty meta) must re-index to populate it; the
//	    dead_symbol entry-point exemption reads this meta.
const ingestSemanticsVersion = "9"

// CanWarmStart reports whether the meta sidecar holds a reusable prior index:
// a non-empty file cache written under the CURRENT ingest semantics AND the
// current index scope (see semanticsStamp — an opt-in ignore configuration is
// part of what the graph means, so flipping it re-certifies with a cold
// pass). files is the cached file count (0 ⇒ cold). Callers use this to
// replace a full re-index with a drift pass (DriftSet +
// IngestChangedWithProgress); any error or mismatch means "start cold",
// never "trust the store".
func (i *Ingester) CanWarmStart(ctx context.Context, root string) (files int, ok bool, err error) {
	if err := i.meta.QueryRowContext(ctx, "SELECT COUNT(*) FROM file_content_cache").Scan(&files); err != nil {
		return 0, false, fmt.Errorf("ingest: warm-start probe: %w", err)
	}
	if files == 0 {
		return 0, false, nil
	}
	var v string
	err = i.meta.QueryRowContext(ctx, "SELECT value FROM ingest_semantics WHERE key = 'semantics_version'").Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return files, false, nil // pre-stamp store (older binary): re-index once
	}
	if err != nil {
		return files, false, fmt.Errorf("ingest: warm-start stamp: %w", err)
	}
	return files, v == i.semanticsStamp(root), nil
}

// stampSemanticsTx records the current ingest semantics (including the
// active index-scope fingerprint) on the supplied transaction. Called at the
// end of a successful FULL pass only — an incremental pass never changes
// semantics, and a store without the stamp must stay cold until a full pass
// under the current binary has run.
func (i *Ingester) stampSemanticsTx(ctx context.Context, tx *sql.Tx, root string) error {
	_, err := tx.ExecContext(ctx,
		"INSERT INTO ingest_semantics(key, value) VALUES('semantics_version', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		i.semanticsStamp(root))
	if err != nil {
		return fmt.Errorf("ingest: stamp semantics: %w", err)
	}
	return nil
}
