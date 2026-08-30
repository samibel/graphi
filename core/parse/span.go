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
// lines. The span is bounded on the right by the NEXT declaration's start
// byte: for a successor on a later line the bound is the start of that line;
// for a SAME-LINE successor the bound is that successor's byte offset
// (column -> byte). When the next declaration's byte offset cannot be
// determined (column absent or out of source bounds) the predecessor emits
// no window span rather than an unverifiable one — an unverifiable window
// would silently leak the successor's body into the predecessor, which
// breaks AC-3/AC-7's no-leak invariant. The byte run is from the start of the
// node's first line to the position of the bound (or the end of the bound's
// line for line-bounded spans). A node whose line lies beyond the source, or
// an empty source, yields no span. All nodes are assumed to belong to src
// (one parsed file); the result is a pure function of (nodes, src) and
// byte-reproducible.
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
		// lineEnd is the bound for a successor on a LATER line: at most that
		// line - 1, then clipped at SpanWindowMaxLines and at EOF. If the
		// successor's line itself is missing (no successor or successor is
		// span-ineligible), the bound stays at the original end line.
		end := start + SpanWindowMaxLines - 1
		var lineBoundLine int // -1 when no later-line successor exists
		for j := i + 1; j < len(cands); j++ {
			next := cands[j]
			if next.Line() > start {
				lineBoundLine = next.Line() - 1
				break
			}
		}
		if lineBoundLine > 0 && lineBoundLine < end {
			end = lineBoundLine
		}
		if end > total {
			end = total
		}
		// Compute the same-line successor's byte offset (column -> byte).
		// A same-line successor at a higher column bounds the window at its
		// start byte; the span's EndByte is that byte position and EndLine is
		// the successor's line (which equals start here). When the byte offset
		// cannot be derived (column absent or out of bounds), the node emits
		// NO window span — better empty than an unverifiable leak.
		sameLineBound := -1
		var sameLineNode model.Node
		for j := i + 1; j < len(cands); j++ {
			next := cands[j]
			if next.Line() != start {
				break
			}
			if next.Column() <= n.Column() {
				continue
			}
			off, ok := byteOffsetAtCol(starts, src, next.Line(), next.Column())
			if !ok {
				sameLineNode = next
				sameLineBound = -1
				break
			}
			if sameLineBound == -1 || off < sameLineBound {
				sameLineBound = off
				sameLineNode = next
			}
			// The first same-line successor with a later column bounds the
			// window; the candidate list is sorted by column so any later
			// successor would be farther right and so is not a tighter bound.
			break
		}
		if sameLineNode.ID() != "" && sameLineBound == -1 {
			// Unverifiable same-line successor: refuse to emit a window span
			// for this node rather than serve a window that silently contains
			// the successor's body. AC-3/AC-7 no-leak fails closed here.
			continue
		}
		// Choose the tighter of the line bound and the same-line byte bound.
		endByte := lineEnd(src, starts, end)
		endLine := end
		if sameLineBound >= 0 && sameLineBound < endByte {
			endByte = sameLineBound
			endLine = sameLineNode.Line()
		}
		out[n.ID()] = SourceSpan{
			StartByte: starts[start-1],
			EndByte:   endByte,
			StartLine: start,
			EndLine:   endLine,
			Method:    SpanMethodWindow,
		}
	}
	return out
}

// byteOffsetAtCol converts a 1-based (line, column) pair to a 0-based byte
// offset within src. columns are byte columns (the contract of
// token.FileSet.Position, which every parser emits). Returns ok=false when
// the pair lies outside src (no fabricated offset).
func byteOffsetAtCol(starts []int, src []byte, line, col int) (int, bool) {
	if line < 1 || line > len(starts) {
		return 0, false
	}
	if col < 1 {
		return 0, false
	}
	lineStart := starts[line-1]
	off := lineStart + (col - 1)
	if off > len(src) {
		return 0, false
	}
	return off, true
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
