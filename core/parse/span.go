package parse

import (
	"fmt"
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
// (column -> byte), validated against THAT LINE'S start/end boundary so a
// column that lands inside a different line does not pass. When the next
// declaration's byte offset cannot be determined (column 0 / unknown column,
// column past the addressed line's end, etc.) the predecessor emits no
// window span rather than an unverifiable one — an unverifiable window
// would silently leak the successor's body into the predecessor, which
// breaks AC-3/AC-7's no-leak invariant. The byte run is from the start of
// the node's first line to the position of the bound (or the end of the
// bound's line for line-bounded spans). A node whose line lies beyond the
// source, or an empty source, yields no span. All nodes are assumed to
// belong to src (one parsed file); the result is a pure function of
// (nodes, src) and byte-reproducible.
//
// SW-260 review round 2 (MAJOR 3): the same-line clip is now validated
// against THAT LINE'S start/end, not against the whole file — the previous
// check accepted a successor at (line=10, col=9000) of a 17-byte file
// because the absolute offset fit inside the file; the column of the
// SUCCESSOR's own line is what bounds the predecessor. Column 0 ("unknown
// column" — model.Node carries Column==0 as the sentinel for "no column
// known") is treated as unverifiable: a predecessor whose only same-line
// successor has column 0 cannot determine where the successor begins, so
// it emits no window. The sort key still uses line then column then ID; the
// column-0 successor sorts AFTER known columns, and the bounds loop skips
// it (column <= n.Column() for any non-zero n) or treats it as unverifiable
// (column 0 falls into the no-known-column branch). The two-line boundary
// check (`off` lies between the line's start and the line's end) closes
// the multi-line leak the prior round-1 test (column 9999 of a 17-byte
// line) missed.
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
		// Column 0 ("unknown") sorts AFTER non-zero columns so a known
		// successor bounds the window when one exists; the bounds loop
		// below still treats column 0 as unverifiable when it is the
		// predecessor's own column (so the predecessor does not silently
		// emit an order-uncertain window).
		if a.Column() != b.Column() {
			if a.Column() == 0 {
				return false
			}
			if b.Column() == 0 {
				return true
			}
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
		// the successor's line (which equals start here). Three conditions
		// make the same-line ordering unverifiable and the predecessor must
		// emit NO window — the SW-260 review-round-2 fail-closed rules:
		//   (a) the predecessor's own column is unknown (column 0) so we
		//       cannot say whether any successor is strictly to its right
		//       on the same line;
		//   (b) the successor's column is unknown (column 0) so we cannot
		//       say where its declaration starts on the line;
		//   (c) the successor's column lies past that line's end so its
		//       byte offset is undefined (and the previous round-1 test
		//       accepted a (line=1, col=9999) of a 17-byte file because
		//       the absolute offset was inside src — that was the bug
		//       MAJOR 3 closed).
		sameLineBound := -1
		var sameLineNode model.Node
		predecessorColumnUnknown := n.Column() == 0
		var unverifiableReason string
		for j := i + 1; j < len(cands); j++ {
			next := cands[j]
			if next.Line() != start {
				break
			}
			if next.Column() <= 0 || next.Column() <= n.Column() {
				// Column 0 ("unknown") or a column not strictly to the
				// right of the predecessor cannot bound the window. If
				// we encounter a column-0 successor on the same line, we
				// cannot establish ordering: the successor's byte offset
				// is unknown (column 0 means "no column known"), so we
				// cannot say where its declaration begins. Fail closed.
				if next.Column() == 0 {
					unverifiableReason = fmt.Sprintf("successor %s has unknown column on line %d", next.QualifiedName(), next.Line())
					break
				}
				// A successor at column <= predecessor's column is not
				// strictly to the right; it cannot bound the window.
				continue
			}
			off, ok := byteOffsetAtCol(starts, src, next.Line(), next.Column())
			if !ok {
				// The successor's column lies past its own line's end
				// (the MAJOR-3 case): the byte offset cannot be trusted.
				unverifiableReason = fmt.Sprintf("successor column %d lies past line %d end", next.Column(), next.Line())
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
		if unverifiableReason != "" {
			// AC-3/AC-7 no-leak fails closed here: a window whose
			// same-line ordering cannot be established is silently
			// guaranteed to leak the successor's body, so we refuse to
			// emit one.
			_ = unverifiableReason // reserved for a future debug surface
			continue
		}
		if predecessorColumnUnknown {
			// Even with no verifiable same-line successor found, a
			// predecessor whose own column is unknown cannot establish
			// ordering on its line: any candidate on the same line with
			// column 0 cannot have been captured above (the loop is
			// already broken on the first col-0 successor), but the
			// predecessor ITSELF does not know where it begins on the
			// line. Fail closed: any same-line candidate (regardless of
			// its column) makes the window unverifiable for this
			// predecessor.
			hasSameLineCand := false
			for j := i + 1; j < len(cands); j++ {
				if cands[j].Line() == start {
					hasSameLineCand = true
					break
				}
			}
			if hasSameLineCand {
				continue
			}
		}
		// Choose the tighter of the line bound and the same-line byte bound.
		endByte := lineEnd(src, starts, end)
		endLine := end
		if sameLineBound >= 0 && sameLineBound < endByte {
			endByte = sameLineBound
			endLine = sameLineNode.Line()
		}
		// Bound the StartByte: when a same-line predecessor exists (a known
		// column strictly to the left of this node on the same line), this
		// node's window starts at the predecessor's EndByte, NOT at the
		// line's start. Without this, two genuine same-line declarations
		// would each open a window from the line's start and overlap —
		// the predecessor's body would leak into the successor's window.
		// The predecessor whose EndByte we are referencing here was
		// already emitted (i = predecessor index, i < this index, and the
		// sort is stable on line then column then ID), so its EndByte is
		// available. A predecessor with column 0 cannot establish a
		// ordering so we keep the line-start default.
		startByte := starts[start-1]
		if !predecessorColumnUnknown && n.Column() > 0 {
			for j := i - 1; j >= 0; j-- {
				pred := cands[j]
				if pred.Line() != start {
					break
				}
				if pred.Column() == 0 || pred.Column() >= n.Column() {
					continue
				}
				if psp, ok := out[pred.ID()]; ok {
					if psp.EndByte > startByte {
						startByte = psp.EndByte
					}
				}
				break
			}
		}
		out[n.ID()] = SourceSpan{
			StartByte: startByte,
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
// the pair cannot be trusted: line out of range, column out of range
// (column 0 means "no column known" — the model.Node sentinel), or the
// computed offset lies past THAT LINE'S exclusive end (the SW-260
// review-round-2 MAJOR-3 fix — the previous check only verified the
// absolute offset was inside src, so a (line=1, col=9000) of a 17-byte
// file was accepted and the derivation thought the successor's byte was
// inside the multi-line source rather than past its own line's end).
func byteOffsetAtCol(starts []int, src []byte, line, col int) (int, bool) {
	if line < 1 || line > len(starts) {
		return 0, false
	}
	if col < 1 {
		return 0, false
	}
	lineStart := starts[line-1]
	lineEndExclusive := len(src)
	if line < len(starts) {
		// The byte position of the next line's start; the current line
		// ends at that position minus the newline itself, but for the
		// purpose of "is the column inside THIS line" we use the
		// inclusive end-of-line (next-line-start minus one) as the
		// upper bound; a column whose absolute offset equals the
		// next-line-start is technically the first column of the next
		// line and is rejected.
		next := starts[line]
		if next > 0 && src[next-1] == '\n' {
			lineEndExclusive = next - 1
		} else {
			lineEndExclusive = next
		}
	}
	off := lineStart + (col - 1)
	if off < lineStart || off >= lineEndExclusive {
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
