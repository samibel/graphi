package parse

import (
	"context"
	"strings"
	"testing"
)

// SW-222 (AX-02) characterization baseline for the PARSER registry.
//
// These tests were written and made green BEFORE the registry-lifecycle
// refactor, and they must pass UNCHANGED after it. They pin the three things
// AC-4 names — registration order, default contents, and override behaviour —
// for `core/parse.Registry`, whose collision policy is LAST-WINS by design
// (registry.go: "a later registration for the same extension/language overrides
// the earlier one"). ADR 0013 threat T5 records that policy as a deliberate
// divergence from `engine/analysis`, so this file exists to prove the
// divergence SURVIVES the unification of the vocabulary rather than being
// quietly harmonised away.

// charParser is a minimal Parser used only to observe registry selection.
type charParser struct {
	lang string
	exts []string
	tag  string
}

func (p charParser) Language() string     { return p.lang }
func (p charParser) Extensions() []string { return p.exts }
func (p charParser) Runtime() Runtime     { return RuntimeStdlib }
func (p charParser) Parse(ctx context.Context, filename string, src []byte) (*ParseResult, error) {
	return &ParseResult{Meta: SourceMeta{Path: filename, Language: p.tag}}, nil
}

// TestBaseline_ParseRegistry_DefaultLanguages pins the exact default parser set
// of the CGo-free default build. A language added or removed here is a product
// change, never a refactor side effect.
func TestBaseline_ParseRegistry_DefaultLanguages(t *testing.T) {
	got := NewDefaultRegistry().Languages()
	want := []string{
		"bash", "c", "c_sharp", "cpp", "css", "go", "hcl", "java", "javascript",
		"json", "kotlin", "lua", "markdown", "php", "python", "ruby", "rust",
		"sql", "toml", "tsx", "typescript", "yaml",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("default parser languages drifted:\n got: %v\nwant: %v", got, want)
	}
}

// TestBaseline_ParseRegistry_DefaultExtensionSelection pins that the default
// registry resolves a representative extension per family to the language whose
// parser owns it — the selection contract ingest depends on.
func TestBaseline_ParseRegistry_DefaultExtensionSelection(t *testing.T) {
	r := NewDefaultRegistry()
	for _, tc := range []struct{ file, lang string }{
		{"a.go", "go"},
		{"a.py", "python"},
		{"a.ts", "typescript"},
		{"a.tsx", "tsx"},
		{"a.js", "javascript"},
		{"a.rs", "rust"},
		{"a.java", "java"},
		{"a.kt", "kotlin"},
		{"a.cs", "c_sharp"},
		{"a.rb", "ruby"},
		{"a.json", "json"},
	} {
		p, err := r.ParserFor(tc.file)
		if err != nil {
			t.Fatalf("ParserFor(%q): %v", tc.file, err)
		}
		if p.Language() != tc.lang {
			t.Fatalf("ParserFor(%q) = %q, want %q", tc.file, p.Language(), tc.lang)
		}
	}
	if _, err := r.ParserFor("a.unknown-ext"); err != ErrNoParser {
		t.Fatalf("ParserFor(unknown) = %v, want ErrNoParser", err)
	}
}

// TestBaseline_ParseRegistry_LastWinsOverride is the T5 divergence, pinned. The
// parse registry is LAST-WINS on purpose so an opt-in backend can supersede a
// stdlib default; this must remain true after AX-02.
func TestBaseline_ParseRegistry_LastWinsOverride(t *testing.T) {
	r := NewRegistry()
	r.Register(charParser{lang: "toy", exts: []string{".toy"}, tag: "first"})
	r.Register(charParser{lang: "toy", exts: []string{".toy"}, tag: "second"})

	byExt, err := r.ParserFor("x.toy")
	if err != nil {
		t.Fatalf("ParserFor: %v", err)
	}
	res, err := byExt.Parse(context.Background(), "x.toy", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if res.Meta.Language != "second" {
		t.Fatalf("by-extension override = %q, want the LAST registration %q", res.Meta.Language, "second")
	}

	byLang, err := r.ParserForLang("toy")
	if err != nil {
		t.Fatalf("ParserForLang: %v", err)
	}
	res, err = byLang.Parse(context.Background(), "x.toy", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if res.Meta.Language != "second" {
		t.Fatalf("by-language override = %q, want the LAST registration %q", res.Meta.Language, "second")
	}
	// An override does not add a second language entry.
	if langs := r.Languages(); len(langs) != 1 || langs[0] != "toy" {
		t.Fatalf("Languages() after override = %v, want [toy]", langs)
	}
}

// TestBaseline_ParseRegistry_NilAndEmptyAreNoOps pins the two documented no-op
// paths: registering nil, and registering a parser with neither language nor
// extension. Neither may panic and neither may mutate the registry.
func TestBaseline_ParseRegistry_NilAndEmptyAreNoOps(t *testing.T) {
	r := NewRegistry()
	r.Register(nil)
	r.Register(charParser{})
	if langs := r.Languages(); len(langs) != 0 {
		t.Fatalf("Languages() after no-op registrations = %v, want empty", langs)
	}
}

// TestBaseline_ParseRegistry_ExtensionNormalisation pins the case-insensitive,
// dot-normalising key derivation both Register and ParserFor apply.
func TestBaseline_ParseRegistry_ExtensionNormalisation(t *testing.T) {
	r := NewRegistry()
	r.Register(charParser{lang: "TOY", exts: []string{"TOY", ".Bar"}, tag: "t"})
	for _, f := range []string{"x.toy", "x.TOY", "x.bar", "x.BAR"} {
		if _, err := r.ParserFor(f); err != nil {
			t.Fatalf("ParserFor(%q): %v", f, err)
		}
	}
	if _, err := r.ParserForLang("toy"); err != nil {
		t.Fatalf("ParserForLang(toy): %v", err)
	}
}
