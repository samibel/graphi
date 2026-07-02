package parse

import (
	"context"
	"fmt"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/core/model"
)

// CSSParser is the SW-054 curated tier-1 CSS parser. It clones the SW-053 recipe over
// the pure-Go gotreesitter CSS grammar (CGo-free; default tier green under
// CGO_ENABLED=0; grammar blob Go-embedded behind subset tag grammar_subset_css). CSS is
// a config/markup language with no callables; the node set collapses to {file, type}
// where each top-level rule's selector becomes a `type` symbol. This exercises the AC#1
// "omit kinds the language lacks" path. CSSParser carries no mutable state and is safe
// for concurrent use.
type CSSParser struct {
	lang      *gts.Language
	extractor SymbolExtractor
}

// NewCSSParser returns a ready CSSParser wired to the pure-Go CSS grammar.
func NewCSSParser() *CSSParser {
	lang := grammars.CssLanguage()
	return &CSSParser{lang: lang, extractor: &cssSymbolExtractor{lang: lang}}
}

// Language implements Parser.
func (*CSSParser) Language() string { return "css" }

// Runtime implements Parser: pure-Go gotreesitter tree-sitter runtime (CGo-free).
func (*CSSParser) Runtime() Runtime { return RuntimeGoTreeSitter }

// Extensions implements Parser.
func (*CSSParser) Extensions() []string { return []string{".css"} }

type cssAST struct {
	root *gts.Node
	src  []byte
	lang *gts.Language
}

// Parse implements Parser.
func (p *CSSParser) Parse(ctx context.Context, filename string, src []byte) (res *ParseResult, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	defer func() {
		if r := recover(); r != nil {
			res = nil
			err = fmt.Errorf("parse: recovered from panic parsing %q: %v", filename, r)
		}
	}()

	tree, perr := parseTreeSitter(ctx, p.lang, src)
	if perr != nil {
		return nil, fmt.Errorf("parse: css error in %q: %w", filename, perr)
	}
	root := &cssAST{root: tree.RootNode(), src: src, lang: p.lang}

	extractor := p.extractor
	if extractor == nil {
		extractor = &cssSymbolExtractor{lang: p.lang}
	}
	nodes, edges, pending, xerr := extractor.Extract(filename, root)
	if xerr != nil {
		return nil, fmt.Errorf("parse: css extraction in %q: %w", filename, xerr)
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

// Kind mapping (CSS collapses onto {file, type}):
//
//	type ← a top-level rule_set's selector text (the styled anchor)
//
// Absent by design: function/method/variable/constant; no import system (Imports empty).

type cssSymbolExtractor struct{ lang *gts.Language }

// Language implements SymbolExtractor.
func (*cssSymbolExtractor) Language() string { return "css" }

// Extract implements SymbolExtractor for CSS.
func (e *cssSymbolExtractor) Extract(filename string, root any) ([]model.Node, []model.Edge, []PendingRef, error) {
	t, ok := root.(*cssAST)
	if !ok || t == nil || t.root == nil {
		return nil, nil, nil, fmt.Errorf("parse: css extractor: expected non-nil *cssAST root for %q, got %T", filename, root)
	}
	w := newCSTWalk(t.lang, t.src, langPackage(filename))
	// SW-055 AC#6: fail-closed parse-depth guard on untrusted input (skips the
	// file with structured, source-free provenance if nesting exceeds the bound).
	if derr := w.guardDepth(t.root, filename, "css"); derr != nil {
		return nil, nil, nil, derr
	}
	for i := 0; i < t.root.ChildCount(); i++ {
		c := t.root.Child(i)
		if c == nil || c.Type(w.lang) != "rule_set" {
			continue
		}
		sel := childByType(c, "selectors", w.lang)
		if sel == nil {
			continue
		}
		name := strings.TrimSpace(sel.Text(w.src))
		if name != "" {
			w.addDef(name, KindType, nodePoint(sel))
		}
	}
	return w.finishExtract(filename, "css")
}
