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

// TestCapsule_UsesParserProvidedSpans drives the production parsers and then
// passes their SourceSpan sidecars to BuildDocument. It prevents the optional
// sub-span fields from becoming a cosmetic contract exercised only by a
// fabricated SourceSpan.
func TestCapsule_UsesParserProvidedSpans(t *testing.T) {
	t.Run("go multiline declaration and block doc", func(t *testing.T) {
		src := []byte("package p\n\n/* Multiline documents Multiline.\n * Continued.\n */\nfunc Multiline(\n\tx int,\n\ty int,\n) (int, error) {\n\treturn x + y, nil\n}\n")
		n, span := parsedNodeSpan(t, "p/multiline.go", src, "p.Multiline")
		if span.DocSpan == nil || span.SignatureSpan == nil {
			t.Fatalf("Go parser sub-spans = doc:%v signature:%v, want both populated", span.DocSpan, span.SignatureSpan)
		}
		d, err := embed.BuildDocument(n, span, embed.Source{Language: "go", Bytes: src})
		if err != nil {
			t.Fatalf("BuildDocument: %v", err)
		}
		wantDoc := "/* Multiline documents Multiline.\n * Continued.\n */"
		wantSig := "func Multiline(\n\tx int,\n\ty int,\n) (int, error) {"
		if d.Capsule.DocComment != wantDoc {
			t.Errorf("DocComment = %q, want parser block comment %q", d.Capsule.DocComment, wantDoc)
		}
		if d.Capsule.Signature != wantSig {
			t.Errorf("Signature = %q, want parser multiline signature %q", d.Capsule.Signature, wantSig)
		}
		if strings.Contains(d.Capsule.Body, "func Multiline(") {
			t.Errorf("Body repeats parser-provided signature: %q", d.Capsule.Body)
		}
	})

	t.Run("typescript decorator and multiline declaration", func(t *testing.T) {
		src := []byte("/** Service docs. */\n@Component({\n  selector: 'x',\n})\nexport class Service {\n  run(): void {}\n}\n")
		n, span := parsedNodeSpan(t, "shop/service.ts", src, "shop.Service")
		if span.DocSpan == nil || span.SignatureSpan == nil {
			t.Fatalf("TypeScript parser sub-spans = doc:%v signature:%v, want both populated", span.DocSpan, span.SignatureSpan)
		}
		d, err := embed.BuildDocument(n, span, embed.Source{Language: "typescript", Bytes: src})
		if err != nil {
			t.Fatalf("BuildDocument: %v", err)
		}
		wantSig := "@Component({\n  selector: 'x',\n})\nexport class Service {"
		if d.Capsule.DocComment != "/** Service docs. */" {
			t.Errorf("DocComment = %q, want parser JSDoc", d.Capsule.DocComment)
		}
		if d.Capsule.Signature != wantSig {
			t.Errorf("Signature = %q, want decorated parser signature %q", d.Capsule.Signature, wantSig)
		}
		if strings.Contains(d.Capsule.Body, "@Component") {
			t.Errorf("Body repeats parser-provided decorator/signature: %q", d.Capsule.Body)
		}
	})
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
	wantSig := "func MultilineFunc(\n\tx int,\n\ty int,\n) (int, error) {"
	if d.Capsule.Signature != wantSig {
		t.Errorf("Signature = %q, want %q (heuristic must include a multiline declaration through its opening brace)", d.Capsule.Signature, wantSig)
	}
	if strings.Contains(d.Capsule.Body, "func MultilineFunc(") {
		t.Errorf("Body contains the signature: %q (signature must be disjoint from body)", d.Capsule.Body[:min(60, len(d.Capsule.Body))])
	}
}

func parsedNodeSpan(t *testing.T, path string, src []byte, qn string) (model.Node, parse.SourceSpan) {
	t.Helper()
	res, err := parse.NewDefaultRegistry().Parse(context.Background(), path, src)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	defer parse.ReleaseRoot(res)
	for _, n := range res.Nodes {
		if n.QualifiedName() != qn {
			continue
		}
		span, ok := res.Spans[n.ID()]
		if !ok {
			t.Fatalf("parser returned no SourceSpan for %s", qn)
		}
		return n, span
	}
	t.Fatalf("parser returned no node %s", qn)
	return model.Node{}, parse.SourceSpan{}
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
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
