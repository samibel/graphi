package embed_test

// SW-267 capsule extraction: parser-provided spans take precedence
// over the line/brace heuristic; the heuristic is rigorously tested
// against the three shapes that broke naive extraction (decorators,
// multiline declarations, block comments).

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/embed"
)

// TestCapsule_UsesParserProvidedSpan pins AC-1 + the reviewer fix:
// when the parser supplies SignatureSpan / DocSpan, the capsule uses
// those byte ranges verbatim. The heuristic is bypassed (no
// line-by-line guessing). Without the parser-span fields the heuristic
// runs; with them, the parser's view wins.
func TestCapsule_UsesParserProvidedSpan(t *testing.T) {
	src := []byte("package p\n\n// Hello says hi.\nfunc Hello() {}\n")
	// Byte layout of the source:
	//   0..9: "package p\n"  (10 bytes)
	//  10:    "\n"
	//  11..28: "// Hello says hi." (18 bytes)
	//  29..43: "func Hello() {}" (15 bytes)
	//  44:    "\n"
	span := parse.SourceSpan{
		StartByte: 0, EndByte: len(src),
		StartLine: 1, EndLine: 4,
		Method: parse.SpanMethodAST,
		// DocSpan: the "// Hello says hi." line (positions 11..28).
		// SignatureSpan: "func Hello() {}" (positions 29..44 inclusive
		// of '}', exclusive endByte = 44).
		DocSpan:       &parse.ByteSpan{StartByte: 11, EndByte: 28},
		SignatureSpan: &parse.ByteSpan{StartByte: 29, EndByte: 44},
	}
	n, _ := model.NewNode("function", "p.Hello", "p/hello.go", 4, 1)
	d, err := embed.BuildDocument(n, span, embed.Source{Language: "go", Bytes: src})
	if err != nil {
		t.Fatalf("BuildDocument: %v", err)
	}
	if d.Capsule.DocComment != "// Hello says hi." {
		t.Errorf("DocComment = %q, want %q (parser span)", d.Capsule.DocComment, "// Hello says hi.")
	}
	if d.Capsule.Signature != "func Hello() {}" {
		t.Errorf("Signature = %q, want %q (parser span)", d.Capsule.Signature, "func Hello() {}")
	}
}

// TestCapsule_HeuristicHandlesDecorators pins the heuristic path for
// the shape the reviewer named first: a line of @decorators precedes
// the declaration. Without the parser-span fields, the heuristic
// must skip the decorator and bind the signature to "func f() {}".
func TestCapsule_HeuristicHandlesDecorators(t *testing.T) {
	src := []byte("@Component({sel: 'x'})\n@Decorator2\nfunc f() {}\n")
	span := parse.SourceSpan{
		StartByte: 0, EndByte: len(src),
		StartLine: 1, EndLine: 3,
		Method: parse.SpanMethodAST,
	}
	n, _ := model.NewNode("function", "p.f", "p/f.go", 3, 1)
	d, err := embed.BuildDocument(n, span, embed.Source{Language: "go", Bytes: src})
	if err != nil {
		t.Fatalf("BuildDocument: %v", err)
	}
	if d.Capsule.Signature != "func f() {}" {
		t.Errorf("Signature = %q, want %q (decorators must not eat the signature)", d.Capsule.Signature, "func f() {}")
	}
	if strings.Contains(d.Capsule.Signature, "@") {
		t.Errorf("Signature contains decorator: %q (heuristic must skip @ lines)", d.Capsule.Signature)
	}
}

// TestCapsule_HeuristicHandlesMultilineDeclarations pins the second
// shape: a function whose opening brace is on a later line than the
// declaration. Without parser-span fields, the heuristic must end
// the signature at the opening `{` and leave the body disjoint.
func TestCapsule_HeuristicHandlesMultilineDeclarations(t *testing.T) {
	src := []byte("// MultilineFunc.\nfunc MultilineFunc(\n\tx int,\n\ty int,\n) (int, error) {\n\treturn 0, nil\n}\n")
	span := parse.SourceSpan{
		StartByte: 0, EndByte: len(src),
		StartLine: 1, EndLine: 8,
		Method: parse.SpanMethodAST,
	}
	n, _ := model.NewNode("function", "p.MultilineFunc", "p/m.go", 3, 1)
	d, err := embed.BuildDocument(n, span, embed.Source{Language: "go", Bytes: src})
	if err != nil {
		t.Fatalf("BuildDocument: %v", err)
	}
	if d.Capsule.Signature != "func MultilineFunc(" {
		t.Errorf("Signature = %q, want %q (heuristic must end at first {)", d.Capsule.Signature, "func MultilineFunc(")
	}
	if strings.Contains(d.Capsule.Body, "func MultilineFunc(") {
		t.Errorf("Body contains the signature: %q (signature must be disjoint from body)", d.Capsule.Body[:min(60, len(d.Capsule.Body))])
	}
}

// TestCapsule_HeuristicHandlesBlockComments pins the third shape: a
// /* ... */ block comment as the doc comment. Without parser-span
// fields, the heuristic must capture the entire block (including
// continuation lines) as DocComment.
func TestCapsule_HeuristicHandlesBlockComments(t *testing.T) {
	src := []byte("/* BlockComment.\n *\n * Continuation line.\n */\nfunc BlockDoc() {}\n")
	span := parse.SourceSpan{
		StartByte: 0, EndByte: len(src),
		StartLine: 1, EndLine: 5,
		Method: parse.SpanMethodAST,
	}
	n, _ := model.NewNode("function", "p.BlockDoc", "p/b.go", 5, 1)
	d, err := embed.BuildDocument(n, span, embed.Source{Language: "go", Bytes: src})
	if err != nil {
		t.Fatalf("BuildDocument: %v", err)
	}
	if !strings.HasPrefix(d.Capsule.DocComment, "/*") {
		t.Errorf("DocComment = %q, want /*-prefixed block comment", d.Capsule.DocComment)
	}
	if !strings.Contains(d.Capsule.DocComment, "Continuation line") {
		t.Errorf("DocComment = %q, want it to include the continuation line of the block comment", d.Capsule.DocComment)
	}
}

// TestCapsule_DocAndSignatureDisjoint pins the stripDocAndSignature
// invariant: the same bytes must not appear in both DocComment and
// Signature, and Signature must not leak into Body.
func TestCapsule_DocAndSignatureDisjoint(t *testing.T) {
	src := []byte("// doc\nfunc F() { x := 1; _ = x }\n")
	span := parse.SourceSpan{
		StartByte: 0, EndByte: len(src),
		StartLine: 1, EndLine: 2,
		Method: parse.SpanMethodAST,
	}
	n, _ := model.NewNode("function", "p.F", "p/f.go", 2, 1)
	d, err := embed.BuildDocument(n, span, embed.Source{Language: "go", Bytes: src})
	if err != nil {
		t.Fatalf("BuildDocument: %v", err)
	}
	if d.Capsule.DocComment == d.Capsule.Signature {
		t.Errorf("DocComment == Signature; they must be disjoint")
	}
	if d.Capsule.Body == d.Capsule.Signature {
		t.Errorf("Body == Signature; they must be disjoint")
	}
	if strings.Contains(d.Capsule.Body, "func F()") {
		t.Errorf("Body contains signature: %q", d.Capsule.Body)
	}
	_ = context.Background
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
