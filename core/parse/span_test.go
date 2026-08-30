package parse

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// SW-260 SourceSpan tests: the non-identity span sidecar carried on
// ParseResult.Spans. Node identity is untouched (AC-1); the Go extractor and
// the TypeScript tree-sitter adapter emit exact `ast` spans covering the full
// declaration including its leading doc comment / decorators (AC-2); every
// other parser leaves Spans nil and DeriveWindowSpans supplies the bounded
// `window` fallback (AC-3).

// goSpanFixture exercises: a doc-commented function, a method, a nested func
// literal, two same-line declarations, a single-spec and a multi-spec GenDecl
// with per-spec docs, a parenthesised single-spec GenDecl (SW-260 review
// finding), and a trailing declaration that must not be leaked into its
// predecessor's span.
const goSpanFixture = `package shop

import "fmt"

// TaxRate is the flat tax.
const TaxRate = 7

// Block groups two vars.
var (
	// total accumulates.
	total int
	// count counts.
	count int
)

// solo is a parenthesised single-spec var.
var (
	// solo accumulates.
	solo int
)

// Cart holds items.
type Cart struct{ items int }

// Add appends an item.
func (c *Cart) Add() { c.items++ }

// outer wraps a nested func literal.
func outer() int {
	inner := func() int { return TaxRate }
	return inner()
}

func a() int { return 1 }; func b() int { return 2 }

// last is the final declaration.
func last() { fmt.Println(total, count, solo) }
`

func parseGoSpanFixture(t *testing.T) *ParseResult {
	t.Helper()
	res, err := NewGoParser().Parse(context.Background(), "shop/cart.go", []byte(goSpanFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return res
}

func spanFor(t *testing.T, res *ParseResult, qn string) (model.Node, SourceSpan) {
	t.Helper()
	n, ok := nodeByQN(res.Nodes, qn)
	if !ok {
		t.Fatalf("node %q not found", qn)
	}
	sp, ok := res.Spans[n.ID()]
	if !ok {
		t.Fatalf("node %q has no span (Spans=%v)", qn, res.Spans)
	}
	return n, sp
}

func spanText(src string, sp SourceSpan) string { return src[sp.StartByte:sp.EndByte] }

// TestSourceSpan_IdentityUnchanged pins AC-1: the sidecar never enters node
// identity — the same fixture yields the same NodeIds it did before spans
// existed, and a node's ID is a pure function of its identity fields.
func TestSourceSpan_IdentityUnchanged(t *testing.T) {
	res := parseGoSpanFixture(t)
	for _, n := range res.Nodes {
		want, err := model.NewNode(n.Kind(), n.QualifiedName(), n.SourcePath(), 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if n.ID() != want.ID() {
			t.Fatalf("node %q id %s != identity-only id %s: the span leaked into identity", n.QualifiedName(), n.ID(), want.ID())
		}
	}
	if SpanMethodAST != "ast" || SpanMethodWindow != "window" {
		t.Fatalf("span methods = %q/%q, want ast/window", SpanMethodAST, SpanMethodWindow)
	}
	if SpanWindowMaxLines != 40 {
		t.Fatalf("SpanWindowMaxLines = %d, want 40", SpanWindowMaxLines)
	}
}

// TestExtractGo_Spans pins AC-2 for the go/ast path.
func TestExtractGo_Spans(t *testing.T) {
	res := parseGoSpanFixture(t)
	cases := []struct {
		qn         string
		start, end int // 1-based lines
		prefix     string
		suffix     string
		mustNot    []string
	}{
		{"shop.TaxRate", 5, 6, "// TaxRate is the flat tax.\nconst TaxRate = 7", "TaxRate = 7", []string{"Block"}},
		{"shop.total", 10, 11, "// total accumulates.\n\ttotal int", "total int", []string{"count", "Block"}},
		{"shop.count", 12, 13, "// count counts.\n\tcount int", "count int", []string{"total"}},
		// shop.solo: parenthesised single-spec var. The span MUST run through the
		// closing paren on line 17 (the SW-260 review-round-1 finding — using
		// s.End() for `var ( X int )` stopped at "X int" and dropped the paren).
		{"shop.solo", 16, 20, "// solo is a parenthesised single-spec var.\nvar (\n\t// solo accumulates.\n\tsolo int\n)", ")", []string{"Cart"}},
		{"shop.Cart", 22, 23, "// Cart holds items.\ntype Cart struct{ items int }", "}", []string{"Add"}},
		{"shop.Cart.Add", 25, 26, "// Add appends an item.\nfunc (c *Cart) Add()", "}", []string{"outer"}},
		{"shop.outer", 28, 32, "// outer wraps a nested func literal.\nfunc outer() int {", "}", []string{"func a()", "func b()"}},
		{"shop.a", 34, 34, "func a() int { return 1 }", "}", []string{"func b"}},
		{"shop.b", 34, 34, "func b() int { return 2 }", "}", []string{"func a"}},
		{"shop.last", 36, 37, "// last is the final declaration.\nfunc last()", "}", nil},
	}
	for _, tc := range cases {
		t.Run(tc.qn, func(t *testing.T) {
			_, sp := spanFor(t, res, tc.qn)
			if sp.Method != SpanMethodAST {
				t.Errorf("method = %q, want ast", sp.Method)
			}
			if sp.StartLine != tc.start || sp.EndLine != tc.end {
				t.Errorf("lines = %d-%d, want %d-%d", sp.StartLine, sp.EndLine, tc.start, tc.end)
			}
			text := spanText(goSpanFixture, sp)
			if !strings.HasPrefix(text, tc.prefix) {
				t.Errorf("span text %q does not start with %q", text, tc.prefix)
			}
			if !strings.HasSuffix(text, tc.suffix) {
				t.Errorf("span text %q does not end with %q", text, tc.suffix)
			}
			for _, bad := range tc.mustNot {
				if strings.Contains(text, bad) {
					t.Errorf("span text %q leaks %q", text, bad)
				}
			}
		})
	}
	// The nested func literal body belongs to outer's span.
	_, outer := spanFor(t, res, "shop.outer")
	if !strings.Contains(spanText(goSpanFixture, outer), "inner := func() int { return TaxRate }") {
		t.Errorf("outer span lost its nested func literal: %q", spanText(goSpanFixture, outer))
	}
	// File nodes carry no span (decision: the file node is not a declaration).
	fileNode, _ := nodeByQN(res.Nodes, "shop/cart.go")
	if _, has := res.Spans[fileNode.ID()]; has {
		t.Errorf("file node must not carry a span")
	}
	if len(res.Spans) != len(res.Nodes)-1 {
		t.Errorf("spans = %d, want one per non-file node (%d)", len(res.Spans), len(res.Nodes)-1)
	}
}

// tsSpanFixture covers: an exported doc-commented function, a class decorated
// before `export`, a decorated method, a class decorated after `export`, an
// unexported decorated class, and two same-line lexical declarations.
const tsSpanFixture = `import x from "y";

/** doc for f */
export function f(a: number): number { return a; }

@Component({sel: 'x'})
export class A {
  @Get()
  run(): void {}

  plain(): void {}
}

export @Dec class B {}

@Injectable()
class C {}

const K = 1; let v = 2;

// detached: blank line above, so it does not attach to g.

function g(): void {}
`

func parseTSSpanFixture(t *testing.T) *ParseResult {
	t.Helper()
	res, err := NewTSParser().Parse(context.Background(), "pkg/a.ts", []byte(tsSpanFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return res
}

// TestExtractTS_Spans pins AC-2 for the tree-sitter adapter (TypeScript):
// spans come from the CST node bounds and include attached decorators and an
// adjacent leading doc comment.
func TestExtractTS_Spans(t *testing.T) {
	res := parseTSSpanFixture(t)
	cases := []struct {
		qn         string
		start, end int
		text       string
		mustNot    []string
	}{
		{"pkg.f", 3, 4, "/** doc for f */\nexport function f(a: number): number { return a; }", []string{"Component"}},
		{"pkg.A", 6, 12, "@Component({sel: 'x'})\nexport class A {\n  @Get()\n  run(): void {}\n\n  plain(): void {}\n}", []string{"@Dec"}},
		{"pkg.run", 8, 9, "@Get()\n  run(): void {}", []string{"plain"}},
		{"pkg.plain", 11, 11, "plain(): void {}", []string{"run", "@Get"}},
		{"pkg.B", 14, 14, "export @Dec class B {}", []string{"Injectable"}},
		{"pkg.C", 16, 17, "@Injectable()\nclass C {}", []string{"const K"}},
		{"pkg.K", 19, 19, "const K = 1;", []string{"let v"}},
		{"pkg.v", 19, 19, "let v = 2;", []string{"const K"}},
		{"pkg.g", 23, 23, "function g(): void {}", []string{"detached"}},
	}
	for _, tc := range cases {
		t.Run(tc.qn, func(t *testing.T) {
			_, sp := spanFor(t, res, tc.qn)
			if sp.Method != SpanMethodAST {
				t.Errorf("method = %q, want ast", sp.Method)
			}
			if sp.StartLine != tc.start || sp.EndLine != tc.end {
				t.Errorf("lines = %d-%d, want %d-%d", sp.StartLine, sp.EndLine, tc.start, tc.end)
			}
			if got := spanText(tsSpanFixture, sp); got != tc.text {
				t.Errorf("span text = %q, want %q", got, tc.text)
			}
			for _, bad := range tc.mustNot {
				if strings.Contains(spanText(tsSpanFixture, sp), bad) {
					t.Errorf("span leaks %q", bad)
				}
			}
		})
	}
	fileNode, _ := nodeByQN(res.Nodes, "pkg/a.ts")
	if _, has := res.Spans[fileNode.ID()]; has {
		t.Errorf("file node must not carry a span")
	}
}

// TestSpans_Deterministic: identical input yields identical spans (byte-level).
func TestSpans_Deterministic(t *testing.T) {
	a, b := parseGoSpanFixture(t), parseGoSpanFixture(t)
	if len(a.Spans) != len(b.Spans) {
		t.Fatalf("span counts differ")
	}
	for id, sa := range a.Spans {
		if sb := b.Spans[id]; sa != sb {
			t.Errorf("span %s differs: %+v vs %+v", id, sa, sb)
		}
	}
	ta, tb := parseTSSpanFixture(t), parseTSSpanFixture(t)
	for id, sa := range ta.Spans {
		if sb := tb.Spans[id]; sa != sb {
			t.Errorf("ts span %s differs: %+v vs %+v", id, sa, sb)
		}
	}
}

// TestParse_OtherParsersLeaveSpansNil pins the seam contract for parsers that
// have no exact adapter yet: Spans is nil, so a consumer applies the window
// fallback rather than reading a fabricated ast span.
func TestParse_OtherParsersLeaveSpansNil(t *testing.T) {
	reg := NewDefaultRegistry()
	res, err := reg.Parse(context.Background(), "shop/cart.py", []byte(pyGoldenFixture))
	if err != nil {
		t.Fatal(err)
	}
	if res.Spans != nil {
		t.Fatalf("python parser emitted spans %v; it has no exact adapter and must leave Spans nil", res.Spans)
	}
}

// TestDeriveWindowSpans pins AC-3: a window starts at the node's line, spans
// at most SpanWindowMaxLines lines, never crosses the next node's start line,
// is clipped at EOF, and is labelled window. A same-line successor (a
// declaration sharing the line but at a higher column) bounds the window at
// its byte offset instead of leaking into the predecessor's text — the SW-260
// review-round-1 finding that closes AC-3/AC-7's no-leak invariant on the
// fallback path.
func TestDeriveWindowSpans(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 100; i++ {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteByte('\n')
	}
	src := b.String()
	mk := func(kind, qn string, line, col int) model.Node {
		n, err := model.NewNode(kind, qn, "p/f.py", line, col)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	file := mk(KindFile, "p/f.py", 1, 1)
	early := mk(KindFunction, "p.early", 3, 1)   // clipped by mid at 10
	mid := mk(KindType, "p.mid", 10, 1)          // clipped at midSame's byte (line 10 col 8): the SW-260 finding
	midSame := mk(KindVariable, "p.midv", 10, 8) // first same-line successor of mid: gets the 40-line bound
	late := mk(KindFunction, "p.late", 90, 1)    // clipped at EOF (100)
	pkg, _ := model.NewNode(KindPackage, "com.x", "", 0, 0)

	// Deliberately unsorted input: the derivation must sort by line itself.
	spans := DeriveWindowSpans([]model.Node{late, file, midSame, mid, early, pkg}, []byte(src))

	if _, has := spans[file.ID()]; has {
		t.Errorf("file node must not get a window")
	}
	if _, has := spans[pkg.ID()]; has {
		t.Errorf("package node (no source) must not get a window")
	}
	check := func(n model.Node, start, end int) {
		t.Helper()
		sp, ok := spans[n.ID()]
		if !ok {
			t.Fatalf("%s: no window span", n.QualifiedName())
		}
		if sp.Method != SpanMethodWindow {
			t.Errorf("%s: method = %q, want window", n.QualifiedName(), sp.Method)
		}
		if sp.StartLine != start || sp.EndLine != end {
			t.Errorf("%s: lines = %d-%d, want %d-%d", n.QualifiedName(), sp.StartLine, sp.EndLine, start, end)
		}
		if sp.EndLine-sp.StartLine+1 > SpanWindowMaxLines {
			t.Errorf("%s: window exceeds bound: %d lines", n.QualifiedName(), sp.EndLine-sp.StartLine+1)
		}
		text := src[sp.StartByte:sp.EndByte]
		if !strings.HasPrefix(text, "line ") {
			t.Errorf("%s: window does not start at a line start: %q", n.QualifiedName(), text[:min(len(text), 12)])
		}
		if strings.HasSuffix(text, "\n") {
			t.Errorf("%s: window must end before the trailing newline", n.QualifiedName())
		}
		if got := strings.Count(text, "\n") + 1; got != end-start+1 {
			t.Errorf("%s: window text spans %d lines, want %d", n.QualifiedName(), got, end-start+1)
		}
	}
	check(early, 3, 9)
	check(mid, 10, 10)     // SW-260 fix: midSame at col 8 bounds mid's window at its byte
	check(midSame, 10, 49) // midSame has no later-col same-line successor; 40-line bound
	check(late, 90, 100)

	// The clip is byte-precise: mid's EndByte equals the byte offset of
	// midSame's start (line 10, column 8). Without the clip mid would still
	// extend to line 49 and silently contain midSame's body.
	midSp := spans[mid.ID()]
	lineStarts := lineStartsForTest([]byte(src))
	wantEnd := lineStarts[9] + 7 // line 10, col 8 -> lineStart[9] + (col-1)
	if midSp.EndByte != wantEnd {
		t.Errorf("mid EndByte = %d, want %d (midSame's start byte)", midSp.EndByte, wantEnd)
	}
	midText := src[midSp.StartByte:midSp.EndByte]
	if strings.Contains(midText, "midSame") || strings.Contains(midText, "p.midv") {
		t.Errorf("mid window leaked midSame text: %q", midText)
	}

	// An empty source or a node beyond EOF yields no span rather than a bogus one.
	if got := DeriveWindowSpans([]model.Node{mid}, nil); len(got) != 0 {
		t.Errorf("empty source produced spans %v", got)
	}
	beyond := mk(KindFunction, "p.beyond", 500, 1)
	if got := DeriveWindowSpans([]model.Node{beyond}, []byte(src)); len(got) != 0 {
		t.Errorf("node beyond EOF produced spans %v", got)
	}
	// Byte-reproducible across runs.
	again := DeriveWindowSpans([]model.Node{late, file, midSame, mid, early, pkg}, []byte(src))
	for id, sp := range spans {
		if again[id] != sp {
			t.Errorf("window span %s differs across runs", id)
		}
	}
}

// TestDeriveWindowSpans_UnverifiableSameLineFailsClosed pins the SW-260
// review-round-1 fail-closed rule: a predecessor whose only same-line
// successor has an UNCOMPUTABLE byte offset (column out of source bounds)
// emits no window span at all rather than serving a window that silently
// leaks the successor's body. AC-3/AC-7 no-leak fails closed here.
func TestDeriveWindowSpans_UnverifiableSameLineFailsClosed(t *testing.T) {
	const src = "alpha beta gamma\n"
	mk := func(kind, qn string, line, col int) model.Node {
		n, err := model.NewNode(kind, qn, "p/f.py", line, col)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	pre := mk(KindFunction, "p.pre", 1, 1)
	// Same line as pre, but a column that lies FAR past the source end
	// (~col 9999 of a ~17-byte line). The byte offset is unverifiable; the
	// predecessor must emit no span rather than a window that swallows the
	// successor.
	suc := mk(KindVariable, "p.suc", 1, 9999)

	spans := DeriveWindowSpans([]model.Node{pre, suc}, []byte(src))
	if _, has := spans[pre.ID()]; has {
		t.Errorf("predecessor with an unverifiable same-line successor must emit no window, got %+v", spans[pre.ID()])
	}
	// The successor still gets its window (no successor of its own).
	if _, has := spans[suc.ID()]; !has {
		t.Errorf("successor with no same-line follower must still get a window")
	}
}

// lineStartsForTest exposes lineStarts to the test package without leaking it
// from the package API. The production seam is internal to DeriveWindowSpans;
// this helper exists only to recompute line-start bytes for assertions in the
// same-line clip test.
func lineStartsForTest(src []byte) []int {
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
