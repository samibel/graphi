package parse

import (
	"sort"

	"github.com/samibel/graphi/core/model"
)

// SpanMethod names how a SourceSpan was derived (SW-260). It is part of the
// span, never of node identity, so a consumer can report the share of exact
// versus heuristic spans instead of treating a window as an AST fact.
type SpanMethod string

const (
	// SpanMethodAST marks an exact span taken from the parser's own AST/CST:
	// the full declaration including its leading doc comment group (Go) or
	// attached decorators / adjacent doc comment (tree-sitter adapters).
	SpanMethodAST SpanMethod = "ast"
	// SpanMethodWindow marks the hard-bounded heuristic fallback DeriveWindowSpans
	// produces for parsers that emit no exact span: from the node's line, at most
	// SpanWindowMaxLines lines, never crossing the next node's start line.
	SpanMethodWindow SpanMethod = "window"
)

// SpanWindowMaxLines bounds the window fallback: a window covers at most this
// many source lines from the node's declaration line (AC-3).
const SpanWindowMaxLines = 40

// SourceSpan is the NON-identity byte/line extent of a node's declaration in
// its source file. It is carried on ParseResult.Spans as a sidecar keyed by
// NodeId and never enters model.Node or its xxhash64 identity, so the graph,
// every NodeId and every existing golden stay byte-identical whether or not a
// parser emits spans. Offsets are 0-based bytes into the parsed source
// (EndByte exclusive); lines are 1-based and inclusive, matching Node.Line.
type SourceSpan struct {
	StartByte, EndByte int
	StartLine, EndLine int
	Method             SpanMethod
}

// DeriveWindowSpans is the single window fallback (AC-3) for parsers whose
// ParseResult.Spans is nil. For every symbol node (file, package and external
// nodes carry no declaration and get no span) it derives a SpanMethodWindow
// span starting at the node's line and covering at most SpanWindowMaxLines
// lines, clipped at the start line of the next node in the same source (nodes
// are sorted by line; declarations sharing a line share a window) and at end
// of file. The span's bytes run from the start of its first line to the end of
// its last line's content (excluding that line's newline). A node whose line
// lies beyond the source, or an empty source, yields no span rather than a
// fabricated one. All nodes are assumed to belong to src (one parsed file);
// the result is a pure function of (nodes, src) and byte-reproducible.
func DeriveWindowSpans(nodes []model.Node, src []byte) map[model.NodeId]SourceSpan {
	starts := lineStarts(src)
	total := len(starts)
	if total == 0 {
		return nil
	}
	cands := make([]model.Node, 0, len(nodes))
	for _, n := range nodes {
		if !spanEligible(n) || n.Line() < 1 || n.Line() > total {
			continue
		}
		cands = append(cands, n)
	}
	if len(cands) == 0 {
		return nil
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.Line() != b.Line() {
			return a.Line() < b.Line()
		}
		if a.Column() != b.Column() {
			return a.Column() < b.Column()
		}
		return a.ID() < b.ID()
	})
	out := make(map[model.NodeId]SourceSpan, len(cands))
	for i, n := range cands {
		start := n.Line()
		end := start + SpanWindowMaxLines - 1
		// Clip at the next node that starts on a LATER line (same-line
		// declarations share the window start and must not clip each other).
		for j := i + 1; j < len(cands); j++ {
			if next := cands[j].Line(); next > start {
				if next-1 < end {
					end = next - 1
				}
				break
			}
		}
		if end > total {
			end = total
		}
		out[n.ID()] = SourceSpan{
			StartByte: starts[start-1],
			EndByte:   lineEnd(src, starts, end),
			StartLine: start,
			EndLine:   end,
			Method:    SpanMethodWindow,
		}
	}
	return out
}

// spanEligible reports whether n is a declaration-bearing symbol node: file
// nodes span nothing, and interned package/external nodes have no source.
func spanEligible(n model.Node) bool {
	switch n.Kind() {
	case KindFile, KindPackage, KindExternal:
		return false
	}
	return n.SourcePath() != ""
}

// lineStarts returns the 0-based byte offset of every line start in src. A
// trailing newline does not open an extra (empty) final line, so the count is
// the number of lines a reader would number. Empty src has no lines.
func lineStarts(src []byte) []int {
	if len(src) == 0 {
		return nil
	}
	starts := []int{0}
	for i, b := range src {
		if b == '\n' && i+1 < len(src) {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// lineEnd returns the exclusive byte offset of the content of 1-based line
// (i.e. the position of its newline, or len(src) for the last line).
func lineEnd(src []byte, starts []int, line int) int {
	if line < len(starts) {
		end := starts[line] - 1 // the '\n' preceding the next line start
		if end >= 0 && end < len(src) && src[end] == '\n' {
			return end
		}
		return starts[line]
	}
	end := len(src)
	if end > 0 && src[end-1] == '\n' {
		end--
	}
	return end
}
