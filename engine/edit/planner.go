package edit

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
)

// blastRadius resolves the EP-004 forward-impact set for a target symbol: the set
// of source FILES that contain the symbol's definition plus every dependent that
// references/calls it. It consumes the analysis impact analyzer
// (Direction:Forward, default kinds {calls,references,defines}) read-only, then
// projects the reached reference nodes down to their SourcePaths. The target's
// own definition file is always included (a rename must rewrite the definition,
// not only its references).
//
// Returns the de-duplicated, sorted set of repo-relative-ish source paths in the
// blast radius and whether the impact result was truncated at the analyzer's
// 1024-node cap (callers surface truncation rather than silently under-editing).
func (a *Applier) blastRadius(ctx context.Context, targetSymbol string) (paths []string, truncated bool, err error) {
	target, err := a.store.GetNode(ctx, model.NodeId(targetSymbol))
	if err != nil {
		return nil, false, fmt.Errorf("%w: resolve target symbol %s: %v", ErrInvalidOp, targetSymbol, err)
	}

	svc := analysis.NewDefaultService(a.store)
	res, err := svc.Dispatch(ctx, "impact", analysis.Params{
		Symbol: model.NodeId(targetSymbol),
		// Blast radius = dependents = Reverse since the v0.1.3 direction fix
		// (rdeps convention; this was spelled Forward before the swap).
		Direction: analysis.Reverse,
	})
	if err != nil {
		return nil, false, fmt.Errorf("%w: impact analysis: %v", ErrReindex, err)
	}

	set := map[string]struct{}{target.SourcePath(): {}}
	for _, rn := range res.Nodes {
		if sp := rn.Node.SourcePath; sp != "" {
			set[sp] = struct{}{}
		}
	}
	paths = make([]string, 0, len(set))
	for p := range set {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths, res.Truncated, nil
}

// planNameRewrite is the net-new span-resolution layer. EP-004 gives us the blast
// radius as NODES (and their files), but model.Node exposes only Line()/Column()
// and no byte span, so we cannot build EditOps directly from the analysis result.
// This layer parses/resolves each file in the blast radius down to concrete
// (FilePath, ByteSpan, Replacement) EditOps by locating every occurrence of the
// old identifier and emitting a replacement with the new one.
//
// Identifier-boundary matching: an occurrence only counts when it is NOT flanked
// by an identifier byte (letter, digit, or underscore), so renaming "Foo" never
// rewrites "FooBar" or "BarFoo". This is the resolve step that turns the
// graph-level blast radius into byte-level edits; a richer implementation would
// consult the parser's reference offsets, but boundary-aware scanning is correct
// and deterministic for the identifier-rename/signature-change shapes SW-036
// targets and keeps the planner free of a grammar dependency.
func planNameRewrite(root string, files []string, oldName, newName string) ([]EditOp, error) {
	if oldName == "" {
		return nil, fmt.Errorf("%w: empty old name", ErrInvalidOp)
	}
	if oldName == newName {
		return nil, fmt.Errorf("%w: old and new name are identical (%q)", ErrInvalidOp, oldName)
	}
	old := []byte(oldName)
	repl := []byte(newName)

	var ops []EditOp
	sorted := make([]string, len(files))
	copy(sorted, files)
	sort.Strings(sorted)
	for _, rel := range sorted {
		abs := joinRoot(root, rel)
		content, err := os.ReadFile(abs) //nolint:gosec // rel comes from in-graph source paths under root
		if err != nil {
			// A file in the blast radius that no longer exists on disk is a
			// referential-drift skip, not a hard error (mirrors the analyzer's
			// tolerance of missing endpoints).
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("%w: read %s for span resolution: %v", ErrInvalidOp, rel, err)
		}
		for _, span := range identifierSpans(content, old) {
			ops = append(ops, EditOp{
				FilePath:    rel,
				ByteSpan:    span,
				Replacement: repl,
			})
		}
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("%w: no occurrences of %q found in blast radius", ErrInvalidOp, oldName)
	}
	return ops, nil
}

// identifierSpans returns every [Start,End) byte span where needle occurs in src
// as a WHOLE identifier (not flanked by an identifier byte). Spans are returned
// in ascending Start order.
func identifierSpans(src, needle []byte) []Span {
	var spans []Span
	from := 0
	for {
		i := bytes.Index(src[from:], needle)
		if i < 0 {
			break
		}
		start := from + i
		end := start + len(needle)
		if !isIdentByte(boundaryByte(src, start-1)) && !isIdentByte(boundaryByte(src, end)) {
			spans = append(spans, Span{Start: start, End: end})
		}
		from = end
	}
	return spans
}

func boundaryByte(src []byte, idx int) byte {
	if idx < 0 || idx >= len(src) {
		return 0
	}
	return src[idx]
}

func isIdentByte(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}

// joinRoot joins a repo-relative source path onto root. resolvePath re-validates
// the result against the root before any write, so this is only used to read
// candidate files during planning.
func joinRoot(root, rel string) string {
	return filepath.Join(root, filepath.FromSlash(rel))
}
