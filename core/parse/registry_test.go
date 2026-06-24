package parse

import (
	"context"
	"errors"
	"go/ast"
	"sync"
	"testing"
)

// AC: registry selects the correct parser by extension/language and Go files route
// to the native Go-AST precision path. Table-driven selection matrix incl.
// case-insensitivity, multi-extension, no-extension, foreign filenames.
func TestParserForSelectionMatrix(t *testing.T) {
	reg := NewDefaultRegistry()

	tests := []struct {
		name     string
		filename string
		wantLang string
		wantErr  error
	}{
		{"go lower", "main.go", "go", nil},
		{"go upper ext", "MAIN.GO", "go", nil},
		{"go mixed ext", "Foo.Go", "go", nil},
		{"go multi-dot", "pkg.test.go", "go", nil},
		{"go with path", "/a/b/c/server.go", "go", nil},
		{"json lower", "config.json", "json", nil},
		{"json upper", "CONFIG.JSON", "json", nil},
		{"unknown ext", "notes.txt", "", ErrNoParser},
		{"no extension", "Makefile", "", ErrNoParser},
		{"empty name", "", "", ErrNoParser},
		{"dotfile no ext", ".gitignore", "gitignore", ErrNoParser}, // filepath.Ext(".gitignore") == "" -> no parser
		{"foreign", "weird.πφ", "", ErrNoParser},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := reg.ParserFor(tt.filename)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ParserFor(%q) err = %v, want %v", tt.filename, err, tt.wantErr)
				}
				if p != nil {
					t.Fatalf("ParserFor(%q) returned non-nil parser on error", tt.filename)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParserFor(%q) unexpected err = %v", tt.filename, err)
			}
			if got := p.Language(); got != tt.wantLang {
				t.Fatalf("ParserFor(%q) lang = %q, want %q", tt.filename, got, tt.wantLang)
			}
		})
	}
}

// AC: Go files route to the native Go-AST path and return a populated ParseResult
// whose Root is the standard-library AST.
func TestGoASTRouting(t *testing.T) {
	reg := NewDefaultRegistry()
	src := []byte("package demo\n\nfunc Add(a, b int) int { return a + b }\n")

	res, err := reg.Parse(context.Background(), "demo.go", src)
	if err != nil {
		t.Fatalf("Parse(.go) err = %v", err)
	}
	if res.Meta.Language != "go" {
		t.Fatalf("language = %q, want go", res.Meta.Language)
	}
	if res.Meta.Path != "demo.go" || res.Meta.Size != len(src) || res.Meta.ContentHash == "" {
		t.Fatalf("incomplete SourceMeta: %+v", res.Meta)
	}
	gast, ok := res.Root.(*goAST)
	if !ok {
		t.Fatalf("Root type = %T, want *goAST (Go-AST precision path)", res.Root)
	}
	if gast.File == nil || gast.File.Name == nil || gast.File.Name.Name != "demo" {
		t.Fatalf("unexpected Go AST: %+v", gast.File)
	}
	// Prove it really is the stdlib go/ast: find the Add func decl.
	var foundAdd bool
	ast.Inspect(gast.File, func(n ast.Node) bool {
		if fd, ok := n.(*ast.FuncDecl); ok && fd.Name.Name == "Add" {
			foundAdd = true
		}
		return true
	})
	if !foundAdd {
		t.Fatal("did not find func Add in parsed Go AST")
	}
}

// AC: a Go syntax error is an ordinary error, not ErrNoParser (a parser WAS
// selected) and not a panic.
func TestGoSyntaxErrorIsNotErrNoParser(t *testing.T) {
	reg := NewDefaultRegistry()
	res, err := reg.Parse(context.Background(), "bad.go", []byte("package x\nfunc ("))
	if err == nil {
		t.Fatal("expected syntax error, got nil")
	}
	if errors.Is(err, ErrNoParser) {
		t.Fatal("syntax error must not be ErrNoParser")
	}
	if res != nil {
		t.Fatal("result must be nil on syntax error")
	}
}

// AC: unknown/unsupported type returns the typed ErrNoParser sentinel, with no
// panic, no partial state, and idempotent on repeat calls (miss path mutates
// nothing).
func TestUnknownTypeErrNoParserIdempotent(t *testing.T) {
	reg := NewDefaultRegistry()
	before := len(reg.Languages())

	for i := 0; i < 3; i++ {
		res, err := reg.Parse(context.Background(), "data.bin", []byte{0x00, 0x01})
		if !errors.Is(err, ErrNoParser) {
			t.Fatalf("call %d: err = %v, want ErrNoParser", i, err)
		}
		if res != nil {
			t.Fatalf("call %d: expected nil result on miss", i)
		}
	}
	if after := len(reg.Languages()); after != before {
		t.Fatalf("miss path mutated registry: langs %d -> %d", before, after)
	}
}

// syntheticParser is a brand-new parser defined entirely in the test. It proves
// open/closed: it registers against the same interface and becomes selectable
// WITHOUT touching GoParser, JSONParser, or the Registry.
type syntheticParser struct{}

func (*syntheticParser) Language() string     { return "synth" }
func (*syntheticParser) Extensions() []string { return []string{".synth", ".syn"} }
func (*syntheticParser) Runtime() Runtime     { return RuntimeStdlib }
func (*syntheticParser) Parse(ctx context.Context, filename string, src []byte) (*ParseResult, error) {
	return &ParseResult{
		Meta: SourceMeta{Path: filename, Language: "synth", ContentHash: contentHash(src), Size: len(src)},
		Root: string(src),
	}, nil
}

// AC: open/closed pluggability — a newly registered parser becomes selectable for
// its extensions without modifying existing parser code.
func TestOpenClosedPluggability(t *testing.T) {
	reg := NewDefaultRegistry()

	// Before registration: unknown.
	if _, err := reg.ParserFor("x.synth"); !errors.Is(err, ErrNoParser) {
		t.Fatalf("pre-register: want ErrNoParser, got %v", err)
	}

	reg.Register(&syntheticParser{})

	for _, fn := range []string{"x.synth", "y.SYN"} {
		p, err := reg.ParserFor(fn)
		if err != nil {
			t.Fatalf("post-register ParserFor(%q) err = %v", fn, err)
		}
		if p.Language() != "synth" {
			t.Fatalf("post-register ParserFor(%q) lang = %q, want synth", fn, p.Language())
		}
	}
	if _, err := reg.ParserForLang("synth"); err != nil {
		t.Fatalf("ParserForLang(synth) err = %v", err)
	}

	// Existing parsers untouched.
	if p, err := reg.ParserFor("main.go"); err != nil || p.Language() != "go" {
		t.Fatalf("existing go parser regressed: p=%v err=%v", p, err)
	}
}

// AC: registry lookup and parser invocation are concurrency-safe (run with -race).
func TestConcurrentRegisterAndLookup(t *testing.T) {
	reg := NewDefaultRegistry()
	var wg sync.WaitGroup

	// Concurrent registrations of distinct synthetic parsers.
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reg.Register(extParser{lang: "g" + string(rune('a'+i)), ext: ".g" + string(rune('a'+i))})
		}(i)
	}
	// Concurrent lookups + parses against the always-present go parser.
	src := []byte("package p\n")
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := reg.Parse(context.Background(), "f.go", src); err != nil {
				t.Errorf("concurrent parse err = %v", err)
			}
			_, _ = reg.ParserFor("nope.unknown")
			_ = reg.Languages()
		}()
	}
	wg.Wait()
}

type extParser struct {
	lang, ext string
}

func (e extParser) Language() string     { return e.lang }
func (e extParser) Extensions() []string { return []string{e.ext} }
func (e extParser) Runtime() Runtime     { return RuntimeStdlib }
func (e extParser) Parse(ctx context.Context, filename string, src []byte) (*ParseResult, error) {
	return &ParseResult{Meta: SourceMeta{Path: filename, Language: e.lang}}, nil
}

// AC: determinism — the same input parses to identical content hashes / results
// across repeated calls and across separate registry instances.
func TestDeterminism(t *testing.T) {
	src := []byte("package d\n\nvar X = 42\n")

	r1, err := NewDefaultRegistry().Parse(context.Background(), "d.go", src)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := NewDefaultRegistry().Parse(context.Background(), "d.go", src)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Meta.ContentHash != r2.Meta.ContentHash {
		t.Fatalf("non-deterministic hash: %s != %s", r1.Meta.ContentHash, r2.Meta.ContentHash)
	}
	if contentHash(src) != contentHash(src) {
		t.Fatal("contentHash not deterministic")
	}
	// Different input -> different hash.
	if contentHash(src) == contentHash([]byte("package d\n\nvar X = 43\n")) {
		t.Fatal("hash collision on distinct input")
	}
}

// AC: context cancellation is honored without panic.
func TestParseRespectsCanceledContext(t *testing.T) {
	reg := NewDefaultRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reg.Parse(ctx, "x.go", []byte("package x\n")); err == nil {
		t.Fatal("expected error from canceled context")
	}
}

// AC: nil / empty parser registration is a safe no-op (no panic, no partial state).
func TestRegisterNilAndEmptyNoop(t *testing.T) {
	reg := NewRegistry()
	reg.Register(nil)
	reg.Register(emptyParser{})
	if len(reg.Languages()) != 0 {
		t.Fatalf("nil/empty registration mutated registry: %v", reg.Languages())
	}
}

type emptyParser struct{}

func (emptyParser) Language() string     { return "" }
func (emptyParser) Extensions() []string { return nil }
func (emptyParser) Runtime() Runtime     { return RuntimeStdlib }
func (emptyParser) Parse(ctx context.Context, filename string, src []byte) (*ParseResult, error) {
	return nil, nil
}

// JSON structural parser sanity (proves the second distinct backend works).
func TestJSONParser(t *testing.T) {
	reg := NewDefaultRegistry()
	res, err := reg.Parse(context.Background(), "c.json", []byte(`{"a":[1,2,3]}`))
	if err != nil {
		t.Fatalf("json parse err = %v", err)
	}
	if res.Meta.Language != "json" {
		t.Fatalf("lang = %q, want json", res.Meta.Language)
	}
	m, ok := res.Root.(map[string]any)
	if !ok || m["a"] == nil {
		t.Fatalf("unexpected json root: %#v", res.Root)
	}
}

// Fuzz/robustness: malformed inputs must never panic; they return an error or a
// result, but the process stays alive.
func FuzzGoParserNoPanic(f *testing.F) {
	f.Add([]byte("package x\nfunc F(){}"))
	f.Add([]byte(""))
	f.Add([]byte("\x00\x00not go"))
	f.Add([]byte("package "))
	g := NewGoParser()
	f.Fuzz(func(t *testing.T, src []byte) {
		// Must not panic regardless of input.
		_, _ = g.Parse(context.Background(), "fuzz.go", src)
	})
}
