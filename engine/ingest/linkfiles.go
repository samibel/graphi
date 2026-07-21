package ingest

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/profile"
	"github.com/samibel/graphi/engine/link"
)

// linkFiles is the post-node-commit linker pass (SW-050). It is called once per
// ingest run AFTER every (re)processed file's nodes are committed, so the
// ordering constraint that previously dropped cross-file edges no longer applies:
// the symbol index is built from the FULL committed node set (store.Nodes) and
// the linker resolves every deferred ref against it.
//
// fileRefs are the link inputs of the (re)processed files. ownedNodeIDs is the
// set of node IDs belonging to those files. parserEdges is the set of edge IDs
// parseAndCommit just (re)committed for those files THIS pass (its res.Edges:
// defines + any edge a parser resolves itself, including cross-file edges some
// parsers emit directly); they are current-by-construction and must be kept.
//
// The sweep removes STALE from-owned linker-kind edges before re-linking: a
// calls/references/imports edge whose From is owned but which was NOT
// (re)committed by parseAndCommit this pass is deleted, then the linker re-emits
// the still-valid ones. Deleting even when To is also owned (BLOCK-2) is required
// because an identity-preserving caller edit keeps the From NodeId, so
// DeleteNode's incident-edge cascade never fires and the stale edge would
// otherwise survive incrementally while being absent from a full pass. Keying the
// keep-set on the freshly-committed parser edges (rather than on To-ownership)
// preserves intra-file AND parser-emitted cross-file edges — only the linker's
// own deferred edges, which the linker re-emits, are swept. This makes the
// incremental result converge byte-identically with a full re-index.
//
// It returns the committed cross-file edge IDs so the incremental path can record
// edit provenance for them.
// linkProgressBatchSize bounds how many files each linker Link call processes
// before linkFiles emits a PhaseLink progress event (WP-02). Grouping/iteration
// order does not affect the committed edge set — construct merges intents by
// content-derived (from,to,kind) and every edge's From is owned by exactly one
// source file, so sub-batching the per-language file list yields a byte-identical
// node/edge set while turning the link phase from one silent block into a stream
// of climbing-Done events on a large repo.
const linkProgressBatchSize = 64

func (i *Ingester) linkFiles(ctx context.Context, w graphstore.Writer, fileRefs []link.FileRefs, ownedNodeIDs map[string]struct{}, parserEdges map[string]struct{}, progress func(ProgressEvent)) ([]string, error) {
	// Nothing reprocessed: no nodes to sweep stale edges from and nothing to
	// re-link. (BLOCK-2: gating on ownedNodeIDs, NOT fileRefs — an edit that
	// removes the LAST cross-file ref leaves fileRefs empty yet still owns
	// reprocessed nodes whose stale from-owned cross-file edges must be swept.
	// Returning early on empty fileRefs skipped that sweep and let the stale edge
	// survive.)
	if len(ownedNodeIDs) == 0 {
		return nil, nil
	}

	nodes, err := i.store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return nil, fmt.Errorf("ingest: link read nodes: %w", err)
	}

	// Remove stale from-owned linker edges before re-linking.
	// WP-08 (deferred): decouple this stale-edge sweep from the full-edge read —
	// scanning every committed edge to find from-owned ones is O(edges) per pass.
	allEdges, err := i.store.Edges(ctx, graphstore.Query{})
	if err != nil {
		return nil, fmt.Errorf("ingest: link read edges: %w", err)
	}
	for _, e := range allEdges {
		if _, fromOwned := ownedNodeIDs[string(e.From())]; !fromOwned {
			continue
		}
		// Only the linker's own edge kinds are swept here.
		if e.Kind() != "calls" && e.Kind() != "references" && e.Kind() != "imports" &&
			e.Kind() != "implements" && e.Kind() != "inherits" && e.Kind() != "overrides" {
			continue
		}
		// Keep any edge parseAndCommit just (re)committed for these files this pass
		// — it is current, not stale. This covers intra-file edges AND any
		// cross-file edge a parser resolves itself (res.Edges). Everything else
		// from-owned of a linker kind is a stale linker edge from a prior pass.
		if _, fresh := parserEdges[string(e.ID())]; fresh {
			continue
		}
		if err := w.DeleteEdge(ctx, e.ID()); err != nil {
			return nil, fmt.Errorf("ingest: delete stale cross-file edge %s: %w", e.ID(), err)
		}
	}

	idx := link.BuildIndex(nodes)

	// FU-5: dispatch the linker per language. Group the (re)processed files by
	// their Language and call Link once per language against the SHARED index
	// (cross-file resolution is intra-language, but the index spans the whole
	// committed node set). Languages are visited in sorted order and edges are
	// keyed by content-derived EdgeId in the store, so the result is independent
	// of grouping/iteration order. A file whose Language has no registered resolver
	// is a no-op (Link returns no edges), exactly as before for non-Go files.
	byLang := map[string][]link.FileRefs{}
	for _, fr := range fileRefs {
		lang := fr.Language
		if lang == "" {
			lang = "go" // FU-1 default: untagged refs are Go (back-compat).
		}
		byLang[lang] = append(byLang[lang], fr)
	}
	langs := make([]string, 0, len(byLang))
	for lang := range byLang {
		langs = append(langs, lang)
	}
	sort.Strings(langs)

	edgeIDs := make([]string, 0)
	var importEdges []model.Edge
	// WP-03: interned external nodes minted by the linker (Go stdlib / 3rd-party
	// call/ref targets) are committed BEFORE their incident edges so the batch's
	// endpoint check (ErrUnknownEdgeEndpoint) passes. Any node that ends up
	// unreferenced (a file dropping its last reference to it) is reaped by the
	// standalone post-commit sweepOrphanExternalNodes pass. Stats are aggregated
	// for the ingest summary.
	var linkStats link.Stats
	// WP-02: link progress. Total is the honest count of files with cross-file
	// refs (the files the linker actually processes); Done climbs by batch as each
	// sub-group of files is resolved and committed, so a large repo shows real
	// link progress instead of a single event bracketed by clock heartbeats.
	total := len(fileRefs)
	done := 0
	// commit resolves and commits ONE sub-batch of files. Splitting a language's
	// files into batches is byte-safe: construct keys edges by content-derived
	// (from,to,kind), every edge's From is owned by exactly one file (so no edge
	// crosses a batch boundary), and minted external nodes upsert idempotently.
	commit := func(lang string, files []link.FileRefs) error {
		extNodes, edges, st, err := i.linker.Link(lang, files, idx)
		if err != nil {
			return fmt.Errorf("ingest: link %s: %w", lang, err)
		}
		linkStats.ResolvedDerived += st.ResolvedDerived
		linkStats.ResolvedHeuristic += st.ResolvedHeuristic
		linkStats.ResolvedExternal += st.ResolvedExternal
		linkStats.Skipped += st.Skipped
		linkStats.Ambiguous += st.Ambiguous
		for _, n := range extNodes {
			if err := w.PutNode(ctx, n); err != nil {
				return fmt.Errorf("ingest: link put external node %s: %w", n.ID(), err)
			}
		}
		for _, e := range edges {
			// Fast mode drops low-value import-fanout edges while preserving
			// core navigable edges (calls, references, hierarchy edges).
			if i.profile == profile.Fast && e.Kind() == "imports" {
				continue
			}
			// Balanced mode aggregates external imports by target package.
			if i.profile == profile.Balanced && e.Kind() == "imports" {
				if path, ok := importPathFromReason(e.Reason()); ok && isExternalImport(path) {
					importEdges = append(importEdges, e)
					continue
				}
			}
			// WP-08 (deferred): chunk this edge commit into ~50k-edge durable
			// transactions instead of accumulating the whole pass in one batch.
			if err := w.PutEdge(ctx, e); err != nil {
				return fmt.Errorf("ingest: link put edge %s: %w", e.ID(), err)
			}
			edgeIDs = append(edgeIDs, string(e.ID()))
		}
		return nil
	}
	for _, lang := range langs {
		files := byLang[lang]
		for start := 0; start < len(files); start += linkProgressBatchSize {
			end := start + linkProgressBatchSize
			if end > len(files) {
				end = len(files)
			}
			if err := commit(lang, files[start:end]); err != nil {
				return nil, err
			}
			done += end - start
			if progress != nil {
				progress(ProgressEvent{Phase: PhaseLink, Done: done, Total: total})
			}
		}
	}

	// Aggregate external imports in balanced mode by target package.
	if i.profile == profile.Balanced && len(importEdges) > 0 {
		groups := aggregateImportsByTarget(importEdges)
		for _, e := range groups {
			if err := w.PutEdge(ctx, e); err != nil {
				return nil, fmt.Errorf("ingest: link put aggregated edge %s: %w", e.ID(), err)
			}
			edgeIDs = append(edgeIDs, string(e.ID()))
		}
	}

	i.lastLinkStats = linkStats
	return edgeIDs, nil
}

// sweepOrphanExternalNodes deletes every interned external node (WP-03) that has
// ZERO incident edges in the COMMITTED graph. External nodes are linker artifacts
// reachable only via their incident calls/references edges and are NOT recorded in
// any file's node_ids, so neither the per-file stale-node purge nor the removed-
// file purge (removeFileTx) ever deletes them: a file that was the SOLE referencer
// of an external symbol (edited to stop referencing it, or deleted outright) leaves
// a zero-edge ghost external node that a full re-index would never mint — a
// byte-parity divergence (Vector 2c).
//
// This runs as a STANDALONE pass over the committed store AFTER the main link/
// delete batch commits. It must be post-commit because the graphstore batch is
// write-only: the true post-pass edge set — including the incident edges that
// removeFileTx cascade-deletes when it removes a file's nodes — is only observable
// once committed. It is NOT gated on any reprocessed-node set, so it fires even for
// a pure single-file deletion (which reprocesses no nodes and so short-circuits
// linkFiles). A shared external node is protected by construction: any other file
// that still references it keeps a surviving incident edge, so it is not an orphan.
func (i *Ingester) sweepOrphanExternalNodes(ctx context.Context) error {
	nodes, err := i.store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return fmt.Errorf("ingest: orphan sweep read nodes: %w", err)
	}
	external := map[model.NodeId]struct{}{}
	for _, n := range nodes {
		if n.Kind() == "external" {
			external[n.ID()] = struct{}{}
		}
	}
	if len(external) == 0 {
		return nil
	}
	edges, err := i.store.Edges(ctx, graphstore.Query{})
	if err != nil {
		return fmt.Errorf("ingest: orphan sweep read edges: %w", err)
	}
	// Any external node incident to any edge (as From or To — external nodes are
	// terminal, so in practice only To) is live; remove it from the orphan set.
	for _, e := range edges {
		delete(external, e.To())
		delete(external, e.From())
	}
	if len(external) == 0 {
		return nil
	}
	orphans := make([]model.NodeId, 0, len(external))
	for id := range external {
		orphans = append(orphans, id)
	}
	sort.Slice(orphans, func(a, b int) bool { return orphans[a] < orphans[b] })

	batch, err := graphstore.BeginBatch(ctx, i.store)
	if err != nil {
		return fmt.Errorf("ingest: orphan sweep begin batch: %w", err)
	}
	for _, id := range orphans {
		if err := batch.DeleteNode(ctx, id); err != nil {
			_ = batch.Rollback()
			return fmt.Errorf("ingest: sweep orphan external node %s: %w", id, err)
		}
	}
	return batch.Commit(ctx)
}

// importPathFromReason extracts the import path from a linker import edge reason.
func importPathFromReason(reason string) (string, bool) {
	const prefix = "file imports package "
	if idx := strings.LastIndex(reason, prefix); idx >= 0 {
		return reason[idx+len(prefix):], true
	}
	return "", false
}

// isExternalImport reports whether path looks like an external (vendored or
// third-party) import path rather than a same-repo module path. The heuristic
// is: any import path containing a dot in its first segment is treated as
// external (e.g. github.com/..., example.com/..., golang.org/...).
func isExternalImport(path string) bool {
	if path == "" {
		return false
	}
	first := path
	if i := strings.Index(first, "/"); i >= 0 {
		first = first[:i]
	}
	return strings.Contains(first, ".")
}

// aggregateImportsByTarget groups external import edges by their target node and
// returns one aggregated edge per target, preserving the target and the first
// source while summarizing the count in the reason. The returned slice is
// sorted by EdgeId for deterministic iteration.
func aggregateImportsByTarget(edges []model.Edge) []model.Edge {
	groups := map[model.NodeId][]model.Edge{}
	for _, e := range edges {
		groups[e.To()] = append(groups[e.To()], e)
	}
	var out []model.Edge
	for _, group := range groups {
		if len(group) == 1 {
			out = append(out, group[0])
			continue
		}
		// Use the first edge as the representative source; all edges share
		// the same kind, so the aggregated edge points from that source to the target.
		rep := group[0]
		var evidence []string
		for _, e := range group {
			evidence = append(evidence, e.Evidence()...)
		}
		reason := fmt.Sprintf("aggregated %d imports of %s", len(group), rep.Reason())
		agg, err := model.NewEdge(rep.From(), rep.To(), rep.Kind(), rep.Tier(), rep.Confidence(), reason, graphstore.CompactEvidence(evidence))
		if err != nil {
			// Deterministic edge construction should never fail for these inputs;
			// fall back to the representative edge so indexing does not abort.
			out = append(out, rep)
			continue
		}
		out = append(out, agg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}
