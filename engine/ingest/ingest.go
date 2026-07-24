// Package ingest implements graphi's incremental source-ingestion pipeline.
//
// Layering: ingest is an engine package. It consumes core/parse and core/model
// and commits nodes/edges through core/graphstore. It persists its own
// bookkeeping (content cache, reverse dependencies, dirty flags) in a separate
// SQLite sidecar so graphstore remains focused on graph data.
//
// Security: all file paths are sanitized relative to the repo root; no
// eval/exec/shell; all SQL is parameterized.
package ingest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/core/profile"
	"github.com/samibel/graphi/engine/link"
)

// fileUnit identifies one walked file: its absolute and repo-relative paths
// plus the content hash of the bytes the walk saw. src is populated only
// transiently — by ParseFile's own read, or inside parseUnit's on-demand
// re-read whose result never outlives the parse — and NEVER by walk():
// retaining every file's bytes on the unit list held the whole repo's source
// resident for the entire pass.
type fileUnit struct {
	path    string
	relPath string
	src     []byte
	hash    string
}

// IngestAll performs a full ingestion of root, parsing every file and
// rebuilding the cache and reverse-dependency index from scratch.
func (i *Ingester) IngestAll(ctx context.Context, root string) error {
	if err := i.guardReadOnly(); err != nil {
		return err
	}
	i.resetSkips()
	// Validate every repository-controlled semantics config before walking,
	// parsing, or persisting source. stampSemanticsTx recomputes the value at the
	// end so a mid-pass config change also fails closed instead of certifying a
	// graph under stale semantics.
	if _, err := i.semanticsStamp(root); err != nil {
		return err
	}
	i.notifyProgress(ctx, ProgressEvent{Phase: PhaseWalk})
	units, err := i.walk(root, func(discovered int) {
		if discovered%64 == 0 {
			i.notifyProgress(ctx, ProgressEvent{Phase: PhaseWalk, Done: discovered})
		}
	})
	if err != nil {
		return err
	}
	i.notifyProgress(ctx, ProgressEvent{Phase: PhaseParse, Total: len(units)})

	// Pure parallel parse BEFORE the meta transaction: parseUnit mutates no
	// state, so the pool only changes wall-clock, never bytes — the commit
	// below applies results serially in the walked (relPath-sorted) order,
	// exactly the SW-101 discipline the watcher path already relies on.
	// Per-file progress is emitted from the pool drain (calling goroutine,
	// completion order), so Done stays monotonic and Path names real files.
	parsed, err := i.parseUnitsParallel(ctx, root, units, func(done int, path string) {
		i.notifyProgress(ctx, ProgressEvent{Phase: PhaseParse, Done: done, Total: len(units), Path: path})
	})
	if err != nil {
		return err
	}

	// Persist the recovery intent before the first durable graph write. The
	// marker remains open on every error path and makes the next warm-start probe
	// force a complete rebuild; finishFullPass clears it only after both stores
	// and all post-pass graph metadata are durable.
	fullPassGeneration, err := i.beginFullPass(ctx)
	if err != nil {
		return err
	}

	if err := i.metaTx(ctx, func(tx *sql.Tx) error {
		// Capture every node id CURRENTLY IN THE STORE (not the meta cache's
		// view of the previous pass) BEFORE this pass writes, so any node this
		// pass does not re-produce is purged — files that disappeared, renamed
		// symbols on a reused store, AND leftovers of an INTERRUPTED earlier
		// pass (ING-DEC / SW-118): a crash between a committed graph batch and
		// the meta commit leaves the graph ahead of the (rolled-back) cache, so
		// a cache-derived prior set would never see — and never purge — those
		// orphans. Deriving the prior set from the authoritative store makes a
		// full pass converge to the fresh-index bytes from ANY partial state.
		// For an uninterrupted store the two sets are identical, so bytes are
		// unchanged on the happy path.
		priorNodes, err := i.store.Nodes(ctx, graphstore.Query{})
		if err != nil {
			return fmt.Errorf("ingest: list prior store nodes: %w", err)
		}
		priorNodeIDs := make([]string, 0, len(priorNodes))
		for _, n := range priorNodes {
			priorNodeIDs = append(priorNodeIDs, string(n.ID()))
		}

		if _, err := tx.ExecContext(ctx, "DELETE FROM file_content_cache"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM reverse_deps"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM dirty_units"); err != nil {
			return err
		}

		// All graph writes of this pass run in batched sessions (one durable
		// transaction per phase instead of one per node/edge). The pass reads
		// its own writes at three seams (reverse-dep index, the linker's edge
		// sweep, typeresolve's committed set), so each batch commits BEFORE the
		// next read. `open` tracks the live batch so any error path rolls it
		// back — leaving it open would hold the store's single-writer lock.
		var open graphstore.Batch
		defer func() {
			if open != nil {
				_ = open.Rollback()
			}
		}()

		i.notifyProgress(ctx, ProgressEvent{Phase: PhaseWrite, Total: len(units)})
		batch, err := graphstore.BeginBatch(ctx, i.store)
		if err != nil {
			return err
		}
		open = batch

		// Build forward refs for each file, then derive reverse deps. The
		// SERIAL apply loop over the sorted units is what pins the committed
		// byte order; the parallel phase above only produced the ParsedFiles.
		refs := make(map[string][]string, len(units))
		var fileRefs []link.FileRefs
		owned := make(map[string]struct{})
		parserEdges := make(map[string]struct{})
		for k, u := range units {
			nodeIDs, edgeIDs, fwd, fr, err := i.commitParsed(ctx, batch, u, parsed[k])
			if err != nil {
				return err
			}
			if err := i.upsertCacheTx(ctx, tx, u.relPath, pfHash(parsed[k], u), nodeIDs, fr != nil); err != nil {
				return err
			}
			refs[u.relPath] = fwd
			for _, id := range nodeIDs {
				owned[id] = struct{}{}
			}
			for _, eid := range edgeIDs {
				parserEdges[eid] = struct{}{}
			}
			if fr != nil {
				fileRefs = append(fileRefs, *fr)
			}
		}
		// WP-02: link-phase START marker. Done=0 with the honest denominator (files
		// that carry cross-file refs); linkFiles then emits climbing-Done events as
		// each batch resolves, so the PhaseLink Done stream is monotonic 0→Total.
		i.notifyProgress(ctx, ProgressEvent{Phase: PhaseLink, Total: len(fileRefs)})

		// Purge any prior node no longer produced by this pass (deleted files,
		// or renamed symbols on a reused store). DeleteNode cascades incident
		// edges, so no stale cross-file edge can survive.
		for _, id := range priorNodeIDs {
			if _, kept := owned[id]; kept {
				continue
			}
			if err := batch.DeleteNode(ctx, model.NodeId(id)); err != nil {
				return fmt.Errorf("ingest: purge stale node %s: %w", id, err)
			}
		}
		open = nil
		if err := batch.Commit(ctx); err != nil {
			return err
		}
		i.notifyProgress(ctx, ProgressEvent{Phase: PhaseFTS, Done: len(units), Total: len(units)})

		// Translate import-path forward refs into the directory key space so the
		// incremental cascade can look them up by directory (BLOCK-1). The index
		// is built from the now-fully-committed node set.
		nodes, err := i.store.Nodes(ctx, graphstore.Query{})
		if err != nil {
			return fmt.Errorf("ingest: read nodes for reverse deps: %w", err)
		}
		idx := link.BuildIndex(nodes)
		dirRefs := make(map[string][]string, len(refs))
		for file, targets := range refs {
			dirRefs[file] = reverseDepKeys(idx, targets)
		}
		if err := i.writeReverseDepsTx(ctx, tx, dirRefs); err != nil {
			return err
		}
		// Post-node-commit linker pass (site 1): all nodes are now committed, so
		// cross-file/cross-package edges can be resolved against the full set.
		// linkFiles reads committed state first, then writes through the batch.
		i.heartbeat(ctx, PhaseLink)
		linkBatch, err := graphstore.BeginBatch(ctx, i.store)
		if err != nil {
			return err
		}
		open = linkBatch
		if _, err := i.linkFiles(ctx, linkBatch, fileRefs, owned, parserEdges, func(ev ProgressEvent) {
			i.notifyProgress(ctx, ev)
		}); err != nil {
			return err
		}
		open = nil
		if err := linkBatch.Commit(ctx); err != nil {
			return err
		}
		// Third phase (site 1): whole-repo go/types confirmed-tier pass. Runs
		// AFTER the linker so its confirmed edges upsert over the heuristic
		// tier for the same logical edges (see typeresolvePass).
		i.notifyProgress(ctx, ProgressEvent{Phase: PhaseResolve, Done: len(units), Total: len(units)})
		i.heartbeat(ctx, PhaseResolve)
		trBatch, err := graphstore.BeginBatch(ctx, i.store)
		if err != nil {
			return err
		}
		open = trBatch
		if _, err := i.typeresolvePass(ctx, trBatch, root, units); err != nil {
			return err
		}
		open = nil
		if err := trBatch.Commit(ctx); err != nil {
			return err
		}
		i.notifyProgress(ctx, ProgressEvent{Phase: PhaseCheckpoint, Done: len(units), Total: len(units)})
		// A completed full pass certifies the store for warm starts under the
		// current ingest semantics (see warmstart.go).
		return i.stampSemanticsTx(ctx, tx, root)
	}); err != nil {
		return err
	}
	// WP-05b-2: intra-procedural taint dataflow. Run the pure per-function
	// source→sink analysis over every parsed Go file and persist the complete,
	// canonical findings set to durable store metadata. It runs AFTER the graph
	// transaction commits and writes only metadata (never nodes/edges), so it is
	// deterministic and byte-parity safe — graphstore Snapshot serializes
	// nodes/edges only, never metadata. The taint dispatch adapter reads these
	// back so `graphi analyze taint` surfaces the flows.
	if err := i.analyzeAndPersistIntraProcTaint(ctx, root, parsed); err != nil {
		return err
	}

	// Persist the active profile to durable store metadata after the full pass
	// commits. A zero/empty profile defaults to "balanced" for forward
	// compatibility and is never treated as an error.
	prof := i.profile
	if prof == "" {
		prof = profile.Balanced
	}
	if err := i.store.SetMetadata(ctx, "index.profile", string(prof)); err != nil {
		return fmt.Errorf("ingest: persist profile metadata: %w", err)
	}
	if sqlStore, ok := i.store.(*graphstore.SQLiteStore); ok {
		if err := sqlStore.WALCheckpoint(ctx, "TRUNCATE"); err != nil {
			return fmt.Errorf("ingest: final checkpoint: %w", err)
		}
	}
	if err := i.finishFullPass(ctx, fullPassGeneration); err != nil {
		return err
	}
	i.notifyProgress(ctx, ProgressEvent{Phase: PhaseDone, Done: len(units), Total: len(units)})
	i.notifyIngest(ctx, "ingest-completed", len(units))
	return nil
}

// IngestChanged performs incremental ingestion: it walks root, skips unchanged
// files via the content cache, re-parses changed files, and re-parses direct
// dependents affected by import/symbol changes. It carries no edit provenance;
// callers that originate an edit use IngestChangedWithProvenance.
func (i *Ingester) IngestChanged(ctx context.Context, root string, changed []string) error {
	if err := i.ingestChanged(ctx, root, changed, nil, nil, nil); err != nil {
		return err
	}
	i.notifyIngest(ctx, "ingest-changed", len(changed))
	return nil
}

// IngestChangedWithProgress is IngestChanged with a progress callback for the
// interactive warm-start path: the same incremental pass, but phase and
// per-file events (parse with the current file, link, resolve, done) are
// delivered to prog. Background callers must keep using IngestChanged — the
// callback is scoped to this call, never stored on the Ingester.
func (i *Ingester) IngestChangedWithProgress(ctx context.Context, root string, changed []string, prog func(ProgressEvent)) error {
	if err := i.ingestChanged(ctx, root, changed, nil, nil, prog); err != nil {
		return err
	}
	i.notifyIngest(ctx, "ingest-changed", len(changed))
	return nil
}

// ApplyChangedParsed is the SW-101 serialized merge/apply entry point for the
// parallel parse path. The caller (engine/watch's bounded worker-pool) has
// already PURELY parsed the changed files into isolated ParsedFile results;
// this method applies them through the exact same canonical-path-sorted,
// transactional incremental path as IngestChanged, so the resulting graph is
// byte-identical to a full single-threaded parse regardless of the order the
// pool produced the results in. A precomputed result is only trusted when it
// parsed successfully AND its content hash still matches the bytes re-read from
// disk; otherwise the file is re-parsed serially (so a mid-flight edit or a
// nondeterministic timeout can never leak stale output into the graph).
func (i *Ingester) ApplyChangedParsed(ctx context.Context, root string, changed []string, parsed []*ParsedFile) error {
	pre := make(map[string]*ParsedFile, len(parsed))
	for _, pf := range parsed {
		if pf != nil {
			pre[pf.RelPath] = pf
		}
	}
	if err := i.ingestChanged(ctx, root, changed, nil, pre, nil); err != nil {
		return err
	}
	i.notifyIngest(ctx, "ingest-changed", len(changed))
	return nil
}

// DriftSet walks root and compares each file's on-disk content hash against the
// cached hash, returning the repo-relative paths whose content differs or are
// new (changed) and the cached paths that no longer exist on disk (deleted).
// It is read-only — it mutates neither the graph nor the cache — and is the
// reconcile primitive for the SW-101 watcher's lost-event safety net: feeding
// (changed, deleted) back through ApplyChangedParsed repairs any drift caused by
// a missed filesystem notification. Both slices are returned sorted.
func (i *Ingester) DriftSet(ctx context.Context, root string) (changed, deleted []string, err error) {
	return i.DriftSetWithProgress(ctx, root, nil)
}

// DriftSetWithProgress is DriftSet with a per-file walk callback (running
// count of files checked), so an interactive warm start can animate the
// change scan. The watcher's reconcile path keeps using DriftSet (nil
// callback) and stays silent. changed is the sorted union of DriftDetail's
// Added and Modified sets, byte-identical to the pre-DriftDetail behavior.
func (i *Ingester) DriftSetWithProgress(ctx context.Context, root string, onFile func(checked int)) (changed, deleted []string, err error) {
	d, err := i.DriftDetail(ctx, root, onFile)
	if err != nil {
		return nil, nil, err
	}
	// Preserve the pre-DriftDetail nil-identity: an empty drift returns a nil
	// slice, not an allocated empty one.
	if total := len(d.Added) + len(d.Modified); total > 0 {
		changed = make([]string, 0, total)
		changed = append(append(changed, d.Added...), d.Modified...)
		sort.Strings(changed)
	}
	return changed, d.Deleted, nil
}

// Drift is the classified change set between the on-disk tree and the content
// cache: paths with no cache entry (Added), a differing content hash
// (Modified), and cached paths no longer on disk (Deleted). Each slice is
// sorted.
type Drift struct {
	Added    []string
	Modified []string
	Deleted  []string
}

// Total is the number of drifted paths across all three classes.
func (d Drift) Total() int { return len(d.Added) + len(d.Modified) + len(d.Deleted) }

// DriftDetail is DriftSet with the changed set split into Added vs Modified,
// for user-facing summaries ("3 added, 9 changed, 2 removed"). It shares
// DriftSet's read-only contract: it mutates neither the graph nor the cache,
// so a read-only Ingester (NewReadOnly) may call it freely.
func (i *Ingester) DriftDetail(ctx context.Context, root string, onFile func(checked int)) (Drift, error) {
	units, err := i.walk(root, onFile)
	if err != nil {
		return Drift{}, err
	}
	cached := make(map[string]string)
	rows, err := i.meta.QueryContext(ctx, "SELECT path, content_hash FROM file_content_cache")
	if err != nil {
		return Drift{}, fmt.Errorf("ingest: drift query cache: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p, h string
		if err := rows.Scan(&p, &h); err != nil {
			return Drift{}, fmt.Errorf("ingest: drift scan cache: %w", err)
		}
		cached[p] = h
	}
	if err := rows.Err(); err != nil {
		return Drift{}, err
	}

	var d Drift
	present := make(map[string]struct{}, len(units))
	for _, u := range units {
		present[u.relPath] = struct{}{}
		if h, ok := cached[u.relPath]; !ok {
			d.Added = append(d.Added, u.relPath)
		} else if h != u.hash {
			d.Modified = append(d.Modified, u.relPath)
		}
	}
	for p := range cached {
		if _, ok := present[p]; !ok {
			d.Deleted = append(d.Deleted, p)
		}
	}
	sort.Strings(d.Added)
	sort.Strings(d.Modified)
	sort.Strings(d.Deleted)
	return d, nil
}

// IngestChangedWithProvenance is the provenance-aware incremental ingest entry
// point. It behaves identically to IngestChanged but additionally records the
// supplied edit provenance against every affected NodeId/EdgeId in the
// edit_provenance side-channel, atomically with the Phase-2 cache/clear-dirty
// commit. prov.EditID is minted once and prov.Timestamp captured once by the
// caller (the engine/edit saga); the same value is shared across the whole
// touched set. The provenance is also persisted on the dirty_units row in Phase
// 1 so RecoverWithRoot reproduces identical side-channel state after a crash.
func (i *Ingester) IngestChangedWithProvenance(ctx context.Context, root string, changed []string, prov EditProvenance) error {
	if err := prov.Validate(); err != nil {
		return err
	}
	return i.ingestChanged(ctx, root, changed, &prov, nil, nil)
}

// ingestChanged is the shared core for the zero-provenance and provenance-aware
// entry points. When prov is non-nil the edit context rides Phase 1 (the dirty
// row) and Phase 2 (the edit_provenance side-channel) of the existing two-phase
// dirty-flag protocol, so a crash before Phase-2 commit leaves the file dirty
// AND no provenance recorded, and a crash after leaves both committed — there is
// no window where the graph is updated but provenance is missing/stale.
//
// precomputed, when non-nil, supplies SW-101 pure-parse results keyed by
// canonical relPath. A result is consumed in place of a serial re-parse only
// when it is a trusted success (not skipped, non-nil, and content-hash-matched
// to the freshly walked bytes); the canonical sorted iteration over walked
// units is unchanged, so the parallel path inherits byte-identical determinism.
// prog, when non-nil, receives per-file/phase progress events for THIS pass
// only (the interactive warm-start path); every background caller passes nil,
// so watcher and edit-applier ingests never draw on anyone's terminal.
func (i *Ingester) ingestChanged(ctx context.Context, root string, changed []string, prov *EditProvenance, precomputed map[string]*ParsedFile, prog func(ProgressEvent)) error {
	if err := i.guardReadOnly(); err != nil {
		return err
	}
	i.resetSkips()
	units, err := i.walk(root, nil)
	if err != nil {
		return err
	}
	// Serial re-parse fallbacks below read their bytes on demand (units carry
	// none); one root handle serves the whole pass.
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("ingest: open root: %w", err)
	}
	defer rootHandle.Close()

	// Collect explicitly changed paths + cascade-affected dependents.
	toProcess := make(map[string]struct{})
	for _, c := range changed {
		rp, err := i.sanitizePath(root, c)
		if err != nil {
			return err
		}
		toProcess[rp] = struct{}{}
	}

	// Add dependents of changed files.
	for c := range toProcess {
		deps, err := i.dependentsOf(ctx, c)
		if err != nil {
			return err
		}
		for _, d := range deps {
			toProcess[d] = struct{}{}
		}
	}

	// A go.mod change (edit/add/delete) re-links every linkable file. The
	// module path steers ONLY the typeresolve pass (the heuristic linker
	// resolves via per-file import paths and never reads go.mod), but a
	// confirmed edge that loses its type-check proof must fall back to the
	// heuristic edge for the same logical relation — and that fallback only
	// exists if linkFiles re-put it this pass. Without this expansion the
	// typeresolve sweep would delete the stale confirmed edge outright,
	// diverging from a fresh full index (which would hold the heuristic
	// edge). go.mod edits are rare; the full re-link is the cheap side of
	// that trade.
	if _, ok := toProcess["go.mod"]; ok {
		linkable, err := i.linkableCachedPaths(ctx)
		if err != nil {
			return err
		}
		for _, p := range linkable {
			toProcess[p] = struct{}{}
		}
	}

	// Phase 1: persist dirty flags (with the edit context, if any) in their own
	// transaction so a crash after this point leaves recoverable state that also
	// reproduces the side-channel.
	if err := i.metaTx(ctx, func(tx *sql.Tx) error {
		for _, u := range units {
			if _, ok := toProcess[u.relPath]; !ok {
				continue
			}
			if err := i.markDirtyTx(ctx, tx, u.relPath, prov); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	// Crash-recovery test hook: fault injected after dirty flags are durable but
	// before any graphstore commit.
	if hookErr := i.takeFailHook(); hookErr != nil {
		return hookErr
	}

	// Phase 2: parse, commit to graphstore, update cache/reverse-deps, record edit
	// provenance, clear dirty — all in one meta transaction.
	progTotal := 0
	if prog != nil {
		for _, u := range units {
			if _, ok := toProcess[u.relPath]; ok {
				progTotal++
			}
		}
		prog(ProgressEvent{Phase: PhaseParse, Total: progTotal})
	}
	progDone := 0
	// WP-05b-2: retain the parse result of each reprocessed file so the
	// intra-procedural taint refresh can re-analyze the touched Go files WITHOUT
	// re-parsing (the parser was already invoked once this pass; re-parsing would
	// double the observable parse count the incremental tests assert on).
	parsedResults := make(map[string]*parse.ParseResult)
	if err := i.metaTx(ctx, func(tx *sql.Tx) error {
		// Mirror IngestAll's batched write sessions: one durable graph
		// transaction per phase, committed before each seam where the pass
		// reads its own writes back. `open` guards the error paths.
		var open graphstore.Batch
		defer func() {
			if open != nil {
				_ = open.Rollback()
			}
		}()
		batch, err := graphstore.BeginBatch(ctx, i.store)
		if err != nil {
			return err
		}
		open = batch

		var fileRefs []link.FileRefs
		owned := make(map[string]struct{})
		parserEdges := make(map[string]struct{})
		fwdByFile := make(map[string][]string)
		for _, u := range units {
			if _, ok := toProcess[u.relPath]; !ok {
				continue
			}
			// SW-101: consume a precomputed pure-parse result only when it is a
			// trusted success whose content hash still matches the bytes walk just
			// re-read; otherwise fall back to a serial parse+commit. Either way the
			// commit runs here in this single serialized goroutine, in canonical
			// relPath-sorted order (units is sorted by walk).
			var pf *ParsedFile
			var err error
			if p, ok := precomputed[u.relPath]; ok && p != nil && !p.skipped && p.result != nil && p.Hash == u.hash {
				pf = p
			} else {
				pf, err = i.parseUnit(ctx, rootHandle, u)
				if err != nil {
					return err
				}
			}
			nodeIDs, edgeIDs, fwd, fr, err := i.commitParsed(ctx, batch, u, pf)
			if err != nil {
				return err
			}
			if pf != nil && pf.result != nil {
				parsedResults[u.relPath] = pf.result
			}
			if err := i.upsertCacheTx(ctx, tx, u.relPath, pfHash(pf, u), nodeIDs, fr != nil); err != nil {
				return err
			}
			fwdByFile[u.relPath] = fwd
			for _, id := range nodeIDs {
				owned[id] = struct{}{}
			}
			for _, eid := range edgeIDs {
				parserEdges[eid] = struct{}{}
			}
			if fr != nil {
				fileRefs = append(fileRefs, *fr)
			}
			// Record provenance for every node AND intra-file edge the
			// incremental pass touched (including reverse-dep cascade units),
			// atomically with the cache/clear-dirty commit.
			if prov != nil {
				if err := i.recordEditProvenanceTx(ctx, tx, "node", nodeIDs, *prov); err != nil {
					return err
				}
				if err := i.recordEditProvenanceTx(ctx, tx, "edge", edgeIDs, *prov); err != nil {
					return err
				}
			}
			if err := i.clearDirtyTx(ctx, tx, u.relPath); err != nil {
				return err
			}
			if prog != nil {
				progDone++
				prog(ProgressEvent{Phase: PhaseParse, Done: progDone, Total: progTotal, Path: u.relPath})
			}
		}

		open = nil
		if err := batch.Commit(ctx); err != nil {
			return err
		}

		// Update reverse deps AFTER all reprocessed nodes are committed, so the
		// import-path → directory translation (BLOCK-1) resolves target packages
		// against the full committed node set rather than a partial mid-loop view.
		if len(fwdByFile) > 0 {
			nodes, err := i.store.Nodes(ctx, graphstore.Query{})
			if err != nil {
				return fmt.Errorf("ingest: read nodes for reverse deps: %w", err)
			}
			idx := link.BuildIndex(nodes)
			files := make([]string, 0, len(fwdByFile))
			for f := range fwdByFile {
				files = append(files, f)
			}
			sort.Strings(files) // deterministic update order
			for _, f := range files {
				if err := i.updateReverseDepsTx(ctx, tx, f, reverseDepKeys(idx, fwdByFile[f])); err != nil {
					return err
				}
			}
		}

		// Post-node-commit linker pass (site 2): re-resolve cross-file edges for
		// the reprocessed files against the full committed node set, removing
		// stale from-owned cross-file edges first. The cascade closure
		// (dependentsOf) guarantees every file whose edges could change is in the
		// reprocessed set, so the incremental result converges with a full pass.
		if prog != nil {
			prog(ProgressEvent{Phase: PhaseLink, Done: progDone, Total: progTotal})
		}
		linkBatch, err := graphstore.BeginBatch(ctx, i.store)
		if err != nil {
			return err
		}
		open = linkBatch
		// WP-02: the incremental path threads its own scoped `prog` (nil for
		// background passes), so linkFiles emits climbing-Done link progress on the
		// warm-start path too without ever leaking through the stored callback.
		linkEdgeIDs, err := i.linkFiles(ctx, linkBatch, fileRefs, owned, parserEdges, prog)
		if err != nil {
			return err
		}
		// Funnel the linker's cross-file edge IDs into the edit-provenance
		// side-channel so an incremental edit records provenance for them too.
		if prov != nil {
			if err := i.recordEditProvenanceTx(ctx, tx, "edge", linkEdgeIDs, *prov); err != nil {
				return err
			}
		}

		// Remove cache entries for files that no longer exist.
		present := make(map[string]struct{}, len(units))
		for _, u := range units {
			present[u.relPath] = struct{}{}
		}
		cached, err := i.cachedPathsTx(ctx, tx)
		if err != nil {
			return err
		}
		for _, p := range cached {
			if _, ok := present[p]; ok {
				continue
			}
			if err := i.removeFileTx(ctx, tx, linkBatch, p); err != nil {
				return err
			}
		}
		open = nil
		if err := linkBatch.Commit(ctx); err != nil {
			return err
		}

		// WP-03 (Vector 2c): reap any interned external node left with zero incident
		// edges by this pass — e.g. a file that was the SOLE referencer of a stdlib
		// symbol got edited or deleted, cascade-removing its last edge. This runs
		// over the now-committed graph (the batch above was write-only, so the true
		// post-cascade edge set is only observable post-commit) and keeps the
		// incremental store byte-identical to a full re-index of the same state.
		if err := i.sweepOrphanExternalNodes(ctx); err != nil {
			return err
		}

		// A parse/syntax error makes parseUnit fail closed with a SkipParseError
		// diagnostic and continue — correct for a FULL index (IngestAll), where a
		// single malformed PRE-EXISTING file must not sink the whole repo. But the
		// incremental path only ever (re)parses files it was explicitly asked to
		// process (the changed set + its reverse-dep cascade, toProcess). If one of
		// THOSE is now unparseable, silently committing this transaction would leave
		// the meta cache (0 nodes / clean) out of sync with the graphstore (the
		// skipped commit deletes no stale nodes) — and, in the edit saga, the
		// post-commit compensate restores the graphstore snapshot but NOT the meta
		// DB, permanently poisoning it. So elevate a SkipParseError of any processed
		// file to a hard error HERE, inside the Phase-2 metaTx, so the transaction
		// rolls back atomically and the meta DB is left untouched (exactly the
		// pre-tolerance behavior). The edit applier's existing re-index-error path
		// then compensates disk + graphstore; the file stays dirty for later retry.
		i.skipMu.Lock()
		for _, s := range i.skipped {
			if s.Reason != SkipParseError {
				continue
			}
			if _, ok := toProcess[s.Path]; ok {
				i.skipMu.Unlock()
				return fmt.Errorf("ingest: reprocessed file %s failed to parse (%s)", s.Path, s.Reason)
			}
		}
		i.skipMu.Unlock()

		// Third phase (site 2): whole-repo go/types confirmed-tier pass, after
		// the linker and after the stale-file cleanup so it sees the final
		// committed node set. Runs only when the change set can affect Go
		// resolution — a pure asset/doc edit skips the whole-repo recompute.
		// Placed after the parse-error elevation above so a doomed transaction
		// never pays for (or half-applies) the pass.
		if touchesGoResolution(toProcess) {
			if prog != nil {
				prog(ProgressEvent{Phase: PhaseResolve, Done: progDone, Total: progTotal})
			}
			trBatch, err := graphstore.BeginBatch(ctx, i.store)
			if err != nil {
				return err
			}
			open = trBatch
			trIDs, err := i.typeresolvePass(ctx, trBatch, root, units)
			if err != nil {
				return err
			}
			open = nil
			if err := trBatch.Commit(ctx); err != nil {
				return err
			}
			if prov != nil {
				if err := i.recordEditProvenanceTx(ctx, tx, "edge", trIDs, *prov); err != nil {
					return err
				}
			}
		}
		// Done only on the success path — a failed transaction must never
		// report completion (mirrors the full-ingest contract).
		if prog != nil {
			prog(ProgressEvent{Phase: PhaseDone, Done: progDone, Total: progTotal})
		}
		return nil
	}); err != nil {
		return err
	}
	// WP-05b-2: refresh the persisted intra-procedural taint findings after the
	// incremental commit. Findings of the reprocessed (and any now-deleted) Go
	// files are recomputed and merged with the retained findings of untouched
	// files, so the persisted set converges with a full re-index. Metadata-only
	// (never nodes/edges) → byte-parity safe.
	return i.refreshIntraProcTaint(ctx, root, toProcess, parsedResults)
}
