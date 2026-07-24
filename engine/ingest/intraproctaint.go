package ingest

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/analysis/intraproctaint"
	"github.com/samibel/graphi/engine/analysis/taint"
)

// intraProcTaintConfig is the taint source/sink/sanitizer config the ingest
// pipeline runs the intra-procedural dataflow with. It is the built-in default
// merged with the repository's optional <root>/.graphi/taint.json (WP-09), so a
// project's custom sources/sinks/sanitizers shape the ingested findings that a
// `graphi analyze taint` run surfaces. A malformed config file fails the pass
// closed rather than silently reverting to defaults.
func intraProcTaintConfig(root string) (taint.Config, error) { return taint.LoadConfig(root) }

// analyzeParsedTaint runs the pure per-function intra-procedural taint
// analysis for ONE full-pass ParsedFile and then releases its Go AST: past
// this point the analysis was the only full-pass consumer of a Go result's
// Root (see parseUnit's non-Go release), so retaining the go/ast+FileSet any
// longer only held the whole repo's Go forest resident until the end-of-pass
// persist. Called from the parse drain (calling goroutine, serial), bounding
// live Go ASTs to the pool width. Non-Go and skipped results are no-ops.
func (i *Ingester) analyzeParsedTaint(cfg taint.Config, pf *ParsedFile) {
	if pf == nil || pf.result == nil {
		return
	}
	file, fset, ok := parse.GoAST(pf.result)
	if !ok {
		return
	}
	pf.taint = intraproctaint.Analyze(file, fset, cfg)
	parse.ReleaseRoot(pf.result)
}

// analyzeAndPersistIntraProcTaint replaces the persisted intra-procedural
// taint findings of a FULL pass with the complete, canonical set concatenated
// from the per-file findings the parse drain computed (analyzeParsedTaint).
// The output is a pure function of the parsed file contents (deterministic:
// per-file findings are canonically ordered and Encode re-sorts the union)
// and writes ONLY graphstore metadata, so it never perturbs the node/edge
// graph (byte-parity safe: Snapshot omits metadata). cfgErr — the config load
// error captured BEFORE the parse phase — is surfaced here, after the graph
// transaction commits, preserving the exact failure point of the old
// load-at-the-end behavior (a malformed .graphi/taint.json fails the pass
// closed without rolling back the committed graph).
//
// analyzedHash is the ContentHash of the config snapshot the drain analyzed
// with. The config is re-loaded HERE — the point the old code loaded it, and
// moments after stampSemanticsTx certified the store against the config's
// current on-disk state — and a mismatch fails the pass closed: persisting
// findings computed under a config that no longer matches the semantics stamp
// would certify stale findings. The failed pass leaves the full-pass recovery
// marker open, so the next run re-indexes under the new config consistently.
func (i *Ingester) analyzeAndPersistIntraProcTaint(ctx context.Context, root string, cfgErr error, analyzedHash string, parsed []*ParsedFile) error {
	if cfgErr != nil {
		return cfgErr
	}
	cfg, err := intraProcTaintConfig(root)
	if err != nil {
		return err
	}
	if cfg.ContentHash != analyzedHash {
		return fmt.Errorf("ingest: taint config changed during the pass; re-run to index under the new config")
	}
	var findings []taint.Finding
	for _, pf := range parsed {
		if pf == nil {
			continue
		}
		findings = append(findings, pf.taint...)
	}
	return i.storeIntraProcTaint(ctx, findings)
}

// refreshIntraProcTaint updates the persisted intra-procedural taint findings
// after an incremental pass. Findings of untouched files that still exist on
// disk are retained; findings of reprocessed or deleted files are dropped; and
// the reprocessed files' ALREADY-PARSED results (parsedResults, produced this
// pass — never re-parsed here) are re-analyzed and merged in. The result
// converges with a full re-index of the same source state. Metadata-only
// (byte-parity safe).
func (i *Ingester) refreshIntraProcTaint(ctx context.Context, root string, reprocessed map[string]struct{}, parsedResults map[string]*parse.ParseResult) error {
	prev, err := i.store.Metadata(ctx, intraproctaint.MetadataKey)
	if err != nil && !errors.Is(err, graphstore.ErrNotFound) {
		return fmt.Errorf("ingest: read intra-proc taint metadata: %w", err)
	}
	existing, err := intraproctaint.Decode(prev)
	if err != nil {
		return err
	}

	cfg, err := intraProcTaintConfig(root)
	if err != nil {
		return err
	}
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("ingest: open root for intra-proc taint refresh: %w", err)
	}
	defer rootHandle.Close()
	out := make([]taint.Finding, 0, len(existing))
	for _, f := range existing {
		file := findingFile(f)
		if file == "" {
			continue // unattributable finding: drop rather than keep stale
		}
		if _, hit := reprocessed[file]; hit {
			continue // recomputed below from the retained parse result
		}
		info, statErr := rootHandle.Lstat(file)
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			// Missing, escaped, final-symlink, and non-regular paths cannot own a
			// current source finding. Drop rather than retaining stale metadata.
			continue
		}
		out = append(out, f)
	}

	// Re-analyze the reprocessed files from the parse results the commit already
	// produced. A reprocessed file with no retained result (skipped, deleted, or
	// non-source) simply contributes no findings — its stale findings were
	// dropped above and are not re-added.
	for relPath := range reprocessed {
		res, ok := parsedResults[relPath]
		if !ok || res == nil {
			continue
		}
		file, fset, ok := parse.GoAST(res)
		if !ok {
			continue
		}
		out = append(out, intraproctaint.Analyze(file, fset, cfg)...)
	}
	return i.storeIntraProcTaint(ctx, out)
}

// storeIntraProcTaint canonically encodes findings and persists them under the
// intra-proc taint metadata key.
func (i *Ingester) storeIntraProcTaint(ctx context.Context, findings []taint.Finding) error {
	enc, err := intraproctaint.Encode(findings)
	if err != nil {
		return err
	}
	if err := i.store.SetMetadata(ctx, intraproctaint.MetadataKey, enc); err != nil {
		return fmt.Errorf("ingest: persist intra-proc taint findings: %w", err)
	}
	return nil
}

// IntraProcTaintFindings returns the persisted intra-procedural taint findings
// for the current index (empty when none were found or the index predates the
// analysis). It is the real surfaced path the vuln-go acceptance gate reads: the
// findings the ingest pipeline computed with the production config and persisted.
func (i *Ingester) IntraProcTaintFindings(ctx context.Context) ([]taint.Finding, error) {
	v, err := i.store.Metadata(ctx, intraproctaint.MetadataKey)
	if err != nil {
		if errors.Is(err, graphstore.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("ingest: read intra-proc taint findings: %w", err)
	}
	return intraproctaint.Decode(v)
}

// findingFile returns the repo-relative source file a finding belongs to. An
// intra-procedural finding's source and sink live in the same file, recorded on
// its path steps' SourcePath.
func findingFile(f taint.Finding) string {
	for _, s := range f.Path {
		if s.SourcePath != "" {
			return s.SourcePath
		}
	}
	return ""
}
