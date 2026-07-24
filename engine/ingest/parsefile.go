package ingest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/link"
)

// parseAndCommit parses one file, writes its nodes/intra-file edges to
// graphstore, and returns the node IDs, the edge IDs it committed (the
// side-channel edge key set), the list of files it references (forward refs),
// and the deferred link inputs (pending refs + imports) for the post-node-commit
// linker pass. Cross-file edges are NOT emitted here — they are emitted by
// linkFiles after every file's nodes are committed (the ordering constraint that
// motivated SW-050).
// ParsedFile is the isolated, immutable output of the SW-101 PURE parse phase
// for one file: the canonical repo-relative path, the deterministic content hash
// of the bytes that were parsed, and the parse result (nil when the file was
// skipped fail-closed). It carries NO graphstore handle and shares no mutable
// state, so a bounded worker-pool may compute many ParsedFiles in parallel
// (parse.Parser is contractually concurrency-safe and deterministic for
// identical input). The serialized merge/apply phase (ApplyChangedParsed) then
// consumes these in canonical path-sorted order, confining all scheduling
// nondeterminism to the parse phase where it cannot reach the graph.
type ParsedFile struct {
	// RelPath is the canonical repo-relative POSIX path of the parsed file.
	RelPath string
	// Hash is hashBytes() of the source the worker parsed. The serialized apply
	// only trusts a precomputed result when this matches the bytes it re-reads
	// from disk, so a mid-flight edit can never commit stale parse output.
	Hash string
	// result is the pure parse output. nil when skipped is true.
	result *parse.ParseResult
	// skipped reports a fail-closed resource-bound breach (oversize/timeout/depth).
	// A precomputed skip is NEVER trusted by the apply (timeout is wall-clock
	// nondeterministic); it forces a serial re-parse so output stays
	// byte-identical to a full single-threaded parse.
	skipped bool
}

// ParseFile reads and PURELY parses the file at repo-relative relPath under root
// and returns an isolated ParsedFile. It mutates no graph state, so it is safe
// to call concurrently from the SW-101 bounded worker-pool. It honors the same
// path sanitization and fail-closed resource bounds as the serial path, so the
// parallel parse set is faithful to the ingestable set.
func (i *Ingester) ParseFile(ctx context.Context, root, relPath string) (*ParsedFile, error) {
	rel, err := i.sanitizePath(root, relPath)
	if err != nil {
		return nil, err
	}
	// walk() prunes ignoredDirNames via filepath.SkipDir and never descends into
	// them, so a full index never reaches here for a node_modules/.git/vendor/...
	// path. The watcher has no walk to prune, though — an fsnotify event for a
	// changed file under one of those directories (which churns constantly during
	// a package-manager install) would otherwise still be read, parsed, and
	// tracked, reintroducing the exact noise/cost the pruning exists to avoid.
	// Mirrors the untracked-file-type contract just below: (nil, nil) = ignore.
	if pathHasIgnoredDir(rel) {
		return nil, nil
	}
	// Opt-in index scope: the watcher must agree with the walk or a watched
	// edit would re-introduce a file the full pass excludes (parity break).
	scope, err := i.ignoreConfigFor(root)
	if err != nil {
		return nil, err
	}
	if scope.active() && scope.ignoreFile(rel) {
		return nil, nil
	}
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("ingest: open root: %w", err)
	}
	defer rootHandle.Close()
	read := readRootedRegularFile(rootHandle, rel, i.bounds.MaxFileSize)
	if read.reason != "" {
		i.recordSkip(SkipDiagnostic{Path: rel, Reason: read.reason, Size: read.size})
		return &ParsedFile{RelPath: rel, skipped: true}, nil
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	u := fileUnit{path: abs, relPath: rel, src: read.src, hash: hashBytes(read.src)}
	pf, err := i.parseUnit(ctx, u)
	if err != nil {
		return nil, err
	}
	return pf, nil
}

// parseUnit is the PURE parse phase for one already-read file unit: it calls the
// language parser and applies the fail-closed resource-bound skip policy, but
// performs NO graphstore mutation. It is safe for concurrent use. commitParsed
// is its serialized counterpart.
func (i *Ingester) parseUnit(ctx context.Context, u fileUnit) (*ParsedFile, error) {
	// SW-055 AC#6: fail-closed parse timeout. Bound the wall-clock time a single
	// Parse may consume on untrusted input; on expiry the parse is abandoned.
	parseCtx := ctx
	if i.bounds.ParseTimeout > 0 {
		var cancel context.CancelFunc
		parseCtx, cancel = context.WithTimeout(ctx, time.Duration(i.bounds.ParseTimeout))
		defer cancel()
	}
	res, err := i.parser.Parse(parseCtx, u.relPath, u.src)
	if err != nil {
		// Fail closed on a resource-bound breach: SKIP the file with a structured,
		// source-free diagnostic and continue ingestion (never parse-anyway / never
		// truncate). The four sentinels below route to that skip path; a genuine
		// parse/syntax error routes to SkipParseError further down (also a skip).
		// Only a parent-context cancellation/deadline aborts the whole pass.
		switch {
		case errors.Is(err, parse.ErrMaxDepthExceeded):
			i.recordSkip(SkipDiagnostic{Path: u.relPath, Reason: SkipMaxDepth})
			return &ParsedFile{RelPath: u.relPath, Hash: u.hash, skipped: true}, nil
		case errors.Is(err, parse.ErrParseTimeout) ||
			(i.bounds.ParseTimeout > 0 && parseCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil):
			i.recordSkip(SkipDiagnostic{Path: u.relPath, Reason: SkipTimeout})
			return &ParsedFile{RelPath: u.relPath, Hash: u.hash, skipped: true}, nil
		case errors.Is(err, parse.ErrFileTooLarge):
			i.recordSkip(SkipDiagnostic{Path: u.relPath, Reason: SkipOversize})
			return &ParsedFile{RelPath: u.relPath, Hash: u.hash, skipped: true}, nil
		case errors.Is(err, parse.ErrNoParser):
			// A file with no registered parser (a macOS .DS_Store, an image, a
			// font, a lockfile, any binary or unrecognized-extension asset — the
			// overwhelming majority of non-source files in a typical repo) is
			// simply not source code, not a resource-bound breach. This is the
			// expected, common case, not a diagnostic-worthy event: no
			// recordSkip, just silently untracked. Previously this fell through
			// to the hard-error return below and aborted indexing of the ENTIRE
			// repo the moment it hit a single such file — which is effectively
			// guaranteed on any real-world repo.
			return &ParsedFile{RelPath: u.relPath, Hash: u.hash, skipped: true}, nil
		}
		// Context cancellation / deadline on the PARENT ctx is a real abort signal
		// (a user interrupt, or an overall ingest deadline) — not a per-file
		// defect. It must stop the whole pass, never be swallowed as a per-file
		// skip. (The parse-scoped timeout, whose parent ctx is still live, is
		// already routed to SkipTimeout by the case above.)
		if ctx.Err() != nil {
			return nil, fmt.Errorf("ingest: parse %s: %w", u.relPath, err)
		}
		// Any remaining error is a genuine parse/syntax error: the file has a
		// registered parser but is not valid source for it (e.g. a WireMock
		// __files response body using Handlebars templating, which is not valid
		// strict JSON). Fail closed exactly like the resource-bound sentinels
		// above — SKIP this one file with a structured, source-free diagnostic and
		// continue indexing the rest of the repo. Previously this aborted the
		// ENTIRE ingest the moment it hit a single malformed file.
		i.recordSkip(SkipDiagnostic{Path: u.relPath, Reason: SkipParseError})
		return &ParsedFile{RelPath: u.relPath, Hash: u.hash, skipped: true}, nil
	}
	// Extraction already ran inside the parser: Nodes/Edges/PendingRefs/
	// Imports/References are complete without the AST. Past this point the
	// only consumer of res.Root in this package is parse.GoAST (the taint
	// pass), so every non-Go backend handle — a tree-sitter tree is routinely
	// 10-40x its source size — is dead weight. Dropping it here frees each
	// tree as soon as its parse finishes; retaining it made a full pass hold
	// every file's tree simultaneously until the END of the pipeline, which
	// on large polyglot workspaces alone reached tens of GB of peak RSS.
	if _, _, ok := parse.GoAST(res); !ok {
		res.Root = nil
	}
	return &ParsedFile{RelPath: u.relPath, Hash: u.hash, result: res}, nil
}

// parseAndCommit parses one file and commits it serially (parse + apply fused),
// preserving the original IngestAll / ingestChanged behavior. The SW-101 parallel
// path instead splits these: parseUnit/ParseFile (pure, poolable) then
// commitParsed (serialized, canonical order).
func (i *Ingester) parseAndCommit(ctx context.Context, w graphstore.Writer, u fileUnit) ([]string, []string, []string, *link.FileRefs, error) {
	pf, err := i.parseUnit(ctx, u)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return i.commitParsed(ctx, w, u, pf)
}

// commitParsed is the SERIALIZED apply phase: it writes the (pre)computed parse
// result for u into the graphstore. It is the sole authority that mutates the
// graph and is only ever called from the single serialized merge goroutine, so
// node/edge insertion order is governed by the caller's canonical path-sorted
// walk — never by worker scheduling. A nil/skipped ParsedFile commits nothing
// (matching the original early-return skip behavior).
func (i *Ingester) commitParsed(ctx context.Context, w graphstore.Writer, u fileUnit, pf *ParsedFile) ([]string, []string, []string, *link.FileRefs, error) {
	if pf == nil || pf.skipped || pf.result == nil {
		return nil, nil, nil, nil, nil
	}
	res := pf.result

	// Remove old nodes for this file before inserting the new parse output. As of
	// SW-036 the graphstore exposes an explicit delete API, so the orphan debt
	// SW-035 documented here is closed: any node whose identity changed
	// (rename/move/signature-change all mint a new NodeId because identity is
	// xxhash64(Kind,QualifiedName,SourcePath)) is dropped along with its incident
	// edges, so the incremental re-index converges byte-for-byte with a full
	// re-index. An identity-PRESERVING edit deletes then re-PutNodes the same ID,
	// which is harmless; computing the new-id set first lets us skip those.
	oldIDs, err := i.cachedNodeIDs(ctx, u.relPath)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	nodeIDs := make([]string, 0, len(res.Nodes))
	newIDs := make(map[string]struct{}, len(res.Nodes))
	for _, n := range res.Nodes {
		newIDs[string(n.ID())] = struct{}{}
	}
	// Delete any previously-committed node for this file whose identity is NOT
	// reproduced by the new parse. DeleteNode cascades incident edges, so stale
	// edges anchored on a removed node can never be orphaned.
	for _, id := range oldIDs {
		if _, kept := newIDs[id]; kept {
			continue
		}
		// WP-01 interned-node lifecycle: an interned `package` node is minted by
		// EVERY file in the package with the same NodeId, so a file dropping it
		// (e.g. its package declaration changed) must NOT delete the node while a
		// sibling file still declares it. This guard is a strict no-op for the
		// per-file symbol/file nodes (whose NodeId embeds the unique source path,
		// so no other cache row references them) and only protects shared nodes.
		shared, err := i.nodeReferencedByOtherFile(ctx, i.meta, u.relPath, id)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		if shared {
			continue
		}
		if err := w.DeleteNode(ctx, model.NodeId(id)); err != nil {
			return nil, nil, nil, nil, fmt.Errorf("ingest: delete stale node %s: %w", id, err)
		}
	}

	for _, n := range res.Nodes {
		if err := w.PutNode(ctx, n); err != nil {
			return nil, nil, nil, nil, fmt.Errorf("ingest: put node: %w", err)
		}
		nodeIDs = append(nodeIDs, string(n.ID()))
	}
	edgeIDs := make([]string, 0, len(res.Edges))
	for _, e := range res.Edges {
		if err := w.PutEdge(ctx, e); err != nil {
			return nil, nil, nil, nil, fmt.Errorf("ingest: put edge: %w", err)
		}
		edgeIDs = append(edgeIDs, string(e.ID()))
	}

	// Capture the linker inputs for the post-node-commit pass. They are non-nil
	// only when the parser recorded deferred refs/imports (the real Go parser);
	// the stub parsers leave them empty, so the linker is a no-op for them.
	var fr *link.FileRefs
	if len(res.PendingRefs) > 0 || len(res.Imports) > 0 {
		fr = &link.FileRefs{
			SourcePath: model.NormalizePath(u.relPath),
			Dir:        posixDir(model.NormalizePath(u.relPath)),
			Language:   res.Meta.Language,
			Pending:    res.PendingRefs,
			Imports:    res.Imports,
		}
	}

	// Forward refs = paths this file imports/uses. For the stub parser this is
	// supplied in the parse result; a real parser derives it from imports.
	return nodeIDs, edgeIDs, res.References, fr, nil
}

// posixDir returns the directory portion of a normalized POSIX path; the repo
// root maps to "" (mirrors engine/link's directory key).
func posixDir(p string) string {
	d := filepath.ToSlash(filepath.Dir(p))
	if d == "." {
		return ""
	}
	return d
}
