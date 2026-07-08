package ingest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/analysis/intraproctaint"
	"github.com/samibel/graphi/engine/analysis/taint"
)

// intraProcTaintConfig is the taint source/sink/sanitizer config the ingest
// pipeline runs the intra-procedural dataflow with. It matches the analyzer's
// default so ingested findings and a `graphi analyze taint` run agree.
func intraProcTaintConfig() taint.Config { return taint.DefaultConfig() }

// analyzeAndPersistIntraProcTaint runs the pure per-function intra-procedural
// taint analysis over every parsed Go file of a FULL pass and replaces the
// persisted findings with the complete, canonical set. It is a pure function of
// the parsed file contents (deterministic) and writes ONLY graphstore metadata,
// so it never perturbs the node/edge graph (byte-parity safe: Snapshot omits
// metadata).
func (i *Ingester) analyzeAndPersistIntraProcTaint(ctx context.Context, parsed []*ParsedFile) error {
	cfg := intraProcTaintConfig()
	var findings []taint.Finding
	for _, pf := range parsed {
		if pf == nil || pf.result == nil {
			continue
		}
		file, fset, ok := parse.GoAST(pf.result)
		if !ok {
			continue
		}
		findings = append(findings, intraproctaint.Analyze(file, fset, cfg)...)
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

	cfg := intraProcTaintConfig()
	out := make([]taint.Finding, 0, len(existing))
	for _, f := range existing {
		file := findingFile(f)
		if file == "" {
			continue // unattributable finding: drop rather than keep stale
		}
		if _, hit := reprocessed[file]; hit {
			continue // recomputed below from the retained parse result
		}
		if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(file))); statErr != nil {
			continue // file gone from disk: drop its findings
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
