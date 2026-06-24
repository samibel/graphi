package parse

import (
	"context"
	"fmt"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/core/model"
)

// TOMLParser is the SW-054 curated tier-1 TOML parser. It clones the SW-053 recipe over
// the pure-Go gotreesitter TOML grammar (CGo-free; default tier green under
// CGO_ENABLED=0; grammar blob Go-embedded behind subset tag grammar_subset_toml). TOML
// is a config language with no callables; the node set collapses to {file, type,
// variable} where each table header is a `type` and each top-level key/value pair is a
// `variable`. This exercises the AC#1 "omit kinds the language lacks" path. TOMLParser
// carries no mutable state and is safe for concurrent use.
type TOMLParser struct {
	lang      *gts.Language
	extractor SymbolExtractor
}

// NewTOMLParser returns a ready TOMLParser wired to the pure-Go TOML grammar.
func NewTOMLParser() *TOMLParser {
	lang := grammars.TomlLanguage()
	return &TOMLParser{lang: lang, extractor: &tomlSymbolExtractor{lang: lang}}
}

// Language implements Parser.
func (*TOMLParser) Language() string { return "toml" }

// Runtime implements Parser: pure-Go gotreesitter tree-sitter runtime (CGo-free).
func (*TOMLParser) Runtime() Runtime { return RuntimeGoTreeSitter }

// Extensions implements Parser.
func (*TOMLParser) Extensions() []string { return []string{".toml"} }

type tomlAST struct {
	root *gts.Node
	src  []byte
	lang *gts.Language
}

// Parse implements Parser.
func (p *TOMLParser) Parse(ctx context.Context, filename string, src []byte) (res *ParseResult, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	defer func() {
		if r := recover(); r != nil {
			res = nil
			err = fmt.Errorf("parse: recovered from panic parsing %q: %v", filename, r)
		}
	}()

	parser := gts.NewParser(p.lang)
	tree, perr := parser.Parse(src)
	if perr != nil {
		return nil, fmt.Errorf("parse: toml error in %q: %w", filename, perr)
	}
	root := &tomlAST{root: tree.RootNode(), src: src, lang: p.lang}

	extractor := p.extractor
	if extractor == nil {
		extractor = &tomlSymbolExtractor{lang: p.lang}
	}
	nodes, edges, pending, xerr := extractor.Extract(filename, root)
	if xerr != nil {
		return nil, fmt.Errorf("parse: toml extraction in %q: %w", filename, xerr)
	}

	return &ParseResult{
		Meta: SourceMeta{
			Path: filename, Language: p.Language(),
			ContentHash: contentHash(src), Size: len(src),
		},
		Root:        root,
		Nodes:       nodes,
		Edges:       edges,
		PendingRefs: pending,
	}, nil
}

// Kind mapping (TOML collapses onto {file, type, variable}):
//
//	type     ← a [table] header (the named section)
//	variable ← a top-level (pre-table) key/value pair's bare_key
//
// Absent by design: function/method/constant; no import system (Imports empty).

type tomlSymbolExtractor struct{ lang *gts.Language }

// Language implements SymbolExtractor.
func (*tomlSymbolExtractor) Language() string { return "toml" }

// Extract implements SymbolExtractor for TOML.
func (e *tomlSymbolExtractor) Extract(filename string, root any) ([]model.Node, []model.Edge, []PendingRef, error) {
	t, ok := root.(*tomlAST)
	if !ok || t == nil || t.root == nil {
		return nil, nil, nil, fmt.Errorf("parse: toml extractor: expected non-nil *tomlAST root for %q, got %T", filename, root)
	}
	w := newCSTWalk(t.lang, t.src, langPackage(filename))
	// SW-055 AC#6: fail-closed parse-depth guard on untrusted input (skips the
	// file with structured, source-free provenance if nesting exceeds the bound).
	if derr := w.guardDepth(t.root, filename, "toml"); derr != nil {
		return nil, nil, nil, derr
	}
	for i := 0; i < t.root.ChildCount(); i++ {
		c := t.root.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "pair":
			if key := childByType(c, "bare_key", w.lang); key != nil {
				w.addDef(key.Text(w.src), KindVariable, nodePoint(key))
			}
		case "table":
			if key := childByType(c, "bare_key", w.lang); key != nil {
				w.addDef(key.Text(w.src), KindType, nodePoint(key))
			} else if dk := childByType(c, "dotted_key", w.lang); dk != nil {
				w.addDef(dk.Text(w.src), KindType, nodePoint(dk))
			}
		}
	}
	return w.finishExtract(filename, "toml")
}
