package embed_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cespare/xxhash/v2"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/internal/goldenfile"
)

// SW-260 SemanticDocument v2 builder tests (AC-4 … AC-7).

const docGoFixture = `package shop

import "fmt"

// TaxRate is the flat tax.
const TaxRate = 7

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
func last() { fmt.Println(TaxRate) }
`

const docTSFixture = `/** doc for f */
export function f(a: number): number { return a; }

@Component({sel: 'x'})
export class A {
  @Get()
  run(): void {}
}

function g(): void {}
`

// docPyFixture has no exact adapter, so its documents ride the window
// fallback: `long` runs past SpanWindowMaxLines and `after` follows it.
var docPyFixture = "def short():\n    return 1\n\ndef long():\n" + strings.Repeat("    x = 1\n", 60) + "\ndef after():\n    return 2\n"

func parseFixture(t *testing.T, path, src string) *parse.ParseResult {
	t.Helper()
	res, err := parse.NewDefaultRegistry().Parse(context.Background(), path, []byte(src))
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return res
}

func fileSource(res *parse.ParseResult, src string) embed.FileSource {
	return embed.FileSource{
		Source: embed.Source{Language: res.Meta.Language, Bytes: []byte(src)},
		Path:   res.Meta.Path,
		Nodes:  res.Nodes,
		Spans:  res.Spans,
	}
}

func docByQN(t *testing.T, docs []embed.SemanticDocument, qn string) embed.SemanticDocument {
	t.Helper()
	for _, d := range docs {
		if d.QualifiedName == qn {
			return d
		}
	}
	t.Fatalf("no document for %q in %d documents", qn, len(docs))
	return embed.SemanticDocument{}
}

func marshalDocs(t *testing.T, docs []embed.SemanticDocument) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(docs); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestBuildDocument_FieldsAndTextOrder pins AC-4: every listed field, the
// schema tag, the two hashes and the fixed text order kind → qualified name →
// path segments → docs/annotations → body.
func TestBuildDocument_FieldsAndTextOrder(t *testing.T) {
	src := "// Hello says hi.\nfunc Hello() {}\n"
	n, err := model.NewNode("function", "greet.Hello", "internal/greet/hello.go", 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	n = n.WithMeta(model.NewNodeMeta([]string{"Deprecated", "Beta"}, []string{"static"}))
	span := parse.SourceSpan{StartByte: 0, EndByte: len(src) - 1, StartLine: 1, EndLine: 2, Method: parse.SpanMethodAST}
	d := embed.BuildDocument(n, span, embed.Source{Language: "go", Bytes: []byte(src)})

	wantText := "function greet.Hello\ninternal greet hello.go\nBeta Deprecated\n// Hello says hi.\nfunc Hello() {}"
	if d.Text != wantText {
		t.Fatalf("text =\n%q\nwant\n%q", d.Text, wantText)
	}
	if d.DocumentSchema != "v2" || embed.DocumentSchema != "v2" {
		t.Errorf("document_schema = %q, want v2", d.DocumentSchema)
	}
	if d.NodeID != n.ID() || d.Language != "go" || d.Kind != "function" || d.QualifiedName != "greet.Hello" || d.Path != "internal/greet/hello.go" {
		t.Errorf("identity fields = %+v", d)
	}
	if d.StartByte != 0 || d.EndByte != len(src)-1 || d.StartLine != 1 || d.EndLine != 2 || d.SpanMethod != "ast" {
		t.Errorf("span fields = %+v", d)
	}
	wantHash := model.FormatID(xxhash.Sum64String(d.Text))
	if d.TextHash != wantHash {
		t.Errorf("text_hash = %s, want xxhash64(text) %s", d.TextHash, wantHash)
	}
	wantID := model.FormatID(xxhash.Sum64String(string(n.ID()) + d.TextHash + "v2"))
	if d.DocumentID != wantID {
		t.Errorf("document_id = %s, want xxhash64(node_id+text_hash+schema) %s", d.DocumentID, wantID)
	}
	if d.Truncated {
		t.Error("a small document must not read truncated")
	}
	// Wire shape: the JSON keys are the AC-4 names.
	raw, _ := json.Marshal(d)
	for _, key := range []string{`"document_id"`, `"node_id"`, `"language"`, `"kind"`, `"qualified_name"`, `"path"`, `"start_byte"`, `"end_byte"`, `"start_line"`, `"end_line"`, `"span_method"`, `"text_hash"`, `"document_schema"`, `"text"`, `"truncated"`} {
		if !bytes.Contains(raw, []byte(key)) {
			t.Errorf("wire form lacks %s: %s", key, raw)
		}
	}
}

// TestBuildDocuments_Golden pins AC-7 (a)–(c) against committed goldens under
// testdata/documents: the Go and TypeScript documents byte-for-byte, no body
// leaking into the next declaration (nested and same-line included), doc
// comments and decorators present, and byte-identical output across two runs.
func TestBuildDocuments_Golden(t *testing.T) {
	cases := []struct {
		name, path, src string
		golden          string
	}{
		{"go", "shop/cart.go", docGoFixture, "go.golden.json"},
		{"typescript", "pkg/a.ts", docTSFixture, "typescript.golden.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := parseFixture(t, tc.path, tc.src)
			docs, stats := embed.BuildDocuments(fileSource(res, tc.src))
			if stats.Documents != len(docs) || stats.SpanMethods["ast"] != len(docs) {
				t.Errorf("stats = %+v for %d documents", stats, len(docs))
			}
			first := marshalDocs(t, docs)
			again, _ := embed.BuildDocuments(fileSource(parseFixture(t, tc.path, tc.src), tc.src))
			if !bytes.Equal(first, marshalDocs(t, again)) {
				t.Error("two runs over identical source produced different document bytes")
			}
			goldenfile.Assert(t, filepath.Join("testdata", "documents", tc.golden), first)
		})
	}

	t.Run("go: no leak, nested included, docs present", func(t *testing.T) {
		res := parseFixture(t, "shop/cart.go", docGoFixture)
		docs, _ := embed.BuildDocuments(fileSource(res, docGoFixture))
		outer := docByQN(t, docs, "shop.outer")
		if !strings.Contains(outer.Text, "inner := func() int { return TaxRate }") {
			t.Errorf("outer lost its nested func literal: %q", outer.Text)
		}
		for _, leak := range []string{"func a()", "func b()", "last"} {
			if strings.Contains(outer.Text, leak) {
				t.Errorf("outer leaks %q", leak)
			}
		}
		a, b := docByQN(t, docs, "shop.a"), docByQN(t, docs, "shop.b")
		if strings.Contains(a.Text, "func b") || strings.Contains(b.Text, "func a") {
			t.Errorf("same-line declarations leak into each other: a=%q b=%q", a.Text, b.Text)
		}
		add := docByQN(t, docs, "shop.Cart.Add")
		if !strings.Contains(add.Text, "// Add appends an item.") {
			t.Errorf("doc comment missing from method document: %q", add.Text)
		}
		if !strings.HasPrefix(add.Text, "method shop.Cart.Add\nshop cart.go\n") {
			t.Errorf("text order broken: %q", add.Text)
		}
		for _, d := range docs {
			if d.Kind == "file" {
				t.Errorf("file node %s became a document", d.QualifiedName)
			}
		}
	})

	t.Run("typescript: decorators and doc comment present, no leak", func(t *testing.T) {
		res := parseFixture(t, "pkg/a.ts", docTSFixture)
		docs, _ := embed.BuildDocuments(fileSource(res, docTSFixture))
		f := docByQN(t, docs, "pkg.f")
		if !strings.Contains(f.Text, "/** doc for f */") || strings.Contains(f.Text, "Component") {
			t.Errorf("f document = %q", f.Text)
		}
		a := docByQN(t, docs, "pkg.A")
		if !strings.Contains(a.Text, "@Component({sel: 'x'})") || !strings.Contains(a.Text, "\nComponent\n") {
			t.Errorf("class document lacks its decorator (body) or annotation (meta): %q", a.Text)
		}
		if strings.Contains(a.Text, "function g") {
			t.Errorf("class document leaks the following function: %q", a.Text)
		}
		run := docByQN(t, docs, "pkg.run")
		if !strings.Contains(run.Text, "@Get()") {
			t.Errorf("method document lacks its decorator: %q", run.Text)
		}
	})
}

// TestBuildDocuments_WindowFallbackIsBounded pins AC-3/AC-7(d) at the document
// level: a parser without exact spans yields window documents that never
// exceed SpanWindowMaxLines, stop at the next declaration, and are labelled.
func TestBuildDocuments_WindowFallbackIsBounded(t *testing.T) {
	res := parseFixture(t, "pkg/mod.py", docPyFixture)
	if res.Spans != nil {
		t.Fatalf("precondition: python emits no spans")
	}
	docs, stats := embed.BuildDocuments(fileSource(res, docPyFixture))
	if len(docs) != 3 || stats.SpanMethods["window"] != 3 || stats.SpanMethods["ast"] != 0 {
		t.Fatalf("docs = %d, stats = %+v", len(docs), stats)
	}
	for _, d := range docs {
		if d.SpanMethod != "window" {
			t.Errorf("%s: span_method = %q, want window", d.QualifiedName, d.SpanMethod)
		}
		if lines := d.EndLine - d.StartLine + 1; lines > parse.SpanWindowMaxLines || lines < 1 {
			t.Errorf("%s: window covers %d lines (bound %d)", d.QualifiedName, lines, parse.SpanWindowMaxLines)
		}
		if strings.Count(d.Text, "\n") > parse.SpanWindowMaxLines+3 {
			t.Errorf("%s: document text exceeds the window bound: %d newlines", d.QualifiedName, strings.Count(d.Text, "\n"))
		}
	}
	short := docByQN(t, docs, "pkg.short")
	if short.StartLine != 1 || short.EndLine != 3 || strings.Contains(short.Text, "def long") {
		t.Errorf("short window = %d-%d %q (must stop before `long`)", short.StartLine, short.EndLine, short.Text)
	}
	long := docByQN(t, docs, "pkg.long")
	if long.StartLine != 4 || long.EndLine != 4+parse.SpanWindowMaxLines-1 || strings.Contains(long.Text, "def after") {
		t.Errorf("long window = %d-%d (want the 40-line bound, not the following def)", long.StartLine, long.EndLine)
	}
	share := stats.SpanMethodShare()
	if share["window"] != 1 || share["ast"] != 0 {
		t.Errorf("share = %v", share)
	}
}

// TestBuildDocuments_Exclusions pins AC-5: generated/vendored paths are
// excluded by the ONE shared classifier (engine/classify), as are file,
// package and external artefacts, and every exclusion is counted by reason.
func TestBuildDocuments_Exclusions(t *testing.T) {
	src := "package v\n\n// Gen is generated.\nfunc Gen() {}\n"
	for _, path := range []string{"vendor/x/y.go", "api/thing_pb.go", "gen/thing.gen.go", "internal/generated/z.go", "node_modules/a/b.ts"} {
		t.Run(path, func(t *testing.T) {
			n, _ := model.NewNode("function", "v.Gen", path, 4, 1)
			docs, stats := embed.BuildDocuments(embed.FileSource{
				Source: embed.Source{Language: "go", Bytes: []byte(src)}, Path: path, Nodes: []model.Node{n},
			})
			if len(docs) != 0 || stats.Excluded != 1 || stats.ExcludedByReason[embed.ExcludeGeneratedPath] != 1 {
				t.Errorf("docs = %d, stats = %+v", len(docs), stats)
			}
		})
	}
	t.Run("artefact kinds", func(t *testing.T) {
		file, _ := model.NewNode(parse.KindFile, "a/b.go", "a/b.go", 1, 1)
		pkg, _ := model.NewNode(parse.KindPackage, "com.x", "", 0, 0)
		ext, _ := model.NewNode(parse.KindExternal, "database/sql.DB.Query", "", 0, 0)
		fn, _ := model.NewNode("function", "b.Real", "a/b.go", 2, 1)
		docs, stats := embed.BuildDocuments(embed.FileSource{
			Source: embed.Source{Language: "go", Bytes: []byte("package b\nfunc Real() {}\n")},
			Path:   "a/b.go", Nodes: []model.Node{file, pkg, ext, fn},
		})
		if len(docs) != 1 || docs[0].QualifiedName != "b.Real" {
			t.Fatalf("docs = %+v", docs)
		}
		if stats.Excluded != 3 || stats.ExcludedByReason[embed.ExcludeFile] != 1 || stats.ExcludedByReason[embed.ExcludePackage] != 1 || stats.ExcludedByReason[embed.ExcludeExternal] != 1 {
			t.Errorf("stats = %+v", stats)
		}
		if stats.Documents != 1 {
			t.Errorf("stats.Documents = %d", stats.Documents)
		}
	})
	t.Run("stats merge", func(t *testing.T) {
		var total embed.DocumentStats
		total.Merge(embed.DocumentStats{Documents: 2, Excluded: 1, ExcludedByReason: map[string]int{"file": 1}, SpanMethods: map[string]int{"ast": 2}})
		total.Merge(embed.DocumentStats{Documents: 1, Truncated: 1, SpanMethods: map[string]int{"window": 1}})
		if total.Documents != 3 || total.Excluded != 1 || total.Truncated != 1 || total.SpanMethods["ast"] != 2 || total.SpanMethods["window"] != 1 || total.ExcludedByReason["file"] != 1 {
			t.Errorf("merged = %+v", total)
		}
		share := total.SpanMethodShare()
		if fmt.Sprintf("%.4f/%.4f", share["ast"], share["window"]) != "0.6667/0.3333" {
			t.Errorf("share = %v", share)
		}
		if got := (embed.DocumentStats{}).SpanMethodShare(); got["ast"] != 0 || got["window"] != 0 || len(got) != 2 {
			t.Errorf("empty share = %v (both keys must be present)", got)
		}
	})
}

type runeTokenizer struct{}

func (runeTokenizer) Truncate(text string, max int) (string, bool) {
	r := []rune(text)
	if len(r) <= max {
		return text, false
	}
	return string(r[:max]), true
}

// TestBuildDocument_Truncation pins AC-6: text is bounded to MaxDocumentTokens
// of the embedder's tokenizer when one is known, otherwise to MaxDocumentBytes
// alone (no whitespace-token approximation), and a large declaration stays ONE
// document marked truncated. SW-260 review round 1: the prior "whitespace
// tokens" fallback was an invented approximation; the byte-cap-only path
// records `bound: "bytes"` so the eval harness can see which bound carried
// the weight.
func TestBuildDocument_Truncation(t *testing.T) {
	if embed.MaxDocumentTokens != 512 {
		t.Fatalf("MaxDocumentTokens = %d, want 512", embed.MaxDocumentTokens)
	}
	big := "package big\n\nfunc Big() {\n" + strings.Repeat("\tcall(one, two, three, four)\n", 400) + "}\n"
	n, _ := model.NewNode("function", "big.Big", "big/big.go", 3, 1)
	span := parse.SourceSpan{StartByte: 13, EndByte: len(big) - 1, StartLine: 3, EndLine: 403, Method: parse.SpanMethodAST}

	t.Run("byte cap only when no tokenizer", func(t *testing.T) {
		// A declaration larger than MaxDocumentBytes with NO tokenizer: no token
		// bound runs, only the byte cap. The document is marked truncated
		// because the byte cap closed the gap; bound must read "bytes" (no
		// invented tokens). The source is intentionally >16 KiB so the byte
		// cap is the only bound that can fire.
		huge := "package big\n\nfunc Big() {\n" + strings.Repeat("\tcall(one, two, three, four)\n", 800) + "}\n"
		hn, _ := model.NewNode("function", "big.Big", "big/big.go", 3, 1)
		hs := parse.SourceSpan{StartByte: 13, EndByte: len(huge) - 1, StartLine: 3, EndLine: 803, Method: parse.SpanMethodAST}
		d := embed.BuildDocument(hn, hs, embed.Source{Language: "go", Bytes: []byte(huge)})
		if !d.Truncated || d.Bound != embed.BoundBytes {
			t.Fatalf("truncated=%v bound=%q, want truncated=true bound=%q", d.Truncated, d.Bound, embed.BoundBytes)
		}
		if len(d.Text) > embed.MaxDocumentBytes {
			t.Errorf("text = %d bytes, want <= %d", len(d.Text), embed.MaxDocumentBytes)
		}
		if !strings.HasPrefix(d.Text, "function big.Big\nbig big.go\nfunc Big() {") {
			t.Errorf("truncation cut the header: %q", d.Text[:min(len(d.Text), 60)])
		}
		if d.EndLine != 803 || d.EndByte != len(huge)-1 {
			t.Errorf("truncation must not rewrite the span: %+v", d)
		}
	})
	t.Run("embedder tokenizer wins when known", func(t *testing.T) {
		d := embed.BuildDocument(n, span, embed.Source{Language: "go", Bytes: []byte(big), Tokenizer: runeTokenizer{}})
		if !d.Truncated || len([]rune(d.Text)) != embed.MaxDocumentTokens {
			t.Errorf("rune-tokenized text = %d runes, truncated=%v", len([]rune(d.Text)), d.Truncated)
		}
		if d.Bound != embed.BoundTokens {
			t.Errorf("bound = %q, want %q (embedder tokenizer closed the gap)", d.Bound, embed.BoundTokens)
		}
	})
	t.Run("byte cap", func(t *testing.T) {
		// 300 whitespace tokens but each 100 bytes long: under the token bound,
		// over the byte cap — with a multi-byte rune straddling the cut.
		word := strings.Repeat("é", 50) // 100 bytes
		huge := "package h\n\nvar H = `" + strings.Repeat(word+" ", 300) + "`\n"
		hn, _ := model.NewNode("variable", "h.H", "h/h.go", 3, 1)
		hs := parse.SourceSpan{StartByte: 11, EndByte: len(huge) - 1, StartLine: 3, EndLine: 3, Method: parse.SpanMethodAST}
		d := embed.BuildDocument(hn, hs, embed.Source{Language: "go", Bytes: []byte(huge)})
		if !d.Truncated || len(d.Text) > embed.MaxDocumentBytes || !json.Valid([]byte(fmt.Sprintf("%q", d.Text))) {
			t.Errorf("byte-capped text: %d bytes, truncated=%v", len(d.Text), d.Truncated)
		}
		if !strings.ContainsRune(d.Text, 'é') || strings.ContainsRune(d.Text, '�') {
			t.Errorf("byte cap split a rune")
		}
		if d.Bound != embed.BoundBytes {
			t.Errorf("bound = %q, want %q (the byte cap closed the gap with no tokenizer set)", d.Bound, embed.BoundBytes)
		}
	})
	t.Run("still one document per node", func(t *testing.T) {
		// Use the >16 KiB fixture so the byte cap actually closes the gap,
		// pinning the "one document per node, truncated" contract end-to-end.
		huge := "package big\n\nfunc Big() {\n" + strings.Repeat("\tcall(one, two, three, four)\n", 800) + "}\n"
		hspan := parse.SourceSpan{StartByte: 13, EndByte: len(huge) - 1, StartLine: 3, EndLine: 803, Method: parse.SpanMethodAST}
		docs, stats := embed.BuildDocuments(embed.FileSource{
			Source: embed.Source{Language: "go", Bytes: []byte(huge)}, Path: "big/big.go",
			Nodes: []model.Node{n}, Spans: map[model.NodeId]parse.SourceSpan{n.ID(): hspan},
		})
		if len(docs) != 1 || !docs[0].Truncated || stats.Truncated != 1 {
			t.Errorf("docs = %d, stats = %+v", len(docs), stats)
		}
	})
	t.Run("small document reads bound=none", func(t *testing.T) {
		small := "package s\n\nfunc S() {}\n"
		sn, _ := model.NewNode("function", "s.S", "s/s.go", 3, 1)
		ss := parse.SourceSpan{StartByte: 12, EndByte: len(small) - 1, StartLine: 3, EndLine: 4, Method: parse.SpanMethodAST}
		dNoTok := embed.BuildDocument(sn, ss, embed.Source{Language: "go", Bytes: []byte(small)})
		if dNoTok.Truncated || dNoTok.Bound != embed.BoundNone {
			t.Errorf("no-tokenizer small doc: truncated=%v bound=%q, want false/%q", dNoTok.Truncated, dNoTok.Bound, embed.BoundNone)
		}
		dWithTok := embed.BuildDocument(sn, ss, embed.Source{Language: "go", Bytes: []byte(small), Tokenizer: runeTokenizer{}})
		if dWithTok.Truncated || dWithTok.Bound != embed.BoundNone {
			t.Errorf("tokenizer small doc: truncated=%v bound=%q, want false/%q", dWithTok.Truncated, dWithTok.Bound, embed.BoundNone)
		}
	})
}
