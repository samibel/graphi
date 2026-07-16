package ingest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/samibel/graphi/core/graphstore"
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
//	10: WP-14 follow-up (cont.) — the C# and TypeScript extractors now attach the
//	    same NON-identity meta (C#: `override` flag + attribute names; TS:
//	    `override` flag), so an older store with empty meta on C#/TS declarations
//	    must re-index for the dead_symbol override exemption to apply.
//	11: WP-14 follow-up (cont.) — the TypeScript extractor now attaches decorator
//	    names + a `decorated` flag to decorated classes/methods (Angular/NestJS
//	    framework entry points), so an older store with empty meta on decorated TS
//	    declarations must re-index for the dead_symbol decorator exemption to apply.
const ingestSemanticsVersion = "11"

const (
	semanticsVersionKey        = "semantics_version"
	fullPassInProgressKey      = "full_pass_in_progress"
	fullPassGenerationKey      = "full_pass_generation"
	graphFullPassGenerationKey = "index.full_ingest_generation"
)

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

	// A full pass writes the graph and this sidecar in separate durable
	// transactions. Never trust the old cache/semantics stamp while such a pass
	// is open: at least one graph batch may already have committed even though
	// the sidecar transaction rolled back. The marker is written before the
	// graph generation (and therefore before the first graph batch).
	if _, found, err := i.semanticsValue(ctx, fullPassInProgressKey); err != nil {
		return files, false, fmt.Errorf("ingest: warm-start full-pass marker: %w", err)
	} else if found {
		return files, false, nil
	}

	// Matching completed generations bind the two independent SQLite files.
	// Missing values identify a pre-generation store and a mismatch identifies
	// an interrupted pass or a one-file restore/revert; both require one full
	// rebuild before any incremental warm start is safe.
	metaGeneration, found, err := i.semanticsValue(ctx, fullPassGenerationKey)
	if err != nil {
		return files, false, fmt.Errorf("ingest: warm-start sidecar generation: %w", err)
	}
	if !found || metaGeneration == "" {
		return files, false, nil
	}
	graphGeneration, err := i.store.Metadata(ctx, graphFullPassGenerationKey)
	if errors.Is(err, graphstore.ErrNotFound) {
		return files, false, nil
	}
	if err != nil {
		return files, false, fmt.Errorf("ingest: warm-start graph generation: %w", err)
	}
	if graphGeneration == "" || graphGeneration != metaGeneration {
		return files, false, nil
	}

	var v string
	err = i.meta.QueryRowContext(ctx, "SELECT value FROM ingest_semantics WHERE key = ?", semanticsVersionKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return files, false, nil // pre-stamp store (older binary): re-index once
	}
	if err != nil {
		return files, false, fmt.Errorf("ingest: warm-start stamp: %w", err)
	}
	stamp, err := i.semanticsStamp(root)
	if err != nil {
		return files, false, err
	}
	return files, v == stamp, nil
}

// stampSemanticsTx records the current ingest semantics (including the
// active index-scope fingerprint) on the supplied transaction. Called at the
// end of a successful FULL pass only — an incremental pass never changes
// semantics, and a store without the stamp must stay cold until a full pass
// under the current binary has run.
func (i *Ingester) stampSemanticsTx(ctx context.Context, tx *sql.Tx, root string) error {
	stamp, err := i.semanticsStamp(root)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		"INSERT INTO ingest_semantics(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		semanticsVersionKey, stamp)
	if err != nil {
		return fmt.Errorf("ingest: stamp semantics: %w", err)
	}
	return nil
}

// beginFullPass persists the cross-database recovery intent. Ordering is the
// safety property: the sidecar marker commits first, then the graph generation
// commits, and only then may IngestAll open its first graph batch. Any failure
// after step one deliberately leaves the marker open so the next session
// rebuilds instead of trusting a potentially divergent graph.
func (i *Ingester) beginFullPass(ctx context.Context) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("ingest: mint full-pass generation: %w", err)
	}
	generation := hex.EncodeToString(raw[:])
	if _, err := i.meta.ExecContext(ctx,
		"INSERT INTO ingest_semantics(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		fullPassInProgressKey, generation); err != nil {
		return "", fmt.Errorf("ingest: persist full-pass marker: %w", err)
	}
	if err := i.store.SetMetadata(ctx, graphFullPassGenerationKey, generation); err != nil {
		return "", fmt.Errorf("ingest: persist graph generation: %w", err)
	}
	return generation, nil
}

// finishFullPass atomically certifies the sidecar at the generation already
// stored with the graph and removes the in-progress marker. It is called only
// after every graph batch, the main sidecar transaction, graph metadata writes,
// and the final SQLite checkpoint have completed.
func (i *Ingester) finishFullPass(ctx context.Context, generation string) error {
	return i.metaTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO ingest_semantics(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
			fullPassGenerationKey, generation); err != nil {
			return fmt.Errorf("ingest: certify sidecar generation: %w", err)
		}
		res, err := tx.ExecContext(ctx,
			"DELETE FROM ingest_semantics WHERE key = ? AND value = ?",
			fullPassInProgressKey, generation)
		if err != nil {
			return fmt.Errorf("ingest: clear full-pass marker: %w", err)
		}
		cleared, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("ingest: count cleared full-pass marker: %w", err)
		}
		if cleared != 1 {
			return fmt.Errorf("ingest: full-pass marker changed before certification")
		}
		return nil
	})
}

func (i *Ingester) semanticsValue(ctx context.Context, key string) (value string, found bool, err error) {
	err = i.meta.QueryRowContext(ctx, "SELECT value FROM ingest_semantics WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}
